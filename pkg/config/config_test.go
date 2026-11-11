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
	cfg.GlobalChatRules.DeduplicateEmotes = true
	cfg.GlobalChatRules.MaxDuplicateWords = 2
	cfg.GlobalChatRules.DefaultVoice = "af_bella"
	cfg.GlobalChatRules.DefaultSpeed = 1.0

	// Case 1: Platform without overrides inherits global
	names, emotes, dedupe, maxDup, platformName, voice, speed := cfg.EffectiveChatRules("twitch")
	if !names || emotes || !dedupe || maxDup != 2 || platformName || voice != "af_bella" || speed != 1.0 {
		t.Errorf("unexpected global inheritance: names=%v, emotes=%v, dedupe=%v, maxDup=%d, platformName=%v, voice=%s, speed=%f",
			names, emotes, dedupe, maxDup, platformName, voice, speed)
	}

	// Case 2: Platform with legacy OverrideRules enabled
	p := cfg.Platforms["twitch"]
	p.Overrides.OverrideRules = true
	p.Overrides.SpeakNames = false
	p.Overrides.SpeakEmotes = true
	p.Overrides.DeduplicateEmotes = false
	p.Overrides.MaxDuplicateWords = 1
	p.Overrides.DefaultVoice = "am_michael"
	p.Overrides.DefaultSpeed = 1.25
	cfg.Platforms["twitch"] = p

	names, emotes, dedupe, maxDup, platformName, voice, speed = cfg.EffectiveChatRules("twitch")
	if names || !emotes || dedupe || maxDup != 1 || platformName || voice != "am_michael" || speed != 1.25 {
		t.Errorf("unexpected platform override: names=%v, emotes=%v, dedupe=%v, maxDup=%d, platformName=%v, voice=%s, speed=%f",
			names, emotes, dedupe, maxDup, platformName, voice, speed)
	}

	// Case 3: Per-setting overrides (independent without master checkbox)
	yt := cfg.Platforms["youtube"]
	yt.Overrides.SpeakNamesMode = OverrideModeOff
	yt.Overrides.SpeakPlatformNameMode = OverrideModeOn
	yt.Overrides.MaxDuplicateWordsMode = "3"
	yt.Overrides.VoiceOverrideMode = OverrideModeCustom
	yt.Overrides.DefaultVoice = "bf_emma"
	yt.Overrides.DefaultSpeed = 1.1
	cfg.Platforms["youtube"] = yt

	names, emotes, dedupe, maxDup, platformName, voice, speed = cfg.EffectiveChatRules("youtube")
	if names || emotes || !dedupe || maxDup != 3 || !platformName || voice != "bf_emma" || speed != 1.1 {
		t.Errorf("unexpected per-setting override: names=%v, emotes=%v, dedupe=%v, maxDup=%d, platformName=%v, voice=%s, speed=%f",
			names, emotes, dedupe, maxDup, platformName, voice, speed)
	}
}

