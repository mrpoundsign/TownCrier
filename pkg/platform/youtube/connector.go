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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
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
	defaultRediscoverPoll = 45 * time.Second
	dataAPIStreamMarker   = "__YOUTUBE_DATA_API__"
)

type broadcastWorker struct {
	info StreamInfo
}

type discoveredStream struct {
	info         StreamInfo
	apiKey       string
	continuation string
}

// Connector manages zero-config YouTube Live stream discovery and InnerTube live chat polling,
// supporting multiple concurrent broadcasts per channel (e.g. simultaneous horizontal + vertical streams),
// plus optional OAuth 2.0 moderation actions via YouTube Data API v3.
type Connector struct {
	mu                 sync.Mutex
	httpClient         *http.Client
	baseURL            string // Overrideable for unit tests (defaults to https://www.youtube.com)
	apiBaseURL         string // Overrideable for unit tests (defaults to https://www.googleapis.com/youtube/v3)
	oauth              *OAuthManager
	activeVideoID      string
	activeLiveChatID   string
	activeWorkers      map[string]*broadcastWorker
	msgVideoID         map[string]string
	waitInterval       time.Duration
	rediscoverInterval time.Duration
	target             string
	status             ConnectionStatus
	running            bool
	cancelFn           context.CancelFunc
	seenIDs            map[string]struct{}
	onEvent            func(event *chat.Event)
	onModeration       func(modEvent *chat.ModerationEvent)
	onStatusChange     func(status ConnectionStatus, detail string)
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
		baseURL:            "https://www.youtube.com",
		apiBaseURL:         DefaultYouTubeAPIBaseURL,
		activeWorkers:      make(map[string]*broadcastWorker),
		msgVideoID:         make(map[string]string),
		waitInterval:       defaultStreamWaitPoll,
		rediscoverInterval: defaultRediscoverPoll,
		status:             StatusDisconnected,
		seenIDs:            make(map[string]struct{}),
		onEvent:            onEvent,
		onModeration:       onModeration,
		onStatusChange:     onStatusChange,
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
// If target is empty and OAuth is connected, it automatically resolves the target from the OAuth profile.
func (c *Connector) Start(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	trimmed := strings.TrimSpace(target)
	if trimmed == "" && c.oauth != nil && c.oauth.HasToken() {
		if tok, err := c.oauth.LoadToken(); err == nil && tok != nil {
			switch {
			case strings.TrimSpace(tok.ChannelHandle) != "":
				trimmed = strings.TrimSpace(tok.ChannelHandle)
			case strings.TrimSpace(tok.ChannelID) != "":
				trimmed = strings.TrimSpace(tok.ChannelID)
			default:
				trimmed = "oauth"
			}
		} else {
			trimmed = "oauth"
		}
	}
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
	c.activeWorkers = make(map[string]*broadcastWorker)
	c.msgVideoID = make(map[string]string)

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
	c.activeWorkers = make(map[string]*broadcastWorker)
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

// ActiveStreams returns a deterministic snapshot of all currently connected YouTube broadcasts.
func (c *Connector) ActiveStreams() []StreamInfo {
	c.mu.Lock()
	out := make([]StreamInfo, 0, len(c.activeWorkers))
	for _, w := range c.activeWorkers {
		out = append(out, w.info)
	}
	c.mu.Unlock()

	orientationRank := func(o StreamOrientation) int {
		switch o {
		case OrientationHorizontal:
			return 0
		case OrientationVertical:
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := orientationRank(out[i].Orientation), orientationRank(out[j].Orientation)
		if ri != rj {
			return ri < rj
		}
		return out[i].VideoID < out[j].VideoID
	})
	return out
}

// ActiveStreamCount returns the number of currently connected YouTube broadcasts.
func (c *Connector) ActiveStreamCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.activeWorkers)
}

func (c *Connector) lookupMessageVideoID(msgID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.msgVideoID[msgID]
}

