package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteBackupAndPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	if err := AtomicWrite(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := Backup(path, ".bak."); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(path, ".bak.")
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].Name == entries[1].Name {
		t.Fatal("rapid backups must have unique names")
	}
	if err = Prune(path, ".bak.", 2); err != nil {
		t.Fatal(err)
	}
	entries, _ = List(path, ".bak.")
	if len(entries) != 2 {
		t.Fatalf("expected two retained backups, got %d", len(entries))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("target permission=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestResolveBackupRejectsTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airoute.yaml")
	if _, ok := ResolveBackup(path, ".bak.", "../airoute.yaml.bak.bad"); ok {
		t.Fatal("path traversal was accepted")
	}
	if _, ok := ResolveBackup(path, ".bak.", "other.yaml.bak.bad"); ok {
		t.Fatal("unrelated backup was accepted")
	}
	if _, ok := ResolveBackup(path, ".bak.", "airoute.yaml.bak.good"); !ok {
		t.Fatal("valid backup was rejected")
	}
}
