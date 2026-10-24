package kick

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

// ConnectionStatus represents the live Kick Pusher connection state.
type ConnectionStatus string

const (
	StatusDisconnected     ConnectionStatus = "disconnected"
	StatusConnecting       ConnectionStatus = "connecting"
	StatusWaitingForStream ConnectionStatus = "waiting"
	StatusConnected        ConnectionStatus = "connected"
)

const (
	DefaultPusherAppKey     = "34bf7a0ff419a2a775b9"
	DefaultPusherWSURL      = "wss://ws-us2.pusher.com/app/" + DefaultPusherAppKey + "?protocol=7&client=js&version=8.4.0-rc2&flash=false"
	DefaultKickAPIBase      = "https://kick.com"
	DefaultFollowerCacheTTL = 30 * time.Minute
	defaultReconnect        = 5 * time.Second
	defaultPingPeriod       = 50 * time.Second
	defaultLivePollPeriod   = 60 * time.Second
	defaultUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36"
)

type followerCacheEntry struct {
	isFollowing bool
	followedAt  time.Time
	checkedAt   time.Time
}

// ChannelInfo holds the resolved Kick Pusher identifiers and live status for a channel.
type ChannelInfo struct {
	ChannelID  int64
	ChatroomID int64
	IsLive     bool
}

// NormalizeChannelInfo parses a Kick channel input (slug, @handle, https://kick.com/<slug>,
// numeric chatroom_id, slug#chatroom_id, or slug#channel_id:chatroom_id) into a clean slug and ChannelInfo.
func NormalizeChannelInfo(raw string) (string, ChannelInfo) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ChannelInfo{}
	}

	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if u, err := url.Parse(trimmed); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && parts[0] != "" {
				trimmed = parts[0]
			}
		}
	}

	trimmed = strings.TrimPrefix(trimmed, "@")

	// Support explicit "slug#channel_id:chatroom_id", "slug#chatroom_id", or "slug:chatroom_id"
	for _, sep := range []string{"#", ":"} {
		if before, after, ok := strings.Cut(trimmed, sep); ok {
			slug := strings.ToLower(strings.TrimSpace(before))
			after = strings.TrimSpace(after)
			if chStr, crStr, hasBoth := strings.Cut(after, ":"); hasBoth {
				chID, err1 := strconv.ParseInt(strings.TrimSpace(chStr), 10, 64)
				crID, err2 := strconv.ParseInt(strings.TrimSpace(crStr), 10, 64)
				if err1 == nil && err2 == nil && crID > 0 {
					if slug == "" {
						slug = strconv.FormatInt(crID, 10)
					}
					return slug, ChannelInfo{ChannelID: chID, ChatroomID: crID}
				}
			}
			if id, err := strconv.ParseInt(after, 10, 64); err == nil && id > 0 {
				if slug == "" {
					slug = strconv.FormatInt(id, 10)
				}
				return slug, ChannelInfo{ChatroomID: id}
			}
		}
	}

	// Support direct numeric chatroom ID
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil && id > 0 {
		return trimmed, ChannelInfo{ChatroomID: id}
	}

	return strings.ToLower(trimmed), ChannelInfo{}
}

// NormalizeChannel parses a Kick channel input into a clean channel label and optional direct chatroom ID.
func NormalizeChannel(raw string) (string, int64) {
	slug, info := NormalizeChannelInfo(raw)
	return slug, info.ChatroomID
}

// ResolveChannelInfo queries Kick's public channel endpoints to resolve a channel slug into its ChannelID and ChatroomID.
func ResolveChannelInfo(ctx context.Context, client *http.Client, apiBaseURL, slug string) (ChannelInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	base := strings.TrimRight(apiBaseURL, "/")
	if base == "" {
		base = DefaultKickAPIBase
	}

	endpoints := []string{
		fmt.Sprintf("%s/api/v2/channels/%s", base, url.PathEscape(slug)),
		fmt.Sprintf("%s/api/v1/channels/%s", base, url.PathEscape(slug)),
		fmt.Sprintf("%s/api/v2/channels/%s/chatroom", base, url.PathEscape(slug)),
	}

	var combined ChannelInfo
	var lastErr error
	for _, ep := range endpoints {
		info, err := fetchChannelInfoFromEndpoint(ctx, client, ep)
		if err != nil {
			lastErr = err
			continue
		}
		if combined.ChannelID <= 0 && info.ChannelID > 0 {
			combined.ChannelID = info.ChannelID
		}
		if combined.ChatroomID <= 0 && info.ChatroomID > 0 {
			combined.ChatroomID = info.ChatroomID
		}
		if info.IsLive {
			combined.IsLive = true
		}
		if combined.ChannelID > 0 && combined.ChatroomID > 0 {
			return combined, nil
		}
	}

	if combined.ChatroomID > 0 {
		return combined, nil
	}
	if lastErr != nil {
		return ChannelInfo{}, lastErr
	}
	return ChannelInfo{}, fmt.Errorf("could not resolve chatroom_id for Kick channel %q", slug)
}

