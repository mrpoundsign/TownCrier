package ui

import (
	"fmt"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/OctochorpeBot/internal/app"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
)

// VoicesTabView provides model management, voice testing, and global voice/speed configuration.
type VoicesTabView struct {
	window   fyne.Window
	session  *app.Session
	modelDir string

	statusLabel  *widget.Label
	modelStatus  *widget.Label
	downloadBtn  *widget.Button
	downloadProg *widget.ProgressBar
	previewBtn   *widget.Button
	sampleEntry  *widget.Entry
}

// NewVoicesTabView constructs a VoicesTabView.
func NewVoicesTabView(w fyne.Window, session *app.Session) *VoicesTabView {
	return &VoicesTabView{
		window:   w,
		session:  session,
		modelDir: tts.DefaultModelDir(),
	}
}

// Build creates the Fyne CanvasObject for the Voices tab.
func (v *VoicesTabView) Build() fyne.CanvasObject {
	v.statusLabel = widget.NewLabelWithStyle("Ready.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	modelCard := v.buildModelManagerCard()
	previewCard := v.buildPreviewCard()
	defaultsCard := v.buildGlobalDefaultsCard()

	content := container.NewVBox(
		modelCard,
		previewCard,
		defaultsCard,
		v.statusLabel,
	)

	return container.NewVScroll(content)
}

func (v *VoicesTabView) buildModelManagerCard() *widget.Card {
	v.modelStatus = widget.NewLabel("")
	v.downloadProg = widget.NewProgressBar()
	v.downloadProg.Hide()

	cfg := v.session.Config()

	if tts.IsModelInstalled(v.modelDir) {
		v.modelStatus.SetText(fmt.Sprintf("Kokoro-82M: Installed (%s)", filepath.Base(v.modelDir)))
	} else {
		v.modelStatus.SetText("Kokoro-82M: Not installed. Download required for neural TTS.")
	}

	v.downloadBtn = widget.NewButtonWithIcon("Download Kokoro-82M Model (~134MB INT8 / ~345MB FP32)", theme.DownloadIcon(), func() {
		v.startModelDownload()
	})

	gpuCheck := widget.NewCheck("Use DirectML GPU Acceleration (model.onnx FP32)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.TTS.UseGPU = b
		})
		v.statusLabel.SetText("GPU preference updated (takes effect on reload).")
	})
	gpuCheck.SetChecked(cfg.TTS.UseGPU)

	cardContent := container.NewVBox(
		v.modelStatus,
		v.downloadProg,
		v.downloadBtn,
		gpuCheck,
	)

	return widget.NewCard("Neural Model Manager", "Offline Kokoro TTS engine weights", cardContent)
}

func (v *VoicesTabView) buildPreviewCard() *widget.Card {
	voices := tts.KokoroVoiceNames
	voiceSelect := widget.NewSelect(voices, nil)
	voiceSelect.SetSelected("af_bella")

	speedLabel := widget.NewLabel("1.0x")
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.SetValue(1.0)
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.1fx", val))
	}

	v.sampleEntry = widget.NewEntry()
	v.sampleEntry.SetText("Hello! I am reading your livestream chat with natural neural speech.")

	v.previewBtn = widget.NewButtonWithIcon("Preview Voice", theme.MediaPlayIcon(), func() {
		text := v.sampleEntry.Text
		voice := voiceSelect.Selected
		speed := float32(speedSlider.Value)
		v.previewVoice(text, voice, speed)
	})
	v.previewBtn.Importance = widget.HighImportance

	cardContent := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Voice:"), nil, voiceSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Speed:"), speedLabel, speedSlider),
		widget.NewLabel("Sample Text:"),
		v.sampleEntry,
		v.previewBtn,
	)

	return widget.NewCard("Voice Selector & Preview", "Audition available Kokoro voices", cardContent)
}

