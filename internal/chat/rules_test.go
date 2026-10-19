package chat

import (
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/config"
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

