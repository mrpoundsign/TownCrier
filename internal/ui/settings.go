package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
	"github.com/mrpoundsign/TownCrier/pkg/audio"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/translate"
	"github.com/mrpoundsign/TownCrier/pkg/tts"
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
	captureDeviceEnum func() ([]audio.DeviceInfo, error)
	statusLabel       *widget.Label
	onStatus                  func(msg string)
	onHotkeyChanged           func(hotkey string) error
	onModeToggleHotkeyChanged func(hotkey string) error
	onTTSOutputModeChanged    func(mode string)
	onServicesChanged         func()
	onModelsChanged           func()
	onAssignVoice             func(platform, username string)
	subTabs           *container.AppTabs
	generalTabItem     *container.TabItem
	ttsTabItem         *container.TabItem
	translationTabItem *container.TabItem
	rulesTabItem       *container.TabItem
	logsTabItem        *container.TabItem
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
	micGateModeSelect *widget.Select
	micDeviceSelect   *widget.Select
	micDeviceRefreshBtn *widget.Button
	micDeviceMap      map[string]string // capture device name -> hex ID
	micThresholdSlider *widget.Slider
	micThresholdLabel  *widget.Label
	micDuckSlider      *widget.Slider
	micDuckLabel       *widget.Label
	micReleaseSlider   *widget.Slider
	micReleaseLabel    *widget.Label
	micLevelBar        *widget.ProgressBar
	micGateStatusLabel *widget.Label
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
	enableTTSCheck            *widget.Check
	enableLogCheck            *widget.Check
	enableTranslationCheck    *widget.Check
	checkUpdatesCheck         *widget.Check
	translationTargetSelect   *widget.Select
	translationConfSelect     *widget.Select
	translationRoutingSelect  *widget.Select
	translationNodesBox       *fyne.Container
	translationNodeHoverLbl   *widget.Label
	translationRefreshNodes   func()
	translationSelectedNode      string
	translationConfigDrawer      *fyne.Container
	translationDoneConfigBtn     *widget.Button
	translationProviderSelect    *widget.Select
	translationLimitLabel        *widget.Label
	translationGuideLink         *widget.Hyperlink
	translationKeyLink           *widget.Hyperlink
	translationTOSLink           *widget.Hyperlink
	translationQuotaLink         *widget.Hyperlink
	translationQuotaSep          *widget.Label
	translationEmailEntry        *ScrollPassEntry
	translationEndpointEntry     *ScrollPassEntry
	translationModelEntry        *ScrollPassEntry
	translationAPIKeyEntry       *ScrollPassEntry
	translationTestEntry         *ScrollPassEntry
	translationTestBtn           *widget.Button
	translationTestResultLbl     *widget.Label
	translationCacheStatsLbl     *widget.Label
	translationClearCacheBtn     *widget.Button
	translationRefreshCacheStats func()
	translationTooltipLayer      *fyne.Container
	translationTooltipBubble     *fyne.Container
	translationTooltipText       *canvas.Text
	translationEmailBox          *fyne.Container
	translationEndpointBox    *fyne.Container
	translationModelBox       *fyne.Container
	translationAPIKeyBox      *fyne.Container
	translationChimeCheck     *widget.Check
	translationChimeEntry     *ScrollPassEntry
	translationChimeBrowseBtn *widget.Button
	translationChimeClearBtn  *widget.Button
	translationChimeTestBtn   *widget.Button
	globalVoiceCard      *widget.Card
	audioCard            *widget.Card
	presetSelect         *widget.Select
	gpuCheck             *widget.Check
	threadsSlider        *widget.Slider
	idleSelect              *widget.Select
	modelChecks             map[string]*widget.Check
	modelUpdateStatuses     map[string]tts.ModelUpdateStatus
	modelUpdatesStatusLbl   *widget.Label
	checkModelUpdatesBtn    *widget.Button
	updateModelsBtn         *widget.Button
	activeReplacementWizard *VoiceReplacementWizard
	deviceRefreshBtn        *widget.Button
	rulesNamesCheck                  *widget.Check
	rulesNaturalizeCheck             *widget.Check
	rulesCoalesceSelect              *widget.Select
	rulesEmotesCheck                 *widget.Check
	rulesDedupeCheck                  *widget.Check
	rulesDupWordsSelect               *widget.Select
	rulesStripZalgoCheck              *widget.Check
	rulesStripASCIIArtCheck           *widget.Check
	rulesCopypastaSelect              *widget.Select
	rulesPlatformCheck                *widget.Check
	rulesMentionsModeSelect           *widget.Select
	rulesAllowBroadcasterCheck        *widget.Check
	rulesPlayAlertIgnoredMentionCheck *widget.Check
	rulesHashVoicesCheck              *widget.Check
	rulesSmartGenderCheck             *widget.Check
	rulesUnknownGenderSelect          *widget.Select
	rulesPermittedVoicesLabel         *widget.Label
	rulesPermittedVoicesBtn           *widget.Button
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
	knownBotsCheck       *widget.Check
	refreshWords         func()
	refreshBots          func()
	exportBackupBtn      *widget.Button
	importBackupBtn      *widget.Button
	schemaSnapshotsBtn   *widget.Button
	onBackupApplied      func(newCfg *config.Config, dbRestored bool)
	ttsEnabled           bool
	loggingEnabled       bool
	translationEnabled   bool
}

