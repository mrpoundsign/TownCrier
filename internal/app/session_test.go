package app

import (
	"errors"
	"sync"
	"testing"

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
