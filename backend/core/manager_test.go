package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockBoxInstance implements BoxInstance for testing lifecycle and shutdown behavior.
type mockBoxInstance struct {
	mu           sync.Mutex
	startErr     error
	closeErr     error
	closeDelay   time.Duration
	closePanics  bool
	closeCalled  bool
	startCalled  bool
	closeBlockCh chan struct{}
}

func (m *mockBoxInstance) Start() error {
	m.mu.Lock()
	m.startCalled = true
	m.mu.Unlock()
	return m.startErr
}

func (m *mockBoxInstance) Close() error {
	m.mu.Lock()
	m.closeCalled = true
	m.mu.Unlock()

	if m.closePanics {
		panic("mock native driver panic during Close()")
	}
	if m.closeBlockCh != nil {
		<-m.closeBlockCh
	}
	if m.closeDelay > 0 {
		time.Sleep(m.closeDelay)
	}
	return m.closeErr
}

// setInstanceForTest injects a mock BoxInstance for testing lifecycle scenarios.
func (m *CoreManager) setInstanceForTest(inst BoxInstance, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instance = inst
	m.cancel = cancel
}

func TestCoreManager_Stop_Normal(t *testing.T) {
	mgr := NewCoreManager()
	mock := &mockBoxInstance{}
	ctx, cancel := context.WithCancel(context.Background())
	mgr.setInstanceForTest(mock, cancel)

	if !mgr.IsRunning() {
		t.Fatal("expected manager to be running after setInstanceForTest")
	}

	err := mgr.Stop()
	if err != nil {
		t.Fatalf("expected nil error from Stop(), got %v", err)
	}
	if mgr.IsRunning() {
		t.Fatal("expected manager to be not running after Stop()")
	}
	if !mock.closeCalled {
		t.Fatal("expected Close() to have been called on instance")
	}
	if ctx.Err() == nil {
		t.Fatal("expected context to be cancelled after Stop()")
	}
}

func TestCoreManager_Stop_TimeoutResilience(t *testing.T) {
	mgr := NewCoreManager()
	mgr.SetCloseTimeout(50 * time.Millisecond)

	// Mock instance whose Close() blocks until unblocked or hangs
	blockCh := make(chan struct{})
	defer close(blockCh) // unblock background goroutine when test finishes

	mock := &mockBoxInstance{closeBlockCh: blockCh}
	_, cancel := context.WithCancel(context.Background())
	mgr.setInstanceForTest(mock, cancel)

	start := time.Now()
	err := mgr.Stop()
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil error from Stop() even on timeout, got %v", err)
	}
	// Must return after roughly ~50ms timeout, NOT hanging indefinitely
	if duration < 40*time.Millisecond || duration > 300*time.Millisecond {
		t.Fatalf("Stop() took %v, expected ~50ms timeout window", duration)
	}
	if mgr.IsRunning() {
		t.Fatal("expected manager to be marked stopped even after timeout")
	}

	// Crucial: Mutex must be released so next operation can proceed immediately
	done := make(chan struct{})
	go func() {
		_ = mgr.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success: mutex was not deadlocked
	case <-time.After(100 * time.Millisecond):
		t.Fatal("deadlock: manager mutex remained locked after timeout")
	}
}

func TestCoreManager_Stop_PanicRecovery(t *testing.T) {
	mgr := NewCoreManager()
	mgr.SetCloseTimeout(100 * time.Millisecond)

	mock := &mockBoxInstance{closePanics: true}
	mgr.setInstanceForTest(mock, func() {})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CoreManager.Stop() did not recover from instance panic: %v", r)
		}
	}()

	err := mgr.Stop()
	if err != nil {
		t.Fatalf("expected nil error from Stop(), got %v", err)
	}
	if mgr.IsRunning() {
		t.Fatal("expected manager to be marked stopped after panic in Close()")
	}
}

func TestCoreManager_Stop_Idempotent(t *testing.T) {
	mgr := NewCoreManager()
	for i := 0; i < 3; i++ {
		if err := mgr.Stop(); err != nil {
			t.Fatalf("iteration %d: unexpected error %v", i, err)
		}
		if mgr.IsRunning() {
			t.Fatalf("iteration %d: expected manager not running", i)
		}
	}
}

func TestCoreManager_Stop_CloseErrorIgnored(t *testing.T) {
	mgr := NewCoreManager()
	mock := &mockBoxInstance{closeErr: errors.New("wintun driver busy")}
	mgr.setInstanceForTest(mock, func() {})

	err := mgr.Stop()
	if err != nil {
		t.Fatalf("expected nil error because Close() failure must not block restart, got: %v", err)
	}
	if mgr.IsRunning() {
		t.Fatal("expected manager to be marked stopped")
	}
}
