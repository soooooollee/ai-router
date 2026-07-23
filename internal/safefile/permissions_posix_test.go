//go:build !windows

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !PermissionsPrivate(info) {
		t.Fatal("0600 file should be private")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if PermissionsPrivate(info) {
		t.Fatal("0644 file should not be private")
	}
}
