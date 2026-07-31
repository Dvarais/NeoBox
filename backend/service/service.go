package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/i18n"

	"fyne.io/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// AppService is the Wails binding surface: every exported method on it is
// callable from the frontend. The implementation is split across the sibling
// files in this package (vpn.go, tray.go, proxy.go, updates.go, …).
//
// # Locking
//
// State is grouped under four locks, each covering one concern, rather than one
// mutex covering all of them. Any given call path takes at most one of these at
// a time — they are never nested — which is what keeps a lock ordering between
// them from existing at all. The one place that used to nest them (rebuilding
// the tray while holding the storage lock) reads its data first and applies it
// second; see RebuildTrayServers.
type AppService struct {
	coreManager *core.CoreManager
	userDataDir string

	// wailsCtxMu guards wailsCtx. sing-box writes log lines from its own
	// goroutines while Wails sets the context from the main one.
	wailsCtx   context.Context
	wailsCtxMu sync.RWMutex

	// fileMu serialises access to settings.json, state.json and
	// subscriptions.json.
	fileMu sync.Mutex

	// trayMu guards the tray menu items and the window visibility they display.
	trayMu          sync.Mutex
	windowVisible   bool
	mStatusItem     *systray.MenuItem
	mToggleItem     *systray.MenuItem
	mServersItem    *systray.MenuItem
	mRestartItem    *systray.MenuItem
	mDisconnectItem *systray.MenuItem
	mQuitItem       *systray.MenuItem
	trayServerItems [50]*TrayServerItem
	trayStarted     bool // true once systray.Run has been entered; guards systray.Quit()
	// The status line is dynamic, so its message id and arguments are kept in
	// order to re-render it in the new language when the user switches.
	trayStatusID   string
	trayStatusArgs []interface{}

	// stateMu guards process-wide session state: what is running, what must be
	// cancelled on shutdown, and what has to be restored afterwards.
	stateMu           sync.Mutex
	cancelMonitor     context.CancelFunc
	cancelAutoUpdate  context.CancelFunc
	clashSecret       string // per-session random secret for Clash API auth
	backupProxyServer string
	backupProxyEnable uint32
	hasProxyBackup    bool
	quitting          bool
	mutexHandle       windows.Handle
	// logStream batches sing-box log lines on their way to the frontend; nil
	// while no core session is running. See logstream.go.
	logStream *logStreamer

	// trafficMu guards the session traffic totals. They are accumulated in Go
	// rather than the frontend so they stay correct across periods where the
	// window is hidden and no traffic-stats events are sent at all.
	trafficMu   sync.Mutex
	sessionUp   int64
	sessionDown int64

	// watchdogMu guards the auto-reconnect watchdog.
	watchdogMu     sync.Mutex
	cancelWatchdog context.CancelFunc
	watchdogLink   string
	watchdogProxy  bool

	quitOnce sync.Once
}

// NewAppService creates a new AppService instance.
func NewAppService(cm *core.CoreManager, userDataDir string) *AppService {
	// Create user data directory if it doesn't exist
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		fmt.Printf("Error creating user data dir: %v\n", err)
	}
	svc := &AppService{
		coreManager: cm,
		userDataDir: userDataDir,
	}
	// Move any proxy credentials an older build left sitting in plaintext in
	// settings.json into the encrypted store. This runs before the first read
	// below so nothing observes the half-migrated state.
	svc.migrateSecretSettings()

	// Adopt the language the user last chose, so the tray, toasts and diagnostics
	// come up translated rather than in the default language.
	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(svc.GetSettings()), &settings); err == nil {
		if lang, ok := settings["language"].(string); ok {
			i18n.SetLanguage(lang)
		}
	}

	// Pick up a system proxy backup left behind by a run that did not shut down
	// cleanly, so the following SetSystemProxy(false) can actually restore it.
	svc.loadProxyBackup()
	// Firewall rules outlive the process, so a crashed session can leave the
	// machine with no internet at all. Clear that before doing anything else.
	svc.recoverKillSwitch()
	return svc
}

// SetContext sets the Wails application context, guarded by wailsCtxMu so the
// concurrent reads in wailsLogWriter.WriteMessage stay safe. It also registers
// the NeoBox AppID used to attribute Windows toast notifications.
func (s *AppService) SetContext(ctx context.Context) {
	s.wailsCtxMu.Lock()
	s.wailsCtx = ctx
	s.wailsCtxMu.Unlock()
	// Register toast AppID once after the app context is available.
	InitNotifications()
}

// emitSafe emits a Wails event thread-safely (wailsCtx may be nil during startup).
func (s *AppService) emitSafe(event string, data ...interface{}) {
	s.wailsCtxMu.RLock()
	ctx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
}

