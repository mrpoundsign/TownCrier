package app

import (
	"sync"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/queue"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

type mockIdleProvider struct {
	mu  sync.Mutex
	dur time.Duration
}

func (m *mockIdleProvider) SetIdle(d time.Duration) {
	m.mu.Lock()
	m.dur = d
	m.mu.Unlock()
}

func (m *mockIdleProvider) IdleDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dur
}

func TestSessionTTSOutputModeCycle(t *testing.T) {
	cfg := config.DefaultConfig()
	session := NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	if got := session.TTSOutputMode(); got != config.TTSModeAlwaysOn {
		t.Fatalf("expected default mode %q, got %q", config.TTSModeAlwaysOn, got)
	}
	if !session.IsTTSSpeakingActive() {
		t.Fatal("expected speaking active in always_on mode")
	}

	// Cycle: always_on -> auto_idle
	if next := session.CycleTTSOutputMode(); next != config.TTSModeAutoIdle {
		t.Fatalf("expected %q, got %q", config.TTSModeAutoIdle, next)
	}
	// Cycle: auto_idle -> always_off
	if next := session.CycleTTSOutputMode(); next != config.TTSModeAlwaysOff {
		t.Fatalf("expected %q, got %q", config.TTSModeAlwaysOff, next)
	}
	if session.IsTTSSpeakingActive() {
		t.Fatal("expected speaking inactive in always_off mode")
	}
	// Cycle: always_off -> always_on
	if next := session.CycleTTSOutputMode(); next != config.TTSModeAlwaysOn {
		t.Fatalf("expected %q, got %q", config.TTSModeAlwaysOn, next)
	}
	if !session.IsTTSSpeakingActive() {
		t.Fatal("expected speaking active after cycling back to always_on")
	}
}

func TestSessionAutoIdleTransitionsAndFlush(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TTS.OutputMode = config.TTSModeAutoIdle
	cfg.TTS.IdleTimeoutSeconds = 15

	session := NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	idleMock := &mockIdleProvider{dur: 2 * time.Second}
	session.SetIdleProvider(idleMock.IdleDuration)

	// Attach a paused queue so we can inspect queued items without audio synthesis
	q := session.Queue()
	q.Pause()

	// 1. User is At Computer (idle = 2s < 15s)
	session.EvaluateIdleState()
	if session.IsUserIdle() {
		t.Fatal("expected IsUserIdle() == false when idle duration is 2s")
	}
	if session.IsTTSSpeakingActive() {
		t.Fatal("expected IsTTSSpeakingActive() == false when At Computer in auto_idle mode")
	}

	// Dispatch a chat event while At Computer -> should NOT enqueue to TTS queue
	if err := session.DispatchEvent(&chat.Event{
		ID:       "msg-at-computer",
		Platform: "twitch",
		Kind:     chat.EventKindMessage,
		UserName: "alice",
		Text:     "hello while streamer is typing",
	}); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if q.Depth() != 0 {
		t.Fatalf("expected queue depth 0 while At Computer, got %d", q.Depth())
	}

	// 2. User goes AFK (idle = 16s >= 15s)
	idleMock.SetIdle(16 * time.Second)
	session.EvaluateIdleState()
	if !session.IsUserIdle() {
		t.Fatal("expected IsUserIdle() == true when idle duration is 16s")
	}
	if !session.IsTTSSpeakingActive() {
		t.Fatal("expected IsTTSSpeakingActive() == true when AFK in auto_idle mode")
	}

	// Dispatch two chat events while AFK -> both should be enqueued
	if err := session.DispatchEvent(&chat.Event{
		ID:       "msg-afk-1",
		Platform: "twitch",
		Kind:     chat.EventKindMessage,
		UserName: "bob",
		Text:     "first message while AFK",
	}); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if err := session.DispatchEvent(&chat.Event{
		ID:       "msg-afk-2",
		Platform: "twitch",
		Kind:     chat.EventKindMessage,
		UserName: "charlie",
		Text:     "second message while AFK",
	}); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if q.Depth() != 2 {
		t.Fatalf("expected queue depth 2 while AFK, got %d", q.Depth())
	}

	// 3. User touches mouse/keyboard (idle drops to 100ms) -> pending queue items must be flushed!
	idleMock.SetIdle(100 * time.Millisecond)
	session.EvaluateIdleState()
	if session.IsUserIdle() {
		t.Fatal("expected IsUserIdle() == false when user returns")
	}
	if session.IsTTSSpeakingActive() {
		t.Fatal("expected IsTTSSpeakingActive() == false when user returns")
	}
	if q.Depth() != 0 {
		t.Fatalf("expected pending queue items to be flushed on activity, got depth %d", q.Depth())
	}

	// 4. Test manual switch from Always On -> Always Off also flushes pending queue items
	if err := session.SetTTSOutputMode(config.TTSModeAlwaysOn); err != nil {
		t.Fatalf("SetTTSOutputMode failed: %v", err)
	}
	_ = q.Enqueue(&queue.Item{
		ID:       "manual-1",
		Text:     "queued item",
		Priority: 1,
	})
	if q.Depth() != 1 {
		t.Fatalf("expected depth 1, got %d", q.Depth())
	}
	if err := session.SetTTSOutputMode(config.TTSModeAlwaysOff); err != nil {
		t.Fatalf("SetTTSOutputMode failed: %v", err)
	}
	if q.Depth() != 0 {
		t.Fatalf("expected queue flushed when switching to always_off, got %d", q.Depth())
	}
}
