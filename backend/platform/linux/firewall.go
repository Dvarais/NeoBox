package linux

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type FirewallManager struct {
	userDataDir string
}

func (f *FirewallManager) SetUserDataDir(dir string) {
	f.userDataDir = dir
}

func (f *FirewallManager) markerPath(userDataDir string) string {
	return filepath.Join(userDataDir, "killswitch.active")
}

// ParseServerIPs extracts IP addresses from serverHost (IPv4, IPv6, bracketed or FQDN).
func ParseServerIPs(serverHost string) []string {
	trimmed := strings.TrimSpace(serverHost)
	cleanHost := strings.Trim(trimmed, "[]")
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		cleanHost = strings.Trim(host, "[]")
	}
	var serverIPs []string
	if ip := net.ParseIP(cleanHost); ip != nil {
		serverIPs = append(serverIPs, ip.String())
	} else if cleanHost != "" {
		if ips, err := net.LookupIP(cleanHost); err == nil {
			for _, ip := range ips {
				serverIPs = append(serverIPs, ip.String())
			}
		}
	}
	return serverIPs
}

// BuildNftablesRules constructs the inet table rules allowing LAN, VPN tunnel and server IP(s).
func BuildNftablesRules(serverIPs []string) []string {
	rules := []string{
		// Idempotent atomic reset: ensures table exists then deletes it completely,
		// unhooking base chains from netfilter and preventing kernel -EBUSY collisions.
		"add table inet neobox_killswitch",
		"delete table inet neobox_killswitch",
		"add table inet neobox_killswitch",
		"add chain inet neobox_killswitch output { type filter hook output priority 0; policy drop; }",
		"add rule inet neobox_killswitch output oifname \"lo\" accept",
		"add rule inet neobox_killswitch output oifname \"tun*\" accept",
		// Guard against invalid conntrack states; established traffic is restricted to tunnel and allowed destinations
		"add rule inet neobox_killswitch output ct state invalid drop",
		// Prevent PMTUD black-holing (RFC 1191) & IPv6 NDP breakage (RFC 4861) - router-advert excluded on output
		"add rule inet neobox_killswitch output icmp type { destination-unreachable, time-exceeded, echo-request, echo-reply } accept",
		"add rule inet neobox_killswitch output icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, echo-request, echo-reply, nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } accept",
		// LAN & local multicast/broadcast addresses (RFC 4291: ff02::/16 link-local multicast only, not ff00::/8)
		"add rule inet neobox_killswitch output ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 224.0.0.0/4, 255.255.255.255 } accept",
		"add rule inet neobox_killswitch output ip6 daddr { ::1, fe80::/10, fc00::/7, ff02::/16 } accept",
	}

	for _, ipStr := range serverIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			rules = append(rules, fmt.Sprintf("add rule inet neobox_killswitch output ip daddr %s accept", ip.String()))
		} else {
			rules = append(rules, fmt.Sprintf("add rule inet neobox_killswitch output ip6 daddr %s accept", ip.String()))
		}
	}
	return rules
}

