package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"NeoBox/backend/storage"
)

// newConcurrentService builds a service whose storage layer is initialised, so
// the subscription paths actually read and write encrypted files.
func newConcurrentService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	return &AppService{userDataDir: dir}
}

// AppService went from one mutex covering everything to four covering one
// concern each. Splitting a lock is how ordering bugs get introduced, and the
// specific hazard here was real: SaveSubscriptions rebuilds the tray right
// after writing, while every other tray path reads the file to rebuild. If the
// rebuild happened under trayMu, the two would take fileMu and trayMu in
// opposite orders.
//
// This test hammers those paths against each other. A lock-order inversion
// shows up as a deadlock, which the harness reports as a timeout rather than a
// failed assertion — so the point of the test is that it TERMINATES.
func TestConcurrentAccessDoesNotDeadlock(t *testing.T) {
	s := newConcurrentService(t)

	// Seed a subscription list so the rebuild path has real work to do.
	subs := []Subscription{{
		ID:    "sub-1",
		Name:  "Test",
		Links: []string{"vless://uuid@example.com:443#node-a", "trojan://pw@example.org:443#node-b"},
	}}
	seed, err := json.Marshal(subs)
	if err != nil {
		t.Fatalf("failed to marshal seed: %v", err)
	}
	if !s.SaveSubscriptions(string(seed)) {
		t.Fatal("seeding SaveSubscriptions failed")
	}

	const workers = 8
	const iterations = 40

	done := make(chan struct{})
	var wg sync.WaitGroup

	run := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				fn(i)
			}
		}()
	}

	for w := 0; w < workers; w++ {
		worker := w
		// Writers: take fileMu, then rebuild (fileMu again, then trayMu).
		run(func(i int) {
			list := []Subscription{{
				ID:    fmt.Sprintf("sub-%d-%d", worker, i),
				Name:  "Test",
				Links: []string{"vless://uuid@example.com:443#node"},
			}}
			payload, _ := json.Marshal(list)
			s.SaveSubscriptions(string(payload))
		})
		// Readers of the same file.
		run(func(int) { s.GetSubscriptions() })
		// The tray path in isolation: fileMu then trayMu.
		run(func(int) { s.RebuildTrayServers() })
		// Tray-only state.
		run(func(int) {
			s.SetWindowVisible(true)
			s.NotifyWindowHidden()
			s.UpdateTrayStatus("status")
		})
		// The other file under fileMu.
		run(func(int) {
			s.SaveSettings(`{"tunMode":false}`)
			s.GetSettings()
		})
		// Session state under stateMu.
		run(func(int) {
			s.SetQuitting(false)
			s.IsQuitting()
			s.StopAutoUpdateScheduler()
		})
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// Dump every goroutine: with a deadlock the stacks name the two locks.
		buf := make([]byte, 1<<20)
		n := runtimeStack(buf)
		t.Fatalf("deadlock: workers did not finish within 30s\n%s", buf[:n])
	}
}

// Reading and writing the same file concurrently must never yield a torn or
// partially-written result — the storage layer writes atomically, and fileMu
// serialises the callers.
func TestConcurrentSubscriptionAccessKeepsFileValid(t *testing.T) {
	s := newConcurrentService(t)

	subs := []Subscription{{ID: "sub-1", Name: "Test", Links: []string{"vless://uuid@example.com:443#n"}}}
	seed, _ := json.Marshal(subs)
	if !s.SaveSubscriptions(string(seed)) {
		t.Fatal("seeding failed")
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			list := []Subscription{{ID: fmt.Sprintf("s%d", i), Name: "T", Links: []string{"vless://u@h:443#n"}}}
			payload, _ := json.Marshal(list)
			s.SaveSubscriptions(string(payload))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			got := s.GetSubscriptions()
			if !json.Valid([]byte(got)) {
				t.Errorf("GetSubscriptions returned invalid JSON: %q", got)
				return
			}
		}
	}()

	wg.Wait()

	// No temporary files may survive a run of concurrent writes.
	entries, err := os.ReadDir(s.userDataDir)
	if err != nil {
		t.Fatalf("failed to list data dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

// runtimeStack wraps runtime.Stack so the import stays local to the helper.
func runtimeStack(buf []byte) int {
	return runtime.Stack(buf, true)
}
