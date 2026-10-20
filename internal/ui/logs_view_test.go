package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestLogsViewLiveTailAndSearch(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestLogsView")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "events.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite backend: %v", err)
	}

	cfg := config.DefaultConfig()
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, backend)
	defer func() { _ = mgr.Close() }()

	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	sess.SetEventLogger(mgr)

	var lastStatus string
	lv := NewLogsView(w, sess)
	lv.SetStatusHandler(func(msg string) {
		lastStatus = msg
	})
	defer lv.Close()

	obj := lv.Build()
	if obj == nil {
		t.Fatal("expected non-nil CanvasObject from LogsView.Build()")
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Append an event while Live Tail is active with roles
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:            "log-1",
		CreatedAt:     now.Add(-2 * time.Minute),
		Platform:      "twitch",
		Channel:       "streamer_one",
		Kind:          "chat",
		UserID:        "u100",
		UserName:      "alice",
		DisplayName:   "Alice",
		Roles:         []string{"moderator", "subscriber"},
		RawText:       "Hello world",
		SpokenText:    "Alice says Hello world",
		VoiceID:       "af_heart",
		Speed:         1.0,
		PassedFilters: true,
		Spoken:        false,
		Outcome:       eventlog.OutcomeQueued,
	})
	if err != nil {
		t.Fatalf("Append log-1 failed: %v", err)
	}

	// Update outcome to spoken
	err = mgr.UpdateOutcome(ctx, "log-1", eventlog.OutcomeUpdate{
		Spoken:    true,
		Outcome:   eventlog.OutcomeSpoken,
		UpdatedAt: now.Add(-90 * time.Second),
	})
	if err != nil {
		t.Fatalf("UpdateOutcome log-1 failed: %v", err)
	}

	// Append a catch-up dropped event
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:            "log-2",
		CreatedAt:     now.Add(-1 * time.Minute),
		Platform:      "youtube",
		Channel:       "yt_chan",
		Kind:          "chat",
		UserID:        "u200",
		UserName:      "bob",
		DisplayName:   "Bob",
		Roles:         []string{"vip"},
		RawText:       "Burst message",
		SpokenText:    "Bob says Burst message",
		VoiceID:       "am_adam",
		PassedFilters: true,
		Spoken:        false,
		Outcome:       eventlog.OutcomeDroppedStale,
	})
	if err != nil {
		t.Fatalf("Append log-2 failed: %v", err)
	}

	// Append a bot event and a custom-bot event
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:            "log-bot-1",
		CreatedAt:     now.Add(-30 * time.Second),
		Platform:      "twitch",
		Channel:       "streamer_one",
		Kind:          "chat",
		UserID:        "u999",
		UserName:      "nightbot",
		DisplayName:   "Nightbot",
		Roles:         []string{"bot"},
		RawText:       "Follow the stream!",
		PassedFilters: false,
		Spoken:        false,
		Outcome:       eventlog.OutcomeDroppedBot,
	})
	if err != nil {
		t.Fatalf("Append log-bot-1 failed: %v", err)
	}

	_ = sess.UpdateConfig(func(c *config.Config) {
		c.Bots.CustomBots = []string{"customspammerbot"}
	})
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:            "log-bot-2",
		CreatedAt:     now.Add(-20 * time.Second),
		Platform:      "twitch",
		Channel:       "streamer_one",
		Kind:          "chat",
		UserID:        "u998",
		UserName:      "customspammerbot",
		DisplayName:   "CustomSpammerBot",
		RawText:       "Automated promo",
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})
	if err != nil {
		t.Fatalf("Append log-bot-2 failed: %v", err)
	}

	// Switch off Live Tail to run historical search queries
	lv.liveTailCheck.SetChecked(false)

	// By default, Hide Bots is checked (true), so only Alice and Bob (2 entries) appear
	lv.RefreshQuery()
	lv.mu.Lock()
	totalCount := len(lv.entries)
	lv.mu.Unlock()
	if totalCount != 2 {
		t.Fatalf("expected 2 non-bot entries with Hide Bots checked, got %d", totalCount)
	}

	// Uncheck Hide Bots and verify all 4 entries (including Nightbot and CustomSpammerBot) appear
	lv.hideBotsCheck.SetChecked(false)
	lv.mu.Lock()
	allWithBotsCount := len(lv.entries)
	lv.mu.Unlock()
	if allWithBotsCount != 4 {
		t.Fatalf("expected 4 entries with Hide Bots unchecked, got %d", allWithBotsCount)
	}

	// Re-check Hide Bots
	lv.hideBotsCheck.SetChecked(true)

	// Filter by Dropped in Queue
	lv.outcomeSel.SetSelected(outcomeLabelDroppedQ)
	lv.mu.Lock()
	droppedCount := len(lv.entries)
	lv.mu.Unlock()
	if droppedCount != 1 {
		t.Fatalf("expected 1 entry for Dropped in Queue, got %d", droppedCount)
	}

	// Reset filters and verify both non-bot entries return and Hide Bots is restored to true
	lv.ResetFilters()
	lv.mu.Lock()
	afterResetCount := len(lv.entries)
	lv.mu.Unlock()
	if afterResetCount != 2 {
		t.Fatalf("expected 2 entries after ResetFilters(), got %d", afterResetCount)
	}

	// Filter by Platform: Twitch -> should return only Alice (1 entry)
	lv.platformSel.SetSelected("Twitch")
	lv.mu.Lock()
	twitchCount := len(lv.entries)
	lv.mu.Unlock()
	if twitchCount != 1 {
		t.Fatalf("expected 1 entry for Platform=Twitch, got %d", twitchCount)
	}

	// Filter by Platform: YouTube -> should return only Bob (1 entry)
	lv.platformSel.SetSelected("YouTube")
	lv.mu.Lock()
	ytCount := len(lv.entries)
	lv.mu.Unlock()
	if ytCount != 1 {
		t.Fatalf("expected 1 entry for Platform=YouTube, got %d", ytCount)
	}

	// Filter by Platform: Kick -> should return 0 entries
	lv.platformSel.SetSelected("Kick")
	lv.mu.Lock()
	kickCount := len(lv.entries)
	lv.mu.Unlock()
	if kickCount != 0 {
		t.Fatalf("expected 0 entries for Platform=Kick, got %d", kickCount)
	}

	// Filter by Platform: All Platforms -> should return both Alice and Bob (2 entries)
	lv.platformSel.SetSelected(platformOptionAll)
	lv.mu.Lock()
	allPlatCount := len(lv.entries)
	lv.mu.Unlock()
	if allPlatCount != 2 {
		t.Fatalf("expected 2 entries for Platform=All Platforms, got %d", allPlatCount)
	}

	// Test retention update via dropdown
	lv.chatRetSel.SetSelected("90 Days")
	if sess.Config().Logging.ChatRetentionDays != 90 {
		t.Errorf("expected ChatRetentionDays to update to 90, got %d", sess.Config().Logging.ChatRetentionDays)
	}
	if lastStatus == "" {
		t.Error("expected status message after retention change")
	}
}

