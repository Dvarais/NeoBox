package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	sclog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
)

// DefaultCloseTimeout is the maximum duration to wait for sing-box (and the Wintun/TUN adapter)
// to tear down gracefully before force-proceeding.
const DefaultCloseTimeout = 4 * time.Second

// BoxInstance abstracts the sing-box instance lifecycle methods.
// Enables mock injection for unit testing without spawning native network drivers.
type BoxInstance interface {
	Start() error
	Close() error
}

// CoreManager controls the lifecycle of the embedded sing-box VPN engine.
type CoreManager struct {
	mu           sync.Mutex
	instance     BoxInstance
	cancel       context.CancelFunc
	closeTimeout time.Duration
	running      atomic.Bool
}

// NewCoreManager creates a new instance of CoreManager.
func NewCoreManager() *CoreManager {
	return &CoreManager{
		closeTimeout: DefaultCloseTimeout,
	}
}

// SetCloseTimeout configures the teardown timeout (primarily for tests).
func (m *CoreManager) SetCloseTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeTimeout = d
}

// closeInstanceWithTimeout closes a BoxInstance asynchronously within a deadline.
//
// [INTERNET SEARCH PATTERN]:
// Based on upstream sing-box / Wintun driver investigation:
// Under Windows, wintun.dll driver teardown or active packet processing loops in CGO can block
// synchronously during Close(). Calling Close() inside an isolated goroutine with panic recovery
// and a select timeout prevents permanent GUI/service thread deadlocks.
func closeInstanceWithTimeout(inst BoxInstance, timeout time.Duration) error {
	if inst == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during sing-box Close(): %v", r)
			}
		}()
		done <- inst.Close()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("sing-box Close() timed out after %v (driver or network teardown hung)", timeout)
	}
}

// Start parses the provided JSON configuration and starts the sing-box instance.
func (m *CoreManager) Start(configJSON string, logWriter sclog.PlatformWriter) error {
	if strings.TrimSpace(configJSON) == "" {
		return errors.New("empty configuration")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// If there's an active instance, stop it first.
	if m.instance != nil {
		_ = m.stopRaw()
	}

	// Create a cancelable context to control the core lifecycle.
	ctx, cancel := context.WithCancel(context.Background())
	ctx = include.Context(ctx)

	// Parse JSON config into sing-box options structure with context registries.
	opt, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(configJSON))
	if err != nil {
		cancel()
		return fmt.Errorf("failed to parse configuration: %w", err)
	}

	// Initialize the sing-box instance with default registries registered in the context.
	instance, err := box.New(box.Options{
		Context:           ctx,
		Options:           opt,
		PlatformLogWriter: logWriter,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to initialize sing-box: %w", err)
	}

	// Start the sing-box service.
	if err := instance.Start(); err != nil {
		cancel()
		timeout := m.closeTimeout
		if timeout <= 0 {
			timeout = DefaultCloseTimeout
		}
		_ = closeInstanceWithTimeout(instance, timeout)
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	m.instance = instance
	m.cancel = cancel
	m.running.Store(true)
	return nil
}

// Stop stops the currently running sing-box instance.
func (m *CoreManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopRaw()
}

// stopRaw performs context cancellation and resource cleanup.
//
// A Close() failure or timeout is logged but never returned: cleanup has still happened,
// and surfacing the error would make callers treat a completed teardown as a
// failure and refuse to restart.
func (m *CoreManager) stopRaw() error {
	m.running.Store(false)
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	inst := m.instance
	if inst == nil {
		return nil
	}
	// Detach instance reference immediately so subsequent operations never see a stale instance.
	m.instance = nil

	timeout := m.closeTimeout
	if timeout <= 0 {
		timeout = DefaultCloseTimeout
	}

	if err := closeInstanceWithTimeout(inst, timeout); err != nil {
		// Log the error/timeout but do NOT return it — a failed close must not block a restart.
		fmt.Printf("[CoreManager] warning: sing-box Close(): %v\n", err)
	}
	return nil
}

// IsRunning checks whether the sing-box instance is active.
// Uses lock-free atomic load so callers (UI, watchdog) never block during teardown.
func (m *CoreManager) IsRunning() bool {
	return m.running.Load()
}
