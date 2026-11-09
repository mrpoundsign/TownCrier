package chat

import (
	"testing"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

func TestEvaluateReadRules_AllChat(t *testing.T) {
	rules := config.ReadRules{
		AllChat: true,
	}

	ev := &Event{
		Kind:  EventKindMessage,
		Text:  "hello world",
		Roles: []string{"broadcaster"},
	}

	speak, text := EvaluateReadRules(ev, rules)
	if !speak {
		t.Error("expected AllChat to speak broadcaster message")
	}
	if text != "" {
		t.Errorf("expected empty modified text, got %q", text)
	}
}

func TestEvaluateReadRules_RoleGating(t *testing.T) {
	rules := config.ReadRules{
		AllChat:   true,
		RoleGated: true,
		Roles:     []string{"mod", "broadcaster"},
	}

	// Broadcaster matches
	broadcasterEv := &Event{
		Kind:  EventKindMessage,
		Text:  "streamer message",
		Roles: []string{"broadcaster"},
	}
	speak, _ := EvaluateReadRules(broadcasterEv, rules)
	if !speak {
		t.Error("expected broadcaster to pass role gate")
	}

	// Regular viewer does not match
	viewerEv := &Event{
		Kind:  EventKindMessage,
		Text:  "viewer message",
		Roles: []string{},
	}
	speak, _ = EvaluateReadRules(viewerEv, rules)
	if speak {
		t.Error("expected regular viewer to be blocked by role gate")
	}
}

func TestEvaluateReadRules_PrefixOnly(t *testing.T) {
	rules := config.ReadRules{
		AllChat:    false,
		PrefixOnly: true,
		Prefix:     "!tts",
	}

	// Without prefix
	normalEv := &Event{
		Kind: EventKindMessage,
		Text: "just chatting",
	}
	speak, _ := EvaluateReadRules(normalEv, rules)
	if speak {
		t.Error("expected normal message to be blocked in prefix-only mode")
	}

	// With prefix
	prefixEv := &Event{
		Kind: EventKindMessage,
		Text: "!tts please speak this",
	}
	speak, stripped := EvaluateReadRules(prefixEv, rules)
	if !speak {
		t.Error("expected prefix message to be spoken")
	}
	if stripped != "please speak this" {
		t.Errorf("expected stripped prefix, got %q", stripped)
	}
}

func TestEvaluateReadRules_PaidOnly(t *testing.T) {
	rules := config.ReadRules{
		AllChat:  false,
		PaidOnly: true,
	}

	// Normal message
	normalEv := &Event{
		Kind: EventKindMessage,
		Text: "free chat",
	}
	speak, _ := EvaluateReadRules(normalEv, rules)
	if speak {
		t.Error("expected normal message to be blocked in paid-only mode")
	}

	// Cheer bits
	cheerEv := &Event{
		Kind:   EventKindCheer,
		Text:   "cheer100 woohoo!",
		Amount: 100,
	}
	speak, _ = EvaluateReadRules(cheerEv, rules)
	if !speak {
		t.Error("expected cheer event to be spoken in paid-only mode")
	}
}

func TestEvaluateReadRules_IgnoredPrefixes(t *testing.T) {
	rules := config.ReadRules{
		AllChat:         true,
		IgnorePrefixes:  true,
		IgnoredPrefixes: []string{"!", "?", "$"},
	}

	// 1. Bot commands starting with ! should be ignored
	botCmdEv := &Event{Kind: EventKindMessage, Text: "!discord"}
	speak, _ := EvaluateReadRules(botCmdEv, rules)
	if speak {
		t.Error("expected !discord to be ignored by IgnoredPrefixes")
	}

	// 2. Bot command starting with ? should be ignored
	qCmdEv := &Event{Kind: EventKindMessage, Text: "?points"}
	speak, _ = EvaluateReadRules(qCmdEv, rules)
	if speak {
		t.Error("expected ?points to be ignored by IgnoredPrefixes")
	}

	// 3. Normal chat message should be spoken
	chatEv := &Event{Kind: EventKindMessage, Text: "hello world"}
	speak, _ = EvaluateReadRules(chatEv, rules)
	if !speak {
		t.Error("expected normal chat message to be spoken")
	}

	// 4. Message with exclamation mark not at start should be spoken
	midExclEv := &Event{Kind: EventKindMessage, Text: "great stream!"}
	speak, _ = EvaluateReadRules(midExclEv, rules)
	if !speak {
		t.Error("expected message with exclamation at end to be spoken")
	}

	// 5. When IgnorePrefixes is disabled, !discord should be spoken
	rulesDisabled := rules
	rulesDisabled.IgnorePrefixes = false
	speak, _ = EvaluateReadRules(botCmdEv, rulesDisabled)
	if !speak {
		t.Error("expected !discord to be spoken when IgnorePrefixes is false")
	}

	// 6. When PrefixOnly is enabled for !tts, !tts message passes while !song is dropped
	rulesPrefixOnly := config.ReadRules{
		AllChat:         true,
		PrefixOnly:      true,
		Prefix:          "!tts",
		IgnorePrefixes:  true,
		IgnoredPrefixes: []string{"!"},
	}
	ttsEv := &Event{Kind: EventKindMessage, Text: "!tts speak this aloud"}
	speak, stripped := EvaluateReadRules(ttsEv, rulesPrefixOnly)
	if !speak || stripped != "speak this aloud" {
		t.Errorf("expected !tts command to pass, got speak=%v text=%q", speak, stripped)
	}

	songEv := &Event{Kind: EventKindMessage, Text: "!song"}
	speak, _ = EvaluateReadRules(songEv, rulesPrefixOnly)
	if speak {
		t.Error("expected !song to be dropped when IgnorePrefixes is active")
	}
}

func TestEvaluateReadRules_FanClubLevel(t *testing.T) {
	rules := config.ReadRules{
		AllChat:         true,
		RoleGated:       true,
		Roles:           []string{"mod", "subscriber", "fan_club"},
		MinFanClubLevel: 10,
	}

	// 1. Fan club member below min level (level 5) without other roles -> blocked
	lowFanEv := &Event{
		Kind:         EventKindMessage,
		Text:         "level 5 fan",
		Roles:        []string{"fan_club"},
		FanClubLevel: 5,
	}
	if speak, _ := EvaluateReadRules(lowFanEv, rules); speak {
		t.Error("expected Fan Club level 5 viewer to be blocked when MinFanClubLevel is 10")
	}

	// 2. Fan club member at min level (level 10) -> allowed
	exactFanEv := &Event{
		Kind:         EventKindMessage,
		Text:         "level 10 fan",
		Roles:        []string{"fan_club"},
		FanClubLevel: 10,
	}
	if speak, _ := EvaluateReadRules(exactFanEv, rules); !speak {
		t.Error("expected Fan Club level 10 viewer to pass when MinFanClubLevel is 10")
	}

	// 3. Fan club member above min level (level 25) -> allowed
	highFanEv := &Event{
		Kind:         EventKindMessage,
		Text:         "level 25 fan",
		Roles:        []string{"fan_club"},
		FanClubLevel: 25,
	}
	if speak, _ := EvaluateReadRules(highFanEv, rules); !speak {
		t.Error("expected Fan Club level 25 viewer to pass when MinFanClubLevel is 10")
	}

	// 4. OR logic: Moderator or Super Fan / Subscriber with low/no Fan Club level -> allowed
	modLowFanEv := &Event{
		Kind:         EventKindMessage,
		Text:         "mod message",
		Roles:        []string{"mod", "fan_club"},
		FanClubLevel: 2,
	}
	if speak, _ := EvaluateReadRules(modLowFanEv, rules); !speak {
		t.Error("expected Moderator with Fan Club level 2 to pass via OR role gate")
	}

	superFanEv := &Event{
		Kind:         EventKindMessage,
		Text:         "superfan message",
		Roles:        []string{"subscriber"},
		FanClubLevel: 0,
	}
	if speak, _ := EvaluateReadRules(superFanEv, rules); !speak {
		t.Error("expected Super Fan / Subscriber to pass via OR role gate")
	}
}

func TestExtractMentionsAndLeadingMention(t *testing.T) {
	// 1. Leading mention and inline mention
	mentions := ExtractMentions("@Alice hey have you seen @Bob_123's stream?")
	if len(mentions) != 2 || mentions[0] != "Alice" || mentions[1] != "Bob_123" {
		t.Fatalf("expected [Alice, Bob_123], got %v", mentions)
	}
	lead, ok := ExtractLeadingMention("@Alice hey have you seen @Bob_123's stream?")
	if !ok || lead != "Alice" {
		t.Fatalf("expected leading mention Alice, got %q (ok=%v)", lead, ok)
	}

	// 2. Mid-sentence mention only (no leading mention)
	mentions = ExtractMentions("I totally agree with @charlie!")
	if len(mentions) != 1 || mentions[0] != "charlie" {
		t.Fatalf("expected [charlie], got %v", mentions)
	}
	if _, ok := ExtractLeadingMention("I totally agree with @charlie!"); ok {
		t.Fatal("expected no leading mention for mid-sentence @charlie")
	}

	// 3. Email addresses and bare '@' must NOT match
	mentions = ExtractMentions("Contact me at support@example.com or @ for info")
	if len(mentions) != 0 {
		t.Fatalf("expected no mentions for email/bare @, got %v", mentions)
	}
	if _, ok := ExtractLeadingMention("support@example.com hello"); ok {
		t.Fatal("expected email at start not to be treated as leading mention")
	}
}

func TestNormalizeBroadcasterHandle(t *testing.T) {
	cases := map[string]string{
		"@MrPoundSign":                                "mrpoundsign",
		"https://www.twitch.tv/MrPoundSign":           "mrpoundsign",
		"https://www.youtube.com/@StreamerHandle":     "streamerhandle",
		"https://www.tiktok.com/@tiktok_creator/live": "tiktok_creator",
		"kickstreamer#12345:67890":                    "kickstreamer",
	}
	for input, want := range cases {
		if got := NormalizeBroadcasterHandle(input); got != want {
			t.Errorf("NormalizeBroadcasterHandle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestShouldIgnoreMentionOrReply(t *testing.T) {
	const broadcaster = "https://www.twitch.tv/streamer_jane"

	leadingViewerEv := &Event{
		Kind: EventKindMessage,
		Text: "@other_viewer how are you doing?",
	}
	midViewerEv := &Event{
		Kind: EventKindMessage,
		Text: "That was awesome @other_viewer!",
	}
	leadingBroadcasterEv := &Event{
		Kind: EventKindMessage,
		Text: "@Streamer_Jane look behind you!",
	}
	midBroadcasterEv := &Event{
		Kind: EventKindMessage,
		Text: "Great play @Streamer_Jane!",
	}
	replyToViewerEv := &Event{
		Kind:        EventKindMessage,
		IsReply:     true,
		ReplyToUser: "other_viewer",
		Text:        "@other_viewer totally agree",
	}
	replyToBroadcasterEv := &Event{
		Kind:        EventKindMessage,
		IsReply:     true,
		ReplyToUser: "streamer_jane",
		Text:        "@Streamer_Jane I love this game",
	}
	paidMentionEv := &Event{
		Kind:   EventKindMessage,
		Amount: 5.00,
		Text:   "@other_viewer hype!",
	}

	// 1. Mode "off": nothing is ignored
	if ShouldIgnoreMentionOrReply(leadingViewerEv, config.IgnoreMentionsOff, false, broadcaster) {
		t.Error("expected Mode Off to not ignore leading mention")
	}
	if ShouldIgnoreMentionOrReply(replyToViewerEv, config.IgnoreMentionsOff, false, broadcaster) {
		t.Error("expected Mode Off to not ignore reply")
	}

	// 2. Mode "replies_and_leading" (allowBroadcaster = false)
	if !ShouldIgnoreMentionOrReply(leadingViewerEv, config.IgnoreMentionsRepliesAndLeading, false, broadcaster) {
		t.Error("expected leading viewer mention to be ignored in replies_and_leading mode")
	}
	if !ShouldIgnoreMentionOrReply(replyToViewerEv, config.IgnoreMentionsRepliesAndLeading, false, broadcaster) {
		t.Error("expected reply to viewer to be ignored in replies_and_leading mode")
	}
	if ShouldIgnoreMentionOrReply(midViewerEv, config.IgnoreMentionsRepliesAndLeading, false, broadcaster) {
		t.Error("expected mid-sentence mention to be spoken in replies_and_leading mode")
	}
	if !ShouldIgnoreMentionOrReply(leadingBroadcasterEv, config.IgnoreMentionsRepliesAndLeading, false, broadcaster) {
		t.Error("expected leading broadcaster mention to be ignored when allowBroadcaster is false")
	}

	// 3. Mode "replies_and_leading" (allowBroadcaster = true)
	if ShouldIgnoreMentionOrReply(leadingBroadcasterEv, config.IgnoreMentionsRepliesAndLeading, true, broadcaster) {
		t.Error("expected leading broadcaster mention to be spoken when allowBroadcaster is true")
	}
	if ShouldIgnoreMentionOrReply(replyToBroadcasterEv, config.IgnoreMentionsRepliesAndLeading, true, broadcaster) {
		t.Error("expected reply to broadcaster to be spoken when allowBroadcaster is true")
	}
	if !ShouldIgnoreMentionOrReply(replyToViewerEv, config.IgnoreMentionsRepliesAndLeading, true, broadcaster) {
		t.Error("expected reply to another viewer to still be ignored when allowBroadcaster is true")
	}

	// 4. Mode "all" (allowBroadcaster = false)
	if !ShouldIgnoreMentionOrReply(midViewerEv, config.IgnoreMentionsAll, false, broadcaster) {
		t.Error("expected mid-sentence viewer mention to be ignored in all mode")
	}
	if !ShouldIgnoreMentionOrReply(midBroadcasterEv, config.IgnoreMentionsAll, false, broadcaster) {
		t.Error("expected mid-sentence broadcaster mention to be ignored in all mode when allowBroadcaster is false")
	}

	// 5. Mode "all" (allowBroadcaster = true)
	if ShouldIgnoreMentionOrReply(midBroadcasterEv, config.IgnoreMentionsAll, true, broadcaster) {
		t.Error("expected mid-sentence broadcaster mention to be spoken in all mode when allowBroadcaster is true")
	}
	if !ShouldIgnoreMentionOrReply(midViewerEv, config.IgnoreMentionsAll, true, broadcaster) {
		t.Error("expected mid-sentence viewer mention to still be ignored in all mode when allowBroadcaster is true")
	}

	// 6. Paid message always bypasses mention filter (4-A)
	if ShouldIgnoreMentionOrReply(paidMentionEv, config.IgnoreMentionsAll, false, broadcaster) {
		t.Error("expected paid message to never be ignored by mention filter")
	}
}

