package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/security"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	sclog "github.com/sagernet/sing-box/log"
	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// currentVersion is the application version. Update this before each release.
// FIX #14: Extracted from CheckUpdates into a package-level constant so it
// cannot be missed during release preparation.
const currentVersion = "1.5.8"

type TrayServerItem struct {
	Item *systray.MenuItem
	Link string
}

// AppService handles all backend operations exposed to the Wails frontend.
type AppService struct {
	coreManager        *core.CoreManager
	userDataDir        string
	wailsCtx           context.Context
	wailsCtxMu         sync.RWMutex // FIX #18: protects wailsCtx
	cancelMonitor      context.CancelFunc
	cancelAutoUpdate   context.CancelFunc // FIX #9: allows stopping the auto-update goroutine
	backupProxyServer  string
	backupProxyEnable  uint32
	hasProxyBackup     bool
	clashSecret        string // per-session random secret for Clash API auth

	windowVisible      bool
	mToggleItem        *systray.MenuItem
	mStatusItem        *systray.MenuItem
	trayServerItems    [50]*TrayServerItem
	mu                 sync.Mutex
}

type wailsLogWriter struct {
	mu  sync.RWMutex
	ctx context.Context
}

// FIX #18: WriteMessage now acquires a read-lock before accessing ctx.
// sing-box calls WriteMessage from its own goroutines, while SetContext is
// called from the Wails main goroutine — without synchronization this is a
// data race that the Go race detector reliably flags.
func (w *wailsLogWriter) WriteMessage(level uint8, message string) {
	w.mu.RLock()
	ctx := w.ctx
	w.mu.RUnlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "xray-log", message)
	}
}

// NewAppService creates a new AppService instance.
func NewAppService(cm *core.CoreManager, userDataDir string) *AppService {
	// Create user data directory if it doesn't exist
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		fmt.Printf("Error creating user data dir: %v\n", err)
	}
	return &AppService{
		coreManager: cm,
		userDataDir: userDataDir,
	}
}

// GetSettings reads and decrypts settings.json.
func (s *AppService) GetSettings() string {
	filePath := filepath.Join(s.userDataDir, "settings.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "{}"
	}

	encryptedData, err := os.ReadFile(filePath)
	if err != nil {
		return "{}"
	}

	decrypted, err := security.Decrypt(encryptedData)
	if err != nil {
		// Дешифровка не удалась — скорее всего DPAPI-ключ сессии изменился
		// после «Завершения работы». Удаляем повреждённый файл, чтобы
		// при следующем SaveSettings настройки записались корректно.
		_ = os.Remove(filePath)
		return "{}"
	}

	return string(decrypted)
}

// SaveSettings encrypts and saves settings.json.
//
// FIX #7: Removed the plaintext fallback on encryption failure. If Encrypt()
// fails, the function now returns false without writing anything to disk.
// Previously, a failed encryption silently stored settings in plaintext,
// creating a discrepancy between what the user expects (encrypted) and what
// is stored on disk (readable by anyone with filesystem access).
func (s *AppService) SaveSettings(settingsJSON string) bool {
	filePath := filepath.Join(s.userDataDir, "settings.json")

	// Apply autostart update if needed based on settings changes
	var settingsMap map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &settingsMap); err == nil {
		openAtLogin, _ := settingsMap["openAtLogin"].(bool)
		exePath, err := os.Executable()
		if err == nil {
			alreadyEnabled := security.IsAutostartEnabled("NeoBox")
			if openAtLogin && !alreadyEnabled {
				_ = security.SetupAutostart("NeoBox", exePath)
			} else if !openAtLogin && alreadyEnabled {
				_ = security.RemoveAutostart("NeoBox")
			}
		}
	}

	encrypted, err := security.Encrypt([]byte(settingsJSON))
	if err != nil {
		// Encryption failed — do NOT fall back to plaintext storage.
		// Return false so the caller knows the save did not succeed.
		fmt.Printf("[SaveSettings] encryption error: %v\n", err)
		return false
	}

	err = os.WriteFile(filePath, encrypted, 0600)
	return err == nil
}

type Subscription struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Links   []string `json:"links"`
	Loading bool     `json:"loading"`
}

