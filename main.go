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
	"NeoBox/backend/security"
	"NeoBox/backend/service"

	ps "github.com/mitchellh/go-ps"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	syswindows "golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	// Clean up any other running instances of the app before starting.
	// Uses a Windows named mutex as the primary single-instance guard.
	mutexHandle, alreadyRunning := acquireSingleInstanceMutex()
	if alreadyRunning {
		// Another instance is already running — bring it to foreground and exit.
		fmt.Println("Another NeoBox instance is already running.")
		os.Exit(0)
	}
	if mutexHandle != 0 {
		defer syswindows.CloseHandle(mutexHandle)
	}

	// Fallback: kill orphaned instances that didn't clean up their mutex
	killOrphanedInstances()

	// 1. Resolve user data directory for settings/subscriptions
	homeDir, _ := os.UserHomeDir()
	userDataDir := filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")
	// Ensure the directory exists before writing the encryption key
	_ = os.MkdirAll(userDataDir, 0755)

	// Initialize AES encryption key (must be before migration and service startup).
	// This replaces the old DPAPI approach which was session-dependent and caused
	// settings loss after shutdown/privilege changes.
	if err := security.InitEncryption(userDataDir); err != nil {
		fmt.Printf("Warning: failed to initialize encryption: %v\n", err)
	}

	// Run migration from old Electron version if present and new Go version folder doesn't exist
	migrateOldSettings(userDataDir)

	// 2. Initialize embedded core manager
	coreManager := core.NewCoreManager()

	// 3. Initialize AppService containing Wails bindings
	appService := service.NewAppService(coreManager, userDataDir)

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
			appService.InitTray(trayIcon)
			appService.StartAutoUpdateScheduler()
		},
		OnShutdown: func(ctx context.Context) {
			// FIX #9: Stop the auto-update background goroutine before cleaning up VPN.
			appService.StopAutoUpdateScheduler()
			// Safe shutdown of VPN processes and proxy cleanup on close
			_ = coreManager.Stop()
			appService.SetSystemProxy(false)
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
	}
}

// acquireSingleInstanceMutex creates a Windows named mutex to ensure only one
// instance of NeoBox runs at a time. Returns the mutex handle and whether
// another instance is already running.
func acquireSingleInstanceMutex() (syswindows.Handle, bool) {
	mutexName, _ := syswindows.UTF16PtrFromString("Global\\NeoBox-SingleInstance-Mutex")
	handle, err := syswindows.CreateMutex(nil, false, mutexName)
	if err != nil {
		if err == syswindows.ERROR_ALREADY_EXISTS {
			return 0, true // Another instance holds the mutex
		}
		// CreateMutex failed for another reason — allow startup anyway
		return 0, false
	}
	return handle, false
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
			if encData, err := os.ReadFile(newSettings); err == nil {
				// FIX #1: Use Windows DPAPI (CryptUnprotectData) to decrypt Electron safeStorage.
				// The old code incorrectly called security.Decrypt() (AES-GCM) on DPAPI data.
				decrypted, err := decryptElectronSafeStorage(encData)
				if err == nil {
					var settingsMap map[string]interface{}
					if err := json.Unmarshal(decrypted, &settingsMap); err == nil {
						// Prevent auto-connect loop on first run after migration
						settingsMap["autoConnect"] = false
						if newJSON, err := json.Marshal(settingsMap); err == nil {
							if encryptedBytes, err := security.Encrypt(newJSON); err == nil {
								_ = os.WriteFile(newSettings, encryptedBytes, 0600)
							}
						}
					}
				} else {
					// DPAPI decryption failed (e.g., different user session or corrupted data).
					// Remove the partially-copied file so the app starts with clean defaults.
					_ = os.Remove(newSettings)
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

// killOrphanedInstances kills NeoBox processes that are running from the same executable
// path as the current process but did not acquire the single-instance mutex (orphaned).
// This serves as a fallback for instances that crashed before releasing the mutex.
//
// FIX #2: Uses full executable path comparison (via QueryFullProcessImageName) instead of
// just the filename, preventing accidental kills of unrelated processes with the same name.
func killOrphanedInstances() {
	currentPID := os.Getpid()
	currentExecutable, err := os.Executable()
	if err != nil {
		return
	}
	// Resolve symlinks to get the canonical path
	currentExecutable, err = filepath.EvalSymlinks(currentExecutable)
	if err != nil {
		return
	}

	processes, err := ps.Processes()
	if err != nil {
		return
	}

	for _, p := range processes {
		if p.Pid() == currentPID {
			continue
		}

		// Get full executable path for this process using Windows API
		fullPath, err := getProcessFullPath(p.Pid())
		if err != nil {
			continue
		}

		// Resolve to canonical path before comparing
		fullPath, err = filepath.EvalSymlinks(fullPath)
		if err != nil {
			continue
		}

		if !filepath.IsAbs(fullPath) || fullPath != currentExecutable {
			continue
		}

		proc, err := os.FindProcess(p.Pid())
		if err != nil {
			continue
		}
		_ = proc.Kill()
		time.Sleep(100 * time.Millisecond)
	}
}

// getProcessFullPath returns the full executable path of a process by PID using
// Windows QueryFullProcessImageNameW API, which is more reliable than go-ps.
func getProcessFullPath(pid int) (string, error) {
	handle, err := syswindows.OpenProcess(
		syswindows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return "", err
	}
	defer syswindows.CloseHandle(handle) //nolint:errcheck

	buf := make([]uint16, syswindows.MAX_PATH)
	size := uint32(len(buf))
	if err := syswindows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return syswindows.UTF16ToString(buf[:size]), nil
}
