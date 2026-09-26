//go:build windows

package windows

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const (
	registryKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	registryName = "CameraConnect"
)

// StartupManager manages Windows "Start with Windows" via registry.
type StartupManager struct{}

func NewStartupManager() *StartupManager {
	return &StartupManager{}
}

// SetEnabled adds or removes the auto-start registry entry.
func (m *StartupManager) SetEnabled(enabled bool, exePath string, minimized bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry key: %w", err)
	}
	defer key.Close()

	if !enabled {
		// Ignore error if value doesn't exist
		_ = key.DeleteValue(registryName)
		return nil
	}

	value := fmt.Sprintf(`"%s"`, exePath)
	if minimized {
		value += " --minimized"
	}

	if err := key.SetStringValue(registryName, value); err != nil {
		return fmt.Errorf("set registry value: %w", err)
	}
	return nil
}

// IsEnabled checks if the auto-start entry exists.
func (m *StartupManager) IsEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.QUERY_VALUE)
	if err != nil {
		return false, nil
	}
	defer key.Close()

	_, _, err = key.GetStringValue(registryName)
	if err != nil {
		return false, nil
	}
	return true, nil
}
