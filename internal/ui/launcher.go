package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// LaunchStateTracker manages the visual checklist and progress during application startup.
type LaunchStateTracker struct {
	window      fyne.Window
	statusLabel *widget.Label
	stepConfig  *widget.Label
	stepAudio   *widget.Label
	stepTTS     *widget.Label
	progress    *widget.ProgressBarInfinite
}

// NewLaunchWindow creates a centered launch status window.
func NewLaunchWindow(a fyne.App) (*LaunchStateTracker, fyne.Window) {
	w := a.NewWindow("TownCrier " + AppVersion + " — Launching")
	w.Resize(fyne.NewSize(450, 260))
	w.SetFixedSize(true)
	w.CenterOnScreen()

	title := widget.NewLabelWithStyle("TownCrier "+AppVersion, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	sub := widget.NewLabelWithStyle("Local Neural TTS Stream Chat Reader", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	tracker := &LaunchStateTracker{
		window:      w,
		statusLabel: widget.NewLabelWithStyle("Starting up subsystems...", fyne.TextAlignCenter, fyne.TextStyle{}),
		stepConfig:  widget.NewLabel("⏳ Reading configuration..."),
		stepAudio:   widget.NewLabel("⏳ Enumerating WASAPI playback devices..."),
		stepTTS:     widget.NewLabel("⏳ Loading Kokoro neural TTS engine..."),
		progress:    widget.NewProgressBarInfinite(),
	}

	content := container.NewVBox(
		title,
		sub,
		tracker.progress,
		tracker.statusLabel,
		widget.NewSeparator(),
		tracker.stepConfig,
		tracker.stepAudio,
		tracker.stepTTS,
	)

	w.SetContent(content)
	return tracker, w
}

// SetConfigStatus updates the configuration step message.
func (t *LaunchStateTracker) SetConfigStatus(done bool, detail string) {
	fyne.Do(func() {
		if done {
			t.stepConfig.SetText("✓ Configuration: " + detail)
		} else {
			t.stepConfig.SetText("⚠️ Configuration: " + detail)
		}
	})
}

// SetAudioStatus updates the audio subsystem step message.
func (t *LaunchStateTracker) SetAudioStatus(done bool, detail string) {
	fyne.Do(func() {
		if done {
			t.stepAudio.SetText("✓ Audio Playback: " + detail)
		} else {
			t.stepAudio.SetText("⚠️ Audio Playback: " + detail)
		}
	})
}

// SetTTSStatus updates the neural TTS engine step message.
func (t *LaunchStateTracker) SetTTSStatus(done bool, detail string) {
	fyne.Do(func() {
		if done {
			t.stepTTS.SetText("✓ Kokoro TTS: " + detail)
		} else {
			t.stepTTS.SetText("ℹ️ Kokoro TTS: " + detail)
		}
	})
}

// SetStatus updates the central status banner message.
func (t *LaunchStateTracker) SetStatus(msg string) {
	fyne.Do(func() {
		t.statusLabel.SetText(msg)
	})
}
