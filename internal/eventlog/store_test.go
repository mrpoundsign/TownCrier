package eventlog

import (
	"context"
	"testing"
	"time"
)

func TestManagerEnabledToggle(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := NewSQLiteBackend(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}

	mgr := NewManager(RetentionPolicy{
		ChatRetentionDays:  30,
		EventRetentionDays: 90,
	}, sqlStore)
	defer func() { _ = mgr.Close() }()

	if !mgr.IsEnabled() {
		t.Fatal("expected manager to be enabled by default")
	}

	// 1. Disable logging
	mgr.SetEnabled(false)
	if mgr.IsEnabled() {
		t.Fatal("expected manager to report disabled")
	}

	// Append when disabled should be a no-op
	entry1 := &Entry{
		ID:        "msg-disabled-1",
		Kind:      "message",
		Platform:  "twitch",
		UserName:  "alice",
		RawText:   "hello",
		CreatedAt: time.Now().UTC(),
	}
	if err := mgr.Append(ctx, entry1); err != nil {
		t.Fatalf("Append returned unexpected error when disabled: %v", err)
	}

	// Sessions when disabled should be a no-op
	sess, err := mgr.StartSession(ctx, time.Now().UTC(), "twitch", "testchan")
	if err != nil {
		t.Fatalf("StartSession returned unexpected error when disabled: %v", err)
	}
	if sess.ID != 0 {
		t.Fatalf("expected empty session ID when disabled, got %d", sess.ID)
	}

	if err := mgr.AddSessionPlatform(ctx, 123, "youtube", "testchan2"); err != nil {
		t.Fatalf("AddSessionPlatform error when disabled: %v", err)
	}
	if err := mgr.EndSession(ctx, 123, time.Now().UTC()); err != nil {
		t.Fatalf("EndSession error when disabled: %v", err)
	}

	// Query should be empty
	res, err := mgr.Query(ctx, QueryFilter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("expected 0 entries while disabled, got %d", len(res.Entries))
	}

	// 2. Re-enable logging
	mgr.SetEnabled(true)
	if !mgr.IsEnabled() {
		t.Fatal("expected manager to report enabled")
	}

	entry2 := &Entry{
		ID:        "msg-enabled-1",
		Kind:      "message",
		Platform:  "twitch",
		UserName:  "bob",
		RawText:   "world",
		CreatedAt: time.Now().UTC(),
	}
	if err := mgr.Append(ctx, entry2); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	res, err = mgr.Query(ctx, QueryFilter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("expected 1 entry after re-enabling, got %d", len(res.Entries))
	}
	if res.Entries[0].ID != "msg-enabled-1" {
		t.Errorf("expected entry 'msg-enabled-1', got %q", res.Entries[0].ID)
	}
}

func TestOutcomeTTSDisabledModel(t *testing.T) {
	label := OutcomeDisplayLabel(OutcomeTTSDisabled)
	if label != "🔇 TTS Disabled" {
		t.Errorf("expected label '🔇 TTS Disabled', got %q", label)
	}
}
