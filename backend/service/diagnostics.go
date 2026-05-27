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
	if ln, err := net.Listen("tcp", "127.0.0.1:20809"); err != nil {
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
	if ln, err := net.Listen("tcp", "127.0.0.1:9097"); err != nil {
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
	conn, err := net.DialTimeout("tcp", "1.1.1.1:80", 3*time.Second)
	if err != nil {
		items = append(items, DiagnosticItem{
			Name:    "Интернет",
			Status:  DiagError,
			Message: "Нет доступа к интернету (1.1.1.1:80 недоступен). Проверьте сетевое соединение.",
		})
	} else {
		_ = conn.Close()
		items = append(items, DiagnosticItem{
			Name:    "Интернет",
			Status:  DiagOK,
			Message: "Интернет доступен",
		})
	}

	// ── 6. DNS resolution ────────────────────────────────────────────────────────
	if _, err := net.LookupHost("cloudflare.com"); err != nil {
		items = append(items, DiagnosticItem{
			Name:    "DNS резолвер",
			Status:  DiagWarning,
			Message: "DNS не работает. Возможны проблемы с подпиской и DNS-over-HTTPS.",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "DNS резолвер",
			Status:  DiagOK,
			Message: "DNS работает корректно",
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

	// ── 8. Encryption key ────────────────────────────────────────────────────────
	s.mu.Lock()
	dataDir := s.userDataDir
	s.mu.Unlock()
	keyPath := filepath.Join(dataDir, "key.bin")
	if info, err := os.Stat(keyPath); err != nil {
		items = append(items, DiagnosticItem{
			Name:    "Ключ шифрования",
			Status:  DiagError,
			Message: "key.bin не найден. Настройки и подписки не будут сохранены.",
		})
	} else if info.Size() != 32 {
		items = append(items, DiagnosticItem{
			Name:    "Ключ шифрования",
			Status:  DiagError,
			Message: "key.bin повреждён (неверный размер). Удалите его и перезапустите приложение.",
		})
	} else {
		items = append(items, DiagnosticItem{
			Name:    "Ключ шифрования",
			Status:  DiagOK,
			Message: "key.bin в порядке",
		})
	}

	result, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(result)
}
