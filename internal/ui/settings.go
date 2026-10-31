package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// SettingsView provides the UI controls for audio, TTS, and global rules configuration.
type SettingsView struct {
	window   fyne.Window
	cfg      *config.Config
	player   audio.Player
	engine   tts.Engine
	session  *app.Session
	modelDir string

	mu                sync.Mutex
	reloadSeq         uint64
	deviceEnum        func() ([]audio.DeviceInfo, error)
	statusLabel       *widget.Label
	onStatus                  func(msg string)
	onHotkeyChanged           func(hotkey string) error
	onModeToggleHotkeyChanged func(hotkey string) error
	onTTSOutputModeChanged    func(mode string)
	onServicesChanged         func()
	onModelsChanged           func()
	onAssignVoice             func(platform, username string)
	subTabs           *container.AppTabs
	generalTabItem    *container.TabItem
	ttsTabItem        *container.TabItem
	rulesTabItem      *container.TabItem
	logsTabItem       *container.TabItem
	twitchTab         *TwitchTabView
	youtubeTab        *YouTubeTabView
	tiktokTab         *TikTokTabView
	kickTab           *KickTabView
	twitchSettingsItem  *container.TabItem
	youtubeSettingsItem *container.TabItem
	tiktokSettingsItem  *container.TabItem
	kickSettingsItem    *container.TabItem
	logsView          *LogsView
	twitchCheck       *widget.Check
	youtubeCheck      *widget.Check
	tiktokCheck       *widget.Check
	kickCheck         *widget.Check
	modelStatus       *widget.Label
	deviceSelect      *widget.Select
	deviceMap         map[string]string // device name -> hex ID
	uiScaleSelect     *widget.Select
	globalVoiceField  *VoiceSpeedField
	syncTTSControls   func()
	prevModels        []string
	prevPreset        string
	prevUseGPU        bool
	initializing      bool

	onTTSChanged         func(enabled bool)
	onLoggingChanged     func(enabled bool)
	onChatScaleChanged   func(pct int)
	featuresCard         *widget.Card
	ttsOutputModeCard    *widget.Card
	ttsOutputModeSelect  *widget.Select
	idleTimeoutSelect    *widget.Select
	ttsModeStatusLabel   *widget.Label
	playAlertCheck       *widget.Check
	alertSoundEntry      *ScrollPassEntry
	alertBrowseBtn       *widget.Button
	alertClearBtn        *widget.Button
	alertTestBtn         *widget.Button
	wsControlCard         *widget.Card
	wsEnabledCheck        *widget.Check
	wsHostEntry           *ScrollPassEntry
	wsPortEntry           *ScrollPassEntry
	wsTokenEntry          *ScrollPassEntry
	wsStatusLabel         *widget.Label
	streamDeckInstallBtn  *widget.Button
	streamDeckStatusLabel *widget.Label
	streamDeckPluginsDir  string
	generalBox           *fyne.Container
	enableTTSCheck       *widget.Check
	enableLogCheck       *widget.Check
	checkUpdatesCheck    *widget.Check
	globalVoiceCard      *widget.Card
	audioCard            *widget.Card
	presetSelect         *widget.Select
	gpuCheck             *widget.Check
	threadsSlider        *widget.Slider
	idleSelect           *widget.Select
	modelChecks          map[string]*widget.Check
	deviceRefreshBtn     *widget.Button
	rulesNamesCheck      *widget.Check
	rulesEmotesCheck     *widget.Check
	rulesDedupeCheck     *widget.Check
	rulesDupWordsSelect  *widget.Select
	rulesPlatformCheck   *widget.Check
	hotkeyCtrlCheck      *widget.Check
	hotkeyShiftCheck     *widget.Check
	hotkeyAltCheck       *widget.Check
	hotkeyKeySelect      *widget.Select
	hotkeyRecordBtn      *widget.Button
	hotkeyResetBtn       *widget.Button
	modeHotkeyCtrlCheck  *widget.Check
	modeHotkeyShiftCheck *widget.Check
	modeHotkeyAltCheck   *widget.Check
	modeHotkeyKeySelect  *widget.Select
	modeHotkeyRecordBtn  *widget.Button
	modeHotkeyResetBtn   *widget.Button
	defaultBadWordsCheck *widget.Check
	newWordEntry         *ScrollPassEntry
	addWordBtn           *widget.Button
	wordDeleteBtns       []*widget.Button
	ttsEnabled           bool
	loggingEnabled       bool
}

// NewSettingsView constructs a SettingsView for configuring audio, TTS hardware, and global rules.
func NewSettingsView(w fyne.Window, cfg *config.Config, player audio.Player, engine tts.Engine) *SettingsView {
	ttsOn := true
	loggingOn := true
	if cfg != nil {
		ttsOn = cfg.TTS.Enabled
		loggingOn = cfg.Logging.Enabled
	}
	return &SettingsView{
		window:         w,
		cfg:            cfg,
		player:         player,
		engine:         engine,
		modelDir:       tts.DefaultModelDir(),
		deviceMap:      make(map[string]string),
		deviceEnum:     audio.EnumeratePlaybackDevices,
		prevModels:     slices.Clone(cfg.TTS.EffectiveEnabledModels()),
		prevPreset:     cfg.TTS.Preset,
		prevUseGPU:     cfg.TTS.UseGPU,
		ttsEnabled:     ttsOn,
		loggingEnabled: loggingOn,
		modelChecks:    make(map[string]*widget.Check),
	}
}

// SetTTSChangedHandler registers a callback invoked when TTS is enabled or disabled from Settings.
func (v *SettingsView) SetTTSChangedHandler(fn func(enabled bool)) {
	v.onTTSChanged = fn
}

// SetLoggingChangedHandler registers a callback invoked when Event Logging is enabled or disabled from Settings.
func (v *SettingsView) SetLoggingChangedHandler(fn func(enabled bool)) {
	v.onLoggingChanged = fn
}

// SetChatScaleChangedHandler registers a callback invoked when the default chat panel scale is updated in Settings.
func (v *SettingsView) SetChatScaleChangedHandler(fn func(pct int)) {
	v.onChatScaleChanged = fn
	if v.logsView != nil {
		v.logsView.SetOnChatScaleChanged(fn)
	}
}

// SetDeviceEnumerator overrides the audio device enumeration function (useful for tests or mocking).
func (v *SettingsView) SetDeviceEnumerator(fn func() ([]audio.DeviceInfo, error)) {
	v.deviceEnum = fn
}

// SetSession attaches the application session to coordinate engine reloads.
func (v *SettingsView) SetSession(s *app.Session) {
	v.session = s
}