func TestLogsTableIconsAndHoverTooltips(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestLogsIcons")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	lv := NewLogsView(w, sess)
	w.SetContent(lv.Build())

	// Verify platform icons & tooltips
	for _, tc := range []struct {
		platform string
		wantSub  string
	}{
		{"twitch", "Twitch"},
		{"youtube", "YouTube"},
		{"tiktok", "TikTok"},
		{"kick", "Kick"},
	} {
		res, tip := platformIconAndTip(tc.platform)
		if res == nil {
			t.Errorf("expected non-nil resource for platform %s", tc.platform)
		}
		if !strings.Contains(tip, tc.wantSub) {
			t.Errorf("unexpected tooltip for %s: %q", tc.platform, tip)
		}
	}

	// Verify role icons (broadcaster, moderator, vip, subscriber, paid event)
	rolesEntry := eventlog.Entry{
		Roles:         []string{"broadcaster", "moderator", "vip", "subscriber"},
		IsPaidOrEvent: true,
		Kind:          "cheer",
		Amount:        500,
	}
	roleIcons := lv.userRoleIcons(rolesEntry)
	if len(roleIcons) != 5 {
		t.Fatalf("expected 5 role/event icons, got %d", len(roleIcons))
	}

	// Verify hover tooltip lifecycle on hoverIconWidget
	icon := lv.newHoverIcon(resSpokenYes, fyne.NewSize(20, 20), "Spoken: Yes (1.00x speed)", nil)
	icon.MouseIn(&desktop.MouseEvent{})
	if lv.hoverInfoLabel.Text != "Spoken: Yes (1.00x speed)" {
		t.Errorf("expected hoverInfoLabel to show tooltip on MouseIn, got %q", lv.hoverInfoLabel.Text)
	}
	if !lv.tooltipBubble.Visible() {
		t.Error("expected tooltipBubble to be visible on MouseIn")
	}

	icon.MouseOut()
	if lv.hoverInfoLabel.Text != "" {
		t.Errorf("expected hoverInfoLabel to clear on MouseOut, got %q", lv.hoverInfoLabel.Text)
	}
	if lv.tooltipBubble.Visible() {
		t.Error("expected tooltipBubble to hide on MouseOut")
	}

	// Verify that a very long message wraps vertically and never expands the table's MinSize width
	baseMinW := lv.listBox.MinSize().Width
	longMsg := strings.Repeat("supercalifragilisticexpialidocious ", 30)
	row := lv.buildLogRow(0, eventlog.Entry{
		CreatedAt:     time.Now(),
		Platform:      "twitch",
		Channel:       "testchannel",
		UserName:      "chatter",
		DisplayName:   "Chatter",
		RawText:       longMsg,
		SpokenText:    "Chatter says " + longMsg,
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})
	if row.MinSize().Width > 900 {
		t.Errorf("expected long message row MinSize().Width <= 900 (clamped), got %v", row.MinSize().Width)
	}
	_ = baseMinW

	// Verify inline emote tokenization and emoteMessageCell rendering
	cache := NewEmoteCache(t.TempDir(), nil)
	kappaURL := "https://static-cdn.jtvnw.net/emoticons/v2/25/static/dark/1.0"
	cache.Put(kappaURL, resPlatformTwitch)
	lv.SetEmoteCache(cache)

	tokens := tokenizeMessageWithEmotes("twitch", "Hello Kappa world!", []chat.EmoteSpan{
		{Start: 6, End: 11, ID: "25", Name: "Kappa"},
	})
	if len(tokens) != 3 || !tokens[1].isEmote || tokens[1].name != "Kappa" || tokens[1].imageURL != kappaURL {
		t.Fatalf("unexpected emote tokens: %+v", tokens)
	}

	emoteRow := lv.buildLogRow(1, eventlog.Entry{
		CreatedAt:     time.Now(),
		Platform:      "twitch",
		Channel:       "testchannel",
		UserName:      "emote_user",
		DisplayName:   "EmoteUser",
		RawText:       "Hello Kappa world!",
		Emotes:        []chat.EmoteSpan{{Start: 6, End: 11, ID: "25", Name: "Kappa"}},
		SpokenText:    "EmoteUser says Hello world!",
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})
	if emoteRow.MinSize().Width > 900 {
		t.Errorf("expected emote message row MinSize().Width <= 900, got %v", emoteRow.MinSize().Width)
	}
}

