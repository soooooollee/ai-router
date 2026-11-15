package clientstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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
	db        *bolt.DB
	path      string
	lastPrune atomic.Int64
}

// Minute buckets exist only to render short request-rate series, so they are
// retained for a day and then dropped. Daily buckets are one small record per
// client per day and stay available for long-range reporting.
const (
	usageMinuteRetention = 24 * time.Hour
	usagePruneInterval   = time.Hour
)

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
	_, _ = store.PruneUsage(context.Background(), time.Now().Add(-usageMinuteRetention))
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
