package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

// ConnectionStatus represents the live YouTube connection state.
type ConnectionStatus string

const (
	StatusDisconnected     ConnectionStatus = "disconnected"
	StatusWaitingForStream ConnectionStatus = "waiting"
	StatusConnected        ConnectionStatus = "connected"
)

const (
	defaultUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	innerTubeClientName   = "WEB"
	innerTubeClientVer    = "2.20250101.00.00"
	defaultStreamWaitPoll = 15 * time.Second
)

// Connector manages zero-config YouTube Live stream discovery and InnerTube live chat polling,
// plus optional OAuth 2.0 moderation actions via YouTube Data API v3.
type Connector struct {
	mu               sync.Mutex
	httpClient       *http.Client
	baseURL          string // Overrideable for unit tests (defaults to https://www.youtube.com)
	apiBaseURL       string // Overrideable for unit tests (defaults to https://www.googleapis.com/youtube/v3)
	oauth            *OAuthManager
	activeVideoID    string
	activeLiveChatID string
	waitInterval     time.Duration
	target           string
	status           ConnectionStatus
	running          bool
	cancelFn         context.CancelFunc
	seenIDs          map[string]struct{}
	onEvent          func(event *chat.Event)
	onModeration     func(modEvent *chat.ModerationEvent)
	onStatusChange   func(status ConnectionStatus, detail string)
}

// NewConnector creates a new YouTube Live connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onModeration func(modEvent *chat.ModerationEvent),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	return &Connector{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		baseURL:        "https://www.youtube.com",
		apiBaseURL:     DefaultYouTubeAPIBaseURL,
		waitInterval:   defaultStreamWaitPoll,
		status:         StatusDisconnected,
		seenIDs:        make(map[string]struct{}),
		onEvent:        onEvent,
		onModeration:   onModeration,
		onStatusChange: onStatusChange,
	}
}

// SetOAuthManager binds an OAuthManager to the connector for YouTube Data API v3 moderation.
func (c *Connector) SetOAuthManager(oauth *OAuthManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.oauth = oauth
}

// SetAPIBaseURL overrides the YouTube Data API v3 base URL (used in unit tests).
func (c *Connector) SetAPIBaseURL(apiBaseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if apiBaseURL != "" {
		c.apiBaseURL = strings.TrimRight(apiBaseURL, "/")
	}
}

// Start begins watching the given YouTube handle (@channel), channel ID, or video URL/ID.
func (c *Connector) Start(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return errors.New("empty YouTube channel or video target")
	}

	if c.running && c.target == trimmed {
		return nil
	}

	c.stopLoopLocked()
	c.target = trimmed
	c.running = true
	c.seenIDs = make(map[string]struct{})

	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFn = cancel

	go c.runLoop(ctx, trimmed)
	return nil
}

// Stop halts the YouTube Live connector.
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

// Target returns the active channel handle or URL.
func (c *Connector) Target() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.target
}

// CheckLiveTarget checks whether the specified YouTube target (or configured target if empty) is live.
func (c *Connector) CheckLiveTarget(ctx context.Context, target string) (bool, error) {
	if target == "" {
		c.mu.Lock()
		target = c.target
		c.mu.Unlock()
	}
	if target == "" {
		return false, nil
	}
	if c.Status() == StatusConnected && (c.Target() == "" || strings.EqualFold(c.Target(), target)) {
		return true, nil
	}

	c.mu.Lock()
	baseURL := c.baseURL
	c.mu.Unlock()

	liveURL, display := NormalizeTarget(target)
	if baseURL != "https://www.youtube.com" && !strings.HasPrefix(target, "http") {
		liveURL = strings.TrimRight(baseURL, "/") + "/" + strings.TrimPrefix(display, "@") + "/live"
	}

	apiKey, continuation, err := c.discoverStream(ctx, liveURL)
	if err != nil || apiKey == "" || continuation == "" {
		return false, nil
	}

	if c.Target() == "" || strings.EqualFold(c.Target(), target) {
		c.updateStatus(StatusConnected, display)
	}
	return true, nil
}

// CheckLive performs an immediate check to determine whether the configured YouTube target is live.
func (c *Connector) CheckLive(ctx context.Context) (bool, error) {
	return c.CheckLiveTarget(ctx, "")
}