// ResolveChatroomID queries Kick's public channel endpoints to resolve a channel slug into its Pusher chatroom_id.
func ResolveChatroomID(ctx context.Context, client *http.Client, apiBaseURL, slug string) (int64, error) {
	info, err := ResolveChannelInfo(ctx, client, apiBaseURL, slug)
	if err != nil {
		return 0, err
	}
	return info.ChatroomID, nil
}

func fetchChannelInfoFromEndpoint(ctx context.Context, client *http.Client, endpoint string) (ChannelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ChannelInfo{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Referer", "https://kick.com/")

	resp, err := client.Do(req)
	if err != nil {
		return ChannelInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ChannelInfo{}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ChannelInfo{}, err
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ChannelInfo{}, err
	}

	isLive := false
	if ls := asMap(m["livestream"]); len(ls) > 0 {
		if active, ok := ls["is_live"].(bool); ok {
			isLive = active
		} else {
			isLive = true
		}
	} else if active, ok := m["is_live"].(bool); ok {
		isLive = active
	}

	// 1. Nested {"id": <channel_id>, "chatroom": {"id": <chatroom_id>}}
	if cr := asMap(m["chatroom"]); len(cr) > 0 {
		crID := int64(asInt(cr["id"]))
		chID := int64(asInt(m["id"]))
		if chID <= 0 {
			chID = int64(asInt(cr["channel_id"]))
		}
		if crID > 0 {
			return ChannelInfo{ChannelID: chID, ChatroomID: crID, IsLive: isLive}, nil
		}
	}

	// 2. Direct {"id": 12345} (from /api/v2/channels/<slug>/chatroom)
	if id := int64(asInt(m["id"])); id > 0 {
		chID := int64(asInt(m["channel_id"]))
		return ChannelInfo{ChannelID: chID, ChatroomID: id, IsLive: isLive}, nil
	}

	return ChannelInfo{}, errors.New("chatroom id not found in response")
}

// Connector manages Kick live chat and event streaming via Pusher WebSocket (D14).
type Connector struct {
	mu                  sync.Mutex
	httpClient          *http.Client
	apiBaseURL          string
	wsURL               string
	reconnectDelay      time.Duration
	pingPeriod          time.Duration
	livePollPeriod      time.Duration
	followerCacheTTL    time.Duration
	channel             string
	status              ConnectionStatus
	statusDetail        string
	running             bool
	isLive              bool
	cancelFn            context.CancelFunc
	activeWS            *websocket.Conn
	onEvent             func(event *chat.Event)
	onModeration        func(modEvent *chat.ModerationEvent)
	onStatusChange      func(status ConnectionStatus, detail string)
	onStreamStateChange func(live bool)
	checkLiveFn         func(ctx context.Context, slug string) (bool, error)
	checkFollowerFn     func(ctx context.Context, slug, username string) (bool, time.Time, error)
	followerCache       map[string]followerCacheEntry
}

// NewConnector creates a new Kick Pusher WebSocket connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onModeration func(modEvent *chat.ModerationEvent),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	return &Connector{
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		apiBaseURL:       DefaultKickAPIBase,
		wsURL:            DefaultPusherWSURL,
		reconnectDelay:   defaultReconnect,
		pingPeriod:       defaultPingPeriod,
		livePollPeriod:   defaultLivePollPeriod,
		followerCacheTTL: DefaultFollowerCacheTTL,
		status:           StatusDisconnected,
		onEvent:          onEvent,
		onModeration:     onModeration,
		onStatusChange:   onStatusChange,
		followerCache:    make(map[string]followerCacheEntry),
	}
}

// SetCheckFollowerFn overrides the Kick user-following API lookup (used in tests).
func (c *Connector) SetCheckFollowerFn(fn func(ctx context.Context, slug, username string) (bool, time.Time, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkFollowerFn = fn
}

// SetFollowerCacheTTL overrides the per-user follower check cache TTL (default 30 minutes).
func (c *Connector) SetFollowerCacheTTL(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.followerCacheTTL = d
	}
}

