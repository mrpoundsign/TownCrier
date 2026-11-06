//go:build !windows

package ui

const (
	HotkeyIDPanic      uintptr = 1
	HotkeyIDModeToggle uintptr = 2
)

// GlobalHotkeyManager is a no-op stub on non-Windows platforms (used during WSL/Linux headless unit tests).
type GlobalHotkeyManager struct {
	hotkeyID  uintptr
	onTrigger func()
}

// NewGlobalHotkeyManager creates a new global hotkey manager stub.
func NewGlobalHotkeyManager(onTrigger func()) *GlobalHotkeyManager {
	return NewGlobalHotkeyManagerWithID(HotkeyIDPanic, onTrigger)
}

// NewGlobalHotkeyManagerWithID creates a new global hotkey manager stub with a specific ID.
func NewGlobalHotkeyManagerWithID(id uintptr, onTrigger func()) *GlobalHotkeyManager {
	return &GlobalHotkeyManager{
		hotkeyID:  id,
		onTrigger: onTrigger,
	}
}

// Register validates the hotkey string on non-Windows platforms.
func (m *GlobalHotkeyManager) Register(hotkeyStr string) error {
	_, err := ParseHotkey(hotkeyStr)
	return err
}

// Close is a no-op on non-Windows platforms.
func (m *GlobalHotkeyManager) Close() {}
