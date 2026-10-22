package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/queue"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestSessionLifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	store := secrets.NewMemoryStore()
	session := NewSession(cfg, store)

	if session.IsRunning() {
		t.Error("expected session not to be running initially")
	}

	// Stop when not running
	if err := session.Stop(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning, got %v", err)
	}

	// Start
	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !session.IsRunning() {
		t.Error("expected session to be running")
	}

	// Start again
	if err := session.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("expected ErrAlreadyStarted, got %v", err)
	}

	// Stop
	if err := session.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if session.IsRunning() {
		t.Error("expected session not to be running after stop")
	}

	// Close permanently shuts down queue
	if err := session.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if session.IsRunning() {
		t.Error("expected session not to be running after close")
	}
}

func TestSessionReloadConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "af_bella"
	session := NewSession(cfg, secrets.NewMemoryStore())

	newCfg := cfg.Clone()
	newCfg.GlobalChatRules.DefaultVoice = "am_adam"

	if err := session.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}

	loaded := session.Config()
	if loaded.GlobalChatRules.DefaultVoice != "am_adam" {
		t.Errorf("expected voice 'am_adam', got %q", loaded.GlobalChatRules.DefaultVoice)
	}

	if err := session.ReloadConfig(nil); err == nil {
		t.Error("expected error reloading nil config")
	}
}

func TestSessionUpdateConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "af_bella"
	session := NewSession(cfg, secrets.NewMemoryStore())

	err := session.UpdateConfig(func(c *config.Config) {
		c.GlobalChatRules.DefaultVoice = "am_michael"
		c.GlobalChatRules.SpeakEmotes = false
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	loaded := session.Config()
	if loaded.GlobalChatRules.DefaultVoice != "am_michael" {
		t.Errorf("expected am_michael, got %s", loaded.GlobalChatRules.DefaultVoice)
	}
	if loaded.GlobalChatRules.SpeakEmotes {
		t.Errorf("expected SpeakEmotes to be false")
	}

	if err := session.UpdateConfig(nil); err == nil {
		t.Error("expected error with nil mutator")
	}
}

func TestSessionConcurrency(t *testing.T) {
	session := NewSession(config.DefaultConfig(), secrets.NewMemoryStore())
	_ = session.Start()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = session.IsRunning()
			c := session.Config()
			c.GlobalChatRules.DefaultSpeed = float32(idx)
			_ = session.ReloadConfig(c)
		}(i)
	}
	wg.Wait()
	_ = session.Stop()
}

