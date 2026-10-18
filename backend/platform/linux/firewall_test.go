package linux_test

import (
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestFirewallMarkerRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "killswitch.active")
	if err := os.WriteFile(marker, []byte("active"), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	fw := &linux.FirewallManager{}
	fw.RecoverKillSwitch(tmpDir)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker to be removed during recovery")
	}
}

func TestFirewallMarkerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	fw := &linux.FirewallManager{}
	fw.SetUserDataDir(tmpDir)

	marker := filepath.Join(tmpDir, "killswitch.active")
	if err := os.WriteFile(marker, []byte("active"), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	if err := fw.DisableKillSwitch(); err != nil {
		t.Fatalf("DisableKillSwitch failed: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker to be cleared by DisableKillSwitch")
	}
}

func TestParseServerIPs(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"203.0.113.10", "203.0.113.10"},
		{"2001:db8::1", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"127.0.0.1", "127.0.0.1"},
	}

	for _, tc := range cases {
		ips := linux.ParseServerIPs(tc.input)
		if len(ips) == 0 || ips[0] != tc.expected {
			t.Errorf("ParseServerIPs(%q) = %v, want %q", tc.input, ips, tc.expected)
		}
	}

	if empty := linux.ParseServerIPs(""); len(empty) != 0 {
		t.Errorf("expected empty input to return empty slice, got %v", empty)
	}
}

func TestBuildNftablesRulesDualStack(t *testing.T) {
	serverIPs := []string{"203.0.113.10", "2001:db8::1"}
	rules := linux.BuildNftablesRules(serverIPs)

	hasIPv4Server := false
	hasIPv6Server := false
	hasIPv4LAN := false
	hasIPv6LAN := false

	for _, r := range rules {
		if r == "add rule inet neobox_killswitch output ip daddr 203.0.113.10 accept" {
			hasIPv4Server = true
		}
		if r == "add rule inet neobox_killswitch output ip6 daddr 2001:db8::1 accept" {
			hasIPv6Server = true
		}
		if r == "add rule inet neobox_killswitch output ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 224.0.0.0/4, 255.255.255.255 } accept" {
			hasIPv4LAN = true
		}
		if r == "add rule inet neobox_killswitch output ip6 daddr { ::1, fe80::/10, fc00::/7, ff00::/8 } accept" {
			hasIPv6LAN = true
		}
	}

	if !hasIPv4Server {
		t.Errorf("missing IPv4 server accept rule in nftables: %v", rules)
	}
	if !hasIPv6Server {
		t.Errorf("missing IPv6 server accept rule in nftables: %v", rules)
	}
	if !hasIPv4LAN {
		t.Errorf("missing IPv4 LAN accept rule in nftables: %v", rules)
	}
	if !hasIPv6LAN {
		t.Errorf("missing IPv6 LAN accept rule in nftables: %v", rules)
	}
}

func TestBuildIptablesCommandsDualStack(t *testing.T) {
	serverIPs := []string{"203.0.113.10", "2001:db8::1"}
	ipv4Cmds, ipv6Cmds := linux.BuildIptablesCommands(serverIPs)

	if len(ipv4Cmds) == 0 {
		t.Fatal("expected non-empty iptables IPv4 commands")
	}
	if len(ipv6Cmds) == 0 {
		t.Fatal("expected non-empty ip6tables IPv6 commands")
	}

	for _, cmd := range ipv4Cmds {
		if cmd[0] != "iptables" {
			t.Errorf("expected command to start with iptables, got %v", cmd)
		}
	}
	for _, cmd := range ipv6Cmds {
		if cmd[0] != "ip6tables" {
			t.Errorf("expected command to start with ip6tables, got %v", cmd)
		}
	}

	hasIPv4Server := false
	for _, cmd := range ipv4Cmds {
		for i, arg := range cmd {
			if arg == "-d" && i+1 < len(cmd) && cmd[i+1] == "203.0.113.10" {
				hasIPv4Server = true
			}
		}
	}
	if !hasIPv4Server {
		t.Errorf("iptables IPv4 commands do not allow 203.0.113.10: %v", ipv4Cmds)
	}

	hasIPv6Server := false
	for _, cmd := range ipv6Cmds {
		for i, arg := range cmd {
			if arg == "-d" && i+1 < len(cmd) && cmd[i+1] == "2001:db8::1" {
				hasIPv6Server = true
			}
		}
	}
	if !hasIPv6Server {
		t.Errorf("ip6tables IPv6 commands do not allow 2001:db8::1: %v", ipv6Cmds)
	}
}
