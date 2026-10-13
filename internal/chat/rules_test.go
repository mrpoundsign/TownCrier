package chat

import (
	"testing"

	"github.com/mrpoundsign/OctochorpeBot/internal/config"
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
