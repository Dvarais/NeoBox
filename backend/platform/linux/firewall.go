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
	cleanHost := strings.Trim(serverHost, "[]")
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
		"add table inet neobox_killswitch",
		"flush table inet neobox_killswitch",
		"add chain inet neobox_killswitch output { type filter hook output priority 0; policy drop; }",
		"add rule inet neobox_killswitch output oifname \"lo\" accept",
		"add rule inet neobox_killswitch output oifname \"tun*\" accept",
		"add rule inet neobox_killswitch output ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 224.0.0.0/4, 255.255.255.255 } accept",
		"add rule inet neobox_killswitch output ip6 daddr { ::1, fe80::/10, fc00::/7, ff00::/8 } accept",
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
		{"ip6tables", "-w", "2", "-A", "NEOBOX_KILLSWITCH", "-d", "ff00::/8", "-j", "ACCEPT"},
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
	for _, rule := range rules {
		args := strings.Fields(rule)
		if out, err := runFirewallCmd("nft", args...); err != nil {
			_ = f.DisableKillSwitch()
			return fmt.Errorf("nft %s failed: %w (output: %s)", rule, err, string(out))
		}
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

func (f *FirewallManager) DisableKillSwitch() error {
	if _, err := exec.LookPath("nft"); err == nil {
		_, _ = runFirewallCmd("nft", "delete", "table", "inet", "neobox_killswitch")
	}
	if _, err := exec.LookPath("iptables"); err == nil {
		_, _ = runFirewallCmd("iptables", "-w", "2", "-D", "OUTPUT", "-j", "NEOBOX_KILLSWITCH")
		_, _ = runFirewallCmd("iptables", "-w", "2", "-F", "NEOBOX_KILLSWITCH")
		_, _ = runFirewallCmd("iptables", "-w", "2", "-X", "NEOBOX_KILLSWITCH")
	}
	if _, err := exec.LookPath("ip6tables"); err == nil {
		_, _ = runFirewallCmd("ip6tables", "-w", "2", "-D", "OUTPUT", "-j", "NEOBOX_KILLSWITCH")
		_, _ = runFirewallCmd("ip6tables", "-w", "2", "-F", "NEOBOX_KILLSWITCH")
		_, _ = runFirewallCmd("ip6tables", "-w", "2", "-X", "NEOBOX_KILLSWITCH")
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