// GetSubscriptions reads and decrypts subscriptions.json.
//
// FIX #8: GetSubscriptions is now a pure read — it no longer writes to disk.
// Previously, every call that found a "bootstrap-free-subs" entry would
// re-encrypt and overwrite subscriptions.json, creating an unintended
// write-on-read side effect. The cleanup is now done once via a dedicated
// helper purgeBootstrapSub() which is called only from SaveSubscriptions.
func (s *AppService) GetSubscriptions() string {
	filePath := filepath.Join(s.userDataDir, "subscriptions.json")
	var decryptedJSON string

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		decryptedJSON = "[]"
	} else {
		encryptedData, err := os.ReadFile(filePath)
		if err != nil {
			decryptedJSON = "[]"
		} else {
			decrypted, err := security.Decrypt(encryptedData)
			if err != nil {
				// Дешифровка не удалась — DPAPI-ключ сессии изменился
				// после «Завершения работы». Удаляем повреждённый файл.
				_ = os.Remove(filePath)
				decryptedJSON = "[]"
			} else {
				decryptedJSON = string(decrypted)
			}
		}
	}

	// Filter out the NeoBox Free bootstrap subscription in-memory only.
	// NOTE: we do NOT write back to disk here — see purgeBootstrapSub().
	var subs []Subscription
	if err := json.Unmarshal([]byte(decryptedJSON), &subs); err == nil {
		hasBootstrap := false
		cleanedSubs := subs[:0]
		for _, sub := range subs {
			if sub.ID == "bootstrap-free-subs" {
				hasBootstrap = true
			} else {
				cleanedSubs = append(cleanedSubs, sub)
			}
		}
		if hasBootstrap {
			if merged, err := json.Marshal(cleanedSubs); err == nil {
				decryptedJSON = string(merged)
			}
		}
	}

	return decryptedJSON
}

// SaveSubscriptions encrypts and saves subscriptions.json.
// It also purges the bootstrap subscription from disk if present.
func (s *AppService) SaveSubscriptions(subsJSON string) bool {
	filePath := filepath.Join(s.userDataDir, "subscriptions.json")

	// FIX #8: Purge bootstrap sub from the JSON being saved rather than doing it
	// on every GetSubscriptions() read. This ensures the file is cleaned up
	// exactly once and not re-written on every read call.
	subsJSON = purgeBootstrapSub(subsJSON)

	encrypted, err := security.Encrypt([]byte(subsJSON))
	var writeErr error
	if err != nil {
		writeErr = os.WriteFile(filePath, []byte(subsJSON), 0600)
	} else {
		writeErr = os.WriteFile(filePath, encrypted, 0600)
	}

	if writeErr == nil {
		s.RebuildTrayServers()
		return true
	}
	return false
}

// purgeBootstrapSub removes the "bootstrap-free-subs" entry from a JSON subscription list.
func purgeBootstrapSub(subsJSON string) string {
	var subs []Subscription
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return subsJSON
	}
	cleaned := subs[:0]
	for _, sub := range subs {
		if sub.ID != "bootstrap-free-subs" {
			cleaned = append(cleaned, sub)
		}
	}
	if len(cleaned) == len(subs) {
		return subsJSON // nothing removed
	}
	if merged, err := json.Marshal(cleaned); err == nil {
		return string(merged)
	}
	return subsJSON
}

