package tiktok

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/steampoweredtaco/gotiktoklive"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

// ConnectionStatus represents the live TikTok connection state.
type ConnectionStatus string

const (
	StatusDisconnected     ConnectionStatus = "disconnected"
	StatusWaitingForStream ConnectionStatus = "waiting"
	StatusConnected        ConnectionStatus = "connected"
)

const defaultStreamWaitPoll = 15 * time.Second

// NormalizeUsername extracts the clean TikTok uniqueId from a @handle, username, or TikTok URL.
func NormalizeUsername(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if u, err := url.Parse(trimmed); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && parts[0] != "" {
				return strings.TrimPrefix(parts[0], "@")
			}
		}
	}
	return strings.TrimPrefix(trimmed, "@")
}

// Connector manages zero-config TikTok Live WebCast tracking and gift streak debouncing.
type Connector struct {
	mu             sync.Mutex
	waitInterval   time.Duration
	debounceWindow time.Duration
	username       string
	status         ConnectionStatus
	running        bool
	cancelFn       context.CancelFunc
	debouncer      *GiftDebouncer
	trackUserFn    func(username string) (<-chan gotiktoklive.Event, func(), error)
	onEvent        func(event *chat.Event)
	onStatusChange func(status ConnectionStatus, detail string)
}

// NewConnector creates a new TikTok Live connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	c := &Connector{
		waitInterval:   defaultStreamWaitPoll,
		debounceWindow: DefaultGiftDebounceWindow,
		status:         StatusDisconnected,
		onEvent:        onEvent,
		onStatusChange: onStatusChange,
	}
	c.trackUserFn = defaultTrackUser
	return c
}

func defaultTrackUser(username string) (<-chan gotiktoklive.Event, func(), error) {
	tt, err := gotiktoklive.NewTikTok()
	if err != nil {
		return nil, nil, err
	}
	tt.SetErrorHandler(func(args ...any) {
		log.Printf("[TikTok] WebCast warning/error: %v", args)
	})
	tt.SetWarnHandler(func(args ...any) {
		log.Printf("[TikTok] WebCast warn: %v", args)
	})

	live, err := tt.TrackUser(username)
	if err != nil {
		return nil, nil, err
	}
	return live.Events, live.Close, nil
}

// Start begins watching the given TikTok @username or live URL.
func (c *Connector) Start(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanUser := NormalizeUsername(target)
	if cleanUser == "" {
		return errors.New("empty TikTok username")
	}

	if c.running && c.username == cleanUser {
		return nil
	}

	c.stopLoopLocked()
	c.username = cleanUser
	c.running = true
	c.debouncer = NewGiftDebouncer(c.debounceWindow, func(ev *chat.Event) {
		if c.onEvent != nil {
			c.onEvent(ev)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFn = cancel

	go c.runLoop(ctx, cleanUser)
	return nil
}

// Stop halts the TikTok Live connector and cancels pending gift timers.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = false
	c.stopLoopLocked()
	c.updateStatusLocked(StatusDisconnected, "Stopped")
	return nil
}

// Status returns the current connection status.
func (c *Connector) Status() ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Username returns the active TikTok username.
func (c *Connector) Username() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.username
}

func (c *Connector) stopLoopLocked() {
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
	if c.debouncer != nil {
		c.debouncer.Close()
		c.debouncer = nil
	}
}

func (c *Connector) updateStatus(status ConnectionStatus, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && status != StatusDisconnected {
		return
	}
	c.updateStatusLocked(status, detail)
}

func (c *Connector) updateStatusLocked(status ConnectionStatus, detail string) {
	c.status = status
	if fn := c.onStatusChange; fn != nil {
		go fn(status, detail)
	}
}

func (c *Connector) runLoop(ctx context.Context, username string) {
	display := "@" + username
	for {
		if ctx.Err() != nil {
			return
		}

		c.updateStatus(StatusWaitingForStream, display)
		eventsCh, closeFn, err := c.trackUserFn(username)
		if err != nil || eventsCh == nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.waitInterval):
				continue
			}
		}

		log.Printf("[TikTok] Connected to live WebCast for %s", display)
		c.updateStatus(StatusConnected, display)

		c.consumeStreamEvents(ctx, eventsCh, closeFn)
	}
}

func (c *Connector) consumeStreamEvents(ctx context.Context, eventsCh <-chan gotiktoklive.Event, closeFn func()) {
	defer func() {
		if closeFn != nil {
			closeFn()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case rawEv, ok := <-eventsCh:
			if !ok {
				return
			}
			if shouldDisconnect := c.handleRawEvent(rawEv); shouldDisconnect {
				return
			}
		}
	}
}

