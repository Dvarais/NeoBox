//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/service"
)

func closeInstanceHandle(h uintptr) {}

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

	if err := service.RelaunchElevated(); err != nil {
		fmt.Fprintf(os.Stderr, "Elevation declined or unavailable, continuing unelevated: %v\n", err)
		return mutexHandle, false
	}
	return 0, true
}

func bringExistingInstanceToForeground() {}

