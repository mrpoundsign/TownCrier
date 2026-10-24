package ui

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// UI scale mode constants and display labels.
const (
	UIScale100  = "1.0"
	UIScale115  = "1.15"
	UIScale125  = "1.25"
	UIScale150  = "1.5"
	UIScaleAuto = "auto"

	uiScaleLabel100  = "100% (Default — Lock DPI)"
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
// Physical window pixel dimensions are preserved across live scale changes so changing UI Scale
// zooms the interface contents cleanly without ballooning or shrinking the OS window.
func ApplyUIScale(appObj fyne.App, scaleMode string) {
	mode := NormalizeUIScale(scaleMode)
	if mode == UIScaleAuto {
		_ = os.Setenv("FYNE_DISABLE_DPI_DETECTION", "false")
		_ = os.Setenv("FYNE_SCALE", "auto")
	} else {
		_ = os.Setenv("FYNE_DISABLE_DPI_DETECTION", "true")
		_ = os.Setenv("FYNE_SCALE", mode)
	}
	if appObj == nil {
		return
	}

	type winSnapshot struct {
		win          fyne.Window
		physW        float32
		physH        float32
		wasMaximized bool
	}
	var snaps []winSnapshot

	if drv := appObj.Driver(); drv != nil {
		for _, w := range drv.AllWindows() {
			if w == nil || w.Canvas() == nil {
				continue
			}
			sc := w.Canvas().Scale()
			sz := w.Canvas().Size()
			if sc > 0 && sz.Width > 0 && sz.Height > 0 {
				snaps = append(snaps, winSnapshot{
					win:          w,
					physW:        sz.Width * sc,
					physH:        sz.Height * sc,
					wasMaximized: isWindowMaximized(w),
				})
			}
		}
	}

	appObj.Settings().SetTheme(NewObsidianTheme())

	for _, snap := range snaps {
		if snap.wasMaximized {
			restoreMaximizedWindow(snap.win)
			if content := snap.win.Canvas().Content(); content != nil {
				content.Refresh()
			}
			continue
		}
		newScale := snap.win.Canvas().Scale()
		if newScale <= 0 {
			continue
		}
		targetDIP := fyne.NewSize(snap.physW/newScale, snap.physH/newScale)
		snap.win.Resize(targetDIP)
		if content := snap.win.Canvas().Content(); content != nil {
			content.Refresh()
		}
	}
}

// ObsidianTheme implements the custom visual identity for TownCrier.
// Palette: Obsidian Slate (#0F172A), Chrome White (#F8FAFC), Electric Sky Blue (#38BDF8).
type ObsidianTheme struct{}

var _ fyne.Theme = (*ObsidianTheme)(nil)

var (
	colorObsidianSlate = color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF} // #0F172A (Window Background)
	colorSlateSurface  = color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF} // #1E293B (Card & Panel Surface)
	colorInputWell     = color.NRGBA{R: 0x0B, G: 0x11, B: 0x20, A: 0xFF} // #0B1120 (Recessed Input Field Well)
	colorButtonSurface = color.NRGBA{R: 0x28, G: 0x35, B: 0x48, A: 0xFF} // #283548 (Elevated Button & Select Surface)
	colorHoverSurface  = color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF} // #334155 (Control Hover Highlight)
	colorSlateBorder   = color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF} // #334155 (Divider & Border)
	colorInputBorder   = color.NRGBA{R: 0x47, G: 0x55, B: 0x69, A: 0xFF} // #475569 (Crisp Input Outline)
	colorSkyBlue       = color.NRGBA{R: 0x38, G: 0xBD, B: 0xF8, A: 0xFF} // #38BDF8 (Primary Accent)
	colorSkyBlueDim    = color.NRGBA{R: 0x02, G: 0x84, B: 0xC7, A: 0xFF} // #0284C7 (Active Tab / Selection)
	colorChromeWhite   = color.NRGBA{R: 0xF8, G: 0xFA, B: 0xFC, A: 0xFF} // #F8FAFC (Headings & Primary Text)
	colorSlateMuted    = color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF} // #94A3B8 (Secondary / Field Label Text)
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
		return colorInputWell
	case theme.ColorNameInputBorder:
		return colorInputBorder
	case theme.ColorNameForeground:
		return colorChromeWhite
	case theme.ColorNamePrimary:
		return colorSkyBlue
	case theme.ColorNameFocus:
		return colorSkyBlue
	case theme.ColorNameSelection:
		return colorSkyBlueDim
	case theme.ColorNameButton:
		return colorButtonSurface
	case theme.ColorNameHover:
		return colorHoverSurface
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
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

// Chat panel independent scaling constants and helpers.
const (
	ChatScaleMin     = 50
	ChatScaleMax     = 300
	ChatScaleDefault = 100

	ChatScaleLabelDefault = "100% (Default)"
	ChatScaleLabelCustom  = "Custom..."
)

// NormalizeChatScale clamps non-zero percentages to [50, 300] and defaults <= 0 to 100.
func NormalizeChatScale(pct int) int {
	if pct <= 0 {
		return ChatScaleDefault
	}
	if pct < ChatScaleMin {
		return ChatScaleMin
	}
	if pct > ChatScaleMax {
		return ChatScaleMax
	}
	return pct
}

