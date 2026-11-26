package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/bot"
	"github.com/mrpoundsign/TownCrier/internal/bot/obs"
	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/tts"
)

// BotTabView provides the UI for Streamer.bot-class Bot Automation (Commands, Triggers,
// Actions & Sub-Actions, Timers, Whisper Voice Commands, Execution Queues, Variables, and OBS Studio v5).
type BotTabView struct {
	window          fyne.Window
	session         *app.Session
	onStatus        func(string)
	onConfigChanged func()

	engineStatusLbl *widget.Label
	obsStatusLbl    *widget.Label
	obsBadgeMu      sync.Mutex

	commandsBox  *fyne.Container
	triggersBox  *fyne.Container
	actionsBox   *fyne.Container
	timersBox    *fyne.Container
	voiceCmdsBox *fyne.Container
	queuesBox    *fyne.Container
	varsBox      *fyne.Container
	groupsBox    *fyne.Container

	voiceWakeWordEntry     *ScrollPassEntry
	voiceWakeWindowSelect  *widget.Select
	voiceWakeSoundCheck    *widget.Check
	voiceResponseLbl       *widget.Label
	voiceMonitorStatusLbl  *widget.Label
	voiceTranscriptFeedLbl *widget.Label
	voiceTranscriptScroll  *container.Scroll
	voiceTestEntry         *ScrollPassEntry
	voiceTestResultLbl     *widget.Label
	voiceTranscriptMu      sync.Mutex
	voiceTranscriptHistory []bot.VoiceTranscriptEvent

	selectedActionID string

	// OBS tab controls exposed for tests and programmatic updates.
	obsEnabledCheck  *widget.Check
	obsAutoReconnChk *widget.Check
	obsHostEntry     *ScrollPassEntry
	obsPortEntry     *ScrollPassEntry
	obsPasswordEntry *widget.Entry
	obsConnectBtn    *widget.Button
	obsDisconnectBtn *widget.Button
	obsTestBtn       *widget.Button

	initializing bool
}

// NewBotTabView creates a BotTabView bound to session.
func NewBotTabView(w fyne.Window, sess *app.Session, onStatus func(string)) *BotTabView {
	return &BotTabView{
		window:   w,
		session:  sess,
		onStatus: onStatus,
	}
}

// SetStatusHandler registers a status bar callback.
func (v *BotTabView) SetStatusHandler(fn func(string)) {
	v.onStatus = fn
}

// SetOnConfigChanged registers a callback invoked when BotTabView mutates config (e.g. auto-enabling Voice Detection).
func (v *BotTabView) SetOnConfigChanged(fn func()) {
	v.onConfigChanged = fn
}

func (v *BotTabView) reportStatus(msg string) {
	if v.onStatus != nil {
		v.onStatus(msg)
	}
}

func (v *BotTabView) currentConfig() *config.Config {
	if v.session == nil {
		return config.DefaultConfig()
	}
	return v.session.Config()
}

func (v *BotTabView) updateBotConfig(fn func(b *config.BotAutomationConfig)) error {
	if v.session == nil {
		return nil
	}
	err := v.session.UpdateConfig(func(c *config.Config) {
		fn(&c.Bot)
		config.NormalizeBotAutomationConfig(&c.Bot)
	})
	v.Refresh()
	if v.onConfigChanged != nil {
		v.onConfigChanged()
	}
	return err
}

func newBotItemID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// Build constructs the complete Bot Automation workspace.
func (v *BotTabView) Build() fyne.CanvasObject {
	v.engineStatusLbl = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.obsStatusLbl = widget.NewLabelWithStyle("OBS: Disconnected", fyne.TextAlignLeading, fyne.TextStyle{})

	subTabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Commands & Voice", theme.MailReplyAllIcon(), v.buildCommandsAndVoiceTab()),
		container.NewTabItemWithIcon("Actions & Sub-Actions", theme.MediaPlayIcon(), v.buildActionsTab()),
		container.NewTabItemWithIcon("Triggers & Timers", theme.HistoryIcon(), v.buildTriggersAndTimersTab()),
		container.NewTabItemWithIcon("Queues & Variables", theme.StorageIcon(), v.buildQueuesAndVarsTab()),
		container.NewTabItemWithIcon("OBS Studio", theme.ComputerIcon(), v.buildOBSTab()),
	)

	if v.session != nil {
		v.session.SetOnOBSStatus(func(st obs.ClientStatus) {
			fyne.Do(func() {
				v.updateOBSStatusBadge(st)
			})
		})
		v.session.AddOnVoiceTranscript(v.SyncVoiceTranscript)
		v.session.AddOnSTTStatus(v.SyncSTTStatus)
		if recent := v.session.RecentVoiceTranscripts(); len(recent) > 0 {
			v.voiceTranscriptMu.Lock()
			v.voiceTranscriptHistory = append([]bot.VoiceTranscriptEvent(nil), recent...)
			v.voiceTranscriptMu.Unlock()
			v.refreshVoiceTranscriptFeed()
		}
	}

	v.Refresh()
	return subTabs
}

// Refresh reloads all Bot Automation lists and status labels from Session.
func (v *BotTabView) Refresh() {
	cfg := v.currentConfig()
	if v.engineStatusLbl != nil {
		if cfg.Bot.Enabled {
			v.engineStatusLbl.SetText("Bot Engine: Active")
		} else {
			v.engineStatusLbl.SetText("Bot Engine: Disabled (enable in Settings → General)")
		}
	}
	if v.obsStatusLbl != nil {
		st := obs.ClientStatus{State: obs.StateDisconnected}
		if v.session != nil && v.session.BotEngine() != nil && v.session.BotEngine().OBSClient() != nil {
			st = v.session.BotEngine().OBSClient().Status()
		}
		v.updateOBSStatusBadge(st)
	}
	v.refreshCommandsAndTriggers(cfg.Bot)
	v.refreshActions(cfg.Bot)
	v.refreshTimersAndVoice(cfg.Bot)
	v.refreshQueuesAndVars(cfg.Bot)
	v.syncOBSFieldsFromConfig(cfg.Bot)
}

func (v *BotTabView) updateOBSStatusBadge(st obs.ClientStatus) {
	v.obsBadgeMu.Lock()
	defer v.obsBadgeMu.Unlock()
	if v.obsStatusLbl == nil {
		return
	}
	next := "⚪ OBS Disconnected"
	switch st.State {
	case obs.StateConnected:
		ver := strings.TrimSpace(st.OBSVersion)
		if ver != "" {
			next = fmt.Sprintf("🟢 OBS %s (%s)", ver, st.Address)
		} else {
			next = fmt.Sprintf("🟢 OBS Connected (%s)", st.Address)
		}
	case obs.StateConnecting:
		next = fmt.Sprintf("🟡 OBS Connecting (%s)...", st.Address)
	case obs.StateError:
		next = "🔴 OBS Error: " + st.LastError
	}
	if v.obsStatusLbl.Text != next {
		v.obsStatusLbl.SetText(next)
	}
}

func (v *BotTabView) actionDisplayMap(b config.BotAutomationConfig) (labels []string, labelToID map[string]string, idToLabel map[string]string) {
	labelToID = make(map[string]string, len(b.Actions))
	idToLabel = make(map[string]string, len(b.Actions))

	nameCounts := make(map[string]int, len(b.Actions))
	baseNames := make([]string, len(b.Actions))
	for i, a := range b.Actions {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = "Untitled Action"
		}
		baseNames[i] = name
		nameCounts[name]++
	}

	seenCounts := make(map[string]int, len(b.Actions))
	for i, a := range b.Actions {
		name := baseNames[i]
		lbl := name
		if nameCounts[name] > 1 {
			seenCounts[name]++
			lbl = fmt.Sprintf("%s (#%d)", name, seenCounts[name])
		}
		labels = append(labels, lbl)
		labelToID[lbl] = a.ID
		idToLabel[a.ID] = lbl
	}
	return labels, labelToID, idToLabel
}

func (v *BotTabView) actionName(b config.BotAutomationConfig, actionID string) string {
	for _, a := range b.Actions {
		if a.ID == actionID {
			if strings.TrimSpace(a.Name) != "" {
				return a.Name
			}
			return "Untitled Action"
		}
	}
	if actionID == "" {
		return "(none)"
	}
	return actionID
}

func (v *BotTabView) queueDisplayMap(b config.BotAutomationConfig) (labels []string, labelToID map[string]string, idToLabel map[string]string) {
	labelToID = make(map[string]string, len(b.Queues))
	idToLabel = make(map[string]string, len(b.Queues))

	nameCounts := make(map[string]int, len(b.Queues))
	baseNames := make([]string, len(b.Queues))
	for i, q := range b.Queues {
		name := strings.TrimSpace(q.Name)
		if name == "" {
			name = q.ID
		}
		baseNames[i] = name
		nameCounts[name]++
	}

	seenCounts := make(map[string]int, len(b.Queues))
	for i, q := range b.Queues {
		name := baseNames[i]
		lbl := name
		if nameCounts[name] > 1 {
			seenCounts[name]++
			lbl = fmt.Sprintf("%s (#%d)", name, seenCounts[name])
		}
		labels = append(labels, lbl)
		labelToID[lbl] = q.ID
		idToLabel[q.ID] = lbl
	}
	return labels, labelToID, idToLabel
}

func (v *BotTabView) queueName(b config.BotAutomationConfig, queueID string) string {
	if strings.TrimSpace(queueID) == "" {
		queueID = "default"
	}
	for _, q := range b.Queues {
		if q.ID == queueID {
			if strings.TrimSpace(q.Name) != "" {
				return q.Name
			}
			return q.ID
		}
	}
	return queueID
}

func (v *BotTabView) pluginDisplayMap(savedUUID string) (labels []string, labelToUUID map[string]string, uuidToLabel map[string]string, byUUID map[string]pluginhost.PluginStatus) {
	labelToUUID = make(map[string]string)
	uuidToLabel = make(map[string]string)
	byUUID = make(map[string]pluginhost.PluginStatus)

	if v.session != nil && v.session.PluginManager() != nil {
		plugins := v.session.PluginManager().Plugins()
		nameCounts := make(map[string]int, len(plugins))
		for _, p := range plugins {
			if p.State == pluginhost.StatusDisabled {
				continue
			}
			name := strings.TrimSpace(p.Name)
			if name == "" {
				name = p.UUID
			}
			nameCounts[name]++
		}
		seen := make(map[string]int, len(plugins))
		for _, p := range plugins {
			if p.State == pluginhost.StatusDisabled {
				continue
			}
			byUUID[p.UUID] = p
			name := strings.TrimSpace(p.Name)
			if name == "" {
				name = p.UUID
			}
			lbl := name
			if nameCounts[name] > 1 {
				seen[name]++
				lbl = fmt.Sprintf("%s (#%d)", name, seen[name])
			}
			labels = append(labels, lbl)
			labelToUUID[lbl] = p.UUID
			uuidToLabel[p.UUID] = lbl
		}
	}

	if clean := strings.TrimSpace(savedUUID); clean != "" {
		if _, ok := uuidToLabel[clean]; !ok {
			lbl := fmt.Sprintf("Offline Plugin (%s)", clean)
			labels = append(labels, lbl)
			labelToUUID[lbl] = clean
			uuidToLabel[clean] = lbl
		}
	}
	return labels, labelToUUID, uuidToLabel, byUUID
}

func pluginActionDisplayMap(p *pluginhost.PluginStatus, savedActionID string) (labels []string, labelToID map[string]string, idToLabel map[string]string, descByID map[string]string) {
	labelToID = make(map[string]string)
	idToLabel = make(map[string]string)
	descByID = make(map[string]string)

	if p != nil {
		nameCounts := make(map[string]int, len(p.Actions))
		for _, ad := range p.Actions {
			name := strings.TrimSpace(ad.GetName())
			if name == "" {
				name = ad.GetId()
			}
			nameCounts[name]++
		}
		seen := make(map[string]int, len(p.Actions))
		for _, ad := range p.Actions {
			name := strings.TrimSpace(ad.GetName())
			if name == "" {
				name = ad.GetId()
			}
			lbl := name
			if nameCounts[name] > 1 {
				seen[name]++
				lbl = fmt.Sprintf("%s (#%d)", name, seen[name])
			}
			labels = append(labels, lbl)
			labelToID[lbl] = ad.GetId()
			idToLabel[ad.GetId()] = lbl
			descByID[ad.GetId()] = strings.TrimSpace(ad.GetDescription())
		}
	}

	if clean := strings.TrimSpace(savedActionID); clean != "" {
		if _, ok := idToLabel[clean]; !ok {
			lbl := clean + " (Offline)"
			labels = append(labels, lbl)
			labelToID[lbl] = clean
			idToLabel[clean] = lbl
		}
	}
	return labels, labelToID, idToLabel, descByID
}