// SetStatusHandler registers a callback invoked when configuration or audio status updates.
func (v *SettingsView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

// SetHotkeyChangedHandler registers a callback invoked when the user changes the global Panic Mute hotkey.
func (v *SettingsView) SetHotkeyChangedHandler(fn func(hotkey string) error) {
	v.onHotkeyChanged = fn
}

// SetModeToggleHotkeyChangedHandler registers a callback invoked when the user changes the global TTS Mode Toggle hotkey.
func (v *SettingsView) SetModeToggleHotkeyChangedHandler(fn func(hotkey string) error) {
	v.onModeToggleHotkeyChanged = fn
}

// SetTTSOutputModeChangedHandler registers a callback invoked when the TTS Output Mode is updated in Settings.
func (v *SettingsView) SetTTSOutputModeChangedHandler(fn func(mode string)) {
	v.onTTSOutputModeChanged = fn
}

// SetServicesChangedHandler registers a callback invoked when streaming services are enabled or disabled.
func (v *SettingsView) SetServicesChangedHandler(fn func()) {
	v.onServicesChanged = fn
}

// SetModelsChangedHandler registers a callback invoked when enabled TTS voice model packs change.
func (v *SettingsView) SetModelsChangedHandler(fn func()) {
	v.onModelsChanged = fn
}

// SetOnAssignVoice registers a callback invoked when a viewer voice is assigned from the Event Logs sub-tab.
func (v *SettingsView) SetOnAssignVoice(fn func(platform, username string)) {
	v.onAssignVoice = fn
	if v.logsView != nil {
		v.logsView.SetOnAssignVoice(fn)
	}
}

// SetPlatformTabs provides the platform configuration views to be hosted inside Settings sub-tabs.
func (v *SettingsView) SetPlatformTabs(twitch *TwitchTabView, yt *YouTubeTabView, tt *TikTokTabView, kick *KickTabView) {
	v.twitchTab = twitch
	v.youtubeTab = yt
	v.tiktokTab = tt
	v.kickTab = kick
}

// SelectPlatformTab switches the active Settings sub-tab to the specified platform.
func (v *SettingsView) SelectPlatformTab(platform string) {
	if v.subTabs == nil {
		return
	}
	v.SyncPlatformTabVisibility()
	var target *container.TabItem
	switch strings.ToLower(platform) {
	case "twitch":
		target = v.twitchSettingsItem
	case "youtube":
		target = v.youtubeSettingsItem
	case "tiktok":
		target = v.tiktokSettingsItem
	case "kick":
		target = v.kickSettingsItem
	}
	if target != nil {
		found := slices.Contains(v.subTabs.Items, target)
		if !found {
			v.subTabs.Append(target)
		}
		v.subTabs.Select(target)
	}
}

// SyncPlatformTabVisibility shows or hides platform sub-tabs in Settings based on enabled status.
func (v *SettingsView) SyncPlatformTabVisibility() {
	if v.subTabs == nil {
		return
	}
	cfg := v.currentConfig()
	items := []*container.TabItem{
		v.generalTabItem,
	}

	if v.ttsEnabled && v.ttsTabItem != nil {
		items = append(items, v.ttsTabItem)
	}

	addIfVisible := func(platform string, item *container.TabItem) {
		if item == nil {
			return
		}
		p, ok := cfg.Platforms[platform]
		if !ok || p.EverEnabled || p.Enabled {
			items = append(items, item)
		}
	}

	addIfVisible("twitch", v.twitchSettingsItem)
	addIfVisible("youtube", v.youtubeSettingsItem)
	addIfVisible("tiktok", v.tiktokSettingsItem)
	addIfVisible("kick", v.kickSettingsItem)

	if v.ttsEnabled && v.rulesTabItem != nil {
		items = append(items, v.rulesTabItem)
	}
	if v.loggingEnabled && v.logsTabItem != nil {
		items = append(items, v.logsTabItem)
	}
	v.subTabs.SetItems(items)
}

// LogsView returns the embedded Event Logs sub-tab view.
func (v *SettingsView) LogsView() *LogsView {
	return v.logsView
}

func (v *SettingsView) currentConfig() *config.Config {
	if v.session != nil {
		v.cfg = v.session.Config()
	}
	return v.cfg
}

// RefreshVoices updates the global voice field when enabled model packs change.
func (v *SettingsView) RefreshVoices() {
	fyne.Do(func() {
		cfg := v.currentConfig()
		v.initializing = true
		defer func() { v.initializing = false }()

		if v.globalVoiceField != nil {
			defVoice := cfg.GlobalChatRules.DefaultVoice
			if strings.TrimSpace(defVoice) == "" {
				if labels := tts.VoiceDisplayLabelsForModels(cfg.TTS.EffectiveEnabledModels()); len(labels) > 0 {
					defVoice = tts.VoiceIDFromLabel(labels[0])
				}
			}
			defSpeed := cfg.GlobalChatRules.DefaultSpeed
			if defSpeed <= 0 {
				defSpeed = 1.0
			}
			v.globalVoiceField.SetVoiceAndSpeed(defVoice, defSpeed)
		}
	})
}

// SyncTTSState updates all Settings controls to reflect whether TTS is enabled or disabled.
func (v *SettingsView) SyncTTSState(enabled bool) {
	v.ttsEnabled = enabled
	fyne.Do(func() {
		if v.enableTTSCheck != nil && v.enableTTSCheck.Checked != enabled {
			v.initializing = true
			v.enableTTSCheck.SetChecked(enabled)
			v.initializing = false
		}
		if v.globalVoiceCard != nil {
			if enabled {
				v.globalVoiceCard.Show()
			} else {
				v.globalVoiceCard.Hide()
			}
		}
		if v.ttsOutputModeCard != nil {
			if enabled {
				v.ttsOutputModeCard.Show()
			} else {
				v.ttsOutputModeCard.Hide()
			}
		}
		if v.audioCard != nil {
			if enabled {
				v.audioCard.Show()
			} else {
				v.audioCard.Hide()
			}
		}
		if v.generalBox != nil {
			v.generalBox.Refresh()
		}
		v.SyncPlatformTabVisibility()
	})
}

// SyncTTSModeState synchronizes the Settings TTS Output Mode controls and live status indicator.
func (v *SettingsView) SyncTTSModeState(mode string, speakingActive bool, userIdle bool) {
	fyne.Do(func() {
		cfg := v.currentConfig()
		v.initializing = true
		defer func() { v.initializing = false }()

		if v.ttsOutputModeSelect != nil {
			target := FormatTTSOutputModeLabel(mode)
			if v.ttsOutputModeSelect.Selected != target {
				v.ttsOutputModeSelect.SetSelected(target)
			}
		}
		timeoutSec := config.DefaultIdleTimeoutSeconds
		if cfg != nil && cfg.TTS.IdleTimeoutSeconds > 0 {
			timeoutSec = cfg.TTS.IdleTimeoutSeconds
		}
		if v.idleTimeoutSelect != nil {
			targetIdle := FormatIdleTimeoutLabel(timeoutSec)
			if v.idleTimeoutSelect.Selected != targetIdle {
				v.idleTimeoutSelect.SetSelected(targetIdle)
			}
		}
		if v.ttsModeStatusLabel != nil {
			v.ttsModeStatusLabel.SetText(FormatTTSModeStatusSummary(mode, speakingActive, userIdle, timeoutSec))
		}
	})
}

// SyncLoggingState synchronizes settings log retention controls with logging enabled state.
func (v *SettingsView) SyncLoggingState(enabled bool) {
	v.loggingEnabled = enabled
	fyne.Do(func() {
		if v.enableLogCheck != nil && v.enableLogCheck.Checked != enabled {
			v.initializing = true
			v.enableLogCheck.SetChecked(enabled)
			v.initializing = false
		}
		if v.logsView != nil {
			v.logsView.SyncLoggingState(enabled)
		}
		v.SyncPlatformTabVisibility()
	})
}

// SyncFromConfig updates the service checkboxes in Settings when modified from a platform tab.
func (v *SettingsView) SyncFromConfig() {
	v.currentConfig()
	v.initializing = true
	defer func() { v.initializing = false }()

	if v.twitchCheck != nil {
		p, ok := v.cfg.Platforms["twitch"]
		v.twitchCheck.SetChecked(!ok || p.EverEnabled || p.Enabled)
	}
	if v.youtubeCheck != nil {
		p, ok := v.cfg.Platforms["youtube"]
		v.youtubeCheck.SetChecked(!ok || p.EverEnabled || p.Enabled)
	}
	if v.tiktokCheck != nil {
		p, ok := v.cfg.Platforms["tiktok"]
		v.tiktokCheck.SetChecked(!ok || p.EverEnabled || p.Enabled)
	}
	if v.kickCheck != nil {
		p, ok := v.cfg.Platforms["kick"]
		v.kickCheck.SetChecked(!ok || p.EverEnabled || p.Enabled)
	}
	if v.uiScaleSelect != nil {
		v.uiScaleSelect.SetSelected(FormatUIScaleLabel(v.cfg.UIScale))
	}
	if v.globalVoiceField != nil {
		v.globalVoiceField.SetVoiceAndSpeed(v.cfg.GlobalChatRules.DefaultVoice, v.cfg.GlobalChatRules.DefaultSpeed)
	}
	if v.ttsOutputModeSelect != nil {
		v.ttsOutputModeSelect.SetSelected(FormatTTSOutputModeLabel(v.cfg.TTS.OutputMode))
	}
	if v.idleTimeoutSelect != nil {
		v.idleTimeoutSelect.SetSelected(FormatIdleTimeoutLabel(v.cfg.TTS.IdleTimeoutSeconds))
	}
	if v.playAlertCheck != nil {
		v.playAlertCheck.SetChecked(v.cfg.TTS.PlayAlertWhenDisabled)
	}
	if v.alertSoundEntry != nil {
		v.alertSoundEntry.SetText(v.cfg.TTS.AlertSoundPath)
	}
	if v.wsEnabledCheck != nil {
		v.wsEnabledCheck.SetChecked(v.cfg.WebSocket.Enabled)
	}
	if v.wsHostEntry != nil {
		v.wsHostEntry.SetText(v.cfg.WebSocket.Host)
	}
	if v.wsPortEntry != nil {
		v.wsPortEntry.SetText(strconv.Itoa(v.cfg.WebSocket.Port))
	}
	if v.wsTokenEntry != nil {
		v.wsTokenEntry.SetText(v.cfg.WebSocket.AuthToken)
	}
	if v.checkUpdatesCheck != nil {
		v.checkUpdatesCheck.SetChecked(v.cfg.Update.CheckOnStartup)
	}
	v.refreshWSStatusLabel()
	if v.logsView != nil {
		v.logsView.SyncFromConfig()
		v.logsView.RefreshQuery()
	}
}

func (v *SettingsView) setStatus(msg string) {
	fyne.Do(func() {
		if v.statusLabel != nil {
			v.statusLabel.SetText(msg)
		}
		if v.onStatus != nil {
			v.onStatus(msg)
		}
	})
}

func (v *SettingsView) updateConfig(fn func(c *config.Config)) {
	if v.initializing {
		return
	}
	if v.session != nil {
		_ = v.session.UpdateConfig(fn)
		v.cfg = v.session.Config()
	} else {
		fn(v.cfg)
	}
	v.setStatus("✓ Settings saved.")
}

// Build generates the Fyne CanvasObject containing the sub-tabbed Settings interface.
func (v *SettingsView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	v.statusLabel = widget.NewLabelWithStyle("Ready.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	// Register device fallback listener if player is present
	if v.player != nil {
		v.player.SetFallbackListener(func(reason string) {
			fyne.Do(func() {
				v.setStatus("⚠️ Audio Alert: " + reason)
				dialog.ShowInformation("Audio Device Fallback", reason, v.window)
			})
		})
	}

	v.featuresCard = v.buildFeaturesCard()
	v.ttsOutputModeCard = v.buildTTSOutputModeCard()
	v.wsControlCard = v.buildWSControlCard()
	servicesCard := v.buildServicesCard()
	v.globalVoiceCard = v.buildGlobalVoiceCard()
	v.audioCard = v.buildAudioCard()
	botsCard := v.buildBotsCard()
	displayCard := v.buildDisplayCard()
	ttsCard := v.buildTTSCard()
	rulesCard := v.buildRulesCard()
	safetyCard := v.buildSafetyCard()

	if !v.ttsEnabled {
		v.ttsOutputModeCard.Hide()
		v.globalVoiceCard.Hide()
		v.audioCard.Hide()
	}

	v.generalBox = container.NewVBox(
		v.featuresCard,
		v.ttsOutputModeCard,
		v.wsControlCard,
		servicesCard,
		v.globalVoiceCard,
		v.audioCard,
		botsCard,
		displayCard,
	)

	generalTabContent := newPanelVScroll(v.generalBox)
	ttsTabContent := newPanelVScroll(container.NewVBox(
		ttsCard,
	))
	v.logsView = NewLogsView(v.window, v.session)
	v.logsView.SetStatusHandler(v.setStatus)
	v.logsView.SetOnAssignVoice(v.onAssignVoice)
	v.logsView.SetOnChatScaleChanged(v.onChatScaleChanged)

	rulesTabContent := newPanelVScroll(container.NewVBox(
		rulesCard,
		safetyCard,
	))

	logsTabContent := newPanelVScroll(container.NewVBox(
		v.logsView.BuildRetentionCard(),
	))

	v.generalTabItem = container.NewTabItemWithIcon("General & Audio", theme.VolumeUpIcon(), generalTabContent)
	v.ttsTabItem = container.NewTabItemWithIcon("TTS & Hardware", theme.ComputerIcon(), ttsTabContent)
	v.rulesTabItem = container.NewTabItemWithIcon("Rules & Safety", theme.ConfirmIcon(), rulesTabContent)
	v.logsTabItem = container.NewTabItemWithIcon("Event Logs", theme.DocumentIcon(), logsTabContent)

	if v.twitchTab != nil {
		v.twitchSettingsItem = container.NewTabItemWithIcon("Twitch", theme.MediaPlayIcon(), v.twitchTab.Build())
	}
	if v.youtubeTab != nil {
		v.youtubeSettingsItem = container.NewTabItemWithIcon("YouTube", theme.MediaVideoIcon(), v.youtubeTab.Build())
	}
	if v.tiktokTab != nil {
		v.tiktokSettingsItem = container.NewTabItemWithIcon("TikTok", theme.MediaRecordIcon(), v.tiktokTab.Build())
	}
	if v.kickTab != nil {
		v.kickSettingsItem = container.NewTabItemWithIcon("Kick", theme.MediaFastForwardIcon(), v.kickTab.Build())
	}

	v.subTabs = container.NewAppTabs(v.generalTabItem, v.ttsTabItem, v.rulesTabItem, v.logsTabItem)
	v.SyncPlatformTabVisibility()
	v.SyncTTSState(v.ttsEnabled)

	return v.subTabs
}

func (v *SettingsView) buildFeaturesCard() *widget.Card {
	cfg := v.currentConfig()

	v.enableTTSCheck = widget.NewCheck("Enable Text-to-Speech (TTS)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.Enabled = checked
		})
		v.ttsEnabled = checked
		if v.ttsOutputModeCard != nil {
			if checked {
				v.ttsOutputModeCard.Show()
			} else {
				v.ttsOutputModeCard.Hide()
			}
		}
		if v.globalVoiceCard != nil {
			if checked {
				v.globalVoiceCard.Show()
			} else {
				v.globalVoiceCard.Hide()
			}
		}
		if v.audioCard != nil {
			if checked {
				v.audioCard.Show()
			} else {
				v.audioCard.Hide()
			}
		}
		if v.generalBox != nil {
			v.generalBox.Refresh()
		}
		v.SyncPlatformTabVisibility()
		if v.onTTSChanged != nil {
			v.onTTSChanged(checked)
		}
		if checked {
			v.setStatus("✓ Text-to-Speech enabled.")
		} else {
			v.setStatus("Text-to-Speech disabled.")
		}
	})
	v.enableTTSCheck.SetChecked(cfg.TTS.Enabled)

	v.enableLogCheck = widget.NewCheck("Enable Event Logging", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Logging.Enabled = checked
		})
		v.loggingEnabled = checked
		if v.session != nil && v.session.EventLogger() != nil {
			v.session.EventLogger().SetEnabled(checked)
		}
		if v.logsView != nil {
			v.logsView.SyncLoggingState(checked)
		}
		v.SyncPlatformTabVisibility()
		if v.onLoggingChanged != nil {
			v.onLoggingChanged(checked)
		}
		if checked {
			v.setStatus("✓ Event logging enabled.")
		} else {
			v.setStatus("Event logging disabled.")
		}
	})
	v.enableLogCheck.SetChecked(cfg.Logging.Enabled)

	v.checkUpdatesCheck = widget.NewCheck("Automatically check for updates on startup", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Update.CheckOnStartup = checked
		})
	})
	v.checkUpdatesCheck.SetChecked(cfg.Update.CheckOnStartup)

	checkNowBtn := widget.NewButtonWithIcon("Check for Updates Now", theme.ViewRefreshIcon(), func() {
		TriggerManualUpdateCheck(v.window, v.session, v.setStatus)
	})

	updateRow := container.NewHBox(v.checkUpdatesCheck, checkNowBtn)

	cardContent := container.NewVBox(
		v.enableTTSCheck,
		v.enableLogCheck,
		updateRow,
	)

	return widget.NewCard("Application Features", "Enable or disable core system features", cardContent)
}

