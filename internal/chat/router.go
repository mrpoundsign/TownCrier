package chat

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// ResolvedSpeech contains the fully resolved voice, speed, and formatted text ready for TTS.
type ResolvedSpeech struct {
	Text        string
	VoiceID     string
	Speed       float32
	SpeakEmotes bool
	Coalesced   bool
	Event       *Event
}

// Router coordinates voice resolution, text formatting, and consecutive-speaker coalescing across global and platform rules.
type Router struct {
	mu             sync.Mutex
	nowFn          func() time.Time
	lastSpeakerKey string
	lastSpokenName string
	lastSpeakerAt  time.Time
}

// NewRouter creates a new speech router.
func NewRouter() *Router {
	return &Router{
		nowFn: time.Now,
	}
}

// SetClock overrides the time source used for consecutive-speaker coalescing windows (useful for unit tests).
func (r *Router) SetClock(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	r.mu.Lock()
	r.nowFn = fn
	r.mu.Unlock()
}

func (r *Router) now() time.Time {
	r.mu.Lock()
	fn := r.nowFn
	r.mu.Unlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

// ResetSpeaker clears any active consecutive-speaker continuation state (e.g. on Panic Mute, mode toggle, or session reset).
func (r *Router) ResetSpeaker() {
	r.mu.Lock()
	r.lastSpeakerKey = ""
	r.lastSpokenName = ""
	r.lastSpeakerAt = time.Time{}
	r.mu.Unlock()
}

// SpeakerKey returns the canonical lowercased "platform:userid" (or username fallback) speaker key for an event.
func SpeakerKey(event *Event) string {
	if event == nil {
		return ""
	}
	idOrName := strings.TrimSpace(event.UserID)
	if idOrName == "" {
		idOrName = strings.TrimSpace(event.UserName)
	}
	if idOrName == "" {
		idOrName = strings.TrimSpace(event.DisplayName)
	}
	idOrName = strings.TrimLeft(idOrName, "@")
	if idOrName == "" {
		return ""
	}
	return config.ViewerKey(event.Platform, idOrName)
}

func resolveSpokenName(event *Event, cfg *config.Config) string {
	if event == nil {
		return ""
	}
	var viewerNickname string
	if cfg != nil && len(cfg.Viewers) > 0 {
		if v, ok := config.LookupViewer(cfg.Viewers, event.Platform, event.UserID, event.UserName); ok {
			viewerNickname = v.Nickname
		}
	}
	spokenName := viewerNickname
	if spokenName == "" {
		spokenName = event.DisplayName
	}
	if spokenName == "" {
		spokenName = event.UserName
	}
	return strings.TrimLeft(strings.TrimSpace(spokenName), "@")
}

func (r *Router) recordSpokenLocked(event *Event, speakNames bool, spokenName string, now time.Time) {
	if event != nil && event.Kind == EventKindMessage && speakNames && spokenName != "" && strings.TrimSpace(event.Text) != "" {
		if spKey := SpeakerKey(event); spKey != "" {
			r.lastSpeakerKey = spKey
			r.lastSpokenName = spokenName
			r.lastSpeakerAt = now
			return
		}
	}
	r.lastSpeakerKey = ""
	r.lastSpokenName = ""
	r.lastSpeakerAt = time.Time{}
}

// RecordSpoken updates the consecutive-speaker continuation state after an event has passed all filters and been accepted for speech.
func (r *Router) RecordSpoken(event *Event, cfg *config.Config) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var speakNames bool
	var spokenName string
	if event != nil {
		speakNames, _, _, _, _, _, _ = cfg.EffectiveChatRules(event.Platform)
		spokenName = resolveSpokenName(event, cfg)
	}
	now := r.now()
	r.mu.Lock()
	r.recordSpokenLocked(event, speakNames, spokenName, now)
	r.mu.Unlock()
}

// TouchSpeaker refreshes the continuation timestamp when an enqueued message from the active consecutive speaker finishes playing.
func (r *Router) TouchSpeaker(event *Event) {
	if event == nil || event.Kind != EventKindMessage {
		return
	}
	spKey := SpeakerKey(event)
	if spKey == "" {
		return
	}
	now := r.now()
	r.mu.Lock()
	if r.lastSpeakerKey == spKey {
		r.lastSpeakerAt = now
	}
	r.mu.Unlock()
}

// Resolve applies the 4-tier voice resolution hierarchy, consecutive-speaker coalescing, and formatting rules to an event.
func (r *Router) Resolve(event *Event, cfg *config.Config) ResolvedSpeech {
	return r.ResolveWithOptions(event, cfg, true)
}

