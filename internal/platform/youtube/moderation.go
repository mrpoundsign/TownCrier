package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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

	endpoint := fmt.Sprintf("%s/liveChat/messages?id=%s", apiBase, url.QueryEscape(msgID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			c.handleUnauthorized(ctx)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("youtube delete message HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
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
// GET /liveBroadcasts?part=id,snippet&broadcastStatus=active&broadcastType=all (1 quota unit).
// This enables discovering Unlisted or newly-transitioned broadcasts that /@handle/live does not redirect to.
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

func fetchActiveBroadcastVideoAndChatID(ctx context.Context, httpClient *http.Client, apiBase, token string) (videoID, liveChatID string, err error) {
	endpoint := apiBase + "/liveBroadcasts?part=id,snippet&broadcastStatus=active&broadcastType=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("youtube liveBroadcasts.list HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet *struct {
				LiveChatID string `json:"liveChatId"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return "", "", err
	}
	for _, it := range parsed.Items {
		if it.Snippet != nil && strings.TrimSpace(it.Snippet.LiveChatID) != "" {
			return strings.TrimSpace(it.ID), strings.TrimSpace(it.Snippet.LiveChatID), nil
		}
	}
	return "", "", errors.New("no active YouTube liveChatId found for stream")
}
