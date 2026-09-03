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
	"unicode/utf16"
	"unsafe"

	"fyne.io/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// System tray: the icon, its menus and the window-visibility state they reflect.

// shellTrayWaitTimeout bounds the wait below. It only ever elapses when the
// shell genuinely is not coming up, so it is generous — but it is also exactly
// the cost of getting the lookup wrong, which is why the call underneath is
// worth reading carefully.
const shellTrayWaitTimeout = 30 * time.Second

// trayServerLimit caps how many server entries the menu will hold.
//
// ponytail: a flat ceiling, not paging. The old menu was a fixed pool of fifty
// items and a subscription with two hundred nodes lost three quarters of them
// with nothing on screen to say so. Three hundred is past any real
// subscription and still a menu Windows draws without complaint; past it the
// list ends with a disabled "…and N more" so the loss is at least visible.
// Paging or a search box is the upgrade, and only if someone actually hits it.
const trayServerLimit = 300

// waitForShellTrayReady blocks until the Explorer shell tray window exists or a
// timeout elapses. It is used before starting the systray loop so that the very
// first Shell_NotifyIcon(NIM_ADD) lands on a ready notification area — this is
// what makes the tray icon appear reliably during early autostart at logon
// (previously it silently failed and the icon only showed up on later redraws).
//
// If the shell is already running (normal interactive launch) this returns
// immediately. It only waits during cold autostart right after logon.
func waitForShellTrayReady() {
	shellTrayPtr, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	deadline := time.Now().Add(shellTrayWaitTimeout)
	for time.Now().Before(deadline) {
		// FindWindowW(lpClassName, lpWindowName) — the class comes FIRST.
		//
		// "Shell_TrayWnd" is the taskbar's window class; its title is empty. Passing
		// it in the second slot asked for a window whose *caption* reads
		// "Shell_TrayWnd", which nothing on Windows has, so this never matched and
		// the loop ran out its full timeout on every single launch. That is where
		// the half-minute between starting NeoBox and its icon appearing went — and
		// with startMinimized the tray icon is the only way into the app, so for
		// that half-minute there was nothing to click at all.
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(shellTrayPtr)), 0)
		if hwnd != 0 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("[tray] notification area did not appear within the timeout; adding the icon anyway")
}

