package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"golang.org/x/net/websocket"
)

const (
	DefaultEventSubWSURL        = "wss://eventsub.wss.twitch.tv/ws"
	DefaultHelixBaseURL         = "https://api.twitch.tv/helix"
	DefaultOAuthValidateURL     = "https://id.twitch.tv/oauth2/validate"
	DefaultTwitchGQLURL         = "https://gql.twitch.tv/gql"
	DefaultTwitchPublicClientID = "kimne78kx3ncx6brgo4mv6wki5h1ko"
	DefaultLivePollInterval     = 30 * time.Second
)

// eventSubEnvelope represents the top-level Twitch EventSub WebSocket frame.
type eventSubEnvelope struct {
	Metadata struct {
		MessageID        string `json:"message_id"`
		MessageType      string `json:"message_type"`
		MessageTimestamp string `json:"message_timestamp"`
		SubscriptionType string `json:"subscription_type,omitempty"`
	} `json:"metadata"`
	Payload json.RawMessage `json:"payload"`
}

type eventSubWelcomePayload struct {
	Session struct {
		ID                      string `json:"id"`
		Status                  string `json:"status"`
		KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
		ReconnectURL            string `json:"reconnect_url"`
	} `json:"session"`
}

type eventSubNotificationPayload struct {
	Subscription struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Version string `json:"version"`
	} `json:"subscription"`
	Event json.RawMessage `json:"event"`
}

type eventSubFollowEvent struct {
	UserID               string `json:"user_id"`
	UserLogin            string `json:"user_login"`
	UserName             string `json:"user_name"`
	BroadcasterUserID    string `json:"broadcaster_user_id"`
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
	BroadcasterUserName  string `json:"broadcaster_user_name"`
	FollowedAt           string `json:"followed_at"`
}

type eventSubRedemptionEvent struct {
	ID                   string `json:"id"`
	BroadcasterUserID    string `json:"broadcaster_user_id"`
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
	BroadcasterUserName  string `json:"broadcaster_user_name"`
	UserID               string `json:"user_id"`
	UserLogin            string `json:"user_login"`
	UserName             string `json:"user_name"`
	UserInput            string `json:"user_input"`
	Status               string `json:"status"`
	RedeemedAt           string `json:"redeemed_at"`
	Reward               struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Cost   int    `json:"cost"`
		Prompt string `json:"prompt"`
	} `json:"reward"`
}

// ParseEventSubNotification parses a Twitch EventSub notification payload into a normalized chat.Event.
func ParseEventSubNotification(subType string, msgID string, eventRaw json.RawMessage, fallbackChannel string) (*chat.Event, error) {
	switch subType {
	case "channel.follow":
		var fe eventSubFollowEvent
		if err := json.Unmarshal(eventRaw, &fe); err != nil {
			return nil, fmt.Errorf("unmarshal channel.follow event: %w", err)
		}
		userLogin := strings.ToLower(strings.TrimSpace(fe.UserLogin))
		displayName := strings.TrimSpace(fe.UserName)
		if displayName == "" {
			displayName = userLogin
		}
		if userLogin == "" {
			return nil, nil
		}
		ch := cleanChannelName(fe.BroadcasterUserLogin)
		if ch == "" {
			ch = fallbackChannel
		}
		ts := time.Now()
		if fe.FollowedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, fe.FollowedAt); err == nil {
				ts = parsed
			}
		}
		id := msgID
		if id == "" {
			id = fmt.Sprintf("twitch-follow-%s-%d", userLogin, ts.UnixNano())
		}
		return &chat.Event{
			ID:          id,
			Platform:    "twitch",
			Channel:     ch,
			Kind:        chat.EventKindFollow,
			Timestamp:   ts,
			UserID:      fe.UserID,
			UserName:    userLogin,
			DisplayName: displayName,
		}, nil

	case "channel.channel_points_custom_reward_redemption.add":
		var re eventSubRedemptionEvent
		if err := json.Unmarshal(eventRaw, &re); err != nil {
			return nil, fmt.Errorf("unmarshal redemption event: %w", err)
		}
		userLogin := strings.ToLower(strings.TrimSpace(re.UserLogin))
		displayName := strings.TrimSpace(re.UserName)
		if displayName == "" {
			displayName = userLogin
		}
		if userLogin == "" {
			return nil, nil
		}
		ch := cleanChannelName(re.BroadcasterUserLogin)
		if ch == "" {
			ch = fallbackChannel
		}
		ts := time.Now()
		if re.RedeemedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, re.RedeemedAt); err == nil {
				ts = parsed
			}
		}
		id := re.ID
		if id == "" {
			id = msgID
		}
		if id == "" {
			id = fmt.Sprintf("twitch-redemption-%s-%d", userLogin, ts.UnixNano())
		}
		return &chat.Event{
			ID:          id,
			Platform:    "twitch",
			Channel:     ch,
			Kind:        chat.EventKindRedemption,
			Timestamp:   ts,
			UserID:      re.UserID,
			UserName:    userLogin,
			DisplayName: displayName,
			Text:        strings.TrimSpace(re.UserInput),
			Amount:      float64(re.Reward.Cost),
			Currency:    "points",
			RewardID:    strings.TrimSpace(re.Reward.ID),
			RewardTitle: strings.TrimSpace(re.Reward.Title),
		}, nil
	}

	return nil, nil
}

