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
	DefaultFollowerCacheTTL     = 30 * time.Minute
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

type eventSubChatNotificationEvent struct {
	BroadcasterUserID    string `json:"broadcaster_user_id"`
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
	BroadcasterUserName  string `json:"broadcaster_user_name"`
	ChatterUserID        string `json:"chatter_user_id"`
	ChatterUserLogin     string `json:"chatter_user_login"`
	ChatterUserName      string `json:"chatter_user_name"`
	ChatterIsAnonymous   bool   `json:"chatter_is_anonymous"`
	MessageID            string `json:"message_id"`
	SystemMessage        string `json:"system_message"`
	NoticeType           string `json:"notice_type"`
	Message              struct {
		Text string `json:"text"`
	} `json:"message"`
	Badges []struct {
		SetID string `json:"set_id"`
		ID    string `json:"id"`
	} `json:"badges"`
	Sub *struct {
		SubTier        string `json:"sub_tier"`
		IsPrime        bool   `json:"is_prime"`
		DurationMonths int    `json:"duration_months"`
	} `json:"sub"`
	Resub *struct {
		CumulativeMonths int    `json:"cumulative_months"`
		DurationMonths   int    `json:"duration_months"`
		StreakMonths     int    `json:"streak_months"`
		SubTier          string `json:"sub_tier"`
		IsPrime          bool   `json:"is_prime"`
	} `json:"resub"`
	SubGift *struct {
		DurationMonths     int    `json:"duration_months"`
		CumulativeTotal    int    `json:"cumulative_total"`
		RecipientUserID    string `json:"recipient_user_id"`
		RecipientUserName  string `json:"recipient_user_name"`
		RecipientUserLogin string `json:"recipient_user_login"`
		SubTier            string `json:"sub_tier"`
	} `json:"sub_gift"`
	CommunitySubGift *struct {
		ID              string `json:"id"`
		Total           int    `json:"total"`
		SubTier         string `json:"sub_tier"`
		CumulativeTotal int    `json:"cumulative_total"`
	} `json:"community_sub_gift"`
	GiftPaidUpgrade *struct {
		GifterIsAnonymous bool   `json:"gifter_is_anonymous"`
		GifterUserID      string `json:"gifter_user_id"`
		GifterUserName    string `json:"gifter_user_name"`
		GifterUserLogin   string `json:"gifter_user_login"`
	} `json:"gift_paid_upgrade"`
	PrimePaidUpgrade *struct {
		SubTier string `json:"sub_tier"`
	} `json:"prime_paid_upgrade"`
	PayItForward *struct {
		GifterIsAnonymous  bool   `json:"gifter_is_anonymous"`
		GifterUserID       string `json:"gifter_user_id"`
		GifterUserName     string `json:"gifter_user_name"`
		GifterUserLogin    string `json:"gifter_user_login"`
		RecipientUserID    string `json:"recipient_user_id"`
		RecipientUserName  string `json:"recipient_user_name"`
		RecipientUserLogin string `json:"recipient_user_login"`
	} `json:"pay_it_forward"`
	Raid *struct {
		UserID      string `json:"user_id"`
		UserName    string `json:"user_name"`
		UserLogin   string `json:"user_login"`
		ViewerCount int    `json:"viewer_count"`
	} `json:"raid"`
	Announcement *struct {
		Color string `json:"color"`
	} `json:"announcement"`
	BitsBadgeTier *struct {
		Tier int `json:"tier"`
	} `json:"bits_badge_tier"`
	CharityDonation *struct {
		CharityName string `json:"charity_name"`
		Amount      struct {
			Value         int    `json:"value"`
			DecimalPlaces int    `json:"decimal_places"`
			Currency      string `json:"currency"`
		} `json:"amount"`
	} `json:"charity_donation"`
	WatchStreak *struct {
		StreakCount          int `json:"streak_count"`
		ChannelPointsAwarded int `json:"channel_points_awarded"`
	} `json:"watch_streak"`
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
			Roles:       []string{"follower"},
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

	case "channel.chat.notification":
		return parseChatNotificationEvent(msgID, eventRaw, fallbackChannel)
	}

	return nil, nil
}

