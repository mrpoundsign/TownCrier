package ui

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// LaunchStateTracker manages the visual checklist and progress during application startup.
type LaunchStateTracker struct {
	window       fyne.Window
	statusLabel  *widget.Label
	stepConfig   *widget.Label
	stepAudio    *widget.Label
	stepTTS      *widget.Label
	progress     *widget.ProgressBarInfinite
	downloadProg *widget.ProgressBar
	actionBox    *fyne.Container
	retryBtn     *widget.Button
	quitBtn      *widget.Button

	mu         sync.Mutex
	lastUpdate time.Time
	onRetryFn  func()
	onQuitFn   func()
}

// NewLaunchWindow creates a centered launch status window.
func NewLaunchWindow(a fyne.App) (*LaunchStateTracker, fyne.Window) {
	w := a.NewWindow("TownCrier " + AppVersion + " — Launching")
	w.Resize(fyne.NewSize(480, 300))
	w.SetFixedSize(true)
	w.CenterOnScreen()

	title := widget.NewLabelWithStyle("TownCrier "+AppVersion, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	sub := widget.NewLabelWithStyle("Local Neural TTS Stream Chat Reader", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	dlProg := widget.NewProgressBar()
	dlProg.Hide()

	tracker := &LaunchStateTracker{
		window:       w,
		statusLabel:  widget.NewLabelWithStyle("Starting up subsystems...", fyne.TextAlignCenter, fyne.TextStyle{}),
		stepConfig:   widget.NewLabel("⏳ Reading configuration..."),
		stepAudio:    widget.NewLabel("⏳ Enumerating WASAPI playback devices..."),
		stepTTS:      widget.NewLabel("⏳ Loading Kokoro neural TTS engine..."),
		progress:     widget.NewProgressBarInfinite(),
		downloadProg: dlProg,
	}

	tracker.retryBtn = widget.NewButtonWithIcon("Retry Download", theme.ViewRefreshIcon(), func() {
		tracker.mu.Lock()
		retry := tracker.onRetryFn
		tracker.mu.Unlock()
		if retry != nil {
			fyne.Do(func() {
				tracker.actionBox.Hide()
				tracker.downloadProg.Hide()
				tracker.progress.Show()
				tracker.progress.Start()
				tracker.stepTTS.SetText("⏳ Loading Kokoro neural TTS engine...")
			})
			go retry()
		}
	})
	tracker.retryBtn.Importance = widget.HighImportance

	tracker.quitBtn = widget.NewButtonWithIcon("Quit", theme.CancelIcon(), func() {
		tracker.mu.Lock()
		quit := tracker.onQuitFn
		tracker.mu.Unlock()
		if quit != nil {
			quit()
		}
	})

	tracker.actionBox = container.NewCenter(container.NewHBox(tracker.retryBtn, tracker.quitBtn))
	tracker.actionBox.Hide()

	content := container.NewVBox(
		title,
		sub,
		tracker.progress,
		tracker.downloadProg,
		tracker.statusLabel,
		widget.NewSeparator(),
		tracker.stepConfig,
		tracker.stepAudio,
		tracker.stepTTS,
		tracker.actionBox,
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
		t.downloadProg.Hide()
		if !t.progress.Visible() {
			t.progress.Show()
		}
		if done {
			t.stepTTS.SetText("✓ Kokoro TTS: " + detail)
		} else {
			t.stepTTS.SetText("❌ Kokoro TTS: " + detail)
		}
	})
}

// SetDownloadProgress switches to the determinate progress bar and updates MB progress at up to 20 Hz.
func (t *LaunchStateTracker) SetDownloadProgress(current, total int64) {
	now := time.Now()
	t.mu.Lock()
	if current < total && now.Sub(t.lastUpdate) < 50*time.Millisecond {
		t.mu.Unlock()
		return
	}
	t.lastUpdate = now
	t.mu.Unlock()

	curMB := float64(current) / (1024 * 1024)
	totMB := float64(total) / (1024 * 1024)
	ratio := 0.0
	if total > 0 {
		ratio = min(float64(current)/float64(total), 1.0)
	}

	fyne.Do(func() {
		if t.progress.Visible() {
			t.progress.Stop()
			t.progress.Hide()
		}
		if !t.downloadProg.Visible() {
			t.downloadProg.Show()
		}
		t.downloadProg.SetValue(ratio)
		msg := fmt.Sprintf("Downloading & extracting Kokoro TTS models... %.1f / %.1f MB", curMB, totMB)
		t.statusLabel.SetText(msg)
		t.stepTTS.SetText(fmt.Sprintf("⏳ Kokoro TTS: Downloading (%.0f%%)...", ratio*100))
	})
}

// ShowFatalError halts startup on the launch window and presents Retry / Quit buttons.
func (t *LaunchStateTracker) ShowFatalError(statusMsg string, ttsDetail string, onRetry func(), onQuit func()) {
	t.mu.Lock()
	t.onRetryFn = onRetry
	t.onQuitFn = onQuit
	t.mu.Unlock()

	fyne.Do(func() {
		t.progress.Stop()
		t.progress.Hide()
		t.downloadProg.Hide()
		t.statusLabel.SetText(statusMsg)
		t.stepTTS.SetText("❌ Kokoro TTS: " + ttsDetail)
		t.actionBox.Show()
	})
}

// SetStatus updates the central status banner message.
func (t *LaunchStateTracker) SetStatus(msg string) {
	fyne.Do(func() {
		t.statusLabel.SetText(msg)
	})
}

