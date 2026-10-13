package ui

import (
	"testing"

	"fyne.io/fyne/v2/theme"
)

func TestObsidianThemeColors(t *testing.T) {
	th := NewObsidianTheme()

	bg := th.Color(theme.ColorNameBackground, theme.VariantDark)
	if bg != colorObsidianSlate {
		t.Errorf("expected background %v, got %v", colorObsidianSlate, bg)
	}

	fg := th.Color(theme.ColorNameForeground, theme.VariantDark)
	if fg != colorChromeWhite {
		t.Errorf("expected foreground %v, got %v", colorChromeWhite, fg)
	}

	pri := th.Color(theme.ColorNamePrimary, theme.VariantDark)
	if pri != colorSkyBlue {
		t.Errorf("expected primary %v, got %v", colorSkyBlue, pri)
	}

	errColor := th.Color(theme.ColorNameError, theme.VariantDark)
	if errColor != colorDangerRed {
		t.Errorf("expected error color %v, got %v", colorDangerRed, errColor)
	}
}