func (v *BotTabView) pluginName(uuid string) string {
	clean := strings.TrimSpace(uuid)
	if clean == "" {
		return "(none)"
	}
	if v.session != nil && v.session.PluginManager() != nil {
		for _, p := range v.session.PluginManager().Plugins() {
			if p.UUID == clean {
				if strings.TrimSpace(p.Name) != "" {
					return p.Name
				}
				return p.UUID
			}
		}
	}
	return clean
}

func (v *BotTabView) pluginActionName(uuid, actionID string) string {
	cleanUUID := strings.TrimSpace(uuid)
	cleanAct := strings.TrimSpace(actionID)
	if cleanAct == "" {
		return "(none)"
	}
	if v.session != nil && v.session.PluginManager() != nil {
		for _, p := range v.session.PluginManager().Plugins() {
			if p.UUID == cleanUUID {
				for _, ad := range p.Actions {
					if ad.GetId() == cleanAct {
						if strings.TrimSpace(ad.GetName()) != "" {
							return ad.GetName()
						}
						return ad.GetId()
					}
				}
			}
		}
	}
	return cleanAct
}

func (v *BotTabView) pluginTriggerName(triggerID string) string {
	clean := strings.TrimSpace(triggerID)
	if clean == "" {
		return "(none)"
	}
	if v.session != nil && v.session.PluginManager() != nil {
		for _, p := range v.session.PluginManager().Plugins() {
			for _, td := range p.Triggers {
				if td.GetId() == clean {
					name := strings.TrimSpace(td.GetName())
					if name == "" {
						name = td.GetId()
					}
					if strings.TrimSpace(p.Name) != "" {
						return p.Name + " — " + name
					}
					return name
				}
			}
		}
	}
	return clean
}

// --- Sub-tab 1: Commands & Voice ---

func (v *BotTabView) buildCommandsAndVoiceTab() fyne.CanvasObject {
	v.commandsBox = container.NewVBox()
	v.voiceCmdsBox = container.NewVBox()

	addCmdBtn := widget.NewButtonWithIcon("Add Command", theme.ContentAddIcon(), func() {
		v.openCommandDialog(-1)
	})
	addCmdBtn.Importance = widget.HighImportance

	addVoiceBtn := widget.NewButtonWithIcon("Add Voice Command", theme.ContentAddIcon(), func() {
		v.openVoiceCmdDialog(-1)
	})
	addVoiceBtn.Importance = widget.HighImportance

	cmdsCard := widget.NewCard(
		"Chat Commands",
		"Respond to !commands across connected platforms (raw Event Bus before TTS filters)",
		container.NewVBox(container.NewHBox(addCmdBtn), v.commandsBox),
	)
	voiceCard := widget.NewCard(
		"Whisper Voice Commands",
		"Trigger actions when local Whisper STT hears a spoken phrase on your configured microphone",
		container.NewVBox(container.NewHBox(addVoiceBtn), v.voiceCmdsBox),
	)

	cfg := v.currentConfig()
	v.voiceWakeWordEntry = NewScrollPassEntry()
	v.voiceWakeWordEntry.SetPlaceHolder("Optional, comma-separated (e.g. octothorpe, hey octo) — 2–3+ syllables work best")
	v.voiceWakeWordEntry.SetText(cfg.VoiceDetection.WakeWord)

	saveWakeSettings := func(word string, windowSec int) {
		clean := strings.TrimSpace(word)
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				c.VoiceDetection.WakeWord = clean
				if windowSec > 0 {
					c.VoiceDetection.WakeWindowSec = windowSec
				}
				config.NormalizeVoiceDetectionConfig(&c.VoiceDetection)
			})
		}
		v.Refresh()
		if v.onConfigChanged != nil {
			v.onConfigChanged()
		}
		if clean == "" {
			v.reportStatus("✓ Global wake word cleared (voice commands listen directly).")
		} else {
			v.reportStatus(fmt.Sprintf("✓ Global wake word set to %q.", clean))
		}
	}
	v.voiceWakeWordEntry.OnSubmitted = func(text string) {
		saveWakeSettings(text, 0)
	}

	v.voiceWakeWindowSelect = widget.NewSelect(voiceWakeWindowOptions, func(selected string) {
		if v.initializing {
			return
		}
		sec := parseWakeWindowOption(selected)
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				c.VoiceDetection.WakeWindowSec = sec
				config.NormalizeVoiceDetectionConfig(&c.VoiceDetection)
			})
		}
		if v.onConfigChanged != nil {
			v.onConfigChanged()
		}
		v.reportStatus(fmt.Sprintf("✓ Wake word armed window set to %ds.", sec))
	})
	prevInit := v.initializing
	v.initializing = true
	v.voiceWakeWindowSelect.SetSelected(formatWakeWindowOption(cfg.VoiceDetection.EffectiveWakeWindowSec()))
	v.initializing = prevInit

	saveWakeBtn := widget.NewButton("Save Wake Word", func() {
		saveWakeSettings(v.voiceWakeWordEntry.Text, 0)
	})
	clearWakeBtn := widget.NewButton("Clear", func() {
		v.voiceWakeWordEntry.SetText("")
		saveWakeSettings("", 0)
	})

	v.voiceWakeSoundCheck = widget.NewCheck("Play beep when wake word activates", func(checked bool) {
		if v.initializing {
			return
		}
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				c.VoiceDetection.SetWakeSoundEnabled(checked)
			})
		}
		if v.onConfigChanged != nil {
			v.onConfigChanged()
		}
		if checked {
			v.reportStatus("✓ Wake-word activation beep enabled.")
		} else {
			v.reportStatus("Wake-word activation beep disabled.")
		}
	})
	prevInitSound := v.initializing
	v.initializing = true
	v.voiceWakeSoundCheck.SetChecked(cfg.VoiceDetection.EffectiveWakeSoundEnabled())
	v.initializing = prevInitSound

	testBeepBtn := widget.NewButton("Test Beep", func() {
		if v.session == nil {
			return
		}
		go func() {
			_ = v.session.PlayWakeWordChime()
		}()
	})

	v.voiceResponseLbl = widget.NewLabel(formatVoiceResponseSummary(cfg.VoiceDetection.ResponseVoiceID, cfg.VoiceDetection.ResponseSpeed))
	v.voiceResponseLbl.Truncation = fyne.TextTruncateEllipsis
	chooseRespVoiceBtn := widget.NewButtonWithIcon("Choose Voice…", theme.VolumeUpIcon(), func() {
		curVD := v.currentConfig().VoiceDetection
		initVoice := strings.TrimSpace(curVD.ResponseVoiceID)
		initSpeed := curVD.ResponseSpeed
		if initSpeed <= 0 {
			initSpeed = 1.0
		}
		ShowVoiceSpeedDialog(v.window, v.session, "Select Keyword Response Voice", initVoice, initSpeed, func(newVoiceID string, newSpeed float32) {
			if v.session != nil {
				_ = v.session.UpdateConfig(func(c *config.Config) {
					c.VoiceDetection.ResponseVoiceID = newVoiceID
					c.VoiceDetection.ResponseSpeed = newSpeed
					config.NormalizeVoiceDetectionConfig(&c.VoiceDetection)
				})
			}
			v.Refresh()
			if v.onConfigChanged != nil {
				v.onConfigChanged()
			}
			v.reportStatus("✓ Keyword response voice set to " + formatVoiceResponseSummary(newVoiceID, newSpeed))
		}, v.onStatus)
	})
	clearRespVoiceBtn := widget.NewButton("Use Default", func() {
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				c.VoiceDetection.ResponseVoiceID = ""
				c.VoiceDetection.ResponseSpeed = 0
			})
		}
		v.Refresh()
		if v.onConfigChanged != nil {
			v.onConfigChanged()
		}
		v.reportStatus("✓ Keyword response voice reset to Global Default.")
	})

	v.voiceMonitorStatusLbl = widget.NewLabel("⚪ Waiting for speech")
	clearFeedBtn := widget.NewButton("Clear Feed", func() {
		if v.session != nil {
			v.session.ClearRecentVoiceTranscripts()
		}
		v.voiceTranscriptMu.Lock()
		v.voiceTranscriptHistory = nil
		v.voiceTranscriptMu.Unlock()
		v.refreshVoiceTranscriptFeed()
	})

	v.voiceTranscriptFeedLbl = widget.NewLabel("No Whisper transcripts heard yet. Speak into your mic or run a test phrase below.")
	v.voiceTranscriptFeedLbl.TextStyle = fyne.TextStyle{Monospace: true}
	v.voiceTranscriptFeedLbl.Wrapping = fyne.TextWrapWord

	v.voiceTranscriptScroll = container.NewVScroll(container.NewThemeOverride(v.voiceTranscriptFeedLbl, NewScaledChatTheme(0.85)))
	v.voiceTranscriptScroll.SetMinSize(fyne.NewSize(420, 130))

	v.voiceTestEntry = NewScrollPassEntry()
	v.voiceTestEntry.SetPlaceHolder("Type a sample phrase (e.g. Octo, say hello. or Auto say hello)")
	v.voiceTestResultLbl = widget.NewLabel("Tip: Whisper adds punctuation (like commas/periods); 'Contains Phrase' ignores punctuation and 'Phonetic / Sound-Alike' matches similar words like \"auto\" or \"otto\" for \"octo\".")
	v.voiceTestResultLbl.Wrapping = fyne.TextWrapWord

	runTestPhrase := func(execute bool) {
		raw := strings.TrimSpace(v.voiceTestEntry.Text)
		if raw == "" {
			v.voiceTestResultLbl.SetText("Enter a phrase above to test matching against your enabled voice commands.")
			return
		}
		if v.session == nil || v.session.BotEngine() == nil {
			v.voiceTestResultLbl.SetText("Bot engine unavailable.")
			return
		}
		ev := v.session.BotEngine().TestVoiceTranscript(raw, execute)
		v.voiceTestResultLbl.SetText(formatVoiceEvalSummary(ev))
	}
	v.voiceTestEntry.OnSubmitted = func(_ string) {
		runTestPhrase(false)
	}
	testMatchBtn := widget.NewButtonWithIcon("Test Match", theme.SearchIcon(), func() {
		runTestPhrase(false)
	})
	testRunBtn := widget.NewButtonWithIcon("Test & Run", theme.MediaPlayIcon(), func() {
		runTestPhrase(true)
	})
	testRunBtn.Importance = widget.HighImportance

	wakeForm := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Wake Word (Optional)", container.NewBorder(nil, nil, nil, container.NewHBox(saveWakeBtn, clearWakeBtn), v.voiceWakeWordEntry)),
			widget.NewFormItem("Armed Window", v.voiceWakeWindowSelect),
			widget.NewFormItem("Response Voice", container.NewBorder(nil, nil, nil, container.NewHBox(chooseRespVoiceBtn, clearRespVoiceBtn), v.voiceResponseLbl)),
		),
		container.NewHBox(v.voiceWakeSoundCheck, testBeepBtn),
	)
	feedHeader := container.NewHBox(widget.NewLabelWithStyle("Recent What Whisper Heard:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), layout.NewSpacer(), v.voiceMonitorStatusLbl, clearFeedBtn)
	testerRow := container.NewBorder(nil, nil, nil, container.NewHBox(testMatchBtn, testRunBtn), v.voiceTestEntry)

	monitorCard := widget.NewCard(
		"Live Whisper Monitor & Voice Command Tester",
		"See exactly what Whisper hears from your mic, set an optional wake word, and test command matching without waiting on audio",
		container.NewVBox(
			wakeForm,
			widget.NewSeparator(),
			feedHeader,
			v.voiceTranscriptScroll,
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Interactive Phrase Tester:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			testerRow,
			v.voiceTestResultLbl,
		),
	)

	return newPanelVScroll(container.NewVBox(cmdsCard, voiceCard, monitorCard))
}

// SyncSTTStatus updates the live Whisper listening indicator in Bot -> Commands & Voice.
func (v *BotTabView) SyncSTTStatus(speaking, transcribing bool, errText string) {
	fyne.Do(func() {
		if v.voiceMonitorStatusLbl == nil {
			return
		}
		if strings.TrimSpace(errText) != "" {
			v.voiceMonitorStatusLbl.SetText("⚠️ " + strings.TrimSpace(errText))
			return
		}
		switch {
		case transcribing:
			v.voiceMonitorStatusLbl.SetText("⏳ Transcribing...")
		case speaking:
			v.voiceMonitorStatusLbl.SetText("🎙️ Speaking...")
		default:
			if v.currentConfig().EffectiveVoiceDetectionEnabled() {
				v.voiceMonitorStatusLbl.SetText("🟢 Listening")
			} else {
				v.voiceMonitorStatusLbl.SetText("⚪ Voice Detection Off")
			}
		}
	})
}

// SyncVoiceTranscript appends a live Whisper transcript evaluation to the monitor feed.
func (v *BotTabView) SyncVoiceTranscript(ev bot.VoiceTranscriptEvent) {
	v.voiceTranscriptMu.Lock()
	v.voiceTranscriptHistory = append(v.voiceTranscriptHistory, ev)
	if len(v.voiceTranscriptHistory) > 15 {
		v.voiceTranscriptHistory = v.voiceTranscriptHistory[len(v.voiceTranscriptHistory)-15:]
	}
	v.voiceTranscriptMu.Unlock()
	fyne.Do(func() {
		v.refreshVoiceTranscriptFeed()
	})
}

func (v *BotTabView) refreshVoiceTranscriptFeed() {
	if v.voiceTranscriptFeedLbl == nil {
		return
	}
	v.voiceTranscriptMu.Lock()
	items := slices.Clone(v.voiceTranscriptHistory)
	v.voiceTranscriptMu.Unlock()

	if len(items) == 0 {
		v.voiceTranscriptFeedLbl.SetText("No Whisper transcripts heard yet. Speak into your mic or run a test phrase below.")
		return
	}
	var sb strings.Builder
	for i, it := range items {
		if i > 0 {
			sb.WriteByte('\n')
		}
		ts := "--:--:--"
		if !it.Timestamp.IsZero() {
			ts = it.Timestamp.Local().Format("15:04:05")
		}
		var meta []string
		if lang := strings.TrimSpace(it.DetectedLang); lang != "" {
			meta = append(meta, lang)
		}
		if it.DecodeMs > 0 {
			meta = append(meta, fmt.Sprintf("%dms", it.DecodeMs))
		}
		metaStr := ""
		if len(meta) > 0 {
			metaStr = " (" + strings.Join(meta, ", ") + ")"
		}
		note := strings.TrimSpace(it.Note)
		if note != "" {
			note = " — " + note
		}
		fmt.Fprintf(&sb, "[%s] %q%s%s", ts, it.RawText, metaStr, note)
	}
	v.voiceTranscriptFeedLbl.SetText(sb.String())
	if v.voiceTranscriptScroll != nil {
		v.voiceTranscriptScroll.ScrollToBottom()
	}
}

func formatVoiceEvalSummary(ev bot.VoiceTranscriptEvent) string {
	var parts []string
	if strings.TrimSpace(ev.Normalized) != "" {
		parts = append(parts, fmt.Sprintf("Normalized: %q", ev.Normalized))
	}
	if strings.TrimSpace(ev.WakeWord) != "" {
		parts = append(parts, fmt.Sprintf("Wake (%s): %s", ev.WakeWord, ev.WakeState))
	}
	header := strings.Join(parts, " • ")
	var lines []string
	if ev.Note != "" {
		lines = append(lines, ev.Note)
	}
	if header != "" {
		lines = append(lines, header)
	}
	for _, m := range ev.Matches {
		state := "WOULD TRIGGER"
		switch {
		case m.Fired:
			state = "FIRED"
		case m.SkipReason != "" && m.SkipReason != "dry run":
			state = "SKIPPED (" + m.SkipReason + ")"
		}
		lines = append(lines, fmt.Sprintf("• %s [%s] → %s (%s)", m.CommandName, state, m.ActionName, botOptionLabel(botVoiceMatchModeOptions, m.MatchMode)))
	}
	return strings.Join(lines, "\n")
}

func formatVoiceResponseSummary(voiceID string, speed float32) string {
	clean := strings.TrimSpace(voiceID)
	if clean == "" {
		return "Global Default TTS Voice"
	}
	if speed > 0 {
		return FormatVoiceSpeedSummary(clean, speed)
	}
	return tts.VoiceDisplayName(clean)
}

func (v *BotTabView) refreshCommandsAndTriggers(b config.BotAutomationConfig) {
	if v.commandsBox != nil {
		rows := make([]fyne.CanvasObject, 0, max(1, len(b.Commands)))
		if len(b.Commands) == 0 {
			rows = append(rows, widget.NewLabel("No chat commands configured yet."))
		}
		for i, cmd := range b.Commands {
			idx := i
			c := cmd
			mode := botOptionLabel(botCommandMatchModeOptions, c.MatchMode)
			title := fmt.Sprintf("%s  [%s • %s]", c.Trigger, c.Name, mode)
			var details []string
			details = append(details, "Action: "+v.actionName(b, c.ActionID))
			if len(c.Aliases) > 0 {
				details = append(details, "Aliases: "+strings.Join(c.Aliases, ", "))
			}
			if c.ConsumeTTS {
				details = append(details, "Suppresses TTS")
			}
			if c.GlobalCooldownSec > 0 || c.UserCooldownSec > 0 {
				details = append(details, fmt.Sprintf("CD: %ds global / %ds user", c.GlobalCooldownSec, c.UserCooldownSec))
			}

			enabledChk := widget.NewCheck("Enabled", func(checked bool) {
				if v.initializing {
					return
				}
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Commands) {
						cfg.Commands[idx].Enabled = checked
					}
				})
			})
			v.initializing = true
			enabledChk.SetChecked(c.Enabled)
			v.initializing = false

			editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
				v.openCommandDialog(idx)
			})
			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Commands) {
						cfg.Commands = slices.Delete(cfg.Commands, idx, idx+1)
					}
				})
			})

			top := container.NewHBox(widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), layout.NewSpacer(), enabledChk, editBtn, delBtn)
			sub := widget.NewLabel(strings.Join(details, " • "))
			sub.Wrapping = fyne.TextWrapWord
			rows = append(rows, container.NewVBox(top, sub, widget.NewSeparator()))
		}
		v.commandsBox.Objects = rows
		v.commandsBox.Refresh()
	}

	if v.triggersBox != nil {
		rows := make([]fyne.CanvasObject, 0, max(1, len(b.Triggers)))
		if len(b.Triggers) == 0 {
			rows = append(rows, widget.NewLabel("No event triggers configured yet."))
		}
		for i, tr := range b.Triggers {
			idx := i
			t := tr
			eventLabel := t.EventKind
			if strings.EqualFold(strings.TrimSpace(t.Source), "plugin_trigger") {
				eventLabel = v.pluginTriggerName(t.EventKind)
			}
			lbl := fmt.Sprintf("%s  (%s: %s → %s)", t.Name, botOptionLabel(botTriggerSourceOptions, t.Source), eventLabel, v.actionName(b, t.ActionID))
			enabledChk := widget.NewCheck("Enabled", func(checked bool) {
				if v.initializing {
					return
				}
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Triggers) {
						cfg.Triggers[idx].Enabled = checked
					}
				})
			})
			v.initializing = true
			enabledChk.SetChecked(t.Enabled)
			v.initializing = false

			editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
				v.openTriggerDialog(idx)
			})
			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Triggers) {
						cfg.Triggers = slices.Delete(cfg.Triggers, idx, idx+1)
					}
				})
			})
			rows = append(rows, container.NewHBox(widget.NewLabel(lbl), layout.NewSpacer(), enabledChk, editBtn, delBtn))
		}
		v.triggersBox.Objects = rows
		v.triggersBox.Refresh()
	}
}

