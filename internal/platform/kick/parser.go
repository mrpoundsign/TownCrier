package kick

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

var kickEmoteRegex = regexp.MustCompile(`\[emote:([0-9]+):([^\]]+)\]`)

// PusherEnvelope represents an inbound or outbound Pusher WebSocket protocol message.
type PusherEnvelope struct {
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
	Channel string          `json:"channel,omitempty"`
}

// ParseKickEmotes replaces Kick's inline [emote:<id>:<name>] tags with <name> and returns
// rune-accurate EmoteSpans so the Sanitizer can strip or speak emotes cleanly (D34, D35).
func ParseKickEmotes(raw string) (string, []chat.EmoteSpan) {
	matches := kickEmoteRegex.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}

	var b strings.Builder
	var spans []chat.EmoteSpan
	lastByteIdx := 0
	currentRuneCount := 0

	for _, m := range matches {
		fullStart, fullEnd := m[0], m[1]
		idStart, idEnd := m[2], m[3]
		nameStart, nameEnd := m[4], m[5]

		prefix := raw[lastByteIdx:fullStart]
		b.WriteString(prefix)
		currentRuneCount += utf8.RuneCountInString(prefix)

		emoteID := raw[idStart:idEnd]
		emoteName := raw[nameStart:nameEnd]
		nameRunes := utf8.RuneCountInString(emoteName)

		var imgURL string
		if emoteID != "" {
			imgURL = "https://files.kick.com/emotes/" + emoteID + "/fullsize"
		}

		spans = append(spans, chat.EmoteSpan{
			Start: currentRuneCount,
			End:   currentRuneCount + nameRunes,
			ID:    emoteID,
			Name:  emoteName,
			URL:   imgURL,
		})

		b.WriteString(emoteName)
		currentRuneCount += nameRunes
		lastByteIdx = fullEnd
	}

	b.WriteString(raw[lastByteIdx:])
	return b.String(), spans
}

