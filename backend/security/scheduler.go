package security

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"NeoBox/backend/core"
)

// safeAutostartName reduces a task name to characters that cannot alter the
// shape of the registry value being written.
func safeAutostartName(taskName string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == ' ' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return -1 // Remove disallowed characters
	}, taskName)
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
func isRoutableServerIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && fakeIPRange != nil && fakeIPRange.Contains(ip4) {
		return false
	}
	return true
}
