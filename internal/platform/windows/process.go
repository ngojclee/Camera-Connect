//go:build windows

package windows

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	modKernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snapshot = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW           = modKernel32.NewProc("Process32NextW")
)

const (
	tH32CS_SNAPPROCESS = 0x00000002
	maxPath            = 260
)

type processEntry32W struct {
	Size              uint32
	Usage             uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	Threads           uint32
	ParentProcessID   uint32
	PriorityClassBase int32
	Flags             uint32
	ExeFile           [maxPath]uint16
}

// ProcessDetector detects running processes via Windows API.
type ProcessDetector struct{}

func NewProcessDetector() *ProcessDetector {
	return &ProcessDetector{}
}

// IsRunning checks if any of the given process names are running.
// Process names are matched case-insensitively.
func (d *ProcessDetector) IsRunning(processNames []string) (bool, error) {
	snap, _, err := procCreateToolhelp32Snapshot.Call(tH32CS_SNAPPROCESS, 0)
	if snap == ^uintptr(0) { // INVALID_HANDLE_VALUE
		return false, err
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var entry processEntry32W
	entry.Size = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return false, nil
	}

	// Build lowercase lookup set
	names := make(map[string]struct{}, len(processNames))
	for _, n := range processNames {
		names[strings.ToLower(n)] = struct{}{}
	}

	for {
		exeName := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		if _, ok := names[exeName]; ok {
			return true, nil
		}

		entry.Size = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}

	return false, nil
}
