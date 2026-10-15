package queue

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

var (
	ErrQueueClosed = errors.New("speech queue is closed")
)

// Item represents an enqueued speech payload ready for synthesis and playback.
type Item struct {
	ID         string
	Event      *chat.Event
	Text       string
	VoiceID    string
	Speed      float32
	EnqueuedAt time.Time
	Priority   int // 0 = High (Paid/Events), 1 = Normal (Chat)
}

// Queue coordinates the two-tier priority speech queue, staleness check, and audio dispatch.
type Queue struct {
	mu          sync.Mutex
	cond        *sync.Cond
	eventsQueue []*Item
	chatQueue   []*Item

	player audio.Player
	engine tts.Engine
	cfgFn  func() *config.Config

	currentItem    *Item
	currentAborted bool
	stopPlaybackCh chan struct{}

	paused   bool
	panicked bool
	closed   bool

	onItemStarted  func(item *Item)
	onItemFinished func(item *Item)
	onDepthChanged func(depth int)
	onStateChanged func(paused, panicked bool)
}

// NewQueue creates a new Priority Speech Queue and starts the background dispatcher.
func NewQueue(player audio.Player, engine tts.Engine, cfgFn func() *config.Config) *Queue {
	if cfgFn == nil {
		defaultCfg := config.DefaultConfig()
		cfgFn = func() *config.Config { return defaultCfg }
	}

	q := &Queue{
		player:         player,
		engine:         engine,
		cfgFn:          cfgFn,
		stopPlaybackCh: make(chan struct{}, 1),
	}
	q.cond = sync.NewCond(&q.mu)

	go q.dispatcherLoop()

	return q
}

// Enqueue adds an item to the appropriate priority tier (0 for paid/events, 1 for chat).
func (q *Queue) Enqueue(item *Item) error {
	if item == nil {
		return errors.New("cannot enqueue nil item")
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	// While Panic Mode is latched active, silently drop incoming speech items
	if q.panicked {
		return nil
	}

	if item.EnqueuedAt.IsZero() {
		item.EnqueuedAt = time.Now()
	}

	if item.Priority == 0 {
		q.eventsQueue = append(q.eventsQueue, item)
	} else {
		q.chatQueue = append(q.chatQueue, item)
	}

	depth := len(q.eventsQueue) + len(q.chatQueue)
	if q.onDepthChanged != nil {
		fn := q.onDepthChanged
		go fn(depth)
	}

	q.cond.Signal()
	return nil
}

// Depth returns the current total count of pending items in both tiers.
func (q *Queue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.eventsQueue) + len(q.chatQueue)
}

// CurrentItem returns the item currently being synthesized or spoken, if any.
func (q *Queue) CurrentItem() *Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.currentItem
}

// Pause pauses the queue; the current item finishes speaking, but subsequent items wait.
func (q *Queue) Pause() {
	q.mu.Lock()
	q.paused = true
	fn := q.onStateChanged
	panicked := q.panicked
	q.mu.Unlock()

	if fn != nil {
		go fn(true, panicked)
	}
}

// Resume unpauses the queue (and clears Panic Mode if active) and wakes up the dispatcher.
func (q *Queue) Resume() {
	q.mu.Lock()
	q.paused = false
	wasPanicked := q.panicked
	q.panicked = false
	if wasPanicked && q.player != nil {
		q.player.ResetPanic()
	}
	fn := q.onStateChanged
	q.cond.Signal()
	q.mu.Unlock()

	if fn != nil {
		go fn(false, false)
	}
}

// IsPaused returns true if the queue is paused.
func (q *Queue) IsPaused() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.paused
}

// IsPanicked returns true if Emergency Panic Mute mode is currently latched active.
func (q *Queue) IsPanicked() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.panicked
}

// Skip aborts the currently speaking audio and moves to the next queued item.
func (q *Queue) Skip() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.abortCurrentLocked()
}

// Clear flushes all pending items in both tiers without interrupting current speech.
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.eventsQueue = q.eventsQueue[:0]
	q.chatQueue = q.chatQueue[:0]

	if q.onDepthChanged != nil {
		fn := q.onDepthChanged
		go fn(0)
	}
}

// Panic executes Emergency Panic Mute (<50ms, D28): latches Panic Mode, flushes all queues, and halts audio instantly.
func (q *Queue) Panic() {
	q.mu.Lock()
	q.panicked = true
	q.paused = true
	q.eventsQueue = q.eventsQueue[:0]
	q.chatQueue = q.chatQueue[:0]

	depthFn := q.onDepthChanged
	stateFn := q.onStateChanged

	q.abortCurrentLocked()
	q.mu.Unlock()

	if depthFn != nil {
		go depthFn(0)
	}
	if stateFn != nil {
		go stateFn(true, true)
	}
}