func TestSessionDispatchEvent(t *testing.T) {
	cfg := config.DefaultConfig()
	session := NewSession(cfg, secrets.NewMemoryStore())

	// Pause queue so we can inspect enqueued items
	session.Queue().Pause()

	// 1. Known bot should be ignored
	botEvent := &chat.Event{
		Platform: "twitch",
		UserName: "nightbot",
		Text:     "I am a bot",
	}
	if err := session.DispatchEvent(botEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 0 {
		t.Errorf("expected bot message to be ignored, depth is %d", session.Queue().Depth())
	}

	// 2. Normal chatter event should be sanitized and enqueued
	chatEvent := &chat.Event{
		ID:          "msg-1",
		Platform:    "twitch",
		UserName:    "alice",
		DisplayName: "Alice",
		Kind:        chat.EventKindMessage,
		Text:        "hello world https://twitch.tv",
	}
	if err := session.DispatchEvent(chatEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 1 {
		t.Fatalf("expected queue depth 1, got %d", session.Queue().Depth())
	}

	// 3. Broadcaster self-chat should be enqueued and spoken (broadcaster is not a bot)
	broadcasterEvent := &chat.Event{
		ID:          "msg-broadcaster",
		Platform:    "twitch",
		UserName:    "mrpoundsign",
		DisplayName: "MrPoundSign",
		Roles:       []string{"broadcaster"},
		Kind:        chat.EventKindMessage,
		Text:        "I am the streamer testing my own voice",
	}
	if err := session.DispatchEvent(broadcasterEvent); err != nil {
		t.Fatalf("DispatchEvent failed for broadcaster: %v", err)
	}
	if session.Queue().Depth() != 2 {
		t.Fatalf("expected queue depth 2 after broadcaster event, got %d", session.Queue().Depth())
	}

	// 4. Paid/Event should be priority 0
	subEvent := &chat.Event{
		ID:          "sub-1",
		Platform:    "twitch",
		UserName:    "bob",
		DisplayName: "Bob",
		Kind:        chat.EventKindSub,
	}
	if err := session.DispatchEvent(subEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 3 {
		t.Fatalf("expected queue depth 3, got %d", session.Queue().Depth())
	}

	// 4. Moderation deletion
	session.DispatchModeration(&chat.ModerationEvent{
		Platform:        "twitch",
		Action:          chat.ModActionDeleteMessage,
		TargetMessageID: "msg-1",
	})
	if session.Queue().Depth() != 2 {
		t.Errorf("expected depth 2 after deleting msg-1, got %d", session.Queue().Depth())
	}

	// 5. Panic mute
	session.Panic()
	if session.Queue().Depth() != 0 {
		t.Errorf("expected depth 0 after Panic, got %d", session.Queue().Depth())
	}
}

func TestSessionTwitchConnector(t *testing.T) {
	cfg := config.DefaultConfig()
	p := cfg.Platforms["twitch"]
	p.Enabled = true
	p.AccountName = "mrpoundsign"
	cfg.Platforms["twitch"] = p

	session := NewSession(cfg, secrets.NewMemoryStore())
	conn := session.TwitchConnector()
	if conn == nil {
		t.Fatal("expected non-nil TwitchConnector")
	}

	_ = session.Start()
	if conn.Channel() != "mrpoundsign" {
		t.Errorf("expected connector channel 'mrpoundsign', got %q", conn.Channel())
	}

	_ = session.Stop()
}

func TestSessionDispatchEventEmotes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.SpeakNames = true
	cfg.GlobalChatRules.SpeakEmotes = false

	session := NewSession(cfg, secrets.NewMemoryStore())
	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = session.Stop() }()

	var receivedItem *queue.Item
	itemCh := make(chan *queue.Item, 10)
	session.Queue().SetOnItemStarted(func(item *queue.Item) {
		itemCh <- item
	})

	// 1. Message with emotes when speakEmotes = false -> emote is stripped, chatter name intact
	event := &chat.Event{
		ID:          "msg-with-emote",
		Platform:    "twitch",
		UserName:    "alice",
		DisplayName: "Alice",
		Kind:        chat.EventKindMessage,
		Text:        "Hello streamer! Kappa",
		EmoteSpans: []chat.EmoteSpan{
			{Start: 16, End: 21, Name: "Kappa"},
		},
	}
	if err := session.DispatchEvent(event); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}

	select {
	case receivedItem = <-itemCh:
		expected := "Alice says: Hello streamer!"
		if receivedItem.Text != expected {
			t.Errorf("expected %q, got %q", expected, receivedItem.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatched item")
	}

	// 2. Pure emote message when speakEmotes = false -> dropped completely
	pureEmoteEvent := &chat.Event{
		ID:          "msg-pure-emote",
		Platform:    "twitch",
		UserName:    "bob",
		DisplayName: "Bob",
		Kind:        chat.EventKindMessage,
		Text:        "Kappa",
		EmoteSpans: []chat.EmoteSpan{
			{Start: 0, End: 5, Name: "Kappa"},
		},
	}
	if err := session.DispatchEvent(pureEmoteEvent); err != nil {
		t.Fatalf("DispatchEvent failed for pure emote: %v", err)
	}

	select {
	case item := <-itemCh:
		t.Fatalf("expected pure emote message to be dropped, but got item: %q", item.Text)
	case <-time.After(100 * time.Millisecond):
		// Expected: nothing enqueued
	}

	// 3. Message with emotes when speakEmotes = true -> emotes preserved
	cfgWithEmotes := cfg.Clone()
	cfgWithEmotes.GlobalChatRules.SpeakEmotes = true
	if err := session.ReloadConfig(cfgWithEmotes); err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}

	eventWithEmotes := &chat.Event{
		ID:          "msg-speak-emotes",
		Platform:    "twitch",
		UserName:    "charlie",
		DisplayName: "Charlie",
		Kind:        chat.EventKindMessage,
		Text:        "Hello Kappa world",
		EmoteSpans: []chat.EmoteSpan{
			{Start: 6, End: 11, Name: "Kappa"},
		},
	}
	if err := session.DispatchEvent(eventWithEmotes); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}

	select {
	case receivedItem = <-itemCh:
		expected := "Charlie says: Hello Kappa world"
		if receivedItem.Text != expected {
			t.Errorf("expected %q, got %q", expected, receivedItem.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatched item with speakEmotes=true")
	}
}

func TestSessionEventLogOutcomes(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	p := cfg.Platforms["twitch"]
	p.AccountName = "mrpoundsign"
	cfg.Platforms["twitch"] = p
	cfg.Queue.MaxAgeSeconds = 1

	sqlStore, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlStore)

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()

	// Pause queue initially so we can test moderation cancel and staleness before resuming
	session.Queue().Pause()

	// 1. Known bot -> PassedFilters = false, Spoken = false, Outcome = dropped_bot
	_ = session.DispatchEvent(&chat.Event{
		ID:       "ev-bot",
		Platform: "twitch",
		UserID:   "999",
		UserName: "nightbot",
		Text:     "bot message",
	})

	// 2. Message that passes filters and will be deleted by moderation while queued
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-mod-del",
		Platform:    "twitch",
		UserID:      "200",
		UserName:    "troll",
		DisplayName: "Troll",
		Text:        "delete me",
	})
	session.DispatchModeration(&chat.ModerationEvent{
		Platform:        "twitch",
		Action:          chat.ModActionDeleteMessage,
		TargetMessageID: "ev-mod-del",
	})

	// 3. Message that passes filters and will be spoken when queue resumes
	doneSpoken := make(chan struct{})
	session.Queue().SetOnItemFinished(func(item *queue.Item) {
		if item != nil && item.ID == "ev-spoken" {
			close(doneSpoken)
		}
	})
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-spoken",
		Platform:    "twitch",
		UserID:      "300",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "hello streamer",
	})

	session.Queue().Resume()

	select {
	case <-doneSpoken:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ev-spoken to finish")
	}

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	byID := make(map[string]eventlog.Entry)
	for _, e := range res.Entries {
		byID[e.ID] = e
	}

	if b, ok := byID["ev-bot"]; !ok || b.PassedFilters || b.Spoken || b.Outcome != eventlog.OutcomeDroppedBot || b.Channel != "mrpoundsign" {
		t.Errorf("unexpected ev-bot log entry: %+v", b)
	}
	if m, ok := byID["ev-mod-del"]; !ok || !m.PassedFilters || m.Spoken || m.Outcome != eventlog.OutcomeDroppedModeration {
		t.Errorf("unexpected ev-mod-del log entry: %+v", m)
	}
	if s, ok := byID["ev-spoken"]; !ok || !s.PassedFilters || !s.Spoken || s.Outcome != eventlog.OutcomeSpoken {
		t.Errorf("unexpected ev-spoken log entry: %+v", s)
	}
}

