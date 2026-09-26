//go:build windows

package detect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives      = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW         = kernel32.NewProc("GetDriveTypeW")
	procGetVolumeInformationW = kernel32.NewProc("GetVolumeInformationW")
)

const (
	driveRemovable = 2
	driveRemote    = 4
	driveCDROM     = 5
	maxVolumeLen   = 261
)

// Drive is a removable drive containing camera media folders.
type Drive struct {
	Letter      string // "E:\\"
	Label       string
	Model       string // correlated from Win32_DiskDrive when available
	PNPDeviceID string
}

// mediaRootFolders mark a drive as camera media.
var mediaRootFolders = []string{"DCIM", "PRIVATE"}

// ScanDrives enumerates removable drives that look like camera cards.
func ScanDrives(ctx context.Context) ([]Drive, error) {
	mask, _, err := procGetLogicalDrives.Call()
	if mask == 0 {
		return nil, err
	}

	var drives []Drive
	for i := 0; i < 26; i++ {
		if err := ctx.Err(); err != nil {
			return drives, err
		}
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + `:\`
		if driveType(letter) != driveRemovable {
			continue
		}
		if !hasCameraFolders(letter) {
			continue
		}
		drives = append(drives, Drive{
			Letter: letter,
			Label:  volumeLabel(letter),
		})
	}
	return drives, nil
}

func driveType(root string) uintptr {
	ptr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0
	}
	ret, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(ptr)))
	return ret
}

func hasCameraFolders(root string) bool {
	for _, folder := range mediaRootFolders {
		info, err := os.Stat(filepath.Join(root, folder))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func volumeLabel(root string) string {
	ptr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	buf := make([]uint16, maxVolumeLen)
	ret, _, _ := procGetVolumeInformationW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0, 0,
	)
	if ret == 0 {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}
