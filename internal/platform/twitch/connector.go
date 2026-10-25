package twitch

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gempir/go-twitch-irc/v4"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"golang.org/x/net/websocket"
)

// ConnectionStatus represents the live Twitch connection state.
type ConnectionStatus string

const (
	StatusDisconnected     ConnectionStatus = "disconnected"
	StatusConnecting       ConnectionStatus = "connecting"
	StatusWaitingForStream ConnectionStatus = "waiting"
	StatusConnected        ConnectionStatus = "connected"
)

// followerCacheEntry caches whether a Twitch user follows the active channel with a 30-minute TTL.
type followerCacheEntry struct {
	following bool
	checkedAt time.Time
}

// Connector manages the Twitch IRC and EventSub WebSocket connections for stream chat reading, alerts, and moderation.
type Connector struct {
	mu               sync.Mutex
	client           *twitch.Client
	channel          string
	broadcasterID    string
	status           ConnectionStatus
	running          bool
	isLive           bool
	livePollCancel   context.CancelFunc
	livePollInterval time.Duration
	followerCache    map[string]followerCacheEntry
	followerCacheTTL time.Duration
	checkFollowerFn  func(ctx context.Context, channel string, userID string, userLogin string) (bool, error)
	gqlURL           string
	checkLiveFn      func(ctx context.Context, channel string) (bool, error)
	oauth            *OAuthManager
	eventSubCancel   context.CancelFunc
	eventSubConn     *websocket.Conn
	eventSubWSURL    string
	helixBaseURL     string
	oauthValidateURL string
	httpClient       *http.Client
	seenEventIDs     map[string]struct{}
	seenEventOrder   []string
	onEvent          func(event *chat.Event)
	onModeration     func(modEvent *chat.ModerationEvent)
	onAutoModHold    func(holdEvent *AutoModHoldEvent)
	onStatusChange   func(status ConnectionStatus, detail string)
	onStreamLive     func(live bool)
}

const maxSeenEventIDs = 256

func (c *Connector) emitEvent(ev *chat.Event) {
	if ev == nil || c.onEvent == nil {
		return
	}
	if id := strings.TrimSpace(ev.ID); id != "" {
		c.mu.Lock()
		if c.seenEventIDs == nil {
			c.seenEventIDs = make(map[string]struct{}, maxSeenEventIDs)
		}
		if _, dup := c.seenEventIDs[id]; dup {
			c.mu.Unlock()
			return
		}
		if len(c.seenEventOrder) >= maxSeenEventIDs {
			evict := c.seenEventOrder[0]
			c.seenEventOrder = c.seenEventOrder[1:]
			delete(c.seenEventIDs, evict)
		}
		c.seenEventIDs[id] = struct{}{}
		c.seenEventOrder = append(c.seenEventOrder, id)
		c.mu.Unlock()
	}
	c.onEvent(ev)
}

// NewConnector creates a new Twitch connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onModeration func(modEvent *chat.ModerationEvent),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	return &Connector{
		status:           StatusDisconnected,
		livePollInterval: DefaultLivePollInterval,
		followerCache:    make(map[string]followerCacheEntry),
		followerCacheTTL: DefaultFollowerCacheTTL,
		gqlURL:           DefaultTwitchGQLURL,
		eventSubWSURL:    DefaultEventSubWSURL,
		helixBaseURL:     DefaultHelixBaseURL,
		oauthValidateURL: DefaultOAuthValidateURL,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		onEvent:          onEvent,
		onModeration:     onModeration,
		onStatusChange:   onStatusChange,
	}
}

// SetCheckFollowerFn overrides the follower status check function (used in tests).
func (c *Connector) SetCheckFollowerFn(fn func(ctx context.Context, channel string, userID string, userLogin string) (bool, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkFollowerFn = fn
}

// SetFollowerCacheTTL overrides the per-user follower check cache TTL (default 30 minutes).
func (c *Connector) SetFollowerCacheTTL(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.followerCacheTTL = d
	}
}