func TestSessionStreamEventsAndRedemptionFilter(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	p := cfg.Platforms["twitch"]
	p.AccountName = "mrpoundsign"
	p.CustomRewardID = "TTS Message"
	// Even with RoleGated enabled, stream alerts (sub, redemption, etc.) must bypass chat read rules
	p.ReadRules.RoleGated = true
	p.ReadRules.Roles = []string{"broadcaster"}
	cfg.Platforms["twitch"] = p

	sqlStore, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlStore)

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()
	session.Queue().Pause()

	// 1. Follow event (default Enabled: false) -> logged in EventLog with OutcomeDroppedDisabled
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-follow-1",
		Platform:    "twitch",
		Kind:        chat.EventKindFollow,
		UserID:      "10",
		UserName:    "newfan",
		DisplayName: "NewFan",
	})

	// 2. Redemption with non-matching reward -> logged in EventLog with OutcomeDroppedDisabled
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-redemp-other",
		Platform:    "twitch",
		Kind:        chat.EventKindRedemption,
		UserID:      "11",
		UserName:    "viewer1",
		DisplayName: "Viewer1",
		RewardID:    "rew-hydrate",
		RewardTitle: "Hydrate!",
	})

	// 3. Redemption with matching reward title -> passes filters and queues formatted speech
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-redemp-match",
		Platform:    "twitch",
		Kind:        chat.EventKindRedemption,
		UserID:      "12",
		UserName:    "viewer2",
		DisplayName: "Viewer2",
		RewardID:    "rew-tts",
		RewardTitle: "TTS Message",
		Text:        "Hello world",
	})

	// 4. Kick Gift below threshold vs above threshold
	kickCfg := cfg.Platforms["kick"]
	gNotif := kickCfg.Notifications["gift"]
	gNotif.Threshold = 100
	kickCfg.Notifications["gift"] = gNotif
	cfg.Platforms["kick"] = kickCfg
	_ = session.ReloadConfig(cfg)

	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-kick-gift-low",
		Platform:    "kick",
		Kind:        chat.EventKindGift,
		UserID:      "21",
		UserName:    "kickuser1",
		DisplayName: "KickUser1",
		Count:       10,
		Amount:      10,
		RewardTitle: "Kicks",
	})

	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-kick-gift-high",
		Platform:    "kick",
		Kind:        chat.EventKindGift,
		UserID:      "22",
		UserName:    "kickuser2",
		DisplayName: "KickUser2",
		Count:       200,
		Amount:      200,
		RewardTitle: "Sub Bomb",
		Text:        "Hype!",
	})

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	byID := make(map[string]eventlog.Entry)
	for _, e := range res.Entries {
		byID[e.ID] = e
	}

	if f, ok := byID["ev-follow-1"]; !ok || f.Outcome != eventlog.OutcomeDroppedDisabled || f.RawText != "Followed the channel" {
		t.Errorf("unexpected follow log entry: %+v", f)
	}
	if rOther, ok := byID["ev-redemp-other"]; !ok || rOther.Outcome != eventlog.OutcomeDroppedDisabled || rOther.RawText != "Redeemed Hydrate!" {
		t.Errorf("unexpected filtered redemption log entry: %+v", rOther)
	}
	if rMatch, ok := byID["ev-redemp-match"]; !ok || !rMatch.PassedFilters || rMatch.Outcome != eventlog.OutcomeQueued || rMatch.SpokenText != "Viewer2 redeemed TTS Message: Hello world" {
		t.Errorf("unexpected matched redemption log entry: %+v", rMatch)
	}
	if gLow, ok := byID["ev-kick-gift-low"]; !ok || gLow.Outcome != eventlog.OutcomeDroppedThreshold {
		t.Errorf("unexpected low kick gift log entry: %+v", gLow)
	}
	if gHigh, ok := byID["ev-kick-gift-high"]; !ok || !gHigh.PassedFilters || gHigh.SpokenText != "KickUser2 sent 200 Sub Bomb: Hype!" {
		t.Errorf("unexpected high kick gift log entry: %+v", gHigh)
	}
}

