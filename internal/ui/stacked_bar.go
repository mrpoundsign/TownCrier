package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// ModelFamilyColor returns the distinct visual color assigned to a model family (or "base") on stacked hardware impact graphs.
func ModelFamilyColor(familyID string) color.NRGBA {
	switch familyID {
	case "base":
		return color.NRGBA{R: 0x47, G: 0x55, B: 0x69, A: 0xFF} // Slate 600 (Base App)
	case tts.ModelSupertonic:
		return color.NRGBA{R: 0x02, G: 0x84, B: 0xC7, A: 0xFF} // Sky Blue 600 (Supertonic INT8)
	case tts.ModelKokoro:
		return color.NRGBA{R: 0x10, G: 0xB9, B: 0x81, A: 0xFF} // Emerald 500 (Kokoro-82M)
	case tts.ModelKittenMini:
		return color.NRGBA{R: 0xD9, G: 0x77, B: 0x06, A: 0xFF} // Amber 600 (Kitten Mini)
	case tts.ModelKittenNano:
		return color.NRGBA{R: 0x93, G: 0x33, B: 0xEA, A: 0xFF} // Purple 600 (Kitten Nano)
	case tts.ModelPiper:
		return color.NRGBA{R: 0xE1, G: 0x1D, B: 0x48, A: 0xFF} // Rose 600 (Piper LibriTTS)
	default:
		return color.NRGBA{R: 0x38, G: 0xBD, B: 0xF8, A: 0xFF}
	}
}

// NewModelColorBadge creates a small colored square swatch paired with a label for legends or model rows.
func NewModelColorBadge(familyID string, text string, bold bool) fyne.CanvasObject {
	swatch := canvas.NewRectangle(ModelFamilyColor(familyID))
	swatch.CornerRadius = 3
	swatch.SetMinSize(fyne.NewSize(12, 12))
	swatchWrap := container.NewCenter(swatch)

	lbl := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: bold})
	return container.NewHBox(swatchWrap, lbl)
}

// StackedImpactBar is a horizontal multi-segment bar graph widget that visualizes additive contributions per model family.
type StackedImpactBar struct {
	widget.BaseWidget
	segments []tts.ImpactSegment
	text     string
}

// NewStackedImpactBar creates a new StackedImpactBar widget.
func NewStackedImpactBar() *StackedImpactBar {
	b := &StackedImpactBar{}
	b.ExtendBaseWidget(b)
	return b
}

// SetData updates the segments and overlay text on the stacked bar and refreshes its canvas.
func (b *StackedImpactBar) SetData(segments []tts.ImpactSegment, text string) {
	b.segments = append(b.segments[:0], segments...)
	b.text = text
	b.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (b *StackedImpactBar) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF})
	bg.CornerRadius = 4
	bg.StrokeColor = color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF}
	bg.StrokeWidth = 1

	label := canvas.NewText("", color.NRGBA{R: 0xF8, G: 0xFA, B: 0xFC, A: 0xFF})
	label.Alignment = fyne.TextAlignCenter
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = theme.TextSize() - 1

	r := &stackedImpactBarRenderer{
		bar:   b,
		bg:    bg,
		label: label,
	}
	r.Refresh()
	return r
}

type stackedImpactBarRenderer struct {
	bar   *StackedImpactBar
	bg    *canvas.Rectangle
	rects []*canvas.Rectangle
	label *canvas.Text
}

func (r *stackedImpactBarRenderer) MinSize() fyne.Size {
	return fyne.NewSize(180, 24)
}

func (r *stackedImpactBarRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))

	x := float32(1)
	innerW := max(size.Width-2, 0)
	innerH := max(size.Height-2, 0)

	for i, seg := range r.bar.segments {
		if i >= len(r.rects) {
			break
		}
		rect := r.rects[i]
		w := innerW * float32(seg.Fraction)
		if w < 3 && seg.Fraction > 0 {
			w = 3
		}
		if x+w > size.Width-1 {
			w = max(size.Width-1-x, 0)
		}
		rect.Move(fyne.NewPos(x, 1))
		rect.Resize(fyne.NewSize(w, innerH))
		x += w
	}

	r.label.Resize(size)
	r.label.Move(fyne.NewPos(0, (size.Height-r.label.MinSize().Height)/2))
}

func (r *stackedImpactBarRenderer) Refresh() {
	for len(r.rects) < len(r.bar.segments) {
		rect := canvas.NewRectangle(color.Transparent)
		r.rects = append(r.rects, rect)
	}
	for i := range r.rects {
		if i < len(r.bar.segments) {
			seg := r.bar.segments[i]
			r.rects[i].FillColor = ModelFamilyColor(seg.FamilyID)
			r.rects[i].Show()
		} else {
			r.rects[i].Hide()
		}
		r.rects[i].Refresh()
	}

	r.label.Text = r.bar.text
	r.label.TextSize = max(theme.TextSize()-1, 11)
	r.label.Refresh()
	r.bg.Refresh()
	r.Layout(r.bar.Size())
	canvas.Refresh(r.bar)
}

func (r *stackedImpactBarRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, len(r.rects)+2)
	objs = append(objs, r.bg)
	for _, rect := range r.rects {
		objs = append(objs, rect)
	}
	objs = append(objs, r.label)
	return objs
}

func (r *stackedImpactBarRenderer) Destroy() {}
