package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExportAndParseBackupScrubsSecrets(t *testing.T) {
	loc := TestLocations(t.TempDir())
	backupPath := filepath.Join(filepath.Dir(loc.ConfigPath()), "backup.json")

	cfg := DefaultConfig()
	cfg.WebSocket.AuthToken = "super-secret-ws-token"
	cfg.Translation.APIKey = "legacy-secret-key"
	for i := range cfg.Translation.Providers {
		cfg.Translation.Providers[i].APIKey = "provider-secret-" + cfg.Translation.Providers[i].Provider
	}
	cfg.Viewers["twitch:alice"] = ViewerConfig{
		Platform: "twitch",
		UserID:   "alice",
		Voice:    "kokoro:af_heart",
		Speed:    1.15,
	}
	cfg.Safety.BlockedWords = []string{"badword1", "badword2"}
	cfg.Bots.CustomBots = []string{"mycustombot"}

	if err := WriteBackupFile(backupPath, cfg, "0.0.9"); err != nil {
		t.Fatalf("WriteBackupFile failed: %v", err)
	}

	// Verify original cfg was not mutated
	if cfg.WebSocket.AuthToken != "super-secret-ws-token" {
		t.Fatal("WriteBackupFile mutated source config WebSocket.AuthToken")
	}

	rawBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	rawStr := string(rawBytes)
	if strings.Contains(rawStr, "super-secret-ws-token") ||
		strings.Contains(rawStr, "legacy-secret-key") ||
		strings.Contains(rawStr, "provider-secret-") {
		t.Fatalf("exported backup leaked secret tokens:\n%s", rawStr)
	}

	bundle, err := ReadBackupFile(backupPath)
	if err != nil {
		t.Fatalf("ReadBackupFile failed: %v", err)
	}
	if bundle.Format != BackupFormat {
		t.Errorf("expected format %q, got %q", BackupFormat, bundle.Format)
	}
	if bundle.AppVersion != "0.0.9" {
		t.Errorf("expected app_version '0.0.9', got %q", bundle.AppVersion)
	}
	if bundle.SchemaVersion != CurrentVersion {
		t.Errorf("expected schema_version %d, got %d", CurrentVersion, bundle.SchemaVersion)
	}

	summary := SummarizeBackup(bundle)
	if summary.ViewerCount != 1 || summary.BlockedWordCount != 2 || summary.CustomBotCount != 1 {
		t.Errorf("unexpected summary counts: %+v", summary)
	}
}

