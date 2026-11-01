//go:build !windows

package security

import (
	"errors"
	"net"
)

// IsWFPActive reports whether a dynamic WFP kernel session is active (Windows only).
func IsWFPActive() bool {
	return false
}

// EnableWFPKillSwitch is a no-op stub on non-Windows platforms.
func EnableWFPKillSwitch(serverIPs []net.IP, tunIfName string) error {
	return errors.New("WFP is only supported on Windows")
}

// DisableWFPKillSwitch is a no-op stub on non-Windows platforms.
func DisableWFPKillSwitch() error {
	return nil
}