func TestUnifiedStreamSessionLifecycleAcrossPlatforms(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()

	sqlStore, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlStore)

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()
	session.Queue().Pause()

	if session.ActiveSessionID() != 0 {
		t.Fatalf("expected initial ActiveSessionID=0, got %d", session.ActiveSessionID())
	}

	// 1. Twitch goes live -> starts StreamSession #1
	session.UpdatePlatformStreamState("twitch", "mrpoundsign", true)
	sid := session.ActiveSessionID()
	if sid <= 0 {
		t.Fatalf("expected positive ActiveSessionID after twitch live, got %d", sid)
	}

	// 2. TikTok also goes live -> joins same StreamSession #1
	session.UpdatePlatformStreamState("tiktok", "@mrpoundsign", true)
	if session.ActiveSessionID() != sid {
		t.Fatalf("expected same ActiveSessionID %d when tiktok joins, got %d", sid, session.ActiveSessionID())
	}

	// 3. Dispatch an event while live -> automatically stamped with SessionID
	_ = session.DispatchEvent(&chat.Event{
		ID:          "ev-in-session",
		Platform:    "twitch",
		Channel:     "mrpoundsign",
		Kind:        chat.EventKindCheer,
		UserID:      "42",
		UserName:    "cheerer",
		DisplayName: "Cheerer",
		Amount:      500,
		Count:       500,
		Currency:    "BITS",
		Text:        "cheer500 great stream!",
	})

	// 4. Twitch goes offline, but TikTok is still live -> session stays open
	session.UpdatePlatformStreamState("twitch", "mrpoundsign", false)
	if session.ActiveSessionID() != sid {
		t.Fatalf("expected session %d to remain open while tiktok is still live, got %d", sid, session.ActiveSessionID())
	}

	// 5. TikTok goes offline -> all platforms offline -> session closes
	session.UpdatePlatformStreamState("tiktok", "@mrpoundsign", false)
	if session.ActiveSessionID() != 0 {
		t.Fatalf("expected ActiveSessionID=0 after all platforms offline, got %d", session.ActiveSessionID())
	}

	sessions, err := logMgr.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 stream session, got %d", len(sessions))
	}
	if sessions[0].ID != sid || sessions[0].EndedAt.IsZero() {
		t.Errorf("expected session %d to be closed, got %+v", sid, sessions[0])
	}
	if len(sessions[0].Platforms) != 2 || len(sessions[0].Channels) != 2 {
		t.Errorf("expected 2 platforms and 2 channels on session, got platforms=%v channels=%v", sessions[0].Platforms, sessions[0].Channels)
	}

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{SessionID: sid})
	if err != nil {
		t.Fatalf("Query by SessionID: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].ID != "ev-in-session" || res.Entries[0].SessionID != sid {
		t.Errorf("expected ev-in-session in session %d, got %+v", sid, res.Entries)
	}
	if res.Summary.TotalBits != 500 {
		t.Errorf("expected TotalBits=500 in session summary, got %+v", res.Summary)
	}
}