// IsStandardChatScalePreset reports whether pct matches one of the built-in dropdown presets.
func IsStandardChatScalePreset(pct int) bool {
	switch pct {
	case 75, 90, 100, 115, 125, 150, 175, 200:
		return true
	default:
		return false
	}
}

// ChatScaleOptions returns the dropdown options for the Default Chat Panel Scale in Settings -> Event Logs.
func ChatScaleOptions() []string {
	return []string{
		"75%",
		"90%",
		ChatScaleLabelDefault,
		"115%",
		"125%",
		"150%",
		"175%",
		"200%",
		ChatScaleLabelCustom,
	}
}

// FormatDefaultInheritLabel formats the "Default (X%)" option label for a Live Log preset selector.
func FormatDefaultInheritLabel(defaultPct int) string {
	return fmt.Sprintf("Default (%d%%)", NormalizeChatScale(defaultPct))
}

// ChatScalePresetOptions returns the dropdown options for a Live Log preset card (including Default Inherit).
func ChatScalePresetOptions(defaultPct int) []string {
	return []string{
		FormatDefaultInheritLabel(defaultPct),
		"75%",
		"90%",
		"100%",
		"115%",
		"125%",
		"150%",
		"175%",
		"200%",
		ChatScaleLabelCustom,
	}
}

// FormatChatScaleLabel maps a persisted default ChatScale percentage to its Settings dropdown option.
func FormatChatScaleLabel(pct int) string {
	norm := NormalizeChatScale(pct)
	if norm == ChatScaleDefault {
		return ChatScaleLabelDefault
	}
	if IsStandardChatScalePreset(norm) {
		return fmt.Sprintf("%d%%", norm)
	}
	return ChatScaleLabelCustom
}

// FormatPresetChatScaleLabel maps a LiveLogPreset's ChatScale (0 = inherit) to its preset dialog dropdown option.
func FormatPresetChatScaleLabel(presetPct, defaultPct int) string {
	if presetPct <= 0 {
		return FormatDefaultInheritLabel(defaultPct)
	}
	norm := NormalizeChatScale(presetPct)
	if IsStandardChatScalePreset(norm) {
		return fmt.Sprintf("%d%%", norm)
	}
	return ChatScaleLabelCustom
}

// ParseChatScaleLabel parses a dropdown selection from either the Settings or Live Log preset scale selector.
func ParseChatScaleLabel(label string) (pct int, isCustom bool, isInherit bool) {
	clean := strings.TrimSpace(label)
	lower := strings.ToLower(clean)
	if strings.HasPrefix(lower, "default") {
		return 0, false, true
	}
	if strings.HasPrefix(lower, "custom") {
		return 0, true, false
	}
	clean = strings.TrimSuffix(clean, "(Default)")
	clean = strings.TrimSpace(clean)
	clean = strings.TrimSuffix(clean, "%")
	clean = strings.TrimSpace(clean)
	if val, err := strconv.Atoi(clean); err == nil && val > 0 {
		return NormalizeChatScale(val), false, false
	}
	return ChatScaleDefault, false, false
}

// ParseCustomChatScaleInput parses a user-entered custom percentage string and clamps it to [50, 300].
func ParseCustomChatScaleInput(text string, fallback int) int {
	clean := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "%"))
	if val, err := strconv.Atoi(clean); err == nil && val > 0 {
		return NormalizeChatScale(val)
	}
	return NormalizeChatScale(fallback)
}

// EffectivePresetChatScale returns the effective chat scale percentage for a LiveLogPreset.
func EffectivePresetChatScale(preset config.LiveLogPreset, defaultPct int) int {
	if preset.ChatScale > 0 {
		return NormalizeChatScale(preset.ChatScale)
	}
	return NormalizeChatScale(defaultPct)
}

// ScaledChatTheme wraps ObsidianTheme to scale text, inline icons, and cell padding inside chat panels
// without affecting the rest of the application UI.
type ScaledChatTheme struct {
	ObsidianTheme
	scale float32
}

var _ fyne.Theme = (*ScaledChatTheme)(nil)

// NewScaledChatTheme constructs a ScaledChatTheme for the given multiplier (e.g., 1.25 for 125%).
func NewScaledChatTheme(scale float32) *ScaledChatTheme {
	if scale <= 0 {
		scale = 1.0
	}
	return &ScaledChatTheme{scale: scale}
}

// Size scales typography, inline icons, and spacing proportionally by the chat panel scale factor.
func (t *ScaledChatTheme) Size(name fyne.ThemeSizeName) float32 {
	base := t.ObsidianTheme.Size(name)
	s := t.scale
	if s <= 0 {
		s = 1.0
	}
	switch name {
	case theme.SizeNameText,
		theme.SizeNameSubHeadingText,
		theme.SizeNameCaptionText,
		theme.SizeNameHeadingText,
		theme.SizeNameInlineIcon,
		theme.SizeNamePadding,
		theme.SizeNameInnerPadding,
		theme.SizeNameLineSpacing:
		return base * s
	default:
		return base
	}
}

