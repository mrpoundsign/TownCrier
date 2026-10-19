package eventlog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
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

	// 1. Append an event that passed filters and is queued (with EmoteSpans)
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
		RawText:       "Hello stream! Kappa",
		Emotes: []chat.EmoteSpan{
			{Start: 14, End: 19, ID: "25", Name: "Kappa", URL: "https://static-cdn.jtvnw.net/emoticons/v2/25/static/dark/1.0"},
		},
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
	if len(spokenRes.Entries[0].Emotes) != 1 || spokenRes.Entries[0].Emotes[0].Name != "Kappa" || spokenRes.Entries[0].Emotes[0].ID != "25" {
		t.Errorf("expected persisted Kappa EmoteSpan on msg-1, got %+v", spokenRes.Entries[0].Emotes)
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

func TestSQLiteKindFilter(t *testing.T) {
	ctx := context.Background()
	sqlBackend, err := NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	defer func() { _ = sqlBackend.Close() }()

	now := time.Now().UTC()
	kinds := []string{"message", "follow", "sub", "resub", "gift_sub", "cheer", "gift", "super_chat", "super_sticker", "raid", "redemption", "moderation"}
	for i, k := range kinds {
		err := sqlBackend.Append(ctx, &Entry{
			ID:            fmt.Sprintf("id-%d-%s", i, k),
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
			Platform:      "twitch",
			Channel:       "mrpoundsign",
			Kind:          k,
			IsPaidOrEvent: k != "message" && k != "moderation",
			UserID:        "u1",
			UserName:      "viewer",
			DisplayName:   "Viewer",
			RawText:       "event " + k,
			Outcome:       OutcomeSpoken,
		})
		if err != nil {
			t.Fatalf("Append(%s): %v", k, err)
		}
	}

	cases := []struct {
		kindFilter string
		wantCount  int
	}{
		{"all", 12},
		{"chat", 1},
		{"alerts", 10},
		{"follow", 1},
		{"sub", 3},
		{"cheer", 4},
		{"raid", 1},
		{"redemption", 1},
		{"moderation", 1},
	}

	for _, tc := range cases {
		res, err := sqlBackend.Query(ctx, QueryFilter{KindFilter: tc.kindFilter})
		if err != nil {
			t.Fatalf("Query(KindFilter=%q): %v", tc.kindFilter, err)
		}
		if res.TotalCount != tc.wantCount {
			t.Errorf("KindFilter=%q: got %d entries, want %d", tc.kindFilter, res.TotalCount, tc.wantCount)
		}
	}
}

func TestPreMigrationBackupAndRotation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "events.db")

	// 1. Manually create a v0 legacy database without stream_sessions or new columns
	rawDB, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	legacyDDL := `
CREATE TABLE event_logs (
	id              TEXT PRIMARY KEY,
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL,
	platform        TEXT NOT NULL,
	channel         TEXT NOT NULL,
	kind            TEXT NOT NULL,
	is_paid_event   INTEGER NOT NULL DEFAULT 0,
	user_id         TEXT NOT NULL,
	user_name       TEXT NOT NULL,
	display_name    TEXT NOT NULL,
	roles_json      TEXT NOT NULL DEFAULT '[]',
	raw_text        TEXT NOT NULL DEFAULT '',
	spoken_text     TEXT NOT NULL DEFAULT '',
	voice_id        TEXT NOT NULL DEFAULT '',
	speed           REAL NOT NULL DEFAULT 1.0,
	amount          REAL NOT NULL DEFAULT 0,
	currency        TEXT NOT NULL DEFAULT '',
	months          INTEGER NOT NULL DEFAULT 0,
	count           INTEGER NOT NULL DEFAULT 0,
	passed_filters  INTEGER NOT NULL DEFAULT 0,
	spoken          INTEGER NOT NULL DEFAULT 0,
	outcome         TEXT NOT NULL,
	outcome_detail  TEXT NOT NULL DEFAULT ''
);
`
	if _, err := rawDB.Exec(legacyDDL); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	t1 := time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)
	t2 := t1.Add(20 * time.Minute)
	// t3 is 3 hours later -> should be split into a second backfilled historical session
	t3 := t1.Add(3 * time.Hour)

	insertLegacy := `
INSERT INTO event_logs (id, created_at, updated_at, platform, channel, kind, is_paid_event, user_id, user_name, display_name, raw_text, amount, currency, count, passed_filters, spoken, outcome)
VALUES (?, ?, ?, ?, ?, ?, ?, 'u1', 'alice', 'Alice', ?, ?, ?, ?, 1, 1, 'spoken');`

	if _, err := rawDB.Exec(insertLegacy, "leg-1", t1.UnixMilli(), t1.UnixMilli(), "twitch", "mrpoundsign", "chat", 0, "hello world", 0, "", 0); err != nil {
		t.Fatalf("insert leg-1: %v", err)
	}
	if _, err := rawDB.Exec(insertLegacy, "leg-2", t2.UnixMilli(), t2.UnixMilli(), "tiktok", "mrpoundsign", "gift", 1, "Heart Me", 10, "Diamonds", 2); err != nil {
		t.Fatalf("insert leg-2: %v", err)
	}
	if _, err := rawDB.Exec(insertLegacy, "leg-3", t3.UnixMilli(), t3.UnixMilli(), "youtube", "@mrpoundsign", "super_chat", 1, "Great stream!", 5.0, "USD", 1); err != nil {
		t.Fatalf("insert leg-3: %v", err)
	}
	_ = rawDB.Close()

	// 2. Open with NewSQLiteBackend -> should create 1 backup file and migrate to v1
	backend, err := NewSQLiteBackend(dbFile)
	if err != nil {
		t.Fatalf("NewSQLiteBackend on legacy DB failed: %v", err)
	}

	backups, err := ListBackups(dbFile)
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup file after first migration, got %d (%v)", len(backups), backups)
	}

	// Verify the backup file is a valid standalone SQLite database with the original 3 rows
	bakDB, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatalf("open backup DB: %v", err)
	}
	var bakCount int
	if err := bakDB.QueryRow(`SELECT COUNT(*) FROM event_logs;`).Scan(&bakCount); err != nil {
		t.Fatalf("count rows in backup DB: %v", err)
	}
	_ = bakDB.Close()
	if bakCount != 3 {
		t.Errorf("expected 3 rows in backup DB, got %d", bakCount)
	}

	// Verify backfilled sessions: (leg-1, leg-2) in session 1, (leg-3) in session 2
	sessions, err := backend.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 backfilled historical stream sessions, got %d: %+v", len(sessions), sessions)
	}

	// Verify migrated columns on leg-2 (gift): ItemName="Heart Me", UnitAmount=5.0
	res, err := backend.Query(ctx, QueryFilter{})
	if err != nil {
		t.Fatalf("Query migrated DB failed: %v", err)
	}
	if res.TotalCount != 3 {
		t.Fatalf("expected 3 migrated entries, got %d", res.TotalCount)
	}
	var foundGift, foundChat bool
	for _, e := range res.Entries {
		switch e.ID {
		case "leg-1":
			foundChat = true
			if e.Kind != "message" {
				t.Errorf("expected leg-1 kind normalized from 'chat' to 'message', got %q", e.Kind)
			}
			if e.SessionID <= 0 {
				t.Errorf("expected leg-1 to have backfilled SessionID > 0, got %d", e.SessionID)
			}
		case "leg-2":
			foundGift = true
			if e.ItemName != "Heart Me" {
				t.Errorf("expected leg-2 ItemName backfilled to 'Heart Me', got %q", e.ItemName)
			}
			if e.UnitAmount != 5.0 {
				t.Errorf("expected leg-2 UnitAmount backfilled to 5.0 (10/2), got %v", e.UnitAmount)
			}
		}
	}
	if !foundChat || !foundGift {
		t.Errorf("missing expected migrated entries (foundChat=%v, foundGift=%v)", foundChat, foundGift)
	}
	_ = backend.Close()

	// 3. Re-opening an already-migrated DB should NOT create an extra backup
	backend2, err := NewSQLiteBackend(dbFile)
	if err != nil {
		t.Fatalf("second NewSQLiteBackend failed: %v", err)
	}
	_ = backend2.Close()

	backupsAfterNoOp, err := ListBackups(dbFile)
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backupsAfterNoOp) != 1 {
		t.Fatalf("expected still 1 backup file when no migration needed, got %d", len(backupsAfterNoOp))
	}

	// 4. Simulate 6 more version upgrades (total 7 backups triggered) -> rotation must keep only the newest 5
	for i := range 6 {
		rDB, err := sql.Open("sqlite", dbFile)
		if err != nil {
			t.Fatalf("open raw db #%d: %v", i, err)
		}
		if _, err := rDB.Exec(`PRAGMA user_version = 0;`); err != nil {
			t.Fatalf("reset user_version #%d: %v", i, err)
		}
		_ = rDB.Close()

		b, err := NewSQLiteBackend(dbFile)
		if err != nil {
			t.Fatalf("NewSQLiteBackend rotation #%d failed: %v", i, err)
		}
		_ = b.Close()
	}

	rotatedBackups, err := ListBackups(dbFile)
	if err != nil {
		t.Fatalf("ListBackups after rotation failed: %v", err)
	}
	if len(rotatedBackups) != maxDBBackups {
		t.Fatalf("expected exactly %d backups kept after rotation, got %d (%v)", maxDBBackups, len(rotatedBackups), rotatedBackups)
	}
	// The very first backup should have been pruned
	if rotatedBackups[0] == backups[0] {
		t.Errorf("expected oldest backup %q to be pruned, but it is still present in %v", backups[0], rotatedBackups)
	}
}