func parseChatNotificationEvent(msgID string, eventRaw json.RawMessage, fallbackChannel string) (*chat.Event, error) {
	var ce eventSubChatNotificationEvent
	if err := json.Unmarshal(eventRaw, &ce); err != nil {
		return nil, fmt.Errorf("unmarshal channel.chat.notification event: %w", err)
	}

	noticeType := strings.ToLower(strings.TrimSpace(ce.NoticeType))
	noticeType = strings.TrimPrefix(noticeType, "shared_chat_")

	userLogin := strings.ToLower(strings.TrimSpace(ce.ChatterUserLogin))
	displayName := strings.TrimSpace(ce.ChatterUserName)
	userID := strings.TrimSpace(ce.ChatterUserID)
	if ce.ChatterIsAnonymous && userLogin == "" {
		userLogin = "anonymous"
		displayName = "Anonymous"
	}
	if displayName == "" {
		displayName = userLogin
	}

	var roles []string
	for _, b := range ce.Badges {
		switch b.SetID {
		case "broadcaster":
			roles = append(roles, "broadcaster")
		case "moderator":
			roles = append(roles, "mod")
		case "vip":
			roles = append(roles, "vip")
		case "subscriber", "founder":
			roles = append(roles, "subscriber")
		}
	}

	var (
		kind        chat.EventKind
		text        = strings.TrimSpace(ce.Message.Text)
		months      int
		count       int
		amount      float64
		currency    string
		subTier     string
		recipient   string
		itemName    string
		rewardTitle string
	)

	switch noticeType {
	case "watch_streak":
		kind = chat.EventKindWatchStreak
		count = 1
		if ce.WatchStreak != nil {
			if ce.WatchStreak.StreakCount > 0 {
				count = ce.WatchStreak.StreakCount
			}
			if ce.WatchStreak.ChannelPointsAwarded > 0 {
				amount = float64(ce.WatchStreak.ChannelPointsAwarded)
				currency = "points"
			}
		}
	case "sub":
		kind = chat.EventKindSub
		months = 1
		count = 1
		if ce.Sub != nil {
			if ce.Sub.IsPrime {
				subTier = "Prime"
			} else {
				subTier = formatTwitchSubPlan(ce.Sub.SubTier)
			}
		}
	case "resub":
		kind = chat.EventKindResub
		months = 1
		count = 1
		if ce.Resub != nil {
			if ce.Resub.CumulativeMonths > 0 {
				months = ce.Resub.CumulativeMonths
			}
			if ce.Resub.IsPrime {
				subTier = "Prime"
			} else {
				subTier = formatTwitchSubPlan(ce.Resub.SubTier)
			}
		}
	case "sub_gift":
		kind = chat.EventKindGiftSub
		count = 1
		if ce.SubGift != nil {
			recipient = strings.TrimSpace(ce.SubGift.RecipientUserName)
			if recipient == "" {
				recipient = strings.TrimSpace(ce.SubGift.RecipientUserLogin)
			}
			subTier = formatTwitchSubPlan(ce.SubGift.SubTier)
		}
		if recipient != "" {
			text = "gifted a subscription to " + recipient
		}
	case "community_sub_gift":
		kind = chat.EventKindGiftSub
		count = 1
		if ce.CommunitySubGift != nil {
			if ce.CommunitySubGift.Total > 0 {
				count = ce.CommunitySubGift.Total
			}
			subTier = formatTwitchSubPlan(ce.CommunitySubGift.SubTier)
		}
		text = fmt.Sprintf("gifted %d community subscriptions!", count)
	case "prime_paid_upgrade":
		kind = chat.EventKindSubUpgrade
		count = 1
		if ce.PrimePaidUpgrade != nil {
			subTier = formatTwitchSubPlan(ce.PrimePaidUpgrade.SubTier)
		}
		if subTier != "" {
			itemName = "Upgraded from a Prime subscription to " + subTier
		} else {
			itemName = "Upgraded from a Prime subscription"
		}
	case "gift_paid_upgrade":
		kind = chat.EventKindSubUpgrade
		count = 1
		if ce.GiftPaidUpgrade != nil && !ce.GiftPaidUpgrade.GifterIsAnonymous {
			recipient = strings.TrimSpace(ce.GiftPaidUpgrade.GifterUserName)
			if recipient == "" {
				recipient = strings.TrimSpace(ce.GiftPaidUpgrade.GifterUserLogin)
			}
		}
		if recipient != "" {
			itemName = "Upgraded a gift subscription from " + recipient
		} else {
			itemName = "Upgraded a gift subscription"
		}
	case "pay_it_forward":
		kind = chat.EventKindSubUpgrade
		count = 1
		if ce.PayItForward != nil {
			recipient = strings.TrimSpace(ce.PayItForward.RecipientUserName)
			if recipient == "" {
				recipient = strings.TrimSpace(ce.PayItForward.RecipientUserLogin)
			}
		}
		if recipient != "" {
			itemName = "Paid forward a gift subscription to " + recipient
		} else {
			itemName = "Paid forward a gift subscription to the community"
		}
	case "raid":
		kind = chat.EventKindRaid
		if ce.Raid != nil {
			if ce.Raid.UserLogin != "" {
				userLogin = strings.ToLower(strings.TrimSpace(ce.Raid.UserLogin))
			}
			if ce.Raid.UserName != "" {
				displayName = strings.TrimSpace(ce.Raid.UserName)
			}
			if ce.Raid.UserID != "" {
				userID = strings.TrimSpace(ce.Raid.UserID)
			}
			count = ce.Raid.ViewerCount
		}
		text = fmt.Sprintf("raided with %d viewers!", count)
	case "bits_badge_tier":
		kind = chat.EventKindBitsBadgeTier
		if ce.BitsBadgeTier != nil {
			count = ce.BitsBadgeTier.Tier
			amount = float64(ce.BitsBadgeTier.Tier)
		}
		currency = "bits"
	case "announcement":
		kind = chat.EventKindAnnouncement
	case "charity_donation":
		kind = chat.EventKindCharityDonation
		if ce.CharityDonation != nil {
			rewardTitle = strings.TrimSpace(ce.CharityDonation.CharityName)
			itemName = rewardTitle
			val := float64(ce.CharityDonation.Amount.Value)
			for range ce.CharityDonation.Amount.DecimalPlaces {
				val /= 10
			}
			amount = val
			currency = strings.ToUpper(strings.TrimSpace(ce.CharityDonation.Amount.Currency))
			if currency == "" {
				currency = "USD"
			}
		}
	default:
		return nil, nil
	}

	if userLogin == "" {
		return nil, nil
	}

	ch := cleanChannelName(ce.BroadcasterUserLogin)
	if ch == "" {
		ch = fallbackChannel
	}
	id := strings.TrimSpace(ce.MessageID)
	if id == "" {
		id = msgID
	}
	if id == "" {
		id = fmt.Sprintf("twitch-notif-%s-%s-%d", noticeType, userLogin, time.Now().UnixNano())
	}

	return &chat.Event{
		ID:          id,
		Platform:    "twitch",
		Channel:     ch,
		Kind:        kind,
		Timestamp:   time.Now(),
		UserID:      userID,
		UserName:    userLogin,
		DisplayName: displayName,
		Roles:       roles,
		Text:        text,
		Amount:      amount,
		Currency:    currency,
		ItemName:    itemName,
		RewardTitle: rewardTitle,
		SubTier:     subTier,
		Recipient:   recipient,
		Months:      months,
		Count:       count,
	}, nil
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

// CheckUserFollowing checks whether a Twitch user follows the currently configured channel,
// caching the result per user for 30 minutes (DefaultFollowerCacheTTL).
func (c *Connector) CheckUserFollowing(ctx context.Context, userID string, userLogin string) (bool, error) {
	cleanID := strings.TrimSpace(userID)
	cleanLogin := strings.ToLower(strings.TrimSpace(userLogin))
	if cleanID == "" && cleanLogin == "" {
		return false, nil
	}

	c.mu.Lock()
	channel := c.channel
	ttl := c.followerCacheTTL
	if ttl <= 0 {
		ttl = DefaultFollowerCacheTTL
	}
	if cleanID != "" {
		if entry, ok := c.followerCache["id:"+cleanID]; ok && time.Since(entry.checkedAt) < ttl {
			c.mu.Unlock()
			return entry.following, nil
		}
	}
	if cleanLogin != "" {
		if entry, ok := c.followerCache["login:"+cleanLogin]; ok && time.Since(entry.checkedAt) < ttl {
			c.mu.Unlock()
			return entry.following, nil
		}
	}
	customFn := c.checkFollowerFn
	oauth := c.oauth
	httpClient := c.httpClient
	helixBase := strings.TrimRight(c.helixBaseURL, "/")
	broadcasterID := c.broadcasterID
	c.mu.Unlock()

	if channel == "" {
		return false, nil
	}
	if cleanLogin != "" && strings.EqualFold(cleanLogin, channel) {
		return true, nil
	}

	var following bool
	if customFn != nil {
		var err error
		following, err = customFn(ctx, channel, cleanID, cleanLogin)
		if err != nil {
			return false, err
		}
	} else {
		if oauth == nil || !oauth.HasToken() {
			return false, nil
		}
		token, err := oauth.GetValidAccessToken(ctx)
		if err != nil || token == "" {
			return false, err
		}
		if httpClient == nil {
			httpClient = &http.Client{Timeout: 10 * time.Second}
		}
		if helixBase == "" {
			helixBase = DefaultHelixBaseURL
		}
		clientID := DefaultTwitchPublicClientID
		if oauth.ClientID() != "" {
			clientID = oauth.ClientID()
		}

		if broadcasterID == "" {
			_, resolvedBroadcasterID, resErr := c.resolveEventSubUserIDs(ctx, token, channel)
			if resErr != nil || resolvedBroadcasterID == "" {
				return false, resErr
			}
			broadcasterID = resolvedBroadcasterID
			c.mu.Lock()
			if c.channel == channel {
				c.broadcasterID = broadcasterID
			}
			c.mu.Unlock()
		}

		if cleanID == "" && cleanLogin != "" {
			uURL := fmt.Sprintf("%s/users?login=%s", helixBase, url.QueryEscape(cleanLogin))
			if req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, uURL, nil); reqErr == nil {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Client-Id", clientID)
				if resp, doErr := httpClient.Do(req); doErr == nil {
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
			}
		}
		if cleanID == "" {
			return false, nil
		}

		reqURL := fmt.Sprintf("%s/channels/followers?broadcaster_id=%s&user_id=%s",
			helixBase, url.QueryEscape(broadcasterID), url.QueryEscape(cleanID))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", clientID)

		resp, err := httpClient.Do(req)
		if err != nil {
			return false, err
		}
		var page struct {
			Data []struct {
				UserID string `json:"user_id"`
			} `json:"data"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("helix /channels/followers status %d", resp.StatusCode)
		}
		if decErr != nil {
			return false, decErr
		}
		following = len(page.Data) > 0
	}

	c.mu.Lock()
	if c.followerCache == nil {
		c.followerCache = make(map[string]followerCacheEntry)
	}
	entry := followerCacheEntry{
		following: following,
		checkedAt: time.Now(),
	}
	if cleanID != "" {
		c.followerCache["id:"+cleanID] = entry
	}
	if cleanLogin != "" {
		c.followerCache["login:"+cleanLogin] = entry
	}
	c.mu.Unlock()

	return following, nil
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

				if err := c.createEventSubSubscription(ctx, token, sessionID, "channel.chat.notification", "1", map[string]string{
					"broadcaster_user_id": broadcasterID,
					"user_id":             moderatorID,
				}); err != nil {
					log.Printf("[Twitch EventSub] Failed to subscribe to channel.chat.notification for #%s: %v", channel, err)
				} else {
					log.Printf("[Twitch EventSub] Subscribed to channel.chat.notification for #%s", channel)
				}

				if err := c.createEventSubSubscription(ctx, token, sessionID, "automod.message.hold", "2", map[string]string{
					"broadcaster_user_id": broadcasterID,
					"moderator_user_id":   moderatorID,
				}); err != nil {
					log.Printf("[Twitch EventSub] Failed to subscribe to automod.message.hold for #%s: %v", channel, err)
				} else {
					log.Printf("[Twitch EventSub] Subscribed to automod.message.hold for #%s", channel)
				}

				if err := c.createEventSubSubscription(ctx, token, sessionID, "automod.message.update", "2", map[string]string{
					"broadcaster_user_id": broadcasterID,
					"moderator_user_id":   moderatorID,
				}); err != nil {
					log.Printf("[Twitch EventSub] Failed to subscribe to automod.message.update for #%s: %v", channel, err)
				} else {
					log.Printf("[Twitch EventSub] Subscribed to automod.message.update for #%s", channel)
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
			case "automod.message.hold", "automod.message.update":
				holdEv, holdErr := ParseAutoModEvent(subType, notif.Event, channel)
				if holdErr != nil {
					log.Printf("[Twitch EventSub] Error parsing %s notification: %v", subType, holdErr)
					continue
				}
				if holdEv != nil {
					c.emitAutoModHold(holdEv)
				}
				continue
			}
			ev, err := ParseEventSubNotification(subType, env.Metadata.MessageID, notif.Event, channel)
			if err != nil {
				log.Printf("[Twitch EventSub] Error parsing %s notification: %v", subType, err)
				continue
			}
			if ev != nil {
				if ev.Kind == chat.EventKindFollow {
					c.MarkUserFollowing(ev.UserID, ev.UserName)
				}
				c.emitEvent(ev)
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