// StartXray parses the selected proxy URL and runs sing-box.
// NOTE: settingsJSON is kept for API compatibility but is intentionally ignored —
// settings are always read fresh from disk to prevent stale/empty frontend state
// (e.g., after a DPAPI key change) from launching VPN with wrong configuration.
func (s *AppService) StartXray(link string, _ string, useSystemProxy bool) map[string]interface{} {
	response := map[string]interface{}{"success": false}

	// 1. Read settings directly from disk (authoritative source)
	var settings core.Settings
	if err := json.Unmarshal([]byte(s.GetSettings()), &settings); err != nil {
		response["error"] = fmt.Sprintf("Failed to parse settings: %v", err)
		return response
	}

	// 1b. Check for admin privileges if TUN mode is enabled
	if settings.TunMode && !s.CheckAdmin() {
		response["error"] = "admin_required"
		return response
	}

	// 1c. Verify the mixed proxy port is available before attempting to start.
	// If port 20809 is already bound by another process, sing-box will fail with a
	// cryptic error. We give a clear message here instead.
	if ln, err := net.Listen("tcp", "127.0.0.1:20809"); err != nil {
		response["error"] = "Порт 20809 уже занят другим процессом. Закройте конфликтующее приложение и попробуйте снова."
		return response
	} else {
		_ = ln.Close()
	}

	// 2. Parse proxy URL
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		response["error"] = fmt.Sprintf("Failed to parse proxy link: %v", err)
		return response
	}

	// 3. Generate configuration
	// Generate a fresh per-session Clash API secret to prevent other local
	// processes from controlling the VPN core via the unauthenticated API.
	secret := generateClashSecret()
	s.mu.Lock()
	s.clashSecret = secret
	s.mu.Unlock()

	cachePath := filepath.Join(s.userDataDir, "cache.db")
	config, err := core.GenerateConfig(outbound, settings, useSystemProxy, cachePath, secret)
	if err != nil {
		response["error"] = fmt.Sprintf("Failed to generate configuration: %v", err)
		return response
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		response["error"] = fmt.Sprintf("Failed to serialize configuration: %v", err)
		return response
	}

	// 4. Start core manager
	// FIX #18: Read wailsCtx under the mutex to avoid data race with SetContext.
	var logWriter sclog.PlatformWriter
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx != nil {
		logWriter = &wailsLogWriter{ctx: wCtx}
	}
	if err := s.coreManager.Start(string(configBytes), logWriter); err != nil {
		response["error"] = fmt.Sprintf("Failed to start sing-box: %v", err)
		return response
	}

	// 4b. Enable Firewall Kill Switch if requested in settings
	if settings.KillSwitch {
		serverIP, _ := outbound["server"].(string)
		_ = security.EnableKillSwitch(serverIP)
	}

	// 5. Update system proxy registry settings if requested (and not in TUN mode)
	if useSystemProxy && !settings.TunMode {
		s.SetSystemProxy(true)
	} else {
		s.SetSystemProxy(false)
	}

	// Start background traffic monitoring
	if s.cancelMonitor != nil {
		s.cancelMonitor()
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	s.cancelMonitor = cancel
	go s.startTrafficMonitor(monitorCtx)

	s.UpdateTrayStatus(fmt.Sprintf("Статус: Подключено (%s)", parseServerNameFromLink(link)))
	// Notify user via Windows toast when connected (window may be hidden in tray)
	go sendToast("✅ NeoBox VPN", "Подключено к серверу: "+parseServerNameFromLink(link))

	response["success"] = true
	return response
}

// StopXray stops sing-box and disables system proxy settings.
func (s *AppService) StopXray() map[string]interface{} {
	response := map[string]interface{}{"success": false}
	
	s.SetSystemProxy(false)
	_ = security.DisableKillSwitch() // Disable firewall rules when disconnecting

	if s.cancelMonitor != nil {
		s.cancelMonitor()
		s.cancelMonitor = nil
	}

	if err := s.coreManager.Stop(); err != nil {
		response["error"] = err.Error()
		return response
	}

	s.wailsCtxMu.RLock()
	wCtxStop := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtxStop != nil {
		wailsruntime.EventsEmit(wCtxStop, "xray-stopped", nil)
	}

	s.UpdateTrayStatus("Статус: Отключено")
	// Notify user via Windows toast on disconnect
	go sendToast("❌ NeoBox VPN", "Соединение разорвано")

	response["success"] = true
	return response
}