func (v *BotTabView) openCommandDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.Commands)
	var cur config.BotCommandDef
	if editing {
		cur = cfg.Commands[idx]
	} else {
		cur = config.BotCommandDef{ID: newBotItemID("cmd"), Enabled: true, MatchMode: "prefix"}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)
	trigEntry := widget.NewEntry()
	trigEntry.SetPlaceHolder("!so")
	trigEntry.SetText(cur.Trigger)
	aliasesEntry := widget.NewEntry()
	aliasesEntry.SetPlaceHolder("!shoutout, !sh")
	aliasesEntry.SetText(strings.Join(cur.Aliases, ", "))
	modeSelect := widget.NewSelect(botOptionLabels(botCommandMatchModeOptions), nil)
	modeSelect.SetSelected(botOptionLabel(botCommandMatchModeOptions, cur.MatchMode))
	caseChk := widget.NewCheck("Case Sensitive", nil)
	caseChk.SetChecked(cur.CaseSensitive)
	consumeChk := widget.NewCheck("Suppress TTS Speech (Consume Command)", nil)
	consumeChk.SetChecked(cur.ConsumeTTS)
	globalCDEntry := widget.NewEntry()
	globalCDEntry.SetText(strconv.Itoa(cur.GlobalCooldownSec))
	userCDEntry := widget.NewEntry()
	userCDEntry.SetText(strconv.Itoa(cur.UserCooldownSec))
	rolesEntry := widget.NewEntry()
	rolesEntry.SetPlaceHolder("broadcaster, mod, vip, subscriber (blank = everyone)")
	rolesEntry.SetText(strings.Join(cur.AllowedRoles, ", "))
	groupsEntry := widget.NewEntry()
	groupsEntry.SetPlaceHolder("regulars, team (optional)")
	groupsEntry.SetText(strings.Join(cur.AllowedGroups, ", "))
	platsEntry := widget.NewEntry()
	platsEntry.SetPlaceHolder("twitch, youtube, kick, tiktok (blank = all)")
	platsEntry.SetText(strings.Join(cur.Platforms, ", "))

	actLabels, labelToID, idToLabel := v.actionDisplayMap(cfg)
	actSelect := widget.NewSelect(actLabels, nil)
	if lbl, ok := idToLabel[cur.ActionID]; ok {
		actSelect.SetSelected(lbl)
	} else if len(actLabels) > 0 {
		actSelect.SetSelected(actLabels[0])
	}

	form := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("Trigger", trigEntry),
			widget.NewFormItem("Match Mode", modeSelect),
			widget.NewFormItem("Aliases", aliasesEntry),
			widget.NewFormItem("Action", actSelect),
			widget.NewFormItem("Global CD (s)", globalCDEntry),
			widget.NewFormItem("User CD (s)", userCDEntry),
			widget.NewFormItem("Allowed Roles", rolesEntry),
			widget.NewFormItem("Allowed Groups", groupsEntry),
			widget.NewFormItem("Platforms", platsEntry),
		),
		caseChk,
		consumeChk,
	)

	title := "Add Chat Command"
	if editing {
		title = "Edit Chat Command"
	}
	scrollForm := container.NewVScroll(form)
	scrollForm.SetMinSize(fyne.NewSize(460, 440))
	dialog.ShowCustomConfirm(title, "Save", "Cancel", scrollForm, func(ok bool) {
		if !ok {
			return
		}
		gcd, _ := strconv.Atoi(strings.TrimSpace(globalCDEntry.Text))
		ucd, _ := strconv.Atoi(strings.TrimSpace(userCDEntry.Text))
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = strings.TrimSpace(trigEntry.Text)
		}
		cur.Trigger = strings.TrimSpace(trigEntry.Text)
		cur.MatchMode = botOptionKey(botCommandMatchModeOptions, modeSelect.Selected)
		cur.Aliases = splitCommaTrim(aliasesEntry.Text)
		cur.CaseSensitive = caseChk.Checked
		cur.ConsumeTTS = consumeChk.Checked
		cur.GlobalCooldownSec = max(0, gcd)
		cur.UserCooldownSec = max(0, ucd)
		cur.AllowedRoles = splitCommaTrim(rolesEntry.Text)
		cur.AllowedGroups = splitCommaTrim(groupsEntry.Text)
		cur.Platforms = splitCommaTrim(platsEntry.Text)
		cur.ActionID = labelToID[actSelect.Selected]
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if editing && idx < len(b.Commands) {
				b.Commands[idx] = cur
			} else {
				b.Commands = append(b.Commands, cur)
			}
		})
	}, v.window)
}

