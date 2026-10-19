package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestYouTubeTabViewBuild(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	w := fyneApp.NewWindow("Test YouTube Tab")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	ytView := NewYouTubeTabView(w, session)
	content := ytView.Build()
	if content == nil {
		t.Fatal("expected non-nil YouTube tab content")
	}
	if ytView.overrideCheck.Text != "Override Global Defaults" {
		t.Errorf("overrideCheck.Text = %q, want 'Override Global Defaults'", ytView.overrideCheck.Text)
	}
	if ytView.overridesBox.Visible() {
		t.Error("expected overridesBox to be hidden when Override Global Defaults is unchecked")
	}
	ytView.overrideCheck.SetChecked(true)
	if !ytView.overridesBox.Visible() {
		t.Error("expected overridesBox to be visible when Override Global Defaults is checked")
	}
}