func (c *Connector) handleRawEvent(rawEv gotiktoklive.Event) bool {
	if rawEv == nil {
		return false
	}

	switch e := rawEv.(type) {
	case gotiktoklive.ChatEvent:
		if e.IsHistory() {
			return false
		}
		ev := mapChatEvent(e)
		if ev != nil && c.onEvent != nil {
			c.onEvent(ev)
		}

	case gotiktoklive.GiftEvent:
		if e.IsHistory() {
			return false
		}
		in := mapGiftEvent(e)
		c.mu.Lock()
		deb := c.debouncer
		c.mu.Unlock()
		if deb != nil {
			deb.Submit(in)
		}

	case gotiktoklive.UserEvent:
		if e.IsHistory() {
			return false
		}
		ev := mapUserEvent(e)
		if ev != nil && c.onEvent != nil {
			c.onEvent(ev)
		}

	case gotiktoklive.ControlEvent:
		// Action == 3 indicates the livestream has ended
		if e.Action == 3 {
			return true
		}

	case gotiktoklive.DisconnectEvent:
		return true
	}

	return false
}

func mapChatEvent(e gotiktoklive.ChatEvent) *chat.Event {
	text := strings.TrimSpace(e.Comment)
	if text == "" {
		return nil
	}
	userID, userName, displayName := extractUserFields(e.User)
	roles := extractRoles(e.User, e.UserIdentity)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := "tt-msg-" + strconv.FormatInt(e.MessageID, 10)
	if e.MessageID == 0 {
		msgID = "tt-msg-" + strconv.FormatInt(ts.UnixNano(), 10)
	}

	return &chat.Event{
		ID:          msgID,
		Platform:    "tiktok",
		Kind:        chat.EventKindMessage,
		Timestamp:   ts,
		UserID:      userID,
		UserName:    userName,
		DisplayName: displayName,
		Roles:       roles,
		Text:        text,
	}
}

func mapGiftEvent(e gotiktoklive.GiftEvent) GiftInput {
	userID, userName, displayName := extractUserFields(e.User)
	roles := extractRoles(e.User, e.UserIdentity)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := ""
	if e.MessageID != 0 {
		msgID = "tt-gift-" + strconv.FormatInt(e.MessageID, 10)
	}

	return GiftInput{
		MessageID:    msgID,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		GiftID:       e.ID,
		GiftName:     e.Name,
		UnitDiamonds: e.Diamonds,
		RepeatCount:  e.RepeatCount,
		RepeatEnd:    e.RepeatEnd,
		IsStreakable: e.Type == 1 || e.IsComboGift,
		Timestamp:    ts,
	}
}

func mapUserEvent(e gotiktoklive.UserEvent) *chat.Event {
	if e.Event != gotiktoklive.USER_FOLLOW {
		return nil
	}
	userID, userName, displayName := extractUserFields(e.User)
	roles := extractRoles(e.User, nil)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := "tt-follow-" + strconv.FormatInt(e.MessageID, 10)
	if e.MessageID == 0 {
		msgID = "tt-follow-" + userID + "-" + strconv.FormatInt(ts.UnixNano(), 10)
	}

	return &chat.Event{
		ID:          msgID,
		Platform:    "tiktok",
		Kind:        chat.EventKindFollow,
		Timestamp:   ts,
		UserID:      userID,
		UserName:    userName,
		DisplayName: displayName,
		Roles:       roles,
	}
}

func extractUserFields(u *gotiktoklive.User) (userID, userName, displayName string) {
	if u == nil {
		return "unknown", "Viewer", "Viewer"
	}
	if u.ID != 0 {
		userID = strconv.FormatInt(u.ID, 10)
	}
	userName = strings.TrimSpace(u.Username)
	displayName = strings.TrimSpace(u.Nickname)
	if displayName == "" {
		displayName = userName
	}
	if userName == "" {
		userName = displayName
	}
	if userID == "" {
		userID = userName
	}
	return userID, userName, displayName
}

func extractRoles(u *gotiktoklive.User, ident *gotiktoklive.UserIdentity) []string {
	var roles []string
	seen := make(map[string]struct{})
	add := func(r string) {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			roles = append(roles, r)
		}
	}

	if ident != nil {
		if ident.IsAnchor {
			add("broadcaster")
		}
		if ident.IsModerator {
			add("mod")
		}
		if ident.IsSubscriber {
			add("subscriber")
		}
		if ident.IsGiftGiver {
			add("vip")
		}
	}

	if u != nil && u.Badge != nil {
		for _, b := range u.Badge.Badges {
			if b == nil {
				continue
			}
			lower := strings.ToLower(b.Name + " " + b.Type)
			if strings.Contains(lower, "moderator") || strings.Contains(lower, "admin") {
				add("mod")
			}
			if strings.Contains(lower, "subscriber") || strings.Contains(lower, "member") {
				add("subscriber")
			}
			if strings.Contains(lower, "top_gifter") || strings.Contains(lower, "gifter") {
				add("vip")
			}
		}
	}

	return roles
}

func parseTikTokTimestamp(ts int64) time.Time {
	// TikTok WebCast timestamps may be in milliseconds or seconds
	if ts > 1_000_000_000_000 {
		return time.UnixMilli(ts)
	}
	return time.Unix(ts, 0)
}
