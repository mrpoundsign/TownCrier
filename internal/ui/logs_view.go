package ui

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"slices"
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
	"github.com/mrpoundsign/TownCrier/internal/bots"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
)

const (
	logsPageSize               = 50
	maxLiveTailRows            = 150
	searchDebounceDuration     = 200 * time.Millisecond
	liveRenderCoalesceDuration = 15 * time.Millisecond
)

// uiRenderMu serializes background-goroutine UI render batches across views.
// In Fyne's headless test driver, fyne.Do(fn) executes directly on the calling
// goroutine without an event loop, so concurrent timer/status callbacks would
// otherwise access Fyne's process-wide font/theme caches in parallel.
var uiRenderMu sync.Mutex

const (
	datePresetAllTime = "All Time"
	datePresetToday   = "Today"
	datePresetLast24h = "Last 24 Hours"
	datePresetLast7d  = "Last 7 Days"
	datePresetLast30d = "Last 30 Days"
	datePresetCustom  = "Custom Range"
)

const (
	platformOptionAll   = "All Platforms"
	sessionOptionAll    = "All Sessions"
	sessionOptionLatest = "Latest Session"
	sessionOptionMulti  = "Select Multiple..."
	sessionIDAll        = int64(0)
	sessionIDLatest     = int64(-1)
	sessionIDMulti      = int64(-2)
)

const (
	outcomeLabelAll      = "All Events"
	outcomeLabelSpoken   = "Spoken Only"
	outcomeLabelPassed   = "Passed Filters (Spoken + Queue-Dropped)"
	outcomeLabelDroppedQ = "Dropped in Queue (Stale / Panic / Mod / Skip)"
	outcomeLabelFiltered = "Filtered Out (Bot / Read Rules / Blocked)"
	outcomeLabelMod      = "Moderation Actions"
)

const (
	eventTypeAll         = "All Types"
	eventTypeChatOnly    = "Chat Only"
	eventTypeAlertsOnly  = "Alerts Only"
	eventTypeFollows     = "Follows"
	eventTypeSubs        = "Subscriptions"
	eventTypeCheers      = "Cheers / Gifts"
	eventTypeRaids       = "Raids / Hosts"
	eventTypeRedemptions = "Redemptions"
	eventTypeModeration  = "Moderation"
)

// LogsView provides the Event Logs search UI, interactive date range pickers, live tail, and retention settings.
type LogsView struct {
	window  fyne.Window
	session *app.Session

	mu           sync.Mutex
	closed       bool
	entries      []eventlog.Entry
	totalCount   int
	currentPage  int
	liveTail     bool
	hideBots     bool
	showSummary  bool
	fromDate     *time.Time
	toDate       *time.Time
	unsubLive    func()
	unsubHeld    func()
	searchTimer  *time.Timer
	renderTimer  *time.Timer
	tabActive    bool
	renderDirty  bool
	renderCount  uint64
	queryCancel  context.CancelFunc
	querySeq     uint64
	initializing bool

	liveTailCheck    *widget.Check
	hideBotsCheck    *widget.Check
	showSummaryCheck *widget.Check
	datePresetSel    *widget.Select
	fromDateBtn      *widget.Button
	toDateBtn        *widget.Button
	platformSel      *widget.Select
	eventTypeSel     *widget.Select
	outcomeSel       *widget.Select
	userSearchEntry  *ScrollPassEntry
	textSearchEntry  *ScrollPassEntry

	enableLogCheck       *widget.Check
	chatRetSel           *widget.Select
	eventRetSel          *widget.Select
	chatScaleSel         *widget.Select
	chatScaleCustomEntry *widget.Entry
	chatScaleCustomBox   *fyne.Container
	pruneBtn             *widget.Button
	addLiveLogBtn        *widget.Button
	disabledBanner       *fyne.Container

	presetChatScale        int
	chatPanelThemeOverride *container.ThemeOverride

	selectedSessionID      int64
	selectedSessionIDs     []int64
	latestSessionID        int64
	selectedPlatform       string
	sessionMap             map[string]int64
	sessionSel             *widget.Select
	multiSessionBtn        *widget.Button
	summary                eventlog.QuerySummary
	summaryTotalsLabel     *widget.Label
	summaryTotalsItemsBox  *fyne.Container
	summaryTotalsContainer *fyne.Container

	heldQueueContainer *fyne.Container
	heldQueueHeaderLbl *widget.Label
	heldQueueToggleBtn *widget.Button
	heldQueueItemsBox  *fyne.Container
	heldQueueExpanded  bool

	summaryLabel    *widget.Label
	hoverInfoLabel  *hoverCellLabel
	prevPageBtn     *widget.Button
	nextPageBtn     *widget.Button
	filterToggleBtn  *widget.Button
	filterDrawer     *fyne.Container
	filtersExpanded  bool
	listBox          *fyne.Container
	subTabs          *container.AppTabs
	liveLogsCardsBox *fyne.Container
	openLiveWindows  map[string]*liveLogWindow

	tooltipLayer  *fyne.Container
	tooltipBubble *fyne.Container
	tooltipText   *canvas.Text
	emoteCache    *EmoteCache

	onStatus           func(msg string)
	onAssignVoice      func(platform, username string)
	onChatScaleChanged func(pct int)
}

// NewLogsView constructs a new LogsView bound to the active Session.
func NewLogsView(w fyne.Window, session *app.Session) *LogsView {
	return &LogsView{
		window:            w,
		session:           session,
		liveTail:          true,
		hideBots:          true,
		showSummary:       true,
		tabActive:         true,
		heldQueueExpanded: true,
		listBox:           container.NewVBox(),
	}
}

// SetTabActive notifies LogsView whether its containing tab is currently visible in the main window.
// While inactive, live-tail UI rebuilds are deferred until the tab becomes active again.
func (v *LogsView) SetTabActive(active bool) {
	v.mu.Lock()
	wasActive := v.tabActive
	v.tabActive = active
	dirty := v.renderDirty
	if active {
		v.renderDirty = false
	} else if v.renderTimer != nil {
		v.renderTimer.Stop()
		v.renderTimer = nil
	}
	v.mu.Unlock()

	if active && !wasActive && dirty {
		fyne.Do(func() {
			v.renderEntries()
		})
	}
}

func (v *LogsView) scheduleLiveRender() {
	v.mu.Lock()
	if !v.tabActive {
		v.renderDirty = true
		v.mu.Unlock()
		return
	}
	if v.renderTimer != nil {
		v.renderDirty = true
		v.mu.Unlock()
		return
	}
	v.renderDirty = false
	v.renderTimer = time.AfterFunc(liveRenderCoalesceDuration, func() {
		v.mu.Lock()
		active := v.tabActive
		v.renderDirty = false
		v.mu.Unlock()
		if active {
			fyne.Do(func() {
				v.renderEntries()
			})
		}
		v.mu.Lock()
		dirty := v.renderDirty
		activeNow := v.tabActive
		v.renderTimer = nil
		v.mu.Unlock()
		if activeNow && dirty {
			v.scheduleLiveRender()
		}
	})
	v.mu.Unlock()
}

