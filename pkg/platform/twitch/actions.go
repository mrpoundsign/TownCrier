package twitch

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

// SetBotOAuthManager binds an optional secondary OAuthManager ("twitch_bot_") used when sending chat messages.
func (c *Connector) SetBotOAuthManager(oauth *OAuthManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.botOAuth = oauth
}

// BotOAuthManager returns the bound secondary bot OAuthManager, if any.
func (c *Connector) BotOAuthManager() *OAuthManager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.botOAuth
}

func normalizeSendAs(sendAs string) string {
	switch strings.ToLower(strings.TrimSpace(sendAs)) {
	case "bot":
		return "bot"
	case "broadcaster", "main", "primary":
		return "broadcaster"
	default:
		return "auto"
	}
}

func (c *Connector) resolveWriteContextAs(ctx context.Context, sendAs string) (httpClient *http.Client, helixBase, clientID, token, senderID, broadcasterID string, usedBot bool, err error) {
	mode := normalizeSendAs(sendAs)

	c.mu.Lock()
	primary := c.oauth
	bot := c.botOAuth
	httpClient = c.httpClient
	helixBase = strings.TrimRight(c.helixBaseURL, "/")
	channel := c.channel
	c.mu.Unlock()

	if channel == "" {
		return nil, "", "", "", "", "", false, errors.New("no active Twitch channel configured")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}

	var activeOAuth *OAuthManager
	switch mode {
	case "bot":
		if bot == nil || !bot.HasToken() {
			return nil, "", "", "", "", "", false, errors.New("secondary Twitch bot OAuth account not connected")
		}
		activeOAuth = bot
		usedBot = true
	case "broadcaster":
		if primary == nil || !primary.HasToken() {
			return nil, "", "", "", "", "", false, errors.New("broadcaster Twitch OAuth not connected")
		}
		activeOAuth = primary
	default: // "auto"
		switch {
		case bot != nil && bot.HasToken():
			activeOAuth = bot
			usedBot = true
		case primary != nil && primary.HasToken():
			activeOAuth = primary
		default:
			return nil, "", "", "", "", "", false, errors.New("twitch OAuth not connected")
		}
	}

	token, err = activeOAuth.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return nil, "", "", "", "", "", usedBot, fmt.Errorf("twitch access token error: %w", err)
	}
	clientID = activeOAuth.ClientID()
	if clientID == "" {
		clientID = DefaultTwitchClientID
	}

	senderID, _, sErr := c.resolveUserIDFromToken(ctx, activeOAuth, token)
	if primary != nil && primary.HasToken() {
		if pTok, pErr := primary.GetValidAccessToken(ctx); pErr == nil && pTok != "" {
			if _, bID, bErr := c.resolveEventSubUserIDs(ctx, pTok, channel); bErr == nil && bID != "" {
				broadcasterID = bID
			}
		}
	}
	if broadcasterID == "" {
		if _, bID, bErr := c.resolveEventSubUserIDs(ctx, token, channel); bErr == nil && bID != "" {
			broadcasterID = bID
		}
	}
	if sErr == nil && senderID != "" && broadcasterID != "" {
		return httpClient, helixBase, clientID, token, senderID, broadcasterID, usedBot, nil
	}
	senderID, broadcasterID, err = c.resolveEventSubUserIDs(ctx, token, channel)
	if err != nil {
		return nil, "", "", "", "", "", usedBot, err
	}
	return httpClient, helixBase, clientID, token, senderID, broadcasterID, usedBot, nil
}

func (c *Connector) resolveUserIDFromToken(ctx context.Context, oauth *OAuthManager, _ string) (string, string, error) {
	if oauth != nil {
		uid, login, _ := oauth.StoredUser()
		if uid != "" {
			return uid, login, nil
		}
		uid, login, _, err := oauth.FetchAuthenticatedUser(ctx)
		return uid, login, err
	}
	return "", "", errors.New("oauth manager nil")
}

func (c *Connector) resolveTwitchUserID(ctx context.Context, httpClient *http.Client, helixBase, clientID, token, userID, login string) (string, error) {
	cleanID := strings.TrimSpace(userID)
	if cleanID != "" {
		return cleanID, nil
	}
	cleanLogin := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(login, "@")))
	if cleanLogin == "" {
		return "", errors.New("target user ID or username is required")
	}

	uURL := fmt.Sprintf("%s/users?login=%s", helixBase, url.QueryEscape(cleanLogin))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var uResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&uResp)
	if resp.StatusCode == http.StatusOK && len(uResp.Data) > 0 && strings.TrimSpace(uResp.Data[0].ID) != "" {
		return strings.TrimSpace(uResp.Data[0].ID), nil
	}
	return "", fmt.Errorf("could not resolve Twitch user ID for %q", cleanLogin)
}

