//go:build windows

package windows

import (
	"time"

	"NeoBox/backend/security"
)

type SecurityManager struct{}

func (s *SecurityManager) Init(userDataDir string) error         { return security.InitEncryption(userDataDir) }
func (s *SecurityManager) Encrypt(plain []byte) ([]byte, error) { return security.Encrypt(plain) }
func (s *SecurityManager) Decrypt(cipher []byte) ([]byte, error) { return security.Decrypt(cipher) }

type FirewallManager struct{}

func (f *FirewallManager) EnableKillSwitch(serverHost string) error { return security.EnableKillSwitch(serverHost) }
func (f *FirewallManager) DisableKillSwitch() error                 { return security.DisableKillSwitch() }
func (f *FirewallManager) RecoverKillSwitch(dir string)             {}

type ProxyManager struct{}

func (m *ProxyManager) SetSystemProxy(addr string) error { return nil }
func (m *ProxyManager) RestoreSystemProxy() error        { return nil }
func (m *ProxyManager) RecoverSystemProxy(dir string)    {}

type PrivilegeManager struct{}

func (m *PrivilegeManager) CanRunTUN() bool           { return IsElevated() }
func (m *PrivilegeManager) CanConfigureFirewall() bool { return IsElevated() }
func (m *PrivilegeManager) PrivilegeHelpMessage() string {
	return "Требуются права администратора Windows для настройки TUN и брандмауэра."
}

type Prober struct{}

func (p *Prober) Ping(host string, timeout time.Duration) int {
	return -1
}

type LifecycleManager struct{}

func (l *LifecycleManager) HideConsoleIfNeeded() {
	security.HideConsoleIfNeeded()
}

func (l *LifecycleManager) TerminateProcess(code int) {
	ExitNow(code)
}
