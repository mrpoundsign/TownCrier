package ui

import (
	"fmt"
	"log"
	"slices"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// MainWindow coordinates the primary Fyne desktop window, tabs, header bar, and global panic hotkeys.
type MainWindow struct {
	Window  fyne.Window
	Session *app.Session

	headerBar     *fyne.Container
	tabsContainer *container.AppTabs
	statusBar     *fyne.Container
	statusLabel   *widget.Label

	dashboardTab *DashboardView
	twitchTab    *TwitchTabView
	youtubeTab   *YouTubeTabView
	tiktokTab    *TikTokTabView
	voicesTab    *VoicesTabView
	viewersTab   *ViewersTabView
	settingsTab  *SettingsView

	dashboardTabItem *container.TabItem
	twitchTabItem    *container.TabItem
	youtubeTabItem   *container.TabItem
	tiktokTabItem    *container.TabItem
	voicesTabItem    *container.TabItem
	viewersTabItem   *container.TabItem
	settingsTabItem  *container.TabItem

	twitchStatusDot  *widget.Label
	youtubeStatusDot *widget.Label
	tiktokStatusDot  *widget.Label
	volumeSlider     *widget.Slider
	panicBtn         *widget.Button
	hotkeyMgr        *GlobalHotkeyManager
	activeShortcut   *desktop.CustomShortcut
}

// NewMainWindow creates and initializes the complete desktop application window.
func NewMainWindow(fyneApp fyne.App, session *app.Session) *MainWindow {
	// Apply Obsidian Slate theme
	fyneApp.Settings().SetTheme(NewObsidianTheme())

	w := fyneApp.NewWindow("TownCrier " + AppVersion + " — Streamer Neural TTS")

	mw := &MainWindow{
		Window:  w,
		Session: session,
	}

	mw.initStatusBar()
	log.Println("[MainWindow] Initializing views...")
	mw.initViews()
	log.Println("[MainWindow] Initializing header...")
	mw.initHeader()
	mw.syncServiceVisibility()
	log.Println("[MainWindow] Initializing shortcuts...")
	mw.initShortcuts()
	log.Println("[MainWindow] Setting window content...")

	mainContent := container.NewBorder(
		mw.headerBar,
		mw.statusBar,
		nil,
		nil,
		mw.tabsContainer,
	)

	w.SetContent(mainContent)
	w.Resize(fyne.NewSize(950, 720))
	w.CenterOnScreen()

	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			log.Println("[MainWindow] Close requested, shutting down subsystems...")
			if mw.hotkeyMgr != nil {
				mw.hotkeyMgr.Close()
			}
			_ = session.Close()
			if p := session.Player(); p != nil {
				_ = p.Close()
			}
			if e := session.Engine(); e != nil {
				_ = e.Close()
			}
		})
	}

	w.SetCloseIntercept(func() {
		cleanup()
		w.Close()
		fyneApp.Quit()
	})

	w.SetOnClosed(func() {
		cleanup()
	})

	return mw
}

func (mw *MainWindow) initStatusBar() {
	mw.statusLabel = widget.NewLabelWithStyle("Ready.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	mw.statusBar = container.NewVBox(
		widget.NewSeparator(),
		container.NewPadded(mw.statusLabel),
	)
}

// SetStatus updates the persistent bottom status bar across all tabs.
func (mw *MainWindow) SetStatus(msg string) {
	fyne.Do(func() {
		if mw.statusLabel != nil {
			mw.statusLabel.SetText(msg)
		}
	})
}

func (mw *MainWindow) currentHotkeyLabel() string {
	hk := mw.Session.Config().Safety.PanicHotkey
	if hk == "" {
		return "Ctrl+Shift+M"
	}
	return hk
}

