//go:build windows

package safefile

import "io/fs"

// PermissionsPrivate deliberately does not interpret FileMode permission bits
// on Windows. Windows protects files with ACLs, while os.FileMode exposes only
// synthetic permission bits that cannot represent those ACLs and commonly
// appear broader than 0600 even for files inside the user's private AppData
// directory.
func PermissionsPrivate(_ fs.FileInfo) bool {
	return true
}
