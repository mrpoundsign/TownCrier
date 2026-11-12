package youtube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
)

// DefaultYouTubeAPIBaseURL is the default base URL for YouTube Data API v3 requests.
const DefaultYouTubeAPIBaseURL = "https://www.googleapis.com/youtube/v3"

func (c *Connector) resolveModContext(ctx context.Context) (httpClient *http.Client, apiBase, token string, err error) {
	c.mu.Lock()
	oauth := c.oauth
	httpClient = c.httpClient
	apiBase = strings.TrimRight(c.apiBaseURL, "/")
	c.mu.Unlock()

	if oauth == nil || !oauth.HasToken() {
		return nil, "", "", errors.New("youtube OAuth not connected")
	}

	token, err = oauth.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return nil, "", "", fmt.Errorf("youtube access token error: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultYouTubeAPIBaseURL
	}
	return httpClient, apiBase, token, nil
}

// DeleteChatMessage deletes a single chat message via YouTube Data API v3 DELETE /liveChat/messages.
func (c *Connector) DeleteChatMessage(ctx context.Context, messageID string) error {
	msgID := strings.TrimSpace(messageID)
	if msgID == "" {
		return errors.New("empty YouTube message ID")
	}

	httpClient, apiBase, token, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	targetID := msgID
	if isInnerTubeShortMessageID(msgID) {
		if liveChatID, lErr := c.resolveLiveChatID(ctx, httpClient, apiBase, token); lErr == nil && liveChatID != "" {
			if fullID := NormalizeDataAPIMessageID(msgID, liveChatID); fullID != "" {
				targetID = fullID
			}
		}
	}

	status, bodyStr, err := c.doDeleteChatMessage(ctx, httpClient, apiBase, token, targetID)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent || status == http.StatusOK {
		return nil
	}

	// If targetID was upgraded from an InnerTube short ID and returned 400/404,
	// invalidate any stale cached liveChatID, re-resolve once, or fall back to raw msgID.
	if targetID != msgID && (status == http.StatusNotFound || status == http.StatusBadRequest) {
		c.clearCachedLiveChatID()
		if liveChatID, lErr := c.resolveLiveChatID(ctx, httpClient, apiBase, token); lErr == nil && liveChatID != "" {
			if retryID := NormalizeDataAPIMessageID(msgID, liveChatID); retryID != "" && retryID != targetID {
				if rStatus, rBody, rErr := c.doDeleteChatMessage(ctx, httpClient, apiBase, token, retryID); rErr == nil {
					if rStatus == http.StatusNoContent || rStatus == http.StatusOK {
						return nil
					}
					status, bodyStr = rStatus, rBody
				}
			}
		}
		if fStatus, _, fErr := c.doDeleteChatMessage(ctx, httpClient, apiBase, token, msgID); fErr == nil && (fStatus == http.StatusNoContent || fStatus == http.StatusOK) {
			return nil
		}
	}

	if status == http.StatusUnauthorized {
		c.handleUnauthorized(ctx)
	}
	return fmt.Errorf("youtube delete message HTTP %d: %s", status, bodyStr)
}

