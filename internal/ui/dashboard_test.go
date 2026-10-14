package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestDashboardViewBuild(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())

	dash := NewDashboardView(win, session)
	content := dash.Build()
	if content == nil {
		t.Fatal("expected non-nil content from DashboardView.Build")
	}

	dash.AddActivity(ActivityEntry{
		Timestamp: time.Now(),
		Platform:  "twitch",
		UserName:  "alice",
		Text:      "test message",
		Kind:      "Chat",
	})

	if len(dash.activities) != 1 {
		t.Fatalf("expected 1 activity entry, got %d", len(dash.activities))
	}
}