func (v *BotTabView) openTriggerDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.Triggers)
	var cur config.BotTriggerDef
	if editing {
		cur = cfg.Triggers[idx]
	} else {
		cur = config.BotTriggerDef{ID: newBotItemID("trig"), Enabled: true, Source: "platform_event", EventKind: "follow"}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)
	sourceSelect := widget.NewSelect(botOptionLabels(botTriggerSourceOptions), nil)
	sourceSelect.SetSelected(botOptionLabel(botTriggerSourceOptions, cur.Source))
	kindEntry := widget.NewEntry()
	kindEntry.SetPlaceHolder("follow, sub, cheer, raid, redemption, or plugin/OBS event ID")
	kindEntry.SetText(cur.EventKind)

	// Plugin trigger quick-picker if plugins export custom triggers.
	var trigOpts []string
	trigOptToID := make(map[string]string)
	trigIDToOpt := make(map[string]string)
	if v.session != nil && v.session.PluginManager() != nil {
		for _, p := range v.session.PluginManager().Plugins() {
			for _, td := range p.Triggers {
				tName := strings.TrimSpace(td.GetName())
				if tName == "" {
					tName = td.GetId()
				}
				lbl := fmt.Sprintf("%s — %s", p.Name, tName)
				trigOpts = append(trigOpts, lbl)
				trigOptToID[lbl] = td.GetId()
				trigIDToOpt[td.GetId()] = lbl
			}
		}
	}
	pluginTrigPicker := widget.NewSelect(trigOpts, func(sel string) {
		if id, ok := trigOptToID[sel]; ok {
			kindEntry.SetText(id)
			sourceSelect.SetSelected(botOptionLabel(botTriggerSourceOptions, "plugin_trigger"))
		}
	})
	pluginTrigPicker.PlaceHolder = "Optional: pick exported plugin trigger..."
	if lbl, ok := trigIDToOpt[cur.EventKind]; ok {
		pluginTrigPicker.SetSelected(lbl)
	}
	if len(trigOpts) == 0 {
		pluginTrigPicker.Disable()
	}

	platEntry := widget.NewEntry()
	platEntry.SetPlaceHolder("blank = any")
	platEntry.SetText(cur.Platform)
	minAmtEntry := widget.NewEntry()
	if cur.MinAmount > 0 {
		minAmtEntry.SetText(strconv.FormatFloat(cur.MinAmount, 'f', -1, 64))
	}
	rewardEntry := widget.NewEntry()
	rewardEntry.SetText(cur.RewardFilter)
	cdEntry := widget.NewEntry()
	cdEntry.SetText(strconv.Itoa(cur.CooldownSec))

	actLabels, labelToID, idToLabel := v.actionDisplayMap(cfg)
	actSelect := widget.NewSelect(actLabels, nil)
	if lbl, ok := idToLabel[cur.ActionID]; ok {
		actSelect.SetSelected(lbl)
	} else if len(actLabels) > 0 {
		actSelect.SetSelected(actLabels[0])
	}

	form := container.NewVScroll(widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Source", sourceSelect),
		widget.NewFormItem("Plugin Trigger", pluginTrigPicker),
		widget.NewFormItem("Event Kind / ID", kindEntry),
		widget.NewFormItem("Platform", platEntry),
		widget.NewFormItem("Min Amount", minAmtEntry),
		widget.NewFormItem("Reward Filter", rewardEntry),
		widget.NewFormItem("Cooldown (s)", cdEntry),
		widget.NewFormItem("Action", actSelect),
	))
	form.SetMinSize(fyne.NewSize(460, 420))

	title := "Add Event Trigger"
	if editing {
		title = "Edit Event Trigger"
	}
	dialog.ShowCustomConfirm(title, "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		minAmt, _ := strconv.ParseFloat(strings.TrimSpace(minAmtEntry.Text), 64)
		cd, _ := strconv.Atoi(strings.TrimSpace(cdEntry.Text))
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = strings.TrimSpace(kindEntry.Text)
		}
		cur.Source = botOptionKey(botTriggerSourceOptions, sourceSelect.Selected)
		cur.EventKind = strings.TrimSpace(kindEntry.Text)
		cur.Platform = strings.TrimSpace(platEntry.Text)
		cur.MinAmount = minAmt
		cur.RewardFilter = strings.TrimSpace(rewardEntry.Text)
		cur.CooldownSec = max(0, cd)
		cur.ActionID = labelToID[actSelect.Selected]
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if editing && idx < len(b.Triggers) {
				b.Triggers[idx] = cur
			} else {
				b.Triggers = append(b.Triggers, cur)
			}
		})
	}, v.window)
}

// --- Sub-tab 2: Actions & Sub-Actions ---

func (v *BotTabView) buildActionsTab() fyne.CanvasObject {
	v.actionsBox = container.NewVBox()
	addActBtn := widget.NewButtonWithIcon("New Action", theme.ContentAddIcon(), func() {
		v.openActionDialog(-1)
	})
	addActBtn.Importance = widget.HighImportance

	card := widget.NewCard(
		"Actions & Sub-Action Chains",
		"Ordered workflows of TTS, chat messages, moderation, variables, HTTP, OBS, and WASM/Native plugin actions",
		container.NewVBox(container.NewHBox(addActBtn), v.actionsBox),
	)
	return newPanelVScroll(card)
}

func (v *BotTabView) refreshActions(b config.BotAutomationConfig) {
	if v.actionsBox == nil {
		return
	}
	rows := make([]fyne.CanvasObject, 0, max(1, len(b.Actions)))
	if len(b.Actions) == 0 {
		rows = append(rows, widget.NewLabel("No actions configured yet. Click 'New Action' to build a workflow."))
	}
	for i, act := range b.Actions {
		actIdx := i
		a := act
		qName := a.QueueID
		if qName == "" {
			qName = "default"
		}

		enabledChk := widget.NewCheck("Enabled", func(checked bool) {
			if v.initializing {
				return
			}
			_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
				if actIdx < len(cfg.Actions) {
					cfg.Actions[actIdx].Enabled = checked
				}
			})
		})
		v.initializing = true
		enabledChk.SetChecked(a.Enabled)
		v.initializing = false

		testBtn := widget.NewButtonWithIcon("Test Run", theme.MediaPlayIcon(), func() {
			v.selectedActionID = a.ID
			v.runSelectedActionTest()
		})
		addSubBtn := widget.NewButtonWithIcon("Add Step", theme.ContentAddIcon(), func() {
			v.openSubActionDialog(actIdx, -1)
		})
		editActBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
			v.openActionDialog(actIdx)
		})
		delActBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
				if actIdx < len(cfg.Actions) {
					cfg.Actions = slices.Delete(cfg.Actions, actIdx, actIdx+1)
				}
			})
		})

		subRows := make([]fyne.CanvasObject, 0, len(a.SubActions)+1)
		if len(a.SubActions) == 0 {
			subRows = append(subRows, widget.NewLabel("  (no sub-actions — click Add Step)"))
		}
		for sIdx, sa := range a.SubActions {
			stepIdx := sIdx
			step := sa
			upBtn := widget.NewButton("↑", func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if actIdx < len(cfg.Actions) && stepIdx > 0 && stepIdx < len(cfg.Actions[actIdx].SubActions) {
						sas := cfg.Actions[actIdx].SubActions
						sas[stepIdx-1], sas[stepIdx] = sas[stepIdx], sas[stepIdx-1]
					}
				})
			})
			downBtn := widget.NewButton("↓", func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if actIdx < len(cfg.Actions) && stepIdx+1 < len(cfg.Actions[actIdx].SubActions) {
						sas := cfg.Actions[actIdx].SubActions
						sas[stepIdx+1], sas[stepIdx] = sas[stepIdx], sas[stepIdx+1]
					}
				})
			})
			if stepIdx == 0 {
				upBtn.Disable()
			}
			if stepIdx+1 >= len(a.SubActions) {
				downBtn.Disable()
			}
			editStepBtn := widget.NewButton("Edit", func() {
				v.openSubActionDialog(actIdx, stepIdx)
			})
			delStepBtn := widget.NewButton("✕", func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if actIdx < len(cfg.Actions) && stepIdx < len(cfg.Actions[actIdx].SubActions) {
						cfg.Actions[actIdx].SubActions = slices.Delete(cfg.Actions[actIdx].SubActions, stepIdx, stepIdx+1)
					}
				})
			})

			lbl := widget.NewLabel(fmt.Sprintf("%d. %s", stepIdx+1, v.summarizeSubAction(b, step)))
			lbl.Wrapping = fyne.TextWrapWord
			subRows = append(subRows, container.NewHBox(lbl, layout.NewSpacer(), upBtn, downBtn, editStepBtn, delStepBtn))
		}

		hdr := container.NewHBox(
			widget.NewLabelWithStyle(fmt.Sprintf("%s  (Queue: %s)", a.Name, v.queueName(b, qName)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			layout.NewSpacer(),
			enabledChk,
			testBtn,
			addSubBtn,
			editActBtn,
			delActBtn,
		)
		cardBody := container.NewVBox(hdr, container.NewVBox(subRows...), widget.NewSeparator())
		rows = append(rows, cardBody)
	}
	v.actionsBox.Objects = rows
	v.actionsBox.Refresh()
}

func (v *BotTabView) summarizeSubAction(b config.BotAutomationConfig, sa config.BotSubActionDef) string {
	switch sa.Kind {
	case "speak_tts":
		if strings.TrimSpace(sa.VoiceID) != "" {
			return fmt.Sprintf("Speak TTS (%s): %q", tts.VoiceDisplayName(sa.VoiceID), sa.Text)
		}
		return fmt.Sprintf("Speak TTS: %q", sa.Text)
	case "send_chat":
		return fmt.Sprintf("Send Chat [%s]: %q", sa.Platform, sa.Text)
	case "play_sound":
		return "Play Sound: " + sa.SoundPath
	case "moderate":
		return fmt.Sprintf("Moderate %s: %s %s", sa.Platform, botOptionLabel(botModerationOpOptions, sa.Operation), sa.Target)
	case "set_var":
		return fmt.Sprintf("Set Var (%s) %s = %q", botOptionLabel(botVarScopeOptions, sa.Scope), sa.Key, sa.Value)
	case "inc_var":
		return fmt.Sprintf("Inc Var (%s) %s += %g", botOptionLabel(botVarScopeOptions, sa.Scope), sa.Key, sa.Delta)
	case "if_else":
		return fmt.Sprintf("If %q %s %q", sa.LHS, botOptionLabel(botConditionOperatorOptions, sa.Operator), sa.RHS)
	case "delay":
		return fmt.Sprintf("Wait %d ms", sa.DelayMs)
	case "http_request":
		return fmt.Sprintf("HTTP %s %s", sa.Method, sa.URL)
	case "run_action":
		return "Run Action → " + v.actionName(b, sa.TargetActionID)
	case "overlay_broadcast":
		return fmt.Sprintf("Overlay Event (%s): %s", v.pluginName(sa.PluginUUID), sa.EventName)
	case "obs":
		opLbl := botOptionLabel(botOBSOperationOptions, sa.OBSOperation)
		var details []string
		if s := strings.TrimSpace(sa.OBSScene); s != "" {
			details = append(details, "scene="+s)
		}
		if s := strings.TrimSpace(sa.OBSSource); s != "" {
			details = append(details, "src="+s)
		}
		if s := strings.TrimSpace(sa.OBSInput); s != "" {
			details = append(details, "input="+s)
		}
		if len(details) > 0 {
			return fmt.Sprintf("OBS: %s (%s)", opLbl, strings.Join(details, " "))
		}
		return "OBS: " + opLbl
	case "plugin_action":
		return fmt.Sprintf("Plugin Action: %s — %s", v.pluginName(sa.PluginUUID), v.pluginActionName(sa.PluginUUID, sa.PluginActionID))
	case "break":
		return "Stop Action (Break)"
	default:
		return botOptionLabel(botSubActionKindOptions, sa.Kind)
	}
}

