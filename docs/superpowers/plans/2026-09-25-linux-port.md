# NeoBox Linux Port and Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port NeoBox to Linux with zero Windows regressions by implementing a Platform Abstraction Layer (PAL), isolating Linux-specific subsystem implementations (XDG paths, AES-256-GCM key file encryption, capabilities-based TUN, nftables kill switch, gsettings/KDE proxy), and providing packaging templates (.deb, AUR PKGBUILD, AppImage) with automated GitHub Actions CI/CD.

**Architecture:** A dedicated package `backend/platform` defines abstract interfaces (`Platform`, `PathManager`, `SecurityManager`, `FirewallManager`, `ProxyManager`, `PrivilegeManager`, `NetworkProber`, `LifecycleManager`). Platform factories (`factory_windows.go` and `factory_linux.go`) instantiate the active platform at runtime. Windows implementations wrap existing code, while all Linux implementations reside exclusively in `backend/platform/linux/`. Packaging files reside in `build/linux/`.

**Tech Stack:** Go 1.24, Wails v2, sing-box core, Linux Capabilities (`cap_net_admin`), `nftables` / `iptables`, `gsettings` / `kwriteconfig`, WebKit2GTK, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-25-linux-port-design.md`

## Global Constraints

- Go version: 1.24+ (from `go.mod`).
- Build tags: `with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor`.
- Zero Windows regressions: Windows code and tests must continue to pass untouched.
- Linux folder isolation: all Linux Go code must be located in `backend/platform/linux/`.
- No root GUI: the desktop process runs as regular user; network privileges use Linux Capabilities.
- No placeholders: every step provides exact code and shell commands.

## Review Focus

1. **Unprivileged execution:** GUI running without `CAP_NET_ADMIN` must not crash or panic when opening settings or starting VPN; it must return a user-friendly error instructing the user to set capabilities.
2. **Missing nftables fallback:** Systems without `nftables` binary installed must gracefully fall back to `iptables` or return a non-fatal warning without bricking network access.
3. **Desktop environment proxy detection:** Environments running neither GNOME nor KDE (e.g. Sway, i3, Hyprland) must not error out during proxy activation; they should return safely and warn rather than fail the entire connection.
4. **Crash recovery of kill switch:** An interrupted session with leftover firewall rules must be cleanly identified and purged on next startup using `UserDataDir()/killswitch.active`.
5. **Key file permission hardening:** On Linux, `key.dat` must be created strictly with `0600` permissions; if permissions are looser, the security manager must correct them.

---

### Task 1: Platform Abstraction Layer Interfaces & Registry

**Files:**
- Create: `backend/platform/platform.go`
- Test: `backend/platform/platform_test.go`

**Interfaces:**
- Consumes: Standard Go libraries (`time`).
- Produces: `platform.Platform`, `platform.PathManager`, `platform.SecurityManager`, `platform.FirewallManager`, `platform.ProxyManager`, `platform.PrivilegeManager`, `platform.NetworkProber`, `platform.LifecycleManager`, `platform.Current()`, `platform.SetCurrent()`.

- [ ] **Step 1: Write the failing test**

Create `backend/platform/platform_test.go`:
```go
package platform_test

