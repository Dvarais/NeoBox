package linux_test

import (
	"strings"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestPrivilegeManager(t *testing.T) {
	mgr := &linux.PrivilegeManager{}
	msg := mgr.PrivilegeHelpMessage()
	if !strings.Contains(msg, "setcap") || !strings.Contains(msg, "cap_net_admin") {
		t.Fatalf("PrivilegeHelpMessage must provide setcap instructions, got: %s", msg)
	}
	// Under normal test running unprivileged, CanRunTUN should return a bool without panicking
	_ = mgr.CanRunTUN()
	_ = mgr.CanConfigureFirewall()
}
