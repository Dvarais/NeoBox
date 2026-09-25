// Verify TLS chains in pure Go against the roots backend/core/roots_windows.go
// installs, never through crypt32 — see that file for the crash this avoids.
// No effect on Linux, where no fallback roots are set.
//
//go:debug x509usefallbackroots=1

package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/security"
	"NeoBox/backend/platform"
	"NeoBox/backend/service"
	"NeoBox/backend/storage"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

// The same icon, desaturated. Which of the two is in the notification area is
// the only thing the tray says about the connection without being opened.
//
//go:embed build/windows/icon-off.ico
var trayIconOff []byte

// goMemoryLimit is a backstop, not a tuning knob. Steady state for this process
// measures around 176 MB of private bytes, most of which is the 35 MB binary
// image, thread stacks and the gVisor TUN stack's packet buffers rather than the
// Go heap, so this ceiling sits far above anything normal operation reaches and
// never costs collection cycles in practice.
//
// What it buys is a bound on the abnormal case: without a limit the GC targets a
// multiple of the live heap and nothing else, so a pathological session -- a
// runaway rule set, a DNS table that will not stop growing -- has no ceiling at
// all short of the machine's memory. Past this point the collector works harder
// instead of the process simply taking more.
const goMemoryLimit = 384 << 20 // 384 MiB

