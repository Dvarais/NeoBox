package security

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"

	"NeoBox/backend/core"
)

// hideWindow sets the SysProcAttr on Windows exec.Cmd to prevent flashing console windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// autostartRunKey is the Windows registry location that Explorer processes on
// every user logon — exactly when the system tray (notification area) is already
// up and ready to receive Shell_NotifyIcon(NIM_ADD).
const autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// safeAutostartName reduces a task name to characters that cannot alter the
// shape of the registry value being written.
//
// One function rather than the three identical copies that were here, each
// carrying a comment asking the next reader to keep it in step with the others.
// Set, remove and query all address the same value, so a copy that drifted
// would not fail loudly — it would write under one name and look for another,
// leaving the autostart checkbox permanently disagreeing with the registry.
func safeAutostartName(taskName string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == ' ' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return -1 // Remove disallowed characters
	}, taskName)
}

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
	// Best-effort: remove any leftover elevated Task Scheduler tasks from the old
	// implementation, which would otherwise launch a second instance at logon.
	RemoveLegacyScheduledTasks()
	removeLegacyScheduledTask(taskName)

	safeName := safeAutostartName(taskName)
	if safeName == "" {
		return fmt.Errorf("invalid task name: must contain only alphanumeric characters, spaces, hyphens, or underscores")
	}

	// Validate appPath — must be an absolute path to an existing .exe file
	if !filepath.IsAbs(appPath) {
		return fmt.Errorf("app path must be absolute: %s", appPath)
	}
	if !strings.HasSuffix(strings.ToLower(appPath), ".exe") {
		return fmt.Errorf("app path must point to an executable (.exe): %s", appPath)
	}
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		return fmt.Errorf("app path does not exist: %s", appPath)
	}

	// appPath goes into a REG_SZ value consumed by the shell. Quote it so paths
	// containing spaces (e.g. "C:\Program Files\NeoBox\NeoBox.exe") are preserved.
	quotedPath := `"` + appPath + `"`

	// Open the key with only the access actually needed to write the value.
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
		return nil // Nothing to remove for invalid name
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		// Key missing means there's nothing to remove — not an error.
		return nil
	}
	defer k.Close()
	_ = k.DeleteValue(safeName)
	return nil
}

// AutostartTarget returns the command line currently registered under the Run
// key, or "" when nothing is registered.
//
// The command line, not a bool. "Is a value there" cannot tell a working
// autostart from one pointing at a path that no longer exists — a reinstall
// into a different directory leaves exactly that behind, and the checkbox in
// the window goes on claiming the app starts with Windows.
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

// removeLegacyScheduledTask deletes a Task Scheduler entry created by the old
// /rl highest implementation. Errors are ignored — the task may simply not exist.
func removeLegacyScheduledTask(taskName string) {
	if taskName == "" {
		return
	}
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

// killSwitchLANRemoteIPs — адреса, которым Kill Switch не мешает: петля и
// локальные сети. Одна запись, которую не принимает netsh, роняет всё правило,
// а с ним и подключение, поэтому список отдельной константой и под тестом.
const killSwitchLANRemoteIPs = "127.0.0.1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,fe80::/10,fc00::/7"

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
	// Resolve the VPN server BEFORE touching the firewall. A block-all rule with
	// no exception for the server cuts the tunnel itself: the core can never
	// reconnect, so nothing is left that could take the rule back down. Refusing
	// to arm a kill switch we cannot arm correctly is the only safe outcome.
	ips, err := resolveKillSwitchHost(serverHost)
	if err != nil {
		return err
	}

	// First disable any pre-existing rules to avoid duplication.
	_ = DisableKillSwitch()

	// 1. Allow local loopback and LAN subnets (IPv4 + IPv6) FIRST.
	// netsh accepts a mixed IPv4/IPv6 address list for one rule.
	//
	// Список вынесен в killSwitchLANRemoteIPs: его инварианты проверяются
	// тестом, потому что одна неудачная запись роняет всё правило целиком, а
	// вместе с ним и подключение.
	//
	// Здесь НЕТ ::1, и это не упущение. netsh отвергает адрес IPv6-петли в
	// remoteip в любой записи — ::1, ::1/128, 0:0:0:0:0:0:0:1, диапазон ::1-::1
	// дают «Указан недопустимый IP-адрес». Одного такого элемента хватало,
	// чтобы правило не создалось целиком, а вместе с ним отваливался весь Kill
	// Switch и отменялось подключение. Потери от отсутствия ::1 нет: брандмауэр
	// Windows трафик петли не фильтрует вовсе, так что разрешать её нечем и
	// незачем — 127.0.0.1 оставлен только потому, что принимается и безвреден.
	//
	// fe80::/10, а не /16: канальные адреса занимают именно /10 (fe80–febf), и
	// узкая маска пропускала бы часть из них мимо разрешения.
	//
	// fc00::/7 — уникальные локальные адреса, IPv6-аналог 10/8 и 192.168/16.
	// Без них домашняя сеть на IPv6 оказывалась отрезанной, хотя IPv4-половина
	// того же правила её разрешала. Наружу такие адреса не маршрутизируются,
	// поэтому разрешение не расширяет дыру в туннеле.
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

	// 2. Allow traffic to the specific VPN server IP(s).
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

	// 3. Block ALL outbound traffic LAST, so the allow exceptions above are
	// already in place when the default-deny takes effect.
	if err := runNetsh("advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch",
		"dir=out",
		"action=block",
		"profile=any",
		"description="+killSwitchDesc,
	); err != nil {
		// Roll back the allow rules: without the block rule they protect nothing,
		// and leaving them behind only pollutes the user's firewall.
		_ = DisableKillSwitch()
		return fmt.Errorf("failed to install the block rule: %w", err)
	}

	return nil
}

