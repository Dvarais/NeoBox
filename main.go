package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"NeoBox/backend/core"
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
	// Ensure only one instance of NeoBox runs at a time via a Windows named
	// kernel mutex. Windows automatically releases the mutex when the owning
	// process exits (even on a crash), so the mutex alone is sufficient and no
	// process enumeration / termination is required.
	mutexHandle, alreadyRunning := acquireSingleInstanceMutex()
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

	// Run migration from the old Electron version, but ONLY when the legacy
	// data folder actually exists. This keeps the DPAPI code path (which
	// antivirus heuristics flag) out of the normal startup of fresh/current
	// installs. The full existence checks are repeated inside the function.
	if legacyElectronDataExists(userDataDir) {
		migrateOldSettings(userDataDir)
	}

	// 2. Initialize embedded core manager
	coreManager := core.NewCoreManager()

	// 3. Initialize AppService containing Wails bindings
	appService := service.NewAppService(coreManager, userDataDir)

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

// acquireSingleInstanceMutex creates a Windows named mutex to ensure only one
// instance of NeoBox runs at a time. Returns the mutex handle and whether
// another instance is already running.
//
// It first tries the Global\ namespace (visible across sessions, including the
// elevated relaunch from RequestAdmin). The Global\ namespace requires the
// SeCreateGlobalPrivilege privilege, which regular interactive users may lack
// in locked-down environments. If Global\ creation fails with anything other
// than ERROR_ALREADY_EXISTS, we transparently fall back to the Local\
// namespace, which is always available to the current user and is sufficient
// for single-instance enforcement within a single user session.
func acquireSingleInstanceMutex() (syswindows.Handle, bool) {
	if handle, already := createSingleInstanceMutex("Global\\NeoBox-SingleInstance-Mutex"); handle != 0 || already {
		return handle, already
	}
	// Global\ failed for a reason other than "already exists" (most likely
	// access denied without SeCreateGlobalPrivilege) — fall back to Local\.
	handle, already := createSingleInstanceMutex("Local\\NeoBox-SingleInstance-Mutex")
	return handle, already
}

// createSingleInstanceMutex creates a mutex with the given name. Returns
// (handle, true) if another instance already holds it, (handle>0, false) on
// successful creation, and (0, false) if creation failed for any other reason.
func createSingleInstanceMutex(name string) (syswindows.Handle, bool) {
	mutexName, _ := syswindows.UTF16PtrFromString(name)
	handle, err := syswindows.CreateMutex(nil, false, mutexName)
	if err != nil {
		if err == syswindows.ERROR_ALREADY_EXISTS {
			if handle != 0 {
				_ = syswindows.CloseHandle(handle)
			}
			return 0, true // Another instance holds the mutex
		}
		// CreateMutex failed for another reason (e.g. access denied on Global\).
		return 0, false
	}
	return handle, false
}

// legacyElectronDataExists reports whether the old Electron-based NeoBox left
// its data folder (<userDataDir>/data/) on disk. This is the sole gate that
// decides whether the DPAPI-based migration (and thus the CryptUnprotectData
// call, which antivirus heuristics flag) runs at all during startup. For any
// install that never had the Electron version, this returns false and the
// entire migration path — including the DPAPI import and proc — is never
// reached at runtime.
func legacyElectronDataExists(userDataDir string) bool {
	oldDataDir := filepath.Join(userDataDir, "data")
	info, err := os.Stat(oldDataDir)
	return err == nil && info.IsDir()
}