// RestartXray restarts the VPN core without disturbing the system proxy backup.
// Unlike calling StopXray + StartXray separately, this preserves the proxy backup
// state so the user's original proxy settings are correctly restored on final disconnect.
func (s *AppService) RestartXray(link string, settingsJSON string, useSystemProxy bool) map[string]interface{} {
	// Stop the core and traffic monitor only — do NOT touch system proxy or kill switch.
	if s.cancelMonitor != nil {
		s.cancelMonitor()
		s.cancelMonitor = nil
	}
	_ = s.coreManager.Stop()

	// Emit stopped event so UI knows the old session ended
	s.wailsCtxMu.RLock()
	wCtxRestart := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtxRestart != nil {
		wailsruntime.EventsEmit(wCtxRestart, "xray-stopped", nil)
	}

	// Start fresh session — proxy backup is still intact from the original StartXray call.
	return s.StartXray(link, settingsJSON, useSystemProxy)
}

// CheckAdmin checks if the application runs with administrative/elevated privileges.
func (s *AppService) CheckAdmin() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// RequestAdmin triggers self-relaunch with administrative privileges.
func (s *AppService) RequestAdmin() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}

	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exePath)
	dirPtr, _ := windows.UTF16PtrFromString(filepath.Dir(exePath))
	argsPtr, _ := windows.UTF16PtrFromString("")

	_ = windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL)
	os.Exit(0)
}

// SetSystemProxy modifies system registry to enable/disable system-wide proxy settings.
func (s *AppService) SetSystemProxy(enable bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		fmt.Printf("Registry error: %v\n", err)
		return
	}
	defer k.Close()

	if enable {
		// Back up pre-existing user proxy configuration if not already ours
		currentServer, _, err := k.GetStringValue("ProxyServer")
		if err == nil && currentServer != "127.0.0.1:20809" && currentServer != "" {
			currentEnable, _, err := k.GetIntegerValue("ProxyEnable")
			if err == nil {
				s.backupProxyServer = currentServer
				s.backupProxyEnable = uint32(currentEnable)
				s.hasProxyBackup = true
			}
		}

		_ = k.SetDWordValue("ProxyEnable", 1)
		_ = k.SetStringValue("ProxyServer", "127.0.0.1:20809")
	} else {
		_ = k.SetDWordValue("ProxyEnable", 0)
		if s.hasProxyBackup {
			_ = k.SetStringValue("ProxyServer", s.backupProxyServer)
			_ = k.SetDWordValue("ProxyEnable", s.backupProxyEnable)
			s.hasProxyBackup = false
			s.backupProxyServer = ""
			s.backupProxyEnable = 0
		}
	}

	// Notify system that Internet Settings have changed so that Edge/Chrome refresh immediately
	// using InternetSetOption.
	dllWinInet := windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption := dllWinInet.NewProc("InternetSetOptionW")
	if procInternetSetOption.Find() == nil {
		// Option flags: INTERNET_OPTION_SETTINGS_CHANGED (39) and INTERNET_OPTION_REFRESH (37)
		_, _, _ = procInternetSetOption.Call(0, 39, 0, 0)
		_, _, _ = procInternetSetOption.Call(0, 37, 0, 0)
	}
}

// PingServer measures TCP round-trip latency to the server host and port.
func (s *AppService) PingServer(link string) int {
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		return -1
	}

	server, _ := outbound["server"].(string)
	port, _ := outbound["server_port"].(int)

	if server == "" || port == 0 {
		return -1
	}

	// Use net.JoinHostPort so IPv6 addresses are correctly wrapped in brackets: [::1]:port
	address := net.JoinHostPort(server, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return -1
	}
	defer conn.Close()

	elapsed := time.Since(start)
	return int(elapsed.Milliseconds())
}

// FetchSubscription loads subscription contents from subscription URL.
func (s *AppService) FetchSubscription(url string) []string {
	links, err := core.FetchSubscription(url)
	if err != nil {
		return []string{}
	}
	return links
}

// ImportClipboard filters proxy links from raw clipboard string.
func (s *AppService) ImportClipboard(text string) []string {
	var links []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "vless://") || strings.HasPrefix(trimmed, "vmess://") ||
			strings.HasPrefix(trimmed, "ss://") || strings.HasPrefix(trimmed, "trojan://") ||
			strings.HasPrefix(trimmed, "tuic://") || strings.HasPrefix(trimmed, "hysteria2://") ||
			strings.HasPrefix(trimmed, "hy2://") {
			links = append(links, trimmed)
		}
	}
	return links
}