// NewSettingsView constructs a SettingsView for configuring audio, TTS hardware, and global rules.
func NewSettingsView(w fyne.Window, cfg *config.Config, player audio.Player, engine tts.Engine) *SettingsView {
	ttsOn := true
	loggingOn := true
	translationOn := false
	if cfg != nil {
		ttsOn = cfg.TTS.Enabled
		loggingOn = cfg.Logging.Enabled
		translationOn = cfg.Translation.Enabled
	}
	return &SettingsView{
		window:             w,
		cfg:                cfg,
		player:             player,
		engine:             engine,
		modelDir:           tts.DefaultModelDir(),
		deviceMap:          make(map[string]string),
		deviceEnum:         audio.EnumeratePlaybackDevices,
		micDeviceMap:       make(map[string]string),
		captureDeviceEnum:  audio.EnumerateCaptureDevices,
		prevModels:         slices.Clone(cfg.TTS.EffectiveEnabledModels()),
		prevPreset:         cfg.TTS.Preset,
		prevUseGPU:         cfg.TTS.UseGPU,
		ttsEnabled:         ttsOn,
		loggingEnabled:     loggingOn,
		translationEnabled: translationOn,
		modelChecks:        make(map[string]*widget.Check),
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

// SetCaptureDeviceEnumerator overrides the microphone capture device enumeration function (useful for tests or mocking).
func (v *SettingsView) SetCaptureDeviceEnumerator(fn func() ([]audio.DeviceInfo, error)) {
	v.captureDeviceEnum = fn
}

// SetModelDir overrides the base TTS model directory (useful for tests or custom model paths).
func (v *SettingsView) SetModelDir(dir string) {
	v.mu.Lock()
	v.modelDir = dir
	v.mu.Unlock()
}

// SetSession attaches the application session to coordinate engine reloads.
func (v *SettingsView) SetSession(s *app.Session) {
	v.session = s
	if s != nil {
		s.SetOnMicLevel(v.SyncMicLevel)
	}
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

// SetBackupAppliedHandler registers a callback invoked when a backup or schema snapshot is applied.
func (v *SettingsView) SetBackupAppliedHandler(fn func(newCfg *config.Config, dbRestored bool)) {
	v.onBackupApplied = fn
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
	if v.translationEnabled && v.translationTabItem != nil {
		items = append(items, v.translationTabItem)
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
		if v.rulesPermittedVoicesLabel != nil {
			v.rulesPermittedVoicesLabel.SetText(FormatPermittedVoicesSummary(cfg.TTS.EffectiveEnabledModels(), cfg.GlobalChatRules.DisabledChatterVoices))
		}
		v.refreshPlatformOverrides()
	})
}

func (v *SettingsView) refreshPlatformOverrides() {
	if v.twitchTab != nil && v.twitchTab.overrides != nil {
		v.twitchTab.overrides.Refresh()
	}
	if v.youtubeTab != nil && v.youtubeTab.overrides != nil {
		v.youtubeTab.overrides.Refresh()
	}
	if v.tiktokTab != nil && v.tiktokTab.overrides != nil {
		v.tiktokTab.overrides.Refresh()
	}
	if v.kickTab != nil && v.kickTab.overrides != nil {
		v.kickTab.overrides.Refresh()
	}
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
	v.syncMicGateControlsFromConfig()
	if v.globalVoiceField != nil {
		v.globalVoiceField.SetVoiceAndSpeed(v.cfg.GlobalChatRules.DefaultVoice, v.cfg.GlobalChatRules.DefaultSpeed)
	}
	if v.rulesNamesCheck != nil {
		v.rulesNamesCheck.SetChecked(v.cfg.GlobalChatRules.SpeakNames)
	}
	if v.rulesNaturalizeCheck != nil {
		v.rulesNaturalizeCheck.SetChecked(v.cfg.GlobalChatRules.NaturalizeNames)
	}
	if v.rulesCoalesceSelect != nil {
		v.rulesCoalesceSelect.SetSelected(CoalesceSecondsToOption(v.cfg.GlobalChatRules.CoalesceConsecutiveSeconds))
		if v.cfg.GlobalChatRules.SpeakNames {
			v.rulesCoalesceSelect.Enable()
		} else {
			v.rulesCoalesceSelect.Disable()
		}
	}
	if v.rulesEmotesCheck != nil {
		v.rulesEmotesCheck.SetChecked(v.cfg.GlobalChatRules.SpeakEmotes)
	}
	if v.rulesDedupeCheck != nil {
		v.rulesDedupeCheck.SetChecked(v.cfg.GlobalChatRules.DeduplicateEmotes)
	}
	if v.rulesDupWordsSelect != nil {
		v.rulesDupWordsSelect.SetSelected(DuplicateWordsToOption(v.cfg.GlobalChatRules.MaxDuplicateWords))
	}
	if v.rulesStripZalgoCheck != nil {
		v.rulesStripZalgoCheck.SetChecked(v.cfg.GlobalChatRules.StripZalgo)
	}
	if v.rulesStripASCIIArtCheck != nil {
		v.rulesStripASCIIArtCheck.SetChecked(v.cfg.GlobalChatRules.StripASCIIArt)
	}
	if v.rulesCopypastaSelect != nil {
		v.rulesCopypastaSelect.SetSelected(CopypastaSecondsToOption(v.cfg.GlobalChatRules.DeduplicateCopypastaWindowSeconds))
	}
	if v.rulesPlatformCheck != nil {
		v.rulesPlatformCheck.SetChecked(v.cfg.GlobalChatRules.SpeakPlatformName)
	}
	if v.rulesMentionsModeSelect != nil {
		v.rulesMentionsModeSelect.SetSelected(IgnoreMentionsModeToOption(v.cfg.GlobalChatRules.IgnoreMentionsMode))
	}
	if v.rulesAllowBroadcasterCheck != nil {
		v.rulesAllowBroadcasterCheck.SetChecked(v.cfg.GlobalChatRules.AllowBroadcasterMentions)
		if config.NormalizeIgnoreMentionsMode(v.cfg.GlobalChatRules.IgnoreMentionsMode) != config.IgnoreMentionsOff {
			v.rulesAllowBroadcasterCheck.Enable()
		} else {
			v.rulesAllowBroadcasterCheck.Disable()
		}
	}
	if v.rulesPlayAlertIgnoredMentionCheck != nil {
		v.rulesPlayAlertIgnoredMentionCheck.SetChecked(v.cfg.GlobalChatRules.PlayAlertOnIgnoredMention)
		if config.NormalizeIgnoreMentionsMode(v.cfg.GlobalChatRules.IgnoreMentionsMode) != config.IgnoreMentionsOff {
			v.rulesPlayAlertIgnoredMentionCheck.Enable()
		} else {
			v.rulesPlayAlertIgnoredMentionCheck.Disable()
		}
	}
	if v.rulesHashVoicesCheck != nil {
		v.rulesHashVoicesCheck.SetChecked(v.cfg.GlobalChatRules.HashChatterVoices)
	}
	if v.rulesSmartGenderCheck != nil {
		v.rulesSmartGenderCheck.SetChecked(v.cfg.GlobalChatRules.SmartGenderVoices)
		if v.cfg.GlobalChatRules.HashChatterVoices {
			v.rulesSmartGenderCheck.Enable()
		} else {
			v.rulesSmartGenderCheck.Disable()
		}
	}
	if v.rulesUnknownGenderSelect != nil {
		v.rulesUnknownGenderSelect.SetSelected(UnknownGenderPolicyToOption(v.cfg.GlobalChatRules.UnknownGenderPolicy))
		if v.cfg.GlobalChatRules.HashChatterVoices && v.cfg.GlobalChatRules.SmartGenderVoices {
			v.rulesUnknownGenderSelect.Enable()
		} else {
			v.rulesUnknownGenderSelect.Disable()
		}
	}
	if v.rulesPermittedVoicesLabel != nil {
		v.rulesPermittedVoicesLabel.SetText(FormatPermittedVoicesSummary(v.cfg.TTS.EffectiveEnabledModels(), v.cfg.GlobalChatRules.DisabledChatterVoices))
	}
	if v.rulesPermittedVoicesBtn != nil {
		if v.cfg.GlobalChatRules.HashChatterVoices {
			v.rulesPermittedVoicesBtn.Enable()
		} else {
			v.rulesPermittedVoicesBtn.Disable()
		}
	}
	if v.defaultBadWordsCheck != nil {
		v.defaultBadWordsCheck.SetChecked(v.cfg.Safety.BlockDefaultBadWords)
	}
	if v.knownBotsCheck != nil {
		v.knownBotsCheck.SetChecked(v.cfg.Bots.IgnoreKnownBots)
	}
	if v.refreshWords != nil {
		v.refreshWords()
	}
	if v.refreshBots != nil {
		v.refreshBots()
	}
	if v.syncTTSControls != nil {
		v.syncTTSControls()
	}
	v.refreshPlatformOverrides()
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
	if v.enableTranslationCheck != nil {
		v.translationEnabled = v.cfg.Translation.Enabled
		v.enableTranslationCheck.SetChecked(v.cfg.Translation.Enabled)
	}
	if v.translationTargetSelect != nil {
		v.translationTargetSelect.SetSelected(FormatTranslationTargetLangOption(v.cfg.Translation.TargetLang))
	}
	if v.translationConfSelect != nil {
		v.translationConfSelect.SetSelected(FormatTranslationConfidenceOption(v.cfg.Translation.MinConfidence))
	}
	if v.translationProviderSelect != nil {
		v.translationProviderSelect.SetSelected(FormatTranslationProviderOption(v.cfg.Translation.Provider))
	}
	if v.translationEndpointEntry != nil {
		v.translationEndpointEntry.SetText(v.cfg.Translation.EndpointURL)
	}
	if v.translationModelEntry != nil {
		v.translationModelEntry.SetText(v.cfg.Translation.Model)
	}
	if v.translationAPIKeyEntry != nil {
		keyVal := v.cfg.Translation.APIKey
		if v.session != nil {
			keyVal = v.session.TranslationAPIKey()
		}
		v.translationAPIKeyEntry.SetText(keyVal)
	}
	v.syncTranslationProviderFields()
	v.refreshWSStatusLabel()
	if v.logsView != nil {
		v.logsView.SyncFromConfig()
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
	backupCard := v.buildBackupCard()
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
		backupCard,
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
	v.translationTabItem = container.NewTabItemWithIcon("Translation", resLogTranslated, v.buildTranslationTab())
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

	v.enableTranslationCheck = widget.NewCheck("Enable Chat Translation (Auto-Detect & Translate Foreign Chat)", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Translation.Enabled = checked
		})
		v.translationEnabled = checked
		v.SyncPlatformTabVisibility()
		v.refreshPlatformOverrides()
		if checked {
			v.setStatus("✓ Chat Translation enabled. Configure target language and provider in the Translation tab.")
		} else {
			v.setStatus("Chat Translation disabled.")
		}
	})
	v.enableTranslationCheck.SetChecked(cfg.Translation.Enabled)

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
		v.enableTranslationCheck,
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

	v.streamDeckStatusLabel = widget.NewLabel("")
	v.streamDeckStatusLabel.Wrapping = fyne.TextWrapWord

	refreshStreamDeckUI := func(justInstalledPath string) {
		var st streamdeck.PluginStatus
		if testing.Testing() && v.streamDeckPluginsDir == "" {
			st = streamdeck.PluginStatus{
				Installed:      false,
				BundledVersion: streamdeck.BundledPluginVersion(),
			}
		} else {
			st = streamdeck.CheckPluginStatus(v.streamDeckPluginsDir)
		}
		if !st.Installed {
			v.streamDeckStatusLabel.SetText("Stream Deck Plugin: Not installed (Bundled: v" + st.BundledVersion + ")")
			if v.streamDeckInstallBtn != nil {
				v.streamDeckInstallBtn.SetText("Install Elgato Stream Deck Plugin (v" + st.BundledVersion + ")")
				v.streamDeckInstallBtn.Importance = widget.HighImportance
				v.streamDeckInstallBtn.Refresh()
			}
			return
		}
		if st.NeedsUpdate {
			v.streamDeckStatusLabel.SetText(fmt.Sprintf("⬆ Stream Deck Plugin update available: v%s -> v%s", st.InstalledVersion, st.BundledVersion))
			if v.streamDeckInstallBtn != nil {
				v.streamDeckInstallBtn.SetText("Update Elgato Stream Deck Plugin to v" + st.BundledVersion)
				v.streamDeckInstallBtn.Importance = widget.HighImportance
				v.streamDeckInstallBtn.Refresh()
			}
			return
		}
		if justInstalledPath != "" {
			v.streamDeckStatusLabel.SetText(fmt.Sprintf("✓ Installed Stream Deck plugin v%s to %s (restart Elgato Stream Deck app to load)", st.InstalledVersion, justInstalledPath))
		} else {
			v.streamDeckStatusLabel.SetText("✓ Stream Deck Plugin v" + st.InstalledVersion + " installed (up to date)")
		}
		if v.streamDeckInstallBtn != nil {
			v.streamDeckInstallBtn.SetText("Reinstall Elgato Stream Deck Plugin (v" + st.BundledVersion + ")")
			v.streamDeckInstallBtn.Importance = widget.MediumImportance
			v.streamDeckInstallBtn.Refresh()
		}
	}

	v.streamDeckInstallBtn = widget.NewButtonWithIcon("Install Elgato Stream Deck Plugin", theme.DownloadIcon(), func() {
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
		refreshStreamDeckUI(installedPath)
		v.setStatus("✓ Elgato Stream Deck plugin v" + streamdeck.BundledPluginVersion() + " installed & WebSocket control server enabled.")
	})
	v.streamDeckInstallBtn.Importance = widget.HighImportance
	refreshStreamDeckUI("")

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
			v.refreshPlatformOverrides()
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

	// --- Microphone VAD Gate & Ducking Controls ---
	v.micGateModeSelect = widget.NewSelect(MicGateModeOptions, func(selected string) {
		if v.initializing {
			return
		}
		mode := ParseMicGateModeLabel(selected)
		v.updateConfig(func(c *config.Config) {
			c.Audio.MicGateMode = mode
		})
		v.syncMicGateEnablement(mode)
		v.updateMicGateStatusText(audio.MinCaptureDBFS, false)
		v.setStatus("Microphone VAD Gate mode set to: " + selected)
	})
	v.micGateModeSelect.SetSelected(FormatMicGateModeLabel(v.cfg.Audio.MicGateMode))

	v.micDeviceSelect = widget.NewSelect([]string{"Default Microphone"}, func(selected string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		devHex := v.micDeviceMap[selected]
		v.mu.Unlock()

		v.updateConfig(func(c *config.Config) {
			c.Audio.MicDeviceName = selected
			c.Audio.MicDeviceID = devHex
		})
		v.setStatus("Active microphone input: " + selected)
	})
	v.refreshCaptureDeviceList()

	micRefreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshCaptureDeviceList()
		v.setStatus("Microphone input list refreshed.")
	})
	v.micDeviceRefreshBtn = micRefreshBtn

	initThresh := v.cfg.Audio.MicThresholdDB
	if initThresh == 0 {
		initThresh = config.DefaultMicThresholdDB
	}
	v.micThresholdLabel = widget.NewLabel(fmt.Sprintf("%.0f dB", initThresh))
	v.micThresholdSlider = widget.NewSlider(-55, -10)
	v.micThresholdSlider.Step = 1
	v.micThresholdSlider.SetValue(float64(initThresh))
	v.micThresholdSlider.OnChanged = func(val float64) {
		v.micThresholdLabel.SetText(fmt.Sprintf("%.0f dB", val))
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Audio.MicThresholdDB = float32(val)
		})
	}

	initDuckPct := int(math.Round(float64(v.cfg.Audio.MicDuckLevel) * 100))
	if initDuckPct <= 0 || initDuckPct > 100 {
		initDuckPct = 20
	}
	v.micDuckLabel = widget.NewLabel(fmt.Sprintf("%d%%", initDuckPct))
	v.micDuckSlider = widget.NewSlider(5, 80)
	v.micDuckSlider.Step = 5
	v.micDuckSlider.SetValue(float64(initDuckPct))
	v.micDuckSlider.OnChanged = func(val float64) {
		pct := int(math.Round(val))
		v.micDuckLabel.SetText(fmt.Sprintf("%d%%", pct))
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Audio.MicDuckLevel = float32(pct) / 100.0
		})
	}

	initRelMs := v.cfg.Audio.MicReleaseMs
	if initRelMs <= 0 {
		initRelMs = config.DefaultMicReleaseMs
	}
	v.micReleaseLabel = widget.NewLabel(fmt.Sprintf("%d ms", initRelMs))
	v.micReleaseSlider = widget.NewSlider(100, 2000)
	v.micReleaseSlider.Step = 50
	v.micReleaseSlider.SetValue(float64(initRelMs))
	v.micReleaseSlider.OnChanged = func(val float64) {
		ms := int(math.Round(val))
		v.micReleaseLabel.SetText(fmt.Sprintf("%d ms", ms))
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Audio.MicReleaseMs = ms
		})
	}

	v.micLevelBar = widget.NewProgressBar()
	v.micLevelBar.Min = 0.0
	v.micLevelBar.Max = 1.0
	v.micLevelBar.SetValue(0.0)
	v.micLevelBar.TextFormatter = func() string { return "" }

	v.micGateStatusLabel = widget.NewLabel("")
	v.updateMicGateStatusText(audio.MinCaptureDBFS, false)
	v.syncMicGateEnablement(v.cfg.Audio.MicGateMode)

	micModeRow := container.NewBorder(nil, nil, widget.NewLabel("Mic VAD Gate Mode:"), nil, v.micGateModeSelect)
	micDeviceRow := container.NewBorder(nil, nil, widget.NewLabel("Microphone Input:"), micRefreshBtn, v.micDeviceSelect)
	micThreshRow := container.NewBorder(nil, nil, widget.NewLabel("Voice Threshold:"), v.micThresholdLabel, v.micThresholdSlider)
	micDuckRow := container.NewBorder(nil, nil, widget.NewLabel("Ducked TTS Volume:"), v.micDuckLabel, v.micDuckSlider)
	micReleaseRow := container.NewBorder(nil, nil, widget.NewLabel("Release Hold Delay:"), v.micReleaseLabel, v.micReleaseSlider)
	micMeterRow := container.NewBorder(nil, nil, widget.NewLabel("Live Mic Level:"), v.micGateStatusLabel, v.micLevelBar)

	cardContent := container.NewVBox(
		deviceRow,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Microphone Voice Activity Gate & Ducking", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		micModeRow,
		micDeviceRow,
		micThreshRow,
		micDuckRow,
		micReleaseRow,
		micMeterRow,
	)

	return widget.NewCard("Audio Output & Microphone Gate", "Select WASAPI playback device for OBS routing and configure streamer mic ducking/pausing", cardContent)
}

// MicGateModeOptions lists the display labels for Microphone VAD Gate modes.
var MicGateModeOptions = []string{
	"Off (Disabled)",
	"Duck TTS Volume While Speaking",
	"Pause Speech Between Clauses While Speaking",
}

// FormatMicGateModeLabel converts a canonical MicGateMode string into its UI dropdown label.
func FormatMicGateModeLabel(mode string) string {
	switch config.NormalizeMicGateMode(mode) {
	case config.MicGateModeDuck:
		return "Duck TTS Volume While Speaking"
	case config.MicGateModePause:
		return "Pause Speech Between Clauses While Speaking"
	default:
		return "Off (Disabled)"
	}
}

// ParseMicGateModeLabel converts a UI dropdown label into a canonical MicGateMode string.
func ParseMicGateModeLabel(label string) string {
	switch label {
	case "Duck TTS Volume While Speaking":
		return config.MicGateModeDuck
	case "Pause Speech Between Clauses While Speaking":
		return config.MicGateModePause
	default:
		return config.MicGateModeOff
	}
}

func (v *SettingsView) syncMicGateEnablement(mode string) {
	norm := config.NormalizeMicGateMode(mode)
	activeMode := norm == config.MicGateModeDuck || norm == config.MicGateModePause
	if v.micDeviceSelect != nil {
		if activeMode {
			v.micDeviceSelect.Enable()
		} else {
			v.micDeviceSelect.Disable()
		}
	}
	if v.micThresholdSlider != nil {
		if activeMode {
			v.micThresholdSlider.Enable()
		} else {
			v.micThresholdSlider.Disable()
		}
	}
	if v.micDuckSlider != nil {
		if norm == config.MicGateModeDuck {
			v.micDuckSlider.Enable()
		} else {
			v.micDuckSlider.Disable()
		}
	}
	if v.micReleaseSlider != nil {
		if activeMode {
			v.micReleaseSlider.Enable()
		} else {
			v.micReleaseSlider.Disable()
		}
	}
}

func (v *SettingsView) syncMicGateControlsFromConfig() {
	if v.micGateModeSelect != nil {
		v.micGateModeSelect.SetSelected(FormatMicGateModeLabel(v.cfg.Audio.MicGateMode))
	}
	if v.micThresholdSlider != nil {
		th := v.cfg.Audio.MicThresholdDB
		if th == 0 {
			th = config.DefaultMicThresholdDB
		}
		v.micThresholdSlider.SetValue(float64(th))
		if v.micThresholdLabel != nil {
			v.micThresholdLabel.SetText(fmt.Sprintf("%.0f dB", th))
		}
	}
	if v.micDuckSlider != nil {
		pct := int(math.Round(float64(v.cfg.Audio.MicDuckLevel) * 100))
		if pct <= 0 || pct > 100 {
			pct = 20
		}
		v.micDuckSlider.SetValue(float64(pct))
		if v.micDuckLabel != nil {
			v.micDuckLabel.SetText(fmt.Sprintf("%d%%", pct))
		}
	}
	if v.micReleaseSlider != nil {
		ms := v.cfg.Audio.MicReleaseMs
		if ms <= 0 {
			ms = config.DefaultMicReleaseMs
		}
		v.micReleaseSlider.SetValue(float64(ms))
		if v.micReleaseLabel != nil {
			v.micReleaseLabel.SetText(fmt.Sprintf("%d ms", ms))
		}
	}
	v.syncMicGateEnablement(v.cfg.Audio.MicGateMode)
	v.updateMicGateStatusText(audio.MinCaptureDBFS, false)
}

