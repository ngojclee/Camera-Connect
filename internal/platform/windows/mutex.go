//go:build windows

package windows

import (
	"syscall"
	"unsafe"
)

var (
	procCreateMutexW = modKernel32.NewProc("CreateMutexW")
)

const errorAlreadyExists = 183

// SingleInstance implements single-instance guard via Windows named mutex.
type SingleInstance struct {
	name   string
	handle syscall.Handle
}

func NewSingleInstance(name string) *SingleInstance {
	return &SingleInstance{name: name}
}

// TryAcquire attempts to create a named mutex.
// Returns true if this is the first instance, false if another already holds it.
func (s *SingleInstance) TryAcquire() (bool, error) {
	namePtr, err := syscall.UTF16PtrFromString(s.name)
	if err != nil {
		return false, err
	}

	h, _, err := procCreateMutexW.Call(
		0,
		0,
		uintptr(unsafe.Pointer(namePtr)),
	)
	if h == 0 {
		return false, err
	}

	s.handle = syscall.Handle(h)

	// If ERROR_ALREADY_EXISTS, another instance owns the mutex
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		return false, nil
	}

	return true, nil
}

// Release closes the mutex handle.
func (s *SingleInstance) Release() {
	if s.handle != 0 {
		syscall.CloseHandle(s.handle)
		s.handle = 0
	}
}
