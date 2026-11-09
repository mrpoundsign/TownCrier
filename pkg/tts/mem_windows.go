//go:build windows

package tts

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcess          = kernel32.NewProc("GetCurrentProcess")
	procGetProcessHeap             = kernel32.NewProc("GetProcessHeap")
	procHeapCompact                = kernel32.NewProc("HeapCompact")
	procSetProcessWorkingSetSizeEx = kernel32.NewProc("SetProcessWorkingSetSizeEx")
	procSetDllDirectoryW           = kernel32.NewProc("SetDllDirectoryW")
)

func init() {
	// Register local build/bin with SetDllDirectoryW so that bundled onnxruntime.dll
	// takes precedence over Windows 11 system version in C:\Windows\System32 during unit tests.
	for _, rel := range []string{"build/bin", "../../build/bin", "../build/bin"} {
		if abs, err := filepath.Abs(rel); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "onnxruntime.dll")); err == nil {
				if ptr, err := syscall.UTF16PtrFromString(abs); err == nil {
					_, _, _ = procSetDllDirectoryW.Call(uintptr(unsafe.Pointer(ptr)))
					break
				}
			}
		}
	}
}

// ReclaimOSMemory triggers a Go garbage collection and instructs the Windows kernel
// to compact the process heap and trim unused C++ CRT / ONNX Runtime / DirectML
// pages from the process Working Set so Task Manager immediately reflects freed RAM.
func ReclaimOSMemory() {
	runtime.GC()
	debug.FreeOSMemory()

	if heap, _, _ := procGetProcessHeap.Call(); heap != 0 {
		_, _, _ = procHeapCompact.Call(heap, 0)
	}
	if proc, _, _ := procGetCurrentProcess.Call(); proc != 0 {
		minusOne := ^uintptr(0)
		_, _, _ = procSetProcessWorkingSetSizeEx.Call(proc, minusOne, minusOne, 0)
	}
}