func (v *SettingsView) updateMicGateStatusText(levelDB float32, active bool) {
	if v.micGateStatusLabel == nil {
		return
	}
	mode := config.NormalizeMicGateMode(v.cfg.Audio.MicGateMode)
	if mode == config.MicGateModeOff {
		v.micGateStatusLabel.SetText("Gate Off")
		return
	}
	if active {
		if mode == config.MicGateModeDuck {
			v.micGateStatusLabel.SetText(fmt.Sprintf("%.0f dB • Ducking", levelDB))
		} else {
			v.micGateStatusLabel.SetText(fmt.Sprintf("%.0f dB • Holding Speech", levelDB))
		}
		return
	}
	v.micGateStatusLabel.SetText(fmt.Sprintf("%.0f dB • Open", levelDB))
}

// SyncMicLevel updates the live microphone input level bar and gate status label on the Fyne UI thread.
func (v *SettingsView) SyncMicLevel(levelDB float32, active bool) {
	fyne.Do(func() {
		if v.micLevelBar != nil {
			norm := (float64(levelDB) - float64(audio.MinCaptureDBFS)) / (0.0 - float64(audio.MinCaptureDBFS))
			if norm < 0.0 {
				norm = 0.0
			} else if norm > 1.0 {
				norm = 1.0
			}
			v.micLevelBar.SetValue(norm)
		}
		v.updateMicGateStatusText(levelDB, active)
	})
}

