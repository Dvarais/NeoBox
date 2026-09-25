//go:build !windows

package service

import "os/exec"

// InitNotifications on non-Windows is a no-op.
func InitNotifications(userDataDir string) {}

// sendToast uses notify-send on Linux if present.
func sendToast(title, message string) {
	if _, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command("notify-send", title, message).Run()
	}
}
