package main

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/security"
	"NeoBox/backend/service"

	ps "github.com/mitchellh/go-ps"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	// Clean up any other running instances of the app before starting!
	killExistingInstances()

	// 1. Resolve user data directory for settings/subscriptions
	homeDir, _ := os.UserHomeDir()
	userDataDir := filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")

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

// migrateOldSettings automatically copies old settings.json and subscriptions.json
// from AppData/Roaming/NeoBox to AppData/Roaming/NeoBox-Go, decrypting with DPAPI,
// modifying autoConnect to false, and re-encrypting using the Go DPAPI module.
func migrateOldSettings(userDataDir string) {
	oldDir := filepath.Join(filepath.Dir(userDataDir), "NeoBox")
	oldDataDir := filepath.Join(oldDir, "data")

	// If the new directory doesn't exist yet, but the old data folder does
	if _, err := os.Stat(userDataDir); os.IsNotExist(err) {
		if _, err := os.Stat(oldDataDir); err == nil {
			// Create the new user data directory
			if err := os.MkdirAll(userDataDir, 0755); err != nil {
				return
			}

			// 1. Migrate subscriptions.json
			oldSubs := filepath.Join(oldDataDir, "subscriptions.json")
			newSubs := filepath.Join(userDataDir, "subscriptions.json")
			if _, err := os.Stat(oldSubs); err == nil {
				_ = copyFile(oldSubs, newSubs)
			}

			// 2. Migrate settings.json
			oldSettings := filepath.Join(oldDataDir, "settings.json")
			newSettings := filepath.Join(userDataDir, "settings.json")
			if _, err := os.Stat(oldSettings); err == nil {
				if err := copyFile(oldSettings, newSettings); err == nil {
					// Read, decrypt, change autoConnect, re-encrypt
					if encData, err := os.ReadFile(newSettings); err == nil {
						decrypted, err := decryptElectronSafeStorage(encData)
						if err == nil {
							var settingsMap map[string]interface{}
							if err := json.Unmarshal(decrypted, &settingsMap); err == nil {
								// Set autoConnect to false to prevent startup loop on first migration start
								settingsMap["autoConnect"] = false

								// Re-serialize and encrypt using native Go DPAPI
								if newJSON, err := json.Marshal(settingsMap); err == nil {
									if encryptedBytes, err := security.Encrypt(newJSON); err == nil {
										_ = os.WriteFile(newSettings, encryptedBytes, 0600)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// decryptElectronSafeStorage decrypts Electron safeStorage DPAPI encrypted strings.
// Electron safeStorage prepends a "v10" prefix (0x76, 0x31, 0x30) to the DPAPI payload on Windows.
func decryptElectronSafeStorage(data []byte) ([]byte, error) {
	if len(data) > 3 && string(data[:3]) == "v10" {
		data = data[3:]
	}
	return security.Decrypt(data)
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

func killExistingInstances() {
	currentPID := os.Getpid()
	currentExecutable, err := os.Executable()
	if err != nil {
		return
	}
	execName := filepath.Base(currentExecutable)

	processes, err := ps.Processes()
	if err != nil {
		return
	}

	for _, p := range processes {
		if p.Pid() != currentPID && p.Executable() == execName {
			proc, err := os.FindProcess(p.Pid())
			if err == nil {
				_ = proc.Kill()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