// SetEmoteCache overrides the EmoteCache used for inline emote rendering (useful in tests).
func (v *LogsView) SetEmoteCache(cache *EmoteCache) {
	v.emoteCache = cache
}

// SetStatusHandler sets the status bar callback.
func (v *LogsView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

// SetOnAssignVoice sets the callback invoked when a viewer voice override is saved from a log row.
func (v *LogsView) SetOnAssignVoice(fn func(platform, username string)) {
	v.onAssignVoice = fn
}

// SetOnChatScaleChanged sets the callback invoked when the default chat panel scale is updated in Settings.
func (v *LogsView) SetOnChatScaleChanged(fn func(pct int)) {
	v.onChatScaleChanged = fn
}

func (v *LogsView) defaultChatScalePct() int {
	if v.session != nil && v.session.Config() != nil {
		return NormalizeChatScale(v.session.Config().Logging.ChatScale)
	}
	return ChatScaleDefault
}

func (v *LogsView) effectiveChatScalePct() int {
	v.mu.Lock()
	pScale := v.presetChatScale
	v.mu.Unlock()
	if pScale > 0 {
		return NormalizeChatScale(pScale)
	}
	return v.defaultChatScalePct()
}

func (v *LogsView) chatScaleFactor() float32 {
	return float32(v.effectiveChatScalePct()) / 100.0
}

// ApplyChatScale updates the scoped chat panel theme, re-renders rows at the new scale,
// and propagates inherited scale updates to any open Live Log pop-out windows.
func (v *LogsView) ApplyChatScale() {
	fyne.Do(func() {
		s := v.chatScaleFactor()
		if v.chatPanelThemeOverride != nil {
			uiRenderMu.Lock()
			v.chatPanelThemeOverride.Theme = NewScaledChatTheme(s)
			v.chatPanelThemeOverride.Refresh()
			uiRenderMu.Unlock()
		}
		v.renderEntries()
		v.refreshLiveLogCards()

		v.mu.Lock()
		var inherited []*liveLogWindow
		for _, lw := range v.openLiveWindows {
			if lw != nil && lw.preset.ChatScale <= 0 && lw.view != nil {
				inherited = append(inherited, lw)
			}
		}
		v.mu.Unlock()

		for _, lw := range inherited {
			lw.view.ApplyChatScale()
		}
	})
}

// SyncFromConfig updates the retention and chat scale controls from the active session config.
func (v *LogsView) SyncFromConfig() {
	if v.session == nil || v.session.Config() == nil {
		return
	}
	cfg := v.session.Config()
	v.initializing = true
	defer func() { v.initializing = false }()

	pct := NormalizeChatScale(cfg.Logging.ChatScale)
	lbl := FormatChatScaleLabel(pct)
	if v.chatScaleSel != nil {
		v.chatScaleSel.SetSelected(lbl)
	}
	if v.chatScaleCustomEntry != nil {
		v.chatScaleCustomEntry.SetText(strconv.Itoa(pct))
	}
	if v.chatScaleCustomBox != nil {
		if lbl == ChatScaleLabelCustom {
			v.chatScaleCustomBox.Show()
		} else {
			v.chatScaleCustomBox.Hide()
		}
	}
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

// Close unsubscribes any active Live Tail listener, closes any open pop-out windows, and cancels any pending search timer or query.
func (v *LogsView) Close() {
	v.mu.Lock()
	v.closed = true
	unsub := v.unsubLive
	v.unsubLive = nil
	unsubHeld := v.unsubHeld
	v.unsubHeld = nil
	if v.searchTimer != nil {
		v.searchTimer.Stop()
		v.searchTimer = nil
	}
	if v.renderTimer != nil {
		v.renderTimer.Stop()
		v.renderTimer = nil
	}
	if v.queryCancel != nil {
		v.queryCancel()
		v.queryCancel = nil
	}
	v.querySeq++
	var toClose []*liveLogWindow
	for _, lw := range v.openLiveWindows {
		if lw != nil {
			toClose = append(toClose, lw)
		}
	}
	v.openLiveWindows = nil
	v.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if unsubHeld != nil {
		unsubHeld()
	}
	for _, lw := range toClose {
		lw.Close()
	}
	uiRenderMu.Lock()
	uiRenderMu.Unlock() //nolint:staticcheck // barrier ensuring any in-flight renderEntries completes before Close returns
}

// Build constructs the Chat Logs UI canvas object with Live Logs and Search History sub-tabs.
func (v *LogsView) Build() fyne.CanvasObject {
	v.mu.Lock()
	v.closed = false
	v.mu.Unlock()
	v.initializing = true
	defer func() { v.initializing = false }()

	v.tooltipBubble, v.tooltipText = buildTooltipBubble()
	v.tooltipLayer = container.NewWithoutLayout(v.tooltipBubble)

	// Initialize retention widgets as well so programmatic/test access works seamlessly.
	_ = v.BuildRetentionCard()

	v.initSummaryTotalsBanner()

	v.heldQueueContainer = v.buildHeldQueueBanner()

	v.filterDrawer = v.buildFilterDrawer()
	v.filterDrawer.Show()
	v.filtersExpanded = true

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

	v.refreshSessionDropdown()
	v.subscribeLiveTail()
	v.RefreshQuery()

	topSection := container.NewVBox(
		compactTopBar,
		v.filterDrawer,
	)

	chatPanelHeader := container.NewVBox(
		v.summaryTotalsContainer,
		v.heldQueueContainer,
		tableHeader,
	)
	chatPanelBody := container.NewBorder(
		chatPanelHeader,
		nil,
		nil,
		nil,
		listScroll,
	)
	v.chatPanelThemeOverride = container.NewThemeOverride(chatPanelBody, NewScaledChatTheme(v.chatScaleFactor()))

	searchLayout := container.NewBorder(
		topSection,
		nil,
		nil,
		nil,
		v.chatPanelThemeOverride,
	)

	v.subTabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Live Logs", theme.VisibilityIcon(), v.buildLiveLogsTab()),
		container.NewTabItemWithIcon("Search History", theme.SearchIcon(), searchLayout),
	)

	bannerBg := canvas.NewRectangle(color.NRGBA{R: 69, G: 26, B: 3, A: 240})
	bannerBg.CornerRadius = 4
	bannerText := widget.NewLabelWithStyle("⚠️ Live Event Logging is disabled in Settings. Inbound stream events and chat messages are not being recorded to disk.", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	v.disabledBanner = container.NewStack(
		bannerBg,
		container.NewPadded(bannerText),
	)

	loggingEnabled := true
	if v.session != nil {
		loggingEnabled = v.session.Config().Logging.Enabled
	}
	if !loggingEnabled {
		v.disabledBanner.Show()
		if v.liveTailCheck != nil {
			v.liveTailCheck.Disable()
		}
	} else {
		v.disabledBanner.Hide()
	}

	mainBox := container.NewBorder(
		v.disabledBanner,
		nil,
		nil,
		nil,
		v.subTabs,
	)

	return container.NewStack(mainBox, v.tooltipLayer)
}

func newFilterFieldLabel(text string) fyne.CanvasObject {
	lbl := canvas.NewText(text, colorSlateMuted)
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	lbl.TextSize = theme.TextSize()
	return container.NewCenter(container.NewPadded(lbl))
}

func newBorderedControl(ctrl fyne.CanvasObject) fyne.CanvasObject {
	box := canvas.NewRectangle(colorInputWell)
	box.StrokeColor = colorInputBorder
	box.StrokeWidth = 1
	box.CornerRadius = 4
	return container.NewStack(box, ctrl)
}

func (v *LogsView) initSummaryTotalsBanner() {
	v.summaryTotalsLabel = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	v.summaryTotalsItemsBox = container.NewHBox(v.summaryTotalsLabel)
	totalsBg := canvas.NewRectangle(colorSlateSurface)
	totalsBorder := canvas.NewRectangle(colorSkyBlueDim)
	totalsBorder.SetMinSize(fyne.NewSize(1, 1))
	v.summaryTotalsContainer = container.NewStack(
		totalsBg,
		container.NewBorder(nil, totalsBorder, nil, nil, container.NewPadded(container.NewCenter(v.summaryTotalsItemsBox))),
	)
	v.summaryTotalsContainer.Hide()
}

func (v *LogsView) buildSummaryTotalsObjects(s eventlog.QuerySummary) []fyne.CanvasObject {
	type item struct {
		icon fyne.Resource
		text string
	}
	var items []item
	if s.TotalBits > 0 {
		items = append(items, item{text: fmt.Sprintf("🪙 %.0f Bits", s.TotalBits)})
	}
	if s.TotalDiamonds > 0 {
		items = append(items, item{text: fmt.Sprintf("💎 %.0f Diamonds", s.TotalDiamonds)})
	}
	if s.TotalJewels > 0 {
		items = append(items, item{icon: resEventJewel, text: fmt.Sprintf("%.0f Jewels", s.TotalJewels)})
	}
	if len(s.SuperChatByCurr) > 0 {
		currs := make([]string, 0, len(s.SuperChatByCurr))
		for curr := range s.SuperChatByCurr {
			currs = append(currs, curr)
		}
		slices.Sort(currs)
		for _, curr := range currs {
			amt := s.SuperChatByCurr[curr]
			if amt > 0 {
				items = append(items, item{text: fmt.Sprintf("💬 %.0f %s", amt, curr)})
			}
		}
	}
	if s.TotalSubs > 0 {
		items = append(items, item{text: fmt.Sprintf("⭐ %d Subs", s.TotalSubs)})
	}
	if s.TotalGiftSubs > 0 {
		items = append(items, item{text: fmt.Sprintf("🎁 %d Gift Subs", s.TotalGiftSubs)})
	}
	if s.TotalKicks > 0 {
		items = append(items, item{text: fmt.Sprintf("🟢 %.0f Kicks", s.TotalKicks)})
	}
	if s.TotalFollows > 0 {
		items = append(items, item{text: fmt.Sprintf("❤️ %d Follows", s.TotalFollows)})
	}
	if s.TotalRaids > 0 {
		items = append(items, item{text: fmt.Sprintf("🚀 %d Raids", s.TotalRaids)})
	}
	if s.TotalRedemptions > 0 {
		items = append(items, item{text: fmt.Sprintf("🎟️ %d Redemptions", s.TotalRedemptions)})
	}
	if len(items) == 0 {
		return nil
	}

	scale := v.chatScaleFactor()
	iconSz := fyne.NewSize(18*scale, 18*scale)
	var objs []fyne.CanvasObject
	for i, it := range items {
		if i > 0 {
			objs = append(objs, widget.NewLabelWithStyle("•", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}))
		}
		lbl := widget.NewLabelWithStyle(it.text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		if it.icon != nil {
			img := canvas.NewImageFromResource(it.icon)
			img.FillMode = canvas.ImageFillContain
			img.SetMinSize(iconSz)
			objs = append(objs, container.NewHBox(container.NewCenter(img), lbl))
		} else {
			objs = append(objs, lbl)
		}
	}
	return objs
}

func formatSummaryTotals(s eventlog.QuerySummary) string {
	var parts []string
	if s.TotalBits > 0 {
		parts = append(parts, fmt.Sprintf("🪙 %.0f Bits", s.TotalBits))
	}
	if s.TotalDiamonds > 0 {
		parts = append(parts, fmt.Sprintf("💎 %.0f Diamonds", s.TotalDiamonds))
	}
	if s.TotalJewels > 0 {
		parts = append(parts, fmt.Sprintf("♦️ %.0f Jewels", s.TotalJewels))
	}
	if len(s.SuperChatByCurr) > 0 {
		currs := make([]string, 0, len(s.SuperChatByCurr))
		for curr := range s.SuperChatByCurr {
			currs = append(currs, curr)
		}
		slices.Sort(currs)
		for _, curr := range currs {
			amt := s.SuperChatByCurr[curr]
			if amt > 0 {
				parts = append(parts, fmt.Sprintf("💬 %.0f %s", amt, curr))
			}
		}
	}
	if s.TotalSubs > 0 {
		parts = append(parts, fmt.Sprintf("⭐ %d Subs", s.TotalSubs))
	}
	if s.TotalGiftSubs > 0 {
		parts = append(parts, fmt.Sprintf("🎁 %d Gift Subs", s.TotalGiftSubs))
	}
	if s.TotalKicks > 0 {
		parts = append(parts, fmt.Sprintf("🟢 %.0f Kicks", s.TotalKicks))
	}
	if s.TotalFollows > 0 {
		parts = append(parts, fmt.Sprintf("❤️ %d Follows", s.TotalFollows))
	}
	if s.TotalRaids > 0 {
		parts = append(parts, fmt.Sprintf("🚀 %d Raids", s.TotalRaids))
	}
	if s.TotalRedemptions > 0 {
		parts = append(parts, fmt.Sprintf("🎟️ %d Redemptions", s.TotalRedemptions))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "   •   ")
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
		if v.session.EventLogger() != nil {
			v.session.EventLogger().SetEnabled(checked)
		}
		v.SyncLoggingState(checked)
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

	v.pruneBtn = widget.NewButtonWithIcon("Prune Expired Now", theme.DeleteIcon(), func() {
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

	defaultScalePct := NormalizeChatScale(cfg.Logging.ChatScale)
	v.chatScaleCustomEntry = widget.NewEntry()
	v.chatScaleCustomEntry.SetPlaceHolder("50-300")
	v.chatScaleCustomEntry.SetText(strconv.Itoa(defaultScalePct))

	applyCustomDefaultScale := func(raw string, clampText bool) {
		if v.initializing || v.session == nil {
			return
		}
		clean := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
		val, err := strconv.Atoi(clean)
		if err != nil || val <= 0 {
			if clampText {
				fallback := v.defaultChatScalePct()
				v.initializing = true
				v.chatScaleCustomEntry.SetText(strconv.Itoa(fallback))
				v.initializing = false
			}
			return
		}
		if !clampText && (val < ChatScaleMin || val > ChatScaleMax) {
			return
		}
		pct := NormalizeChatScale(val)
		if clampText && strconv.Itoa(pct) != raw {
			v.initializing = true
			v.chatScaleCustomEntry.SetText(strconv.Itoa(pct))
			v.initializing = false
		}
		_ = v.session.UpdateConfig(func(c *config.Config) {
			c.Logging.ChatScale = pct
		})
		v.ApplyChatScale()
		if v.onChatScaleChanged != nil {
			v.onChatScaleChanged(pct)
		}
		v.setStatus(fmt.Sprintf("✓ Default chat panel scale updated to %d%%.", pct))
	}

	v.chatScaleCustomEntry.OnChanged = func(s string) {
		applyCustomDefaultScale(s, false)
	}
	v.chatScaleCustomEntry.OnSubmitted = func(s string) {
		applyCustomDefaultScale(s, true)
	}

	v.chatScaleCustomBox = container.NewHBox(
		v.chatScaleCustomEntry,
		widget.NewLabel("% (50–300%)"),
	)

	v.chatScaleSel = widget.NewSelect(ChatScaleOptions(), func(selected string) {
		if v.initializing {
			return
		}
		pct, isCustom, _ := ParseChatScaleLabel(selected)
		if isCustom {
			cur := v.defaultChatScalePct()
			v.initializing = true
			v.chatScaleCustomEntry.SetText(strconv.Itoa(cur))
			v.initializing = false
			if v.chatScaleCustomBox != nil {
				v.chatScaleCustomBox.Show()
			}
			return
		}
		if v.chatScaleCustomBox != nil {
			v.chatScaleCustomBox.Hide()
		}
		v.initializing = true
		v.chatScaleCustomEntry.SetText(strconv.Itoa(pct))
		v.initializing = false
		if v.session != nil {
			_ = v.session.UpdateConfig(func(c *config.Config) {
				c.Logging.ChatScale = pct
			})
		}
		v.ApplyChatScale()
		if v.onChatScaleChanged != nil {
			v.onChatScaleChanged(pct)
		}
		v.setStatus(fmt.Sprintf("✓ Default chat panel scale updated to %d%%.", pct))
	})
	initScaleLabel := FormatChatScaleLabel(defaultScalePct)
	v.chatScaleSel.SetSelected(initScaleLabel)
	if initScaleLabel == ChatScaleLabelCustom {
		v.chatScaleCustomBox.Show()
	} else {
		v.chatScaleCustomBox.Hide()
	}

	if !cfg.Logging.Enabled {
		v.chatRetSel.Disable()
		v.eventRetSel.Disable()
		v.chatScaleSel.Disable()
		v.chatScaleCustomEntry.Disable()
		v.pruneBtn.Disable()
	}

	topRow := container.NewBorder(nil, nil, v.enableLogCheck, v.pruneBtn)
	ttlRow := container.NewHBox(
		widget.NewLabel("Chat TTL:"),
		v.chatRetSel,
		widget.NewSeparator(),
		widget.NewLabel("Alert/Paid TTL:"),
		v.eventRetSel,
	)
	scaleRow := container.NewHBox(
		widget.NewLabel("Default Chat Panel Scale:"),
		v.chatScaleSel,
		v.chatScaleCustomBox,
	)

	return widget.NewCard("Log Retention & Storage", "Configure automatic log expiration, storage cleanup, and default chat panel scale", container.NewVBox(topRow, ttlRow, scaleRow))
}

// SyncLoggingState updates retention and live tail controls to reflect whether event logging is enabled.
func (v *LogsView) SyncLoggingState(enabled bool) {
	fyne.Do(func() {
		if v.enableLogCheck != nil && v.enableLogCheck.Checked != enabled {
			v.initializing = true
			v.enableLogCheck.SetChecked(enabled)
			v.initializing = false
		}
		if v.chatRetSel != nil {
			if enabled {
				v.chatRetSel.Enable()
			} else {
				v.chatRetSel.Disable()
			}
		}
		if v.eventRetSel != nil {
			if enabled {
				v.eventRetSel.Enable()
			} else {
				v.eventRetSel.Disable()
			}
		}
		if v.chatScaleSel != nil {
			if enabled {
				v.chatScaleSel.Enable()
			} else {
				v.chatScaleSel.Disable()
			}
		}
		if v.chatScaleCustomEntry != nil {
			if enabled {
				v.chatScaleCustomEntry.Enable()
			} else {
				v.chatScaleCustomEntry.Disable()
			}
		}
		if v.pruneBtn != nil {
			if enabled {
				v.pruneBtn.Enable()
			} else {
				v.pruneBtn.Disable()
			}
		}
		if v.disabledBanner != nil {
			if enabled {
				v.disabledBanner.Hide()
			} else {
				v.disabledBanner.Show()
			}
		}
		if v.liveTailCheck != nil {
			if enabled {
				v.liveTailCheck.Enable()
			} else {
				v.liveTailCheck.Disable()
			}
		}
		if v.addLiveLogBtn != nil {
			if enabled {
				v.addLiveLogBtn.Enable()
			} else {
				v.addLiveLogBtn.Disable()
			}
		}
		v.refreshLiveLogCards()
	})
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

	v.hideBotsCheck = widget.NewCheck("🤖 Hide Bots", func(checked bool) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.hideBots = checked
		v.currentPage = 0
		v.mu.Unlock()
		v.RefreshQuery()
	})
	v.hideBotsCheck.SetChecked(v.hideBots)

	v.showSummaryCheck = widget.NewCheck("📊 Show Summary", func(checked bool) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.showSummary = checked
		v.mu.Unlock()
		v.renderEntries()
	})
	v.showSummaryCheck.SetChecked(v.showSummary)

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

	v.sessionMap = map[string]int64{
		sessionOptionAll:    sessionIDAll,
		sessionOptionLatest: sessionIDLatest,
		sessionOptionMulti:  sessionIDMulti,
	}
	v.sessionSel = widget.NewSelect([]string{sessionOptionAll, sessionOptionLatest, sessionOptionMulti}, func(selected string) {
		if v.initializing {
			return
		}
		sid := v.sessionMap[selected]
		if sid == sessionIDMulti {
			v.showMultiSessionDialog()
			return
		}
		v.mu.Lock()
		v.selectedSessionID = sid
		v.selectedSessionIDs = nil
		v.currentPage = 0
		v.mu.Unlock()
		if v.multiSessionBtn != nil {
			v.multiSessionBtn.SetText("☑")
		}
		v.RefreshQuery()
	})
	v.sessionSel.SetSelected(sessionOptionAll)

	v.multiSessionBtn = widget.NewButton("☑", func() {
		v.showMultiSessionDialog()
	})

	v.platformSel = widget.NewSelect([]string{platformOptionAll, "Twitch", "YouTube", "TikTok", "Kick"}, func(selected string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.selectedPlatform = normalizePlatformOption(selected)
		v.currentPage = 0
		v.mu.Unlock()
		v.RefreshQuery()
	})
	v.platformSel.SetSelected(platformOptionAll)

	v.eventTypeSel = widget.NewSelect([]string{
		eventTypeAll,
		eventTypeChatOnly,
		eventTypeAlertsOnly,
		eventTypeFollows,
		eventTypeSubs,
		eventTypeCheers,
		eventTypeRaids,
		eventTypeRedemptions,
		eventTypeModeration,
	}, func(_ string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.currentPage = 0
		v.mu.Unlock()
		v.RefreshQuery()
	})
	v.eventTypeSel.SetSelected(eventTypeAll)

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
		v.mu.Lock()
		v.currentPage = 0
		v.mu.Unlock()
		v.RefreshQuery()
	})
	v.outcomeSel.SetSelected(outcomeLabelAll)

	v.userSearchEntry = NewScrollPassEntry()
	v.userSearchEntry.SetPlaceHolder("Username, display name, or unique user ID...")
	v.userSearchEntry.OnChanged = func(_ string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.currentPage = 0
		v.mu.Unlock()
		v.scheduleSearch()
	}

	v.textSearchEntry = NewScrollPassEntry()
	v.textSearchEntry.SetPlaceHolder("Search message or spoken text...")
	v.textSearchEntry.OnChanged = func(_ string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		v.currentPage = 0
		v.mu.Unlock()
		v.scheduleSearch()
	}

	clearAllBtn := widget.NewButtonWithIcon("Clear All", theme.ContentClearIcon(), func() {
		v.ResetFilters()
	})

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshSessionDropdown()
		v.RefreshQuery()
	})

	topRow := container.NewHBox(
		v.liveTailCheck,
		v.hideBotsCheck,
		v.showSummaryCheck,
		widget.NewSeparator(),
		newFilterFieldLabel("Date Range:"),
		newBorderedControl(v.datePresetSel),
		newBorderedControl(v.fromDateBtn),
		newBorderedControl(v.toDateBtn),
		clearAllBtn,
		refreshBtn,
	)

	sessionControls := container.NewBorder(nil, nil, nil, newBorderedControl(v.multiSessionBtn), newBorderedControl(v.sessionSel))

	midRow := container.NewGridWithColumns(4,
		container.NewBorder(nil, nil, newFilterFieldLabel("Session:"), nil, sessionControls),
		container.NewBorder(nil, nil, newFilterFieldLabel("Platform:"), nil, newBorderedControl(v.platformSel)),
		container.NewBorder(nil, nil, newFilterFieldLabel("Type:"), nil, newBorderedControl(v.eventTypeSel)),
		container.NewBorder(nil, nil, newFilterFieldLabel("Status:"), nil, newBorderedControl(v.outcomeSel)),
	)

	searchRow := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, newFilterFieldLabel("User / ID:"), nil, v.userSearchEntry),
		container.NewBorder(nil, nil, newFilterFieldLabel("Message:"), nil, v.textSearchEntry),
	)

	drawerBg := canvas.NewRectangle(colorSlateSurface)
	bottomBorder := canvas.NewRectangle(colorSlateBorder)
	bottomBorder.SetMinSize(fyne.NewSize(1, 1))
	return container.NewStack(
		drawerBg,
		container.NewBorder(nil, bottomBorder, nil, nil, container.NewPadded(container.NewVBox(topRow, midRow, searchRow))),
	)
}

