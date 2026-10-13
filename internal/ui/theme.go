package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// ObsidianTheme implements the custom visual identity for OctochorpeBot.
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
