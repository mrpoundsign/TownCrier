//go:build windows

package app

import (
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	idleUser32           = syscall.NewLazyDLL("user32.dll")
	idleKernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetLastInputInfo = idleUser32.NewProc("GetLastInputInfo")
	procGetTickCount     = idleKernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// SystemIdleDuration returns the elapsed duration since the last system-wide keyboard or mouse input on Windows.
func SystemIdleDuration() time.Duration {
	if testing.Testing() {
		return 0
	}

	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))

	ret, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if ret == 0 {
		return 0
	}

	nowTicks, _, _ := procGetTickCount.Call()
	elapsedMs := uint32(nowTicks) - lii.dwTime
	return time.Duration(elapsedMs) * time.Millisecond
}
