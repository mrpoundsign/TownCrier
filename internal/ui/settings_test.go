package ui

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
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
	if len(view.subTabs.Items) != 4 {
		t.Errorf("expected 4 default settings sub-tabs (General & Audio, TTS & Hardware, Rules & Safety, Event Logs), got %d", len(view.subTabs.Items))
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
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

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
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

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
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
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

func TestSettingsView_TTSAndLoggingToggles(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestTTSLoggingToggles")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})

	var ttsToggled atomic.Bool
	view.SetTTSChangedHandler(func(enabled bool) {
		ttsToggled.Store(true)
	})

	_ = view.Build()

	if view.enableTTSCheck == nil {
		t.Fatal("expected enableTTSCheck to be initialized")
	}
	if !view.enableTTSCheck.Checked {
		t.Fatal("expected enableTTSCheck to be checked by default")
	}
	if len(view.subTabs.Items) != 4 {
		t.Fatalf("expected 4 subtabs initially, got %d", len(view.subTabs.Items))
	}

	// 1. SyncTTSState(false) removes TTS & Hardware, Rules & Safety subtabs and hides voice/audio cards
	view.SyncTTSState(false)
	if view.globalVoiceCard.Visible() {
		t.Error("expected globalVoiceCard to be hidden when TTS is disabled")
	}
	if view.audioCard.Visible() {
		t.Error("expected audioCard to be hidden when TTS is disabled")
	}
	if len(view.subTabs.Items) != 2 {
		t.Fatalf("expected 2 subtabs when TTS is disabled (General & Audio, Event Logs), got %d", len(view.subTabs.Items))
	}

	// 2. SyncLoggingState(false) removes Event Logs subtab
	view.SyncLoggingState(false)
	if len(view.subTabs.Items) != 1 {
		t.Fatalf("expected 1 subtab when both TTS and Logging are disabled (General & Audio), got %d", len(view.subTabs.Items))
	}

	// 3. Re-enable both
	view.SyncTTSState(true)
	view.SyncLoggingState(true)
	if !view.globalVoiceCard.Visible() {
		t.Error("expected globalVoiceCard to be visible when TTS is enabled")
	}
	if !view.audioCard.Visible() {
		t.Error("expected audioCard to be visible when TTS is enabled")
	}
	if len(view.subTabs.Items) != 4 {
		t.Fatalf("expected 4 subtabs when both are re-enabled, got %d", len(view.subTabs.Items))
	}

	// 4. User unchecks enableTTSCheck
	view.enableTTSCheck.SetChecked(false)
	if !ttsToggled.Load() {
		t.Error("expected onTTSChanged callback to be called")
	}
	if sess.Config().TTS.Enabled {
		t.Error("expected session config TTS.Enabled to be false")
	}
}

func TestSettingsView_StreamDeckOneClickInstall(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestStreamDeckInstall")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.streamDeckPluginsDir = t.TempDir()
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})

	_ = view.Build()

	if view.streamDeckInstallBtn == nil {
		t.Fatal("expected streamDeckInstallBtn to be initialized")
	}
	if streamdeck.IsPluginInstalled(view.streamDeckPluginsDir) {
		t.Fatal("expected Stream Deck plugin not installed prior to button tap")
	}
	if !strings.Contains(view.streamDeckStatusLabel.Text, "Not installed") {
		t.Errorf("expected initial status to say Not installed, got %q", view.streamDeckStatusLabel.Text)
	}

	test.Tap(view.streamDeckInstallBtn)

	if !streamdeck.IsPluginInstalled(view.streamDeckPluginsDir) {
		t.Fatal("expected Stream Deck plugin to be installed after tapping streamDeckInstallBtn")
	}
	if !strings.Contains(view.streamDeckStatusLabel.Text, "Installed Stream Deck plugin v"+streamdeck.BundledPluginVersion()) {
		t.Errorf("unexpected streamDeckStatusLabel text: %q", view.streamDeckStatusLabel.Text)
	}
	if !strings.Contains(view.streamDeckInstallBtn.Text, "Reinstall Elgato Stream Deck Plugin") {
		t.Errorf("expected button text to say Reinstall after install, got %q", view.streamDeckInstallBtn.Text)
	}

	// Simulate an outdated installed plugin version (0.9.0.0) and rebuild view to verify update prompt
	manifestPath := filepath.Join(view.streamDeckPluginsDir, streamdeck.PluginDirName, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"Version":"0.9.0.0"}`), 0o600); err != nil {
		t.Fatalf("write older manifest.json: %v", err)
	}
	_ = view.Build()
	if !strings.Contains(view.streamDeckStatusLabel.Text, "update available: v0.9.0.0 -> v"+streamdeck.BundledPluginVersion()) {
		t.Errorf("expected update available status label, got %q", view.streamDeckStatusLabel.Text)
	}
	if !strings.Contains(view.streamDeckInstallBtn.Text, "Update Elgato Stream Deck Plugin to v"+streamdeck.BundledPluginVersion()) {
		t.Errorf("expected Update button label, got %q", view.streamDeckInstallBtn.Text)
	}
}

func TestSettingsAndPlatformTab_IgnoreMentionsControls(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestIgnoreMentionsControls")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	_ = view.Build()

	if view.rulesMentionsModeSelect == nil || view.rulesAllowBroadcasterCheck == nil || view.rulesPlayAlertIgnoredMentionCheck == nil {
		t.Fatal("expected mention rule controls on SettingsView to be initialized")
	}

	// Initially "off" -> sub-checkboxes disabled
	if !view.rulesAllowBroadcasterCheck.Disabled() || !view.rulesPlayAlertIgnoredMentionCheck.Disabled() {
		t.Error("expected sub-checkboxes to be disabled when IgnoreMentionsMode is 'off'")
	}

	// Change to Ignore Replies & Starting @ Mentions
	view.rulesMentionsModeSelect.SetSelected(IgnoreMentionsOptionRepliesAndLeading)
	if sess.Config().GlobalChatRules.IgnoreMentionsMode != config.IgnoreMentionsRepliesAndLeading {
		t.Errorf("expected IgnoreMentionsMode=%q, got %q", config.IgnoreMentionsRepliesAndLeading, sess.Config().GlobalChatRules.IgnoreMentionsMode)
	}
	if view.rulesAllowBroadcasterCheck.Disabled() || view.rulesPlayAlertIgnoredMentionCheck.Disabled() {
		t.Error("expected sub-checkboxes to be enabled when IgnoreMentionsMode != 'off'")
	}

	view.rulesPlayAlertIgnoredMentionCheck.SetChecked(true)
	if !sess.Config().GlobalChatRules.PlayAlertOnIgnoredMention {
		t.Error("expected GlobalChatRules.PlayAlertOnIgnoredMention to be true")
	}

	// Also verify platform tab override controls (TwitchTabView) and dynamic Global (...) label updates
	twitchTab := NewTwitchTabView(w, sess)
	view.SetPlatformTabs(twitchTab, nil, nil, nil)
	_ = twitchTab.Build()

	if twitchTab.overrides.MentionsModeSelect.Selected != "Global (Ignore Replies & Starting @ Mentions)" {
		t.Errorf("expected Global (Ignore Replies & Starting @ Mentions), got %q", twitchTab.overrides.MentionsModeSelect.Selected)
	}
	if twitchTab.overrides.PlayAlertIgnoredMentionSelect.Selected != "Global ("+boolOnAlertMention+")" {
		t.Errorf("expected Global (%s), got %q", boolOnAlertMention, twitchTab.overrides.PlayAlertIgnoredMentionSelect.Selected)
	}

	// Changing a global rule dynamically refreshes the Global (...) label on platform tabs
	view.rulesNamesCheck.SetChecked(false)
	if twitchTab.overrides.NamesSelect.Selected != "Global ("+boolOffSpeakNames+")" {
		t.Errorf("expected NamesSelect to refresh to 'Global (%s)', got %q", boolOffSpeakNames, twitchTab.overrides.NamesSelect.Selected)
	}

	twitchTab.overrides.MentionsModeSelect.SetSelected(IgnoreMentionsOptionAll)
	twitchTab.overrides.AllowBroadcasterSelect.SetSelected(boolOffAllowBcaster)
	twitchTab.overrides.PlayAlertIgnoredMentionSelect.SetSelected(boolOnAlertMention)

	mode, allowBroadcaster, playAlert := sess.Config().EffectiveMentionRules("twitch")
	if mode != config.IgnoreMentionsAll || allowBroadcaster || !playAlert {
		t.Errorf("expected twitch override (all, false, true), got (%q, %v, %v)", mode, allowBroadcaster, playAlert)
	}
}

func TestSettingsView_TranslationTabAndOverrides(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestTranslationTab")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	twitchTab := NewTwitchTabView(w, sess)
	view.SetPlatformTabs(twitchTab, nil, nil, nil)
	_ = view.Build()

	// Initially Translation is disabled -> 5 sub-tabs (General, TTS, Twitch, Rules, Event Logs)
	if view.enableTranslationCheck == nil || view.enableTranslationCheck.Checked {
		t.Fatal("expected enableTranslationCheck to be unchecked by default")
	}

	// Enable Chat Translation -> adds Translation sub-tab and updates Twitch override Global label
	view.enableTranslationCheck.SetChecked(true)
	if !sess.Config().Translation.Enabled {
		t.Error("expected Translation.Enabled to be true")
	}
	hasTranslationTab := slices.Contains(view.subTabs.Items, view.translationTabItem)
	if !hasTranslationTab {
		t.Error("expected Translation sub-tab to be visible when Translation is enabled")
	}
	if twitchTab.overrides.TranslateSelect.Selected != "Global ("+boolOnTranslateChat+")" {
		t.Errorf("expected Twitch TranslateSelect 'Global (%s)', got %q", boolOnTranslateChat, twitchTab.overrides.TranslateSelect.Selected)
	}

	// Default provider is MyMemory Translated API
	if !strings.Contains(view.translationLimitLabel.Text, "MyMemory") {
		t.Errorf("expected default translationLimitLabel to describe MyMemory limits, got %q", view.translationLimitLabel.Text)
	}
	if view.translationKeyLink == nil || !strings.Contains(view.translationKeyLink.Text, "MyMemory") {
		t.Errorf("expected default translationKeyLink to reference MyMemory")
	}
	if view.translationTOSLink == nil || !strings.Contains(view.translationTOSLink.Text, "MyMemory") {
		t.Errorf("expected default translationTOSLink to reference MyMemory")
	}
	view.translationEmailEntry.SetText("streamer@example.com")
	if sess.Config().Translation.ContactEmail != "streamer@example.com" {
		t.Errorf("expected ContactEmail 'streamer@example.com', got %q", sess.Config().Translation.ContactEmail)
	}
	if !strings.Contains(view.translationLimitLabel.Text, "10x Email Quota Active") {
		t.Errorf("expected translationLimitLabel to reflect 10x email quota, got %q", view.translationLimitLabel.Text)
	}

	// Change Target Language to Tagalog (Filipino) (tl) and Routing Mode to Round-Robin
	view.translationTargetSelect.SetSelected("Tagalog (Filipino) (tl)")
	if sess.Config().Translation.TargetLang != "tl" {
		t.Errorf("expected TargetLang 'tl', got %q", sess.Config().Translation.TargetLang)
	}

	view.translationRoutingSelect.SetSelected(TranslationRoutingOptionRoundRobin)
	if sess.Config().Translation.RoutingMode != config.TranslationRoutingRoundRobin {
		t.Errorf("expected RoutingMode %q, got %q", config.TranslationRoutingRoundRobin, sess.Config().Translation.RoutingMode)
	}

	if view.translationChimeCheck == nil || !view.translationChimeCheck.Checked {
		t.Error("expected translationChimeCheck to be checked by default")
	}
	view.translationChimeCheck.SetChecked(false)
	if sess.Config().Translation.PlayChimeBeforeSpeak {
		t.Error("expected PlayChimeBeforeSpeak=false after unchecking")
	}

	if view.translationNodesBox == nil || len(view.translationNodesBox.Objects) != len(config.AllTranslationProviders) {
		t.Errorf("expected %d provider node rows, got %d", len(config.AllTranslationProviders), len(view.translationNodesBox.Objects))
	}

	// By default, no provider drawer is open and test button says "Test Translation Chain"
	if view.translationConfigDrawer == nil || view.translationConfigDrawer.Visible() {
		t.Error("expected translationConfigDrawer to be hidden by default until Configure is clicked")
	}
	if view.translationTestBtn == nil || view.translationTestBtn.Text != "Test Translation Chain" {
		t.Errorf("expected default test button text 'Test Translation Chain', got %q", view.translationTestBtn.Text)
	}

	view.translationProviderSelect.SetSelected("Local LLM (Ollama / OpenAI API)")
	if view.selectedTranslationProvider() != config.TranslationProviderOllama {
		t.Errorf("expected selectedTranslationProvider %q, got %q", config.TranslationProviderOllama, view.selectedTranslationProvider())
	}
	if !view.translationConfigDrawer.Visible() {
		t.Error("expected translationConfigDrawer to be visible when configuring a provider")
	}
	if !strings.Contains(view.translationTestBtn.Text, "Local LLM") {
		t.Errorf("expected test button to target Local LLM while configuring, got %q", view.translationTestBtn.Text)
	}
	if !strings.Contains(view.translationLimitLabel.Text, "Local LLM") {
		t.Errorf("expected translationLimitLabel to describe Local LLM limits, got %q", view.translationLimitLabel.Text)
	}

	// Clicking "Done Configuring" closes the drawer and resets the test button to "Test Translation Chain"
	view.translationDoneConfigBtn.OnTapped()
	if view.translationConfigDrawer.Visible() {
		t.Error("expected translationConfigDrawer to hide after clicking Done Configuring")
	}
	if view.translationTestBtn.Text != "Test Translation Chain" {
		t.Errorf("expected test button text 'Test Translation Chain' after Done, got %q", view.translationTestBtn.Text)
	}

	// Override Twitch translation to Off
	twitchTab.overrides.TranslateSelect.SetSelected(boolOffTranslateChat)
	if sess.Config().EffectiveTranslationEnabled("twitch") {
		t.Error("expected EffectiveTranslationEnabled('twitch') to be false after override")
	}
	if !sess.Config().EffectiveTranslationEnabled("youtube") {
		t.Error("expected EffectiveTranslationEnabled('youtube') to inherit global true")
	}
}

func TestSettingsView_VoiceReplacementWizard(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestVoiceReplacementWizard")

	cfg := config.DefaultConfig()
	cfg.TTS.Preset = tts.PresetCustom
	cfg.TTS.EnabledModels = []string{tts.ModelKokoro, tts.ModelSupertonic}
	cfg.GlobalChatRules.DefaultVoice = "supertonic:f1"
	cfg.GlobalChatRules.DefaultSpeed = 1.0
	cfg.Viewers["twitch:alice"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "alice",
		Voice:    "supertonic:m2",
		Speed:    1.15,
	}
	twitchCfg := cfg.Platforms["twitch"]
	twitchCfg.Overrides.VoiceOverrideMode = config.OverrideModeCustom
	twitchCfg.Overrides.DefaultVoice = "supertonic:f2"
	cfg.Platforms["twitch"] = twitchCfg

	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.modelDir = t.TempDir()
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	twitchTab := NewTwitchTabView(w, sess)
	view.SetPlatformTabs(twitchTab, nil, nil, nil)

	var modelsChangedCount int
	view.SetModelsChangedHandler(func() {
		modelsChangedCount++
	})

	_ = view.Build()

	superChk := view.modelChecks[tts.ModelSupertonic]
	if superChk == nil || !superChk.Checked {
		t.Fatal("expected supertonic checkbox to be checked initially")
	}

	// 1. Unchecking in-use Supertonic pack opens the VoiceReplacementWizard and keeps checkbox checked while open
	superChk.SetChecked(false)
	wiz := view.activeReplacementWizard
	if wiz == nil {
		t.Fatal("expected activeReplacementWizard to be opened when unchecking in-use model pack")
	}
	if !superChk.Checked {
		t.Error("expected supertonic checkbox to stay checked until wizard is confirmed")
	}
	if len(wiz.usages) != 3 {
		t.Fatalf("expected 3 affected voice usages (global, viewer alice, twitch override), got %d", len(wiz.usages))
	}

	// 2. Cancelling the wizard leaves config and enabled model packs untouched
	test.Tap(wiz.cancelBtn)
	if view.activeReplacementWizard != nil {
		t.Error("expected activeReplacementWizard to be nil after cancel")
	}
	if !slices.Contains(sess.Config().TTS.EnabledModels, tts.ModelSupertonic) {
		t.Error("expected supertonic to remain enabled after cancelling wizard")
	}
	if sess.Config().GlobalChatRules.DefaultVoice != "supertonic:f1" {
		t.Errorf("expected Global Default Voice to remain supertonic:f1, got %q", sess.Config().GlobalChatRules.DefaultVoice)
	}

	// 3. Uncheck again, use SetBulkVoice("af_bella", 1.1) + Clear Overrides, then Confirm
	superChk.SetChecked(false)
	wiz = view.activeReplacementWizard
	if wiz == nil {
		t.Fatal("expected activeReplacementWizard on second uncheck")
	}
	wiz.SetBulkVoice("af_bella", 1.1)
	if wiz.clearOverridesBtn == nil {
		t.Fatal("expected clearOverridesBtn to be present when viewer/platform overrides exist")
	}
	test.Tap(wiz.clearOverridesBtn)
	test.Tap(wiz.confirmBtn)

	if superChk.Checked {
		t.Error("expected supertonic checkbox to be unchecked after confirming wizard")
	}
	updatedCfg := sess.Config()
	if slices.Equal(updatedCfg.TTS.EnabledModels, []string{tts.ModelKokoro}) == false {
		t.Errorf("expected EnabledModels=[kokoro], got %v", updatedCfg.TTS.EnabledModels)
	}
	if updatedCfg.GlobalChatRules.DefaultVoice != "af_bella" {
		t.Errorf("expected Global Default Voice reassigned to af_bella, got %q", updatedCfg.GlobalChatRules.DefaultVoice)
	}
	if _, exists := updatedCfg.Viewers["twitch:alice"]; exists {
		t.Error("expected alice viewer override to be cleared and removed")
	}
	if updatedCfg.Platforms["twitch"].Overrides.VoiceOverrideMode != config.OverrideModeGlobal {
		t.Errorf("expected Twitch voice override to reset to global, got %q", updatedCfg.Platforms["twitch"].Overrides.VoiceOverrideMode)
	}
	if view.globalVoiceField.VoiceID() != "af_bella" {
		t.Errorf("expected SettingsView globalVoiceField to refresh to af_bella, got %q", view.globalVoiceField.VoiceID())
	}
	if modelsChangedCount == 0 {
		t.Error("expected onModelsChanged callback to fire after wizard confirmation")
	}

	// 4. Attempting to uncheck the last remaining model pack (Kokoro) blocks immediately without opening the wizard
	kokoroChk := view.modelChecks[tts.ModelKokoro]
	kokoroChk.SetChecked(false)
	if !kokoroChk.Checked {
		t.Error("expected last remaining model pack checkbox to remain checked")
	}
	if view.activeReplacementWizard != nil {
		t.Error("expected wizard NOT to open when no other model packs remain enabled")
	}

	// 5. Switching to Fast & Lean preset (which enables only Supertonic and disables Kokoro) opens the wizard for af_bella
	fastPreset := tts.PresetByID(tts.PresetFastLean)
	view.presetSelect.SetSelected(fastPreset.Label)
	wiz = view.activeReplacementWizard
	if wiz == nil {
		t.Fatal("expected activeReplacementWizard to open when switching to preset that disables in-use Kokoro pack")
	}
	// Default replacement voice for Fast & Lean (Supertonic) should be supertonic:f1
	test.Tap(wiz.confirmBtn)

	finalCfg := sess.Config()
	if finalCfg.TTS.Preset != tts.PresetFastLean {
		t.Errorf("expected Preset=%q after confirming preset wizard, got %q", tts.PresetFastLean, finalCfg.TTS.Preset)
	}
	if finalCfg.GlobalChatRules.DefaultVoice != "supertonic:f1" {
		t.Errorf("expected Global Default Voice reassigned to supertonic:f1, got %q", finalCfg.GlobalChatRules.DefaultVoice)
	}
}

func TestBackupImportDialogFlow(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	win := fyneApp.NewWindow("Test Backup Import")

	baseCfg := config.DefaultConfig()
	baseCfg.Audio.DeviceName = "Local OBS Cable"
	baseCfg.Viewers["twitch:local_viewer"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "local_viewer",
		Voice:    "supertonic:m1",
		Speed:    1.0,
	}

	cStore := config.NewMemoryStore(baseCfg)
	sess := app.NewSession(baseCfg, secrets.NewMemoryStore(), cStore)
	defer func() { _ = sess.Close() }()

	incoming := config.DefaultConfig()
	incoming.Audio.DeviceName = "Other PC Device"
	incoming.Viewers["youtube:remote_viewer"] = config.ViewerConfig{
		Platform: "youtube",
		UserID:   "remote_viewer",
		Voice:    "kokoro:af_bella",
		Speed:    1.2,
	}
	incoming.Safety.BlockedWords = []string{"imported_badword"}
	incoming.Bots.CustomBots = []string{"imported_bot"}

	rawBytes, err := config.ExportBackup(incoming, "0.0.9")
	if err != nil {
		t.Fatalf("ExportBackup failed: %v", err)
	}
	bundle, err := config.ParseBackup(rawBytes)
	if err != nil {
		t.Fatalf("ParseBackup failed: %v", err)
	}

	var appliedCfg *config.Config
	var modelsChangedFlag bool
	var statusMsg string

	dlg := NewBackupImportDialog(win, sess, bundle, "test-backup.json", func(newCfg *config.Config, modelsChanged bool) {
		appliedCfg = newCfg
		modelsChangedFlag = modelsChanged
	}, func(msg string) {
		statusMsg = msg
	})

	// Uncheck BlockedAndBotsCheck so only Viewers/Rules/LiveLogs/Translation merge
	dlg.BlockedAndBotsCheck.SetChecked(false)
	test.Tap(dlg.ImportBtn)

	if appliedCfg == nil {
		t.Fatal("expected onApply callback to receive merged config")
	}
	if !modelsChangedFlag {
		t.Error("expected modelsChangedFlag=true because kokoro model was auto-enabled for imported viewer")
	}
	if appliedCfg.Audio.DeviceName != "Local OBS Cable" {
		t.Errorf("expected local audio device preserved, got %q", appliedCfg.Audio.DeviceName)
	}
	if _, ok := appliedCfg.Viewers["twitch:local_viewer"]; !ok {
		t.Error("expected local_viewer preserved in selective merge")
	}
	if _, ok := appliedCfg.Viewers["youtube:remote_viewer"]; !ok {
		t.Error("expected remote_viewer added in selective merge")
	}
	if len(appliedCfg.Safety.BlockedWords) != 0 {
		t.Errorf("expected BlockedWords not merged when checkbox unchecked, got %v", appliedCfg.Safety.BlockedWords)
	}
	if !strings.Contains(statusMsg, "Configuration backup imported") {
		t.Errorf("unexpected status message: %q", statusMsg)
	}
}

func TestWriteSupportBackupZipAndDialog(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	win := fyneApp.NewWindow("Test Support Backup")
	tmpDir := t.TempDir()

	baseCfg := config.DefaultConfig()
	baseCfg.WebSocket.AuthToken = "secret-ws-token-must-not-appear"
	baseCfg.Translation.APIKey = "secret-api-key-must-not-appear"

	cStore := config.NewMemoryStore(baseCfg)
	sess := app.NewSession(baseCfg, secrets.NewMemoryStore(), cStore)
	defer func() { _ = sess.Close() }()

	// Attach a temporary SQLite event log database with an entry
	dbPath := filepath.Join(tmpDir, "events.db")
	sqlBackend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteBackend: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{}, sqlBackend)
	sess.SetEventLogger(logMgr)
	if err := logMgr.Append(context.Background(), &eventlog.Entry{
		ID:       "ev-support-1",
		Platform: "twitch",
		Channel:  "streamer",
		Kind:     "message",
		UserID:   "u1",
		UserName: "alice",
		RawText:  "support test message",
		Outcome:  eventlog.OutcomeSpoken,
	}); err != nil {
		t.Fatalf("logMgr.Append: %v", err)
	}

	// Create a temporary log file
	logFile := filepath.Join(tmpDir, "towncrier.log")
	if err := os.WriteFile(logFile, []byte("2026/11/08 18:00:00 test log line\n"), 0o600); err != nil {
		t.Fatalf("write test log file: %v", err)
	}

	dlg := NewSupportBackupDialog(win, sess, nil)
	if dlg.IncludeLogsCheck.Checked {
		t.Error("expected IncludeLogsCheck to be unchecked by default")
	}
	if !dlg.IncludeConfigCheck.Checked || !dlg.IncludeDBCheck.Checked {
		t.Error("expected IncludeConfigCheck and IncludeDBCheck to be checked by default")
	}
	dlg.IncludeLogsCheck.SetChecked(true)
	dlg.NotesEntry.SetText("Steps to reproduce: clicked test button")
	opts := dlg.Options()
	opts.LogFilePath = logFile

	var buf bytes.Buffer
	files, err := WriteSupportBackupZip(&buf, sess, opts)
	if err != nil {
		t.Fatalf("WriteSupportBackupZip failed: %v", err)
	}

	expectedFiles := []string{
		"support-notes.txt",
		"config-backup.json",
		"towncrier.log",
		"events.db",
		"support-manifest.json",
	}
	if !slices.Equal(files, expectedFiles) {
		t.Fatalf("expected archived files %v, got %v", expectedFiles, files)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	foundEntries := make(map[string]string)
	for _, zf := range zr.File {
		rc, openErr := zf.Open()
		if openErr != nil {
			t.Fatalf("open zip entry %s: %v", zf.Name, openErr)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		foundEntries[zf.Name] = string(b)
	}

	if strings.Contains(foundEntries["config-backup.json"], "secret-ws-token-must-not-appear") ||
		strings.Contains(foundEntries["config-backup.json"], "secret-api-key-must-not-appear") {
		t.Error("expected secrets to be scrubbed from config-backup.json in support zip")
	}
	if !strings.Contains(foundEntries["support-notes.txt"], "Steps to reproduce: clicked test button") {
		t.Errorf("unexpected support-notes.txt content: %q", foundEntries["support-notes.txt"])
	}
	if !strings.Contains(foundEntries["towncrier.log"], "test log line") {
		t.Errorf("unexpected towncrier.log content: %q", foundEntries["towncrier.log"])
	}
	if len(foundEntries["events.db"]) == 0 {
		t.Error("expected non-empty events.db snapshot in support zip")
	}

	// Verify round-trip parsing of the .zip archive via ParseBackupArchiveBytes & NewBackupArchiveImportDialog
	arch, err := ParseBackupArchiveBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseBackupArchiveBytes failed: %v", err)
	}
	if arch.Bundle == nil || len(arch.DatabaseBytes) == 0 {
		t.Fatalf("expected parsed archive to contain both Config bundle and DatabaseBytes")
	}
	impDlg := NewBackupArchiveImportDialog(win, sess, arch, "towncrier-backup-2026-11-08-190000-v0.0.9.zip", nil, nil)
	if impDlg.RestoreDBCheck == nil || !impDlg.RestoreDBCheck.Checked {
		t.Error("expected RestoreDBCheck to be present and checked by default for .zip containing events.db")
	}
	if _, err := impDlg.TriggerImport(); err != nil {
		t.Fatalf("impDlg.TriggerImport on .zip archive failed: %v", err)
	}
}

func TestSettingsView_CoalesceConsecutiveSpeakerControls(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestCoalesceControls")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.modelDir = t.TempDir()
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mock1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	twitchTab := NewTwitchTabView(w, sess)
	view.SetPlatformTabs(twitchTab, nil, nil, nil)

	_ = view.Build()

	if view.rulesCoalesceSelect == nil {
		t.Fatal("expected rulesCoalesceSelect to be initialized")
	}
	if view.rulesCoalesceSelect.Selected != "20 seconds (Default)" {
		t.Fatalf("expected default rulesCoalesceSelect=%q, got %q", "20 seconds (Default)", view.rulesCoalesceSelect.Selected)
	}

	// Selecting "30 seconds" updates config
	view.rulesCoalesceSelect.SetSelected("30 seconds")
	if sess.Config().GlobalChatRules.CoalesceConsecutiveSeconds != 30 {
		t.Fatalf("expected CoalesceConsecutiveSeconds=30, got %d", sess.Config().GlobalChatRules.CoalesceConsecutiveSeconds)
	}

	// Selecting "Off (Always Speak Name)" sets -1 in config and 0 effective
	view.rulesCoalesceSelect.SetSelected("Off (Always Speak Name)")
	if sess.Config().GlobalChatRules.CoalesceConsecutiveSeconds != -1 {
		t.Fatalf("expected CoalesceConsecutiveSeconds=-1 when Off selected, got %d", sess.Config().GlobalChatRules.CoalesceConsecutiveSeconds)
	}
	if sess.Config().EffectiveCoalesceSeconds("twitch") != 0 {
		t.Fatalf("expected EffectiveCoalesceSeconds('twitch')=0 when Off selected")
	}

	// Unchecking SpeakNames disables rulesCoalesceSelect and platform CoalesceSelect (when inheriting global)
	view.rulesNamesCheck.SetChecked(false)
	if !view.rulesCoalesceSelect.Disabled() {
		t.Fatal("expected rulesCoalesceSelect to be disabled when rulesNamesCheck is unchecked")
	}
	if !twitchTab.overrides.CoalesceSelect.Disabled() {
		t.Fatal("expected twitch CoalesceSelect to be disabled when global SpeakNames is off and inherited")
	}

	// Re-check SpeakNames and set platform override on Twitch to 15s
	view.rulesNamesCheck.SetChecked(true)
	view.rulesCoalesceSelect.SetSelected("20 seconds (Default)")
	if view.rulesCoalesceSelect.Disabled() {
		t.Fatal("expected rulesCoalesceSelect to be enabled when rulesNamesCheck is checked")
	}

	twitchTab.overrides.CoalesceSelect.SetSelected("15 seconds")
	if sess.Config().EffectiveCoalesceSeconds("twitch") != 15 {
		t.Fatalf("expected EffectiveCoalesceSeconds('twitch')=15 after override, got %d", sess.Config().EffectiveCoalesceSeconds("twitch"))
	}
	if sess.Config().EffectiveCoalesceSeconds("youtube") != 20 {
		t.Fatalf("expected EffectiveCoalesceSeconds('youtube')=20 inheriting global, got %d", sess.Config().EffectiveCoalesceSeconds("youtube"))
	}
}





