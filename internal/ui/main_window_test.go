package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestMainWindowCreation(t *testing.T) {
	appObj := test.NewApp()
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())

	mw := NewMainWindow(appObj, session)
	if mw == nil || mw.Window == nil {
		t.Fatal("expected non-nil MainWindow and Window")
	}

	if mw.tabsContainer == nil {
		t.Fatal("expected non-nil tabsContainer")
	}

	// Should have 5 tabs: Dashboard, Twitch, Voices, Viewers, Settings
	if len(mw.tabsContainer.Items) != 5 {
		t.Errorf("expected 5 tabs, got %d", len(mw.tabsContainer.Items))
	}

	expectedTabs := []string{"Dashboard", "Twitch", "Voices", "Viewers", "Settings"}
	for i, name := range expectedTabs {
		if mw.tabsContainer.Items[i].Text != name {
			t.Errorf("tab %d: expected %q, got %q", i, name, mw.tabsContainer.Items[i].Text)
		}
	}
}

func TestMainWindowCloseIntercept(t *testing.T) {
	appObj := test.NewApp()
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	_ = session.Start()

	mw := NewMainWindow(appObj, session)
	if mw == nil || mw.Window == nil {
		t.Fatal("expected non-nil MainWindow")
	}

	// Trigger window close (invokes SetCloseIntercept)
	mw.Window.Close()

	if session.IsRunning() {
		t.Error("expected session to be stopped after window close intercept")
	}
}

func TestMainWindowPanicToggle(t *testing.T) {
	appObj := test.NewApp()
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	_ = session.Start()

	mw := NewMainWindow(appObj, session)

	if session.IsPanicked() {
		t.Fatal("expected session to not be panicked initially")
	}

	// Activate Panic Mode
	mw.triggerTogglePanic()
	if !session.IsPanicked() {
		t.Error("expected session to be panicked after first toggle")
	}
	if mw.panicBtn.Text != "🚨 PANIC MUTED — Click or Esc to Unmute" {
		t.Errorf("unexpected panicBtn text when panicked: %q", mw.panicBtn.Text)
	}

	// Deactivate Panic Mode
	mw.triggerTogglePanic()
	if session.IsPanicked() {
		t.Error("expected session to not be panicked after second toggle")
	}
	if mw.panicBtn.Text != "PANIC MUTE (Esc / Ctrl+Shift+M)" {
		t.Errorf("unexpected panicBtn text when unmuted: %q", mw.panicBtn.Text)
	}
}