// ClearPanic unlatches Panic Mode and resumes normal queue processing.
func (q *Queue) ClearPanic() {
	q.Resume()
}

// TogglePanic toggles Emergency Panic Mute mode on or off, returning the new panicked state.
func (q *Queue) TogglePanic() bool {
	if q.IsPanicked() {
		q.ClearPanic()
		return false
	}
	q.Panic()
	return true
}

// CancelMessage implements Moderation Sync (D29): drops matching message from queue
// or halts playback immediately if currently playing.
func (q *Queue) CancelMessage(targetMessageID string) {
	if targetMessageID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.currentItem != nil && q.currentItem.ID == targetMessageID {
		q.abortCurrentLocked()
	}

	q.eventsQueue = filterOutID(q.eventsQueue, targetMessageID)
	q.chatQueue = filterOutID(q.chatQueue, targetMessageID)

	depth := len(q.eventsQueue) + len(q.chatQueue)
	if q.onDepthChanged != nil {
		fn := q.onDepthChanged
		go fn(depth)
	}
}

// CancelUser implements Moderation Sync (D29): drops all messages from the specified user
// and halts current audio if the user is currently speaking.
func (q *Queue) CancelUser(platform string, targetUserID string) {
	if targetUserID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.currentItem != nil && q.currentItem.Event != nil {
		if q.currentItem.Event.Platform == platform && q.currentItem.Event.UserID == targetUserID {
			q.abortCurrentLocked()
		}
	}

	q.eventsQueue = filterOutUser(q.eventsQueue, platform, targetUserID)
	q.chatQueue = filterOutUser(q.chatQueue, platform, targetUserID)

	depth := len(q.eventsQueue) + len(q.chatQueue)
	if q.onDepthChanged != nil {
		fn := q.onDepthChanged
		go fn(depth)
	}
}

// SetPlayer dynamically updates the audio output player.
func (q *Queue) SetPlayer(player audio.Player) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.player = player
}

// SetEngine dynamically updates the speech synthesis engine.
func (q *Queue) SetEngine(engine tts.Engine) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.engine = engine
}

// SetOnItemStarted registers a callback invoked when an item begins synthesis/speech.
func (q *Queue) SetOnItemStarted(fn func(item *Item)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onItemStarted = fn
}

// SetOnItemFinished registers a callback invoked when an item finishes speaking or is aborted.
func (q *Queue) SetOnItemFinished(fn func(item *Item)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onItemFinished = fn
}

// SetOnQueueDepthChanged registers a callback invoked whenever queue depth changes.
func (q *Queue) SetOnQueueDepthChanged(fn func(depth int)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onDepthChanged = fn
}

// SetOnStateChanged registers a callback invoked whenever paused or panicked state changes.
func (q *Queue) SetOnStateChanged(fn func(paused, panicked bool)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onStateChanged = fn
}

// Close halts the dispatcher and releases resources.
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.abortCurrentLocked()
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *Queue) abortCurrentLocked() {
	q.currentAborted = true
	if q.player != nil {
		q.player.Stop()
	}
	select {
	case q.stopPlaybackCh <- struct{}{}:
	default:
	}
}

// dispatcherLoop processes queued items sequentially with priority and staleness protection.
func (q *Queue) dispatcherLoop() {
	for {
		item, depth, ok := q.popNext()
		if !ok {
			return
		}

		if item == nil {
			continue
		}

		q.processItem(item, depth)
	}
}

