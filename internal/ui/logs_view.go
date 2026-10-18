package ui

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
)

const (
	logsPageSize    = 50
	maxLiveTailRows = 150
)

const (
	datePresetAllTime = "All Time"
	datePresetToday   = "Today"
	datePresetLast24h = "Last 24 Hours"
	datePresetLast7d  = "Last 7 Days"
	datePresetLast30d = "Last 30 Days"
	datePresetCustom  = "Custom Range"
)

const (
	platformOptionAll = "All Platforms"
)

const (
	outcomeLabelAll      = "All Events"
	outcomeLabelSpoken   = "Spoken Only"
	outcomeLabelPassed   = "Passed Filters (Spoken + Queue-Dropped)"
	outcomeLabelDroppedQ = "Dropped in Queue (Stale / Panic / Mod / Skip)"
	outcomeLabelFiltered = "Filtered Out (Bot / Read Rules / Blocked)"
	outcomeLabelMod      = "Moderation Actions"
)

// LogsView provides the Event Logs search UI, interactive date range pickers, live tail, and retention settings.
type LogsView struct {
	window  fyne.Window
	session *app.Session

	mu           sync.Mutex
	entries      []eventlog.Entry
	totalCount   int
	currentPage  int
	liveTail     bool
	fromDate     *time.Time
	toDate       *time.Time
	unsubLive    func()
	initializing bool

	liveTailCheck   *widget.Check
	datePresetSel   *widget.Select
	fromDateBtn     *widget.Button
	toDateBtn       *widget.Button
	platformSel     *widget.Select
	outcomeSel      *widget.Select
	userSearchEntry *ScrollPassEntry
	textSearchEntry *ScrollPassEntry

	enableLogCheck *widget.Check
	chatRetSel     *widget.Select
	eventRetSel    *widget.Select

	summaryLabel    *widget.Label
	hoverInfoLabel  *hoverCellLabel
	prevPageBtn     *widget.Button
	nextPageBtn     *widget.Button
	filterToggleBtn *widget.Button
	filterDrawer    *fyne.Container
	filtersExpanded bool
	listBox         *fyne.Container

	tooltipLayer  *fyne.Container
	tooltipBubble *fyne.Container
	tooltipText   *canvas.Text

	onStatus      func(msg string)
	onAssignVoice func(platform, username string)
}

// NewLogsView constructs a new LogsView bound to the active Session.
func NewLogsView(w fyne.Window, session *app.Session) *LogsView {
	return &LogsView{
		window:   w,
		session:  session,
		liveTail: true,
		listBox:  container.NewVBox(),
	}
}