func (v *LogsView) refreshSessionDropdown() {
	if v.sessionSel == nil {
		return
	}
	var sessions []eventlog.StreamSession
	if v.session != nil && v.session.EventLogger() != nil {
		sList, err := v.session.EventLogger().ListSessions(context.Background(), 25)
		if err == nil {
			sessions = sList
		}
	}

	options := []string{sessionOptionAll, sessionOptionLatest, sessionOptionMulti}
	newMap := map[string]int64{
		sessionOptionAll:    sessionIDAll,
		sessionOptionLatest: sessionIDLatest,
		sessionOptionMulti:  sessionIDMulti,
	}

	activeSID := int64(0)
	if v.session != nil {
		activeSID = v.session.ActiveSessionID()
	}

	latestSID := activeSID
	if latestSID <= 0 && len(sessions) > 0 {
		latestSID = sessions[0].ID
	}
	v.mu.Lock()
	v.latestSessionID = latestSID
	multiCount := len(v.selectedSessionIDs)
	v.mu.Unlock()

	for _, s := range sessions {
		durStr := ""
		endStr := "LIVE"
		if !s.EndedAt.IsZero() {
			endStr = s.EndedAt.Local().Format("15:04")
			dur := s.EndedAt.Sub(s.StartedAt).Round(time.Minute)
			durStr = fmt.Sprintf(" (%s)", dur)
		} else if s.ID == activeSID {
			endStr = "LIVE NOW"
		}
		platStr := strings.Join(s.Platforms, "/")
		if platStr == "" {
			platStr = "Stream"
		}
		label := fmt.Sprintf("#%d: %s %s–%s%s", s.ID, platStr, s.StartedAt.Local().Format("01/02 15:04"), endStr, durStr)
		if s.ID == activeSID {
			label = "🟢 " + label
		}
		options = append(options, label)
		newMap[label] = s.ID
	}

	v.sessionMap = newMap
	v.sessionSel.Options = options
	selectedLabel := sessionOptionAll
	if multiCount > 0 {
		selectedLabel = sessionOptionMulti
	} else {
		for lbl, id := range v.sessionMap {
			if id == v.selectedSessionID {
				selectedLabel = lbl
				break
			}
		}
	}
	v.initializing = true
	v.sessionSel.SetSelected(selectedLabel)
	v.initializing = false
	v.sessionSel.Refresh()
}

