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
	if cfg.GlobalChatRules.DefaultVoice != "supertonic:f1" {
		t.Errorf("expected default voice 'supertonic:f1', got %q", cfg.GlobalChatRules.DefaultVoice)
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
	if !cfg.TTS.Enabled {
		t.Error("expected TTS.Enabled to be true by default")
	}
	if cfg.Logging.ChatScale != 100 {
		t.Errorf("expected Logging.ChatScale 100 by default, got %d", cfg.Logging.ChatScale)
	}
}

func TestChatScalePersistenceAndClamping(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	cfg.Logging.ChatScale = 145
	cfg.Logging.LiveLogs = []LiveLogPreset{
		{
			ID:        "preset-inherit",
			Name:      "Inherit",
			ChatScale: 0,
		},
		{
			ID:        "preset-custom",
			Name:      "Custom 180",
			ChatScale: 180,
		},
		{
			ID:        "preset-low",
			Name:      "Too Low",
			ChatScale: 20,
		},
		{
			ID:        "preset-high",
			Name:      "Too High",
			ChatScale: 500,
		},
	}

	if err := Save(configPath, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Logging.ChatScale != 145 {
		t.Errorf("expected Logging.ChatScale 145, got %d", loaded.Logging.ChatScale)
	}
	if len(loaded.Logging.LiveLogs) != 4 {
		t.Fatalf("expected 4 LiveLogPresets, got %d", len(loaded.Logging.LiveLogs))
	}
	if loaded.Logging.LiveLogs[0].ChatScale != 0 {
		t.Errorf("expected preset 0 ChatScale 0 (inherit), got %d", loaded.Logging.LiveLogs[0].ChatScale)
	}
	if loaded.Logging.LiveLogs[1].ChatScale != 180 {
		t.Errorf("expected preset 1 ChatScale 180, got %d", loaded.Logging.LiveLogs[1].ChatScale)
	}
	if loaded.Logging.LiveLogs[2].ChatScale != 50 {
		t.Errorf("expected preset 2 ChatScale clamped to 50, got %d", loaded.Logging.LiveLogs[2].ChatScale)
	}
	if loaded.Logging.LiveLogs[3].ChatScale != 300 {
		t.Errorf("expected preset 3 ChatScale clamped to 300, got %d", loaded.Logging.LiveLogs[3].ChatScale)
	}
}

func TestTTSConfigEnabledRetention(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	cfg.TTS.Enabled = false
	cfg.TTS.UseGPU = true
	cfg.TTS.NumThreads = 6
	cfg.TTS.ModelFamily = "kokoro"
	cfg.TTS.Preset = "balanced"
	cfg.GlobalChatRules.DefaultVoice = "af_bella"

	if err := Save(configPath, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.TTS.Enabled {
		t.Error("expected loaded.TTS.Enabled to remain false")
	}
	if !loaded.TTS.UseGPU {
		t.Errorf("expected TTS.UseGPU true, got %v", loaded.TTS.UseGPU)
	}
	if loaded.TTS.NumThreads != 6 {
		t.Errorf("expected TTS.NumThreads 6, got %d", loaded.TTS.NumThreads)
	}
	if loaded.TTS.ModelFamily != "kokoro" {
		t.Errorf("expected TTS.ModelFamily 'kokoro', got %q", loaded.TTS.ModelFamily)
	}
	if loaded.TTS.Preset != "balanced" {
		t.Errorf("expected TTS.Preset 'balanced', got %q", loaded.TTS.Preset)
	}
	if loaded.GlobalChatRules.DefaultVoice != "af_bella" {
		t.Errorf("expected DefaultVoice 'af_bella', got %q", loaded.GlobalChatRules.DefaultVoice)
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
	t.Setenv("APPDATA", tmpDir)

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

	newPath := filepath.Join(tmpDir, "com.github.mrpoundsign.towncrier", "config.json")

	// Calling Load with non-existent newPath should detect legacy config and migrate to newPath
	migrated, err := Load(newPath)
	if err != nil {
		t.Fatalf("Load with legacy migration failed: %v", err)
	}

	if migrated.GlobalChatRules.DefaultVoice != "af_nicole" {
		t.Errorf("expected migrated voice 'af_nicole', got %q", migrated.GlobalChatRules.DefaultVoice)
	}
	if migrated.Audio.DeviceName != "Migrated Device" {
		t.Errorf("expected migrated device 'Migrated Device', got %q", migrated.Audio.DeviceName)
	}

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

func TestLookupViewerMultiPlatform(t *testing.T) {
	viewers := map[string]ViewerConfig{
		"twitch:mrpoundsign": {
			Platform: "twitch",
			UserID:   "mrpoundsign",
			Accounts: []ViewerAccount{
				{Platform: "twitch", UserID: "mrpoundsign"},
				{Platform: "youtube", UserID: "@MrPoundSignYT"},
				{Platform: "tiktok", UserID: "poundsign_tt"},
			},
			Voice:    "am_adam",
			Speed:    1.25,
			Nickname: "Pound",
		},
	}

	// Match Twitch account
	vc, ok := LookupViewer(viewers, "twitch", "mrpoundsign", "mrpoundsign")
	if !ok || vc.Nickname != "Pound" || vc.Voice != "am_adam" {
		t.Fatalf("expected Twitch match, got %+v (ok=%v)", vc, ok)
	}

	// Match YouTube account with or without leading '@'
	vc, ok = LookupViewer(viewers, "youtube", "UC12345", "@mrpoundsignyt")
	if !ok || vc.Nickname != "Pound" || vc.Speed != 1.25 {
		t.Fatalf("expected YouTube match with @handle, got %+v (ok=%v)", vc, ok)
	}
	vc, ok = LookupViewer(viewers, "youtube", "", "MrPoundSignYT")
	if !ok || vc.Nickname != "Pound" {
		t.Fatalf("expected YouTube match without @ prefix, got %+v (ok=%v)", vc, ok)
	}

	// Match TikTok account
	key, vc, ok := LookupViewerWithKey(viewers, "tiktok", "poundsign_tt", "poundsign_tt")
	if !ok || key != "twitch:mrpoundsign" || vc.Voice != "am_adam" {
		t.Fatalf("expected TikTok match with key 'twitch:mrpoundsign', got key=%q vc=%+v (ok=%v)", key, vc, ok)
	}
}
