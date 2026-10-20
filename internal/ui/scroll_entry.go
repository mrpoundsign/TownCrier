package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// ScrollPassEntry is a single-line text entry widget that forwards mouse wheel
// scroll events to its enclosing panel scroll container when the entry is not
// focused, while retaining normal internal text scrolling when focused.
type ScrollPassEntry struct {
	widget.Entry
	focused      bool
	parentScroll fyne.Scrollable
	innerScroll  fyne.Scrollable
}

// NewScrollPassEntry creates a new single-line ScrollPassEntry.
func NewScrollPassEntry() *ScrollPassEntry {
	e := &ScrollPassEntry{}
	e.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	e.ExtendBaseWidget(e)
	return e
}

// FocusGained marks the entry as focused so scroll wheel events control the entry text.
func (e *ScrollPassEntry) FocusGained() {
	e.focused = true
	e.Entry.FocusGained()
}

// FocusLost marks the entry as unfocused so scroll wheel events pass through to the parent panel.
func (e *ScrollPassEntry) FocusLost() {
	e.focused = false
	e.Entry.FocusLost()
}

// DoubleTapped marks the entry as focused and delegates to widget.Entry.
func (e *ScrollPassEntry) DoubleTapped(pe *fyne.PointEvent) {
	e.focused = true
	e.Entry.DoubleTapped(pe)
}

// Focused reports whether the entry currently holds input focus.
func (e *ScrollPassEntry) Focused() bool {
	return e.focused
}

// SetParentScroll explicitly binds the enclosing scroll container that should
// receive scroll events when this entry is unfocused.
func (e *ScrollPassEntry) SetParentScroll(s fyne.Scrollable) {
	e.parentScroll = s
}

func (e *ScrollPassEntry) handleScrolled(ev *fyne.ScrollEvent) {
	if e.focused && !e.Disabled() {
		if e.innerScroll != nil {
			e.innerScroll.Scrolled(ev)
		}
		return
	}
	if sc := e.resolveParentScroll(); sc != nil {
		sc.Scrolled(ev)
	}
}

func (e *ScrollPassEntry) resolveParentScroll() fyne.Scrollable {
	if e.parentScroll != nil {
		return e.parentScroll
	}
	app := fyne.CurrentApp()
	if app == nil || app.Driver() == nil {
		return nil
	}
	c := app.Driver().CanvasForObject(e)
	if c == nil {
		return nil
	}
	if overlays := c.Overlays(); overlays != nil {
		for _, ov := range overlays.List() {
			if sc := findEnclosingScroll(ov, e, nil); sc != nil {
				e.parentScroll = sc
				return sc
			}
		}
	}
	if sc := findEnclosingScroll(c.Content(), e, nil); sc != nil {
		e.parentScroll = sc
		return sc
	}
	return nil
}

func findEnclosingScroll(node, target fyne.CanvasObject, currentScroll fyne.Scrollable) fyne.Scrollable {
	if node == nil {
		return nil
	}
	if node == target {
		return currentScroll
	}

	nextScroll := currentScroll
	if sc, ok := node.(fyne.Scrollable); ok {
		nextScroll = sc
	}

	switch co := node.(type) {
	case *container.Scroll:
		return findEnclosingScroll(co.Content, target, co)
	case *fyne.Container:
		for _, child := range co.Objects {
			if res := findEnclosingScroll(child, target, nextScroll); res != nil {
				return res
			}
		}
	case *widget.Card:
		return findEnclosingScroll(co.Content, target, nextScroll)
	case *container.AppTabs:
		for _, item := range co.Items {
			if item != nil {
				if res := findEnclosingScroll(item.Content, target, nextScroll); res != nil {
					return res
				}
			}
		}
	}
	return nil
}

const defaultPanelMinWidth float32 = 480

// panelWidthClampLayout clamps the horizontal MinSize reported to an enclosing
// vertical Scroll container so wide child widgets (long labels, selects, or cards)
// never inflate the OS window's minimum width limit, while still stretching the
// child across 100% of the available scroll viewport width during Layout.
type panelWidthClampLayout struct {
	minWidth float32
}

func (l *panelWidthClampLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		h := max(size.Height, obj.MinSize().Height)
		obj.Move(fyne.NewPos(0, 0))
		obj.Resize(fyne.NewSize(size.Width, h))
		if newH := max(size.Height, obj.MinSize().Height); newH != h {
			obj.Resize(fyne.NewSize(size.Width, newH))
		}
	}
}

func (l *panelWidthClampLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var maxW, maxH float32
	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		ms := obj.MinSize()
		if ms.Width > maxW {
			maxW = ms.Width
		}
		if ms.Height > maxH {
			maxH = ms.Height
		}
	}
	clampW := l.minWidth
	if clampW <= 0 {
		clampW = defaultPanelMinWidth
	}
	if maxW > clampW {
		maxW = clampW
	}
	return fyne.NewSize(maxW, maxH)
}

