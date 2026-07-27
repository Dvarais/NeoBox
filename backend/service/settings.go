package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NeoBox/backend/security"
	"NeoBox/backend/storage"
)

// Application settings. They live in two files:
//
//	settings.json  plain, user-editable JSON — routing rules, DNS, toggles
//	state.json     encrypted, the fields that carry proxy credentials
//
// The split exists because those two kinds of data have opposite requirements
// and used to share one file. settings.json held lastSelectedServer and
// favoriteLinks — full vless:// / trojan:// / ss:// links, complete with UUIDs
// and passwords. That was a plaintext copy of exactly what subscriptions.json is
// encrypted to protect, so encrypting the subscriptions bought very little.
//
// The two halves are merged back together by GetSettings, so every caller —
// including the frontend — still sees one flat settings object and none of them
// needed changing.

// secretSettingKeys are the settings fields whose values are proxy links. These
// are the fields that move to the encrypted file; everything else in settings
// (toggles, DNS choice, domain and process lists) carries no credentials and is
// deliberately left readable so users can edit it by hand.
var secretSettingKeys = []string{"lastSelectedServer", "favoriteLinks"}

// settingsPath returns the location of the plaintext settings file.
func (s *AppService) settingsPath() string {
	return filepath.Join(s.userDataDir, "settings.json")
}

// statePath returns the location of the encrypted settings file.
func (s *AppService) statePath() string {
	return filepath.Join(s.userDataDir, "state.json")
}

// readPlainSettingsLocked parses settings.json. A missing, unreadable or
// malformed file yields an empty map so the app falls back to defaults rather
// than refusing to start. Callers must already hold fileMu.
func (s *AppService) readPlainSettingsLocked() map[string]interface{} {
	data, err := os.ReadFile(s.settingsPath())
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Printf("[settings] read error: %v\n", err)
		}
		return map[string]interface{}{}
	}

	var settings map[string]interface{}
	// Unmarshalling into a map rather than calling json.Valid also rejects a file
	// that parses but is not an object — an array or a bare string would other-
	// wise pass validation and then break every consumer downstream.
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		fmt.Println("[settings] settings.json contains invalid JSON — returning defaults")
		return map[string]interface{}{}
	}
	return settings
}

// writePlainSettingsLocked writes settings.json pretty-printed, so the file stays
// something a user can open in a text editor. The write goes through
// storage.WriteFile to make it atomic: settings.json is rewritten on every
// toggle, and a crash midway through would otherwise leave a truncated file that
// reads as "no settings at all". Callers must already hold fileMu.
func (s *AppService) writePlainSettingsLocked(settings map[string]interface{}) error {
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}
	return storage.WriteFile(s.settingsPath(), out)
}

// readSecretSettingsLocked decrypts state.json. A missing file is the normal
// state on a fresh install — it is only written once the user picks a server or
// stars one. A file that fails to decrypt has already been quarantined by
// ReadSecret; returning an empty map then costs the user their selection and
// favourites but leaves the rest of their settings intact.
// Callers must already hold fileMu.
func (s *AppService) readSecretSettingsLocked() map[string]interface{} {
	data, found, err := storage.ReadSecret(s.statePath())
	if err != nil {
		fmt.Printf("[settings] state.json read error: %v\n", err)
		return map[string]interface{}{}
	}
	if !found {
		return map[string]interface{}{}
	}

	var secrets map[string]interface{}
	if err := json.Unmarshal(data, &secrets); err != nil || secrets == nil {
		fmt.Println("[settings] state.json contains invalid JSON — treating as empty")
		return map[string]interface{}{}
	}
	return secrets
}

// writeSecretSettingsLocked encrypts secrets into state.json. An empty set is
// still written out as "{}" rather than skipped: leaving the previous file in
// place would resurrect credentials the user has just cleared.
// Callers must already hold fileMu.
func (s *AppService) writeSecretSettingsLocked(secrets map[string]interface{}) error {
	data, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to encode secret settings: %w", err)
	}
	return storage.WriteSecret(s.statePath(), data)
}

// splitSecretSettings removes the credential-bearing fields from settings and
// returns them, leaving settings with only what is safe to store in the clear.
func splitSecretSettings(settings map[string]interface{}) map[string]interface{} {
	secrets := make(map[string]interface{}, len(secretSettingKeys))
	for _, key := range secretSettingKeys {
		if value, ok := settings[key]; ok {
			secrets[key] = value
			delete(settings, key)
		}
	}
	return secrets
}