// CanCheckFollowers reports whether the Twitch connector can perform on-demand follower checks (OAuth token or test hook configured).
func (c *Connector) CanCheckFollowers() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel == "" {
		return false
	}
	if c.checkFollowerFn != nil {
		return true
	}
	return c.oauth != nil && c.oauth.HasToken()
}

// MarkUserFollowing records in the in-memory TTL cache that the user is currently following the channel.
func (c *Connector) MarkUserFollowing(userID string, userLogin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.followerCache == nil {
		c.followerCache = make(map[string]followerCacheEntry)
	}
	entry := followerCacheEntry{
		following: true,
		checkedAt: time.Now(),
	}
	if id := strings.TrimSpace(userID); id != "" {
		c.followerCache["id:"+id] = entry
	}
	if login := strings.ToLower(strings.TrimSpace(userLogin)); login != "" {
		c.followerCache["login:"+login] = entry
	}
}

// SetOnStreamStateChange registers a callback invoked whenever the channel's live broadcast state changes.
func (c *Connector) SetOnStreamStateChange(fn func(live bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStreamLive = fn
}

// SetCheckLiveFn overrides the live stream check function (used in tests).
func (c *Connector) SetCheckLiveFn(fn func(ctx context.Context, channel string) (bool, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkLiveFn = fn
}

// SetLivePollInterval overrides the periodic live stream check interval.
func (c *Connector) SetLivePollInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.livePollInterval = d
	}
}

// IsLive reports whether the connected Twitch channel is currently broadcasting live.
func (c *Connector) IsLive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isLive
}

// CheckLiveChannel checks whether the specified channel (or configured channel if empty) is live.
func (c *Connector) CheckLiveChannel(ctx context.Context, channel string) (bool, error) {
	if channel == "" {
		c.mu.Lock()
		channel = c.channel
		c.mu.Unlock()
	}
	if channel == "" {
		return false, nil
	}
	if c.IsLive() && (c.Channel() == "" || strings.EqualFold(c.Channel(), channel)) {
		return true, nil
	}
	live, err := c.checkChannelLive(ctx, channel)
	if err != nil {
		return false, err
	}
	if live && (c.Channel() == "" || strings.EqualFold(c.Channel(), channel)) {
		c.setStreamLive(true)
	}
	return live, nil
}

// CheckLive performs an immediate live status check for the configured channel and updates stream state.
func (c *Connector) CheckLive(ctx context.Context) (bool, error) {
	return c.CheckLiveChannel(ctx, "")
}

// SetOAuthManager attaches the Twitch OAuth manager for EventSub WebSocket subscriptions.
func (c *Connector) SetOAuthManager(oauth *OAuthManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.oauth = oauth
}

// SetEventSubEndpoints overrides the EventSub WebSocket, Helix API, and OAuth validate URLs (used in tests).
func (c *Connector) SetEventSubEndpoints(wsURL, helixBaseURL, validateURL string, httpClient *http.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wsURL != "" {
		c.eventSubWSURL = wsURL
	}
	if helixBaseURL != "" {
		c.helixBaseURL = helixBaseURL
	}
	if validateURL != "" {
		c.oauthValidateURL = validateURL
	}
	if httpClient != nil {
		c.httpClient = httpClient
	}
}

// RefreshOAuth switches the active Twitch connection between full Helix/EventSub mode (when OAuth is connected)
// and zero-config anonymous IRC + GQL mode (when OAuth is disconnected).
func (c *Connector) RefreshOAuth() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.channel == "" {
		c.stopEventSubLocked()
		return
	}
	c.connectLocked()
}

// Start connects to the given Twitch channel (e.g. "mrpoundsign").
func (c *Connector) Start(channel string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanChannel := cleanChannelName(channel)
	if cleanChannel == "" && c.oauth != nil && c.oauth.HasToken() {
		_, login, _ := c.oauth.StoredUser()
		cleanChannel = cleanChannelName(login)
	}
	if cleanChannel == "" {
		return errors.New("empty channel name")
	}

	hasOAuth := c.oauth != nil && c.oauth.HasToken()
	if c.running && c.channel == cleanChannel {
		if (hasOAuth && c.eventSubCancel != nil && c.client == nil) || (!hasOAuth && c.client != nil && c.eventSubCancel == nil) {
			return nil
		}
	}

	c.channel = cleanChannel
	c.running = true
	c.connectLocked()
	return nil
}

