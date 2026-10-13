package main

import (
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"github.com/mrpoundsign/OctochorpeBot/internal/app"
	"github.com/mrpoundsign/OctochorpeBot/internal/audio"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/logging"
	"github.com/mrpoundsign/OctochorpeBot/internal/secrets"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
	"github.com/mrpoundsign/OctochorpeBot/internal/ui"
)

func parseLogFlagEarly() string {
	for i, arg := range os.Args {
		if (arg == "-log" || arg == "--log") && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
		if strings.HasPrefix(arg, "-log=") || strings.HasPrefix(arg, "--log=") {
			parts := strings.SplitN(arg, "=", 2)
			return parts[1]
		}
	}
	return ""
}

func main() {
	logPath, err := logging.Init(parseLogFlagEarly())
	if err != nil {
		log.Printf("Warning: failed to initialize persistent log file: %v", err)
	} else {
		log.Printf("=== OctochorpeBot Started (Log: %s) ===", logPath)
	}
	defer logging.Close()
	defer logging.RecoverAndExit("main")

	// Direct flag errors to logger
	flag.CommandLine.SetOutput(log.Writer())

	logFlag := flag.String("log", "", "Path to custom log file")
	twitchChannelFlag := flag.String("twitch-channel", "", "Twitch channel name to configure")
	flag.Parse()
	_ = logFlag

	myApp := fyneapp.NewWithID("com.github.mrpoundsign.octochorpebot")

	// 1. Pop up initial launch status window immediately
	tracker, launchWin := ui.NewLaunchWindow(myApp)
	launchWin.Show()

	// 2. Perform initialization in background goroutine so window renders instantly
	go func() {
		defer logging.RecoverAndExit("background-init")

		// Step 1: Configuration
		tracker.SetStatus("Loading configuration...")
		cfg, err := config.Load("")
		if err != nil {
			log.Printf("Using default config (load error: %v)", err)
			cfg = config.DefaultConfig()
			tracker.SetConfigStatus(false, "Using defaults")
		} else {
			log.Printf("Loaded config.json successfully")
			tracker.SetConfigStatus(true, "Loaded config.json")
		}

		if *twitchChannelFlag != "" {
			if cfg.Platforms == nil {
				cfg.Platforms = make(map[string]config.PlatformConfig)
			}
			p := cfg.Platforms["twitch"]
			p.AccountName = *twitchChannelFlag
			p.Enabled = true
			cfg.Platforms["twitch"] = p
			_ = config.Save("", cfg)
			log.Printf("Configured Twitch channel from CLI flag: %s", *twitchChannelFlag)
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
			log.Printf("Audio device initialized successfully")
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
			log.Printf("Found Kokoro models in %s. Initializing sherpa-onnx (threads=%d, gpu=%v)...", modelDir, threads, cfg.TTS.UseGPU)
			eng, engErr := tts.NewSherpaEngine(modelDir, threads, cfg.TTS.UseGPU)
			if engErr != nil {
				log.Printf("Warning: failed to initialize Kokoro TTS engine: %v", engErr)
				tracker.SetTTSStatus(false, "Init error: "+engErr.Error())
			} else {
				engine = eng
				log.Printf("Kokoro engine ready (provider=%s)", eng.Provider())
				tracker.SetTTSStatus(true, "Kokoro-82M int8 ("+eng.Provider()+")")
			}
		} else {
			log.Printf("Kokoro model not installed in %s", modelDir)
			tracker.SetTTSStatus(false, "Model not installed (available in Settings)")
		}

		// Brief pause so user can see all status items verified
		tracker.SetStatus("Ready! Opening main interface...")
		time.Sleep(400 * time.Millisecond)

		// Step 4: Initialize application session and open full MainWindow
		log.Println("Starting application session...")
		session := app.NewSession(cfg, secrets.NewKeyringStore(""))
		if player != nil {
			session.SetPlayer(player)
		}
		if engine != nil {
			session.SetEngine(engine)
		}
		if startErr := session.Start(); startErr != nil {
			log.Printf("Warning: session.Start() error: %v", startErr)
		}

		fyne.Do(func() {
			defer logging.RecoverAndExit("ui-init")
			log.Println("Creating MainWindow and rendering tabs...")
			mainWindow := ui.NewMainWindow(myApp, session)
			mainWindow.Window.SetMaster()
			mainWindow.Window.Show()
			log.Println("MainWindow rendered and shown. Closing launch window...")
			launchWin.Close()
			log.Println("Launch window closed.")
		})
	}()

	log.Println("Entering Fyne event loop...")
	myApp.Run()
	log.Println("Fyne event loop terminated.")
}