func TestSession_InlineGiftAndStickerLogFormatting(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.SpeakEmotes = false // Ensure SpeakEmotes=false does NOT strip gift names from TTS!

	sqlStore, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlStore)

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()
	session.Queue().Pause()

	// 1. Dispatch TikTok Heart Me Gift with inline EmoteSpan
	_ = session.DispatchEvent(&chat.Event{
		ID:          "tt-heartme-1",
		Platform:    "tiktok",
		Kind:        chat.EventKindGift,
		UserID:      "501",
		UserName:    "heartgifter",
		DisplayName: "Heart Gifter",
		Text:        "Heart Me",
		ItemName:    "Heart Me",
		RewardTitle: "Heart Me",
		Count:       1,
		UnitAmount:  1,
		Amount:      1,
		Currency:    "Diamonds",
		EmoteSpans: []chat.EmoteSpan{
			{
				ID:    "7934",
				Name:  "Heart Me",
				Start: 0,
				End:   8,
				URL:   "https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/d56945782445b0b8c8658ed44f894c7b~tplv-obj.webp",
			},
		},
		Timestamp: time.Now(),
	})

	// 2. Dispatch YouTube Super Sticker with inline EmoteSpan
	_ = session.DispatchEvent(&chat.Event{
		ID:          "yt-sticker-1",
		Platform:    "youtube",
		Kind:        chat.EventKindSuperSticker,
		UserID:      "601",
		UserName:    "stickerfan",
		DisplayName: "Sticker Fan",
		Text:        "Pear cheering",
		ItemName:    "Pear cheering",
		Count:       1,
		UnitAmount:  5,
		Amount:      5,
		Currency:    "dollars",
		EmoteSpans: []chat.EmoteSpan{
			{
				ID:    "yt-sticker",
				Name:  "Pear cheering",
				Start: 0,
				End:   13,
				URL:   "https://lh3.googleusercontent.com/sticker.png",
			},
		},
		Timestamp: time.Now(),
	})

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(res.Entries))
	}

	var ttEntry, ytEntry eventlog.Entry
	for _, e := range res.Entries {
		switch e.ID {
		case "tt-heartme-1":
			ttEntry = e
		case "yt-sticker-1":
			ytEntry = e
		}
	}

	// Verify TikTok Heart Me entry has both text and trailing inline icon span
	if ttEntry.RawText != "Sent 1 Heart Me :Heart Me:" {
		t.Errorf("ttEntry.RawText = %q, want %q", ttEntry.RawText, "Sent 1 Heart Me :Heart Me:")
	}
	if len(ttEntry.Emotes) != 1 {
		t.Fatalf("expected 1 emote span on ttEntry, got %d", len(ttEntry.Emotes))
	}
	runes := []rune(ttEntry.RawText)
	emSpan := ttEntry.Emotes[0]
	if string(runes[emSpan.Start:emSpan.End]) != ":Heart Me:" {
		t.Errorf("ttEntry emote span slice = %q, want %q", string(runes[emSpan.Start:emSpan.End]), ":Heart Me:")
	}
	if ttEntry.SpokenText == "" {
		t.Errorf("expected Heart Me gift to be spoken even when SpeakEmotes=false")
	}

	// Verify YouTube Super Sticker entry has trailing inline sticker span
	if ytEntry.RawText != "Super Sticker (5 dollars) :Pear cheering:" {
		t.Errorf("ytEntry.RawText = %q, want %q", ytEntry.RawText, "Super Sticker (5 dollars) :Pear cheering:")
	}
	if len(ytEntry.Emotes) != 1 {
		t.Fatalf("expected 1 emote span on ytEntry, got %d", len(ytEntry.Emotes))
	}
	ytRunes := []rune(ytEntry.RawText)
	ytSpan := ytEntry.Emotes[0]
	if string(ytRunes[ytSpan.Start:ytSpan.End]) != ":Pear cheering:" {
		t.Errorf("ytEntry emote span slice = %q, want %q", string(ytRunes[ytSpan.Start:ytSpan.End]), ":Pear cheering:")
	}
}

