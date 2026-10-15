package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// ResolvedSpeech contains the fully resolved voice, speed, and formatted text ready for TTS.
type ResolvedSpeech struct {
	Text        string
	VoiceID     string
	Speed       float32
	SpeakEmotes bool
	Event       *Event
}

// Router coordinates voice resolution and text formatting across global and platform rules.
type Router struct{}

// NewRouter creates a new speech router.
func NewRouter() *Router {
	return &Router{}
}

// Resolve applies the 4-tier voice resolution hierarchy and formatting rules to an event.
func (r *Router) Resolve(event *Event, cfg *config.Config) ResolvedSpeech {
	if event == nil {
		return ResolvedSpeech{
			VoiceID: "af_bella",
			Speed:   1.0,
		}
	}

	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	// 1. Determine effective rules (Platform Override vs Global Rules)
	speakNames := cfg.GlobalChatRules.SpeakNames
	speakEmotes := cfg.GlobalChatRules.SpeakEmotes
	speakPlatformName := cfg.GlobalChatRules.SpeakPlatformName
	platformDefaultVoice := ""
	var platformDefaultSpeed float32

	pCfg, hasPlatform := cfg.Platforms[event.Platform]
	if hasPlatform && pCfg.Overrides.OverrideRules {
		speakNames = pCfg.Overrides.SpeakNames
		speakEmotes = pCfg.Overrides.SpeakEmotes
		speakPlatformName = pCfg.Overrides.SpeakPlatformName
		platformDefaultVoice = pCfg.Overrides.DefaultVoice
		platformDefaultSpeed = pCfg.Overrides.DefaultSpeed
	}

	// 2. Lookup Viewer Overrides (case-insensitive)
	var viewerVoice string
	var viewerSpeed float32
	var viewerNickname string

	if len(cfg.Viewers) > 0 {
		if v, ok := config.LookupViewer(cfg.Viewers, event.Platform, event.UserID, event.UserName); ok {
			viewerVoice = v.Voice
			viewerSpeed = v.Speed
			viewerNickname = v.Nickname
		}
	}

	// 3. Resolve Voice (Viewer -> Event -> Platform -> Global)
	voiceID := viewerVoice
	if voiceID == "" {
		voiceID = event.VoiceOverride
	}
	if voiceID == "" {
		voiceID = platformDefaultVoice
	}
	if voiceID == "" {
		voiceID = cfg.GlobalChatRules.DefaultVoice
	}
	if voiceID == "" {
		voiceID = "af_bella"
	}

	// 4. Resolve Speed (Viewer -> Platform -> Global)
	speed := viewerSpeed
	if speed <= 0 {
		speed = platformDefaultSpeed
	}
	if speed <= 0 {
		speed = cfg.GlobalChatRules.DefaultSpeed
	}
	if speed <= 0 {
		speed = 1.0
	}

	// 5. Determine Spoken Display Name
	spokenName := viewerNickname
	if spokenName == "" {
		spokenName = event.DisplayName
	}
	if spokenName == "" {
		spokenName = event.UserName
	}

	// 6. Format Spoken Text
	var text string
	if event.Kind != EventKindMessage {
		text = r.formatEventText(event, spokenName, pCfg)
	} else {
		if speakNames && spokenName != "" {
			text = fmt.Sprintf("%s says: %s", spokenName, event.Text)
		} else {
			text = event.Text
		}
	}

	if speakPlatformName && event.Platform != "" {
		text = fmt.Sprintf("%s, %s", event.Platform, text)
	}

	return ResolvedSpeech{
		Text:        strings.TrimSpace(text),
		VoiceID:     voiceID,
		Speed:       speed,
		SpeakEmotes: speakEmotes,
		Event:       event,
	}
}

// formatEventText formats notification messages based on platform templates or sensible defaults.
func (r *Router) formatEventText(event *Event, spokenName string, pCfg config.PlatformConfig) string {
	template := ""
	if pCfg.Notifications != nil {
		if n, ok := pCfg.Notifications[string(event.Kind)]; ok && n.Template != "" {
			template = n.Template
		}
	}

	if template == "" {
		switch event.Kind {
		case EventKindSub:
			template = "{user} just subscribed!"
		case EventKindResub:
			template = "{user} resubscribed for {months} months!"
		case EventKindGiftSub:
			template = "{user} gifted {count} subscriptions!"
		case EventKindCheer:
			if event.Text != "" {
				template = "{user} cheered {amount} bits! {message}"
			} else {
				template = "{user} cheered {amount} bits!"
			}
		case EventKindRaid:
			template = "{user} is raiding with {count} viewers!"
		case EventKindFollow:
			template = "{user} is now following!"
		case EventKindRedemption:
			if event.Text != "" {
				template = "{user} redeemed: {message}"
			} else {
				template = "{user} redeemed a reward!"
			}
		case EventKindSuperChat:
			if event.Text != "" {
				template = "{user} sent {amount} {currency}! {message}"
			} else {
				template = "{user} sent {amount} {currency}!"
			}
		case EventKindSuperSticker:
			template = "{user} sent a super sticker!"
		case EventKindGift:
			template = "{user} sent {count} {gift}!"
		default:
			if event.Text != "" {
				template = "{user}: {message}"
			} else {
				template = "{user}"
			}
		}
	}

	countVal := event.Count
	if countVal <= 0 {
		countVal = 1
	}

	replacer := strings.NewReplacer(
		"{user}", spokenName,
		"{amount}", formatAmount(event.Amount),
		"{bits}", formatAmount(event.Amount),
		"{diamonds}", formatAmount(event.Amount),
		"{currency}", event.Currency,
		"{months}", strconv.Itoa(event.Months),
		"{count}", strconv.Itoa(countVal),
		"{viewers}", strconv.Itoa(countVal),
		"{gift}", event.Text,
		"{message}", event.Text,
	)

	return replacer.Replace(template)
}

func formatAmount(amount float64) string {
	if amount == float64(int64(amount)) {
		return strconv.FormatInt(int64(amount), 10)
	}
	return strconv.FormatFloat(amount, 'f', 2, 64)
}