// IdleTimeoutOptions lists selectable keyboard/mouse inactivity thresholds for Auto-AFK mode.
var IdleTimeoutOptions = []string{
	"5 seconds",
	"10 seconds",
	"15 seconds",
	"30 seconds",
	"60 seconds",
	"120 seconds",
	"300 seconds",
}

// FormatIdleTimeoutLabel returns the UI dropdown label for a timeout in seconds.
func FormatIdleTimeoutLabel(sec int) string {
	switch sec {
	case 5:
		return "5 seconds"
	case 10:
		return "10 seconds"
	case 15:
		return "15 seconds"
	case 30:
		return "30 seconds"
	case 60:
		return "60 seconds"
	case 120:
		return "120 seconds"
	case 300:
		return "300 seconds"
	default:
		if sec <= 0 {
			return "15 seconds"
		}
		return fmt.Sprintf("%d seconds", sec)
	}
}

// ParseIdleTimeoutLabel parses a UI dropdown label into seconds.
func ParseIdleTimeoutLabel(label string) int {
	var sec int
	if _, err := fmt.Sscanf(label, "%d", &sec); err == nil && sec > 0 {
		return sec
	}
	return config.DefaultIdleTimeoutSeconds
}

func (v *SettingsView) buildTTSOutputModeCard() *widget.Card {
	cfg := v.currentConfig()
	initialMode := config.NormalizeTTSOutputMode(cfg.TTS.OutputMode)
	initialTimeout := cfg.TTS.IdleTimeoutSeconds
	if initialTimeout <= 0 {
		initialTimeout = config.DefaultIdleTimeoutSeconds
	}

	speakingActive := initialMode == config.TTSModeAlwaysOn
	userIdle := false
	if v.session != nil {
		speakingActive = v.session.IsTTSSpeakingActive()
		userIdle = v.session.IsUserIdle()
	}

	v.ttsModeStatusLabel = widget.NewLabelWithStyle(
		FormatTTSModeStatusSummary(initialMode, speakingActive, userIdle, initialTimeout),
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)

	v.ttsOutputModeSelect = widget.NewSelect(TTSOutputModeOptions, func(selected string) {
		if v.initializing {
			return
		}
		mode := ParseTTSOutputModeLabel(selected)
		if v.session != nil {
			_ = v.session.SetTTSOutputMode(mode)
			v.cfg = v.session.Config()
		} else {
			v.updateConfig(func(c *config.Config) {
				c.TTS.OutputMode = mode
			})
		}
		if v.onTTSOutputModeChanged != nil {
			v.onTTSOutputModeChanged(mode)
		}
		v.setStatus("✓ TTS Output Mode set to " + FormatTTSOutputModeLabel(mode))
	})
	v.ttsOutputModeSelect.SetSelected(FormatTTSOutputModeLabel(initialMode))

	v.idleTimeoutSelect = widget.NewSelect(IdleTimeoutOptions, func(selected string) {
		if v.initializing {
			return
		}
		sec := ParseIdleTimeoutLabel(selected)
		v.updateConfig(func(c *config.Config) {
			c.TTS.IdleTimeoutSeconds = sec
		})
		if v.session != nil {
			v.session.EvaluateIdleState()
		}
		if v.ttsModeStatusLabel != nil {
			curMode := config.NormalizeTTSOutputMode(v.currentConfig().TTS.OutputMode)
			active := curMode == config.TTSModeAlwaysOn
			idle := false
			if v.session != nil {
				active = v.session.IsTTSSpeakingActive()
				idle = v.session.IsUserIdle()
			}
			v.ttsModeStatusLabel.SetText(FormatTTSModeStatusSummary(curMode, active, idle, sec))
		}
		v.setStatus(fmt.Sprintf("✓ Auto-AFK inactivity timeout set to %d seconds.", sec))
	})
	v.idleTimeoutSelect.SetSelected(FormatIdleTimeoutLabel(initialTimeout))

	modeRow := container.NewHBox(
		widget.NewLabel("Output Mode:"),
		v.ttsOutputModeSelect,
		widget.NewLabel("Auto-AFK Timeout:"),
		v.idleTimeoutSelect,
	)

	descLabel := widget.NewLabelWithStyle(
		"Keeps neural TTS models warm in RAM for instant 0ms switching. In Auto-AFK mode, TownCrier stays quiet while you use your keyboard/mouse and automatically reads chat aloud after inactivity.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	descLabel.Wrapping = fyne.TextWrapWord

	v.playAlertCheck = widget.NewCheck("Play alert sound for incoming messages when TTS output is inactive (At Computer / Always Off)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.PlayAlertWhenDisabled = checked
		})
	})
	v.playAlertCheck.SetChecked(cfg.TTS.PlayAlertWhenDisabled)

	alertEntry := NewScrollPassEntry()
	alertEntry.SetPlaceHolder("Built-in two-tone harmonic chime (or select a custom .wav / .mp3 file)")
	alertEntry.SetText(cfg.TTS.AlertSoundPath)
	alertEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		v.updateConfig(func(c *config.Config) {
			c.TTS.AlertSoundPath = trimmed
		})
	}
	v.alertSoundEntry = alertEntry

	browseBtn := widget.NewButtonWithIcon("Browse...", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				v.setStatus("⚠️ Failed to open file: " + err.Error())
				return
			}
			if reader == nil {
				return
			}
			_ = reader.Close()
			chosen := reader.URI().Path()
			alertEntry.SetText(chosen)
			v.setStatus("✓ Custom alert sound set to " + chosen)
		}, v.window)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".wav", ".wave", ".mp3"}))
		fd.Show()
	})
	v.alertBrowseBtn = browseBtn

	clearBtn := widget.NewButtonWithIcon("Use Built-in Chime", theme.ContentClearIcon(), func() {
		alertEntry.SetText("")
		v.setStatus("✓ Reset alert sound to built-in harmonic chime.")
	})
	v.alertClearBtn = clearBtn

	testBtn := widget.NewButtonWithIcon("Test Alert Sound", theme.MediaPlayIcon(), func() {
		go func() {
			var err error
			if v.session != nil {
				err = v.session.PlayAlertSound(true)
			} else if v.player != nil {
				cache := audio.NewAlertSoundCache()
				samples, rate, loadErr := cache.Resolve(strings.TrimSpace(alertEntry.Text))
				if loadErr != nil {
					err = loadErr
				} else {
					err = v.player.Play(samples, rate)
				}
			}
			if err != nil {
				v.setStatus("⚠️ Alert sound error: " + err.Error())
			} else {
				v.setStatus("🔊 Played alert sound preview.")
			}
		}()
	})
	v.alertTestBtn = testBtn

	alertBtns := container.NewHBox(browseBtn, clearBtn, testBtn)
	alertFileRow := container.NewBorder(nil, nil, widget.NewLabel("Alert Sound:"), alertBtns, alertEntry)

	cardContent := container.NewVBox(
		modeRow,
		v.ttsModeStatusLabel,
		descLabel,
		widget.NewSeparator(),
		v.playAlertCheck,
		alertFileRow,
	)

	return widget.NewCard("TTS Output Mode & Auto-AFK", "Control when chat is spoken aloud vs. alerted with a chime while at your computer", cardContent)
}

func (v *SettingsView) refreshWSStatusLabel() {
	if v.wsStatusLabel == nil {
		return
	}
	cfg := v.currentConfig()
	if cfg == nil || !cfg.WebSocket.Enabled {
		v.wsStatusLabel.SetText("⚪ Control Server Disabled")
		return
	}
	host := strings.TrimSpace(cfg.WebSocket.Host)
	if host == "" {
		host = config.DefaultWebSocketHost
	}
	port := cfg.WebSocket.Port
	if port <= 0 || port > 65535 {
		port = config.DefaultWebSocketPort
	}
	if v.session != nil && v.session.WSServer() != nil && v.session.WSServer().IsRunning() {
		addr := v.session.WSServer().Addr()
		clients := v.session.WSServer().ClientCount()
		v.wsStatusLabel.SetText(fmt.Sprintf("🟢 Listening on ws://%s/ws & http://%s/api/* (%d connected clients)", addr, addr, clients))
		return
	}
	v.wsStatusLabel.SetText(fmt.Sprintf("🟢 Configured for ws://%s:%d/ws & http://%s:%d/api/*", host, port, host, port))
}

