// Package common defines cross-platform interfaces for OS-specific operations.
// Implementations live in sibling packages (e.g. platform/windows).
package common

// ProcessDetector checks whether a named process is running.
type ProcessDetector interface {
	IsRunning(processNames []string) (bool, error)
}

// StartupManager manages "start with OS" registration.
type StartupManager interface {
	SetEnabled(enabled bool, exePath string, minimized bool) error
	IsEnabled() (bool, error)
}

// SingleInstance ensures only one copy of the application runs.
type SingleInstance interface {
	// TryAcquire attempts to become the single instance.
	// Returns true if acquired, false if another instance is running.
	TryAcquire() (bool, error)
	// Release releases the single-instance lock.
	Release()
}

// Notifier sends OS-native notifications (toast, balloon, etc.).
type Notifier interface {
	Notify(title, message string) error
}