func (v *BotTabView) openActionDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.Actions)
	var cur config.BotActionDef
	if editing {
		cur = cfg.Actions[idx]
	} else {
		cur = config.BotActionDef{ID: newBotItemID("act"), Enabled: true, QueueID: "default"}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)

	qLabels, qLabelToID, qIDToLabel := v.queueDisplayMap(cfg)
	qSelect := widget.NewSelect(qLabels, nil)
	if lbl, ok := qIDToLabel[cur.QueueID]; ok {
		qSelect.SetSelected(lbl)
	} else if len(qLabels) > 0 {
		qSelect.SetSelected(qLabels[0])
	}

	form := widget.NewForm(
		widget.NewFormItem("Action Name", nameEntry),
		widget.NewFormItem("Execution Queue", qSelect),
	)
	title := "New Bot Action"
	if editing {
		title = "Edit Bot Action"
	}
	dialog.ShowCustomConfirm(title, "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = "Untitled Action"
		}
		if qID, ok := qLabelToID[qSelect.Selected]; ok {
			cur.QueueID = qID
		}
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if editing && idx < len(b.Actions) {
				b.Actions[idx] = cur
			} else {
				b.Actions = append(b.Actions, cur)
			}
		})
	}, v.window)
}

func (v *BotTabView) openSubActionDialog(actIdx, stepIdx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	if actIdx < 0 || actIdx >= len(cfg.Actions) {
		return
	}
	editing := stepIdx >= 0 && stepIdx < len(cfg.Actions[actIdx].SubActions)
	var cur config.BotSubActionDef
	if editing {
		cur = cfg.Actions[actIdx].SubActions[stepIdx]
	} else {
		cur = config.BotSubActionDef{ID: newBotItemID("step"), Kind: "send_chat", Enabled: true}
	}

	kindSelect := widget.NewSelect(botOptionLabels(botSubActionKindOptions), nil)
	kindSelect.SetSelected(botOptionLabel(botSubActionKindOptions, cur.Kind))
	if kindSelect.Selected == "" {
		kindSelect.SetSelected(botOptionLabel(botSubActionKindOptions, "send_chat"))
	}

	textEntry := widget.NewMultiLineEntry()
	textEntry.SetMinRowsVisible(2)
	textEntry.SetText(cur.Text)

	soundPathEntry := widget.NewEntry()
	soundPathEntry.SetPlaceHolder("Path to .wav or .mp3 audio file")
	soundPathEntry.SetText(cur.SoundPath)

	eventNameEntry := widget.NewEntry()
	eventNameEntry.SetPlaceHolder("e.g. alert_triggered")
	eventNameEntry.SetText(cur.EventName)

	payloadEntry := widget.NewMultiLineEntry()
	payloadEntry.SetMinRowsVisible(2)
	payloadEntry.SetPlaceHolder(`e.g. {"user":"%user%"}`)
	payloadEntry.SetText(cur.Payload)

	selectedVoiceID := strings.TrimSpace(cur.VoiceID)
	selectedSpeed := cur.Speed
	voiceSummaryLbl := widget.NewLabel("")
	voiceSummaryLbl.Truncation = fyne.TextTruncateEllipsis
	updateVoiceSummary := func() {
		if selectedVoiceID == "" {
			if respVoice := strings.TrimSpace(v.currentConfig().VoiceDetection.ResponseVoiceID); respVoice != "" {
				voiceSummaryLbl.SetText("Default (Keyword: " + tts.VoiceDisplayName(respVoice) + ", else Global)")
			} else {
				voiceSummaryLbl.SetText("Global Default Voice")
			}
		} else {
			voiceSummaryLbl.SetText(FormatVoiceSpeedSummary(selectedVoiceID, selectedSpeed))
		}
		voiceSummaryLbl.Refresh()
	}
	updateVoiceSummary()

	chooseVoiceBtn := widget.NewButtonWithIcon("Choose Voice…", theme.VolumeUpIcon(), func() {
		initVoice := selectedVoiceID
		initSpeed := selectedSpeed
		if initSpeed <= 0 {
			initSpeed = 1.0
		}
		ShowVoiceSpeedDialog(v.window, v.session, "Select Sub-Action Voice", initVoice, initSpeed, func(newVoiceID string, newSpeed float32) {
			selectedVoiceID = newVoiceID
			selectedSpeed = newSpeed
			updateVoiceSummary()
		}, v.onStatus)
	})
	clearVoiceBtn := widget.NewButton("Use Default", func() {
		selectedVoiceID = ""
		selectedSpeed = 0
		updateVoiceSummary()
	})
	voicePickerRow := container.NewBorder(nil, nil, nil, container.NewHBox(chooseVoiceBtn, clearVoiceBtn), voiceSummaryLbl)

	platEntry := widget.NewEntry()
	platEntry.SetPlaceHolder("blank = triggering platform")
	platEntry.SetText(cur.Platform)
	announceChk := widget.NewCheck("Send as Twitch Announcement", nil)
	announceChk.SetChecked(cur.AsAnnouncement)
	sendAsOptions := []string{"Auto (Bot if connected)", "Bot Account", "Broadcaster Account"}
	sendAsSel := widget.NewSelect(sendAsOptions, nil)
	switch strings.ToLower(strings.TrimSpace(cur.SendAs)) {
	case "bot":
		sendAsSel.SetSelected("Bot Account")
	case "broadcaster", "main", "primary":
		sendAsSel.SetSelected("Broadcaster Account")
	default:
		sendAsSel.SetSelected("Auto (Bot if connected)")
	}

	opSelect := widget.NewSelect(botOptionLabels(botModerationOpOptions), nil)
	if cur.Operation != "" {
		opSelect.SetSelected(botOptionLabel(botModerationOpOptions, cur.Operation))
	} else {
		opSelect.SetSelected(botOptionLabel(botModerationOpOptions, "timeout"))
	}
	targetEntry := widget.NewEntry()
	targetEntry.SetText(cur.Target)
	durEntry := widget.NewEntry()
	durEntry.SetText(strconv.Itoa(cur.DurationSeconds))

	scopeSelect := widget.NewSelect(botOptionLabels(botVarScopeOptions), nil)
	scopeSelect.SetSelected(botOptionLabel(botVarScopeOptions, cur.Scope))
	keyEntry := widget.NewEntry()
	keyEntry.SetText(cur.Key)
	valEntry := widget.NewEntry()
	valEntry.SetText(cur.Value)
	varKindSelect := widget.NewSelect(botOptionLabels(botVarKindOptions), nil)
	varKindSelect.SetSelected(botOptionLabel(botVarKindOptions, cur.VarKind))
	deltaEntry := widget.NewEntry()
	if cur.Delta != 0 {
		deltaEntry.SetText(strconv.FormatFloat(cur.Delta, 'f', -1, 64))
	} else {
		deltaEntry.SetText("1")
	}

	delayEntry := widget.NewEntry()
	delayEntry.SetText(strconv.Itoa(cur.DelayMs))

	httpMethodSelect := widget.NewSelect([]string{"GET", "POST", "PUT", "PATCH"}, nil)
	if cur.Method != "" {
		httpMethodSelect.SetSelected(cur.Method)
	} else {
		httpMethodSelect.SetSelected("GET")
	}
	httpURLEntry := widget.NewEntry()
	httpURLEntry.SetText(cur.URL)
	httpBodyEntry := widget.NewMultiLineEntry()
	httpBodyEntry.SetText(cur.Body)
	httpSaveVarEntry := widget.NewEntry()
	httpSaveVarEntry.SetText(cur.SaveVar)

	obsOpSelect := widget.NewSelect(botOptionLabels(botOBSOperationOptions), nil)
	obsOpSelect.SetSelected(botOptionLabel(botOBSOperationOptions, cur.OBSOperation))
	obsSceneEntry := widget.NewEntry()
	obsSceneEntry.SetText(cur.OBSScene)
	obsSourceEntry := widget.NewEntry()
	obsSourceEntry.SetText(cur.OBSSource)
	obsVisibleChk := widget.NewCheck("Visible / Muted", nil)
	obsVisibleChk.SetChecked(cur.OBSVisible || cur.OBSMuted)
	obsInputEntry := widget.NewEntry()
	obsInputEntry.SetText(cur.OBSInput)

	// Cascading Plugin -> Action pickers (no raw UUIDs or Action IDs exposed in UI)
	pluginLabels, pluginLabelToUUID, pluginUUIDToLabel, pluginByUUID := v.pluginDisplayMap(cur.PluginUUID)
	selectedPluginUUID := strings.TrimSpace(cur.PluginUUID)
	selectedPluginActionID := strings.TrimSpace(cur.PluginActionID)

	var actLabelToID map[string]string
	var actDescByID map[string]string
	pluginActDescLbl := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	pluginActDescLbl.Wrapping = fyne.TextWrapWord

	pluginActSelect := widget.NewSelect(nil, func(sel string) {
		if id, ok := actLabelToID[sel]; ok {
			selectedPluginActionID = id
			pluginActDescLbl.SetText(actDescByID[id])
		} else {
			selectedPluginActionID = ""
			pluginActDescLbl.SetText("")
		}
	})
	pluginActSelect.PlaceHolder = "Select an action..."

	syncPluginActions := func(pluginUUID, preferredActionID string) {
		var pPtr *pluginhost.PluginStatus
		if st, ok := pluginByUUID[pluginUUID]; ok {
			pPtr = &st
		}
		var actLabels []string
		var actIDToLabel map[string]string
		actLabels, actLabelToID, actIDToLabel, actDescByID = pluginActionDisplayMap(pPtr, preferredActionID)
		pluginActSelect.Options = actLabels
		if len(actLabels) == 0 {
			pluginActSelect.ClearSelected()
			pluginActSelect.Disable()
			selectedPluginActionID = ""
			pluginActDescLbl.SetText("Selected plugin exports no actions.")
			pluginActSelect.Refresh()
			return
		}
		pluginActSelect.Enable()
		if lbl, ok := actIDToLabel[preferredActionID]; ok {
			pluginActSelect.SetSelected(lbl)
		} else {
			pluginActSelect.SetSelected(actLabels[0])
		}
		pluginActSelect.Refresh()
	}

	pluginSelect := widget.NewSelect(pluginLabels, func(sel string) {
		if uuid, ok := pluginLabelToUUID[sel]; ok {
			prevUUID := selectedPluginUUID
			selectedPluginUUID = uuid
			prefAct := ""
			if prevUUID == uuid {
				prefAct = selectedPluginActionID
			}
			syncPluginActions(uuid, prefAct)
		}
	})
	pluginSelect.PlaceHolder = "No plugins installed"
	if len(pluginLabels) == 0 {
		pluginSelect.Disable()
		pluginActSelect.Disable()
	} else {
		if lbl, ok := pluginUUIDToLabel[selectedPluginUUID]; ok {
			pluginSelect.SetSelected(lbl)
		} else {
			pluginSelect.SetSelected(pluginLabels[0])
		}
	}

	overlayPluginLabels, overlayPluginLabelToUUID, overlayPluginUUIDToLabel, _ := v.pluginDisplayMap(cur.PluginUUID)
	selectedOverlayPluginUUID := strings.TrimSpace(cur.PluginUUID)
	overlayPluginSelect := widget.NewSelect(overlayPluginLabels, func(sel string) {
		if uuid, ok := overlayPluginLabelToUUID[sel]; ok {
			selectedOverlayPluginUUID = uuid
		}
	})
	overlayPluginSelect.PlaceHolder = "No plugins installed"
	if len(overlayPluginLabels) == 0 {
		overlayPluginSelect.Disable()
	} else if lbl, ok := overlayPluginUUIDToLabel[selectedOverlayPluginUUID]; ok {
		overlayPluginSelect.SetSelected(lbl)
	} else {
		overlayPluginSelect.SetSelected(overlayPluginLabels[0])
	}

	targetActLabels, targetMap, targetRevMap := v.actionDisplayMap(cfg)
	targetActSelect := widget.NewSelect(targetActLabels, nil)
	if lbl, ok := targetRevMap[cur.TargetActionID]; ok {
		targetActSelect.SetSelected(lbl)
	} else if len(targetActLabels) > 0 {
		targetActSelect.SetSelected(targetActLabels[0])
	}

	ifLHSEntry := widget.NewEntry()
	ifLHSEntry.SetText(cur.LHS)
	ifOpSelect := widget.NewSelect(botOptionLabels(botConditionOperatorOptions), nil)
	ifOpSelect.SetSelected(botOptionLabel(botConditionOperatorOptions, cur.Operator))
	ifRHSEntry := widget.NewEntry()
	ifRHSEntry.SetText(cur.RHS)

	detailsBox := container.NewVBox()
	rebuildDetails := func(selectedLabel string) {
		k := botOptionKey(botSubActionKindOptions, selectedLabel)
		var items []*widget.FormItem
		switch k {
		case "send_chat":
			items = append(items,
				widget.NewFormItem("Platform", platEntry),
				widget.NewFormItem("Message Text", textEntry),
				widget.NewFormItem("Send As (Twitch)", sendAsSel),
				widget.NewFormItem("Options", announceChk),
			)
		case "speak_tts":
			items = append(items,
				widget.NewFormItem("Speech Text", textEntry),
				widget.NewFormItem("Voice & Speed", voicePickerRow),
			)
		case "play_sound":
			items = append(items, widget.NewFormItem("Audio File Path", soundPathEntry))
		case "moderate":
			items = append(items,
				widget.NewFormItem("Platform", platEntry),
				widget.NewFormItem("Operation", opSelect),
				widget.NewFormItem("Target User/Msg", targetEntry),
				widget.NewFormItem("Duration (s)", durEntry),
			)
		case "set_var":
			items = append(items,
				widget.NewFormItem("Scope", scopeSelect),
				widget.NewFormItem("Key", keyEntry),
				widget.NewFormItem("Type", varKindSelect),
				widget.NewFormItem("Value", valEntry),
			)
		case "inc_var":
			items = append(items,
				widget.NewFormItem("Scope", scopeSelect),
				widget.NewFormItem("Key", keyEntry),
				widget.NewFormItem("Delta", deltaEntry),
			)
		case "delay":
			items = append(items, widget.NewFormItem("Delay (ms)", delayEntry))
		case "http_request":
			items = append(items,
				widget.NewFormItem("Method", httpMethodSelect),
				widget.NewFormItem("URL", httpURLEntry),
				widget.NewFormItem("Body", httpBodyEntry),
				widget.NewFormItem("Save Response Var", httpSaveVarEntry),
			)
		case "obs":
			items = append(items,
				widget.NewFormItem("OBS Operation", obsOpSelect),
				widget.NewFormItem("Scene", obsSceneEntry),
				widget.NewFormItem("Source", obsSourceEntry),
				widget.NewFormItem("Input", obsInputEntry),
				widget.NewFormItem("State", obsVisibleChk),
			)
		case "plugin_action":
			items = append(items,
				widget.NewFormItem("Plugin", pluginSelect),
				widget.NewFormItem("Action", pluginActSelect),
				widget.NewFormItem("", pluginActDescLbl),
			)
		case "overlay_broadcast":
			items = append(items,
				widget.NewFormItem("Plugin", overlayPluginSelect),
				widget.NewFormItem("Event Name", eventNameEntry),
				widget.NewFormItem("JSON Payload", payloadEntry),
			)
		case "run_action":
			items = append(items, widget.NewFormItem("Target Action", targetActSelect))
		case "if_else":
			items = append(items,
				widget.NewFormItem("Left Value (e.g. %arg0%)", ifLHSEntry),
				widget.NewFormItem("Operator", ifOpSelect),
				widget.NewFormItem("Right Value", ifRHSEntry),
			)
		}
		if len(items) == 0 {
			detailsBox.Objects = []fyne.CanvasObject{widget.NewLabel("No extra parameters required — halts any remaining steps in this action.")}
		} else {
			detailsBox.Objects = []fyne.CanvasObject{widget.NewForm(items...)}
		}
		detailsBox.Refresh()
	}
	kindSelect.OnChanged = rebuildDetails
	rebuildDetails(kindSelect.Selected)

	body := container.NewVScroll(container.NewVBox(
		widget.NewForm(widget.NewFormItem("Step Kind", kindSelect)),
		detailsBox,
	))
	body.SetMinSize(fyne.NewSize(500, 420))

	title := "Add Sub-Action"
	if editing {
		title = "Edit Sub-Action"
	}
	dialog.ShowCustomConfirm(title, "Save", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		cur.Kind = botOptionKey(botSubActionKindOptions, kindSelect.Selected)
		cur.Platform = strings.TrimSpace(platEntry.Text)
		switch cur.Kind {
		case "play_sound":
			cur.SoundPath = strings.TrimSpace(soundPathEntry.Text)
		case "overlay_broadcast":
			cur.Payload = payloadEntry.Text
			cur.EventName = strings.TrimSpace(eventNameEntry.Text)
			cur.PluginUUID = selectedOverlayPluginUUID
		case "plugin_action":
			cur.PluginUUID = selectedPluginUUID
			cur.PluginActionID = selectedPluginActionID
		default:
			cur.Text = textEntry.Text
		}
		cur.VoiceID = selectedVoiceID
		cur.Speed = selectedSpeed
		cur.AsAnnouncement = announceChk.Checked
		switch sendAsSel.Selected {
		case "Bot Account":
			cur.SendAs = "bot"
		case "Broadcaster Account":
			cur.SendAs = "broadcaster"
		default:
			cur.SendAs = ""
		}
		cur.Operation = botOptionKey(botModerationOpOptions, opSelect.Selected)
		cur.Target = strings.TrimSpace(targetEntry.Text)
		cur.DurationSeconds, _ = strconv.Atoi(strings.TrimSpace(durEntry.Text))
		cur.Scope = botOptionKey(botVarScopeOptions, scopeSelect.Selected)
		cur.Key = strings.TrimSpace(keyEntry.Text)
		cur.VarKind = botOptionKey(botVarKindOptions, varKindSelect.Selected)
		cur.Value = valEntry.Text
		cur.Delta, _ = strconv.ParseFloat(strings.TrimSpace(deltaEntry.Text), 64)
		cur.DelayMs, _ = strconv.Atoi(strings.TrimSpace(delayEntry.Text))
		cur.Method = httpMethodSelect.Selected
		cur.URL = strings.TrimSpace(httpURLEntry.Text)
		cur.Body = httpBodyEntry.Text
		cur.SaveVar = strings.TrimSpace(httpSaveVarEntry.Text)
		cur.OBSOperation = botOptionKey(botOBSOperationOptions, obsOpSelect.Selected)
		cur.OBSScene = strings.TrimSpace(obsSceneEntry.Text)
		cur.OBSSource = strings.TrimSpace(obsSourceEntry.Text)
		cur.OBSInput = strings.TrimSpace(obsInputEntry.Text)
		cur.OBSVisible = obsVisibleChk.Checked
		cur.OBSMuted = obsVisibleChk.Checked
		cur.TargetActionID = targetMap[targetActSelect.Selected]
		cur.LHS = ifLHSEntry.Text
		cur.Operator = botOptionKey(botConditionOperatorOptions, ifOpSelect.Selected)
		cur.RHS = ifRHSEntry.Text

		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if actIdx >= len(b.Actions) {
				return
			}
			if editing && stepIdx < len(b.Actions[actIdx].SubActions) {
				b.Actions[actIdx].SubActions[stepIdx] = cur
			} else {
				b.Actions[actIdx].SubActions = append(b.Actions[actIdx].SubActions, cur)
			}
		})
	}, v.window)
}

