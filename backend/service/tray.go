package service

import (
	"NeoBox/backend/core"
	"NeoBox/backend/i18n"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"fyne.io/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// System tray: the icon, its menus and the window-visibility state they reflect.

type TrayServerItem struct {
	Item *systray.MenuItem
	Link string
}

// waitForShellTrayReady blocks until the Explorer shell tray window exists or a
// timeout elapses. It is used before starting the systray loop so that the very
// first Shell_NotifyIcon(NIM_ADD) lands on a ready notification area — this is
// what makes the tray icon appear reliably during early autostart at logon
// (previously it silently failed and the icon only showed up on later redraws).
//
// If the shell is already running (normal interactive launch) this returns
// immediately. It only waits during cold autostart right after logon.
func waitForShellTrayReady() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	procFindWindowW := user32.NewProc("FindWindowW")

	shellTrayPtr, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(shellTrayPtr)))
		if hwnd != 0 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// InitTray starts the system tray loop in a background goroutine.
func (s *AppService) InitTray(iconBytes []byte) {
	go func() {
		// Save the icon to disk next to the executable if missing, so Windows toast notifications can load it.
		// Run inside the background goroutine to prevent blocking the main app startup thread.
		if exePath, err := os.Executable(); err == nil {
			iconPath := filepath.Join(filepath.Dir(exePath), "icon.ico")
			if _, err := os.Stat(iconPath); os.IsNotExist(err) {
				_ = os.WriteFile(iconPath, iconBytes, 0644)
			}
		}

		runtime.LockOSThread()
		// Wait for the Explorer shell (notification area) to be ready before
		// entering the systray loop. If Shell_NotifyIcon(NIM_ADD) runs before
		// the tray window exists — common during early autostart at logon —
		// the call silently fails and the icon never appears until the area is
		// redrawn later. waitForShellTray polls FindWindowW("Shell_TrayWnd").
		waitForShellTrayReady()
		s.trayMu.Lock()
		s.trayStarted = true
		s.trayMu.Unlock()
		systray.Run(func() {
			systray.SetIcon(iconBytes)
			systray.SetTitle("NeoBox")
			systray.SetTooltip("NeoBox VPN")

			s.trayMu.Lock()
			// Add read-only status header
			mStatus := systray.AddMenuItem(i18n.T(i18n.TrayStatusDisconnected), i18n.T(i18n.TrayStatusTooltip))
			mStatus.Disable()
			s.mStatusItem = mStatus
			systray.AddSeparator()

			toggleText := i18n.T(i18n.TrayShowWindow)
			if s.windowVisible {
				toggleText = i18n.T(i18n.TrayHideWindow)
			}
			mToggle := systray.AddMenuItem(toggleText, i18n.T(i18n.TrayToggleTooltip))
			s.mToggleItem = mToggle

			mServers := systray.AddMenuItem(i18n.T(i18n.TraySelectServer), i18n.T(i18n.TraySelectTooltip))
			s.mServersItem = mServers
			s.trayMu.Unlock()

			systray.AddSeparator()

			mRestart := systray.AddMenuItem(i18n.T(i18n.TrayRestart), i18n.T(i18n.TrayRestartTooltip))
			mDisconnect := systray.AddMenuItem(i18n.T(i18n.TrayDisconnect), i18n.T(i18n.TrayDisconnectTooltip))

			systray.AddSeparator()
			mQuit := systray.AddMenuItem(i18n.T(i18n.TrayQuit), i18n.T(i18n.TrayQuitTooltip))

			// Keep the remaining items so a language change can re-title them.
			s.trayMu.Lock()
			s.mRestartItem = mRestart
			s.mDisconnectItem = mDisconnect
			s.mQuitItem = mQuit
			s.trayMu.Unlock()

			// Initialize the 50 hidden items pool, load subscriptions, and setup click listeners
			// in a separate background goroutine. This allows the tray icon and basic options
			// to load instantly without waiting for 50 Win32 native calls and disk reads.
			go func() {
				s.trayMu.Lock()
				for i := 0; i < 50; i++ {
					subItem := mServers.AddSubMenuItem("", "")
					subItem.Hide()
					s.trayServerItems[i] = &TrayServerItem{Item: subItem}
				}
				s.trayMu.Unlock()

				// Initial servers list build
				s.RebuildTrayServers()

				// Start click listener goroutines for the 50 server items
				for i := 0; i < 50; i++ {
					go func(idx int) {
						for range s.trayServerItems[idx].Item.ClickedCh {
							s.trayMu.Lock()
							link := s.trayServerItems[idx].Link
							s.trayMu.Unlock()
							if link != "" {
								s.SelectAndConnectServer(link)
							}
						}
					}(i)
				}
			}()

			for {
				select {
				case <-mToggle.ClickedCh:
					// windowVisible lives under trayMu and wailsCtx under wailsCtxMu. Take
					// them separately and snapshot the context before calling into the Wails
					// runtime, so no lock is held across an external call.
					s.trayMu.Lock()
					visible := s.windowVisible
					if visible {
						mToggle.SetTitle(i18n.T(i18n.TrayShowWindow))
						s.windowVisible = false
					} else {
						mToggle.SetTitle(i18n.T(i18n.TrayHideWindow))
						s.windowVisible = true
					}
					s.trayMu.Unlock()

					s.wailsCtxMu.RLock()
					wCtxToggle := s.wailsCtx
					s.wailsCtxMu.RUnlock()
					if wCtxToggle != nil {
						if visible {
							wailsruntime.WindowHide(wCtxToggle)
							s.onWindowHidden()
						} else {
							s.BringToFront()
							wailsruntime.EventsEmit(wCtxToggle, "window-restored", nil)
							s.onWindowRestored()
						}
					}

				case <-mRestart.ClickedCh:
					s.wailsCtxMu.RLock()
					wCtxRestart := s.wailsCtx
					s.wailsCtxMu.RUnlock()
					if wCtxRestart != nil {
						wailsruntime.EventsEmit(wCtxRestart, "tray-restart", nil)
					}

				case <-mDisconnect.ClickedCh:
					s.wailsCtxMu.RLock()
					wCtxDisconnect := s.wailsCtx
					s.wailsCtxMu.RUnlock()
					if wCtxDisconnect != nil {
						wailsruntime.EventsEmit(wCtxDisconnect, "tray-toggle-connection", nil)
					}

				case <-mQuit.ClickedCh:
					s.Quit()
					s.wailsCtxMu.RLock()
					wCtxQuit := s.wailsCtx
					s.wailsCtxMu.RUnlock()
					if wCtxQuit != nil {
						wailsruntime.Quit(wCtxQuit)
					}
					return
				}
			}
		}, func() {})
	}()
}