func TestParseRawConfigFileAsBackup(t *testing.T) {
	loc := TestLocations(t.TempDir())
	cfg := DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "piper:0"
	cfg.Bots.CustomBots = []string{" streamelements_custom "}

	if err := Save(loc.ConfigPath(), cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	bundle, err := ReadBackupFile(loc.ConfigPath())
	if err != nil {
		t.Fatalf("ReadBackupFile on raw config.json failed: %v", err)
	}
	if bundle.Config.GlobalChatRules.DefaultVoice != "piper:0" {
		t.Errorf("expected voice 'piper:0', got %q", bundle.Config.GlobalChatRules.DefaultVoice)
	}
	if !slices.Contains(bundle.Config.TTS.EnabledModels, "piper") {
		t.Errorf("expected 'piper' model family to be auto-enabled in migrated backup, got %v", bundle.Config.TTS.EnabledModels)
	}
}

func TestApplyBackupSelectiveMergeAndFullRestore(t *testing.T) {
	store := NewMemoryStore(DefaultConfig())
	current, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load failed: %v", err)
	}

	// Setup machine-specific fields and existing items on current PC
	current.Audio.DeviceID = "local-wasapi-id"
	current.Audio.DeviceName = "Local OBS Virtual Cable"
	current.TTS.ModelPath = `C:\LocalModels`
	current.TTS.UseGPU = true
	current.TTS.NumThreads = 8
	current.TTS.Preset = "high_quality"
	current.TTS.AlertSoundPath = `C:\LocalSounds\alert.wav`
	current.Translation.ChimeSoundPath = `C:\LocalSounds\chime.wav`
	current.Logging.DBPath = `C:\LocalLogs\events.db`
	current.WebSocket.AuthToken = "local-ws-secret"
	current.Viewers["twitch:existing_user"] = ViewerConfig{
		Platform: "twitch",
		UserID:   "existing_user",
		Voice:    "supertonic:m1",
		Speed:    1.0,
	}
	current.Safety.BlockedWords = []string{"SpamWord", "SharedWord"}
	current.Bots.CustomBots = []string{"localbot", "sharedbot"}

	// Setup incoming backup from another PC
	incoming := DefaultConfig()
	incoming.Audio.DeviceID = "other-pc-id"
	incoming.Audio.DeviceName = "Other PC Speakers"
	incoming.TTS.ModelPath = `D:\OtherModels`
	incoming.TTS.UseGPU = false
	incoming.TTS.NumThreads = 2
	incoming.TTS.Preset = "fast_lean"
	incoming.TTS.AlertSoundPath = `D:\OtherSounds\alert.wav`
	incoming.Translation.ChimeSoundPath = `D:\OtherSounds\chime.wav`
	incoming.Logging.DBPath = `D:\OtherLogs\events.db`
	incoming.GlobalChatRules.DefaultVoice = "kitten_mini:expr-voice-2-f"
	incoming.GlobalChatRules.DefaultSpeed = 1.25
	incoming.Viewers["youtube:new_viewer"] = ViewerConfig{
		Platform: "youtube",
		UserID:   "new_viewer",
		Voice:    "kokoro:af_bella",
		Speed:    1.1,
	}
	incoming.Safety.BlockedWords = []string{"sharedword", "NewBadWord"}
	incoming.Bots.CustomBots = []string{"@SharedBot", "@RemoteBot"}
	incoming.Logging.LiveLogs = append(DefaultLiveLogPresets(), LiveLogPreset{
		ID:   "custom-vip-tab",
		Name: "VIP Chat",
	})

	exportedBytes, err := ExportBackup(incoming, "0.0.9")
	if err != nil {
		t.Fatalf("ExportBackup failed: %v", err)
	}
	bundle, err := ParseBackup(exportedBytes)
	if err != nil {
		t.Fatalf("ParseBackup failed: %v", err)
	}

	// 1. Test Selective Merge
	opts := DefaultImportOptions()
	merged, err := ApplyBackup(current, bundle, opts)
	if err != nil {
		t.Fatalf("ApplyBackup (selective merge) failed: %v", err)
	}

	// Machine-specific settings must be preserved
	if merged.Audio.DeviceID != "local-wasapi-id" || merged.Audio.DeviceName != "Local OBS Virtual Cable" {
		t.Errorf("expected local audio device preserved, got %+v", merged.Audio)
	}
	if merged.TTS.ModelPath != `C:\LocalModels` || !merged.TTS.UseGPU || merged.TTS.NumThreads != 8 {
		t.Errorf("expected local TTS hardware settings preserved, got %+v", merged.TTS)
	}
	if merged.Translation.ChimeSoundPath != `C:\LocalSounds\chime.wav` {
		t.Errorf("expected local ChimeSoundPath preserved, got %q", merged.Translation.ChimeSoundPath)
	}
	if merged.WebSocket.AuthToken != "local-ws-secret" {
		t.Errorf("expected local WebSocket.AuthToken preserved, got %q", merged.WebSocket.AuthToken)
	}

	// Both existing and incoming viewers must be present
	if _, ok := merged.Viewers["twitch:existing_user"]; !ok {
		t.Error("expected existing viewer 'twitch:existing_user' to be preserved in selective merge")
	}
	if _, ok := merged.Viewers["youtube:new_viewer"]; !ok {
		t.Error("expected incoming viewer 'youtube:new_viewer' to be added in selective merge")
	}

	// Blocked words deduplicated case-insensitively
	if len(merged.Safety.BlockedWords) != 3 {
		t.Errorf("expected 3 deduplicated blocked words, got %v", merged.Safety.BlockedWords)
	}

	// Custom bots deduplicated and normalized to lowercase without '@'
	if !slices.Equal(merged.Bots.CustomBots, []string{"localbot", "sharedbot", "remotebot"}) {
		t.Errorf("expected deduplicated bots [localbot sharedbot remotebot], got %v", merged.Bots.CustomBots)
	}

	// Custom LiveLog preset merged without duplicating built-in presets
	foundVIP := false
	for _, p := range merged.Logging.LiveLogs {
		if p.ID == "custom-vip-tab" {
			foundVIP = true
		}
	}
	if !foundVIP {
		t.Error("expected custom LiveLog preset 'custom-vip-tab' to be merged")
	}

	// Model families required by imported voices must be enabled
	if !slices.Contains(merged.TTS.EnabledModels, "kitten_mini") || !slices.Contains(merged.TTS.EnabledModels, "kokoro") {
		t.Errorf("expected kitten_mini and kokoro models to be enabled after import, got %v", merged.TTS.EnabledModels)
	}

	// 2. Test Full Restore (with PreserveMachinePaths = true)
	fullOpts := DefaultImportOptions()
	fullOpts.Mode = ImportModeFullRestore
	fullOpts.PreserveMachinePaths = true
	restoredKeepPaths, err := ApplyBackup(current, bundle, fullOpts)
	if err != nil {
		t.Fatalf("ApplyBackup (full restore keep paths) failed: %v", err)
	}
	if _, ok := restoredKeepPaths.Viewers["twitch:existing_user"]; ok {
		t.Error("expected full restore to replace Viewers map rather than keeping old viewer")
	}
	if restoredKeepPaths.Audio.DeviceName != "Local OBS Virtual Cable" {
		t.Errorf("expected full restore with PreserveMachinePaths=true to keep local audio device, got %q", restoredKeepPaths.Audio.DeviceName)
	}

	// 3. Test Full Restore (with PreserveMachinePaths = false)
	fullOpts.PreserveMachinePaths = false
	restoredAll, err := ApplyBackup(current, bundle, fullOpts)
	if err != nil {
		t.Fatalf("ApplyBackup (full restore overwrite paths) failed: %v", err)
	}
	if restoredAll.Audio.DeviceName != "Other PC Speakers" {
		t.Errorf("expected full restore with PreserveMachinePaths=false to use backup audio device, got %q", restoredAll.Audio.DeviceName)
	}
}