// newWidthClampContainer wraps content in a container that clamps its reported
// horizontal MinSize to defaultPanelMinWidth while stretching content to 100%
// of the available width during Layout.
func newWidthClampContainer(content fyne.CanvasObject) *fyne.Container {
	return container.New(&panelWidthClampLayout{minWidth: defaultPanelMinWidth}, content)
}

// newPanelVScroll wraps content in a width-clamped vertical scroll container and
// binds all descendant ScrollPassEntry widgets to forward unfocused wheel events to it.
func newPanelVScroll(content fyne.CanvasObject) *container.Scroll {
	clamped := newWidthClampContainer(content)
	scroll := container.NewVScroll(clamped)
	bindScrollPassEntries(content, scroll)
	return scroll
}

func indentLeft(px float32, obj fyne.CanvasObject) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(px, 1))
	return container.NewBorder(nil, nil, spacer, nil, obj)
}

func bindScrollPassEntries(node fyne.CanvasObject, scroll fyne.Scrollable) {
	if node == nil {
		return
	}
	switch co := node.(type) {
	case *ScrollPassEntry:
		co.SetParentScroll(scroll)
	case *container.Scroll:
		bindScrollPassEntries(co.Content, co)
	case *fyne.Container:
		for _, child := range co.Objects {
			bindScrollPassEntries(child, scroll)
		}
	case *widget.Card:
		bindScrollPassEntries(co.Content, scroll)
	}
}

// CreateRenderer wraps widget.Entry's renderer with a trailing scrollCatcher so
// Fyne's Scrollable hit-testing routes wheel events through ScrollPassEntry.
func (e *ScrollPassEntry) CreateRenderer() fyne.WidgetRenderer {
	e.ExtendBaseWidget(e)
	base := e.Entry.CreateRenderer()
	for _, obj := range base.Objects() {
		if sc, ok := obj.(fyne.Scrollable); ok {
			e.innerScroll = sc
			break
		}
	}
	return &scrollPassEntryRenderer{
		base:    base,
		entry:   e,
		catcher: &scrollCatcher{entry: e},
	}
}

type scrollCatcher struct {
	entry  *ScrollPassEntry
	pos    fyne.Position
	size   fyne.Size
	hidden bool
}

var (
	_ fyne.CanvasObject = (*scrollCatcher)(nil)
	_ fyne.Scrollable   = (*scrollCatcher)(nil)
)

func (c *scrollCatcher) MinSize() fyne.Size      { return fyne.Size{} }
func (c *scrollCatcher) Move(p fyne.Position)    { c.pos = p }
func (c *scrollCatcher) Position() fyne.Position { return c.pos }
func (c *scrollCatcher) Resize(s fyne.Size)      { c.size = s }
func (c *scrollCatcher) Size() fyne.Size          { return c.size }
func (c *scrollCatcher) Hide()                   { c.hidden = true }
func (c *scrollCatcher) Visible() bool           { return !c.hidden }
func (c *scrollCatcher) Show()                   { c.hidden = false }
func (c *scrollCatcher) Refresh()                {}

func (c *scrollCatcher) Scrolled(ev *fyne.ScrollEvent) {
	if c.entry != nil {
		c.entry.handleScrolled(ev)
	}
}

type scrollPassEntryRenderer struct {
	base    fyne.WidgetRenderer
	entry   *ScrollPassEntry
	catcher *scrollCatcher
}

func (r *scrollPassEntryRenderer) Destroy() {
	r.base.Destroy()
}

func (r *scrollPassEntryRenderer) Layout(size fyne.Size) {
	r.base.Layout(size)
	r.catcher.Move(fyne.NewPos(0, 0))
	r.catcher.Resize(size)
}

func (r *scrollPassEntryRenderer) MinSize() fyne.Size {
	return r.base.MinSize()
}

func (r *scrollPassEntryRenderer) Objects() []fyne.CanvasObject {
	baseObjs := r.base.Objects()
	objs := make([]fyne.CanvasObject, len(baseObjs)+1)
	copy(objs, baseObjs)
	objs[len(baseObjs)] = r.catcher
	return objs
}

func (r *scrollPassEntryRenderer) Refresh() {
	if r.entry.innerScroll == nil {
		for _, obj := range r.base.Objects() {
			if sc, ok := obj.(fyne.Scrollable); ok {
				r.entry.innerScroll = sc
				break
			}
		}
	}
	r.base.Refresh()
	r.catcher.Resize(r.entry.Size())
}