// BuildIptablesCommands constructs iptables (IPv4) and ip6tables (IPv6) command sequences.
func BuildIptablesCommands(serverIPs []string) (ipv4Commands [][]string, ipv6Commands [][]string) {
	var ipv4Servers, ipv6Servers []string
	for _, ipStr := range serverIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			ipv4Servers = append(ipv4Servers, ip.String())
		} else {
			ipv6Servers = append(ipv6Servers, ip.String())
		}
	}

	// 1. IPv4 iptables rules
	ipv4Commands = [][]string{
		{"iptables", "-w", "2", "-N", "NEOBOX_KILLSWITCH"},
		{"iptables", "-w", "2", "-I", "OUTPUT", "1", "-j", "NEOBOX_KILLSWITCH"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-o", "lo", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-o", "tun+", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "127.0.0.0/8", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "10.0.0.0/8", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "172.16.0.0/12", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "192.168.0.0/16", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "169.254.0.0/16", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "224.0.0.0/4", "-j", "ACCEPT"},
		{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "255.255.255.255", "-j", "ACCEPT"},
	}
	for _, ip := range ipv4Servers {
		ipv4Commands = append(ipv4Commands, []string{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", ip, "-j", "ACCEPT"})
	}
	ipv4Commands = append(ipv4Commands, []string{"iptables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-j", "DROP"})

	// 2. IPv6 ip6tables rules (essential to prevent IPv6 leaks outside the VPN tunnel)
	ipv6Commands = [][]string{
		{"ip6tables", "-w", "2", "-N", "NEOBOX_KILLSWITCH"},
		{"ip6tables", "-w", "2", "-I", "OUTPUT", "1", "-j", "NEOBOX_KILLSWITCH"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-o", "lo", "-j", "ACCEPT"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-o", "tun+", "-j", "ACCEPT"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "::1/128", "-j", "ACCEPT"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "fe80::/10", "-j", "ACCEPT"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "fc00::/7", "-j", "ACCEPT"},
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "ff02::/16", "-j", "ACCEPT"},
	}
	for _, ip := range ipv6Servers {
		ipv6Commands = append(ipv6Commands, []string{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", ip, "-j", "ACCEPT"})
	}
	ipv6Commands = append(ipv6Commands, []string{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-j", "DROP"})

	return ipv4Commands, ipv6Commands
}

func (f *FirewallManager) EnableKillSwitch(serverHost string) error {
	_ = f.DisableKillSwitch()

	serverIPs := ParseServerIPs(serverHost)
	if len(serverIPs) == 0 {
		return fmt.Errorf("cannot arm kill switch: no valid server IP addresses found for host %q", serverHost)
	}

	var err error
	if _, errPath := exec.LookPath("nft"); errPath == nil {
		err = f.enableNftables(serverIPs)
	} else if _, errPath := exec.LookPath("iptables"); errPath == nil {
		err = f.enableIptables(serverIPs)
	} else {
		return fmt.Errorf("neither nftables nor iptables found on system")
	}

	if err != nil {
		_ = f.DisableKillSwitch()
		return err
	}

	if f.userDataDir != "" {
		marker := f.markerPath(f.userDataDir)
		_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0644)
	}
	return nil
}

func (f *FirewallManager) enableNftables(serverIPs []string) error {
	rules := BuildNftablesRules(serverIPs)
	script := strings.Join(rules, "\n") + "\n"
	if out, err := runFirewallCmdInput([]byte(script), "nft", "-f", "-"); err != nil {
		_ = f.DisableKillSwitch()
		return fmt.Errorf("nft -f - failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func (f *FirewallManager) enableIptables(serverIPs []string) error {
	ipv4Commands, ipv6Commands := BuildIptablesCommands(serverIPs)

	for _, args := range ipv4Commands {
		if out, err := runFirewallCmd(args[0], args[1:]...); err != nil {
			_ = f.DisableKillSwitch()
			return fmt.Errorf("iptables %s failed: %w (output: %s)", strings.Join(args[1:], " "), err, string(out))
		}
	}

	if _, errPath := exec.LookPath("ip6tables"); errPath == nil {
		for _, args := range ipv6Commands {
			if out, err := runFirewallCmd(args[0], args[1:]...); err != nil {
				_ = f.DisableKillSwitch()
				return fmt.Errorf("ip6tables %s failed: %w (output: %s)", strings.Join(args[1:], " "), err, string(out))
			}
		}
	}

	return nil
}

func (f *FirewallManager) RulesPresent() bool {
	if _, err := exec.LookPath("nft"); err == nil {
		out, err := runFirewallCmd("nft", "list", "table", "inet", "neobox_killswitch")
		if err == nil && len(out) > 0 {
			return true
		}
	}
	if _, err := exec.LookPath("iptables"); err == nil {
		_, err := runFirewallCmd("iptables", "-w", "2", "-C", "OUTPUT", "-j", "NEOBOX_KILLSWITCH")
		if err == nil {
			return true
		}
	}
	return false
}

// BuildIPv6LeakProtectionRules constructs netfilter rules to block all non-tunnel global IPv6 leaks (RFC 4291 2000::/3)
// and unencrypted DNS leaks over IPv6 when the full Kill Switch is disarmed.
func BuildIPv6LeakProtectionRules() []string {
	return []string{
		"add table inet neobox_ipv6_guard",
		"delete table inet neobox_ipv6_guard",
		"add table inet neobox_ipv6_guard",
		"add chain inet neobox_ipv6_guard output { type filter hook output priority -10; policy accept; }",
		"add rule inet neobox_ipv6_guard output oifname \"lo\" accept",
		"add rule inet neobox_ipv6_guard output oifname \"tun*\" accept",
		"add rule inet neobox_ipv6_guard output icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, echo-request, echo-reply, nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } accept",
		"add rule inet neobox_ipv6_guard output ip6 daddr { ::1, fe80::/10, fc00::/7, ff02::/16 } accept",
		"add rule inet neobox_ipv6_guard output udp dport { 53, 853 } drop",
		"add rule inet neobox_ipv6_guard output tcp dport { 53, 853 } drop",
		"add rule inet neobox_ipv6_guard output ip6 daddr 2000::/3 drop",
	}
}

func (f *FirewallManager) EnableIPv6LeakProtection() error {
	if _, err := exec.LookPath("nft"); err == nil {
		rules := BuildIPv6LeakProtectionRules()
		script := strings.Join(rules, "\n") + "\n"
		if out, err := runFirewallCmdInput([]byte(script), "nft", "-f", "-"); err != nil {
			return fmt.Errorf("enable IPv6 leak guard failed: %w (output: %s)", err, string(out))
		}
	}
	return nil
}

func (f *FirewallManager) DisableIPv6LeakProtection() error {
	if _, err := exec.LookPath("nft"); err == nil {
		const disarmScript = "add table inet neobox_ipv6_guard\ndelete table inet neobox_ipv6_guard\n"
		_, _ = runFirewallCmdInput([]byte(disarmScript), "nft", "-f", "-")
	}
	return nil
}

func (f *FirewallManager) DisableKillSwitch() error {
	if _, err := exec.LookPath("nft"); err == nil {
		// Atomic idempotent disarm via stdin: create if missing, then delete.
		// Guarantees returncode 0 without spewing "table does not exist" errors.
		const disarmScript = "add table inet neobox_killswitch\ndelete table inet neobox_killswitch\nadd table inet neobox_ipv6_guard\ndelete table inet neobox_ipv6_guard\n"
		_, _ = runFirewallCmdInput([]byte(disarmScript), "nft", "-f", "-")
	}
	// Only invoke legacy iptables cleanup if iptables is present AND rules are actually registered.
	if _, err := exec.LookPath("iptables"); err == nil {
		if _, checkErr := runFirewallCmd("iptables", "-w", "2", "-C", "OUTPUT", "-j", "NEOBOX_KILLSWITCH"); checkErr == nil {
			_, _ = runFirewallCmd("iptables", "-w", "2", "-D", "OUTPUT", "-j", "NEOBOX_KILLSWITCH")
			_, _ = runFirewallCmd("iptables", "-w", "2", "-F", "NEOBOX_KILLSWITCH")
			_, _ = runFirewallCmd("iptables", "-w", "2", "-X", "NEOBOX_KILLSWITCH")
		}
	}
	if _, err := exec.LookPath("ip6tables"); err == nil {
		if _, checkErr := runFirewallCmd("ip6tables", "-w", "2", "-C", "OUTPUT", "-j", "NEOBOX_KILLSWITCH"); checkErr == nil {
			_, _ = runFirewallCmd("ip6tables", "-w", "2", "-D", "OUTPUT", "-j", "NEOBOX_KILLSWITCH")
			_, _ = runFirewallCmd("ip6tables", "-w", "2", "-F", "NEOBOX_KILLSWITCH")
			_, _ = runFirewallCmd("ip6tables", "-w", "2", "-X", "NEOBOX_KILLSWITCH")
		}
	}
	if f.userDataDir != "" {
		_ = os.Remove(f.markerPath(f.userDataDir))
	}
	return nil
}

func (f *FirewallManager) RecoverKillSwitch(userDataDir string) {
	marker := f.markerPath(userDataDir)
	if _, err := os.Stat(marker); err == nil {
		_ = f.DisableKillSwitch()
		_ = os.Remove(marker)
	}
}