// migrateSecretSettings moves credential fields left in settings.json by a build
// from before the split into the encrypted file, and strips them from the
// plaintext one.
//
// It runs once at startup rather than lazily on the next save, for the same
// reason storage.Init migrates eagerly: waiting for the user to happen to change
// a setting would leave their proxy credentials sitting in a world-readable file
// indefinitely, and a user who never touches the settings page would never
// trigger it at all.
//
// Failures are logged, not returned. A migration that could not complete leaves
// settings.json exactly as it was, so the app keeps working off the plaintext
// copy; the next save rewrites the file without those fields anyway.
func (s *AppService) migrateSecretSettings() {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	settings := s.readPlainSettingsLocked()
	legacy := splitSecretSettings(settings)
	if len(legacy) == 0 {
		return
	}

	secrets := s.readSecretSettingsLocked()
	for key, value := range legacy {
		// state.json wins where both files carry a field: it is what every save
		// since the split has been writing to, so the plaintext copy is the stale
		// one.
		if _, exists := secrets[key]; !exists {
			secrets[key] = value
		}
	}

	if err := s.writeSecretSettingsLocked(secrets); err != nil {
		fmt.Printf("[settings] warning: could not encrypt credentials from settings.json: %v\n", err)
		return
	}
	// Only now is it safe to drop the plaintext copy — the encrypted file is on
	// disk and readable.
	if err := s.writePlainSettingsLocked(settings); err != nil {
		fmt.Printf("[settings] warning: could not strip credentials from settings.json: %v\n", err)
		return
	}
	fmt.Printf("[settings] moved %d credential field(s) from settings.json into encrypted state.json\n", len(legacy))
}

// GetSettings returns the user's settings as a JSON string, merging the
// plaintext half with the decrypted half. It is a pure read — it never writes to
// disk; moving credentials out of settings.json is migrateSecretSettings' job
// and happens once at startup.
func (s *AppService) GetSettings() string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	settings := s.readPlainSettingsLocked()

	// Overlay rather than merge-if-absent: if migration has not run or could not
	// finish, settings.json may still hold a stale plaintext copy of these fields
	// and the encrypted file is the authoritative one.
	for key, value := range s.readSecretSettingsLocked() {
		settings[key] = value
	}

	out, err := json.Marshal(settings)
	if err != nil {
		fmt.Printf("[GetSettings] encode error: %v\n", err)
		return "{}"
	}
	return string(out)
}

// SaveSettings splits the incoming settings and writes both halves: credentials
// encrypted into state.json, everything else as plain, human-readable JSON.
//
// NOTE: the credential half is replaced wholesale, not merged. The frontend's
// collectAndSaveSettings always sends the complete settings object, so a field
// absent from settingsJSON means the user cleared it. Anything that saves a
// partial object would erase their favourites.
func (s *AppService) SaveSettings(settingsJSON string) bool {
	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil || settings == nil {
		fmt.Println("[SaveSettings] refusing to write invalid JSON")
		return false
	}

	// Apply autostart update if needed based on settings changes. This touches the
	// registry, not the settings files, so it needs no lock.
	openAtLogin, _ := settings["openAtLogin"].(bool)
	if exePath, err := os.Executable(); err == nil {
		alreadyEnabled := security.IsAutostartEnabled("NeoBox")
		if openAtLogin && !alreadyEnabled {
			_ = security.SetupAutostart("NeoBox", exePath)
		} else if !openAtLogin && alreadyEnabled {
			_ = security.RemoveAutostart("NeoBox")
		}
	}

	lang, hasLang := settings["language"].(string)
	secrets := splitSecretSettings(settings)

	if err := func() error {
		s.fileMu.Lock()
		defer s.fileMu.Unlock()
		// Credentials first: if this fails the save is reported as failed and
		// settings.json is left untouched, so the two files cannot drift apart
		// with the user believing the save succeeded.
		if err := s.writeSecretSettingsLocked(secrets); err != nil {
			return fmt.Errorf("failed to write encrypted state: %w", err)
		}
		if err := s.writePlainSettingsLocked(settings); err != nil {
			return fmt.Errorf("failed to write settings.json: %w", err)
		}
		return nil
	}(); err != nil {
		fmt.Printf("[SaveSettings] %v\n", err)
		return false
	}

	// Keep the backend's language in step with the interface. The tray menu,
	// toasts and diagnostics are rendered in Go and never pass through the
	// frontend's translation table, so this is what stops them being stuck in
	// whatever language the app started in.
	//
	// This MUST stay outside the lock above. applyLanguage rebuilds the tray,
	// and that path takes fileMu itself to read the subscriptions — calling it
	// while holding fileMu deadlocked the app on every language switch, since
	// sync.Mutex is not reentrant.
	if hasLang {
		s.applyLanguage(lang)
	}
	return true
}