func TestRecoverOpenSessionsOnStartup(t *testing.T) {
	ctx := context.Background()
	dbFile := filepath.Join(t.TempDir(), "events.db")

	backend, err := NewSQLiteBackend(dbFile)
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}

	tStart := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	sess1, err := backend.StartSession(ctx, tStart, "twitch", "mrpoundsign")
	if err != nil {
		t.Fatalf("StartSession 1 failed: %v", err)
	}
	if !sess1.EndedAt.IsZero() {
		t.Fatalf("expected active session to have zero EndedAt, got %v", sess1.EndedAt)
	}

	tEvent1 := tStart.Add(15 * time.Minute)
	tEvent2 := tStart.Add(42 * time.Minute)

	for i, ts := range []time.Time{tEvent1, tEvent2} {
		if err := backend.Append(ctx, &Entry{
			ID:        fmt.Sprintf("ev-%d", i+1),
			SessionID: sess1.ID,
			CreatedAt: ts,
			Platform:  "twitch",
			Channel:   "mrpoundsign",
			Kind:      "message",
			UserID:    "u1",
			UserName:  "chatter",
			RawText:   "msg",
			Outcome:   OutcomeSpoken,
		}); err != nil {
			t.Fatalf("Append ev-%d: %v", i+1, err)
		}
	}

	// Simulate crash/exit without calling EndSession, then re-open
	_ = backend.Close()

	reopened, err := NewSQLiteBackend(dbFile)
	if err != nil {
		t.Fatalf("reopen NewSQLiteBackend failed: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	sessions, err := reopened.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions after recovery failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if !sessions[0].EndedAt.Equal(tEvent2) {
		t.Errorf("expected unclosed session to recover EndedAt=%v (last event time), got %v", tEvent2, sessions[0].EndedAt)
	}
}