func (mw *MainWindow) initViews() {
	cfg := mw.Session.Config()
	player := mw.Session.Player()
	engine := mw.Session.Engine()

	mw.dashboardTab = NewDashboardView(mw.Window, mw.Session)
	mw.dashboardTab.SetStatusHandler(mw.SetStatus)
	mw.twitchTab = NewTwitchTabView(mw.Window, mw.Session)
	mw.twitchTab.SetStatusHandler(mw.SetStatus)
	mw.youtubeTab = NewYouTubeTabView(mw.Window, mw.Session)
	mw.youtubeTab.SetStatusHandler(mw.SetStatus)
	mw.tiktokTab = NewTikTokTabView(mw.Window, mw.Session)
	mw.tiktokTab.SetStatusHandler(mw.SetStatus)
	mw.voicesTab = NewVoicesTabView(mw.Window, mw.Session)
	mw.voicesTab.SetStatusHandler(mw.SetStatus)
	mw.viewersTab = NewViewersTabView(mw.Window, mw.Session)
	mw.viewersTab.SetStatusHandler(mw.SetStatus)
	mw.dashboardTab.SetOnAssignVoice(func(platform, username string) {
		mw.viewersTab.Refresh()
	})
	mw.settingsTab = NewSettingsView(mw.Window, cfg, player, engine)
	mw.settingsTab.SetSession(mw.Session)
	mw.settingsTab.SetStatusHandler(mw.SetStatus)
	mw.settingsTab.SetHotkeyChangedHandler(func(hotkey string) error {
		return mw.ApplyPanicHotkey(hotkey)
	})
	mw.settingsTab.SetServicesChangedHandler(func() {
		mw.syncServiceVisibility()
	})

	mw.dashboardTabItem = container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), mw.dashboardTab.Build())
	mw.twitchTabItem = container.NewTabItemWithIcon("Twitch", theme.MediaPlayIcon(), mw.twitchTab.Build())
	mw.youtubeTabItem = container.NewTabItemWithIcon("YouTube", theme.MediaVideoIcon(), mw.youtubeTab.Build())
	mw.tiktokTabItem = container.NewTabItemWithIcon("TikTok", theme.MediaRecordIcon(), mw.tiktokTab.Build())
	mw.voicesTabItem = container.NewTabItemWithIcon("Voices", theme.VolumeUpIcon(), mw.voicesTab.Build())
	mw.viewersTabItem = container.NewTabItemWithIcon("Viewers", theme.AccountIcon(), mw.viewersTab.Build())
	mw.settingsTabItem = container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), mw.settingsTab.Build())

	mw.tabsContainer = container.NewAppTabs(
		mw.dashboardTabItem,
		mw.twitchTabItem,
		mw.youtubeTabItem,
		mw.tiktokTabItem,
		mw.voicesTabItem,
		mw.viewersTabItem,
		mw.settingsTabItem,
	)
}

func (mw *MainWindow) syncServiceVisibility() {
	cfg := mw.Session.Config()

	twitchCfg, hasTwitch := cfg.Platforms["twitch"]
	twitchVisible := !hasTwitch || twitchCfg.EverEnabled || twitchCfg.Enabled

	ytCfg, hasYT := cfg.Platforms["youtube"]
	ytVisible := !hasYT || ytCfg.EverEnabled || ytCfg.Enabled

	ttCfg, hasTT := cfg.Platforms["tiktok"]
	ttVisible := !hasTT || ttCfg.EverEnabled || ttCfg.Enabled

	if mw.tabsContainer != nil {
		selected := mw.tabsContainer.Selected()
		items := []*container.TabItem{mw.dashboardTabItem}
		if twitchVisible {
			items = append(items, mw.twitchTabItem)
		}
		if ytVisible {
			items = append(items, mw.youtubeTabItem)
		}
		if ttVisible {
			items = append(items, mw.tiktokTabItem)
		}
		items = append(items, mw.voicesTabItem, mw.viewersTabItem, mw.settingsTabItem)
		mw.tabsContainer.SetItems(items)
		if slices.Contains(items, selected) {
			mw.tabsContainer.Select(selected)
		}
	}

	if mw.twitchStatusDot != nil {
		if twitchVisible {
			mw.twitchStatusDot.Show()
		} else {
			mw.twitchStatusDot.Hide()
		}
	}
	if mw.youtubeStatusDot != nil {
		if ytVisible {
			mw.youtubeStatusDot.Show()
		} else {
			mw.youtubeStatusDot.Hide()
		}
	}
	if mw.tiktokStatusDot != nil {
		if ttVisible {
			mw.tiktokStatusDot.Show()
		} else {
			mw.tiktokStatusDot.Hide()
		}
	}
	if mw.headerBar != nil {
		mw.headerBar.Refresh()
	}

	if mw.twitchTab != nil {
		mw.twitchTab.SyncFromConfig()
	}
	if mw.youtubeTab != nil {
		mw.youtubeTab.SyncFromConfig()
	}
	if mw.tiktokTab != nil {
		mw.tiktokTab.SyncFromConfig()
	}
}