// InitTray starts the system tray loop in a background goroutine. iconOn is the
// brand icon shown while a session is up; iconOff is its greyed twin, and the
// state of the connection is the one thing the tray can say without being
// opened at all.
func (s *AppService) InitTray(iconOn, iconOff []byte) {
	s.trayMu.Lock()
	s.trayIconOn, s.trayIconOff = iconOn, iconOff
	s.trayMu.Unlock()

	go func() {
		// Windows toasts load their icon from a file, so one has to exist on
		// disk. It goes into the user data directory, not next to the
		// executable: the installer puts NeoBox under Program Files, which an
		// unelevated process cannot write to — and NeoBox only elevates when TUN
		// or the Kill Switch ask it to. For everyone else the write failed
		// silently and their notifications came up with no icon at all.
		//
		// Run inside the background goroutine to keep startup off the disk.
		iconPath := filepath.Join(s.userDataDir, "icon.ico")
		if _, err := os.Stat(iconPath); os.IsNotExist(err) {
			_ = os.WriteFile(iconPath, iconOn, 0644)
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
			systray.SetIcon(iconOff)
			systray.SetTitle("NeoBox")
			systray.SetTooltip(i18n.T(i18n.TrayTipDisconnected))

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

			mProfiles := systray.AddMenuItem(i18n.T(i18n.TrayProfiles), i18n.T(i18n.TrayProfilesTooltip))
			// Скрыт, пока профилей нет: пустое подменю в трее — это пункт,
			// который открывается в ничто.
			mProfiles.Hide()
			s.mProfilesItem = mProfiles
			s.trayMu.Unlock()

			systray.AddSeparator()

			// Быстрые переключатели. Ради них раньше приходилось открывать окно
			// и идти в «Настройки» — за галкой, которую человек и так знает
			// наизусть.
			//
			// Ставит их фронтенд, как и профили: применение TUN или Kill Switch
			// — это пересборка конфигурации и переподключение, и второй
			// реализации того же самого на Go быть не должно. Отсюда уходит
			// только событие, а обратно состояние приходит через SaveSettings.
			mKillSwitch := systray.AddMenuItemCheckbox(i18n.T(i18n.TrayKillSwitch), i18n.T(i18n.TrayKillSwitchTooltip), false)
			mTun := systray.AddMenuItemCheckbox(i18n.T(i18n.TrayTunMode), i18n.T(i18n.TrayTunModeTooltip), false)
			mSysProxy := systray.AddMenuItemCheckbox(i18n.T(i18n.TraySystemProxy), i18n.T(i18n.TraySystemProxyTooltip), true)

			systray.AddSeparator()

			mRestart := systray.AddMenuItem(i18n.T(i18n.TrayRestart), i18n.T(i18n.TrayRestartTooltip))
			mDisconnect := systray.AddMenuItem(i18n.T(i18n.TrayDisconnect), i18n.T(i18n.TrayDisconnectTooltip))

			systray.AddSeparator()
			mQuit := systray.AddMenuItem(i18n.T(i18n.TrayQuit), i18n.T(i18n.TrayQuitTooltip))

			// Keep the remaining items so a language change can re-title them.
			s.trayMu.Lock()
			s.mKillSwitchItem = mKillSwitch
			s.mTunItem = mTun
			s.mSysProxyItem = mSysProxy
			s.mRestartItem = mRestart
			s.mDisconnectItem = mDisconnect
			s.mQuitItem = mQuit
			s.trayMu.Unlock()

			// The lists come off disk, so they are built after the icon and the
			// fixed items are already on screen.
			go func() {
				s.RebuildTrayServers()
				s.RebuildTrayProfiles()
				s.RebuildTrayToggles()
			}()

			for {
				select {
				case <-mToggle.ClickedCh:
					// isWindowVisible asks Windows, not the last thing the
					// frontend said. That is the whole point: every path that
					// moved the window without telling us used to leave this
					// menu offering the wrong half of the toggle.
					if s.isWindowVisible() {
						s.hideWindow()
					} else {
						s.BringToFront()
						s.emitSafe("window-restored", nil)
						s.onWindowRestored()
					}

				case <-mKillSwitch.ClickedCh:
					s.emitSafe("tray-toggle-setting", "killSwitch")

				case <-mTun.ClickedCh:
					s.emitSafe("tray-toggle-setting", "tunMode")

				case <-mSysProxy.ClickedCh:
					s.emitSafe("tray-toggle-setting", "systemProxy")

				case <-mRestart.ClickedCh:
					s.emitSafe("tray-restart", nil)

				case <-mDisconnect.ClickedCh:
					s.emitSafe("tray-toggle-connection", nil)

				case <-mQuit.ClickedCh:
					s.Quit()
					wCtxQuit := s.context()
					if wCtxQuit != nil {
						wailsruntime.Quit(wCtxQuit)
					}
					return
				}
			}
		}, func() {})
	}()
}

// ── Видимость окна ───────────────────────────────────────────────────────────

// user32 entry points. RegisterHotKey and friends live in hotkey.go and share
// the same lazy DLL.
var (
	procFindWindowW     = user32.NewProc("FindWindowW")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
)

// mainWindowClass is the window class Wails' winc creates the main window with
// (internal/frontend/desktop/windows/winc/form.go). Looking the window up by
// title alone matches anything else on the desktop with that caption — an
// Explorer folder named NeoBox is enough — so the class is what makes the
// lookup ours.
const mainWindowClass = "winc_Form"

// FindMainWindow returns NeoBox's own top-level window, or 0 before Wails has
// created it.
func FindMainWindow() uintptr {
	class, err := windows.UTF16PtrFromString(mainWindowClass)
	if err != nil {
		return 0
	}
	title, err := windows.UTF16PtrFromString("NeoBox")
	if err != nil {
		return 0
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)))
	return hwnd
}

