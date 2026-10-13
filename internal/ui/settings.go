package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/OctochorpeBot/internal/app"
	"github.com/mrpoundsign/OctochorpeBot/internal/audio"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
)

// SettingsView provides the UI controls for audio, TTS, and global rules QA & configuration.
type SettingsView struct {
	window   fyne.Window
	cfg      *config.Config
	player   audio.Player
	engine   tts.Engine
	session  *app.Session
	modelDir string

	mu           sync.Mutex
	deviceEnum   func() ([]audio.DeviceInfo, error)
	statusLabel  *widget.Label
	modelStatus  *widget.Label
	deviceSelect *widget.Select
	deviceMap    map[string]string // device name -> hex ID
	downloadBtn  *widget.Button
	downloadProg *widget.ProgressBar
	speakBtn     *widget.Button
}

// NewSettingsView constructs a SettingsView for testing and configuring audio and speech.
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

// Build generates the Fyne CanvasObject containing the full Settings & Test interface.
func (v *SettingsView) Build() fyne.CanvasObject {
	v.statusLabel = widget.NewLabelWithStyle("Ready.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	// Register device fallback listener if player is present
	if v.player != nil {
		v.player.SetFallbackListener(func(reason string) {
			fyne.Do(func() {
				v.statusLabel.SetText("⚠️ Audio Alert: " + reason)
				dialog.ShowInformation("Audio Device Fallback", reason, v.window)
			})
		})
	}

	audioCard := v.buildAudioCard()
	ttsCard := v.buildTTSCard()
	rulesCard := v.buildRulesCard()
	safetyCard := v.buildSafetyCard()
	botsCard := v.buildBotsCard()
	bottomBar := v.buildBottomBar()

	content := container.NewVBox(
		audioCard,
		ttsCard,
		rulesCard,
		safetyCard,
		botsCard,
		bottomBar,
	)

	return container.NewVScroll(content)
}

func (v *SettingsView) buildAudioCard() *widget.Card {
	v.deviceSelect = widget.NewSelect([]string{"Default Audio Device"}, func(selected string) {
		v.mu.Lock()
		devHex := v.deviceMap[selected]
		v.cfg.Audio.DeviceName = selected
		v.cfg.Audio.DeviceID = devHex
		v.mu.Unlock()

		if v.player != nil {
			go func() {
				err := v.player.SwitchDevice(devHex)
				fyne.Do(func() {
					if err != nil {
						v.statusLabel.SetText(fmt.Sprintf("Failed to switch audio device: %v", err))
					} else {
						v.statusLabel.SetText("Active audio device: " + selected)
					}
				})
			}()
		}
	})

	v.refreshDeviceList()

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshDeviceList()
		v.statusLabel.SetText("Audio device list refreshed.")
	})

	deviceRow := container.NewBorder(nil, nil, widget.NewLabel("Output Device:"), refreshBtn, v.deviceSelect)

	vol := v.cfg.Audio.Volume
	if vol <= 0 {
		vol = 1.0
	}
	volLabel := widget.NewLabel(fmt.Sprintf("Volume: %d%%", int(vol*100)))
	volSlider := widget.NewSlider(0, 100)
	volSlider.SetValue(float64(vol * 100))
	volSlider.OnChanged = func(val float64) {
		gain := float32(val / 100.0)
		v.cfg.Audio.Volume = gain
		volLabel.SetText(fmt.Sprintf("Volume: %d%%", int(val)))
		if v.player != nil {
			v.player.SetVolume(gain)
		}
	}
	volumeRow := container.NewBorder(nil, nil, volLabel, nil, volSlider)

	panicBtn := widget.NewButtonWithIcon("EMERGENCY PANIC STOP (Instant Silence)", theme.MediaStopIcon(), func() {
		if v.player != nil {
			v.player.PanicStop()
		}
		v.statusLabel.SetText("🚨 Emergency panic stop triggered! Audio queue silenced.")
	})
	panicBtn.Importance = widget.DangerImportance

	resetPanicBtn := widget.NewButton("Reset Panic State", func() {
		if v.player != nil {
			v.player.ResetPanic()
		}
		v.statusLabel.SetText("Panic state cleared. Audio playback re-enabled.")
	})

	panicRow := container.NewGridWithColumns(2, panicBtn, resetPanicBtn)

	cardContent := container.NewVBox(
		deviceRow,
		volumeRow,
		panicRow,
	)

	return widget.NewCard("Audio Output & Playback", "WASAPI device selection and master volume", cardContent)
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

	selectedVoice := v.cfg.GlobalChatRules.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = tts.DefaultVoiceID
	}
	voiceSelect := widget.NewSelect(tts.KokoroVoiceNames, func(s string) {
		v.cfg.GlobalChatRules.DefaultVoice = s
	})
	voiceSelect.SetSelected(selectedVoice)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("Default Voice:"), nil, voiceSelect)

	speed := v.cfg.GlobalChatRules.DefaultSpeed
	if speed <= 0 {
		speed = 1.0
	}
	speedLabel := widget.NewLabel(fmt.Sprintf("Speed: %.2fx", speed))
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.Step = 0.05
	speedSlider.SetValue(float64(speed))
	speedSlider.OnChanged = func(val float64) {
		v.cfg.GlobalChatRules.DefaultSpeed = float32(val)
		speedLabel.SetText(fmt.Sprintf("Speed: %.2fx", val))
	}
	speedRow := container.NewBorder(nil, nil, speedLabel, nil, speedSlider)

	testEntry := widget.NewEntry()
	testEntry.SetText("Hello! OctochorpeBot audio output and Kokoro TTS are working properly.")

	v.speakBtn = widget.NewButtonWithIcon("Test Speak", theme.MediaPlayIcon(), func() {
		text := strings.TrimSpace(testEntry.Text)
		if text == "" {
			return
		}
		v.testSpeak(text, voiceSelect.Selected, float32(speedSlider.Value))
	})
	v.speakBtn.Importance = widget.HighImportance

	gpuCheck := widget.NewCheck("Use GPU Acceleration (DirectML)", func(checked bool) {
		if v.cfg.TTS.UseGPU == checked {
			return
		}
		v.cfg.TTS.UseGPU = checked
		if tts.IsModelInstalled(v.modelDir) {
			go func() {
				threads := v.cfg.TTS.NumThreads
				if threads <= 0 {
					threads = 4
				}
				eng, err := tts.NewSherpaEngine(v.modelDir, threads, checked)
				fyne.Do(func() {
					if err != nil {
						v.statusLabel.SetText(fmt.Sprintf("Failed to switch GPU acceleration: %v", err))
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
						v.statusLabel.SetText("TTS engine reloaded: Provider is " + eng.Provider())
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
		voiceRow,
		speedRow,
		widget.NewLabel("Test Speech Text:"),
		testEntry,
		v.speakBtn,
	)

	return widget.NewCard("Kokoro TTS & Voice Preview", "Synthesize neural speech and preview voices", cardContent)
}

func (v *SettingsView) startModelDownload() {
	v.downloadBtn.Disable()
	v.downloadProg.Show()
	v.downloadProg.SetValue(0)
	v.statusLabel.SetText("Downloading Kokoro-82M neural model files...")

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
				v.statusLabel.SetText(fmt.Sprintf("Download failed: %v", err))
				dialog.ShowError(err, v.window)
				return
			}

			v.modelStatus.SetText(fmt.Sprintf("Kokoro Model: Installed (%s)", filepath.Base(v.modelDir)))
			v.downloadBtn.Hide()
			v.statusLabel.SetText("Kokoro model downloaded and verified! Initializing engine...")

			// Initialize engine
			threads := v.cfg.TTS.NumThreads
			if threads <= 0 {
				threads = 4
			}
			eng, engErr := tts.NewSherpaEngine(v.modelDir, threads, v.cfg.TTS.UseGPU)
			if engErr != nil {
				v.statusLabel.SetText(fmt.Sprintf("Failed to initialize TTS engine: %v", engErr))
				return
			}
			v.mu.Lock()
			v.engine = eng
			v.mu.Unlock()
			v.statusLabel.SetText("Kokoro TTS engine ready.")
		})
	}()
}

func (v *SettingsView) testSpeak(text, voice string, speed float32) {
	v.mu.Lock()
	eng := v.engine
	v.mu.Unlock()

	if eng == nil {
		// Attempt lazy load if installed
		if tts.IsModelInstalled(v.modelDir) {
			var err error
			threads := v.cfg.TTS.NumThreads
			if threads <= 0 {
				threads = 4
			}
			eng, err = tts.NewSherpaEngine(v.modelDir, threads, v.cfg.TTS.UseGPU)
			if err != nil {
				v.statusLabel.SetText(fmt.Sprintf("Failed to load TTS engine: %v", err))
				return
			}
			v.mu.Lock()
			v.engine = eng
			v.mu.Unlock()
		} else {
			v.statusLabel.SetText("TTS engine not loaded: model is not installed.")
			return
		}
	}

	v.speakBtn.Disable()
	v.statusLabel.SetText(fmt.Sprintf("Synthesizing '%s' with voice '%s'...", text, voice))

	go func() {
		t0 := time.Now()
		samples, sampleRate, err := eng.Synthesize(text, voice, speed)
		dur := time.Since(t0)

		fyne.Do(func() {
			v.speakBtn.Enable()
			if err != nil {
				v.statusLabel.SetText(fmt.Sprintf("Synthesis failed: %v", err))
				return
			}

			audioDur := float64(len(samples)) / float64(sampleRate)
			rtf := dur.Seconds() / audioDur
			status := fmt.Sprintf("Synthesized in %v (audio: %.1fs, RTF: %.2fx). Playing...",
				dur.Round(time.Millisecond), audioDur, rtf)
			v.statusLabel.SetText(status)

			if v.player != nil {
				v.player.ResetPanic()
				if playErr := v.player.Play(samples, sampleRate); playErr != nil {
					v.statusLabel.SetText(fmt.Sprintf("Playback error: %v", playErr))
				}
			}
		})
	}()
}

func (v *SettingsView) buildRulesCard() *widget.Card {
	checkNames := widget.NewCheck("Speak Chatter Names (e.g. 'User says...')", func(b bool) {
		v.cfg.GlobalChatRules.SpeakNames = b
	})
	checkNames.SetChecked(v.cfg.GlobalChatRules.SpeakNames)

	checkEmotes := widget.NewCheck("Speak Platform Emotes (unchecked = strip emotes)", func(b bool) {
		v.cfg.GlobalChatRules.SpeakEmotes = b
	})
	checkEmotes.SetChecked(v.cfg.GlobalChatRules.SpeakEmotes)

	checkPlatform := widget.NewCheck("Speak Platform Name Prefix (e.g. '[Twitch]')", func(b bool) {
		v.cfg.GlobalChatRules.SpeakPlatformName = b
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
	hotkeyLabel := widget.NewLabelWithStyle("Global Panic Mute Hotkey: Ctrl+Shift+M or Esc (<50ms halt)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	wordListContainer := container.NewVBox()

	refreshWords := func() {
		wordListContainer.Objects = nil
		if len(v.cfg.Safety.BlockedWords) == 0 {
			wordListContainer.Add(widget.NewLabelWithStyle("No words currently blocked.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
		} else {
			for _, w := range v.cfg.Safety.BlockedWords {
				word := w
				delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
					var filtered []string
					for _, item := range v.cfg.Safety.BlockedWords {
						if item != word {
							filtered = append(filtered, item)
						}
					}
					v.cfg.Safety.BlockedWords = filtered
					v.statusLabel.SetText(fmt.Sprintf("Removed %q from blocklist.", word))
				})
				delBtn.Importance = widget.LowImportance
				row := container.NewBorder(nil, nil, widget.NewLabel("• "+word), delBtn)
				wordListContainer.Add(row)
			}
		}
		wordListContainer.Refresh()
	}

	refreshWords()

	newWordEntry := widget.NewEntry()
	newWordEntry.SetPlaceHolder("Enter word or pattern to block (e.g. slurs, bad words)")

	addBtn := widget.NewButtonWithIcon("Block Word", theme.ContentAddIcon(), func() {
		text := strings.TrimSpace(newWordEntry.Text)
		if text == "" {
			return
		}
		v.cfg.Safety.BlockedWords = append(v.cfg.Safety.BlockedWords, text)
		newWordEntry.SetText("")
		refreshWords()
		v.statusLabel.SetText(fmt.Sprintf("Added %q to blocklist.", text))
	})

	addRow := container.NewBorder(nil, nil, nil, addBtn, newWordEntry)

	cardContent := container.NewVBox(
		hotkeyLabel,
		widget.NewLabel("Blocked Words (masked with '***' before TTS):"),
		wordListContainer,
		addRow,
	)

	return widget.NewCard("Streamer Safety & Blocklist", "Emergency controls and profanity filters", cardContent)
}

func (v *SettingsView) buildBotsCard() *widget.Card {
	knownBotsCheck := widget.NewCheck("Ignore Known Stream Bots (Nightbot, StreamElements, Fossabot, etc.)", func(b bool) {
		v.cfg.Bots.IgnoreKnownBots = b
	})
	knownBotsCheck.SetChecked(v.cfg.Bots.IgnoreKnownBots)

	cardContent := container.NewVBox(
		knownBotsCheck,
	)

	return widget.NewCard("Bot Filters", "Filter out automated stream bots", cardContent)
}

func (v *SettingsView) buildBottomBar() fyne.CanvasObject {
	saveBtn := widget.NewButtonWithIcon("Save Settings", theme.DocumentSaveIcon(), func() {
		if err := config.Save("", v.cfg); err != nil {
			v.statusLabel.SetText(fmt.Sprintf("Failed to save settings: %v", err))
			dialog.ShowError(err, v.window)
		} else {
			v.statusLabel.SetText("Settings saved successfully to config.json.")
			dialog.ShowInformation("Saved", "Configuration saved successfully!", v.window)
		}
	})
	saveBtn.Importance = widget.HighImportance

	return container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, saveBtn, nil, v.statusLabel),
	)
}