func TestSessionFollowerRoleGatingTwitchAndKick(t *testing.T) {
	ctx := context.Background()
	sqlBackend, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlBackend)
	defer func() { _ = logMgr.Close() }()

	cfg := config.DefaultConfig()
	twCfg := cfg.Platforms["twitch"]
	twCfg.AccountName = "mrpoundsign"
	twCfg.ReadRules.AllChat = true
	twCfg.ReadRules.RoleGated = true
	twCfg.ReadRules.Roles = []string{"follower"}
	cfg.Platforms["twitch"] = twCfg

	kickCfg := cfg.Platforms["kick"]
	kickCfg.AccountName = "mrpoundsign"
	kickCfg.ReadRules.AllChat = true
	kickCfg.ReadRules.RoleGated = true
	kickCfg.ReadRules.Roles = []string{"follower"}
	cfg.Platforms["kick"] = kickCfg

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()
	session.Queue().Pause()

	// Configure Twitch custom follower checker (requires channel set on connector)
	_ = session.TwitchConnector().SetChannel("mrpoundsign")
	session.TwitchConnector().SetCheckFollowerFn(func(_ context.Context, channel string, userID string, userLogin string) (bool, error) {
		return channel == "mrpoundsign" && (userID == "tw-fol-1" || userLogin == "loyalfollower"), nil
	})

	// Configure Kick custom follower checker
	session.KickConnector().SetCheckFollowerFn(func(_ context.Context, slug, username string) (bool, time.Time, error) {
		if slug == "mrpoundsign" && username == "kickfollower" {
			return true, time.Now().UTC(), nil
		}
		return false, time.Time{}, nil
	})

	// 1. Twitch non-follower should be rejected by RoleGated=["follower"]
	_ = session.DispatchEvent(&chat.Event{
		ID:        "tw-nonfol-msg",
		Platform:  "twitch",
		Channel:   "mrpoundsign",
		Kind:      chat.EventKindMessage,
		UserID:    "tw-stranger-1",
		UserName:  "stranger",
		Text:      "Hello from stranger",
		Timestamp: time.Now(),
	})

	// 2. Twitch follower checked via CheckUserFollowing (30-min TTL) should get "follower" role and pass RoleGated=["follower"]
	_ = session.DispatchEvent(&chat.Event{
		ID:        "tw-fol-msg",
		Platform:  "twitch",
		Channel:   "mrpoundsign",
		Kind:      chat.EventKindMessage,
		UserID:    "tw-fol-1",
		UserName:  "loyalfollower",
		Text:      "Hello from loyal follower",
		Timestamp: time.Now(),
	})

	// 3. Live EventKindFollow from "stranger" should immediately promote them in the TTL cache so their next chat passes
	_ = session.DispatchEvent(&chat.Event{
		ID:        "tw-stranger-follow",
		Platform:  "twitch",
		Channel:   "mrpoundsign",
		Kind:      chat.EventKindFollow,
		UserID:    "tw-stranger-1",
		UserName:  "stranger",
		Timestamp: time.Now(),
	})
	_ = session.DispatchEvent(&chat.Event{
		ID:        "tw-stranger-after-follow",
		Platform:  "twitch",
		Channel:   "mrpoundsign",
		Kind:      chat.EventKindMessage,
		UserID:    "tw-stranger-1",
		UserName:  "stranger",
		Text:      "I just followed!",
		Timestamp: time.Now(),
	})

	// 4. Kick follower checked via CheckUserFollowing should pass RoleGated=["follower"]
	_ = session.DispatchEvent(&chat.Event{
		ID:        "kick-fol-msg",
		Platform:  "kick",
		Channel:   "mrpoundsign",
		Kind:      chat.EventKindMessage,
		UserID:    "k-101",
		UserName:  "kickfollower",
		Text:      "Hello from Kick follower",
		Timestamp: time.Now(),
	})

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byID := make(map[string]eventlog.Entry)
	for _, e := range res.Entries {
		byID[e.ID] = e
	}

	if byID["tw-nonfol-msg"].PassedFilters {
		t.Error("expected tw-nonfol-msg to be rejected by RoleGated=['follower']")
	}
	if !byID["tw-fol-msg"].PassedFilters {
		t.Error("expected tw-fol-msg to pass RoleGated=['follower']")
	}
	if !byID["tw-stranger-after-follow"].PassedFilters {
		t.Error("expected tw-stranger-after-follow to pass RoleGated=['follower'] after live follow event")
	}
	if !byID["kick-fol-msg"].PassedFilters {
		t.Error("expected kick-fol-msg to pass RoleGated=['follower']")
	}
}