func TestMigrateLegacyOverrideRulesToPerSettingModes(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	tw := cfg.Platforms["twitch"]
	tw.Overrides.OverrideRules = true
	tw.Overrides.SpeakNames = false
	tw.Overrides.SpeakEmotes = true
	tw.Overrides.DeduplicateEmotes = false
	tw.Overrides.MaxDuplicateWords = -1
	tw.Overrides.SpeakPlatformName = true
	tw.Overrides.IgnoreMentionsMode = IgnoreMentionsAll
	tw.Overrides.AllowBroadcasterMentions = true
	tw.Overrides.PlayAlertOnIgnoredMention = true
	tw.Overrides.DefaultVoice = "am_adam"
	tw.Overrides.DefaultSpeed = 1.2
	cfg.Platforms["twitch"] = tw

	if err := Save(configPath, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	ov := loaded.Platforms["twitch"].Overrides
	if ov.OverrideRules {
		t.Error("expected legacy OverrideRules to be cleared to false after migration")
	}
	if ov.SpeakNamesMode != OverrideModeOff || ov.SpeakEmotesMode != OverrideModeOn || ov.DeduplicateEmotesMode != OverrideModeOff {
		t.Errorf("unexpected migrated bool modes: names=%q, emotes=%q, dedupe=%q", ov.SpeakNamesMode, ov.SpeakEmotesMode, ov.DeduplicateEmotesMode)
	}
	if ov.MaxDuplicateWordsMode != OverrideModeOff {
		t.Errorf("expected MaxDuplicateWordsMode='off', got %q", ov.MaxDuplicateWordsMode)
	}
	if ov.SpeakPlatformNameMode != OverrideModeOn {
		t.Errorf("expected SpeakPlatformNameMode='on', got %q", ov.SpeakPlatformNameMode)
	}
	if ov.IgnoreMentionsOverride != IgnoreMentionsAll || ov.AllowBroadcasterMentionsMode != OverrideModeOn || ov.PlayAlertOnIgnoredMentionMode != OverrideModeOn {
		t.Errorf("unexpected migrated mention modes: mode=%q, broadcaster=%q, alert=%q",
			ov.IgnoreMentionsOverride, ov.AllowBroadcasterMentionsMode, ov.PlayAlertOnIgnoredMentionMode)
	}
	if ov.VoiceOverrideMode != OverrideModeCustom {
		t.Errorf("expected VoiceOverrideMode='custom', got %q", ov.VoiceOverrideMode)
	}

	// Verify unoverridden platform (youtube) kept all modes as global
	ytOv := loaded.Platforms["youtube"].Overrides
	if NormalizeBoolOverrideMode(ytOv.SpeakNamesMode) != OverrideModeGlobal || NormalizeVoiceOverrideMode(ytOv.VoiceOverrideMode) != OverrideModeGlobal {
		t.Errorf("expected unoverridden youtube to stay global, got %+v", ytOv)
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

func TestEffectiveMentionRules(t *testing.T) {
	cfg := DefaultConfig()

	// Default is "off", false, false
	mode, allowBroadcaster, playAlert := cfg.EffectiveMentionRules("twitch")
	if mode != IgnoreMentionsOff || allowBroadcaster || playAlert {
		t.Fatalf("expected default ('off', false, false), got (%q, %v, %v)", mode, allowBroadcaster, playAlert)
	}

	// Set global rules
	cfg.GlobalChatRules.IgnoreMentionsMode = IgnoreMentionsRepliesAndLeading
	cfg.GlobalChatRules.AllowBroadcasterMentions = true
	cfg.GlobalChatRules.PlayAlertOnIgnoredMention = true

	mode, allowBroadcaster, playAlert = cfg.EffectiveMentionRules("twitch")
	if mode != IgnoreMentionsRepliesAndLeading || !allowBroadcaster || !playAlert {
		t.Fatalf("expected inherited ('replies_and_leading', true, true), got (%q, %v, %v)", mode, allowBroadcaster, playAlert)
	}

	// Enable platform override on twitch
	p := cfg.Platforms["twitch"]
	p.Overrides.OverrideRules = true
	p.Overrides.IgnoreMentionsMode = IgnoreMentionsAll
	p.Overrides.AllowBroadcasterMentions = false
	p.Overrides.PlayAlertOnIgnoredMention = false
	cfg.Platforms["twitch"] = p

	mode, allowBroadcaster, playAlert = cfg.EffectiveMentionRules("twitch")
	if mode != IgnoreMentionsAll || allowBroadcaster || playAlert {
		t.Fatalf("expected twitch override ('all', false, false), got (%q, %v, %v)", mode, allowBroadcaster, playAlert)
	}

	// YouTube still inherits global
	mode, allowBroadcaster, playAlert = cfg.EffectiveMentionRules("youtube")
	if mode != IgnoreMentionsRepliesAndLeading || !allowBroadcaster || !playAlert {
		t.Fatalf("expected youtube inherited ('replies_and_leading', true, true), got (%q, %v, %v)", mode, allowBroadcaster, playAlert)
	}
}

func TestTranslationConfigAndOverrides(t *testing.T) {
	store := NewMemoryStore(nil)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Translation.Enabled {
		t.Fatalf("expected default Translation.Enabled=false")
	}
	if cfg.Translation.Provider != TranslationProviderMyMemory {
		t.Fatalf("expected default Translation.Provider=%q, got %q", TranslationProviderMyMemory, cfg.Translation.Provider)
	}
	if cfg.Translation.TargetLang != DefaultTranslationTargetLang {
		t.Fatalf("expected default Translation.TargetLang=%q, got %q", DefaultTranslationTargetLang, cfg.Translation.TargetLang)
	}
	if NormalizeTranslationTargetLang("tl") != "tl" {
		t.Fatalf("expected NormalizeTranslationTargetLang('tl')='tl', got %q", NormalizeTranslationTargetLang("tl"))
	}
	if NormalizeTranslationProvider("deepl") != TranslationProviderDeepL {
		t.Fatalf("expected NormalizeTranslationProvider('deepl')=%q", TranslationProviderDeepL)
	}
	if NormalizeTranslationProvider("google") != TranslationProviderGoogle {
		t.Fatalf("expected NormalizeTranslationProvider('google')=%q", TranslationProviderGoogle)
	}
	if NormalizeTranslationRoutingMode("round_robin") != TranslationRoutingRoundRobin {
		t.Fatalf("expected NormalizeTranslationRoutingMode('round_robin')=%q", TranslationRoutingRoundRobin)
	}
	if NormalizeTranslationRoutingMode("invalid") != TranslationRoutingPriority {
		t.Fatalf("expected NormalizeTranslationRoutingMode('invalid')=%q", TranslationRoutingPriority)
	}
	if !cfg.Translation.PlayChimeBeforeSpeak {
		t.Fatalf("expected default PlayChimeBeforeSpeak=true")
	}
	if len(cfg.Translation.Providers) != len(AllTranslationProviders) {
		t.Fatalf("expected %d providers by default, got %d", len(AllTranslationProviders), len(cfg.Translation.Providers))
	}
	if cfg.Translation.Providers[0].Provider != TranslationProviderMyMemory || !cfg.Translation.Providers[0].Enabled {
		t.Fatalf("expected MyMemory enabled at #1 by default, got %+v", cfg.Translation.Providers[0])
	}

	// Verify legacy single-provider migration populates Providers with legacy primary #1 and MyMemory #2
	legacy := TranslationConfig{
		Enabled:      true,
		Provider:     TranslationProviderOllama,
		EndpointURL:  "http://127.0.0.1:11434",
		Model:        "llama3.2",
		ContactEmail: "test@example.com",
	}
	NormalizeTranslationProviders(&legacy)
	if len(legacy.Providers) != len(AllTranslationProviders) {
		t.Fatalf("expected %d providers after migration, got %d", len(AllTranslationProviders), len(legacy.Providers))
	}
	if legacy.Providers[0].Provider != TranslationProviderOllama || !legacy.Providers[0].Enabled || legacy.Providers[0].Model != "llama3.2" {
		t.Fatalf("expected migrated #1 to be Ollama enabled with model llama3.2, got %+v", legacy.Providers[0])
	}
	if legacy.Providers[1].Provider != TranslationProviderMyMemory || !legacy.Providers[1].Enabled || legacy.Providers[1].ContactEmail != "test@example.com" {
		t.Fatalf("expected migrated #2 to be MyMemory enabled with ContactEmail, got %+v", legacy.Providers[1])
	}

	if cfg.EffectiveTranslationEnabled("twitch") {
		t.Fatalf("expected EffectiveTranslationEnabled('twitch')=false by default")
	}

	// Enable globally
	cfg.Translation.Enabled = true
	if !cfg.EffectiveTranslationEnabled("twitch") {
		t.Fatalf("expected EffectiveTranslationEnabled('twitch')=true when enabled globally")
	}

	// Override off on twitch, on on youtube
	pTw := cfg.Platforms["twitch"]
	pTw.Overrides.TranslateMode = OverrideModeOff
	cfg.Platforms["twitch"] = pTw

	pYt := cfg.Platforms["youtube"]
	pYt.Overrides.TranslateMode = OverrideModeOn
	cfg.Platforms["youtube"] = pYt

	if cfg.EffectiveTranslationEnabled("twitch") {
		t.Fatalf("expected EffectiveTranslationEnabled('twitch')=false when overridden off")
	}
	cfg.Translation.Enabled = false
	if !cfg.EffectiveTranslationEnabled("youtube") {
		t.Fatalf("expected EffectiveTranslationEnabled('youtube')=true when overridden on")
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
}

func TestEffectiveCoalesceSecondsAndOverrides(t *testing.T) {
	store := NewMemoryStore(nil)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.GlobalChatRules.CoalesceConsecutiveSeconds != DefaultCoalesceConsecutiveSeconds {
		t.Fatalf("expected default CoalesceConsecutiveSeconds=%d, got %d", DefaultCoalesceConsecutiveSeconds, cfg.GlobalChatRules.CoalesceConsecutiveSeconds)
	}
	if got := cfg.EffectiveCoalesceSeconds("twitch"); got != 20 {
		t.Fatalf("expected EffectiveCoalesceSeconds('twitch')=20 by default, got %d", got)
	}

	// Global Off (-1) disables coalescing
	cfg.GlobalChatRules.CoalesceConsecutiveSeconds = -1
	if got := cfg.EffectiveCoalesceSeconds("twitch"); got != 0 {
		t.Fatalf("expected EffectiveCoalesceSeconds('twitch')=0 when global is -1, got %d", got)
	}

	// Per-platform override via CoalesceConsecutiveMode
	pTw := cfg.Platforms["twitch"]
	pTw.Overrides.CoalesceConsecutiveMode = "30"
	cfg.Platforms["twitch"] = pTw
	if got := cfg.EffectiveCoalesceSeconds("twitch"); got != 30 {
		t.Fatalf("expected EffectiveCoalesceSeconds('twitch')=30 when overridden to '30', got %d", got)
	}

	// Per-platform override to 'off' when global is 15s
	cfg.GlobalChatRules.CoalesceConsecutiveSeconds = 15
	pYt := cfg.Platforms["youtube"]
	pYt.Overrides.CoalesceConsecutiveMode = OverrideModeOff
	cfg.Platforms["youtube"] = pYt
	if got := cfg.EffectiveCoalesceSeconds("youtube"); got != 0 {
		t.Fatalf("expected EffectiveCoalesceSeconds('youtube')=0 when overridden to off, got %d", got)
	}
	if got := cfg.EffectiveCoalesceSeconds("kick"); got != 15 {
		t.Fatalf("expected EffectiveCoalesceSeconds('kick')=15 inheriting global, got %d", got)
	}

	// SpeakNames disabled on platform forces EffectiveCoalesceSeconds to 0
	pKick := cfg.Platforms["kick"]
	pKick.Overrides.SpeakNamesMode = OverrideModeOff
	cfg.Platforms["kick"] = pKick
	if got := cfg.EffectiveCoalesceSeconds("kick"); got != 0 {
		t.Fatalf("expected EffectiveCoalesceSeconds('kick')=0 when SpeakNames is off, got %d", got)
	}
}

func TestEffectiveNaturalizeNames(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.EffectiveNaturalizeNames("twitch") {
		t.Fatal("expected EffectiveNaturalizeNames('twitch')=true by default")
	}

	// Global off
	cfg.GlobalChatRules.NaturalizeNames = false
	if cfg.EffectiveNaturalizeNames("twitch") {
		t.Fatal("expected EffectiveNaturalizeNames('twitch')=false when global is false")
	}

	// Platform override On
	pTw := cfg.Platforms["twitch"]
	pTw.Overrides.NaturalizeNamesMode = OverrideModeOn
	cfg.Platforms["twitch"] = pTw
	if !cfg.EffectiveNaturalizeNames("twitch") {
		t.Fatal("expected EffectiveNaturalizeNames('twitch')=true when overridden to On")
	}

	// Platform override Off when global is On
	cfg.GlobalChatRules.NaturalizeNames = true
	pYt := cfg.Platforms["youtube"]
	pYt.Overrides.NaturalizeNamesMode = OverrideModeOff
	cfg.Platforms["youtube"] = pYt
	if cfg.EffectiveNaturalizeNames("youtube") {
		t.Fatal("expected EffectiveNaturalizeNames('youtube')=false when overridden to Off")
	}
}

func TestEffectiveAntiSpamRulesAndOverrides(t *testing.T) {
	store := NewMemoryStore(nil)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	zalgo, ascii, windowSec := cfg.EffectiveAntiSpamRules("twitch")
	if !zalgo || !ascii || windowSec != DefaultCopypastaWindowSeconds {
		t.Fatalf("expected default (true, true, %d), got (%v, %v, %d)", DefaultCopypastaWindowSeconds, zalgo, ascii, windowSec)
	}

	// Override Twitch: disable Zalgo & Copypasta, keep ASCII Art
	pTw := cfg.Platforms["twitch"]
	pTw.Overrides.StripZalgoMode = OverrideModeOff
	pTw.Overrides.DeduplicateCopypastaMode = OverrideModeOff
	cfg.Platforms["twitch"] = pTw

	zalgo, ascii, windowSec = cfg.EffectiveAntiSpamRules("twitch")
	if zalgo || !ascii || windowSec != 0 {
		t.Fatalf("expected twitch override (false, true, 0), got (%v, %v, %d)", zalgo, ascii, windowSec)
	}

	// Override YouTube to 60s window
	pYt := cfg.Platforms["youtube"]
	pYt.Overrides.DeduplicateCopypastaMode = "60"
	cfg.Platforms["youtube"] = pYt

	_, _, ytWin := cfg.EffectiveAntiSpamRules("youtube")
	if ytWin != 60 {
		t.Fatalf("expected youtube override window 60, got %d", ytWin)
	}
}


