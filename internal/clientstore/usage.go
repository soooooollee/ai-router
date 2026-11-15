package clientstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *BoltStore) ReserveUsage(_ context.Context, scope Scope, reservation UsageReservation, policy ClientPolicy) error {
	scope = scope.Normalize()
	if reservation.RequestID == "" || reservation.ClientID == "" || reservation.InputTokens < 0 || reservation.OutputTokens < 0 {
		return errors.New("valid usage reservation is required")
	}
	if reservation.CreatedAt.IsZero() {
		reservation.CreatedAt = time.Now().UTC()
	}
	reservation.TenantID, reservation.ProjectID = scope.TenantID, scope.ProjectID
	if reservation.Day.IsZero() {
		reservation.Day = reservation.CreatedAt
	}
	reservation.Day = startOfDay(reservation.Day)
	if reservation.Minute.IsZero() {
		reservation.Minute = reservation.CreatedAt
	}
	reservation.Minute = startOfMinute(reservation.Minute)
	return s.db.Update(func(tx *bolt.Tx) error {
		reservations := tx.Bucket(bucketUsageReservations)
		reservationKey := reservationKey(scope, reservation.RequestID)
		if reservations.Get(reservationKey) != nil {
			return nil
		}
		key := usageKey(scope, reservation.ClientID, reservation.Day)
		bucket := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: reservation.ClientID, WindowStart: reservation.Day, WindowKind: "day"}
		if raw := tx.Bucket(bucketUsageDaily).Get(key); raw != nil {
			if err := decode(raw, &bucket); err != nil {
				return err
			}
		}
		if policy.DailyRequestLimit > 0 && bucket.Requests+1 > policy.DailyRequestLimit {
			return ErrQuotaExhausted
		}
		if policy.DailyInputTokens > 0 && bucket.InputTokens+bucket.ReservedInputTokens+reservation.InputTokens > policy.DailyInputTokens {
			return ErrQuotaExhausted
		}
		if policy.DailyOutputTokens > 0 && bucket.OutputTokens+bucket.ReservedOutputTokens+reservation.OutputTokens > policy.DailyOutputTokens {
			return ErrQuotaExhausted
		}
		bucket.Requests++
		bucket.ReservedInputTokens += reservation.InputTokens
		bucket.ReservedOutputTokens += reservation.OutputTokens
		if err := put(tx.Bucket(bucketUsageDaily), key, bucket); err != nil {
			return err
		}
		minuteKey := minuteUsageKey(scope, reservation.ClientID, reservation.Minute)
		minute := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: reservation.ClientID, WindowStart: reservation.Minute, WindowKind: "minute"}
		if raw := tx.Bucket(bucketUsageMinute).Get(minuteKey); raw != nil {
			if err := decode(raw, &minute); err != nil {
				return err
			}
		}
		minute.Requests++
		minute.ReservedInputTokens += reservation.InputTokens
		minute.ReservedOutputTokens += reservation.OutputTokens
		if err := put(tx.Bucket(bucketUsageMinute), minuteKey, minute); err != nil {
			return err
		}
		return put(reservations, reservationKey, reservation)
	})
}

func (s *BoltStore) SettleUsage(_ context.Context, scope Scope, delta UsageDelta) error {
	scope = scope.Normalize()
	if delta.RequestID == "" || delta.InputTokens < 0 || delta.OutputTokens < 0 {
		return errors.New("valid usage settlement is required")
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		reservations := tx.Bucket(bucketUsageReservations)
		reservationKey := reservationKey(scope, delta.RequestID)
		var reservation UsageReservation
		if err := decode(reservations.Get(reservationKey), &reservation); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		key := usageKey(scope, reservation.ClientID, reservation.Day)
		var bucket UsageBucket
		if err := decode(tx.Bucket(bucketUsageDaily).Get(key), &bucket); err != nil {
			return err
		}
		bucket.ReservedInputTokens = max64(0, bucket.ReservedInputTokens-reservation.InputTokens)
		bucket.ReservedOutputTokens = max64(0, bucket.ReservedOutputTokens-reservation.OutputTokens)
		bucket.InputTokens += delta.InputTokens
		bucket.OutputTokens += delta.OutputTokens
		if delta.Error {
			bucket.Errors++
		}
		if delta.Rejected {
			bucket.Rejected++
		}
		if delta.Estimated {
			bucket.Estimated++
		}
		if err := put(tx.Bucket(bucketUsageDaily), key, bucket); err != nil {
			return err
		}
		minuteKey := minuteUsageKey(scope, reservation.ClientID, reservation.Minute)
		var minute UsageBucket
		if err := decode(tx.Bucket(bucketUsageMinute).Get(minuteKey), &minute); err != nil {
			return err
		}
		minute.ReservedInputTokens = max64(0, minute.ReservedInputTokens-reservation.InputTokens)
		minute.ReservedOutputTokens = max64(0, minute.ReservedOutputTokens-reservation.OutputTokens)
		minute.InputTokens += delta.InputTokens
		minute.OutputTokens += delta.OutputTokens
		if delta.Error {
			minute.Errors++
		}
		if delta.Rejected {
			minute.Rejected++
		}
		if delta.Estimated {
			minute.Estimated++
		}
		if err := put(tx.Bucket(bucketUsageMinute), minuteKey, minute); err != nil {
			return err
		}
		return reservations.Delete(reservationKey)
	})
	if err != nil {
		return err
	}
	s.maybePruneUsage()
	return nil
}

