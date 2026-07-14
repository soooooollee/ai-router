package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrInvalidBackupName = errors.New("invalid managed backup name")

// Entry describes a backup managed by AI Router.
type Entry struct {
	Name       string
	Path       string
	Size       int64
	ModifiedAt time.Time
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Backup copies the current file to a unique, private backup. A missing source
// is not an error and returns an empty backup name.
func Backup(path, marker string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return BackupData(path, marker, raw)
}

// BackupData writes an explicit snapshot using the same naming and permission
// rules as Backup. It is useful when one visible backup represents multiple
// related files.
func BackupData(path, marker string, raw []byte) (string, error) {
	name := path + marker + time.Now().UTC().Format("20060102T150405.000000000Z") + "." + randomSuffix()
	if err := AtomicWrite(name, raw, 0600); err != nil {
		return "", err
	}
	return name, nil
}

// AtomicWrite writes and fsyncs a temporary file before atomically replacing
// the target, then fsyncs the containing directory.
func AtomicWrite(path string, raw []byte, permission fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp := path + ".airoute.tmp." + randomSuffix()
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permission)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	if directory, openErr := os.Open(dir); openErr == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	complete = true
	return nil
}

func List(path, marker string) ([]Entry, error) {
	matches, err := filepath.Glob(path + marker + "*")
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(matches))
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr == nil {
			out = append(out, Entry{Name: filepath.Base(match), Path: match, Size: info.Size(), ModifiedAt: info.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

func Prune(path, marker string, keep int) error {
	entries, err := List(path, marker)
	if err != nil {
		return err
	}
	if keep < 0 {
		keep = 0
	}
	for _, entry := range entries[min(keep, len(entries)):] {
		if err = os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ResolveBackup validates a user-provided basename against a managed marker.
func ResolveBackup(path, marker, name string) (string, bool) {
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", false
	}
	if !strings.HasPrefix(name, filepath.Base(path)+marker) {
		return "", false
	}
	return filepath.Join(filepath.Dir(path), name), true
}

// RemoveBackup deletes only a regular backup file managed for path.
func RemoveBackup(path, marker, name string) error {
	backupPath, ok := ResolveBackup(path, marker, name)
	if !ok {
		return ErrInvalidBackupName
	}
	info, err := os.Lstat(backupPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidBackupName
	}
	return os.Remove(backupPath)
}
