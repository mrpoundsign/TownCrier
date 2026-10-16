package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// lastScrollableChild returns the last fyne.Scrollable in the entry's renderer objects,
// matching Fyne's FindObjectAtPositionMatching behavior for Scrollable hit-testing.
func lastScrollableChild(t *testing.T, e *ScrollPassEntry) fyne.Scrollable {
	t.Helper()
	r := test.WidgetRenderer(e)
	var found fyne.Scrollable
	for _, obj := range r.Objects() {
		if sc, ok := obj.(fyne.Scrollable); ok && obj.Visible() {
			found = sc
		}
	}
	if found == nil {
		t.Fatal("expected at least one visible fyne.Scrollable in ScrollPassEntry renderer")
	}
	return found
}

func TestScrollPassEntry_UnfocusedScrollsParentPanel(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestScrollPass")

	entry := NewScrollPassEntry()
	entry.SetText("some text")

	// Build tall content so the VScroll container has vertical scroll range
	vbox := container.NewVBox(entry)
	for range 30 {
		vbox.Add(widget.NewLabel("Filler line to ensure vertical scrolling range"))
	}

	panelScroll := newPanelVScroll(vbox)
	w.SetContent(panelScroll)
	w.Resize(fyne.NewSize(300, 150))

	if entry.Focused() {
		t.Fatal("expected entry to start unfocused")
	}

	target := lastScrollableChild(t, entry)

	// Scroll down while entry is unfocused -> should scroll the parent panel
	target.Scrolled(&fyne.ScrollEvent{
		Scrolled: fyne.NewDelta(0, -50),
	})

	if panelScroll.Offset.Y <= 0 {
		t.Errorf("expected parent panel Offset.Y > 0 after scrolling over unfocused entry, got %v", panelScroll.Offset.Y)
	}

	offsetAfterUnfocused := panelScroll.Offset.Y

	// Focus the entry -> scrolling should now be captured by the entry, not the parent panel
	w.Canvas().Focus(entry)
	if !entry.Focused() {
		t.Fatal("expected entry to be focused after Canvas().Focus(entry)")
	}

	target.Scrolled(&fyne.ScrollEvent{
		Scrolled: fyne.NewDelta(0, -50),
	})

	if panelScroll.Offset.Y != offsetAfterUnfocused {
		t.Errorf("expected parent panel Offset.Y to remain %v when entry is focused, got %v", offsetAfterUnfocused, panelScroll.Offset.Y)
	}

	// Unfocus the entry -> scrolling should once again scroll the parent panel
	w.Canvas().Unfocus()
	if entry.Focused() {
		t.Fatal("expected entry to be unfocused after Canvas().Unfocus()")
	}

	target.Scrolled(&fyne.ScrollEvent{
		Scrolled: fyne.NewDelta(0, -50),
	})

	if panelScroll.Offset.Y <= offsetAfterUnfocused {
		t.Errorf("expected parent panel Offset.Y > %v after unfocusing and scrolling, got %v", offsetAfterUnfocused, panelScroll.Offset.Y)
	}
}

func TestScrollPassEntry_CanvasFallbackResolution(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestCanvasFallback")

	entry := NewScrollPassEntry()
	vbox := container.NewVBox(widget.NewCard("Card", "", container.NewVBox(entry)))
	for range 30 {
		vbox.Add(widget.NewLabel("Filler row"))
	}

	// Use standard container.NewVScroll without pre-binding via newPanelVScroll
	panelScroll := container.NewVScroll(vbox)
	w.SetContent(panelScroll)
	w.Resize(fyne.NewSize(300, 150))

	target := lastScrollableChild(t, entry)
	target.Scrolled(&fyne.ScrollEvent{
		Scrolled: fyne.NewDelta(0, -40),
	})

	if panelScroll.Offset.Y <= 0 {
		t.Errorf("expected canvas fallback to resolve enclosing VScroll and scroll Offset.Y > 0, got %v", panelScroll.Offset.Y)
	}
}
