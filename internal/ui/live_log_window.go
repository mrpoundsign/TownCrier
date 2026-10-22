package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// liveLogWindow represents an active zero-chrome pop-out Live Log window for a saved preset.
type liveLogWindow struct {
	preset config.LiveLogPreset
	win    fyne.Window
	view   *LogsView
}

// Close closes the pop-out window and unsubscribes its live tail listener.
func (lw *liveLogWindow) Close() {
	if lw == nil {
		return
	}
	if lw.view != nil {
		lw.view.Close()
	}
	if lw.win != nil {
		lw.win.Close()
	}
}

func (v *LogsView) effectiveLiveLogPresets() []config.LiveLogPreset {
	if v.session != nil && v.session.Config() != nil {
		presets := v.session.Config().Logging.LiveLogs
		if len(presets) > 0 {
			out := make([]config.LiveLogPreset, len(presets))
			copy(out, presets)
			return out
		}
	}
	return config.DefaultLiveLogPresets()
}

func formatLiveLogPresetSummary(p config.LiveLogPreset) string {
	var parts []string
	if strings.EqualFold(strings.TrimSpace(p.SessionMode), "all") {
		parts = append(parts, "Session: All Sessions")
	} else {
		parts = append(parts, "Session: Latest Session (Auto-Switching)")
	}

	plat := strings.TrimSpace(p.Platform)
	if plat == "" {
		plat = platformOptionAll
	}
	parts = append(parts, "Platform: "+plat)

	evType := strings.TrimSpace(p.EventType)
	if evType == "" {
		evType = eventTypeAll
	}
	if evType != eventTypeAll {
		parts = append(parts, "Type: "+evType)
	}

	outc := strings.TrimSpace(p.Outcome)
	if outc == "" {
		outc = outcomeLabelAll
	}
	if outc != outcomeLabelAll {
		parts = append(parts, "Status: "+outc)
	}

	if uq := strings.TrimSpace(p.UserQuery); uq != "" {
		parts = append(parts, "User: \""+uq+"\"")
	}
	if tq := strings.TrimSpace(p.TextQuery); tq != "" {
		parts = append(parts, "Message: \""+tq+"\"")
	}

	if p.HideBots {
		parts = append(parts, "Hide Bots: On")
	} else {
		parts = append(parts, "Hide Bots: Off")
	}

	if p.ShowSummary {
		parts = append(parts, "Summary: On")
	} else {
		parts = append(parts, "Summary: Off")
	}

	return strings.Join(parts, "  •  ")
}

func (v *LogsView) buildLiveLogsTab() fyne.CanvasObject {
	headerTitle := widget.NewLabelWithStyle("Saved Live Log Views", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	headerSub := widget.NewLabel("Click Open Window on any card to launch a dedicated zero-chrome live log window. Use + Add Live Log to create custom filtered views.")
	headerSub.Wrapping = fyne.TextWrapWord

	addBtn := widget.NewButtonWithIcon("+ Add Live Log", theme.ContentAddIcon(), func() {
		v.showLiveLogPresetDialog(nil)
	})
	addBtn.Importance = widget.HighImportance
	v.addLiveLogBtn = addBtn
	if v.session != nil && !v.session.Config().Logging.Enabled {
		addBtn.Disable()
	}

	headerRow := container.NewBorder(nil, nil, nil, addBtn, container.NewVBox(headerTitle, headerSub))

	v.liveLogsCardsBox = container.NewGridWithColumns(2)
	v.refreshLiveLogCards()

	content := container.NewVBox(
		headerRow,
		widget.NewSeparator(),
		v.liveLogsCardsBox,
	)
	return newPanelVScroll(container.NewPadded(content))
}

func (v *LogsView) refreshLiveLogCards() {
	if v.liveLogsCardsBox == nil {
		return
	}
	presets := v.effectiveLiveLogPresets()
	v.liveLogsCardsBox.Objects = nil

	for _, preset := range presets {
		p := preset
		openBtn := widget.NewButtonWithIcon("⧉ Open Window", theme.VisibilityIcon(), func() {
			v.OpenLiveLogWindow(p)
		})
		openBtn.Importance = widget.HighImportance
		if v.session != nil && !v.session.Config().Logging.Enabled {
			openBtn.Disable()
		}

		editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
			cp := p
			v.showLiveLogPresetDialog(&cp)
		})

		delBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
			v.deleteLiveLogPreset(p.ID)
		})

		actions := container.NewBorder(nil, nil, nil, container.NewHBox(editBtn, delBtn), openBtn)
		card := widget.NewCard(p.Name, formatLiveLogPresetSummary(p), actions)
		v.liveLogsCardsBox.Add(card)
	}
	v.liveLogsCardsBox.Refresh()
}

