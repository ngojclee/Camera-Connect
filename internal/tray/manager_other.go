//go:build !windows

package tray

import "context"

// Manager is a no-op tray host on non-Windows platforms for now.
type Manager struct{}

// Options configures tray bootstrap behavior.
type Options struct {
	AppName      string
	AgentPID     int
	UIExecutable string
	PipeName     string
	StatusPath   string
}

// NewManager creates a no-op manager on non-Windows.
func NewManager(Options) *Manager {
	return &Manager{}
}

// Start does nothing on non-Windows.
func (m *Manager) Start(context.Context) error {
	return nil
}

// Stop does nothing on non-Windows.
func (m *Manager) Stop() error {
	return nil
}