// --- Sub-tab 3: Triggers & Timers ---

func (v *BotTabView) buildTriggersAndTimersTab() fyne.CanvasObject {
	v.triggersBox = container.NewVBox()
	v.timersBox = container.NewVBox()

	addTrigBtn := widget.NewButtonWithIcon("Add Trigger", theme.ContentAddIcon(), func() {
		v.openTriggerDialog(-1)
	})
	addTrigBtn.Importance = widget.HighImportance

	addTimerBtn := widget.NewButtonWithIcon("Add Timer", theme.ContentAddIcon(), func() {
		v.openTimerDialog(-1)
	})
	addTimerBtn.Importance = widget.HighImportance

	trigsCard := widget.NewCard(
		"Event Triggers",
		"React to stream events (follow/sub/cheer/raid/redemption), plugin triggers, or OBS events",
		container.NewVBox(container.NewHBox(addTrigBtn), v.triggersBox),
	)
	timersCard := widget.NewCard(
		"Interval Timers",
		"Fire actions on a fixed interval (optionally requiring live stream & minimum chat lines)",
		container.NewVBox(container.NewHBox(addTimerBtn), v.timersBox),
	)
	return newPanelVScroll(container.NewVBox(trigsCard, timersCard))
}

func (v *BotTabView) refreshTimersAndVoice(b config.BotAutomationConfig) {
	if v.timersBox != nil {
		rows := make([]fyne.CanvasObject, 0, max(1, len(b.Timers)))
		if len(b.Timers) == 0 {
			rows = append(rows, widget.NewLabel("No timers configured yet."))
		}
		for i, tm := range b.Timers {
			idx := i
			t := tm
			lbl := fmt.Sprintf("%s  (every %ds, minLines=%d, liveOnly=%v → %s)", t.Name, t.IntervalSeconds, t.MinChatLines, t.LiveOnly, v.actionName(b, t.ActionID))
			chk := widget.NewCheck("Enabled", func(checked bool) {
				if v.initializing {
					return
				}
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Timers) {
						cfg.Timers[idx].Enabled = checked
					}
				})
			})
			v.initializing = true
			chk.SetChecked(t.Enabled)
			v.initializing = false

			editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
				v.openTimerDialog(idx)
			})
			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Timers) {
						cfg.Timers = slices.Delete(cfg.Timers, idx, idx+1)
					}
				})
			})
			rows = append(rows, container.NewHBox(widget.NewLabel(lbl), layout.NewSpacer(), chk, editBtn, delBtn))
		}
		v.timersBox.Objects = rows
		v.timersBox.Refresh()
	}

	if v.voiceCmdsBox != nil {
		fullCfg := v.currentConfig()
		vdOn := fullCfg.EffectiveVoiceDetectionEnabled()
		wakeWord := strings.TrimSpace(fullCfg.VoiceDetection.WakeWord)
		if v.voiceWakeWordEntry != nil && v.voiceWakeWordEntry.Text != fullCfg.VoiceDetection.WakeWord {
			prevInit := v.initializing
			v.initializing = true
			v.voiceWakeWordEntry.SetText(fullCfg.VoiceDetection.WakeWord)
			v.initializing = prevInit
		}
		if v.voiceWakeWindowSelect != nil {
			prevInit := v.initializing
			v.initializing = true
			v.voiceWakeWindowSelect.SetSelected(formatWakeWindowOption(fullCfg.VoiceDetection.EffectiveWakeWindowSec()))
			v.initializing = prevInit
		}
		if v.voiceWakeSoundCheck != nil {
			prevInit := v.initializing
			v.initializing = true
			v.voiceWakeSoundCheck.SetChecked(fullCfg.VoiceDetection.EffectiveWakeSoundEnabled())
			v.initializing = prevInit
		}
		if v.voiceResponseLbl != nil {
			v.voiceResponseLbl.SetText(formatVoiceResponseSummary(fullCfg.VoiceDetection.ResponseVoiceID, fullCfg.VoiceDetection.ResponseSpeed))
		}
		if v.voiceMonitorStatusLbl != nil {
			if vdOn {
				if !strings.HasPrefix(v.voiceMonitorStatusLbl.Text, "🎙") && !strings.HasPrefix(v.voiceMonitorStatusLbl.Text, "⏳") && !strings.HasPrefix(v.voiceMonitorStatusLbl.Text, "⚠") {
					v.voiceMonitorStatusLbl.SetText("🟢 Listening")
				}
			} else {
				v.voiceMonitorStatusLbl.SetText("⚪ Voice Detection Off")
			}
		}
		rows := make([]fyne.CanvasObject, 0, max(1, len(b.VoiceCommands)+1))
		if !vdOn && len(b.VoiceCommands) > 0 {
			enableVDBtn := widget.NewButton("Enable Voice Detection", func() {
				if v.session != nil {
					_ = v.session.UpdateConfig(func(c *config.Config) {
						c.SetVoiceDetectionEnabled(true)
					})
				}
				v.Refresh()
				if v.onConfigChanged != nil {
					v.onConfigChanged()
				}
				v.reportStatus("✓ Voice Detection enabled for Bot Voice Commands.")
			})
			enableVDBtn.Importance = widget.HighImportance
			banner := container.NewHBox(
				widget.NewLabelWithStyle("⚪ Voice Detection is disabled — voice commands are paused.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
				layout.NewSpacer(),
				enableVDBtn,
			)
			rows = append(rows, banner, widget.NewSeparator())
		}
		if len(b.VoiceCommands) == 0 {
			rows = append(rows, widget.NewLabel("No voice commands configured yet."))
		}
		for i, vc := range b.VoiceCommands {
			idx := i
			item := vc
			pausedSuffix := ""
			if item.Enabled && !vdOn {
				pausedSuffix = " [Paused — Voice Detection off]"
			}
			wakeSuffix := ""
			if wakeWord != "" {
				if item.IgnoreWakeWord {
					wakeSuffix = " [Always listens]"
				} else {
					wakeSuffix = " [Wake: " + wakeWord + "]"
				}
			}
			lbl := fmt.Sprintf("%s  (%q [%s], cd=%ds → %s)%s%s", item.Name, item.Phrase, botOptionLabel(botVoiceMatchModeOptions, item.MatchMode), item.CooldownSec, v.actionName(b, item.ActionID), wakeSuffix, pausedSuffix)
			chk := widget.NewCheck("Enabled", func(checked bool) {
				if v.initializing {
					return
				}
				if v.session != nil {
					_ = v.session.UpdateConfig(func(c *config.Config) {
						if idx < len(c.Bot.VoiceCommands) {
							c.Bot.VoiceCommands[idx].Enabled = checked
							if checked && strings.TrimSpace(c.Bot.VoiceCommands[idx].Phrase) != "" {
								c.SetVoiceDetectionEnabled(true)
							}
						}
						config.NormalizeBotAutomationConfig(&c.Bot)
					})
					v.Refresh()
					if v.onConfigChanged != nil {
						v.onConfigChanged()
					}
				}
			})
			v.initializing = true
			chk.SetChecked(item.Enabled)
			v.initializing = false

			editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
				v.openVoiceCmdDialog(idx)
			})
			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.VoiceCommands) {
						cfg.VoiceCommands = slices.Delete(cfg.VoiceCommands, idx, idx+1)
					}
				})
			})
			rows = append(rows, container.NewHBox(widget.NewLabel(lbl), layout.NewSpacer(), chk, editBtn, delBtn))
		}
		v.voiceCmdsBox.Objects = rows
		v.voiceCmdsBox.Refresh()
	}
}