func (v *SettingsView) buildWSControlCard() *widget.Card {
	cfg := v.currentConfig()

	v.wsStatusLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	v.wsEnabledCheck = widget.NewCheck("Enable Local WebSocket & HTTP Control Server (Stream Deck, Streamer.bot, TouchPortal)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.WebSocket.Enabled = checked
		})
		v.refreshWSStatusLabel()
		if checked {
			v.setStatus("✓ Local WebSocket & HTTP Control Server enabled.")
		} else {
			v.setStatus("Local WebSocket & HTTP Control Server disabled.")
		}
	})
	v.wsEnabledCheck.SetChecked(cfg.WebSocket.Enabled)

	hostEntry := NewScrollPassEntry()
	hostEntry.SetPlaceHolder(config.DefaultWebSocketHost)
	host := strings.TrimSpace(cfg.WebSocket.Host)
	if host == "" {
		host = config.DefaultWebSocketHost
	}
	hostEntry.SetText(host)
	v.wsHostEntry = hostEntry

	portEntry := NewScrollPassEntry()
	portEntry.SetPlaceHolder(strconv.Itoa(config.DefaultWebSocketPort))
	port := cfg.WebSocket.Port
	if port <= 0 || port > 65535 {
		port = config.DefaultWebSocketPort
	}
	portEntry.SetText(strconv.Itoa(port))
	v.wsPortEntry = portEntry

	tokenEntry := NewScrollPassEntry()
	tokenEntry.Password = true
	tokenEntry.SetPlaceHolder("Optional shared secret token (leave blank for localhost no-auth)")
	tokenEntry.SetText(cfg.WebSocket.AuthToken)
	v.wsTokenEntry = tokenEntry

	applyNetBtn := widget.NewButtonWithIcon("Apply Network Settings", theme.ConfirmIcon(), func() {
		newHost := strings.TrimSpace(hostEntry.Text)
		if newHost == "" {
			newHost = config.DefaultWebSocketHost
			hostEntry.SetText(newHost)
		}
		newPort, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || newPort <= 0 || newPort > 65535 {
			v.setStatus("⚠️ Invalid port number (must be 1..65535)")
			return
		}
		newToken := strings.TrimSpace(tokenEntry.Text)
		v.updateConfig(func(c *config.Config) {
			c.WebSocket.Host = newHost
			c.WebSocket.Port = newPort
			c.WebSocket.AuthToken = newToken
		})
		v.refreshWSStatusLabel()
		v.setStatus(fmt.Sprintf("✓ Control Server settings updated (%s:%d).", newHost, newPort))
	})

	netRow := container.NewGridWithColumns(3,
		container.NewBorder(nil, nil, widget.NewLabel("Host:"), nil, hostEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Port:"), nil, portEntry),
		applyNetBtn,
	)
	tokenRow := container.NewBorder(nil, nil, widget.NewLabel("Auth Token:"), nil, tokenEntry)

	v.refreshWSStatusLabel()

	infoLabel := widget.NewLabelWithStyle(
		"Supports binary Protocol Buffers (proto/control/v1/control.proto) and JSON over WebSocket (/ws), plus HTTP REST webhooks (/api/status, /api/speak, /api/mode, /api/panic, /api/queue, /api/volume).",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	infoLabel.Wrapping = fyne.TextWrapWord

	v.streamDeckStatusLabel = widget.NewLabel("Stream Deck Plugin: Ready to install (com.github.mrpoundsign.towncrier.sdPlugin)")
	v.streamDeckStatusLabel.Wrapping = fyne.TextWrapWord
	if v.streamDeckPluginsDir != "" && streamdeck.IsPluginInstalled(v.streamDeckPluginsDir) {
		v.streamDeckStatusLabel.SetText("✓ Stream Deck Plugin installed")
	}

	v.streamDeckInstallBtn = widget.NewButtonWithIcon("Install / Update Elgato Stream Deck Plugin", theme.DownloadIcon(), func() {
		installedPath, err := streamdeck.InstallPlugin(v.streamDeckPluginsDir)
		if err != nil {
			errMsg := fmt.Sprintf("⚠️ Stream Deck plugin install failed: %v", err)
			v.streamDeckStatusLabel.SetText(errMsg)
			v.setStatus(errMsg)
			return
		}
		if cur := v.currentConfig(); cur != nil && !cur.WebSocket.Enabled {
			v.updateConfig(func(c *config.Config) {
				c.WebSocket.Enabled = true
			})
			v.initializing = true
			if v.wsEnabledCheck != nil {
				v.wsEnabledCheck.SetChecked(true)
			}
			v.initializing = false
			v.refreshWSStatusLabel()
		}
		okMsg := fmt.Sprintf("✓ Installed Stream Deck plugin to %s (restart Elgato Stream Deck app to load)", installedPath)
		v.streamDeckStatusLabel.SetText(okMsg)
		v.setStatus("✓ Elgato Stream Deck plugin installed & WebSocket control server enabled.")
	})
	v.streamDeckInstallBtn.Importance = widget.HighImportance

	sdRow := container.NewBorder(nil, nil, v.streamDeckInstallBtn, nil, v.streamDeckStatusLabel)

	cardContent := container.NewVBox(
		v.wsEnabledCheck,
		v.wsStatusLabel,
		netRow,
		tokenRow,
		infoLabel,
		widget.NewSeparator(),
		sdRow,
	)

	return widget.NewCard("WebSocket & HTTP Control API", "External automation, Elgato Stream Deck, and webhook integration", cardContent)
}

func (v *SettingsView) buildServicesCard() *widget.Card {
	twitchCfg, hasTwitch := v.cfg.Platforms["twitch"]
	twitchVisible := !hasTwitch || twitchCfg.EverEnabled || twitchCfg.Enabled

	v.twitchCheck = widget.NewCheck("Twitch (shows Twitch tab & loads connector)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["twitch"]
			if !ok {
				p = config.DefaultConfig().Platforms["twitch"]
			}
			p.EverEnabled = checked
			p.Enabled = checked
			c.Platforms["twitch"] = p
		})
		if v.onServicesChanged != nil {
			v.onServicesChanged()
		}
	})
	v.twitchCheck.SetChecked(twitchVisible)

	ytCfg, hasYT := v.cfg.Platforms["youtube"]
	ytVisible := !hasYT || ytCfg.EverEnabled || ytCfg.Enabled

	v.youtubeCheck = widget.NewCheck("YouTube Live (shows YouTube tab & loads connector)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["youtube"]
			if !ok {
				p = config.DefaultConfig().Platforms["youtube"]
			}
			p.EverEnabled = checked
			p.Enabled = checked
			c.Platforms["youtube"] = p
		})
		if v.onServicesChanged != nil {
			v.onServicesChanged()
		}
	})
	v.youtubeCheck.SetChecked(ytVisible)

	ttCfg, hasTT := v.cfg.Platforms["tiktok"]
	ttVisible := !hasTT || ttCfg.EverEnabled || ttCfg.Enabled

	v.tiktokCheck = widget.NewCheck("TikTok Live (shows TikTok tab & loads connector)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["tiktok"]
			if !ok {
				p = config.DefaultConfig().Platforms["tiktok"]
			}
			p.EverEnabled = checked
			p.Enabled = checked
			c.Platforms["tiktok"] = p
		})
		if v.onServicesChanged != nil {
			v.onServicesChanged()
		}
	})
	v.tiktokCheck.SetChecked(ttVisible)

	kickCfg, hasKick := v.cfg.Platforms["kick"]
	kickVisible := !hasKick || kickCfg.EverEnabled || kickCfg.Enabled

	v.kickCheck = widget.NewCheck("Kick (shows Kick tab & loads connector)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["kick"]
			if !ok {
				p = config.DefaultConfig().Platforms["kick"]
			}
			p.EverEnabled = checked
			p.Enabled = checked
			c.Platforms["kick"] = p
		})
		if v.onServicesChanged != nil {
			v.onServicesChanged()
		}
	})
	v.kickCheck.SetChecked(kickVisible)

	cardContent := container.NewVBox(
		v.twitchCheck,
		v.youtubeCheck,
		v.tiktokCheck,
		v.kickCheck,
	)

	return widget.NewCard("Streaming Services", "Show/hide platform tabs and load or unload their connectors", cardContent)
}

func (v *SettingsView) buildGlobalVoiceCard() *widget.Card {
	cfg := v.currentConfig()

	selectedVoice := cfg.GlobalChatRules.DefaultVoice
	if strings.TrimSpace(selectedVoice) == "" {
		if labels := tts.VoiceDisplayLabelsForModels(cfg.TTS.EffectiveEnabledModels()); len(labels) > 0 {
			selectedVoice = tts.VoiceIDFromLabel(labels[0])
		}
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
		container.NewBorder(nil, nil, widget.NewLabel("Current:"), nil, v.globalVoiceField.Container),
	)

	return widget.NewCard("Global Default Voice", "", cardContent)
}

func (v *SettingsView) buildAudioCard() *widget.Card {
	v.deviceSelect = widget.NewSelect([]string{"Default Audio Device"}, func(selected string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		devHex := v.deviceMap[selected]
		v.mu.Unlock()

		v.updateConfig(func(c *config.Config) {
			c.Audio.DeviceName = selected
			c.Audio.DeviceID = devHex
		})

		if v.player != nil {
			go func() {
				err := v.player.SwitchDevice(devHex)
				if err != nil {
					v.setStatus(fmt.Sprintf("Failed to switch audio device: %v", err))
				} else {
					v.setStatus("Active audio device: " + selected)
				}
			}()
		}
	})

	v.refreshDeviceList()

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshDeviceList()
		v.setStatus("Audio device list refreshed.")
	})
	v.deviceRefreshBtn = refreshBtn

	deviceRow := container.NewBorder(nil, nil, widget.NewLabel("Output Device:"), refreshBtn, v.deviceSelect)

	cardContent := container.NewVBox(
		deviceRow,
	)

	return widget.NewCard("Audio Output Device", "Select WASAPI playback device for OBS routing", cardContent)
}

func (v *SettingsView) refreshDeviceList() {
	v.mu.Lock()
	enumFn := v.deviceEnum
	if enumFn == nil {
		enumFn = audio.EnumeratePlaybackDevices
	}
	v.mu.Unlock()

	devices, err := enumFn()

	options := []string{"Default Audio Device"}
	newMap := make(map[string]string)
	newMap["Default Audio Device"] = ""

	if err == nil {
		for _, dev := range devices {
			options = append(options, dev.Name)
			newMap[dev.Name] = dev.ID
		}
	}

	v.mu.Lock()
	v.deviceMap = newMap
	targetName := v.cfg.Audio.DeviceName
	v.mu.Unlock()

	if v.deviceSelect != nil {
		v.deviceSelect.Options = options
		selected := "Default Audio Device"
		if targetName != "" {
			for _, opt := range options {
				if opt == targetName {
					selected = opt
					break
				}
			}
		}
		v.deviceSelect.SetSelected(selected)
	}
}

