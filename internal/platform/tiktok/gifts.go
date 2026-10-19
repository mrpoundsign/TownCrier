package tiktok

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/steampoweredtaco/gotiktoklive"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

const DefaultGiftDebounceWindow = 2 * time.Second

// Known fallback icon URLs for common TikTok gifts when room GiftInfo is unavailable.
const (
	DefaultHeartMeGiftID  int64 = 7934
	DefaultHeartMeIconURL       = "https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/d56945782445b0b8c8658ed44f894c7b~tplv-obj.webp"
	DefaultRoseGiftID     int64 = 5655
	DefaultRoseIconURL          = "https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/eba3a9bb85c33e017f3648eaf88d7189~tplv-obj.webp"
)

// GiftCatalogEntry holds metadata and icon URL for a TikTok gift.
type GiftCatalogEntry struct {
	ID       int64
	Name     string
	Diamonds int
	ImageURL string
}

// GiftCatalog caches TikTok gift definitions by ID and case-insensitive name.
type GiftCatalog struct {
	mu     sync.RWMutex
	byID   map[int64]GiftCatalogEntry
	byName map[string]GiftCatalogEntry
}

// NewGiftCatalog creates a GiftCatalog pre-populated with common fallback gifts (Heart Me, Rose).
func NewGiftCatalog() *GiftCatalog {
	c := &GiftCatalog{
		byID:   make(map[int64]GiftCatalogEntry),
		byName: make(map[string]GiftCatalogEntry),
	}
	c.Register(GiftCatalogEntry{
		ID:       DefaultHeartMeGiftID,
		Name:     "Heart Me",
		Diamonds: 1,
		ImageURL: DefaultHeartMeIconURL,
	})
	c.Register(GiftCatalogEntry{
		ID:       DefaultRoseGiftID,
		Name:     "Rose",
		Diamonds: 1,
		ImageURL: DefaultRoseIconURL,
	})
	return c
}

// Register adds or updates a gift entry in the catalog.
func (c *GiftCatalog) Register(entry GiftCatalogEntry) {
	if c == nil {
		return
	}
	entry.Name = strings.TrimSpace(entry.Name)
	entry.ImageURL = strings.TrimSpace(entry.ImageURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.ID > 0 {
		c.byID[entry.ID] = entry
	}
	if entry.Name != "" {
		c.byName[strings.ToLower(entry.Name)] = entry
	}
}

// PopulateFromGiftInfo loads all gifts from gotiktoklive's room GiftInfo response.
func (c *GiftCatalog) PopulateFromGiftInfo(info *gotiktoklive.GiftInfo) {
	if c == nil || info == nil {
		return
	}
	for _, g := range info.Gifts {
		imgURL := firstNonEmptyURL(g.Icon.URLList)
		if imgURL == "" {
			imgURL = firstNonEmptyURL(g.Image.URLList)
		}
		if g.ID <= 0 && g.Name == "" {
			continue
		}
		c.Register(GiftCatalogEntry{
			ID:       int64(g.ID),
			Name:     g.Name,
			Diamonds: g.DiamondCount,
			ImageURL: imgURL,
		})
	}
}

// Lookup resolves a gift by ID first, then by case-insensitive name.
func (c *GiftCatalog) Lookup(id int64, name string) (GiftCatalogEntry, bool) {
	if c == nil {
		return GiftCatalogEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if id > 0 {
		if e, ok := c.byID[id]; ok {
			return e, true
		}
	}
	cleanName := strings.ToLower(strings.TrimSpace(name))
	if cleanName != "" {
		if e, ok := c.byName[cleanName]; ok {
			return e, true
		}
	}
	return GiftCatalogEntry{}, false
}

func firstNonEmptyURL(urls []string) string {
	for _, u := range urls {
		trimmed := strings.TrimSpace(u)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

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
	ImageURL     string
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
	var spans []chat.EmoteSpan
	if imgURL := strings.TrimSpace(in.ImageURL); imgURL != "" {
		spans = []chat.EmoteSpan{
			{
				Start: 0,
				End:   utf8.RuneCountInString(in.GiftName),
				ID:    strconv.FormatInt(in.GiftID, 10),
				Name:  in.GiftName,
				URL:   imgURL,
			},
		}
	}
	return &chat.Event{
		ID:           msgID,
		Platform:     "tiktok",
		Kind:         chat.EventKindGift,
		Timestamp:    in.Timestamp,
		UserID:       in.UserID,
		UserName:     in.UserName,
		DisplayName:  in.DisplayName,
		Roles:        in.Roles,
		FanClubLevel: in.FanClubLevel,
		Text:         in.GiftName,
		ItemName:     in.GiftName,
		RewardTitle:  in.GiftName,
		EmoteSpans:   spans,
		Count:        count,
		UnitAmount:   float64(in.UnitDiamonds),
		Amount:       totalDiamonds,
		Currency:     "Diamonds",
	}
}

