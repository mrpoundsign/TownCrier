package ui

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"github.com/mrpoundsign/TownCrier/internal/tts"
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

func waitForCondition(fn func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

func TestModelDownloadModal_ProgressAndStep(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestModal")

	var cancelled atomic.Bool
	modal := NewModelDownloadModal(w, "Piper", 65, func() {
		cancelled.Store(true)
	})

	if modal == nil {
		t.Fatal("expected non-nil ModelDownloadModal")
	}
	if !strings.Contains(modal.stepLbl.Text, "Piper") {
		t.Errorf("expected step label to mention Piper, got %q", modal.stepLbl.Text)
	}

	modal.SetStep(2, 3, "Matcha", 40)
	if !waitForCondition(func() bool {
		return strings.Contains(modal.stepLbl.Text, "Model 2 of 3") && strings.Contains(modal.stepLbl.Text, "Matcha")
	}, time.Second) {
		t.Errorf("step text did not update, got %q", modal.stepLbl.Text)
	}

	modal.UpdateProgress(20*1024*1024, 40*1024*1024)
	if !waitForCondition(func() bool {
		return modal.progress.Value == 0.5 && strings.Contains(modal.progressLbl.Text, "50%")
	}, time.Second) {
		t.Errorf("progress did not update, got val=%v text=%q", modal.progress.Value, modal.progressLbl.Text)
	}

	test.Tap(modal.cancelBtn)
	if !waitForCondition(func() bool {
		return cancelled.Load() && modal.IsCancelling()
	}, time.Second) {
		t.Error("expected cancel callback and cancelling state after tap")
	}

	modal.Dismiss()
	if !modal.IsDismissed() {
		t.Error("expected modal to report dismissed after Dismiss()")
	}
}

func TestSettingsView_MissingModels(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestMissingModels")

	cfg := config.DefaultConfig()
	view := NewSettingsView(w, cfg, nil, nil)
	tmpDir := t.TempDir()
	view.modelDir = tmpDir

	missing := view.missingModelFamilies([]string{tts.ModelKokoro, tts.ModelPiper}, false)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing models, got %v", missing)
	}
}

func TestSettingsView_RollbackTTSConfig(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestRollback")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.modelDir = t.TempDir()
	_ = view.Build()

	// Mutate to an invalid setting
	view.updateConfig(func(c *config.Config) {
		c.TTS.EnabledModels = []string{tts.ModelPiper}
		c.TTS.Preset = tts.PresetCustom
	})

	// Rollback to kokoro
	view.rollbackTTSConfig([]string{tts.ModelKokoro}, tts.PresetFastLean, false, 4, 12, 0)

	curCfg := sess.Config()
	if len(curCfg.TTS.EnabledModels) != 1 || curCfg.TTS.EnabledModels[0] != tts.ModelKokoro {
		t.Errorf("expected rolled back models to be [%s], got %v", tts.ModelKokoro, curCfg.TTS.EnabledModels)
	}
	if curCfg.TTS.Preset != tts.PresetFastLean {
		t.Errorf("expected rolled back preset to be %s, got %s", tts.PresetFastLean, curCfg.TTS.Preset)
	}
}



