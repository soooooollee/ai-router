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
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Version 2 adds recoverable managed credentials. Version 5 removes every
// credential using the retired air_sk_* format. Version 6 removes the briefly
// introduced automatic Default credential; access keys are user-created.
// Older binaries must refuse to open the database because rewriting a
// credential could otherwise discard newer state fields.
const schemaVersion = 6

var (
	bucketMeta               = []byte("meta")
	bucketClients            = []byte("clients")
	bucketCredentials        = []byte("credentials")
	bucketCredentialByPrefix = []byte("credential_by_prefix")
	bucketCredentialByHMAC   = []byte("credential_by_hmac")
	bucketPolicies           = []byte("policies")
	bucketUsageDaily         = []byte("usage_daily")
	bucketUsageMinute        = []byte("usage_minute")
	bucketUsageReservations  = []byte("usage_reservations")
	bucketAuditEvents        = []byte("audit_events")
)

type BoltStore struct {
	db   *bolt.DB
	path string
}

func Open(path string) (*BoltStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("client state database path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create client state directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("secure client state directory: %w", err)
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open client state database: %w", err)
	}
	store := &BoltStore{db: db, path: path}
	if err = store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure client state database: %w", err)
	}
	return store, nil
}

func (s *BoltStore) migrate() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{
			bucketMeta, bucketClients, bucketCredentials, bucketCredentialByPrefix, bucketCredentialByHMAC,
			bucketPolicies, bucketUsageDaily, bucketUsageMinute,
			bucketUsageReservations, bucketAuditEvents,
		} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create state bucket %s: %w", name, err)
			}
		}
		meta := tx.Bucket(bucketMeta)
		version := 0
		if raw := meta.Get([]byte("schema_version")); len(raw) > 0 {
			version, _ = strconv.Atoi(string(raw))
		}
		if version > schemaVersion {
			return fmt.Errorf("client state schema %d is newer than supported schema %d", version, schemaVersion)
		}
		credentials := tx.Bucket(bucketCredentials)
		byPrefix := tx.Bucket(bucketCredentialByPrefix)
		byHMAC := tx.Bucket(bucketCredentialByHMAC)
		type credentialDelete struct {
			key         []byte
			prefixIndex []byte
			hmacIndex   []byte
		}
		deletes := make([]credentialDelete, 0)
		if err := credentials.ForEach(func(key, raw []byte) error {
			var credential Credential
			if err := decode(raw, &credential); err != nil {
				return err
			}
			keyString := string(key)
			separator := strings.LastIndex(keyString, "\x00")
			if separator < 0 {
				return errors.New("credential state key has no scope")
			}
			scopePrefix := keyString[:separator+1]
			removeRetiredFormat := version < 5 && strings.HasPrefix(credential.Prefix, "air_sk_")
			removeAutomaticDefault := version < 6 && credential.ClientID == "client_default"
			if removeRetiredFormat || removeAutomaticDefault {
				entry := credentialDelete{key: append([]byte(nil), key...)}
				if credential.Prefix != "" {
					entry.prefixIndex = []byte(scopePrefix + credential.Prefix)
				}
				if credential.HMACKeyID != "" && len(credential.SecretHMAC) > 0 {
					entry.hmacIndex = []byte(scopePrefix + credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC))
				}
				deletes = append(deletes, entry)
				return nil
			}
			if credential.Prefix != "" {
				if err := byPrefix.Put([]byte(scopePrefix+credential.Prefix), []byte(credential.ID)); err != nil {
					return err
				}
			}
			if credential.HMACKeyID != "" && len(credential.SecretHMAC) > 0 {
				index := credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
				if err := byHMAC.Put([]byte(scopePrefix+index), []byte(credential.ID)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		for _, entry := range deletes {
			if err := credentials.Delete(entry.key); err != nil {
				return err
			}
			if len(entry.prefixIndex) > 0 {
				if err := byPrefix.Delete(entry.prefixIndex); err != nil {
					return err
				}
			}
			if len(entry.hmacIndex) > 0 {
				if err := byHMAC.Delete(entry.hmacIndex); err != nil {
					return err
				}
			}
		}
		if version < 6 {
			if err := tx.Bucket(bucketClients).Delete(scopedKey(DefaultScope, "client_default")); err != nil {
				return err
			}
			if err := tx.Bucket(bucketPolicies).Delete(scopedKey(DefaultScope, "policy_default")); err != nil {
				return err
			}
		}
		return meta.Put([]byte("schema_version"), []byte(strconv.Itoa(schemaVersion)))
	})
}

