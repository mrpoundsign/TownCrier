package ui

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ModelDownloadModal manages a blocking modal dialog overlay shown during voice model downloads.
// It intercepts all mouse and keyboard interactions across the parent window until downloading
// and extracting finish, fail, or are cancelled.
type ModelDownloadModal struct {
	mu          sync.Mutex
	win         fyne.Window
	dlg         dialog.Dialog
	titleLbl    *widget.Label
	stepLbl     *widget.Label
	progress    *widget.ProgressBar
	progressLbl *widget.Label
	noticeLbl   *widget.Label
	cancelBtn   *widget.Button
	onCancel    func()
	lastUpdate  time.Time
	dismissed   bool
	cancelling  bool
}

// NewModelDownloadModal constructs and displays a modal download dialog attached to the given window.
func NewModelDownloadModal(win fyne.Window, initialModelName string, initialSizeMB int, onCancel func()) *ModelDownloadModal {
	m := &ModelDownloadModal{
		win:      win,
		onCancel: onCancel,
	}

	m.titleLbl = widget.NewLabelWithStyle("Downloading Voice Model Files", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	stepText := fmt.Sprintf("Downloading %s (~%d MB)...", initialModelName, initialSizeMB)
	if initialSizeMB <= 0 {
		stepText = fmt.Sprintf("Downloading %s...", initialModelName)
	}
	m.stepLbl = widget.NewLabelWithStyle(stepText, fyne.TextAlignCenter, fyne.TextStyle{})
	m.stepLbl.Wrapping = fyne.TextWrapWord

	m.progress = widget.NewProgressBar()
	m.progress.Min = 0.0
	m.progress.Max = 1.0
	m.progress.SetValue(0.0)

	m.progressLbl = widget.NewLabelWithStyle("Starting download...", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	m.noticeLbl = widget.NewLabelWithStyle("TownCrier settings and navigation are paused until downloads finish.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	m.noticeLbl.Wrapping = fyne.TextWrapWord

	m.cancelBtn = widget.NewButtonWithIcon("Cancel Download", theme.CancelIcon(), func() {
		m.mu.Lock()
		if m.cancelling || m.dismissed {
			m.mu.Unlock()
			return
		}
		m.cancelling = true
		cancelFn := m.onCancel
		m.cancelBtn.Disable()
		m.cancelBtn.SetText("Cancelling download...")
		m.stepLbl.SetText("Cancelling download and cleaning up temporary files...")
		m.mu.Unlock()

		if cancelFn != nil {
			go cancelFn()
		}
	})

	icon := widget.NewIcon(theme.DownloadIcon())
	header := container.NewHBox(icon, m.titleLbl)
	headerCenter := container.NewCenter(header)

	content := container.NewVBox(
		headerCenter,
		widget.NewSeparator(),
		m.stepLbl,
		m.progress,
		m.progressLbl,
		widget.NewSeparator(),
		m.noticeLbl,
		container.NewCenter(m.cancelBtn),
	)

	padded := container.NewPadded(content)

	if win != nil {
		m.dlg = dialog.NewCustomWithoutButtons("Voice Model Downloader", padded, win)
		m.dlg.Resize(fyne.NewSize(460, 260))
		m.dlg.Show()
	}

	return m
}

// SetStep updates the current step description and resets the progress bar for a new model pack.
func (m *ModelDownloadModal) SetStep(currentStep, totalSteps int, modelName string, sizeMB int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dismissed || m.cancelling {
		return
	}

	var stepText string
	if totalSteps > 1 {
		if sizeMB > 0 {
			stepText = fmt.Sprintf("Model %d of %d: Downloading %s (~%d MB)...", currentStep, totalSteps, modelName, sizeMB)
		} else {
			stepText = fmt.Sprintf("Model %d of %d: Downloading %s...", currentStep, totalSteps, modelName)
		}
	} else {
		if sizeMB > 0 {
			stepText = fmt.Sprintf("Downloading %s (~%d MB)...", modelName, sizeMB)
		} else {
			stepText = fmt.Sprintf("Downloading %s...", modelName)
		}
	}

	fyne.Do(func() {
		if m.stepLbl != nil {
			m.stepLbl.SetText(stepText)
		}
		if m.progress != nil {
			m.progress.SetValue(0.0)
		}
		if m.progressLbl != nil {
			m.progressLbl.SetText("Connecting to server...")
		}
	})
}

// UpdateProgress updates the progress bar and byte progress label at up to ~30 Hz.
func (m *ModelDownloadModal) UpdateProgress(current, total int64) {
	m.mu.Lock()
	if m.dismissed || m.cancelling {
		m.mu.Unlock()
		return
	}

	now := time.Now()
	// Throttle UI refreshes to ~30Hz (33ms) unless completed
	if current < total && now.Sub(m.lastUpdate) < 33*time.Millisecond {
		m.mu.Unlock()
		return
	}
	m.lastUpdate = now
	m.mu.Unlock()

	ratio := 0.0
	var text string
	if total > 0 {
		ratio = float64(current) / float64(total)
		if ratio > 1.0 {
			ratio = 1.0
		}
		curMB := float64(current) / (1024 * 1024)
		totMB := float64(total) / (1024 * 1024)
		pct := int(ratio * 100)
		text = fmt.Sprintf("%.1f / %.1f MB (%d%%)", curMB, totMB, pct)
	} else {
		curMB := float64(current) / (1024 * 1024)
		text = fmt.Sprintf("%.1f MB downloaded", curMB)
	}

	fyne.Do(func() {
		m.mu.Lock()
		dismissed := m.dismissed
		m.mu.Unlock()
		if dismissed {
			return
		}
		if m.progress != nil && total > 0 {
			m.progress.SetValue(ratio)
		}
		if m.progressLbl != nil {
			m.progressLbl.SetText(text)
		}
	})
}

// Dismiss closes and dismisses the modal dialog thread-safely.
func (m *ModelDownloadModal) Dismiss() {
	m.mu.Lock()
	if m.dismissed {
		m.mu.Unlock()
		return
	}
	m.dismissed = true
	dlg := m.dlg
	m.mu.Unlock()

	if dlg != nil {
		fyne.Do(func() {
			dlg.Hide()
		})
	}
}

// IsDismissed reports whether the modal has been dismissed.
func (m *ModelDownloadModal) IsDismissed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dismissed
}

// IsCancelling reports whether user clicked cancel.
func (m *ModelDownloadModal) IsCancelling() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelling
}
