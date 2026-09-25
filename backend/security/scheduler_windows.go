//go:build windows

package security

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// hideWindow sets the SysProcAttr on Windows exec.Cmd to prevent flashing console windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// autostartRunKey is the Windows registry location that Explorer processes on
// every user logon — exactly when the system tray (notification area) is already
// up and ready to receive Shell_NotifyIcon(NIM_ADD).
const autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetupAutostart registers NeoBox to launch at user logon via the per-user
// registry Run key (HKCU\...\Run).
func SetupAutostart(taskName string, appPath string) error {
	RemoveLegacyScheduledTasks()
	removeLegacyScheduledTask(taskName)

	safeName := safeAutostartName(taskName)
	if safeName == "" {
		return fmt.Errorf("invalid task name: must contain only alphanumeric characters, spaces, hyphens, or underscores")
	}

	if !filepath.IsAbs(appPath) {
		return fmt.Errorf("app path must be absolute: %s", appPath)
	}
	if !strings.HasSuffix(strings.ToLower(appPath), ".exe") {
		return fmt.Errorf("app path must point to an executable (.exe): %s", appPath)
	}
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		return fmt.Errorf("app path does not exist: %s", appPath)
	}

	quotedPath := `"` + appPath + `"`

	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open Run registry key: %w", err)
	}
	defer k.Close()

	if err := k.SetStringValue(safeName, quotedPath); err != nil {
		return fmt.Errorf("failed to set autostart value: %w", err)
	}
	return nil
}

// RemoveAutostart removes the Run-key value, and also best-effort removes any
// legacy Task Scheduler entry from the previous /rl highest implementation.
func RemoveAutostart(taskName string) error {
	RemoveLegacyScheduledTasks()
	removeLegacyScheduledTask(taskName)

	safeName := safeAutostartName(taskName)
	if safeName == "" {
		return nil
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	_ = k.DeleteValue(safeName)
	return nil
}

// AutostartTarget returns the command line currently registered under the Run
// key, or "" when nothing is registered.
func AutostartTarget(taskName string) string {
	safeName := safeAutostartName(taskName)
	if safeName == "" {
		return ""
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	value, _, err := k.GetStringValue(safeName)
	if err != nil {
		return ""
	}
	return value
}

// RemoveLegacyScheduledTasks removes any legacy elevated Task Scheduler tasks ("NeoBox", "NeoBox-Go", etc.)
// left over from previous versions.
func RemoveLegacyScheduledTasks() {
	names := []string{"NeoBox", "NeoBox-Go"}
	for _, name := range names {
		removeLegacyScheduledTask(name)
	}
}

func removeLegacyScheduledTask(taskName string) {
	if taskName == "" {
		return
	}
	safeName := strings.ReplaceAll(taskName, `"`, `'`)
	cmd := exec.Command("schtasks", "/delete", "/tn", safeName, "/f")
	hideWindow(cmd)
	_ = cmd.Run()
}

const killSwitchDesc = "NeoBox VPN Kill Switch - blocks traffic outside the VPN tunnel. Enabled by the user in NeoBox settings; removed automatically on disconnect."
const killSwitchLANRemoteIPs = "127.0.0.1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,fe80::/10,fc00::/7"

// EnableKillSwitch sets up Windows Firewall rules to block all WAN traffic
// except to local LAN and the VPN server IP.
func EnableKillSwitch(serverHost string) error {
	ips, err := resolveKillSwitchHost(serverHost)
	if err != nil {
		return err
	}

	_ = DisableKillSwitch()

	if err := runNetsh("advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-LAN",
		"dir=out",
		"action=allow",
		"remoteip="+killSwitchLANRemoteIPs,
		"profile=any",
		"description="+killSwitchDesc,
	); err != nil {
		_ = DisableKillSwitch()
		return fmt.Errorf("failed to allow LAN traffic: %w", err)
	}

	for _, ip := range ips {
		if err := runNetsh("advfirewall", "firewall", "add", "rule",
			"name=NeoBox-KillSwitch-Allow",
			"dir=out",
			"action=allow",
			"remoteip="+ip.String(),
			"profile=any",
			"description="+killSwitchDesc,
		); err != nil {
			_ = DisableKillSwitch()
			return fmt.Errorf("failed to allow VPN server %s: %w", ip, err)
		}
	}

	if err := runNetsh("advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch",
		"dir=out",
		"action=block",
		"profile=any",
		"description="+killSwitchDesc,
	); err != nil {
		_ = DisableKillSwitch()
		return fmt.Errorf("failed to install the block rule: %w", err)
	}

	return nil
}

var killSwitchRuleNames = []string{
	"NeoBox-KillSwitch",
	"NeoBox-KillSwitch-LAN",
	"NeoBox-KillSwitch-Allow",
	"NeoBox-KillSwitch-IPv6",
	"NeoBox-KillSwitch-LANv6",
}

// DisableKillSwitch removes the NeoBox firewall rules and verifies they are gone.
func DisableKillSwitch() error {
	for _, name := range killSwitchRuleNames {
		cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		hideWindow(cmd)
		_ = cmd.Run()
	}

	if remaining := KillSwitchRulesPresent(); remaining {
		return fmt.Errorf("NeoBox firewall rules are still present after removal — administrator rights are required to delete them")
	}
	return nil
}

// KillSwitchRulesPresent reports whether any NeoBox firewall rule currently exists.
func KillSwitchRulesPresent() bool {
	for _, name := range killSwitchRuleNames {
		cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
		hideWindow(cmd)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func runNetsh(args ...string) error {
	cmd := exec.Command("netsh", args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh %s failed: %w (output: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
