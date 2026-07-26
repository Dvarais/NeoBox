package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/core"
	"NeoBox/backend/storage"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows system proxy: pointing it at NeoBox and restoring what the user had.

// systemProxyAddr is the loopback endpoint NeoBox installs as the Windows
// system proxy. It must match the "mixed-in" inbound in the generated sing-box
// config, and it is the marker used to tell our own setting apart from one the
// user configured themselves.
const systemProxyAddr = core.ProxyListenAddr

// internetSettingsKey is the per-user registry location holding the WinINET
// proxy configuration that Edge, Chrome and most Windows apps follow.
const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// proxyBackup is the user's own system proxy configuration, captured before
// NeoBox pointed the setting at itself.
type proxyBackup struct {
	Server string `json:"server"`
	Enable uint32 `json:"enable"`
}

// proxyBackupPath is where the captured configuration is persisted.
func (s *AppService) proxyBackupPath() string {
	return filepath.Join(s.userDataDir, "proxy-backup.json")
}

// loadProxyBackup reads a backup left by a previous run into memory. It runs at
// construction so that a session which crashed while holding the system proxy
// can still have the user's configuration put back on the next start.
func (s *AppService) loadProxyBackup() {
	data, err := os.ReadFile(s.proxyBackupPath())
	if err != nil {
		return
	}
	var b proxyBackup
	if err := json.Unmarshal(data, &b); err != nil {
		fmt.Printf("[proxy] discarding unreadable backup: %v\n", err)
		_ = os.Remove(s.proxyBackupPath())
		return
	}
	// Never restore our own address as if it were the user's: doing so would
	// leave the machine pointing at a loopback port with nothing behind it.
	if b.Server == "" || b.Server == systemProxyAddr {
		_ = os.Remove(s.proxyBackupPath())
		return
	}
	s.backupProxyServer = b.Server
	s.backupProxyEnable = b.Enable
	s.hasProxyBackup = true
}

// SetSystemProxy points the Windows per-user proxy settings at NeoBox, or puts
// back whatever the user had configured before.
//
// The displaced configuration is persisted to disk rather than only held in
// memory. NeoBox can die without running its shutdown path — a panic in a
// sing-box goroutine, a forced update, Task Manager — and an in-memory backup
// dies with it, stranding the user's browsers on a loopback port that nothing
// is listening on any more.
//
// Disabling is deliberately conservative: with no backup to restore, the
// setting is cleared ONLY when it still points at NeoBox. Unconditionally
// writing ProxyEnable=0 is what silently switched off the proxy of every user
// who had one of their own, on every single start.
func (s *AppService) SetSystemProxy(enable bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		fmt.Printf("Registry error: %v\n", err)
		return
	}
	defer k.Close()

	if enable {
		s.captureProxyBackup(k)
		_ = k.SetDWordValue("ProxyEnable", 1)
		_ = k.SetStringValue("ProxyServer", systemProxyAddr)
	} else {
		s.restoreSystemProxy(k)
	}

	notifyInternetSettingsChanged()
}

// captureProxyBackup records the user's proxy configuration before it is
// replaced, both in memory and on disk.
func (s *AppService) captureProxyBackup(k registry.Key) {
	currentServer, _, err := k.GetStringValue("ProxyServer")
	if err != nil || currentServer == "" || currentServer == systemProxyAddr {
		// Nothing of the user's to preserve, or NeoBox is already installed.
		return
	}
	currentEnable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	// Keep the first capture. StartXray runs again on every watchdog reconnect,
	// and only the first one saw the value NeoBox actually displaced.
	if s.hasProxyBackup {
		return
	}
	s.backupProxyServer = currentServer
	s.backupProxyEnable = uint32(currentEnable)
	s.hasProxyBackup = true

	b := proxyBackup{Server: currentServer, Enable: uint32(currentEnable)}
	data, err := json.Marshal(b)
	if err != nil {
		return
	}
	if err := storage.WriteFile(s.proxyBackupPath(), data); err != nil {
		fmt.Printf("[proxy] warning: could not persist backup, a crash will not be recoverable: %v\n", err)
	}
}

// restoreSystemProxy puts the user's configuration back, or clears the setting
// if — and only if — it is still pointing at NeoBox.
func (s *AppService) restoreSystemProxy(k registry.Key) {
	s.stateMu.Lock()
	hasBackup := s.hasProxyBackup
	backupServer := s.backupProxyServer
	backupEnable := s.backupProxyEnable
	s.hasProxyBackup = false
	s.backupProxyServer = ""
	s.backupProxyEnable = 0
	s.stateMu.Unlock()

	if hasBackup {
		_ = k.SetStringValue("ProxyServer", backupServer)
		_ = k.SetDWordValue("ProxyEnable", backupEnable)
		_ = os.Remove(s.proxyBackupPath())
		return
	}

	// No backup exists, which means NeoBox never displaced anything. Touch the
	// setting only if it is still ours — a foreign proxy here belongs to the
	// user and must be left exactly as it is.
	currentServer, _, err := k.GetStringValue("ProxyServer")
	if err != nil || currentServer != systemProxyAddr {
		return
	}
	_ = k.SetDWordValue("ProxyEnable", 0)
	_ = k.SetStringValue("ProxyServer", "")
}

// notifyInternetSettingsChanged tells WinINET that the proxy configuration
// changed so Edge/Chrome pick it up immediately instead of on next launch.
func notifyInternetSettingsChanged() {
	dllWinInet := windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption := dllWinInet.NewProc("InternetSetOptionW")
	if procInternetSetOption.Find() == nil {
		// Option flags: INTERNET_OPTION_SETTINGS_CHANGED (39) and INTERNET_OPTION_REFRESH (37)
		_, _, _ = procInternetSetOption.Call(0, 39, 0, 0)
		_, _, _ = procInternetSetOption.Call(0, 37, 0, 0)
	}
}