// CheckUpdates queries GitHub API to check if a new version is available.
func (s *AppService) CheckUpdates() map[string]interface{} {
	response := map[string]interface{}{"available": false}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/Dvarais/NeoBox/releases/latest", nil)
	if err != nil {
		return response
	}
	req.Header.Set("User-Agent", "NeoBox-App")

	resp, err := client.Do(req)
	if err != nil {
		return response
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return response
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return response
	}

	var releaseInfo map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &releaseInfo); err != nil {
		return response
	}

	latestTag, _ := releaseInfo["tag_name"].(string)
	latestVersion := strings.Replace(latestTag, "v", "", 1)
	// FIX #14: Use the package-level constant instead of a hardcoded inline string.
	if s.isNewer(latestVersion, currentVersion) {
		htmlURL, _ := releaseInfo["html_url"].(string)
		body, _ := releaseInfo["body"].(string)

		response["available"] = true
		response["version"] = latestVersion
		response["url"] = htmlURL
		response["body"] = body

		// Extract download URL for the Windows .exe installer
		if assets, ok := releaseInfo["assets"].([]interface{}); ok {
			for _, assetVal := range assets {
				if asset, ok := assetVal.(map[string]interface{}); ok {
					name, _ := asset["name"].(string)
					url, _ := asset["browser_download_url"].(string)
					if strings.HasSuffix(strings.ToLower(name), ".exe") {
						response["downloadUrl"] = url
						response["assetName"] = name
						break
					}
				}
			}
		}
	}

	return response
}

// DownloadAndInstallUpdate downloads the installer from the given URL,
// reporting progress to the frontend, and runs it upon completion.
func (s *AppService) DownloadAndInstallUpdate(downloadURL string) error {
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()

	if wCtx == nil {
		return fmt.Errorf("wails context is not initialized")
	}

	// Extract filename from download URL
	parts := strings.Split(downloadURL, "/")
	filename := "neobox_update.exe"
	if len(parts) > 0 {
		filename = parts[len(parts)-1]
	}

	tempDir := os.TempDir()
	installerPath := filepath.Join(tempDir, filename)

	// Start downloading in a background goroutine so we return immediately to the frontend,
	// allowing it to show the progress bar.
	go func() {
		err := s.performDownload(wCtx, downloadURL, installerPath)
		if err != nil {
			wailsruntime.EventsEmit(wCtx, "update-error", err.Error())
			return
		}

		// Download complete!
		wailsruntime.EventsEmit(wCtx, "update-complete", nil)

		// Wait a split second for frontend to process before starting installer
		time.Sleep(1 * time.Second)

		// Start installer asynchronously
		cmd := exec.Command(installerPath)
		err = cmd.Start()
		if err != nil {
			wailsruntime.EventsEmit(wCtx, "update-error", "Failed to start installer: "+err.Error())
			return
		}

		// Quit our application immediately so the installer can overwrite NeoBox.exe
		wailsruntime.Quit(wCtx)
	}()

	return nil
}

