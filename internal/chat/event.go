package chat

import (
	"time"
)

// EventKind specifies the normalized classification of an inbound stream event.
type EventKind string

const (
	EventKindMessage      EventKind = "message"
	EventKindFollow       EventKind = "follow"
	EventKindSub          EventKind = "sub"
	EventKindResub        EventKind = "resub"
	EventKindGiftSub      EventKind = "gift_sub"
	EventKindCheer        EventKind = "cheer"
	EventKindRaid         EventKind = "raid"
	EventKindRedemption   EventKind = "redemption"
	EventKindSuperChat    EventKind = "super_chat"
	EventKindSuperSticker EventKind = "super_sticker"
	EventKindGift         EventKind = "gift"
)

// EmoteSpan defines the location and metadata of an emote in a message text.
// Start and End represent rune indices (Start inclusive, End exclusive).
type EmoteSpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
}

// ImageURL returns the direct image URL for this emote on the given platform,
// falling back to deterministic platform CDN URLs when URL is not explicitly set.
func (s EmoteSpan) ImageURL(platform string) string {
	if s.URL != "" {
		return s.URL
	}
	if s.ID == "" {
		return ""
	}
	switch platform {
	case "twitch":
		return "https://static-cdn.jtvnw.net/emoticons/v2/" + s.ID + "/static/dark/1.0"
	case "kick":
		return "https://files.kick.com/emotes/" + s.ID + "/fullsize"
	default:
		return ""
	}
}

// Event represents a unified stream chat or notification event across all platforms.
type Event struct {
	ID            string      `json:"id"`
	Platform      string      `json:"platform"`
	Channel       string      `json:"channel,omitempty"`
	Kind          EventKind   `json:"kind"`
	Timestamp     time.Time   `json:"timestamp"`
	UserID        string      `json:"user_id"`
	UserName      string      `json:"user_name"`
	DisplayName   string      `json:"display_name"`
	Roles         []string    `json:"roles,omitempty"`
	FanClubLevel  int         `json:"fan_club_level,omitempty"`
	IsBotSignal   bool        `json:"is_bot_signal"`
	Text          string      `json:"text"`
	EmoteSpans    []EmoteSpan `json:"emote_spans,omitempty"`
	Amount        float64     `json:"amount,omitempty"`
	Currency      string      `json:"currency,omitempty"`
	Months        int         `json:"months,omitempty"`
	Count         int         `json:"count,omitempty"`
	VoiceOverride string      `json:"voice_override,omitempty"`
	Raw           any         `json:"raw,omitempty"`
}

// IsPaidOrEvent reports whether this event is a high-priority stream event or paid message.
// Paid and stream notification events are never dropped for queue staleness (D30).
func (e *Event) IsPaidOrEvent() bool {
	if e == nil {
		return false
	}
	if e.Kind != EventKindMessage {
		return true
	}
	return e.Amount > 0
}

// ModerationAction specifies the type of moderation event detected.
type ModerationAction string

const (
	ModActionDeleteMessage ModerationAction = "delete_message"
	ModActionBanUser       ModerationAction = "ban_user"
	ModActionClearChat     ModerationAction = "clear_chat"
)

// ModerationEvent represents a moderation sync action from Twitch, YouTube, etc.
type ModerationEvent struct {
	Platform        string           `json:"platform"`
	Channel         string           `json:"channel,omitempty"`
	Action          ModerationAction `json:"action"`
	TargetMessageID string           `json:"target_message_id,omitempty"`
	TargetUserID    string           `json:"target_user_id,omitempty"`
	Timestamp       time.Time        `json:"timestamp"`
}
