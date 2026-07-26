package service

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/i18n"
	"NeoBox/backend/security"
)

// DiagnosticStatus is the outcome level of a single diagnostic check.
type DiagnosticStatus string

const (
	DiagOK      DiagnosticStatus = "ok"
	DiagWarning DiagnosticStatus = "warning"
	DiagError   DiagnosticStatus = "error"
)

// DiagnosticItem represents one diagnostic check with its result.
type DiagnosticItem struct {
	Name    string           `json:"name"`
	Status  DiagnosticStatus `json:"status"`
	Message string           `json:"message"`
}

// RunDiagnostics performs a series of pre-flight system checks and returns
// a JSON array of DiagnosticItem objects. The frontend uses this to render
// a diagnostics screen showing users exactly what is wrong before they try
// to connect.
//
// All checks are best-effort and independent — one failure does not stop the rest.
func (s *AppService) RunDiagnostics() string {
	var items []DiagnosticItem

	// Port numbers appear in the labels the user reads, so they are formatted from
	// the same constants the sockets bind rather than typed out a second time.
	proxyPortName := i18n.T(i18n.DiagProxyPortName, core.ProxyListenPort)
	clashPortName := i18n.T(i18n.DiagClashPortName, core.ClashAPIPort)

	// ── 1. Wintun driver ────────────────────────────────────────────────────────
	exePath, _ := os.Executable()
	wintunPath := filepath.Join(filepath.Dir(exePath), "wintun.dll")
	if _, err := os.Stat(wintunPath); err == nil {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagWintunName),
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagWintunFound, wintunPath),
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagWintunName),
			Status:  DiagError,
			Message: i18n.T(i18n.DiagWintunMissing),
		})
	}

	// ── 2. Administrator privileges ─────────────────────────────────────────────
	if s.CheckAdmin() {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagAdminName),
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagAdminYes),
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagAdminName),
			Status:  DiagWarning,
			Message: i18n.T(i18n.DiagAdminNo),
		})
	}

	// ── 3. Proxy port ────────────────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    proxyPortName,
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagPortInUseByVPN),
		})
	} else if ln, err := net.Listen("tcp", core.ProxyListenAddr); err != nil {
		items = append(items, DiagnosticItem{
			Name:    proxyPortName,
			Status:  DiagError,
			Message: i18n.T(i18n.DiagPortBusy, core.ProxyListenPort),
		})
	} else {
		_ = ln.Close()
		items = append(items, DiagnosticItem{
			Name:    proxyPortName,
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagPortFree),
		})
	}

	// ── 4. Clash API port ────────────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    clashPortName,
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagPortInUseByVPNAPI),
		})
	} else if ln, err := net.Listen("tcp", core.ClashAPIAddr); err != nil {
		items = append(items, DiagnosticItem{
			Name:    clashPortName,
			Status:  DiagWarning,
			Message: i18n.T(i18n.DiagClashPortBusy, core.ClashAPIPort),
		})
	} else {
		_ = ln.Close()
		items = append(items, DiagnosticItem{
			Name:    clashPortName,
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagPortFree),
		})
	}

	// ── 5. Internet connectivity ─────────────────────────────────────────────────
	// Check multiple hosts (Google, Cloudflare, Yandex) to prevent false warnings in regions
	// where certain IP addresses (like 1.1.1.1) are blocked by local ISPs.
	internetOk := false
	internetTargets := []string{"8.8.8.8:80", "1.1.1.1:80", "yandex.ru:80"}
	var activeTarget string
	for _, target := range internetTargets {
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			internetOk = true
			activeTarget = target
			break
		}
	}

	if !internetOk {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagInternetName),
			Status:  DiagError,
			Message: i18n.T(i18n.DiagInternetFail),
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagInternetName),
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagInternetOK, activeTarget),
		})
	}

	// ── 6. DNS resolution ────────────────────────────────────────────────────────
	// Check DNS port connectivity over TCP for multiple reliable DNS servers.
	dnsOk := false
	dnsTargets := []string{"8.8.8.8:53", "1.1.1.1:53", "77.88.8.8:53"}
	var activeDnsTarget string
	for _, target := range dnsTargets {
		dnsConn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err == nil {
			_ = dnsConn.Close()
			dnsOk = true
			activeDnsTarget = target
			break
		}
	}

	if !dnsOk {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagDNSName),
			Status:  DiagWarning,
			Message: i18n.T(i18n.DiagDNSFail),
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagDNSName),
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagDNSOK, activeDnsTarget),
		})
	}

	// ── 7. Leftover Kill Switch rules ────────────────────────────────────────────
	// Firewall rules outlive the process. If a previous session crashed while the
	// Kill Switch was armed and the rules could not be removed at startup (which
	// needs elevation), the machine has no internet and the cause is invisible.
	if security.KillSwitchRulesPresent() && !s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagKillSwitchName),
			Status:  DiagError,
			Message: i18n.T(i18n.DiagKillSwitchLeftover),
		})
	}

	// ── 8. VPN core status ───────────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagCoreName),
			Status:  DiagOK,
			Message: i18n.T(i18n.DiagCoreRunning),
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    i18n.T(i18n.DiagCoreName),
			Status:  DiagWarning,
			Message: i18n.T(i18n.DiagCoreStopped),
		})
	}

	result, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(result)
}
