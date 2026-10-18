package queue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

type mockPlayer struct {
	mu        sync.Mutex
	playing   atomic.Bool
	stopCount atomic.Int32
	samples   []float32
}

func (m *mockPlayer) Play(samples []float32, sampleRate int) error {
	m.mu.Lock()
	m.samples = samples
	m.mu.Unlock()
	return nil
}

func (m *mockPlayer) Stop() {
	m.stopCount.Add(1)
	m.playing.Store(false)
}

func (m *mockPlayer) PanicStop()  { m.Stop() }
func (m *mockPlayer) ResetPanic() { m.playing.Store(false) }
func (m *mockPlayer) SwitchDevice(dev string) error { return nil }
func (m *mockPlayer) SetVolume(vol float32) {}
func (m *mockPlayer) Volume() float32       { return 1.0 }
func (m *mockPlayer) IsPlaying() bool       { return m.playing.Load() }
func (m *mockPlayer) Close() error          { return nil }
func (m *mockPlayer) SetFallbackListener(fn func(reason string)) {}

type mockEngine struct {
	mu           sync.Mutex
	synthesized  []string
	speeds       []float32
	synthLatency time.Duration
}

func (m *mockEngine) Synthesize(text string, voiceID string, speed float32) ([]float32, int, error) {
	m.mu.Lock()
	m.synthesized = append(m.synthesized, text)
	m.speeds = append(m.speeds, speed)
	lat := m.synthLatency
	m.mu.Unlock()

	if lat > 0 {
		time.Sleep(lat)
	}

	return []float32{0.1, 0.2, 0.3}, 24000, nil
}

func (m *mockEngine) Voices() []string { return []string{"af_bella"} }
func (m *mockEngine) NumSpeakers() int { return 1 }
func (m *mockEngine) Close() error     { return nil }

func TestQueuePriority(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{}
	cfg := config.DefaultConfig()

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	// Pause queue while filling to test dequeue order
	q.Pause()

	// 1. Enqueue normal chat message first
	_ = q.Enqueue(&Item{
		ID:       "chat-1",
		Text:     "chat message 1",
		Priority: 1,
	})

	// 2. Enqueue event message second
	_ = q.Enqueue(&Item{
		ID:       "sub-1",
		Text:     "user subscribed!",
		Priority: 0,
	})

	if q.Depth() != 2 {
		t.Fatalf("expected depth 2, got %d", q.Depth())
	}

	// Resume queue and let it process
	var startedOrder []string
	var mu sync.Mutex
	done := make(chan struct{})

	q.SetOnItemFinished(func(item *Item) {
		mu.Lock()
		startedOrder = append(startedOrder, item.ID)
		if len(startedOrder) == 2 {
			close(done)
		}
		mu.Unlock()
		player.Stop() // simulate immediate playback finish
	})

	q.Resume()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for items to process")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(startedOrder) != 2 {
		t.Fatalf("expected 2 items processed, got %d", len(startedOrder))
	}
	// Sub (Priority 0) should be processed BEFORE chat (Priority 1)
	if startedOrder[0] != "sub-1" || startedOrder[1] != "chat-1" {
		t.Errorf("expected [sub-1, chat-1], got %v", startedOrder)
	}
}