func TestLogsViewStreamSessionAndTotalsSummary(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestStreamSessionsAndTotals")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions_events.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite backend: %v", err)
	}

	cfg := config.DefaultConfig()
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, backend)
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()
	startTime := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	streamSess, err := backend.StartSession(ctx, startTime, "twitch", "teststreamer")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sid := streamSess.ID
	_ = backend.AddSessionPlatform(ctx, sid, "youtube", "teststreamer")

	// Event 1: in session 1 (Bits cheer with Recipient and UnitAmount)
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:            "sess-ev-1",
		SessionID:     sid,
		CreatedAt:     startTime.Add(15 * time.Minute),
		Platform:      "twitch",
		Channel:       "teststreamer",
		Kind:          "cheer",
		UserID:        "u1",
		UserName:      "bitsfan",
		DisplayName:   "BitsFan",
		Amount:        500,
		Count:         500,
		Currency:      "BITS",
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})

	// Event 2: in session 1 (YouTube Super Sticker with ItemName and UnitAmount)
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:            "sess-ev-2",
		SessionID:     sid,
		CreatedAt:     startTime.Add(30 * time.Minute),
		Platform:      "youtube",
		Channel:       "teststreamer",
		Kind:          string(chat.EventKindSuperSticker),
		UserID:        "u2",
		UserName:      "stickerfan",
		DisplayName:   "StickerFan",
		ItemName:      "Pear Cheering",
		UnitAmount:    5,
		Amount:        5,
		Currency:      "dollars",
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})

	// Event 3: in session 1 (Twitch Gift Sub with Recipient and SubTier)
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:            "sess-ev-3",
		SessionID:     sid,
		CreatedAt:     startTime.Add(45 * time.Minute),
		Platform:      "twitch",
		Channel:       "teststreamer",
		Kind:          string(chat.EventKindGiftSub),
		UserID:        "u3",
		UserName:      "gifter",
		DisplayName:   "Gifter",
		Recipient:     "lucky_viewer",
		SubTier:       "Tier 1",
		Count:         1,
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})

	// Close session 1
	endTime := startTime.Add(2 * time.Hour)
	_ = backend.EndSession(ctx, sid, endTime)

	// Event 4: outside session 1 (SessionID = 0)
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:            "out-ev-1",
		SessionID:     0,
		CreatedAt:     endTime.Add(1 * time.Hour),
		Platform:      "tiktok",
		Channel:       "teststreamer",
		Kind:          string(chat.EventKindGift),
		UserID:        "u4",
		UserName:      "ttfan",
		DisplayName:   "TTFan",
		ItemName:      "Heart Me",
		UnitAmount:    1,
		Amount:        10,
		Count:         10,
		Currency:      "Diamonds",
		PassedFilters: true,
		Spoken:        true,
		Outcome:       eventlog.OutcomeSpoken,
	})

	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	sess.SetEventLogger(mgr)

	lv := NewLogsView(w, sess)
	defer lv.Close()
	_ = lv.Build()

	// 1. Initial All Sessions query: should return all 4 events
	lv.liveTailCheck.SetChecked(false)
	lv.RefreshQuery()
	lv.mu.Lock()
	countAll := len(lv.entries)
	totalBits := lv.summary.TotalBits
	lv.mu.Unlock()
	if countAll != 4 {
		t.Fatalf("expected 4 entries for All Sessions, got %d", countAll)
	}
	if totalBits != 500 {
		t.Errorf("expected 500 bits in All Sessions summary, got %v", totalBits)
	}

	// 2. Verify Stream Session dropdown options
	if len(lv.sessionSel.Options) < 2 {
		t.Fatalf("expected at least 2 session options ('All Sessions' + Session #1), got %v", lv.sessionSel.Options)
	}

	// 3. Select Session #1 via dropdown
	var sess1Label string
	for lbl, sID := range lv.sessionMap {
		if sID == sid {
			sess1Label = lbl
			break
		}
	}
	if sess1Label == "" {
		t.Fatalf("session #%d not found in sessionMap: %+v", sid, lv.sessionMap)
	}

	lv.sessionSel.SetSelected(sess1Label)
	lv.mu.Lock()
	sessEntries := len(lv.entries)
	sessSummary := lv.summary
	lv.mu.Unlock()
	if sessEntries != 3 {
		t.Fatalf("expected 3 entries filtered by Session #%d, got %d", sid, sessEntries)
	}
	if sessSummary.TotalBits != 500 || sessSummary.TotalGiftSubs != 1 {
		t.Errorf("unexpected session summary: %+v", sessSummary)
	}

	// 4. Verify summaryTotalsLabel rendering
	totalsText := lv.summaryTotalsLabel.Text
	if !strings.Contains(totalsText, "500 Bits") || !strings.Contains(totalsText, "1 Gift Subs") {
		t.Errorf("summaryTotalsLabel does not contain expected metrics: %q", totalsText)
	}

	// 5. Verify row tooltip contains ItemName, Recipient, and SubTier
	rolesEntry := eventlog.Entry{
		Kind:       string(chat.EventKindGiftSub),
		Recipient:  "lucky_viewer",
		SubTier:    "Tier 1",
		ItemName:   "Sub Gift",
		UnitAmount: 1,
	}
	row := lv.buildLogRow(0, rolesEntry)
	if row == nil {
		t.Fatal("expected non-nil row")
	}

	// 6. Test timestamp range query with HH:MM:SS
	fromTime := startTime.Add(20 * time.Minute) // 10:20:00 (excludes ev-1 at 10:15:00)
	toTime := startTime.Add(35 * time.Minute)   // 10:35:00 (includes ev-2 at 10:30:00, excludes ev-3 at 10:45:00)
	lv.setDateRange(&fromTime, &toTime)
	lv.RefreshQuery()
	lv.mu.Lock()
	timeEntries := len(lv.entries)
	lv.mu.Unlock()
	if timeEntries != 1 {
		t.Fatalf("expected 1 entry in timestamp range 10:20:00–10:35:00, got %d", timeEntries)
	}

	// 7. Verify formatSummaryTotals helper
	testSummary := eventlog.QuerySummary{
		TotalBits:       1000,
		TotalDiamonds:   250,
		TotalSubs:       3,
		TotalGiftSubs:   1,
		TotalFollows:    10,
		TotalRaids:      2,
		SuperChatByCurr: map[string]float64{"USD": 50},
	}
	formatted := formatSummaryTotals(testSummary)
	for _, expectedToken := range []string{"1000 Bits", "250 Diamonds", "3 Subs", "1 Gift Subs", "10 Follows", "2 Raids", "50 USD"} {
		if !strings.Contains(formatted, expectedToken) {
			t.Errorf("formatSummaryTotals missing %q: %q", expectedToken, formatted)
		}
	}

	// 8. Verify debounced userSearchEntry and textSearchEntry filtering
	lv.ResetFilters()
	lv.liveTailCheck.SetChecked(false)

	lv.userSearchEntry.SetText("sticker")
	lv.textSearchEntry.SetText("Pear")
	time.Sleep(searchDebounceDuration + 100*time.Millisecond)

	lv.mu.Lock()
	debouncedCount := len(lv.entries)
	var firstID string
	if debouncedCount > 0 {
		firstID = lv.entries[0].ID
	}
	lv.mu.Unlock()
	if debouncedCount != 1 || firstID != "sess-ev-2" {
		t.Fatalf("expected debounced search to match sess-ev-2, got count=%d firstID=%q", debouncedCount, firstID)
	}
}

