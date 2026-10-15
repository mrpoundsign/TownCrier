package ui

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
)

// Win32 modifier and virtual-key constants used by ParseHotkey across platforms.
const (
	ModAlt      uint32 = 0x0001
	ModControl  uint32 = 0x0002
	ModShift    uint32 = 0x0004
	ModNoRepeat uint32 = 0x4000
)

// SupportedHotkeyKeys lists the canonical key names selectable in the Settings UI.
var SupportedHotkeyKeys = []string{
	"M", "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z",
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
	"Space", "Tab", "Backspace", "Insert", "Delete", "Home", "End", "PageUp", "PageDown", "Pause",
}

// HotkeySpec represents a parsed hotkey combination.
type HotkeySpec struct {
	Ctrl   bool
	Shift  bool
	Alt    bool
	Key    string // Canonical key name (e.g. "M", "F9", "Space")
	WinMod uint32 // Win32 modifier bitmask (ModControl | ModShift | ModAlt | ModNoRepeat)
	WinVK  uint32 // Win32 Virtual-Key code
}

// String returns the canonical human-readable hotkey string (e.g. "Ctrl+Shift+M").
func (h HotkeySpec) String() string {
	var parts []string
	if h.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if h.Shift {
		parts = append(parts, "Shift")
	}
	if h.Alt {
		parts = append(parts, "Alt")
	}
	if h.Key != "" {
		parts = append(parts, h.Key)
	}
	return strings.Join(parts, "+")
}

// FyneModifier returns the Fyne KeyModifier bitmask for in-window shortcut registration.
func (h HotkeySpec) FyneModifier() fyne.KeyModifier {
	var mod fyne.KeyModifier
	if h.Ctrl {
		mod |= fyne.KeyModifierControl
	}
	if h.Shift {
		mod |= fyne.KeyModifierShift
	}
	if h.Alt {
		mod |= fyne.KeyModifierAlt
	}
	return mod
}

// FyneKeyName returns the Fyne KeyName for in-window shortcut registration.
func (h HotkeySpec) FyneKeyName() fyne.KeyName {
	switch h.Key {
	case "Space":
		return fyne.KeySpace
	case "Tab":
		return fyne.KeyTab
	case "Backspace":
		return fyne.KeyBackspace
	case "Insert":
		return fyne.KeyInsert
	case "Delete":
		return fyne.KeyDelete
	case "Home":
		return fyne.KeyHome
	case "End":
		return fyne.KeyEnd
	case "PageUp":
		return fyne.KeyPageUp
	case "PageDown":
		return fyne.KeyPageDown
	default:
		return fyne.KeyName(h.Key)
	}
}

// ParseHotkey parses a hotkey string like "Ctrl+Shift+M" or "F9" into a HotkeySpec.
func ParseHotkey(raw string) (HotkeySpec, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return HotkeySpec{}, errors.New("hotkey cannot be empty")
	}

	var spec HotkeySpec
	for p := range strings.SplitSeq(trimmed, "+") {
		token := strings.TrimSpace(p)
		if token == "" {
			continue
		}
		switch strings.ToLower(token) {
		case "ctrl", "control":
			spec.Ctrl = true
		case "shift":
			spec.Shift = true
		case "alt":
			spec.Alt = true
		default:
			if spec.Key != "" {
				return HotkeySpec{}, fmt.Errorf("multiple non-modifier keys in %q", raw)
			}
			canonical, vk, ok := resolveKeyNameAndVK(token)
			if !ok {
				return HotkeySpec{}, fmt.Errorf("unsupported hotkey key %q", token)
			}
			spec.Key = canonical
			spec.WinVK = vk
		}
	}

	if spec.Key == "" {
		return HotkeySpec{}, fmt.Errorf("hotkey %q must include a non-modifier key", raw)
	}

	// Single letter/digit keys without a modifier would block normal typing OS-wide;
	// require at least one modifier for single-character alphanumeric keys.
	if len(spec.Key) == 1 && !spec.Ctrl && !spec.Shift && !spec.Alt {
		return HotkeySpec{}, fmt.Errorf("key %q requires at least one modifier (Ctrl, Shift, or Alt) for global use", spec.Key)
	}

	spec.WinMod = ModNoRepeat
	if spec.Ctrl {
		spec.WinMod |= ModControl
	}
	if spec.Shift {
		spec.WinMod |= ModShift
	}
	if spec.Alt {
		spec.WinMod |= ModAlt
	}

	return spec, nil
}

// CanonicalKeyFromFyne maps a Fyne KeyName to our canonical key string if supported.
func CanonicalKeyFromFyne(name fyne.KeyName) (string, bool) {
	s := string(name)
	canonical, _, ok := resolveKeyNameAndVK(s)
	return canonical, ok
}

func resolveKeyNameAndVK(token string) (string, uint32, bool) {
	upper := strings.ToUpper(strings.TrimSpace(token))

	// A-Z (VK 0x41 - 0x5A)
	if len(upper) == 1 && upper[0] >= 'A' && upper[0] <= 'Z' {
		return upper, uint32(upper[0]), true
	}

	// 0-9 (VK 0x30 - 0x39)
	if len(upper) == 1 && upper[0] >= '0' && upper[0] <= '9' {
		return upper, uint32(upper[0]), true
	}

	// F1-F12 (VK 0x70 - 0x7B)
	for i := 1; i <= 12; i++ {
		fName := fmt.Sprintf("F%d", i)
		if upper == fName {
			return fName, uint32(0x70 + i - 1), true
		}
	}

	switch upper {
	case "SPACE":
		return "Space", 0x20, true
	case "TAB":
		return "Tab", 0x09, true
	case "BACKSPACE", "BACK":
		return "Backspace", 0x08, true
	case "INSERT", "INS":
		return "Insert", 0x2D, true
	case "DELETE", "DEL":
		return "Delete", 0x2E, true
	case "HOME":
		return "Home", 0x24, true
	case "END":
		return "End", 0x23, true
	case "PAGEUP", "PGUP", "PRIOR":
		return "PageUp", 0x21, true
	case "PAGEDOWN", "PGDN", "NEXT":
		return "PageDown", 0x22, true
	case "PAUSE":
		return "Pause", 0x13, true
	}

	return "", 0, false
}
