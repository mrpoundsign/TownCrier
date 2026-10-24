package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
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

	if p.ChatScale > 0 {
		parts = append(parts, fmt.Sprintf("Scale: %d%%", NormalizeChatScale(p.ChatScale)))
	} else {
		parts = append(parts, "Scale: Default")
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
	uiRenderMu.Lock()
	defer uiRenderMu.Unlock()
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
	uiRenderMu.Lock()
	win.SetContent(content)
	uiRenderMu.Unlock()

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

	v.initSummaryTotalsBanner()

	v.heldQueueContainer = v.buildHeldQueueBanner()

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
		v.heldQueueContainer,
		tableHeader,
	)

	mainLayout := container.NewBorder(
		topSection,
		nil,
		nil,
		nil,
		listScroll,
	)
	uiRenderMu.Lock()
	v.chatPanelThemeOverride = container.NewThemeOverride(mainLayout, NewScaledChatTheme(v.chatScaleFactor()))
	uiRenderMu.Unlock()

	return container.NewStack(v.chatPanelThemeOverride, v.tooltipLayer)
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
	v.presetChatScale = preset.ChatScale
	v.liveTail = true
	v.currentPage = 0
	v.mu.Unlock()

	if v.chatPanelThemeOverride != nil {
		uiRenderMu.Lock()
		v.chatPanelThemeOverride.Theme = NewScaledChatTheme(v.chatScaleFactor())
		v.chatPanelThemeOverride.Refresh()
		uiRenderMu.Unlock()
	}

	v.initializing = false
	v.RefreshQuery()
}

func (v *LogsView) previewLiveLogPreset(p config.LiveLogPreset) {
	if strings.TrimSpace(p.ID) == "" {
		return
	}
	v.mu.Lock()
	openWin := v.openLiveWindows[p.ID]
	v.mu.Unlock()
	if openWin != nil {
		if openWin.win != nil && strings.TrimSpace(p.Name) != "" {
			openWin.win.SetTitle("TownCrier — " + strings.TrimSpace(p.Name))
		}
		if openWin.view != nil {
			openWin.view.applyLivePreset(p)
		}
	}
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
	var originalPreset config.LiveLogPreset
	hasOriginal := existing != nil
	if existing != nil {
		title = "Edit Live Log — " + existing.Name
		preset = *existing
		originalPreset = *existing
	}

	var dialogInitializing = true
	defer func() { dialogInitializing = false }()

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

	defaultPct := v.defaultChatScalePct()
	customScaleEntry := widget.NewEntry()
	customScaleEntry.SetPlaceHolder("50-300")
	initCustomVal := defaultPct
	if preset.ChatScale > 0 {
		initCustomVal = NormalizeChatScale(preset.ChatScale)
	}
	customScaleEntry.SetText(strconv.Itoa(initCustomVal))
	customScaleBox := container.NewBorder(nil, nil, nil, widget.NewLabel("%"), customScaleEntry)

	var scaleSel *widget.Select
	buildDraft := func(clampCustom bool) (config.LiveLogPreset, bool) {
		draft := preset
		draft.Name = strings.TrimSpace(nameEntry.Text)
		if draft.Name == "" {
			draft.Name = "Live Log"
		}
		if sessionSel.Selected == sessionOptionAll {
			draft.SessionMode = "all"
		} else {
			draft.SessionMode = "latest"
		}
		draft.HideBots = hideBotsCheck.Checked
		draft.ShowSummary = showSummaryCheck.Checked
		draft.Platform = platSel.Selected
		draft.EventType = typeSel.Selected
		draft.Outcome = statusSel.Selected
		draft.UserQuery = strings.TrimSpace(userEntry.Text)
		draft.TextQuery = strings.TrimSpace(msgEntry.Text)

		pct, isCustom, isInherit := ParseChatScaleLabel(scaleSel.Selected)
		switch {
		case isInherit:
			draft.ChatScale = 0
		case isCustom:
			if clampCustom {
				draft.ChatScale = ParseCustomChatScaleInput(customScaleEntry.Text, defaultPct)
			} else {
				clean := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(customScaleEntry.Text), "%"))
				val, err := strconv.Atoi(clean)
				if err != nil || val < ChatScaleMin || val > ChatScaleMax {
					return draft, false
				}
				draft.ChatScale = val
			}
		default:
			draft.ChatScale = pct
		}
		return draft, true
	}

	triggerLivePreview := func(clampCustom bool) {
		if dialogInitializing || !hasOriginal {
			return
		}
		if draft, valid := buildDraft(clampCustom); valid {
			v.previewLiveLogPreset(draft)
		}
	}

	scaleSel = widget.NewSelect(ChatScalePresetOptions(defaultPct), func(selected string) {
		_, isCustom, _ := ParseChatScaleLabel(selected)
		if isCustom {
			customScaleBox.Show()
		} else {
			customScaleBox.Hide()
		}
		triggerLivePreview(false)
	})
	initScaleLbl := FormatPresetChatScaleLabel(preset.ChatScale, defaultPct)
	scaleSel.SetSelected(initScaleLbl)
	if initScaleLbl == ChatScaleLabelCustom {
		customScaleBox.Show()
	} else {
		customScaleBox.Hide()
	}

	customScaleEntry.OnChanged = func(_ string) {
		triggerLivePreview(false)
	}
	customScaleEntry.OnSubmitted = func(_ string) {
		parsed := ParseCustomChatScaleInput(customScaleEntry.Text, defaultPct)
		dialogInitializing = true
		customScaleEntry.SetText(strconv.Itoa(parsed))
		dialogInitializing = false
		triggerLivePreview(true)
	}

	nameEntry.OnChanged = func(_ string) { triggerLivePreview(false) }
	hideBotsCheck.OnChanged = func(_ bool) { triggerLivePreview(false) }
	showSummaryCheck.OnChanged = func(_ bool) { triggerLivePreview(false) }
	sessionSel.OnChanged = func(_ string) { triggerLivePreview(false) }
	platSel.OnChanged = func(_ string) { triggerLivePreview(false) }
	typeSel.OnChanged = func(_ string) { triggerLivePreview(false) }
	statusSel.OnChanged = func(_ string) { triggerLivePreview(false) }
	userEntry.OnChanged = func(_ string) { triggerLivePreview(false) }
	msgEntry.OnChanged = func(_ string) { triggerLivePreview(false) }

	nameRow := container.NewBorder(nil, nil, newFilterFieldLabel("Name:"), nil, nameEntry)
	togglesRow := container.NewHBox(
		hideBotsCheck,
		showSummaryCheck,
		widget.NewSeparator(),
		newFilterFieldLabel("Chat Scale:"),
		newBorderedControl(scaleSel),
		customScaleBox,
	)

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
			if hasOriginal {
				v.previewLiveLogPreset(originalPreset)
			}
			return
		}
		finalDraft, _ := buildDraft(true)
		v.saveLiveLogPreset(finalDraft)
	}, v.window)
	d.Resize(fyne.NewSize(680, 320))
	d.Show()
}
