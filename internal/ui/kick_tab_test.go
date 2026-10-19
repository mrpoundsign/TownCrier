package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestKickTabViewBuild(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	w := fyneApp.NewWindow("Test Kick Tab")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	kickView := NewKickTabView(w, session)
	content := kickView.Build()
	if content == nil {
		t.Fatal("expected non-nil Kick tab content")
	}
	if kickView.overrideCheck.Text != "Override Global Defaults" {
		t.Errorf("overrideCheck.Text = %q, want 'Override Global Defaults'", kickView.overrideCheck.Text)
	}
	if kickView.overridesBox.Visible() {
		t.Error("expected overridesBox to be hidden when Override Global Defaults is unchecked")
	}
	kickView.overrideCheck.SetChecked(true)
	if !kickView.overridesBox.Visible() {
		t.Error("expected overridesBox to be visible when Override Global Defaults is checked")
	}
}