func (c *Connector) stopLoopLocked() {
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
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

func (c *Connector) runLoop(ctx context.Context, target string) {
	liveURL, display := NormalizeTarget(target)
	if c.baseURL != "https://www.youtube.com" && !strings.HasPrefix(target, "http") {
		liveURL = strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimPrefix(display, "@") + "/live"
	}

	for {
		if ctx.Err() != nil {
			return
		}

		c.updateStatus(StatusWaitingForStream, display)
		apiKey, continuation, err := c.discoverStream(ctx, liveURL)
		if err != nil || apiKey == "" || continuation == "" {
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

		log.Printf("[YouTube] Discovered active live chat for %s", display)
		c.updateStatus(StatusConnected, display)

		c.streamLiveChat(ctx, apiKey, continuation)
	}
}

func (c *Connector) discoverStream(ctx context.Context, liveURL string) (string, string, error) {
	apiKey, continuation, err := c.fetchWatchPageTokens(ctx, liveURL)
	if err == nil && apiKey != "" && continuation != "" {
		return apiKey, continuation, nil
	}

	// If OAuth is connected, attempt authenticated broadcast discovery (detects Unlisted streams,
	// scheduled streams transitioning to live, or multi-stream broadcasts).
	c.mu.Lock()
	hasOAuth := c.oauth != nil && c.oauth.HasToken()
	baseURL := strings.TrimRight(c.baseURL, "/")
	c.mu.Unlock()

	if hasOAuth {
		if videoID, liveChatID, bErr := c.discoverAuthenticatedBroadcast(ctx); bErr == nil && videoID != "" {
			c.mu.Lock()
			c.activeVideoID = videoID
			if liveChatID != "" {
				c.activeLiveChatID = liveChatID
			}
			c.mu.Unlock()

			watchURL := fmt.Sprintf("%s/watch?v=%s", baseURL, videoID)
			if k, cont, wErr := c.fetchWatchPageTokens(ctx, watchURL); wErr == nil && k != "" && cont != "" {
				return k, cont, nil
			}
		}
	}

	return "", "", err
}

func (c *Connector) fetchWatchPageTokens(ctx context.Context, liveURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb"})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return "", "", err
	}

	htmlStr := string(bodyBytes)
	apiKey, continuation, ok := ExtractBootstrapTokens(htmlStr)
	if !ok {
		return "", "", errors.New("stream not currently live or chat disabled")
	}
	if vid, _, found := ExtractVideoIDAndDetails(htmlStr); found && vid != "" {
		c.mu.Lock()
		if c.activeVideoID != vid {
			c.activeVideoID = vid
			c.activeLiveChatID = ""
		}
		c.mu.Unlock()
	}
	return apiKey, continuation, nil
}

func (c *Connector) streamLiveChat(ctx context.Context, apiKey string, initialContinuation string) {
	continuation := initialContinuation
	isFirstPoll := true

	for continuation != "" {
		if ctx.Err() != nil {
			return
		}

		events, modEvents, nextCont, delay, err := c.fetchLiveChatPage(ctx, apiKey, continuation)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[YouTube] Live chat poll error: %v", err)
			return
		}

		targetChan := c.Target()

		// Dispatch moderation events immediately
		for _, modEv := range modEvents {
			if modEv != nil && modEv.Channel == "" {
				modEv.Channel = targetChan
			}
			if c.onModeration != nil {
				c.onModeration(modEv)
			}
		}

		// On the very first poll upon connecting, mark historical replay messages as seen
		// so we don't read out a backlog of old messages from before TownCrier connected,
		// while still reading any brand-new messages arriving on subsequent polls.
		for _, ev := range events {
			if ev == nil {
				continue
			}
			if ev.Channel == "" {
				ev.Channel = targetChan
			}
			c.mu.Lock()
			_, alreadySeen := c.seenIDs[ev.ID]
			if !alreadySeen && ev.ID != "" {
				// Prevent unbounded map growth over multi-hour streams
				if len(c.seenIDs) > 5000 {
					c.seenIDs = make(map[string]struct{})
				}
				c.seenIDs[ev.ID] = struct{}{}
			}
			c.mu.Unlock()

			if alreadySeen {
				continue
			}

			// Skip historical chat messages older than 15 seconds on the initial bootstrap batch
			if isFirstPoll && ev.Kind == chat.EventKindMessage && time.Since(ev.Timestamp) > 15*time.Second {
				continue
			}

			if c.onEvent != nil {
				c.onEvent(ev)
			}
		}

		isFirstPoll = false
		continuation = nextCont
		if continuation == "" {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (c *Connector) fetchLiveChatPage(ctx context.Context, apiKey string, continuation string) ([]*chat.Event, []*chat.ModerationEvent, string, time.Duration, error) {
	endpoint := fmt.Sprintf("%s/youtubei/v1/live_chat/get_live_chat?key=%s&prettyPrint=false", strings.TrimRight(c.baseURL, "/"), apiKey)

	reqPayload := getLiveChatRequest{
		Context: innerTubeContext{
			Client: innerTubeClient{
				ClientName:    innerTubeClientName,
				ClientVersion: innerTubeClientVer,
				Hl:            "en",
				Gl:            "US",
			},
		},
		Continuation: continuation,
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, nil, "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, nil, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", 0, fmt.Errorf("get_live_chat HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, nil, "", 0, err
	}

	return ParseLiveChatResponse(data)
}
