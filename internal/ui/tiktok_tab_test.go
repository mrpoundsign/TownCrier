package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestTikTokTabViewBuild(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	w := fyneApp.NewWindow("Test TikTok Tab")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	ttView := NewTikTokTabView(w, session)
	content := ttView.Build()
	if content == nil {
		t.Fatal("expected non-nil TikTok tab content")
	}

	subChk, ok := ttView.roleChecks["subscriber"]
	if !ok || subChk.Text != "Super Fan / Subscriber" {
		t.Fatalf("expected subscriber check label 'Super Fan / Subscriber', got %+v", subChk)
	}

	fcChk, ok := ttView.roleChecks["fan_club"]
	if !ok || fcChk.Text != "Fan Club" {
		t.Fatalf("expected fan_club check label 'Fan Club', got %+v", fcChk)
	}

	if !ttView.fanClubLevelEntry.Disabled() {
		t.Error("expected fanClubLevelEntry to be disabled when RoleGated is unchecked")
	}

	// Enable RoleGated and Fan Club checkbox
	ttView.roleGatedCheck.SetChecked(true)
	fcChk.SetChecked(true)
	if ttView.fanClubLevelEntry.Disabled() {
		t.Error("expected fanClubLevelEntry to be enabled when RoleGated and Fan Club are checked")
	}

	ttView.fanClubLevelEntry.SetText("15")
	gotCfg := session.Config().Platforms["tiktok"].ReadRules
	if gotCfg.MinFanClubLevel != 15 {
		t.Errorf("MinFanClubLevel = %d, want 15", gotCfg.MinFanClubLevel)
	}

	if ttView.overrideCheck.Text != "Override Global Defaults" {
		t.Errorf("overrideCheck.Text = %q, want 'Override Global Defaults'", ttView.overrideCheck.Text)
	}
	if ttView.overridesBox.Visible() {
		t.Error("expected overridesBox to be hidden when Override Global Defaults is unchecked")
	}
	ttView.overrideCheck.SetChecked(true)
	if !ttView.overridesBox.Visible() {
		t.Error("expected overridesBox to be visible when Override Global Defaults is checked")
	}
}