// popNext retrieves the next eligible item according to priority and staleness (D30).
func (q *Queue) popNext() (*Item, int, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		if q.closed {
			return nil, 0, false
		}

		if q.paused {
			q.cond.Wait()
			continue
		}

		cfg := q.cfgFn()
		maxAge := 30 * time.Second
		if cfg != nil && cfg.Queue.MaxAgeSeconds > 0 {
			maxAge = time.Duration(cfg.Queue.MaxAgeSeconds) * time.Second
		}

		// 1. Pop from Events / Paid tier (Tier 0) - NEVER dropped for staleness
		if len(q.eventsQueue) > 0 {
			item := q.eventsQueue[0]
			q.eventsQueue = q.eventsQueue[1:]
			q.currentItem = item
			q.currentAborted = false
			depth := len(q.eventsQueue) + len(q.chatQueue)
			if q.onDepthChanged != nil {
				fn := q.onDepthChanged
				go fn(depth)
			}
			return item, depth, true
		}

		// 2. Pop from Chat tier (Tier 1) - Dropped if older than maxAge (D30)
		if len(q.chatQueue) > 0 {
			item := q.chatQueue[0]
			q.chatQueue = q.chatQueue[1:]

			// Staleness check (D30)
			if time.Since(item.EnqueuedAt) > maxAge {
				// Drop stale chat message
				depth := len(q.eventsQueue) + len(q.chatQueue)
				if q.onDepthChanged != nil {
					fn := q.onDepthChanged
					go fn(depth)
				}
				continue
			}

			q.currentItem = item
			q.currentAborted = false
			depth := len(q.eventsQueue) + len(q.chatQueue)
			if q.onDepthChanged != nil {
				fn := q.onDepthChanged
				go fn(depth)
			}
			return item, depth, true
		}

		// Nothing to process, wait for new items
		q.cond.Wait()
	}
}

// processItem handles TTS synthesis, fast catchup speed adjustment, and audio playback.
func (q *Queue) processItem(item *Item, depth int) {
	q.mu.Lock()
	onStarted := q.onItemStarted
	onFinished := q.onItemFinished
	engine := q.engine
	player := q.player
	cfg := q.cfgFn()
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		q.currentItem = nil
		q.currentAborted = false
		q.mu.Unlock()

		if onFinished != nil {
			onFinished(item)
		}
	}()

	// Calculate base speed, falling back to GlobalChatRules.DefaultSpeed (or 1.0)
	speed := item.Speed
	if speed <= 0 {
		if cfg != nil && cfg.GlobalChatRules.DefaultSpeed > 0 {
			speed = cfg.GlobalChatRules.DefaultSpeed
		} else {
			speed = 1.0
		}
	}

	// Apply Fast Catchup multiplier (default 1.3x of base/default speed) if pending queue depth meets threshold
	catchupThreshold := 5
	var catchupSpeed float32 = 1.3
	if cfg != nil {
		if cfg.Queue.CatchupThreshold > 0 {
			catchupThreshold = cfg.Queue.CatchupThreshold
		}
		if cfg.Queue.CatchupSpeed > 0 {
			catchupSpeed = cfg.Queue.CatchupSpeed
		}
	}

	if depth >= catchupThreshold {
		speed *= catchupSpeed
	}
	item.Speed = speed

	if onStarted != nil {
		onStarted(item)
	}

	if item.Text == "" {
		return
	}

	// Synthesize speech via TTS engine
	if engine == nil {
		log.Printf("[Queue] Synthesis skipped: TTS engine is nil")
		return
	}

	log.Printf("[Queue] Synthesizing speech for %q (voice=%s, speed=%.2f)...", item.Text, item.VoiceID, speed)
	samples, sampleRate, err := engine.Synthesize(item.Text, item.VoiceID, speed)
	if err != nil || len(samples) == 0 {
		log.Printf("[Queue] Synthesis failed or returned empty samples: err=%v, sampleCount=%d", err, len(samples))
		return
	}

	// Check if aborted during synthesis
	q.mu.Lock()
	if q.currentAborted || q.closed {
		q.mu.Unlock()
		log.Printf("[Queue] Playback aborted: item was cancelled during synthesis")
		return
	}
	q.mu.Unlock()

	// Play via audio player
	if player == nil {
		log.Printf("[Queue] Playback skipped: audio player is nil")
		return
	}

	log.Printf("[Queue] Playing audio (%d samples at %d Hz)...", len(samples), sampleRate)
	if err := player.Play(samples, sampleRate); err != nil {
		log.Printf("[Queue] Player.Play failed: %v", err)
		return
	}

	// Wait for playback completion or interruption
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		q.mu.Lock()
		aborted := q.currentAborted || q.closed
		q.mu.Unlock()

		if aborted || !player.IsPlaying() {
			break
		}

		select {
		case <-q.stopPlaybackCh:
			return
		case <-ticker.C:
		}
	}
}

func filterOutID(slice []*Item, id string) []*Item {
	filtered := make([]*Item, 0, len(slice))
	for _, it := range slice {
		if it.ID != id {
			filtered = append(filtered, it)
		}
	}
	return filtered
}

func filterOutUser(slice []*Item, platform string, userID string) []*Item {
	filtered := make([]*Item, 0, len(slice))
	for _, it := range slice {
		if it.Event != nil && it.Event.Platform == platform && it.Event.UserID == userID {
			continue
		}
		filtered = append(filtered, it)
	}
	return filtered
}
