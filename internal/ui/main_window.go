package ui

import (
	"fmt"
	"log"
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
	voicesTab    *VoicesTabView
	viewersTab   *ViewersTabView
	settingsTab  *SettingsView

	twitchStatusDot *widget.Label
	volumeSlider    *widget.Slider
	panicBtn        *widget.Button
}

// NewMainWindow creates and initializes the complete desktop application window.
func NewMainWindow(fyneApp fyne.App, session *app.Session) *MainWindow {
	// Apply Obsidian Slate theme
	fyneApp.Settings().SetTheme(NewObsidianTheme())

	w := fyneApp.NewWindow("TownCrier — Streamer Neural TTS")

	mw := &MainWindow{
		Window:  w,
		Session: session,
	}

	mw.initStatusBar()
	mw.initViews()
	mw.initHeader()
	mw.initShortcuts()

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

func (mw *MainWindow) initViews() {
	cfg := mw.Session.Config()
	player := mw.Session.Player()
	engine := mw.Session.Engine()

	mw.dashboardTab = NewDashboardView(mw.Window, mw.Session)
	mw.twitchTab = NewTwitchTabView(mw.Window, mw.Session)
	mw.twitchTab.SetStatusHandler(mw.SetStatus)
	mw.voicesTab = NewVoicesTabView(mw.Window, mw.Session)
	mw.viewersTab = NewViewersTabView(mw.Window, mw.Session)
	mw.settingsTab = NewSettingsView(mw.Window, cfg, player, engine)
	mw.settingsTab.SetSession(mw.Session)
	mw.settingsTab.SetStatusHandler(mw.SetStatus)

	mw.tabsContainer = container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), mw.dashboardTab.Build()),
		container.NewTabItemWithIcon("Twitch", theme.MediaPlayIcon(), mw.twitchTab.Build()),
		container.NewTabItemWithIcon("Voices", theme.VolumeUpIcon(), mw.voicesTab.Build()),
		container.NewTabItemWithIcon("Viewers", theme.AccountIcon(), mw.viewersTab.Build()),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), mw.settingsTab.Build()),
	)
}

func (mw *MainWindow) initHeader() {
	cfg := mw.Session.Config()

	// Platform indicator
	mw.twitchStatusDot = widget.NewLabelWithStyle("Twitch: ⚪", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	pCfg := cfg.Platforms["twitch"]
	if pCfg.Enabled && pCfg.AccountName != "" {
		mw.twitchStatusDot.SetText("Twitch: 🟡 Connecting...")
	} else if pCfg.Enabled {
		mw.twitchStatusDot.SetText("Twitch: 🟡 (No Channel)")
	}

	mw.Session.SetOnTwitchStatus(func(status string, detail string) {
		fyne.Do(func() {
			switch status {
			case "connected":
				mw.twitchStatusDot.SetText("Twitch: 🟢 " + detail)
			case "connecting":
				mw.twitchStatusDot.SetText("Twitch: 🟡 Connecting...")
			default:
				mw.twitchStatusDot.SetText("Twitch: ⚪ Disconnected")
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
	mw.panicBtn = widget.NewButtonWithIcon("PANIC MUTE (Esc / Ctrl+Shift+M)", theme.MediaStopIcon(), func() {
		mw.Session.Panic()
	})
	mw.panicBtn.Importance = widget.DangerImportance

	brandTitle := widget.NewLabelWithStyle("⚡ TownCrier", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	aboutBtn := widget.NewButtonWithIcon("About", theme.InfoIcon(), func() {
		ShowAboutDialog(mw.Window)
	})

	leftSide := container.NewHBox(brandTitle, widget.NewSeparator(), mw.twitchStatusDot)
	rightSide := container.NewHBox(volContainer, aboutBtn, mw.panicBtn)

	mw.headerBar = container.NewVBox(
		container.NewBorder(nil, nil, leftSide, rightSide),
		widget.NewSeparator(),
	)
}

func (mw *MainWindow) initShortcuts() {
	// 1. Escape key panic listener
	mw.Window.Canvas().SetOnTypedKey(func(ke *fyne.KeyEvent) {
		if ke.Name == fyne.KeyEscape {
			mw.Session.Panic()
		}
	})

	// 2. Ctrl+Shift+M shortcut
	shortcut := &desktop.CustomShortcut{
		KeyName:  fyne.KeyM,
		Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift,
	}
	mw.Window.Canvas().AddShortcut(shortcut, func(sc fyne.Shortcut) {
		mw.Session.Panic()
	})
}