// SetSelectedSessions configures the search filter to match one or more specific session IDs.
func (v *LogsView) SetSelectedSessions(ids []int64) {
	var clean []int64
	for _, id := range ids {
		if id > 0 && !slices.Contains(clean, id) {
			clean = append(clean, id)
		}
	}
	v.mu.Lock()
	switch len(clean) {
	case 0:
		v.selectedSessionIDs = nil
		v.selectedSessionID = sessionIDAll
	case 1:
		v.selectedSessionIDs = nil
		v.selectedSessionID = clean[0]
	default:
		v.selectedSessionIDs = clean
		v.selectedSessionID = sessionIDMulti
	}
	v.currentPage = 0
	v.mu.Unlock()

	if v.multiSessionBtn != nil {
		if len(clean) > 1 {
			v.multiSessionBtn.SetText(fmt.Sprintf("☑ (%d)", len(clean)))
		} else {
			v.multiSessionBtn.SetText("☑")
		}
	}
	v.refreshSessionDropdown()
	v.RefreshQuery()
}

func (v *LogsView) showMultiSessionDialog() {
	var sessions []eventlog.StreamSession
	if v.session != nil && v.session.EventLogger() != nil {
		if sList, err := v.session.EventLogger().ListSessions(context.Background(), 50); err == nil {
			sessions = sList
		}
	}

	activeSID := int64(0)
	if v.session != nil {
		activeSID = v.session.ActiveSessionID()
	}

	optLabels := make([]string, 0, len(sessions))
	labelToID := make(map[string]int64, len(sessions))
	var preSelected []string

	v.mu.Lock()
	currentMulti := slices.Clone(v.selectedSessionIDs)
	currentSingle := v.selectedSessionID
	v.mu.Unlock()

	for _, s := range sessions {
		endStr := "LIVE"
		durStr := ""
		if !s.EndedAt.IsZero() {
			endStr = s.EndedAt.Local().Format("15:04")
			dur := s.EndedAt.Sub(s.StartedAt).Round(time.Minute)
			durStr = fmt.Sprintf(" (%s)", dur)
		} else if s.ID == activeSID {
			endStr = "LIVE NOW"
		}
		platStr := strings.Join(s.Platforms, "/")
		if platStr == "" {
			platStr = "Stream"
		}
		label := fmt.Sprintf("#%d: %s %s–%s%s", s.ID, platStr, s.StartedAt.Local().Format("01/02 15:04"), endStr, durStr)
		if s.ID == activeSID {
			label = "🟢 " + label
		}
		optLabels = append(optLabels, label)
		labelToID[label] = s.ID
		if slices.Contains(currentMulti, s.ID) || (len(currentMulti) == 0 && currentSingle == s.ID) {
			preSelected = append(preSelected, label)
		}
	}

	checkGroup := widget.NewCheckGroup(optLabels, nil)
	checkGroup.SetSelected(preSelected)

	selectAllBtn := widget.NewButton("Select All", func() {
		checkGroup.SetSelected(optLabels)
	})
	clearBtn := widget.NewButton("Clear", func() {
		checkGroup.SetSelected(nil)
	})

	scrollBox := container.NewVScroll(checkGroup)
	scrollBox.SetMinSize(fyne.NewSize(420, 260))

	content := container.NewBorder(
		container.NewHBox(widget.NewLabel("Choose one or more stream sessions:"), selectAllBtn, clearBtn),
		nil,
		nil,
		nil,
		scrollBox,
	)

	dialog.ShowCustomConfirm("Select Stream Sessions", "Apply", "Cancel", content, func(ok bool) {
		if !ok {
			v.refreshSessionDropdown()
			return
		}
		var chosen []int64
		for _, lbl := range checkGroup.Selected {
			if id, exists := labelToID[lbl]; exists && id > 0 {
				chosen = append(chosen, id)
			}
		}
		v.SetSelectedSessions(chosen)
	}, v.window)
}

