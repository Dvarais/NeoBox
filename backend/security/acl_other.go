//go:build !windows

package security

import "os"

// ProtectFile restricts file permissions to 0600 on POSIX systems.
func ProtectFile(path string) error {
	return os.Chmod(path, 0600)
}