func (s *BoltStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func scopedKey(scope Scope, id string) []byte {
	scope = scope.Normalize()
	return []byte(scope.TenantID + "\x00" + scope.ProjectID + "\x00" + id)
}

func scopedPrefix(scope Scope) []byte {
	scope = scope.Normalize()
	return []byte(scope.TenantID + "\x00" + scope.ProjectID + "\x00")
}

func encode(value any) ([]byte, error) { return json.Marshal(value) }

func decode(raw []byte, value any) error {
	if len(raw) == 0 {
		return ErrNotFound
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("decode client state: %w", err)
	}
	return nil
}

func put(bucket *bolt.Bucket, key []byte, value any) error {
	raw, err := encode(value)
	if err != nil {
		return err
	}
	return bucket.Put(key, raw)
}

func clientInScope(client Client, scope Scope) bool {
	scope = scope.Normalize()
	return client.TenantID == scope.TenantID && client.ProjectID == scope.ProjectID
}

func (s *BoltStore) CreateClient(_ context.Context, scope Scope, client Client, policy ClientPolicy) error {
	scope = scope.Normalize()
	now := time.Now().UTC()
	client.ID = strings.TrimSpace(client.ID)
	client.Name = strings.TrimSpace(client.Name)
	client.PolicyID = strings.TrimSpace(client.PolicyID)
	if client.ID == "" || client.Name == "" || client.PolicyID == "" {
		return errors.New("client id, name and policy id are required")
	}
	if client.Status == "" {
		client.Status = ClientActive
	}
	if client.Status != ClientActive && client.Status != ClientDisabled {
		return errors.New("new client status must be active or disabled")
	}
	client.TenantID, client.ProjectID = scope.TenantID, scope.ProjectID
	if client.CreatedAt.IsZero() {
		client.CreatedAt = now
	}
	client.UpdatedAt = now
	policy.ID = client.PolicyID
	policy.ProjectID = scope.ProjectID
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, client.ID)
		if tx.Bucket(bucketClients).Get(key) != nil {
			return ErrAlreadyExists
		}
		if tx.Bucket(bucketPolicies).Get(scopedKey(scope, policy.ID)) != nil {
			return ErrAlreadyExists
		}
		if err := put(tx.Bucket(bucketClients), key, client); err != nil {
			return err
		}
		return put(tx.Bucket(bucketPolicies), scopedKey(scope, policy.ID), policy)
	})
}

func validClientTransition(from, to ClientStatus) bool {
	if from == to {
		return true
	}
	if from == ClientDeleted {
		return false
	}
	switch to {
	case ClientActive, ClientDisabled, ClientDeleted:
		return true
	default:
		return false
	}
}

func (s *BoltStore) UpdateClient(_ context.Context, scope Scope, client Client) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, client.ID)
		var existing Client
		if err := decode(tx.Bucket(bucketClients).Get(key), &existing); err != nil {
			return err
		}
		if !clientInScope(existing, scope) || !validClientTransition(existing.Status, client.Status) {
			return ErrInvalidState
		}
		if strings.TrimSpace(client.Name) == "" || client.PolicyID != existing.PolicyID {
			return errors.New("client name is required and policy id cannot change")
		}
		client.TenantID, client.ProjectID = scope.TenantID, scope.ProjectID
		client.CreatedAt = existing.CreatedAt
		client.UpdatedAt = time.Now().UTC()
		return put(tx.Bucket(bucketClients), key, client)
	})
}

func (s *BoltStore) GetClient(_ context.Context, scope Scope, id string) (Client, error) {
	scope = scope.Normalize()
	var client Client
	err := s.db.View(func(tx *bolt.Tx) error {
		return decode(tx.Bucket(bucketClients).Get(scopedKey(scope, id)), &client)
	})
	if err == nil && !clientInScope(client, scope) {
		err = ErrNotFound
	}
	return client, err
}

