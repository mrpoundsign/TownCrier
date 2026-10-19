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
	StatusDisconnected ConnectionStatus = "disconnected"
	StatusConnecting   ConnectionStatus = "connecting"
	StatusConnected    ConnectionStatus = "connected"
)

const (
	DefaultPusherWSURL = "wss://ws-us2.pusher.com/app/32cbd69e4b950bf97679?protocol=7&client=js&version=8.4.0-rc2&flash=false"
	DefaultKickAPIBase = "https://kick.com"
	defaultReconnect   = 5 * time.Second
	defaultPingPeriod  = 50 * time.Second
	defaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36"
)

// ChannelInfo holds the resolved Kick Pusher identifiers for a channel.
type ChannelInfo struct {
	ChannelID  int64
	ChatroomID int64
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

	// 1. Nested {"id": <channel_id>, "chatroom": {"id": <chatroom_id>}}
	if cr := asMap(m["chatroom"]); len(cr) > 0 {
		crID := int64(asInt(cr["id"]))
		chID := int64(asInt(m["id"]))
		if chID <= 0 {
			chID = int64(asInt(cr["channel_id"]))
		}
		if crID > 0 {
			return ChannelInfo{ChannelID: chID, ChatroomID: crID}, nil
		}
	}

	// 2. Direct {"id": 12345} (from /api/v2/channels/<slug>/chatroom)
	if id := int64(asInt(m["id"])); id > 0 {
		chID := int64(asInt(m["channel_id"]))
		return ChannelInfo{ChannelID: chID, ChatroomID: id}, nil
	}

	return ChannelInfo{}, errors.New("chatroom id not found in response")
}

// Connector manages Kick live chat and event streaming via Pusher WebSocket (D14).
type Connector struct {
	mu             sync.Mutex
	httpClient     *http.Client
	apiBaseURL     string
	wsURL          string
	reconnectDelay time.Duration
	pingPeriod     time.Duration
	channel        string
	status         ConnectionStatus
	running        bool
	cancelFn       context.CancelFunc
	activeWS       *websocket.Conn
	onEvent        func(event *chat.Event)
	onModeration   func(modEvent *chat.ModerationEvent)
	onStatusChange func(status ConnectionStatus, detail string)
}

// NewConnector creates a new Kick Pusher WebSocket connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onModeration func(modEvent *chat.ModerationEvent),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	return &Connector{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		apiBaseURL:     DefaultKickAPIBase,
		wsURL:          DefaultPusherWSURL,
		reconnectDelay: defaultReconnect,
		pingPeriod:     defaultPingPeriod,
		status:         StatusDisconnected,
		onEvent:        onEvent,
		onModeration:   onModeration,
		onStatusChange: onStatusChange,
	}
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
	c.status = status
	if fn := c.onStatusChange; fn != nil {
		go fn(status, detail)
	}
}

func (c *Connector) runLoop(ctx context.Context, slug string, directInfo ChannelInfo) {
	info := directInfo

	for {
		if ctx.Err() != nil {
			return
		}

		c.updateStatus(StatusConnecting, slug)

		if info.ChatroomID <= 0 {
			resolved, err := ResolveChannelInfo(ctx, c.httpClient, c.apiBaseURL, slug)
			if err != nil {
				log.Printf("[Kick] Failed to resolve chatroom_id for %q: %v (tip: you can also enter slug#chatroom_id)", slug, err)
				if !c.sleepOrCancel(ctx, c.reconnectDelay) {
					return
				}
				continue
			}
			info = resolved
		}

		err := c.connectAndStream(ctx, slug, info)
		if err != nil && ctx.Err() == nil {
			log.Printf("[Kick] WebSocket session ended for %q: %v", slug, err)
		}

		if !c.sleepOrCancel(ctx, c.reconnectDelay) {
			return
		}
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
	if info.ChannelID > 0 {
		chTopic := fmt.Sprintf("channel.%d", info.ChannelID)
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
		} else {
			log.Printf("[Kick] Subscribed to Pusher channel events %s for %s", chTopic, slug)
		}
	}

	log.Printf("[Kick] Connected to Pusher %s for %s", chatroomTopic, slug)
	c.updateStatus(StatusConnected, slug)

	// Start periodic ping goroutine
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(c.pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingDone:
				return
			case <-ticker.C:
				_ = websocket.Message.Send(conn, `{"event":"pusher:ping","data":{}}`)
			}
		}
	}()

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
			log.Printf("[Kick] Permissive parse warning: %v", err)
			continue
		}

		if evName == "pusher:ping" {
			_ = websocket.Message.Send(conn, `{"event":"pusher:pong","data":{}}`)
			continue
		}

		if ev != nil {
			if ev.Channel == "" {
				ev.Channel = slug
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
