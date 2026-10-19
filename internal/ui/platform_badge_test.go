package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

func TestPlatformConnectionBadgeInit(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	badge := newPlatformConnectionBadge("Twitch", resPlatformTwitch, "⚪", "Twitch: Inactive", nil, nil)
	if badge == nil {
		t.Fatal("expected non-nil platformConnectionBadge")
	}

	if badge.StatusDot() != "⚪" {
		t.Errorf("expected initial status dot '⚪', got %q", badge.StatusDot())
	}
	if badge.Tooltip() != "Twitch: Inactive" {
		t.Errorf("expected initial tooltip 'Twitch: Inactive', got %q", badge.Tooltip())
	}
	if badge.Cursor() != desktop.DefaultCursor {
		t.Errorf("expected default cursor without onTap, got %v", badge.Cursor())
	}
}

func TestPlatformConnectionBadgeSetStatus(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	badge := newPlatformConnectionBadge("YouTube", resPlatformYouTube, "⚪", "YouTube: Inactive", nil, nil)
	badge.SetStatus("🟢", "YouTube: Connected (#stream)")

	if badge.StatusDot() != "🟢" {
		t.Errorf("expected status dot '🟢', got %q", badge.StatusDot())
	}
	if badge.Tooltip() != "YouTube: Connected (#stream)" {
		t.Errorf("expected tooltip 'YouTube: Connected (#stream)', got %q", badge.Tooltip())
	}
}

func TestPlatformConnectionBadgeHover(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	var hoveredTip string
	var isHovered bool
	onHover := func(obj fyne.CanvasObject, tip string, h bool) {
		hoveredTip = tip
		isHovered = h
	}

	badge := newPlatformConnectionBadge("TikTok", resPlatformTikTok, "🟡", "TikTok: Waiting...", onHover, nil)

	// Simulate MouseIn
	badge.MouseIn(&desktop.MouseEvent{})
	if !isHovered || hoveredTip != "TikTok: Waiting..." {
		t.Errorf("expected hover true with tip 'TikTok: Waiting...', got hovered=%v tip=%q", isHovered, hoveredTip)
	}

	// MouseMoved does nothing
	badge.MouseMoved(&desktop.MouseEvent{})

	// Simulate MouseOut
	badge.MouseOut()
	if isHovered || hoveredTip != "" {
		t.Errorf("expected hover false with empty tip, got hovered=%v tip=%q", isHovered, hoveredTip)
	}
}

func TestPlatformConnectionBadgeTap(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	tapped := false
	badge := newPlatformConnectionBadge("Kick", resPlatformKick, "🟢", "Kick: Connected", nil, func() {
		tapped = true
	})

	if badge.Cursor() != desktop.PointerCursor {
		t.Errorf("expected pointer cursor with onTap assigned, got %v", badge.Cursor())
	}

	badge.Tapped(&fyne.PointEvent{})
	if !tapped {
		t.Error("expected onTap callback to be invoked on tap")
	}
}