func (v *LogsView) saveLiveLogPreset(p config.LiveLogPreset) {
	if strings.TrimSpace(p.ID) == "" {
		p.ID = fmt.Sprintf("live-log-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "Live Log"
	}

	if v.session != nil {
		_ = v.session.UpdateConfig(func(c *config.Config) {
			if len(c.Logging.LiveLogs) == 0 {
				c.Logging.LiveLogs = config.DefaultLiveLogPresets()
			}
			replaced := false
			for i := range c.Logging.LiveLogs {
				if c.Logging.LiveLogs[i].ID == p.ID {
					c.Logging.LiveLogs[i] = p
					replaced = true
					break
				}
			}
			if !replaced {
				c.Logging.LiveLogs = append(c.Logging.LiveLogs, p)
			}
		})
	}

	v.mu.Lock()
	openWin := v.openLiveWindows[p.ID]
	v.mu.Unlock()
	if openWin != nil {
		openWin.preset = p
		if openWin.win != nil {
			openWin.win.SetTitle("TownCrier — " + p.Name)
		}
		if openWin.view != nil {
			openWin.view.applyLivePreset(p)
		}
	}

	v.refreshLiveLogCards()
	v.setStatus(fmt.Sprintf("✓ Saved Live Log '%s'.", p.Name))
}

func (v *LogsView) deleteLiveLogPreset(id string) {
	v.mu.Lock()
	openWin := v.openLiveWindows[id]
	delete(v.openLiveWindows, id)
	v.mu.Unlock()
	if openWin != nil {
		openWin.Close()
	}

	if v.session != nil {
		_ = v.session.UpdateConfig(func(c *config.Config) {
			filtered := make([]config.LiveLogPreset, 0, len(c.Logging.LiveLogs))
			for _, item := range c.Logging.LiveLogs {
				if item.ID != id {
					filtered = append(filtered, item)
				}
			}
			c.Logging.LiveLogs = filtered
		})
	}

	v.refreshLiveLogCards()
	v.setStatus("✓ Removed Live Log card.")
}

// OpenLiveLogWindow opens or focuses a zero-chrome pop-out window for the given LiveLogPreset.
func (v *LogsView) OpenLiveLogWindow(preset config.LiveLogPreset) fyne.Window {
	v.mu.Lock()
	if v.openLiveWindows == nil {
		v.openLiveWindows = make(map[string]*liveLogWindow)
	}
	if existing, ok := v.openLiveWindows[preset.ID]; ok && existing != nil && existing.win != nil {
		v.mu.Unlock()
		existing.win.RequestFocus()
		existing.win.Show()
		return existing.win
	}
	v.mu.Unlock()

	appInstance := fyne.CurrentApp()
	if appInstance == nil {
		return nil
	}

	win := appInstance.NewWindow("TownCrier — " + preset.Name)
	win.Resize(fyne.NewSize(860, 540))

	popView := NewLogsView(win, v.session)
	popView.SetEmoteCache(v.emoteCache)
	popView.SetStatusHandler(v.onStatus)
	popView.SetOnAssignVoice(v.onAssignVoice)

	content := popView.buildZeroChrome(preset)
	win.SetContent(content)

	lw := &liveLogWindow{
		preset: preset,
		win:    win,
		view:   popView,
	}

	v.mu.Lock()
	v.openLiveWindows[preset.ID] = lw
	v.mu.Unlock()

	win.SetOnClosed(func() {
		popView.Close()
		v.mu.Lock()
		if v.openLiveWindows != nil && v.openLiveWindows[preset.ID] == lw {
			delete(v.openLiveWindows, preset.ID)
		}
		v.mu.Unlock()
	})

	win.Show()
	return win
}

// buildZeroChrome constructs a zero-configuration-chrome Live Log view for pop-out windows.
// Only the optional Alerts Summary banner and the live log table are rendered.
func (v *LogsView) buildZeroChrome(preset config.LiveLogPreset) fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	v.tooltipBubble, v.tooltipText = buildTooltipBubble()
	v.tooltipLayer = container.NewWithoutLayout(v.tooltipBubble)

	v.summaryTotalsLabel = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	totalsBg := canvas.NewRectangle(colorSlateSurface)
	totalsBorder := canvas.NewRectangle(colorSkyBlueDim)
	totalsBorder.SetMinSize(fyne.NewSize(1, 1))
	v.summaryTotalsContainer = container.NewStack(
		totalsBg,
		container.NewBorder(nil, totalsBorder, nil, nil, container.NewPadded(v.summaryTotalsLabel)),
	)
	v.summaryTotalsContainer.Hide()

	// Initialize hidden filter controls so currentFilter() and applyLivePreset() work identically.
	_ = v.buildFilterDrawer()
	v.refreshSessionDropdown()

	tableHeader := v.buildTableHeader()
	v.listBox = container.NewVBox()
	listScroll := newPanelVScroll(v.listBox)

	v.applyLivePreset(preset)
	v.subscribeLiveTail()

	topSection := container.NewVBox(
		v.summaryTotalsContainer,
		tableHeader,
	)

	mainLayout := container.NewBorder(
		topSection,
		nil,
		nil,
		nil,
		listScroll,
	)

	return container.NewStack(mainLayout, v.tooltipLayer)
}