// ResolveWithOptions resolves voice, speed, and formatted speech text for an event.
// When commitState is true, the router immediately updates its consecutive-speaker tracking state.
// When commitState is false, the caller is responsible for calling RecordSpoken once the event is actually enqueued for speech.
func (r *Router) ResolveWithOptions(event *Event, cfg *config.Config, commitState bool) ResolvedSpeech {
	if event == nil {
		return ResolvedSpeech{
			VoiceID: "af_bella",
			Speed:   1.0,
		}
	}

	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	// 1. Determine effective rules (Per-Setting Platform Overrides vs Global Rules)
	speakNames, speakEmotes, _, _, speakPlatformName, effectiveDefaultVoice, effectiveDefaultSpeed := cfg.EffectiveChatRules(event.Platform)
	coalesceSec := cfg.EffectiveCoalesceSeconds(event.Platform)
	pCfg := cfg.Platforms[event.Platform]

	// 2. Lookup Viewer Overrides (case-insensitive)
	var viewerVoice string
	var viewerSpeed float32

	if len(cfg.Viewers) > 0 {
		if v, ok := config.LookupViewer(cfg.Viewers, event.Platform, event.UserID, event.UserName); ok {
			viewerVoice = v.Voice
			viewerSpeed = v.Speed
		}
	}

	// 3. Resolve Voice (Viewer -> Event -> Platform -> Global)
	voiceID := viewerVoice
	if voiceID == "" {
		voiceID = event.VoiceOverride
	}
	if voiceID == "" {
		voiceID = effectiveDefaultVoice
	}
	if voiceID == "" {
		voiceID = "af_bella"
	}

	// 4. Resolve Speed (Viewer -> Platform -> Global)
	speed := viewerSpeed
	if speed <= 0 {
		speed = effectiveDefaultSpeed
	}
	if speed <= 0 {
		speed = 1.0
	}

	// 5. Determine Spoken Display Name (strip leading '@' so TTS never speaks "at")
	spokenName := resolveSpokenName(event, cfg)

	// 6. Check Consecutive Speaker Coalescing
	spKey := SpeakerKey(event)
	coalesced := false
	now := r.now()

	r.mu.Lock()
	if event.Kind == EventKindMessage && speakNames && spokenName != "" && coalesceSec > 0 && spKey != "" {
		if r.lastSpeakerKey == spKey && r.lastSpokenName == spokenName && !r.lastSpeakerAt.IsZero() {
			if now.Sub(r.lastSpeakerAt) <= time.Duration(coalesceSec)*time.Second {
				coalesced = true
			}
		}
	}
	if commitState {
		r.recordSpokenLocked(event, speakNames, spokenName, now)
	}
	r.mu.Unlock()

	// 7. Format Spoken Text
	var text string
	if event.Kind != EventKindMessage {
		text = r.formatEventText(event, spokenName, pCfg)
	} else {
		if speakNames && spokenName != "" && !coalesced {
			text = fmt.Sprintf("%s says: %s", spokenName, event.Text)
		} else {
			text = event.Text
		}
	}

	if speakPlatformName && event.Platform != "" && !coalesced {
		text = fmt.Sprintf("%s, %s", event.Platform, text)
	}

	return ResolvedSpeech{
		Text:        strings.TrimSpace(text),
		VoiceID:     voiceID,
		Speed:       speed,
		SpeakEmotes: speakEmotes,
		Coalesced:   coalesced,
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
		case EventKindGiftRedemption:
			if strings.TrimSpace(event.Recipient) != "" {
				template = "{user} received a gift membership from {recipient}!"
			} else {
				template = "{user} received a gift membership!"
			}
		case EventKindSubUpgrade:
			if event.Text != "" {
				template = "{user} upgraded their subscription! {message}"
			} else {
				template = "{user} upgraded their subscription!"
			}
		case EventKindCheer:
			if event.Text != "" {
				template = "{user} cheered {amount} bits! {message}"
			} else {
				template = "{user} cheered {amount} bits!"
			}
		case EventKindBitsBadgeTier:
			if event.Text != "" {
				template = "{user} unlocked the {amount} bits badge! {message}"
			} else {
				template = "{user} unlocked the {amount} bits badge!"
			}
		case EventKindRaid:
			template = "{user} is raiding with {count} viewers!"
		case EventKindFollow:
			template = "{user} is now following!"
		case EventKindWatchStreak:
			if event.Text != "" {
				template = "{user} is on a {count} stream watch streak! {message}"
			} else {
				template = "{user} is on a {count} stream watch streak!"
			}
		case EventKindRedemption:
			switch {
			case event.RewardTitle != "" && event.Text != "":
				template = "{user} redeemed {reward}: {message}"
			case event.RewardTitle != "":
				template = "{user} redeemed {reward}!"
			case event.Text != "":
				template = "{user} redeemed: {message}"
			default:
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
		case EventKindCharityDonation:
			if event.Text != "" {
				template = "{user} donated {amount} {currency}! {message}"
			} else {
				template = "{user} donated {amount} {currency}!"
			}
		case EventKindAnnouncement:
			if event.Text != "" {
				template = "Announcement from {user}: {message}"
			} else {
				template = "Announcement from {user}!"
			}
		case EventKindShare:
			template = "{user} shared the stream!"
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

	rewardName := strings.TrimSpace(event.RewardTitle)
	if rewardName == "" {
		rewardName = "a reward"
	}

	giftName := strings.TrimSpace(event.ItemName)
	if giftName == "" {
		giftName = strings.TrimSpace(event.RewardTitle)
	}
	if giftName == "" {
		giftName = strings.TrimSpace(event.Text)
	}
	if giftName == "" {
		giftName = "gifts"
	}

	recipientName := strings.TrimSpace(event.Recipient)
	if recipientName == "" {
		recipientName = "someone"
	}

	replacer := strings.NewReplacer(
		"{user}", spokenName,
		"{amount}", formatAmount(event.Amount),
		"{bits}", formatAmount(event.Amount),
		"{diamonds}", formatAmount(event.Amount),
		"{currency}", event.Currency,
		"{months}", strconv.Itoa(max(1, event.Months)),
		"{count}", strconv.Itoa(countVal),
		"{streak}", strconv.Itoa(countVal),
		"{viewers}", strconv.Itoa(countVal),
		"{reward}", rewardName,
		"{gift}", giftName,
		"{recipient}", recipientName,
		"{message}", strings.TrimSpace(event.Text),
	)

	out := strings.TrimSpace(replacer.Replace(template))
	out = strings.TrimRight(out, ":- ")
	return strings.TrimSpace(out)
}

func formatAmount(amount float64) string {
	if amount == float64(int64(amount)) {
		return strconv.FormatInt(int64(amount), 10)
	}
	return strconv.FormatFloat(amount, 'f', 2, 64)
}
