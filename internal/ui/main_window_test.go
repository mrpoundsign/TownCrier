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
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	mw := NewMainWindow(appObj, session)
	if mw == nil || mw.Window == nil {
		t.Fatal("expected non-nil MainWindow and Window")
	}

	if mw.dashboardBtn == nil || mw.viewersBtn == nil || mw.logsBtn == nil || mw.settingsBtn == nil {
		t.Fatal("expected non-nil navigation buttons")
	}

	if mw.dashboardBtn.Text != "Dashboard" {
		t.Errorf("expected 'Dashboard', got %q", mw.dashboardBtn.Text)
	}
	if mw.viewersBtn.Text != "Viewers" {
		t.Errorf("expected 'Viewers', got %q", mw.viewersBtn.Text)
	}
	if mw.logsBtn.Text != "Logs" {
		t.Errorf("expected 'Logs', got %q", mw.logsBtn.Text)
	}
	if mw.settingsBtn.Text != "" {
		t.Errorf("expected empty text for settings gear button, got %q", mw.settingsBtn.Text)
	}

	if mw.currentTab != TabIndexDashboard {
		t.Errorf("expected initial tab Dashboard (%d), got %d", TabIndexDashboard, mw.currentTab)
	}

	// Test switching tabs
	mw.SelectTab(TabIndexLogs)
	if mw.currentTab != TabIndexLogs {
		t.Errorf("expected current tab Logs (%d), got %d", TabIndexLogs, mw.currentTab)
	}
}

func TestMainWindowCloseIntercept(t *testing.T) {
	appObj := test.NewApp()
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
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
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
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
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	mw := NewMainWindow(appObj, session)
	defer func() {
		if mw.hotkeyMgr != nil {
			mw.hotkeyMgr.Close()
		}
	}()

	// Settings sub-tabs initially have 8 items: General & Audio, TTS & Hardware, Twitch, YouTube, TikTok, Kick, Rules & Safety, Event Logs
	if len(mw.settingsTab.subTabs.Items) != 8 {
		t.Fatalf("expected 8 settings sub-tabs initially, got %d", len(mw.settingsTab.subTabs.Items))
	}

	// Disable YouTube in Settings
	mw.settingsTab.youtubeCheck.SetChecked(false)
	if len(mw.settingsTab.subTabs.Items) != 7 {
		t.Fatalf("expected 7 settings sub-tabs after disabling YouTube, got %d", len(mw.settingsTab.subTabs.Items))
	}
	if mw.youtubeStatusDot.Visible() {
		t.Error("expected youtubeStatusDot to be hidden when YouTube service is disabled")
	}
	if session.Config().Platforms["youtube"].Enabled {
		t.Error("expected youtube Enabled to be false when unchecked in Settings")
	}

	// Re-enable YouTube in Settings
	mw.settingsTab.youtubeCheck.SetChecked(true)
	if len(mw.settingsTab.subTabs.Items) != 8 {
		t.Fatalf("expected 8 settings sub-tabs after re-enabling YouTube, got %d", len(mw.settingsTab.subTabs.Items))
	}
	if !mw.youtubeStatusDot.Visible() {
		t.Error("expected youtubeStatusDot to be visible when YouTube service is enabled")
	}
	if !session.Config().Platforms["youtube"].Enabled {
		t.Error("expected youtube Enabled to be true when checked in Settings")
	}

	// Disable TikTok in Settings
	mw.settingsTab.tiktokCheck.SetChecked(false)
	if len(mw.settingsTab.subTabs.Items) != 7 {
		t.Fatalf("expected 7 settings sub-tabs after disabling TikTok, got %d", len(mw.settingsTab.subTabs.Items))
	}
	if mw.tiktokStatusDot.Visible() {
		t.Error("expected tiktokStatusDot to be hidden when TikTok service is disabled")
	}

	// Disable Kick in Settings
	mw.settingsTab.kickCheck.SetChecked(false)
	if len(mw.settingsTab.subTabs.Items) != 6 {
		t.Fatalf("expected 6 settings sub-tabs after disabling Kick, got %d", len(mw.settingsTab.subTabs.Items))
	}
	if mw.kickStatusDot.Visible() {
		t.Error("expected kickStatusDot to be hidden when Kick service is disabled")
	}
}