func TestConfigSchemaPreMigrationBackupAndDowngradeDetection(t *testing.T) {
	loc := TestLocations(t.TempDir())
	configPath := loc.ConfigPath()

	// Write a v1 config file directly to disk
	v1Cfg := DefaultConfig()
	v1Cfg.Version = 1
	v1Cfg.GlobalChatRules.DefaultVoice = "supertonic:f2"
	v1Data, err := json.MarshalIndent(v1Cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal v1 config: %v", err)
	}
	if err := os.WriteFile(configPath, v1Data, 0o600); err != nil {
		t.Fatalf("write v1 config: %v", err)
	}

	// Loading v1 config should create a config.v1.<timestamp>.bak.json and upgrade on disk to CurrentVersion
	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load v1 config failed: %v", err)
	}
	if loaded.Version != CurrentVersion {
		t.Errorf("expected upgraded Version %d, got %d", CurrentVersion, loaded.Version)
	}

	backups, err := ListConfigBackups(configPath)
	if err != nil {
		t.Fatalf("ListConfigBackups failed: %v", err)
	}
	if len(backups) != 1 || backups[0].SchemaVersion != 1 {
		t.Fatalf("expected 1 backup with SchemaVersion=1, got %+v", backups)
	}

	// Now simulate a future version (schema v99) saved on disk and then opening an older binary
	futureCfg := DefaultConfig()
	futureCfg.Version = 99
	futureCfg.GlobalChatRules.DefaultVoice = "kokoro:am_adam"
	futureData, err := json.MarshalIndent(futureCfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal future config: %v", err)
	}
	if err := os.WriteFile(configPath, futureData, 0o600); err != nil {
		t.Fatalf("write future config: %v", err)
	}

	regressedCfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load future config failed: %v", err)
	}
	savedVer, supVer, candidate, regressed := CheckConfigSchemaRegression(regressedCfg, configPath)
	if !regressed {
		t.Fatalf("expected schema regression detected for v99 > v%d", CurrentVersion)
	}
	if savedVer != 99 || supVer != CurrentVersion {
		t.Errorf("expected savedVer=99, supVer=%d, got %d, %d", CurrentVersion, savedVer, supVer)
	}
	if candidate == nil || candidate.SchemaVersion != 1 {
		t.Fatalf("expected candidate backup v1, got %+v", candidate)
	}

	// Restore the v1 backup and verify regression is cleared and original voice restored
	restored, err := RestoreConfigBackup(candidate.Path, configPath)
	if err != nil {
		t.Fatalf("RestoreConfigBackup failed: %v", err)
	}
	if restored.GlobalChatRules.DefaultVoice != "supertonic:f2" {
		t.Errorf("expected restored voice 'supertonic:f2', got %q", restored.GlobalChatRules.DefaultVoice)
	}
	if _, _, _, stillRegressed := CheckConfigSchemaRegression(restored, configPath); stillRegressed {
		t.Error("expected schema regression to be cleared after RestoreConfigBackup")
	}
}

