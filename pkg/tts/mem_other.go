//go:build !windows

package tts

import (
	"runtime"
	"runtime/debug"
)

// ReclaimOSMemory triggers a Go garbage collection and releases unused memory pages back to the OS.
func ReclaimOSMemory() {
	runtime.GC()
	debug.FreeOSMemory()
}
