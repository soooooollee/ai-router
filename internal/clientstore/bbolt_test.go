package clientstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/protocol/ir"
	bolt "go.etcd.io/bbolt"
)

func testStore(t *testing.T) *BoltStore {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "gateway-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedClient(t *testing.T, store Store, id string, policy ClientPolicy) Client {
	t.Helper()
	if policy.ID == "" {
		policy.ID = "policy_" + id
	}
	client := Client{ID: id, Name: "Test client", Status: ClientActive, PolicyID: policy.ID}
	if err := store.CreateClient(context.Background(), DefaultScope, client, policy); err != nil {
		t.Fatal(err)
	}
	created, err := store.GetClient(context.Background(), DefaultScope, id)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestBoltStoreClientCredentialLifecycleAndScope(t *testing.T) {
	store := testStore(t)
	client := seedClient(t, store, "client_a", ClientPolicy{AllowedModels: []string{"gpt"}, AllowedProtocols: []ir.Protocol{ir.OpenAIResponses}})
	if client.TenantID != DefaultTenantID || client.ProjectID != DefaultProjectID {
		t.Fatalf("default scope was not persisted: %#v", client)
	}
	credential := Credential{
		ID: "cred_a", ClientID: client.ID, Prefix: "air_sk_live_cred_a", SecretHMAC: []byte("digest"), HMACKeyID: "key-v1",
		Kind: CredentialStandard, Status: CredentialActive,
	}
	if err := store.CreateCredential(context.Background(), DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCredential(context.Background(), DefaultScope, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.SecretHMAC) != "digest" || got.HMACKeyID != "key-v1" {
		t.Fatalf("credential verification fields were not persisted: %#v", got)
	}
	other := Scope{TenantID: "tenant_other", ProjectID: "project_other"}
	if _, err = store.GetCredential(context.Background(), other, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope credential read returned %v", err)
	}
	if err = store.UpdateCredentialStatus(context.Background(), DefaultScope, credential.ID, CredentialDisabled, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateCredentialStatus(context.Background(), DefaultScope, credential.ID, CredentialActive, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateCredentialStatus(context.Background(), DefaultScope, credential.ID, CredentialRevoked, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateCredentialStatus(context.Background(), DefaultScope, credential.ID, CredentialActive, time.Now()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("revoked credential was re-enabled: %v", err)
	}
	if err = store.DeleteCredential(context.Background(), DefaultScope, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetCredential(context.Background(), DefaultScope, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential remained readable: %v", err)
	}
	if _, err = store.GetCredentialByHMAC(context.Background(), DefaultScope, credential.HMACKeyID, credential.SecretHMAC); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential HMAC index remained readable: %v", err)
	}
}

func TestBoltStoreRejectsDeletingActiveCredential(t *testing.T) {
	store := testStore(t)
	client := seedClient(t, store, "active_delete_client", ClientPolicy{})
	credential := Credential{ID: "active_delete", ClientID: client.ID, Prefix: "sk-active-delete", SecretHMAC: []byte("digest"), HMACKeyID: "key-v1", Status: CredentialActive}
	if err := store.CreateCredential(context.Background(), DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCredential(context.Background(), DefaultScope, credential.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("active credential deletion returned %v", err)
	}
}

func TestCredentialViewReportsElapsedExpiryWithoutExposingDigest(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	credential := Credential{ID: "expired", ClientID: "client", Status: CredentialActive, ExpiresAt: &past, SecretHMAC: []byte("private"), HMACKeyID: "private-key-id"}
	view := credential.View()
	if view.Status != CredentialExpired {
		t.Fatalf("elapsed credential view remained %s", view.Status)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte("hmac")) {
		t.Fatalf("credential view exposed verification material: %s", raw)
	}
}

func TestBoltStoreUsageReservationSettlementAndQuota(t *testing.T) {
	store := testStore(t)
	policy := ClientPolicy{ID: "policy_a", DailyRequestLimit: 2, DailyInputTokens: 100, DailyOutputTokens: 50}
	client := seedClient(t, store, "client_a", policy)
	now := time.Now().UTC()
	first := UsageReservation{RequestID: "req_1", ClientID: client.ID, Day: now, InputTokens: 30, OutputTokens: 20}
	if err := store.ReserveUsage(context.Background(), DefaultScope, first, policy); err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveUsage(context.Background(), DefaultScope, first, policy); err != nil {
		t.Fatalf("idempotent reservation failed: %v", err)
	}
	if err := store.SettleUsage(context.Background(), DefaultScope, UsageDelta{RequestID: first.RequestID, InputTokens: 25, OutputTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUsage(context.Background(), DefaultScope, UsageDelta{RequestID: first.RequestID, InputTokens: 25, OutputTokens: 8}); err != nil {
		t.Fatalf("idempotent settlement failed: %v", err)
	}
	second := UsageReservation{RequestID: "req_2", ClientID: client.ID, Day: now, InputTokens: 70, OutputTokens: 42}
	if err := store.ReserveUsage(context.Background(), DefaultScope, second, policy); err != nil {
		t.Fatal(err)
	}
	third := UsageReservation{RequestID: "req_3", ClientID: client.ID, Day: now, InputTokens: 1, OutputTokens: 1}
	if err := store.ReserveUsage(context.Background(), DefaultScope, third, policy); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("request quota was not enforced: %v", err)
	}
	if err := store.SettleUsage(context.Background(), DefaultScope, UsageDelta{RequestID: second.RequestID, InputTokens: 60, OutputTokens: 35, Error: true, Estimated: true}); err != nil {
		t.Fatal(err)
	}
	usage, err := store.GetUsage(context.Background(), DefaultScope, client.ID, UsageQuery{From: now, To: now})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total.Requests != 2 || usage.Total.InputTokens != 85 || usage.Total.OutputTokens != 43 || usage.Total.Errors != 1 || usage.Total.Estimated != 1 {
		t.Fatalf("unexpected settled usage: %#v", usage.Total)
	}
	if usage.Total.ReservedInputTokens != 0 || usage.Total.ReservedOutputTokens != 0 {
		t.Fatalf("reservation was not released: %#v", usage.Total)
	}
	if len(usage.Minute) != 1 || usage.Minute[0].Requests != 2 || usage.Minute[0].InputTokens != 85 || usage.Minute[0].OutputTokens != 43 {
		t.Fatalf("minute usage was not aggregated: %#v", usage.Minute)
	}
}

func TestBoltStoreAuditSanitizationAndBackup(t *testing.T) {
	store := testStore(t)
	client := seedClient(t, store, "client_a", ClientPolicy{})
	if err := store.CreateCredential(context.Background(), DefaultScope, Credential{ID: "credential_a", ClientID: client.ID, Prefix: "air_sk_live_credential_a", SecretHMAC: []byte("digest"), HMACKeyID: "key-v2", Status: CredentialActive}); err != nil {
		t.Fatal(err)
	}
	event := AuditEvent{
		ID: "audit_a", ActorType: "admin", ActorID: "local", Action: "credential.created",
		ResourceType: "client", ResourceID: "client_a",
		Metadata: map[string]string{"client_id": "client_a", "credential_prefix": "air_sk_live_x", "secret": "must-not-survive", "api_key": "must-not-survive", "note": "air_sk_live_placeholder"},
	}
	if err := store.AppendAudit(context.Background(), DefaultScope, event); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAudit(context.Background(), DefaultScope, AuditFilter{ClientID: "client_a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metadata["secret"] != "" || events[0].Metadata["api_key"] != "" || events[0].Metadata["note"] != "" || events[0].Metadata["credential_prefix"] == "" {
		t.Fatalf("audit metadata was not sanitized: %#v", events)
	}
	destination := filepath.Join(t.TempDir(), "backup", "state.db")
	masterKeyPath := filepath.Join(t.TempDir(), "credential-master.key")
	if err := os.WriteFile(masterKeyPath, []byte("private-master-key"), 0600); err != nil {
		t.Fatal(err)
	}
	refused := filepath.Join(t.TempDir(), "missing-key", "state.db")
	if _, err := store.Backup(context.Background(), refused, nil, masterKeyPath); err == nil {
		t.Fatal("backup succeeded without a required credential master key")
	}
	if _, err := os.Stat(refused); !os.IsNotExist(err) {
		t.Fatalf("refused backup left a partial database: %v", err)
	}
	manifest, err := store.Backup(context.Background(), destination, []string{"key-v2", "key-v1"}, masterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SHA256 == "" || len(manifest.HMACKeyIDs) != 1 || manifest.HMACKeyIDs[0] != "key-v2" {
		t.Fatalf("invalid backup manifest: %#v", manifest)
	}
	if manifest.MasterKeyFile == "" || manifest.MasterKeySHA256 == "" || manifest.ExternalMasterKey {
		t.Fatalf("master key was not included in backup: %#v", manifest)
	}
	for _, path := range []string{destination, destination + ".manifest.json", destination + ".master.key"} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("backup permissions are too broad: %s %o", path, info.Mode().Perm())
		}
	}
}

func TestPolicyValidationRejectsInvalidLimitsAndCIDR(t *testing.T) {
	for _, policy := range []ClientPolicy{
		{ID: "p", RequestsPerMinute: -1},
		{ID: "p", AllowedCIDRs: []string{"not-a-network"}},
		{ID: "p", AllowedModels: []string{"m", "m"}},
		{ID: "p", AllowedProtocols: []ir.Protocol{ir.OpenAIChat, ir.OpenAIChat}},
		{ID: "p", AllowedProtocols: []ir.Protocol{"made-up-protocol"}},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %#v", policy)
		}
	}
}

func TestCredentialHMACIndexAtTenThousandRecords(t *testing.T) {
	store := testStore(t)
	client := seedClient(t, store, "client_scale", ClientPolicy{})
	keyID := "hmac_scale"
	var target Credential
	err := store.db.Update(func(tx *bolt.Tx) error {
		credentials := tx.Bucket(bucketCredentials)
		byPrefix := tx.Bucket(bucketCredentialByPrefix)
		byHMAC := tx.Bucket(bucketCredentialByHMAC)
		for index := 0; index < 10_000; index++ {
			id := fmt.Sprintf("scale_%05d", index)
			digest := sha256.Sum256([]byte(id))
			credential := Credential{ID: id, ClientID: client.ID, Kind: CredentialStandard, Prefix: "air_sk_live_" + id, SecretHMAC: digest[:], HMACKeyID: keyID, Status: CredentialActive, CreatedAt: time.Now().UTC()}
			if err := put(credentials, scopedKey(DefaultScope, id), credential); err != nil {
				return err
			}
			if err := byPrefix.Put(scopedKey(DefaultScope, credential.Prefix), []byte(id)); err != nil {
				return err
			}
			hmacIndex := keyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
			if err := byHMAC.Put(scopedKey(DefaultScope, hmacIndex), []byte(id)); err != nil {
				return err
			}
			if index == 9_999 {
				target = credential
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	credential, err := store.GetCredentialByHMAC(context.Background(), DefaultScope, target.HMACKeyID, target.SecretHMAC)
	if err != nil || credential.ID != target.ID {
		t.Fatalf("indexed lookup failed: %#v %v", credential, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("indexed credential lookup took too long: %s", elapsed)
	}
}

func TestStoreMigrationRebuildsCredentialIndexes(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "gateway-state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	client := seedClient(t, store, "migration_client", ClientPolicy{})
	digest := sha256.Sum256([]byte("migration-secret"))
	credential := Credential{ID: "migration_credential", ClientID: client.ID, Prefix: "air_sk_live_migration", SecretHMAC: digest[:], HMACKeyID: "hmac_migration", Status: CredentialActive}
	if err = store.CreateCredential(context.Background(), DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveUsage(context.Background(), DefaultScope, UsageReservation{RequestID: "migration_request", ClientID: client.ID, CreatedAt: time.Now(), InputTokens: 4, OutputTokens: 8}, ClientPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err = store.SettleUsage(context.Background(), DefaultScope, UsageDelta{RequestID: "migration_request", InputTokens: 3, OutputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if err = store.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketCredentialByPrefix); err != nil {
			return err
		}
		if err := tx.DeleteBucket(bucketCredentialByHMAC); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	indexed, err := reopened.GetCredentialByHMAC(context.Background(), DefaultScope, credential.HMACKeyID, credential.SecretHMAC)
	if err != nil || indexed.ID != credential.ID {
		t.Fatalf("migration did not rebuild HMAC index: %#v %v", indexed, err)
	}
	usage, err := reopened.GetUsage(context.Background(), DefaultScope, client.ID, UsageQuery{})
	if err != nil || usage.Total.Requests != 1 || usage.Total.InputTokens != 3 || usage.Total.OutputTokens != 5 {
		t.Fatalf("usage did not survive restart: %#v %v", usage, err)
	}
}

func TestStoreMigrationRemovesPreviousKeyFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	client := seedClient(t, store, "old_managed_client", ClientPolicy{})
	digest := sha256.Sum256([]byte("old-managed-secret"))
	credential := Credential{
		ID: "old_managed", ClientID: client.ID, Kind: CredentialStandard,
		Prefix: "air_sk_live_old", SecretHMAC: digest[:], HMACKeyID: "hmac_old",
		Status: CredentialActive,
	}
	if err = store.CreateCredential(context.Background(), DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	if err = store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put([]byte("schema_version"), []byte("4"))
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.GetCredential(context.Background(), DefaultScope, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("previous key format was not removed: %v", err)
	}
	if _, err = store.GetCredentialByHMAC(context.Background(), DefaultScope, credential.HMACKeyID, credential.SecretHMAC); !errors.Is(err, ErrNotFound) {
		t.Fatalf("previous key HMAC index was not removed: %v", err)
	}
}

func TestStoreMigrationRemovesAutomaticDefaultCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	client := Client{ID: "client_default", Name: "Default", Status: ClientActive, PolicyID: "policy_default"}
	if err = store.CreateClient(context.Background(), DefaultScope, client, ClientPolicy{ID: client.PolicyID}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("automatic-default-secret"))
	credential := Credential{
		ID: "automatic_default", ClientID: client.ID, Kind: CredentialManaged,
		Prefix: "sk-automatic", SecretHMAC: digest[:], HMACKeyID: "hmac_default",
		SecretCiphertext: []byte("encrypted"), SecretNonce: []byte("nonce"),
		Status: CredentialActive,
	}
	if err = store.CreateCredential(context.Background(), DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	if err = store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put([]byte("schema_version"), []byte("5"))
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.GetClient(context.Background(), DefaultScope, client.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("automatic default client was not removed: %v", err)
	}
	if _, err = store.GetCredential(context.Background(), DefaultScope, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("automatic default credential was not removed: %v", err)
	}
	if _, err = store.GetCredentialByHMAC(context.Background(), DefaultScope, credential.HMACKeyID, credential.SecretHMAC); !errors.Is(err, ErrNotFound) {
		t.Fatalf("automatic default HMAC index was not removed: %v", err)
	}
	if _, err = store.GetPolicy(context.Background(), DefaultScope, client.PolicyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("automatic default policy was not removed: %v", err)
	}
}

func TestVerifyAndRestoreBackupRequiresMatchingFilesAndStoppedDatabase(t *testing.T) {
	source := testStore(t)
	client := seedClient(t, source, "source_client", ClientPolicy{})
	if err := source.CreateCredential(context.Background(), DefaultScope, Credential{ID: "source_credential", ClientID: client.ID, Prefix: "air_sk_live_source", SecretHMAC: []byte("source-digest"), HMACKeyID: "key-v1", Status: CredentialActive}); err != nil {
		t.Fatal(err)
	}
	backupDirectory := t.TempDir()
	backupDatabase := filepath.Join(backupDirectory, "state.db")
	backupMaster := filepath.Join(t.TempDir(), "credential-master.key")
	if err := os.WriteFile(backupMaster, []byte("backup-master-key-material"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Backup(context.Background(), backupDatabase, []string{"key-v1"}, backupMaster); err != nil {
		t.Fatal(err)
	}
	manifestPath := backupDatabase + ".manifest.json"
	if _, err := VerifyBackup(backupDatabase, manifestPath, nil); err == nil {
		t.Fatal("backup verification ignored missing HMAC key")
	}
	if _, err := VerifyBackup(backupDatabase, manifestPath, []string{"key-v1"}); err != nil {
		t.Fatal(err)
	}
	targetDirectory := t.TempDir()
	targetDatabase := filepath.Join(targetDirectory, "gateway-state.db")
	targetMaster := filepath.Join(targetDirectory, "credential-master.key")
	locked, err := Open(targetDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err = RestoreBackup(targetDatabase, targetMaster, backupDatabase, manifestPath, []string{"key-v1"}); err == nil {
		t.Fatal("restore replaced a locked database")
	}
	if err = locked.Close(); err != nil {
		t.Fatal(err)
	}
	if err = RestoreBackup(targetDatabase, targetMaster, backupDatabase, manifestPath, []string{"key-v1"}); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(targetDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.GetClient(context.Background(), DefaultScope, "source_client"); err != nil {
		t.Fatalf("restored state is missing source client: %v", err)
	}
	masterRaw, err := os.ReadFile(targetMaster)
	if err != nil || string(masterRaw) != "backup-master-key-material" {
		t.Fatalf("master key was not restored: %q %v", masterRaw, err)
	}
	corrupted := filepath.Join(t.TempDir(), "state.db")
	if err = os.WriteFile(corrupted, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(corrupted, manifestPath, []string{"key-v1"}); err == nil {
		t.Fatal("corrupted backup passed verification")
	}
}