func (s *BoltStore) ListClients(ctx context.Context, scope Scope, filter ClientFilter) ([]ClientSummary, error) {
	scope = scope.Normalize()
	clients := make([]Client, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketClients).Cursor()
		prefix := scopedPrefix(scope)
		for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
			var client Client
			if err := decode(raw, &client); err != nil {
				return err
			}
			if (!filter.IncludeDeleted && client.Status == ClientDeleted) || (filter.Status != "" && client.Status != filter.Status) {
				continue
			}
			if search := strings.ToLower(strings.TrimSpace(filter.Search)); search != "" && !strings.Contains(strings.ToLower(client.Name+" "+client.Description), search) {
				continue
			}
			clients = append(clients, client)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].CreatedAt.Before(clients[j].CreatedAt) })
	out := make([]ClientSummary, 0, len(clients))
	for _, client := range clients {
		policy, err := s.GetPolicy(ctx, scope, client.PolicyID)
		if err != nil {
			return nil, err
		}
		credentials, err := s.ListCredentials(ctx, scope, client.ID)
		if err != nil {
			return nil, err
		}
		views := make([]CredentialView, 0, len(credentials))
		active := 0
		for _, credential := range credentials {
			views = append(views, credential.View())
			if credential.Status == CredentialActive && (credential.ExpiresAt == nil || credential.ExpiresAt.After(time.Now())) {
				active++
			}
		}
		usage, err := s.GetUsage(ctx, scope, client.ID, UsageQuery{From: startOfDay(time.Now()), To: time.Now()})
		if err != nil {
			return nil, err
		}
		today := UsageBucket{}
		if len(usage.Daily) > 0 {
			today = usage.Daily[len(usage.Daily)-1]
		}
		out = append(out, ClientSummary{Client: client, Policy: policy, Credentials: views, ActiveCredentials: active, Today: today})
	}
	return out, nil
}

func (s *BoltStore) CreateCredential(_ context.Context, scope Scope, credential Credential) error {
	scope = scope.Normalize()
	credential.ID = strings.TrimSpace(credential.ID)
	credential.ClientID = strings.TrimSpace(credential.ClientID)
	credential.Prefix = strings.TrimSpace(credential.Prefix)
	if credential.ID == "" || credential.ClientID == "" || credential.Prefix == "" || len(credential.SecretHMAC) == 0 || credential.HMACKeyID == "" {
		return errors.New("credential id, client id, prefix, HMAC and HMAC key id are required")
	}
	if credential.Kind == "" {
		credential.Kind = CredentialStandard
	}
	if credential.Kind != CredentialStandard && credential.Kind != CredentialManaged {
		return errors.New("unsupported credential kind")
	}
	if credential.Kind == CredentialManaged && (len(credential.SecretCiphertext) == 0 || len(credential.SecretNonce) == 0) {
		return errors.New("managed credential requires encrypted secret material")
	}
	if credential.Status == "" {
		credential.Status = CredentialActive
	}
	if credential.Status != CredentialActive && credential.Status != CredentialDisabled {
		return errors.New("new credential status must be active or disabled")
	}
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = time.Now().UTC()
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		var client Client
		if err := decode(tx.Bucket(bucketClients).Get(scopedKey(scope, credential.ClientID)), &client); err != nil {
			return err
		}
		if client.Status == ClientDeleted || !clientInScope(client, scope) {
			return ErrInvalidState
		}
		key := scopedKey(scope, credential.ID)
		if tx.Bucket(bucketCredentials).Get(key) != nil || tx.Bucket(bucketCredentialByPrefix).Get(scopedKey(scope, credential.Prefix)) != nil {
			return ErrAlreadyExists
		}
		if err := put(tx.Bucket(bucketCredentials), key, credential); err != nil {
			return err
		}
		if err := tx.Bucket(bucketCredentialByPrefix).Put(scopedKey(scope, credential.Prefix), []byte(credential.ID)); err != nil {
			return err
		}
		hmacIndex := credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
		if tx.Bucket(bucketCredentialByHMAC).Get(scopedKey(scope, hmacIndex)) != nil {
			return ErrAlreadyExists
		}
		return tx.Bucket(bucketCredentialByHMAC).Put(scopedKey(scope, hmacIndex), []byte(credential.ID))
	})
}

func (s *BoltStore) GetCredential(_ context.Context, scope Scope, id string) (Credential, error) {
	scope = scope.Normalize()
	var credential Credential
	err := s.db.View(func(tx *bolt.Tx) error {
		if err := decode(tx.Bucket(bucketCredentials).Get(scopedKey(scope, id)), &credential); err != nil {
			return err
		}
		var client Client
		return decode(tx.Bucket(bucketClients).Get(scopedKey(scope, credential.ClientID)), &client)
	})
	return credential, err
}

func (s *BoltStore) GetCredentialByHMAC(ctx context.Context, scope Scope, keyID string, digest []byte) (Credential, error) {
	scope = scope.Normalize()
	var credentialID string
	err := s.db.View(func(tx *bolt.Tx) error {
		index := keyID + "\x00" + hex.EncodeToString(digest)
		raw := tx.Bucket(bucketCredentialByHMAC).Get(scopedKey(scope, index))
		if len(raw) == 0 {
			return ErrNotFound
		}
		credentialID = string(raw)
		return nil
	})
	if err != nil {
		return Credential{}, err
	}
	return s.GetCredential(ctx, scope, credentialID)
}

