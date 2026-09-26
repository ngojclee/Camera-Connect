//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	winplatform "github.com/ngojclee/camera-connect/internal/platform/windows"
)

type uiSingleInstanceGuard interface {
	Release()
}

var (
	user32ProcFindWindowW         = syscall.NewLazyDLL("user32.dll").NewProc("FindWindowW")
	user32ProcShowWindow          = syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow")
	user32ProcSetForegroundWindow = syscall.NewLazyDLL("user32.dll").NewProc("SetForegroundWindow")
)

const (
	swRestore = 9
)

func acquireUISingleInstance() (uiSingleInstanceGuard, bool, error) {
	guard := winplatform.NewSingleInstance("CameraConnectUI_Mutex")
	acquired, err := guard.TryAcquire()
	if err != nil {
		return nil, false, err
	}
	if !acquired {
		return nil, false, nil
	}
	return guard, true, nil
}

func focusExistingUIWindow(runtimeMode string) error {
	titles := windowTitlesForRuntime(runtimeMode)
	for _, title := range titles {
		hwnd, err := findWindowByTitle(title)
		if err != nil {
			continue
		}
		_, _, _ = user32ProcShowWindow.Call(hwnd, swRestore)
		ret, _, callErr := user32ProcSetForegroundWindow.Call(hwnd)
		if ret == 0 && callErr != syscall.Errno(0) {
			return fmt.Errorf("set foreground window for title %q: %w", title, callErr)
		}
		return nil
	}
	return fmt.Errorf("window not found for runtime %q (titles tried: %s)", runtimeMode, strings.Join(titles, ", "))
}

func findWindowByTitle(title string) (uintptr, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, fmt.Errorf("encode window title: %w", err)
	}
	hwnd, _, _ := user32ProcFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd == 0 {
		return 0, fmt.Errorf("window with title %q not found", title)
	}
	return hwnd, nil
}

func windowTitlesForRuntime(runtimeMode string) []string {
	return []string{
		"CameraConnect",
		"Camera Connect",
	}
}