func (v *SettingsView) buildDisplayCard() *widget.Card {
	v.uiScaleSelect = widget.NewSelect(UIScaleOptions(), func(selected string) {
		if v.initializing {
			return
		}
		mode := ParseUIScaleLabel(selected)
		v.updateConfig(func(c *config.Config) {
			c.UIScale = mode
		})
		ApplyUIScale(fyne.CurrentApp(), mode)
		v.setStatus("UI Scale updated to " + selected)
	})
	v.uiScaleSelect.SetSelected(FormatUIScaleLabel(v.cfg.UIScale))

	scaleRow := container.NewHBox(widget.NewLabel("UI Scale:"), v.uiScaleSelect)
	cardContent := container.NewVBox(
		scaleRow,
	)

	return widget.NewCard("Display & Scaling", "Lock DPI scaling or customize interface zoom", cardContent)
}

func (v *SettingsView) buildTTSCard() *widget.Card {
	v.modelStatus = widget.NewLabel(v.formatModelStatusText())
	v.modelStatus.Wrapping = fyne.TextWrapWord

	var syncingControls bool

	// --- Hardware Impact Stacked Graphs & Tradeoff Notice ---
	ramDetail := widget.NewLabel("")
	ramDetail.Wrapping = fyne.TextWrapWord
	ramBar := NewStackedImpactBar()
	cpuDetail := widget.NewLabel("")
	cpuDetail.Wrapping = fyne.TextWrapWord
	cpuBar := NewStackedImpactBar()
	gpuDetail := widget.NewLabel("")
	gpuDetail.Wrapping = fyne.TextWrapWord
	gpuBar := NewStackedImpactBar()
	latencyDetail := widget.NewLabel("")
	latencyDetail.Wrapping = fyne.TextWrapWord
	latencyBar := NewStackedImpactBar()

	tradeoffLabel := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	tradeoffLabel.Wrapping = fyne.TextWrapWord

	legendRow := container.NewGridWrap(
		fyne.NewSize(145, 24),
		NewModelColorBadge("base", "Base App", false),
		NewModelColorBadge(tts.ModelSupertonic, "Supertonic INT8", false),
		NewModelColorBadge(tts.ModelKokoro, "Kokoro-82M", false),
		NewModelColorBadge(tts.ModelKokoroIntl, "Kokoro Intl", false),
		NewModelColorBadge(tts.ModelKittenMini, "Kitten Mini", false),
		NewModelColorBadge(tts.ModelKittenNano, "Kitten Nano", false),
		NewModelColorBadge(tts.ModelPiper, "Piper LibriTTS", false),
	)

	updateImpactUI := func() {
		cfg := v.currentConfig()
		enabled := cfg.TTS.EffectiveEnabledModels()
		threads := cfg.TTS.NumThreads
		if threads <= 0 {
			threads = 4
		}
		impact := tts.EstimateHardwareImpact(enabled, cfg.TTS.UseGPU, threads)

		ramDetail.SetText(impact.RAMText)
		ramBar.SetData(impact.RAMSegments, impact.RAMBarText)

		cpuDetail.SetText(impact.CPUText)
		cpuBar.SetData(impact.CPUSegments, impact.CPUBarText)

		gpuDetail.SetText(impact.GPUText)
		gpuBar.SetData(impact.GPUSegments, impact.GPUBarText)

		latencyDetail.SetText(impact.LatencyText)
		latencyBar.SetData(impact.LatencySegments, impact.LatencyBarText)

		tradeoffLabel.SetText(impact.TradeoffNotice)
	}

	makeMeterRow := func(title string, detail *widget.Label, bar *StackedImpactBar) fyne.CanvasObject {
		header := container.NewBorder(
			nil,
			nil,
			widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			nil,
			detail,
		)
		return container.NewVBox(header, bar)
	}

	// --- Hardware & Memory Controls ---
	presetLabels := make([]string, 0, len(tts.HardwarePresets))
	for _, p := range tts.HardwarePresets {
		presetLabels = append(presetLabels, p.Label)
	}
	presetSelect := widget.NewSelect(presetLabels, nil)
	v.presetSelect = presetSelect
	presetDescLabel := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	presetDescLabel.Wrapping = fyne.TextWrapWord

	gpuCheck := widget.NewCheck("Use DirectML GPU for Kokoro-82M (fast ~0.3s start, +590 MB RAM)", nil)
	v.gpuCheck = gpuCheck

	initThreads := v.cfg.TTS.NumThreads
	if initThreads <= 0 {
		initThreads = 4
	}
	threadsValLabel := widget.NewLabel(fmt.Sprintf("%d threads", initThreads))
	threadsSlider := widget.NewSlider(1, 12)
	threadsSlider.Step = 1
	threadsSlider.SetValue(float64(initThreads))
	v.threadsSlider = threadsSlider

	idleOptions := []string{
		"Never (Keep Models Warm in RAM)",
		"After 5 minutes idle",
		"After 10 minutes idle",
		"After 15 minutes idle",
		"After 30 minutes idle",
	}
	idleMinutesFromLabel := func(lbl string) int {
		switch lbl {
		case "After 5 minutes idle":
			return 5
		case "After 10 minutes idle":
			return 10
		case "After 15 minutes idle":
			return 15
		case "After 30 minutes idle":
			return 30
		default:
			return 0
		}
	}
	idleLabelFromMinutes := func(m int) string {
		switch m {
		case 5:
			return "After 5 minutes idle"
		case 10:
			return "After 10 minutes idle"
		case 15:
			return "After 15 minutes idle"
		case 30:
			return "After 30 minutes idle"
		default:
			return "Never (Keep Models Warm in RAM)"
		}
	}
	idleSelect := widget.NewSelect(idleOptions, nil)
	v.idleSelect = idleSelect

	// --- Voice Model Pack Checkboxes ---
	modelChecks := make(map[string]*widget.Check, len(tts.AllModelFamilies))
	v.modelChecks = modelChecks
	modelPackRows := container.NewVBox()

	formatPackCheckLabel := func(spec tts.ModelFamilySpec) string {
		cfg := v.currentConfig()
		status := fmt.Sprintf("Downloads ~%d MB on enable", spec.DownloadSizeMB)
		if spec.ID == tts.ModelKokoroIntl && tts.IsModelFamilyInstalled(v.modelDir, tts.ModelKokoro, cfg.TTS.UseGPU) {
			status = "Installed (Shares Kokoro)"
		} else if tts.IsModelFamilyInstalled(v.modelDir, spec.ID, cfg.TTS.UseGPU) {
			status = "Installed"
		}
		return fmt.Sprintf("%s [%s]", spec.Name, status)
	}

	refreshPackLabels := func() {
		for _, spec := range tts.AllModelFamilies {
			if chk, ok := modelChecks[spec.ID]; ok {
				chk.SetText(formatPackCheckLabel(spec))
				chk.Refresh()
			}
		}
		if v.modelStatus != nil {
			v.modelStatus.SetText(v.formatModelStatusText())
		}
	}

	syncWidgetsFromConfig := func() {
		cfg := v.currentConfig()
		syncingControls = true
		defer func() { syncingControls = false }()

		activePreset := tts.PresetByID(cfg.TTS.Preset)
		if cfg.TTS.Preset == tts.PresetCustom {
			for _, p := range tts.HardwarePresets {
				if p.ID == tts.PresetCustom {
					activePreset = p
					break
				}
			}
		}
		presetSelect.SetSelected(activePreset.Label)
		presetDescLabel.SetText(activePreset.Description)

		enabledSet := make(map[string]bool)
		for _, m := range cfg.TTS.EffectiveEnabledModels() {
			enabledSet[m] = true
		}
		for _, spec := range tts.AllModelFamilies {
			if chk, ok := modelChecks[spec.ID]; ok {
				chk.SetChecked(enabledSet[spec.ID])
			}
		}

		gpuCheck.SetChecked(cfg.TTS.UseGPU)
		if enabledSet[tts.ModelKokoro] || enabledSet[tts.ModelKokoroIntl] {
			gpuCheck.Enable()
		} else {
			gpuCheck.Disable()
		}

		th := cfg.TTS.NumThreads
		if th <= 0 {
			th = 4
		}
		threadsSlider.SetValue(float64(th))
		threadsValLabel.SetText(fmt.Sprintf("%d threads", th))

		idleSelect.SetSelected(idleLabelFromMinutes(cfg.TTS.IdleUnloadMinutes))
		refreshPackLabels()
		updateImpactUI()

		v.mu.Lock()
		if len(v.prevModels) == 0 {
			v.prevModels = slices.Clone(cfg.TTS.EffectiveEnabledModels())
			v.prevPreset = cfg.TTS.Preset
			v.prevUseGPU = cfg.TTS.UseGPU
		}
		v.mu.Unlock()
	}
	v.syncTTSControls = syncWidgetsFromConfig

	markCustomPreset := func() {
		syncingControls = true
		for _, p := range tts.HardwarePresets {
			if p.ID == tts.PresetCustom {
				presetSelect.SetSelected(p.Label)
				presetDescLabel.SetText(p.Description)
				break
			}
		}
		syncingControls = false
	}

	for _, spec := range tts.AllModelFamilies {
		famSpec := spec
		chk := widget.NewCheck(formatPackCheckLabel(famSpec), nil)
		modelChecks[famSpec.ID] = chk

		summaryLbl := widget.NewLabelWithStyle("      "+famSpec.Summary, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
		summaryLbl.Wrapping = fyne.TextWrapWord

		chk.OnChanged = func(checked bool) {
			if v.initializing || syncingControls {
				return
			}
			cfg := v.currentConfig()
			if !checked {
				if usages := tts.FindVoiceUsages(cfg, []string{famSpec.ID}); len(usages) > 0 {
					syncingControls = true
					chk.SetChecked(true)
					syncingControls = false
					v.setStatus(fmt.Sprintf("⚠️ Cannot disable %s — %d assigned voice(s) in use.", famSpec.ShortName, len(usages)))
					if v.window != nil {
						dialog.ShowInformation("Cannot Disable Model Pack — Voices In Use", tts.FormatVoiceUsagesWarning(usages), v.window)
					}
					return
				}
			}

			var selectedModels []string
			for _, s := range tts.AllModelFamilies {
				if c, ok := modelChecks[s.ID]; ok && c.Checked {
					selectedModels = append(selectedModels, s.ID)
				}
			}
			if len(selectedModels) == 0 {
				syncingControls = true
				chk.SetChecked(true)
				syncingControls = false
				v.setStatus("⚠️ At least one voice model pack must remain enabled.")
				return
			}

			if slices.Contains(selectedModels, tts.ModelKokoro) || slices.Contains(selectedModels, tts.ModelKokoroIntl) {
				gpuCheck.Enable()
			} else {
				gpuCheck.Disable()
			}

			v.updateConfig(func(c *config.Config) {
				c.TTS.Preset = tts.PresetCustom
				c.TTS.EnabledModels = selectedModels
			})
			markCustomPreset()
			updateImpactUI()
			if v.onModelsChanged != nil {
				v.onModelsChanged()
			}
			v.reloadTTSEngine(refreshPackLabels)
		}

		colorDot := NewModelColorBadge(famSpec.ID, "", false)
		checkHeader := container.NewBorder(nil, nil, colorDot, nil, chk)
		if famSpec.ID == tts.ModelKokoro {
			indentedGPU := container.NewBorder(nil, nil, widget.NewLabel("      ↳ "), nil, gpuCheck)
			modelPackRows.Add(container.NewVBox(checkHeader, summaryLbl, indentedGPU))
		} else {
			modelPackRows.Add(container.NewVBox(checkHeader, summaryLbl))
		}
	}

	presetSelect.OnChanged = func(selected string) {
		if v.initializing || syncingControls {
			return
		}
		cfg := v.currentConfig()
		pSpec, ok := tts.PresetByLabel(selected)
		if !ok {
			return
		}
		if pSpec.ID == tts.PresetCustom {
			presetDescLabel.SetText(pSpec.Description)
			v.updateConfig(func(c *config.Config) {
				c.TTS.Preset = tts.PresetCustom
			})
			return
		}

		disabledFamilies := tts.DisabledModelFamilies(cfg.TTS.EffectiveEnabledModels(), pSpec.EnabledModels)
		if usages := tts.FindVoiceUsages(cfg, disabledFamilies); len(usages) > 0 {
			syncWidgetsFromConfig()
			v.setStatus(fmt.Sprintf("⚠️ Cannot switch preset — %d assigned voice(s) use a model pack that would be disabled.", len(usages)))
			if v.window != nil {
				dialog.ShowInformation("Cannot Disable Model Pack — Voices In Use", tts.FormatVoiceUsagesWarning(usages), v.window)
			}
			return
		}

		presetDescLabel.SetText(pSpec.Description)
		v.updateConfig(func(c *config.Config) {
			c.TTS.Preset = pSpec.ID
			c.TTS.EnabledModels = slices.Clone(pSpec.EnabledModels)
			c.TTS.UseGPU = pSpec.UseGPU
			c.TTS.NumThreads = pSpec.NumThreads
			c.TTS.MaxClauseWords = pSpec.MaxClauseWords
			c.TTS.IdleUnloadMinutes = pSpec.IdleUnloadMinutes
		})

		syncWidgetsFromConfig()
		if v.onModelsChanged != nil {
			v.onModelsChanged()
		}
		v.reloadTTSEngine(refreshPackLabels)
	}

	gpuCheck.OnChanged = func(checked bool) {
		cfg := v.currentConfig()
		if v.initializing || syncingControls || cfg.TTS.UseGPU == checked {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.Preset = tts.PresetCustom
			c.TTS.UseGPU = checked
		})
		markCustomPreset()
		updateImpactUI()
		v.reloadTTSEngine(refreshPackLabels)
	}

	threadsSlider.OnChanged = func(val float64) {
		cfg := v.currentConfig()
		th := max(int(val), 1)
		threadsValLabel.SetText(fmt.Sprintf("%d threads", th))
		if v.initializing || syncingControls || cfg.TTS.NumThreads == th {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.Preset = tts.PresetCustom
			c.TTS.NumThreads = th
		})
		markCustomPreset()
		updateImpactUI()
		v.reloadTTSEngine(refreshPackLabels)
	}

	idleSelect.OnChanged = func(selected string) {
		cfg := v.currentConfig()
		mins := idleMinutesFromLabel(selected)
		if v.initializing || syncingControls || cfg.TTS.IdleUnloadMinutes == mins {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.Preset = tts.PresetCustom
			c.TTS.IdleUnloadMinutes = mins
		})
		markCustomPreset()
		v.reloadTTSEngine(refreshPackLabels)
	}

	syncWidgetsFromConfig()

	v.enableTTSCheck = widget.NewCheck("Enable Text-to-Speech (TTS)", func(checked bool) {
		if v.initializing || syncingControls {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.Enabled = checked
		})
		if !checked {
			if v.session != nil {
				if eng := v.session.Engine(); eng != nil {
					_ = eng.Close()
					v.session.SetEngine(nil)
				}
				tts.ReclaimOSMemory()
			}
			v.setStatus("✓ Text-to-Speech disabled.")
		} else {
			v.setStatus("Enabling Text-to-Speech...")
			v.reloadTTSEngine(refreshPackLabels)
		}
		if v.onTTSChanged != nil {
			v.onTTSChanged(checked)
		}
		v.SyncTTSState(checked)
	})
	v.enableTTSCheck.SetChecked(v.currentConfig().TTS.Enabled)

	presetRow := container.NewBorder(
		nil,
		nil,
		widget.NewLabelWithStyle("Performance Preset:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil,
		presetSelect,
	)

	metersBox := container.NewVBox(
		widget.NewLabelWithStyle("Estimated Hardware & Latency Impact (Additive Per Loaded Model):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		legendRow,
		makeMeterRow("Memory Footprint (RAM):", ramDetail, ramBar),
		makeMeterRow("CPU Compute Load:", cpuDetail, cpuBar),
		makeMeterRow("GPU / DirectML Load:", gpuDetail, gpuBar),
		makeMeterRow("Speech Startup Delay:", latencyDetail, latencyBar),
		tradeoffLabel,
	)

	threadsRow := container.NewBorder(
		nil,
		nil,
		widget.NewLabel("CPU Inference Threads (4 recommended):"),
		threadsValLabel,
		threadsSlider,
	)
	idleRow := container.NewBorder(
		nil,
		nil,
		widget.NewLabel("Idle Model Auto-Unload:"),
		nil,
		idleSelect,
	)

	cardContent := container.NewVBox(
		v.modelStatus,
		presetRow,
		presetDescLabel,
		widget.NewSeparator(),
		metersBox,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Enabled Voice Model Packs (Multiple Allowed • Downloaded on Demand):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		modelPackRows,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("CPU & Memory Tuning:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		threadsRow,
		idleRow,
	)

	return widget.NewCard("TTS Engine, Voice Packs & Hardware Presets", "Configure voice model packs, CPU/GPU tradeoffs, and RAM footprint", cardContent)
}

func (v *SettingsView) formatModelStatusText() string {
	cfg := v.currentConfig()
	enabled := cfg.TTS.EffectiveEnabledModels()
	names := make([]string, 0, len(enabled))
	for _, id := range enabled {
		if spec, ok := tts.ModelSpecByID(id); ok {
			names = append(names, spec.ShortName)
		} else {
			names = append(names, id)
		}
	}
	mode := "CPU INT8"
	if cfg.TTS.UseGPU && slices.Contains(enabled, tts.ModelKokoro) {
		mode = "DirectML GPU (Kokoro) + CPU"
	}
	return fmt.Sprintf("Active Packs: %s  •  Mode: %s", strings.Join(names, ", "), mode)
}

func (v *SettingsView) missingModelFamilies(enabledModels []string, useGPU bool) []string {
	var missing []string
	for _, fam := range enabledModels {
		if fam == tts.ModelKokoro || fam == tts.ModelKokoroIntl {
			if !tts.IsKokoroInstalledForMode(v.modelDir, useGPU) {
				if !slices.Contains(missing, tts.ModelKokoro) {
					missing = append(missing, tts.ModelKokoro)
				}
			}
			continue
		}
		if !tts.IsModelFamilyInstalled(v.modelDir, fam, useGPU) {
			if !slices.Contains(missing, fam) {
				missing = append(missing, fam)
			}
		}
	}
	return missing
}

func (v *SettingsView) rollbackTTSConfig(prevModels []string, prevPreset string, prevUseGPU bool, threads, maxClause, idleMin int) {
	if len(prevModels) == 0 {
		prevModels = []string{tts.ModelKokoro}
	}
	v.updateConfig(func(c *config.Config) {
		c.TTS.EnabledModels = slices.Clone(prevModels)
		if prevPreset != "" {
			c.TTS.Preset = prevPreset
		}
		c.TTS.UseGPU = prevUseGPU
	})

	fyne.Do(func() {
		v.mu.Lock()
		syncFn := v.syncTTSControls
		v.mu.Unlock()
		if syncFn != nil {
			syncFn()
		}
		if v.onModelsChanged != nil {
			v.onModelsChanged()
		}
	})

	eng, err := tts.NewMultiEngine(v.modelDir, prevModels, threads, prevUseGPU, maxClause, idleMin)
	fyne.Do(func() {
		v.mu.Lock()
		if err == nil && eng != nil {
			v.engine = eng
			v.prevModels = slices.Clone(prevModels)
			v.prevPreset = prevPreset
			v.prevUseGPU = prevUseGPU
		}
		v.mu.Unlock()
		if v.session != nil && eng != nil {
			v.session.SetEngine(eng)
		}
	})
}

func (v *SettingsView) reloadTTSEngine(onDoneUI func()) {
	// Skip background model downloads/reloads in headless unit tests where base modelDir is not populated
	if !tts.IsAnyModelFamilyInstalled(v.modelDir) {
		return
	}

	v.mu.Lock()
	v.reloadSeq++
	mySeq := v.reloadSeq
	enabledModels := v.cfg.TTS.EffectiveEnabledModels()
	useGPU := v.cfg.TTS.UseGPU
	threads := v.cfg.TTS.NumThreads
	if threads <= 0 {
		threads = 4
	}
	maxClause := v.cfg.TTS.MaxClauseWords
	if maxClause <= 0 {
		maxClause = tts.DefaultMaxClauseWords
	}
	idleMin := v.cfg.TTS.IdleUnloadMinutes

	prevModels := slices.Clone(v.prevModels)
	prevPreset := v.prevPreset
	prevUseGPU := v.prevUseGPU
	v.mu.Unlock()

	missing := v.missingModelFamilies(enabledModels, useGPU)

	cancelCtx, cancelFn := context.WithCancel(context.Background())

	var modal *ModelDownloadModal
	if len(missing) > 0 && v.window != nil {
		firstSpec, _ := tts.ModelSpecByID(missing[0])
		modal = NewModelDownloadModal(v.window, firstSpec.ShortName, firstSpec.DownloadSizeMB, func() {
			cancelFn()
		})
	}

	go func() {
		defer cancelFn()

		// 1. Detach and close the existing engine FIRST so old and new models are never held in RAM simultaneously
		v.mu.Lock()
		if v.reloadSeq != mySeq {
			v.mu.Unlock()
			if modal != nil {
				modal.Dismiss()
			}
			return
		}
		oldEng := v.engine
		v.engine = nil
		v.mu.Unlock()

		if v.session != nil {
			v.session.SetEngine(nil)
		}
		if oldEng != nil {
			_ = oldEng.Close()
		}
		tts.ReclaimOSMemory()

		// 2. Download any missing enabled model families
		if len(missing) > 0 {
			for idx, fam := range missing {
				v.mu.Lock()
				canceled := v.reloadSeq != mySeq
				v.mu.Unlock()
				if canceled || cancelCtx.Err() != nil {
					if modal != nil {
						modal.Dismiss()
					}
					v.rollbackTTSConfig(prevModels, prevPreset, prevUseGPU, threads, maxClause, idleMin)
					return
				}

				spec, _ := tts.ModelSpecByID(fam)
				if modal != nil {
					modal.SetStep(idx+1, len(missing), spec.ShortName, spec.DownloadSizeMB)
				}

				onProg := func(cur, tot int64) {
					if modal != nil {
						modal.UpdateProgress(cur, tot)
					} else if tot > 0 {
						v.setStatus(fmt.Sprintf("Downloading %s (%d%%)...", spec.ShortName, cur*100/tot))
					}
				}

				var dlErr error
				if fam == tts.ModelKokoro || fam == tts.ModelKokoroIntl {
					v.setStatus("Downloading Kokoro-82M model files...")
					dlErr = tts.DownloadKokoroModelForModeWithContext(cancelCtx, v.modelDir, useGPU, onProg)
				} else {
					v.setStatus(fmt.Sprintf("Downloading %s (~%d MB)...", spec.ShortName, spec.DownloadSizeMB))
					dlErr = tts.DownloadModelFamilyWithContext(cancelCtx, v.modelDir, fam, useGPU, onProg)
				}

				if dlErr != nil {
					if modal != nil {
						modal.Dismiss()
					}
					if errors.Is(dlErr, context.Canceled) || cancelCtx.Err() != nil {
						v.setStatus("Voice model download cancelled.")
					} else {
						v.setStatus(fmt.Sprintf("⚠️ Failed to download %s: %v", spec.ShortName, dlErr))
						fyne.Do(func() {
							if v.window != nil {
								dialog.ShowError(fmt.Errorf("failed to download %s: %w", spec.ShortName, dlErr), v.window)
							}
						})
					}
					v.rollbackTTSConfig(prevModels, prevPreset, prevUseGPU, threads, maxClause, idleMin)
					return
				}
			}
		}

		if modal != nil {
			modal.Dismiss()
		}

		v.mu.Lock()
		if v.reloadSeq != mySeq {
			v.mu.Unlock()
			return
		}
		v.mu.Unlock()

		v.setStatus("Reloading neural TTS engine(s)...")
		eng, err := tts.NewMultiEngine(v.modelDir, enabledModels, threads, useGPU, maxClause, idleMin)

		fyne.Do(func() {
			v.mu.Lock()
			if v.reloadSeq != mySeq {
				v.mu.Unlock()
				if eng != nil {
					_ = eng.Close()
				}
				return
			}
			if err == nil && eng != nil {
				v.engine = eng
				v.prevModels = slices.Clone(enabledModels)
				v.prevPreset = v.cfg.TTS.Preset
				v.prevUseGPU = useGPU
			}
			v.mu.Unlock()

			if onDoneUI != nil {
				onDoneUI()
			}
			if v.onModelsChanged != nil {
				v.onModelsChanged()
			}

			if err != nil {
				v.setStatus(fmt.Sprintf("Failed to reload TTS engine: %v", err))
			} else {
				if v.session != nil {
					v.session.SetEngine(eng)
				}
				v.setStatus("✓ TTS engine reloaded: " + eng.Provider())
			}
		})
	}()
}

var DuplicateWordsOptions = []string{"Off (Unlimited)", "1", "2 (Default)", "3"}

func DuplicateWordsToOption(val int) string {
	switch val {
	case -1:
		return "Off (Unlimited)"
	case 1:
		return "1"
	case 2, 0:
		return "2 (Default)"
	case 3:
		return "3"
	default:
		if val < 0 {
			return "Off (Unlimited)"
		}
		return strconv.Itoa(val)
	}
}

func OptionToDuplicateWords(opt string) int {
	switch opt {
	case "Off (Unlimited)":
		return -1
	case "1":
		return 1
	case "2 (Default)":
		return 2
	case "3":
		return 3
	default:
		var n int
		if _, err := fmt.Sscanf(opt, "%d", &n); err == nil && n >= 0 {
			return n
		}
		return 2
	}
}

func (v *SettingsView) buildRulesCard() *widget.Card {
	checkNames := widget.NewCheck("Speak Chatter Names (e.g. 'User says...')", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakNames = b
		})
	})
	checkNames.SetChecked(v.cfg.GlobalChatRules.SpeakNames)
	v.rulesNamesCheck = checkNames

	checkEmotes := widget.NewCheck("Speak Emotes & Emojis (unchecked = strip emotes & emojis)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakEmotes = b
		})
	})
	checkEmotes.SetChecked(v.cfg.GlobalChatRules.SpeakEmotes)
	v.rulesEmotesCheck = checkEmotes

	checkDedupe := widget.NewCheck("Deduplicate Emotes & Emojis (speak at most once per sentence)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DeduplicateEmotes = b
		})
	})
	checkDedupe.SetChecked(v.cfg.GlobalChatRules.DeduplicateEmotes)
	v.rulesDedupeCheck = checkDedupe

	dupWordsSelect := widget.NewSelect(DuplicateWordsOptions, func(s string) {
		val := OptionToDuplicateWords(s)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.MaxDuplicateWords = val
		})
	})
	dupWordsSelect.SetSelected(DuplicateWordsToOption(v.cfg.GlobalChatRules.MaxDuplicateWords))
	v.rulesDupWordsSelect = dupWordsSelect
	dupWordsRow := container.NewHBox(
		widget.NewLabel("Max Consecutive Duplicate Words:"),
		dupWordsSelect,
	)

	checkPlatform := widget.NewCheck("Speak Platform Name Prefix (e.g. '[Twitch]')", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakPlatformName = b
		})
	})
	checkPlatform.SetChecked(v.cfg.GlobalChatRules.SpeakPlatformName)
	v.rulesPlatformCheck = checkPlatform

	cardContent := container.NewVBox(
		checkNames,
		checkEmotes,
		checkDedupe,
		dupWordsRow,
		checkPlatform,
	)

	return widget.NewCard("Global Chat Rules", "Default reading behavior across all connected platforms", cardContent)
}