// MarkUserFollowing records in the in-memory TTL cache that username is currently following slug.
func (c *Connector) MarkUserFollowing(slug, username string) {
	cleanSlug, _ := NormalizeChannelInfo(slug)
	cleanUser := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if cleanSlug == "" || cleanUser == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.followerCache == nil {
		c.followerCache = make(map[string]followerCacheEntry)
	}
	now := time.Now()
	c.followerCache[cleanSlug+":"+cleanUser] = followerCacheEntry{
		isFollowing: true,
		followedAt:  now,
		checkedAt:   now,
	}
}

// CanCheckFollowers reports whether the connector is active or configured with a custom follower check function.
func (c *Connector) CanCheckFollowers() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running || c.checkFollowerFn != nil
}

// SetOnStreamStateChange registers a callback invoked when the Kick channel transitions between live and offline.
func (c *Connector) SetOnStreamStateChange(fn func(live bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStreamStateChange = fn
}

// SetCheckLiveFn overrides the live stream status check function (for testing).
func (c *Connector) SetCheckLiveFn(fn func(ctx context.Context, slug string) (bool, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkLiveFn = fn
}

// SetLivePollPeriod overrides the periodic live status polling interval.
func (c *Connector) SetLivePollPeriod(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.livePollPeriod = d
	}
}

// IsLive returns whether the Kick channel is currently broadcasting live.
func (c *Connector) IsLive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isLive
}

func (c *Connector) setStreamLive(live bool) {
	c.mu.Lock()
	if (!c.running && live) || c.isLive == live {
		c.mu.Unlock()
		return
	}
	c.isLive = live
	if c.running && c.status != StatusDisconnected {
		if live {
			c.updateStatusLocked(StatusConnected, c.channel)
		} else {
			c.updateStatusLocked(StatusWaitingForStream, c.channel)
		}
	}
	fn := c.onStreamStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(live)
	}
}

func (c *Connector) checkChannelLive(ctx context.Context, slug string) (bool, error) {
	c.mu.Lock()
	fn := c.checkLiveFn
	client := c.httpClient
	base := c.apiBaseURL
	c.mu.Unlock()

	if fn != nil {
		return fn(ctx, slug)
	}
	info, err := ResolveChannelInfo(ctx, client, base, slug)
	if err != nil {
		return false, err
	}
	return info.IsLive, nil
}

// Start begins connecting to the given Kick channel slug, URL, or chatroom ID.
func (c *Connector) Start(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanSlug, directInfo := NormalizeChannelInfo(target)
	if cleanSlug == "" && directInfo.ChatroomID == 0 {
		return errors.New("empty Kick channel")
	}

	if c.running && c.channel == strings.TrimSpace(target) {
		return nil
	}

	c.stopLoopLocked()
	c.channel = strings.TrimSpace(target)
	c.running = true

	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFn = cancel

	go c.runLoop(ctx, cleanSlug, directInfo)
	return nil
}

// Stop disconnects from Kick Pusher WebSocket and halts background loops.
func (c *Connector) Stop() error {
	c.mu.Lock()
	c.running = false
	c.stopLoopLocked()
	wasLive := c.isLive
	c.isLive = false
	streamFn := c.onStreamStateChange
	c.updateStatusLocked(StatusDisconnected, "Stopped")
	c.mu.Unlock()

	if wasLive && streamFn != nil {
		streamFn(false)
	}
	return nil
}

// Status returns the current connection status.
func (c *Connector) Status() ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Channel returns the configured Kick channel target.
func (c *Connector) Channel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channel
}

