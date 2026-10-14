package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestSettingsViewBuild(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("Test")

	cfg := config.DefaultConfig()
	view := NewSettingsView(w, cfg, nil, nil)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{
			{ID: "mock1", Name: "Mock Speakers", IsDefault: true},
		}, nil
	})

	obj := view.Build()
	if obj == nil {
		t.Fatal("expected non-nil CanvasObject from Build()")
	}

	if view.statusLabel == nil {
		t.Error("expected statusLabel to be initialized")
	}

	if view.deviceSelect == nil {
		t.Error("expected deviceSelect to be initialized")
	}

	if len(view.deviceSelect.Options) == 0 {
		t.Error("expected at least Default Audio Device option")
	}
}

func TestSettingsViewAutoSave(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestAutoSave")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore())

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	var lastStatus string
	view.SetStatusHandler(func(msg string) {
		lastStatus = msg
	})

	_ = view.Build()

	view.updateConfig(func(c *config.Config) {
		c.GlobalChatRules.SpeakNames = false
	})

	if sess.Config().GlobalChatRules.SpeakNames {
		t.Error("expected SpeakNames to be updated to false in session")
	}
	if lastStatus != "✓ Settings saved." {
		t.Errorf("expected '✓ Settings saved.', got %q", lastStatus)
	}
}
