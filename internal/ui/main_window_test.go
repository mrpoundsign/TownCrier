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

	// Should have 9 tabs: Dashboard, Twitch, YouTube, TikTok, Kick, Voices, Viewers, Chat Logs, Settings
	if len(mw.tabsContainer.Items) != 9 {
		t.Errorf("expected 9 tabs, got %d", len(mw.tabsContainer.Items))
	}

	expectedTabs := []string{"Dashboard", "Twitch", "YouTube", "TikTok", "Kick", "Voices", "Viewers", "Chat Logs", "Settings"}
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
	defer func() {
		if mw.hotkeyMgr != nil {
			mw.hotkeyMgr.Close()
		}
	}()

	if session.IsPanicked() {
		t.Fatal("expected session to not be panicked initially")
	}

	// Activate Panic Mode
	mw.triggerTogglePanic()
	if !session.IsPanicked() {
		t.Error("expected session to be panicked after first toggle")
	}
	if mw.panicBtn.Text != "🚨 PANIC MUTED — Click or Ctrl+Shift+M to Unmute" {
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

	// Test custom hotkey application
	_ = session.UpdateConfig(func(c *config.Config) {
		c.Safety.PanicHotkey = "Alt+F9"
	})
	if err := mw.ApplyPanicHotkey("Alt+F9"); err != nil {
		t.Errorf("ApplyPanicHotkey failed: %v", err)
	}
	if mw.panicBtn.Text != "PANIC MUTE (Esc / Alt+F9)" {
		t.Errorf("unexpected panicBtn text after hotkey change: %q", mw.panicBtn.Text)
	}
}

func TestMainWindowServiceVisibilityToggle(t *testing.T) {
	appObj := test.NewApp()
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	mw := NewMainWindow(appObj, session)
	defer func() {
		if mw.hotkeyMgr != nil {
			mw.hotkeyMgr.Close()
		}
	}()

	// Initially 9 tabs (Dashboard, Twitch, YouTube, TikTok, Kick, Voices, Viewers, Chat Logs, Settings)
	if len(mw.tabsContainer.Items) != 9 {
		t.Fatalf("expected 9 tabs initially, got %d", len(mw.tabsContainer.Items))
	}

	// Disable YouTube in Settings
	mw.settingsTab.youtubeCheck.SetChecked(false)
	if len(mw.tabsContainer.Items) != 8 {
		t.Fatalf("expected 8 tabs after disabling YouTube, got %d", len(mw.tabsContainer.Items))
	}
	if mw.youtubeStatusDot.Visible() {
		t.Error("expected youtubeStatusDot to be hidden when YouTube service is disabled")
	}
	if session.Config().Platforms["youtube"].Enabled {
		t.Error("expected youtube Enabled to be false when unchecked in Settings")
	}

	// Re-enable YouTube in Settings
	mw.settingsTab.youtubeCheck.SetChecked(true)
	if len(mw.tabsContainer.Items) != 9 {
		t.Fatalf("expected 9 tabs after re-enabling YouTube, got %d", len(mw.tabsContainer.Items))
	}
	if !mw.youtubeStatusDot.Visible() {
		t.Error("expected youtubeStatusDot to be visible when YouTube service is enabled")
	}
	if !session.Config().Platforms["youtube"].Enabled {
		t.Error("expected youtube Enabled to be true when checked in Settings")
	}

	// Disable TikTok in Settings
	mw.settingsTab.tiktokCheck.SetChecked(false)
	if len(mw.tabsContainer.Items) != 8 {
		t.Fatalf("expected 8 tabs after disabling TikTok, got %d", len(mw.tabsContainer.Items))
	}
	if mw.tiktokStatusDot.Visible() {
		t.Error("expected tiktokStatusDot to be hidden when TikTok service is disabled")
	}

	// Disable Kick in Settings
	mw.settingsTab.kickCheck.SetChecked(false)
	if len(mw.tabsContainer.Items) != 7 {
		t.Fatalf("expected 7 tabs after disabling Kick, got %d", len(mw.tabsContainer.Items))
	}
	if mw.kickStatusDot.Visible() {
		t.Error("expected kickStatusDot to be hidden when Kick service is disabled")
	}
}


