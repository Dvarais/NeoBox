package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/storage"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Subscriptions: encrypted storage, fetching and the auto-update scheduler.

type Subscription struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Links   []string `json:"links"`
	Loading bool     `json:"loading"`
}

// subscriptionsPath returns the location of the encrypted subscriptions file.
func (s *AppService) subscriptionsPath() string {
	return filepath.Join(s.userDataDir, "subscriptions.json")
}

// readSubscriptionsLocked returns the decrypted subscriptions JSON, or nil when
// the file is absent, unreadable, or does not contain valid JSON.
// Callers must already hold fileMu.
func (s *AppService) readSubscriptionsLocked() []byte {
	data, found, err := storage.ReadSecret(s.subscriptionsPath())
	if err != nil {
		fmt.Printf("[subscriptions] read error: %v\n", err)
		return nil
	}
	if !found {
		return nil
	}
	if !json.Valid(data) {
		fmt.Println("[subscriptions] file contains invalid JSON — treating as empty")
		return nil
	}
	return data
}

// GetSubscriptions returns the stored subscriptions as a JSON string.
//
// The file itself is encrypted at rest (AES-256-GCM under a DPAPI-protected
// key) because it holds full proxy links — UUIDs and passwords — so it is NOT
// user-editable, unlike settings.json.
// NOTE: GetSubscriptions is a pure read — it never writes to disk.
func (s *AppService) GetSubscriptions() string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	data := s.readSubscriptionsLocked()
	if data == nil {
		return "[]"
	}
	rawJSON := string(data)

	// Filter out the NeoBox Free bootstrap subscription in-memory only.
	// NOTE: we do NOT write back to disk here — see purgeBootstrapSub().
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err == nil {
		hasBootstrap := false
		cleanedSubs := subs[:0]
		for _, sub := range subs {
			if sub.ID == "bootstrap-free-subs" {
				hasBootstrap = true
			} else {
				cleanedSubs = append(cleanedSubs, sub)
			}
		}
		if hasBootstrap {
			if merged, err := json.Marshal(cleanedSubs); err == nil {
				rawJSON = string(merged)
			}
		}
	}

	return rawJSON
}

// SaveSubscriptions encrypts and saves the subscription list.
// It also purges the bootstrap subscription from disk if present.
func (s *AppService) SaveSubscriptions(subsJSON string) bool {
	// Purge bootstrap sub from the JSON being saved.
	subsJSON = purgeBootstrapSub(subsJSON)

	// Validate JSON before writing
	if !json.Valid([]byte(subsJSON)) {
		fmt.Println("[SaveSubscriptions] refusing to write invalid JSON")
		return false
	}

	// Indent the payload before sealing it. The file on disk is ciphertext, but
	// the plaintext stays readable for anyone debugging via an export.
	var pretty []interface{}
	var out []byte
	if err := json.Unmarshal([]byte(subsJSON), &pretty); err == nil {
		out, _ = json.MarshalIndent(pretty, "", "  ")
	}
	if out == nil {
		out = []byte(subsJSON)
	}

	s.fileMu.Lock()
	err := storage.WriteSecret(s.subscriptionsPath(), out)
	s.fileMu.Unlock()
	if err != nil {
		fmt.Printf("[SaveSubscriptions] write failed: %v\n", err)
		return false
	}

	// Rebuild AFTER releasing fileMu. RebuildTrayServers takes fileMu itself to
	// read the list back and then trayMu to apply it; doing that while still
	// holding fileMu would nest the two locks and invert the ordering used by
	// every other tray path.
	s.RebuildTrayServers()
	return true
}

// purgeBootstrapSub removes the "bootstrap-free-subs" entry from a JSON subscription list.
func purgeBootstrapSub(subsJSON string) string {
	var subs []Subscription
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return subsJSON
	}
	cleaned := subs[:0]
	for _, sub := range subs {
		if sub.ID != "bootstrap-free-subs" {
			cleaned = append(cleaned, sub)
		}
	}
	if len(cleaned) == len(subs) {
		return subsJSON // nothing removed
	}
	if merged, err := json.Marshal(cleaned); err == nil {
		return string(merged)
	}
	return subsJSON
}

// FetchSubscription loads subscription contents from subscription URL.
//
// The error is returned rather than swallowed: Wails rejects the JavaScript
// promise with its text, which is how the user finds out that a subscription
// came back as a login page or in a format NeoBox cannot read. Silently
// returning an empty list left the interface with nothing to say.
func (s *AppService) FetchSubscription(url string) ([]string, error) {
	return core.FetchSubscription(url)
}

// ImportClipboard filters proxy links from raw clipboard string.
func (s *AppService) ImportClipboard(text string) []string {
	var links []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if core.IsProxyLink(trimmed) {
			links = append(links, trimmed)
		}
	}
	return links
}

// StartAutoUpdateScheduler runs a background loop to update all subscriptions
// every 24 hours if the auto-update setting is enabled, starting once shortly
// after launch.
//
// The loop is context-driven so shutdown can stop it: the cancel function lives
// in s.cancelAutoUpdate and is invoked by StopAutoUpdateScheduler.
func (s *AppService) StartAutoUpdateScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stateMu.Lock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate() // stop any previous scheduler
	}
	s.cancelAutoUpdate = cancel
	s.stateMu.Unlock()

	go func() {
		// Wait 5 seconds after startup to let the app initialize
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}

		for {
			// Check if auto-update is enabled in settings
			settingsJSON := s.GetSettings()
			var settings map[string]interface{}
			_ = json.Unmarshal([]byte(settingsJSON), &settings)

			autoUpdate, _ := settings["autoUpdateSubs"].(bool)
			if autoUpdate {
				s.UpdateAllSubscriptions()
			}

			// Wait 24 hours or until shutdown
			select {
			case <-time.After(24 * time.Hour):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopAutoUpdateScheduler stops the background auto-update goroutine.
// Should be called during application shutdown.
func (s *AppService) StopAutoUpdateScheduler() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate()
		s.cancelAutoUpdate = nil
	}
}

// UpdateAllSubscriptions downloads the latest links for all subscriptions.
func (s *AppService) UpdateAllSubscriptions() {
	subsJSON := s.GetSubscriptions()
	var subs []map[string]interface{}
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return
	}

	updatedAny := false
	for _, sub := range subs {
		url, _ := sub["url"].(string)
		if url == "" {
			continue
		}

		links, err := core.FetchSubscription(url)
		if err == nil && len(links) > 0 {
			sub["links"] = links
			updatedAny = true
		}
	}

	if updatedAny {
		newSubsJSON, err := json.Marshal(subs)
		if err == nil {
			s.SaveSubscriptions(string(newSubsJSON))

			// Notify frontend that subscriptions have been updated
			s.wailsCtxMu.RLock()
			wCtxSubs := s.wailsCtx
			s.wailsCtxMu.RUnlock()
			if wCtxSubs != nil {
				wailsruntime.EventsEmit(wCtxSubs, "subscriptions-updated", nil)
			}
		}
	}
}
