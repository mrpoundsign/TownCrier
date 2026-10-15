package ui

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/queue"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

type ActivityEntry struct {
	Timestamp time.Time
	Platform  string
	UserName  string
	Text      string
	Voice     string
	Kind      string
}

// DashboardView provides live monitoring, speech status, queue controls, and activity feed.
type DashboardView struct {
	window  fyne.Window
	session *app.Session

	mu         sync.Mutex
	activities []ActivityEntry

	nowPlayingLabel *widget.Label
	queueDepthLabel *widget.Label
	pauseResumeBtn  *widget.Button
	feedContainer   *fyne.Container
	onAssignVoice   func(platform, username string)
	onStatus        func(msg string)
}

// NewDashboardView constructs a DashboardView linked to the active session.
func NewDashboardView(w fyne.Window, session *app.Session) *DashboardView {
	v := &DashboardView{
		window:        w,
		session:       session,
		feedContainer: container.NewVBox(),
	}

	// Register queue event listeners
	if q := session.Queue(); q != nil {
		q.SetOnItemStarted(func(item *queue.Item) {
			fyne.Do(func() {
				v.updateCurrentlySpeaking(item)
			})
		})
		q.SetOnItemFinished(func(item *queue.Item) {
			fyne.Do(func() {
				v.updateFinishedSpeaking(item)
			})
		})
		q.SetOnQueueDepthChanged(func(depth int) {
			fyne.Do(func() {
				v.updateQueueDepth(depth)
			})
		})
	}

	// Register live stream event listener
	session.SetOnEventDispatched(func(ev *chat.Event, voice string) {
		if ev == nil {
			return
		}
		ts := ev.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		name := ev.DisplayName
		if name == "" {
			name = ev.UserName
		}
		v.AddActivity(ActivityEntry{
			Timestamp: ts,
			Platform:  ev.Platform,
			UserName:  name,
			Text:      ev.Text,
			Voice:     voice,
			Kind:      string(ev.Kind),
		})
	})

	return v
}

// SetOnAssignVoice sets a callback for when a voice is assigned to a chatter from the Dashboard.
func (v *DashboardView) SetOnAssignVoice(fn func(platform, username string)) {
	v.onAssignVoice = fn
}

