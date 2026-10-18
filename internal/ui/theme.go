package ui

import (
	"image/color"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// UI scale mode constants and display labels.
const (
	UIScale100  = "1.0"
	UIScale115  = "1.15"
	UIScale125  = "1.25"
	UIScale150  = "1.5"
	UIScaleAuto = "auto"

	uiScaleLabel100  = "100% (Default — Lock Multi-Monitor DPI)"
	uiScaleLabel115  = "115%"
	uiScaleLabel125  = "125%"
	uiScaleLabel150  = "150%"
	uiScaleLabelAuto = "Auto (Per-Monitor EDID)"
)

// UIScaleOptions returns the human-readable labels for the UI Scale selector.
func UIScaleOptions() []string {
	return []string{
		uiScaleLabel100,
		uiScaleLabel115,
		uiScaleLabel125,
		uiScaleLabel150,
		uiScaleLabelAuto,
	}
}

// NormalizeUIScale Mode returns a canonical UI scale mode string ("1.0", "1.15", "1.25", "1.5", or "auto").
func NormalizeUIScale(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case UIScale100, "1", "100%":
		return UIScale100
	case UIScale115, "115%":
		return UIScale115
	case UIScale125, "125%":
		return UIScale125
	case UIScale150, "1.50", "150%":
		return UIScale150
	case UIScaleAuto:
		return UIScaleAuto
	default:
		return UIScale100
	}
}

// FormatUIScaleLabel maps a persisted config UIScale mode to its dropdown label.
func FormatUIScaleLabel(mode string) string {
	switch NormalizeUIScale(mode) {
	case UIScale115:
		return uiScaleLabel115
	case UIScale125:
		return uiScaleLabel125
	case UIScale150:
		return uiScaleLabel150
	case UIScaleAuto:
		return uiScaleLabelAuto
	default:
		return uiScaleLabel100
	}
}

// ParseUIScaleLabel maps a dropdown label back to its persisted config UIScale mode.
func ParseUIScaleLabel(label string) string {
	switch strings.TrimSpace(label) {
	case uiScaleLabel115:
		return UIScale115
	case uiScaleLabel125:
		return UIScale125
	case uiScaleLabel150:
		return UIScale150
	case uiScaleLabelAuto:
		return UIScaleAuto
	default:
		return UIScale100
	}
}

// ApplyUIScale configures Fyne's DPI detection and user scale environment variables,
// and triggers a live scale reload across all open windows if appObj is non-nil.
// By default ("1.0"), Fyne's EDID-based per-monitor DPI detection is disabled so Windows DWM
// handles multi-monitor scaling without zoom jumps when dragging between laptop and desktop screens.
func ApplyUIScale(appObj fyne.App, scaleMode string) {
	mode := NormalizeUIScale(scaleMode)
	if mode == UIScaleAuto {
		_ = os.Setenv("FYNE_DISABLE_DPI_DETECTION", "false")
		_ = os.Setenv("FYNE_SCALE", "auto")
	} else {
		_ = os.Setenv("FYNE_DISABLE_DPI_DETECTION", "true")
		_ = os.Setenv("FYNE_SCALE", mode)
	}
	if appObj != nil {
		appObj.Settings().SetTheme(NewObsidianTheme())
	}
}

// ObsidianTheme implements the custom visual identity for TownCrier.
// Palette: Obsidian Slate (#0F172A), Chrome White (#F8FAFC), Electric Sky Blue (#38BDF8).
type ObsidianTheme struct{}

var _ fyne.Theme = (*ObsidianTheme)(nil)

var (
	colorObsidianSlate = color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF} // #0F172A (Window Background)
	colorSlateSurface  = color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF} // #1E293B (Card & Input Background)
	colorSlateBorder   = color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF} // #334155 (Divider & Border)
	colorSkyBlue       = color.NRGBA{R: 0x38, G: 0xBD, B: 0xF8, A: 0xFF} // #38BDF8 (Primary Accent)
	colorSkyBlueDim    = color.NRGBA{R: 0x02, G: 0x84, B: 0xC7, A: 0xFF} // #0284C7 (Active Tab / Hover)
	colorChromeWhite   = color.NRGBA{R: 0xF8, G: 0xFA, B: 0xFC, A: 0xFF} // #F8FAFC (Headings & Primary Text)
	colorSlateMuted    = color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF} // #94A3B8 (Secondary Text)
	colorDangerRed     = color.NRGBA{R: 0xEF, G: 0x44, B: 0x44, A: 0xFF} // #EF4444 (Panic / Error)
	colorSuccessGreen  = color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF} // #22C55E (Connected / Success)
	colorWarningAmber  = color.NRGBA{R: 0xF5, G: 0x9E, B: 0x0B, A: 0xFF} // #F59E0B (Warning / Fallback)
	colorDisabledSlate = color.NRGBA{R: 0x47, G: 0x55, B: 0x69, A: 0xFF} // #475569 (Disabled)
)

func NewObsidianTheme() *ObsidianTheme {
	return &ObsidianTheme{}
}

func (t *ObsidianTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colorObsidianSlate
	case theme.ColorNameInputBackground:
		return colorSlateSurface
	case theme.ColorNameForeground:
		return colorChromeWhite
	case theme.ColorNamePrimary:
		return colorSkyBlue
	case theme.ColorNameFocus:
		return colorSkyBlue
	case theme.ColorNameSelection:
		return colorSkyBlueDim
	case theme.ColorNameButton:
		return colorSlateSurface
	case theme.ColorNameDisabled:
		return colorDisabledSlate
	case theme.ColorNameError:
		return colorDangerRed
	case theme.ColorNameSuccess:
		return colorSuccessGreen
	case theme.ColorNameWarning:
		return colorWarningAmber
	case theme.ColorNameSeparator:
		return colorSlateBorder
	case theme.ColorNamePlaceHolder:
		return colorSlateMuted
	default:
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
}

func (t *ObsidianTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *ObsidianTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *ObsidianTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

