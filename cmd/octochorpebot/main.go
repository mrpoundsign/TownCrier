package main

import (
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"github.com/mrpoundsign/OctochorpeBot/internal/audio"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
	"github.com/mrpoundsign/OctochorpeBot/internal/ui"
)

func main() {
	myApp := app.NewWithID("com.github.mrpoundsign.octochorpebot")

	// 1. Pop up initial launch status window immediately
	tracker, launchWin := ui.NewLaunchWindow(myApp)
	launchWin.Show()

	// 2. Perform initialization in background goroutine so window renders instantly
	go func() {
		// Step 1: Configuration
		tracker.SetStatus("Loading configuration...")
		cfg, err := config.Load("")
		if err != nil {
			log.Printf("Using default config (load error: %v)", err)
			cfg = config.DefaultConfig()
			tracker.SetConfigStatus(false, "Using defaults")
		} else {
			tracker.SetConfigStatus(true, "Loaded config.json")
		}

		// Step 2: Audio subsystem
		tracker.SetStatus("Connecting to audio subsystem...")
		player, err := audio.NewPlayer(cfg.Audio.DeviceID)
		if err != nil {
			log.Printf("Warning: failed to open target audio device: %v", err)
			tracker.SetAudioStatus(false, "Using fallback")
		} else {
			if cfg.Audio.Volume > 0 {
				player.SetVolume(cfg.Audio.Volume)
			}
			tracker.SetAudioStatus(true, "WASAPI ready")
		}

		// Step 3: Neural TTS engine
		tracker.SetStatus("Initializing Kokoro neural TTS model...")
		var engine tts.Engine
		modelDir := tts.DefaultModelDir()
		if tts.IsModelInstalled(modelDir) {
			threads := cfg.TTS.NumThreads
			if threads <= 0 {
				threads = 4
			}
			eng, engErr := tts.NewSherpaEngine(modelDir, threads, cfg.TTS.UseGPU)
			if engErr != nil {
				log.Printf("Warning: failed to initialize Kokoro TTS engine: %v", engErr)
				tracker.SetTTSStatus(false, "Init error: "+engErr.Error())
			} else {
				engine = eng
				tracker.SetTTSStatus(true, "Kokoro-82M int8 ("+eng.Provider()+")")
			}
		} else {
			tracker.SetTTSStatus(false, "Model not installed (available in Settings)")
		}

		// Brief pause so user can see all status items verified
		tracker.SetStatus("Ready! Opening main interface...")
		time.Sleep(400 * time.Millisecond)

		// Step 4: Open main window and close launcher window
		fyne.Do(func() {
			mainWin := myApp.NewWindow("OctochorpeBot")
			settingsView := ui.NewSettingsView(mainWin, cfg, player, engine)

			tabs := container.NewAppTabs(
				container.NewTabItemWithIcon("Settings & Hardware Test", theme.SettingsIcon(), settingsView.Build()),
			)

			mainWin.SetContent(tabs)
			mainWin.Resize(fyne.NewSize(750, 650))
			mainWin.CenterOnScreen()
			mainWin.SetOnClosed(func() {
				if player != nil {
					_ = player.Close()
				}
				if engine != nil {
					_ = engine.Close()
				}
			})

			mainWin.Show()
			launchWin.Close()
		})
	}()

	myApp.Run()
}