func TestSessionWatchStreakAndExtendedEventFormatting(t *testing.T) {
	ctx := context.Background()
	sqlBackend, err := eventlog.NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend: %v", err)
	}
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlBackend)
	defer func() { _ = logMgr.Close() }()

	cfg := config.DefaultConfig()
	twCfg := cfg.Platforms["twitch"]
	wsNotif := twCfg.Notifications["watch_streak"]
	wsNotif.Threshold = 3
	twCfg.Notifications["watch_streak"] = wsNotif
	cfg.Platforms["twitch"] = twCfg

	session := NewSession(cfg, secrets.NewMemoryStore())
	session.SetEventLogger(logMgr)
	defer func() { _ = session.Close() }()
	session.Queue().Pause()

	// 1. Watch Streak above threshold -> logged with formatted RawText and queued for TTS
	_ = session.DispatchEvent(&chat.Event{
		ID:          "tw-streak-5",
		Platform:    "twitch",
		Kind:        chat.EventKindWatchStreak,
		UserID:      "u1",
		UserName:    "streakfan",
		DisplayName: "StreakFan",
		Count:       5,
		Amount:      450,
		Currency:    "points",
		Text:        "Never missed one!",
	})

	// 2. Watch Streak below threshold -> still logged in Chat Log, but dropped from TTS
	_ = session.DispatchEvent(&chat.Event{
		ID:          "tw-streak-2",
		Platform:    "twitch",
		Kind:        chat.EventKindWatchStreak,
		UserID:      "u2",
		UserName:    "newstreak",
		DisplayName: "NewStreak",
		Count:       2,
	})

	// 3. TikTok Share (default Enabled: false) -> logged in Chat Log, dropped from TTS
	_ = session.DispatchEvent(&chat.Event{
		ID:          "tt-share-1",
		Platform:    "tiktok",
		Kind:        chat.EventKindShare,
		UserID:      "u3",
		UserName:    "sharer",
		DisplayName: "Sharer",
	})

	// 4. Kick Multi-Recipient Gift Sub -> logged with all recipient names
	_ = session.DispatchEvent(&chat.Event{
		ID:          "kick-giftsub-multi",
		Platform:    "kick",
		Kind:        chat.EventKindGiftSub,
		UserID:      "u4",
		UserName:    "biggifter",
		DisplayName: "BigGifter",
		Count:       3,
		Recipient:   "Alice, Bob, Charlie",
	})

	res, err := logMgr.Query(ctx, eventlog.QueryFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byID := make(map[string]eventlog.Entry)
	for _, e := range res.Entries {
		byID[e.ID] = e
	}

	s5 := byID["tw-streak-5"]
	if !s5.PassedFilters || s5.RawText != "Shared a 5-stream watch streak (+450 pts): Never missed one!" {
		t.Errorf("unexpected tw-streak-5 entry: %+v", s5)
	}
	s2 := byID["tw-streak-2"]
	if s2.PassedFilters || s2.Outcome != eventlog.OutcomeDroppedThreshold || s2.RawText != "Shared a 2-stream watch streak" {
		t.Errorf("unexpected tw-streak-2 entry: %+v", s2)
	}
	sh := byID["tt-share-1"]
	if sh.PassedFilters || sh.Outcome != eventlog.OutcomeDroppedDisabled || sh.RawText != "Shared the stream" {
		t.Errorf("unexpected tt-share-1 entry: %+v", sh)
	}
	kg := byID["kick-giftsub-multi"]
	if !kg.PassedFilters || kg.RawText != "Gifted 3 subscriptions to Alice, Bob, Charlie" {
		t.Errorf("unexpected kick-giftsub-multi entry: %+v", kg)
	}
}


