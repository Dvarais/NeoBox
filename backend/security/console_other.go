//go:build !windows

package security

// HideConsoleIfNeeded is a no-op on non-Windows platforms.
func HideConsoleIfNeeded() bool {
	return false
}