func (c *Connector) startEventSubLocked() {
	c.stopEventSubLocked()

	if !c.running || c.channel == "" || c.oauth == nil || !c.oauth.HasToken() {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.eventSubCancel = cancel
	ch := c.channel

	go c.runEventSubLoop(ctx, ch)
}

func (c *Connector) stopEventSubLocked() {
	if c.eventSubCancel != nil {
		c.eventSubCancel()
		c.eventSubCancel = nil
	}
	if c.eventSubConn != nil {
		_ = c.eventSubConn.Close()
		c.eventSubConn = nil
	}
}

func (c *Connector) runEventSubLoop(ctx context.Context, channel string) {
	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second

	wsURL := c.eventSubWSURL
	if wsURL == "" {
		wsURL = DefaultEventSubWSURL
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		reconnectURL, err := c.connectAndStreamEventSub(ctx, wsURL, channel)
		if ctx.Err() != nil {
			return
		}

		if reconnectURL != "" {
			wsURL = reconnectURL
			backoff = 2 * time.Second
			continue
		}

		wsURL = c.eventSubWSURL
		if wsURL == "" {
			wsURL = DefaultEventSubWSURL
		}

		if err != nil {
			log.Printf("[Twitch EventSub] Disconnected for #%s: %v (retrying in %v)", channel, err, backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Connector) connectAndStreamEventSub(ctx context.Context, wsURL string, channel string) (string, error) {
	if c.oauth == nil {
		return "", errors.New("oauth manager not configured")
	}

	token, err := c.oauth.GetValidAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("get valid access token: %w", err)
	}

	moderatorID, broadcasterID, err := c.resolveEventSubUserIDs(ctx, token, channel)
	if err != nil {
		return "", fmt.Errorf("resolve user IDs: %w", err)
	}

	wsCfg, err := websocket.NewConfig(wsURL, "https://www.twitch.tv")
	if err != nil {
		return "", fmt.Errorf("eventsub websocket config: %w", err)
	}
	conn, err := websocket.DialConfig(wsCfg)
	if err != nil {
		return "", fmt.Errorf("dial eventsub websocket: %w", err)
	}

	c.mu.Lock()
	if ctx.Err() != nil {
		c.mu.Unlock()
		_ = conn.Close()
		return "", ctx.Err()
	}
	c.eventSubConn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if c.eventSubConn == conn {
			c.eventSubConn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	subscribed := false

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		var rawMsg []byte
		if err := websocket.Message.Receive(conn, &rawMsg); err != nil {
			return "", err
		}

		var env eventSubEnvelope
		if err := json.Unmarshal(rawMsg, &env); err != nil {
			continue
		}

		switch env.Metadata.MessageType {
		case "session_welcome":
			var welcome eventSubWelcomePayload
			if err := json.Unmarshal(env.Payload, &welcome); err != nil || welcome.Session.ID == "" {
				continue
			}
			if !subscribed {
				subscribed = true
				sessionID := welcome.Session.ID
				// Create EventSub subscriptions for follow and channel point redemptions
				if err := c.createEventSubSubscription(ctx, token, sessionID, "channel.follow", "2", map[string]string{
					"broadcaster_user_id": broadcasterID,
					"moderator_user_id":   moderatorID,
				}); err != nil {
					log.Printf("[Twitch EventSub] Failed to subscribe to channel.follow for #%s: %v", channel, err)
				} else {
					log.Printf("[Twitch EventSub] Subscribed to channel.follow for #%s", channel)
				}

				if err := c.createEventSubSubscription(ctx, token, sessionID, "channel.channel_points_custom_reward_redemption.add", "1", map[string]string{
					"broadcaster_user_id": broadcasterID,
				}); err != nil {
					log.Printf("[Twitch EventSub] Failed to subscribe to channel point redemptions for #%s: %v", channel, err)
				} else {
					log.Printf("[Twitch EventSub] Subscribed to channel point redemptions for #%s", channel)
				}

				_ = c.createEventSubSubscription(ctx, token, sessionID, "stream.online", "1", map[string]string{
					"broadcaster_user_id": broadcasterID,
				})
				_ = c.createEventSubSubscription(ctx, token, sessionID, "stream.offline", "1", map[string]string{
					"broadcaster_user_id": broadcasterID,
				})
			}

		case "session_reconnect":
			var rec eventSubWelcomePayload
			if err := json.Unmarshal(env.Payload, &rec); err == nil && rec.Session.ReconnectURL != "" {
				return rec.Session.ReconnectURL, nil
			}

		case "notification":
			var notif eventSubNotificationPayload
			if err := json.Unmarshal(env.Payload, &notif); err != nil {
				continue
			}
			subType := notif.Subscription.Type
			if subType == "" {
				subType = env.Metadata.SubscriptionType
			}
			switch subType {
			case "stream.online":
				c.setStreamLive(true)
				continue
			case "stream.offline":
				c.setStreamLive(false)
				continue
			}
			ev, err := ParseEventSubNotification(subType, env.Metadata.MessageID, notif.Event, channel)
			if err != nil {
				log.Printf("[Twitch EventSub] Error parsing %s notification: %v", subType, err)
				continue
			}
			if ev != nil && c.onEvent != nil {
				c.onEvent(ev)
			}
		}
	}
}

func (c *Connector) startLivePollLocked(channel string) {
	c.stopLivePollLocked()
	if !c.running || channel == "" {
		return
	}

	interval := c.livePollInterval
	if interval <= 0 {
		interval = DefaultLivePollInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.livePollCancel = cancel

	go func(ch string, pollEvery time.Duration) {
		if live, err := c.checkChannelLive(ctx, ch); err == nil && ctx.Err() == nil {
			c.setStreamLive(live)
		}
		ticker := time.NewTicker(pollEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if live, err := c.checkChannelLive(ctx, ch); err == nil && ctx.Err() == nil {
					c.setStreamLive(live)
				}
			}
		}
	}(channel, interval)
}

func (c *Connector) stopLivePollLocked() {
	if c.livePollCancel != nil {
		c.livePollCancel()
		c.livePollCancel = nil
	}
}

func (c *Connector) checkChannelLive(ctx context.Context, channel string) (bool, error) {
	c.mu.Lock()
	customFn := c.checkLiveFn
	httpClient := c.httpClient
	helixBase := strings.TrimRight(c.helixBaseURL, "/")
	gqlURL := c.gqlURL
	oauth := c.oauth
	c.mu.Unlock()

	if customFn != nil {
		return customFn(ctx, channel)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	if gqlURL == "" {
		gqlURL = DefaultTwitchGQLURL
	}

	// 1. Prefer authenticated Helix /streams?user_login=<channel> when OAuth is connected
	if oauth != nil && oauth.HasToken() {
		if token, err := oauth.GetValidAccessToken(ctx); err == nil && token != "" {
			streamsURL := fmt.Sprintf("%s/streams?user_login=%s", helixBase, url.QueryEscape(channel))
			if req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, streamsURL, nil); reqErr == nil {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Client-Id", oauth.ClientID())
				if resp, doErr := httpClient.Do(req); doErr == nil {
					var sResp struct {
						Data []struct {
							ID   string `json:"id"`
							Type string `json:"type"`
						} `json:"data"`
					}
					_ = json.NewDecoder(resp.Body).Decode(&sResp)
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						isLive := len(sResp.Data) > 0 && (strings.EqualFold(sResp.Data[0].Type, "live") || sResp.Data[0].ID != "")
						return isLive, nil
					}
				}
			}
		}
	}

	// 2. Zero-auth fallback via Twitch public GQL UseLive query
	gqlBody := map[string]any{
		"query":     `query($login:String!){user(login:$login){stream{id}}}`,
		"variables": map[string]string{"login": channel},
	}
	rawBody, err := json.Marshal(gqlBody)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlURL, bytes.NewReader(rawBody))
	if err != nil {
		return false, err
	}
	req.Header.Set("Client-ID", DefaultTwitchPublicClientID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("twitch gql status %d", resp.StatusCode)
	}

	var gqlResp struct {
		Data struct {
			User *struct {
				Stream *struct {
					ID string `json:"id"`
				} `json:"stream"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return false, err
	}
	if gqlResp.Data.User == nil || gqlResp.Data.User.Stream == nil {
		return false, nil
	}
	return strings.TrimSpace(gqlResp.Data.User.Stream.ID) != "", nil
}

func (c *Connector) resolveEventSubUserIDs(ctx context.Context, token string, channelLogin string) (moderatorID string, broadcasterID string, err error) {
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	helixBase := strings.TrimRight(c.helixBaseURL, "/")
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	validateURL := c.oauthValidateURL
	if validateURL == "" {
		validateURL = DefaultOAuthValidateURL
	}
	clientID := c.oauth.ClientID()

	// 1. Validate OAuth token to obtain authenticated user_id (moderator_user_id)
	valReq, err := http.NewRequestWithContext(ctx, http.MethodGet, validateURL, nil)
	if err == nil {
		valReq.Header.Set("Authorization", "OAuth "+token)
		if resp, doErr := httpClient.Do(valReq); doErr == nil {
			var valData struct {
				UserID string `json:"user_id"`
				Login  string `json:"login"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&valData)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && valData.UserID != "" {
				moderatorID = valData.UserID
				if strings.EqualFold(valData.Login, channelLogin) {
					broadcasterID = valData.UserID
				}
			}
		}
	}

	// 2. If broadcasterID (or moderatorID) is still unknown, query Helix /users
	if broadcasterID == "" {
		uURL := fmt.Sprintf("%s/users?login=%s", helixBase, url.QueryEscape(channelLogin))
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uURL, nil)
		if reqErr != nil {
			return "", "", reqErr
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", clientID)

		resp, doErr := httpClient.Do(req)
		if doErr != nil {
			return "", "", doErr
		}
		var usersResp struct {
			Data []struct {
				ID    string `json:"id"`
				Login string `json:"login"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&usersResp)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(usersResp.Data) == 0 {
			return "", "", fmt.Errorf("helix /users lookup failed for %q (status %d)", channelLogin, resp.StatusCode)
		}
		broadcasterID = usersResp.Data[0].ID
	}

	if moderatorID == "" {
		moderatorID = broadcasterID
	}

	return moderatorID, broadcasterID, nil
}

func (c *Connector) createEventSubSubscription(ctx context.Context, token string, sessionID string, subType string, version string, condition map[string]string) error {
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	helixBase := strings.TrimRight(c.helixBaseURL, "/")
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	clientID := c.oauth.ClientID()

	bodyObj := map[string]any{
		"type":      subType,
		"version":   version,
		"condition": condition,
		"transport": map[string]string{
			"method":     "websocket",
			"session_id": sessionID,
		},
	}
	rawBody, err := json.Marshal(bodyObj)
	if err != nil {
		return err
	}

	subURL := helixBase + "/eventsub/subscriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, subURL, bytes.NewReader(rawBody))
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

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("eventsub subscribe %s status %d: %s", subType, resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	return nil
}
