//go:build !windows

package security

import "os"

// ProtectFile restricts file permissions to 0600 (or 0700 for directories) on POSIX systems.
// Setting 0600 on a directory removes execute permissions (+x) and breaks folder access.
func ProtectFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}