func TestQueueStalenessD30(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{}
	cfg := config.DefaultConfig()
	cfg.Queue.MaxAgeSeconds = 1 // 1 second for fast test

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	q.Pause()

	// Enqueue old chat message (2 seconds ago)
	_ = q.Enqueue(&Item{
		ID:         "stale-chat",
		Text:       "this chat is stale",
		Priority:   1,
		EnqueuedAt: time.Now().Add(-2 * time.Second),
	})

	// Enqueue old event message (2 seconds ago) - should NEVER be dropped
	_ = q.Enqueue(&Item{
		ID:         "old-event",
		Text:       "this event is old but preserved",
		Priority:   0,
		EnqueuedAt: time.Now().Add(-2 * time.Second),
	})

	// Enqueue fresh chat message
	_ = q.Enqueue(&Item{
		ID:         "fresh-chat",
		Text:       "this chat is fresh",
		Priority:   1,
		EnqueuedAt: time.Now(),
	})

	var finished []string
	var mu sync.Mutex
	done := make(chan struct{})

	q.SetOnItemFinished(func(item *Item) {
		mu.Lock()
		finished = append(finished, item.ID)
		if len(finished) == 2 {
			close(done)
		}
		mu.Unlock()
		player.Stop()
	})

	q.Resume()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queue processing")
	}

	mu.Lock()
	defer mu.Unlock()
	// Stale chat should have been dropped! Only old-event and fresh-chat should finish.
	for _, id := range finished {
		if id == "stale-chat" {
			t.Errorf("stale chat message was processed when it should have been dropped")
		}
	}
	if len(finished) != 2 {
		t.Errorf("expected 2 finished items, got %d: %v", len(finished), finished)
	}
}

func TestQueueModerationCancel(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{synthLatency: 50 * time.Millisecond}
	cfg := config.DefaultConfig()

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	// Enqueue messages
	_ = q.Enqueue(&Item{
		ID:       "msg-1",
		Event:    &chat.Event{Platform: "twitch", UserID: "bad_user", ID: "msg-1"},
		Text:     "bad message 1",
		Priority: 1,
	})
	_ = q.Enqueue(&Item{
		ID:       "msg-2",
		Event:    &chat.Event{Platform: "twitch", UserID: "bad_user", ID: "msg-2"},
		Text:     "bad message 2",
		Priority: 1,
	})
	_ = q.Enqueue(&Item{
		ID:       "msg-3",
		Event:    &chat.Event{Platform: "twitch", UserID: "good_user", ID: "msg-3"},
		Text:     "good message",
		Priority: 1,
	})

	// Moderation ban: CancelUser for bad_user
	q.CancelUser("twitch", "bad_user")

	if q.Depth() != 1 {
		t.Errorf("expected depth 1 after user cancellation, got %d", q.Depth())
	}

	// Cancel remaining message by ID
	q.CancelMessage("msg-3")
	if q.Depth() != 0 {
		t.Errorf("expected depth 0 after message cancellation, got %d", q.Depth())
	}
}

func TestQueuePanicMute(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{}
	cfg := config.DefaultConfig()

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	q.Pause()

	for i := range 5 {
		_ = q.Enqueue(&Item{
			ID:       string(rune('a' + i)),
			Text:     "message",
			Priority: 1,
		})
	}

	if q.Depth() != 5 {
		t.Fatalf("expected depth 5, got %d", q.Depth())
	}

	q.Panic()

	if !q.IsPanicked() {
		t.Error("expected queue to be in panicked state after Panic()")
	}
	if q.Depth() != 0 {
		t.Errorf("expected depth 0 after Panic, got %d", q.Depth())
	}
	if player.stopCount.Load() == 0 {
		t.Errorf("expected player.Stop() to be called on Panic")
	}

	// Items enqueued while panicked should be ignored
	_ = q.Enqueue(&Item{ID: "ignored", Text: "should drop", Priority: 1})
	if q.Depth() != 0 {
		t.Errorf("expected item enqueued during panic to be dropped, got depth %d", q.Depth())
	}

	// TogglePanic should clear panic state
	if q.TogglePanic() {
		t.Error("expected TogglePanic to return false when clearing panic")
	}
	if q.IsPanicked() || q.IsPaused() {
		t.Error("expected queue to be unpanicked and unpaused after TogglePanic")
	}
}

