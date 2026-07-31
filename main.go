package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"NeoBox/backend/core"
	"NeoBox/backend/security"
	"NeoBox/backend/service"
	"NeoBox/backend/storage"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	syswindows "golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	// Hide the console window immediately if running in standalone mode (e.g. from registry startup)
	security.HideConsoleIfNeeded()

	// Resolve the user data directory first: the crash log lives inside it and
	// must be armed before anything else can fail.
	homeDir, _ := os.UserHomeDir()
	userDataDir := filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")
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
	if mutexHandle != 0 {
		defer syswindows.CloseHandle(mutexHandle)
	}

	// 2. Initialize embedded core manager
	coreManager := core.NewCoreManager()

	// 3. Initialize AppService containing Wails bindings
	appService := service.NewAppService(coreManager, userDataDir)
	appService.SetMutexHandle(mutexHandle)

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

	// Start system tray immediately before launching the main window/WebView2.
	// This ensures the tray icon appears instantly, even if the app starts minimized.
	appService.InitTray(trayIcon)

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
		// Opaque, and matching --bg-color in style.css: the page paints a solid
		// background anyway, so a transparent one only showed through in the
		// resize gutters and during the fade-out animation.
		BackgroundColour: &options.RGBA{R: 11, G: 15, B: 25, A: 255},
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

			// Securely wipe encryption keys from memory before shutdown
			security.SecureWipe()

			// Run clean shutdown in a goroutine so it doesn't block OnShutdown.
			go appService.Quit()

			// Watchdog: if the process is still alive after a few seconds it means
			// something (stuck WebView2, tray loop, blocked goroutine) prevented a
			// normal exit. Force-terminate to avoid leaving a zombie process.
			// On the happy path the app exits before this fires.
			go func() {
				time.Sleep(5 * time.Second)
				os.Exit(0)
			}()
		},
		Bind: []interface{}{
			appService,
		},
		Windows: &windows.Options{
			// The page fills itself with an opaque background (body in
			// style.css), so nothing was ever visible through the window.
			// Asking for transparency anyway bought no glass effect and cost
			// real memory: it puts WebView2 on its transparent composition
			// path, which keeps extra render surfaces alive in the GPU process.
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			BackdropType:         windows.None,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("Error starting NeoBox:", err.Error())
		appService.Quit()
	}
}

// bringExistingInstanceToForeground finds the window of the running instance of NeoBox by its title,
// restores it if minimized, and brings it to the foreground.
func bringExistingInstanceToForeground() {
	user32 := syswindows.NewLazySystemDLL("user32.dll")
	procFindWindowW := user32.NewProc("FindWindowW")
	procShowWindow := user32.NewProc("ShowWindow")
	procSetForegroundWindow := user32.NewProc("SetForegroundWindow")
	procIsIconic := user32.NewProc("IsIconic")

	titlePtr, _ := syswindows.UTF16PtrFromString("NeoBox")
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd != 0 {
		// SW_RESTORE = 9, SW_SHOW = 5
		isMinimized, _, _ := procIsIconic.Call(hwnd)
		if isMinimized != 0 {
			_, _, _ = procShowWindow.Call(hwnd, 9) // SW_RESTORE
		} else {
			_, _, _ = procShowWindow.Call(hwnd, 5) // SW_SHOW
		}
		_, _, _ = procSetForegroundWindow.Call(hwnd)
	}
}
