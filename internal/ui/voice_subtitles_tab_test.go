package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/pkg/audio"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
	"github.com/mrpoundsign/TownCrier/pkg/stt"
)

func TestSharedTranslationTabAndVoiceSubtitlesLanguageLists(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()
	w := fyneApp.NewWindow("TestVoiceSubtitles")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()
	sess.SetSTTModelDir(t.TempDir())

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetSTTModelDir(t.TempDir())
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "spk1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	view.SetCaptureDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mic1", Name: "Shure MV7", IsDefault: true}}, nil
	})
	view.SetSTTGPUDeviceEnumerator(func() ([]stt.GPUDevice, error) {
		return []stt.GPUDevice{
			{GPUIndex: 0, VKIndex: 0, Name: "Vulkan0", Description: "Intel(R) HD Graphics 630", Type: "igpu", MemoryTotalMB: 4096},
			{GPUIndex: 1, VKIndex: 1, Name: "Vulkan1", Description: "NVIDIA GeForce GTX 1060", Type: "dgpu", MemoryTotalMB: 6144},
		}, nil
	})

	obj := view.Build()

	// 1. Neither Chat Translation nor Voice Subtitles enabled -> neither sub-tab shown
	if slices.Contains(view.subTabs.Items, view.translationTabItem) {
		t.Fatal("expected Translation sub-tab to be hidden by default")
	}
	if slices.Contains(view.subTabs.Items, view.voiceSubtitlesTabItem) {
		t.Fatal("expected Voice Subtitles sub-tab to be hidden by default")
	}

	// 2. Enable ONLY Voice Subtitles -> BOTH Voice Subtitles AND shared Translation sub-tabs appear,
	// while Chat-specific cards inside Translation remain hidden!
	view.enableVoiceSubtitlesCheck.SetChecked(true)
	if !sess.Config().VoiceSubtitles.Enabled {
		t.Fatal("expected VoiceSubtitles.Enabled=true in Session config")
	}
	if !sess.Config().WebSocket.SubtitlesOverlayEnabled {
		t.Fatal("expected SubtitlesOverlayEnabled to auto-enable when Voice Subtitles is turned on")
	}
	if !slices.Contains(view.subTabs.Items, view.voiceSubtitlesTabItem) {
		t.Fatal("expected Voice Subtitles sub-tab to appear")
	}
	if !slices.Contains(view.subTabs.Items, view.translationTabItem) {
		t.Fatal("expected shared Translation sub-tab to appear when Voice Subtitles is enabled")
	}
	if !view.chatTranslationLangCard.Hidden || !view.chatTranslationChimeCard.Hidden {
		t.Fatal("expected Chat-specific translation cards to remain hidden when Chat Translation is off")
	}
	if minW := obj.MinSize().Width; minW > 550 {
		t.Errorf("expected SettingsView MinSize().Width <= 550 DIPs with Voice Subtitles enabled, got %v", minW)
	}

	// 3. Also enable Chat Translation -> Chat-specific cards inside Translation become visible
	view.enableTranslationCheck.SetChecked(true)
	if view.chatTranslationLangCard.Hidden || view.chatTranslationChimeCard.Hidden {
		t.Fatal("expected Chat-specific translation cards to show when Chat Translation is enabled")
	}

	// 4. Add a Spoken Language via pulldown + Add button ("Japanese (ja)")
	view.vsSpokenAddSelect.SetSelected("Japanese (ja)")
	test.Tap(view.vsAddSpokenBtn)
	if got := sess.Config().VoiceSubtitles.SpokenLanguages; !slices.Equal(got, []string{"en", "tl", "ja"}) {
		t.Fatalf("expected SpokenLanguages [en tl ja], got %v", got)
	}

	// 5. Add a Target Display Language via pulldown + Add button ("Spanish — es")
	view.vsTargetAddSelect.SetSelected(FormatTranslationTargetLangOption("es"))
	test.Tap(view.vsAddTargetBtn)
	if got := sess.Config().VoiceSubtitles.TargetLanguages; !slices.Equal(got, []string{"en", "tl", "ja", "es"}) {
		t.Fatalf("expected TargetLanguages [en tl ja es], got %v", got)
	}

	// 6. Move row #2 ("tl") up to #1 -> ["tl", "en", "ja", "es"]
	row1Border := view.vsTargetListBox.Objects[1].(*fyne.Container)
	// First child in NewBorder(..., left, right, center) Objects: center is [0], left is [1]
	var moveUpBtn *widget.Button
	for _, child := range row1Border.Objects {
		if hbox, ok := child.(*fyne.Container); ok && len(hbox.Objects) >= 2 {
			if btn, okBtn := hbox.Objects[0].(*widget.Button); okBtn {
				moveUpBtn = btn
				break
			}
		}
	}
	if moveUpBtn == nil {
		t.Fatal("failed to locate MoveUp button on target language row #2")
	}
	test.Tap(moveUpBtn)
	if got := sess.Config().VoiceSubtitles.TargetLanguages; !slices.Equal(got, []string{"tl", "en", "ja", "es"}) {
		t.Fatalf("expected reordered TargetLanguages [tl en ja es], got %v", got)
	}

	// 7. Initially no GGML model is installed in temp dir -> Compute Device selector is locked/disabled with loud prompt
	if !view.vsComputeDeviceSelect.Disabled() {
		t.Fatal("expected vsComputeDeviceSelect to be disabled before GPU-ready GGML model is installed")
	}

	// Simulate GGML model + Silero VAD installed on disk -> Compute Device selector unlocks and lists Intel HD 630 iGPU
	sttDir := view.effectiveSTTModelDir()
	_ = os.MkdirAll(filepath.Join(sttDir, stt.ModelWhisperTurbo), 0755)
	_ = os.WriteFile(stt.VadModelPath(sttDir), []byte("vad"), 0644)
	ggmlPath, _ := stt.GGMLModelPath(sttDir, stt.ModelWhisperTurbo)
	_ = os.WriteFile(ggmlPath, []byte("ggml"), 0644)
	view.refreshVoiceSubtitleModelStatus()

	if view.vsComputeDeviceSelect.Disabled() {
		t.Fatal("expected vsComputeDeviceSelect to unlock once GGML model is installed")
	}

	igpuDev := stt.GPUDevice{GPUIndex: 0, VKIndex: 0, Name: "Vulkan0", Description: "Intel(R) HD Graphics 630", Type: "igpu", MemoryTotalMB: 4096}
	view.vsComputeDeviceSelect.SetSelected(igpuDev.UILabel())
	if got := sess.Config().VoiceSubtitles.ComputeDevice; got != "vulkan:Intel(R) HD Graphics 630" {
		t.Fatalf("expected ComputeDevice 'vulkan:Intel(R) HD Graphics 630', got %q", got)
	}
	view.vsThreadsSelect.SetSelected("2 Threads (4-Core Laptop Friendly)")
	if got := sess.Config().VoiceSubtitles.NumThreads; got != 2 {
		t.Fatalf("expected NumThreads=2, got %d", got)
	}

	// 8. Clicking "Configure Shared Translation Providers" switches subTabs to Translation
	test.Tap(view.vsOpenTranslationTabBtn)
	if view.subTabs.Selected() != view.translationTabItem {
		t.Fatal("expected clicking Configure Shared Translation Providers to select Translation tab")
	}

	// 9. Voice Detection card lives in General and auto-enabled when Voice Subtitles turned on
	if !sess.Config().EffectiveVoiceDetectionEnabled() || !view.enableVoiceDetectionCheck.Checked {
		t.Fatal("expected Voice Detection to auto-enable when Voice Subtitles is turned on")
	}
	if !view.voiceDetectionCard.Visible() {
		t.Fatal("expected voiceDetectionCard in General to be visible when Voice Detection is enabled")
	}

	// Clicking "Configure Microphone & Whisper Model" jumps back to General -> Voice Detection
	test.Tap(view.vsOpenVoiceDetectionBtn)
	if view.subTabs.Selected() != view.generalTabItem {
		t.Fatal("expected Configure Microphone & Whisper Model button to select General tab")
	}

	// 10. Master disable of Voice Detection hides voiceDetectionCard, Voice Subtitles tab, and OBS Subtitles overlay
	// while preserving VoiceSubtitles.Enabled so re-enabling restores them cleanly.
	view.enableVoiceDetectionCheck.SetChecked(false)
	if sess.Config().EffectiveVoiceDetectionEnabled() {
		t.Fatal("expected EffectiveVoiceDetectionEnabled()=false after unchecking master toggle")
	}
	if sess.Config().EffectiveSubtitlesOverlayEnabled() {
		t.Fatal("expected EffectiveSubtitlesOverlayEnabled()=false when Voice Detection is disabled")
	}
	if view.voiceDetectionCard.Visible() {
		t.Fatal("expected voiceDetectionCard to hide when Voice Detection is disabled")
	}
	if slices.Contains(view.subTabs.Items, view.voiceSubtitlesTabItem) {
		t.Fatal("expected Voice Subtitles sub-tab to hide when Voice Detection is disabled")
	}
	if view.wsSubtitlesOverlayGroup.Visible() {
		t.Fatal("expected OBS Subtitles overlay settings group to hide when Voice Detection is disabled")
	}
	if !sess.Config().VoiceSubtitles.Enabled {
		t.Fatal("expected VoiceSubtitles.Enabled to remain preserved in config while Voice Detection is off")
	}

	// Re-enabling Voice Detection restores Voice Subtitles tab and OBS Subtitles overlay
	view.enableVoiceDetectionCheck.SetChecked(true)
	if !sess.Config().EffectiveVoiceSubtitlesEnabled() || !slices.Contains(view.subTabs.Items, view.voiceSubtitlesTabItem) {
		t.Fatal("expected re-enabling Voice Detection to restore Voice Subtitles tab")
	}

	// 11. Wake word controls update Session config while preserving SettingsView MinSize().Width <= 550
	if view.vdWakeWordEntry == nil || view.vdWakeWindowSelect == nil || view.vdLastHeardLabel == nil {
		t.Fatal("expected wake word and last heard controls to be constructed")
	}
	view.vdWakeWordEntry.OnSubmitted("octo")
	if got := sess.Config().VoiceDetection.WakeWord; got != "octo" {
		t.Fatalf("expected WakeWord 'octo', got %q", got)
	}
	view.vdWakeWindowSelect.SetSelected("10 seconds")
	if got := sess.Config().VoiceDetection.EffectiveWakeWindowSec(); got != 10 {
		t.Fatalf("expected EffectiveWakeWindowSec()=10, got %d", got)
	}
	if view.vdWakeSoundCheck == nil || !view.vdWakeSoundCheck.Checked {
		t.Fatal("expected vdWakeSoundCheck to default to checked")
	}
	view.vdWakeSoundCheck.SetChecked(false)
	if sess.Config().VoiceDetection.EffectiveWakeSoundEnabled() {
		t.Fatal("expected EffectiveWakeSoundEnabled()=false after unchecking vdWakeSoundCheck")
	}
	if view.vdResponseVoiceLabel == nil || view.vdResponseVoiceLabel.Text != "Global Default TTS Voice" {
		t.Fatalf("expected default vdResponseVoiceLabel 'Global Default TTS Voice', got %v", view.vdResponseVoiceLabel)
	}
	if err := sess.UpdateConfig(func(c *config.Config) {
		c.VoiceDetection.ResponseVoiceID = "bm_lewis"
		c.VoiceDetection.ResponseSpeed = 1.15
	}); err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	view.SyncFromConfig()
	if view.vdResponseVoiceLabel.Text != FormatVoiceSpeedSummary("bm_lewis", 1.15) {
		t.Fatalf("expected updated vdResponseVoiceLabel summary, got %q", view.vdResponseVoiceLabel.Text)
	}
	if minW := obj.MinSize().Width; minW > 550 {
		t.Errorf("expected SettingsView MinSize().Width <= 550 DIPs with wake controls, got %v", minW)
	}
}

func TestSettingsViewBuildPreservesInitializingGuard(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()
	w := fyneApp.NewWindow("TestInitGuard")

	cfg := config.DefaultConfig()
	cfg.VoiceDetection.WakeWord = "octo"
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()
	sess.SetSTTModelDir(t.TempDir())

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetSTTModelDir(t.TempDir())
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "spk1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	view.SetCaptureDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "mic1", Name: "Shure MV7", IsDefault: true}}, nil
	})
	view.SetSTTGPUDeviceEnumerator(func() ([]stt.GPUDevice, error) {
		return nil, nil
	})

	view.initializing = true
	_ = view.buildVoiceDetectionCard()
	if !view.initializing {
		t.Fatal("expected buildVoiceDetectionCard to preserve outer initializing=true guard")
	}
	view.syncVoiceSubtitlesControlsFromConfig()
	if !view.initializing {
		t.Fatal("expected syncVoiceSubtitlesControlsFromConfig to preserve outer initializing=true guard")
	}
}
