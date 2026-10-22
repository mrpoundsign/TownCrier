package chat

import (
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

func TestRouterVoiceResolutionHierarchy(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "af_bella"

	p := cfg.Platforms["twitch"]
	p.Overrides.OverrideRules = true
	p.Overrides.DefaultVoice = "am_adam"
	cfg.Platforms["twitch"] = p

	// 1. Global fallback when platform override is not enabled
	eventGlobal := &Event{Platform: "youtube", Kind: EventKindMessage, Text: "hi"}
	res := router.Resolve(eventGlobal, cfg)
	if res.VoiceID != "af_bella" {
		t.Errorf("expected global default voice 'af_bella', got %q", res.VoiceID)
	}

	// 2. Platform override
	eventTwitch := &Event{Platform: "twitch", Kind: EventKindMessage, Text: "hi"}
	res = router.Resolve(eventTwitch, cfg)
	if res.VoiceID != "am_adam" {
		t.Errorf("expected platform override voice 'am_adam', got %q", res.VoiceID)
	}

	// 3. Event-specific voice override
	eventCustom := &Event{Platform: "twitch", Kind: EventKindMessage, VoiceOverride: "bf_emma", Text: "hi"}
	res = router.Resolve(eventCustom, cfg)
	if res.VoiceID != "bf_emma" {
		t.Errorf("expected event voice override 'bf_emma', got %q", res.VoiceID)
	}

	// 4. Viewer override takes highest precedence
	cfg.Viewers["twitch:vip1"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "vip1",
		Voice:    "bm_george",
	}
	eventViewer := &Event{Platform: "twitch", UserID: "vip1", VoiceOverride: "bf_emma", Kind: EventKindMessage, Text: "hi"}
	res = router.Resolve(eventViewer, cfg)
	if res.VoiceID != "bm_george" {
		t.Errorf("expected viewer override voice 'bm_george', got %q", res.VoiceID)
	}
}

func TestRouterVoiceResolutionCaseInsensitive(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()

	cfg.Viewers["twitch:mrpoundsign"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "MrPoundSign",
		Voice:    "am_adam",
	}

	event := &Event{
		Platform: "twitch",
		UserID:   "mrpoundsign",
		UserName: "MrPoundSign",
		Kind:     EventKindMessage,
		Text:     "test voice",
	}

	res := router.Resolve(event, cfg)
	if res.VoiceID != "am_adam" {
		t.Errorf("expected voice 'am_adam', got %q", res.VoiceID)
	}

	eventUpper := &Event{
		Platform: "twitch",
		UserID:   "MRPOUNDSIGN",
		UserName: "mrpoundsign",
		Kind:     EventKindMessage,
		Text:     "test voice",
	}
	resUpper := router.Resolve(eventUpper, cfg)
	if resUpper.VoiceID != "am_adam" {
		t.Errorf("expected voice 'am_adam' for uppercase lookup, got %q", resUpper.VoiceID)
	}
}

func TestRouterSpeedResolution(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultSpeed = 1.0

	// 1. Global default
	event := &Event{Platform: "twitch", Kind: EventKindMessage, Text: "hi"}
	res := router.Resolve(event, cfg)
	if res.Speed != 1.0 {
		t.Errorf("expected default speed 1.0, got %f", res.Speed)
	}

	// 2. Platform override
	p := cfg.Platforms["twitch"]
	p.Overrides.OverrideRules = true
	p.Overrides.DefaultSpeed = 1.1
	cfg.Platforms["twitch"] = p

	res = router.Resolve(event, cfg)
	if res.Speed != 1.1 {
		t.Errorf("expected platform speed 1.1, got %f", res.Speed)
	}

	// 3. Viewer speed override
	cfg.Viewers["twitch:fast_user"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "fast_user",
		Speed:    1.25,
	}
	eventViewer := &Event{Platform: "twitch", UserID: "fast_user", Kind: EventKindMessage, Text: "hi"}
	res = router.Resolve(eventViewer, cfg)
	if res.Speed != 1.25 {
		t.Errorf("expected viewer speed 1.25, got %f", res.Speed)
	}
}

func TestRouterTextFormatting(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.SpeakNames = true

	// Standard message with speak_names = true
	event := &Event{
		Platform:    "twitch",
		UserName:    "bob",
		DisplayName: "BobTheStreamer",
		Kind:        EventKindMessage,
		Text:        "hello world",
	}

	res := router.Resolve(event, cfg)
	if res.Text != "BobTheStreamer says: hello world" {
		t.Errorf("expected 'BobTheStreamer says: hello world', got %q", res.Text)
	}

	// Nickname override in viewers
	cfg.Viewers["twitch:bob_id"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "bob_id",
		Nickname: "Bobby",
	}
	event.UserID = "bob_id"
	res = router.Resolve(event, cfg)
	if res.Text != "Bobby says: hello world" {
		t.Errorf("expected 'Bobby says: hello world', got %q", res.Text)
	}

	// Speak names = false
	cfg.GlobalChatRules.SpeakNames = false
	res = router.Resolve(event, cfg)
	if res.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", res.Text)
	}

	// Speak platform name = true
	cfg.GlobalChatRules.SpeakPlatformName = true
	res = router.Resolve(event, cfg)
	if res.Text != "twitch, hello world" {
		t.Errorf("expected 'twitch, hello world', got %q", res.Text)
	}

	// Leading '@' in YouTube/TikTok/Kick display names should never be spoken as "at"
	cfg.GlobalChatRules.SpeakNames = true
	cfg.GlobalChatRules.SpeakPlatformName = false
	ytEvent := &Event{
		Platform:    "youtube",
		UserName:    "@mrpoundsign",
		DisplayName: "@mrpoundsign",
		Kind:        EventKindMessage,
		Text:        "testing youtube handle",
	}
	resYT := router.Resolve(ytEvent, cfg)
	if resYT.Text != "mrpoundsign says: testing youtube handle" {
		t.Errorf("expected 'mrpoundsign says: testing youtube handle', got %q", resYT.Text)
	}
}

