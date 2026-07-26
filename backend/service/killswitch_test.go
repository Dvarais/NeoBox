package service

import (
	"os"
	"testing"
)

// The marker is the only thing that tells a fresh start that a previous session
// died with firewall rules installed, so its lifecycle is what makes recovery
// possible at all.
func TestKillSwitchMarkerLifecycle(t *testing.T) {
	s := newTestService(t)

	if _, err := os.Stat(s.killSwitchMarkerPath()); !os.IsNotExist(err) {
		t.Fatal("a fresh data directory must not contain a marker")
	}

	if err := os.WriteFile(s.killSwitchMarkerPath(), []byte("2026-07-26T00:00:00Z"), 0600); err != nil {
		t.Fatalf("failed to seed the marker: %v", err)
	}
	if _, err := os.Stat(s.killSwitchMarkerPath()); err != nil {
		t.Fatalf("the seeded marker should exist: %v", err)
	}
}

// recoverKillSwitch must do nothing at all when no marker is present: it would
// otherwise spawn netsh on every single start for the majority of users, who
// never enable the Kill Switch.
func TestRecoverKillSwitchIsNoOpWithoutMarker(t *testing.T) {
	s := newTestService(t)

	// Must not panic, must not create anything.
	s.recoverKillSwitch()

	entries, err := os.ReadDir(s.userDataDir)
	if err != nil {
		t.Fatalf("failed to list the data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("recoverKillSwitch touched the data directory: %v", entries)
	}
}

// The marker path must live inside the user data directory, next to the other
// recovery state, rather than anywhere global.
func TestKillSwitchMarkerPathIsInDataDir(t *testing.T) {
	s := newTestService(t)

	got := s.killSwitchMarkerPath()
	if want := s.userDataDir; len(got) <= len(want) || got[:len(want)] != want {
		t.Errorf("marker path %q is not inside the data dir %q", got, want)
	}
}