// ParsePusherFrame permissively parses a raw Pusher WebSocket JSON frame (D40).
// It returns a normalized *chat.Event or *chat.ModerationEvent if applicable, plus the raw event name.
func ParsePusherFrame(raw []byte) (*chat.Event, *chat.ModerationEvent, string, error) {
	var env PusherEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, "", fmt.Errorf("unmarshal pusher envelope: %w", err)
	}

	if strings.HasPrefix(env.Event, "pusher:") || strings.HasPrefix(env.Event, "pusher_internal:") {
		return nil, nil, env.Event, nil
	}

	payloadMap, err := decodePermissiveData(env.Data)
	if err != nil {
		return nil, nil, env.Event, err
	}

	shortEvent := env.Event
	if idx := strings.LastIndex(shortEvent, `\`); idx >= 0 {
		shortEvent = shortEvent[idx+1:]
	}

	switch shortEvent {
	case "ChatMessageSentEvent", "ChatMessageEvent":
		ev := parseChatMessage(payloadMap)
		return ev, nil, env.Event, nil

	case "SubscriptionEvent", "SubscriptionCreated":
		ev := parseSubscription(payloadMap)
		return ev, nil, env.Event, nil

	case "GiftedSubscriptionsEvent", "GiftedSubscriptions", "LuckyUsersWhoGotGiftSubscriptionsEvent":
		ev := parseGiftedSubscriptions(payloadMap)
		return ev, nil, env.Event, nil

	case "StreamHostEvent", "StreamHosted":
		ev := parseStreamHost(payloadMap)
		return ev, nil, env.Event, nil

	case "FollowersUpdated", "ChannelFollowEvent", "FollowEvent":
		ev := parseFollow(payloadMap)
		return ev, nil, env.Event, nil

	case "KicksGifted", "GiftEvent", "KicksGiftedEvent":
		ev := parseKickGift(payloadMap)
		return ev, nil, env.Event, nil

	case "MessageDeletedEvent":
		mod := parseMessageDeleted(payloadMap)
		return nil, mod, env.Event, nil

	case "UserBannedEvent":
		mod := parseUserBanned(payloadMap)
		return nil, mod, env.Event, nil

	case "ChatroomClearEvent":
		return nil, &chat.ModerationEvent{
			Platform:  "kick",
			Action:    chat.ModActionClearChat,
			Timestamp: time.Now(),
		}, env.Event, nil
	}

	return nil, nil, env.Event, nil
}

func decodePermissiveData(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}

	// Pusher usually encodes "data" as a JSON-escaped string, but handle direct JSON objects too.
	var dataStr string
	if err := json.Unmarshal(raw, &dataStr); err == nil {
		var m map[string]any
		if err := json.Unmarshal([]byte(dataStr), &m); err != nil {
			return nil, fmt.Errorf("unmarshal inner pusher data string: %w", err)
		}
		return m, nil
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal pusher data object: %w", err)
	}
	return m, nil
}

func parseChatMessage(m map[string]any) *chat.Event {
	// Some schema revisions wrap message inside "message" object
	msgObj := asMap(m["message"])
	if len(msgObj) == 0 {
		msgObj = m
	}

	content := strings.TrimSpace(asString(msgObj["content"]))
	if content == "" {
		return nil
	}

	msgID := asString(msgObj["id"])
	if msgID == "" {
		msgID = fmt.Sprintf("kick-msg-%d", time.Now().UnixNano())
	}

	sender := asMap(m["sender"])
	if len(sender) == 0 {
		sender = asMap(msgObj["sender"])
	}
	if len(sender) == 0 {
		sender = asMap(m["user"])
	}

	userID := asString(sender["id"])
	username := strings.TrimLeft(strings.TrimSpace(asString(sender["username"])), "@")
	slug := strings.TrimLeft(strings.TrimSpace(asString(sender["slug"])), "@")
	if username == "" {
		username = slug
	}
	if userID == "" {
		userID = strings.ToLower(username)
	}

	roles := extractKickRoles(sender)
	cleanText, spans := ParseKickEmotes(content)

	ts := time.Now()
	if createdStr := asString(msgObj["created_at"]); createdStr != "" {
		if parsedTS, err := time.Parse(time.RFC3339, createdStr); err == nil {
			ts = parsedTS
		}
	}

	return &chat.Event{
		ID:          msgID,
		Platform:    "kick",
		Kind:        chat.EventKindMessage,
		Timestamp:   ts,
		UserID:      userID,
		UserName:    strings.ToLower(username),
		DisplayName: username,
		Roles:       roles,
		Text:        cleanText,
		EmoteSpans:  spans,
	}
}

func extractKickRoles(sender map[string]any) []string {
	var roles []string
	seen := make(map[string]struct{})
	add := func(r string) {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			roles = append(roles, r)
		}
	}

	identity := asMap(sender["identity"])
	badgesRaw := identity["badges"]
	if badgesRaw == nil {
		badgesRaw = sender["badges"]
	}

	if badgeSlice, ok := badgesRaw.([]any); ok {
		for _, item := range badgeSlice {
			bm := asMap(item)
			bType := strings.ToLower(asString(bm["type"]))
			if bType == "" {
				bType = strings.ToLower(asString(bm["text"]))
			}
			switch bType {
			case "broadcaster", "owner", "host":
				add("broadcaster")
			case "moderator", "mod":
				add("mod")
			case "vip":
				add("vip")
			case "og", "founder":
				add("og")
			case "subscriber", "sub_gifter":
				add("subscriber")
			}
		}
	}

	return roles
}

func parseSubscription(m map[string]any) *chat.Event {
	username := strings.TrimLeft(strings.TrimSpace(asString(m["username"])), "@")
	if username == "" {
		subMap := asMap(m["subscriber"])
		username = strings.TrimLeft(strings.TrimSpace(asString(subMap["username"])), "@")
	}
	if username == "" {
		userMap := asMap(m["user"])
		username = strings.TrimLeft(strings.TrimSpace(asString(userMap["username"])), "@")
	}
	if username == "" {
		username = "Someone"
	}

	months := asInt(m["months"])
	if months <= 0 {
		months = 1
	}

	kind := chat.EventKindSub
	if months > 1 {
		kind = chat.EventKindResub
	}

	msgText := strings.TrimSpace(asString(m["message"]))
	cleanText, spans := ParseKickEmotes(msgText)

	return &chat.Event{
		ID:          fmt.Sprintf("kick-sub-%s-%d", strings.ToLower(username), time.Now().UnixNano()),
		Platform:    "kick",
		Kind:        kind,
		Timestamp:   time.Now(),
		UserID:      strings.ToLower(username),
		UserName:    strings.ToLower(username),
		DisplayName: username,
		Months:      months,
		Text:        cleanText,
		EmoteSpans:  spans,
	}
}

func parseGiftedSubscriptions(m map[string]any) *chat.Event {
	gifter := strings.TrimLeft(strings.TrimSpace(asString(m["gifter_username"])), "@")
	if gifter == "" {
		gifter = strings.TrimLeft(strings.TrimSpace(asString(m["username"])), "@")
	}
	if gifter == "" {
		gifter = "An anonymous gifter"
	}

	count := asInt(m["count"])
	if count <= 0 {
		if giftedList, ok := m["gifted_usernames"].([]any); ok {
			count = len(giftedList)
		}
	}
	if count <= 0 {
		count = 1
	}

	return &chat.Event{
		ID:          fmt.Sprintf("kick-giftsub-%s-%d", strings.ToLower(gifter), time.Now().UnixNano()),
		Platform:    "kick",
		Kind:        chat.EventKindGiftSub,
		Timestamp:   time.Now(),
		UserID:      strings.ToLower(gifter),
		UserName:    strings.ToLower(gifter),
		DisplayName: gifter,
		Count:       count,
	}
}

func parseStreamHost(m map[string]any) *chat.Event {
	host := strings.TrimLeft(strings.TrimSpace(asString(m["host_username"])), "@")
	if host == "" {
		host = strings.TrimLeft(strings.TrimSpace(asString(m["username"])), "@")
	}
	if host == "" {
		return nil
	}
	viewers := asInt(m["number_viewers"])
	if viewers <= 0 {
		viewers = asInt(m["viewers_count"])
	}
	if viewers <= 0 {
		viewers = 1
	}

	return &chat.Event{
		ID:          fmt.Sprintf("kick-host-%s-%d", strings.ToLower(host), time.Now().UnixNano()),
		Platform:    "kick",
		Kind:        chat.EventKindRaid,
		Timestamp:   time.Now(),
		UserID:      strings.ToLower(host),
		UserName:    strings.ToLower(host),
		DisplayName: host,
		Count:       viewers,
	}
}

func parseFollow(m map[string]any) *chat.Event {
	username := strings.TrimLeft(strings.TrimSpace(asString(m["username"])), "@")
	if username == "" {
		fMap := asMap(m["follower"])
		username = strings.TrimLeft(strings.TrimSpace(asString(fMap["username"])), "@")
	}
	if username == "" {
		uMap := asMap(m["user"])
		username = strings.TrimLeft(strings.TrimSpace(asString(uMap["username"])), "@")
	}
	if username == "" {
		return nil
	}

	return &chat.Event{
		ID:          fmt.Sprintf("kick-follow-%s-%d", strings.ToLower(username), time.Now().UnixNano()),
		Platform:    "kick",
		Kind:        chat.EventKindFollow,
		Timestamp:   time.Now(),
		UserID:      strings.ToLower(username),
		UserName:    strings.ToLower(username),
		DisplayName: username,
	}
}

func parseKickGift(m map[string]any) *chat.Event {
	sender := asMap(m["sender"])
	if len(sender) == 0 {
		sender = asMap(m["user"])
	}
	username := strings.TrimLeft(strings.TrimSpace(asString(sender["username"])), "@")
	if username == "" {
		username = strings.TrimLeft(strings.TrimSpace(asString(m["username"])), "@")
	}
	if username == "" {
		username = strings.TrimLeft(strings.TrimSpace(asString(m["sender_username"])), "@")
	}
	if username == "" {
		username = "Someone"
	}

	giftMap := asMap(m["gift"])
	giftName := strings.TrimSpace(asString(giftMap["name"]))
	if giftName == "" {
		giftName = strings.TrimSpace(asString(m["gift_name"]))
	}
	if giftName == "" {
		giftName = "Kicks"
	}

	amount := asInt(giftMap["amount"])
	if amount <= 0 {
		amount = asInt(m["amount"])
	}
	if amount <= 0 {
		amount = asInt(m["count"])
	}
	if amount <= 0 {
		amount = 1
	}

	msgText := strings.TrimSpace(asString(m["message"]))
	cleanText, spans := ParseKickEmotes(msgText)

	return &chat.Event{
		ID:          fmt.Sprintf("kick-gift-%s-%d", strings.ToLower(username), time.Now().UnixNano()),
		Platform:    "kick",
		Kind:        chat.EventKindGift,
		Timestamp:   time.Now(),
		UserID:      strings.ToLower(username),
		UserName:    strings.ToLower(username),
		DisplayName: username,
		Count:       amount,
		Amount:      float64(amount),
		RewardTitle: giftName,
		Text:        cleanText,
		EmoteSpans:  spans,
	}
}

func parseMessageDeleted(m map[string]any) *chat.ModerationEvent {
	msgMap := asMap(m["message"])
	targetID := asString(msgMap["id"])
	if targetID == "" {
		targetID = asString(m["message_id"])
	}
	if targetID == "" {
		targetID = asString(m["id"])
	}
	if targetID == "" {
		return nil
	}

	return &chat.ModerationEvent{
		Platform:        "kick",
		Action:          chat.ModActionDeleteMessage,
		TargetMessageID: targetID,
		Timestamp:       time.Now(),
	}
}

func parseUserBanned(m map[string]any) *chat.ModerationEvent {
	userMap := asMap(m["user"])
	targetUser := asString(userMap["id"])
	if targetUser == "" {
		targetUser = strings.ToLower(asString(userMap["username"]))
	}
	if targetUser == "" {
		targetUser = asString(m["user_id"])
	}
	if targetUser == "" {
		return nil
	}

	return &chat.ModerationEvent{
		Platform:     "kick",
		Action:       chat.ModActionBanUser,
		TargetUserID: targetUser,
		Timestamp:    time.Now(),
	}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return ""
	}
}

func asInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	case json.Number:
		n, _ := val.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(val))
		return n
	default:
		return 0
	}
}
