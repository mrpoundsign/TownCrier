package ui

import (
	"os"
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

	if view.subTabs == nil {
		t.Fatal("expected subTabs to be initialized")
	}
	if len(view.subTabs.Items) != 3 {
		t.Errorf("expected 3 default settings sub-tabs (General & Audio, TTS & Hardware, Rules & Safety), got %d", len(view.subTabs.Items))
	}
	if view.logsView == nil {
		t.Error("expected logsView to be initialized")
	}
	if view.uiScaleSelect == nil {
		t.Fatal("expected uiScaleSelect to be initialized")
	}
	if view.uiScaleSelect.Selected != FormatUIScaleLabel("1.0") {
		t.Errorf("expected default UI scale %q, got %q", FormatUIScaleLabel("1.0"), view.uiScaleSelect.Selected)
	}
	if minW := obj.MinSize().Width; minW > 550 {
		t.Errorf("expected SettingsView MinSize().Width <= 550 DIPs so it never forces wide OS windows, got %v", minW)
	}
}

func TestUIScaleSettingAndEnv(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestUIScale")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore())

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) { return nil, nil })
	_ = view.Build()

	view.uiScaleSelect.SetSelected("125%")
	if sess.Config().UIScale != "1.25" {
		t.Errorf("expected UIScale '1.25', got %q", sess.Config().UIScale)
	}
	if got := os.Getenv("FYNE_DISABLE_DPI_DETECTION"); got != "true" {
		t.Errorf("expected FYNE_DISABLE_DPI_DETECTION='true', got %q", got)
	}
	if got := os.Getenv("FYNE_SCALE"); got != "1.25" {
		t.Errorf("expected FYNE_SCALE='1.25', got %q", got)
	}

	view.uiScaleSelect.SetSelected(FormatUIScaleLabel("auto"))
	if sess.Config().UIScale != "auto" {
		t.Errorf("expected UIScale 'auto', got %q", sess.Config().UIScale)
	}
	if got := os.Getenv("FYNE_DISABLE_DPI_DETECTION"); got != "false" {
		t.Errorf("expected FYNE_DISABLE_DPI_DETECTION='false', got %q", got)
	}
	if got := os.Getenv("FYNE_SCALE"); got != "auto" {
		t.Errorf("expected FYNE_SCALE='auto', got %q", got)
	}

	// Restore default 1.0 after test
	ApplyUIScale(appObj, "1.0")
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

func TestLaunchWindowProgressAndError(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()

	tracker, win := NewLaunchWindow(appObj)
	if tracker == nil || win == nil {
		t.Fatal("expected non-nil LaunchStateTracker and Window")
	}

	tracker.SetDownloadProgress(50*1024*1024, 100*1024*1024)
	if !tracker.downloadProg.Visible() {
		t.Error("expected downloadProg to be visible during download")
	}
	if tracker.downloadProg.Value != 0.5 {
		t.Errorf("expected downloadProg value 0.5, got %v", tracker.downloadProg.Value)
	}

	quitCalled := false
	tracker.ShowFatalError("Network error", "Connection refused", nil, func() {
		quitCalled = true
	})
	if !tracker.actionBox.Visible() {
		t.Error("expected actionBox with Retry/Quit buttons to be visible on fatal error")
	}
	test.Tap(tracker.quitBtn)
	if !quitCalled {
		t.Error("expected onQuit callback to be invoked when Quit button is tapped")
	}
}