// Stop disconnects from Twitch IRC and terminates the connector.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = false
	c.disconnectLocked()
	c.updateStatusLocked(StatusDisconnected, "Stopped")
	return nil
}

// SetChannel dynamically switches the target channel.
func (c *Connector) SetChannel(channel string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanChannel := cleanChannelName(channel)
	if cleanChannel == c.channel {
		return nil
	}

	c.channel = cleanChannel
	if !c.running {
		return nil
	}

	if cleanChannel == "" {
		c.disconnectLocked()
		c.updateStatusLocked(StatusDisconnected, "No channel specified")
		return nil
	}

	c.connectLocked()
	return nil
}

// Status returns the current connection status.
func (c *Connector) Status() ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Channel returns the current channel name.
func (c *Connector) Channel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channel
}

func (c *Connector) connectLocked() {
	c.disconnectLocked()

	ch := c.channel
	if ch == "" {
		return
	}

	c.updateStatusLocked(StatusConnecting, "Connecting to #"+ch+"...")

	// When OAuth is connected, use 100% official Twitch Helix API + EventSub WebSocket (no anonymous IRC client).
	if c.oauth != nil && c.oauth.HasToken() {
		c.startEventSubLocked()
		c.startLivePollLocked(ch)
		return
	}

	client := twitch.NewAnonymousClient()

	client.OnConnect(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.running && c.channel == ch {
			if c.isLive {
				c.updateStatusLocked(StatusConnected, "#"+ch)
			} else {
				c.updateStatusLocked(StatusWaitingForStream, "#"+ch)
			}
			log.Printf("[Twitch] Connected to #%s (isLive=%v)", ch, c.isLive)
		}
	})

	client.OnPrivateMessage(func(message twitch.PrivateMessage) {
		c.handlePrivateMessage(message)
	})

	client.OnClearMessage(func(message twitch.ClearMessage) {
		c.handleClearMessage(message)
	})

	client.OnClearChatMessage(func(message twitch.ClearChatMessage) {
		c.handleClearChat(message)
	})

	client.OnUserNoticeMessage(func(message twitch.UserNoticeMessage) {
		c.handleUserNotice(message)
	})

	client.Join(ch)
	c.client = client

	go func(cl *twitch.Client, targetChannel string) {
		err := cl.Connect()
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.client == cl {
			c.client = nil
			if c.running {
				c.setStreamLiveLocked(false)
				c.updateStatusLocked(StatusDisconnected, "Disconnected from #"+targetChannel)
			}
		}

		if err != nil && !errors.Is(err, twitch.ErrClientDisconnected) {
			log.Printf("[Twitch] Connection error for #%s: %v", targetChannel, err)
		}
	}(client, ch)

	c.startLivePollLocked(ch)
}

func (c *Connector) disconnectLocked() {
	c.stopLivePollLocked()
	c.stopEventSubLocked()
	c.setStreamLiveLocked(false)
	c.broadcasterID = ""
	c.followerCache = make(map[string]followerCacheEntry)
	if c.client != nil {
		_ = c.client.Disconnect()
		c.client = nil
	}
}

func (c *Connector) setStreamLive(live bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && live {
		return
	}
	c.setStreamLiveLocked(live)
}

func (c *Connector) setStreamLiveLocked(live bool) {
	if c.isLive == live {
		return
	}
	c.isLive = live
	if c.running && (c.client != nil || c.eventSubConn != nil || c.eventSubCancel != nil) && c.status != StatusDisconnected {
		if live {
			c.updateStatusLocked(StatusConnected, "#"+c.channel)
		} else {
			c.updateStatusLocked(StatusWaitingForStream, "#"+c.channel)
		}
	}
	if fn := c.onStreamLive; fn != nil {
		go fn(live)
	}
}

func (c *Connector) updateStatusLocked(status ConnectionStatus, detail string) {
	c.status = status
	if fn := c.onStatusChange; fn != nil {
		go fn(status, detail)
	}
}

