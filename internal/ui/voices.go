package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// VoicesTabView provides voice testing and global voice/speed configuration.
type VoicesTabView struct {
	window  fyne.Window
	session *app.Session

	onStatus     func(msg string)
	previewBtn   *widget.Button
	sampleEntry  *widget.Entry
	initializing bool
}

// NewVoicesTabView constructs a VoicesTabView.
func NewVoicesTabView(w fyne.Window, session *app.Session) *VoicesTabView {
	return &VoicesTabView{
		window:  w,
		session: session,
	}
}

// SetStatusHandler registers a callback invoked when voice preview or settings status updates.
func (v *VoicesTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *VoicesTabView) setStatus(msg string) {
	fyne.Do(func() {
		if v.onStatus != nil {
			v.onStatus(msg)
		}
	})
}

// Build creates the Fyne CanvasObject for the Voices tab.
func (v *VoicesTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	defaultsCard := v.buildGlobalDefaultsCard()
	previewCard := v.buildPreviewCard()

	content := container.NewVBox(
		defaultsCard,
		previewCard,
	)

	return container.NewVScroll(content)
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
	globalSpeedLabel := widget.NewLabel(fmt.Sprintf("%.2fx", sp))
	globalSpeedSlider := widget.NewSlider(0.5, 2.0)
	globalSpeedSlider.Step = 0.05
	globalSpeedSlider.SetValue(float64(sp))
	globalSpeedSlider.OnChanged = func(val float64) {
		globalSpeedLabel.SetText(fmt.Sprintf("%.2fx", val))
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

func (v *VoicesTabView) previewVoice(text, voice string, speed float32) {
	eng := v.session.Engine()
	if eng == nil {
		v.setStatus("TTS engine not loaded.")
		return
	}

	player := v.session.Player()
	v.previewBtn.Disable()
	v.setStatus(fmt.Sprintf("Synthesizing preview with '%s'...", voice))

	go func() {
		t0 := time.Now()
		samples, sampleRate, err := eng.Synthesize(text, voice, speed)
		dur := time.Since(t0)

		fyne.Do(func() {
			v.previewBtn.Enable()
			if err != nil {
				v.setStatus(fmt.Sprintf("Synthesis failed: %v", err))
				return
			}

			v.setStatus(fmt.Sprintf("Synthesized in %v. Playing preview...", dur.Round(time.Millisecond)))
			if player != nil {
				player.ResetPanic()
				_ = player.Play(samples, sampleRate)
			}
		})
	}()
}

func (v *VoicesTabView) updateConfig(fn func(c *config.Config)) {
	if v.initializing {
		return
	}
	if v.session != nil {
		_ = v.session.UpdateConfig(fn)
	}
	v.setStatus("✓ Settings saved.")
}