func (v *SettingsView) buildSafetyCard() *widget.Card {
	curHotkey := v.cfg.Safety.PanicHotkey
	if curHotkey == "" {
		curHotkey = "Ctrl+Shift+M"
	}
	parsedSpec, parseErr := ParseHotkey(curHotkey)
	if parseErr != nil {
		parsedSpec, _ = ParseHotkey("Ctrl+Shift+M")
	}

	hotkeyDisplay := widget.NewLabelWithStyle(
		fmt.Sprintf("System-Wide Panic Hotkey: %s (plus Esc when TownCrier window is focused)", parsedSpec.String()),
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	hotkeyDisplay.Wrapping = fyne.TextWrapWord

	var syncingHotkeyUI bool

	ctrlCheck := widget.NewCheck("Ctrl", nil)
	shiftCheck := widget.NewCheck("Shift", nil)
	altCheck := widget.NewCheck("Alt", nil)
	keySelect := widget.NewSelect(SupportedHotkeyKeys, nil)

	v.hotkeyCtrlCheck = ctrlCheck
	v.hotkeyShiftCheck = shiftCheck
	v.hotkeyAltCheck = altCheck
	v.hotkeyKeySelect = keySelect

	syncWidgetsFromSpec := func(spec HotkeySpec) {
		syncingHotkeyUI = true
		ctrlCheck.SetChecked(spec.Ctrl)
		shiftCheck.SetChecked(spec.Shift)
		altCheck.SetChecked(spec.Alt)
		keySelect.SetSelected(spec.Key)
		hotkeyDisplay.SetText(fmt.Sprintf("System-Wide Panic Hotkey: %s (plus Esc when TownCrier window is focused)", spec.String()))
		syncingHotkeyUI = false
	}

	applyNewHotkey := func(candidate string) {
		spec, err := ParseHotkey(candidate)
		if err != nil {
			v.setStatus("⚠️ Invalid hotkey: " + err.Error())
			return
		}
		canonical := spec.String()
		v.updateConfig(func(c *config.Config) {
			c.Safety.PanicHotkey = canonical
		})
		syncWidgetsFromSpec(spec)
		if v.onHotkeyChanged != nil {
			if regErr := v.onHotkeyChanged(canonical); regErr != nil {
				v.setStatus(fmt.Sprintf("⚠️ Hotkey saved (%s), but OS registration failed: %v", canonical, regErr))
				return
			}
		}
		v.setStatus("✓ Global Panic Mute hotkey set to " + canonical)
	}

	onComboChanged := func() {
		if v.initializing || syncingHotkeyUI {
			return
		}
		tempSpec := HotkeySpec{
			Ctrl:  ctrlCheck.Checked,
			Shift: shiftCheck.Checked,
			Alt:   altCheck.Checked,
			Key:   keySelect.Selected,
		}
		applyNewHotkey(tempSpec.String())
	}

	syncWidgetsFromSpec(parsedSpec)

	ctrlCheck.OnChanged = func(bool) { onComboChanged() }
	shiftCheck.OnChanged = func(bool) { onComboChanged() }
	altCheck.OnChanged = func(bool) { onComboChanged() }
	keySelect.OnChanged = func(string) { onComboChanged() }

	recordBtn := widget.NewButtonWithIcon("Record Keypress...", theme.MediaRecordIcon(), func() {
		v.showRecordHotkeyDialog(parsedSpec, func(recorded string) {
			applyNewHotkey(recorded)
		})
	})
	v.hotkeyRecordBtn = recordBtn

	resetHotkeyBtn := widget.NewButtonWithIcon("Reset Default", theme.ViewRefreshIcon(), func() {
		applyNewHotkey("Ctrl+Shift+M")
	})
	v.hotkeyResetBtn = resetHotkeyBtn

	hotkeyControlsRow := container.NewHBox(
		widget.NewLabel("Modifiers:"),
		ctrlCheck,
		shiftCheck,
		altCheck,
		widget.NewLabel("Key:"),
		keySelect,
		recordBtn,
		resetHotkeyBtn,
	)

	// --- System-Wide TTS Mode Toggle Hotkey ---
	curModeHotkey := v.cfg.Safety.ModeToggleHotkey
	if curModeHotkey == "" {
		curModeHotkey = config.DefaultModeToggleHotkey
	}
	parsedModeSpec, parseModeErr := ParseHotkey(curModeHotkey)
	if parseModeErr != nil {
		parsedModeSpec, _ = ParseHotkey(config.DefaultModeToggleHotkey)
	}

	modeHotkeyDisplay := widget.NewLabelWithStyle(
		fmt.Sprintf("System-Wide TTS Mode Toggle Hotkey: %s (cycles Always On → Auto-AFK → Always Off)", parsedModeSpec.String()),
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	modeHotkeyDisplay.Wrapping = fyne.TextWrapWord

	var syncingModeHotkeyUI bool

	modeCtrlCheck := widget.NewCheck("Ctrl", nil)
	modeShiftCheck := widget.NewCheck("Shift", nil)
	modeAltCheck := widget.NewCheck("Alt", nil)
	modeKeySelect := widget.NewSelect(SupportedHotkeyKeys, nil)

	v.modeHotkeyCtrlCheck = modeCtrlCheck
	v.modeHotkeyShiftCheck = modeShiftCheck
	v.modeHotkeyAltCheck = modeAltCheck
	v.modeHotkeyKeySelect = modeKeySelect

	syncModeWidgetsFromSpec := func(spec HotkeySpec) {
		syncingModeHotkeyUI = true
		modeCtrlCheck.SetChecked(spec.Ctrl)
		modeShiftCheck.SetChecked(spec.Shift)
		modeAltCheck.SetChecked(spec.Alt)
		modeKeySelect.SetSelected(spec.Key)
		modeHotkeyDisplay.SetText(fmt.Sprintf("System-Wide TTS Mode Toggle Hotkey: %s (cycles Always On → Auto-AFK → Always Off)", spec.String()))
		syncingModeHotkeyUI = false
	}

	applyNewModeHotkey := func(candidate string) {
		spec, err := ParseHotkey(candidate)
		if err != nil {
			v.setStatus("⚠️ Invalid mode toggle hotkey: " + err.Error())
			return
		}
		canonical := spec.String()
		v.updateConfig(func(c *config.Config) {
			c.Safety.ModeToggleHotkey = canonical
		})
		syncModeWidgetsFromSpec(spec)
		if v.onModeToggleHotkeyChanged != nil {
			if regErr := v.onModeToggleHotkeyChanged(canonical); regErr != nil {
				v.setStatus(fmt.Sprintf("⚠️ Mode hotkey saved (%s), but OS registration failed: %v", canonical, regErr))
				return
			}
		}
		v.setStatus("✓ Global TTS Mode Toggle hotkey set to " + canonical)
	}

	onModeComboChanged := func() {
		if v.initializing || syncingModeHotkeyUI {
			return
		}
		tempSpec := HotkeySpec{
			Ctrl:  modeCtrlCheck.Checked,
			Shift: modeShiftCheck.Checked,
			Alt:   modeAltCheck.Checked,
			Key:   modeKeySelect.Selected,
		}
		applyNewModeHotkey(tempSpec.String())
	}

	syncModeWidgetsFromSpec(parsedModeSpec)

	modeCtrlCheck.OnChanged = func(bool) { onModeComboChanged() }
	modeShiftCheck.OnChanged = func(bool) { onModeComboChanged() }
	modeAltCheck.OnChanged = func(bool) { onModeComboChanged() }
	modeKeySelect.OnChanged = func(string) { onModeComboChanged() }

	modeRecordBtn := widget.NewButtonWithIcon("Record Keypress...", theme.MediaRecordIcon(), func() {
		v.showRecordHotkeyDialog(parsedModeSpec, func(recorded string) {
			applyNewModeHotkey(recorded)
		})
	})
	v.modeHotkeyRecordBtn = modeRecordBtn

	modeResetHotkeyBtn := widget.NewButtonWithIcon("Reset Default", theme.ViewRefreshIcon(), func() {
		applyNewModeHotkey(config.DefaultModeToggleHotkey)
	})
	v.modeHotkeyResetBtn = modeResetHotkeyBtn

	modeHotkeyControlsRow := container.NewHBox(
		widget.NewLabel("Modifiers:"),
		modeCtrlCheck,
		modeShiftCheck,
		modeAltCheck,
		widget.NewLabel("Key:"),
		modeKeySelect,
		modeRecordBtn,
		modeResetHotkeyBtn,
	)

	wordListContainer := container.NewVBox()

	var refreshWords func()
	refreshWords = func() {
		wordListContainer.Objects = nil
		v.wordDeleteBtns = nil
		if len(v.cfg.Safety.BlockedWords) == 0 {
			wordListContainer.Add(widget.NewLabelWithStyle("No words currently blocked.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
		} else {
			for _, w := range v.cfg.Safety.BlockedWords {
				word := w
				delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
					v.updateConfig(func(c *config.Config) {
						var filtered []string
						for _, item := range c.Safety.BlockedWords {
							if item != word {
								filtered = append(filtered, item)
							}
						}
						c.Safety.BlockedWords = filtered
					})
					refreshWords()
					v.setStatus(fmt.Sprintf("Removed %q from blocklist.", word))
				})
				delBtn.Importance = widget.LowImportance
				if !v.ttsEnabled {
					delBtn.Disable()
				}
				v.wordDeleteBtns = append(v.wordDeleteBtns, delBtn)
				row := container.NewBorder(nil, nil, widget.NewLabel("• "+word), delBtn)
				wordListContainer.Add(row)
			}
		}
		wordListContainer.Refresh()
	}

	refreshWords()

	newWordEntry := NewScrollPassEntry()
	newWordEntry.SetPlaceHolder("Enter word or pattern to block (e.g. slurs, bad words)")
	v.newWordEntry = newWordEntry

	addBtn := widget.NewButtonWithIcon("Block Word", theme.ContentAddIcon(), func() {
		text := strings.TrimSpace(newWordEntry.Text)
		if text == "" {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Safety.BlockedWords = append(c.Safety.BlockedWords, text)
		})
		newWordEntry.SetText("")
		refreshWords()
		v.setStatus(fmt.Sprintf("Added %q to blocklist.", text))
	})
	v.addWordBtn = addBtn

	addRow := container.NewBorder(nil, nil, nil, addBtn, newWordEntry)

	defaultBadWordsCheck := widget.NewCheck("Block Default Bad Words (built-in stream-safe profanity & slur list)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.Safety.BlockDefaultBadWords = b
		})
	})
	defaultBadWordsCheck.SetChecked(v.cfg.Safety.BlockDefaultBadWords)
	v.defaultBadWordsCheck = defaultBadWordsCheck

	cardContent := container.NewVBox(
		hotkeyDisplay,
		hotkeyControlsRow,
		widget.NewSeparator(),
		modeHotkeyDisplay,
		modeHotkeyControlsRow,
		widget.NewSeparator(),
		defaultBadWordsCheck,
		widget.NewLabel("Custom Blocked Words (masked with '***' before TTS):"),
		wordListContainer,
		addRow,
	)

	return widget.NewCard("Streamer Safety & Blocklist", "System-wide emergency hotkey and profanity filters", cardContent)
}

