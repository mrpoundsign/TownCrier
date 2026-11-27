package ui

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var (
	resStatusDotGreen = fyne.NewStaticResource("status_dot_green.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16">`+
			`<circle cx="8" cy="8" r="5" fill="#22C55E"/>`+
			`</svg>`))

	resStatusDotYellow = fyne.NewStaticResource("status_dot_yellow.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16">`+
			`<circle cx="8" cy="8" r="5" fill="#F59E0B"/>`+
			`</svg>`))

	resStatusDotRed = fyne.NewStaticResource("status_dot_red.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16">`+
			`<circle cx="8" cy="8" r="5" fill="#EF4444"/>`+
			`</svg>`))

	resStatusDotGray = fyne.NewStaticResource("status_dot_gray.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16">`+
			`<circle cx="8" cy="8" r="5" fill="#64748B"/>`+
			`</svg>`))
)

func statusDotResource(dot string) fyne.Resource {
	switch dot {
	case "🟢", "green", "connected":
		return resStatusDotGreen
	case "🟡", "yellow", "connecting", "waiting", "needs_config":
		return resStatusDotYellow
	case "🔴", "red", "disconnected", "error":
		return resStatusDotRed
	default:
		return resStatusDotGray
	}
}

func canonicalDotString(dot string) string {
	switch dot {
	case "🟢", "green", "connected":
		return "🟢"
	case "🟡", "yellow", "connecting", "waiting", "needs_config":
		return "🟡"
	case "🔴", "red", "disconnected", "error":
		return "🔴"
	default:
		return "⚪"
	}
}

// platformConnectionBadge renders a platform's SVG vector logo paired with an SVG status dot
// (or a numbered green circle when multiple concurrent streams are active on the platform).
// It supports hover tooltips and optional click handlers to jump to the platform tab.
type platformConnectionBadge struct {
	widget.BaseWidget
	platform    string
	res         fyne.Resource
	dotString   string
	streamCount int
	dotImage    *canvas.Image
	countCircle *canvas.Circle
	countText   *canvas.Text
	countBadge  *fyne.Container
	rootBox     *fyne.Container
	tooltip     string
	onHover     func(obj fyne.CanvasObject, tip string, hovered bool)
	onTap       func()
}

var (
	_ fyne.Widget        = (*platformConnectionBadge)(nil)
	_ desktop.Hoverable  = (*platformConnectionBadge)(nil)
	_ desktop.Cursorable = (*platformConnectionBadge)(nil)
	_ fyne.Tappable      = (*platformConnectionBadge)(nil)
)

// newPlatformConnectionBadge creates a new platform connection badge.
func newPlatformConnectionBadge(platform string, res fyne.Resource, initialDot, initialTip string, onHover func(obj fyne.CanvasObject, tip string, hovered bool), onTap func()) *platformConnectionBadge {
	b := &platformConnectionBadge{
		platform:  platform,
		res:       res,
		dotString: canonicalDotString(initialDot),
		tooltip:   initialTip,
		onHover:   onHover,
		onTap:     onTap,
	}

	iconImg := canvas.NewImageFromResource(res)
	iconImg.FillMode = canvas.ImageFillContain
	iconImg.SetMinSize(fyne.NewSize(18, 18))

	dotRes := statusDotResource(initialDot)
	b.dotImage = canvas.NewImageFromResource(dotRes)
	b.dotImage.FillMode = canvas.ImageFillContain
	b.dotImage.SetMinSize(fyne.NewSize(10, 10))

	b.countCircle = canvas.NewCircle(color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF})
	b.countText = canvas.NewText("", color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF})
	b.countText.TextStyle = fyne.TextStyle{Bold: true}
	b.countText.TextSize = 10
	b.countText.Alignment = fyne.TextAlignCenter
	b.countBadge = container.NewGridWrap(
		fyne.NewSize(15, 15),
		container.NewStack(b.countCircle, container.NewCenter(b.countText)),
	)
	b.countBadge.Hide()

	b.rootBox = container.NewHBox(iconImg, container.NewCenter(container.NewStack(b.dotImage, b.countBadge)))

	b.ExtendBaseWidget(b)
	return b
}

// CreateRenderer implements fyne.Widget.
func (b *platformConnectionBadge) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.rootBox)
}

// SetStatus updates the indicator dot and hover tooltip message.
func (b *platformConnectionBadge) SetStatus(dot, tip string) {
	b.SetStatusWithCount(dot, tip, 0)
}

// SetStatusWithCount updates the indicator dot, hover tooltip, and optional active stream count.
// When the status is green ("🟢") and streamCount > 1, a green circle with the count overlaid is shown.
func (b *platformConnectionBadge) SetStatusWithCount(dot, tip string, streamCount int) {
	b.tooltip = tip
	b.dotString = canonicalDotString(dot)
	if b.dotString == "🟢" && streamCount > 1 {
		b.streamCount = streamCount
		b.countText.Text = strconv.Itoa(streamCount)
		b.countText.Refresh()
		b.dotImage.Hide()
		b.countBadge.Show()
	} else {
		if b.dotString == "🟢" && streamCount == 1 {
			b.streamCount = 1
		} else {
			b.streamCount = 0
		}
		b.dotImage.Resource = statusDotResource(dot)
		b.dotImage.Refresh()
		b.countBadge.Hide()
		b.dotImage.Show()
	}
	b.Refresh()
}

// StreamCount returns the number of active live streams reflected by this badge.
func (b *platformConnectionBadge) StreamCount() int {
	return b.streamCount
}

// Tooltip returns the current tooltip text.
func (b *platformConnectionBadge) Tooltip() string {
	return b.tooltip
}

// StatusDot returns the current dot text.
func (b *platformConnectionBadge) StatusDot() string {
	return b.dotString
}

// MouseIn triggers the hover tooltip callback.
func (b *platformConnectionBadge) MouseIn(*desktop.MouseEvent) {
	if b.onHover != nil && b.tooltip != "" {
		b.onHover(b, b.tooltip, true)
	}
}

// MouseMoved implements desktop.Hoverable.
func (b *platformConnectionBadge) MouseMoved(*desktop.MouseEvent) {}

// MouseOut dismisses the hover tooltip.
func (b *platformConnectionBadge) MouseOut() {
	if b.onHover != nil {
		b.onHover(b, "", false)
	}
}

// Cursor returns a pointer cursor if a click action is assigned.
func (b *platformConnectionBadge) Cursor() desktop.Cursor {
	if b.onTap != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

// Tapped invokes the click handler if assigned.
func (b *platformConnectionBadge) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}