func (mw *MainWindow) initHeader() {
	cfg := mw.Session.Config()

	// Platform indicators
	mw.twitchStatusDot = widget.NewLabelWithStyle("Twitch: ⚪", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	pCfg := cfg.Platforms["twitch"]
	if pCfg.Enabled && pCfg.AccountName != "" {
		mw.twitchStatusDot.SetText("Twitch: 🟡 Connecting...")
	} else if pCfg.Enabled {
		mw.twitchStatusDot.SetText("Twitch: 🟡 (No Channel)")
	}

	mw.Session.SetOnTwitchStatus(func(status string, detail string) {
		if mw.twitchTab != nil {
			mw.twitchTab.UpdateStatus(status, detail)
		}
		fyne.Do(func() {
			switch status {
			case "connected":
				mw.twitchStatusDot.SetText("Twitch: 🟢 " + detail)
			case "connecting":
				mw.twitchStatusDot.SetText("Twitch: 🟡 Connecting...")
			default:
				mw.twitchStatusDot.SetText("Twitch: ⚪")
			}
		})
	})

	mw.youtubeStatusDot = widget.NewLabelWithStyle("YT: ⚪", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	ytCfg := cfg.Platforms["youtube"]
	if ytCfg.Enabled && ytCfg.AccountName != "" {
		mw.youtubeStatusDot.SetText("YT: 🟡 Waiting...")
	}

	mw.Session.SetOnYouTubeStatus(func(status string, detail string) {
		if mw.youtubeTab != nil {
			mw.youtubeTab.UpdateStatus(status, detail)
		}
		fyne.Do(func() {
			switch status {
			case "connected":
				mw.youtubeStatusDot.SetText("YT: 🟢 " + detail)
			case "waiting":
				mw.youtubeStatusDot.SetText("YT: 🟡 Waiting...")
			default:
				mw.youtubeStatusDot.SetText("YT: ⚪")
			}
		})
	})

	mw.tiktokStatusDot = widget.NewLabelWithStyle("TikTok: ⚪", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	ttCfg := cfg.Platforms["tiktok"]
	if ttCfg.Enabled && ttCfg.AccountName != "" {
		mw.tiktokStatusDot.SetText("TikTok: 🟡 Waiting...")
	}

	mw.Session.SetOnTikTokStatus(func(status string, detail string) {
		if mw.tiktokTab != nil {
			mw.tiktokTab.UpdateStatus(status, detail)
		}
		fyne.Do(func() {
			switch status {
			case "connected":
				mw.tiktokStatusDot.SetText("TikTok: 🟢 " + detail)
			case "waiting":
				mw.tiktokStatusDot.SetText("TikTok: 🟡 Waiting...")
			default:
				mw.tiktokStatusDot.SetText("TikTok: ⚪")
			}
		})
	})

	// Master volume slider
	vol := cfg.Audio.Volume
	if vol <= 0 {
		vol = 1.0
	}
	volLabel := widget.NewLabel(fmt.Sprintf("Vol: %d%%", int(vol*100)))
	mw.volumeSlider = widget.NewSlider(0, 100)
	mw.volumeSlider.SetValue(float64(vol * 100))
	mw.volumeSlider.OnChanged = func(val float64) {
		gain := float32(val / 100.0)
		volLabel.SetText(fmt.Sprintf("Vol: %d%%", int(val)))
		if p := mw.Session.Player(); p != nil {
			p.SetVolume(gain)
		}
		c := mw.Session.Config()
		c.Audio.Volume = gain
		_ = mw.Session.ReloadConfig(c)
		_ = config.Save("", c)
	}

	volContainer := container.NewBorder(nil, nil, volLabel, nil, mw.volumeSlider)

	// Panic Button
	hk := mw.currentHotkeyLabel()
	mw.panicBtn = widget.NewButtonWithIcon(fmt.Sprintf("PANIC MUTE (Esc / %s)", hk), theme.MediaStopIcon(), func() {
		mw.triggerTogglePanic()
	})
	mw.panicBtn.Importance = widget.DangerImportance

	if q := mw.Session.Queue(); q != nil {
		q.SetOnStateChanged(func(paused, panicked bool) {
			fyne.Do(func() {
				mw.syncPanicUI(paused, panicked)
			})
		})
	}

	brandTitle := widget.NewLabelWithStyle("⚡ TownCrier "+AppVersion, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	aboutBtn := widget.NewButtonWithIcon("About", theme.InfoIcon(), func() {
		ShowAboutDialog(mw.Window)
	})

	leftSide := container.NewHBox(brandTitle, widget.NewSeparator(), mw.twitchStatusDot, mw.youtubeStatusDot, mw.tiktokStatusDot)
	rightSide := container.NewHBox(volContainer, aboutBtn, mw.panicBtn)

	mw.headerBar = container.NewVBox(
		container.NewBorder(nil, nil, leftSide, rightSide),
		widget.NewSeparator(),
	)
}

func (mw *MainWindow) triggerTogglePanic() {
	panicked := mw.Session.TogglePanic()
	q := mw.Session.Queue()
	paused := false
	if q != nil {
		paused = q.IsPaused()
	}
	mw.syncPanicUI(paused, panicked)
	hk := mw.currentHotkeyLabel()
	if panicked {
		mw.SetStatus(fmt.Sprintf("🚨 PANIC MUTE ACTIVE — Speech halted and queue cleared. Click Unmute or press Esc / %s to resume.", hk))
	} else {
		mw.SetStatus("✓ Panic Mute cleared. Speech queue resumed.")
	}
}

func (mw *MainWindow) syncPanicUI(paused, panicked bool) {
	hk := mw.currentHotkeyLabel()
	if mw.panicBtn != nil {
		if panicked {
			mw.panicBtn.SetText(fmt.Sprintf("🚨 PANIC MUTED — Click or %s to Unmute", hk))
			mw.panicBtn.SetIcon(theme.VolumeMuteIcon())
			mw.panicBtn.Importance = widget.WarningImportance
		} else {
			mw.panicBtn.SetText(fmt.Sprintf("PANIC MUTE (Esc / %s)", hk))
			mw.panicBtn.SetIcon(theme.MediaStopIcon())
			mw.panicBtn.Importance = widget.DangerImportance
		}
		mw.panicBtn.Refresh()
	}
	if mw.dashboardTab != nil {
		mw.dashboardTab.SyncQueueState(paused, panicked)
	}
}

func (mw *MainWindow) initShortcuts() {
	// 1. Escape key panic toggle listener (when TownCrier window is focused)
	mw.Window.Canvas().SetOnTypedKey(func(ke *fyne.KeyEvent) {
		if ke.Name == fyne.KeyEscape {
			mw.triggerTogglePanic()
		}
	})

	// 2. System-wide global hotkey + in-window shortcut
	mw.hotkeyMgr = NewGlobalHotkeyManager(func() {
		fyne.Do(func() {
			mw.triggerTogglePanic()
		})
	})

	if err := mw.ApplyPanicHotkey(mw.currentHotkeyLabel()); err != nil {
		log.Printf("[MainWindow] Warning: failed to register global hotkey: %v", err)
	}
}

// ApplyPanicHotkey dynamically updates both the in-window shortcut and OS-wide global hotkey.
func (mw *MainWindow) ApplyPanicHotkey(hotkeyStr string) error {
	spec, err := ParseHotkey(hotkeyStr)
	if err != nil {
		return err
	}

	if mw.activeShortcut != nil {
		mw.Window.Canvas().RemoveShortcut(mw.activeShortcut)
		mw.activeShortcut = nil
	}

	if spec.FyneModifier() != 0 {
		sc := &desktop.CustomShortcut{
			KeyName:  spec.FyneKeyName(),
			Modifier: spec.FyneModifier(),
		}
		mw.Window.Canvas().AddShortcut(sc, func(fyne.Shortcut) {
			mw.triggerTogglePanic()
		})
		mw.activeShortcut = sc
	}

	var regErr error
	if mw.hotkeyMgr != nil {
		regErr = mw.hotkeyMgr.Register(spec.String())
	}

	q := mw.Session.Queue()
	paused := false
	if q != nil {
		paused = q.IsPaused()
	}
	mw.syncPanicUI(paused, mw.Session.IsPanicked())

	return regErr
}


