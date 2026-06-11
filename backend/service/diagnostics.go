package service

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"time"
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

	// ── 1. Wintun driver ────────────────────────────────────────────────────────
	exePath, _ := os.Executable()
	wintunPath := filepath.Join(filepath.Dir(exePath), "wintun.dll")
	if _, err := os.Stat(wintunPath); err == nil {
		items = append(items, DiagnosticItem{
			Name:    "Wintun драйвер",
			Status:  DiagOK,
			Message: "Найден (" + wintunPath + ")",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "Wintun драйвер",
			Status:  DiagError,
			Message: "wintun.dll не найден рядом с исполняемым файлом. TUN режим будет недоступен.",
		})
	}

	// ── 2. Administrator privileges ─────────────────────────────────────────────
	if s.CheckAdmin() {
		items = append(items, DiagnosticItem{
			Name:    "Права администратора",
			Status:  DiagOK,
			Message: "Запущен с правами администратора — TUN режим доступен",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "Права администратора",
			Status:  DiagWarning,
			Message: "Нет прав администратора. Системный прокси работает, TUN режим — нет.",
		})
	}

	// ── 3. Proxy port 20809 ──────────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    "Порт прокси (20809)",
			Status:  DiagOK,
			Message: "Порт занят текущим активным подключением VPN",
		})
	} else if ln, err := net.Listen("tcp", "127.0.0.1:20809"); err != nil {
		items = append(items, DiagnosticItem{
			Name:    "Порт прокси (20809)",
			Status:  DiagError,
			Message: "Порт 20809 занят другим процессом. VPN не запустится пока порт не освобождён.",
		})
	} else {
		_ = ln.Close()
		items = append(items, DiagnosticItem{
			Name:    "Порт прокси (20809)",
			Status:  DiagOK,
			Message: "Порт свободен",
		})
	}

	// ── 4. Clash API port 9097 ───────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    "Порт Clash API (9097)",
			Status:  DiagOK,
			Message: "Порт занят текущим активным подключением VPN (статистика работает)",
		})
	} else if ln, err := net.Listen("tcp", "127.0.0.1:9097"); err != nil {
		items = append(items, DiagnosticItem{
			Name:    "Порт Clash API (9097)",
			Status:  DiagWarning,
			Message: "Порт 9097 занят. Статистика трафика в реальном времени может не работать.",
		})
	} else {
		_ = ln.Close()
		items = append(items, DiagnosticItem{
			Name:    "Порт Clash API (9097)",
			Status:  DiagOK,
			Message: "Порт свободен",
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
			Name:    "Интернет",
			Status:  DiagError,
			Message: "Нет доступа к интернету (проверенные хосты недоступны). Проверьте сетевое соединение.",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "Интернет",
			Status:  DiagOK,
			Message: "Интернет доступен (успешное подключение к " + activeTarget + ")",
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
			Name:    "DNS резолвер",
			Status:  DiagWarning,
			Message: "Порты DNS недоступны. Возможны проблемы с подпиской и DNS-over-HTTPS.",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "DNS резолвер",
			Status:  DiagOK,
			Message: "DNS порт доступен (успешное подключение к " + activeDnsTarget + ")",
		})
	}

	// ── 7. VPN core status ───────────────────────────────────────────────────────
	if s.coreManager.IsRunning() {
		items = append(items, DiagnosticItem{
			Name:    "VPN ядро (sing-box)",
			Status:  DiagOK,
			Message: "Запущено и работает",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "VPN ядро (sing-box)",
			Status:  DiagWarning,
			Message: "VPN не подключён",
		})
	}


	result, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(result)
}
