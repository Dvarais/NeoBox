package service

import (
	"encoding/json"
	"testing"
	"time"

	"NeoBox/backend/storage"
)

func newSettingsService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	return &AppService{userDataDir: dir}
}

// Saving a language change used to deadlock: SaveSettings held fileMu and called
// applyLanguage, which rebuilds the tray, and the rebuild takes fileMu itself to
// read the subscriptions. sync.Mutex is not reentrant, so the app froze on every
// language switch. The point of this test is that it TERMINATES.
func TestSaveSettingsWithLanguageChangeDoesNotDeadlock(t *testing.T) {
	s := newSettingsService(t)

	// Seed subscriptions so the tray rebuild actually reaches the file read that
	// contends for fileMu, rather than bailing out early on an absent file.
	subs := []Subscription{{
		ID:    "sub-1",
		Name:  "Test",
		Links: []string{"trojan://s3cr3t-password@example.org:443#node"},
	}}
	seed, err := json.Marshal(subs)
	if err != nil {
		t.Fatalf("failed to marshal seed: %v", err)
	}
	if !s.SaveSubscriptions(string(seed)) {
		t.Fatal("seeding SaveSubscriptions failed")
	}

	done := make(chan bool, 1)
	go func() {
		// Two different languages, so applyLanguage cannot short-circuit on
		// "already in this language" and skip the tray rebuild entirely.
		okRU := s.SaveSettings(`{"language":"RU","tunMode":true}`)
		okEN := s.SaveSettings(`{"language":"EN","tunMode":true}`)
		done <- okRU && okEN
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Error("SaveSettings reported failure on a language change")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SaveSettings deadlocked on a language change")
	}
}
