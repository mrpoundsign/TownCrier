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
	sampleEntry  *ScrollPassEntry
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

	return newPanelVScroll(content)
}

func (v *VoicesTabView) buildPreviewCard() *widget.Card {
	voiceLabels := tts.VoiceDisplayLabels()
	voiceSelect := widget.NewSelect(voiceLabels, nil)
	voiceSelect.SetSelected(tts.VoiceDisplayName("af_bella"))

	speedLabel := widget.NewLabel("1.00x")
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.Step = 0.05
	speedSlider.SetValue(1.0)
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.2fx", val))
	}

	v.sampleEntry = NewScrollPassEntry()
	v.sampleEntry.SetText("Hello! I am reading your livestream chat with natural neural speech.")

	v.previewBtn = widget.NewButtonWithIcon("Preview Voice", theme.MediaPlayIcon(), func() {
		text := v.sampleEntry.Text
		voice := tts.VoiceIDFromLabel(voiceSelect.Selected)
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
	voiceLabels := tts.VoiceDisplayLabels()

	selectedVoice := cfg.GlobalChatRules.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = "af_bella"
	}

	globalVoiceSelect := widget.NewSelect(voiceLabels, nil)
	globalVoiceSelect.SetSelected(tts.VoiceDisplayName(selectedVoice))
	globalVoiceSelect.OnChanged = func(s string) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DefaultVoice = tts.VoiceIDFromLabel(s)
		})
	}

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
		if player != nil {
			player.ResetPanic()
		}

		if streamEng, ok := eng.(tts.StreamingEngine); ok {
			startedPlaying := false
			var preBuffer []float32
			preBufferSR := 24000
			var ttfa time.Duration

			err := streamEng.SynthesizeStream(text, voice, speed, func(samples []float32, sampleRate int) bool {
				if len(samples) == 0 {
					return true
				}
				if sampleRate > 0 {
					preBufferSR = sampleRate
				}
				minPreBufferSamples := preBufferSR * 9 / 10
				if !startedPlaying {
					preBuffer = append(preBuffer, samples...)
					if len(preBuffer) >= minPreBufferSamples {
						startedPlaying = true
						ttfa = time.Since(t0)
						v.setStatus(fmt.Sprintf("First audio in %v — playing preview...", ttfa.Round(time.Millisecond)))
						if player != nil {
							_ = player.Play(preBuffer, preBufferSR)
						}
						preBuffer = nil
					}
					return true
				}
				if player != nil {
					_ = player.Play(samples, sampleRate)
				}
				return true
			})
			totalDur := time.Since(t0)
			if !startedPlaying && len(preBuffer) > 0 {
				ttfa = totalDur
				if player != nil && err == nil {
					_ = player.Play(preBuffer, preBufferSR)
				}
			}
			fyne.Do(func() {
				v.previewBtn.Enable()
				if err != nil {
					v.setStatus(fmt.Sprintf("Synthesis failed: %v", err))
					return
				}
				v.setStatus(fmt.Sprintf("Synthesized in %v (first audio %v). Playing preview...", totalDur.Round(time.Millisecond), ttfa.Round(time.Millisecond)))
			})
			return
		}

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

