package eventlog

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockWebhookSink struct {
	BaseSink
	mu       sync.Mutex
	appended []*Entry
}

func (w *mockWebhookSink) Name() string {
	return "mock_webhook"
}

func (w *mockWebhookSink) Append(_ context.Context, entry *Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.appended = append(w.appended, entry.Clone())
	return nil
}

func (w *mockWebhookSink) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.appended)
}

func TestSQLiteBackendAndManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	dbFile := filepath.Join(t.TempDir(), "events.db")

	sqlBackend, err := NewSQLiteBackend(dbFile)
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}

	webhook := &mockWebhookSink{}
	mgr := NewManager(RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlBackend)
	mgr.AddBackend(webhook)
	defer func() { _ = mgr.Close() }()

	var liveEvents []LiveUpdate
	unsub := mgr.Subscribe(func(ev LiveUpdate) {
		liveEvents = append(liveEvents, ev)
	})
	defer unsub()

	baseTime := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	// 1. Append an event that passed filters and is queued
	e1 := &Entry{
		ID:            "msg-1",
		CreatedAt:     baseTime,
		Platform:      "twitch",
		Channel:       "mrpoundsign",
		Kind:          "message",
		IsPaidOrEvent: false,
		UserID:        "10001",
		UserName:      "alice_tw",
		DisplayName:   "Alice",
		Roles:         []string{"vip", "subscriber"},
		RawText:       "Hello stream!",
		SpokenText:    "Alice says Hello stream!",
		VoiceID:       "supertonic:f1",
		Speed:         1.0,
		PassedFilters: true,
		Spoken:        false,
		Outcome:       OutcomeQueued,
	}
	if err := mgr.Append(ctx, e1); err != nil {
		t.Fatalf("Append e1 failed: %v", err)
	}

	// 2. Append an event that passed filters, then gets dropped in queue for 30s staleness
	e2 := &Entry{
		ID:            "msg-2",
		CreatedAt:     baseTime.Add(5 * time.Second),
		Platform:      "youtube",
		Channel:       "@mrpoundsign",
		Kind:          "message",
		IsPaidOrEvent: false,
		UserID:        "UC_BOB_99",
		UserName:      "bob_yt",
		DisplayName:   "Bob",
		RawText:       "Burst chat message",
		SpokenText:    "Bob says Burst chat message",
		VoiceID:       "kokoro:am_adam",
		Speed:         1.0,
		PassedFilters: true,
		Spoken:        false,
		Outcome:       OutcomeQueued,
	}
	if err := mgr.Append(ctx, e2); err != nil {
		t.Fatalf("Append e2 failed: %v", err)
	}

	// 3. Append a bot message rejected by pre-queue filter
	e3 := &Entry{
		ID:            "msg-3",
		CreatedAt:     baseTime.Add(10 * time.Second),
		Platform:      "twitch",
		Channel:       "mrpoundsign",
		Kind:          "message",
		IsPaidOrEvent: false,
		UserID:        "99999",
		UserName:      "nightbot",
		DisplayName:   "Nightbot",
		RawText:       "Follow the channel!",
		PassedFilters: false,
		Spoken:        false,
		Outcome:       OutcomeDroppedBot,
		OutcomeDetail: "Known bot ignored",
	}
	if err := mgr.Append(ctx, e3); err != nil {
		t.Fatalf("Append e3 failed: %v", err)
	}

	// Transition e1 -> Spoken (with 1.3x catchup speed)
	if err := mgr.UpdateOutcome(ctx, "msg-1", OutcomeUpdate{
		Spoken:  true,
		Outcome: OutcomeSpoken,
		Speed:   1.3,
	}); err != nil {
		t.Fatalf("UpdateOutcome e1 failed: %v", err)
	}

	// Transition e2 -> DroppedStale
	if err := mgr.UpdateOutcome(ctx, "msg-2", OutcomeUpdate{
		Spoken:        false,
		Outcome:       OutcomeDroppedStale,
		OutcomeDetail: "Exceeded 30s queue max age",
	}); err != nil {
		t.Fatalf("UpdateOutcome e2 failed: %v", err)
	}

	// Verify webhook sink received all 3 appends and no-op'd on UpdateOutcome/Query/Prune
	if webhook.Count() != 3 {
		t.Errorf("expected webhook sink to receive 3 appends, got %d", webhook.Count())
	}
	if _, err := webhook.Query(ctx, QueryFilter{}); err != nil {
		t.Errorf("expected BaseSink.Query no-op nil error, got %v", err)
	}
	if _, err := webhook.Prune(ctx, RetentionPolicy{ChatRetentionDays: 7}); err != nil {
		t.Errorf("expected BaseSink.Prune no-op nil error, got %v", err)
	}

	// Verify Live Tail pub/sub received 3 appends + 2 outcome updates = 5 events
	if len(liveEvents) != 5 {
		t.Errorf("expected 5 live tail events, got %d", len(liveEvents))
	}

	// Query Spoken Only
	spokenRes, err := mgr.Query(ctx, QueryFilter{
		OutcomeMode: FilterOutcomeSpokenOnly,
		Retention:   RetentionPolicy{Now: baseTime.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("Query SpokenOnly failed: %v", err)
	}
	if spokenRes.TotalCount != 1 || len(spokenRes.Entries) != 1 || spokenRes.Entries[0].ID != "msg-1" {
		t.Fatalf("expected only msg-1 in SpokenOnly, got %+v", spokenRes)
	}
	if !spokenRes.Entries[0].Spoken || !spokenRes.Entries[0].PassedFilters || spokenRes.Entries[0].Speed != 1.3 {
		t.Errorf("unexpected msg-1 state: %+v", spokenRes.Entries[0])
	}

	// Query PassedFilters (should include both msg-1 Spoken and msg-2 DroppedStale, excluding msg-3 DroppedBot)
	passedRes, err := mgr.Query(ctx, QueryFilter{
		OutcomeMode: FilterOutcomePassedFilters,
		Retention:   RetentionPolicy{Now: baseTime.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("Query PassedFilters failed: %v", err)
	}
	if passedRes.TotalCount != 2 {
		t.Errorf("expected 2 entries in PassedFilters, got %d", passedRes.TotalCount)
	}

	// Query DroppedQueue (should return only msg-2)
	droppedQueueRes, err := mgr.Query(ctx, QueryFilter{
		OutcomeMode: FilterOutcomeDroppedQueue,
		Retention:   RetentionPolicy{Now: baseTime.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("Query DroppedQueue failed: %v", err)
	}
	if droppedQueueRes.TotalCount != 1 || droppedQueueRes.Entries[0].ID != "msg-2" {
		t.Errorf("expected msg-2 in DroppedQueue, got %+v", droppedQueueRes.Entries)
	}

	// Query by Unique UserID ("UC_BOB_99")
	userRes, err := mgr.Query(ctx, QueryFilter{
		UserQuery: "UC_BOB_99",
		Retention: RetentionPolicy{Now: baseTime.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("Query by UserID failed: %v", err)
	}
	if userRes.TotalCount != 1 || userRes.Entries[0].UserName != "bob_yt" {
		t.Errorf("expected bob_yt when searching by UserID UC_BOB_99, got %+v", userRes.Entries)
	}

	// DistinctChannels
	channels, err := mgr.DistinctChannels(ctx, "")
	if err != nil {
		t.Fatalf("DistinctChannels failed: %v", err)
	}
	if len(channels) != 2 {
		t.Errorf("expected 2 distinct channels, got %v", channels)
	}
}

func TestDynamicCreatedAtRetentionIncreaseAndPrune(t *testing.T) {
	ctx := context.Background()
	sqlBackend, err := NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend(:memory:) failed: %v", err)
	}
	defer func() { _ = sqlBackend.Close() }()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	entries := []*Entry{
		{
			ID:            "chat-5d",
			CreatedAt:     now.AddDate(0, 0, -5),
			Platform:      "twitch",
			Channel:       "chan1",
			Kind:          "message",
			IsPaidOrEvent: false,
			UserID:        "u1",
			UserName:      "user1",
			DisplayName:   "User1",
			RawText:       "5 days ago chat",
			PassedFilters: true,
			Spoken:        true,
			Outcome:       OutcomeSpoken,
		},
		{
			ID:            "chat-15d",
			CreatedAt:     now.AddDate(0, 0, -15),
			Platform:      "twitch",
			Channel:       "chan1",
			Kind:          "message",
			IsPaidOrEvent: false,
			UserID:        "u2",
			UserName:      "user2",
			DisplayName:   "User2",
			RawText:       "15 days ago chat",
			PassedFilters: true,
			Spoken:        true,
			Outcome:       OutcomeSpoken,
		},
		{
			ID:            "chat-40d",
			CreatedAt:     now.AddDate(0, 0, -40),
			Platform:      "twitch",
			Channel:       "chan1",
			Kind:          "message",
			IsPaidOrEvent: false,
			UserID:        "u3",
			UserName:      "user3",
			DisplayName:   "User3",
			RawText:       "40 days ago chat",
			PassedFilters: true,
			Spoken:        true,
			Outcome:       OutcomeSpoken,
		},
		{
			ID:            "sub-40d",
			CreatedAt:     now.AddDate(0, 0, -40),
			Platform:      "twitch",
			Channel:       "chan1",
			Kind:          "sub",
			IsPaidOrEvent: true,
			UserID:        "u4",
			UserName:      "user4",
			DisplayName:   "User4",
			RawText:       "40 days ago subscription",
			PassedFilters: true,
			Spoken:        true,
			Outcome:       OutcomeSpoken,
		},
	}

	for _, e := range entries {
		if err := sqlBackend.Append(ctx, e); err != nil {
			t.Fatalf("Append %s failed: %v", e.ID, err)
		}
	}

	// If retention policy is 7 days for chat and 90 days for events, read-time cutoff hides chat-15d and chat-40d
	res7d, err := sqlBackend.Query(ctx, QueryFilter{
		Retention: RetentionPolicy{
			ChatRetentionDays:  7,
			EventRetentionDays: 90,
			Now:                now,
		},
	})
	if err != nil {
		t.Fatalf("Query with 7d policy failed: %v", err)
	}
	if res7d.TotalCount != 2 {
		t.Errorf("expected 2 visible entries under 7d chat / 90d event policy (chat-5d and sub-40d), got %d", res7d.TotalCount)
	}

	// If the user increases ChatRetentionDays from 7 to 30 before pruning, chat-15d is immediately visible and preserved!
	policy30d := RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
		Now:                now,
	}
	res30d, err := sqlBackend.Query(ctx, QueryFilter{
		Retention: policy30d,
	})
	if err != nil {
		t.Fatalf("Query with 30d policy failed: %v", err)
	}
	if res30d.TotalCount != 3 {
		t.Errorf("expected 3 visible entries when ChatRetentionDays increased to 30 (chat-5d, chat-15d, sub-40d), got %d", res30d.TotalCount)
	}

	// Prune with 30d chat / 90d event policy: only chat-40d should be deleted; sub-40d is kept
	deleted, err := sqlBackend.Prune(ctx, policy30d)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected exactly 1 row (chat-40d) pruned, got %d", deleted)
	}

	// Query with 0 (unlimited) retention to verify physical rows remaining
	resAll, err := sqlBackend.Query(ctx, QueryFilter{})
	if err != nil {
		t.Fatalf("Query all failed: %v", err)
	}
	if resAll.TotalCount != 3 {
		t.Errorf("expected 3 physical rows remaining after prune, got %d", resAll.TotalCount)
	}
}