// windowHandleLocked caches the main window handle. Callers must hold trayMu.
func (s *AppService) windowHandleLocked() uintptr {
	if s.mainHWND == 0 {
		s.mainHWND = FindMainWindow()
	}
	return s.mainHWND
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
//
// It asks Windows rather than trusting the flag the frontend maintains. That
// flag is a shadow copy of something the OS already knows, and every path that
// moved the window without updating it — a hotkey, a stray focus event, the
// taskbar — desynchronised the two. The flag survives only as the answer for
// the moments before the window exists.
func (s *AppService) isWindowVisible() bool {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()

	hwnd := s.windowHandleLocked()
	if hwnd == 0 {
		return s.windowVisible
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	// Свёрнутое окно формально visible, но смотреть в него некому — а решение
	// «слать ли события в WebView2» именно об этом.
	minimised, _, _ := procIsIconic.Call(hwnd)
	return minimised == 0
}

// hideWindow puts the window in the tray from the backend's own side (the tray
// toggle). The frontend's own close and minimise buttons come in through
// NotifyWindowHidden instead, having already hidden the window themselves.
func (s *AppService) hideWindow() {
	wCtx := s.context()
	if wCtx == nil {
		return
	}
	wailsruntime.WindowHide(wCtx)
	s.markWindowHidden()
	s.onWindowHidden()
}

// markWindowShown records that the window is on screen and re-titles the tray
// toggle to match. Callers must not hold trayMu.
func (s *AppService) markWindowShown() {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	s.windowVisible = true
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle(i18n.T(i18n.TrayHideWindow))
	}
}

// markWindowHidden is its mirror. Callers must not hold trayMu.
func (s *AppService) markWindowHidden() {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	s.windowVisible = false
	if s.mToggleItem != nil {
		s.mToggleItem.SetTitle(i18n.T(i18n.TrayShowWindow))
	}
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
	s.markWindowHidden()
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
	releaseIdleMemory()
}

// NotifyWindowShown is called from the frontend when the window is shown.
//
// Уведомление проверяется по Windows, а не принимается на слово. Страница судит
// о видимости по событию focus, а оно приходит с опозданием: закрытие окна
// крестиком идёт с задержкой на анимацию, и focus от того же самого нажатия
// успевает прийти уже после того, как окно спрятали. Go тогда снова считал окно
// видимым, и в трее над спрятанным окном оставалось «Скрыть интерфейс».
//
// Один источник этих запоздалых событий — слушатель mousedown, звавший
// bringToFront, — уже убран (см. renderer.ts возле init()). Но убирать их по
// одному бессмысленно: focus в странице никогда и не был утверждением о том,
// что окно на экране. Здесь стоит та же проверка, что и у пункта трея, —
// isWindowVisible спрашивает у системы, и спурьёзное уведомление отбрасывается
// независимо от того, что его породило.
func (s *AppService) NotifyWindowShown() {
	if !s.isWindowVisible() {
		return
	}
	s.markWindowShown()
	// Emit outside of lock to avoid holding mu while calling Wails runtime.
	s.emitSafe("window-restored", nil)
	s.onWindowRestored()
}

// BringToFront forces the application window to the foreground and focuses it.
//
// It is also the one place through which the window comes back: the tray
// toggle, the global hotkey and a server picked from the tray all end up here.
// So the "window is on screen" mark is set here rather than by each caller —
// the hotkey never set it at all, and the tray was left offering to hide a
// window that was already showing.
func (s *AppService) BringToFront() {
	wCtx := s.context()
	if wCtx == nil {
		return
	}
	wailsruntime.WindowShow(wCtx)
	wailsruntime.WindowUnminimise(wCtx)
	// Toggle AlwaysOnTop briefly to force window focus on Windows
	wailsruntime.WindowSetAlwaysOnTop(wCtx, true)
	wailsruntime.WindowSetAlwaysOnTop(wCtx, false)
	s.markWindowShown()
}

// ── Состояние подключения ────────────────────────────────────────────────────

// setTrayConnected switches everything the icon says about the session at once:
// the status line inside the menu, the icon itself and its hover text.
//
// The icon and the tooltip are the only parts readable without opening the
// menu, and while the window is in the tray they are the whole interface.
func (s *AppService) setTrayConnected(connected bool, serverName string) {
	if connected {
		s.setTrayStatus(i18n.TrayStatusConnected, serverName)
	} else {
		s.setTrayStatus(i18n.TrayStatusDisconnected)
	}

	s.trayMu.Lock()
	s.trayConnected = connected
	s.trayServerName = serverName
	icon := s.trayIconOff
	if connected {
		icon = s.trayIconOn
	}
	started := s.trayStarted
	s.trayMu.Unlock()

	if started && icon != nil {
		systray.SetIcon(icon)
	}
	s.refreshTrayTooltip()
}

// refreshTrayTooltip rewrites the hover text from the current session totals.
// Called once per traffic sample, and that is deliberate: while the window is
// in the tray this is where the numbers still move.
func (s *AppService) refreshTrayTooltip() {
	s.trayMu.Lock()
	connected := s.trayConnected
	name := s.trayServerName
	started := s.trayStarted
	s.trayMu.Unlock()

	if !started {
		return
	}
	if !connected {
		systray.SetTooltip(i18n.T(i18n.TrayTipDisconnected))
		return
	}
	up, down := s.sessionTraffic()
	// Windows truncates szTip at 128 characters, and a long node name plus two
	// byte counts can reach it. The name is the part worth losing last, so it
	// is what gets clipped.
	systray.SetTooltip(i18n.T(i18n.TrayTipConnected, clipTrayName(name), formatTrayBytes(up), formatTrayBytes(down)))
}

// clipTrayName keeps the tooltip inside the limit Windows imposes on a
// notification icon.
//
// Лимит szTip — 128 единиц UTF-16, а не 128 рун, и разница здесь не
// теоретическая: имена узлов в подписках сплошь и рядом несут флаговые эмодзи,
// каждое из которых занимает две единицы. Шестьдесят таких рун — это сто
// двадцать единиц, плюс префикс «NeoBox — подключено» и два счётчика байтов.
// systray копирует строку в [128]uint16 (systray_windows.go), и переполнение
// означает массив без завершающего нуля: оболочка читает дальше, в соседние
// поля NOTIFYICONDATA, и показывает мусор. Поэтому бюджет считается в тех же
// единицах, в которых его считает Windows.
func clipTrayName(name string) string {
	const maxUnits = 60
	units := 0
	for i, r := range name {
		size := utf16.RuneLen(r)
		if size < 0 {
			size = 1 // суррогат-одиночка: Windows увидит один U+FFFD
		}
		if units+size > maxUnits {
			return name[:i] + "…"
		}
		units += size
	}
	return name
}

// formatTrayBytes renders a byte count the way the speedometer in the window
// does — the tooltip is the same number seen from outside.
func formatTrayBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), [...]string{"KB", "MB", "GB", "TB"}[exp])
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
		{s.mProfilesItem, i18n.TrayProfiles},
		{s.mKillSwitchItem, i18n.TrayKillSwitch},
		{s.mTunItem, i18n.TrayTunMode},
		{s.mSysProxyItem, i18n.TraySystemProxy},
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

	s.refreshTrayTooltip()
	// The server entries carry subscription names, not translated text, but the
	// list is rebuilt anyway so a language change leaves nothing stale.
	s.RebuildTrayServers()
}

