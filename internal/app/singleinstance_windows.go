//go:build windows

package app

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

const errorAlreadyExists syscall.Errno = 183

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = modkernel32.NewProc("CreateMutexW")
	procCloseHandle = modkernel32.NewProc("CloseHandle")
)

func acquirePlatformInstanceLock(name string) (func(), error) {
	mutexName, err := syscall.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, fmt.Errorf("invalid mutex name %q: %w", name, err)
	}

	handle, _, lastErr := procCreateMutex.Call(
		0,
		0,
		uintptr(unsafe.Pointer(mutexName)),
	)
	if handle == 0 {
		return nil, fmt.Errorf("CreateMutexW failed: %w", lastErr)
	}

	if errors.Is(lastErr, errorAlreadyExists) {
		_, _, _ = procCloseHandle.Call(handle)
		return nil, ErrInstanceAlreadyRunning
	}

	return func() {
		_, _, _ = procCloseHandle.Call(handle)
	}, nil
}