func (v *SettingsView) showRecordHotkeyDialog(initial HotkeySpec, onSave func(hotkey string)) {
	recCtrl := widget.NewCheck("Ctrl", nil)
	recCtrl.SetChecked(initial.Ctrl)
	recShift := widget.NewCheck("Shift", nil)
	recShift.SetChecked(initial.Shift)
	recAlt := widget.NewCheck("Alt", nil)
	recAlt.SetChecked(initial.Alt)

	capturedKey := initial.Key
	promptLabel := widget.NewLabelWithStyle(
		fmt.Sprintf("Press any key (A-Z, 0-9, F1-F12, Space...) — Current key: [%s]", capturedKey),
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	prevTypedKey := v.window.Canvas().OnTypedKey()
	restoreHook := func() {
		v.window.Canvas().SetOnTypedKey(prevTypedKey)
	}

	v.window.Canvas().SetOnTypedKey(func(ke *fyne.KeyEvent) {
		if canonical, ok := CanonicalKeyFromFyne(ke.Name); ok {
			capturedKey = canonical
			// If a single character key was pressed with no modifiers checked, default to Ctrl+Shift
			if len(capturedKey) == 1 && !recCtrl.Checked && !recShift.Checked && !recAlt.Checked {
				recCtrl.SetChecked(true)
				recShift.SetChecked(true)
			}
			promptLabel.SetText(fmt.Sprintf("Captured Key: [%s]", capturedKey))
		}
	})

	modRow := container.NewHBox(widget.NewLabel("Modifiers:"), recCtrl, recShift, recAlt)
	content := container.NewVBox(
		modRow,
		promptLabel,
	)

	dialog.ShowCustomConfirm("Record Panic Hotkey", "Save Hotkey", "Cancel", content, func(confirmed bool) {
		restoreHook()
		if !confirmed {
			return
		}
		spec := HotkeySpec{
			Ctrl:  recCtrl.Checked,
			Shift: recShift.Checked,
			Alt:   recAlt.Checked,
			Key:   capturedKey,
		}
		onSave(spec.String())
	}, v.window)
}

func (v *SettingsView) buildBotsCard() *widget.Card {
	knownBotsCheck := widget.NewCheck("Ignore Known Stream Bots (Nightbot, StreamElements, Fossabot, etc.)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.Bots.IgnoreKnownBots = b
		})
	})
	knownBotsCheck.SetChecked(v.cfg.Bots.IgnoreKnownBots)

	botListContainer := container.NewVBox()
	var refreshBots func()
	refreshBots = func() {
		v.currentConfig()
		botListContainer.Objects = nil
		if len(v.cfg.Bots.CustomBots) == 0 {
			botListContainer.Add(widget.NewLabelWithStyle("No custom bot accounts added.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
		} else {
			for i, botName := range v.cfg.Bots.CustomBots {
				idx := i
				bName := botName
				delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
					v.updateConfig(func(c *config.Config) {
						if idx >= 0 && idx < len(c.Bots.CustomBots) {
							c.Bots.CustomBots = append(c.Bots.CustomBots[:idx], c.Bots.CustomBots[idx+1:]...)
						}
					})
					refreshBots()
					v.setStatus(fmt.Sprintf("Removed %q from custom bots.", bName))
				})
				row := container.NewBorder(nil, nil, nil, delBtn, widget.NewLabel(bName))
				botListContainer.Add(row)
			}
		}
		botListContainer.Refresh()
	}
	refreshBots()

	newBotEntry := NewScrollPassEntry()
	newBotEntry.SetPlaceHolder("Enter custom bot username (e.g. mychannelbot)...")
	addBotBtn := widget.NewButtonWithIcon("Add Bot", theme.ContentAddIcon(), func() {
		text := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(newBotEntry.Text), "@"))
		if text == "" {
			return
		}
		v.updateConfig(func(c *config.Config) {
			if !slices.Contains(c.Bots.CustomBots, text) {
				c.Bots.CustomBots = append(c.Bots.CustomBots, text)
			}
		})
		newBotEntry.SetText("")
		refreshBots()
		v.setStatus(fmt.Sprintf("Added %q to custom bots.", text))
	})

	addRow := container.NewBorder(nil, nil, nil, addBotBtn, newBotEntry)

	cardContent := container.NewVBox(
		knownBotsCheck,
		widget.NewSeparator(),
		widget.NewLabel("Custom Bot Usernames (ignored by TTS and hidden when 'Hide Bots' is active):"),
		botListContainer,
		addRow,
	)

	return widget.NewCard("Bot Filters", "Filter out automated stream bots", cardContent)
}
