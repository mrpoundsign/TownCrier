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


