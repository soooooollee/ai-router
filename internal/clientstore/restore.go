package clientstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

func ReadBackupManifest(path string) (BackupManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BackupManifest{}, err
	}
	var manifest BackupManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return BackupManifest{}, fmt.Errorf("decode client state backup manifest: %w", err)
	}
	if manifest.Version != 1 || manifest.Database == "" || manifest.SHA256 == "" {
		return BackupManifest{}, errors.New("client state backup manifest is invalid")
	}
	return manifest, nil
}

func VerifyBackup(databasePath, manifestPath string, availableHMACKeyIDs []string) (BackupManifest, error) {
	manifest, err := ReadBackupManifest(manifestPath)
	if err != nil {
		return BackupManifest{}, err
	}
	if filepath.Base(databasePath) != manifest.Database || filepath.Base(manifest.Database) != manifest.Database {
		return BackupManifest{}, errors.New("backup database does not match its manifest")
	}
	checksum, err := fileSHA256(databasePath)
	if err != nil {
		return BackupManifest{}, err
	}
	if checksum != manifest.SHA256 {
		return BackupManifest{}, errors.New("backup database checksum does not match")
	}
	if manifest.MasterKeyFile != "" {
		if filepath.Base(manifest.MasterKeyFile) != manifest.MasterKeyFile {
			return BackupManifest{}, errors.New("backup master key filename is invalid")
		}
		masterPath := filepath.Join(filepath.Dir(databasePath), manifest.MasterKeyFile)
		masterChecksum, checksumErr := fileSHA256(masterPath)
		if checksumErr != nil {
			return BackupManifest{}, checksumErr
		}
		if masterChecksum != manifest.MasterKeySHA256 {
			return BackupManifest{}, errors.New("backup master key checksum does not match")
		}
	} else if !manifest.ExternalMasterKey {
		return BackupManifest{}, errors.New("backup has no credential master key reference")
	}
	available := map[string]bool{}
	for _, id := range availableHMACKeyIDs {
		available[id] = true
	}
	missing := make([]string, 0)
	for _, id := range manifest.HMACKeyIDs {
		if !available[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return BackupManifest{}, fmt.Errorf("backup requires unavailable HMAC keys: %v", missing)
	}
	return manifest, nil
}

// RestoreBackup replaces a stopped local state database and its local master
// key. The caller must verify the backup key ring first and provide every HMAC
// key ID required by the manifest. An active AI Router process holds the bbolt
// lock and causes this operation to fail before any file is replaced.
func RestoreBackup(targetDatabase, targetMasterKey, backupDatabase, manifestPath string, availableHMACKeyIDs []string) error {
	manifest, err := VerifyBackup(backupDatabase, manifestPath, availableHMACKeyIDs)
	if err != nil {
		return err
	}
	if err = ensureDatabaseUnlocked(targetDatabase); err != nil {
		return err
	}
	databaseRaw, err := os.ReadFile(backupDatabase)
	if err != nil {
		return err
	}
	var masterRaw []byte
	if manifest.MasterKeyFile != "" {
		masterRaw, err = os.ReadFile(filepath.Join(filepath.Dir(backupDatabase), manifest.MasterKeyFile))
		if err != nil {
			return err
		}
	}
	previousDatabase, databaseExists := readOptional(targetDatabase)
	previousMaster, masterExists := readOptional(targetMasterKey)
	rollback := func() {
		if databaseExists {
			_ = writePrivateFile(targetDatabase, previousDatabase)
		} else {
			_ = os.Remove(targetDatabase)
		}
		if manifest.MasterKeyFile != "" {
			if masterExists {
				_ = writePrivateFile(targetMasterKey, previousMaster)
			} else {
				_ = os.Remove(targetMasterKey)
			}
		}
	}
	if manifest.MasterKeyFile != "" {
		if err = writePrivateFile(targetMasterKey, masterRaw); err != nil {
			return err
		}
	}
	if err = writePrivateFile(targetDatabase, databaseRaw); err != nil {
		rollback()
		return err
	}
	validated, err := Open(targetDatabase)
	if err != nil {
		rollback()
		return fmt.Errorf("restored client state is invalid: %w", err)
	}
	if err = validated.Close(); err != nil {
		rollback()
		return err
	}
	return nil
}

func ensureDatabaseUnlocked(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil
	database, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 100 * time.Millisecond})
	if err != nil {
		return fmt.Errorf("client state database is in use; stop AI Router before restoring: %w", err)
	}
	err = database.Close()
	if !existed {
		_ = os.Remove(path)
	}
	return err
}

func readOptional(path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	return raw, err == nil
}
