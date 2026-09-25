//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/service"
	syswindows "golang.org/x/sys/windows"
)

func closeInstanceHandle(h uintptr) {
	if h != 0 {
		_ = syswindows.CloseHandle(syswindows.Handle(h))
	}
}

func relaunchElevatedIfNeeded(userDataDir string, mutexHandle uintptr) (uintptr, bool) {
	if service.IsElevated() {
		return mutexHandle, false
	}

	data, err := os.ReadFile(filepath.Join(userDataDir, "settings.json"))
	if err != nil {
		return mutexHandle, false
	}
	var settings struct {
		TunMode    bool `json:"tunMode"`
		KillSwitch bool `json:"killSwitch"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return mutexHandle, false
	}
	if !settings.TunMode && !settings.KillSwitch {
		return mutexHandle, false
	}

	if mutexHandle != 0 {
		closeInstanceHandle(mutexHandle)
		mutexHandle = 0
	}

	if err := service.RelaunchElevated(); err != nil {
		fmt.Fprintf(os.Stderr, "Elevation declined or unavailable, continuing unelevated: %v\n", err)
		h, _ := service.AcquireSingleInstanceMutex()
		return h, false
	}
	return 0, true
}

func bringExistingInstanceToForeground() {
	user32 := syswindows.NewLazySystemDLL("user32.dll")
	procShowWindow := user32.NewProc("ShowWindow")
	procSetForegroundWindow := user32.NewProc("SetForegroundWindow")
	procIsIconic := user32.NewProc("IsIconic")

	hwnd := service.FindMainWindow()
	if hwnd != 0 {
		isMinimized, _, _ := procIsIconic.Call(hwnd)
		if isMinimized != 0 {
			_, _, _ = procShowWindow.Call(hwnd, 9)
		} else {
			_, _, _ = procShowWindow.Call(hwnd, 5)
		}
		_, _, _ = procSetForegroundWindow.Call(hwnd)
	}
}