func (v *LogsView) resolveSessionID(selectedID int64) int64 {
	if selectedID != sessionIDLatest {
		return selectedID
	}
	if v.session != nil {
		if active := v.session.ActiveSessionID(); active > 0 {
			return active
		}
	}
	v.mu.Lock()
	latest := v.latestSessionID
	v.mu.Unlock()
	if latest > 0 {
		return latest
	}
	if v.session != nil && v.session.EventLogger() != nil {
		if sList, err := v.session.EventLogger().ListSessions(context.Background(), 1); err == nil && len(sList) > 0 {
			v.mu.Lock()
			v.latestSessionID = sList[0].ID
			v.mu.Unlock()
			return sList[0].ID
		}
	}
	return 0
}

func (v *LogsView) buildPaginationBar() *fyne.Container {
	v.filterToggleBtn = widget.NewButton("▾ Filters & Live Tail", func() {
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

	cols := container.New(logTableLayout{getWidth: v.currentTableWidth, getScale: v.chatScaleFactor},
		colHdr("Time", fyne.TextAlignLeading),
		colHdr("Svc", fyne.TextAlignCenter),
		colHdr("User", fyne.TextAlignLeading),
		colHdr("Message", fyne.TextAlignLeading),
		colHdr("TTS Status", fyne.TextAlignCenter),
		colHdr("Mod Controls", fyne.TextAlignCenter),
	)

	return container.NewStack(
		headerBg,
		container.NewBorder(nil, bottomBorder, nil, nil, cols),
	)
}

// ResetFilters clears all active search filters and restores Live Tail mode.
func (v *LogsView) ResetFilters() {
	v.initializing = true
	v.selectedSessionID = sessionIDAll
	v.selectedSessionIDs = nil
	if v.sessionSel != nil {
		v.sessionSel.SetSelected(sessionOptionAll)
	}
	if v.multiSessionBtn != nil {
		v.multiSessionBtn.SetText("☑")
	}
	v.setDateRange(nil, nil)
	if v.datePresetSel != nil {
		v.datePresetSel.SetSelected(datePresetAllTime)
	}
	if v.platformSel != nil {
		v.platformSel.SetSelected(platformOptionAll)
	}
	if v.eventTypeSel != nil {
		v.eventTypeSel.SetSelected(eventTypeAll)
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
	if v.hideBotsCheck != nil {
		v.hideBotsCheck.SetChecked(true)
	}
	if v.showSummaryCheck != nil {
		v.showSummaryCheck.SetChecked(true)
	}
	v.mu.Lock()
	v.selectedPlatform = ""
	v.liveTail = true
	v.hideBots = true
	v.showSummary = true
	v.currentPage = 0
	v.mu.Unlock()
	v.initializing = false

	v.RefreshQuery()
}

func normalizePlatformOption(opt string) string {
	clean := strings.ToLower(strings.TrimSpace(opt))
	switch clean {
	case "", "all", strings.ToLower(platformOptionAll):
		return ""
	default:
		return clean
	}
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
			v.fromDateBtn.SetText("From: " + from.Format("2006-01-02 15:04:05"))
		}
	}
	if v.toDateBtn != nil {
		if to == nil || to.IsZero() {
			v.toDateBtn.SetText("To: Any")
		} else {
			v.toDateBtn.SetText("To: " + to.Format("2006-01-02 15:04:05"))
		}
	}
}

func (v *LogsView) currentFilter() eventlog.QueryFilter {
	v.mu.Lock()
	from := v.fromDate
	to := v.toDate
	page := v.currentPage
	hideBots := v.hideBots
	selectedSession := v.selectedSessionID
	multiSessions := slices.Clone(v.selectedSessionIDs)
	plat := v.selectedPlatform
	v.mu.Unlock()

	sessionID := int64(0)
	if len(multiSessions) == 0 {
		sessionID = v.resolveSessionID(selectedSession)
	}

	if v.platformSel != nil {
		plat = normalizePlatformOption(v.platformSel.Selected)
	}

	kindFilter := ""
	if v.eventTypeSel != nil {
		switch v.eventTypeSel.Selected {
		case eventTypeChatOnly:
			kindFilter = "chat"
		case eventTypeAlertsOnly:
			kindFilter = "alerts"
		case eventTypeFollows:
			kindFilter = "follow"
		case eventTypeSubs:
			kindFilter = "sub"
		case eventTypeCheers:
			kindFilter = "cheer"
		case eventTypeRaids:
			kindFilter = "raid"
		case eventTypeRedemptions:
			kindFilter = "redemption"
		case eventTypeModeration:
			kindFilter = "moderation"
		}
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
		userQ = strings.TrimSpace(v.userSearchEntry.GetText())
	}
	textQ := ""
	if v.textSearchEntry != nil {
		textQ = strings.TrimSpace(v.textSearchEntry.GetText())
	}

	var excludedUsers []string
	if hideBots {
		var cfg *config.Config
		if v.session != nil {
			cfg = v.session.Config()
		}
		excludedUsers = bots.ExcludedUserNames(cfg)
	}

	return eventlog.QueryFilter{
		SessionID:     sessionID,
		SessionIDs:    multiSessions,
		From:          from,
		To:            to,
		Platform:      plat,
		KindFilter:    kindFilter,
		UserQuery:     userQ,
		TextQuery:     textQ,
		OutcomeMode:   mode,
		HideBots:      hideBots,
		ExcludedUsers: excludedUsers,
		Limit:         logsPageSize,
		Offset:        page * logsPageSize,
		SkipSummary:   page > 0,
	}
}

// scheduleSearch debounces rapid keystrokes in the user and message search fields
// before running an asynchronous database query off the UI thread.
func (v *LogsView) scheduleSearch() {
	v.mu.Lock()
	if v.searchTimer != nil {
		v.searchTimer.Stop()
	}
	if v.queryCancel != nil {
		v.queryCancel()
		v.queryCancel = nil
	}
	v.searchTimer = time.AfterFunc(searchDebounceDuration, func() {
		v.runAsyncQuery()
	})
	v.mu.Unlock()
}

func (v *LogsView) runAsyncQuery() {
	filter := v.currentFilter()

	v.mu.Lock()
	if v.queryCancel != nil {
		v.queryCancel()
	}
	v.querySeq++
	seq := v.querySeq
	ctx, cancel := context.WithCancel(context.Background())
	v.queryCancel = cancel
	v.mu.Unlock()

	defer cancel()

	var res *eventlog.QueryResult
	if v.session != nil && v.session.EventLogger() != nil {
		r, err := v.session.EventLogger().Query(ctx, filter)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("LogsView async query failed (platform=%q, session=%d): %v", filter.Platform, filter.SessionID, err)
			}
			return
		}
		res = r
	}
	if res == nil {
		res = &eventlog.QueryResult{}
	}

	v.mu.Lock()
	if ctx.Err() != nil || v.querySeq != seq {
		v.mu.Unlock()
		return
	}
	v.entries = res.Entries
	v.totalCount = res.TotalCount
	if !filter.SkipSummary {
		v.summary = res.Summary
	}
	v.mu.Unlock()

	fyne.Do(func() {
		v.renderEntries()
	})
}

