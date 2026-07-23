package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/clientauth"
	"github.com/zbss/airoute/internal/clientstore"
)

func clientStateCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("client-state requires backup, verify, or restore")
	}
	action := args[0]
	args = args[1:]
	statePath, _, err := runtimePaths()
	if err != nil {
		return err
	}
	runtimeDirectory := filepath.Dir(statePath)
	databasePath := filepath.Join(runtimeDirectory, "gateway-state.db")
	masterKeyPath := filepath.Join(runtimeDirectory, "credential-master.key")
	switch action {
	case "backup":
		flags := flag.NewFlagSet("client-state backup", flag.ContinueOnError)
		output := flags.String("output", "", "backup database path")
		if err = flags.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*output) == "" {
			directory := filepath.Join(runtimeDirectory, "backups", time.Now().UTC().Format("20060102T150405.000000000Z"))
			*output = filepath.Join(directory, "gateway-state.db")
		}
		store, openErr := clientstore.Open(databasePath)
		if openErr != nil {
			return fmt.Errorf("open client state for backup (stop AI Router or create the backup from the console): %w", openErr)
		}
		defer store.Close()
		keys, keyErr := clientauth.LoadOrCreateKeyRing(masterKeyPath)
		if keyErr != nil {
			return keyErr
		}
		manifest, backupErr := store.Backup(context.Background(), *output, keys.IDs(), keys.BackupPath())
		if backupErr != nil {
			return backupErr
		}
		fmt.Printf("Client state backup created: %s\n", *output)
		fmt.Printf("Manifest: %s.manifest.json\n", *output)
		fmt.Printf("Required HMAC keys: %s\n", strings.Join(manifest.HMACKeyIDs, ", "))
		return nil
	case "verify", "restore":
		flags := flag.NewFlagSet("client-state "+action, flag.ContinueOnError)
		backup := flags.String("backup", "", "backup database path")
		if err = flags.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*backup) == "" {
			return errors.New("--backup is required")
		}
		manifestPath := *backup + ".manifest.json"
		manifest, readErr := clientstore.ReadBackupManifest(manifestPath)
		if readErr != nil {
			return readErr
		}
		var keys *clientauth.KeyRing
		if manifest.MasterKeyFile != "" {
			keys, err = clientauth.LoadBackupKeyRing(filepath.Join(filepath.Dir(*backup), manifest.MasterKeyFile))
		} else {
			if strings.TrimSpace(os.Getenv("AIROUTE_CREDENTIAL_MASTER_KEY")) == "" {
				return errors.New("backup uses an external master key; AIROUTE_CREDENTIAL_MASTER_KEY is required")
			}
			keys, err = clientauth.LoadOrCreateKeyRing(masterKeyPath)
		}
		if err != nil {
			return err
		}
		if _, err = clientstore.VerifyBackup(*backup, manifestPath, keys.IDs()); err != nil {
			return err
		}
		if action == "verify" {
			fmt.Printf("Client state backup is valid: %s\n", *backup)
			return nil
		}
		if err = clientstore.RestoreBackup(databasePath, masterKeyPath, *backup, manifestPath, keys.IDs()); err != nil {
			return err
		}
		fmt.Printf("Client state restored: %s\n", databasePath)
		return nil
	default:
		return fmt.Errorf("unknown client-state action %q", action)
	}
}
