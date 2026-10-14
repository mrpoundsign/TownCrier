package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Version != CurrentVersion {
		t.Errorf("expected version %d, got %d", CurrentVersion, cfg.Version)
	}
	if !cfg.GlobalChatRules.SpeakNames {
		t.Error("expected speak_names to be true by default")
	}
	if cfg.GlobalChatRules.SpeakEmotes {
		t.Error("expected speak_emotes to be false by default")
	}
	if cfg.GlobalChatRules.DefaultVoice != "af_bella" {
		t.Errorf("expected default voice 'af_bella', got %q", cfg.GlobalChatRules.DefaultVoice)
	}
	if cfg.GlobalChatRules.DefaultSpeed != 1.0 {
		t.Errorf("expected default speed 1.0, got %f", cfg.GlobalChatRules.DefaultSpeed)
	}
	if !cfg.Platforms["twitch"].EverEnabled {
		t.Error("expected Twitch EverEnabled to be true by default")
	}
	if !cfg.Bots.IgnoreKnownBots {
		t.Error("expected IgnoreKnownBots to be true by default")
	}
}

func TestSaveAndLoadAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "sub", "config.json")

	cfg := DefaultConfig()
	cfg.Audio.DeviceName = "Virtual Cable A"
	cfg.GlobalChatRules.DefaultVoice = "am_adam"
	cfg.GlobalChatRules.DefaultSpeed = 1.15

	if err := Save(configPath, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Audio.DeviceName != "Virtual Cable A" {
		t.Errorf("expected device name 'Virtual Cable A', got %q", loaded.Audio.DeviceName)
	}
	if loaded.GlobalChatRules.DefaultVoice != "am_adam" {
		t.Errorf("expected voice 'am_adam', got %q", loaded.GlobalChatRules.DefaultVoice)
	}
	if loaded.GlobalChatRules.DefaultSpeed != 1.15 {
		t.Errorf("expected speed 1.15, got %f", loaded.GlobalChatRules.DefaultSpeed)
	}
}

func TestLoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "missing.json")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("expected nil error on missing config, got %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("expected default version, got %d", cfg.Version)
	}
}

func TestLoadCorrupt(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "corrupt.json")

	if err := os.WriteFile(configPath, []byte("invalid json {{"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error when loading corrupt JSON")
	}
}

func TestEffectiveChatRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GlobalChatRules.SpeakNames = true
	cfg.GlobalChatRules.SpeakEmotes = false
	cfg.GlobalChatRules.DefaultVoice = "af_bella"
	cfg.GlobalChatRules.DefaultSpeed = 1.0

	// Case 1: Platform without overrides inherits global
	names, emotes, platformName, voice, speed := cfg.EffectiveChatRules("twitch")
	if !names || emotes || platformName || voice != "af_bella" || speed != 1.0 {
		t.Errorf("unexpected global inheritance: names=%v, emotes=%v, platformName=%v, voice=%s, speed=%f",
			names, emotes, platformName, voice, speed)
	}

	// Case 2: Platform with overrides enabled
	p := cfg.Platforms["twitch"]
	p.Overrides.OverrideRules = true
	p.Overrides.SpeakNames = false
	p.Overrides.SpeakEmotes = true
	p.Overrides.DefaultVoice = "am_michael"
	p.Overrides.DefaultSpeed = 1.25
	cfg.Platforms["twitch"] = p

	names, emotes, platformName, voice, speed = cfg.EffectiveChatRules("twitch")
	if names || !emotes || platformName || voice != "am_michael" || speed != 1.25 {
		t.Errorf("unexpected platform override: names=%v, emotes=%v, platformName=%v, voice=%s, speed=%f",
			names, emotes, platformName, voice, speed)
	}
}

func TestClone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Audio.DeviceName = "Original"

	clone := cfg.Clone()
	clone.Audio.DeviceName = "Modified"

	if cfg.Audio.DeviceName != "Original" {
		t.Errorf("modifying clone mutated original config: %s", cfg.Audio.DeviceName)
	}
}

func TestLegacyMigration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	legacyDir := filepath.Join(tmpDir, "OctochorpeBot")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	legacyCfg := DefaultConfig()
	legacyCfg.GlobalChatRules.DefaultVoice = "af_nicole"
	legacyCfg.Audio.DeviceName = "Migrated Device"

	legacyPath := filepath.Join(legacyDir, "config.json")
	if err := Save(legacyPath, legacyCfg); err != nil {
		t.Fatalf("failed to save legacy config: %v", err)
	}

	// Calling Load with "" should detect legacy config and migrate to TownCrier
	migrated, err := Load("")
	if err != nil {
		t.Fatalf("Load with legacy migration failed: %v", err)
	}

	if migrated.GlobalChatRules.DefaultVoice != "af_nicole" {
		t.Errorf("expected migrated voice 'af_nicole', got %q", migrated.GlobalChatRules.DefaultVoice)
	}
	if migrated.Audio.DeviceName != "Migrated Device" {
		t.Errorf("expected migrated device 'Migrated Device', got %q", migrated.Audio.DeviceName)
	}

	newPath := filepath.Join(tmpDir, "com.github.mrpoundsign.towncrier", "config.json")
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected migrated config at %s, got error: %v", newPath, err)
	}
}

func TestLookupViewerCaseInsensitive(t *testing.T) {
	viewers := map[string]ViewerConfig{
		"twitch:mrpoundsign": {
			Platform: "twitch",
			UserID:   "MrPoundSign",
			Voice:    "am_adam",
			Speed:    1.2,
		},
		"twitch:mixedcaseuser": {
			Platform: "twitch",
			UserID:   "MixedCaseUser",
			Voice:    "af_bella",
			Allowed:  true,
		},
		"Twitch:OldLegacyUser": {
			Platform: "Twitch",
			UserID:   "OldLegacyUser",
			Voice:    "am_michael",
			Ignored:  true,
		},
	}

	// 1. Lowercase lookup matching mixed-case UserID
	vc, ok := LookupViewer(viewers, "twitch", "mrpoundsign", "mrpoundsign")
	if !ok || vc.Voice != "am_adam" {
		t.Errorf("expected to find twitch:mrpoundsign with voice am_adam, got %v (ok=%v)", vc, ok)
	}

	// 2. Uppercase platform and username lookup
	vc, ok = LookupViewer(viewers, "TWITCH", "12345", "MRPOUNDSIGN")
	if !ok || vc.Voice != "am_adam" {
		t.Errorf("expected to find MRPOUNDSIGN by username, got %v (ok=%v)", vc, ok)
	}

	// 3. MixedCaseUser lookup with lowercase
	vc, ok = LookupViewer(viewers, "twitch", "", "mixedcaseuser")
	if !ok || !vc.Allowed {
		t.Errorf("expected to find mixedcaseuser, got %v (ok=%v)", vc, ok)
	}

	// 4. Legacy unnormalized key
	vc, ok = LookupViewer(viewers, "twitch", "oldlegacyuser", "oldlegacyuser")
	if !ok || !vc.Ignored {
		t.Errorf("expected to find OldLegacyUser, got %v (ok=%v)", vc, ok)
	}

	// 5. Non-existent viewer
	_, ok = LookupViewer(viewers, "twitch", "unknown", "unknown")
	if ok {
		t.Error("expected non-existent viewer to return false")
	}
}
