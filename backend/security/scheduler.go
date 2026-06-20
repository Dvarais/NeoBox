package security

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

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
//
// Why registry Run instead of Task Scheduler (schtasks /rl highest):
//   - The /rl highest "onlogon" task fired BEFORE Explorer finished booting, so
//     systray.Run()'s Shell_NotifyIcon(NIM_ADD) silently failed and the tray
//     icon never appeared until the notification area was redrawn later.
//   - Running elevated (/rl highest) also broke UIPI: an elevated process can
//     have trouble interacting with Explorer's medium-integrity shell.
//   - The Run key is processed by Explorer after the shell is fully ready, in
//     the normal user session, so the tray icon shows reliably and immediately.
//
// TUN mode no longer gets auto-elevated privileges; when needed it requests
// elevation via RequestAdmin() (UAC prompt) — exactly like the manual toggle.
//
// Any legacy "NeoBox" Task Scheduler entry from the old implementation is
// best-effort cleaned up here so the user doesn't get a duplicate launch.
func SetupAutostart(taskName string, appPath string) error {
	// Best-effort: remove a leftover elevated Task Scheduler task from the old
	// implementation, which would otherwise launch a second instance at logon.
	removeLegacyScheduledTask(taskName)

	// appPath goes into a REG_SZ value consumed by the shell. Quote it so paths
	// containing spaces (e.g. "C:\Program Files\NeoBox\NeoBox.exe") are preserved.
	quotedPath := `"` + appPath + `"`

	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open Run registry key: %w", err)
	}
	defer k.Close()

	if err := k.SetStringValue(taskName, quotedPath); err != nil {
		return fmt.Errorf("failed to set autostart value: %w", err)
	}
	return nil
}

// RemoveAutostart removes the Run-key value, and also best-effort removes any
// legacy Task Scheduler entry from the previous /rl highest implementation.
func RemoveAutostart(taskName string) error {
	removeLegacyScheduledTask(taskName)

	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		// Key missing means there's nothing to remove — not an error.
		return nil
	}
	defer k.Close()
	_ = k.DeleteValue(taskName)
	return nil
}

// IsAutostartEnabled reports whether the Run-key value is present.
func IsAutostartEnabled(taskName string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(taskName)
	return err == nil
}

// removeLegacyScheduledTask deletes a Task Scheduler entry created by the old
// /rl highest implementation. Errors are ignored — the task may simply not exist.
func removeLegacyScheduledTask(taskName string) {
	// Sanitize only the task name to prevent argument injection via /tn.
	safeName := strings.ReplaceAll(taskName, `"`, `'`)
	cmd := exec.Command("schtasks", "/delete", "/tn", safeName, "/f")
	hideWindow(cmd)
	_ = cmd.Run()
}

// killSwitchDesc is the human-readable description attached to every firewall
// rule created by the Kill Switch. Antivirus analysts reviewing false-positive
// reports rely on rule descriptions to distinguish a user-enabled VPN leak
// guard from ransomware-style traffic blocking, so this must clearly state
// intent and that it was enabled by the user.
//
// NOTE: ASCII hyphen only (no em-dash or other non-ASCII punctuation) — netsh
// on some Windows locales mis-parses non-ASCII characters in the description
// operand, which can cause the rule creation to silently fail.
const killSwitchDesc = "NeoBox VPN Kill Switch - blocks traffic outside the VPN tunnel. Enabled by the user in NeoBox settings; removed automatically on disconnect."

// EnableKillSwitch sets up Windows Firewall rules to block all WAN traffic
// except to local LAN and the VPN server IP, preventing traffic leaks when the
// VPN tunnel drops.
//
// AV-heuristic note: a "block all outbound" firewall rule is the same shape as
// ransomware behavior, so every rule carries an explicit, human-readable
// description (killSwitchDesc) documenting that it is a user-enabled VPN leak
// guard. The block rule omits a literal "0.0.0.0/0" / "::/0" remote-address
// operand - `action=block` with no address constraint already blocks all
// outbound traffic for the direction, and avoiding the blanket-CIDR signature
// keeps static analysis calmer without changing behavior.
//
// Rule ORDERING: the allow exceptions (LAN, VPN server) are created BEFORE the
// block-all rule. Windows Firewall evaluates rules in creation order; creating
// the block first would momentarily block ALL outbound traffic (including the
// DNS/connection to the VPN server itself) until the allow rules land a few
// milliseconds later, which can tear down an already-established tunnel.
func EnableKillSwitch(serverHost string) error {
	// First disable any pre-existing rules to avoid duplication.
	_ = DisableKillSwitch()

	// 1. Allow local loopback and LAN subnets (IPv4 + IPv6) FIRST.
	// netsh accepts a mixed IPv4/IPv6 address list for one rule.
	cmd1 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-LAN",
		"dir=out",
		"action=allow",
		"remoteip=127.0.0.1,::1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,fe80::/16",
		"profile=any",
		"description="+killSwitchDesc,
	)
	hideWindow(cmd1)
	_ = cmd1.Run()

	// 2. Allow traffic to the specific VPN server IP(s).
	// If serverHost is a domain name, we resolve it to IP addresses.
	if serverHost != "" && serverHost != "127.0.0.1" && serverHost != "localhost" {
		var ips []net.IP
		if parsed := net.ParseIP(serverHost); parsed != nil {
			ips = append(ips, parsed)
		} else {
			// It is a domain name - resolve it with a timeout to prevent hanging.
			// We resolve BEFORE installing the block-all rule so DNS still works.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", serverHost)
			cancel()
			if err == nil {
				ips = resolved
			}
		}

		for _, ip := range ips {
			if ip == nil {
				continue
			}
			ipStr := ip.String()
			cmd2 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
				"name=NeoBox-KillSwitch-Allow",
				"dir=out",
				"action=allow",
				"remoteip="+ipStr,
				"profile=any",
				"description="+killSwitchDesc,
			)
			hideWindow(cmd2)
			_ = cmd2.Run()
		}
	}

	// 3. Block ALL outbound traffic LAST, so the allow exceptions above are
	// already in place when the default-deny takes effect.
	cmd3 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch",
		"dir=out",
		"action=block",
		"profile=any",
		"description="+killSwitchDesc,
	)
	hideWindow(cmd3)
	_ = cmd3.Run()

	return nil
}

// DisableKillSwitch removes the NeoBox-related firewall rules.
func DisableKillSwitch() error {
	ruleNames := []string{
		"NeoBox-KillSwitch",
		"NeoBox-KillSwitch-LAN",
		"NeoBox-KillSwitch-Allow",
		// Legacy rule names from the previous implementation — still cleaned up
		// so users who had the old rules don't accumulate leftovers.
		"NeoBox-KillSwitch-IPv6",
		"NeoBox-KillSwitch-LANv6",
	}

	for _, name := range ruleNames {
		cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		hideWindow(cmd)
		_ = cmd.Run()
	}

	return nil
}
