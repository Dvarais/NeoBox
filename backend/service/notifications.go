package service

import (
	"fmt"
	"os"
	"path/filepath"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"golang.org/x/sys/windows/registry"
)

// neoBoxAppID is the Windows Application User Model ID (AUMID) used to attribute
// toast notifications to NeoBox in the Action Center.
const neoBoxAppID = "NeoBox.VPN.Client"

// InitNotifications registers NeoBox in the Windows registry under
// HKCU\SOFTWARE\Classes\AppUserModelId so the OS can attribute toast
// notifications to the correct application name and icon.
// Must be called once at application startup (before sending any toasts).
func InitNotifications() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}

	keyPath := `SOFTWARE\Classes\AppUserModelId\` + neoBoxAppID
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		fmt.Printf("[notifications] failed to register AppID: %v\n", err)
		return
	}
	defer k.Close()

	_ = k.SetStringValue("DisplayName", "NeoBox VPN")
	// Point to the icon next to the executable (build/windows/icon.ico extracted at runtime)
	iconPath := filepath.Join(filepath.Dir(exePath), "icon.ico")
	_ = k.SetStringValue("IconUri", iconPath)
}

// sendToast sends a best-effort Windows toast notification.
// Errors are logged but never propagate — notifications are non-critical UI candy.
func sendToast(title, message string) {
	n := toast.Notification{
		AppID: neoBoxAppID,
		Title: title,
		Body:  message,
	}
	if err := n.Push(); err != nil {
		fmt.Printf("[notifications] toast failed (%q / %q): %v\n", title, message, err)
	}
}