func (c *Connector) doDeleteChatMessage(ctx context.Context, httpClient *http.Client, apiBase, token, id string) (int, string, error) {
	endpoint := fmt.Sprintf("%s/liveChat/messages?id=%s", apiBase, url.QueryEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return resp.StatusCode, "", nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

func (c *Connector) clearCachedLiveChatID() {
	c.mu.Lock()
	c.activeLiveChatID = ""
	c.mu.Unlock()
}

// isInnerTubeShortMessageID reports whether msgID is an InnerTube-sourced liveChatMessage ID
// that omits Protobuf Field 1 (live_chat_id) and only carries Field 2 (item_key, tag 0x12)
// or a raw inner item_key (tag 0x0a without LCC. prefix).
func isInnerTubeShortMessageID(msgID string) bool {
	trimmed := strings.TrimSpace(msgID)
	hasLCC := strings.HasPrefix(trimmed, "LCC.")
	b64Part := strings.TrimPrefix(trimmed, "LCC.")
	raw, ok := decodeYouTubeBase64(b64Part)
	if !ok || len(raw) < 4 {
		return false
	}
	// InnerTube LCC.EhwKGk... starts with Field 2 (0x12) followed by nested Field 1 (0x0a).
	if raw[0] == 0x12 && raw[2] == 0x0a {
		return true
	}
	// Raw InnerTube ChwKGk... without "LCC." prefix starts with 0x0a <len> 0x0a <len>.
	if !hasLCC && raw[0] == 0x0a && raw[2] == 0x0a && int(raw[1])+2 == len(raw) {
		return true
	}
	return false
}

// NormalizeDataAPIMessageID upgrades an InnerTube liveChatMessage ID (e.g. "LCC.EhwKGk..." or "ChwKGk...")
// into a full YouTube Data API v3 liveChatMessage ID ("LCC.Cjg...EhwKGk...") by prepending Protobuf Field 1
// (the decoded liveChatID bytes) ahead of Field 2 (the chat item key). If msgID is already a full Data API ID
// or cannot be decoded as an InnerTube protobuf ID, msgID is returned unchanged.
func NormalizeDataAPIMessageID(msgID, liveChatID string) string {
	trimmedMsg := strings.TrimSpace(msgID)
	trimmedChat := strings.TrimSpace(liveChatID)
	if trimmedMsg == "" || trimmedChat == "" {
		return trimmedMsg
	}

	hasLCC := strings.HasPrefix(trimmedMsg, "LCC.")
	b64Part := strings.TrimPrefix(trimmedMsg, "LCC.")
	msgBytes, ok := decodeYouTubeBase64(b64Part)
	if !ok || len(msgBytes) < 4 {
		return trimmedMsg
	}

	// If raw ChwKGk... (no LCC. prefix, outer tag 0x0a), convert outer tag to Field 2 (0x12).
	if !hasLCC && msgBytes[0] == 0x0a && msgBytes[2] == 0x0a && int(msgBytes[1])+2 == len(msgBytes) {
		cloned := make([]byte, len(msgBytes))
		copy(cloned, msgBytes)
		cloned[0] = 0x12
		msgBytes = cloned
	}

	// Only prepend Field 1 (live_chat_id) if msgBytes starts at Field 2 (0x12).
	if msgBytes[0] != 0x12 {
		return trimmedMsg
	}

	chatBytes, ok := decodeYouTubeBase64(trimmedChat)
	if !ok || len(chatBytes) == 0 {
		return trimmedMsg
	}

	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(chatBytes)))

	full := make([]byte, 0, 1+n+len(chatBytes)+len(msgBytes))
	full = append(full, 0x0a) // Field 1 (live_chat_id), wire type 2 (length-delimited)
	full = append(full, lenBuf[:n]...)
	full = append(full, chatBytes...)
	full = append(full, msgBytes...)

	return "LCC." + base64.RawURLEncoding.EncodeToString(full)
}

func decodeYouTubeBase64(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) > 0 {
			return b, true
		}
	}
	return nil, false
}

type youtubeLiveChatBanRequest struct {
	Snippet youtubeLiveChatBanSnippet `json:"snippet"`
}

type youtubeLiveChatBanSnippet struct {
	LiveChatID         string                  `json:"liveChatId"`
	Type               string                  `json:"type"`
	BanDurationSeconds int                     `json:"banDurationSeconds,omitempty"`
	BannedUserDetails  youtubeBannedUserDetail `json:"bannedUserDetails"`
}

type youtubeBannedUserDetail struct {
	ChannelID string `json:"channelId"`
}

