package ui

import (
	"testing"
)

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		input     string
		wantStr   string
		wantVK    uint32
		wantMod   uint32
		expectErr bool
	}{
		{
			input:   "Ctrl+Shift+M",
			wantStr: "Ctrl+Shift+M",
			wantVK:  'M',
			wantMod: ModNoRepeat | ModControl | ModShift,
		},
		{
			input:   "alt + f9",
			wantStr: "Alt+F9",
			wantVK:  0x78, // VK_F9
			wantMod: ModNoRepeat | ModAlt,
		},
		{
			input:   "F12",
			wantStr: "F12",
			wantVK:  0x7B, // VK_F12
			wantMod: ModNoRepeat,
		},
		{
			input:   "Ctrl+Space",
			wantStr: "Ctrl+Space",
			wantVK:  0x20, // VK_SPACE
			wantMod: ModNoRepeat | ModControl,
		},
		{
			input:     "M", // single alphanumeric without modifier should error
			expectErr: true,
		},
		{
			input:     "Ctrl+Shift", // modifier only should error
			expectErr: true,
		},
		{
			input:     "",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		spec, err := ParseHotkey(tc.input)
		if tc.expectErr {
			if err == nil {
				t.Errorf("ParseHotkey(%q) expected error, got nil (spec=%+v)", tc.input, spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHotkey(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if spec.String() != tc.wantStr {
			t.Errorf("ParseHotkey(%q).String() = %q, want %q", tc.input, spec.String(), tc.wantStr)
		}
		if spec.WinVK != tc.wantVK {
			t.Errorf("ParseHotkey(%q).WinVK = 0x%x, want 0x%x", tc.input, spec.WinVK, tc.wantVK)
		}
		if spec.WinMod != tc.wantMod {
			t.Errorf("ParseHotkey(%q).WinMod = 0x%x, want 0x%x", tc.input, spec.WinMod, tc.wantMod)
		}
	}
}