func TestMainWindowPlatformStatusResolution(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	cfg := config.DefaultConfig()
	cfg.Platforms["twitch"] = config.PlatformConfig{
		Enabled:     false,
		AccountName: "",
	}
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	mw := NewMainWindow(appObj, session)
	defer func() {
		if mw.hotkeyMgr != nil {
			mw.hotkeyMgr.Close()
		}
		mw.Window.Close()
	}()

	// 1. Disabled platform returns ⚪ Inactive
	dot, tip := mw.resolvePlatformStatus("twitch", "disconnected", "")
	if dot != "⚪" || tip != "Twitch: Inactive" {
		t.Errorf("expected ⚪ Twitch: Inactive, got %s %s", dot, tip)
	}

	// 2. Enabled but no channel returns 🟡 Enabled (No Channel Configured)
	c := session.Config()
	pCfg := c.Platforms["twitch"]
	pCfg.Enabled = true
	pCfg.AccountName = ""
	c.Platforms["twitch"] = pCfg
	_ = session.ReloadConfig(c)

	dot, tip = mw.resolvePlatformStatus("twitch", "disconnected", "")
	if dot != "🟡" || tip != "Twitch: Enabled (No Channel Configured)" {
		t.Errorf("expected 🟡 Twitch: Enabled (No Channel Configured), got %s %s", dot, tip)
	}

	// 3. Enabled with channel configured
	pCfg.AccountName = "streamer"
	c.Platforms["twitch"] = pCfg
	_ = session.ReloadConfig(c)

	dot, tip = mw.resolvePlatformStatus("twitch", "connected", "#streamer")
	if dot != "🟢" || tip != "Twitch: Live (#streamer)" {
		t.Errorf("expected 🟢 Twitch: Live (#streamer), got %s %s", dot, tip)
	}

	dot, tip = mw.resolvePlatformStatus("twitch", "waiting", "")
	if dot != "🟡" || tip != "Twitch: Waiting for stream..." {
		t.Errorf("expected 🟡 Twitch: Waiting for stream..., got %s %s", dot, tip)
	}

	dot, tip = mw.resolvePlatformStatus("twitch", "connecting", "")
	if dot != "🟡" || tip != "Twitch: Connecting..." {
		t.Errorf("expected 🟡 Twitch: Connecting..., got %s %s", dot, tip)
	}

	dot, tip = mw.resolvePlatformStatus("twitch", "disconnected", "network down")
	if dot != "🔴" || tip != "Twitch: Disconnected (network down)" {
		t.Errorf("expected 🔴 Twitch: Disconnected (network down), got %s %s", dot, tip)
	}

	// 4. Test updatePlatformBadge propagates to status badge widget
	mw.updatePlatformBadge("twitch", "connected", "live")
	if mw.twitchStatusDot.StatusDot() != "🟢" {
		t.Errorf("expected badge status dot 🟢, got %s", mw.twitchStatusDot.StatusDot())
	}
	if mw.twitchStatusDot.Tooltip() != "Twitch: Live (live)" {
		t.Errorf("expected badge tooltip 'Twitch: Live (live)', got %s", mw.twitchStatusDot.Tooltip())
	}

	// 5. Test clicking platform badge routes to Settings -> platform sub-tab
	test.Tap(mw.twitchStatusDot)
	if mw.currentTab != TabIndexSettings {
		t.Errorf("expected currentTab to be Settings (%d), got %d", TabIndexSettings, mw.currentTab)
	}
	if sel := mw.settingsTab.subTabs.Selected(); sel == nil || sel.Text != "Twitch" {
		t.Errorf("expected selected Settings sub-tab to be 'Twitch', got %+v", sel)
	}
}