func (s *BoltStore) ListCredentials(_ context.Context, scope Scope, clientID string) ([]Credential, error) {
	scope = scope.Normalize()
	out := make([]Credential, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketCredentials).Cursor()
		prefix := scopedPrefix(scope)
		for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
			var credential Credential
			if err := decode(raw, &credential); err != nil {
				return err
			}
			if credential.ClientID == clientID {
				out = append(out, credential)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, err
}

func validCredentialTransition(from, to CredentialStatus) bool {
	if from == to {
		return true
	}
	if from == CredentialRevoked || from == CredentialExpired {
		return false
	}
	switch to {
	case CredentialActive, CredentialDisabled, CredentialExpired, CredentialRevoked:
		return true
	default:
		return false
	}
}

func (s *BoltStore) UpdateCredentialStatus(_ context.Context, scope Scope, id string, status CredentialStatus, now time.Time) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		if !validCredentialTransition(credential.Status, status) {
			return ErrInvalidState
		}
		credential.Status = status
		if status == CredentialRevoked {
			value := now.UTC()
			credential.RevokedAt = &value
		}
		return put(tx.Bucket(bucketCredentials), key, credential)
	})
}

func (s *BoltStore) DeleteCredential(_ context.Context, scope Scope, id string) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		if status := credential.View().Status; status != CredentialRevoked && status != CredentialExpired {
			return ErrInvalidState
		}
		if err := tx.Bucket(bucketCredentials).Delete(key); err != nil {
			return err
		}
		if credential.Prefix != "" {
			if err := tx.Bucket(bucketCredentialByPrefix).Delete(scopedKey(scope, credential.Prefix)); err != nil {
				return err
			}
		}
		if credential.HMACKeyID != "" && len(credential.SecretHMAC) > 0 {
			index := credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
			if err := tx.Bucket(bucketCredentialByHMAC).Delete(scopedKey(scope, index)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BoltStore) TouchCredential(_ context.Context, scope Scope, id string, at time.Time) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		value := at.UTC()
		credential.LastUsedAt = &value
		return put(tx.Bucket(bucketCredentials), key, credential)
	})
}

func (s *BoltStore) GetPolicy(_ context.Context, scope Scope, id string) (ClientPolicy, error) {
	scope = scope.Normalize()
	var policy ClientPolicy
	err := s.db.View(func(tx *bolt.Tx) error {
		return decode(tx.Bucket(bucketPolicies).Get(scopedKey(scope, id)), &policy)
	})
	if err == nil && policy.ProjectID != scope.ProjectID {
		err = ErrNotFound
	}
	return policy, err
}

func (s *BoltStore) UpdatePolicy(_ context.Context, scope Scope, policy ClientPolicy) error {
	scope = scope.Normalize()
	policy.ProjectID = scope.ProjectID
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, policy.ID)
		if tx.Bucket(bucketPolicies).Get(key) == nil {
			return ErrNotFound
		}
		return put(tx.Bucket(bucketPolicies), key, policy)
	})
}

func startOfDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func startOfMinute(value time.Time) time.Time {
	value = value.UTC()
	return value.Truncate(time.Minute)
}

func usageKey(scope Scope, clientID string, day time.Time) []byte {
	return scopedKey(scope, clientID+"\x00"+startOfDay(day).Format("2006-01-02"))
}

func minuteUsageKey(scope Scope, clientID string, minute time.Time) []byte {
	return scopedKey(scope, clientID+"\x00"+startOfMinute(minute).Format(time.RFC3339))
}

func reservationKey(scope Scope, requestID string) []byte { return scopedKey(scope, requestID) }

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
	return s.db.Update(func(tx *bolt.Tx) error {
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
}

func (s *BoltStore) AddRejectedUsage(_ context.Context, scope Scope, clientID string, at time.Time) error {
	scope = scope.Normalize()
	day := startOfDay(at)
	minuteStart := startOfMinute(at)
	return s.db.Update(func(tx *bolt.Tx) error {
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

func (s *BoltStore) RequiredHMACKeyIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCredentials).ForEach(func(_, raw []byte) error {
			var credential Credential
			if err := decode(raw, &credential); err != nil {
				return err
			}
			if credential.HMACKeyID != "" && credential.Status != CredentialRevoked {
				ids[credential.HMACKeyID] = true
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
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