func (v *BotTabView) openTimerDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.Timers)
	var cur config.BotTimerDef
	if editing {
		cur = cfg.Timers[idx]
	} else {
		cur = config.BotTimerDef{ID: newBotItemID("tm"), Enabled: true, IntervalSeconds: 300, LiveOnly: true}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)
	intEntry := widget.NewEntry()
	intEntry.SetText(strconv.Itoa(cur.IntervalSeconds))
	linesEntry := widget.NewEntry()
	linesEntry.SetText(strconv.Itoa(cur.MinChatLines))
	liveChk := widget.NewCheck("Only run while stream is live", nil)
	liveChk.SetChecked(cur.LiveOnly)

	actLabels, labelToID, idToLabel := v.actionDisplayMap(cfg)
	actSelect := widget.NewSelect(actLabels, nil)
	if lbl, ok := idToLabel[cur.ActionID]; ok {
		actSelect.SetSelected(lbl)
	} else if len(actLabels) > 0 {
		actSelect.SetSelected(actLabels[0])
	}

	form := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("Interval (sec)", intEntry),
			widget.NewFormItem("Min Chat Lines", linesEntry),
			widget.NewFormItem("Action", actSelect),
		),
		liveChk,
	)
	dialog.ShowCustomConfirm("Timer", "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		sec, _ := strconv.Atoi(strings.TrimSpace(intEntry.Text))
		lines, _ := strconv.Atoi(strings.TrimSpace(linesEntry.Text))
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = "Timer"
		}
		cur.IntervalSeconds = max(1, sec)
		cur.MinChatLines = max(0, lines)
		cur.LiveOnly = liveChk.Checked
		cur.ActionID = labelToID[actSelect.Selected]
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if editing && idx < len(b.Timers) {
				b.Timers[idx] = cur
			} else {
				b.Timers = append(b.Timers, cur)
			}
		})
	}, v.window)
}

func (v *BotTabView) openVoiceCmdDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.VoiceCommands)
	var cur config.BotVoiceCommandDef
	if editing {
		cur = cfg.VoiceCommands[idx]
	} else {
		cur = config.BotVoiceCommandDef{ID: newBotItemID("vc"), Enabled: true, MatchMode: "contains"}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)
	phraseEntry := widget.NewEntry()
	phraseEntry.SetText(cur.Phrase)
	modeSelect := widget.NewSelect(botOptionLabels(botVoiceMatchModeOptions), nil)
	modeSelect.SetSelected(botOptionLabel(botVoiceMatchModeOptions, cur.MatchMode))
	cdEntry := widget.NewEntry()
	cdEntry.SetText(strconv.Itoa(cur.CooldownSec))

	actLabels, labelToID, idToLabel := v.actionDisplayMap(cfg)
	actSelect := widget.NewSelect(actLabels, nil)
	if lbl, ok := idToLabel[cur.ActionID]; ok {
		actSelect.SetSelected(lbl)
	} else if len(actLabels) > 0 {
		actSelect.SetSelected(actLabels[0])
	}

	ignoreWakeChk := widget.NewCheck("Always listen (bypass global wake word)", nil)
	ignoreWakeChk.SetChecked(cur.IgnoreWakeWord)
	globalWake := strings.TrimSpace(v.currentConfig().VoiceDetection.WakeWord)
	wakeHintText := "No global wake word configured (all enabled phrases match directly)."
	if globalWake != "" {
		wakeHintText = fmt.Sprintf("Global wake word is %q — this command waits for it unless bypassed.", globalWake)
	}
	wakeHint := widget.NewLabelWithStyle(wakeHintText, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	wakeHint.Wrapping = fyne.TextWrapWord

	form := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("Phrase", phraseEntry),
			widget.NewFormItem("Match Mode", modeSelect),
			widget.NewFormItem("Cooldown (s)", cdEntry),
			widget.NewFormItem("Action", actSelect),
		),
		ignoreWakeChk,
		wakeHint,
	)
	dialog.ShowCustomConfirm("Whisper Voice Command", "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		cd, _ := strconv.Atoi(strings.TrimSpace(cdEntry.Text))
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = strings.TrimSpace(phraseEntry.Text)
		}
		cur.Phrase = strings.TrimSpace(phraseEntry.Text)
		cur.MatchMode = botOptionKey(botVoiceMatchModeOptions, modeSelect.Selected)
		cur.IgnoreWakeWord = ignoreWakeChk.Checked
		cur.CooldownSec = max(0, cd)
		cur.ActionID = labelToID[actSelect.Selected]
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				if editing && idx < len(c.Bot.VoiceCommands) {
					c.Bot.VoiceCommands[idx] = cur
				} else {
					c.Bot.VoiceCommands = append(c.Bot.VoiceCommands, cur)
				}
				if cur.Enabled && strings.TrimSpace(cur.Phrase) != "" {
					c.SetVoiceDetectionEnabled(true)
				}
				config.NormalizeBotAutomationConfig(&c.Bot)
			})
			v.Refresh()
			if v.onConfigChanged != nil {
				v.onConfigChanged()
			}
		}
	}, v.window)
}

// --- Sub-tab 4: Queues & Variables ---

func (v *BotTabView) buildQueuesAndVarsTab() fyne.CanvasObject {
	v.queuesBox = container.NewVBox()
	v.varsBox = container.NewVBox()
	v.groupsBox = container.NewVBox()

	addQBtn := widget.NewButtonWithIcon("Add Queue", theme.ContentAddIcon(), func() {
		v.openQueueDialog(-1)
	})
	clearQBtn := widget.NewButtonWithIcon("Clear All Queues", theme.DeleteIcon(), func() {
		if v.session != nil && v.session.BotEngine() != nil {
			v.session.BotEngine().ClearQueues()
			v.reportStatus("✓ Cleared all bot execution queues.")
			v.Refresh()
		}
	})

	queuesCard := widget.NewCard(
		"Execution Queues",
		"Blocking (FIFO) and Concurrent workers executing actions safely without stalling TTS",
		container.NewVBox(container.NewHBox(addQBtn, clearQBtn), v.queuesBox),
	)

	varScopeSelect := widget.NewSelect(botOptionLabels(botGlobalVarScopeOptions), nil)
	varScopeSelect.SetSelected(botOptionLabel(botGlobalVarScopeOptions, "global_persistent"))
	varKeyEntry := NewScrollPassEntry()
	varKeyEntry.SetPlaceHolder("key")
	varKindSelect := widget.NewSelect(botOptionLabels(botVarKindOptions), nil)
	varKindSelect.SetSelected(botOptionLabel(botVarKindOptions, "string"))
	varValEntry := NewScrollPassEntry()
	varValEntry.SetPlaceHolder("value")
	setVarBtn := widget.NewButton("Set Variable", func() {
		st := v.botStore()
		if st == nil {
			return
		}
		ephem := botOptionKey(botGlobalVarScopeOptions, varScopeSelect.Selected) == "global_ephemeral"
		kindKey := botOptionKey(botVarKindOptions, varKindSelect.Selected)
		if err := st.SetGlobalVar(varKeyEntry.Text, kindKey, varValEntry.Text, ephem); err != nil {
			v.reportStatus("Set variable failed: " + err.Error())
			return
		}
		varKeyEntry.SetText("")
		varValEntry.SetText("")
		v.Refresh()
	})
	varsCard := widget.NewCard(
		"Variables Inspector",
		"Global persistent/ephemeral & per-user variables persisted in bot_state.db",
		container.NewVBox(
			container.NewGridWithColumns(5, varScopeSelect, varKeyEntry, varKindSelect, varValEntry, setVarBtn),
			v.varsBox,
		),
	)

	grpNameEntry := NewScrollPassEntry()
	grpNameEntry.SetPlaceHolder("group (e.g. regulars)")
	grpPlatEntry := NewScrollPassEntry()
	grpPlatEntry.SetText("twitch")
	grpUserIDEntry := NewScrollPassEntry()
	grpUserIDEntry.SetPlaceHolder("user ID")
	grpUserNameEntry := NewScrollPassEntry()
	grpUserNameEntry.SetPlaceHolder("username")
	addGrpBtn := widget.NewButton("Add Member", func() {
		st := v.botStore()
		if st == nil {
			return
		}
		if err := st.AddUserToGroup(grpNameEntry.Text, grpPlatEntry.Text, grpUserIDEntry.Text, grpUserNameEntry.Text); err != nil {
			v.reportStatus("Add member failed: " + err.Error())
			return
		}
		grpUserIDEntry.SetText("")
		grpUserNameEntry.SetText("")
		v.Refresh()
	})
	groupsCard := widget.NewCard(
		"Permission Groups",
		"Named user groups referenced by chat command AllowedGroups",
		container.NewVBox(
			container.NewGridWithColumns(5, grpNameEntry, grpPlatEntry, grpUserIDEntry, grpUserNameEntry, addGrpBtn),
			v.groupsBox,
		),
	)

	return newPanelVScroll(container.NewVBox(queuesCard, varsCard, groupsCard))
}

func (v *BotTabView) botStore() *bot.Store {
	if v.session == nil || v.session.BotEngine() == nil {
		return nil
	}
	return v.session.BotEngine().Store()
}