func (s *AppService) performDownload(ctx context.Context, url, destPath string) error {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NeoBox-App")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	totalSize := resp.ContentLength
	buffer := make([]byte, 32*1024)
	var downloaded int64
	var lastPercent int = -1

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			_, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)

			if totalSize > 0 {
				percentage := int(float64(downloaded) / float64(totalSize) * 100)
				if percentage != lastPercent {
					wailsruntime.EventsEmit(ctx, "update-progress", percentage)
					lastPercent = percentage
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	return nil
}

func (s *AppService) isNewer(latest, current string) bool {
	lParts := strings.Split(latest, ".")
	cParts := strings.Split(current, ".")

	for i := 0; i < len(lParts) || i < len(cParts); i++ {
		l := 0
		c := 0
		if i < len(lParts) {
			l, _ = strconv.Atoi(lParts[i])
		}
		if i < len(cParts) {
			c, _ = strconv.Atoi(cParts[i])
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

// SetContext sets the Wails application context.
// FIX #18: Protected by wailsCtxMu so concurrent reads in wailsLogWriter.WriteMessage are safe.
// Also registers the NeoBox AppID for Windows toast notifications.
func (s *AppService) SetContext(ctx context.Context) {
	s.wailsCtxMu.Lock()
	s.wailsCtx = ctx
	s.wailsCtxMu.Unlock()
	// Register toast AppID once after the app context is available.
	InitNotifications()
}

// startTrafficMonitor connects to sing-box clash_api /traffic endpoint
// and streams real-time upload and download speeds to the Wails frontend.
// The per-session clashSecret is sent as a Bearer token so only NeoBox
// can consume the Clash API (security improvement #1).
func (s *AppService) startTrafficMonitor(ctx context.Context) {
	// Give clash_api half a second to bind and boot up
	time.Sleep(500 * time.Millisecond)

	// Snapshot the current session secret (protected by mu)
	s.mu.Lock()
	secret := s.clashSecret
	s.mu.Unlock()

	client := &http.Client{Timeout: 0} // infinite timeout for stream
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:9097/traffic", nil)
	if err != nil {
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			var stats struct {
				Up   int64 `json:"up"`
				Down int64 `json:"down"`
			}
			if err := dec.Decode(&stats); err != nil {
				// Exit if stream is broken or closed
				return
			}

			// Emit stats to the Wails frontend
			s.wailsCtxMu.RLock()
			wCtx := s.wailsCtx
			s.wailsCtxMu.RUnlock()
			if wCtx != nil {
				wailsruntime.EventsEmit(wCtx, "traffic-stats", map[string]interface{}{
					"up":   stats.Up,
					"down": stats.Down,
				})
			}
		}
	}
}

// InitTray starts the system tray loop in a background goroutine.
func (s *AppService) InitTray(iconBytes []byte) {
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(iconBytes)
			systray.SetTitle("NeoBox")
			systray.SetTooltip("NeoBox VPN")

			s.mu.Lock()
			// Add read-only status header
			mStatus := systray.AddMenuItem("Статус: Отключено", "Текущий статус подключения")
			mStatus.Disable()
			s.mStatusItem = mStatus
			systray.AddSeparator()

			toggleText := "Открыть интерфейс"
			if s.windowVisible {
				toggleText = "Скрыть интерфейс"
			}
			mToggle := systray.AddMenuItem(toggleText, "Показать/Скрыть окно приложения")
			s.mToggleItem = mToggle

			mServers := systray.AddMenuItem("Выбрать сервер", "Выбрать сервер из подписок")

			// Initialize the 50 hidden items pool
			for i := 0; i < 50; i++ {
				subItem := mServers.AddSubMenuItem("", "")
				subItem.Hide()
				s.trayServerItems[i] = &TrayServerItem{Item: subItem}
			}
			s.mu.Unlock()

			// Initial servers list build
			s.RebuildTrayServers()

			systray.AddSeparator()

			mRestart := systray.AddMenuItem("Перезапустить VPN", "Перезапустить текущее VPN соединение")
			mDisconnect := systray.AddMenuItem("Отключиться", "Разорвать VPN соединение")

			systray.AddSeparator()
			mQuit := systray.AddMenuItem("Выход", "Закрыть NeoBox")

			// Start click listener goroutines for the 50 server items
			for i := 0; i < 50; i++ {
				go func(idx int) {
					for range s.trayServerItems[idx].Item.ClickedCh {
						s.mu.Lock()
						link := s.trayServerItems[idx].Link
						s.mu.Unlock()
						if link != "" {
							s.SelectAndConnectServer(link)
						}
					}
				}(i)
			}

			for {
				select {
				case <-mToggle.ClickedCh:
					s.mu.Lock()
					if s.windowVisible {
						if s.wailsCtx != nil {
							wailsruntime.WindowHide(s.wailsCtx)
						}
						mToggle.SetTitle("Открыть интерфейс")
						s.windowVisible = false
					} else {
						if s.wailsCtx != nil {
							wailsruntime.WindowShow(s.wailsCtx)
							wailsruntime.WindowUnminimise(s.wailsCtx)
							wailsruntime.EventsEmit(s.wailsCtx, "window-restored", nil)
						}
						mToggle.SetTitle("Скрыть интерфейс")
						s.windowVisible = true
					}
					s.mu.Unlock()

				case <-mRestart.ClickedCh:
					if s.wailsCtx != nil {
						wailsruntime.EventsEmit(s.wailsCtx, "tray-restart", nil)
					}

				case <-mDisconnect.ClickedCh:
					if s.wailsCtx != nil {
						wailsruntime.EventsEmit(s.wailsCtx, "tray-toggle-connection", nil)
					}

				case <-mQuit.ClickedCh:
					// Safe shutdown of VPN processes and proxy cleanup on exit
					_ = s.coreManager.Stop()
					s.SetSystemProxy(false)
					systray.Quit()
					if s.wailsCtx != nil {
						wailsruntime.Quit(s.wailsCtx)
					}
					return
				}
			}
		}, func() {})
	}()
}

