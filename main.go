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

	// SECURITY: Run anti-tampering and anti-debugging checks at startup.
	// This detects debuggers, sandboxes, and binary modification before any sensitive operations.
	if err := security.SecureStartup(); err != nil {
		fmt.Fprintf(os.Stderr, "Security check failed: %v\n", err)
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
		os.Exit(0)
	}
	if mutexHandle != 0 {
		defer syswindows.CloseHandle(mutexHandle)
	}

	// 1. Resolve user data directory for settings/subscriptions
	homeDir, _ := os.UserHomeDir()
	userDataDir := filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")
	// Ensure the directory exists before writing the encryption key
	_ = os.MkdirAll(userDataDir, 0755)

	// 2. Initialize embedded core manager
	coreManager := core.NewCoreManager()

	// 3. Initialize AppService containing Wails bindings
	appService := service.NewAppService(coreManager, userDataDir)
	appService.SetMutexHandle(mutexHandle)

	// Clean up any stale system proxy settings from a previous crashed run
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
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0}, // Transparent background
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
		// Native Windows backdrop configurations for premium glass effects
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			BackdropType:         windows.Acrylic, // High-fidelity Windows Acrylic blurring
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