func (v *VoicesTabView) buildGlobalDefaultsCard() *widget.Card {
	cfg := v.session.Config()
	voices := tts.KokoroVoiceNames

	selectedVoice := cfg.GlobalChatRules.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = "af_bella"
	}

	globalVoiceSelect := widget.NewSelect(voices, func(s string) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DefaultVoice = s
		})
	})
	globalVoiceSelect.SetSelected(selectedVoice)

	sp := cfg.GlobalChatRules.DefaultSpeed
	if sp <= 0 {
		sp = 1.0
	}
	globalSpeedLabel := widget.NewLabel(fmt.Sprintf("%.1fx", sp))
	globalSpeedSlider := widget.NewSlider(0.5, 2.0)
	globalSpeedSlider.SetValue(float64(sp))
	globalSpeedSlider.OnChanged = func(val float64) {
		globalSpeedLabel.SetText(fmt.Sprintf("%.1fx", val))
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DefaultSpeed = float32(val)
		})
	}

	cardContent := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Global Default Voice:"), nil, globalVoiceSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Global Default Speed:"), globalSpeedLabel, globalSpeedSlider),
	)

	return widget.NewCard("Global Fallback Voice", "Default voice and speed for chat and alerts", cardContent)
}

func (v *VoicesTabView) startModelDownload() {
	v.downloadBtn.Disable()
	v.downloadProg.Show()
	v.downloadProg.SetValue(0)
	v.statusLabel.SetText("Downloading Kokoro-82M neural model files...")

	go func() {
		err := tts.DownloadKokoroModel(v.modelDir, func(current, total int64) {
			if total > 0 {
				fyne.Do(func() {
					v.downloadProg.SetValue(float64(current) / float64(total))
				})
			}
		})

		fyne.Do(func() {
			v.downloadProg.Hide()
			if err != nil {
				v.downloadBtn.Enable()
				v.statusLabel.SetText(fmt.Sprintf("Download failed: %v", err))
				dialog.ShowError(err, v.window)
				return
			}

			v.modelStatus.SetText(fmt.Sprintf("Kokoro Model: Installed (%s)", filepath.Base(v.modelDir)))
			v.downloadBtn.Hide()
			v.statusLabel.SetText("Kokoro model downloaded and verified! Initializing engine...")

			cfg := v.session.Config()
			threads := cfg.TTS.NumThreads
			if threads <= 0 {
				threads = 4
			}
			eng, engErr := tts.NewSherpaEngine(v.modelDir, threads, cfg.TTS.UseGPU)
			if engErr != nil {
				v.statusLabel.SetText(fmt.Sprintf("Failed to initialize TTS engine: %v", engErr))
				return
			}
			v.session.SetEngine(eng)
			v.statusLabel.SetText("Kokoro TTS engine ready.")
		})
	}()
}

func (v *VoicesTabView) previewVoice(text, voice string, speed float32) {
	eng := v.session.Engine()
	if eng == nil {
		v.statusLabel.SetText("TTS engine not loaded. Download model first.")
		return
	}

	player := v.session.Player()
	v.previewBtn.Disable()
	v.statusLabel.SetText(fmt.Sprintf("Synthesizing preview with '%s'...", voice))

	go func() {
		t0 := time.Now()
		samples, sampleRate, err := eng.Synthesize(text, voice, speed)
		dur := time.Since(t0)

		fyne.Do(func() {
			v.previewBtn.Enable()
			if err != nil {
				v.statusLabel.SetText(fmt.Sprintf("Synthesis failed: %v", err))
				return
			}

			v.statusLabel.SetText(fmt.Sprintf("Synthesized in %v. Playing preview...", dur.Round(time.Millisecond)))
			if player != nil {
				_ = player.Play(samples, sampleRate)
			}
		})
	}()
}

func (v *VoicesTabView) updateConfig(fn func(c *config.Config)) {
	cfg := v.session.Config()
	fn(cfg)
	_ = v.session.ReloadConfig(cfg)
	_ = config.Save("", cfg)
}