func (c *Connector) handlePrivateMessage(msg twitch.PrivateMessage) {
	if c.onEvent == nil {
		return
	}

	roles := extractRoles(msg.User.Badges)
	if c.channel != "" && cleanChannelName(msg.User.Name) == c.channel {
		if !slices.Contains(roles, "broadcaster") {
			roles = append(roles, "broadcaster")
		}
	}
	log.Printf("[Twitch] PRIVMSG received: author=%q roles=%v msg=%q", msg.User.Name, roles, msg.Message)
	emotes := mapEmotes(msg.Emotes)

	kind := chat.EventKindMessage
	var amount float64
	var unitAmount float64
	var currency string
	var count int
	var rewardTitle string
	rewardID := strings.TrimSpace(msg.CustomRewardID)
	var msgIDTag string
	if msg.Tags != nil {
		if rewardID == "" {
			rewardID = strings.TrimSpace(msg.Tags["custom-reward-id"])
		}
		msgIDTag = strings.TrimSpace(msg.Tags["msg-id"])
	}

	if msg.Bits > 0 {
		kind = chat.EventKindCheer
		amount = float64(msg.Bits)
		unitAmount = 1
		count = msg.Bits
		currency = "bits"
	} else {
		switch msgIDTag {
		case "highlighted-message":
			kind = chat.EventKindRedemption
			rewardTitle = "Highlight My Message"
		case "gigantified-emote-message":
			kind = chat.EventKindRedemption
			rewardTitle = "Gigantified Emote"
		case "animated-message":
			kind = chat.EventKindRedemption
			rewardTitle = "Message Effects"
		default:
			if rewardID != "" && (c.oauth == nil || !c.oauth.HasToken()) {
				// Anonymous IRC fallback for Channel Point redemptions with text input when EventSub OAuth is not active
				kind = chat.EventKindRedemption
			}
		}
	}

	displayName := msg.User.DisplayName
	if displayName == "" {
		displayName = msg.User.Name
	}

	ch := cleanChannelName(msg.Channel)
	if ch == "" {
		ch = c.Channel()
	}

	ev := &chat.Event{
		ID:          msg.ID,
		Platform:    "twitch",
		Channel:     ch,
		Kind:        kind,
		Timestamp:   msg.Time,
		UserID:      msg.User.ID,
		UserName:    msg.User.Name,
		DisplayName: displayName,
		Roles:       roles,
		Text:        msg.Message,
		EmoteSpans:  emotes,
		Amount:      amount,
		UnitAmount:  unitAmount,
		Currency:    currency,
		Count:       count,
		RewardID:    rewardID,
		RewardTitle: rewardTitle,
		Raw:         msg,
	}

	c.emitEvent(ev)
}

func (c *Connector) handleClearMessage(msg twitch.ClearMessage) {
	if c.onModeration == nil || msg.TargetMsgID == "" {
		return
	}

	ch := cleanChannelName(msg.Channel)
	if ch == "" {
		ch = c.Channel()
	}

	c.onModeration(&chat.ModerationEvent{
		Platform:        "twitch",
		Channel:         ch,
		Action:          chat.ModActionDeleteMessage,
		TargetMessageID: msg.TargetMsgID,
		Timestamp:       time.Now(),
	})
}

func (c *Connector) handleClearChat(msg twitch.ClearChatMessage) {
	if c.onModeration == nil {
		return
	}

	ch := cleanChannelName(msg.Channel)
	if ch == "" {
		ch = c.Channel()
	}

	// If msg.TargetUsername is empty, entire chat was cleared
	if msg.TargetUsername == "" {
		c.onModeration(&chat.ModerationEvent{
			Platform:  "twitch",
			Channel:   ch,
			Action:    chat.ModActionClearChat,
			Timestamp: time.Now(),
		})
		return
	}

	// Chatter timed out or banned
	target := msg.TargetUsername
	if target == "" {
		target = msg.TargetUserID
	}
	c.onModeration(&chat.ModerationEvent{
		Platform:     "twitch",
		Channel:      ch,
		Action:       chat.ModActionBanUser,
		TargetUserID: target,
		Timestamp:    time.Now(),
	})
}

