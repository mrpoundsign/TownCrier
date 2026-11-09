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

// AutoModHoldEvent represents an inbound Twitch EventSub automod.message.hold or automod.message.update (v2) event.
type AutoModHoldEvent struct {
	MessageID            string    `json:"message_id"`
	BroadcasterUserID    string    `json:"broadcaster_user_id"`
	BroadcasterUserLogin string    `json:"broadcaster_user_login"`
	Channel              string    `json:"channel"`
	UserID               string    `json:"user_id"`
	UserLogin            string    `json:"user_login"`
	UserName             string    `json:"user_name"`
	Text                 string    `json:"text"`
	Reason               string    `json:"reason"`
	HeldAt               time.Time `json:"held_at"`
	Status               string    `json:"status"` // "PENDING", "APPROVED", "DENIED", "EXPIRED"
}

type eventSubAutoModPayload struct {
	BroadcasterUserID    string `json:"broadcaster_user_id"`
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
	BroadcasterUserName  string `json:"broadcaster_user_name"`
	UserID               string `json:"user_id"`
	UserLogin            string `json:"user_login"`
	UserName             string `json:"user_name"`
	MessageID            string `json:"message_id"`
	Message              struct {
		Text string `json:"text"`
	} `json:"message"`
	Reason  string `json:"reason"`
	Automod *struct {
		Category string `json:"category"`
		Level    int    `json:"level"`
	} `json:"automod"`
	BlockedTerm *struct {
		TermsFound []struct {
			Boundary struct {
				StartPos int `json:"start_pos"`
				EndPos   int `json:"end_pos"`
			} `json:"boundary"`
		} `json:"terms_found"`
	} `json:"blocked_term"`
	HeldAt string `json:"held_at"`
	Status string `json:"status"`
}

