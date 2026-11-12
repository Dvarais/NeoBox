//go:build !windows

package security

// EnsureWindhawkCompatibility is a no-op on non-Windows systems.
func EnsureWindhawkCompatibility() error {
	return nil
}

// IsWindhawkInjected returns false on non-Windows systems.
func IsWindhawkInjected() bool {
	return false
}

// IsWindhawkInstalled returns false on non-Windows systems.
func IsWindhawkInstalled() bool {
	return false
}