func TestProgressiveLegacyTranslationUpgradeAndBackupMerge(t *testing.T) {
	loc := TestLocations(t.TempDir())
	configPath := loc.ConfigPath()

	// 1. Simulate an existing pre-VoiceSubtitles v2 config.json with custom Translation provider order,
	// Round-Robin routing, Japanese chat target, and custom chime.
	legacyJSON := `{
  "version": 2,
  "translation": {
    "enabled": true,
    "routing_mode": "round_robin",
    "play_chime_before_speak": false,
    "chime_sound_path": "C:\\Sounds\\custom_chime.wav",
    "target_lang": "ja",
    "min_confidence": 0.80,
    "providers": [
      {"provider": "deepl", "enabled": true},
      {"provider": "ollama", "enabled": true, "endpoint_url": "http://127.0.0.1:11434", "model": "qwen2.5:7b"},
      {"provider": "mymemory", "enabled": false, "contact_email": "streamer@example.com"}
    ]
  }
}`
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	upgraded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load legacy config failed: %v", err)
	}

	// Verify 100% of existing Translation settings & provider chain order survived intact
	if !upgraded.Translation.Enabled || upgraded.Translation.RoutingMode != TranslationRoutingRoundRobin {
		t.Fatalf("unexpected upgraded Translation header fields: %+v", upgraded.Translation)
	}
	if upgraded.Translation.TargetLang != "ja" || upgraded.Translation.MinConfidence != 0.80 || upgraded.Translation.PlayChimeBeforeSpeak {
		t.Fatalf("unexpected upgraded Chat Translation settings: %+v", upgraded.Translation)
	}
	if len(upgraded.Translation.Providers) != 5 ||
		upgraded.Translation.Providers[0].Provider != TranslationProviderDeepL ||
		upgraded.Translation.Providers[1].Provider != TranslationProviderOllama ||
		upgraded.Translation.Providers[1].Model != "qwen2.5:7b" {
		t.Fatalf("unexpected upgraded Translation provider chain: %+v", upgraded.Translation.Providers)
	}

	// Verify VoiceSubtitles was progressively populated with safe defaults (disabled by default)
	if upgraded.VoiceSubtitles.Enabled {
		t.Fatal("expected VoiceSubtitles.Enabled=false on progressive upgrade")
	}
	if !slices.Equal(upgraded.VoiceSubtitles.SpokenLanguages, []string{"en", "tl"}) ||
		!slices.Equal(upgraded.VoiceSubtitles.TargetLanguages, []string{"en", "tl", "ja"}) {
		t.Fatalf("unexpected VoiceSubtitles default language lists: %+v", upgraded.VoiceSubtitles)
	}

	// 2. Verify Backup Export/Parse/ApplyBackup carries custom 2-language VoiceSubtitles list while keeping local GPU/threads
	upgraded.VoiceSubtitles.Enabled = true
	upgraded.VoiceSubtitles.TargetLanguages = []string{"tl", "ja"}
	upgraded.VoiceSubtitles.UseGPU = true
	upgraded.VoiceSubtitles.NumThreads = 12
	upgraded.WebSocket.SubtitlesOverlayEnabled = true

	backupBytes, err := ExportBackup(upgraded, "0.0.14")
	if err != nil {
		t.Fatalf("ExportBackup failed: %v", err)
	}
	bundle, err := ParseBackup(backupBytes)
	if err != nil {
		t.Fatalf("ParseBackup failed: %v", err)
	}
	if !slices.Equal(bundle.Config.VoiceSubtitles.TargetLanguages, []string{"tl", "ja"}) {
		t.Fatalf("ParseBackup failed to preserve 2-element TargetLanguages [tl ja], got %v", bundle.Config.VoiceSubtitles.TargetLanguages)
	}

	localCfg := DefaultConfig()
	localCfg.VoiceSubtitles.UseGPU = false
	localCfg.VoiceSubtitles.NumThreads = 4

	merged, err := ApplyBackup(localCfg, bundle, DefaultImportOptions())
	if err != nil {
		t.Fatalf("ApplyBackup failed: %v", err)
	}
	if !merged.VoiceSubtitles.Enabled || !merged.WebSocket.SubtitlesOverlayEnabled {
		t.Fatal("expected Selective Merge with IncludeTranslation=true to import VoiceSubtitles")
	}
	if !slices.Equal(merged.VoiceSubtitles.TargetLanguages, []string{"tl", "ja"}) {
		t.Fatalf("expected merged TargetLanguages [tl ja], got %v", merged.VoiceSubtitles.TargetLanguages)
	}
	if merged.VoiceSubtitles.UseGPU || merged.VoiceSubtitles.NumThreads != 4 {
		t.Fatalf("expected PreserveMachinePaths=true to keep local UseGPU=false & NumThreads=4, got %+v", merged.VoiceSubtitles)
	}
}