// resolveKillSwitchHost turns the VPN server address into the IPs that must stay
// reachable while the kill switch is armed.
func resolveKillSwitchHost(serverHost string) ([]net.IP, error) {
	if serverHost == "" || serverHost == "127.0.0.1" || serverHost == "localhost" {
		return nil, fmt.Errorf("cannot arm the kill switch: no VPN server address to exempt")
	}

	if parsed := net.ParseIP(serverHost); parsed != nil {
		if !isRoutableServerIP(parsed) {
			return nil, fmt.Errorf("cannot arm the kill switch: %q is not a routable VPN server address", serverHost)
		}
		return []net.IP{parsed}, nil
	}

	// A domain name — resolve it with a timeout. This happens before the block
	// rule exists, so DNS still works.
	//
	// core.ResolveHost, а не системный резолвер: исключение в firewall строится
	// по адресу сервера, и подменённый провайдером ответ прописал бы в брандмауэр
	// чужой адрес — kill switch встал бы охранять не ту дверь.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	resolved, err := core.ResolveHost(ctx, serverHost)
	if err != nil {
		return nil, fmt.Errorf("cannot arm the kill switch: failed to resolve VPN server %q: %w", serverHost, err)
	}

	ips := make([]net.IP, 0, len(resolved))
	for _, ip := range resolved {
		if ip == nil || !isRoutableServerIP(ip) {
			continue
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("cannot arm the kill switch: %q resolved to no usable address "+
			"(a fake-IP answer cannot be used as a firewall exception)", serverHost)
	}
	return ips, nil
}

// fakeIPRange mirrors the inet4_range of the "dns-fake" server in
// core.GenerateConfig. Keep the two in sync.
var fakeIPRange = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("198.18.0.0/15")
	return n
}()

// isRoutableServerIP reports whether ip can serve as a firewall exception for
// the VPN server.
//
// With FakeDNS enabled, name resolution inside this process is answered by the
// tunnel itself and returns a synthetic 198.18.x.x address. Allow-listing that
// address protects nothing AND omits the real server, so the block-all rule
// would cut the tunnel it is supposed to keep alive.
func isRoutableServerIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && fakeIPRange != nil && fakeIPRange.Contains(ip4) {
		return false
	}
	return true
}

// killSwitchRuleNames is every rule name NeoBox has ever created, including the
// legacy ones, so cleanup never leaves a user's firewall blocking traffic.
var killSwitchRuleNames = []string{
	"NeoBox-KillSwitch",
	"NeoBox-KillSwitch-LAN",
	"NeoBox-KillSwitch-Allow",
	// Legacy rule names from the previous implementation — still cleaned up
	// so users who had the old rules don't accumulate leftovers.
	"NeoBox-KillSwitch-IPv6",
	"NeoBox-KillSwitch-LANv6",
}

// DisableKillSwitch removes the NeoBox firewall rules and verifies they are gone.
//
// Deleting a rule that does not exist makes netsh exit non-zero, which is
// indistinguishable from a real failure by exit code alone and is not worth
// parsing out of localised output. So the deletes stay best-effort and the
// outcome is established afterwards by asking whether any rule still matches —
// a check that is locale-independent and is what callers actually need to know.
func DisableKillSwitch() error {
	for _, name := range killSwitchRuleNames {
		cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		hideWindow(cmd)
		_ = cmd.Run()
	}

	if remaining := KillSwitchRulesPresent(); remaining {
		return fmt.Errorf("NeoBox firewall rules are still present after removal — " +
			"administrator rights are required to delete them")
	}
	return nil
}

// KillSwitchRulesPresent reports whether any NeoBox firewall rule currently
// exists. `netsh ... show rule` exits 0 only when at least one rule matches, so
// the exit code alone answers the question without parsing any output.
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

// runNetsh executes a netsh command and turns a non-zero exit into a descriptive
// error. The output is included because netsh reports the actual cause there —
// most often a missing elevation, which is the failure users actually hit.
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