func main() {
	debug.SetMemoryLimit(goMemoryLimit)

	// Hide the console window immediately if running in standalone mode (e.g. from registry startup)
	security.HideConsoleIfNeeded()

	userDataDir := platform.Current().Paths().UserDataDir()
	// Ensure the directory exists before writing the encryption key
	_ = os.MkdirAll(userDataDir, 0755)

	// Capture panics to disk. The sing-box core runs inside this process, so a
	// panic in one of its goroutines kills NeoBox outright — and with the console
	// detached above, the stack trace would otherwise go nowhere and the window
	// would just disappear without a trace anywhere in the system.
	_ = security.InitCrashLog(security.CrashLogPath(userDataDir))

	// Bring up encrypted storage before anything can touch the subscription file.
	// This also migrates a plaintext subscriptions.json left by an older build.
	// Failing hard is deliberate: continuing would leave every save of the user's
	// proxy credentials silently failing, and the app is unusable either way.
	if err := storage.Init(userDataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Storage initialization failed: %v\n", err)
		security.MarkCleanExit("storage initialization failed")
		os.Exit(1)
	}

	// Ensure only one instance of NeoBox runs at a time via a Windows named
	// kernel mutex. Windows automatically releases the mutex when the owning
	// process exits (even on a crash), so the mutex alone is sufficient and no
	// process enumeration / termination is required.
	mutexHandle, alreadyRunning := service.AcquireSingleInstanceMutex()
	if alreadyRunning {
		// Another instance is already running — bring it to foreground and exit.
		fmt.Println("Another NeoBox instance is already running. Focusing existing window...")
		bringExistingInstanceToForeground()
		security.MarkCleanExit("another instance is running")
		os.Exit(0)
	}
	// Elevate now, before anything expensive exists.
	//
	// TUN mode and the Kill Switch both need administrator rights, and nothing
	// used to notice that until the frontend had loaded and asked to connect. A
	// machine configured for TUN therefore paid for two complete cold starts on
	// every launch: storage, the tray, WebView2 and the whole UI came up once
	// unelevated only to relaunch and do all of it again. The crash log records
	// the two starts five to six seconds apart.
	//
	// The decision needs one plaintext field out of settings.json, so it can be
	// made here — before the tray icon, the window and the core manager exist,
	// and before NewAppService tries a kill-switch recovery that needs the very
	// rights we are about to ask for.
	mutexHandle, relaunched := relaunchElevatedIfNeeded(userDataDir, mutexHandle)
	if relaunched {
		security.MarkCleanExit("relaunch as administrator")
		os.Exit(0)
	}

	// Clean up any legacy Task Scheduler tasks ("NeoBox", "NeoBox-Go") when elevated.
	if service.IsElevated() {
		security.RemoveLegacyScheduledTasks()
	}

	// 2. Initialize embedded core manager
	coreManager := core.NewCoreManager()

	// 3. Initialize AppService containing Wails bindings
	appService := service.NewAppService(coreManager, userDataDir)
	appService.SetMutexHandle(mutexHandle)
	// Close whatever handle the service holds at that point, not the value read
	// here: RequestAdmin releases the mutex for an elevated relaunch and acquires
	// a fresh one when the UAC prompt is declined, which leaves this variable
	// naming a handle that no longer exists.
	defer func() {
		if h := appService.MutexHandle(); h != 0 {
			closeInstanceHandle(h)
		}
	}()

	// Undo a system proxy left installed by a previous run that crashed. This
	// restores the user's own configuration when NewAppService found a persisted
	// backup, and otherwise clears the setting only if it still points at NeoBox
	// — a proxy the user configured themselves is never touched.
	appService.SetSystemProxy(false)

	// Read settings to check if we should start minimized (hidden) in tray
	startHidden := false
	settingsJSON := appService.GetSettings()
	var settingsMap map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &settingsMap); err == nil {
		if startMin, ok := settingsMap["startMinimized"].(bool); ok && startMin {
			startHidden = true
		}
	}
	appService.SetWindowVisible(!startHidden)

	// Clear any WebView2 process left over from a previous session, before this
	// one can attach to it.
	//
	// WebView2 keeps one browser process per user data folder and hands it to
	// whoever names that folder next, so a leftover is not idle load — it is the
	// process the interface below is about to be rendered inside. A session that
	// followed one which did not shut down cleanly was drawn by the previous
	// session's wedged renderer from its very first frame. See
	// backend/service/webview_children.go.
	//
	// After the single-instance check on purpose: that is what guarantees no
	// other NeoBox legitimately owns one of these.
	service.SweepOrphanedWebViews(userDataDir)

	// Start system tray immediately before launching the main window/WebView2.
	// This ensures the tray icon appears instantly, even if the app starts minimized.
	appService.InitTray(trayIcon, trayIconOff)

	// Create application with custom modern options
	err := wails.Run(&options.App{
		Title:         "NeoBox",
		Width:         950,
		Height:        700,
		MinWidth:      800,
		MinHeight:     600,
		Frameless:     true, // Frameless window for sleek custom titlebar layout
		DisableResize: false,
		StartHidden:   startHidden,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Opaque, and matching --grad-start in style.css: the page paints a
		// gradient over it anyway, so a transparent one only showed through in
		// the resize gutters and during the fade-out animation.
		//
		// Это левый стоп градиента, а не его середина: гаттеры окружают окно со
		// всех сторон, и самый тёмный из трёх цветов меньше всех бросается в
		// глаза на любой из кромок.
		//
		// Значение здесь — только стандартная тема и только до первого кадра.
		// Дальше цвет гаттеров ведёт фронтенд: applyTheme в
		// frontend/modules/theme.ts зовёт WindowSetBackgroundColour с самым
		// тёмным стопом выбранной темы. Без этого на любой чужой теме по краям
		// окна оставалась бы полоска старого почти чёрного.
		//
		// Связь с CSS по-прежнему держится только комментарием, но касается
		// теперь одной пары значений: этой строки и --grad-start в style.css.
		BackgroundColour: &options.RGBA{R: 8, G: 9, B: 9, A: 255},
		OnStartup: func(ctx context.Context) {
			appService.SetContext(ctx)
			appService.StartAutoUpdateScheduler()
		},
		OnBeforeClose: func(ctx context.Context) bool {
			if appService.IsQuitting() {
				return false // Allow closing/quitting
			}
			// Hide window instead of closing it, and update tray state
			wailsruntime.WindowHide(ctx)
			appService.NotifyWindowHidden()
			return true // Prevent actual close
		},
		OnShutdown: func(ctx context.Context) {
			// Record the intentional exit, so a session block in the crash log
			// that lacks this marker can be read as a crash.
			security.MarkCleanExit("shutdown")

			// Last resort. Everything below is bounded, and the exit after
			// wails.Run returns is not reached if Wails' own teardown does not
			// return — and then nothing at all would end the process. On the happy
			// path the app is long gone before this fires.
			go func() {
				time.Sleep(shutdownGrace + shutdownWatchdogSlack)
				fmt.Fprintln(os.Stderr, "[shutdown] teardown never returned; terminating")
				service.ExitNow(0)
			}()

			// Securely wipe encryption keys from memory before shutdown
			security.SecureWipe()

			// Wait for the clean shutdown instead of firing it off and racing it.
			//
			// Quit() is what removes the system proxy and the firewall Kill Switch,
			// and both of those outlive the process — a run that skips them leaves
			// the machine proxied at a dead port, or with no internet at all. It
			// used to be started with `go` and never waited for: main returned from
			// wails.Run immediately afterwards, so whether the cleanup finished was
			// down to which goroutine won.
			//
			// The wait is bounded because Quit() ends in systray.Quit(), which
			// enters a message-loop teardown that can hang. Bounded, not absent.
			done := make(chan struct{})
			go func() {
				appService.Quit()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(shutdownGrace):
				fmt.Fprintln(os.Stderr, "[shutdown] clean shutdown did not finish in time; exiting anyway")
			}
		},
		Bind: []interface{}{
			appService,
		},
		Windows: getWindowsOptions(userDataDir),
	})

	if err != nil {
		println("Error starting NeoBox:", err.Error())
		appService.Quit()
	}

	// Not os.Exit and not a plain return: with the tray and WebView2 up, both of
	// those wedge the process instead of ending it. See service.ExitNow.
	// Everything that has to outlive the process happened in OnShutdown above.
	service.ExitNow(0)
}

// shutdownGrace bounds the clean shutdown in OnShutdown. It is long enough for
// the registry write and the netsh calls that remove the Kill Switch, and short
// enough that a wedged systray teardown does not keep the user waiting.
const shutdownGrace = 5 * time.Second

// shutdownWatchdogSlack is how much longer than shutdownGrace the watchdog
// waits, so it only ever fires when the bounded wait itself did not return.
const shutdownWatchdogSlack = 3 * time.Second