func TestQueueFastCatchup(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{}
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultSpeed = 1.2
	cfg.Queue.CatchupThreshold = 5
	cfg.Queue.CatchupSpeed = 1.3

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	q.Pause()

	// Enqueue 6 items (exceeds threshold 5 when first item is popped, leaving 5 pending)
	// Leave Speed: 0 so it inherits DefaultSpeed (1.2) and multiplies by CatchupSpeed (1.3) -> 1.56
	for i := range 6 {
		_ = q.Enqueue(&Item{
			ID:       string(rune('1' + i)),
			Text:     "message",
			Speed:    0,
			Priority: 1,
		})
	}

	var startedSpeed float32
	done := make(chan struct{})
	var once sync.Once
	q.SetOnItemStarted(func(item *Item) {
		once.Do(func() {
			startedSpeed = item.Speed
		})
	})
	q.SetOnItemFinished(func(item *Item) {
		select {
		case <-done:
		default:
			close(done)
		}
	})

	q.Resume()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for catchup item")
	}

	engine.mu.Lock()
	var firstSpeed float32
	if len(engine.speeds) > 0 {
		firstSpeed = engine.speeds[0]
	}
	engine.mu.Unlock()

	// Should have 1.2 * 1.3 = 1.56 speed
	if firstSpeed < 1.55 || firstSpeed > 1.57 {
		t.Errorf("expected fast catchup speed ~1.56 (1.2 * 1.3), got %f", firstSpeed)
	}
	if startedSpeed < 1.55 || startedSpeed > 1.57 {
		t.Errorf("expected onItemStarted item.Speed ~1.56, got %f", startedSpeed)
	}
}

func TestQueuePauseResume(t *testing.T) {
	player := &mockPlayer{}
	engine := &mockEngine{}
	cfg := config.DefaultConfig()

	q := NewQueue(player, engine, func() *config.Config { return cfg })
	defer q.Close()

	q.Pause()
	if !q.IsPaused() {
		t.Error("expected queue to be paused")
	}

	_ = q.Enqueue(&Item{
		ID:       "msg-1",
		Text:     "test",
		Priority: 1,
	})

	// Wait a bit, nothing should be processed while paused
	time.Sleep(50 * time.Millisecond)
	if q.Depth() != 1 {
		t.Errorf("expected item to remain queued while paused, got depth %d", q.Depth())
	}

	// Resume
	done := make(chan struct{})
	q.SetOnItemFinished(func(item *Item) {
		close(done)
	})

	q.Resume()
	if q.IsPaused() {
		t.Error("expected queue to not be paused after resume")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for item after resume")
	}
}

type mockStreamingEngine struct {
	mockEngine
	clauses [][]float32
}

func (m *mockStreamingEngine) SynthesizeStream(text string, voiceID string, speed float32, onChunk tts.StreamCallback) error {
	m.mu.Lock()
	m.synthesized = append(m.synthesized, text)
	m.speeds = append(m.speeds, speed)
	chunks := m.clauses
	m.mu.Unlock()

	for _, c := range chunks {
		if !onChunk(c, 24000) {
			break
		}
	}
	return nil
}

func TestQueueStreamingClauses(t *testing.T) {
	player := &mockPlayer{}
	sEng := &mockStreamingEngine{
		clauses: [][]float32{
			{0.1, 0.2},
			{0.3, 0.4},
			{0.5, 0.6},
		},
	}
	cfg := config.DefaultConfig()

	q := NewQueue(player, sEng, func() *config.Config { return cfg })
	defer q.Close()

	done := make(chan struct{})
	q.SetOnItemFinished(func(item *Item) {
		close(done)
	})

	_ = q.Enqueue(&Item{
		ID:       "stream-1",
		Text:     "Clause one, clause two, clause three.",
		Priority: 1,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streaming item")
	}

	player.mu.Lock()
	lastSamples := player.samples
	player.mu.Unlock()
	if len(lastSamples) != 6 || lastSamples[0] != 0.1 || lastSamples[5] != 0.6 {
		t.Errorf("expected pre-buffered streamed clauses [0.1..0.6] (len 6), got %v", lastSamples)
	}
}

