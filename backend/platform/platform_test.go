package platform_test

import (
	"testing"

	"NeoBox/backend/platform"
)

type mockPlatform struct {
	paths      platform.PathManager
	security   platform.SecurityManager
	firewall   platform.FirewallManager
	proxy      platform.ProxyManager
	privileges platform.PrivilegeManager
	prober     platform.NetworkProber
	lifecycle  platform.LifecycleManager
}

func (m *mockPlatform) Paths() platform.PathManager           { return m.paths }
func (m *mockPlatform) Security() platform.SecurityManager     { return m.security }
func (m *mockPlatform) Firewall() platform.FirewallManager     { return m.firewall }
func (m *mockPlatform) Proxy() platform.ProxyManager           { return m.proxy }
func (m *mockPlatform) Privileges() platform.PrivilegeManager { return m.privileges }
func (m *mockPlatform) Prober() platform.NetworkProber         { return m.prober }
func (m *mockPlatform) Lifecycle() platform.LifecycleManager   { return m.lifecycle }

func TestPlatformRegistration(t *testing.T) {
	mock := &mockPlatform{}
	platform.SetCurrent(mock)
	if platform.Current() != mock {
		t.Fatalf("expected platform.Current() to return registered mock")
	}
}
