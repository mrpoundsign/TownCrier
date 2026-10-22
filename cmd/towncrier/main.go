package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"github.com/mrpoundsign/TownCrier/assets"
	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/logging"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"github.com/mrpoundsign/TownCrier/internal/tts"
	"github.com/mrpoundsign/TownCrier/internal/ui"
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
	if err := run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func run() error {
	logPath, err := logging.Init(parseLogFlagEarly())
	if err != nil {
		log.Printf("Warning: failed to initialize persistent log file: %v", err)
	} else {
		log.Printf("=== TownCrier Started (Log: %s) ===", logPath)
	}
	defer logging.Close()
	defer logging.RecoverAndExit("main")

	// Direct flag errors to logger
	flag.CommandLine.SetOutput(log.Writer())

	logFlag := flag.String("log", "", "Path to custom log file")
	twitchChannelFlag := flag.String("twitch-channel", "", "Twitch channel name to configure")
	flag.Parse()
	_ = logFlag

	if os.Getenv("FYNE_DISABLE_DPI_DETECTION") == "" && os.Getenv("FYNE_SCALE") == "" {
		initialScale := ui.UIScale100
		if preCfg, cfgErr := config.Load(""); cfgErr == nil && preCfg != nil && preCfg.UIScale != "" {
			initialScale = preCfg.UIScale
		}
		ui.ApplyUIScale(nil, initialScale)
	}

	myApp := fyneapp.NewWithID("com.github.mrpoundsign.towncrier")
	myApp.SetIcon(assets.AppIcon)

	var activeSession *app.Session
	var activePlayer audio.Player
	var activeEngine tts.Engine

	// Register OS signal handler for graceful shutdown on SIGINT/SIGTERM (e.g. ^C)
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		sig, ok := <-sigChan
		if !ok {
			return
		}
		log.Printf("Received termination signal (%v), shutting down gracefully...", sig)
		fyne.Do(func() {
			myApp.Quit()
		})

		// If a second signal arrives before graceful shutdown completes, force immediate exit
		sig2, ok := <-sigChan
		if ok {
			log.Printf("Received second termination signal (%v), forcing exit...", sig2)
			os.Exit(1)
		}
	}()

	// 1. Pop up initial launch status window immediately
	tracker, launchWin := ui.NewLaunchWindow(myApp)
	launchWin.SetCloseIntercept(func() {
		log.Println("Launch window closed by user, aborting startup...")
		launchWin.Close()
		myApp.Quit()
	})
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
			activePlayer = player
			if cfg.Audio.Volume > 0 {
				player.SetVolume(cfg.Audio.Volume)
			}
			log.Printf("Audio device initialized successfully")
			tracker.SetAudioStatus(true, "WASAPI ready")
		}

		// Step 3 & 4: Neural TTS engine (auto-download if missing, hard gate before opening MainWindow)
		quitApp := func() {
			fyne.Do(func() {
				launchWin.Close()
				myApp.Quit()
			})
		}

		var initTTSAndLaunch func()
		initTTSAndLaunch = func() {
			defer logging.RecoverAndExit("tts-init")

			var engine tts.Engine
			if !cfg.TTS.Enabled {
				log.Println("TTS is disabled in configuration. Skipping model download checks and initialization.")
				tracker.SetTTSStatus(true, "Disabled")
			} else {
				modelDir := tts.DefaultModelDir()
				enabledModels := cfg.TTS.EffectiveEnabledModels()

				for _, fam := range enabledModels {
					if fam == tts.ModelKokoro || fam == tts.ModelKokoroIntl {
						if !tts.IsKokoroInstalledForMode(modelDir, cfg.TTS.UseGPU) {
							log.Printf("Kokoro models missing in %s; starting download...", modelDir)
							tracker.SetDownloadModel("Kokoro-82M")
							tracker.SetStatus("Downloading Kokoro-82M neural TTS models...")
							dlErr := tts.DownloadKokoroModelForMode(modelDir, cfg.TTS.UseGPU, tracker.SetDownloadProgress)
							if dlErr != nil {
								log.Printf("Error: failed to download Kokoro TTS models: %v", dlErr)
								tracker.ShowFatalError(
									"Failed to download required TTS models. Check your internet connection.",
									"Download failed ("+dlErr.Error()+")",
									initTTSAndLaunch,
									quitApp,
								)
								return
							}
							log.Printf("Kokoro models downloaded and extracted to %s", modelDir)
						}
						continue
					}

					if !tts.IsModelFamilyInstalled(modelDir, fam, cfg.TTS.UseGPU) {
						spec, _ := tts.ModelSpecByID(fam)
						log.Printf("Model pack %q missing in %s; starting download...", fam, modelDir)
						tracker.SetDownloadModel(spec.ShortName)
						tracker.SetStatus("Downloading " + spec.ShortName + " neural TTS models...")
						dlErr := tts.DownloadModelFamily(modelDir, fam, cfg.TTS.UseGPU, tracker.SetDownloadProgress)
						if dlErr != nil {
							log.Printf("Error: failed to download %s TTS models: %v", fam, dlErr)
							tracker.ShowFatalError(
								"Failed to download "+spec.ShortName+" TTS models. Check your internet connection.",
								"Download failed ("+dlErr.Error()+")",
								initTTSAndLaunch,
								quitApp,
							)
							return
						}
						log.Printf("Model pack %q downloaded and extracted", fam)
					}
				}

				tracker.SetStatus("Initializing neural TTS model(s)...")
				threads := cfg.TTS.NumThreads
				if threads <= 0 {
					threads = 4
				}
				maxClause := cfg.TTS.MaxClauseWords
				if maxClause <= 0 {
					maxClause = tts.DefaultMaxClauseWords
				}
				log.Printf("Initializing MultiEngine (models=%v, threads=%d, gpu=%v)...", enabledModels, threads, cfg.TTS.UseGPU)
				eng, engErr := tts.NewMultiEngine(modelDir, enabledModels, threads, cfg.TTS.UseGPU, maxClause, cfg.TTS.IdleUnloadMinutes)
				if engErr != nil {
					log.Printf("Error: failed to initialize TTS engine: %v", engErr)
					tracker.ShowFatalError(
						"Failed to initialize neural TTS engine.",
						"Init error: "+engErr.Error(),
						initTTSAndLaunch,
						quitApp,
					)
					return
				}

				engine = eng
				activeEngine = eng
				log.Printf("TTS engine ready (provider=%s)", eng.Provider())
				tracker.SetTTSStatus(true, eng.Provider())
			}

			// Brief pause so user can see all status items verified
			tracker.SetStatus("Ready! Opening main interface...")
			if cfg.TTS.Enabled {
				time.Sleep(400 * time.Millisecond)
			} else {
				time.Sleep(50 * time.Millisecond)
			}

			// Step 4: Initialize application session and open full MainWindow
			log.Println("Starting application session...")
			session := app.NewSession(cfg, secrets.NewKeyringStore(""))
			activeSession = session

			if sqlStore, logErr := eventlog.NewSQLiteBackend(cfg.Logging.DBPath); logErr != nil {
				log.Printf("Warning: failed to initialize SQLite event log store: %v", logErr)
			} else {
				logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
					ChatRetentionDays:  cfg.Logging.ChatRetentionDays,
					EventRetentionDays: cfg.Logging.EventRetentionDays,
				}, sqlStore)
				logMgr.SetEnabled(cfg.Logging.Enabled)
				logMgr.StartAutoPrune(15 * time.Minute)
				session.SetEventLogger(logMgr)
				log.Printf("Event log initialized at %s", sqlStore.Path())
			}

			if player != nil {
				session.SetPlayer(player)
			}
			session.SetEngine(engine)
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
		}

		initTTSAndLaunch()
	}()

	log.Println("Entering Fyne event loop...")
	myApp.Run()
	log.Println("Fyne event loop terminated. Shutting down subsystems...")

	if activeSession != nil {
		_ = activeSession.Close()
		if p := activeSession.Player(); p != nil {
			_ = p.Close()
		}
		if e := activeSession.Engine(); e != nil {
			_ = e.Close()
		}
	} else {
		if activePlayer != nil {
			_ = activePlayer.Close()
		}
		if activeEngine != nil {
			_ = activeEngine.Close()
		}
	}
	log.Println("Subsystems shutdown complete. Process exiting.")
	return nil
}

