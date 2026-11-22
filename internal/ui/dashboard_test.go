package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestDashboardViewBuild(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	dash := NewDashboardView(win, session)
	content := dash.Build()
	if content == nil {
		t.Fatal("expected non-nil content from DashboardView.Build")
	}

	dash.AddActivity(ActivityEntry{
		Timestamp: time.Now(),
		Platform:  "twitch",
		UserName:  "alice",
		Text:      "test message",
		Kind:      "Chat",
	})

	if len(dash.activities) != 1 {
		t.Fatalf("expected 1 activity entry, got %d", len(dash.activities))
	}
}

func TestDashboardSyncTTSState(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	dash := NewDashboardView(win, session)
	_ = dash.Build()

	// 1. SyncTTSState(false) completely hides the topSection (speaking card & queue controls)
	dash.SyncTTSState(false)
	if dash.topSection.Visible() {
		t.Error("expected topSection to be hidden when TTS is disabled")
	}

	// 2. SyncTTSState(true) restores the topSection
	dash.SyncTTSState(true)
	if !dash.topSection.Visible() {
		t.Error("expected topSection to be visible when TTS is enabled")
	}
	if dash.nowPlayingLabel.Text != "Idle (No speech active)" {
		t.Errorf("expected nowPlayingLabel 'Idle (No speech active)', got %q", dash.nowPlayingLabel.Text)
	}
	if dash.queueDepthLabel.Text != "Queue: 0 pending items" {
		t.Errorf("expected queueDepthLabel 'Queue: 0 pending items', got %q", dash.queueDepthLabel.Text)
	}
	if dash.pauseResumeBtn.Disabled() {
		t.Error("expected pauseResumeBtn to be enabled")
	}
	if dash.skipBtn.Disabled() {
		t.Error("expected skipBtn to be enabled")
	}
	if dash.clearBtn.Disabled() {
		t.Error("expected clearBtn to be enabled")
	}
}

func TestDashboardTabDeferral(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	win := appObj.NewWindow("TestTabDeferral")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	dash := NewDashboardView(win, session)
	_ = dash.Build()

	// Mark Dashboard tab inactive while user is on another tab
	dash.SetTabActive(false)
	dash.AddActivity(ActivityEntry{
		Timestamp: time.Now(),
		Platform:  "twitch",
		UserName:  "bob",
		Text:      "deferred message",
		Kind:      "Chat",
	})

	if len(dash.activities) != 1 {
		t.Fatalf("expected 1 activity buffered while inactive, got %d", len(dash.activities))
	}
	dash.logView.mu.Lock()
	dirty := dash.logView.renderDirty
	dash.logView.mu.Unlock()
	if !dirty {
		t.Fatal("expected embedded logView renderDirty=true while inactive")
	}

	// Activate Dashboard tab -> should render the buffered activity and clear renderDirty
	dash.SetTabActive(true)
	dash.logView.mu.Lock()
	dirty = dash.logView.renderDirty
	rowCount := len(dash.logView.rowCache)
	dash.logView.mu.Unlock()
	if dirty {
		t.Fatal("expected embedded logView renderDirty=false after activating tab")
	}
	if rowCount != 1 {
		t.Fatalf("expected 1 rendered log row after activating tab, got %d", rowCount)
	}
}

func TestDashboardLogViewWithEventLogger(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	win := appObj.NewWindow("TestDashEventLogger")
	defer win.Close()

	cfg := config.DefaultConfig()
	cfg.Logging.Enabled = true
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	dbPath := filepath.Join(t.TempDir(), "dash_events.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{ChatRetentionDays: 30, EventRetentionDays: 90}, backend)
	defer func() { _ = mgr.Close() }()
	session.SetEventLogger(mgr)

	dash := NewDashboardView(win, session)
	defer dash.Close()
	win.SetContent(dash.Build())

	entry := eventlog.Entry{
		ID:            "ev-live-1",
		CreatedAt:     time.Now(),
		Platform:      "twitch",
		Kind:          "chat",
		UserID:        "u-1",
		UserName:      "alice",
		DisplayName:   "Alice",
		RawText:       "hello live logview",
		PassedFilters: true,
		Outcome:       eventlog.OutcomeQueued,
	}
	if err := mgr.Append(context.Background(), &entry); err != nil {
		t.Fatalf("mgr.Append failed: %v", err)
	}

	// Calling AddActivity while EventLogger is enabled must not duplicate the entry
	dash.AddActivity(ActivityEntry{
		ID:        "ev-live-1",
		Timestamp: entry.CreatedAt,
		Platform:  "twitch",
		UserName:  "alice",
		Text:      "hello live logview",
		Kind:      "chat",
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dash.logView.mu.Lock()
		n := len(dash.logView.entries)
		_, hasRow := dash.logView.rowCache["ev-live-1"]
		dash.logView.mu.Unlock()
		if n == 1 && hasRow {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	dash.logView.mu.Lock()
	defer dash.logView.mu.Unlock()
	t.Fatalf("expected 1 rendered entry in embedded logView, got entries=%d rowCache=%d", len(dash.logView.entries), len(dash.logView.rowCache))
}

func TestDashboardLogViewFallbackWhenLoggingDisabled(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	win := appObj.NewWindow("TestDashLoggingDisabled")
	defer win.Close()

	cfg := config.DefaultConfig()
	cfg.Logging.Enabled = false
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	dbPath := filepath.Join(t.TempDir(), "dash_disabled.db")
	backend, err := eventlog.NewSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteBackend failed: %v", err)
	}
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{ChatRetentionDays: 30, EventRetentionDays: 90}, backend)
	defer func() { _ = mgr.Close() }()
	session.SetEventLogger(mgr)

	dash := NewDashboardView(win, session)
	defer dash.Close()
	win.SetContent(dash.Build())

	dash.AddActivity(ActivityEntry{
		ID:        "fallback-1",
		Timestamp: time.Now(),
		Platform:  "youtube",
		UserName:  "charlie",
		Text:      "in-memory fallback message",
		Kind:      "chat",
	})

	dash.logView.mu.Lock()
	defer dash.logView.mu.Unlock()
	if len(dash.logView.entries) != 1 {
		t.Fatalf("expected 1 in-memory entry when logging disabled, got %d", len(dash.logView.entries))
	}
	if _, ok := dash.logView.rowCache["fallback-1"]; !ok {
		t.Fatalf("expected rendered logview row 'fallback-1' in rowCache")
	}
}


