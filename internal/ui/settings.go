package ui

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
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
	onStatus          func(msg string)
	onHotkeyChanged   func(hotkey string) error
	onServicesChanged func()
	onModelsChanged   func()
	onAssignVoice     func(platform, username string)
	subTabs           *container.AppTabs
	logsView          *LogsView
	twitchCheck       *widget.Check
	youtubeCheck      *widget.Check
	tiktokCheck       *widget.Check
	kickCheck         *widget.Check
	modelStatus       *widget.Label
	deviceSelect      *widget.Select
	deviceMap         map[string]string // device name -> hex ID
	initializing      bool
}

// NewSettingsView constructs a SettingsView for configuring audio, TTS hardware, and global rules.
func NewSettingsView(w fyne.Window, cfg *config.Config, player audio.Player, engine tts.Engine) *SettingsView {
	return &SettingsView{
		window:     w,
		cfg:        cfg,
		player:     player,
		engine:     engine,
		modelDir:   tts.DefaultModelDir(),
		deviceMap:  make(map[string]string),
		deviceEnum: audio.EnumeratePlaybackDevices,
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
	if v.logsView != nil {
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
		_ = config.Save("", v.cfg)
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

	servicesCard := v.buildServicesCard()
	audioCard := v.buildAudioCard()
	ttsCard := v.buildTTSCard()
	rulesCard := v.buildRulesCard()
	safetyCard := v.buildSafetyCard()
	botsCard := v.buildBotsCard()

	generalTabContent := newPanelVScroll(container.NewVBox(
		servicesCard,
		audioCard,
	))
	ttsTabContent := newPanelVScroll(container.NewVBox(
		ttsCard,
	))
	rulesTabContent := newPanelVScroll(container.NewVBox(
		rulesCard,
		safetyCard,
		botsCard,
	))

	v.logsView = NewLogsView(v.window, v.session)
	v.logsView.SetStatusHandler(v.setStatus)
	v.logsView.SetOnAssignVoice(v.onAssignVoice)
	logsTabContent := newPanelVScroll(container.NewVBox(
		v.logsView.BuildRetentionCard(),
	))

	generalTabItem := container.NewTabItemWithIcon("General & Audio", theme.VolumeUpIcon(), generalTabContent)
	ttsTabItem := container.NewTabItemWithIcon("TTS & Hardware", theme.ComputerIcon(), ttsTabContent)
	rulesTabItem := container.NewTabItemWithIcon("Rules & Safety", theme.ConfirmIcon(), rulesTabContent)
	logsTabItem := container.NewTabItemWithIcon("Event Logs", theme.DocumentIcon(), logsTabContent)

	v.subTabs = container.NewAppTabs(
		generalTabItem,
		ttsTabItem,
		rulesTabItem,
		logsTabItem,
	)

	return v.subTabs
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

	return widget.NewCard("Streaming Services", "Enable or disable streaming platforms to show/hide their tabs and load/unload their connectors", cardContent)
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

func (v *SettingsView) buildTTSCard() *widget.Card {
	v.modelStatus = widget.NewLabel(v.formatModelStatusText())

	var syncingControls bool

	// --- Hardware Impact Stacked Graphs & Tradeoff Notice ---
	ramDetail := widget.NewLabel("")
	ramBar := NewStackedImpactBar()
	cpuDetail := widget.NewLabel("")
	cpuBar := NewStackedImpactBar()
	gpuDetail := widget.NewLabel("")
	gpuBar := NewStackedImpactBar()
	latencyDetail := widget.NewLabel("")
	latencyBar := NewStackedImpactBar()

	tradeoffLabel := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	tradeoffLabel.Wrapping = fyne.TextWrapWord

	legendRow := container.NewGridWrap(
		fyne.NewSize(145, 24),
		NewModelColorBadge("base", "Base App", false),
		NewModelColorBadge(tts.ModelSupertonic, "Supertonic INT8", false),
		NewModelColorBadge(tts.ModelKokoro, "Kokoro-82M", false),
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
	presetDescLabel := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	presetDescLabel.Wrapping = fyne.TextWrapWord

	gpuCheck := widget.NewCheck("Use GPU Acceleration for Kokoro-82M (DirectML — fast ~0.3s start, uses ~700 MB extra RAM)", nil)

	initThreads := v.cfg.TTS.NumThreads
	if initThreads <= 0 {
		initThreads = 4
	}
	threadsValLabel := widget.NewLabel(fmt.Sprintf("%d threads", initThreads))
	threadsSlider := widget.NewSlider(1, 12)
	threadsSlider.Step = 1
	threadsSlider.SetValue(float64(initThreads))

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

	// --- Voice Model Pack Checkboxes ---
	modelChecks := make(map[string]*widget.Check, len(tts.AllModelFamilies))
	modelPackRows := container.NewVBox()

	formatPackCheckLabel := func(spec tts.ModelFamilySpec) string {
		cfg := v.currentConfig()
		status := fmt.Sprintf("Downloads ~%d MB on enable", spec.DownloadSizeMB)
		if tts.IsModelFamilyInstalled(v.modelDir, spec.ID, cfg.TTS.UseGPU) {
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
		if enabledSet[tts.ModelKokoro] {
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
	}

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

			if slices.Contains(selectedModels, tts.ModelKokoro) {
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
		widget.NewLabel("CPU Inference Threads (4 recommended for INT8 SIMD):"),
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
	v.mu.Unlock()

	go func() {
		// 1. Detach and close the existing engine FIRST so old and new models are never held in RAM simultaneously
		v.mu.Lock()
		if v.reloadSeq != mySeq {
			v.mu.Unlock()
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
		for _, fam := range enabledModels {
			v.mu.Lock()
			canceled := v.reloadSeq != mySeq
			v.mu.Unlock()
			if canceled {
				return
			}

			if fam == tts.ModelKokoro {
				if !tts.IsKokoroInstalledForMode(v.modelDir, useGPU) {
					v.setStatus("Downloading Kokoro-82M model files...")
					if dlErr := tts.DownloadKokoroModelForMode(v.modelDir, useGPU, func(cur, tot int64) {
						if tot > 0 {
							v.setStatus(fmt.Sprintf("Downloading Kokoro-82M (%d%%)...", cur*100/tot))
						}
					}); dlErr != nil {
						v.setStatus(fmt.Sprintf("Kokoro download failed (%v); falling back to installed files", dlErr))
						if useGPU && tts.IsCPUModelInstalled(v.modelDir) {
							useGPU = false
							v.updateConfig(func(c *config.Config) {
								c.TTS.UseGPU = false
							})
						}
					}
				}
				continue
			}

			if !tts.IsModelFamilyInstalled(v.modelDir, fam, useGPU) {
				spec, _ := tts.ModelSpecByID(fam)
				v.setStatus(fmt.Sprintf("Downloading %s (~%d MB)...", spec.ShortName, spec.DownloadSizeMB))
				if dlErr := tts.DownloadModelFamily(v.modelDir, fam, useGPU, func(cur, tot int64) {
					if tot > 0 {
						v.setStatus(fmt.Sprintf("Downloading %s (%d%%)...", spec.ShortName, cur*100/tot))
					}
				}); dlErr != nil {
					v.setStatus(fmt.Sprintf("⚠️ Failed to download %s: %v", spec.ShortName, dlErr))
				}
			}
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

func (v *SettingsView) buildRulesCard() *widget.Card {
	checkNames := widget.NewCheck("Speak Chatter Names (e.g. 'User says...')", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakNames = b
		})
	})
	checkNames.SetChecked(v.cfg.GlobalChatRules.SpeakNames)

	checkEmotes := widget.NewCheck("Speak Platform Emotes (unchecked = strip emotes)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakEmotes = b
		})
	})
	checkEmotes.SetChecked(v.cfg.GlobalChatRules.SpeakEmotes)

	checkPlatform := widget.NewCheck("Speak Platform Name Prefix (e.g. '[Twitch]')", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			c.GlobalChatRules.SpeakPlatformName = b
		})
	})
	checkPlatform.SetChecked(v.cfg.GlobalChatRules.SpeakPlatformName)

	cardContent := container.NewVBox(
		checkNames,
		checkEmotes,
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

	var syncingHotkeyUI bool

	ctrlCheck := widget.NewCheck("Ctrl", nil)
	shiftCheck := widget.NewCheck("Shift", nil)
	altCheck := widget.NewCheck("Alt", nil)
	keySelect := widget.NewSelect(SupportedHotkeyKeys, nil)

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

	resetHotkeyBtn := widget.NewButtonWithIcon("Reset Default", theme.ViewRefreshIcon(), func() {
		applyNewHotkey("Ctrl+Shift+M")
	})

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

	wordListContainer := container.NewVBox()

	var refreshWords func()
	refreshWords = func() {
		wordListContainer.Objects = nil
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
				row := container.NewBorder(nil, nil, widget.NewLabel("• "+word), delBtn)
				wordListContainer.Add(row)
			}
		}
		wordListContainer.Refresh()
	}

	refreshWords()

	newWordEntry := NewScrollPassEntry()
	newWordEntry.SetPlaceHolder("Enter word or pattern to block (e.g. slurs, bad words)")

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

	addRow := container.NewBorder(nil, nil, nil, addBtn, newWordEntry)

	cardContent := container.NewVBox(
		hotkeyDisplay,
		hotkeyControlsRow,
		widget.NewSeparator(),
		widget.NewLabel("Blocked Words (masked with '***' before TTS):"),
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

	cardContent := container.NewVBox(
		knownBotsCheck,
	)

	return widget.NewCard("Bot Filters", "Filter out automated stream bots", cardContent)
}
