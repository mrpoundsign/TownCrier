package tiktok

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

const DefaultGiftDebounceWindow = 2 * time.Second

// GiftInput represents a raw TikTok gift event fed into the streak debouncer.
type GiftInput struct {
	MessageID    string
	UserID       string
	UserName     string
	DisplayName  string
	Roles        []string
	FanClubLevel int
	GiftID       int64
	GiftName     string
	UnitDiamonds int
	RepeatCount  int
	RepeatEnd    bool
	IsStreakable bool
	Timestamp    time.Time
}

type pendingStreak struct {
	input GiftInput
	timer *time.Timer
}

// GiftDebouncer coalesces rapid TikTok gift combo streaks into a single announcement (D33).
// Streakable gifts are flushed immediately when RepeatEnd is true, or after the sliding
// debounce window (default 2s) expires without further increments.
type GiftDebouncer struct {
	mu      sync.Mutex
	window  time.Duration
	onEmit  func(ev *chat.Event)
	pending map[string]*pendingStreak
	closed  bool
}

// NewGiftDebouncer creates a new GiftDebouncer with the given debounce window and callback.
func NewGiftDebouncer(window time.Duration, onEmit func(ev *chat.Event)) *GiftDebouncer {
	if window <= 0 {
		window = DefaultGiftDebounceWindow
	}
	return &GiftDebouncer{
		window:  window,
		onEmit:  onEmit,
		pending: make(map[string]*pendingStreak),
	}
}

// Submit processes an incoming TikTok gift event, either emitting immediately or debouncing a streak.
func (d *GiftDebouncer) Submit(in GiftInput) {
	if in.RepeatCount <= 0 {
		in.RepeatCount = 1
	}
	if in.GiftName == "" {
		in.GiftName = "Gift"
	}
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now()
	}

	if !in.IsStreakable {
		ev := buildGiftChatEvent(in)
		if d.onEmit != nil {
			d.onEmit(ev)
		}
		return
	}

	userKey := in.UserID
	if userKey == "" {
		userKey = in.UserName
	}
	streakKey := userKey + ":" + strconv.FormatInt(in.GiftID, 10)

	var immediateEmit *chat.Event

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}

	existing, exists := d.pending[streakKey]
	if exists {
		if in.RepeatCount >= existing.input.RepeatCount {
			existing.input = in
		} else {
			existing.input.RepeatEnd = in.RepeatEnd
		}
		if in.RepeatEnd {
			existing.timer.Stop()
			finalInput := existing.input
			delete(d.pending, streakKey)
			immediateEmit = buildGiftChatEvent(finalInput)
		} else {
			existing.timer.Reset(d.window)
		}
	} else {
		if in.RepeatEnd {
			immediateEmit = buildGiftChatEvent(in)
		} else {
			ps := &pendingStreak{
				input: in,
			}
			ps.timer = time.AfterFunc(d.window, func() {
				d.flushKey(streakKey)
			})
			d.pending[streakKey] = ps
		}
	}
	d.mu.Unlock()

	if immediateEmit != nil && d.onEmit != nil {
		d.onEmit(immediateEmit)
	}
}

func (d *GiftDebouncer) flushKey(streakKey string) {
	d.mu.Lock()
	ps, ok := d.pending[streakKey]
	if !ok || d.closed {
		d.mu.Unlock()
		return
	}
	finalInput := ps.input
	delete(d.pending, streakKey)
	d.mu.Unlock()

	if d.onEmit != nil {
		d.onEmit(buildGiftChatEvent(finalInput))
	}
}

// Close cancels all active debounce timers without emitting further events.
func (d *GiftDebouncer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for k, ps := range d.pending {
		ps.timer.Stop()
		delete(d.pending, k)
	}
}

func buildGiftChatEvent(in GiftInput) *chat.Event {
	count := in.RepeatCount
	if count <= 0 {
		count = 1
	}
	totalDiamonds := float64(in.UnitDiamonds * count)
	msgID := in.MessageID
	if msgID == "" {
		msgID = fmt.Sprintf("tt-gift-%s-%d-%d", in.UserID, in.GiftID, in.Timestamp.UnixNano())
	}
	return &chat.Event{
		ID:          msgID,
		Platform:    "tiktok",
		Kind:        chat.EventKindGift,
		Timestamp:   in.Timestamp,
		UserID:      in.UserID,
		UserName:     in.UserName,
		DisplayName:  in.DisplayName,
		Roles:        in.Roles,
		FanClubLevel: in.FanClubLevel,
		Text:         in.GiftName,
		Count:       count,
		Amount:      totalDiamonds,
		Currency:    "Diamonds",
	}
}