// SetWindowVisible sets the initial window visibility state.
func (s *AppService) SetWindowVisible(visible bool) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	s.windowVisible = visible
}

// isWindowVisible reports whether the main window is currently on screen.
// While it is not, there is nobody to read live output, so the log and traffic
// streams stop pushing events into WebView2 entirely.
func (s *AppService) isWindowVisible() bool {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	return s.windowVisible
}

// onWindowRestored hands the frontend everything that accumulated while it was
// hidden: the buffered log lines, and one traffic sample carrying the session
// totals so the counters pick up where the traffic actually left off rather
// than where the last delivered event did.
//
// Callers must not hold trayMu — this reaches into other locks and the Wails
// runtime.
func (s *AppService) onWindowRestored() {
	s.flushLogStream()

	// Only when a session is actually running: the frontend reveals the
	// speedometer on the first traffic-stats event, and it has no business
	// appearing while disconnected.
	if !s.coreManager.IsRunning() {
		return
	}
	totalUp, totalDown := s.sessionTraffic()
	s.emitSafe("traffic-stats", map[string]interface{}{
		"up":        int64(0),
		"down":      int64(0),
		"totalUp":   totalUp,
		"totalDown": totalDown,
	})
}

// NotifyWindowHidden is called from the frontend when the window is hidden.
func (s *AppService) NotifyWindowHidden() {
	s.trayMu.Lock()
	s.windowVisible = false
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle(i18n.T(i18n.TrayShowWindow))
	}
	s.trayMu.Unlock()
	s.onWindowHidden()
}

// onWindowHidden tells the frontend to go idle. Wails hides the Win32 window
// without touching the WebView2 controller's visibility, so the page never sees
// a visibilitychange and would otherwise keep its timers and animations running
// against a window nobody can see.
//
// Callers must not hold trayMu.
func (s *AppService) onWindowHidden() {
	s.emitSafe("window-hidden", nil)
}

// NotifyWindowShown is called from the frontend when the window is shown.
func (s *AppService) NotifyWindowShown() {
	s.trayMu.Lock()
	s.windowVisible = true
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle(i18n.T(i18n.TrayHideWindow))
	}
	s.trayMu.Unlock()
	// Emit outside of lock to avoid holding mu while calling Wails runtime.
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx != nil {
		wailsruntime.EventsEmit(wCtx, "window-restored", nil)
	}
	s.onWindowRestored()
}

// BringToFront forces the application window to the foreground and focuses it.
func (s *AppService) BringToFront() {
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx != nil {
		wailsruntime.WindowShow(wCtx)
		wailsruntime.WindowUnminimise(wCtx)
		// Toggle AlwaysOnTop briefly to force window focus on Windows
		wailsruntime.WindowSetAlwaysOnTop(wCtx, true)
		wailsruntime.WindowSetAlwaysOnTop(wCtx, false)
	}
}

