package ui

import (
	"testing"

	"fyne.io/fyne/v2/theme"

	"github.com/mrpoundsign/TownCrier/internal/config"
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

func TestChatScaleHelpers(t *testing.T) {
	if got := NormalizeChatScale(0); got != 100 {
		t.Errorf("expected NormalizeChatScale(0) = 100, got %d", got)
	}
	if got := NormalizeChatScale(20); got != 50 {
		t.Errorf("expected NormalizeChatScale(20) = 50, got %d", got)
	}
	if got := NormalizeChatScale(450); got != 300 {
		t.Errorf("expected NormalizeChatScale(450) = 300, got %d", got)
	}
	if got := NormalizeChatScale(135); got != 135 {
		t.Errorf("expected NormalizeChatScale(135) = 135, got %d", got)
	}

	if lbl := FormatChatScaleLabel(100); lbl != ChatScaleLabelDefault {
		t.Errorf("expected %q, got %q", ChatScaleLabelDefault, lbl)
	}
	if lbl := FormatChatScaleLabel(150); lbl != "150%" {
		t.Errorf("expected '150%%', got %q", lbl)
	}
	if lbl := FormatChatScaleLabel(135); lbl != ChatScaleLabelCustom {
		t.Errorf("expected %q for custom scale, got %q", ChatScaleLabelCustom, lbl)
	}

	if lbl := FormatPresetChatScaleLabel(0, 125); lbl != "Default (125%)" {
		t.Errorf("expected 'Default (125%%)', got %q", lbl)
	}
	if lbl := FormatPresetChatScaleLabel(175, 100); lbl != "175%" {
		t.Errorf("expected '175%%', got %q", lbl)
	}
	if lbl := FormatPresetChatScaleLabel(140, 100); lbl != ChatScaleLabelCustom {
		t.Errorf("expected %q, got %q", ChatScaleLabelCustom, lbl)
	}

	pct, isCustom, isInherit := ParseChatScaleLabel("Default (125%)")
	if !isInherit || isCustom || pct != 0 {
		t.Errorf("unexpected ParseChatScaleLabel for Default: pct=%d custom=%v inherit=%v", pct, isCustom, isInherit)
	}
	pct, isCustom, isInherit = ParseChatScaleLabel("Custom...")
	if !isCustom || isInherit || pct != 0 {
		t.Errorf("unexpected ParseChatScaleLabel for Custom: pct=%d custom=%v inherit=%v", pct, isCustom, isInherit)
	}
	pct, isCustom, isInherit = ParseChatScaleLabel("150%")
	if isCustom || isInherit || pct != 150 {
		t.Errorf("unexpected ParseChatScaleLabel for 150%%: pct=%d custom=%v inherit=%v", pct, isCustom, isInherit)
	}

	if got := ParseCustomChatScaleInput("165%", 100); got != 165 {
		t.Errorf("expected 165, got %d", got)
	}
	if got := ParseCustomChatScaleInput("10", 100); got != 50 {
		t.Errorf("expected clamped 50, got %d", got)
	}
	if got := ParseCustomChatScaleInput("999", 100); got != 300 {
		t.Errorf("expected clamped 300, got %d", got)
	}
	if got := ParseCustomChatScaleInput("invalid", 125); got != 125 {
		t.Errorf("expected fallback 125, got %d", got)
	}

	pInherit := config.LiveLogPreset{ChatScale: 0}
	if got := EffectivePresetChatScale(pInherit, 150); got != 150 {
		t.Errorf("expected inherited 150, got %d", got)
	}
	pOverride := config.LiveLogPreset{ChatScale: 200}
	if got := EffectivePresetChatScale(pOverride, 150); got != 200 {
		t.Errorf("expected override 200, got %d", got)
	}
}

func TestScaledChatTheme(t *testing.T) {
	base := NewObsidianTheme()
	scaled := NewScaledChatTheme(1.5)

	baseText := base.Size(theme.SizeNameText)
	scaledText := scaled.Size(theme.SizeNameText)
	if scaledText != baseText*1.5 {
		t.Errorf("expected scaled text size %f, got %f", baseText*1.5, scaledText)
	}

	baseScroll := base.Size(theme.SizeNameScrollBar)
	scaledScroll := scaled.Size(theme.SizeNameScrollBar)
	if scaledScroll != baseScroll {
		t.Errorf("expected scrollbar size to remain %f, got %f", baseScroll, scaledScroll)
	}
}