func (s *BoltStore) AddRejectedUsage(_ context.Context, scope Scope, clientID string, at time.Time) error {
	scope = scope.Normalize()
	day := startOfDay(at)
	minuteStart := startOfMinute(at)
	err := s.db.Update(func(tx *bolt.Tx) error {
		key := usageKey(scope, clientID, day)
		bucket := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: clientID, WindowStart: day, WindowKind: "day"}
		if raw := tx.Bucket(bucketUsageDaily).Get(key); raw != nil {
			if err := decode(raw, &bucket); err != nil {
				return err
			}
		}
		bucket.Rejected++
		if err := put(tx.Bucket(bucketUsageDaily), key, bucket); err != nil {
			return err
		}
		minuteKey := minuteUsageKey(scope, clientID, minuteStart)
		minute := UsageBucket{TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: clientID, WindowStart: minuteStart, WindowKind: "minute"}
		if raw := tx.Bucket(bucketUsageMinute).Get(minuteKey); raw != nil {
			if err := decode(raw, &minute); err != nil {
				return err
			}
		}
		minute.Rejected++
		return put(tx.Bucket(bucketUsageMinute), minuteKey, minute)
	})
	if err != nil {
		return err
	}
	s.maybePruneUsage()
	return nil
}

// PruneUsage deletes minute usage buckets that start before the cutoff and
// reports how many were removed. Without it every minute of every client stays
// in the database forever.
func (s *BoltStore) PruneUsage(_ context.Context, before time.Time) (int, error) {
	cutoff := startOfMinute(before)
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUsageMinute)
		cursor := bucket.Cursor()
		var stale [][]byte
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var usage UsageBucket
			if decodeErr := decode(raw, &usage); decodeErr != nil {
				return decodeErr
			}
			if usage.WindowStart.Before(cutoff) {
				stale = append(stale, append([]byte(nil), key...))
			}
		}
		for _, key := range stale {
			if deleteErr := bucket.Delete(key); deleteErr != nil {
				return deleteErr
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// maybePruneUsage keeps retention off the request path by running at most once
// per interval; the first caller after the window wins the race.
func (s *BoltStore) maybePruneUsage() {
	now := time.Now()
	previous := s.lastPrune.Load()
	if previous != 0 && now.Sub(time.Unix(0, previous)) < usagePruneInterval {
		return
	}
	if !s.lastPrune.CompareAndSwap(previous, now.UnixNano()) {
		return
	}
	_, _ = s.PruneUsage(context.Background(), now.Add(-usageMinuteRetention))
}

func (s *BoltStore) GetUsage(_ context.Context, scope Scope, clientID string, query UsageQuery) (UsageSummary, error) {
	scope = scope.Normalize()
	if query.From.IsZero() {
		query.From = time.Unix(0, 0)
	}
	if query.To.IsZero() {
		query.To = time.Now().UTC()
	}
	from, to := startOfDay(query.From), startOfDay(query.To)
	fromMinute, toMinute := startOfMinute(query.From), startOfMinute(query.To)
	summary := UsageSummary{ClientID: clientID, Daily: []UsageBucket{}}
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketUsageDaily).Cursor()
		prefix := scopedKey(scope, clientID+"\x00")
		for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
			var bucket UsageBucket
			if err := decode(raw, &bucket); err != nil {
				return err
			}
			if bucket.WindowStart.Before(from) || bucket.WindowStart.After(to) {
				continue
			}
			summary.Daily = append(summary.Daily, bucket)
			summary.Total.Requests += bucket.Requests
			summary.Total.Errors += bucket.Errors
			summary.Total.Rejected += bucket.Rejected
			summary.Total.InputTokens += bucket.InputTokens
			summary.Total.OutputTokens += bucket.OutputTokens
			summary.Total.ReservedInputTokens += bucket.ReservedInputTokens
			summary.Total.ReservedOutputTokens += bucket.ReservedOutputTokens
			summary.Total.Estimated += bucket.Estimated
		}
		return nil
	})
	if err == nil {
		err = s.db.View(func(tx *bolt.Tx) error {
			cursor := tx.Bucket(bucketUsageMinute).Cursor()
			prefix := scopedKey(scope, clientID+"\x00")
			for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
				var bucket UsageBucket
				if decodeErr := decode(raw, &bucket); decodeErr != nil {
					return decodeErr
				}
				if bucket.WindowStart.Before(fromMinute) || bucket.WindowStart.After(toMinute) {
					continue
				}
				summary.Minute = append(summary.Minute, bucket)
			}
			return nil
		})
	}
	sort.Slice(summary.Daily, func(i, j int) bool { return summary.Daily[i].WindowStart.Before(summary.Daily[j].WindowStart) })
	sort.Slice(summary.Minute, func(i, j int) bool { return summary.Minute[i].WindowStart.Before(summary.Minute[j].WindowStart) })
	summary.Total.TenantID, summary.Total.ProjectID, summary.Total.ClientID = scope.TenantID, scope.ProjectID, clientID
	summary.Total.WindowKind = "total"
	return summary, err
}

func sanitizeAuditMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "hmac") || strings.Contains(lower, "api_key") || strings.Contains(lower, "access_token") || strings.Contains(lower, "authorization") {
			continue
		}
		lowerValue := strings.ToLower(value)
		if !strings.Contains(lower, "prefix") && (strings.Contains(lowerValue, "air_sk_live_") || strings.Contains(lowerValue, "air_sk_test_") || strings.HasPrefix(lowerValue, "sk-") || strings.HasPrefix(lowerValue, "bearer ")) {
			continue
		}
		if len(value) > 1024 {
			value = value[:1024]
		}
		out[key] = value
	}
	return out
}

func (s *BoltStore) AppendAudit(_ context.Context, scope Scope, event AuditEvent) error {
	scope = scope.Normalize()
	if event.ID == "" || event.Action == "" || event.ResourceType == "" || event.ResourceID == "" {
		return errors.New("audit id, action, resource type and resource id are required")
	}
	event.TenantID, event.ProjectID = scope.TenantID, scope.ProjectID
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.Metadata = sanitizeAuditMetadata(event.Metadata)
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, event.CreatedAt.UTC().Format("20060102T150405.000000000")+"\x00"+event.ID)
		return put(tx.Bucket(bucketAuditEvents), key, event)
	})
}

func (s *BoltStore) ListAudit(_ context.Context, scope Scope, filter AuditFilter) ([]AuditEvent, error) {
	scope = scope.Normalize()
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := make([]AuditEvent, 0, limit)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketAuditEvents).Cursor()
		prefix := scopedPrefix(scope)
		for key, raw := cursor.Last(); key != nil && len(out) < limit; key, raw = cursor.Prev() {
			if !strings.HasPrefix(string(key), string(prefix)) {
				continue
			}
			var event AuditEvent
			if err := decode(raw, &event); err != nil {
				return err
			}
			if filter.ClientID != "" && event.ResourceID != filter.ClientID && event.Metadata["client_id"] != filter.ClientID {
				continue
			}
			out = append(out, event)
		}
		return nil
	})
	return out, err
}

func (s *BoltStore) Backup(ctx context.Context, destination string, hmacKeyIDs []string, masterKeyPath string) (BackupManifest, error) {
	if err := ctx.Err(); err != nil {
		return BackupManifest{}, err
	}
	if strings.TrimSpace(destination) == "" {
		return BackupManifest{}, errors.New("backup destination is required")
	}
	requiredHMACKeyIDs, err := s.RequiredHMACKeyIDs(ctx)
	if err != nil {
		return BackupManifest{}, err
	}
	available := map[string]bool{}
	for _, id := range hmacKeyIDs {
		available[id] = true
	}
	for _, id := range requiredHMACKeyIDs {
		if !available[id] {
			return BackupManifest{}, fmt.Errorf("credential master key %s is unavailable; backup refused", id)
		}
	}
	if filepath.Ext(destination) == "" {
		destination += ".db"
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return BackupManifest{}, err
	}
	temporary := destination + ".tmp"
	_ = os.Remove(temporary)
	if err := s.db.View(func(tx *bolt.Tx) error { return tx.CopyFile(temporary, 0600) }); err != nil {
		return BackupManifest{}, err
	}
	checksum, err := fileSHA256(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return BackupManifest{}, err
	}
	if err = os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return BackupManifest{}, err
	}
	manifest := BackupManifest{Version: 1, CreatedAt: time.Now().UTC(), Database: filepath.Base(destination), SHA256: checksum, HMACKeyIDs: requiredHMACKeyIDs}
	if strings.TrimSpace(masterKeyPath) == "" {
		manifest.ExternalMasterKey = true
	} else {
		masterRaw, readErr := os.ReadFile(masterKeyPath)
		if readErr != nil {
			return BackupManifest{}, fmt.Errorf("read credential master key for backup: %w", readErr)
		}
		masterDestination := destination + ".master.key"
		if writeErr := writePrivateFile(masterDestination, masterRaw); writeErr != nil {
			return BackupManifest{}, writeErr
		}
		masterChecksum, checksumErr := fileSHA256(masterDestination)
		if checksumErr != nil {
			return BackupManifest{}, checksumErr
		}
		manifest.MasterKeyFile = filepath.Base(masterDestination)
		manifest.MasterKeySHA256 = masterChecksum
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupManifest{}, err
	}
	manifestTemporary := destination + ".manifest.json.tmp"
	if err = os.WriteFile(manifestTemporary, append(raw, '\n'), 0600); err != nil {
		return BackupManifest{}, err
	}
	if err = os.Rename(manifestTemporary, destination+".manifest.json"); err != nil {
		_ = os.Remove(manifestTemporary)
		return BackupManifest{}, err
	}
	return manifest, nil
}

func writePrivateFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