// BanOrTimeoutUser bans (durationSeconds <= 0) or times out (durationSeconds > 0) a user in YouTube Live Chat
// via YouTube Data API v3 POST /liveChat/bans?part=snippet.
func (c *Connector) BanOrTimeoutUser(ctx context.Context, targetChannelID string, durationSeconds int) error {
	channelID := strings.TrimSpace(targetChannelID)
	if channelID == "" {
		return errors.New("empty target YouTube channel ID")
	}

	httpClient, apiBase, token, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	liveChatID, err := c.resolveLiveChatID(ctx, httpClient, apiBase, token)
	if err != nil {
		return err
	}

	banType := "permanent"
	dur := 0
	if durationSeconds > 0 {
		banType = "temporary"
		dur = durationSeconds
	}

	payload := youtubeLiveChatBanRequest{
		Snippet: youtubeLiveChatBanSnippet{
			LiveChatID:         liveChatID,
			Type:               banType,
			BanDurationSeconds: dur,
			BannedUserDetails: youtubeBannedUserDetail{
				ChannelID: channelID,
			},
		},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := apiBase + "/liveChat/bans?part=snippet"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		if resp.StatusCode == http.StatusNotFound {
			c.clearCachedLiveChatID()
		}
		if resp.StatusCode == http.StatusUnauthorized {
			c.handleUnauthorized(ctx)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("youtube ban/timeout HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Connector) resolveLiveChatID(ctx context.Context, httpClient *http.Client, apiBase, token string) (string, error) {
	c.mu.Lock()
	cached := c.activeLiveChatID
	videoID := c.activeVideoID
	target := c.target
	c.mu.Unlock()

	if cached != "" {
		return cached, nil
	}

	if videoID == "" {
		videoID = extractVideoIDFromTargetURL(target)
	}

	if videoID != "" {
		chatID, err := fetchLiveChatIDByVideo(ctx, httpClient, apiBase, token, videoID)
		if err == nil && chatID != "" {
			c.mu.Lock()
			c.activeVideoID = videoID
			c.activeLiveChatID = chatID
			c.mu.Unlock()
			return chatID, nil
		}
	}

	// Fallback: query the authenticated user's active broadcast
	videoID, chatID, err := fetchActiveBroadcastVideoAndChatID(ctx, httpClient, apiBase, token)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if videoID != "" {
		c.activeVideoID = videoID
	}
	c.activeLiveChatID = chatID
	c.mu.Unlock()
	return chatID, nil
}

func extractVideoIDFromTargetURL(target string) string {
	s := strings.TrimSpace(target)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil {
		if v := strings.TrimSpace(u.Query().Get("v")); v != "" {
			return v
		}
		if after, ok := strings.CutPrefix(u.Path, "/live/"); ok {
			parts := strings.Split(after, "/")
			if len(parts) > 0 && parts[0] != "" {
				return parts[0]
			}
		}
	}
	return ""
}

func fetchLiveChatIDByVideo(ctx context.Context, httpClient *http.Client, apiBase, token, videoID string) (string, error) {
	endpoint := fmt.Sprintf("%s/videos?part=liveStreamingDetails&id=%s", apiBase, url.QueryEscape(videoID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("youtube videos.list HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Items []struct {
			LiveStreamingDetails *struct {
				ActiveLiveChatID string `json:"activeLiveChatId"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Items) == 0 || parsed.Items[0].LiveStreamingDetails == nil {
		return "", errors.New("no activeLiveChatId found on video")
	}
	chatID := strings.TrimSpace(parsed.Items[0].LiveStreamingDetails.ActiveLiveChatID)
	if chatID == "" {
		return "", errors.New("empty activeLiveChatId on video")
	}
	return chatID, nil
}

func (c *Connector) handleUnauthorized(ctx context.Context) {
	c.mu.Lock()
	oauth := c.oauth
	c.mu.Unlock()
	if oauth != nil {
		_, _ = oauth.HandleUnauthorized(ctx)
	}
}

// discoverAuthenticatedBroadcast checks the authenticated user's active YouTube broadcasts via
// GET /liveBroadcasts?part=id,snippet,status&mine=true&broadcastType=all (1 quota unit), with fallback
// to broadcastStatus=active. This enables discovering Private, Unlisted, or newly-transitioned/testing
// broadcasts that /@handle/live does not redirect to.
func (c *Connector) discoverAuthenticatedBroadcast(ctx context.Context) (videoID, liveChatID string, err error) {
	httpClient, apiBase, token, err := c.resolveModContext(ctx)
	if err != nil {
		return "", "", err
	}
	vid, chatID, err := fetchActiveBroadcastVideoAndChatID(ctx, httpClient, apiBase, token)
	if err != nil && strings.Contains(err.Error(), "HTTP 401") {
		c.handleUnauthorized(ctx)
	}
	return vid, chatID, err
}

type liveBroadcastItem struct {
	ID      string `json:"id"`
	Snippet *struct {
		LiveChatID         string `json:"liveChatId"`
		ActualStartTime    string `json:"actualStartTime"`
		ActualEndTime      string `json:"actualEndTime"`
		IsDefaultBroadcast bool   `json:"isDefaultBroadcast"`
	} `json:"snippet"`
	Status *struct {
		LifeCycleStatus string `json:"lifeCycleStatus"`
		PrivacyStatus   string `json:"privacyStatus"`
	} `json:"status"`
}

func fetchActiveBroadcastVideoAndChatID(ctx context.Context, httpClient *http.Client, apiBase, token string) (videoID, liveChatID string, err error) {
	items, err := queryLiveBroadcasts(ctx, httpClient, apiBase, token, "/liveBroadcasts?part=id,snippet,status&mine=true&broadcastType=all&maxResults=25")
	if err != nil {
		return "", "", err
	}
	if vid, chatID := selectActiveBroadcast(items); vid != "" && chatID != "" {
		return vid, chatID, nil
	}

	// Fallback for accounts/tests that only respond to broadcastStatus=active
	activeItems, activeErr := queryLiveBroadcasts(ctx, httpClient, apiBase, token, "/liveBroadcasts?part=id,snippet,status&broadcastStatus=active&broadcastType=all")
	if activeErr == nil {
		if vid, chatID := selectActiveBroadcast(activeItems); vid != "" && chatID != "" {
			return vid, chatID, nil
		}
	}

	return "", "", errors.New("no active YouTube liveChatId found for stream")
}

func queryLiveBroadcasts(ctx context.Context, httpClient *http.Client, apiBase, token, pathAndQuery string) ([]liveBroadcastItem, error) {
	endpoint := apiBase + pathAndQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("youtube liveBroadcasts.list HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Items []liveBroadcastItem `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Items, nil
}

func selectActiveBroadcast(items []liveBroadcastItem) (videoID, liveChatID string) {
	// Pass 1: explicitly "live" broadcasts
	for _, it := range items {
		if it.Snippet == nil || strings.TrimSpace(it.Snippet.LiveChatID) == "" {
			continue
		}
		if it.Status != nil && strings.EqualFold(strings.TrimSpace(it.Status.LifeCycleStatus), "live") {
			return strings.TrimSpace(it.ID), strings.TrimSpace(it.Snippet.LiveChatID)
		}
	}
	// Pass 2: "testing" or "liveStarting" broadcasts (e.g., private preview or transitioning to live in YouTube Studio)
	for _, it := range items {
		if it.Snippet == nil || strings.TrimSpace(it.Snippet.LiveChatID) == "" {
			continue
		}
		if it.Status != nil {
			lc := strings.ToLower(strings.TrimSpace(it.Status.LifeCycleStatus))
			if lc == "testing" || lc == "livestarting" || lc == "teststarting" {
				return strings.TrimSpace(it.ID), strings.TrimSpace(it.Snippet.LiveChatID)
			}
		}
	}
	// Pass 3: items with an actualStartTime and no actualEndTime, or items where status was omitted
	for _, it := range items {
		if it.Snippet == nil || strings.TrimSpace(it.Snippet.LiveChatID) == "" {
			continue
		}
		if strings.TrimSpace(it.Snippet.ActualEndTime) != "" {
			continue
		}
		if it.Status != nil {
			lc := strings.ToLower(strings.TrimSpace(it.Status.LifeCycleStatus))
			if lc == "complete" || lc == "revoked" || lc == "created" || lc == "ready" {
				continue
			}
		}
		return strings.TrimSpace(it.ID), strings.TrimSpace(it.Snippet.LiveChatID)
	}
	return "", ""
}

// fetchDataAPILiveChatPage polls YouTube Data API v3 GET /liveChat/messages (used for Private or Testing
// broadcasts where unauthenticated InnerTube watch-page scraping is blocked by LOGIN_REQUIRED).
func (c *Connector) fetchDataAPILiveChatPage(ctx context.Context, liveChatID, pageToken string) ([]*chat.Event, []*chat.ModerationEvent, string, time.Duration, error) {
	httpClient, apiBase, token, err := c.resolveModContext(ctx)
	if err != nil {
		return nil, nil, "", 0, err
	}

	q := url.Values{}
	q.Set("liveChatId", liveChatID)
	q.Set("part", "id,snippet,authorDetails")
	q.Set("maxResults", "200")
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}

	endpoint := apiBase + "/liveChat/messages?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			c.clearCachedLiveChatID()
		}
		if resp.StatusCode == http.StatusUnauthorized {
			c.handleUnauthorized(ctx)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, "", 0, fmt.Errorf("youtube liveChat/messages HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, nil, "", 0, err
	}

	return ParseDataAPILiveChatResponse(data)
}