// releaseIdleMemory hands pages the Go heap no longer needs back to the OS.
//
// The runtime's scavenger does this on its own, but lazily and over minutes, so
// after a burst of work the process keeps holding the peak long after it stopped
// needing it -- which is precisely the number a user sees in Task Manager and
// reads as the application's appetite.
//
// Call it only on transitions where the burst is genuinely over and a pause
// costs nothing: the window going to the tray, a session ending. It forces a
// full collection, so it has no business anywhere near a hot path.
func releaseIdleMemory() {
	debug.FreeOSMemory()
}

// stopLogStream ends the current session's log batching after a final flush, so
// the tail of the output still reaches the UI. No-op when nothing is running.
func (s *AppService) stopLogStream() {
	s.stateMu.Lock()
	ls := s.logStream
	s.logStream = nil
	s.stateMu.Unlock()
	if ls != nil {
		ls.stopStreaming()
	}
}

// flushLogStream pushes whatever has accumulated while the window was hidden.
func (s *AppService) flushLogStream() {
	s.stateMu.Lock()
	ls := s.logStream
	s.stateMu.Unlock()
	if ls != nil {
		ls.flush()
	}
}

// resetSessionTraffic zeroes the counters at the start of a core session.
func (s *AppService) resetSessionTraffic() {
	s.trafficMu.Lock()
	s.sessionUp, s.sessionDown = 0, 0
	s.trafficMu.Unlock()
}

// addSessionTraffic folds one sample of the Clash traffic stream into the
// session totals and returns the running totals.
func (s *AppService) addSessionTraffic(up, down int64) (totalUp, totalDown int64) {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	s.sessionUp += up
	s.sessionDown += down
	return s.sessionUp, s.sessionDown
}

// sessionTraffic reads the running totals.
func (s *AppService) sessionTraffic() (totalUp, totalDown int64) {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	return s.sessionUp, s.sessionDown
}

// SaveLogs writes the provided log text to a timestamped file in the logs directory.
// Returns the absolute path of the saved file, or empty string on error.
func (s *AppService) SaveLogs(content string) string {
	logsDir := filepath.Join(s.userDataDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return ""
	}
	fileName := time.Now().Format("2006-01-02_15-04-05") + ".log"
	filePath := filepath.Join(logsDir, fileName)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return ""
	}
	return filePath
}

// OpenLogsFolder opens the logs directory in Windows Explorer.
func (s *AppService) OpenLogsFolder() {
	logsDir := filepath.Join(s.userDataDir, "logs")
	_ = os.MkdirAll(logsDir, 0755)
	exec.Command("explorer", logsDir).Start()
}

// generateClashSecret returns a cryptographically secure 32-character hex secret
// guarding the Clash API, which would otherwise let any local process drive the
// VPN core.
//
// There is deliberately no fallback value. An earlier version fell back to a
// hardcoded string, which was compiled into the binary and trivially recovered
// from it — a predictable secret is the same as no secret at all. So a failure
// here is reported to the caller, which abandons the connection: refusing to
// connect is the only outcome that does not leave the API unguarded.
func generateClashSecret() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("crypto/rand is unavailable: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// SetQuitting sets the quitting flag.
func (s *AppService) SetQuitting(quitting bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.quitting = quitting
}

// IsQuitting returns whether the application is shutting down.
func (s *AppService) IsQuitting() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.quitting
}

// Quit performs clean and safe application shutdown.
// It is guaranteed to run only once using sync.Once.
func (s *AppService) Quit() {
	s.quitOnce.Do(func() {
		s.SetQuitting(true)
		// Stop the watchdog first
		s.stopWatchdog()
		// Stop the auto-update scheduler
		s.StopAutoUpdateScheduler()
		// Safe shutdown of VPN processes
		if s.coreManager != nil {
			_ = s.coreManager.Stop()
		}
		// Clean up system proxy settings
		s.SetSystemProxy(false)
		// Disable the firewall Kill Switch — otherwise the block-all rules would
		// remain in Windows Firewall and the user would lose ALL internet access
		// after quitting the app (rules persist across app restarts/reboots).
		// If removal fails the marker stays on disk and the next start retries.
		s.disableKillSwitch()
		// Quit the system tray message loop and remove the tray icon.
		// Only call systray.Quit() if systray.Run() was actually entered — calling
		// it before Run() starts (e.g. when wails.Run failed during early startup
		// before the tray goroutine ran) can panic or hang inside the systray lib.
		s.trayMu.Lock()
		started := s.trayStarted
		s.trayMu.Unlock()
		if started {
			systray.Quit()
		}
	})
}
