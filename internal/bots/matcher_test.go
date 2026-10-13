package bots

import (
	"testing"

	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
)

func TestMatcherKnownBots(t *testing.T) {
	m, err := NewMatcher()
	if err != nil {
		t.Fatalf("failed to create matcher: %v", err)
	}

	cfg := config.DefaultConfig()

	// Nightbot is in known_bots.json
	event := &chat.Event{
		Platform: "twitch",
		UserName: "nightbot",
	}

	if !m.ShouldIgnore(event, cfg) {
		t.Errorf("expected Nightbot to be ignored")
	}

	// Disable IgnoreKnownBots
	cfg.Bots.IgnoreKnownBots = false
	if m.ShouldIgnore(event, cfg) {
		t.Errorf("expected Nightbot not to be ignored when IgnoreKnownBots is false")
	}
}

func TestMatcherBroadcasterSelfChat(t *testing.T) {
	m, err := NewMatcher()
	if err != nil {
		t.Fatalf("failed to create matcher: %v", err)
	}

	cfg := config.DefaultConfig()
	p := cfg.Platforms["twitch"]
	p.AccountName = "StreamerGuy"
	cfg.Platforms["twitch"] = p

	event := &chat.Event{
		Platform: "twitch",
		UserName: "streamerguy",
	}

	// Default IgnoreBroadcaster is true -> should ignore
	if !m.ShouldIgnore(event, cfg) {
		t.Errorf("expected broadcaster self-chat to be ignored by default")
	}

	// D43: IgnoreBroadcaster disabled for solo testing -> should NOT ignore
	cfg.Bots.IgnoreBroadcaster = false
	if m.ShouldIgnore(event, cfg) {
		t.Errorf("expected broadcaster self-chat to NOT be ignored when IgnoreBroadcaster is false")
	}
}

func TestMatcherViewerOverrides(t *testing.T) {
	m, err := NewMatcher()
	if err != nil {
		t.Fatalf("failed to create matcher: %v", err)
	}

	cfg := config.DefaultConfig()

	// Regular user ignored explicitly
	cfg.Viewers["twitch:1234"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "1234",
		Ignored:  true,
	}

	eventUser := &chat.Event{
		Platform: "twitch",
		UserID:   "1234",
		UserName: "friendly_viewer",
	}

	if !m.ShouldIgnore(eventUser, cfg) {
		t.Errorf("expected user with Ignored=true to be ignored")
	}

	// Known bot explicitly allowed
	cfg.Viewers["twitch:nightbot_id"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "nightbot_id",
		Allowed:  true,
	}

	eventBot := &chat.Event{
		Platform: "twitch",
		UserID:   "nightbot_id",
		UserName: "nightbot",
	}

	if m.ShouldIgnore(eventBot, cfg) {
		t.Errorf("expected bot with Allowed=true NOT to be ignored")
	}
}

func TestMatcherAddRemoveBot(t *testing.T) {
	m, err := NewMatcher()
	if err != nil {
		t.Fatalf("failed to create matcher: %v", err)
	}

	if m.IsKnownBot("custom_bot") {
		t.Errorf("expected custom_bot to not be in known bots initially")
	}

	m.AddBot("custom_bot")
	if !m.IsKnownBot("custom_bot") {
		t.Errorf("expected custom_bot to be recognized after AddBot")
	}

	m.RemoveBot("custom_bot")
	if m.IsKnownBot("custom_bot") {
		t.Errorf("expected custom_bot to be removed after RemoveBot")
	}
}