// migrateOldSettings migrates settings and subscriptions from the old Electron-based
// NeoBox version (which stored data in AppData/Roaming/NeoBox/data/) to the new Go
// version (which stores directly in AppData/Roaming/NeoBox/).
//
// The key fix: both versions share the same root folder (%APPDATA%\NeoBox), so we
// cannot check directory existence. Instead, we check for the new-format settings.json
// directly — if it doesn't exist but the old data/ subfolder does, we migrate.
func migrateOldSettings(userDataDir string) {
	// Old Electron version stored data in a "data" subfolder
	oldDataDir := filepath.Join(userDataDir, "data")

	// New Go version stores files directly in userDataDir
	newSettings := filepath.Join(userDataDir, "settings.json")
	newSubs := filepath.Join(userDataDir, "subscriptions.json")

	// Only migrate if new-format settings don't exist yet but old data folder does.
	// FIX #17: simplified from !os.IsNotExist(err) double-negation to err == nil.
	if _, err := os.Stat(newSettings); err == nil {
		return // File exists — already migrated or fresh install with settings
	}
	if _, err := os.Stat(oldDataDir); err != nil {
		return // No old data folder found — nothing to migrate
	}

	// Ensure the target directory exists
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return
	}

	// 1. Migrate subscriptions.json
	oldSubs := filepath.Join(oldDataDir, "subscriptions.json")
	if _, err := os.Stat(oldSubs); err == nil {
		_ = copyFile(oldSubs, newSubs)
	}

	// 2. Migrate settings.json — decrypt Electron DPAPI, set autoConnect=false, re-encrypt
	oldSettings := filepath.Join(oldDataDir, "settings.json")
	if _, err := os.Stat(oldSettings); err == nil {
		if err := copyFile(oldSettings, newSettings); err == nil {
			encData, err := os.ReadFile(newSettings)
			if err == nil {
				// FIX #1: Use Windows DPAPI (CryptUnprotectData) to decrypt Electron safeStorage.
				// The old code incorrectly called security.Decrypt() (AES-GCM) on DPAPI data.
				migrated := false
				decrypted, err := decryptElectronSafeStorage(encData)
				if err == nil {
					var settingsMap map[string]interface{}
					if err := json.Unmarshal(decrypted, &settingsMap); err == nil {
						// Prevent auto-connect loop on first run after migration
						settingsMap["autoConnect"] = false
						if newJSON, err := json.MarshalIndent(settingsMap, "", "  "); err == nil {
							if err := os.WriteFile(newSettings, newJSON, 0644); err == nil {
								migrated = true
							}
						}
					}
				}
				// On ANY failure (DPAPI decrypt, JSON parse, or write), preserve the
				// partially-copied file as a .corrupt.bak rather than deleting it, so
				// the user's old settings are never lost silently. The app will then
				// start with clean defaults (GetSettings returns "{}" for a missing file).
				if !migrated {
					_ = os.Rename(newSettings, newSettings+".corrupt.bak")
				}
			}
		}
	}
}

// dpApiBlob is the Windows DATA_BLOB structure used by CryptUnprotectData.
type dpApiBlob struct {
	cbData uint32
	pbData *byte
}

var (
	modCrypt32             = syswindows.NewLazySystemDLL("crypt32.dll")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
)

// decryptWithDPAPI decrypts data that was encrypted with Windows CryptProtectData (DPAPI).
// This is required to read settings encrypted by the old Electron safeStorage implementation.
func decryptWithDPAPI(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	inBlob := dpApiBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var outBlob dpApiBlob

	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}
	defer syswindows.LocalFree(syswindows.Handle(unsafe.Pointer(outBlob.pbData))) //nolint:errcheck

	if outBlob.cbData == 0 {
		return []byte{}, nil
	}
	plaintext := make([]byte, outBlob.cbData)
	copy(plaintext, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return plaintext, nil
}

// decryptElectronSafeStorage decrypts Electron safeStorage DPAPI encrypted strings.
// Electron safeStorage prepends a "v10" prefix (0x76, 0x31, 0x30) to the DPAPI payload on Windows.
// FIX #1: Previously this incorrectly called security.Decrypt() (AES-GCM) on DPAPI data.
func decryptElectronSafeStorage(data []byte) ([]byte, error) {
	if len(data) > 3 && string(data[:3]) == "v10" {
		data = data[3:]
	}
	// Correctly use Windows DPAPI (CryptUnprotectData) instead of AES-GCM
	return decryptWithDPAPI(data)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
