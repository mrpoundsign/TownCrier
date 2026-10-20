package ui

import (
	"fmt"
	"image/color"
	"log"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// Tab index constants for the top-level main window views.
const (
	TabIndexDashboard = 0
	TabIndexViewers   = 1
	TabIndexLogs      = 2
	TabIndexSettings  = 3
)

// MainWindow coordinates the primary Fyne desktop window, tabs, header bar, and global panic hotkeys.
type MainWindow struct {
	Window  fyne.Window
	Session *app.Session

	headerBar        *fyne.Container
	contentContainer *fyne.Container
	statusBar        *fyne.Container
	statusLabel      *widget.Label

	currentTab   int
	dashboardBtn *widget.Button
	viewersBtn   *widget.Button
	logsBtn      *widget.Button
	settingsBtn  *widget.Button

	dashboardObj fyne.CanvasObject
	viewersObj   fyne.CanvasObject
	chatLogsObj  fyne.CanvasObject
	settingsObj  fyne.CanvasObject

	dashboardTab *DashboardView
	twitchTab    *TwitchTabView
	youtubeTab   *YouTubeTabView
	tiktokTab    *TikTokTabView
	kickTab      *KickTabView
	viewersTab   *ViewersTabView
	chatLogsTab  *LogsView
	settingsTab  *SettingsView

	twitchStatusDot  *platformConnectionBadge
	youtubeStatusDot *platformConnectionBadge
	tiktokStatusDot  *platformConnectionBadge
	kickStatusDot    *platformConnectionBadge
	statusMu         sync.RWMutex
	platformStatuses map[string]platformStatusState

	tooltipLayer   *fyne.Container
	tooltipBubble  *fyne.Container
	tooltipText    *canvas.Text
	volumeSlider   *widget.Slider
	panicBtn       *widget.Button
	hotkeyMgr      *GlobalHotkeyManager
	activeShortcut *desktop.CustomShortcut
}

// NewMainWindow creates and initializes the complete desktop application window.
func NewMainWindow(fyneApp fyne.App, session *app.Session) *MainWindow {
	// Apply Obsidian Slate theme
	fyneApp.Settings().SetTheme(NewObsidianTheme())

	w := fyneApp.NewWindow("TownCrier " + AppVersion + " — Streamer Neural TTS")

	mw := &MainWindow{
		Window:           w,
		Session:          session,
		platformStatuses: make(map[string]platformStatusState),
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
		mw.contentContainer,
	)

	mw.tooltipText = canvas.NewText("", color.NRGBA{R: 248, G: 250, B: 252, A: 255})
	mw.tooltipText.TextSize = 11
	mw.tooltipBubble = container.NewStack(
		canvas.NewRectangle(color.NRGBA{R: 15, G: 23, B: 42, A: 235}),
		container.NewPadded(mw.tooltipText),
	)
	mw.tooltipBubble.Hide()
	mw.tooltipLayer = container.NewWithoutLayout(mw.tooltipBubble)

	rootContent := container.NewStack(mainContent, mw.tooltipLayer)

	w.SetContent(rootContent)
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
	mw.statusLabel.Truncation = fyne.TextTruncateEllipsis

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

	statusRow := container.NewBorder(nil, nil, nil, mw.panicBtn, mw.statusLabel)
	mw.statusBar = container.NewVBox(
		widget.NewSeparator(),
		container.NewPadded(statusRow),
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

func (mw *MainWindow) showTooltipForObject(obj fyne.CanvasObject, tip string, hovered bool) {
	if !hovered || tip == "" {
		if mw.tooltipBubble != nil {
			mw.tooltipBubble.Hide()
		}
		return
	}

	if mw.statusLabel != nil {
		mw.statusLabel.SetText(tip)
	}

	if mw.tooltipBubble == nil || mw.tooltipText == nil || mw.tooltipLayer == nil {
		return
	}

	mw.tooltipText.Text = tip
	mw.tooltipText.Refresh()
	bubbleSize := mw.tooltipBubble.MinSize()
	mw.tooltipBubble.Resize(bubbleSize)

	appInstance := fyne.CurrentApp()
	if appInstance == nil || appInstance.Driver() == nil {
		return
	}
	objPos := appInstance.Driver().AbsolutePositionForObject(obj)
	layerPos := appInstance.Driver().AbsolutePositionForObject(mw.tooltipLayer)
	rel := objPos.Subtract(layerPos)

	x := rel.X + (obj.Size().Width-bubbleSize.Width)/2
	layerSize := mw.tooltipLayer.Size()
	if layerSize.Width > 0 && x+bubbleSize.Width > layerSize.Width-8 {
		x = layerSize.Width - bubbleSize.Width - 8
	}
	if x < 8 {
		x = 8
	}
	y := rel.Y + obj.Size().Height + 4
	if layerSize.Height > 0 && y+bubbleSize.Height > layerSize.Height-8 {
		y = rel.Y - bubbleSize.Height - 4
	}

	mw.tooltipBubble.Move(fyne.NewPos(x, y))
	mw.tooltipBubble.Show()
	mw.tooltipBubble.Refresh()
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
	mw.twitchTab.SetStatusHandler(func(msg string) {
		mw.SetStatus(msg)
		mw.updatePlatformBadge("twitch", "", "")
	})
	mw.youtubeTab = NewYouTubeTabView(mw.Window, mw.Session)
	mw.youtubeTab.SetStatusHandler(func(msg string) {
		mw.SetStatus(msg)
		mw.updatePlatformBadge("youtube", "", "")
	})
	mw.tiktokTab = NewTikTokTabView(mw.Window, mw.Session)
	mw.tiktokTab.SetStatusHandler(func(msg string) {
		mw.SetStatus(msg)
		mw.updatePlatformBadge("tiktok", "", "")
	})
	mw.kickTab = NewKickTabView(mw.Window, mw.Session)
	mw.kickTab.SetStatusHandler(func(msg string) {
		mw.SetStatus(msg)
		mw.updatePlatformBadge("kick", "", "")
	})
	mw.viewersTab = NewViewersTabView(mw.Window, mw.Session)
	mw.viewersTab.SetStatusHandler(mw.SetStatus)
	mw.chatLogsTab = NewLogsView(mw.Window, mw.Session)
	mw.chatLogsTab.SetStatusHandler(mw.SetStatus)
	mw.chatLogsTab.SetOnAssignVoice(func(platform, username string) {
		mw.viewersTab.Refresh()
	})
	mw.dashboardTab.SetOnAssignVoice(func(platform, username string) {
		mw.viewersTab.Refresh()
	})
	mw.settingsTab = NewSettingsView(mw.Window, cfg, player, engine)
	mw.settingsTab.SetSession(mw.Session)
	mw.settingsTab.SetStatusHandler(mw.SetStatus)
	mw.settingsTab.SetOnAssignVoice(func(platform, username string) {
		mw.viewersTab.Refresh()
	})
	mw.settingsTab.SetHotkeyChangedHandler(func(hotkey string) error {
		return mw.ApplyPanicHotkey(hotkey)
	})
	mw.settingsTab.SetServicesChangedHandler(func() {
		mw.syncServiceVisibility()
	})
	mw.settingsTab.SetModelsChangedHandler(func() {
		if mw.settingsTab != nil {
			mw.settingsTab.RefreshVoices()
		}
		if mw.twitchTab != nil {
			mw.twitchTab.RefreshVoices()
		}
		if mw.youtubeTab != nil {
			mw.youtubeTab.RefreshVoices()
		}
		if mw.tiktokTab != nil {
			mw.tiktokTab.RefreshVoices()
		}
		if mw.kickTab != nil {
			mw.kickTab.RefreshVoices()
		}
		if mw.viewersTab != nil {
			mw.viewersTab.Refresh()
		}
	})

	mw.settingsTab.SetPlatformTabs(mw.twitchTab, mw.youtubeTab, mw.tiktokTab, mw.kickTab)

	mw.dashboardObj = mw.dashboardTab.Build()
	mw.viewersObj = newWidthClampContainer(mw.viewersTab.Build())
	mw.chatLogsObj = newWidthClampContainer(mw.chatLogsTab.Build())
	mw.settingsObj = mw.settingsTab.Build()

	mw.contentContainer = container.NewStack(
		mw.dashboardObj,
		mw.viewersObj,
		mw.chatLogsObj,
		mw.settingsObj,
	)

	mw.dashboardBtn = widget.NewButtonWithIcon("Dashboard", theme.HomeIcon(), func() { mw.SelectTab(TabIndexDashboard) })
	mw.viewersBtn = widget.NewButtonWithIcon("Viewers", theme.AccountIcon(), func() { mw.SelectTab(TabIndexViewers) })
	mw.logsBtn = widget.NewButtonWithIcon("Logs", theme.DocumentIcon(), func() { mw.SelectTab(TabIndexLogs) })
	mw.settingsBtn = widget.NewButtonWithIcon("", theme.SettingsIcon(), func() { mw.SelectTab(TabIndexSettings) })

	mw.SelectTab(TabIndexDashboard)
}

// SelectTab activates the specified main view (Dashboard, Viewers, Logs, or Settings).
func (mw *MainWindow) SelectTab(index int) {
	mw.currentTab = index

	if mw.dashboardObj != nil {
		mw.dashboardObj.Hide()
	}
	if mw.viewersObj != nil {
		mw.viewersObj.Hide()
	}
	if mw.chatLogsObj != nil {
		mw.chatLogsObj.Hide()
	}
	if mw.settingsObj != nil {
		mw.settingsObj.Hide()
	}

	if mw.dashboardBtn != nil {
		mw.dashboardBtn.Importance = widget.MediumImportance
	}
	if mw.viewersBtn != nil {
		mw.viewersBtn.Importance = widget.MediumImportance
	}
	if mw.logsBtn != nil {
		mw.logsBtn.Importance = widget.MediumImportance
	}
	if mw.settingsBtn != nil {
		mw.settingsBtn.Importance = widget.MediumImportance
	}

	switch index {
	case TabIndexDashboard:
		if mw.dashboardObj != nil {
			mw.dashboardObj.Show()
		}
		if mw.dashboardBtn != nil {
			mw.dashboardBtn.Importance = widget.HighImportance
		}
	case TabIndexViewers:
		if mw.viewersObj != nil {
			mw.viewersObj.Show()
		}
		if mw.viewersBtn != nil {
			mw.viewersBtn.Importance = widget.HighImportance
		}
		if mw.viewersTab != nil {
			mw.viewersTab.Refresh()
		}
	case TabIndexLogs:
		if mw.chatLogsObj != nil {
			mw.chatLogsObj.Show()
		}
		if mw.logsBtn != nil {
			mw.logsBtn.Importance = widget.HighImportance
		}
		if mw.chatLogsTab != nil {
			mw.chatLogsTab.RefreshQuery()
		}
	case TabIndexSettings:
		if mw.settingsObj != nil {
			mw.settingsObj.Show()
		}
		if mw.settingsBtn != nil {
			mw.settingsBtn.Importance = widget.HighImportance
		}
		if mw.settingsTab != nil {
			mw.settingsTab.SyncFromConfig()
		}
	}

	if mw.dashboardBtn != nil {
		mw.dashboardBtn.Refresh()
	}
	if mw.viewersBtn != nil {
		mw.viewersBtn.Refresh()
	}
	if mw.logsBtn != nil {
		mw.logsBtn.Refresh()
	}
	if mw.settingsBtn != nil {
		mw.settingsBtn.Refresh()
	}
	if mw.contentContainer != nil {
		mw.contentContainer.Refresh()
	}
}

func (mw *MainWindow) syncServiceVisibility() {
	cfg := mw.Session.Config()

	twitchCfg, hasTwitch := cfg.Platforms["twitch"]
	twitchVisible := !hasTwitch || twitchCfg.EverEnabled || twitchCfg.Enabled

	ytCfg, hasYT := cfg.Platforms["youtube"]
	ytVisible := !hasYT || ytCfg.EverEnabled || ytCfg.Enabled

	ttCfg, hasTT := cfg.Platforms["tiktok"]
	ttVisible := !hasTT || ttCfg.EverEnabled || ttCfg.Enabled

	kickCfg, hasKick := cfg.Platforms["kick"]
	kickVisible := !hasKick || kickCfg.EverEnabled || kickCfg.Enabled

	if mw.settingsTab != nil {
		mw.settingsTab.SyncPlatformTabVisibility()
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
	if mw.kickStatusDot != nil {
		if kickVisible {
			mw.kickStatusDot.Show()
		} else {
			mw.kickStatusDot.Hide()
		}
	}
	mw.updatePlatformBadge("twitch", "", "")
	mw.updatePlatformBadge("youtube", "", "")
	mw.updatePlatformBadge("tiktok", "", "")
	mw.updatePlatformBadge("kick", "", "")
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
	if mw.kickTab != nil {
		mw.kickTab.SyncFromConfig()
	}
}

func (mw *MainWindow) initHeader() {
	cfg := mw.Session.Config()

	// Platform indicators
	twitchDot, twitchTip := mw.resolvePlatformStatus("twitch", "", "")
	mw.twitchStatusDot = newPlatformConnectionBadge(
		"Twitch",
		resPlatformTwitch,
		twitchDot,
		twitchTip,
		mw.showTooltipForObject,
		func() {
			mw.SelectTab(TabIndexSettings)
			if mw.settingsTab != nil {
				mw.settingsTab.SelectPlatformTab("twitch")
			}
		},
	)

	mw.Session.SetOnTwitchStatus(func(status string, detail string) {
		if mw.twitchTab != nil {
			mw.twitchTab.UpdateStatus(status, detail)
		}
		mw.updatePlatformBadge("twitch", status, detail)
	})

	ytDot, ytTip := mw.resolvePlatformStatus("youtube", "", "")
	mw.youtubeStatusDot = newPlatformConnectionBadge(
		"YouTube",
		resPlatformYouTube,
		ytDot,
		ytTip,
		mw.showTooltipForObject,
		func() {
			mw.SelectTab(TabIndexSettings)
			if mw.settingsTab != nil {
				mw.settingsTab.SelectPlatformTab("youtube")
			}
		},
	)

	mw.Session.SetOnYouTubeStatus(func(status string, detail string) {
		if mw.youtubeTab != nil {
			mw.youtubeTab.UpdateStatus(status, detail)
		}
		mw.updatePlatformBadge("youtube", status, detail)
	})

	ttDot, ttTip := mw.resolvePlatformStatus("tiktok", "", "")
	mw.tiktokStatusDot = newPlatformConnectionBadge(
		"TikTok",
		resPlatformTikTok,
		ttDot,
		ttTip,
		mw.showTooltipForObject,
		func() {
			mw.SelectTab(TabIndexSettings)
			if mw.settingsTab != nil {
				mw.settingsTab.SelectPlatformTab("tiktok")
			}
		},
	)

	mw.Session.SetOnTikTokStatus(func(status string, detail string) {
		if mw.tiktokTab != nil {
			mw.tiktokTab.UpdateStatus(status, detail)
		}
		mw.updatePlatformBadge("tiktok", status, detail)
	})

	kickDot, kickTip := mw.resolvePlatformStatus("kick", "", "")
	mw.kickStatusDot = newPlatformConnectionBadge(
		"Kick",
		resPlatformKick,
		kickDot,
		kickTip,
		mw.showTooltipForObject,
		func() {
			mw.SelectTab(TabIndexSettings)
			if mw.settingsTab != nil {
				mw.settingsTab.SelectPlatformTab("kick")
			}
		},
	)

	mw.Session.SetOnKickStatus(func(status string, detail string) {
		if mw.kickTab != nil {
			mw.kickTab.UpdateStatus(status, detail)
		}
		mw.updatePlatformBadge("kick", status, detail)
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

	volSliderSpacer := canvas.NewRectangle(color.Transparent)
	volSliderSpacer.SetMinSize(fyne.NewSize(90, 1))
	volContainer := container.NewBorder(nil, nil, volLabel, nil, container.NewStack(volSliderSpacer, mw.volumeSlider))

	aboutBtn := widget.NewButtonWithIcon("About", theme.InfoIcon(), func() {
		ShowAboutDialog(mw.Window)
	})

	navButtons := container.NewHBox(
		mw.dashboardBtn,
		mw.viewersBtn,
		mw.logsBtn,
		mw.settingsBtn,
	)
	leftSide := container.NewHBox(
		mw.twitchStatusDot,
		mw.youtubeStatusDot,
		mw.tiktokStatusDot,
		mw.kickStatusDot,
		widget.NewSeparator(),
		navButtons,
	)
	rightSide := container.NewHBox(volContainer, aboutBtn)

	mw.headerBar = container.NewVBox(
		container.NewHBox(leftSide, layout.NewSpacer(), rightSide),
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

type platformStatusState struct {
	status string
	detail string
}

func (mw *MainWindow) resolvePlatformStatus(platform, rawStatus, detail string) (string, string) {
	cfg := mw.Session.Config()
	pCfg := cfg.Platforms[platform]

	var name string
	switch platform {
	case "twitch":
		name = "Twitch"
	case "youtube":
		name = "YouTube"
	case "tiktok":
		name = "TikTok"
	case "kick":
		name = "Kick"
	default:
		name = platform
	}

	if !pCfg.Enabled {
		return "⚪", name + ": Inactive"
	}

	if pCfg.AccountName == "" {
		return "🟡", name + ": Enabled (No Channel Configured)"
	}

	switch rawStatus {
	case "connected", "live":
		tip := name + ": Live"
		if detail != "" {
			tip += " (" + detail + ")"
		}
		return "🟢", tip
	case "connecting":
		tip := name + ": Connecting..."
		if detail != "" {
			tip += " (" + detail + ")"
		}
		return "🟡", tip
	case "waiting":
		tip := name + ": Waiting for stream..."
		if detail != "" {
			tip += " (" + detail + ")"
		}
		return "🟡", tip
	case "disconnected":
		tip := name + ": Disconnected"
		if detail != "" {
			tip += " (" + detail + ")"
		}
		return "🔴", tip
	default:
		switch platform {
		case "twitch":
			return "🟡", fmt.Sprintf("Twitch: Waiting for stream... (#%s)", pCfg.AccountName)
		case "kick":
			return "🟡", fmt.Sprintf("Kick: Waiting for stream... (%s)", pCfg.AccountName)
		default:
			return "🟡", fmt.Sprintf("%s: Waiting for stream... (%s)", name, pCfg.AccountName)
		}
	}
}

func (mw *MainWindow) updatePlatformBadge(platform, status, detail string) {
	mw.statusMu.Lock()
	if mw.platformStatuses == nil {
		mw.platformStatuses = make(map[string]platformStatusState)
	}
	if status != "" || detail != "" {
		mw.platformStatuses[platform] = platformStatusState{status: status, detail: detail}
	}
	current := mw.platformStatuses[platform]
	mw.statusMu.Unlock()

	dot, tip := mw.resolvePlatformStatus(platform, current.status, current.detail)

	fyne.Do(func() {
		switch platform {
		case "twitch":
			if mw.twitchStatusDot != nil {
				mw.twitchStatusDot.SetStatus(dot, tip)
			}
		case "youtube":
			if mw.youtubeStatusDot != nil {
				mw.youtubeStatusDot.SetStatus(dot, tip)
			}
		case "tiktok":
			if mw.tiktokStatusDot != nil {
				mw.tiktokStatusDot.SetStatus(dot, tip)
			}
		case "kick":
			if mw.kickStatusDot != nil {
				mw.kickStatusDot.SetStatus(dot, tip)
			}
		}
	})
}