func TestLogsViewLiveTailSummaryTotalsUpdate(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestLiveTailSummaryTotals")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "live_summary_events.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite backend: %v", err)
	}

	cfg := config.DefaultConfig()
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, backend)
	defer func() { _ = mgr.Close() }()

	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	sess.SetEventLogger(mgr)

	lv := NewLogsView(w, sess)
	defer lv.Close()
	_ = lv.Build()

	// Initial state: 0 events, summary banner should be empty
	if lv.summaryTotalsLabel.Text != "" {
		t.Errorf("expected empty initial summaryTotalsLabel, got %q", lv.summaryTotalsLabel.Text)
	}

	ctx := context.Background()

	// 1. Live stream alert arrives: Twitch Cheer
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:        "live-ev-cheer",
		CreatedAt: time.Now().UTC(),
		Platform:  "twitch",
		Kind:      string(chat.EventKindCheer),
		Amount:    250,
		UserName:  "cheerer",
		Spoken:    true,
		Outcome:   eventlog.OutcomeSpoken,
	})
	if err != nil {
		t.Fatalf("Append cheer: %v", err)
	}

	// 2. Live stream alert arrives: Twitch Sub
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:        "live-ev-sub",
		CreatedAt: time.Now().UTC(),
		Platform:  "twitch",
		Kind:      string(chat.EventKindSub),
		UserName:  "subscriber",
		Spoken:    true,
		Outcome:   eventlog.OutcomeSpoken,
	})
	if err != nil {
		t.Fatalf("Append sub: %v", err)
	}

	// 3. Live stream alert arrives: YouTube Superchat
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:        "live-ev-superchat",
		CreatedAt: time.Now().UTC(),
		Platform:  "youtube",
		Kind:      string(chat.EventKindSuperChat),
		Amount:    15,
		Currency:  "USD",
		UserName:  "ytfan",
		Spoken:    true,
		Outcome:   eventlog.OutcomeSpoken,
	})
	if err != nil {
		t.Fatalf("Append superchat: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	lv.mu.Lock()
	sum := lv.summary.Clone()
	count := len(lv.entries)
	lv.mu.Unlock()

	if count != 3 {
		t.Fatalf("expected 3 entries in live tail, got %d", count)
	}
	if sum.TotalBits != 250 {
		t.Errorf("expected 250 bits in live summary, got %v", sum.TotalBits)
	}
	if sum.TotalSubs != 1 {
		t.Errorf("expected 1 sub in live summary, got %d", sum.TotalSubs)
	}
	if sum.SuperChatByCurr["USD"] != 15 {
		t.Errorf("expected 15 USD in live summary, got %v", sum.SuperChatByCurr["USD"])
	}

	totalsText := lv.summaryTotalsLabel.Text
	if !strings.Contains(totalsText, "250 Bits") || !strings.Contains(totalsText, "1 Subs") || !strings.Contains(totalsText, "15 USD") {
		t.Errorf("summaryTotalsLabel does not reflect live updates: %q", totalsText)
	}
	if !lv.summaryTotalsContainer.Visible() {
		t.Errorf("expected summaryTotalsContainer to be visible after alerts arrived")
	}

	// 4. Regular chat message arrives: should be added to entries but NOT increment alert counts
	err = mgr.Append(ctx, &eventlog.Entry{
		ID:        "live-ev-msg",
		CreatedAt: time.Now().UTC(),
		Platform:  "twitch",
		Kind:      string(chat.EventKindMessage),
		UserName:  "chatter",
		RawText:   "hello stream",
		Spoken:    true,
		Outcome:   eventlog.OutcomeSpoken,
	})
	if err != nil {
		t.Fatalf("Append message: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	lv.mu.Lock()
	sumAfterMsg := lv.summary.Clone()
	countAfterMsg := len(lv.entries)
	lv.mu.Unlock()

	if countAfterMsg != 4 {
		t.Fatalf("expected 4 entries in live tail after message, got %d", countAfterMsg)
	}
	if sumAfterMsg.TotalBits != 250 || sumAfterMsg.TotalSubs != 1 || sumAfterMsg.SuperChatByCurr["USD"] != 15 {
		t.Errorf("regular chat message modified alert summary: %+v", sumAfterMsg)
	}

	// 5. Toggling Show Summary hides/shows summaryTotalsContainer
	lv.showSummaryCheck.SetChecked(false)
	if lv.summaryTotalsContainer.Visible() {
		t.Error("expected summaryTotalsContainer to hide when showSummaryCheck is unchecked")
	}
	lv.showSummaryCheck.SetChecked(true)
	if !lv.summaryTotalsContainer.Visible() {
		t.Error("expected summaryTotalsContainer to show when showSummaryCheck is re-checked")
	}
}

func TestLogsViewLatestSessionAutoSwitchAndThemeContrast(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("TestLatestSession")

	// Verify ObsidianTheme provides distinct input background, input border, and button colors vs card surface
	th := NewObsidianTheme()
	inputBg := th.Color("inputBackground", 0)
	inputBorder := th.Color("inputBorder", 0)
	btnBg := th.Color("button", 0)
	if inputBg == colorSlateSurface {
		t.Errorf("expected inputBackground (%v) to differ from colorSlateSurface (%v)", inputBg, colorSlateSurface)
	}
	if btnBg == colorSlateSurface {
		t.Errorf("expected button surface (%v) to differ from colorSlateSurface (%v)", btnBg, colorSlateSurface)
	}
	if inputBorder != colorInputBorder {
		t.Errorf("expected inputBorder (%v) to match colorInputBorder (%v)", inputBorder, colorInputBorder)
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "latest_session_events.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite backend: %v", err)
	}

	cfg := config.DefaultConfig()
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, backend)
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()
	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s1, err := backend.StartSession(ctx, t1, "twitch", "streamer")
	if err != nil {
		t.Fatalf("StartSession 1: %v", err)
	}
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:        "s1-ev1",
		SessionID: s1.ID,
		CreatedAt: t1.Add(1 * time.Minute),
		Platform:  "twitch",
		Kind:      "message",
		UserName:  "alice",
		RawText:   "In session 1",
		Outcome:   eventlog.OutcomeSpoken,
	})

	sess := app.NewSession(cfg, secrets.NewMemoryStore())
	sess.SetEventLogger(mgr)

	lv := NewLogsView(w, sess)
	defer lv.Close()
	_ = lv.Build()

	// Select "Latest Session" -> should resolve to Session #1
	lv.sessionSel.SetSelected(sessionOptionLatest)
	lv.mu.Lock()
	c1 := len(lv.entries)
	lv.mu.Unlock()
	if c1 != 1 || lv.entries[0].ID != "s1-ev1" {
		t.Fatalf("expected Latest Session to resolve to s1-ev1, got count=%d", c1)
	}

	// Start Session #2 and append an event in Session #2 -> Latest Session should auto-switch to Session #2
	t2 := t1.Add(2 * time.Hour)
	s2, err := backend.StartSession(ctx, t2, "twitch", "streamer")
	if err != nil {
		t.Fatalf("StartSession 2: %v", err)
	}
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:        "s2-ev1",
		SessionID: s2.ID,
		CreatedAt: t2.Add(1 * time.Minute),
		Platform:  "twitch",
		Kind:      "message",
		UserName:  "bob",
		RawText:   "In session 2",
		Outcome:   eventlog.OutcomeSpoken,
	})

	time.Sleep(50 * time.Millisecond)

	lv.mu.Lock()
	c2 := len(lv.entries)
	var firstID string
	if c2 > 0 {
		firstID = lv.entries[0].ID
	}
	lv.mu.Unlock()
	if c2 != 1 || firstID != "s2-ev1" {
		t.Fatalf("expected Latest Session to automatically switch to Session #2 (s2-ev1), got count=%d firstID=%q", c2, firstID)
	}

	// Create Session #3 with an event, then select multiple sessions (Session #1 + Session #2)
	t3 := t2.Add(2 * time.Hour)
	s3, err := backend.StartSession(ctx, t3, "twitch", "streamer")
	if err != nil {
		t.Fatalf("StartSession 3: %v", err)
	}
	_ = mgr.Append(ctx, &eventlog.Entry{
		ID:        "s3-ev1",
		SessionID: s3.ID,
		CreatedAt: t3.Add(1 * time.Minute),
		Platform:  "twitch",
		Kind:      "message",
		UserName:  "charlie",
		RawText:   "In session 3",
		Outcome:   eventlog.OutcomeSpoken,
	})

	lv.SetSelectedSessions([]int64{s1.ID, s2.ID})
	lv.mu.Lock()
	cMulti := len(lv.entries)
	lv.mu.Unlock()
	if cMulti != 2 {
		t.Fatalf("expected 2 entries when filtering by multiple sessions [s1, s2], got %d", cMulti)
	}
	if lv.multiSessionBtn.Text != "☑ (2)" {
		t.Errorf("expected multiSessionBtn label '☑ (2)', got %q", lv.multiSessionBtn.Text)
	}
}