// SetWindowVisible sets the initial window visibility state.
func (s *AppService) SetWindowVisible(visible bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowVisible = visible
}

// NotifyWindowHidden is called from the frontend when the window is hidden.
func (s *AppService) NotifyWindowHidden() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowVisible = false
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle("Открыть интерфейс")
	}
}

// NotifyWindowShown is called from the frontend when the window is shown.
func (s *AppService) NotifyWindowShown() {
	s.mu.Lock()
	s.windowVisible = true
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle("Скрыть интерфейс")
	}
	s.mu.Unlock()
	// Emit outside of lock to avoid holding mu while calling Wails runtime.
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx != nil {
		wailsruntime.EventsEmit(wCtx, "window-restored", nil)
	}
}

// UpdateTrayStatus updates the status header menu item in the system tray.
func (s *AppService) UpdateTrayStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mStatusItem != nil {
		s.mStatusItem.SetTitle(status)
	}
}

// SelectAndConnectServer notifies the frontend to connect to the specified proxy server.
func (s *AppService) SelectAndConnectServer(link string) {
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx == nil {
		return
	}

	// Show the window so they can see the connection progress
	wailsruntime.WindowShow(wCtx)
	wailsruntime.WindowUnminimise(wCtx)
	wailsruntime.EventsEmit(wCtx, "window-restored", nil)
	s.NotifyWindowShown()

	settingsJSON := s.GetSettings()
	var settings map[string]interface{}
	_ = json.Unmarshal([]byte(settingsJSON), &settings)

	useSystemProxy, _ := settings["systemProxy"].(bool)

	wailsruntime.EventsEmit(wCtx, "tray-start-reconnect", map[string]interface{}{
		"link":           link,
		"useSystemProxy": useSystemProxy,
	})
}

// RebuildTrayServers updates the system tray servers list from saved subscriptions.
func (s *AppService) RebuildTrayServers() {
	filePath := filepath.Join(s.userDataDir, "subscriptions.json")

	s.mu.Lock()
	defer s.mu.Unlock()

	hideAll := func() {
		for i := 0; i < 50; i++ {
			if s.trayServerItems[i] != nil {
				s.trayServerItems[i].Link = ""
				s.trayServerItems[i].Item.Hide()
			}
		}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		hideAll()
		return
	}

	encryptedData, err := os.ReadFile(filePath)
	if err != nil {
		hideAll()
		return
	}

	decrypted, err := security.Decrypt(encryptedData)
	if err != nil {
		decrypted = encryptedData
	}

	var subs []Subscription
	if err := json.Unmarshal(decrypted, &subs); err != nil {
		hideAll()
		return
	}

	type ServerInfo struct {
		Name string
		Link string
	}
	var servers []ServerInfo

	for _, sub := range subs {
		for _, link := range sub.Links {
			name := parseServerNameFromLink(link)
			servers = append(servers, ServerInfo{
				Name: fmt.Sprintf("[%s] %s", sub.Name, name),
				Link: link,
			})
		}
	}

	// Populate tray server items
	// FIX #15: Log a warning when there are more servers than the tray can hold.
	if len(servers) > 50 {
		fmt.Printf("[RebuildTrayServers] warning: %d servers found but tray only supports 50; extra servers will be hidden.\n", len(servers))
	}
	for i := 0; i < 50; i++ {
		if s.trayServerItems[i] == nil {
			continue
		}
		if i < len(servers) {
			s.trayServerItems[i].Link = servers[i].Link

			protocol := ""
			if strings.HasPrefix(servers[i].Link, "vless://") {
				protocol = "vless:"
			} else if strings.HasPrefix(servers[i].Link, "vmess://") {
				protocol = "vmess:"
			} else if strings.HasPrefix(servers[i].Link, "ss://") {
				protocol = "ss:"
			} else if strings.HasPrefix(servers[i].Link, "trojan://") {
				protocol = "trojan:"
			} else if strings.HasPrefix(servers[i].Link, "tuic://") {
				protocol = "tuic:"
			} else if strings.HasPrefix(servers[i].Link, "hysteria2://") || strings.HasPrefix(servers[i].Link, "hy2://") {
				protocol = "hy2:"
			}

			s.trayServerItems[i].Item.SetTitle(fmt.Sprintf("%s %s", protocol, servers[i].Name))
			s.trayServerItems[i].Item.Show()
		} else {
			s.trayServerItems[i].Link = ""
			s.trayServerItems[i].Item.Hide()
		}
	}
}

