//go:build windows

package tts

import (
	"runtime"
	"runtime/debug"
	"syscall"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcess          = kernel32.NewProc("GetCurrentProcess")
	procGetProcessHeap             = kernel32.NewProc("GetProcessHeap")
	procHeapCompact                = kernel32.NewProc("HeapCompact")
	procSetProcessWorkingSetSizeEx = kernel32.NewProc("SetProcessWorkingSetSizeEx")
)

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
