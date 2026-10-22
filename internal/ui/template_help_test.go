package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestEventTemplateVarsCoverage(t *testing.T) {
	platforms := []string{"twitch", "youtube", "tiktok", "kick"}
	for _, p := range platforms {
		vars := EventTemplateVars(p, "sub")
		if len(vars) == 0 {
			t.Errorf("expected template vars for %s sub", p)
		}
		foundUser := false
		for _, v := range vars {
			if v.Var == "{user}" {
				foundUser = true
				break
			}
		}
		if !foundUser {
			t.Errorf("expected {user} in %s sub template vars", p)
		}
	}

	// Specific Twitch watch_streak vars
	wsVars := EventTemplateVars("twitch", "watch_streak")
	if len(wsVars) < 3 {
		t.Fatalf("expected at least 3 vars for twitch watch_streak, got %d", len(wsVars))
	}
	hasStreak := false
	hasAmount := false
	for _, v := range wsVars {
		if v.Var == "{count}" {
			hasStreak = true
		}
		if v.Var == "{amount}" {
			hasAmount = true
		}
	}
	if !hasStreak || !hasAmount {
		t.Errorf("expected {count} and {amount} in watch_streak vars, got %+v", wsVars)
	}
}

func TestTemplateHelpMarkerLifecycle(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	win := app.NewWindow("Test")
	defer win.Close()

	var statusMsg string
	vars := []TemplateVar{
		{Var: "{user}", Desc: "Username"},
		{Var: "{count}", Desc: "Count"},
	}
	entry := widget.NewEntry()
	marker := newTemplateHelpMarker(win, "test", "Test Alert", vars, func(msg string) {
		statusMsg = msg
	}, entry)

	if marker.minSize != fyne.NewSize(20, 20) {
		t.Errorf("unexpected minSize: %v", marker.minSize)
	}

	// MouseIn triggers status
	marker.MouseIn(nil)
	if statusMsg == "" {
		t.Error("expected onStatus to be called on MouseIn")
	}

	// Tap triggers modal dialog without panic
	marker.Tapped(nil)

	// Test insertVariable helper
	entry.SetText("Hello")
	entry.CursorColumn = 5
	insertVariable(entry, "{user}")
	if entry.Text != "Hello {user}" {
		t.Errorf("expected 'Hello {user}', got '%s'", entry.Text)
	}

	// Insert into empty entry
	emptyEntry := widget.NewEntry()
	insertVariable(emptyEntry, "{user}")
	if emptyEntry.Text != "{user}" {
		t.Errorf("expected '{user}', got '%s'", emptyEntry.Text)
	}
}
