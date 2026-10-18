//go:build !windows

package security

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// getMachineSecret gathers unique host and user credentials on Linux/POSIX:
// /etc/machine-id, /var/lib/dbus/machine-id, UID, home directory, and hostname.
func getMachineSecret() string {
	var parts []string

	// 1. System machine ID (systemd / D-Bus)
	hasMachineID := false
	for _, idPath := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if content, err := os.ReadFile(idPath); err == nil {
			id := strings.TrimSpace(string(content))
			if len(id) > 0 {
				parts = append(parts, id)
				hasMachineID = true
				break
			}
		}
	}

	// 2. Hostname fallback: only used if neither /etc/machine-id nor dbus machine-id
	// is present, so DHCP hostname changes on mobile devices cannot break existing keys.
	if !hasMachineID {
		if host, err := os.Hostname(); err == nil && host != "" {
			parts = append(parts, host)
		}
	}

	// 3. User identification (UID + Home directory)
	parts = append(parts, fmt.Sprintf("uid:%d", os.Getuid()))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		parts = append(parts, home)
	}

	// Domain separation constant
	parts = append(parts, "NeoBox-Linux-Credential-Storage-v1")

	return strings.Join(parts, "::")
}

// dpapiProtect encrypts data on Linux/POSIX using host/user-bound AES-256-GCM.
func dpapiProtect(data []byte) ([]byte, error) {
	return machineProtectWithSecret(data, getMachineSecret())
}

// dpapiUnprotect decrypts data on Linux/POSIX using host/user-bound AES-256-GCM.
func dpapiUnprotect(data []byte) ([]byte, error) {
	return machineUnprotectWithSecret(data, getMachineSecret())
}

// LockMemory pins data in RAM using mlock(2) to prevent swapping to disk on POSIX.
func LockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_ = unix.Mlock(data)
	return nil
}

// UnlockMemory releases a region pinned by LockMemory.
func UnlockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_ = unix.Munlock(data)
	return nil
}
