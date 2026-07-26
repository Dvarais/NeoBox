package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/security"
)

// Application settings, stored as plain user-editable JSON.

// GetSettings reads settings.json and returns its contents as a JSON string.
// The file is plain JSON — users can edit it directly in a text editor.
func (s *AppService) GetSettings() string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	filePath := filepath.Join(s.userDataDir, "settings.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "{}"
		}
		fmt.Printf("[GetSettings] read error: %v\n", err)
		return "{}"
	}
	// Validate it is parseable JSON before returning
	if !json.Valid(data) {
		fmt.Println("[GetSettings] settings.json contains invalid JSON — returning defaults")
		return "{}"
	}
	return string(data)
}

// SaveSettings saves settings to settings.json as plain, human-readable JSON.
// Users can open and edit this file directly in any text editor.
func (s *AppService) SaveSettings(settingsJSON string) bool {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	filePath := filepath.Join(s.userDataDir, "settings.json")

	// Validate JSON before writing to avoid corrupting the file
	if !json.Valid([]byte(settingsJSON)) {
		fmt.Println("[SaveSettings] refusing to write invalid JSON")
		return false
	}

	// Apply autostart update if needed based on settings changes
	var settingsMap map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &settingsMap); err == nil {
		// Keep the backend's language in step with the interface. The tray menu,
		// toasts and diagnostics are rendered in Go and never pass through the
		// frontend's translation table, so this is what stops them being stuck in
		// whatever language the app started in.
		if lang, ok := settingsMap["language"].(string); ok {
			s.applyLanguage(lang)
		}

		openAtLogin, _ := settingsMap["openAtLogin"].(bool)
		exePath, err := os.Executable()
		if err == nil {
			alreadyEnabled := security.IsAutostartEnabled("NeoBox")
			if openAtLogin && !alreadyEnabled {
				_ = security.SetupAutostart("NeoBox", exePath)
			} else if !openAtLogin && alreadyEnabled {
				_ = security.RemoveAutostart("NeoBox")
			}
		}
	}

	// Pretty-print for human readability
	var pretty map[string]interface{}
	var out []byte
	if err := json.Unmarshal([]byte(settingsJSON), &pretty); err == nil {
		out, _ = json.MarshalIndent(pretty, "", "  ")
	}
	if out == nil {
		out = []byte(settingsJSON)
	}

	err := os.WriteFile(filePath, out, 0644)
	return err == nil
}