// setTrayStatus renders the status line from a message id, remembering the id
// and its arguments so the line can be re-rendered after a language change.
func (s *AppService) setTrayStatus(id string, args ...interface{}) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	s.trayStatusID = id
	s.trayStatusArgs = args
	if s.mStatusItem != nil {
		s.mStatusItem.SetTitle(i18n.T(id, args...))
	}
}

// applyLanguage switches the language used for backend-rendered text and
// re-titles the tray, which is built once at startup and would otherwise stay
// in the language the app happened to start in.
func (s *AppService) applyLanguage(code string) {
	if i18n.Language() == i18n.Lang(strings.ToUpper(strings.TrimSpace(code))) {
		return
	}
	i18n.SetLanguage(code)
	s.retranslateTray()
}

// retranslateTray re-titles every tray item in the current language.
func (s *AppService) retranslateTray() {
	s.trayMu.Lock()
	items := []struct {
		item *systray.MenuItem
		id   string
	}{
		{s.mServersItem, i18n.TraySelectServer},
		{s.mRestartItem, i18n.TrayRestart},
		{s.mDisconnectItem, i18n.TrayDisconnect},
		{s.mQuitItem, i18n.TrayQuit},
	}
	for _, it := range items {
		if it.item != nil {
			it.item.SetTitle(i18n.T(it.id))
		}
	}

	if s.mToggleItem != nil {
		if s.windowVisible {
			s.mToggleItem.SetTitle(i18n.T(i18n.TrayHideWindow))
		} else {
			s.mToggleItem.SetTitle(i18n.T(i18n.TrayShowWindow))
		}
	}

	if s.mStatusItem != nil && s.trayStatusID != "" {
		s.mStatusItem.SetTitle(i18n.T(s.trayStatusID, s.trayStatusArgs...))
	}
	s.trayMu.Unlock()

	// The server entries carry subscription names, not translated text, but the
	// list is rebuilt anyway so a language change leaves nothing stale.
	s.RebuildTrayServers()
}

// UpdateTrayStatus updates the status header menu item in the system tray.
func (s *AppService) UpdateTrayStatus(status string) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
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
	s.BringToFront()
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

// trayServer is one entry of the tray's server submenu.
type trayServer struct {
	Name string
	Link string
}

// trayProtocolLabels abbreviates a canonical protocol for the tray menu, where
// horizontal space is tight. An unknown protocol yields "", which simply leaves
// the entry unprefixed.
var trayProtocolLabels = map[string]string{
	"vless":     "vless:",
	"vmess":     "vmess:",
	"ss":        "ss:",
	"trojan":    "trojan:",
	"tuic":      "tuic:",
	"hysteria2": "hy2:",
	"hysteria":  "hy1:",
}

// RebuildTrayServers mirrors the saved subscriptions into the tray menu.
//
// The subscription file is read BEFORE the tray lock is taken, in two distinct
// steps. That ordering is load-bearing: SaveSubscriptions rebuilds the tray
// right after writing, so if the rebuild read the file while holding trayMu the
// two paths would acquire fileMu and trayMu in opposite orders and could
// deadlock. Reading first also keeps disk I/O out of a lock that is otherwise
// only held across fast Win32 menu calls.
func (s *AppService) RebuildTrayServers() {
	s.applyTrayServers(s.loadTrayServers())
}

// loadTrayServers reads the subscriptions and flattens them into menu entries.
// It takes fileMu and holds no other lock.
func (s *AppService) loadTrayServers() []trayServer {
	s.fileMu.Lock()
	data := s.readSubscriptionsLocked()
	s.fileMu.Unlock()

	if data == nil {
		return nil
	}

	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil
	}

	var servers []trayServer
	for _, sub := range subs {
		for _, link := range sub.Links {
			servers = append(servers, trayServer{
				Name: fmt.Sprintf("[%s] %s", sub.Name, parseServerNameFromLink(link)),
				Link: link,
			})
		}
	}
	return servers
}

// applyTrayServers writes the entries into the tray menu. It takes trayMu and
// reads nothing from disk.
func (s *AppService) applyTrayServers(servers []trayServer) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()

	// The menu is a fixed pool, so anything beyond it is silently unreachable.
	if len(servers) > 50 {
		fmt.Printf("[RebuildTrayServers] warning: %d servers found but tray only supports 50; extra servers will be hidden.\n", len(servers))
	}
	for i := 0; i < 50; i++ {
		if s.trayServerItems[i] == nil {
			continue
		}
		if i < len(servers) {
			s.trayServerItems[i].Link = servers[i].Link

			label := trayProtocolLabels[core.ProtocolOf(servers[i].Link)]
			s.trayServerItems[i].Item.SetTitle(fmt.Sprintf("%s %s", label, servers[i].Name))
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
