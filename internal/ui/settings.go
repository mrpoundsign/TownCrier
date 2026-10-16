package ui

import (
	"fmt"
	"path/filepath"
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
	deviceEnum        func() ([]audio.DeviceInfo, error)
	statusLabel       *widget.Label
	onStatus          func(msg string)
	onHotkeyChanged   func(hotkey string) error
	onServicesChanged func()
	twitchCheck       *widget.Check
	youtubeCheck      *widget.Check
	tiktokCheck       *widget.Check
	kickCheck         *widget.Check
	modelStatus       *widget.Label
	deviceSelect      *widget.Select
	deviceMap         map[string]string // device name -> hex ID
	downloadBtn       *widget.Button
	downloadProg      *widget.ProgressBar
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

// SyncFromConfig updates the service checkboxes in Settings when modified from a platform tab.
func (v *SettingsView) SyncFromConfig() {
	if v.session != nil {
		v.cfg = v.session.Config()
	}
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

// Build generates the Fyne CanvasObject containing the Settings interface.
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

	content := container.NewVBox(
		servicesCard,
		audioCard,
		ttsCard,
		rulesCard,
		safetyCard,
		botsCard,
	)

	return newPanelVScroll(content)
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
	installed := tts.IsModelInstalled(v.modelDir)
	statusText := fmt.Sprintf("Kokoro Model: Installed (%s)", filepath.Base(v.modelDir))
	if !installed {
		statusText = "Kokoro Model: Not Found in " + v.modelDir
	}
	v.modelStatus = widget.NewLabel(statusText)

	v.downloadProg = widget.NewProgressBar()
	v.downloadProg.Hide()

	v.downloadBtn = widget.NewButtonWithIcon("Download Kokoro Model", theme.DownloadIcon(), func() {
		v.startModelDownload()
	})
	if installed {
		v.downloadBtn.Hide()
	}

	gpuCheck := widget.NewCheck("Use GPU Acceleration (DirectML)", func(checked bool) {
		if v.initializing || v.cfg.TTS.UseGPU == checked {
			return
		}
		v.updateConfig(func(c *config.Config) {
			c.TTS.UseGPU = checked
		})
		if tts.IsModelInstalled(v.modelDir) {
			go func() {
				threads := v.cfg.TTS.NumThreads
				if threads <= 0 {
					threads = 4
				}
				eng, err := tts.NewSherpaEngine(v.modelDir, threads, checked)
				fyne.Do(func() {
					if err != nil {
						v.setStatus(fmt.Sprintf("Failed to switch GPU acceleration: %v", err))
					} else {
						v.mu.Lock()
						if v.engine != nil {
							_ = v.engine.Close()
						}
						v.engine = eng
						v.mu.Unlock()
						if v.session != nil {
							v.session.SetEngine(eng)
						}
						v.setStatus("TTS engine reloaded: Provider is " + eng.Provider())
					}
				})
			}()
		}
	})
	gpuCheck.SetChecked(v.cfg.TTS.UseGPU)

	cardContent := container.NewVBox(
		v.modelStatus,
		v.downloadProg,
		v.downloadBtn,
		gpuCheck,
	)

	return widget.NewCard("TTS Engine & Hardware", "Kokoro neural TTS model and DirectML GPU acceleration", cardContent)
}

func (v *SettingsView) startModelDownload() {
	v.downloadBtn.Disable()
	v.downloadProg.Show()
	v.downloadProg.SetValue(0)
	v.setStatus("Downloading Kokoro-82M neural model files...")

	go func() {
		err := tts.DownloadKokoroModel(v.modelDir, func(current, total int64) {
			if total > 0 {
				fyne.Do(func() {
					v.downloadProg.SetValue(float64(current) / float64(total))
				})
			}
		})

		fyne.Do(func() {
			v.downloadProg.Hide()
			if err != nil {
				v.downloadBtn.Enable()
				v.setStatus(fmt.Sprintf("Download failed: %v", err))
				dialog.ShowError(err, v.window)
				return
			}

			v.modelStatus.SetText(fmt.Sprintf("Kokoro Model: Installed (%s)", filepath.Base(v.modelDir)))
			v.downloadBtn.Hide()
			v.setStatus("Kokoro model downloaded and verified! Initializing engine...")

			// Initialize engine
			threads := v.cfg.TTS.NumThreads
			if threads <= 0 {
				threads = 4
			}
			eng, engErr := tts.NewSherpaEngine(v.modelDir, threads, v.cfg.TTS.UseGPU)
			if engErr != nil {
				v.setStatus(fmt.Sprintf("Failed to initialize TTS engine: %v", engErr))
				return
			}
			v.mu.Lock()
			v.engine = eng
			v.mu.Unlock()
			v.setStatus("Kokoro TTS engine ready.")
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