func (v *SettingsView) refreshCaptureDeviceList() {
	v.mu.Lock()
	enumFn := v.captureDeviceEnum
	if enumFn == nil {
		enumFn = audio.EnumerateCaptureDevices
	}
	v.mu.Unlock()

	devices, err := enumFn()

	options := []string{"Default Microphone"}
	newMap := make(map[string]string)
	newMap["Default Microphone"] = ""

	if err == nil {
		for _, dev := range devices {
			options = append(options, dev.Name)
			newMap[dev.Name] = dev.ID
		}
	}

	v.mu.Lock()
	v.micDeviceMap = newMap
	targetName := v.cfg.Audio.MicDeviceName
	targetID := v.cfg.Audio.MicDeviceID
	v.mu.Unlock()

	if v.micDeviceSelect != nil {
		v.micDeviceSelect.Options = options
		selected := "Default Microphone"
		if targetName != "" {
			for _, opt := range options {
				if opt == targetName {
					selected = opt
					break
				}
			}
		}
		if selected == "Default Microphone" && targetID != "" && err == nil {
			for _, dev := range devices {
				if audio.MatchHexDeviceIDStrings(dev.ID, targetID) {
					selected = dev.Name
					break
				}
			}
		}
		v.micDeviceSelect.SetSelected(selected)
	}
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
	targetID := v.cfg.Audio.DeviceID
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
		if selected == "Default Audio Device" && targetID != "" && err == nil {
			for _, dev := range devices {
				if audio.MatchHexDeviceIDStrings(dev.ID, targetID) {
					selected = dev.Name
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

func (v *SettingsView) buildBackupCard() *widget.Card {
	descLbl := widget.NewLabel(
		"Export a portable .zip backup containing your configuration, chatter voice profiles, and Event Log database (OAuth tokens & API keys are never exported), import a backup to merge or switch configurations, or restore an automatic schema snapshot.",
	)
	descLbl.Wrapping = fyne.TextWrapWord

	applyImportedOrRestored := func(newCfg *config.Config, modelsChanged bool, dbRestored bool) {
		if newCfg != nil {
			v.cfg = newCfg.Clone()
		}
		v.SyncFromConfig()
		v.SyncTTSState(v.cfg.TTS.Enabled)
		v.SyncLoggingState(v.cfg.Logging.Enabled)
		v.SyncPlatformTabVisibility()
		if v.onServicesChanged != nil {
			v.onServicesChanged()
		}
		if v.onModelsChanged != nil {
			v.onModelsChanged()
		}
		if v.onBackupApplied != nil {
			v.onBackupApplied(v.cfg, dbRestored)
		}
		if modelsChanged && !testing.Testing() {
			v.reloadTTSEngine(nil)
		}
	}

	v.exportBackupBtn = widget.NewButtonWithIcon("Export Backup…", theme.DownloadIcon(), func() {
		dlg := NewSupportBackupDialog(v.window, v.session, v.setStatus)
		dlg.Show()
	})
	v.exportBackupBtn.Importance = widget.HighImportance

	v.importBackupBtn = widget.NewButtonWithIcon("Import Backup…", theme.UploadIcon(), func() {
		ShowBackupImportFilePicker(v.window, v.session, func(newCfg *config.Config, modelsChanged bool) {
			applyImportedOrRestored(newCfg, modelsChanged, true)
		}, v.setStatus)
	})

	v.schemaSnapshotsBtn = widget.NewButtonWithIcon("Schema Snapshots…", theme.HistoryIcon(), func() {
		ShowSchemaSnapshotsDialog(v.window, v.session, func(newCfg *config.Config, dbRestored bool) {
			applyImportedOrRestored(newCfg, false, dbRestored)
		}, v.setStatus)
	})

	btnRow := container.NewHBox(
		v.exportBackupBtn,
		v.importBackupBtn,
		v.schemaSnapshotsBtn,
	)

	cardContent := container.NewVBox(
		descLbl,
		btnRow,
	)

	return widget.NewCard("Backup, Restore & Migration", "Export or import .zip backups across PCs or roll back schema snapshots", cardContent)
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

	v.modelUpdateStatuses = tts.InspectAllLocalModelStatuses(v.modelDir, v.currentConfig().TTS.UseGPU)
	v.modelUpdatesStatusLbl = widget.NewLabel("")
	v.modelUpdatesStatusLbl.Wrapping = fyne.TextWrapWord

	formatPackCheckLabel := func(spec tts.ModelFamilySpec) string {
		cfg := v.currentConfig()
		v.mu.Lock()
		st, hasSt := v.modelUpdateStatuses[spec.ID]
		v.mu.Unlock()
		if !hasSt {
			st = tts.InspectLocalModelStatus(v.modelDir, spec.ID, cfg.TTS.UseGPU)
		}

		status := fmt.Sprintf("Downloads ~%d MB on enable", spec.DownloadSizeMB)
		switch {
		case st.UpdateAvailable && spec.ID == tts.ModelKokoroIntl:
			status = "⬆ Update Available (Shares Kokoro)"
		case st.UpdateAvailable:
			status = fmt.Sprintf("⬆ Update Available (~%d MB)", spec.DownloadSizeMB)
		case spec.ID == tts.ModelKokoroIntl && tts.IsModelFamilyInstalled(v.modelDir, tts.ModelKokoro, cfg.TTS.UseGPU):
			status = "Installed (Shares Kokoro)"
		case tts.IsModelFamilyInstalled(v.modelDir, spec.ID, cfg.TTS.UseGPU):
			status = "Installed"
		}
		return fmt.Sprintf("%s [%s]", spec.Name, status)
	}

	syncModelUpdateControls := func() {
		v.mu.Lock()
		outdated := tts.OutdatedModelFamilies(v.modelUpdateStatuses)
		v.mu.Unlock()
		if v.updateModelsBtn != nil {
			if len(outdated) > 0 {
				v.updateModelsBtn.SetText(fmt.Sprintf("Update Voice Models (%d)", len(outdated)))
				v.updateModelsBtn.Enable()
			} else {
				v.updateModelsBtn.SetText("Update Voice Models")
				v.updateModelsBtn.Disable()
			}
		}
		if v.modelUpdatesStatusLbl != nil && v.modelUpdatesStatusLbl.Text == "" && len(outdated) > 0 {
			names := make([]string, 0, len(outdated))
			for _, id := range outdated {
				if spec, ok := tts.ModelSpecByID(id); ok {
					names = append(names, spec.ShortName)
				} else {
					names = append(names, id)
				}
			}
			v.modelUpdatesStatusLbl.SetText(fmt.Sprintf("⬆ %d voice model update(s) available: %s", len(outdated), strings.Join(names, ", ")))
		}
	}

	refreshPackLabels := func() {
		cfg := v.currentConfig()
		localSt := tts.InspectAllLocalModelStatuses(v.modelDir, cfg.TTS.UseGPU)
		v.mu.Lock()
		if v.modelUpdateStatuses == nil {
			v.modelUpdateStatuses = localSt
		} else {
			for k, lst := range localSt {
				if prev, ok := v.modelUpdateStatuses[k]; ok && prev.UpdateAvailable && lst.Installed {
					// Preserve remote-detected update flag until updated
					v.modelUpdateStatuses[k] = prev
				} else {
					v.modelUpdateStatuses[k] = lst
				}
			}
		}
		v.mu.Unlock()

		for _, spec := range tts.AllModelFamilies {
			if chk, ok := modelChecks[spec.ID]; ok {
				chk.SetText(formatPackCheckLabel(spec))
				chk.Refresh()
			}
		}
		if v.modelStatus != nil {
			v.modelStatus.SetText(v.formatModelStatusText())
		}
		syncModelUpdateControls()
	}

	v.checkModelUpdatesBtn = widget.NewButtonWithIcon("Check for Voice Updates", theme.ViewRefreshIcon(), func() {
		cfg := v.currentConfig()
		v.checkModelUpdatesBtn.Disable()
		v.modelUpdatesStatusLbl.SetText("Checking GitHub Releases for voice model updates...")
		v.setStatus("Checking for updated neural TTS voice models...")

		runCheck := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			statuses, err := tts.CheckModelUpdates(ctx, v.modelDir, cfg.TTS.UseGPU)
			applyResult := func() {
				v.checkModelUpdatesBtn.Enable()
				v.mu.Lock()
				v.modelUpdateStatuses = statuses
				v.mu.Unlock()
				outdated := tts.OutdatedModelFamilies(statuses)
				if err != nil && len(outdated) == 0 {
					msg := fmt.Sprintf("⚠️ Could not check remote voice updates: %v", err)
					v.modelUpdatesStatusLbl.SetText(msg)
					v.setStatus(msg)
					refreshPackLabels()
					return
				}
				if len(outdated) > 0 {
					names := make([]string, 0, len(outdated))
					for _, id := range outdated {
						if spec, ok := tts.ModelSpecByID(id); ok {
							names = append(names, spec.ShortName)
						} else {
							names = append(names, id)
						}
					}
					msg := fmt.Sprintf("⬆ %d voice model update(s) available: %s", len(outdated), strings.Join(names, ", "))
					v.modelUpdatesStatusLbl.SetText(msg)
					v.setStatus(msg)
				} else {
					msg := "✓ All installed voice model packs are up to date."
					v.modelUpdatesStatusLbl.SetText(msg)
					v.setStatus(msg)
				}
				refreshPackLabels()
			}
			if testing.Testing() {
				applyResult()
			} else {
				fyne.Do(applyResult)
			}
		}
		if testing.Testing() {
			runCheck()
		} else {
			go runCheck()
		}
	})

	v.updateModelsBtn = widget.NewButtonWithIcon("Update Voice Models", theme.DownloadIcon(), func() {
		v.mu.Lock()
		outdated := tts.OutdatedModelFamilies(v.modelUpdateStatuses)
		v.mu.Unlock()
		if len(outdated) == 0 {
			return
		}
		v.updateOutdatedModels(outdated, refreshPackLabels)
	})
	v.updateModelsBtn.Importance = widget.HighImportance
	syncModelUpdateControls()

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

			if !checked {
				if usages := tts.FindVoiceUsages(cfg, []string{famSpec.ID}); len(usages) > 0 {
					syncingControls = true
					chk.SetChecked(true)
					syncingControls = false
					v.setStatus(fmt.Sprintf("⚠️ %s has %d assigned voice(s) — choose replacement voices to disable.", famSpec.ShortName, len(usages)))
					v.activeReplacementWizard = ShowVoiceReplacementWizard(
						v.window,
						v.session,
						cfg,
						usages,
						selectedModels,
						func(replacements []tts.VoiceReplacement) {
							v.activeReplacementWizard = nil
							syncingControls = true
							chk.SetChecked(false)
							syncingControls = false

							if slices.Contains(selectedModels, tts.ModelKokoro) || slices.Contains(selectedModels, tts.ModelKokoroIntl) {
								gpuCheck.Enable()
							} else {
								gpuCheck.Disable()
							}

							v.updateConfig(func(c *config.Config) {
								tts.ApplyVoiceUsageReplacements(c, usages, replacements)
								c.TTS.Preset = tts.PresetCustom
								c.TTS.EnabledModels = selectedModels
							})
							markCustomPreset()
							updateImpactUI()
							v.RefreshVoices()
							if v.onModelsChanged != nil {
								v.onModelsChanged()
							}
							v.reloadTTSEngine(refreshPackLabels)
						},
						func() {
							v.activeReplacementWizard = nil
							v.setStatus("Model pack removal cancelled.")
						},
						v.onStatus,
					)
					return
				}
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
			v.setStatus(fmt.Sprintf("⚠️ Switching to %s disables a pack with %d assigned voice(s) — choose replacements to continue.", pSpec.Label, len(usages)))
			v.activeReplacementWizard = ShowVoiceReplacementWizard(
				v.window,
				v.session,
				cfg,
				usages,
				pSpec.EnabledModels,
				func(replacements []tts.VoiceReplacement) {
					v.activeReplacementWizard = nil
					presetDescLabel.SetText(pSpec.Description)
					v.updateConfig(func(c *config.Config) {
						tts.ApplyVoiceUsageReplacements(c, usages, replacements)
						c.TTS.Preset = pSpec.ID
						c.TTS.EnabledModels = slices.Clone(pSpec.EnabledModels)
						c.TTS.UseGPU = pSpec.UseGPU
						c.TTS.NumThreads = pSpec.NumThreads
						c.TTS.MaxClauseWords = pSpec.MaxClauseWords
						c.TTS.IdleUnloadMinutes = pSpec.IdleUnloadMinutes
					})

					syncWidgetsFromConfig()
					v.RefreshVoices()
					if v.onModelsChanged != nil {
						v.onModelsChanged()
					}
					v.reloadTTSEngine(refreshPackLabels)
				},
				func() {
					v.activeReplacementWizard = nil
					v.setStatus("Preset change cancelled.")
				},
				v.onStatus,
			)
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

	modelUpdatesActionRow := container.NewHBox(
		v.checkModelUpdatesBtn,
		v.updateModelsBtn,
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
		modelUpdatesActionRow,
		v.modelUpdatesStatusLbl,
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

func (v *SettingsView) updateOutdatedModels(outdated []string, onDoneUI func()) {
	if len(outdated) == 0 {
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
	v.mu.Unlock()

	cancelCtx, cancelFn := context.WithCancel(context.Background())

	var modal *ModelDownloadModal
	if !testing.Testing() && v.window != nil {
		firstSpec, _ := tts.ModelSpecByID(outdated[0])
		modal = NewModelDownloadModal(v.window, firstSpec.ShortName, firstSpec.DownloadSizeMB, func() {
			cancelFn()
		})
	}

	runUpdate := func() {
		defer cancelFn()

		// 1. Detach and close the existing engine FIRST so open ONNX files can be replaced on Windows
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

		// 2. Force-download and replace each outdated model pack
		for idx, fam := range outdated {
			v.mu.Lock()
			canceled := v.reloadSeq != mySeq
			v.mu.Unlock()
			if canceled || cancelCtx.Err() != nil {
				if modal != nil {
					modal.Dismiss()
				}
				return
			}

			spec, _ := tts.ModelSpecByID(fam)
			if modal != nil {
				modal.SetStep(idx+1, len(outdated), spec.ShortName, spec.DownloadSizeMB)
			}

			onProg := func(cur, tot int64) {
				if modal != nil {
					modal.UpdateProgress(cur, tot)
				} else if tot > 0 {
					v.setStatus(fmt.Sprintf("Updating %s (%d%%)...", spec.ShortName, cur*100/tot))
				}
			}

			v.setStatus(fmt.Sprintf("Updating %s (~%d MB)...", spec.ShortName, spec.DownloadSizeMB))
			if dlErr := tts.ForceUpdateModelFamilyWithContext(cancelCtx, v.modelDir, fam, useGPU, onProg); dlErr != nil {
				if modal != nil {
					modal.Dismiss()
				}
				msg := fmt.Sprintf("⚠️ Failed to update %s: %v", spec.ShortName, dlErr)
				v.setStatus(msg)
				finishErrUI := func() {
					if v.modelUpdatesStatusLbl != nil {
						v.modelUpdatesStatusLbl.SetText(msg)
					}
					if onDoneUI != nil {
						onDoneUI()
					}
				}
				if testing.Testing() {
					finishErrUI()
				} else {
					fyne.Do(finishErrUI)
				}
				return
			}
		}

		if modal != nil {
			modal.Dismiss()
		}

		v.mu.Lock()
		v.modelUpdateStatuses = tts.InspectAllLocalModelStatuses(v.modelDir, useGPU)
		v.mu.Unlock()

		var eng tts.Engine
		var engErr error
		if !testing.Testing() {
			v.setStatus("Reloading neural TTS engine(s)...")
			eng, engErr = tts.NewMultiEngine(v.modelDir, enabledModels, threads, useGPU, maxClause, idleMin)
		}

		finishUI := func() {
			v.mu.Lock()
			if engErr == nil && eng != nil {
				v.engine = eng
			}
			v.mu.Unlock()

			if v.session != nil && eng != nil {
				v.session.SetEngine(eng)
			}
			if v.modelUpdatesStatusLbl != nil {
				v.modelUpdatesStatusLbl.SetText("✓ Voice model packs updated to latest release!")
			}
			if onDoneUI != nil {
				onDoneUI()
			}
			v.RefreshVoices()
			if v.onModelsChanged != nil {
				v.onModelsChanged()
			}
			v.setStatus("✓ Voice model packs updated!")
		}
		if testing.Testing() {
			finishUI()
		} else {
			fyne.Do(finishUI)
		}
	}

	if testing.Testing() {
		runUpdate()
	} else {
		go runUpdate()
	}
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

var CoalesceSpeakerOptions = []string{
	"Off (Always Speak Name)",
	"10 seconds",
	"15 seconds",
	"20 seconds (Default)",
	"30 seconds",
	"60 seconds",
}

func CoalesceSecondsToOption(val int) string {
	switch val {
	case -1:
		return "Off (Always Speak Name)"
	case 10:
		return "10 seconds"
	case 15:
		return "15 seconds"
	case 20, 0:
		return "20 seconds (Default)"
	case 30:
		return "30 seconds"
	case 60:
		return "60 seconds"
	default:
		if val < 0 {
			return "Off (Always Speak Name)"
		}
		return fmt.Sprintf("%d seconds", val)
	}
}

func OptionToCoalesceSeconds(opt string) int {
	switch opt {
	case "Off (Always Speak Name)", "off", "-1":
		return -1
	case "10 seconds", "10":
		return 10
	case "15 seconds", "15":
		return 15
	case "20 seconds (Default)", "20":
		return 20
	case "30 seconds", "30":
		return 30
	case "60 seconds", "60":
		return 60
	default:
		var n int
		if _, err := fmt.Sscanf(opt, "%d", &n); err == nil && n > 0 {
			return n
		}
		return config.DefaultCoalesceConsecutiveSeconds
	}
}

var CopypastaWindowOptions = []string{
	"Off (Allow All)",
	"15 seconds",
	"30 seconds (Default)",
	"60 seconds",
}

func CopypastaSecondsToOption(val int) string {
	switch val {
	case -1:
		return "Off (Allow All)"
	case 15:
		return "15 seconds"
	case 30, 0:
		return "30 seconds (Default)"
	case 60:
		return "60 seconds"
	default:
		if val < 0 {
			return "Off (Allow All)"
		}
		return fmt.Sprintf("%d seconds", val)
	}
}

func OptionToCopypastaSeconds(opt string) int {
	switch opt {
	case "Off (Allow All)", "off", "-1":
		return -1
	case "15 seconds", "15":
		return 15
	case "30 seconds (Default)", "30":
		return 30
	case "60 seconds", "60":
		return 60
	default:
		var n int
		if _, err := fmt.Sscanf(opt, "%d", &n); err == nil && n > 0 {
			return n
		}
		return config.DefaultCopypastaWindowSeconds
	}
}

const (
	IgnoreMentionsOptionOff               = "Read All Mentions & Replies"
	IgnoreMentionsOptionRepliesAndLeading = "Ignore Replies & Starting @ Mentions"
	IgnoreMentionsOptionAll               = "Ignore All @ Mentions & Replies"
)

var IgnoreMentionsOptions = []string{
	IgnoreMentionsOptionOff,
	IgnoreMentionsOptionRepliesAndLeading,
	IgnoreMentionsOptionAll,
}

func IgnoreMentionsModeToOption(mode string) string {
	switch config.NormalizeIgnoreMentionsMode(mode) {
	case config.IgnoreMentionsRepliesAndLeading:
		return IgnoreMentionsOptionRepliesAndLeading
	case config.IgnoreMentionsAll:
		return IgnoreMentionsOptionAll
	default:
		return IgnoreMentionsOptionOff
	}
}

func OptionToIgnoreMentionsMode(opt string) string {
	switch opt {
	case IgnoreMentionsOptionRepliesAndLeading:
		return config.IgnoreMentionsRepliesAndLeading
	case IgnoreMentionsOptionAll:
		return config.IgnoreMentionsAll
	default:
		return config.IgnoreMentionsOff
	}
}

const (
	UnknownGenderOptionBalanced        = "Balanced 50/50 (Feminine & Masculine)"
	UnknownGenderOptionPreferFeminine  = "Prefer Feminine Voices"
	UnknownGenderOptionPreferMasculine = "Prefer Masculine Voices"
	UnknownGenderOptionDefaultVoice    = "Use Global Default Voice"
)

var UnknownGenderPolicyOptions = []string{
	UnknownGenderOptionBalanced,
	UnknownGenderOptionPreferFeminine,
	UnknownGenderOptionPreferMasculine,
	UnknownGenderOptionDefaultVoice,
}

func UnknownGenderPolicyToOption(policy string) string {
	switch config.NormalizeUnknownGenderPolicy(policy) {
	case config.UnknownGenderPreferFeminine:
		return UnknownGenderOptionPreferFeminine
	case config.UnknownGenderPreferMasculine:
		return UnknownGenderOptionPreferMasculine
	case config.UnknownGenderDefaultVoice:
		return UnknownGenderOptionDefaultVoice
	default:
		return UnknownGenderOptionBalanced
	}
}

func OptionToUnknownGenderPolicy(opt string) string {
	switch opt {
	case UnknownGenderOptionPreferFeminine:
		return config.UnknownGenderPreferFeminine
	case UnknownGenderOptionPreferMasculine:
		return config.UnknownGenderPreferMasculine
	case UnknownGenderOptionDefaultVoice:
		return config.UnknownGenderDefaultVoice
	default:
		return config.UnknownGenderBalanced
	}
}

func (v *SettingsView) buildRulesCard() *widget.Card {
	coalesceSelect := widget.NewSelect(CoalesceSpeakerOptions, func(s string) {
		val := OptionToCoalesceSeconds(s)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.CoalesceConsecutiveSeconds = val
		})
		v.refreshPlatformOverrides()
	})
	coalesceSelect.SetSelected(CoalesceSecondsToOption(v.cfg.GlobalChatRules.CoalesceConsecutiveSeconds))
	if !v.cfg.GlobalChatRules.SpeakNames {
		coalesceSelect.Disable()
	}
	v.rulesCoalesceSelect = coalesceSelect
	coalesceRow := container.NewHBox(
		widget.NewLabel("Coalesce Consecutive Messages (Same Speaker):"),
		coalesceSelect,
	)

	checkNames := widget.NewCheck("Speak Chatter Names (e.g. 'User says...')", func(b bool) {
		if b {
			coalesceSelect.Enable()
		} else {
			coalesceSelect.Disable()
		}
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakNames = b
		})
		v.refreshPlatformOverrides()
	})
	checkNames.SetChecked(v.cfg.GlobalChatRules.SpeakNames)
	v.rulesNamesCheck = checkNames

	checkNaturalize := widget.NewCheck("Naturalize Usernames (split CamelCase/snake_case, strip wrappers & trailing numbers)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.NaturalizeNames = b
		})
		v.refreshPlatformOverrides()
	})
	checkNaturalize.SetChecked(v.cfg.GlobalChatRules.NaturalizeNames)
	v.rulesNaturalizeCheck = checkNaturalize

	checkEmotes := widget.NewCheck("Speak Emotes & Emojis (unchecked = strip emotes & emojis)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakEmotes = b
		})
		v.refreshPlatformOverrides()
	})
	checkEmotes.SetChecked(v.cfg.GlobalChatRules.SpeakEmotes)
	v.rulesEmotesCheck = checkEmotes

	checkDedupe := widget.NewCheck("Deduplicate Emotes & Emojis (speak at most once per sentence)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DeduplicateEmotes = b
		})
		v.refreshPlatformOverrides()
	})
	checkDedupe.SetChecked(v.cfg.GlobalChatRules.DeduplicateEmotes)
	v.rulesDedupeCheck = checkDedupe

	dupWordsSelect := widget.NewSelect(DuplicateWordsOptions, func(s string) {
		val := OptionToDuplicateWords(s)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.MaxDuplicateWords = val
		})
		v.refreshPlatformOverrides()
	})
	dupWordsSelect.SetSelected(DuplicateWordsToOption(v.cfg.GlobalChatRules.MaxDuplicateWords))
	v.rulesDupWordsSelect = dupWordsSelect
	dupWordsRow := container.NewHBox(
		widget.NewLabel("Max Consecutive Duplicate Words:"),
		dupWordsSelect,
	)

	checkStripZalgo := widget.NewCheck("Strip Zalgo Combining Marks (cap stacked diacritics at 2 per character)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.StripZalgo = b
		})
		v.refreshPlatformOverrides()
	})
	checkStripZalgo.SetChecked(v.cfg.GlobalChatRules.StripZalgo)
	v.rulesStripZalgoCheck = checkStripZalgo

	checkStripASCIIArt := widget.NewCheck("Strip Braille, Box-Drawing & ASCII Symbol Art Floods", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.StripASCIIArt = b
		})
		v.refreshPlatformOverrides()
	})
	checkStripASCIIArt.SetChecked(v.cfg.GlobalChatRules.StripASCIIArt)
	v.rulesStripASCIIArtCheck = checkStripASCIIArt

	copypastaSelect := widget.NewSelect(CopypastaWindowOptions, func(s string) {
		val := OptionToCopypastaSeconds(s)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.DeduplicateCopypastaWindowSeconds = val
		})
		v.refreshPlatformOverrides()
	})
	copypastaSelect.SetSelected(CopypastaSecondsToOption(v.cfg.GlobalChatRules.DeduplicateCopypastaWindowSeconds))
	v.rulesCopypastaSelect = copypastaSelect
	copypastaRow := container.NewHBox(
		widget.NewLabel("Suppress Multi-User Copypasta Waves:"),
		copypastaSelect,
	)

	checkPlatform := widget.NewCheck("Speak Platform Name Prefix (e.g. '[Twitch]')", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakPlatformName = b
		})
		v.refreshPlatformOverrides()
	})
	checkPlatform.SetChecked(v.cfg.GlobalChatRules.SpeakPlatformName)
	v.rulesPlatformCheck = checkPlatform

	allowBroadcasterCheck := widget.NewCheck("Still read messages that @ mention the broadcaster", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.AllowBroadcasterMentions = b
		})
		v.refreshPlatformOverrides()
	})
	allowBroadcasterCheck.SetChecked(v.cfg.GlobalChatRules.AllowBroadcasterMentions)
	v.rulesAllowBroadcasterCheck = allowBroadcasterCheck

	playAlertIgnoredCheck := widget.NewCheck("Play configured alert sound when an @ mention or reply is ignored", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.PlayAlertOnIgnoredMention = b
		})
		v.refreshPlatformOverrides()
	})
	playAlertIgnoredCheck.SetChecked(v.cfg.GlobalChatRules.PlayAlertOnIgnoredMention)
	v.rulesPlayAlertIgnoredMentionCheck = playAlertIgnoredCheck

	syncMentionChecksState := func(mode string) {
		if config.NormalizeIgnoreMentionsMode(mode) != config.IgnoreMentionsOff {
			allowBroadcasterCheck.Enable()
			playAlertIgnoredCheck.Enable()
		} else {
			allowBroadcasterCheck.Disable()
			playAlertIgnoredCheck.Disable()
		}
	}
	syncMentionChecksState(v.cfg.GlobalChatRules.IgnoreMentionsMode)

	mentionsModeSelect := widget.NewSelect(IgnoreMentionsOptions, func(s string) {
		mode := OptionToIgnoreMentionsMode(s)
		syncMentionChecksState(mode)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.IgnoreMentionsMode = mode
		})
		v.refreshPlatformOverrides()
	})
	mentionsModeSelect.SetSelected(IgnoreMentionsModeToOption(v.cfg.GlobalChatRules.IgnoreMentionsMode))
	v.rulesMentionsModeSelect = mentionsModeSelect
	mentionsModeRow := container.NewHBox(
		widget.NewLabel("@ Mentions & Replies:"),
		mentionsModeSelect,
	)

	unknownGenderSelect := widget.NewSelect(UnknownGenderPolicyOptions, func(s string) {
		pol := OptionToUnknownGenderPolicy(s)
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.UnknownGenderPolicy = pol
		})
	})
	unknownGenderSelect.SetSelected(UnknownGenderPolicyToOption(v.cfg.GlobalChatRules.UnknownGenderPolicy))
	v.rulesUnknownGenderSelect = unknownGenderSelect
	unknownGenderRow := container.NewHBox(
		widget.NewLabel("Unknown Gender Voice Pool:"),
		unknownGenderSelect,
	)

	permittedVoicesLabel := widget.NewLabel(FormatPermittedVoicesSummary(v.cfg.TTS.EffectiveEnabledModels(), v.cfg.GlobalChatRules.DisabledChatterVoices))
	v.rulesPermittedVoicesLabel = permittedVoicesLabel

	permittedVoicesBtn := widget.NewButtonWithIcon("Select Voices...", theme.ListIcon(), func() {
		cfg := v.currentConfig()
		ShowPermittedVoicesDialog(
			v.window,
			v.session,
			cfg.TTS.EffectiveEnabledModels(),
			cfg.GlobalChatRules.DisabledChatterVoices,
			func(disabledVoices []string) {
				v.updateConfig(func(c *config.Config) {
					c.GlobalChatRules.DisabledChatterVoices = slices.Clone(disabledVoices)
				})
				cfgNow := v.currentConfig()
				permittedVoicesLabel.SetText(FormatPermittedVoicesSummary(cfgNow.TTS.EffectiveEnabledModels(), cfgNow.GlobalChatRules.DisabledChatterVoices))
				v.setStatus("Permitted auto-assign voices updated.")
			},
			v.setStatus,
		)
	})
	v.rulesPermittedVoicesBtn = permittedVoicesBtn

	permittedVoicesRow := container.NewHBox(
		widget.NewLabel("Permitted Auto-Assign Voices:"),
		permittedVoicesLabel,
		permittedVoicesBtn,
	)

	smartGenderCheck := widget.NewCheck("Smart Gender Detection (Chat Pronouns + Username Analysis)", func(b bool) {
		if v.cfg.GlobalChatRules.HashChatterVoices && b {
			unknownGenderSelect.Enable()
		} else {
			unknownGenderSelect.Disable()
		}
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SmartGenderVoices = b
		})
		v.refreshPlatformOverrides()
	})
	smartGenderCheck.SetChecked(v.cfg.GlobalChatRules.SmartGenderVoices)
	v.rulesSmartGenderCheck = smartGenderCheck

	hashVoicesCheck := widget.NewCheck("Assign Unique Voices to Chatters Automatically (Deterministic Hash — Never Pollutes Viewers List)", func(b bool) {
		if b {
			smartGenderCheck.Enable()
			permittedVoicesBtn.Enable()
			if smartGenderCheck.Checked {
				unknownGenderSelect.Enable()
			} else {
				unknownGenderSelect.Disable()
			}
		} else {
			smartGenderCheck.Disable()
			unknownGenderSelect.Disable()
			permittedVoicesBtn.Disable()
		}
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.HashChatterVoices = b
		})
		v.refreshPlatformOverrides()
	})
	hashVoicesCheck.SetChecked(v.cfg.GlobalChatRules.HashChatterVoices)
	v.rulesHashVoicesCheck = hashVoicesCheck

	if !v.cfg.GlobalChatRules.HashChatterVoices {
		smartGenderCheck.Disable()
		unknownGenderSelect.Disable()
		permittedVoicesBtn.Disable()
	} else if !v.cfg.GlobalChatRules.SmartGenderVoices {
		unknownGenderSelect.Disable()
	}

	cardContent := container.NewVBox(
		checkNames,
		checkNaturalize,
		coalesceRow,
		checkEmotes,
		checkDedupe,
		dupWordsRow,
		checkStripZalgo,
		checkStripASCIIArt,
		copypastaRow,
		checkPlatform,
		widget.NewSeparator(),
		hashVoicesCheck,
		smartGenderCheck,
		unknownGenderRow,
		permittedVoicesRow,
		widget.NewSeparator(),
		mentionsModeRow,
		allowBroadcasterCheck,
		playAlertIgnoredCheck,
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
	v.refreshWords = refreshWords

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
	v.knownBotsCheck = knownBotsCheck

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
	v.refreshBots = refreshBots

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

// TranslationProviderOptions lists the selectable translation backends in the UI.
var TranslationProviderOptions = []string{
	"MyMemory Translated API (Free — Zero Setup, 5k–50k chars/day)",
	"Google Cloud Translation API v2 (Official API Key — 500k chars/mo Free)",
	"DeepL API Free / Pro (Official API Key — 500k chars/mo Free)",
	"LibreTranslate (Local / Self-Hosted)",
	"Local LLM (Ollama / OpenAI API)",
}

// FormatTranslationProviderOption converts a canonical provider identifier into its UI display label.
func FormatTranslationProviderOption(provider string) string {
	switch config.NormalizeTranslationProvider(provider) {
	case config.TranslationProviderGoogle:
		return "Google Cloud Translation API v2 (Official API Key — 500k chars/mo Free)"
	case config.TranslationProviderDeepL:
		return "DeepL API Free / Pro (Official API Key — 500k chars/mo Free)"
	case config.TranslationProviderLibreTranslate:
		return "LibreTranslate (Local / Self-Hosted)"
	case config.TranslationProviderOllama:
		return "Local LLM (Ollama / OpenAI API)"
	default:
		return "MyMemory Translated API (Free — Zero Setup, 5k–50k chars/day)"
	}
}

// ParseTranslationProviderOption converts a UI display label back into its canonical provider identifier.
func ParseTranslationProviderOption(option string) string {
	switch option {
	case "Google Cloud Translation API v2 (Official API Key — 500k chars/mo Free)":
		return config.TranslationProviderGoogle
	case "DeepL API Free / Pro (Official API Key — 500k chars/mo Free)":
		return config.TranslationProviderDeepL
	case "LibreTranslate (Local / Self-Hosted)":
		return config.TranslationProviderLibreTranslate
	case "Local LLM (Ollama / OpenAI API)":
		return config.TranslationProviderOllama
	default:
		return config.TranslationProviderMyMemory
	}
}

// TranslationConfidenceOptions lists the selectable language detection sensitivity presets.
var TranslationConfidenceOptions = []string{
	"Strict (0.80 — High certainty only)",
	"Balanced (0.65 — Recommended)",
	"Aggressive (0.45 — Translate short phrases)",
}

// FormatTranslationConfidenceOption converts a numeric confidence threshold into its UI preset label.
func FormatTranslationConfidenceOption(val float64) string {
	norm := config.NormalizeTranslationMinConfidence(val)
	switch {
	case norm >= 0.75:
		return "Strict (0.80 — High certainty only)"
	case norm <= 0.55:
		return "Aggressive (0.45 — Translate short phrases)"
	default:
		return "Balanced (0.65 — Recommended)"
	}
}

// ParseTranslationConfidenceOption converts a UI preset label into its numeric confidence threshold.
func ParseTranslationConfidenceOption(option string) float64 {
	switch option {
	case "Strict (0.80 — High certainty only)":
		return 0.80
	case "Aggressive (0.45 — Translate short phrases)":
		return 0.45
	default:
		return 0.65
	}
}

// TranslationTargetLangOptions returns the selectable target language labels (`Name (code)`).
func TranslationTargetLangOptions() []string {
	opts := make([]string, 0, len(translate.TargetLanguages))
	for _, lang := range translate.TargetLanguages {
		opts = append(opts, fmt.Sprintf("%s (%s)", lang.Name, lang.Code))
	}
	return opts
}

// FormatTranslationTargetLangOption formats a canonical ISO-639-1 code into its UI dropdown label.
func FormatTranslationTargetLangOption(code string) string {
	norm := config.NormalizeTranslationTargetLang(code)
	for _, lang := range translate.TargetLanguages {
		if lang.Code == norm {
			return fmt.Sprintf("%s (%s)", lang.Name, lang.Code)
		}
	}
	return "English (en)"
}

// ParseTranslationTargetLangOption extracts the canonical ISO-639-1 code from a UI dropdown label.
func ParseTranslationTargetLangOption(option string) string {
	if openIdx := strings.LastIndex(option, "("); openIdx >= 0 {
		if closeIdx := strings.LastIndex(option, ")"); closeIdx > openIdx+1 {
			return config.NormalizeTranslationTargetLang(option[openIdx+1 : closeIdx])
		}
	}
	return config.NormalizeTranslationTargetLang(option)
}

const (
	TranslationRoutingOptionPriority   = "Priority Fallback (Top-to-Bottom — Default)"
	TranslationRoutingOptionRoundRobin = "Round-Robin Load Balancing"
)

// TranslationRoutingOptions lists the selectable multi-provider routing strategies.
var TranslationRoutingOptions = []string{
	TranslationRoutingOptionPriority,
	TranslationRoutingOptionRoundRobin,
}

// FormatTranslationRoutingOption converts a canonical routing mode into its UI display label.
func FormatTranslationRoutingOption(mode string) string {
	if config.NormalizeTranslationRoutingMode(mode) == config.TranslationRoutingRoundRobin {
		return TranslationRoutingOptionRoundRobin
	}
	return TranslationRoutingOptionPriority
}

// ParseTranslationRoutingOption converts a UI routing label into its canonical routing mode.
func ParseTranslationRoutingOption(option string) string {
	if option == TranslationRoutingOptionRoundRobin {
		return config.TranslationRoutingRoundRobin
	}
	return config.TranslationRoutingPriority
}

// TranslationProviderShortName returns a concise human-readable title for a translation provider node.
func TranslationProviderShortName(provider string) string {
	switch config.NormalizeTranslationProvider(provider) {
	case config.TranslationProviderGoogle:
		return "Google Cloud Translation v2"
	case config.TranslationProviderDeepL:
		return "DeepL API (Free / Pro)"
	case config.TranslationProviderLibreTranslate:
		return "LibreTranslate"
	case config.TranslationProviderOllama:
		return "Local LLM (Ollama / OpenAI)"
	default:
		return "MyMemory Translated API (Free)"
	}
}

// FormatTranslationNodeStatus returns the SVG icon resource, compact status badge text, and detailed
// mouseover tooltip string for a translation provider node.
func FormatTranslationNodeStatus(entry config.TranslationProviderEntry, st translate.ProviderStatus, now time.Time) (fyne.Resource, string, string) {
	name := TranslationProviderShortName(entry.Provider)
	if !entry.Enabled {
		return resNodeDisabled,
			"Disabled",
			fmt.Sprintf("Node: %s — Disabled (Check the box to enable in routing chain)", name)
	}
	if st.InBackoff(now) {
		rem := max(st.CooldownUntil.Sub(now).Round(time.Second), time.Second)
		reason := strings.TrimSpace(st.LastError)
		if reason == "" {
			reason = "Rate limit or transient error"
		}
		tip := fmt.Sprintf("⚠️ Backoff Reason: %s • %s (%s remaining, failure #%d • %d ok / %d failed)", reason, name, rem, st.ConsecutiveFail, st.SuccessCount, st.FailureCount)
		return resNodeBackoff, fmt.Sprintf("Backoff (%s)", rem), tip
	}
	badge := "Healthy (Ready)"
	if st.SuccessCount > 0 {
		badge = fmt.Sprintf("Healthy (%d ok, %dms)", st.SuccessCount, st.LastLatency.Milliseconds())
	}
	tip := fmt.Sprintf("Node: %s — Healthy (Ready) • Session: %d translated / %d failed", name, st.SuccessCount, st.FailureCount)
	if st.LastLatency > 0 {
		tip += fmt.Sprintf(" • Last Latency: %dms", st.LastLatency.Milliseconds())
	}
	if st.LastError != "" {
		tip += " • Last Error: " + st.LastError
	}
	return resNodeHealthy, badge, tip
}

// nodeDragHandle is a compact drag-and-drop grip ("≡") that reorders a provider node row when dragged vertically.
type nodeDragHandle struct {
	widget.Label
	onMove func(deltaSteps int)
	accumY float32
}

var (
	_ fyne.Draggable     = (*nodeDragHandle)(nil)
	_ desktop.Cursorable = (*nodeDragHandle)(nil)
)

func newNodeDragHandle(onMove func(deltaSteps int)) *nodeDragHandle {
	h := &nodeDragHandle{onMove: onMove}
	h.Text = "≡"
	h.Alignment = fyne.TextAlignCenter
	h.TextStyle = fyne.TextStyle{Bold: true}
	h.ExtendBaseWidget(h)
	return h
}

func (h *nodeDragHandle) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (h *nodeDragHandle) Dragged(ev *fyne.DragEvent) {
	if ev == nil || h.onMove == nil {
		return
	}
	h.accumY += ev.Dragged.DY
	const stepThreshold = 32.0
	if h.accumY <= -stepThreshold {
		h.accumY = 0
		h.onMove(-1)
	} else if h.accumY >= stepThreshold {
		h.accumY = 0
		h.onMove(1)
	}
}

func (h *nodeDragHandle) DragEnd() {
	h.accumY = 0
}

// FormatTranslationRateLimitSummary returns the dynamic rate limit & pricing guidance text for the active provider.
func FormatTranslationRateLimitSummary(provider string, hasAPIKey bool, hasEmail bool) string {
	switch config.NormalizeTranslationProvider(provider) {
	case config.TranslationProviderGoogle:
		base := "Rate Limits & 4-Step Setup (Google Cloud Translation Basic v2 — Official API Key Required):\n"
		if hasAPIKey {
			base = "Rate Limits & Pricing (Google Cloud Translation Basic v2 — Active API Key):\n"
		}
		return base +
			"• Why Basic (v2): Google's docs call v2 'Basic' and v3 'Advanced'. Basic (v2) accepts a simple API key and includes 500,000 FREE characters/month (~10,000 chat messages).\n" +
			"• 4-Step Console Setup: (1) Create/Select a GCP Project → (2) Enable Cloud Translation API → (3) Go to APIs & Services > Credentials > + Create Credentials > API key → (4) In IAM & Admin > Quotas, set 'v2 and v3 general model characters per day' to 15,000 to guarantee $0.00 overages."
	case config.TranslationProviderDeepL:
		return "Rate Limits & Pricing (DeepL API Free / Pro — Official API Key Required):\n" +
			"• Free Tier: 500,000 characters/month FREE (~10,000 chat messages) with a free DeepL developer key (ends in ':fx', hard-capped so you can never be charged overages).\n" +
			"• DeepL Pro: Unlimited pay-as-you-go volume."
	case config.TranslationProviderLibreTranslate:
		return "Rate Limits & Pricing (LibreTranslate):\n" +
			"• Local / Self-Hosted (http://localhost:5000): Unlimited messages, 100% offline & free.\n" +
			"• Public / Hosted Instances: Subject to the instance operator's rate limits (API key optional)."
	case config.TranslationProviderOllama:
		return "Rate Limits & Pricing (Local LLM / OpenAI-Compatible API):\n" +
			"• Local Ollama / LM Studio (http://localhost:11434): Unlimited messages, 100% offline & free on your GPU/CPU (recommended model: qwen2.5:3b).\n" +
			"• Hosted OpenAI-Compatible Endpoints: Subject to your provider's token rate limits and pricing."
	default:
		if hasEmail {
			return "Rate Limits & Pricing (MyMemory Translated API — 10x Email Quota Active):\n" +
				"• Free Quota: Up to 50,000 characters/day (~1,000 foreign chat messages/day) FREE.\n" +
				"• Official public REST API by Translated.net (no account signup, password, or credit card required)."
		}
		return "Rate Limits & Pricing (MyMemory Translated API — Default Zero-Setup Free Mode):\n" +
			"• Anonymous Free Quota: 5,000 characters/day (~100 foreign chat messages/day) with zero setup.\n" +
			"• 10x Free Upgrade: Enter your email address below to boost your free quota to 50,000 characters/day (~1,000 messages/day) with no signup or credit card."
	}
}

// TranslationProviderGuideURL returns the anchored URL on the TownCrier website for a provider's setup guide.
func TranslationProviderGuideURL(provider string) string {
	norm := config.NormalizeTranslationProvider(provider)
	return "https://mrpoundsign.com/TownCrier/translation-guides.html#" + norm
}

// TranslationProviderLinks returns the display text and URLs for getting an API key/setup guide and viewing the provider's Terms of Service.
func TranslationProviderLinks(provider string) (keyText, keyURL, tosText, tosURL string) {
	switch config.NormalizeTranslationProvider(provider) {
	case config.TranslationProviderGoogle:
		return "Open Google Cloud Credentials (Create API Key)",
			"https://console.cloud.google.com/apis/credentials",
			"Google Cloud Terms & Pricing",
			"https://cloud.google.com/terms/service-terms"
	case config.TranslationProviderDeepL:
		return "Get Free DeepL API Key (500k chars/mo)",
			"https://www.deepl.com/pro-api",
			"DeepL API Terms & Privacy Policy",
			"https://www.deepl.com/pro-license"
	case config.TranslationProviderLibreTranslate:
		return "LibreTranslate Self-Host Guide & API Keys",
			"https://github.com/LibreTranslate/LibreTranslate",
			"LibreTranslate Public Portal & Terms",
			"https://portal.libretranslate.com"
	case config.TranslationProviderOllama:
		return "Download Ollama & Local LLM Setup",
			"https://ollama.com/download",
			"Ollama Terms of Service & Privacy",
			"https://ollama.com/terms"
	default:
		return "MyMemory API & Free Quota Docs",
			"https://mymemory.translated.net/doc/usagelimits.php",
			"MyMemory Terms of Service & GDPR Privacy",
			"https://mymemory.translated.net/doc/terms.php"
	}
}

func (v *SettingsView) selectedTranslationProvider() string {
	if v.translationSelectedNode != "" {
		return config.NormalizeTranslationProvider(v.translationSelectedNode)
	}
	cfg := v.currentConfig()
	return config.NormalizeTranslationProvider(cfg.Translation.Provider)
}

func (v *SettingsView) providerEntryFor(provider string) config.TranslationProviderEntry {
	norm := config.NormalizeTranslationProvider(provider)
	cfg := v.currentConfig()
	tCfg := cfg.Translation
	config.NormalizeTranslationProviders(&tCfg)
	for _, p := range tCfg.Providers {
		if config.NormalizeTranslationProvider(p.Provider) == norm {
			return p
		}
	}
	return config.TranslationProviderEntry{Provider: norm}
}

func (v *SettingsView) updateProviderEntry(provider string, fn func(entry *config.TranslationProviderEntry)) {
	norm := config.NormalizeTranslationProvider(provider)
	v.updateConfig(func(c *config.Config) {
		config.NormalizeTranslationProviders(&c.Translation)
		for i := range c.Translation.Providers {
			if config.NormalizeTranslationProvider(c.Translation.Providers[i].Provider) == norm {
				fn(&c.Translation.Providers[i])
				if norm == config.TranslationProviderMyMemory {
					c.Translation.ContactEmail = c.Translation.Providers[i].ContactEmail
				}
				if c.Translation.Provider == norm {
					c.Translation.EndpointURL = c.Translation.Providers[i].EndpointURL
					c.Translation.Model = c.Translation.Providers[i].Model
				}
				break
			}
		}
	})
	if v.session != nil && v.session.Translator() != nil {
		v.session.Translator().ResetProviderBackoff(norm)
	}
}

func (v *SettingsView) syncTranslationProviderFields() {
	prov := v.selectedTranslationProvider()
	entry := v.providerEntryFor(prov)

	wasInit := v.initializing
	v.initializing = true
	if v.translationProviderSelect != nil {
		v.translationProviderSelect.SetSelected(FormatTranslationProviderOption(prov))
	}
	if v.translationEmailEntry != nil {
		v.translationEmailEntry.SetText(entry.ContactEmail)
	}
	if v.translationEndpointEntry != nil {
		v.translationEndpointEntry.SetText(entry.EndpointURL)
	}
	if v.translationModelEntry != nil {
		v.translationModelEntry.SetText(entry.Model)
	}
	var keyVal string
	if v.session != nil {
		keyVal = v.session.TranslationProviderAPIKey(prov)
	} else {
		keyVal = entry.APIKey
	}
	if v.translationAPIKeyEntry != nil {
		v.translationAPIKeyEntry.SetText(keyVal)
	}
	v.initializing = wasInit

	hasKey := strings.TrimSpace(keyVal) != ""
	hasEmail := strings.TrimSpace(entry.ContactEmail) != ""

	if v.translationLimitLabel != nil {
		v.translationLimitLabel.SetText(FormatTranslationRateLimitSummary(prov, hasKey, hasEmail))
	}

	if v.translationGuideLink != nil {
		v.translationGuideLink.SetText("Setup Guide (" + TranslationProviderShortName(prov) + ")")
		if u, err := url.Parse(TranslationProviderGuideURL(prov)); err == nil {
			v.translationGuideLink.SetURL(u)
		}
	}

	keyText, keyRawURL, tosText, tosRawURL := TranslationProviderLinks(prov)
	if v.translationKeyLink != nil {
		v.translationKeyLink.SetText(keyText)
		if u, err := url.Parse(keyRawURL); err == nil {
			v.translationKeyLink.SetURL(u)
		}
	}
	if v.translationTOSLink != nil {
		v.translationTOSLink.SetText(tosText)
		if u, err := url.Parse(tosRawURL); err == nil {
			v.translationTOSLink.SetURL(u)
		}
	}
	if v.translationQuotaLink != nil && v.translationQuotaSep != nil {
		if prov == config.TranslationProviderGoogle {
			v.translationQuotaSep.Show()
			v.translationQuotaLink.Show()
		} else {
			v.translationQuotaSep.Hide()
			v.translationQuotaLink.Hide()
		}
	}

	if v.translationEmailBox != nil {
		if prov == config.TranslationProviderMyMemory {
			v.translationEmailBox.Show()
		} else {
			v.translationEmailBox.Hide()
		}
	}

	if v.translationAPIKeyEntry != nil {
		switch prov {
		case config.TranslationProviderGoogle:
			v.translationAPIKeyEntry.SetPlaceHolder("Required — Enter Google Cloud Translation API v2 key (stored in OS Keyring)")
		case config.TranslationProviderDeepL:
			v.translationAPIKeyEntry.SetPlaceHolder("Required — Enter DeepL API Free (:fx) or Pro key (stored in OS Keyring)")
		default:
			v.translationAPIKeyEntry.SetPlaceHolder("Optional — Leave blank for free mode, or enter API key (stored in OS Keyring)")
		}
	}

	if v.translationEndpointBox != nil {
		switch prov {
		case config.TranslationProviderLibreTranslate:
			if v.translationEndpointEntry != nil {
				v.translationEndpointEntry.SetPlaceHolder(config.DefaultLibreTranslateURL)
			}
			v.translationEndpointBox.Show()
		case config.TranslationProviderOllama:
			if v.translationEndpointEntry != nil {
				v.translationEndpointEntry.SetPlaceHolder(config.DefaultOllamaURL)
			}
			v.translationEndpointBox.Show()
		default:
			v.translationEndpointBox.Hide()
		}
	}

	if v.translationModelBox != nil {
		if prov == config.TranslationProviderOllama {
			v.translationModelBox.Show()
		} else {
			v.translationModelBox.Hide()
		}
	}

	if v.translationConfigDrawer != nil {
		if v.translationSelectedNode != "" {
			v.translationConfigDrawer.Show()
		} else {
			v.translationConfigDrawer.Hide()
		}
	}

	if v.translationTestBtn != nil {
		if v.translationSelectedNode != "" {
			v.translationTestBtn.SetText("Test " + TranslationProviderShortName(v.translationSelectedNode))
		} else {
			v.translationTestBtn.SetText("Test Translation Chain")
		}
	}
}

func (v *SettingsView) showTranslationTooltip(obj fyne.CanvasObject, tip string, hovered bool) {
	defaultHoverMsg := "Hover over any node status icon or badge above to inspect live health, latency, and backoff reason."
	if v.translationNodeHoverLbl != nil {
		if hovered && tip != "" {
			v.translationNodeHoverLbl.SetText("🔍 " + tip)
		} else {
			v.translationNodeHoverLbl.SetText(defaultHoverMsg)
		}
	}
	if !hovered || tip == "" {
		if v.translationTooltipBubble != nil {
			v.translationTooltipBubble.Hide()
		}
		return
	}
	if v.translationTooltipBubble == nil || v.translationTooltipText == nil || v.translationTooltipLayer == nil {
		return
	}
	shortTip := truncateTooltip(tip, 125)
	v.translationTooltipText.Text = shortTip
	v.translationTooltipText.Refresh()
	bubbleSize := v.translationTooltipBubble.MinSize()
	v.translationTooltipBubble.Resize(bubbleSize)

	appInstance := fyne.CurrentApp()
	if appInstance == nil || appInstance.Driver() == nil {
		return
	}
	objPos := appInstance.Driver().AbsolutePositionForObject(obj)
	layerPos := appInstance.Driver().AbsolutePositionForObject(v.translationTooltipLayer)
	relX := objPos.X - layerPos.X
	relY := objPos.Y - layerPos.Y

	layerWidth := v.translationTooltipLayer.Size().Width
	if layerWidth <= 0 && v.window != nil && v.window.Canvas() != nil {
		layerWidth = v.window.Canvas().Size().Width - 32
	}
	if layerWidth <= 0 {
		layerWidth = 880
	}

	x := max(relX-bubbleSize.Width+obj.Size().Width, float32(8))
	if x+bubbleSize.Width > layerWidth-8 {
		x = max(layerWidth-bubbleSize.Width-8, float32(8))
	}
	y := relY - bubbleSize.Height - 4
	if y < 4 {
		y = relY + obj.Size().Height + 4
	}
	v.translationTooltipBubble.Move(fyne.NewPos(x, y))
	v.translationTooltipBubble.Show()
	v.translationTooltipBubble.Refresh()
}

// FormatTestTranslationOutput formats the multi-line test result showing input, per-sentence cache/API resolution, and final output.
func FormatTestTranslationOutput(input, detName, detISO, tgtName, tgtLang, elapsedStr string, res translate.Result) string {
	srcLang := res.SourceLang
	if srcLang == "" || srcLang == "auto" {
		srcLang = detISO
	}
	srcName := translate.LanguageName(srcLang)
	if strings.TrimSpace(res.TranslatedText) == "" {
		return fmt.Sprintf(
			"Input — %s (%s): %q\nOutput — Already matches target language %s (%s) (short-circuited locally, 0 API characters used).",
			srcName, srcLang, input, tgtName, tgtLang,
		)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Input — %s (%s): %q\n", srcName, srcLang, input)

	for _, st := range res.Sentences {
		tier := "RAM"
		if st.PromotedToDisk {
			tier = "RAM + Disk"
		}
		provName := TranslationProviderShortName(st.Provider)
		switch {
		case st.FromCache && st.LiveTranslated != "":
			fmt.Fprintf(&b, "  • Sentence #%d [Cached (Hits: %d, %s) + Live API Verified via %s]:\n      Cached: %q | Live API: %q\n",
				st.Index, st.CacheHits, tier, provName, st.Translated, st.LiveTranslated)
		case st.FromCache:
			fmt.Fprintf(&b, "  • Sentence #%d [Cached — Hits: %d, %s (skipped API)]: %q → %q\n",
				st.Index, st.CacheHits, tier, st.Original, st.Translated)
		case st.SkippedTarget:
			fmt.Fprintf(&b, "  • Sentence #%d [Skipped — Already %s (%s)]: %q\n",
				st.Index, tgtName, tgtLang, st.Original)
		default:
			fmt.Fprintf(&b, "  • Sentence #%d [Live API — %s, saved to %s cache (Hits: %d)]: %q → %q\n",
				st.Index, provName, tier, max(st.CacheHits, 1), st.Original, st.Translated)
		}
	}

	viaStr := elapsedStr
	if res.Provider != "" {
		viaStr = fmt.Sprintf("%s, %s", TranslationProviderShortName(res.Provider), elapsedStr)
	}
	fmt.Fprintf(&b, "Output — %s (%s) (%s): %q", tgtName, tgtLang, viaStr, res.TranslatedText)
	return b.String()
}

func (v *SettingsView) buildTranslationTab() fyne.CanvasObject {
	v.updateConfig(func(c *config.Config) {
		config.NormalizeTranslationProviders(&c.Translation)
	})
	cfg := v.currentConfig()

	v.translationTooltipBubble, v.translationTooltipText = buildTooltipBubble()
	v.translationTooltipLayer = container.NewWithoutLayout(v.translationTooltipBubble)

	v.translationTargetSelect = widget.NewSelect(TranslationTargetLangOptions(), func(selected string) {
		if v.initializing {
			return
		}
		code := ParseTranslationTargetLangOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.Translation.TargetLang = code
		})
		v.setStatus("✓ Target translation language set to " + selected)
	})
	v.translationTargetSelect.SetSelected(FormatTranslationTargetLangOption(cfg.Translation.TargetLang))

	v.translationConfSelect = widget.NewSelect(TranslationConfidenceOptions, func(selected string) {
		if v.initializing {
			return
		}
		conf := ParseTranslationConfidenceOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.Translation.MinConfidence = conf
		})
		v.setStatus(fmt.Sprintf("✓ Language detection sensitivity set to %.2f", conf))
	})
	v.translationConfSelect.SetSelected(FormatTranslationConfidenceOption(cfg.Translation.MinConfidence))

	makeFieldRow := func(label string, ctrl fyne.CanvasObject) *fyne.Container {
		lbl := widget.NewLabel(label)
		return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(230, 36), lbl), nil, ctrl)
	}

	detectDesc := widget.NewLabelWithStyle(
		"Foreign chat messages are automatically detected in-process (<0.1ms) and split into sentences so repeated greetings, copypastas, and mixed-language messages resolve cached or target-language sentences locally at 0 API cost.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	detectDesc.Wrapping = fyne.TextWrapWord

	v.translationCacheStatsLbl = widget.NewLabel("")
	v.translationRefreshCacheStats = func() {
		if v.translationCacheStatsLbl == nil {
			return
		}
		memCount, diskCount := 0, 0
		if v.session != nil {
			memCount, diskCount = v.session.TranslationCacheStats()
		}
		v.translationCacheStatsLbl.SetText(fmt.Sprintf("%d in RAM (L1) • %d promoted to Disk (L2, ≥2 hits)", memCount, diskCount))
	}
	v.translationClearCacheBtn = widget.NewButtonWithIcon("Clear Translation Cache", theme.DeleteIcon(), func() {
		if v.session != nil {
			_ = v.session.ClearTranslationCache()
		}
		if v.translationRefreshCacheStats != nil {
			v.translationRefreshCacheStats()
		}
		v.setStatus("✓ Cleared RAM and Disk translation caches.")
	})
	v.translationRefreshCacheStats()
	cacheRow := makeFieldRow("Sentence Cache (Spam Protection):", container.NewBorder(nil, nil, nil, v.translationClearCacheBtn, v.translationCacheStatsLbl))

	langCard := widget.NewCard(
		"Language Detection, Target Language & Sentence Cache",
		"Choose your target language and manage the two-tier sentence translation cache",
		container.NewVBox(
			detectDesc,
			makeFieldRow("Target Language:", v.translationTargetSelect),
			makeFieldRow("Detection Sensitivity:", v.translationConfSelect),
			cacheRow,
		),
	)

	// --- Translation Notification Chime Card ---
	v.translationChimeCheck = widget.NewCheck("Play 3-note notification chime before speaking translated messages", func(checked bool) {
		if v.initializing {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.Translation.PlayChimeBeforeSpeak = checked
		})
	})
	v.translationChimeCheck.SetChecked(cfg.Translation.PlayChimeBeforeSpeak)

	chimeEntry := NewScrollPassEntry()
	chimeEntry.SetPlaceHolder("Built-in 3-note ascending chime (or select a custom .wav / .mp3 file)")
	chimeEntry.SetText(cfg.Translation.ChimeSoundPath)
	chimeEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		v.updateConfig(func(c *config.Config) {
			c.Translation.ChimeSoundPath = trimmed
		})
	}
	v.translationChimeEntry = chimeEntry

	chimeBrowseBtn := widget.NewButtonWithIcon("Browse...", theme.FolderOpenIcon(), func() {
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
			chimeEntry.SetText(chosen)
			v.setStatus("✓ Custom translation chime set to " + chosen)
		}, v.window)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".wav", ".wave", ".mp3"}))
		fd.Show()
	})
	v.translationChimeBrowseBtn = chimeBrowseBtn

	chimeClearBtn := widget.NewButtonWithIcon("Use Built-in 3-Note Chime", theme.ContentClearIcon(), func() {
		chimeEntry.SetText("")
		v.setStatus("✓ Reset translation sound to built-in 3-note harmonic chime.")
	})
	v.translationChimeClearBtn = chimeClearBtn

	chimeTestBtn := widget.NewButtonWithIcon("Test Sound", theme.MediaPlayIcon(), func() {
		go func() {
			var err error
			if v.session != nil {
				err = v.session.PlayTranslationChime()
			} else if v.player != nil {
				cache := audio.NewAlertSoundCache()
				samples, rate, loadErr := cache.ResolveTranslationChime(strings.TrimSpace(chimeEntry.Text))
				if loadErr != nil {
					err = loadErr
				} else {
					err = v.player.Play(samples, rate)
				}
			}
			if err != nil {
				v.setStatus("⚠️ Translation chime error: " + err.Error())
			} else {
				v.setStatus("🔊 Played 3-note translation notification chime preview.")
			}
		}()
	})
	v.translationChimeTestBtn = chimeTestBtn

	chimeBtns := container.NewHBox(chimeBrowseBtn, chimeClearBtn, chimeTestBtn)
	chimeFileRow := container.NewBorder(nil, nil, widget.NewLabel("Translation Sound:"), chimeBtns, chimeEntry)

	chimeCard := widget.NewCard(
		"Translation Notification Sound",
		"Audio cue played immediately before a translated foreign message is read aloud",
		container.NewVBox(
			v.translationChimeCheck,
			chimeFileRow,
		),
	)

	// --- Multi-Provider Routing Mode & Reorderable Node List ---
	v.translationRoutingSelect = widget.NewSelect(TranslationRoutingOptions, func(selected string) {
		if v.initializing {
			return
		}
		mode := ParseTranslationRoutingOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.Translation.RoutingMode = mode
		})
		v.setStatus("✓ Translation routing mode set to " + selected)
	})
	v.translationRoutingSelect.SetSelected(FormatTranslationRoutingOption(cfg.Translation.RoutingMode))

	v.translationNodesBox = container.NewVBox()
	defaultHoverMsg := "Hover over any node status icon or badge above to inspect live health, latency, and backoff reason."
	v.translationNodeHoverLbl = widget.NewLabelWithStyle(defaultHoverMsg, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	v.translationNodeHoverLbl.Wrapping = fyne.TextWrapWord

	moveNode := func(fromIdx, delta int) {
		cur := v.currentConfig()
		tCfg := cur.Translation
		config.NormalizeTranslationProviders(&tCfg)
		toIdx := fromIdx + delta
		if fromIdx < 0 || fromIdx >= len(tCfg.Providers) || toIdx < 0 || toIdx >= len(tCfg.Providers) {
			return
		}
		v.updateConfig(func(c *config.Config) {
			config.NormalizeTranslationProviders(&c.Translation)
			c.Translation.Providers[fromIdx], c.Translation.Providers[toIdx] = c.Translation.Providers[toIdx], c.Translation.Providers[fromIdx]
			for _, p := range c.Translation.Providers {
				if p.Enabled {
					c.Translation.Provider = p.Provider
					break
				}
			}
		})
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
	}

	v.translationRefreshNodes = func() {
		if v.translationNodesBox == nil {
			return
		}
		cur := v.currentConfig()
		tCfg := cur.Translation
		config.NormalizeTranslationProviders(&tCfg)

		var statuses map[string]translate.ProviderStatus
		if v.session != nil && v.session.Translator() != nil {
			statuses = v.session.Translator().ProviderStatuses()
		}
		now := time.Now()
		v.translationNodesBox.Objects = nil

		for idx, entry := range tCfg.Providers {
			rowIdx := idx
			nodeEntry := entry
			prov := config.NormalizeTranslationProvider(nodeEntry.Provider)
			st := statuses[prov]

			iconRes, badgeText, tipText := FormatTranslationNodeStatus(nodeEntry, st, now)

			dragGrip := newNodeDragHandle(func(deltaSteps int) {
				moveNode(rowIdx, deltaSteps)
			})

			upBtn := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				moveNode(rowIdx, -1)
			})
			if rowIdx == 0 {
				upBtn.Disable()
			}
			downBtn := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				moveNode(rowIdx, 1)
			})
			if rowIdx == len(tCfg.Providers)-1 {
				downBtn.Disable()
			}

			rankLbl := widget.NewLabelWithStyle(fmt.Sprintf("#%d", rowIdx+1), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

			enableChk := widget.NewCheck(TranslationProviderShortName(prov), func(checked bool) {
				v.updateProviderEntry(prov, func(e *config.TranslationProviderEntry) {
					e.Enabled = checked
				})
				if v.translationRefreshNodes != nil {
					v.translationRefreshNodes()
				}
			})
			enableChk.Checked = nodeEntry.Enabled

			hoverIcon := &hoverIconWidget{
				res:     iconRes,
				minSize: fyne.NewSize(20, 20),
				tooltip: tipText,
				onHover: v.showTranslationTooltip,
			}
			hoverIcon.ExtendBaseWidget(hoverIcon)

			badgeLbl := &hoverCellLabel{
				tooltip: tipText,
				onHover: v.showTranslationTooltip,
			}
			badgeLbl.Text = badgeText
			badgeLbl.TextStyle = fyne.TextStyle{Italic: true}
			badgeLbl.Truncation = fyne.TextTruncateOff
			badgeLbl.ExtendBaseWidget(badgeLbl)

			isConfiguring := v.translationSelectedNode == prov
			cfgBtnLabel := "Configure"
			cfgBtnIcon := theme.SettingsIcon()
			cfgBtnImp := widget.MediumImportance
			if isConfiguring {
				cfgBtnLabel = "Done"
				cfgBtnIcon = theme.ConfirmIcon()
				cfgBtnImp = widget.HighImportance
			}
			cfgBtn := widget.NewButtonWithIcon(cfgBtnLabel, cfgBtnIcon, func() {
				if v.translationSelectedNode == prov {
					v.translationSelectedNode = ""
				} else {
					v.translationSelectedNode = prov
				}
				v.syncTranslationProviderFields()
				if v.translationRefreshNodes != nil {
					v.translationRefreshNodes()
				}
			})
			cfgBtn.Importance = cfgBtnImp

			rightControls := []fyne.CanvasObject{hoverIcon, badgeLbl}
			if st.InBackoff(now) {
				resetNodeBtn := widget.NewButtonWithIcon("Reset", theme.ViewRefreshIcon(), func() {
					if v.session != nil && v.session.Translator() != nil {
						v.session.Translator().ResetProviderBackoff(prov)
					}
					if v.translationRefreshNodes != nil {
						v.translationRefreshNodes()
					}
					v.setStatus("✓ Cleared backoff cooldown for " + TranslationProviderShortName(prov))
				})
				rightControls = append(rightControls, resetNodeBtn)
			}
			rightControls = append(rightControls, cfgBtn)

			leftControls := container.NewHBox(
				container.NewGridWrap(fyne.NewSize(24, 32), dragGrip),
				upBtn,
				downBtn,
				rankLbl,
				enableChk,
			)
			row := container.NewBorder(nil, nil, leftControls, container.NewHBox(rightControls...), widget.NewLabel(""))
			v.translationNodesBox.Add(row)
		}
		v.translationNodesBox.Refresh()
	}

	resetAllBtn := widget.NewButtonWithIcon("Reset All Backoffs", theme.ViewRefreshIcon(), func() {
		if v.session != nil && v.session.Translator() != nil {
			v.session.Translator().ResetAllBackoffs()
		}
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
		v.setStatus("✓ Reset all translation provider backoff timers.")
	})

	routingHeader := container.NewBorder(
		nil,
		nil,
		container.NewGridWrap(fyne.NewSize(230, 36), widget.NewLabel("Routing & Failover Mode:")),
		resetAllBtn,
		v.translationRoutingSelect,
	)

	chainDesc := widget.NewLabelWithStyle(
		"Drag '≡' or click ▲/▼ to reorder providers. When a provider fails (HTTP 429, quota exhausted, or offline), TownCrier automatically cascades to the next enabled node and applies exponential backoff (15s → 30s → 1m → 2m → 5m → 15m → 30m → max 1h).",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	chainDesc.Wrapping = fyne.TextWrapWord

	// --- Collapsible Selected Provider Configuration Drawer ---
	v.translationLimitLabel = widget.NewLabel("")
	v.translationLimitLabel.Wrapping = fyne.TextWrapWord
	limitBannerRow := container.NewBorder(
		nil,
		nil,
		container.NewVBox(widget.NewIcon(theme.InfoIcon())),
		nil,
		v.translationLimitLabel,
	)

	v.translationGuideLink = widget.NewHyperlink("", nil)
	v.translationKeyLink = widget.NewHyperlink("", nil)
	v.translationTOSLink = widget.NewHyperlink("", nil)
	quotaURL, _ := url.Parse("https://console.cloud.google.com/iam-admin/quotas")
	v.translationQuotaLink = widget.NewHyperlink("Set $0 Daily Quota Cap (Prevent Overages)", quotaURL)
	v.translationQuotaSep = widget.NewLabel("•")
	linksRow := container.NewHBox(
		widget.NewIcon(theme.HelpIcon()),
		v.translationGuideLink,
		widget.NewLabel("•"),
		v.translationKeyLink,
		widget.NewLabel("•"),
		widget.NewIcon(theme.DocumentIcon()),
		v.translationTOSLink,
		v.translationQuotaSep,
		v.translationQuotaLink,
	)

	v.translationProviderSelect = widget.NewSelect(TranslationProviderOptions, func(selected string) {
		if v.initializing {
			return
		}
		prov := ParseTranslationProviderOption(selected)
		v.translationSelectedNode = prov
		v.syncTranslationProviderFields()
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
	})

	v.translationDoneConfigBtn = widget.NewButtonWithIcon("Done Configuring", theme.ConfirmIcon(), func() {
		v.translationSelectedNode = ""
		v.syncTranslationProviderFields()
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
	})
	v.translationDoneConfigBtn.Importance = widget.HighImportance

	configHeaderRow := makeFieldRow(
		"Configuring Provider Node:",
		container.NewBorder(nil, nil, nil, v.translationDoneConfigBtn, v.translationProviderSelect),
	)

	v.translationEmailEntry = NewScrollPassEntry()
	v.translationEmailEntry.SetPlaceHolder("Optional — Enter your email to boost free MyMemory quota 10x (50,000 chars/day, no signup)")
	v.translationEmailEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		v.updateProviderEntry(v.selectedTranslationProvider(), func(e *config.TranslationProviderEntry) {
			e.ContactEmail = trimmed
		})
		v.syncTranslationProviderFields()
	}
	v.translationEmailBox = makeFieldRow("Email (10x Free Quota):", v.translationEmailEntry)

	v.translationEndpointEntry = NewScrollPassEntry()
	v.translationEndpointEntry.SetPlaceHolder(config.DefaultLibreTranslateURL)
	v.translationEndpointEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		v.updateProviderEntry(v.selectedTranslationProvider(), func(e *config.TranslationProviderEntry) {
			e.EndpointURL = trimmed
		})
	}
	v.translationEndpointBox = makeFieldRow("Server Endpoint URL:", v.translationEndpointEntry)

	v.translationModelEntry = NewScrollPassEntry()
	v.translationModelEntry.SetPlaceHolder(config.DefaultOllamaModel + " (e.g. qwen2.5:3b, llama3.2:3b, gemma2:2b)")
	v.translationModelEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		v.updateProviderEntry(v.selectedTranslationProvider(), func(e *config.TranslationProviderEntry) {
			e.Model = trimmed
		})
	}
	v.translationModelBox = makeFieldRow("LLM Model Name:", v.translationModelEntry)

	v.translationAPIKeyEntry = NewScrollPassEntry()
	v.translationAPIKeyEntry.Password = true
	v.translationAPIKeyEntry.SetPlaceHolder("Optional — Leave blank for free zero-setup mode, or enter API key (stored in OS Keyring)")
	v.translationAPIKeyEntry.OnChanged = func(text string) {
		if v.initializing {
			return
		}
		trimmed := strings.TrimSpace(text)
		prov := v.selectedTranslationProvider()
		if v.session != nil {
			_ = v.session.SetTranslationProviderAPIKey(prov, trimmed)
		}
		v.updateProviderEntry(prov, func(e *config.TranslationProviderEntry) {
			e.APIKey = ""
		})
		v.syncTranslationProviderFields()
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
	}

	clearKeyBtn := widget.NewButtonWithIcon("Clear Key", theme.ContentClearIcon(), func() {
		prov := v.selectedTranslationProvider()
		v.translationAPIKeyEntry.SetText("")
		if v.session != nil {
			_ = v.session.SetTranslationProviderAPIKey(prov, "")
		}
		v.syncTranslationProviderFields()
		if v.translationRefreshNodes != nil {
			v.translationRefreshNodes()
		}
		v.setStatus("✓ Cleared API key for " + TranslationProviderShortName(prov))
	})
	v.translationAPIKeyBox = makeFieldRow("API Key (OS Keyring):", container.NewBorder(nil, nil, nil, clearKeyBtn, v.translationAPIKeyEntry))

	v.translationConfigDrawer = container.NewVBox(
		widget.NewSeparator(),
		configHeaderRow,
		limitBannerRow,
		linksRow,
		widget.NewSeparator(),
		v.translationEmailBox,
		v.translationEndpointBox,
		v.translationModelBox,
		v.translationAPIKeyBox,
	)

	v.translationTestEntry = NewScrollPassEntry()
	v.translationTestEntry.SetPlaceHolder("Enter foreign text (single or multi-sentence) to test...")
	v.translationTestEntry.SetText("Hola amigos, este directo está increíble hoy!")

	v.translationTestResultLbl = widget.NewLabelWithStyle("Click 'Test' to verify translation and inspect per-sentence cache hits.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	v.translationTestResultLbl.Wrapping = fyne.TextWrapWord

	v.translationTestBtn = widget.NewButtonWithIcon("Test Translation Chain", theme.MediaPlayIcon(), func() {
		sample := strings.TrimSpace(v.translationTestEntry.GetText())
		if sample == "" {
			sample = "Hola amigos, este directo está increíble hoy!"
		}
		curCfg := v.currentConfig()
		targetLang := config.NormalizeTranslationTargetLang(curCfg.Translation.TargetLang)
		targetName := translate.LanguageName(targetLang)
		detectedISO := translate.DetectSourceLang(sample, targetLang)
		detectedName := translate.LanguageName(detectedISO)
		targetProv := v.translationSelectedNode

		v.translationTestBtn.Disable()
		v.translationTestResultLbl.SetText(fmt.Sprintf("⏳ Detected Language: %s (%s) → Translating to %s (%s)...", detectedName, detectedISO, targetName, targetLang))

		go func(input, detISO, detName, tgtLang, tgtName, specificProv string) {
			var res translate.Result
			var elapsedStr string
			var err error
			if v.session != nil {
				r, dur, e := v.session.TestTranslationForProvider(context.Background(), specificProv, input)
				res = r
				elapsedStr = fmt.Sprintf("%dms", dur.Milliseconds())
				err = e
			} else {
				svc := translate.NewService()
				prov := v.selectedTranslationProvider()
				entry := v.providerEntryFor(prov)
				r, e := svc.TranslateChain(context.Background(), input, tgtLang, detISO, config.TranslationRoutingPriority, []translate.Options{
					{
						Provider:     prov,
						TargetLang:   tgtLang,
						SourceLang:   detISO,
						ContactEmail: entry.ContactEmail,
						EndpointURL:  entry.EndpointURL,
						Model:        entry.Model,
						APIKey:       strings.TrimSpace(v.translationAPIKeyEntry.GetText()),
					},
				})
				res = r
				elapsedStr = "ok"
				err = e
			}
			fyne.Do(func() {
				v.translationTestBtn.Enable()
				if v.translationRefreshNodes != nil {
					v.translationRefreshNodes()
				}
				if v.translationRefreshCacheStats != nil {
					v.translationRefreshCacheStats()
				}
				if err != nil {
					msg := fmt.Sprintf("⚠️ Input — %s (%s): %q\nError: %v", detName, detISO, input, err)
					v.translationTestResultLbl.SetText(msg)
					v.setStatus("⚠️ Translation test failed: " + err.Error())
					return
				}
				outMsg := FormatTestTranslationOutput(input, detName, detISO, tgtName, tgtLang, elapsedStr, res)
				v.translationTestResultLbl.SetText(outMsg)
				v.setStatus("✓ Translation test complete.")
			})
		}(sample, detectedISO, detectedName, targetLang, targetName, targetProv)
	})

	testRow := makeFieldRow("Test Sample Phrase:", container.NewBorder(nil, nil, nil, v.translationTestBtn, v.translationTestEntry))

	v.syncTranslationProviderFields()
	v.translationRefreshNodes()

	providerCard := widget.NewCard(
		"Translation Provider Chain, Load Balancing & Rate Limits",
		"Click 'Configure' on any provider row to edit its API key/endpoint and test that service directly",
		container.NewVBox(
			routingHeader,
			chainDesc,
			v.translationNodesBox,
			v.translationNodeHoverLbl,
			v.translationConfigDrawer,
			widget.NewSeparator(),
			testRow,
			v.translationTestResultLbl,
		),
	)

	scrollBody := newPanelVScroll(container.NewVBox(
		langCard,
		chimeCard,
		providerCard,
	))
	return container.NewStack(scrollBody, v.translationTooltipLayer)
}