// SendChatMessage sends a Twitch chat message via Helix POST /chat/messages (preferring bot account if connected).
func (c *Connector) SendChatMessage(ctx context.Context, text string) error {
	return c.SendChatMessageAs(ctx, text, "", "")
}

// ReplyChatMessage sends a Twitch chat message (optionally replying to replyParentMessageID) via Helix POST /chat/messages.
func (c *Connector) ReplyChatMessage(ctx context.Context, text, replyParentMessageID string) error {
	return c.SendChatMessageAs(ctx, text, replyParentMessageID, "")
}

// SendChatMessageAs sends a Twitch chat message (or reply) using sendAs ("auto", "bot", or "broadcaster").
func (c *Connector) SendChatMessageAs(ctx context.Context, text, replyParentMessageID, sendAs string) error {
	msg := strings.TrimSpace(text)
	if msg == "" {
		return errors.New("empty chat message")
	}
	httpClient, helixBase, clientID, token, senderID, broadcasterID, _, err := c.resolveWriteContextAs(ctx, sendAs)
	if err != nil {
		return err
	}

	payload := map[string]string{
		"broadcaster_id": broadcasterID,
		"sender_id":      senderID,
		"message":        msg,
	}
	if rp := strings.TrimSpace(replyParentMessageID); rp != "" {
		payload["reply_parent_message_id"] = rp
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, helixBase+"/chat/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twitch send chat status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// SendAnnouncement sends a Twitch chat announcement via Helix POST /chat/announcements (preferring bot account if connected, with automatic broadcaster fallback on 401/403).
func (c *Connector) SendAnnouncement(ctx context.Context, text, color string) error {
	return c.SendAnnouncementAs(ctx, text, color, "")
}

// SendAnnouncementAs sends a Twitch chat announcement using sendAs ("auto", "bot", or "broadcaster").
func (c *Connector) SendAnnouncementAs(ctx context.Context, text, color, sendAs string) error {
	msg := strings.TrimSpace(text)
	if msg == "" {
		return errors.New("empty announcement text")
	}
	mode := normalizeSendAs(sendAs)

	sendWith := func(targetMode string) (int, bool, error) {
		httpClient, helixBase, clientID, token, moderatorID, broadcasterID, usedBot, err := c.resolveWriteContextAs(ctx, targetMode)
		if err != nil {
			return 0, usedBot, err
		}

		payload := map[string]string{"message": msg}
		if clr := strings.ToLower(strings.TrimSpace(color)); clr != "" {
			payload["color"] = clr
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, usedBot, err
		}

		reqURL := fmt.Sprintf("%s/chat/announcements?broadcaster_id=%s&moderator_id=%s",
			helixBase, url.QueryEscape(broadcasterID), url.QueryEscape(moderatorID))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(raw))
		if err != nil {
			return 0, usedBot, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", clientID)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, usedBot, err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return resp.StatusCode, usedBot, fmt.Errorf("twitch send announcement status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return resp.StatusCode, usedBot, nil
	}

	status, usedBot, err := sendWith(mode)
	if err != nil && mode == "auto" && usedBot && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		c.mu.Lock()
		hasPrimary := c.oauth != nil && c.oauth.HasToken()
		c.mu.Unlock()
		if hasPrimary {
			_, _, err = sendWith("broadcaster")
		}
	}
	return err
}

// SendShoutout sends a Twitch shoutout via Helix POST /chat/shoutouts (preferring bot account if connected, with automatic broadcaster fallback on 401/403).
func (c *Connector) SendShoutout(ctx context.Context, targetUserID, targetLogin string) error {
	sendWith := func(targetMode string) (int, bool, error) {
		httpClient, helixBase, clientID, token, moderatorID, broadcasterID, usedBot, err := c.resolveWriteContextAs(ctx, targetMode)
		if err != nil {
			return 0, usedBot, err
		}
		toID, err := c.resolveTwitchUserID(ctx, httpClient, helixBase, clientID, token, targetUserID, targetLogin)
		if err != nil {
			return 0, usedBot, err
		}

		reqURL := fmt.Sprintf("%s/chat/shoutouts?from_broadcaster_id=%s&to_broadcaster_id=%s&moderator_id=%s",
			helixBase, url.QueryEscape(broadcasterID), url.QueryEscape(toID), url.QueryEscape(moderatorID))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		if err != nil {
			return 0, usedBot, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", clientID)

		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, usedBot, err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return resp.StatusCode, usedBot, fmt.Errorf("twitch send shoutout status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return resp.StatusCode, usedBot, nil
	}

	status, usedBot, err := sendWith("auto")
	if err != nil && usedBot && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		c.mu.Lock()
		hasPrimary := c.oauth != nil && c.oauth.HasToken()
		c.mu.Unlock()
		if hasPrimary {
			_, _, err = sendWith("broadcaster")
		}
	}
	return err
}

// UpdateRedemptionStatus marks a Channel Points redemption as FULFILLED or CANCELED.
func (c *Connector) UpdateRedemptionStatus(ctx context.Context, rewardID, redemptionID, status string) error {
	cleanReward := strings.TrimSpace(rewardID)
	cleanRedemption := strings.TrimSpace(redemptionID)
	cleanStatus := strings.ToUpper(strings.TrimSpace(status))
	if cleanReward == "" || cleanRedemption == "" {
		return errors.New("reward_id and redemption_id are required")
	}
	if cleanStatus != "FULFILLED" && cleanStatus != "CANCELED" {
		return fmt.Errorf("invalid redemption status %q (must be FULFILLED or CANCELED)", status)
	}

	httpClient, helixBase, clientID, token, _, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(map[string]string{"status": cleanStatus})
	if err != nil {
		return err
	}
	reqURL := fmt.Sprintf("%s/channel_points/custom_rewards/redemptions?broadcaster_id=%s&reward_id=%s&id=%s",
		helixBase, url.QueryEscape(broadcasterID), url.QueryEscape(cleanReward), url.QueryEscape(cleanRedemption))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, reqURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twitch update redemption status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// CreateClip creates a clip of the active Twitch broadcast and returns its edit URL.
func (c *Connector) CreateClip(ctx context.Context) (string, error) {
	httpClient, helixBase, clientID, token, _, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return "", err
	}
	reqURL := fmt.Sprintf("%s/clips?broadcaster_id=%s", helixBase, url.QueryEscape(broadcasterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("twitch create clip status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			EditURL string `json:"edit_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Data) == 0 {
		return "", errors.New("twitch create clip returned empty data")
	}
	if u := strings.TrimSpace(parsed.Data[0].EditURL); u != "" {
		return u, nil
	}
	if id := strings.TrimSpace(parsed.Data[0].ID); id != "" {
		return "https://clips.twitch.tv/" + id, nil
	}
	return "", nil
}

// CreateStreamMarker creates a Twitch stream marker with an optional description.
func (c *Connector) CreateStreamMarker(ctx context.Context, description string) error {
	httpClient, helixBase, clientID, token, _, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"user_id": broadcasterID,
	}
	if d := strings.TrimSpace(description); d != "" {
		payload["description"] = d
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, helixBase+"/streams/markers", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twitch create marker status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ModifyChannelInfo updates Twitch channel title, category (game_id), or tags via Helix PATCH /channels.
func (c *Connector) ModifyChannelInfo(ctx context.Context, title, categoryID string, tags []string) error {
	httpClient, helixBase, clientID, token, _, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}
	payload := make(map[string]any)
	if t := strings.TrimSpace(title); t != "" {
		payload["title"] = t
	}
	if g := strings.TrimSpace(categoryID); g != "" {
		payload["game_id"] = g
	}
	if len(tags) > 0 {
		payload["tags"] = tags
	}
	if len(payload) == 0 {
		return errors.New("no channel modifications specified")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reqURL := fmt.Sprintf("%s/channels?broadcaster_id=%s", helixBase, url.QueryEscape(broadcasterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, reqURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twitch modify channel status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// UnbanUser removes a ban or timeout for a Twitch user via Helix DELETE /moderation/bans.
func (c *Connector) UnbanUser(ctx context.Context, targetUserID, targetLogin string) error {
	httpClient, helixBase, clientID, token, moderatorID, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}
	targetID, err := c.resolveTwitchUserID(ctx, httpClient, helixBase, clientID, token, targetUserID, targetLogin)
	if err != nil {
		return err
	}
	reqURL := fmt.Sprintf("%s/moderation/bans?broadcaster_id=%s&moderator_id=%s&user_id=%s",
		helixBase, url.QueryEscape(broadcasterID), url.QueryEscape(moderatorID), url.QueryEscape(targetID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twitch unban status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
