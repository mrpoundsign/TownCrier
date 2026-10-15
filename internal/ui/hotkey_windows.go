//go:build windows

package ui

import (
	"log"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
)

const (
	wmHotkey uint32 = 0x0312
	wmQuit   uint32 = 0x0012
	hotkeyID uintptr = 1
)

type winPoint struct {
	X, Y int32
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
}

// GlobalHotkeyManager manages an OS-wide global hotkey on Windows via Win32 RegisterHotKey.
type GlobalHotkeyManager struct {
	mu       sync.Mutex
	threadID uint32
	stopDone chan struct{}
	onTrigger func()
}

// NewGlobalHotkeyManager creates a new Windows global hotkey manager.
func NewGlobalHotkeyManager(onTrigger func()) *GlobalHotkeyManager {
	return &GlobalHotkeyManager{
		onTrigger: onTrigger,
	}
}

// Register parses the hotkey string and registers it system-wide on Windows, replacing any previous registration.
func (m *GlobalHotkeyManager) Register(hotkeyStr string) error {
	spec, err := ParseHotkey(hotkeyStr)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.stopLocked()
	doneCh := make(chan struct{})
	m.stopDone = doneCh
	m.mu.Unlock()

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(doneCh)

		tid, _, _ := procGetCurrentThreadId.Call()

		ret, _, regErr := procRegisterHotKey.Call(
			0,
			hotkeyID,
			uintptr(spec.WinMod),
			uintptr(spec.WinVK),
		)
		if ret == 0 {
			log.Printf("[Hotkey] win32 RegisterHotKey(%q) failed: %v", spec.String(), regErr)
			return
		}
		defer func() {
			_, _, _ = procUnregisterHotKey.Call(0, hotkeyID)
		}()

		m.mu.Lock()
		m.threadID = uint32(tid)
		m.mu.Unlock()

		log.Printf("[Hotkey] Registered system-wide Windows hotkey: %s (mod=0x%x, vk=0x%x, tid=%d)",
			spec.String(), spec.WinMod, spec.WinVK, tid)

		var msg winMsg
		for {
			r, _, _ := procGetMessageW.Call(
				uintptr(unsafe.Pointer(&msg)),
				0,
				0,
				0,
			)
			// GetMessageW returns 0 on WM_QUIT or -1 on error
			if int32(r) <= 0 {
				break
			}
			if msg.Message == wmHotkey && msg.WParam == hotkeyID {
				if m.onTrigger != nil {
					go m.onTrigger()
				}
			}
		}
	}()

	return nil
}

// Close unregisters the active system-wide hotkey and stops the listener thread.
func (m *GlobalHotkeyManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *GlobalHotkeyManager) stopLocked() {
	if m.stopDone == nil {
		return
	}
	tid := m.threadID
	m.stopDone = nil
	m.threadID = 0

	if tid != 0 {
		_, _, _ = procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
	}
}
