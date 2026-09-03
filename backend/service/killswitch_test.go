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

// Состояние Kill Switch, каким его видит интерфейс.
//
// Оно существует затем, чтобы «интернета нет» не выглядело как поломка
// провайдера: правила брандмауэра переживают процесс, который их поставил, и до
// сих пор единственным их следом был файл-маркер, о котором пользователь не
// знает.
func TestGetKillSwitchStateFollowsMarker(t *testing.T) {
	s := newTestService(t)

	state := s.GetKillSwitchState()
	if state["active"] != false {
		t.Errorf("без маркера active должно быть false, получено %v", state["active"])
	}
	if state["stuck"] != false {
		t.Errorf("на свежем запуске stuck должно быть false, получено %v", state["stuck"])
	}

	if err := os.WriteFile(s.killSwitchMarkerPath(), []byte("2026-08-13T00:00:00Z"), 0600); err != nil {
		t.Fatalf("не удалось создать маркер: %v", err)
	}
	if state := s.GetKillSwitchState(); state["active"] != true {
		t.Errorf("с маркером active должно быть true, получено %v", state["active"])
	}
}

// Застрявшие правила — худший сценарий продукта: машина без сети, приложение
// ещё даже не подключено, а причина до этой правки оставалась только в
// crash-логе. Флаг должен доезжать до интерфейса.
func TestGetKillSwitchStateReportsStuck(t *testing.T) {
	s := newTestService(t)

	s.stateMu.Lock()
	s.killSwitchStuck = true
	s.stateMu.Unlock()

	state := s.GetKillSwitchState()
	if state["stuck"] != true {
		t.Errorf("stuck не доехало до состояния: %v", state)
	}
}
