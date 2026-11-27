package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestSectionedPanel_ScrollToAndSyncVisibility(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	makeBlock := func(title string) *widget.Card {
		lbl := widget.NewLabel(title + "\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6")
		return widget.NewCard(title, "Subtitle", lbl)
	}

	c1 := makeBlock("Section One")
	nestedBtn := widget.NewButton("Nested Target", nil)
	c2 := widget.NewCard("Section Two", "", container.NewVBox(
		widget.NewLabel("Spacer line 1\nSpacer line 2\nSpacer line 3\nSpacer line 4"),
		nestedBtn,
		widget.NewLabel("Spacer line 5\nSpacer line 6"),
	))
	c3 := makeBlock("Section Three")

	content := container.NewVBox(c1, c2, c3)
	sp := newSectionedPanelVScroll(content, []panelSection{
		{Label: "One", Target: c1},
		{Label: "Nested", Target: nestedBtn},
		{Label: "Three", Target: c3},
	})

	w := appObj.NewWindow("SectionNavTest")
	w.SetContent(sp.Root())
	w.Resize(fyne.NewSize(900, 260))
	w.Show()

	if !sp.railWrap.Visible() {
		t.Fatal("expected section rail to be visible when 3 sections are visible")
	}

	// Scroll to nested target inside card c2
	sp.ScrollTo(1)
	if sp.scroll.Offset.Y <= 0 {
		t.Fatalf("expected scroll.Offset.Y > 0 after jumping to nested section, got %v", sp.scroll.Offset.Y)
	}
	if sp.buttons[1].Importance != widget.HighImportance {
		t.Errorf("expected active button 1 to have HighImportance, got %v", sp.buttons[1].Importance)
	}
	if sp.buttons[0].Importance != widget.LowImportance {
		t.Errorf("expected inactive button 0 to have LowImportance, got %v", sp.buttons[0].Importance)
	}

	// Verify OnScrolled updates active button highlight back to section 0 at top
	sp.scroll.Offset = fyne.NewPos(0, 0)
	sp.scroll.OnScrolled(sp.scroll.Offset)
	if sp.buttons[0].Importance != widget.HighImportance {
		t.Errorf("expected scrolling to top to highlight button 0, got %v", sp.buttons[0].Importance)
	}

	// Hide c2 and c3 -> only 1 visible section -> rail auto-hides
	c2.Hide()
	c3.Hide()
	sp.SyncVisibility()
	if sp.buttons[1].Visible() || sp.buttons[2].Visible() {
		t.Fatal("expected buttons for hidden sections to be hidden")
	}
	if sp.railWrap.Visible() {
		t.Fatal("expected section rail to auto-hide when <= 1 section is visible")
	}

	// Re-show c3 -> 2 visible sections -> rail re-shows
	c3.Show()
	sp.SyncVisibility()
	if !sp.railWrap.Visible() {
		t.Fatal("expected section rail to re-show when >= 2 sections are visible")
	}
	if !sp.buttons[2].Visible() {
		t.Fatal("expected button 2 to be visible after c3.Show()")
	}
}
