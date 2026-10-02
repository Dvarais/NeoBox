package linux_test

import (
	"strings"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestPrivilegeManager(t *testing.T) {
	mgr := &linux.PrivilegeManager{}
	msg := mgr.PrivilegeHelpMessage()
	if !strings.Contains(msg, "setcap") || !strings.Contains(msg, "cap_net_admin") || !strings.Contains(msg, "+eip") {
		t.Fatalf("PrivilegeHelpMessage must provide setcap instructions with +eip, got: %s", msg)
	}
	// Under normal test running unprivileged, CanRunTUN should return a bool without panicking
	_ = mgr.CanRunTUN()
	_ = mgr.CanConfigureFirewall()
}

func TestParseCapNetAdmin(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected bool
	}{
		{
			name:     "empty status",
			status:   "",
			expected: false,
		},
		{
			name:     "no capabilities",
			status:   "Name:\tneobox\nCapInh:\t0000000000000000\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\n",
			expected: false,
		},
		{
			name:     "has cap_net_admin in CapEff (bit 12: 0x1000)",
			status:   "Name:\tneobox\nCapInh:\t0000000000000000\nCapPrm:\t0000000000001000\nCapEff:\t0000000000001000\n",
			expected: true,
		},
		{
			name:     "has cap_net_admin and cap_net_bind_service (bits 10 and 12: 0x1400)",
			status:   "Name:\tneobox\nCapInh:\t0000000000001400\nCapPrm:\t0000000000001400\nCapEff:\t0000000000001400\n",
			expected: true,
		},
		{
			name:     "has only cap_net_bind_service (bit 10: 0x0400)",
			status:   "Name:\tneobox\nCapInh:\t0000000000000400\nCapPrm:\t0000000000000400\nCapEff:\t0000000000000400\n",
			expected: false,
		},
		{
			name:     "full root capabilities",
			status:   "Name:\tneobox\nCapInh:\t0000000000000000\nCapPrm:\t000001ffffffffff\nCapEff:\t000001ffffffffff\n",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := linux.ParseCapNetAdmin(tc.status)
			if got != tc.expected {
				t.Errorf("ParseCapNetAdmin() = %v, expected %v", got, tc.expected)
			}
		})
	}
}

