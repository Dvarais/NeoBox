//go:build !windows

package security

import (
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/platform"
)

func linuxAutostartFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "autostart")
	return filepath.Join(dir, "neobox.desktop"), nil
}

// SetupAutostart on Linux creates a standard XDG autostart .desktop file.
func SetupAutostart(taskName string, appPath string) error {
	path, err := linuxAutostartFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=NeoBox
Exec="%s"
Terminal=false
Icon=neobox
Categories=Network;VPN;
X-GNOME-Autostart-enabled=true
`, appPath)
	return os.WriteFile(path, []byte(content), 0644)
}

// RemoveAutostart on Linux removes the XDG autostart .desktop file.
func RemoveAutostart(taskName string) error {
	path, err := linuxAutostartFilePath()
	if err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}

// AutostartTarget returns the path in the autostart .desktop file if present.
func AutostartTarget(taskName string) string {
	path, err := linuxAutostartFilePath()
	if err != nil {
		return ""
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return path
	}
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

// KillSwitchRulesPresent on non-Windows checks if platform firewall rules exist.
func KillSwitchRulesPresent() bool {
	if p := platform.Current(); p != nil && p.Firewall() != nil {
		if lm, ok := p.Firewall().(interface{ RulesPresent() bool }); ok {
			return lm.RulesPresent()
		}
	}
	return false
}