func TestRouterNotificationEvents(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()

	eventSub := &Event{
		Platform: "twitch",
		UserName: "alice",
		Kind:     EventKindSub,
	}
	res := router.Resolve(eventSub, cfg)
	if res.Text != "alice just subscribed!" {
		t.Errorf("expected 'alice just subscribed!', got %q", res.Text)
	}

	eventResub := &Event{
		Platform: "twitch",
		UserName: "charlie",
		Kind:     EventKindResub,
		Months:   6,
	}
	res = router.Resolve(eventResub, cfg)
	if res.Text != "charlie resubscribed for 6 months!" {
		t.Errorf("expected 'charlie resubscribed for 6 months!', got %q", res.Text)
	}

	eventCheer := &Event{
		Platform: "twitch",
		UserName: "dan",
		Kind:     EventKindCheer,
		Amount:   500,
		Text:     "hype train!",
	}
	res = router.Resolve(eventCheer, cfg)
	if res.Text != "dan cheered 500 bits: hype train!" {
		t.Errorf("expected 'dan cheered 500 bits: hype train!', got %q", res.Text)
	}

	eventStreak := &Event{
		Platform: "twitch",
		UserName: "wifestreamer",
		Kind:     EventKindWatchStreak,
		Count:    5,
		Amount:   450,
		Text:     "Five in a row!",
	}
	res = router.Resolve(eventStreak, cfg)
	if res.Text != "wifestreamer is on a 5 stream watch streak! Five in a row!" {
		t.Errorf("unexpected watch streak speech: %q", res.Text)
	}

	eventGiftRedemp := &Event{
		Platform:  "youtube",
		UserName:  "lucky",
		Kind:      EventKindGiftRedemption,
		Recipient: "gifter123",
	}
	res = router.Resolve(eventGiftRedemp, cfg)
	if res.Text != "lucky received a gift membership from gifter123!" {
		t.Errorf("unexpected gift redemption speech: %q", res.Text)
	}
}