func (c *Connector) handleUserNotice(msg twitch.UserNoticeMessage) {
	if c.onEvent == nil {
		return
	}

	displayName := msg.User.DisplayName
	if displayName == "" {
		displayName = msg.User.Name
	}

	roles := extractRoles(msg.User.Badges)
	emotes := mapEmotes(msg.Emotes)

	var kind chat.EventKind
	var text string
	var months int
	var count int
	var amount float64
	var currency string
	var itemName string
	var rewardTitle string
	var recipient string
	subTier := formatTwitchSubPlan(msg.MsgParams["msg-param-sub-plan"])

	switch msg.MsgID {
	case "sub":
		kind = chat.EventKindSub
		text = msg.Message
		months = 1
		count = 1
	case "resub":
		kind = chat.EventKindResub
		text = msg.Message
		if m, err := strconv.Atoi(msg.MsgParams["msg-param-cumulative-months"]); err == nil {
			months = m
		}
		if months <= 0 {
			months = 1
		}
		count = 1
	case "subgift", "anonsubgift":
		kind = chat.EventKindGiftSub
		recipient = msg.MsgParams["msg-param-recipient-display-name"]
		if recipient == "" {
			recipient = msg.MsgParams["msg-param-recipient-user-name"]
		}
		text = "gifted a subscription to " + recipient
		count = 1
	case "submysterygift":
		kind = chat.EventKindGiftSub
		if cStr, ok := msg.MsgParams["msg-param-mass-gift-count"]; ok {
			count, _ = strconv.Atoi(cStr)
		}
		if count <= 0 {
			count = 1
		}
		text = "gifted " + strconv.Itoa(count) + " community subscriptions!"
	case "primepaidupgrade":
		kind = chat.EventKindSubUpgrade
		text = msg.Message
		count = 1
		if subTier != "" {
			itemName = "Upgraded from a Prime subscription to " + subTier
		} else {
			itemName = "Upgraded from a Prime subscription"
		}
	case "giftpaidupgrade", "anongiftpaidupgrade":
		kind = chat.EventKindSubUpgrade
		text = msg.Message
		count = 1
		sender := strings.TrimSpace(msg.MsgParams["msg-param-sender-name"])
		if sender == "" {
			sender = strings.TrimSpace(msg.MsgParams["msg-param-sender-login"])
		}
		if sender != "" && msg.MsgID != "anongiftpaidupgrade" {
			recipient = sender
			itemName = "Upgraded a gift subscription from " + sender
		} else {
			itemName = "Upgraded a gift subscription"
		}
	case "standardpayforward", "communitypayforward":
		kind = chat.EventKindSubUpgrade
		text = msg.Message
		count = 1
		recipient = strings.TrimSpace(msg.MsgParams["msg-param-recipient-display-name"])
		if recipient == "" {
			recipient = strings.TrimSpace(msg.MsgParams["msg-param-recipient-user-name"])
		}
		if recipient != "" {
			itemName = "Paid forward a gift subscription to " + recipient
		} else {
			itemName = "Paid forward a gift subscription to the community"
		}
	case "extendsub":
		kind = chat.EventKindSubUpgrade
		text = msg.Message
		count = 1
		if m, err := strconv.Atoi(msg.MsgParams["msg-param-cumulative-months"]); err == nil && m > 0 {
			months = m
		}
		itemName = "Extended subscription"
	case "viewermilestone":
		category := strings.ToLower(strings.TrimSpace(msg.MsgParams["msg-param-category"]))
		if category != "" && category != "watch-streak" && category != "watch_streak" {
			return
		}
		kind = chat.EventKindWatchStreak
		text = msg.Message
		if vStr, ok := msg.MsgParams["msg-param-value"]; ok {
			count, _ = strconv.Atoi(strings.TrimSpace(vStr))
		}
		if count <= 0 {
			count = 1
		}
		if rStr, ok := msg.MsgParams["msg-param-copoReward"]; ok {
			if pts, err := strconv.Atoi(strings.TrimSpace(rStr)); err == nil && pts > 0 {
				amount = float64(pts)
				currency = "points"
			}
		}
	case "bitsbadgetier":
		kind = chat.EventKindBitsBadgeTier
		text = msg.Message
		if tStr, ok := msg.MsgParams["msg-param-threshold"]; ok {
			count, _ = strconv.Atoi(strings.TrimSpace(tStr))
			amount = float64(count)
		}
		currency = "bits"
	case "announcement":
		kind = chat.EventKindAnnouncement
		text = msg.Message
	case "charitydonation", "charity_donation":
		kind = chat.EventKindCharityDonation
		text = msg.Message
		amount, currency = parseTwitchCharityAmount(
			msg.MsgParams["msg-param-donation-amount"],
			msg.MsgParams["msg-param-donation-currency"],
			msg.MsgParams["msg-param-exponent"],
		)
		rewardTitle = strings.TrimSpace(msg.MsgParams["msg-param-charity-name"])
		itemName = rewardTitle
	case "raid":
		kind = chat.EventKindRaid
		if cStr, ok := msg.MsgParams["msg-param-viewerCount"]; ok {
			count, _ = strconv.Atoi(cStr)
		}
		text = "raided with " + strconv.Itoa(count) + " viewers!"
	default:
		return
	}

	ch := cleanChannelName(msg.Channel)
	if ch == "" {
		ch = c.Channel()
	}

	ev := &chat.Event{
		ID:          msg.ID,
		Platform:    "twitch",
		Channel:     ch,
		Kind:        kind,
		Timestamp:   msg.Time,
		UserID:      msg.User.ID,
		UserName:    msg.User.Name,
		DisplayName: displayName,
		Roles:       roles,
		Text:        text,
		EmoteSpans:  emotes,
		Amount:      amount,
		Currency:    currency,
		ItemName:    itemName,
		RewardTitle: rewardTitle,
		SubTier:     subTier,
		Recipient:   recipient,
		Months:      months,
		Count:       count,
		Raw:         msg,
	}

	c.emitEvent(ev)
}

