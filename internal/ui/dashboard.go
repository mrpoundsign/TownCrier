package ui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/OctochorpeBot/internal/app"
	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/queue"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
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
	testUserEntry   *widget.Entry
	testMsgEntry    *widget.Entry
	onAssignVoice   func(platform, username string)
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

	return v
}

// SetOnAssignVoice sets a callback for when "Assign Voice" is clicked on a recent chatter.
func (v *DashboardView) SetOnAssignVoice(fn func(platform, username string)) {
	v.onAssignVoice = fn
}

// Build creates the Fyne CanvasObject for the Dashboard tab.
func (v *DashboardView) Build() fyne.CanvasObject {
	speakingCard := v.buildSpeakingCard()
	controlsCard := v.buildControlsCard()
	testCard := v.buildTestCard()
	activityCard := v.buildActivityCard()

	content := container.NewVBox(
		speakingCard,
		controlsCard,
		testCard,
		activityCard,
	)

	return container.NewVScroll(content)
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

	panicBtn := widget.NewButtonWithIcon("PANIC MUTE (Instant Halt)", theme.MediaStopIcon(), func() {
		v.session.Panic()
		if v.nowPlayingLabel != nil {
			v.nowPlayingLabel.SetText("🚨 Panic Mute Activated: Speech halted and queue cleared.")
		}
	})
	panicBtn.Importance = widget.DangerImportance

	buttonsRow := container.NewGridWithColumns(4, v.pauseResumeBtn, skipBtn, clearBtn, panicBtn)

	cardContent := container.NewVBox(
		container.NewBorder(nil, nil, v.queueDepthLabel, nil),
		buttonsRow,
	)

	return widget.NewCard("Queue & Playback Controls", "Manage live TTS queue and emergency mute", cardContent)
}

func (v *DashboardView) buildTestCard() *widget.Card {
	v.testUserEntry = widget.NewEntry()
	v.testUserEntry.SetPlaceHolder("Chatter name (e.g. alice)")
	v.testUserEntry.SetText("alice")

	v.testMsgEntry = widget.NewEntry()
	v.testMsgEntry.SetPlaceHolder("Message text (e.g. Hello world! https://twitch.tv ???? loooool)")
	v.testMsgEntry.SetText("Hello world! Check out https://twitch.tv for details! ??? wwwww")

	sendBtn := widget.NewButtonWithIcon("Send Test Chat Event", theme.MailSendIcon(), func() {
		user := strings.TrimSpace(v.testUserEntry.Text)
		msg := strings.TrimSpace(v.testMsgEntry.Text)
		if msg == "" {
			return
		}
		if user == "" {
			user = "test_viewer"
		}

		event := &chat.Event{
			ID:          fmt.Sprintf("test-%d", time.Now().UnixNano()),
			Platform:    "twitch",
			Kind:        chat.EventKindMessage,
			Timestamp:   time.Now(),
			UserID:      "test_" + user,
			UserName:    user,
			DisplayName: user,
			Text:        msg,
		}

		_ = v.session.DispatchEvent(event)
		v.AddActivity(ActivityEntry{
			Timestamp: time.Now(),
			Platform:  "twitch",
			UserName:  user,
			Text:      msg,
			Kind:      "Chat",
		})
	})
	sendBtn.Importance = widget.HighImportance

	inputRow := container.NewBorder(nil, nil, widget.NewLabel("Sender:"), nil, v.testUserEntry)
	msgRow := container.NewBorder(nil, nil, widget.NewLabel("Text:"), sendBtn, v.testMsgEntry)

	cardContent := container.NewVBox(
		inputRow,
		msgRow,
	)

	return widget.NewCard("Test Message Dispatcher", "Simulate inbound live chat to test routing and sanitization", cardContent)
}

func (v *DashboardView) buildActivityCard() *widget.Card {
	v.feedContainer.Objects = nil

	if len(v.activities) == 0 {
		v.feedContainer.Add(widget.NewLabelWithStyle("No stream events yet this session.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
	} else {
		v.refreshFeedLocked()
	}

	return widget.NewCard("Live Activity Feed", "Recent chatter messages and stream alerts", v.feedContainer)
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
		cfg := v.session.Config()
		key := fmt.Sprintf("%s:%s", platform, username)
		cfg.Viewers[key] = config.ViewerConfig{
			Platform: platform,
			UserID:   username,
			Voice:    voiceSelect.Selected,
			Speed:    float32(speedSlider.Value),
			Ignored:  ignoreCheck.Checked,
		}
		_ = v.session.ReloadConfig(cfg)
		_ = config.Save("", cfg)
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
