//go:build !windows

package security

import (
	"NeoBox/backend/platform"
)

// SetupAutostart on non-Windows (stub).
func SetupAutostart(taskName string, appPath string) error {
	return nil
}

// RemoveAutostart on non-Windows (stub).
func RemoveAutostart(taskName string) error {
	return nil
}

// AutostartTarget on non-Windows (stub).
func AutostartTarget(taskName string) string {
	return ""
}

// RemoveLegacyScheduledTasks on non-Windows (no-op).
func RemoveLegacyScheduledTasks() {}

// EnableKillSwitch delegates to the platform FirewallManager.
func EnableKillSwitch(serverHost string) error {
	if p := platform.Current(); p != nil && p.Firewall() != nil {
		return p.Firewall().EnableKillSwitch(serverHost)
	}
	return nil
}

// DisableKillSwitch delegates to the platform FirewallManager.
func DisableKillSwitch() error {
	if p := platform.Current(); p != nil && p.Firewall() != nil {
		return p.Firewall().DisableKillSwitch()
	}
	return nil
}

// KillSwitchRulesPresent on non-Windows.
func KillSwitchRulesPresent() bool {
	return false
}
