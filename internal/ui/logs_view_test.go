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

	// Switch off Live Tail to run historical search queries
	lv.liveTailCheck.SetChecked(false)

	lv.RefreshQuery()
	lv.mu.Lock()
	totalCount := len(lv.entries)
	lv.mu.Unlock()
	if totalCount != 2 {
		t.Fatalf("expected 2 entries in unrestricted query, got %d", totalCount)
	}

	// Filter by Dropped in Queue
	lv.outcomeSel.SetSelected(outcomeLabelDroppedQ)
	lv.mu.Lock()
	droppedCount := len(lv.entries)
	lv.mu.Unlock()
	if droppedCount != 1 {
		t.Fatalf("expected 1 entry for Dropped in Queue, got %d", droppedCount)
	}

	// Reset filters and verify both return
	lv.ResetFilters()
	lv.mu.Lock()
	afterResetCount := len(lv.entries)
	lv.mu.Unlock()
	if afterResetCount != 2 {
		t.Fatalf("expected 2 entries after ResetFilters(), got %d", afterResetCount)
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