func (v *BotTabView) refreshQueuesAndVars(b config.BotAutomationConfig) {
	if v.queuesBox != nil {
		statsByID := make(map[string]bot.QueueStat)
		if v.session != nil && v.session.BotEngine() != nil {
			for _, st := range v.session.BotEngine().QueueStats() {
				statsByID[st.ID] = st
			}
		}
		rows := make([]fyne.CanvasObject, 0, len(b.Queues))
		for i, q := range b.Queues {
			idx := i
			st := statsByID[q.ID]
			mode := "Concurrent"
			if q.Blocking {
				mode = "Blocking"
			}
			lbl := widget.NewLabel(fmt.Sprintf("%s • %s • Max=%d • Running=%d Pending=%d", v.queueName(b, q.ID), mode, q.MaxPending, st.Running, st.Pending))
			editBtn := widget.NewButton("Edit", func() {
				v.openQueueDialog(idx)
			})
			delBtn := widget.NewButton("Delete", func() {
				_ = v.updateBotConfig(func(cfg *config.BotAutomationConfig) {
					if idx < len(cfg.Queues) {
						cfg.Queues = slices.Delete(cfg.Queues, idx, idx+1)
					}
				})
			})
			if q.ID == "default" || q.ID == "concurrent" {
				delBtn.Disable()
			}
			rows = append(rows, container.NewHBox(lbl, layout.NewSpacer(), editBtn, delBtn))
		}
		v.queuesBox.Objects = rows
		v.queuesBox.Refresh()
	}

	st := v.botStore()
	if v.varsBox != nil {
		if st == nil {
			v.varsBox.Objects = []fyne.CanvasObject{widget.NewLabel("Bot variable store unavailable.")}
		} else {
			var rows []fyne.CanvasObject
			for _, ephem := range []bool{false, true} {
				scopeLbl := botOptionLabel(botVarScopeOptions, "global_persistent")
				if ephem {
					scopeLbl = botOptionLabel(botVarScopeOptions, "global_ephemeral")
				}
				for _, ve := range st.ListGlobalVars(ephem) {
					key := ve.Key
					isE := ephem
					rm := widget.NewButton("", func() {
						_ = st.DeleteGlobalVar(key, isE)
						v.Refresh()
					})
					rm.SetIcon(theme.DeleteIcon())
					rows = append(rows, container.NewHBox(
						widget.NewLabel(fmt.Sprintf("[%s] %s (%s) = %q", scopeLbl, ve.Key, botOptionLabel(botVarKindOptions, ve.Kind), ve.Value)),
						layout.NewSpacer(),
						rm,
					))
				}
			}
			for _, uv := range st.ListUserVars("", "") {
				plat, uid, key := uv.Platform, uv.UserID, uv.Key
				rm := widget.NewButton("", func() {
					_ = st.DeleteUserVar(plat, uid, key)
					v.Refresh()
				})
				rm.SetIcon(theme.DeleteIcon())
				rows = append(rows, container.NewHBox(
					widget.NewLabel(fmt.Sprintf("[user:%s/%s] %s (%s) = %q", uv.Platform, uv.UserID, uv.Key, uv.Kind, uv.Value)),
					layout.NewSpacer(),
					rm,
				))
			}
			if len(rows) == 0 {
				rows = append(rows, widget.NewLabel("No variables stored yet."))
			}
			v.varsBox.Objects = rows
		}
		v.varsBox.Refresh()
	}

	if v.groupsBox != nil {
		if st == nil {
			v.groupsBox.Objects = []fyne.CanvasObject{widget.NewLabel("Bot store unavailable.")}
		} else {
			var rows []fyne.CanvasObject
			for _, grp := range st.ListGroups() {
				gName := grp
				for _, mem := range st.ListGroupMembers(gName) {
					m := mem
					rm := widget.NewButton("", func() {
						_ = st.RemoveUserFromGroup(gName, m.Platform, m.UserID)
						v.Refresh()
					})
					rm.SetIcon(theme.DeleteIcon())
					rows = append(rows, container.NewHBox(
						widget.NewLabel(fmt.Sprintf("[%s] %s (%s:%s)", gName, m.UserName, m.Platform, m.UserID)),
						layout.NewSpacer(),
						rm,
					))
				}
			}
			if len(rows) == 0 {
				rows = append(rows, widget.NewLabel("No group members stored yet."))
			}
			v.groupsBox.Objects = rows
		}
		v.groupsBox.Refresh()
	}
}

func (v *BotTabView) openQueueDialog(idx int) {
	if v.window == nil {
		return
	}
	cfg := v.currentConfig().Bot
	editing := idx >= 0 && idx < len(cfg.Queues)
	var cur config.BotQueueDef
	if editing {
		cur = cfg.Queues[idx]
	} else {
		cur = config.BotQueueDef{ID: newBotItemID("q"), Blocking: true, MaxPending: 64}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(cur.Name)
	maxEntry := widget.NewEntry()
	maxEntry.SetText(strconv.Itoa(cur.MaxPending))
	blockChk := widget.NewCheck("Blocking (run one action at a time in FIFO order)", nil)
	blockChk.SetChecked(cur.Blocking)

	form := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Display Name", nameEntry),
			widget.NewFormItem("Max Pending", maxEntry),
		),
		blockChk,
	)
	dialog.ShowCustomConfirm("Execution Queue", "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		mp, _ := strconv.Atoi(strings.TrimSpace(maxEntry.Text))
		cur.Name = strings.TrimSpace(nameEntry.Text)
		if cur.Name == "" {
			cur.Name = "Custom Queue"
		}
		cur.MaxPending = max(1, mp)
		cur.Blocking = blockChk.Checked
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			if editing && idx < len(b.Queues) {
				b.Queues[idx] = cur
			} else {
				b.Queues = append(b.Queues, cur)
			}
		})
	}, v.window)
}

// --- Sub-tab 5: OBS Studio WebSocket v5 ---

func (v *BotTabView) buildOBSTab() fyne.CanvasObject {
	v.obsEnabledCheck = widget.NewCheck("Enable OBS Studio WebSocket v5 Integration", func(checked bool) {
		if v.initializing {
			return
		}
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			b.OBS.Enabled = checked
		})
	})

	v.obsAutoReconnChk = widget.NewCheck("Auto-Connect & Reconnect Automatically", func(checked bool) {
		if v.initializing {
			return
		}
		_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
			b.OBS.AutoReconnect = checked
		})
	})

	v.obsHostEntry = NewScrollPassEntry()
	v.obsHostEntry.SetPlaceHolder(config.DefaultOBSHost)
	v.obsPortEntry = NewScrollPassEntry()
	v.obsPortEntry.SetPlaceHolder(strconv.Itoa(config.DefaultOBSPort))
	v.obsPasswordEntry = widget.NewPasswordEntry()
	v.obsPasswordEntry.SetPlaceHolder("Stored encrypted in OS Keyring (never config.json)")

	v.obsConnectBtn = widget.NewButtonWithIcon("Save & Connect OBS", theme.LoginIcon(), v.saveAndConnectOBS)
	v.obsConnectBtn.Importance = widget.HighImportance
	v.obsDisconnectBtn = widget.NewButtonWithIcon("Disconnect", theme.LogoutIcon(), func() {
		if v.session != nil && v.session.BotEngine() != nil && v.session.BotEngine().OBSClient() != nil {
			v.session.BotEngine().OBSClient().Disconnect()
			v.reportStatus("OBS WebSocket disconnected.")
			v.Refresh()
		}
	})
	v.obsTestBtn = widget.NewButtonWithIcon("Test Connection", theme.ConfirmIcon(), v.testOBSConnection)

	form := widget.NewForm(
		widget.NewFormItem("OBS Host", v.obsHostEntry),
		widget.NewFormItem("OBS Port", v.obsPortEntry),
		widget.NewFormItem("WebSocket Password", v.obsPasswordEntry),
	)

	card := widget.NewCard(
		"OBS Studio WebSocket v5",
		"Configure obs-websocket (Tools → WebSocket Server Settings in OBS Studio v28+)",
		container.NewVBox(
			v.obsStatusLbl,
			v.obsEnabledCheck,
			v.obsAutoReconnChk,
			form,
			container.NewHBox(v.obsConnectBtn, v.obsDisconnectBtn, v.obsTestBtn),
		),
	)
	return newPanelVScroll(card)
}

func (v *BotTabView) runSelectedActionTest() {
	if v.selectedActionID == "" {
		v.reportStatus("Select an action first.")
		return
	}
	if v.session == nil || v.session.BotEngine() == nil {
		v.reportStatus("Bot engine unavailable")
		return
	}
	args := map[string]string{
		"platform":    "twitch",
		"user":        "tester",
		"displayName": "tester",
		"message":     "test run",
		"arg0":        "test",
	}
	if err := v.session.BotEngine().RunActionByID(v.selectedActionID, args, false); err != nil {
		v.reportStatus("Test Run failed: " + err.Error())
		return
	}
	v.reportStatus("✓ Executed Test Run: " + v.actionName(v.currentConfig().Bot, v.selectedActionID))
}

func (v *BotTabView) syncOBSFieldsFromConfig(b config.BotAutomationConfig) {
	v.initializing = true
	defer func() { v.initializing = false }()

	if v.obsEnabledCheck != nil && v.obsEnabledCheck.Checked != b.OBS.Enabled {
		v.obsEnabledCheck.SetChecked(b.OBS.Enabled)
	}
	if v.obsAutoReconnChk != nil && v.obsAutoReconnChk.Checked != b.OBS.AutoReconnect {
		v.obsAutoReconnChk.SetChecked(b.OBS.AutoReconnect)
	}
	if v.obsHostEntry != nil {
		host := b.OBS.Host
		if host == "" {
			host = config.DefaultOBSHost
		}
		if v.obsHostEntry.Text != host {
			v.obsHostEntry.SetText(host)
		}
	}
	if v.obsPortEntry != nil {
		port := b.OBS.Port
		if port <= 0 {
			port = config.DefaultOBSPort
		}
		portStr := strconv.Itoa(port)
		if v.obsPortEntry.Text != portStr {
			v.obsPortEntry.SetText(portStr)
		}
	}
	if v.obsPasswordEntry != nil && v.session != nil && v.session.Secrets() != nil {
		pw, _ := v.session.Secrets().Get(config.SecretOBSPassword)
		if v.obsPasswordEntry.Text != pw {
			v.obsPasswordEntry.SetText(pw)
		}
	}
}

func (v *BotTabView) saveOBSSettings() (host string, port int, pw string) {
	host = strings.TrimSpace(v.obsHostEntry.Text)
	if host == "" {
		host = config.DefaultOBSHost
	}
	port, _ = strconv.Atoi(strings.TrimSpace(v.obsPortEntry.Text))
	if port <= 0 || port > 65535 {
		port = config.DefaultOBSPort
	}
	pw = v.obsPasswordEntry.Text
	if v.session != nil && v.session.Secrets() != nil {
		if pw == "" {
			_ = v.session.Secrets().Delete(config.SecretOBSPassword)
		} else {
			_ = v.session.Secrets().Set(config.SecretOBSPassword, pw)
		}
	}
	autoConn := v.obsAutoReconnChk != nil && v.obsAutoReconnChk.Checked
	_ = v.updateBotConfig(func(b *config.BotAutomationConfig) {
		b.OBS.Enabled = true
		b.OBS.Host = host
		b.OBS.Port = port
		b.OBS.AutoReconnect = autoConn
	})
	return host, port, pw
}

func (v *BotTabView) saveAndConnectOBS() {
	host, port, pw := v.saveOBSSettings()
	if v.session == nil || v.session.BotEngine() == nil || v.session.BotEngine().OBSClient() == nil {
		v.reportStatus("OBS settings saved.")
		return
	}
	client := v.session.BotEngine().OBSClient()
	if v.obsAutoReconnChk != nil && v.obsAutoReconnChk.Checked {
		client.StartAutoReconnect(host, port, pw)
		v.reportStatus(fmt.Sprintf("Connecting to OBS WebSocket at %s:%d...", host, port))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		err := client.Connect(ctx, host, port, pw)
		fyne.Do(func() {
			if err != nil {
				v.reportStatus("OBS connection failed: " + err.Error())
			} else {
				v.reportStatus("✓ Connected to OBS WebSocket.")
			}
			v.Refresh()
		})
	}()
}

func (v *BotTabView) testOBSConnection() {
	host, port, pw := v.saveOBSSettings()
	if v.session == nil || v.session.BotEngine() == nil || v.session.BotEngine().OBSClient() == nil {
		v.reportStatus("OBS client unavailable.")
		return
	}
	client := v.session.BotEngine().OBSClient()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if !client.IsConnected() {
			if err := client.Connect(ctx, host, port, pw); err != nil {
				fyne.Do(func() {
					v.reportStatus("OBS test failed: " + err.Error())
					v.Refresh()
				})
				return
			}
		}
		_, err := client.Call(ctx, "GetVersion", nil)
		fyne.Do(func() {
			if err != nil {
				v.reportStatus("OBS GetVersion failed: " + err.Error())
			} else {
				st := client.Status()
				v.reportStatus(fmt.Sprintf("✓ OBS Studio %s responding at %s", st.OBSVersion, st.Address))
			}
			v.Refresh()
		})
	}()
}

func splitCommaTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
