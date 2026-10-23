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
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

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

func TestDashboardSyncTTSState(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	dash := NewDashboardView(win, session)
	_ = dash.Build()

	// 1. SyncTTSState(false) completely hides the topSection (speaking card & queue controls)
	dash.SyncTTSState(false)
	if dash.topSection.Visible() {
		t.Error("expected topSection to be hidden when TTS is disabled")
	}

	// 2. SyncTTSState(true) restores the topSection
	dash.SyncTTSState(true)
	if !dash.topSection.Visible() {
		t.Error("expected topSection to be visible when TTS is enabled")
	}
	if dash.nowPlayingLabel.Text != "Idle (No speech active)" {
		t.Errorf("expected nowPlayingLabel 'Idle (No speech active)', got %q", dash.nowPlayingLabel.Text)
	}
	if dash.queueDepthLabel.Text != "Queue: 0 pending items" {
		t.Errorf("expected queueDepthLabel 'Queue: 0 pending items', got %q", dash.queueDepthLabel.Text)
	}
	if dash.pauseResumeBtn.Disabled() {
		t.Error("expected pauseResumeBtn to be enabled")
	}
	if dash.skipBtn.Disabled() {
		t.Error("expected skipBtn to be enabled")
	}
	if dash.clearBtn.Disabled() {
		t.Error("expected clearBtn to be enabled")
	}
}