func parseTwitchCharityAmount(rawVal, rawCurrency, rawExponent string) (float64, string) {
	val, _ := strconv.ParseFloat(strings.TrimSpace(rawVal), 64)
	exp, err := strconv.Atoi(strings.TrimSpace(rawExponent))
	if err != nil {
		// Default exponent of 2 (cents) if rawVal looks like integer minor units and exponent is specified
		exp = 0
	}
	for range exp {
		val /= 10
	}
	curr := strings.ToUpper(strings.TrimSpace(rawCurrency))
	if curr == "" {
		curr = "USD"
	}
	return val, curr
}

func formatTwitchSubPlan(plan string) string {
	switch strings.TrimSpace(plan) {
	case "Prime":
		return "Prime"
	case "1000":
		return "Tier 1"
	case "2000":
		return "Tier 2"
	case "3000":
		return "Tier 3"
	default:
		return ""
	}
}

func extractRoles(badges map[string]int) []string {
	var roles []string
	if _, ok := badges["broadcaster"]; ok {
		roles = append(roles, "broadcaster")
	}
	if _, ok := badges["moderator"]; ok {
		roles = append(roles, "mod")
	}
	if _, ok := badges["vip"]; ok {
		roles = append(roles, "vip")
	}
	if _, ok := badges["subscriber"]; ok {
		roles = append(roles, "subscriber")
	}
	return roles
}

func mapEmotes(twitchEmotes []*twitch.Emote) []chat.EmoteSpan {
	var spans []chat.EmoteSpan
	for _, em := range twitchEmotes {
		if em == nil {
			continue
		}
		var imgURL string
		if em.ID != "" {
			imgURL = "https://static-cdn.jtvnw.net/emoticons/v2/" + em.ID + "/static/dark/1.0"
		}
		for _, pos := range em.Positions {
			spans = append(spans, chat.EmoteSpan{
				Start: pos.Start,
				End:   pos.End + 1, // Twitch is inclusive; EmoteSpan is exclusive
				ID:    em.ID,
				Name:  em.Name,
				URL:   imgURL,
			})
		}
	}
	return spans
}

func cleanChannelName(channel string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(channel), "#"))
}