func (v *LogsView) applyLivePreset(preset config.LiveLogPreset) {
	v.initializing = true

	sid := sessionIDLatest
	if strings.EqualFold(strings.TrimSpace(preset.SessionMode), "all") {
		sid = sessionIDAll
	}

	platOpt := strings.TrimSpace(preset.Platform)
	if platOpt == "" {
		platOpt = platformOptionAll
	}
	evTypeOpt := strings.TrimSpace(preset.EventType)
	if evTypeOpt == "" {
		evTypeOpt = eventTypeAll
	}
	outcomeOpt := strings.TrimSpace(preset.Outcome)
	if outcomeOpt == "" {
		outcomeOpt = outcomeLabelAll
	}

	if v.platformSel != nil {
		v.platformSel.SetSelected(platOpt)
	}
	if v.eventTypeSel != nil {
		v.eventTypeSel.SetSelected(evTypeOpt)
	}
	if v.outcomeSel != nil {
		v.outcomeSel.SetSelected(outcomeOpt)
	}
	if v.userSearchEntry != nil {
		v.userSearchEntry.SetText(preset.UserQuery)
	}
	if v.textSearchEntry != nil {
		v.textSearchEntry.SetText(preset.TextQuery)
	}

	v.mu.Lock()
	v.selectedSessionID = sid
	v.selectedSessionIDs = nil
	v.selectedPlatform = normalizePlatformOption(platOpt)
	v.hideBots = preset.HideBots
	v.showSummary = preset.ShowSummary
	v.liveTail = true
	v.currentPage = 0
	v.mu.Unlock()

	v.initializing = false
	v.RefreshQuery()
}