func parseServerNameFromLink(link string) string {
	sanitized := strings.TrimSpace(link)
	sanitized = strings.ReplaceAll(sanitized, " ", "%20")
	sanitized = strings.ReplaceAll(sanitized, "\t", "%09")
	u, err := url.Parse(sanitized)
	if err != nil {
		return "Unknown Server"
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	} else {
		name, _ = url.QueryUnescape(name)
	}
	return name
}

// StartAutoUpdateScheduler runs a background loop to update all subscriptions every 24 hours
// if the auto-update setting is enabled, and runs once immediately on startup.
//
// FIX #9: The goroutine now accepts a context so it can be stopped on application shutdown.
// The context is stored in s.cancelAutoUpdate and cancelled during OnShutdown via StopAutoUpdateScheduler.
func (s *AppService) StartAutoUpdateScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate() // stop any previous scheduler
	}
	s.cancelAutoUpdate = cancel
	s.mu.Unlock()

	go func() {
		// Wait 5 seconds after startup to let the app initialize
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}

		for {
			// Check if auto-update is enabled in settings
			settingsJSON := s.GetSettings()
			var settings map[string]interface{}
			_ = json.Unmarshal([]byte(settingsJSON), &settings)

			autoUpdate, _ := settings["autoUpdateSubs"].(bool)
			if autoUpdate {
				s.UpdateAllSubscriptions()
			}

			// Wait 24 hours or until shutdown
			select {
			case <-time.After(24 * time.Hour):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopAutoUpdateScheduler stops the background auto-update goroutine.
// Should be called during application shutdown.
func (s *AppService) StopAutoUpdateScheduler() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate()
		s.cancelAutoUpdate = nil
	}
}

// UpdateAllSubscriptions downloads the latest links for all subscriptions.
func (s *AppService) UpdateAllSubscriptions() {
	subsJSON := s.GetSubscriptions()
	var subs []map[string]interface{}
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return
	}

	updatedAny := false
	for _, sub := range subs {
		url, _ := sub["url"].(string)
		if url == "" {
			continue
		}

		links, err := core.FetchSubscription(url)
		if err == nil && len(links) > 0 {
			sub["links"] = links
			updatedAny = true
		}
	}

	if updatedAny {
		newSubsJSON, err := json.Marshal(subs)
		if err == nil {
			s.SaveSubscriptions(string(newSubsJSON))
			
			// Notify frontend that subscriptions have been updated
			s.wailsCtxMu.RLock()
			wCtxSubs := s.wailsCtx
			s.wailsCtxMu.RUnlock()
			if wCtxSubs != nil {
				wailsruntime.EventsEmit(wCtxSubs, "subscriptions-updated", nil)
			}
		}
	}
}

// generateClashSecret generates a cryptographically secure random 32-character hex secret
// to secure the Clash API endpoint.
func generateClashSecret() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to secure hardcoded string in the extremely unlikely event that
		// crypto/rand is completely broken.
		return "neobox-secure-fallback-clash-secret"
	}
	return hex.EncodeToString(bytes)
}

