package kick

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SetOAuthManager binds a Kick OAuthManager to the connector for Kick Public API v1 moderation and channel discovery.
func (c *Connector) SetOAuthManager(oauth *OAuthManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.oauth = oauth
}

// SetPublicAPIBaseURL overrides the Kick Public API v1 base URL (used in unit tests).
func (c *Connector) SetPublicAPIBaseURL(publicAPIURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if publicAPIURL != "" {
		c.publicAPIURL = strings.TrimRight(publicAPIURL, "/")
	}
}

func (c *Connector) resolveModContext(ctx context.Context) (httpClient *http.Client, apiBase, token string, oauth *OAuthManager, err error) {
	c.mu.Lock()
	oauth = c.oauth
	httpClient = c.httpClient
	apiBase = strings.TrimRight(c.publicAPIURL, "/")
	c.mu.Unlock()

	if oauth == nil || !oauth.HasToken() {
		return nil, "", "", nil, errors.New("kick OAuth not connected")
	}

	token, err = oauth.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return nil, "", "", nil, fmt.Errorf("kick access token error: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultKickPublicAPIBaseURL
	}
	return httpClient, apiBase, token, oauth, nil
}

// DeleteChatMessage deletes a single chat message via Kick Public API v1 DELETE /public/v1/chat/{message_id}.
func (c *Connector) DeleteChatMessage(ctx context.Context, messageID string) error {
	msgID := strings.TrimSpace(messageID)
	if msgID == "" {
		return errors.New("empty Kick message ID")
	}

	httpClient, apiBase, token, oauth, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/chat/%s", apiBase, url.PathEscape(msgID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && oauth != nil {
		_, _ = oauth.HandleUnauthorized(ctx)
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kick delete message HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type kickBanRequest struct {
	BroadcasterUserID int64  `json:"broadcaster_user_id"`
	UserID            int64  `json:"user_id"`
	Duration          int    `json:"duration,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// BanOrTimeoutUser bans (durationSeconds <= 0) or times out (durationSeconds > 0, converted to minutes 1..10080)
// a user in Kick chat via Kick Public API v1 POST /public/v1/moderation/bans.
func (c *Connector) BanOrTimeoutUser(ctx context.Context, targetUserID string, durationSeconds int, reason string) error {
	uidStr := strings.TrimSpace(targetUserID)
	if uidStr == "" {
		return errors.New("empty target Kick user ID")
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		return fmt.Errorf("invalid numeric Kick user ID %q", uidStr)
	}

	httpClient, apiBase, token, oauth, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	broadcasterID, err := c.resolveBroadcasterUserID(ctx, oauth)
	if err != nil || broadcasterID <= 0 {
		return fmt.Errorf("resolve Kick broadcaster_user_id: %w", err)
	}

	durMin := 0
	if durationSeconds > 0 {
		durMin = max(1, min(10080, (durationSeconds+59)/60))
	}

	payload := kickBanRequest{
		BroadcasterUserID: broadcasterID,
		UserID:            uid,
		Duration:          durMin,
		Reason:            strings.TrimSpace(reason),
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := apiBase + "/moderation/bans"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && oauth != nil {
		_, _ = oauth.HandleUnauthorized(ctx)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kick ban/timeout HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Connector) resolveBroadcasterUserID(ctx context.Context, oauth *OAuthManager) (int64, error) {
	if oauth == nil {
		return 0, errors.New("kick OAuth not configured")
	}
	if tok, err := oauth.LoadToken(); err == nil && tok != nil && tok.BroadcasterUserID > 0 {
		return tok.BroadcasterUserID, nil
	}
	bID, _, _, err := oauth.FetchAuthenticatedChannel(ctx)
	if err != nil {
		return 0, err
	}
	if bID <= 0 {
		return 0, errors.New("authenticated Kick channel has no broadcaster_user_id")
	}
	return bID, nil
}

func (c *Connector) checkLivePublicV1(ctx context.Context, slug string) (bool, error) {
	httpClient, apiBase, token, oauth, err := c.resolveModContext(ctx)
	if err != nil {
		return false, err
	}

	cleanSlug, _ := NormalizeChannelInfo(slug)
	endpoint := apiBase + "/channels"
	if cleanSlug != "" {
		endpoint += "?slug=" + url.QueryEscape(cleanSlug)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && oauth != nil {
		_, _ = oauth.HandleUnauthorized(ctx)
		return false, errors.New("kick /public/v1/channels HTTP 401 unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("kick /public/v1/channels HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			BroadcasterUserID int64  `json:"broadcaster_user_id"`
			Slug              string `json:"slug"`
			Stream            *struct {
				IsLive bool `json:"is_live"`
			} `json:"stream"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return false, err
	}
	if len(parsed.Data) == 0 {
		return false, nil
	}
	if parsed.Data[0].Stream != nil {
		return parsed.Data[0].Stream.IsLive, nil
	}
	return false, nil
}
