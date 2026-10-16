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

// NormalizeChannel parses a Kick channel input (slug, @handle, https://kick.com/<slug>,
// numeric chatroom_id, or slug#chatroom_id) into a clean channel label and optional direct chatroom ID.
func NormalizeChannel(raw string) (string, int64) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", 0
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

	// Support explicit "slug#123456" or "slug:123456" syntax
	for _, sep := range []string{"#", ":"} {
		if before, after, ok := strings.Cut(trimmed, sep); ok {
			if id, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil && id > 0 {
				slug := strings.TrimSpace(before)
				if slug == "" {
					slug = strconv.FormatInt(id, 10)
				}
				return slug, id
			}
		}
	}

	// Support direct numeric chatroom ID
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil && id > 0 {
		return trimmed, id
	}

	return strings.ToLower(trimmed), 0
}

// ResolveChatroomID queries Kick's public channel endpoints to resolve a channel slug into its Pusher chatroom_id.
func ResolveChatroomID(ctx context.Context, client *http.Client, apiBaseURL, slug string) (int64, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	base := strings.TrimRight(apiBaseURL, "/")
	if base == "" {
		base = DefaultKickAPIBase
	}

	endpoints := []string{
		fmt.Sprintf("%s/api/v2/channels/%s/chatroom", base, url.PathEscape(slug)),
		fmt.Sprintf("%s/api/v2/channels/%s", base, url.PathEscape(slug)),
		fmt.Sprintf("%s/api/v1/channels/%s", base, url.PathEscape(slug)),
	}

	var lastErr error
	for _, ep := range endpoints {
		id, err := fetchChatroomIDFromEndpoint(ctx, client, ep)
		if err == nil && id > 0 {
			return id, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("could not resolve chatroom_id for Kick channel %q", slug)
}

func fetchChatroomIDFromEndpoint(ctx context.Context, client *http.Client, endpoint string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Referer", "https://kick.com/")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return 0, err
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, err
	}

	// 1. Nested {"chatroom": {"id": 12345}}
	if cr := asMap(m["chatroom"]); len(cr) > 0 {
		if id := int64(asInt(cr["id"])); id > 0 {
			return id, nil
		}
	}

	// 2. Direct {"id": 12345} (from /api/v2/channels/<slug>/chatroom)
	if id := int64(asInt(m["id"])); id > 0 {
		return id, nil
	}

	return 0, errors.New("chatroom id not found in response")
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

	cleanSlug, directID := NormalizeChannel(target)
	if cleanSlug == "" && directID == 0 {
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

	go c.runLoop(ctx, cleanSlug, directID)
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

func (c *Connector) runLoop(ctx context.Context, slug string, directID int64) {
	chatroomID := directID

	for {
		if ctx.Err() != nil {
			return
		}

		c.updateStatus(StatusConnecting, slug)

		if chatroomID <= 0 {
			resolvedID, err := ResolveChatroomID(ctx, c.httpClient, c.apiBaseURL, slug)
			if err != nil {
				log.Printf("[Kick] Failed to resolve chatroom_id for %q: %v (tip: you can also enter slug#chatroom_id)", slug, err)
				if !c.sleepOrCancel(ctx, c.reconnectDelay) {
					return
				}
				continue
			}
			chatroomID = resolvedID
		}

		err := c.connectAndStream(ctx, slug, chatroomID)
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

func (c *Connector) connectAndStream(ctx context.Context, slug string, chatroomID int64) error {
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

	// Send Pusher subscribe command for chatrooms.<id>.v2
	channelTopic := fmt.Sprintf("chatrooms.%d.v2", chatroomID)
	subPayload := map[string]any{
		"event": "pusher:subscribe",
		"data": map[string]string{
			"auth":    "",
			"channel": channelTopic,
		},
	}
	subBytes, _ := json.Marshal(subPayload)
	if err := websocket.Message.Send(conn, string(subBytes)); err != nil {
		return fmt.Errorf("send pusher:subscribe: %w", err)
	}

	log.Printf("[Kick] Connected to Pusher %s for %s", channelTopic, slug)
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

		if ev != nil && c.onEvent != nil {
			c.onEvent(ev)
		}
		if mod != nil && c.onModeration != nil {
			c.onModeration(mod)
		}
	}
}
