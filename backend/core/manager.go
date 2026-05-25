package core

import (
	"context"
	"fmt"
	"sync"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	sclog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
)

// CoreManager controls the lifecycle of the embedded sing-box VPN engine.
type CoreManager struct {
	mu       sync.Mutex
	instance *box.Box
	cancel   context.CancelFunc
}

// NewCoreManager creates a new instance of CoreManager.
func NewCoreManager() *CoreManager {
	return &CoreManager{}
}

// Start parses the provided JSON configuration and starts the sing-box instance.
func (m *CoreManager) Start(configJSON string, logWriter sclog.PlatformWriter) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If there's an active instance, stop it first.
	if m.instance != nil {
		m.stopRaw()
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
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	m.instance = instance
	m.cancel = cancel
	return nil
}

// Stop stops the currently running sing-box instance.
func (m *CoreManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopRaw()
}

// stopRaw performs context cancellation and resource cleanup.
func (m *CoreManager) stopRaw() error {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.instance != nil {
		err := m.instance.Close()
		m.instance = nil
		return err
	}
	return nil
}

// IsRunning checks whether the sing-box instance is active.
func (m *CoreManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instance != nil
}
