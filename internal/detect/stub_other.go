//go:build !windows

package detect

import "context"

// Drive is a removable drive containing camera media folders.
type Drive struct {
	Letter      string
	Label       string
	Model       string
	PNPDeviceID string
}

// PortableDevice is a WPD/MTP device (non-Windows stub).
type PortableDevice struct {
	PNPDeviceID string
	Model       string
	Service     string
}

// ScanDrives returns nil on non-Windows platforms (Phase 2 is Windows-first).
func ScanDrives(ctx context.Context) ([]Drive, error) { return nil, nil }

// ScanPortableDevices returns nil on non-Windows platforms.
func ScanPortableDevices(ctx context.Context) ([]PortableDevice, error) { return nil, nil }

// CorrelateDriveModels is a no-op on non-Windows.
func CorrelateDriveModels(_ []Drive, _ []PortableDevice) {}

// IsKnownCameraVID always reports false on non-Windows.
func IsKnownCameraVID(_ string) bool { return false }