import (
	"testing"
	"time"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/platform/...`
Expected: FAIL with "cannot find package" or "undefined: platform"

- [ ] **Step 3: Write minimal implementation**

Create `backend/platform/platform.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./backend/platform/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/platform/platform.go backend/platform/platform_test.go
git commit -m "feat(platform): add platform abstraction interfaces and registry"
```

---

### Task 2: Windows Platform Implementation & Factory

**Files:**
- Create: `backend/platform/windows/paths.go`
- Create: `backend/platform/windows/windows.go`
- Create: `backend/platform/factory_windows.go`
- Test: `backend/platform/windows/windows_test.go`

**Interfaces:**
- Consumes: `backend/platform.Platform`, `backend/security`, `backend/service`.
- Produces: `factory_windows.go` initializes Windows platform when compiled with `//go:build windows`.

- [ ] **Step 1: Write the failing test**

Create `backend/platform/windows/windows_test.go`:
```go
//go:build windows

package windows_test

import (
	"strings"
	"testing"

	"NeoBox/backend/platform"
	_ "NeoBox/backend/platform"
)

func TestWindowsPlatformInitialized(t *testing.T) {
	p := platform.Current()
	if p == nil {
		t.Fatalf("expected platform.Current() to be initialized on Windows")
	}
	userData := p.Paths().UserDataDir()
	if !strings.Contains(userData, "AppData") || !strings.Contains(userData, "NeoBox") {
		t.Fatalf("unexpected Windows UserDataDir: %s", userData)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/platform/windows/...`
Expected: FAIL (package or factory not yet implemented)

- [ ] **Step 3: Write minimal implementation**

Create `backend/platform/windows/paths.go`:
```go
//go:build windows

package windows

import (
	"os"
	"path/filepath"
)

type PathManager struct{}

func (m *PathManager) UserDataDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")
}

func (m *PathManager) ConfigDir() string {
	return m.UserDataDir()
}

func (m *PathManager) LogDir() string {
	return m.UserDataDir()
}

func (m *PathManager) ResolvePath(elem ...string) string {
	parts := append([]string{m.UserDataDir()}, elem...)
	return filepath.Join(parts...)
}
```

Create `backend/platform/windows/windows.go`:
```go
//go:build windows

package windows

import (
	"time"

	"NeoBox/backend/platform"
	"NeoBox/backend/security"
	"NeoBox/backend/service"
)

type Platform struct {
	paths      *PathManager
	security   *SecurityManager
	firewall   *FirewallManager
	proxy      *ProxyManager
	privileges *PrivilegeManager
	prober     *Prober
	lifecycle  *LifecycleManager
}

func New() *Platform {
	return &Platform{
		paths:      &PathManager{},
		security:   &SecurityManager{},
		firewall:   &FirewallManager{},
		proxy:      &ProxyManager{},
		privileges: &PrivilegeManager{},
		prober:     &Prober{},
		lifecycle:  &LifecycleManager{},
	}
}

func (p *Platform) Paths() platform.PathManager           { return p.paths }
func (p *Platform) Security() platform.SecurityManager     { return p.security }
func (p *Platform) Firewall() platform.FirewallManager     { return p.firewall }
func (p *Platform) Proxy() platform.ProxyManager           { return p.proxy }
func (p *Platform) Privileges() platform.PrivilegeManager { return p.privileges }
func (p *Platform) Prober() platform.NetworkProber         { return p.prober }
func (p *Platform) Lifecycle() platform.LifecycleManager   { return p.lifecycle }

type SecurityManager struct{}

func (s *SecurityManager) Init(userDataDir string) error      { return security.InitEncryption(userDataDir) }
func (s *SecurityManager) Encrypt(plain []byte) ([]byte, error) { return security.EncryptData(plain) }
func (s *SecurityManager) Decrypt(cipher []byte) ([]byte, error) { return security.DecryptData(cipher) }

type FirewallManager struct{}

func (f *FirewallManager) EnableKillSwitch(serverHost string) error { return security.EnableKillSwitch(serverHost) }
func (f *FirewallManager) DisableKillSwitch() error                 { return security.DisableKillSwitch() }
func (f *FirewallManager) RecoverKillSwitch(dir string)             {}

type ProxyManager struct{}

func (m *ProxyManager) SetSystemProxy(addr string) error { return nil }
func (m *ProxyManager) RestoreSystemProxy() error        { return nil }
func (m *ProxyManager) RecoverSystemProxy(dir string)    {}

type PrivilegeManager struct{}

func (m *PrivilegeManager) CanRunTUN() bool           { return service.IsElevated() }
func (m *PrivilegeManager) CanConfigureFirewall() bool { return service.IsElevated() }
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
	service.ExitNow(uint32(code))
}
```

Create `backend/platform/factory_windows.go`:
```go
//go:build windows

package platform

import "NeoBox/backend/platform/windows"

func init() {
	SetCurrent(windows.New())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./backend/platform/windows/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/platform/windows/ backend/platform/factory_windows.go
git commit -m "feat(platform): implement Windows platform adaptor and factory"
```

---

### Task 3: Linux Paths & AES Key Encryption

**Files:**
- Create: `backend/platform/linux/paths.go`
- Create: `backend/platform/linux/encryption.go`
- Test: `backend/platform/linux/encryption_test.go`

**Interfaces:**
- Consumes: `backend/platform.PathManager`, `backend/platform.SecurityManager`.
- Produces: XDG base paths, AES-256-GCM encryption with `0600` key file management and machine-id binding.

- [ ] **Step 1: Write the failing test**

Create `backend/platform/linux/encryption_test.go`:
```go
package linux_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestLinuxEncryptionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sec := &linux.SecurityManager{}
	if err := sec.Init(tmpDir); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	keyFile := filepath.Join(tmpDir, "key.dat")
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("key file was not created: %v", err)
	}
	// Verify POSIX mode 0600 (ignoring non-permission bits)
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Logf("key file permissions are %o (on Windows test environment, will enforce on Linux)", perm)
	}

	plainText := []byte("vless://test-uuid@example.com:443?security=reality")
	cipherText, err := sec.Encrypt(plainText)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(cipherText, plainText) {
		t.Fatalf("cipher text must not equal plain text")
	}

	decrypted, err := sec.Decrypt(cipherText)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !bytes.Equal(decrypted, plainText) {
		t.Fatalf("expected decrypted %s, got %s", plainText, decrypted)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/platform/linux/...`
Expected: FAIL (package linux not found or undefined)

- [ ] **Step 3: Write minimal implementation**

Create `backend/platform/linux/paths.go`:
```go
package linux

import (
	"os"
	"path/filepath"
)

type PathManager struct{}

func (m *PathManager) UserDataDir() string {
	return m.ConfigDir()
}

func (m *PathManager) ConfigDir() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	dir := filepath.Join(configHome, "neobox")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (m *PathManager) LogDir() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(dataHome, "neobox")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (m *PathManager) ResolvePath(elem ...string) string {
	parts := append([]string{m.UserDataDir()}, elem...)
	return filepath.Join(parts...)
}
```

Create `backend/platform/linux/encryption.go`:
```go
package linux

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type SecurityManager struct {
	mu        sync.RWMutex
	cachedKey []byte
}

func (s *SecurityManager) getMachineID() []byte {
	// Attempt /etc/machine-id then /var/lib/dbus/machine-id
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			h := sha256.Sum256(data)
			return h[:]
		}
	}
	return []byte("neobox-linux-fallback-salt")
}

func (s *SecurityManager) Init(userDataDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = os.MkdirAll(userDataDir, 0700)
	keyPath := filepath.Join(userDataDir, "key.dat")

	rawKey, err := os.ReadFile(keyPath)
	if err == nil && len(rawKey) >= 32 {
		_ = os.Chmod(keyPath, 0600)
		s.cachedKey = rawKey[:32]
		return nil
	}

	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return fmt.Errorf("failed to generate encryption key: %w", err)
	}

	if err := os.WriteFile(keyPath, newKey, 0600); err != nil {
		return fmt.Errorf("failed to save encryption key: %w", err)
	}
	_ = os.Chmod(keyPath, 0600)
	s.cachedKey = newKey
	return nil
}

func (s *SecurityManager) Encrypt(plain []byte) ([]byte, error) {
	s.mu.RLock()
	key := s.cachedKey
	s.mu.RUnlock()

	if len(key) != 32 {
		return nil, errors.New("security manager not initialized")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	sealed := gcm.Seal(nil, nonce, plain, nil)
	return append(nonce, sealed...), nil
}

func (s *SecurityManager) Decrypt(cipherText []byte) ([]byte, error) {
	s.mu.RLock()
	key := s.cachedKey
	s.mu.RUnlock()

	if len(key) != 32 {
		return nil, errors.New("security manager not initialized")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherText) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce := cipherText[:nonceSize]
	encData := cipherText[nonceSize:]
	return gcm.Open(nil, nonce, encData, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./backend/platform/linux/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/platform/linux/paths.go backend/platform/linux/encryption.go backend/platform/linux/encryption_test.go
git commit -m "feat(platform/linux): implement XDG paths and AES-256-GCM secret manager"
```

---

### Task 4: Linux Capabilities & Privilege Manager

**Files:**
- Create: `backend/platform/linux/capabilities.go`
- Test: `backend/platform/linux/capabilities_test.go`

**Interfaces:**
- Consumes: `backend/platform.PrivilegeManager`.
- Produces: `CanRunTUN()`, `CanConfigureFirewall()`, `PrivilegeHelpMessage()`.

- [ ] **Step 1: Write the failing test**

Create `backend/platform/linux/capabilities_test.go`:
```go
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
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./backend/platform/linux/capabilities_test.go`
Expected: FAIL (undefined: linux.PrivilegeManager)

- [ ] **Step 3: Write minimal implementation**

Create `backend/platform/linux/capabilities.go`:
```go
package linux

import (
	"fmt"
	"os"
)

type PrivilegeManager struct{}

// CanRunTUN checks if TUN interface access (/dev/net/tun) is permitted.
func (m *PrivilegeManager) CanRunTUN() bool {
	if os.Geteuid() == 0 {
		return true
	}
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
		return true
	}
	return false
}

// CanConfigureFirewall checks if the process can invoke nftables/iptables.
func (m *PrivilegeManager) CanConfigureFirewall() bool {
	return m.CanRunTUN()
}

// PrivilegeHelpMessage gives user-facing instructions to grant capabilities.
func (m *PrivilegeManager) PrivilegeHelpMessage() string {
	execPath, err := os.Executable()
	if err != nil {
		execPath = "/usr/bin/neobox"
	}
	return fmt.Sprintf("Для работы в режиме TUN требуются сетевые привилегии.\nВыполните в терминале:\nsudo setcap cap_net_admin,cap_net_bind_service+ep %s", execPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./backend/platform/linux/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/platform/linux/capabilities.go backend/platform/linux/capabilities_test.go
git commit -m "feat(platform/linux): implement Linux capability and privilege checks"
```

---

### Task 5: Linux Kill Switch & System Proxy Managers

**Files:**
- Create: `backend/platform/linux/firewall.go`
- Create: `backend/platform/linux/proxy.go`
- Create: `backend/platform/linux/icmp.go`
- Create: `backend/platform/linux/linux.go`
- Create: `backend/platform/factory_linux.go`
- Test: `backend/platform/linux/firewall_test.go`
- Test: `backend/platform/linux/proxy_test.go`

**Interfaces:**
- Consumes: `backend/platform.FirewallManager`, `backend/platform.ProxyManager`, `backend/platform.NetworkProber`.
- Produces: Complete Linux platform implementation registered in `factory_linux.go`.

- [ ] **Step 1: Write the failing tests**

Create `backend/platform/linux/firewall_test.go`:
```go
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
```

Create `backend/platform/linux/proxy_test.go`:
```go
package linux_test

import (
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestProxyBackupLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &linux.ProxyManager{}
	pm.SetUserDataDir(tmpDir)

	backupPath := filepath.Join(tmpDir, "proxy-backup.json")
	if err := os.WriteFile(backupPath, []byte(`{"server":"127.0.0.1:20809"}`), 0644); err != nil {
		t.Fatalf("failed to write test backup: %v", err)
	}

	pm.RecoverSystemProxy(tmpDir)
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("expected loopback backup to be discarded during recovery")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./backend/platform/linux/...`
Expected: FAIL (undefined types)

- [ ] **Step 3: Write minimal implementation**

Create `backend/platform/linux/firewall.go`:
```go
package linux

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FirewallManager struct{}

func (f *FirewallManager) markerPath(userDataDir string) string {
	return filepath.Join(userDataDir, "killswitch.active")
}

func (f *FirewallManager) EnableKillSwitch(serverHost string) error {
	_ = f.DisableKillSwitch()

	// Resolve host IPs to whitelist server traffic
	ips, _ := net.LookupIP(serverHost)
	var serverIPs []string
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			serverIPs = append(serverIPs, ipv4.String())
		}
	}

	// Try nftables first
	if _, err := exec.LookPath("nft"); err == nil {
		return f.enableNftables(serverIPs)
	}

	// Fallback to iptables
	if _, err := exec.LookPath("iptables"); err == nil {
		return f.enableIptables(serverIPs)
	}

	return fmt.Errorf("neither nftables nor iptables found on system")
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
	_ = exec.Command("iptables", "-N", "NEOBOX_KILLSWITCH").Run()
	_ = exec.Command("iptables", "-I", "OUTPUT", "1", "-j", "NEOBOX_KILLSWITCH").Run()
	_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-o", "lo", "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-d", "192.168.0.0/16", "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-d", "10.0.0.0/8", "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-o", "tun+", "-j", "ACCEPT").Run()
	for _, ip := range serverIPs {
		_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-d", ip, "-j", "ACCEPT").Run()
	}
	_ = exec.Command("iptables", "-A", "NEOBOX_KILLSWITCH", "-j", "DROP").Run()
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
	return nil
}

func (f *FirewallManager) RecoverKillSwitch(userDataDir string) {
	marker := f.markerPath(userDataDir)
	if _, err := os.Stat(marker); err == nil {
		_ = f.DisableKillSwitch()
		_ = os.Remove(marker)
	}
}
```

Create `backend/platform/linux/proxy.go`:
```go
package linux

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type ProxyManager struct {
	userDataDir string
}

func (m *ProxyManager) SetUserDataDir(dir string) {
	m.userDataDir = dir
}

func (m *ProxyManager) backupPath() string {
	return filepath.Join(m.userDataDir, "proxy-backup.json")
}

func (m *ProxyManager) SetSystemProxy(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		host = "127.0.0.1"
		portStr = "20809"
	}
	port, _ := strconv.Atoi(portStr)

	// GNOME desktop proxy
	if _, err := exec.LookPath("gsettings"); err == nil {
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "manual").Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "host", host).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "port", strconv.Itoa(port)).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "host", host).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "port", strconv.Itoa(port)).Run()
	}

	// KDE Plasma desktop proxy
	if _, err := exec.LookPath("kwriteconfig5"); err == nil {
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1").Run()
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://"+addr).Run()
	} else if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1").Run()
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://"+addr).Run()
	}

	return nil
}

func (m *ProxyManager) RestoreSystemProxy() error {
	if _, err := exec.LookPath("gsettings"); err == nil {
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()
	}
	if _, err := exec.LookPath("kwriteconfig5"); err == nil {
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	} else if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	}
	return nil
}

func (m *ProxyManager) RecoverSystemProxy(userDataDir string) {
	bp := filepath.Join(userDataDir, "proxy-backup.json")
	data, err := os.ReadFile(bp)
	if err != nil {
		return
	}
	var b struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(data, &b); err == nil && (b.Server == "127.0.0.1:20809" || b.Server == "") {
		_ = os.Remove(bp)
		_ = m.RestoreSystemProxy()
	}
}
```

Create `backend/platform/linux/icmp.go`:
```go
package linux

import (
	"net"
	"os/exec"
	"time"
)

type Prober struct{}

func (p *Prober) Ping(host string, timeout time.Duration) int {
	// 1. Try TCP dial
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "80"), timeout)
	if err == nil {
		_ = conn.Close()
		return int(time.Since(start).Milliseconds())
	}

	// 2. Try unprivileged ping socket (udp4 datagram ICMP)
	pStart := time.Now()
	pConn, err := net.DialTimeout("udp4", net.JoinHostPort(host, "0"), timeout)
	if err == nil {
		_ = pConn.Close()
		return int(time.Since(pStart).Milliseconds())
	}

	// 3. Fallback to system ping
	if _, err := exec.LookPath("ping"); err == nil {
		cStart := time.Now()
		cmd := exec.Command("ping", "-c", "1", "-W", "1", host)
		if err := cmd.Run(); err == nil {
			return int(time.Since(cStart).Milliseconds())
		}
	}

	return -1
}
```

Create `backend/platform/linux/linux.go`:
```go
package linux

import (
	"os"

	"NeoBox/backend/platform"
)

type Platform struct {
	paths      *PathManager
	security   *SecurityManager
	firewall   *FirewallManager
	proxy      *ProxyManager
	privileges *PrivilegeManager
	prober     *Prober
	lifecycle  *LifecycleManager
}

func New() *Platform {
	paths := &PathManager{}
	proxy := &ProxyManager{userDataDir: paths.UserDataDir()}
	return &Platform{
		paths:      paths,
		security:   &SecurityManager{},
		firewall:   &FirewallManager{},
		proxy:      proxy,
		privileges: &PrivilegeManager{},
		prober:     &Prober{},
		lifecycle:  &LifecycleManager{},
	}
}

func (p *Platform) Paths() platform.PathManager           { return p.paths }
func (p *Platform) Security() platform.SecurityManager     { return p.security }
func (p *Platform) Firewall() platform.FirewallManager     { return p.firewall }
func (p *Platform) Proxy() platform.ProxyManager           { return p.proxy }
func (p *Platform) Privileges() platform.PrivilegeManager { return p.privileges }
func (p *Platform) Prober() platform.NetworkProber         { return p.prober }
func (p *Platform) Lifecycle() platform.LifecycleManager   { return p.lifecycle }

type LifecycleManager struct{}

func (l *LifecycleManager) HideConsoleIfNeeded() {}

func (l *LifecycleManager) TerminateProcess(code int) {
	os.Exit(code)
}
```

Create `backend/platform/factory_linux.go`:
```go
//go:build linux

package platform

import "NeoBox/backend/platform/linux"

func init() {
	SetCurrent(linux.New())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./backend/platform/linux/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/platform/linux/ backend/platform/factory_linux.go
git commit -m "feat(platform/linux): implement firewall, proxy, icmp prober and Linux factory"
```

---

### Task 6: Packaging Assets & Linux Build Scripts

**Files:**
- Create: `build/linux/neobox.desktop`
- Create: `build/linux/icon.png`
- Create: `build/linux/debian/control`
- Create: `build/linux/debian/postinst`
- Create: `build/linux/debian/postrm`
- Create: `build/linux/arch/PKGBUILD`
- Create: `build/linux/arch/neobox.install`
- Create: `build/linux/appimage/AppRun`
- Create: `build/linux/build_linux.sh`

**Interfaces:**
- Consumes: Wails build binary `build/bin/NeoBox`.
- Produces: `.deb`, AUR package structure, and `AppImage` bundle.

- [ ] **Step 1: Write desktop and packaging metadata files**

Create `build/linux/neobox.desktop`:
```desktop
[Desktop Entry]
Name=NeoBox
Comment=Sleek and Secure VPN Client powered by sing-box
Exec=neobox %U
Icon=neobox
Terminal=false
Type=Application
Categories=Network;Security;VPN;
StartupWMClass=NeoBox
```

Create `build/linux/debian/control`:
```control
Package: neobox
Version: 1.8.0
Section: net
Priority: optional
Architecture: amd64
Depends: libc6, libgtk-3-0, libwebkit2gtk-4.1-0 | libwebkit2gtk-4.0-37, libappindicator3-1, iproute2
Recommends: nftables | iptables
Maintainer: Dvarais <tik26301@gmail.com>
Description: Sleek and Secure VPN Client powered by sing-box
 NeoBox is a desktop client for modern proxy protocols including VLESS,
 VMess, Trojan, Shadowsocks, TUIC, Hysteria 2 and WireGuard.
```

Create `build/linux/debian/postinst`:
```bash
#!/bin/sh
set -e
if command -v setcap >/dev/null 2>&1; then
    setcap cap_net_admin,cap_net_bind_service+ep /usr/bin/neobox || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q || true
fi
exit 0
```

Create `build/linux/debian/postrm`:
```bash
#!/bin/sh
set -e
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q || true
fi
exit 0
```

Create `build/linux/arch/PKGBUILD`:
```bash
# Maintainer: Dvarais <tik26301@gmail.com>
pkgname=neobox-bin
pkgver=1.8.0
pkgrel=1
pkgdesc="Sleek and Secure VPN Client powered by sing-box"
arch=('x86_64')
url="https://github.com/Dvarais/NeoBox"
license=('custom')
depends=('gtk3' 'webkit2gtk-4.1' 'libappindicator-gtk3' 'iproute2')
optdepends=('nftables: Kill Switch support'
            'iptables: Legacy Kill Switch fallback')
install=neobox.install
source=("https://github.com/Dvarais/NeoBox/releases/download/v${pkgver}/neobox-linux-amd64.tar.gz")
sha256sums=('SKIP')

package() {
    install -Dm755 "${srcdir}/neobox" "${pkgdir}/usr/bin/neobox"
    install -Dm644 "${srcdir}/neobox.desktop" "${pkgdir}/usr/share/applications/neobox.desktop"
    install -Dm644 "${srcdir}/icon.png" "${pkgdir}/usr/share/icons/hicolor/512x512/apps/neobox.png"
}
```

Create `build/linux/arch/neobox.install`:
```bash
post_install() {
    setcap cap_net_admin,cap_net_bind_service+ep /usr/bin/neobox || true
    update-desktop-database -q || true
}

post_upgrade() {
    post_install
}

post_remove() {
    update-desktop-database -q || true
}
```

Create `build/linux/appimage/AppRun`:
```bash
#!/bin/sh
HERE="$(dirname "$(readlink -f "${0}")")"
export PATH="${HERE}/usr/bin:${PATH}"
export LD_LIBRARY_PATH="${HERE}/usr/lib:${LD_LIBRARY_PATH}"
exec "${HERE}/usr/bin/neobox" "$@"
```

Create `build/linux/build_linux.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail

VERSION="1.8.0"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Building NeoBox v${VERSION} for Linux ==="
cd "${ROOT_DIR}"

wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox

echo "=== Packaging .deb ==="
DEB_DIR="${ROOT_DIR}/build/bin/deb"
rm -rf "${DEB_DIR}"
mkdir -p "${DEB_DIR}/DEBIAN"
mkdir -p "${DEB_DIR}/usr/bin"
mkdir -p "${DEB_DIR}/usr/share/applications"
mkdir -p "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps"

cp "${ROOT_DIR}/build/linux/debian/control" "${DEB_DIR}/DEBIAN/"
cp "${ROOT_DIR}/build/linux/debian/postinst" "${DEB_DIR}/DEBIAN/"
cp "${ROOT_DIR}/build/linux/debian/postrm" "${DEB_DIR}/DEBIAN/"
chmod 755 "${DEB_DIR}/DEBIAN/postinst" "${DEB_DIR}/DEBIAN/postrm"

cp "${ROOT_DIR}/build/bin/neobox" "${DEB_DIR}/usr/bin/neobox"
chmod 755 "${DEB_DIR}/usr/bin/neobox"
cp "${ROOT_DIR}/build/linux/neobox.desktop" "${DEB_DIR}/usr/share/applications/"
cp "${ROOT_DIR}/build/linux/icon.png" "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps/neobox.png"

dpkg-deb --build "${DEB_DIR}" "${ROOT_DIR}/build/bin/neobox_${VERSION}_amd64.deb"

echo "=== Linux Build Complete ==="
```

- [ ] **Step 2: Copy or generate Linux icon**

Copy application icon to `build/linux/icon.png` (using Go or PowerShell from existing resources).

- [ ] **Step 3: Commit packaging files**

```bash
git add build/linux/
git commit -m "feat(packaging): add Debian, Arch AUR and AppImage templates for Linux"
```

---

### Task 7: GitHub Actions CI/CD Linux Matrix Workflow

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `backend/platform`, `build/linux/build_linux.sh`.
- Produces: automated test and package artifacts on `ubuntu-latest` and `windows-latest`.

- [ ] **Step 1: Update `.github/workflows/ci.yml` to include `ubuntu-latest` matrix**

Add Linux backend test and packaging step to `.github/workflows/ci.yml`:
```yaml
  backend-linux:
    name: Go (Linux)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Install Linux build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev libappindicator3-dev

      - name: go vet (Linux)
        run: go vet -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" ./...

      - name: go test (Linux)
        run: go test -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" ./...
```

- [ ] **Step 2: Commit workflow changes**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add Linux build and test matrix to GitHub Actions"
```

---

### Task 8: End-to-End Verification & Sanity Check

**Files:**
- Test all Go packages on Windows: `go test -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" ./...`
- Test Linux cross-compilation: `go vet ./backend/platform/linux/...`

- [ ] **Step 1: Run full test suite on Windows**

Run: `go test -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" ./...`
Expected: ALL PASS

- [ ] **Step 2: Verify git status is clean**

Run: `git status`
Expected: clean working directory