func (v *LogsView) showLiveLogPresetDialog(existing *config.LiveLogPreset) {
	title := "Add Live Log"
	preset := config.LiveLogPreset{
		Name:        "New Live Log",
		SessionMode: "latest",
		HideBots:    true,
		ShowSummary: true,
		Platform:    platformOptionAll,
		EventType:   eventTypeAll,
		Outcome:     outcomeLabelAll,
	}
	if existing != nil {
		title = "Edit Live Log — " + existing.Name
		preset = *existing
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Live Log Name (e.g. Latest Session, Twitch Alerts)...")
	nameEntry.SetText(preset.Name)

	hideBotsCheck := widget.NewCheck("🤖 Hide Bots", nil)
	hideBotsCheck.SetChecked(preset.HideBots)

	showSummaryCheck := widget.NewCheck("📊 Show Alerts Summary", nil)
	showSummaryCheck.SetChecked(preset.ShowSummary)

	sessionOpts := []string{sessionOptionLatest, sessionOptionAll}
	sessionSel := widget.NewSelect(sessionOpts, nil)
	if strings.EqualFold(strings.TrimSpace(preset.SessionMode), "all") {
		sessionSel.SetSelected(sessionOptionAll)
	} else {
		sessionSel.SetSelected(sessionOptionLatest)
	}

	platSel := widget.NewSelect([]string{platformOptionAll, "Twitch", "YouTube", "TikTok", "Kick"}, nil)
	if preset.Platform != "" {
		platSel.SetSelected(preset.Platform)
	} else {
		platSel.SetSelected(platformOptionAll)
	}

	typeSel := widget.NewSelect([]string{
		eventTypeAll,
		eventTypeChatOnly,
		eventTypeAlertsOnly,
		eventTypeFollows,
		eventTypeSubs,
		eventTypeCheers,
		eventTypeRaids,
		eventTypeRedemptions,
		eventTypeModeration,
	}, nil)
	if preset.EventType != "" {
		typeSel.SetSelected(preset.EventType)
	} else {
		typeSel.SetSelected(eventTypeAll)
	}

	statusSel := widget.NewSelect([]string{
		outcomeLabelAll,
		outcomeLabelSpoken,
		outcomeLabelPassed,
		outcomeLabelDroppedQ,
		outcomeLabelFiltered,
		outcomeLabelMod,
	}, nil)
	if preset.Outcome != "" {
		statusSel.SetSelected(preset.Outcome)
	} else {
		statusSel.SetSelected(outcomeLabelAll)
	}

	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("Username, display name, or unique user ID...")
	userEntry.SetText(preset.UserQuery)

	msgEntry := widget.NewEntry()
	msgEntry.SetPlaceHolder("Search message or spoken text...")
	msgEntry.SetText(preset.TextQuery)

	nameRow := container.NewBorder(nil, nil, newFilterFieldLabel("Name:"), nil, nameEntry)
	togglesRow := container.NewHBox(hideBotsCheck, showSummaryCheck)

	midRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, newFilterFieldLabel("Session:"), nil, newBorderedControl(sessionSel)),
		container.NewBorder(nil, nil, newFilterFieldLabel("Platform:"), nil, newBorderedControl(platSel)),
		container.NewBorder(nil, nil, newFilterFieldLabel("Type:"), nil, newBorderedControl(typeSel)),
		container.NewBorder(nil, nil, newFilterFieldLabel("Status:"), nil, newBorderedControl(statusSel)),
	)

	searchRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, newFilterFieldLabel("User / ID:"), nil, userEntry),
		container.NewBorder(nil, nil, newFilterFieldLabel("Message:"), nil, msgEntry),
	)

	formBox := container.NewVBox(
		nameRow,
		togglesRow,
		widget.NewSeparator(),
		midRow,
		searchRow,
	)
	formWrap := container.NewPadded(formBox)

	d := dialog.NewCustomConfirm(title, "Save", "Cancel", formWrap, func(ok bool) {
		if !ok {
			return
		}
		preset.Name = strings.TrimSpace(nameEntry.Text)
		if preset.Name == "" {
			preset.Name = "Live Log"
		}
		if sessionSel.Selected == sessionOptionAll {
			preset.SessionMode = "all"
		} else {
			preset.SessionMode = "latest"
		}
		preset.HideBots = hideBotsCheck.Checked
		preset.ShowSummary = showSummaryCheck.Checked
		preset.Platform = platSel.Selected
		preset.EventType = typeSel.Selected
		preset.Outcome = statusSel.Selected
		preset.UserQuery = strings.TrimSpace(userEntry.Text)
		preset.TextQuery = strings.TrimSpace(msgEntry.Text)

		v.saveLiveLogPreset(preset)
	}, v.window)
	d.Resize(fyne.NewSize(680, 320))
	d.Show()
}
