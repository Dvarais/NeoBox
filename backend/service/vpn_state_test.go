package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/storage"
)

func TestVPNState_TransitionsAndString(t *testing.T) {
	tests := []struct {
		state    VPNState
		expected string
	}{
		{VPNStateDisconnected, "disconnected"},
		{VPNStateConnecting, "connecting"},
		{VPNStateConnected, "connected"},
		{VPNStateDisconnecting, "disconnecting"},
		{VPNState(99), "unknown"},
	}

	for _, tc := range tests {
		if tc.state.String() != tc.expected {
			t.Errorf("state %d: expected string %q, got %q", tc.state, tc.expected, tc.state.String())
		}
	}
}

func TestVPNStateMachine_ConcurrentStart_ReentrancyBlocked(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	s := &AppService{
		userDataDir: dir,
		coreManager: core.NewCoreManager(),
	}

	// Initially Disconnected
	if s.GetVPNState() != VPNStateDisconnected {
		t.Fatalf("expected initial state Disconnected, got %v", s.GetVPNState())
	}

	const callers = 10
	var wg sync.WaitGroup
	var acceptedStarts int32

	// Simulate 10 simultaneous clicks on "Connect"
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Try starting with an invalid link (we test the state machine gate, not the tunnel)
			res := s.StartXray("invalid-link", "", false)
			// If it passed the state machine gate, res["error"] will NOT say "cannot start VPN: already ..."
			errStr, _ := res["error"].(string)
			if errStr != fmt.Sprintf("cannot start VPN: already %s", VPNStateConnecting) &&
				errStr != fmt.Sprintf("cannot start VPN: already %s", VPNStateConnected) {
				atomic.AddInt32(&acceptedStarts, 1)
			}
		}(i)
	}

	wg.Wait()

	// Exactly ONE caller must be admitted through the state transition gate!
	if acceptedStarts != 1 {
		t.Fatalf("expected exactly 1 admitted StartXray call, got %d", acceptedStarts)
	}

	// After failed start (invalid link), state must reset back to Disconnected
	if s.GetVPNState() != VPNStateDisconnected {
		t.Fatalf("expected state to reset to Disconnected after failed start, got %v", s.GetVPNState())
	}
}

func TestVPNStateMachine_ConcurrentStop_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	s := &AppService{
		userDataDir: dir,
		coreManager: core.NewCoreManager(),
	}

	// Manually set to Connected
	s.setVPNState(VPNStateConnected)

	const callers = 10
	var wg sync.WaitGroup
	var successes int32

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := s.StopXray()
			if succ, _ := res["success"].(bool); succ {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}

	wg.Wait()

	// Final state must be Disconnected
	if s.GetVPNState() != VPNStateDisconnected {
		t.Fatalf("expected final state Disconnected, got %v", s.GetVPNState())
	}

	// Calling StopXray on already Disconnected should succeed idempotently
	res := s.StopXray()
	if succ, _ := res["success"].(bool); !succ {
		t.Fatalf("expected idempotent StopXray to succeed, got %v", res)
	}
}

func TestVPNStateMachine_StopWhileConnecting(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	s := &AppService{
		userDataDir: dir,
		coreManager: core.NewCoreManager(),
	}

	// Simulate that connection is currently Connecting
	s.setVPNState(VPNStateConnecting)

	// User clicks Disconnect while Connecting
	res := s.StopXray()
	if succ, _ := res["success"].(bool); !succ {
		t.Fatalf("expected StopXray while Connecting to succeed, got %v", res)
	}

	if s.GetVPNState() != VPNStateDisconnected {
		t.Fatalf("expected state Disconnected after StopXray, got %v", s.GetVPNState())
	}
}

func TestVPNStateMachine_RapidToggleRace(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	s := &AppService{
		userDataDir: dir,
		coreManager: core.NewCoreManager(),
	}

	// Race StartXray and StopXray from multiple goroutines simultaneously
	const iters = 50
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.StartXray("invalid-link", "", false)
			time.Sleep(time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.StopXray()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()

	// Must never deadlock and final state must be valid
	finalState := s.GetVPNState()
	if finalState != VPNStateDisconnected && finalState != VPNStateConnected {
		t.Fatalf("expected clean end state Disconnected or Connected, got %v", finalState)
	}
}

func TestVPNStateMachine_RestartGuards(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	s := &AppService{
		userDataDir: dir,
		coreManager: core.NewCoreManager(),
	}

	// 1. Restart while Disconnecting should fail cleanly
	s.setVPNState(VPNStateDisconnecting)
	res := s.RestartXray("dummy", "", false)
	if succ, _ := res["success"].(bool); succ {
		t.Fatal("expected RestartXray to fail while Disconnecting")
	}

	// 2. Restart while Connecting should also fail cleanly
	s.setVPNState(VPNStateConnecting)
	res = s.RestartXray("dummy", "", false)
	if succ, _ := res["success"].(bool); succ {
		t.Fatal("expected RestartXray to fail while Connecting")
	}
}