// SelectAndConnectServer notifies the frontend to connect to the specified proxy server.
func (s *AppService) SelectAndConnectServer(link string) {
	wCtx := s.context()
	if wCtx == nil {
		return
	}

	// Show the window so they can see the connection progress
	s.BringToFront()
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

// ── Серверы ──────────────────────────────────────────────────────────────────

// traySubscription is one subscription and the servers it contributes.
type traySubscription struct {
	Name    string
	Servers []trayServer
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
	"anytls":    "anytls:",
	"socks":     "socks:",
	"http":      "http:",
	"wireguard": "wg:",
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

// loadTrayServers reads the subscriptions and groups them into menu entries.
// It takes fileMu and holds no other lock.
func (s *AppService) loadTrayServers() []traySubscription {
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

	var grouped []traySubscription
	for _, sub := range subs {
		entry := traySubscription{Name: sub.Name}
		for _, link := range sub.Links {
			label := trayProtocolLabels[core.ProtocolOf(link)]
			entry.Servers = append(entry.Servers, trayServer{
				Name: strings.TrimSpace(label + " " + parseServerNameFromLink(link)),
				Link: link,
			})
		}
		if len(entry.Servers) > 0 {
			grouped = append(grouped, entry)
		}
	}
	return grouped
}

// applyTrayServers writes the entries into the tray menu. It takes trayMu and
// reads nothing from disk.
//
// The menu is torn down and rebuilt rather than reshuffled inside a fixed pool.
// The pool existed because systray could not delete items; this fork can
// (MenuItem.Remove), and with it the fifty-item ceiling and the "[subscription]
// node" prefix on every single line both go away — each subscription gets its
// own submenu instead.
//
// ponytail: rebuilding races systray's own click dispatch. Its
// systrayMenuItemSelected looks an item up under a read lock, releases it, and
// only then sends on ClickedCh, while Remove closes that channel — so a click
// landing in the microseconds between the two panics on a closed channel. The
// window needs a click on a tray entry at the exact instant a subscription is
// saved or the language changes, and closing it properly means patching the
// upstream library. If it ever shows up in the crash log, the fix is to make
// the click listener own the removal instead of this function.
func (s *AppService) applyTrayServers(subs []traySubscription) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()

	if s.mServersItem == nil {
		return // трей ещё не собран
	}

	// Only the items sitting directly under "Select server" are removed here:
	// Remove() takes a submenu's children down with it, and removing an already
	// removed item closes its ClickedCh twice.
	for _, item := range s.trayServerItems {
		item.Remove()
	}
	s.trayServerItems = nil

	total := 0
	for _, sub := range subs {
		total += len(sub.Servers)
	}

	// Один список не нуждается в собственной папке: подписка у большинства
	// одна, и лишний уровень меню — это лишнее движение мышью на каждое
	// переключение.
	grouped := len(subs) > 1
	shown := 0
	for _, sub := range subs {
		// Проверка стоит до создания папки, а не только внутри цикла по
		// серверам. Раньше break выходил лишь из внутреннего цикла, и у каждой
		// оставшейся подписки появлялся заголовок подменю, открывающийся в
		// пустоту: лимит выбран предыдущими списками, а папка уже создана.
		if shown >= trayServerLimit {
			break
		}
		parent := s.mServersItem
		if grouped {
			group := s.mServersItem.AddSubMenuItem(sub.Name, sub.Name)
			s.trayServerItems = append(s.trayServerItems, group)
			parent = group
		}
		for _, srv := range sub.Servers {
			if shown >= trayServerLimit {
				break
			}
			item := parent.AddSubMenuItem(srv.Name, srv.Link)
			if !grouped {
				s.trayServerItems = append(s.trayServerItems, item)
			}
			go s.watchTrayServer(item, srv.Link)
			shown++
		}
	}

	if total > shown {
		more := s.mServersItem.AddSubMenuItem(i18n.T(i18n.TrayMoreServers, total-shown), "")
		more.Disable()
		s.trayServerItems = append(s.trayServerItems, more)
	}
}

// watchTrayServer connects one menu entry to the server it names. The goroutine
// ends by itself: Remove closes ClickedCh, so a rebuild collects the listeners
// of the menu it replaced.
func (s *AppService) watchTrayServer(item *systray.MenuItem, link string) {
	for range item.ClickedCh {
		s.SelectAndConnectServer(link)
	}
}

// ── Профили ──────────────────────────────────────────────────────────────────

// trayProfile — то немногое, что о профиле нужно знать трею.
type trayProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RebuildTrayProfiles перечитывает профили и перестраивает подменю. Зовётся из
// фронтенда после каждого сохранения или удаления профиля.
func (s *AppService) RebuildTrayProfiles() {
	s.applyTrayProfiles(s.loadTrayProfiles())
}

// loadTrayProfiles читает профили из зашифрованной половины настроек.
//
// Через readSecretSettingsLocked, а не GetSettings: последний склеивает обе
// половины и кодирует результат обратно в строку, а здесь нужно одно поле.
func (s *AppService) loadTrayProfiles() []trayProfile {
	s.fileMu.Lock()
	secrets := s.readSecretSettingsLocked()
	s.fileMu.Unlock()

	raw, ok := secrets["profiles"]
	if !ok {
		return nil
	}
	// Обратно в JSON и разбор в типизированную структуру: readSecretSettings
	// отдаёт map[string]interface{}, а вручную вынимать из него поля значило бы
	// писать разбор JSON руками.
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var profiles []trayProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil
	}
	return profiles
}

