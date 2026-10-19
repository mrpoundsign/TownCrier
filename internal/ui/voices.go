package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// VoicesTabView provides global voice and speed configuration.
type VoicesTabView struct {
	window  fyne.Window
	session *app.Session

	onStatus         func(msg string)
	globalVoiceField *VoiceSpeedField
	initializing     bool
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

// RefreshVoices updates the global voice field when enabled model packs change in Settings.
func (v *VoicesTabView) RefreshVoices() {
	fyne.Do(func() {
		cfg := v.session.Config()
		labels := tts.VoiceDisplayLabelsForModels(cfg.TTS.EffectiveEnabledModels())
		v.initializing = true
		defer func() { v.initializing = false }()

		if v.globalVoiceField != nil {
			defVoice := cfg.GlobalChatRules.DefaultVoice
			if defVoice == "" && len(labels) > 0 {
				defVoice = tts.VoiceIDFromLabel(labels[0])
			}
			defSpeed := cfg.GlobalChatRules.DefaultSpeed
			if defSpeed <= 0 {
				defSpeed = 1.0
			}
			v.globalVoiceField.SetVoiceAndSpeed(defVoice, defSpeed)
		}
	})
}

// Build creates the Fyne CanvasObject for the Voices tab.
func (v *VoicesTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	defaultsCard := v.buildGlobalDefaultsCard()

	content := container.NewVBox(
		defaultsCard,
	)

	return newPanelVScroll(content)
}

func (v *VoicesTabView) buildGlobalDefaultsCard() *widget.Card {
	cfg := v.session.Config()

	selectedVoice := cfg.GlobalChatRules.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = tts.DefaultVoiceID
	}
	sp := cfg.GlobalChatRules.DefaultSpeed
	if sp <= 0 {
		sp = 1.0
	}

	v.globalVoiceField = NewVoiceSpeedField(
		v.window,
		v.session,
		"Edit Global Default Voice & Speed",
		selectedVoice,
		sp,
		func(voiceID string, speed float32) {
			v.updateConfig(func(c *config.Config) {
				c.GlobalChatRules.DefaultVoice = voiceID
				c.GlobalChatRules.DefaultSpeed = speed
			})
		},
		v.setStatus,
	)

	cardContent := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Global Default Voice & Speed:"), nil, v.globalVoiceField.Container),
	)

	return widget.NewCard("Global Fallback Voice", "Default voice and speed for chat and alerts", cardContent)
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