// RefreshQuery executes the current QueryFilter against the session's EventLogger and updates the UI.
func (v *LogsView) RefreshQuery() {
	v.mu.Lock()
	if v.searchTimer != nil {
		v.searchTimer.Stop()
		v.searchTimer = nil
	}
	if v.renderTimer != nil {
		v.renderTimer.Stop()
		v.renderTimer = nil
	}
	if v.queryCancel != nil {
		v.queryCancel()
		v.queryCancel = nil
	}
	v.querySeq++
	v.mu.Unlock()

	filter := v.currentFilter()

	var res *eventlog.QueryResult
	if v.session != nil && v.session.EventLogger() != nil {
		r, err := v.session.EventLogger().Query(context.Background(), filter)
		if err != nil {
			log.Printf("LogsView query failed (platform=%q, session=%d): %v", filter.Platform, filter.SessionID, err)
		} else if r != nil {
			res = r
		}
	}
	if res == nil {
		res = &eventlog.QueryResult{}
	}

	v.mu.Lock()
	v.entries = res.Entries
	v.totalCount = res.TotalCount
	if !filter.SkipSummary {
		v.summary = res.Summary
	}
	v.mu.Unlock()

	fyne.Do(func() {
		v.renderEntries()
	})
}

func (v *LogsView) subscribeLiveTail() {
	v.Close()
	v.mu.Lock()
	v.closed = false
	v.mu.Unlock()
	if v.session == nil || v.session.EventLogger() == nil {
		return
	}

	unsub := v.session.EventLogger().Subscribe(func(ev eventlog.LiveUpdate) {
		if ev.Kind == eventlog.LiveUpdateAppend && ev.Entry != nil && ev.Entry.SessionID > 0 {
			v.mu.Lock()
			prevLatest := v.latestSessionID
			selectedMode := v.selectedSessionID
			if ev.Entry.SessionID > v.latestSessionID {
				v.latestSessionID = ev.Entry.SessionID
			}
			newLatest := v.latestSessionID
			v.mu.Unlock()

			if newLatest != prevLatest {
				fyne.Do(func() {
					v.refreshSessionDropdown()
				})
				if selectedMode == sessionIDLatest {
					v.RefreshQuery()
					return
				}
			}
		}

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
			if !filter.SkipSummary {
				v.summary.AddEntry(*ev.Entry)
			}
			v.mu.Unlock()

			v.scheduleLiveRender()

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
				v.scheduleLiveRender()
			} else if filter.OutcomeMode == eventlog.FilterOutcomeSpokenOnly || filter.OutcomeMode == eventlog.FilterOutcomeDroppedQueue {
				v.RefreshQuery()
			}
		}
	})

	v.mu.Lock()
	v.unsubLive = unsub
	v.mu.Unlock()

	if v.session != nil {
		unsubHeld := v.session.SubscribeHeldMessages(func() {
			fyne.Do(func() {
				v.renderHeldQueue()
			})
		})
		v.mu.Lock()
		v.unsubHeld = unsubHeld
		v.mu.Unlock()
	}
}