// SetStatusHandler sets the status bar callback.
func (v *LogsView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

// SetOnAssignVoice sets the callback invoked when a viewer voice override is saved from a log row.
func (v *LogsView) SetOnAssignVoice(fn func(platform, username string)) {
	v.onAssignVoice = fn
}

func (v *LogsView) setStatus(msg string) {
	fyne.Do(func() {
		if v.onStatus != nil {
			v.onStatus(msg)
		}
	})
}

func (v *LogsView) currentTableWidth() float32 {
	if v.listBox != nil && v.listBox.Size().Width > 0 {
		return v.listBox.Size().Width
	}
	if v.tooltipLayer != nil && v.tooltipLayer.Size().Width > 0 {
		return v.tooltipLayer.Size().Width
	}
	if v.window != nil && v.window.Canvas() != nil && v.window.Canvas().Size().Width > 0 {
		return v.window.Canvas().Size().Width - 32
	}
	return 880
}

func truncateTooltip(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func (v *LogsView) showTooltipForObject(obj fyne.CanvasObject, tip string, hovered bool) {
	if !hovered || tip == "" {
		if v.tooltipBubble != nil {
			v.tooltipBubble.Hide()
		}
		if v.hoverInfoLabel != nil {
			v.hoverInfoLabel.SetText("")
		}
		return
	}

	shortTip := truncateTooltip(tip, 110)
	if v.hoverInfoLabel != nil {
		v.hoverInfoLabel.SetText(shortTip)
	}
	if v.tooltipBubble == nil || v.tooltipText == nil || v.tooltipLayer == nil {
		return
	}

	v.tooltipText.Text = shortTip
	v.tooltipText.Refresh()
	bubbleSize := v.tooltipBubble.MinSize()
	v.tooltipBubble.Resize(bubbleSize)

	appInstance := fyne.CurrentApp()
	if appInstance == nil || appInstance.Driver() == nil {
		return
	}
	objPos := appInstance.Driver().AbsolutePositionForObject(obj)
	layerPos := appInstance.Driver().AbsolutePositionForObject(v.tooltipLayer)
	rel := objPos.Subtract(layerPos)

	x := rel.X + (obj.Size().Width-bubbleSize.Width)/2
	layerSize := v.tooltipLayer.Size()
	if layerSize.Width > 0 && x+bubbleSize.Width > layerSize.Width-8 {
		x = layerSize.Width - bubbleSize.Width - 8
	}
	if x < 8 {
		x = 8
	}
	y := rel.Y - bubbleSize.Height - 4
	if y < 4 {
		y = rel.Y + obj.Size().Height + 4
	}

	v.tooltipBubble.Move(fyne.NewPos(x, y))
	v.tooltipBubble.Show()
	v.tooltipBubble.Refresh()
}

// Close unsubscribes any active Live Tail listener.
func (v *LogsView) Close() {
	v.mu.Lock()
	unsub := v.unsubLive
	v.unsubLive = nil
	v.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

// Build constructs the Chat Logs UI canvas object with a collapsed-by-default filter drawer.
func (v *LogsView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	v.tooltipBubble, v.tooltipText = buildTooltipBubble()
	v.tooltipLayer = container.NewWithoutLayout(v.tooltipBubble)

	// Initialize retention widgets as well so programmatic/test access works seamlessly.
	_ = v.BuildRetentionCard()

	v.filterDrawer = v.buildFilterDrawer()
	v.filterDrawer.Hide()
	v.filtersExpanded = false

	compactTopBar := v.buildPaginationBar()
	tableHeader := v.buildTableHeader()

	v.listBox = container.NewVBox()
	listScroll := newPanelVScroll(v.listBox)
	if v.userSearchEntry != nil {
		v.userSearchEntry.SetParentScroll(listScroll)
	}
	if v.textSearchEntry != nil {
		v.textSearchEntry.SetParentScroll(listScroll)
	}

	v.subscribeLiveTail()
	v.RefreshQuery()

	topSection := container.NewVBox(
		compactTopBar,
		v.filterDrawer,
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

// BuildRetentionCard constructs the Log Retention & Storage settings card for the Settings tab.
func (v *LogsView) BuildRetentionCard() *widget.Card {
	cfg := config.DefaultConfig()
	if v.session != nil {
		cfg = v.session.Config()
	}

	v.enableLogCheck = widget.NewCheck("Enable Event Logging", func(checked bool) {
		if v.initializing || v.session == nil {
			return
		}
		_ = v.session.UpdateConfig(func(c *config.Config) {
			c.Logging.Enabled = checked
		})
		v.setStatus("✓ Event logging setting saved.")
	})
	v.enableLogCheck.SetChecked(cfg.Logging.Enabled)

	chatRetOptions := []string{"7 Days", "14 Days", "30 Days", "90 Days", "Keep Forever"}
	v.chatRetSel = widget.NewSelect(chatRetOptions, func(selected string) {
		if v.initializing || v.session == nil {
			return
		}
		days := parseRetentionDays(selected, 30)
		_ = v.session.UpdateConfig(func(c *config.Config) {
			c.Logging.ChatRetentionDays = days
		})
		v.RefreshQuery()
		v.setStatus("✓ Chat log retention updated.")
	})
	v.chatRetSel.SetSelected(formatRetentionDays(cfg.Logging.ChatRetentionDays))

	eventRetOptions := []string{"30 Days", "90 Days", "180 Days", "365 Days", "Keep Forever"}
	v.eventRetSel = widget.NewSelect(eventRetOptions, func(selected string) {
		if v.initializing || v.session == nil {
			return
		}
		days := parseRetentionDays(selected, 90)
		_ = v.session.UpdateConfig(func(c *config.Config) {
			c.Logging.EventRetentionDays = days
		})
		v.RefreshQuery()
		v.setStatus("✓ Alert/Paid log retention updated.")
	})
	v.eventRetSel.SetSelected(formatRetentionDays(cfg.Logging.EventRetentionDays))

	pruneBtn := widget.NewButtonWithIcon("Prune Expired Now", theme.DeleteIcon(), func() {
		if v.session == nil || v.session.EventLogger() == nil {
			return
		}
		deleted, err := v.session.EventLogger().PruneNow(context.Background())
		if err != nil {
			v.setStatus("⚠️ Log prune error: " + err.Error())
			return
		}
		v.RefreshQuery()
		v.setStatus(fmt.Sprintf("✓ Pruned %d expired log entries.", deleted))
	})

	row := container.NewHBox(
		v.enableLogCheck,
		widget.NewSeparator(),
		widget.NewLabel("Chat TTL:"),
		v.chatRetSel,
		widget.NewLabel("Alert/Paid TTL:"),
		v.eventRetSel,
		pruneBtn,
	)

	return widget.NewCard("Log Retention & Storage", "Dynamic created_at retention preserves existing logs if you increase TTL", row)
}

func (v *LogsView) buildFilterDrawer() *fyne.Container {
	v.liveTailCheck = widget.NewCheck("🟢 Live Tail", func(checked bool) {
		v.mu.Lock()
		v.liveTail = checked
		if checked {
			v.currentPage = 0
		}
		v.mu.Unlock()
		if checked {
			v.RefreshQuery()
		}
	})
	v.liveTailCheck.SetChecked(true)

	presets := []string{
		datePresetAllTime,
		datePresetToday,
		datePresetLast24h,
		datePresetLast7d,
		datePresetLast30d,
		datePresetCustom,
	}
	v.datePresetSel = widget.NewSelect(presets, func(selected string) {
		if v.initializing {
			return
		}
		now := time.Now()
		switch selected {
		case datePresetAllTime:
			v.setDateRange(nil, nil)
		case datePresetToday:
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), now.Location())
			v.setDateRange(&start, &end)
		case datePresetLast24h:
			start := now.Add(-24 * time.Hour)
			v.setDateRange(&start, nil)
		case datePresetLast7d:
			start := now.AddDate(0, 0, -7)
			v.setDateRange(&start, nil)
		case datePresetLast30d:
			start := now.AddDate(0, 0, -30)
			v.setDateRange(&start, nil)
		}
		v.currentPage = 0
		v.RefreshQuery()
	})
	v.datePresetSel.SetSelected(datePresetAllTime)

	v.fromDateBtn = widget.NewButtonWithIcon("From: Any", theme.CalendarIcon(), func() {
		v.showDatePickerDialog("Select Start Date (From)", v.fromDate, false, func(chosen *time.Time) {
			v.initializing = true
			v.datePresetSel.SetSelected(datePresetCustom)
			v.initializing = false
			v.setDateRange(chosen, v.toDate)
			v.currentPage = 0
			v.RefreshQuery()
		})
	})

	v.toDateBtn = widget.NewButtonWithIcon("To: Any", theme.CalendarIcon(), func() {
		v.showDatePickerDialog("Select End Date (To)", v.toDate, true, func(chosen *time.Time) {
			v.initializing = true
			v.datePresetSel.SetSelected(datePresetCustom)
			v.initializing = false
			v.setDateRange(v.fromDate, chosen)
			v.currentPage = 0
			v.RefreshQuery()
		})
	})

	v.platformSel = widget.NewSelect([]string{platformOptionAll, "Twitch", "YouTube", "TikTok", "Kick"}, func(_ string) {
		if v.initializing {
			return
		}
		v.currentPage = 0
		v.RefreshQuery()
	})
	v.platformSel.SetSelected(platformOptionAll)

	v.outcomeSel = widget.NewSelect([]string{
		outcomeLabelAll,
		outcomeLabelSpoken,
		outcomeLabelPassed,
		outcomeLabelDroppedQ,
		outcomeLabelFiltered,
		outcomeLabelMod,
	}, func(_ string) {
		if v.initializing {
			return
		}
		v.currentPage = 0
		v.RefreshQuery()
	})
	v.outcomeSel.SetSelected(outcomeLabelAll)

	v.userSearchEntry = NewScrollPassEntry()
	v.userSearchEntry.SetPlaceHolder("Username, display name, or unique user ID...")
	v.userSearchEntry.OnChanged = func(_ string) {
		if v.initializing {
			return
		}
		v.currentPage = 0
		v.RefreshQuery()
	}

	v.textSearchEntry = NewScrollPassEntry()
	v.textSearchEntry.SetPlaceHolder("Search message or spoken text...")
	v.textSearchEntry.OnChanged = func(_ string) {
		if v.initializing {
			return
		}
		v.currentPage = 0
		v.RefreshQuery()
	}

	resetBtn := widget.NewButtonWithIcon("Reset Filters", theme.ContentClearIcon(), func() {
		v.ResetFilters()
	})

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.RefreshQuery()
	})

	topRow := container.NewHBox(
		v.liveTailCheck,
		widget.NewSeparator(),
		widget.NewLabel("Date Range:"),
		v.datePresetSel,
		v.fromDateBtn,
		v.toDateBtn,
		resetBtn,
		refreshBtn,
	)

	midRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, widget.NewLabel("Platform:"), nil, v.platformSel),
		container.NewBorder(nil, nil, widget.NewLabel("Status:"), nil, v.outcomeSel),
	)

	searchRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, widget.NewLabel("User / ID:"), nil, v.userSearchEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Message:"), nil, v.textSearchEntry),
	)

	drawerBg := canvas.NewRectangle(colorSlateSurface)
	return container.NewStack(
		drawerBg,
		container.NewPadded(container.NewVBox(topRow, midRow, searchRow)),
	)
}

