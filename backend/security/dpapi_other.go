//go:build !windows

package security

// On non-Windows platforms (e.g. Linux), file-level permissions (0600)
// and directory permissions (0700) protect the key file.
func dpapiProtect(data []byte) ([]byte, error) {
	return data, nil
}

func dpapiUnprotect(data []byte) ([]byte, error) {
	return data, nil
}

func LockMemory(data []byte) error {
	return nil
}

func UnlockMemory(data []byte) error {
	return nil
}