func entryMatchesFilter(e eventlog.Entry, f eventlog.QueryFilter) bool {
	if len(f.SessionIDs) > 0 {
		if !slices.Contains(f.SessionIDs, e.SessionID) {
			return false
		}
	} else if f.SessionID > 0 && e.SessionID != f.SessionID {
		return false
	}
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
	kindLower := strings.ToLower(strings.TrimSpace(e.Kind))
	switch strings.ToLower(strings.TrimSpace(f.KindFilter)) {
	case "", "all":
	case "chat", "message":
		if kindLower != "message" {
			return false
		}
	case "alerts":
		if kindLower == "message" || kindLower == "moderation" || kindLower == "" {
			return false
		}
	case "follow":
		if kindLower != "follow" {
			return false
		}
	case "sub":
		switch kindLower {
		case "sub", "resub", "gift_sub", "gift_redemption", "sub_upgrade", "membership", "gift_membership":
		default:
			return false
		}
	case "cheer":
		switch kindLower {
		case "cheer", "bits_badge_tier", "gift", "superchat", "supersticker", "super_chat", "super_sticker", "charity_donation":
		default:
			return false
		}
	case "raid":
		if kindLower != "raid" {
			return false
		}
	case "redemption":
		if kindLower != "redemption" {
			return false
		}
	case "moderation":
		if kindLower != "moderation" {
			return false
		}
	default:
		if !strings.EqualFold(kindLower, strings.TrimSpace(f.KindFilter)) {
			return false
		}
	}
	if f.HideBots && f.Outcome != eventlog.OutcomeDroppedBot {
		if e.Outcome == eventlog.OutcomeDroppedBot {
			return false
		}
		for _, r := range e.Roles {
			if strings.EqualFold(strings.TrimSpace(r), "bot") {
				return false
			}
		}
		uName := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e.UserName), "@"))
		uID := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e.UserID), "@"))
		for _, ex := range f.ExcludedUsers {
			exClean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ex), "@"))
			if exClean != "" && (exClean == uName || (uID != "" && exClean == uID)) {
				return false
			}
		}
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
			!strings.Contains(strings.ToLower(e.SpokenText), tq) &&
			!strings.Contains(strings.ToLower(e.ItemName), tq) &&
			!strings.Contains(strings.ToLower(e.RewardTitle), tq) &&
			!strings.Contains(strings.ToLower(e.Recipient), tq) {
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
	uiRenderMu.Lock()
	defer func() {
		v.mu.Lock()
		v.mu.Unlock() //nolint:staticcheck // establishes happens-before edge after Fyne cache updates
		uiRenderMu.Unlock()
	}()

	if v.listBox == nil {
		return
	}

	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.renderCount++
	entries := make([]eventlog.Entry, len(v.entries))
	copy(entries, v.entries)
	total := v.totalCount
	page := v.currentPage
	showSum := v.showSummary
	summary := v.summary.Clone()
	v.mu.Unlock()

	v.renderHeldQueueLocked()

	if v.summaryLabel != nil {
		if total == 0 || len(entries) == 0 {
			v.summaryLabel.SetText("0 matching events")
		} else {
			startIdx := page*logsPageSize + 1
			endIdx := startIdx + len(entries) - 1
			v.summaryLabel.SetText(fmt.Sprintf("Showing %d–%d of %d matching events", startIdx, endIdx, total))
		}
	}

	if v.summaryTotalsLabel != nil {
		txt := formatSummaryTotals(summary)
		if showSum && txt != "" {
			v.summaryTotalsLabel.SetText(txt)
			if v.summaryTotalsItemsBox != nil {
				v.summaryTotalsItemsBox.Objects = v.buildSummaryTotalsObjects(summary)
				v.summaryTotalsItemsBox.Refresh()
			}
			if v.summaryTotalsContainer != nil {
				v.summaryTotalsContainer.Show()
				v.summaryTotalsContainer.Refresh()
			}
		} else {
			v.summaryTotalsLabel.SetText("")
			if v.summaryTotalsItemsBox != nil {
				v.summaryTotalsItemsBox.Objects = nil
				v.summaryTotalsItemsBox.Refresh()
			}
			if v.summaryTotalsContainer != nil {
				v.summaryTotalsContainer.Hide()
				v.summaryTotalsContainer.Refresh()
			}
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
	s := v.chatScaleFactor()
	iconSz := fyne.NewSize(20*s, 20*s)

	// [0] Time column (HH:MM:SS, never truncated; date is shown on date separator banners)
	localTS := e.CreatedAt.Local().Format("15:04:05")
	timeTip := fmt.Sprintf("Time: %s Local (%s UTC)",
		e.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		e.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	timeCell := v.newTimeCellLabel(localTS, timeTip)

	// [1] Service icon column
	platRes, platTip := platformIconAndTip(e.Platform)
	svcCell := v.newHoverIcon(platRes, iconSz, platTip, nil)

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
	if e.Recipient != "" {
		userTip += " • Recipient: @" + e.Recipient
	}
	if e.ItemName != "" {
		userTip += " • Item: " + e.ItemName
		if e.UnitAmount > 0 {
			userTip += fmt.Sprintf(" (%.0f each)", e.UnitAmount)
		}
	}
	if e.SubTier != "" {
		userTip += " • Tier: " + e.SubTier
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

	// [3] Message column (expanding flex, word-wrapped with inline emote support)
	msgTip := ""
	if e.SpokenText != "" && !strings.EqualFold(e.SpokenText, e.RawText) {
		msgTip = "Spoken as: " + e.SpokenText
	}
	var msgCell fyne.CanvasObject
	if len(e.Emotes) > 0 {
		ec := newScaledEmoteMessageCell(v.emoteCache, s, v.showTooltipForObject)
		ec.SetContent(e.Platform, e.RawText, e.Emotes, msgTip, "")
		msgCell = ec
	} else {
		msgLine := strings.ReplaceAll(strings.TrimSpace(e.RawText), "\n", " ")
		if msgLine == "" {
			msgLine = fmt.Sprintf("(%s event)", e.Kind)
		}
		msgCell = v.newWrappedCellLabel(msgLine, msgTip, fyne.TextStyle{})
	}

	// [4] Collapsed TTS Status icon column ([Filter] [Spoken] [Voice])
	ttsStatusCell := v.ttsStatusIcons(e)

	// [5] Moderation Controls icon column ([Delete] [Timeout] [Ban] [Mute TTS])
	modControlsCell := v.modControlIcons(e)

	rowCols := container.New(logTableLayout{getWidth: v.currentTableWidth, getScale: v.chatScaleFactor},
		timeCell,
		svcCell,
		userCell,
		msgCell,
		ttsStatusCell,
		modControlsCell,
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
	dash.SetOnAssignVoice(func(p, u string) {
		if v.onAssignVoice != nil {
			v.onAssignVoice(p, u)
		}
		v.RefreshQuery()
	})
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

	timeEntry := widget.NewEntry()
	timeEntry.SetPlaceHolder("HH:MM:SS")
	switch {
	case initial != nil && !initial.IsZero():
		timeEntry.SetText(initial.Local().Format("15:04:05"))
	case endOfDay:
		timeEntry.SetText("23:59:59")
	default:
		timeEntry.SetText("00:00:00")
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
	nowBtn := widget.NewButton("Now", func() {
		viewDate = time.Now()
		dateEntry.SetText(viewDate.Format("2006-01-02"))
		timeEntry.SetText(viewDate.Format("15:04:05"))
		renderCalendar()
	})
	clearBtn := widget.NewButton("Clear (Any)", func() {
		dateEntry.SetText("")
		timeEntry.SetText("")
		renderCalendar()
	})

	renderCalendar()

	navRow := container.NewBorder(nil, nil, prevMonthBtn, nextMonthBtn, monthLabel)
	entryRow := container.NewBorder(nil, nil, widget.NewLabel("Date (YYYY-MM-DD):"), container.NewHBox(todayBtn, nowBtn, clearBtn), dateEntry)
	timeRow := container.NewBorder(nil, nil, widget.NewLabel("Time (HH:MM:SS):"), nil, timeEntry)

	content := container.NewVBox(
		navRow,
		gridBox,
		widget.NewSeparator(),
		entryRow,
		timeRow,
	)

	dialog.ShowCustomConfirm(title, "Apply", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		dTxt := strings.TrimSpace(dateEntry.Text)
		if dTxt == "" {
			onSelect(nil)
			return
		}
		tTxt := strings.TrimSpace(timeEntry.Text)
		if tTxt == "" {
			if endOfDay {
				tTxt = "23:59:59"
			} else {
				tTxt = "00:00:00"
			}
		}

		combined := dTxt + " " + tTxt
		parsed, err := time.ParseInLocation("2006-01-02 15:04:05", combined, now.Location())
		if err != nil {
			parsed, err = time.ParseInLocation("2006-01-02 15:04", combined, now.Location())
			if err != nil {
				parsed, err = time.ParseInLocation("2006-01-02", dTxt, now.Location())
				if err != nil {
					v.setStatus("⚠️ Invalid date/time format. Use YYYY-MM-DD and HH:MM:SS.")
					return
				}
				if endOfDay {
					parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, now.Location())
				}
			}
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
