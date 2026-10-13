package app

import (
	"errors"
	"sync"
	"testing"

	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/secrets"
)

func TestSessionLifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	store := secrets.NewMemoryStore()
	session := NewSession(cfg, store)

	if session.IsRunning() {
		t.Error("expected session not to be running initially")
	}

	// Stop when not running
	if err := session.Stop(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning, got %v", err)
	}

	// Start
	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !session.IsRunning() {
		t.Error("expected session to be running")
	}

	// Start again
	if err := session.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("expected ErrAlreadyStarted, got %v", err)
	}

	// Stop
	if err := session.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if session.IsRunning() {
		t.Error("expected session not to be running after stop")
	}
}

func TestSessionReloadConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "af_bella"
	session := NewSession(cfg, secrets.NewMemoryStore())

	newCfg := cfg.Clone()
	newCfg.GlobalChatRules.DefaultVoice = "am_adam"

	if err := session.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}

	loaded := session.Config()
	if loaded.GlobalChatRules.DefaultVoice != "am_adam" {
		t.Errorf("expected voice 'am_adam', got %q", loaded.GlobalChatRules.DefaultVoice)
	}

	if err := session.ReloadConfig(nil); err == nil {
		t.Error("expected error reloading nil config")
	}
}

func TestSessionConcurrency(t *testing.T) {
	session := NewSession(config.DefaultConfig(), secrets.NewMemoryStore())
	_ = session.Start()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = session.IsRunning()
			c := session.Config()
			c.GlobalChatRules.DefaultSpeed = float32(idx)
			_ = session.ReloadConfig(c)
		}(i)
	}
	wg.Wait()
	_ = session.Stop()
}

func TestSessionDispatchEvent(t *testing.T) {
	cfg := config.DefaultConfig()
	session := NewSession(cfg, secrets.NewMemoryStore())

	// Pause queue so we can inspect enqueued items
	session.Queue().Pause()

	// 1. Known bot should be ignored
	botEvent := &chat.Event{
		Platform: "twitch",
		UserName: "nightbot",
		Text:     "I am a bot",
	}
	if err := session.DispatchEvent(botEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 0 {
		t.Errorf("expected bot message to be ignored, depth is %d", session.Queue().Depth())
	}

	// 2. Normal chatter event should be sanitized and enqueued
	chatEvent := &chat.Event{
		ID:          "msg-1",
		Platform:    "twitch",
		UserName:    "alice",
		DisplayName: "Alice",
		Kind:        chat.EventKindMessage,
		Text:        "hello world https://twitch.tv",
	}
	if err := session.DispatchEvent(chatEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 1 {
		t.Fatalf("expected queue depth 1, got %d", session.Queue().Depth())
	}

	// 3. Paid/Event should be priority 0
	subEvent := &chat.Event{
		ID:          "sub-1",
		Platform:    "twitch",
		UserName:    "bob",
		DisplayName: "Bob",
		Kind:        chat.EventKindSub,
	}
	if err := session.DispatchEvent(subEvent); err != nil {
		t.Fatalf("DispatchEvent failed: %v", err)
	}
	if session.Queue().Depth() != 2 {
		t.Fatalf("expected queue depth 2, got %d", session.Queue().Depth())
	}

	// 4. Moderation deletion
	session.DispatchModeration(&chat.ModerationEvent{
		Platform:        "twitch",
		Action:          chat.ModActionDeleteMessage,
		TargetMessageID: "msg-1",
	})
	if session.Queue().Depth() != 1 {
		t.Errorf("expected depth 1 after deleting msg-1, got %d", session.Queue().Depth())
	}

	// 5. Panic mute
	session.Panic()
	if session.Queue().Depth() != 0 {
		t.Errorf("expected depth 0 after Panic, got %d", session.Queue().Depth())
	}
}
