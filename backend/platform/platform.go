package platform

import "time"

// Platform defines the unified interface implemented by each OS.
type Platform interface {
	Paths() PathManager
	Security() SecurityManager
	Firewall() FirewallManager
	Proxy() ProxyManager
	Privileges() PrivilegeManager
	Prober() NetworkProber
	Lifecycle() LifecycleManager
}

// PathManager resolves OS-specific application directories.
type PathManager interface {
	UserDataDir() string
	ConfigDir() string
	LogDir() string
	ResolvePath(elem ...string) string
}

// SecurityManager handles secrets and payload encryption at rest.
type SecurityManager interface {
	Init(userDataDir string) error
	Encrypt(plain []byte) ([]byte, error)
	Decrypt(cipher []byte) ([]byte, error)
}

// FirewallManager manages VPN leak guard rules.
type FirewallManager interface {
	EnableKillSwitch(serverHost string) error
	DisableKillSwitch() error
	RecoverKillSwitch(userDataDir string)
}

// ProxyManager manages OS-level system proxy settings.
type ProxyManager interface {
	SetSystemProxy(addr string) error
	RestoreSystemProxy() error
	RecoverSystemProxy(userDataDir string)
}

// PrivilegeManager checks capabilities and elevation.
type PrivilegeManager interface {
	CanRunTUN() bool
	CanConfigureFirewall() bool
	PrivilegeHelpMessage() string
}

// NetworkProber measures latency to remote endpoints.
type NetworkProber interface {
	Ping(host string, timeout time.Duration) int
}

// LifecycleManager controls console visibility and clean process termination.
type LifecycleManager interface {
	HideConsoleIfNeeded()
	TerminateProcess(code int)
}

var current Platform

// Current returns the active platform implementation.
func Current() Platform {
	return current
}

// SetCurrent registers the active platform implementation.
func SetCurrent(p Platform) {
	current = p
}