func (c *Connector) stopLoopLocked() {
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
	if c.activeWS != nil {
		_ = c.activeWS.Close()
		c.activeWS = nil
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
	if c.status == status && c.statusDetail == detail {
		return
	}
	c.status = status
	c.statusDetail = detail
	if fn := c.onStatusChange; fn != nil {
		go fn(status, detail)
	}
}

func (c *Connector) runLoop(ctx context.Context, slug string, directInfo ChannelInfo) {
	info := directInfo
	baseDelay := c.reconnectDelay
	if baseDelay <= 0 {
		baseDelay = defaultReconnect
	}
	maxBackoff := 12 * baseDelay
	if baseDelay >= time.Second && maxBackoff < 60*time.Second {
		maxBackoff = 60 * time.Second
	}
	backoff := baseDelay
	hadFatalError := false

	for {
		if ctx.Err() != nil {
			return
		}

		if !hadFatalError {
			c.updateStatus(StatusConnecting, slug)
		}

		if info.ChatroomID <= 0 {
			resolved, err := ResolveChannelInfo(ctx, c.httpClient, c.apiBaseURL, slug)
			if err != nil {
				log.Printf("[Kick] Failed to resolve chatroom_id for %q: %v (tip: you can also enter slug#chatroom_id)", slug, err)
				c.updateStatus(StatusDisconnected, "Channel lookup failed — enter "+slug+"#chatroom_id")
				if !c.sleepOrCancel(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			info = resolved
		}

		connStart := time.Now()
		err := c.connectAndStream(ctx, slug, info)
		if ctx.Err() != nil {
			return
		}

		if pusherErr, ok := errors.AsType[*PusherError](err); ok && pusherErr.IsFatal() {
			hadFatalError = true
			backoff = maxBackoff
			c.updateStatus(StatusDisconnected, pusherErr.Error())
			log.Printf("[Kick] Fatal Pusher error for %q: %v (backing off for %v)", slug, pusherErr, backoff)
			if !c.sleepOrCancel(ctx, backoff) {
				return
			}
			continue
		}

		hadFatalError = false
		if err != nil {
			log.Printf("[Kick] WebSocket session ended for %q: %v", slug, err)
		}

		if time.Since(connStart) >= 3*baseDelay {
			backoff = baseDelay
		}
		if !c.sleepOrCancel(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (c *Connector) sleepOrCancel(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *Connector) connectAndStream(ctx context.Context, slug string, info ChannelInfo) error {
	wsCfg, err := websocket.NewConfig(c.wsURL, "https://kick.com")
	if err != nil {
		return fmt.Errorf("websocket config: %w", err)
	}
	wsCfg.Header.Set("User-Agent", defaultUserAgent)

	conn, err := websocket.DialConfig(wsCfg)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	c.activeWS = conn
	c.mu.Unlock()

	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		if c.activeWS == conn {
			c.activeWS = nil
		}
		c.mu.Unlock()
	}()

	// 1. Subscribe to chatrooms.<id>.v2 for chat messages & moderation
	chatroomTopic := fmt.Sprintf("chatrooms.%d.v2", info.ChatroomID)
	subPayload := map[string]any{
		"event": "pusher:subscribe",
		"data": map[string]string{
			"auth":    "",
			"channel": chatroomTopic,
		},
	}
	subBytes, _ := json.Marshal(subPayload)
	if err := websocket.Message.Send(conn, string(subBytes)); err != nil {
		return fmt.Errorf("send pusher:subscribe (%s): %w", chatroomTopic, err)
	}

	// 2. Subscribe to channel.<id> for followers, subscriptions, gifted subs, hosts, and gifts
	var chTopic string
	if info.ChannelID > 0 {
		chTopic = fmt.Sprintf("channel.%d", info.ChannelID)
		chSubPayload := map[string]any{
			"event": "pusher:subscribe",
			"data": map[string]string{
				"auth":    "",
				"channel": chTopic,
			},
		}
		chSubBytes, _ := json.Marshal(chSubPayload)
		if err := websocket.Message.Send(conn, string(chSubBytes)); err != nil {
			log.Printf("[Kick] Warning: failed to subscribe to %s: %v", chTopic, err)
			chTopic = ""
		}
	}

	c.mu.Lock()
	pingInterval := c.pingPeriod
	livePollInterval := c.livePollPeriod
	hasCheckFn := c.checkLiveFn != nil
	c.mu.Unlock()

	var loopDone chan struct{}
	defer func() {
		if loopDone != nil {
			close(loopDone)
		}
	}()

	established := false
	ensureEstablished := func() {
		if established {
			return
		}
		established = true
		if chTopic != "" {
			log.Printf("[Kick] Subscribed to Pusher channel events %s for %s", chTopic, slug)
		}
		log.Printf("[Kick] Connected to Pusher %s for %s", chatroomTopic, slug)
		if info.IsLive {
			c.updateStatus(StatusConnected, slug)
			c.setStreamLive(true)
		} else {
			c.updateStatus(StatusWaitingForStream, slug)
		}

		loopDone = make(chan struct{})
		doneCh := loopDone
		go func() {
			ticker := time.NewTicker(pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-doneCh:
					return
				case <-ticker.C:
					_ = websocket.Message.Send(conn, `{"event":"pusher:ping","data":{}}`)
				}
			}
		}()

		go func() {
			if !info.IsLive || hasCheckFn {
				if live, err := c.checkChannelLive(ctx, slug); err == nil {
					c.setStreamLive(live)
				}
			}
			ticker := time.NewTicker(livePollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-doneCh:
					return
				case <-ticker.C:
					if live, err := c.checkChannelLive(ctx, slug); err == nil {
						c.setStreamLive(live)
					}
				}
			}
		}()
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		var rawMsg string
		if err := websocket.Message.Receive(conn, &rawMsg); err != nil {
			return err
		}

		ev, mod, evName, err := ParsePusherFrame([]byte(rawMsg))
		if err != nil {
			if pusherErr, ok := errors.AsType[*PusherError](err); ok {
				return pusherErr
			}
			log.Printf("[Kick] Permissive parse warning: %v", err)
			continue
		}

		ensureEstablished()

		if evName == "pusher:ping" {
			_ = websocket.Message.Send(conn, `{"event":"pusher:pong","data":{}}`)
			continue
		}

		if live, matched := ParseStreamStateEvent(evName); matched {
			c.setStreamLive(live)
		}

		if ev != nil {
			if ev.Channel == "" {
				ev.Channel = slug
			}
			if ev.Kind == chat.EventKindFollow {
				c.MarkUserFollowing(ev.Channel, ev.UserName)
			}
			if c.onEvent != nil {
				c.onEvent(ev)
			}
		}
		if mod != nil {
			if mod.Channel == "" {
				mod.Channel = slug
			}
			if c.onModeration != nil {
				c.onModeration(mod)
			}
		}
	}
}

// CheckUserFollowing checks whether username follows slug on Kick, caching the result per user for 30 minutes (DefaultFollowerCacheTTL).
func (c *Connector) CheckUserFollowing(ctx context.Context, slug, username string) (bool, time.Time, error) {
	cleanSlug, _ := NormalizeChannelInfo(slug)
	cleanUser := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if cleanSlug == "" || cleanUser == "" {
		return false, time.Time{}, nil
	}
	if strings.EqualFold(cleanUser, cleanSlug) {
		return true, time.Time{}, nil
	}

	cacheKey := cleanSlug + ":" + cleanUser

	c.mu.Lock()
	ttl := c.followerCacheTTL
	if ttl <= 0 {
		ttl = DefaultFollowerCacheTTL
	}
	if entry, ok := c.followerCache[cacheKey]; ok && time.Since(entry.checkedAt) < ttl {
		c.mu.Unlock()
		return entry.isFollowing, entry.followedAt, nil
	}
	customFn := c.checkFollowerFn
	client := c.httpClient
	base := strings.TrimRight(c.apiBaseURL, "/")
	c.mu.Unlock()

	var (
		isFollowing bool
		followedAt  time.Time
		err         error
	)

	if customFn != nil {
		isFollowing, followedAt, err = customFn(ctx, cleanSlug, cleanUser)
	} else {
		isFollowing, followedAt, err = queryKickChannelUserFollowing(ctx, client, base, cleanSlug, cleanUser)
	}
	if err != nil {
		return false, time.Time{}, err
	}

	c.mu.Lock()
	if c.followerCache == nil {
		c.followerCache = make(map[string]followerCacheEntry)
	}
	c.followerCache[cacheKey] = followerCacheEntry{
		isFollowing: isFollowing,
		followedAt:  followedAt,
		checkedAt:   time.Now(),
	}
	c.mu.Unlock()

	return isFollowing, followedAt, nil
}

func queryKickChannelUserFollowing(ctx context.Context, client *http.Client, apiBaseURL, slug, username string) (bool, time.Time, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	base := strings.TrimRight(apiBaseURL, "/")
	if base == "" {
		base = DefaultKickAPIBase
	}

	ep := fmt.Sprintf("%s/api/v2/channels/%s/users/%s", base, url.PathEscape(slug), url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
	if err != nil {
		return false, time.Time{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Referer", "https://kick.com/")

	resp, err := client.Do(req)
	if err != nil {
		return false, time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, time.Time{}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, ep)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return false, time.Time{}, err
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false, time.Time{}, err
	}

	if isFol, ok := m["is_following"].(bool); ok && isFol {
		return true, parseKickFollowTime(m), nil
	}
	if fs := asString(m["following_since"]); fs != "" {
		return true, parseKickFollowTime(m), nil
	}
	if fa := asString(m["followed_at"]); fa != "" {
		return true, parseKickFollowTime(m), nil
	}
	return false, time.Time{}, nil
}

func parseKickFollowTime(m map[string]any) time.Time {
	for _, key := range []string{"following_since", "followed_at"} {
		if raw := strings.TrimSpace(asString(m[key])); raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t
			}
		}
	}
	return time.Now().UTC()
}
