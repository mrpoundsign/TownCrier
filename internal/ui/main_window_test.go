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