// applyTrayProfiles перестраивает подменю. Берёт trayMu и с диска не читает.
func (s *AppService) applyTrayProfiles(profiles []trayProfile) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()

	if s.mProfilesItem == nil {
		return // трей ещё не собран
	}
	for _, item := range s.trayProfileItems {
		item.Remove()
	}
	s.trayProfileItems = nil

	if len(profiles) == 0 {
		s.mProfilesItem.Hide()
		return
	}
	s.mProfilesItem.Show()

	for _, profile := range profiles {
		item := s.mProfilesItem.AddSubMenuItem(profile.Name, profile.Name)
		s.trayProfileItems = append(s.trayProfileItems, item)
		go s.watchTrayProfile(item, profile.ID)
	}
}

// watchTrayProfile применяет профиль фронтендом: профиль — это набор значений
// интерфейса, и раскладывать их умеет только он.
func (s *AppService) watchTrayProfile(item *systray.MenuItem, id string) {
	for range item.ClickedCh {
		s.emitSafe("tray-profile-selected", id)
	}
}

// ── Быстрые переключатели ────────────────────────────────────────────────────

// trayToggles — состояние трёх галок, которые продублированы в меню.
type trayToggles struct {
	KillSwitch  bool
	TunMode     bool
	SystemProxy bool
}