func TestStreamSessionLifecycleAndSummarize(t *testing.T) {
	ctx := context.Background()
	backend, err := NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	defer func() { _ = backend.Close() }()

	mgr := NewManager(RetentionPolicy{}, backend)
	defer func() { _ = mgr.Close() }()

	tStart := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	sess, err := mgr.StartSession(ctx, tStart, "Twitch", "mrpoundsign")
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if err := mgr.AddSessionPlatform(ctx, sess.ID, "TikTok", "@mrpoundsign"); err != nil {
		t.Fatalf("AddSessionPlatform failed: %v", err)
	}
	// Duplicate call should be idempotent
	if err := mgr.AddSessionPlatform(ctx, sess.ID, "twitch", "mrpoundsign"); err != nil {
		t.Fatalf("AddSessionPlatform duplicate failed: %v", err)
	}

	events := []*Entry{
		{
			ID: "s-sub", SessionID: sess.ID, CreatedAt: tStart.Add(1 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "sub", IsPaidOrEvent: true,
			UserID: "u1", UserName: "subber1", SubTier: "Tier 1", Months: 1, Count: 1, Outcome: OutcomeSpoken,
		},
		{
			ID: "s-resub", SessionID: sess.ID, CreatedAt: tStart.Add(2 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "resub", IsPaidOrEvent: true,
			UserID: "u2", UserName: "subber2", SubTier: "Prime", Months: 6, Count: 1, Outcome: OutcomeSpoken,
		},
		{
			ID: "s-giftsub", SessionID: sess.ID, CreatedAt: tStart.Add(3 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "gift_sub", IsPaidOrEvent: true,
			UserID: "u3", UserName: "gifter", Count: 5, Outcome: OutcomeSpoken,
		},
		{
			ID: "s-cheer", SessionID: sess.ID, CreatedAt: tStart.Add(4 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "cheer", IsPaidOrEvent: true,
			UserID: "u4", UserName: "cheerer", Amount: 500, Currency: "bits", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-ttgift", SessionID: sess.ID, CreatedAt: tStart.Add(5 * time.Minute),
			Platform: "tiktok", Channel: "@mrpoundsign", Kind: "gift", IsPaidOrEvent: true,
			UserID: "u5", UserName: "ttfan", ItemName: "Heart Me", UnitAmount: 1, Amount: 10, Count: 10, Currency: "Diamonds", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-kickgift", SessionID: sess.ID, CreatedAt: tStart.Add(6 * time.Minute),
			Platform: "kick", Channel: "mrpoundsign", Kind: "gift", IsPaidOrEvent: true,
			UserID: "u6", UserName: "kickfan", ItemName: "Kicks", UnitAmount: 1, Amount: 250, Count: 250, Currency: "Kicks", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-superchat", SessionID: sess.ID, CreatedAt: tStart.Add(7 * time.Minute),
			Platform: "youtube", Channel: "@mrpoundsign", Kind: "super_chat", IsPaidOrEvent: true,
			UserID: "u7", UserName: "ytfan", Amount: 20.0, Currency: "USD", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-supersticker", SessionID: sess.ID, CreatedAt: tStart.Add(8 * time.Minute),
			Platform: "youtube", Channel: "@mrpoundsign", Kind: "super_sticker", IsPaidOrEvent: true,
			UserID: "u8", UserName: "ytfan2", Amount: 5.0, Currency: "USD", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-raid", SessionID: sess.ID, CreatedAt: tStart.Add(9 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "raid", IsPaidOrEvent: true,
			UserID: "u9", UserName: "raider", Count: 42, Outcome: OutcomeSpoken,
		},
		{
			ID: "s-follow", SessionID: sess.ID, CreatedAt: tStart.Add(10 * time.Minute),
			Platform: "tiktok", Channel: "@mrpoundsign", Kind: "follow", IsPaidOrEvent: true,
			UserID: "u10", UserName: "follower1", Outcome: OutcomeSpoken,
		},
		{
			ID: "s-redemption", SessionID: sess.ID, CreatedAt: tStart.Add(11 * time.Minute),
			Platform: "twitch", Channel: "mrpoundsign", Kind: "redemption", IsPaidOrEvent: true,
			UserID: "u11", UserName: "redeemer", RewardTitle: "Hydrate", Amount: 100, Currency: "points", Outcome: OutcomeSpoken,
		},
	}

	for _, ev := range events {
		if err := mgr.Append(ctx, ev); err != nil {
			t.Fatalf("Append(%s) failed: %v", ev.ID, err)
		}
	}

	tEnd := tStart.Add(2 * time.Hour)
	if err := mgr.EndSession(ctx, sess.ID, tEnd); err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}

	sessions, err := mgr.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if len(sessions[0].Platforms) != 2 || sessions[0].Platforms[0] != "twitch" || sessions[0].Platforms[1] != "tiktok" {
		t.Errorf("unexpected session platforms: %v", sessions[0].Platforms)
	}
	if !sessions[0].EndedAt.Equal(tEnd) {
		t.Errorf("expected session EndedAt=%v, got %v", tEnd, sessions[0].EndedAt)
	}

	res, err := mgr.Query(ctx, QueryFilter{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("Query by SessionID failed: %v", err)
	}
	if res.TotalCount != len(events) {
		t.Errorf("expected %d events in session, got %d", len(events), res.TotalCount)
	}

	sum := res.Summary
	if sum.TotalSubs != 2 {
		t.Errorf("TotalSubs: got %d, want 2", sum.TotalSubs)
	}
	if sum.TotalGiftSubs != 5 {
		t.Errorf("TotalGiftSubs: got %d, want 5", sum.TotalGiftSubs)
	}
	if sum.TotalBits != 500 {
		t.Errorf("TotalBits: got %v, want 500", sum.TotalBits)
	}
	if sum.TotalDiamonds != 10 {
		t.Errorf("TotalDiamonds: got %v, want 10", sum.TotalDiamonds)
	}
	if sum.TotalKicks != 250 {
		t.Errorf("TotalKicks: got %v, want 250", sum.TotalKicks)
	}
	if sum.SuperChatByCurr["USD"] != 25.0 {
		t.Errorf("SuperChatByCurr[USD]: got %v, want 25.0", sum.SuperChatByCurr["USD"])
	}
	if sum.TotalRaids != 1 || sum.TotalRaidViewers != 42 {
		t.Errorf("Raids: got %d raids (%d viewers), want 1 raid (42 viewers)", sum.TotalRaids, sum.TotalRaidViewers)
	}
	if sum.TotalFollows != 1 {
		t.Errorf("TotalFollows: got %d, want 1", sum.TotalFollows)
	}
	if sum.TotalRedemptions != 1 {
		t.Errorf("TotalRedemptions: got %d, want 1", sum.TotalRedemptions)
	}
}


