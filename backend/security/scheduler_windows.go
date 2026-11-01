//go:build windows

package security

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "schtasks", "/delete", "/tn", safeName, "/f")
	hideWindow(cmd)
	_ = cmd.Run()
}

const killSwitchDesc = "NeoBox VPN Kill Switch - blocks traffic outside the VPN tunnel. Enabled by the user in NeoBox settings; removed automatically on disconnect."
const killSwitchLANRemoteIPs = "127.0.0.1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,fe80::/10,fc00::/7"

var (
	savedPoliciesMu sync.Mutex
	savedPolicies   = make(map[string]string) // profile -> policy (e.g. "domainprofile": "blockinbound,allowoutbound")
)

var defaultProfiles = []string{"domainprofile", "privateprofile", "publicprofile"}
var policyRegex = regexp.MustCompile(`(?i)(block|allow)inbound\s*,\s*(block|allow)outbound`)

func saveCurrentFirewallPolicies() {
	savedPoliciesMu.Lock()
	defer savedPoliciesMu.Unlock()

	for _, profile := range defaultProfiles {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, "netsh", "advfirewall", "show", profile, "firewallpolicy")
		hideWindow(cmd)
		out, err := cmd.Output()
		cancel()
		if err == nil {
			match := policyRegex.FindString(string(out))
			if match != "" {
				savedPolicies[profile] = strings.ToLower(strings.ReplaceAll(match, " ", ""))
			}
		}
	}
}

func restoreFirewallPolicies() error {
	savedPoliciesMu.Lock()
	defer savedPoliciesMu.Unlock()

	if len(savedPolicies) == 0 {
		return runNetsh("advfirewall", "set", "allprofiles", "firewallpolicy", "blockinbound,allowoutbound")
	}

	var firstErr error
	for profile, policy := range savedPolicies {
		if err := runNetsh("advfirewall", "set", profile, "firewallpolicy", policy); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	savedPolicies = make(map[string]string)
	return firstErr
}

func findTunInterfaceName() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "tun-") || strings.Contains(name, "wintun") || name == "tun-neobox" {
			return iface.Name
		}
	}
	return ""
}

func removeKillSwitchRules() {
	var wg sync.WaitGroup
	for _, name := range killSwitchRuleNames {
		wg.Add(1)
		go func(ruleName string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+ruleName)
			hideWindow(cmd)
			_ = cmd.Run()
		}(name)
	}
	wg.Wait()
}

// EnableKillSwitch sets up WFP kernel rules (or Netsh fallback) to block all WAN traffic
// except to local LAN, the VPN server IP, the NeoBox app, and the virtual TUN adapter.
//
// Tier 1 attempts a dynamic kernel WFP session via fwpuclnt.dll. WFP dynamic sessions
// are automatically destroyed by the Windows kernel if the process terminates, preventing
// network lockouts. If Base Filtering Engine (BFE) is unavailable, it gracefully falls back
// to Netsh profile policies.
func EnableKillSwitch(serverHost string) error {
	ips, err := resolveKillSwitchHost(serverHost)
	if err != nil {
		return err
	}

	tunName := findTunInterfaceName()

	// Tier 1: Try Native Kernel WFP with Dynamic Session (0 ms teardown on crash)
	if err := EnableWFPKillSwitch(ips, tunName); err == nil {
		return nil
	}

	// Tier 2: Fallback to netsh if BFE service is unavailable
	return enableNetshKillSwitch(ips, tunName)
}

func enableNetshKillSwitch(ips []net.IP, tunName string) error {
	// 1. Capture and save current baseline firewall profile policies BEFORE touching anything
	saveCurrentFirewallPolicies()

	// 2. Remove any previous NeoBox rules without prematurely resetting profile policies
	removeKillSwitchRules()

	// 3. Allow LAN traffic (RFC 1918 + loopback + link-local + ULA)
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

	// 4. Allow direct traffic to VPN server IP(s) on all profiles
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

	// 5. Allow current NeoBox executable (which embeds sing-box core and its encrypted tunnel transport)
	if exe, err := os.Executable(); err == nil && exe != "" {
		_ = runNetsh("advfirewall", "firewall", "add", "rule",
			"name=NeoBox-KillSwitch-App",
			"dir=out",
			"action=allow",
			"program="+exe,
			"profile=any",
			"description="+killSwitchDesc,
		)
	}

	// 6. Allow traffic routed through the virtual TUN adapter ("tun-neobox") via PowerShell InterfaceAlias
	if tunName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		psCmd := fmt.Sprintf(`New-NetFirewallRule -Name 'NeoBox-KillSwitch-TUN' -DisplayName 'NeoBox-KillSwitch-TUN' -Direction Outbound -InterfaceAlias '%s' -Action Allow -Description '%s'`, tunName, killSwitchDesc)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psCmd)
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = DisableKillSwitch()
			return fmt.Errorf("failed to allow TUN interface (%s): %w (output: %s)", tunName, err, strings.TrimSpace(string(out)))
		}
	}

	// 7. Switch default outbound action to block (the real kill switch).
	// This establishes a true default-deny posture where allow rules are properly respected.
	if err := runNetsh("advfirewall", "set", "allprofiles", "firewallpolicy", "blockinbound,blockoutbound"); err != nil {
		_ = DisableKillSwitch()
		return fmt.Errorf("failed to set blockoutbound firewall policy: %w", err)
	}

	return nil
}

var killSwitchRuleNames = []string{
	"NeoBox-KillSwitch",
	"NeoBox-KillSwitch-LAN",
	"NeoBox-KillSwitch-Allow",
	"NeoBox-KillSwitch-IPv6",
	"NeoBox-KillSwitch-LANv6",
	"NeoBox-KillSwitch-App",
	"NeoBox-KillSwitch-TUN",
}

// DisableKillSwitch removes the NeoBox firewall rules and restores default policy.
func DisableKillSwitch() error {
	_ = DisableWFPKillSwitch()

	// 1. Revert default outbound policy back to saved state (or standard allow)
	_ = restoreFirewallPolicies()

	// 2. Remove all created firewall rules
	removeKillSwitchRules()

	if remaining := KillSwitchRulesPresent(); remaining {
		return fmt.Errorf("NeoBox firewall rules are still present after removal — administrator rights are required to delete them")
	}
	return nil
}

// KillSwitchRulesPresent reports whether any NeoBox firewall rule currently exists.
func KillSwitchRulesPresent() bool {
	if IsWFPActive() {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Check core NeoBox rules
	checkRules := []string{
		"NeoBox-KillSwitch-LAN",
		"NeoBox-KillSwitch-Allow",
		"NeoBox-KillSwitch-App",
		"NeoBox-KillSwitch-TUN",
	}

	for _, name := range checkRules {
		cmd := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
		hideWindow(cmd)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func runNetsh(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netsh", args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh %s failed: %w (output: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
