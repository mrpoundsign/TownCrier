package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const sectionRailWidth float32 = 175

// panelSection pairs a human-readable quick-jump label with a target CanvasObject inside a scrollable panel.
type panelSection struct {
	Label  string
	Target fyne.CanvasObject
}

// sectionedPanel pairs a width-clamped vertical scroll container on the left with a
// synchronized "Sections" quick-jump rail on the right.
type sectionedPanel struct {
	root      *fyne.Container
	scroll    *container.Scroll
	railWrap  *fyne.Container
	railBox   *fyne.Container
	sections  []panelSection
	buttons   []*widget.Button
	activeIdx int
}

// newSectionedPanelVScroll wraps content in a width-clamped vertical scroll container paired with
// a right-hand quick-jump section rail that scrolls directly to visible sections.
func newSectionedPanelVScroll(content fyne.CanvasObject, sections []panelSection) *sectionedPanel {
	scroll := newPanelVScroll(content)
	railBox := container.NewVBox()
	buttons := make([]*widget.Button, len(sections))

	sp := &sectionedPanel{
		scroll:    scroll,
		railBox:   railBox,
		sections:  sections,
		buttons:   buttons,
		activeIdx: -1,
	}

	for i, sec := range sections {
		idx := i
		btn := widget.NewButton(sec.Label, func() {
			sp.ScrollTo(idx)
		})
		btn.Alignment = widget.ButtonAlignLeading
		btn.Importance = widget.LowImportance
		buttons[i] = btn
		railBox.Add(btn)
	}

	scroll.OnScrolled = func(pos fyne.Position) {
		sp.syncActiveFromScroll(pos.Y)
	}

	hdr := widget.NewLabelWithStyle("Sections", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	railInner := container.NewVBox(
		hdr,
		widget.NewSeparator(),
		railBox,
	)
	railScroll := container.NewVScroll(container.NewPadded(railInner))

	widthSpacer := canvas.NewRectangle(color.Transparent)
	widthSpacer.SetMinSize(fyne.NewSize(sectionRailWidth, 1))

	bg := canvas.NewRectangle(colorSlateSurface)
	divider := canvas.NewRectangle(colorSlateBorder)
	divider.SetMinSize(fyne.NewSize(1, 1))

	railBody := container.NewStack(bg, widthSpacer, railScroll)
	sp.railWrap = container.NewBorder(nil, nil, divider, nil, railBody)
	sp.root = container.New(&sectionedPanelLayout{
		scroll:   scroll,
		railWrap: sp.railWrap,
		minWidth: defaultPanelMinWidth,
	}, scroll, sp.railWrap)

	sp.SyncVisibility()
	return sp
}

type sectionedPanelLayout struct {
	scroll   *container.Scroll
	railWrap fyne.CanvasObject
	minWidth float32
}

func (l *sectionedPanelLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	if l.scroll == nil {
		return
	}
	railW := float32(0)
	if l.railWrap != nil && l.railWrap.Visible() {
		railW = max(sectionRailWidth, l.railWrap.MinSize().Width)
		if railW > size.Width {
			railW = size.Width
		}
	}
	scrollW := max(float32(0), size.Width-railW)
	l.scroll.Move(fyne.NewPos(0, 0))
	l.scroll.Resize(fyne.NewSize(scrollW, size.Height))
	if l.railWrap != nil && l.railWrap.Visible() {
		l.railWrap.Move(fyne.NewPos(scrollW, 0))
		l.railWrap.Resize(fyne.NewSize(railW, size.Height))
	}
}

func (l *sectionedPanelLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	var minW, minH float32
	if l.scroll != nil {
		ms := l.scroll.MinSize()
		minW = ms.Width
		minH = ms.Height
	}
	if l.railWrap != nil && l.railWrap.Visible() {
		rms := l.railWrap.MinSize()
		minW += rms.Width
		minH = max(minH, rms.Height)
	}
	clampW := l.minWidth
	if clampW <= 0 {
		clampW = defaultPanelMinWidth
	}
	if minW > clampW {
		minW = clampW
	}
	return fyne.NewSize(minW, minH)
}

// Root returns the top-level CanvasObject containing the scroll pane and right-hand section rail.
func (sp *sectionedPanel) Root() fyne.CanvasObject {
	if sp == nil {
		return nil
	}
	return sp.root
}

// Scroll returns the underlying vertical scroll container.
func (sp *sectionedPanel) Scroll() *container.Scroll {
	if sp == nil {
		return nil
	}
	return sp.scroll
}

// ScrollTo scrolls the left pane so the section at idx sits at the top of the viewport.
func (sp *sectionedPanel) ScrollTo(idx int) {
	if sp == nil || sp.scroll == nil || sp.scroll.Content == nil {
		return
	}
	if idx < 0 || idx >= len(sp.sections) {
		return
	}
	sec := sp.sections[idx]
	if !sp.isSectionVisible(sec) {
		return
	}

	targetY, ok := findTargetYOffset(sp.scroll.Content, sec.Target)
	if !ok {
		return
	}
	if targetY < 0 {
		targetY = 0
	}

	if viewH := sp.scroll.Size().Height; viewH > 0 {
		contentH := max(sp.scroll.Content.Size().Height, sp.scroll.Content.MinSize().Height)
		maxY := max(float32(0), contentH-viewH)
		if targetY > maxY {
			targetY = maxY
		}
	}

	sp.scroll.Offset = fyne.NewPos(0, targetY)
	sp.scroll.Refresh()
	sp.setActiveIndex(idx)
}

// SyncVisibility shows/hides section buttons to match target visibility and hides the rail
// automatically when 1 or fewer sections are visible.
func (sp *sectionedPanel) SyncVisibility() {
	if sp == nil {
		return
	}
	visibleCount := 0
	firstVisible := -1
	activeStillVisible := false

	for i, sec := range sp.sections {
		vis := sp.isSectionVisible(sec)
		if btn := sp.buttons[i]; btn != nil {
			if vis {
				btn.Show()
			} else {
				btn.Hide()
			}
		}
		if vis {
			visibleCount++
			if firstVisible < 0 {
				firstVisible = i
			}
			if i == sp.activeIdx {
				activeStillVisible = true
			}
		}
	}

	if sp.railWrap != nil {
		if visibleCount <= 1 {
			sp.railWrap.Hide()
		} else {
			sp.railWrap.Show()
		}
	}

	if !activeStillVisible {
		sp.setActiveIndex(firstVisible)
	}
	if sp.railBox != nil {
		sp.railBox.Refresh()
	}
	if sp.root != nil {
		sp.root.Refresh()
	}
}

func (sp *sectionedPanel) syncActiveFromScroll(scrollY float32) {
	if sp == nil || sp.scroll == nil || sp.scroll.Content == nil {
		return
	}
	best := -1
	firstVisible := -1
	for i, sec := range sp.sections {
		if !sp.isSectionVisible(sec) {
			continue
		}
		if firstVisible < 0 {
			firstVisible = i
		}
		if y, ok := findTargetYOffset(sp.scroll.Content, sec.Target); ok && y <= scrollY+24 {
			best = i
		}
	}
	if best < 0 {
		best = firstVisible
	}
	if best >= 0 {
		sp.setActiveIndex(best)
	}
}

func (sp *sectionedPanel) setActiveIndex(idx int) {
	sp.activeIdx = idx
	for i, btn := range sp.buttons {
		if btn == nil {
			continue
		}
		want := widget.LowImportance
		if i == idx {
			want = widget.HighImportance
		}
		if btn.Importance != want {
			btn.Importance = want
			btn.Refresh()
		}
	}
}

func (sp *sectionedPanel) isSectionVisible(sec panelSection) bool {
	if sp == nil || sec.Target == nil || !sec.Target.Visible() {
		return false
	}
	if sp.scroll == nil || sp.scroll.Content == nil {
		return sec.Target.Visible()
	}
	_, ok := findTargetYOffset(sp.scroll.Content, sec.Target)
	return ok
}

// findTargetYOffset returns target's Y coordinate relative to root by summing visible
// descendant Position().Y values from root down to target.
func findTargetYOffset(root, target fyne.CanvasObject) (float32, bool) {
	if root == nil || target == nil {
		return 0, false
	}
	if root == target {
		return 0, true
	}
	return walkTargetYOffset(root, target, 0)
}

func walkTargetYOffset(node, target fyne.CanvasObject, accumY float32) (float32, bool) {
	if node == nil {
		return 0, false
	}
	switch co := node.(type) {
	case *fyne.Container:
		for _, child := range co.Objects {
			if child == nil || !child.Visible() {
				continue
			}
			nextY := accumY + child.Position().Y
			if child == target {
				return nextY, true
			}
			if y, ok := walkTargetYOffset(child, target, nextY); ok {
				return y, true
			}
		}
	case *widget.Card:
		if co.Content != nil && co.Content.Visible() {
			nextY := accumY + co.Content.Position().Y
			if co.Content == target {
				return nextY, true
			}
			if y, ok := walkTargetYOffset(co.Content, target, nextY); ok {
				return y, true
			}
		}
	case *container.Scroll:
		if co.Content != nil && co.Content.Visible() {
			nextY := accumY + co.Content.Position().Y
			if co.Content == target {
				return nextY, true
			}
			if y, ok := walkTargetYOffset(co.Content, target, nextY); ok {
				return y, true
			}
		}
	}
	return 0, false
}