// SetStatusHandler registers a callback invoked when dashboard actions update status.
func (v *DashboardView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

// Build creates the Fyne CanvasObject for the Dashboard tab.
func (v *DashboardView) Build() fyne.CanvasObject {
	speakingCard := v.buildSpeakingCard()
	controlsCard := v.buildControlsCard()
	activityCard := v.buildActivityCard()

	topSection := container.NewVBox(
		speakingCard,
		controlsCard,
	)

	return container.NewBorder(
		topSection,
		nil,
		nil,
		nil,
		activityCard,
	)
}

func (v *DashboardView) buildSpeakingCard() *widget.Card {
	v.nowPlayingLabel = widget.NewLabelWithStyle("Idle (No speech active)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	cardContent := container.NewVBox(
		v.nowPlayingLabel,
	)

	return widget.NewCard("Currently Speaking", "Active speech output", cardContent)
}

func (v *DashboardView) buildControlsCard() *widget.Card {
	q := v.session.Queue()
	initialDepth := 0
	isPaused := false
	if q != nil {
		initialDepth = q.Depth()
		isPaused = q.IsPaused()
	}

	v.queueDepthLabel = widget.NewLabel(fmt.Sprintf("Queue: %d pending items", initialDepth))

	pauseText := "Pause Queue"
	pauseIcon := theme.MediaPauseIcon()
	if isPaused {
		pauseText = "Resume Queue"
		pauseIcon = theme.MediaPlayIcon()
	}

	v.pauseResumeBtn = widget.NewButtonWithIcon(pauseText, pauseIcon, func() {
		if q == nil {
			return
		}
		if q.IsPaused() {
			q.Resume()
			v.pauseResumeBtn.SetText("Pause Queue")
			v.pauseResumeBtn.SetIcon(theme.MediaPauseIcon())
		} else {
			q.Pause()
			v.pauseResumeBtn.SetText("Resume Queue")
			v.pauseResumeBtn.SetIcon(theme.MediaPlayIcon())
		}
	})

	skipBtn := widget.NewButtonWithIcon("Skip Current", theme.MediaFastForwardIcon(), func() {
		if q != nil {
			q.Skip()
		}
	})

	clearBtn := widget.NewButtonWithIcon("Clear Pending Queue", theme.ContentClearIcon(), func() {
		if q != nil {
			q.Clear()
		}
	})

	buttonsRow := container.NewGridWithColumns(3, v.pauseResumeBtn, skipBtn, clearBtn)

	cardContent := container.NewVBox(
		container.NewBorder(nil, nil, v.queueDepthLabel, nil),
		buttonsRow,
	)

	return widget.NewCard("Queue & Playback Controls", "Manage live TTS queue", cardContent)
}

func (v *DashboardView) buildActivityCard() *widget.Card {
	v.feedContainer.Objects = nil

	if len(v.activities) == 0 {
		v.feedContainer.Add(widget.NewLabelWithStyle("No stream events yet this session.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
	} else {
		v.refreshFeedLocked()
	}

	return widget.NewCard("Live Activity Feed", "Recent chatter messages and stream alerts", container.NewVScroll(v.feedContainer))
}

// AddActivity appends a new event to the live activity feed (thread-safe).
func (v *DashboardView) AddActivity(entry ActivityEntry) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Keep last 50 entries
	if len(v.activities) >= 50 {
		v.activities = v.activities[1:]
	}
	v.activities = append(v.activities, entry)

	fyne.Do(func() {
		v.refreshFeedLocked()
	})
}

func (v *DashboardView) refreshFeedLocked() {
	v.feedContainer.Objects = nil
	for _, entry := range slices.Backward(v.activities) {
		row := v.buildActivityRow(entry)
		v.feedContainer.Add(row)
	}
	v.feedContainer.Refresh()
}

func (v *DashboardView) buildActivityRow(entry ActivityEntry) fyne.CanvasObject {
	timeStr := entry.Timestamp.Format("15:04:05")
	meta := fmt.Sprintf("[%s] %s (%s)", timeStr, entry.UserName, entry.Platform)

	nameLabel := widget.NewLabelWithStyle(meta, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	textLabel := widget.NewLabel(entry.Text)

	assignBtn := widget.NewButtonWithIcon("Assign Voice", theme.AccountIcon(), func() {
		v.showQuickAssignDialog(entry.Platform, entry.UserName)
	})

	topRow := container.NewBorder(nil, nil, nameLabel, assignBtn)
	return container.NewVBox(topRow, textLabel, widget.NewSeparator())
}

func (v *DashboardView) showQuickAssignDialog(platform, username string) {
	voices := tts.KokoroVoiceNames
	voiceSelect := widget.NewSelect(voices, nil)
	voiceSelect.SetSelected("af_bella")

	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.SetValue(1.0)
	speedLabel := widget.NewLabel("1.0x")
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.1fx", val))
	}

	ignoreCheck := widget.NewCheck("Ignore this chatter (silent)", nil)

	formContent := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Assign voice for: %s (%s)", username, platform)),
		widget.NewLabel("Voice:"),
		voiceSelect,
		container.NewBorder(nil, nil, widget.NewLabel("Speed:"), speedLabel, speedSlider),
		ignoreCheck,
	)

	dialog.ShowCustomConfirm("Assign Chatter Voice", "Save", "Cancel", formContent, func(confirmed bool) {
		if !confirmed {
			return
		}
		_ = v.session.UpdateConfig(func(cfg *config.Config) {
			if cfg.Viewers == nil {
				cfg.Viewers = make(map[string]config.ViewerConfig)
			}
			key := config.ViewerKey(platform, username)
			cfg.Viewers[key] = config.ViewerConfig{
				Platform: platform,
				UserID:   username,
				Voice:    voiceSelect.Selected,
				Speed:    float32(speedSlider.Value),
				Ignored:  ignoreCheck.Checked,
			}
		})
		if v.onAssignVoice != nil {
			v.onAssignVoice(platform, username)
		}
		if v.onStatus != nil {
			v.onStatus("✓ Assigned voice for " + username)
		}
	}, v.window)
}

func (v *DashboardView) updateCurrentlySpeaking(item *queue.Item) {
	if v.nowPlayingLabel == nil || item == nil {
		return
	}
	speaker := "Unknown"
	if item.Event != nil && item.Event.UserName != "" {
		speaker = item.Event.UserName
	}
	v.nowPlayingLabel.SetText(fmt.Sprintf("🗣️ Speaking [%s]: %s (Voice: %s, Speed: %.1fx)",
		speaker, item.Text, item.VoiceID, item.Speed))
}

func (v *DashboardView) updateFinishedSpeaking(item *queue.Item) {
	if v.nowPlayingLabel == nil {
		return
	}
	v.nowPlayingLabel.SetText("Idle (No speech active)")
}

func (v *DashboardView) updateQueueDepth(depth int) {
	if v.queueDepthLabel == nil {
		return
	}
	v.queueDepthLabel.SetText(fmt.Sprintf("Queue: %d pending items", depth))
}