// CheckLiveTarget checks whether the specified YouTube target (or configured target if empty) is live.
func (c *Connector) CheckLiveTarget(ctx context.Context, target string) (bool, error) {
	if target == "" {
		c.mu.Lock()
		target = c.target
		if target == "" && c.oauth != nil && c.oauth.HasToken() {
			if tok, err := c.oauth.LoadToken(); err == nil && tok != nil {
				switch {
				case strings.TrimSpace(tok.ChannelHandle) != "":
					target = strings.TrimSpace(tok.ChannelHandle)
				case strings.TrimSpace(tok.ChannelID) != "":
					target = strings.TrimSpace(tok.ChannelID)
				default:
					target = "oauth"
				}
			} else {
				target = "oauth"
			}
		}
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

	streams, err := c.discoverStreams(ctx, target, liveURL)
	if err != nil || len(streams) == 0 {
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

func (c *Connector) effectiveRediscoverInterval() time.Duration {
	c.mu.Lock()
	wait := c.waitInterval
	redisc := c.rediscoverInterval
	c.mu.Unlock()
	if wait > 0 && wait < 2*time.Second && redisc == defaultRediscoverPoll {
		return wait
	}
	if redisc <= 0 {
		return defaultRediscoverPoll
	}
	return redisc
}

func (c *Connector) runLoop(ctx context.Context, target string) {
	liveURL, display := NormalizeTarget(target)
	c.mu.Lock()
	baseURL := c.baseURL
	c.mu.Unlock()
	if baseURL != "https://www.youtube.com" && !strings.HasPrefix(target, "http") {
		liveURL = strings.TrimRight(baseURL, "/") + "/" + strings.TrimPrefix(display, "@") + "/live"
	}

	workerDoneCh := make(chan struct{}, 8)
	lastNotifiedCount := -1

	for {
		if ctx.Err() != nil {
			return
		}

		streams, _ := c.discoverStreams(ctx, target, liveURL)
		if ctx.Err() != nil {
			return
		}

		for _, ds := range streams {
			key := ds.info.VideoID
			if key == "" {
				key = "__default__"
			}

			c.mu.Lock()
			if _, exists := c.activeWorkers[key]; exists {
				// Update metadata if newly enriched
				if ds.info.LiveChatID != "" && c.activeWorkers[key].info.LiveChatID == "" {
					c.activeWorkers[key].info.LiveChatID = ds.info.LiveChatID
				}
				if ds.info.Title != "" && c.activeWorkers[key].info.Title == "" {
					c.activeWorkers[key].info.Title = ds.info.Title
				}
				if ds.info.Orientation != OrientationUnknown && c.activeWorkers[key].info.Orientation == OrientationUnknown {
					c.activeWorkers[key].info.Orientation = ds.info.Orientation
				}
				c.mu.Unlock()
				continue
			}

			// Avoid spawning a second worker if an existing worker already owns this unified liveChatId
			if ds.info.LiveChatID != "" {
				dupChat := false
				for _, w := range c.activeWorkers {
					if w.info.LiveChatID != "" && w.info.LiveChatID == ds.info.LiveChatID {
						dupChat = true
						break
					}
				}
				if dupChat {
					c.mu.Unlock()
					continue
				}
			}

			c.activeWorkers[key] = &broadcastWorker{info: ds.info}
			if ds.info.VideoID != "" && c.activeVideoID == "" {
				c.activeVideoID = ds.info.VideoID
			}
			if ds.info.LiveChatID != "" && c.activeLiveChatID == "" {
				c.activeLiveChatID = ds.info.LiveChatID
			}
			c.mu.Unlock()

			log.Printf("[YouTube] Discovered active live chat for %s (video=%s, orientation=%s)", display, ds.info.VideoID, ds.info.Orientation)
			go c.runWorker(ctx, display, key, ds, workerDoneCh)
		}

		activeCount := c.ActiveStreamCount()
		if activeCount > 0 {
			if lastNotifiedCount != activeCount {
				lastNotifiedCount = activeCount
				c.updateStatus(StatusConnected, display)
			}
			select {
			case <-ctx.Done():
				return
			case <-workerDoneCh:
				// Re-evaluate immediately when a stream worker ends
			case <-time.After(c.effectiveRediscoverInterval()):
			}
		} else {
			if lastNotifiedCount != 0 {
				lastNotifiedCount = 0
				c.updateStatus(StatusWaitingForStream, display)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.waitInterval):
			}
		}
	}
}

func (c *Connector) runWorker(ctx context.Context, display, key string, ds discoveredStream, doneCh chan<- struct{}) {
	defer func() {
		c.mu.Lock()
		delete(c.activeWorkers, key)
		if c.activeVideoID == ds.info.VideoID {
			c.activeVideoID = ""
			c.activeLiveChatID = ""
			for _, w := range c.activeWorkers {
				c.activeVideoID = w.info.VideoID
				c.activeLiveChatID = w.info.LiveChatID
				break
			}
		}
		remaining := len(c.activeWorkers)
		running := c.running
		c.mu.Unlock()

		if running && ctx.Err() == nil {
			if remaining > 0 {
				c.updateStatus(StatusConnected, display)
			} else {
				c.updateStatus(StatusWaitingForStream, display)
			}
		}
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}()

	if ds.apiKey == dataAPIStreamMarker {
		c.streamDataAPILiveChat(ctx, ds.info, ds.continuation)
	} else {
		c.streamLiveChat(ctx, ds.info, ds.apiKey, ds.continuation)
	}
}

func (c *Connector) discoverStreams(ctx context.Context, target, liveURL string) ([]discoveredStream, error) {
	c.mu.Lock()
	hasOAuth := c.oauth != nil && c.oauth.HasToken()
	baseURL := strings.TrimRight(c.baseURL, "/")
	activeVids := make(map[string]struct{}, len(c.activeWorkers))
	activeChats := make(map[string]struct{}, len(c.activeWorkers))
	for k, w := range c.activeWorkers {
		activeVids[k] = struct{}{}
		if w.info.VideoID != "" {
			activeVids[w.info.VideoID] = struct{}{}
		}
		if w.info.LiveChatID != "" {
			activeChats[w.info.LiveChatID] = struct{}{}
		}
	}
	c.mu.Unlock()

	var results []discoveredStream
	resultIdxByVid := make(map[string]int)
	seenChats := make(map[string]struct{})
	var lastErr error

	addResult := func(ds discoveredStream) {
		vid := ds.info.VideoID
		if vid != "" {
			if idx, ok := resultIdxByVid[vid]; ok {
				if ds.info.LiveChatID != "" && results[idx].info.LiveChatID == "" {
					results[idx].info.LiveChatID = ds.info.LiveChatID
				}
				if ds.info.Title != "" && results[idx].info.Title == "" {
					results[idx].info.Title = ds.info.Title
				}
				if ds.info.Orientation != OrientationUnknown && results[idx].info.Orientation == OrientationUnknown {
					results[idx].info.Orientation = ds.info.Orientation
				}
				return
			}
		}
		if ds.info.LiveChatID != "" {
			if _, dup := seenChats[ds.info.LiveChatID]; dup {
				return
			}
			if _, active := activeChats[ds.info.LiveChatID]; active {
				return
			}
			seenChats[ds.info.LiveChatID] = struct{}{}
		}
		if vid != "" {
			resultIdxByVid[vid] = len(results)
		}
		results = append(results, ds)
	}

	// Step 1: Channel /streams tab discovery (0 quota; finds simultaneous horizontal + vertical streams)
	streamsURL, isChannel := NormalizeStreamsTabURL(target)
	if isChannel && streamsURL != "" {
		if baseURL != "https://www.youtube.com" && !strings.HasPrefix(target, "http") {
			_, disp := NormalizeTarget(target)
			streamsURL = baseURL + "/" + strings.TrimPrefix(disp, "@") + "/streams"
		}
		if vids, sErr := c.fetchStreamsTabVideoIDs(ctx, streamsURL); sErr == nil {
			for _, vid := range vids {
				if _, active := activeVids[vid]; active {
					continue
				}
				watchURL := fmt.Sprintf("%s/watch?v=%s", baseURL, vid)
				if k, cont, info, wErr := c.fetchWatchPageTokens(ctx, watchURL); wErr == nil && k != "" && cont != "" {
					if info.VideoID == "" {
						info.VideoID = vid
					}
					addResult(discoveredStream{info: info, apiKey: k, continuation: cont})
				}
			}
		}
	}

	// Step 2: Direct liveURL fallback (handles direct video URLs and channels where /streams returned nothing)
	if !isChannel || (len(results) == 0 && len(activeVids) == 0) {
		k, cont, info, err := c.fetchWatchPageTokens(ctx, liveURL)
		if err == nil && k != "" && cont != "" {
			key := info.VideoID
			if key == "" {
				key = "__default__"
			}
			if _, active := activeVids[key]; !active {
				addResult(discoveredStream{info: info, apiKey: k, continuation: cont})
			}
		} else if err != nil {
			lastErr = err
		}
	}

	// Step 3: Authenticated OAuth discovery (finds Private/Unlisted/Testing streams and any concurrent broadcast
	// not yet listed on the public /streams tab)
	if hasOAuth {
		if broadcasts, bErr := c.discoverAuthenticatedBroadcasts(ctx); bErr == nil {
			for _, b := range broadcasts {
				if b.VideoID == "" {
					continue
				}
				// If already discovered in Step 1/2, enrich its liveChatId/title
				if idx, ok := resultIdxByVid[b.VideoID]; ok {
					if b.LiveChatID != "" && results[idx].info.LiveChatID == "" {
						results[idx].info.LiveChatID = b.LiveChatID
					}
					if b.Title != "" && results[idx].info.Title == "" {
						results[idx].info.Title = b.Title
					}
					continue
				}
				if _, active := activeVids[b.VideoID]; active {
					// Enrich already-running worker with liveChatId/title if missing
					c.mu.Lock()
					if w, ok := c.activeWorkers[b.VideoID]; ok {
						if b.LiveChatID != "" && w.info.LiveChatID == "" {
							w.info.LiveChatID = b.LiveChatID
						}
						if b.Title != "" && w.info.Title == "" {
							w.info.Title = b.Title
						}
					}
					c.mu.Unlock()
					continue
				}
				if b.LiveChatID != "" {
					if _, active := activeChats[b.LiveChatID]; active {
						continue
					}
				}

				watchURL := fmt.Sprintf("%s/watch?v=%s", baseURL, b.VideoID)
				if k, cont, info, wErr := c.fetchWatchPageTokens(ctx, watchURL); wErr == nil && k != "" && cont != "" {
					if info.VideoID == "" {
						info.VideoID = b.VideoID
					}
					if info.LiveChatID == "" {
						info.LiveChatID = b.LiveChatID
					}
					if info.Title == "" {
						info.Title = b.Title
					}
					addResult(discoveredStream{info: info, apiKey: k, continuation: cont})
					continue
				}

				// Private/Testing fallback: YouTube Data API v3 liveChat polling
				if b.LiveChatID != "" {
					addResult(discoveredStream{
						info:         b,
						apiKey:       dataAPIStreamMarker,
						continuation: b.LiveChatID,
					})
				}
			}
		}
	}

	if len(results) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return results, nil
}

func (c *Connector) fetchStreamsTabVideoIDs(ctx context.Context, streamsURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb"})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	return ExtractActiveLiveVideoIDs(string(bodyBytes)), nil
}

func (c *Connector) fetchWatchPageTokens(ctx context.Context, liveURL string) (string, string, StreamInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
	if err != nil {
		return "", "", StreamInfo{}, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb"})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", StreamInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", StreamInfo{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return "", "", StreamInfo{}, err
	}

	htmlStr := string(bodyBytes)
	apiKey, continuation, ok := ExtractBootstrapTokens(htmlStr)
	if !ok {
		return "", "", StreamInfo{}, errors.New("stream not currently live or chat disabled")
	}
	info, _, _ := ExtractWatchPageMetadata(htmlStr)
	if info.VideoID != "" {
		c.mu.Lock()
		if c.activeVideoID == "" {
			c.activeVideoID = info.VideoID
		}
		c.mu.Unlock()
	}
	return apiKey, continuation, info, nil
}

func (c *Connector) streamLiveChat(ctx context.Context, stream StreamInfo, apiKey string, initialContinuation string) {
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
			log.Printf("[YouTube] Live chat poll error (video=%s): %v", stream.VideoID, err)
			return
		}

		c.dispatchPolledBatch(stream, events, modEvents, isFirstPoll)

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

func (c *Connector) streamDataAPILiveChat(ctx context.Context, stream StreamInfo, liveChatID string) {
	pageToken := ""
	isFirstPoll := true

	for {
		if ctx.Err() != nil {
			return
		}

		// Periodically check if the broadcast became Public/Unlisted so we can seamlessly switch
		// to 0-quota InnerTube polling without consuming Data API quota.
		if !isFirstPoll && stream.VideoID != "" {
			c.mu.Lock()
			baseURL := strings.TrimRight(c.baseURL, "/")
			c.mu.Unlock()
			watchURL := fmt.Sprintf("%s/watch?v=%s", baseURL, stream.VideoID)
			if k, cont, info, wErr := c.fetchWatchPageTokens(ctx, watchURL); wErr == nil && k != "" && cont != "" {
				if info.VideoID == "" {
					info.VideoID = stream.VideoID
				}
				if info.LiveChatID == "" {
					info.LiveChatID = liveChatID
				}
				if info.Title == "" {
					info.Title = stream.Title
				}
				c.mu.Lock()
				if w, ok := c.activeWorkers[stream.VideoID]; ok {
					w.info = info
				}
				c.mu.Unlock()
				log.Printf("[YouTube] Broadcast %s is now accessible via InnerTube; switching from Data API to InnerTube polling", stream.VideoID)
				c.streamLiveChat(ctx, info, k, cont)
				return
			}
		}

		events, modEvents, nextToken, delay, err := c.fetchDataAPILiveChatPage(ctx, liveChatID, pageToken)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[YouTube] Data API live chat poll error (video=%s): %v", stream.VideoID, err)
			return
		}

		c.dispatchPolledBatch(stream, events, modEvents, isFirstPoll)

		isFirstPoll = false
		if nextToken == "" {
			return
		}
		pageToken = nextToken

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (c *Connector) dispatchPolledBatch(stream StreamInfo, events []*chat.Event, modEvents []*chat.ModerationEvent, isFirstPoll bool) {
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
		if ev.StreamID == "" {
			ev.StreamID = stream.VideoID
		}
		if ev.StreamTitle == "" {
			ev.StreamTitle = stream.Title
		}
		if ev.StreamOrientation == "" {
			ev.StreamOrientation = string(stream.Orientation)
		}
		c.mu.Lock()
		_, alreadySeen := c.seenIDs[ev.ID]
		if !alreadySeen && ev.ID != "" {
			// Prevent unbounded map growth over multi-hour streams
			if len(c.seenIDs) > 5000 {
				c.seenIDs = make(map[string]struct{})
			}
			c.seenIDs[ev.ID] = struct{}{}
			if stream.VideoID != "" {
				if len(c.msgVideoID) > 5000 {
					c.msgVideoID = make(map[string]string)
				}
				c.msgVideoID[ev.ID] = stream.VideoID
			}
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