func (v *LogsView) buildPaginationBar() *fyne.Container {
	v.filterToggleBtn = widget.NewButton("▸ Filters & Live Tail", func() {
		v.filtersExpanded = !v.filtersExpanded
		if v.filtersExpanded {
			v.filterToggleBtn.SetText("▾ Filters & Live Tail")
			if v.filterDrawer != nil {
				v.filterDrawer.Show()
			}
		} else {
			v.filterToggleBtn.SetText("▸ Filters & Live Tail")
			if v.filterDrawer != nil {
				v.filterDrawer.Hide()
			}
		}
	})

	v.summaryLabel = widget.NewLabelWithStyle("0 matching events", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.hoverInfoLabel = v.newHoverCellLabel("", "", fyne.TextStyle{Italic: true})
	v.hoverInfoLabel.Alignment = fyne.TextAlignCenter

	v.prevPageBtn = widget.NewButtonWithIcon("Prev", theme.NavigateBackIcon(), func() {
		v.mu.Lock()
		if v.currentPage > 0 {
			v.currentPage--
		}
		v.mu.Unlock()
		v.RefreshQuery()
	})
	v.prevPageBtn.Disable()

	v.nextPageBtn = widget.NewButtonWithIcon("Next", theme.NavigateNextIcon(), func() {
		v.mu.Lock()
		v.currentPage++
		v.liveTail = false
		v.mu.Unlock()
		if v.liveTailCheck != nil {
			v.liveTailCheck.SetChecked(false)
		}
		v.RefreshQuery()
	})
	v.nextPageBtn.Disable()

	leftBox := container.NewHBox(v.filterToggleBtn, widget.NewSeparator(), v.summaryLabel)
	navBox := container.NewHBox(v.prevPageBtn, v.nextPageBtn)
	return container.NewBorder(nil, widget.NewSeparator(), leftBox, navBox, v.hoverInfoLabel)
}

func (v *LogsView) buildTableHeader() fyne.CanvasObject {
	headerBg := canvas.NewRectangle(colorSlateSurface)
	bottomBorder := canvas.NewRectangle(colorSlateBorder)
	bottomBorder.SetMinSize(fyne.NewSize(1, 1))

	colHdr := func(title string, align fyne.TextAlign) *widget.Label {
		return widget.NewLabelWithStyle(title, align, fyne.TextStyle{Bold: true})
	}

	cols := container.New(logTableLayout{getWidth: v.currentTableWidth},
		colHdr("Time", fyne.TextAlignLeading),
		colHdr("Svc", fyne.TextAlignCenter),
		colHdr("User", fyne.TextAlignLeading),
		colHdr("Message", fyne.TextAlignLeading),
		colHdr("Filter", fyne.TextAlignCenter),
		colHdr("Spoken", fyne.TextAlignCenter),
		colHdr("Voice", fyne.TextAlignCenter),
	)

	return container.NewStack(
		headerBg,
		container.NewBorder(nil, bottomBorder, nil, nil, cols),
	)
}

// ResetFilters clears all active search filters and restores Live Tail mode.
func (v *LogsView) ResetFilters() {
	v.initializing = true
	v.setDateRange(nil, nil)
	if v.datePresetSel != nil {
		v.datePresetSel.SetSelected(datePresetAllTime)
	}
	if v.platformSel != nil {
		v.platformSel.SetSelected(platformOptionAll)
	}
	if v.outcomeSel != nil {
		v.outcomeSel.SetSelected(outcomeLabelAll)
	}
	if v.userSearchEntry != nil {
		v.userSearchEntry.SetText("")
	}
	if v.textSearchEntry != nil {
		v.textSearchEntry.SetText("")
	}
	if v.liveTailCheck != nil {
		v.liveTailCheck.SetChecked(true)
	}
	v.mu.Lock()
	v.liveTail = true
	v.currentPage = 0
	v.mu.Unlock()
	v.initializing = false

	v.RefreshQuery()
}

func (v *LogsView) setDateRange(from, to *time.Time) {
	v.mu.Lock()
	v.fromDate = from
	v.toDate = to
	v.mu.Unlock()

	if v.fromDateBtn != nil {
		if from == nil || from.IsZero() {
			v.fromDateBtn.SetText("From: Any")
		} else {
			v.fromDateBtn.SetText("From: " + from.Format("2006-01-02"))
		}
	}
	if v.toDateBtn != nil {
		if to == nil || to.IsZero() {
			v.toDateBtn.SetText("To: Any")
		} else {
			v.toDateBtn.SetText("To: " + to.Format("2006-01-02"))
		}
	}
}

func (v *LogsView) currentFilter() eventlog.QueryFilter {
	v.mu.Lock()
	from := v.fromDate
	to := v.toDate
	page := v.currentPage
	v.mu.Unlock()

	plat := ""
	if v.platformSel != nil && v.platformSel.Selected != "" && v.platformSel.Selected != platformOptionAll {
		plat = strings.ToLower(v.platformSel.Selected)
	}

	mode := eventlog.FilterOutcomeAll
	if v.outcomeSel != nil {
		switch v.outcomeSel.Selected {
		case outcomeLabelSpoken:
			mode = eventlog.FilterOutcomeSpokenOnly
		case outcomeLabelPassed:
			mode = eventlog.FilterOutcomePassedFilters
		case outcomeLabelDroppedQ:
			mode = eventlog.FilterOutcomeDroppedQueue
		case outcomeLabelFiltered:
			mode = eventlog.FilterOutcomeFilteredOut
		case outcomeLabelMod:
			mode = eventlog.FilterOutcomeModeration
		}
	}

	userQ := ""
	if v.userSearchEntry != nil {
		userQ = strings.TrimSpace(v.userSearchEntry.Text)
	}
	textQ := ""
	if v.textSearchEntry != nil {
		textQ = strings.TrimSpace(v.textSearchEntry.Text)
	}

	return eventlog.QueryFilter{
		From:        from,
		To:          to,
		Platform:    plat,
		UserQuery:   userQ,
		TextQuery:   textQ,
		OutcomeMode: mode,
		Limit:       logsPageSize,
		Offset:      page * logsPageSize,
	}
}

// RefreshQuery executes the current QueryFilter against the session's EventLogger and updates the UI.
func (v *LogsView) RefreshQuery() {
	filter := v.currentFilter()

	var res *eventlog.QueryResult
	if v.session != nil && v.session.EventLogger() != nil {
		r, err := v.session.EventLogger().Query(context.Background(), filter)
		if err == nil && r != nil {
			res = r
		}
	}
	if res == nil {
		res = &eventlog.QueryResult{}
	}

	v.mu.Lock()
	v.entries = res.Entries
	v.totalCount = res.TotalCount
	v.mu.Unlock()

	fyne.Do(func() {
		v.renderEntries()
	})
}

func (v *LogsView) subscribeLiveTail() {
	v.Close()
	if v.session == nil || v.session.EventLogger() == nil {
		return
	}

	unsub := v.session.EventLogger().Subscribe(func(ev eventlog.LiveUpdate) {
		v.mu.Lock()
		isLive := v.liveTail && v.currentPage == 0
		v.mu.Unlock()
		if !isLive {
			return
		}

		filter := v.currentFilter()

		switch ev.Kind {
		case eventlog.LiveUpdateAppend:
			if ev.Entry == nil || !entryMatchesFilter(*ev.Entry, filter) {
				return
			}
			v.mu.Lock()
			v.entries = append([]eventlog.Entry{*ev.Entry}, v.entries...)
			if len(v.entries) > maxLiveTailRows {
				v.entries = v.entries[:maxLiveTailRows]
			}
			v.totalCount++
			v.mu.Unlock()

			fyne.Do(func() {
				v.renderEntries()
			})

		case eventlog.LiveUpdateOutcome:
			v.mu.Lock()
			updated := false
			for i := range v.entries {
				if v.entries[i].ID == ev.EventID {
					v.entries[i].Spoken = ev.Update.Spoken
					v.entries[i].Outcome = ev.Update.Outcome
					v.entries[i].OutcomeDetail = ev.Update.OutcomeDetail
					if ev.Update.Speed > 0 {
						v.entries[i].Speed = ev.Update.Speed
					}
					v.entries[i].UpdatedAt = ev.Update.UpdatedAt
					updated = true
					break
				}
			}
			v.mu.Unlock()

			if updated {
				// If an outcome filter is active (e.g. Spoken Only), re-run query so newly spoken items appear
				if filter.OutcomeMode != "" && filter.OutcomeMode != eventlog.FilterOutcomeAll {
					v.RefreshQuery()
					return
				}
				fyne.Do(func() {
					v.renderEntries()
				})
			} else if filter.OutcomeMode == eventlog.FilterOutcomeSpokenOnly || filter.OutcomeMode == eventlog.FilterOutcomeDroppedQueue {
				v.RefreshQuery()
			}
		}
	})

	v.mu.Lock()
	v.unsubLive = unsub
	v.mu.Unlock()
}

func entryMatchesFilter(e eventlog.Entry, f eventlog.QueryFilter) bool {
	if f.From != nil && !f.From.IsZero() && e.CreatedAt.Before(*f.From) {
		return false
	}
	if f.To != nil && !f.To.IsZero() && e.CreatedAt.After(*f.To) {
		return false
	}
	if f.Platform != "" && f.Platform != "all" && !strings.EqualFold(e.Platform, f.Platform) {
		return false
	}
	if f.Channel != "" && !strings.EqualFold(f.Channel, "all") && !strings.EqualFold(e.Channel, f.Channel) {
		return false
	}
	if f.UserQuery != "" {
		uq := strings.ToLower(f.UserQuery)
		if !strings.Contains(strings.ToLower(e.UserID), uq) &&
			!strings.Contains(strings.ToLower(e.UserName), uq) &&
			!strings.Contains(strings.ToLower(e.DisplayName), uq) {
			return false
		}
	}
	if f.TextQuery != "" {
		tq := strings.ToLower(f.TextQuery)
		if !strings.Contains(strings.ToLower(e.RawText), tq) &&
			!strings.Contains(strings.ToLower(e.SpokenText), tq) {
			return false
		}
	}
	switch f.OutcomeMode {
	case eventlog.FilterOutcomeSpokenOnly:
		return e.Spoken
	case eventlog.FilterOutcomePassedFilters:
		return e.PassedFilters
	case eventlog.FilterOutcomeDroppedQueue:
		return e.PassedFilters && !e.Spoken && e.Outcome != eventlog.OutcomeQueued
	case eventlog.FilterOutcomeFilteredOut:
		return !e.PassedFilters && e.Outcome != eventlog.OutcomeModerationEvent
	case eventlog.FilterOutcomeModeration:
		return e.Outcome == eventlog.OutcomeModerationEvent || e.Outcome == eventlog.OutcomeDroppedModeration
	}
	return true
}

func (v *LogsView) renderEntries() {
	if v.listBox == nil {
		return
	}

	v.mu.Lock()
	entries := make([]eventlog.Entry, len(v.entries))
	copy(entries, v.entries)
	total := v.totalCount
	page := v.currentPage
	v.mu.Unlock()

	if v.summaryLabel != nil {
		if total == 0 || len(entries) == 0 {
			v.summaryLabel.SetText("0 matching events")
		} else {
			startIdx := page*logsPageSize + 1
			endIdx := startIdx + len(entries) - 1
			v.summaryLabel.SetText(fmt.Sprintf("Showing %d–%d of %d matching events", startIdx, endIdx, total))
		}
	}

	if v.prevPageBtn != nil {
		if page > 0 {
			v.prevPageBtn.Enable()
		} else {
			v.prevPageBtn.Disable()
		}
	}
	if v.nextPageBtn != nil {
		if (page+1)*logsPageSize < total {
			v.nextPageBtn.Enable()
		} else {
			v.nextPageBtn.Disable()
		}
	}

	v.listBox.Objects = nil
	if len(entries) == 0 {
		v.listBox.Add(widget.NewLabelWithStyle("No matching log entries found.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
		v.listBox.Refresh()
		return
	}

	lastDateKey := ""
	for i, entry := range entries {
		localTime := entry.CreatedAt.Local()
		dateKey := localTime.Format("2006-01-02")
		if dateKey != lastDateKey {
			lastDateKey = dateKey
			v.listBox.Add(v.buildDateSeparatorRow(localTime))
		}
		v.listBox.Add(v.buildLogRow(i, entry))
	}
	v.listBox.Refresh()
}

func (v *LogsView) buildDateSeparatorRow(t time.Time) fyne.CanvasObject {
	sepBg := canvas.NewRectangle(color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF})
	accentLine := canvas.NewRectangle(colorSkyBlueDim)
	accentLine.SetMinSize(fyne.NewSize(1, 1))

	dateStr := fmt.Sprintf("📅  %s  —  %s", t.Format("2006-01-02"), t.Format("Monday, January 2, 2006"))
	lbl := v.newHoverCellLabel(dateStr, "", fyne.TextStyle{Bold: true})

	return container.NewStack(
		sepBg,
		container.NewBorder(nil, accentLine, nil, nil, lbl),
	)
}

func (v *LogsView) buildLogRow(rowIdx int, e eventlog.Entry) fyne.CanvasObject {
	// [0] Time column (HH:MM:SS, never truncated; date is shown on date separator banners)
	localTS := e.CreatedAt.Local().Format("15:04:05")
	timeTip := fmt.Sprintf("Time: %s Local (%s UTC)",
		e.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		e.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	timeCell := v.newTimeCellLabel(localTS, timeTip)

	// [1] Service icon column
	platRes, platTip := platformIconAndTip(e.Platform)
	svcCell := v.newHoverIcon(platRes, fyne.NewSize(20, 20), platTip, nil)

	// [2] User & Role/Tag icons column
	displayName := e.DisplayName
	if displayName == "" {
		displayName = e.UserName
	}
	if displayName == "" {
		displayName = "—"
	}

	userTip := "User: " + displayName
	if e.UserName != "" && !strings.EqualFold(e.UserName, displayName) {
		userTip += " (@" + e.UserName + ")"
	}
	if e.UserID != "" {
		userTip += " • ID: " + e.UserID
	}
	if len(e.Roles) > 0 {
		userTip += " • Tags: " + strings.Join(e.Roles, ", ")
	}

	roleIcons := v.userRoleIcons(e)
	userNameLabel := v.newHoverCellLabel(displayName, userTip, fyne.TextStyle{Bold: true})
	var userCell fyne.CanvasObject
	if len(roleIcons) > 0 {
		iconBox := container.NewHBox(roleIcons...)
		userCell = container.NewBorder(nil, nil, iconBox, nil, userNameLabel)
	} else {
		userCell = userNameLabel
	}

	// [3] Message column (expanding flex, word-wrapped)
	msgLine := strings.ReplaceAll(strings.TrimSpace(e.RawText), "\n", " ")
	if msgLine == "" {
		msgLine = fmt.Sprintf("(%s event)", e.Kind)
	}
	msgTip := ""
	if e.SpokenText != "" && !strings.EqualFold(e.SpokenText, e.RawText) {
		msgTip = "Spoken as: " + e.SpokenText
	}
	msgCell := v.newWrappedCellLabel(msgLine, msgTip, fyne.TextStyle{})

	// [4] Passed Filters icon column
	filterRes, filterTip := filterStatusIconAndTip(e)
	filterCell := v.newHoverIcon(filterRes, fyne.NewSize(20, 20), filterTip, nil)

	// [5] Spoken / Queue Outcome icon column
	spokenRes, spokenTip := spokenStatusIconAndTip(e)
	spokenCell := v.newHoverIcon(spokenRes, fyne.NewSize(20, 20), spokenTip, nil)

	// [6] Voice icon column (clickable to assign voice override)
	voiceRes, voiceTip := voiceStatusIconAndTip(e)
	var onVoiceTap func()
	if e.UserName != "" && e.Kind != "moderation" {
		plat := e.Platform
		uName := e.UserName
		onVoiceTap = func() {
			v.showAssignVoiceDialog(plat, uName)
		}
	}
	voiceCell := v.newHoverIcon(voiceRes, fyne.NewSize(20, 20), voiceTip, onVoiceTap)

	rowCols := container.New(logTableLayout{getWidth: v.currentTableWidth},
		timeCell,
		svcCell,
		userCell,
		msgCell,
		filterCell,
		spokenCell,
		voiceCell,
	)

	var bgColor color.Color = colorObsidianSlate
	if rowIdx%2 == 1 {
		bgColor = color.NRGBA{R: 0x15, G: 0x20, B: 0x33, A: 0xFF}
	}
	rowBg := canvas.NewRectangle(bgColor)
	divider := canvas.NewRectangle(color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF})
	divider.SetMinSize(fyne.NewSize(1, 1))

	return container.NewStack(
		rowBg,
		container.NewBorder(nil, divider, nil, nil, rowCols),
	)
}

func (v *LogsView) showAssignVoiceDialog(platform, username string) {
	if v.session == nil {
		return
	}
	dash := NewDashboardView(v.window, v.session)
	dash.SetOnAssignVoice(v.onAssignVoice)
	dash.SetStatusHandler(v.onStatus)
	dash.showQuickAssignDialog(platform, username)
}

// showDatePickerDialog opens an interactive Calendar Date Picker modal with month/year navigation,
// a 7-column day grid, direct YYYY-MM-DD input, and Today / Clear buttons.
func (v *LogsView) showDatePickerDialog(title string, initial *time.Time, endOfDay bool, onSelect func(*time.Time)) {
	now := time.Now()
	viewDate := now
	if initial != nil && !initial.IsZero() {
		viewDate = initial.Local()
	}

	dateEntry := widget.NewEntry()
	dateEntry.SetPlaceHolder("YYYY-MM-DD")
	if initial != nil && !initial.IsZero() {
		dateEntry.SetText(initial.Local().Format("2006-01-02"))
	}

	monthLabel := widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	gridBox := container.NewGridWithColumns(7)

	var renderCalendar func()
	renderCalendar = func() {
		monthLabel.SetText(viewDate.Format("January 2006"))
		gridBox.Objects = nil

		for _, wd := range []string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"} {
			gridBox.Add(widget.NewLabelWithStyle(wd, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}))
		}

		firstOfMonth := time.Date(viewDate.Year(), viewDate.Month(), 1, 0, 0, 0, 0, now.Location())
		startWeekday := int(firstOfMonth.Weekday())
		daysInMonth := time.Date(viewDate.Year(), viewDate.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()

		for range startWeekday {
			gridBox.Add(widget.NewLabel(""))
		}

		selectedStr := strings.TrimSpace(dateEntry.Text)
		for day := 1; day <= daysInMonth; day++ {
			d := day
			dayDate := time.Date(viewDate.Year(), viewDate.Month(), d, 0, 0, 0, 0, now.Location())
			dayStr := dayDate.Format("2006-01-02")
			btn := widget.NewButton(strconv.Itoa(d), func() {
				dateEntry.SetText(dayStr)
				renderCalendar()
			})
			if dayStr == selectedStr {
				btn.Importance = widget.HighImportance
			}
			gridBox.Add(btn)
		}
		gridBox.Refresh()
	}

	prevMonthBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		viewDate = viewDate.AddDate(0, -1, 0)
		renderCalendar()
	})
	nextMonthBtn := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		viewDate = viewDate.AddDate(0, 1, 0)
		renderCalendar()
	})

	todayBtn := widget.NewButton("Today", func() {
		viewDate = time.Now()
		dateEntry.SetText(viewDate.Format("2006-01-02"))
		renderCalendar()
	})
	clearBtn := widget.NewButton("Clear (Any)", func() {
		dateEntry.SetText("")
		renderCalendar()
	})

	renderCalendar()

	navRow := container.NewBorder(nil, nil, prevMonthBtn, nextMonthBtn, monthLabel)
	entryRow := container.NewBorder(nil, nil, widget.NewLabel("Date (YYYY-MM-DD):"), container.NewHBox(todayBtn, clearBtn), dateEntry)

	content := container.NewVBox(
		navRow,
		gridBox,
		widget.NewSeparator(),
		entryRow,
	)

	dialog.ShowCustomConfirm(title, "Apply", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		txt := strings.TrimSpace(dateEntry.Text)
		if txt == "" {
			onSelect(nil)
			return
		}
		parsed, err := time.ParseInLocation("2006-01-02", txt, now.Location())
		if err != nil {
			v.setStatus("⚠️ Invalid date format. Use YYYY-MM-DD.")
			return
		}
		if endOfDay {
			parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), now.Location())
		}
		onSelect(&parsed)
	}, v.window)
}

func parseRetentionDays(label string, fallback int) int {
	switch label {
	case "7 Days":
		return 7
	case "14 Days":
		return 14
	case "30 Days":
		return 30
	case "90 Days":
		return 90
	case "180 Days":
		return 180
	case "365 Days":
		return 365
	case "Keep Forever":
		return 0
	default:
		return fallback
	}
}

func formatRetentionDays(days int) string {
	switch days {
	case 0:
		return "Keep Forever"
	case 7:
		return "7 Days"
	case 14:
		return "14 Days"
	case 30:
		return "30 Days"
	case 90:
		return "90 Days"
	case 180:
		return "180 Days"
	case 365:
		return "365 Days"
	default:
		return "30 Days"
	}
}
