package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestTwitchTabViewBuild(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())

	tab := NewTwitchTabView(win, session)
	content := tab.Build()
	if content == nil {
		t.Fatal("expected non-nil content from TwitchTabView.Build")
	}

	tab.channelEntry.SetText("streamer_test")
	if session.Config().Platforms["twitch"].AccountName != "streamer_test" {
		t.Errorf("expected channel 'streamer_test', got %q", session.Config().Platforms["twitch"].AccountName)
	}

	tab.enableCheck.SetChecked(true)
	if !session.Config().Platforms["twitch"].Enabled {
		t.Error("expected twitch enabled to be true")
	}

	tab.ignorePrefixCheck.SetChecked(true)
	tab.ignorePrefixEntry.SetText("!, ?, $")
	ignRules := session.Config().Platforms["twitch"].ReadRules
	if !ignRules.IgnorePrefixes {
		t.Error("expected IgnorePrefixes to be true")
	}
	if len(ignRules.IgnoredPrefixes) != 3 || ignRules.IgnoredPrefixes[1] != "?" {
		t.Errorf("expected 3 prefixes with ?, got %v", ignRules.IgnoredPrefixes)
	}
}

func TestTwitchTabViewBuildPreEnabled(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("TestPreEnabled")
	cfg := config.DefaultConfig()
	p := cfg.Platforms["twitch"]
	p.Enabled = true
	p.AccountName = "mrpoundsign"
	p.ReadRules.RoleGated = true
	p.ReadRules.Roles = []string{"mod", "broadcaster"}
	cfg.Platforms["twitch"] = p
	session := app.NewSession(cfg, secrets.NewMemoryStore())

	tab := NewTwitchTabView(win, session)
	content := tab.Build()
	if content == nil {
		t.Fatal("expected non-nil content from TwitchTabView.Build")
	}

	if tab.statusBadge == nil || tab.statusBadge.Text != "🟢 Ready (mrpoundsign)" {
		t.Errorf("expected ready badge, got %v", tab.statusBadge)
	}
}

func TestTwitchTabStatusHandler(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("TestStatus")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())

	tab := NewTwitchTabView(win, session)
	var lastStatus string
	tab.SetStatusHandler(func(msg string) {
		lastStatus = msg
	})

	_ = tab.Build()

	tab.overrideCheck.SetChecked(true)
	if lastStatus != "✓ Settings saved." {
		t.Errorf("expected '✓ Settings saved.', got %q", lastStatus)
	}
	if !session.Config().Platforms["twitch"].Overrides.OverrideRules {
		t.Error("expected OverrideRules to be true")
	}
}
