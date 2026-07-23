//go:build !windows

package safefile

import "io/fs"

// PermissionsPrivate reports whether a sensitive file is inaccessible to the
// owning user's group and to other users on POSIX platforms.
func PermissionsPrivate(info fs.FileInfo) bool {
	return info.Mode().Perm()&0077 == 0
}
