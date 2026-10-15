//go:build !windows

package ui

// GlobalHotkeyManager is a no-op stub on non-Windows platforms (used during WSL/Linux headless unit tests).
type GlobalHotkeyManager struct {
	onTrigger func()
}

// NewGlobalHotkeyManager creates a new global hotkey manager stub.
func NewGlobalHotkeyManager(onTrigger func()) *GlobalHotkeyManager {
	return &GlobalHotkeyManager{
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
