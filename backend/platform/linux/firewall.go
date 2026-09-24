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

func (f *FirewallManager) EnableKillSwitch(serverHost string) error {
	_ = f.DisableKillSwitch()

	ips, _ := net.LookupIP(serverHost)
	var serverIPs []string
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			serverIPs = append(serverIPs, ipv4.String())
		}
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
		return err
	}

	if f.userDataDir != "" {
		marker := f.markerPath(f.userDataDir)
		_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0644)
	}
	return nil
}

func (f *FirewallManager) enableNftables(serverIPs []string) error {
	rules := []string{
		"add table inet neobox_killswitch",
		"flush table inet neobox_killswitch",
		"add chain inet neobox_killswitch output { type filter hook output priority 0; policy drop; }",
		"add rule inet neobox_killswitch output oifname \"lo\" accept",
		"add rule inet neobox_killswitch output ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 } accept",
		"add rule inet neobox_killswitch output oifname \"tun*\" accept",
	}
	for _, ip := range serverIPs {
		rules = append(rules, fmt.Sprintf("add rule inet neobox_killswitch output ip daddr %s accept", ip))
	}

	for _, rule := range rules {
		args := strings.Fields(rule)
		cmd := exec.Command("nft", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = f.DisableKillSwitch()
			return fmt.Errorf("nft %s failed: %w (output: %s)", rule, err, string(out))
		}
	}
	return nil
}

func (f *FirewallManager) enableIptables(serverIPs []string) error {
	commands := [][]string{
		{"iptables", "-N", "NEOBOX_KILLSWITCH"},
		{"iptables", "-I", "OUTPUT", "1", "-j", "NEOBOX_KILLSWITCH"},
		{"iptables", "-A", "NEOBOX_KILLSWITCH", "-o", "lo", "-j", "ACCEPT"},
		{"iptables", "-A", "NEOBOX_KILLSWITCH", "-d", "192.168.0.0/16", "-j", "ACCEPT"},
		{"iptables", "-A", "NEOBOX_KILLSWITCH", "-d", "10.0.0.0/8", "-j", "ACCEPT"},
		{"iptables", "-A", "NEOBOX_KILLSWITCH", "-o", "tun+", "-j", "ACCEPT"},
	}
	for _, ip := range serverIPs {
		commands = append(commands, []string{"iptables", "-A", "NEOBOX_KILLSWITCH", "-d", ip, "-j", "ACCEPT"})
	}
	commands = append(commands, []string{"iptables", "-A", "NEOBOX_KILLSWITCH", "-j", "DROP"})

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = f.DisableKillSwitch()
			return fmt.Errorf("iptables %s failed: %w (output: %s)", strings.Join(args[1:], " "), err, string(out))
		}
	}
	return nil
}

func (f *FirewallManager) DisableKillSwitch() error {
	if _, err := exec.LookPath("nft"); err == nil {
		_ = exec.Command("nft", "delete", "table", "inet", "neobox_killswitch").Run()
	}
	if _, err := exec.LookPath("iptables"); err == nil {
		_ = exec.Command("iptables", "-D", "OUTPUT", "-j", "NEOBOX_KILLSWITCH").Run()
		_ = exec.Command("iptables", "-F", "NEOBOX_KILLSWITCH").Run()
		_ = exec.Command("iptables", "-X", "NEOBOX_KILLSWITCH").Run()
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
