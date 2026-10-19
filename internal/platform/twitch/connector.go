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
	StatusDisconnected ConnectionStatus = "disconnected"
	StatusConnecting   ConnectionStatus = "connecting"
	StatusConnected    ConnectionStatus = "connected"
)

// Connector manages the Twitch IRC and EventSub WebSocket connections for stream chat reading, alerts, and moderation.
type Connector struct {
	mu               sync.Mutex
	client           *twitch.Client
	channel          string
	status           ConnectionStatus
	running          bool
	oauth            *OAuthManager
	eventSubCancel   context.CancelFunc
	eventSubConn     *websocket.Conn
	eventSubWSURL    string
	helixBaseURL     string
	oauthValidateURL string
	httpClient       *http.Client
	onEvent          func(event *chat.Event)
	onModeration     func(modEvent *chat.ModerationEvent)
	onStatusChange   func(status ConnectionStatus, detail string)
}

// NewConnector creates a new Twitch connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onModeration func(modEvent *chat.ModerationEvent),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	return &Connector{
		status:           StatusDisconnected,
		eventSubWSURL:    DefaultEventSubWSURL,
		helixBaseURL:     DefaultHelixBaseURL,
		oauthValidateURL: DefaultOAuthValidateURL,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		onEvent:          onEvent,
		onModeration:     onModeration,
		onStatusChange:   onStatusChange,
	}
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

// RefreshOAuth restarts or stops the EventSub WebSocket loop based on the current OAuth token state.
func (c *Connector) RefreshOAuth() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.channel == "" {
		c.stopEventSubLocked()
		return
	}
	if c.oauth != nil && c.oauth.HasToken() {
		c.startEventSubLocked()
	} else {
		c.stopEventSubLocked()
	}
}

// Start connects to the given Twitch channel (e.g. "mrpoundsign").
func (c *Connector) Start(channel string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanChannel := cleanChannelName(channel)
	if cleanChannel == "" {
		return errors.New("empty channel name")
	}

	if c.running && c.client != nil && c.channel == cleanChannel {
		return nil
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

	client := twitch.NewAnonymousClient()

	client.OnConnect(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.running && c.channel == ch {
			c.updateStatusLocked(StatusConnected, "Connected to #"+ch)
			log.Printf("[Twitch] Connected to #%s", ch)
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
				c.updateStatusLocked(StatusDisconnected, "Disconnected from #"+targetChannel)
			}
		}

		if err != nil && !errors.Is(err, twitch.ErrClientDisconnected) {
			log.Printf("[Twitch] Connection error for #%s: %v", targetChannel, err)
		}
	}(client, ch)

	c.startEventSubLocked()
}

func (c *Connector) disconnectLocked() {
	c.stopEventSubLocked()
	if c.client != nil {
		_ = c.client.Disconnect()
		c.client = nil
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
	if msg.Bits > 0 {
		kind = chat.EventKindCheer
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
		Amount:      float64(msg.Bits),
		Currency:    "bits",
		Raw:         msg,
	}

	c.onEvent(ev)
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

	switch msg.MsgID {
	case "sub":
		kind = chat.EventKindSub
		text = msg.Message
	case "resub":
		kind = chat.EventKindResub
		text = msg.Message
		if m, err := strconv.Atoi(msg.MsgParams["msg-param-cumulative-months"]); err == nil {
			months = m
		}
	case "subgift", "anonsubgift":
		kind = chat.EventKindGiftSub
		recipient := msg.MsgParams["msg-param-recipient-display-name"]
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
		Months:      months,
		Count:       count,
		Raw:         msg,
	}

	c.onEvent(ev)
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