// SetOnAutoModHold registers the callback invoked when Twitch EventSub delivers an automod.message.hold or automod.message.update notification.
func (c *Connector) SetOnAutoModHold(fn func(ev *AutoModHoldEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onAutoModHold = fn
}

func (c *Connector) emitAutoModHold(ev *AutoModHoldEvent) {
	if ev == nil {
		return
	}
	c.mu.Lock()
	fn := c.onAutoModHold
	c.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// ParseAutoModEvent parses a Twitch EventSub automod.message.hold or automod.message.update (v2) event payload.
func ParseAutoModEvent(subType string, eventRaw json.RawMessage, fallbackChannel string) (*AutoModHoldEvent, error) {
	var p eventSubAutoModPayload
	if err := json.Unmarshal(eventRaw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal %s event: %w", subType, err)
	}

	msgID := strings.TrimSpace(p.MessageID)
	userLogin := strings.ToLower(strings.TrimSpace(p.UserLogin))
	displayName := strings.TrimSpace(p.UserName)
	if displayName == "" {
		displayName = userLogin
	}
	if msgID == "" || userLogin == "" {
		return nil, nil
	}

	ch := cleanChannelName(p.BroadcasterUserLogin)
	if ch == "" {
		ch = fallbackChannel
	}

	heldAt := time.Now().UTC()
	if p.HeldAt != "" {
		if parsed, err := time.Parse(time.RFC3339, p.HeldAt); err == nil {
			heldAt = parsed
		}
	}

	reason := formatAutoModReason(p)
	status := strings.ToUpper(strings.TrimSpace(p.Status))
	if status == "" {
		if subType == "automod.message.hold" {
			status = "PENDING"
		} else {
			status = "DENIED"
		}
	}

	return &AutoModHoldEvent{
		MessageID:            msgID,
		BroadcasterUserID:    strings.TrimSpace(p.BroadcasterUserID),
		BroadcasterUserLogin: ch,
		Channel:              ch,
		UserID:               strings.TrimSpace(p.UserID),
		UserLogin:            userLogin,
		UserName:             displayName,
		Text:                 strings.TrimSpace(p.Message.Text),
		Reason:               reason,
		HeldAt:               heldAt,
		Status:               status,
	}, nil
}

func formatAutoModReason(p eventSubAutoModPayload) string {
	switch strings.ToLower(strings.TrimSpace(p.Reason)) {
	case "automod":
		if p.Automod != nil && strings.TrimSpace(p.Automod.Category) != "" {
			cat := strings.TrimSpace(p.Automod.Category)
			if p.Automod.Level > 0 {
				return fmt.Sprintf("AutoMod: %s (Lvl %d)", cat, p.Automod.Level)
			}
			return "AutoMod: " + cat
		}
		return "AutoMod"
	case "blocked_term":
		if p.BlockedTerm != nil && len(p.BlockedTerm.TermsFound) > 0 {
			b := p.BlockedTerm.TermsFound[0].Boundary
			runes := []rune(p.Message.Text)
			if b.StartPos >= 0 && b.EndPos >= b.StartPos && b.EndPos < len(runes) {
				term := strings.TrimSpace(string(runes[b.StartPos : b.EndPos+1]))
				if term != "" {
					return fmt.Sprintf("Blocked Term: %q", term)
				}
			}
		}
		return "Blocked Term"
	default:
		if p.Automod != nil && strings.TrimSpace(p.Automod.Category) != "" {
			if p.Automod.Level > 0 {
				return fmt.Sprintf("AutoMod: %s (Lvl %d)", strings.TrimSpace(p.Automod.Category), p.Automod.Level)
			}
			return "AutoMod: " + strings.TrimSpace(p.Automod.Category)
		}
		if r := strings.TrimSpace(p.Reason); r != "" {
			return r
		}
		return "Held for Review"
	}
}

func (c *Connector) resolveModContext(ctx context.Context) (httpClient *http.Client, helixBase, clientID, token, moderatorID, broadcasterID string, err error) {
	c.mu.Lock()
	oauth := c.oauth
	httpClient = c.httpClient
	helixBase = strings.TrimRight(c.helixBaseURL, "/")
	channel := c.channel
	c.mu.Unlock()

	if oauth == nil || !oauth.HasToken() {
		return nil, "", "", "", "", "", errors.New("twitch OAuth not connected")
	}
	if channel == "" {
		return nil, "", "", "", "", "", errors.New("no active Twitch channel configured")
	}

	token, err = oauth.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return nil, "", "", "", "", "", fmt.Errorf("twitch access token error: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	clientID = oauth.ClientID()
	if clientID == "" {
		clientID = DefaultTwitchClientID
	}

	moderatorID, broadcasterID, err = c.resolveEventSubUserIDs(ctx, token, channel)
	if err != nil {
		return nil, "", "", "", "", "", err
	}
	return httpClient, helixBase, clientID, token, moderatorID, broadcasterID, nil
}

// DeleteChatMessage deletes a specific chat message on Twitch via Helix DELETE /moderation/chat.
func (c *Connector) DeleteChatMessage(ctx context.Context, messageID string) error {
	msgID := strings.TrimSpace(messageID)
	if msgID == "" {
		return errors.New("empty message ID")
	}

	httpClient, helixBase, clientID, token, moderatorID, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	reqURL := fmt.Sprintf("%s/moderation/chat?broadcaster_id=%s&moderator_id=%s&message_id=%s",
		helixBase,
		url.QueryEscape(broadcasterID),
		url.QueryEscape(moderatorID),
		url.QueryEscape(msgID),
	)

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
		return fmt.Errorf("twitch delete message status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// BanOrTimeoutUser bans (durationSeconds <= 0) or times out (durationSeconds > 0) a Twitch user via Helix POST /moderation/bans.
func (c *Connector) BanOrTimeoutUser(ctx context.Context, targetUserID string, targetLogin string, durationSeconds int, reason string) error {
	cleanID := strings.TrimSpace(targetUserID)
	cleanLogin := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(targetLogin, "@")))
	if cleanID == "" && cleanLogin == "" {
		return errors.New("target user ID or username is required")
	}

	httpClient, helixBase, clientID, token, moderatorID, broadcasterID, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	if cleanID == "" && cleanLogin != "" {
		uURL := fmt.Sprintf("%s/users?login=%s", helixBase, url.QueryEscape(cleanLogin))
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uURL, nil)
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", clientID)
		resp, doErr := httpClient.Do(req)
		if doErr != nil {
			return doErr
		}
		var uResp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&uResp)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && len(uResp.Data) > 0 {
			cleanID = strings.TrimSpace(uResp.Data[0].ID)
		}
	}
	if cleanID == "" {
		return fmt.Errorf("could not resolve Twitch user ID for %q", targetLogin)
	}

	dataObj := map[string]any{
		"user_id": cleanID,
	}
	if durationSeconds > 0 {
		dataObj["duration"] = durationSeconds
	}
	if r := strings.TrimSpace(reason); r != "" {
		dataObj["reason"] = r
	}

	rawBody, err := json.Marshal(map[string]any{"data": dataObj})
	if err != nil {
		return err
	}

	reqURL := fmt.Sprintf("%s/moderation/bans?broadcaster_id=%s&moderator_id=%s",
		helixBase,
		url.QueryEscape(broadcasterID),
		url.QueryEscape(moderatorID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(rawBody))
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
		return fmt.Errorf("twitch ban/timeout status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ResolveAutoModMessage approves ("ALLOW") or denies ("DENY") a held AutoMod message via Helix POST /moderation/automod/message.
func (c *Connector) ResolveAutoModMessage(ctx context.Context, msgID string, action string) error {
	cleanMsgID := strings.TrimSpace(msgID)
	cleanAction := strings.ToUpper(strings.TrimSpace(action))
	if cleanMsgID == "" {
		return errors.New("empty held message ID")
	}
	if cleanAction != "ALLOW" && cleanAction != "DENY" {
		return fmt.Errorf("invalid automod action %q (must be ALLOW or DENY)", action)
	}

	httpClient, helixBase, clientID, token, moderatorID, _, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	rawBody, err := json.Marshal(map[string]string{
		"user_id": moderatorID,
		"msg_id":  cleanMsgID,
		"action":  cleanAction,
	})
	if err != nil {
		return err
	}

	reqURL := helixBase + "/moderation/automod/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(rawBody))
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
		return fmt.Errorf("twitch automod resolve status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
