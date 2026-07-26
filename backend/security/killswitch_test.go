package security

import (
	"net"
	"strings"
	"testing"
)

// A block-all rule with no exception for the VPN server cuts the tunnel itself,
// which leaves nothing running that could take the rule back down. Refusing to
// arm is the only safe outcome, so these inputs must all be rejected.
func TestResolveKillSwitchHostRejectsUnusableAddresses(t *testing.T) {
	for _, host := range []string{"", "127.0.0.1", "localhost"} {
		t.Run("host="+host, func(t *testing.T) {
			ips, err := resolveKillSwitchHost(host)
			if err == nil {
				t.Errorf("expected %q to be rejected, got %v", host, ips)
			}
			if ips != nil {
				t.Errorf("no addresses should be returned on rejection, got %v", ips)
			}
		})
	}
}

func TestResolveKillSwitchHostAcceptsLiteralIPs(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{"IPv4", "203.0.113.10"},
		{"IPv6", "2001:db8::1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ips, err := resolveKillSwitchHost(tc.host)
			if err != nil {
				t.Fatalf("resolveKillSwitchHost(%q) failed: %v", tc.host, err)
			}
			if len(ips) != 1 || !ips[0].Equal(net.ParseIP(tc.host)) {
				t.Errorf("got %v, want a single address equal to %s", ips, tc.host)
			}
		})
	}
}

// With FakeDNS on, resolution inside this process is answered by the tunnel and
// returns a synthetic 198.18.x.x address. Allow-listing that would omit the real
// server, so the block-all rule would cut the very tunnel it protects.
//
// This is not hypothetical: it is what an earlier version of this test hit on a
// developer machine with FakeDNS active, where even a reserved .invalid name
// resolved successfully.
func TestFakeIPAddressesAreNotUsableExceptions(t *testing.T) {
	for _, addr := range []string{"198.18.0.1", "198.18.2.131", "198.19.255.254"} {
		if isRoutableServerIP(net.ParseIP(addr)) {
			t.Errorf("%s is inside the fake-IP range and must not be used as a firewall exception", addr)
		}
	}

	for _, addr := range []string{"203.0.113.10", "8.8.8.8", "2001:db8::1"} {
		if !isRoutableServerIP(net.ParseIP(addr)) {
			t.Errorf("%s is a real address and must be usable as a firewall exception", addr)
		}
	}
}

func TestUnroutableAddressesAreRejected(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "0.0.0.0", "224.0.0.1", "::1", "::"} {
		if isRoutableServerIP(net.ParseIP(addr)) {
			t.Errorf("%s must not be usable as a firewall exception", addr)
		}
	}
}

// The same guard must apply to a literal address, not only to a resolved name,
// so there is no path that installs a rule for an unusable exception.
func TestResolveKillSwitchHostRejectsFakeIPLiteral(t *testing.T) {
	ips, err := resolveKillSwitchHost("198.18.2.131")
	if err == nil {
		t.Fatalf("expected a literal fake IP to be rejected, got %v", ips)
	}
	if !strings.Contains(err.Error(), "kill switch") {
		t.Errorf("the error should explain that the kill switch was not armed, got: %v", err)
	}
}

// Cleanup must cover the legacy rule names too, otherwise users upgrading from
// an older build keep a block-all rule nobody removes.
func TestKillSwitchRuleNamesCoverLegacyRules(t *testing.T) {
	required := []string{
		"NeoBox-KillSwitch",
		"NeoBox-KillSwitch-LAN",
		"NeoBox-KillSwitch-Allow",
		"NeoBox-KillSwitch-IPv6",
		"NeoBox-KillSwitch-LANv6",
	}

	for _, want := range required {
		found := false
		for _, got := range killSwitchRuleNames {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rule %q is never cleaned up", want)
		}
	}
}

// runNetsh must surface the failure rather than swallow it — that swallowing is
// what let a Kill Switch report success while nothing was blocking traffic.
func TestRunNetshReportsFailure(t *testing.T) {
	err := runNetsh("advfirewall", "firewall", "show", "rule", "name=NeoBox-Definitely-No-Such-Rule")
	if err == nil {
		t.Fatal("expected an error for a rule that does not exist")
	}
	if !strings.Contains(err.Error(), "netsh") {
		t.Errorf("the error should name the failing command, got: %v", err)
	}
}