// RebuildTrayToggles перечитывает настройки и ставит галки в меню.
func (s *AppService) RebuildTrayToggles() {
	s.fileMu.Lock()
	settings := s.readPlainSettingsLocked()
	s.fileMu.Unlock()
	s.applyTrayToggles(trayTogglesFrom(settings))
}

// trayTogglesFrom reads the three fields out of a settings object. systemProxy
// defaults to on when absent, matching the frontend — a missing field there
// means "never saved", not "off".
func trayTogglesFrom(settings map[string]interface{}) trayToggles {
	value := func(key string, fallback bool) bool {
		if v, ok := settings[key].(bool); ok {
			return v
		}
		return fallback
	}
	return trayToggles{
		KillSwitch:  value("killSwitch", false),
		TunMode:     value("tunMode", false),
		SystemProxy: value("systemProxy", true),
	}
}

// applyTrayToggles ставит галки. Берёт trayMu и с диска не читает.
func (s *AppService) applyTrayToggles(t trayToggles) {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()

	for _, pair := range []struct {
		item *systray.MenuItem
		on   bool
	}{
		{s.mKillSwitchItem, t.KillSwitch},
		{s.mTunItem, t.TunMode},
		{s.mSysProxyItem, t.SystemProxy},
	} {
		if pair.item == nil {
			continue
		}
		if pair.on {
			pair.item.Check()
		} else {
			pair.item.Uncheck()
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
