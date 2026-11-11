package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	CurrentVersion   = 2
	maxConfigBackups = 5
)

// Config represents the application configuration hierarchy.
type Config struct {
	Version             int                       `json:"version"`
	LoadedSchemaVersion int                       `json:"-"`
	UIScale             string                    `json:"ui_scale,omitempty"`
	Update          UpdateConfig              `json:"update"`
	Audio           AudioConfig               `json:"audio"`
	TTS             TTSConfig                 `json:"tts"`
	Safety          SafetyConfig              `json:"safety"`
	Queue           QueueConfig               `json:"queue"`
	Bots            BotConfig                 `json:"bots"`
	Logging         LoggingConfig             `json:"logging"`
	WebSocket       WebSocketConfig           `json:"websocket"`
	Translation     TranslationConfig         `json:"translation"`
	GlobalChatRules GlobalChatRules           `json:"global_chat_rules"`
	Viewers         map[string]ViewerConfig   `json:"viewers"`
	Platforms       map[string]PlatformConfig `json:"platforms"`
}

const (
	TranslationProviderMyMemory       = "mymemory"
	TranslationProviderGoogle         = "google"
	TranslationProviderDeepL          = "deepl"
	TranslationProviderLibreTranslate = "libretranslate"
	TranslationProviderOllama         = "ollama"

	TranslationRoutingPriority   = "priority"
	TranslationRoutingRoundRobin = "round_robin"

	DefaultTranslationTargetLang    = "en"
	DefaultTranslationMinConfidence = 0.65
	DefaultLibreTranslateURL        = "http://localhost:5000"
	DefaultOllamaURL                = "http://localhost:11434"
	DefaultOllamaModel              = "qwen2.5:3b"
)

// AllTranslationProviders is the canonical ordered list of all supported translation provider IDs.
var AllTranslationProviders = []string{
	TranslationProviderMyMemory,
	TranslationProviderGoogle,
	TranslationProviderDeepL,
	TranslationProviderLibreTranslate,
	TranslationProviderOllama,
}

// TranslationProviderEntry configures a single translation provider node in the fallback/load-balancing chain.
type TranslationProviderEntry struct {
	Provider     string `json:"provider"`
	Enabled      bool   `json:"enabled"`
	ContactEmail string `json:"contact_email,omitempty"`
	EndpointURL  string `json:"endpoint_url,omitempty"`
	Model        string `json:"model,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
}

// TranslationConfig configures automatic language detection and foreign chat translation.
type TranslationConfig struct {
	Enabled              bool                       `json:"enabled"`
	RoutingMode          string                     `json:"routing_mode,omitempty"`
	PlayChimeBeforeSpeak bool                       `json:"play_chime_before_speak"`
	ChimeSoundPath       string                     `json:"chime_sound_path,omitempty"`
	Providers            []TranslationProviderEntry `json:"providers,omitempty"`
	Provider             string                     `json:"provider"`
	TargetLang           string                     `json:"target_lang"`
	MinConfidence        float64                    `json:"min_confidence"`
	ContactEmail         string                     `json:"contact_email,omitempty"`
	EndpointURL          string                     `json:"endpoint_url,omitempty"`
	Model                string                     `json:"model,omitempty"`
	APIKey               string                     `json:"api_key,omitempty"`
}

// NormalizeTranslationRoutingMode returns a canonical routing mode ("priority" or "round_robin").
func NormalizeTranslationRoutingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case TranslationRoutingRoundRobin, "rr", "round-robin", "roundrobin":
		return TranslationRoutingRoundRobin
	default:
		return TranslationRoutingPriority
	}
}

// DefaultTranslationProviders returns the default 5-node translation provider list with MyMemory enabled first.
func DefaultTranslationProviders() []TranslationProviderEntry {
	return []TranslationProviderEntry{
		{
			Provider: TranslationProviderMyMemory,
			Enabled:  true,
		},
		{
			Provider: TranslationProviderGoogle,
			Enabled:  false,
		},
		{
			Provider: TranslationProviderDeepL,
			Enabled:  false,
		},
		{
			Provider:    TranslationProviderLibreTranslate,
			Enabled:     false,
			EndpointURL: DefaultLibreTranslateURL,
		},
		{
			Provider:    TranslationProviderOllama,
			Enabled:     false,
			EndpointURL: DefaultOllamaURL,
			Model:       DefaultOllamaModel,
		},
	}
}

func isUntouchedDefaultProviders(providers []TranslationProviderEntry) bool {
	defs := DefaultTranslationProviders()
	if len(providers) != len(defs) {
		return false
	}
	for i := range defs {
		if providers[i] != defs[i] {
			return false
		}
	}
	return true
}

// NormalizeTranslationProviders ensures t.Providers contains all 5 canonical providers without duplicates,
// migrating any legacy single-provider settings when t.Providers is empty or untouched from defaults.
func NormalizeTranslationProviders(t *TranslationConfig) {
	if t == nil {
		return
	}
	t.RoutingMode = NormalizeTranslationRoutingMode(t.RoutingMode)
	t.Provider = NormalizeTranslationProvider(t.Provider)
	t.TargetLang = NormalizeTranslationTargetLang(t.TargetLang)
	t.MinConfidence = NormalizeTranslationMinConfidence(t.MinConfidence)

	legacyEmpty := len(t.Providers) == 0 || (isUntouchedDefaultProviders(t.Providers) && t.Provider != TranslationProviderMyMemory)
	var out []TranslationProviderEntry
	seen := make(map[string]bool, len(AllTranslationProviders))

	if legacyEmpty {
		legacyPrimary := NormalizeTranslationProvider(t.Provider)
		if legacyPrimary != TranslationProviderMyMemory {
			entry := TranslationProviderEntry{
				Provider: legacyPrimary,
				Enabled:  true,
				APIKey:   strings.TrimSpace(t.APIKey),
			}
			switch legacyPrimary {
			case TranslationProviderLibreTranslate:
				entry.EndpointURL = strings.TrimSpace(t.EndpointURL)
			case TranslationProviderOllama:
				entry.EndpointURL = strings.TrimSpace(t.EndpointURL)
				entry.Model = strings.TrimSpace(t.Model)
			}
			out = append(out, entry)
			seen[legacyPrimary] = true
		}
		mmEntry := TranslationProviderEntry{
			Provider:     TranslationProviderMyMemory,
			Enabled:      true,
			ContactEmail: strings.TrimSpace(t.ContactEmail),
		}
		out = append(out, mmEntry)
		seen[TranslationProviderMyMemory] = true
	} else {
		for _, entry := range t.Providers {
			p := NormalizeTranslationProvider(entry.Provider)
			if seen[p] {
				continue
			}
			seen[p] = true
			entry.Provider = p
			out = append(out, entry)
		}
	}

	for _, def := range DefaultTranslationProviders() {
		if !seen[def.Provider] {
			seen[def.Provider] = true
			out = append(out, def)
		}
	}

	for i := range out {
		switch out[i].Provider {
		case TranslationProviderMyMemory:
			if strings.TrimSpace(out[i].ContactEmail) == "" && strings.TrimSpace(t.ContactEmail) != "" {
				out[i].ContactEmail = strings.TrimSpace(t.ContactEmail)
			}
		case TranslationProviderLibreTranslate:
			if strings.TrimSpace(out[i].EndpointURL) == "" {
				out[i].EndpointURL = DefaultLibreTranslateURL
			}
		case TranslationProviderOllama:
			if strings.TrimSpace(out[i].EndpointURL) == "" {
				out[i].EndpointURL = DefaultOllamaURL
			}
			if strings.TrimSpace(out[i].Model) == "" {
				out[i].Model = DefaultOllamaModel
			}
		}
	}

	t.Providers = out
	if len(t.Providers) > 0 {
		for _, p := range t.Providers {
			if p.Enabled {
				t.Provider = p.Provider
				break
			}
		}
	}
}

// NormalizeTranslationProvider returns a canonical translation provider ("mymemory", "google", "deepl", "libretranslate", or "ollama").
func NormalizeTranslationProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case TranslationProviderGoogle, "google_cloud":
		return TranslationProviderGoogle
	case TranslationProviderDeepL:
		return TranslationProviderDeepL
	case TranslationProviderLibreTranslate, "libre":
		return TranslationProviderLibreTranslate
	case TranslationProviderOllama, "llm", "openai":
		return TranslationProviderOllama
	default:
		return TranslationProviderMyMemory
	}
}

// NormalizeTranslationTargetLang returns a canonical lowercased ISO-639-1 language code, defaulting to "en".
func NormalizeTranslationTargetLang(lang string) string {
	clean := strings.ToLower(strings.TrimSpace(lang))
	switch clean {
	case "en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "ru", "hi", "tl":
		return clean
	default:
		return DefaultTranslationTargetLang
	}
}

// NormalizeTranslationMinConfidence clamps confidence to [0.1, 0.99], defaulting to 0.65 when <= 0.
func NormalizeTranslationMinConfidence(c float64) float64 {
	if c <= 0 {
		return DefaultTranslationMinConfidence
	}
	if c < 0.10 {
		return 0.10
	}
	if c > 0.99 {
		return 0.99
	}
	return c
}

// UpdateConfig configures application version checking and self-update preferences.
type UpdateConfig struct {
	CheckOnStartup bool   `json:"check_on_startup"`
	SkippedVersion string `json:"skipped_version,omitempty"`
}

const (
	DefaultWebSocketHost = "127.0.0.1"
	DefaultWebSocketPort = 8765
)

// WebSocketConfig configures the local dual-mode (Protobuf + JSON) WebSocket and HTTP REST control server.
type WebSocketConfig struct {
	Enabled   bool   `json:"enabled"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	AuthToken string `json:"auth_token,omitempty"`
}

type LoggingConfig struct {
	Enabled                    bool            `json:"enabled"`
	Backend                    string          `json:"backend"`
	DBPath                     string          `json:"db_path,omitempty"`
	ChatRetentionDays          int             `json:"chat_retention_days"`
	EventRetentionDays         int             `json:"event_retention_days"`
	ChatScale                  int             `json:"chat_scale,omitempty"`
	SessionResumeWindowMinutes int             `json:"session_resume_window_minutes,omitempty"`
	LiveLogs                   []LiveLogPreset `json:"live_logs,omitempty"`
}

// LiveLogPreset represents a saved Live Log filter card that can be opened as a zero-chrome pop-out window.
type LiveLogPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SessionMode string `json:"session_mode"` // "latest" or "all"
	HideBots    bool   `json:"hide_bots"`
	ShowSummary bool   `json:"show_summary"`
	Platform    string `json:"platform,omitempty"`
	EventType   string `json:"event_type,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	UserQuery   string `json:"user_query,omitempty"`
	TextQuery   string `json:"text_query,omitempty"`
	ChatScale   int    `json:"chat_scale,omitempty"` // 0 = inherit LoggingConfig.ChatScale, or 50..300
}

// DefaultLiveLogPresets returns the two default saved Live Log cards: "Latest Session" and "All Events".
func DefaultLiveLogPresets() []LiveLogPreset {
	return []LiveLogPreset{
		{
			ID:          "default-latest-session",
			Name:        "Latest Session",
			SessionMode: "latest",
			HideBots:    true,
			ShowSummary: true,
			Platform:    "All Platforms",
			EventType:   "All Types",
			Outcome:     "All Events",
		},
		{
			ID:          "default-all-events",
			Name:        "All Events",
			SessionMode: "all",
			HideBots:    true,
			ShowSummary: true,
			Platform:    "All Platforms",
			EventType:   "All Types",
			Outcome:     "All Events",
		},
	}
}

const (
	MicGateModeOff   = "off"
	MicGateModeDuck  = "duck"
	MicGateModePause = "pause"

	DefaultMicThresholdDB float32 = -35.0
	DefaultMicDuckLevel   float32 = 0.20
	DefaultMicReleaseMs   int     = 600
)

// NormalizeMicGateMode returns a canonical MicGateMode ("off", "duck", or "pause").
func NormalizeMicGateMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case MicGateModeDuck, "duck_volume", "attenuate":
		return MicGateModeDuck
	case MicGateModePause, "pause_speech", "hold":
		return MicGateModePause
	default:
		return MicGateModeOff
	}
}

type AudioConfig struct {
	DeviceID       string  `json:"device_id"`
	DeviceName     string  `json:"device_name"`
	Volume         float32 `json:"volume"`                     // 0.0 to 1.0
	MicGateMode    string  `json:"mic_gate_mode,omitempty"`    // "off", "duck", "pause"
	MicDeviceID    string  `json:"mic_device_id,omitempty"`    // capture device ID hex (empty = system default)
	MicDeviceName  string  `json:"mic_device_name,omitempty"`  // capture device display name
	MicThresholdDB float32 `json:"mic_threshold_db,omitempty"` // RMS activation threshold in dBFS (default -35.0)
	MicDuckLevel   float32 `json:"mic_duck_level,omitempty"`   // ducked volume multiplier 0.05..0.80 (default 0.20)
	MicReleaseMs   int     `json:"mic_release_ms,omitempty"`   // release hold delay in ms (default 600)
}

// TTS operational output modes while the TTS engine remains warm in memory.
const (
	TTSModeAlwaysOn  = "always_on"
	TTSModeAutoIdle  = "auto_idle"
	TTSModeAlwaysOff = "always_off"

	DefaultIdleTimeoutSeconds = 15
	DefaultModeToggleHotkey   = "Ctrl+Shift+T"
	DefaultAlertCooldownMs    = 1500
)

// NormalizeTTSOutputMode returns a valid canonical TTSOutputMode, defaulting to TTSModeAlwaysOn.
func NormalizeTTSOutputMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case TTSModeAutoIdle, "auto", "idle", "afk":
		return TTSModeAutoIdle
	case TTSModeAlwaysOff, "off", "disabled", "muted":
		return TTSModeAlwaysOff
	default:
		return TTSModeAlwaysOn
	}
}

// NextTTSOutputMode cycles through Always On -> Auto (AFK Idle) -> Always Off -> Always On.
func NextTTSOutputMode(current string) string {
	switch NormalizeTTSOutputMode(current) {
	case TTSModeAlwaysOn:
		return TTSModeAutoIdle
	case TTSModeAutoIdle:
		return TTSModeAlwaysOff
	default:
		return TTSModeAlwaysOn
	}
}

type TTSConfig struct {
	Enabled               bool     `json:"enabled"`
	OutputMode            string   `json:"output_mode,omitempty"`          // "always_on", "auto_idle", "always_off"
	IdleTimeoutSeconds    int      `json:"idle_timeout_seconds,omitempty"` // seconds of keyboard/mouse inactivity before enabling TTS in auto_idle mode
	PlayAlertWhenDisabled bool     `json:"play_alert_when_disabled"`       // play chime/WAV/MP3 alert when TTS output is inactive
	AlertSoundPath        string   `json:"alert_sound_path,omitempty"`     // optional custom .wav or .mp3 file path (empty = built-in chime)
	AlertCooldownMs       int      `json:"alert_cooldown_ms,omitempty"`    // minimum interval between alert sounds in ms (default 1500)
	Preset                string   `json:"preset,omitempty"`               // "ultra_low", "fast_lean", "balanced", "beefy", "custom"
	EnabledModels      []string `json:"enabled_models,omitempty"`       // ["kokoro", "supertonic", "kitten_mini", "kitten_nano", "piper"]
	ModelFamily        string   `json:"model_family"`                   // "kokoro"
	ModelPath          string   `json:"model_path"`
	NumThreads         int      `json:"num_threads"`
	UseGPU             bool     `json:"use_gpu"`
	MaxClauseWords     int      `json:"max_clause_words,omitempty"`
	IdleUnloadMinutes  int      `json:"idle_unload_minutes,omitempty"`
}

// EffectiveEnabledModels returns the non-empty list of enabled TTS model families.
func (t TTSConfig) EffectiveEnabledModels() []string {
	if len(t.EnabledModels) > 0 {
		out := make([]string, 0, len(t.EnabledModels))
		seen := make(map[string]struct{}, len(t.EnabledModels))
		for _, m := range t.EnabledModels {
			m = strings.ToLower(strings.TrimSpace(m))
			if m == "" {
				continue
			}
			if _, ok := seen[m]; !ok {
				seen[m] = struct{}{}
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	fam := strings.ToLower(strings.TrimSpace(t.ModelFamily))
	if fam == "" {
		fam = "kokoro"
	}
	return []string{fam}
}

type SafetyConfig struct {
	PanicHotkey          string   `json:"panic_hotkey"`
	ModeToggleHotkey     string   `json:"mode_toggle_hotkey,omitempty"`
	BlockDefaultBadWords bool     `json:"block_default_bad_words"`
	BlockedWords         []string `json:"blocked_words"`
}

type QueueConfig struct {
	MaxAgeSeconds    int     `json:"max_age_seconds"`
	CatchupThreshold int     `json:"catchup_threshold"`
	CatchupSpeed     float32 `json:"catchup_speed"`
}

type BotConfig struct {
	IgnoreKnownBots bool     `json:"ignore_known_bots"`
	AutoUpdate      bool     `json:"auto_update"`
	CustomBots      []string `json:"custom_bots,omitempty"`
}

const (
	IgnoreMentionsOff               = "off"
	IgnoreMentionsRepliesAndLeading = "replies_and_leading"
	IgnoreMentionsAll               = "all"

	UnknownGenderBalanced        = "balanced"
	UnknownGenderPreferFeminine  = "prefer_feminine"
	UnknownGenderPreferMasculine = "prefer_masculine"
	UnknownGenderDefaultVoice    = "default_voice"

	DefaultCoalesceConsecutiveSeconds = 20
	DefaultCopypastaWindowSeconds     = 30
)

// NormalizeUnknownGenderPolicy returns a canonical UnknownGenderPolicy ("balanced", "prefer_feminine", "prefer_masculine", or "default_voice").
func NormalizeUnknownGenderPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case UnknownGenderPreferFeminine, "feminine", "female", "f":
		return UnknownGenderPreferFeminine
	case UnknownGenderPreferMasculine, "masculine", "male", "m":
		return UnknownGenderPreferMasculine
	case UnknownGenderDefaultVoice, "default":
		return UnknownGenderDefaultVoice
	default:
		return UnknownGenderBalanced
	}
}

// NormalizeIgnoreMentionsMode returns a canonical IgnoreMentionsMode ("off", "replies_and_leading", or "all").
func NormalizeIgnoreMentionsMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case IgnoreMentionsRepliesAndLeading, "replies", "leading":
		return IgnoreMentionsRepliesAndLeading
	case IgnoreMentionsAll, "any":
		return IgnoreMentionsAll
	default:
		return IgnoreMentionsOff
	}
}

type GlobalChatRules struct {
	SpeakNames                        bool     `json:"speak_names"`
	NaturalizeNames                   bool     `json:"naturalize_names"`
	CoalesceConsecutiveSeconds        int      `json:"coalesce_consecutive_seconds,omitempty"`
	SpeakEmotes                       bool     `json:"speak_emotes"`
	DeduplicateEmotes                 bool     `json:"deduplicate_emotes"`
	MaxDuplicateWords                 int      `json:"max_duplicate_words"`
	StripZalgo                        bool     `json:"strip_zalgo"`
	StripASCIIArt                     bool     `json:"strip_ascii_art"`
	DeduplicateCopypastaWindowSeconds int      `json:"deduplicate_copypasta_window_seconds,omitempty"`
	SpeakPlatformName                 bool     `json:"speak_platform_name"`
	IgnoreMentionsMode                string   `json:"ignore_mentions_mode,omitempty"`
	AllowBroadcasterMentions          bool     `json:"allow_broadcaster_mentions,omitempty"`
	PlayAlertOnIgnoredMention         bool     `json:"play_alert_on_ignored_mention,omitempty"`
	HashChatterVoices                 bool     `json:"hash_chatter_voices,omitempty"`
	SmartGenderVoices                 bool     `json:"smart_gender_voices,omitempty"`
	UnknownGenderPolicy               string   `json:"unknown_gender_policy,omitempty"`
	DisabledChatterVoices             []string `json:"disabled_chatter_voices,omitempty"`
	DefaultVoice                      string   `json:"default_voice"`
	DefaultSpeed                      float32  `json:"default_speed"`
}

const (
	OverrideModeGlobal = "global"
	OverrideModeOn     = "on"
	OverrideModeOff    = "off"
	OverrideModeCustom = "custom"
)

// NormalizeBoolOverrideMode returns "global", "on", or "off".
func NormalizeBoolOverrideMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case OverrideModeOn, "true", "yes", "1":
		return OverrideModeOn
	case OverrideModeOff, "false", "no", "0":
		return OverrideModeOff
	default:
		return OverrideModeGlobal
	}
}

// BoolToOverrideMode converts a boolean to "on" or "off".
func BoolToOverrideMode(b bool) string {
	if b {
		return OverrideModeOn
	}
	return OverrideModeOff
}

// NormalizeDuplicateWordsOverrideMode returns "global", "off", "1", "2", or "3".
func NormalizeDuplicateWordsOverrideMode(mode string) string {
	clean := strings.ToLower(strings.TrimSpace(mode))
	switch clean {
	case OverrideModeOff, "-1", "unlimited":
		return OverrideModeOff
	case "1", "2", "3":
		return clean
	default:
		if n, err := strconv.Atoi(clean); err == nil && n > 0 {
			return strconv.Itoa(n)
		}
		return OverrideModeGlobal
	}
}

// DuplicateWordsToOverrideMode converts a MaxDuplicateWords integer into its explicit override mode string.
func DuplicateWordsToOverrideMode(val int) string {
	if val < 0 {
		return OverrideModeOff
	}
	if val == 0 {
		return "2"
	}
	return strconv.Itoa(val)
}

// NormalizeCoalesceOverrideMode returns "global", "off", or a positive seconds string (e.g. "10", "15", "20", "30", "60").
func NormalizeCoalesceOverrideMode(mode string) string {
	clean := strings.ToLower(strings.TrimSpace(mode))
	clean = strings.TrimSuffix(clean, "s")
	switch clean {
	case OverrideModeOff, "-1", "0", "false", "no", "disabled":
		return OverrideModeOff
	default:
		if n, err := strconv.Atoi(clean); err == nil && n > 0 {
			return strconv.Itoa(n)
		}
		return OverrideModeGlobal
	}
}

// CoalesceSecondsToOverrideMode converts a CoalesceConsecutiveSeconds integer into its explicit override mode string.
func CoalesceSecondsToOverrideMode(val int) string {
	if val < 0 {
		return OverrideModeOff
	}
	if val == 0 {
		return strconv.Itoa(DefaultCoalesceConsecutiveSeconds)
	}
	return strconv.Itoa(val)
}

// NormalizeIgnoreMentionsOverrideMode returns "global", "off", "replies_and_leading", or "all".
func NormalizeIgnoreMentionsOverrideMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case IgnoreMentionsOff:
		return IgnoreMentionsOff
	case IgnoreMentionsRepliesAndLeading, "replies", "leading":
		return IgnoreMentionsRepliesAndLeading
	case IgnoreMentionsAll, "any":
		return IgnoreMentionsAll
	default:
		return OverrideModeGlobal
	}
}

// NormalizeVoiceOverrideMode returns "global" or "custom".
func NormalizeVoiceOverrideMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), OverrideModeCustom) {
		return OverrideModeCustom
	}
	return OverrideModeGlobal
}

type PlatformOverrides struct {
	OverrideRules                 bool    `json:"override_rules,omitempty"`
	SpeakNamesMode                string  `json:"speak_names_mode,omitempty"`
	NaturalizeNamesMode           string  `json:"naturalize_names_mode,omitempty"`
	CoalesceConsecutiveMode       string  `json:"coalesce_consecutive_mode,omitempty"`
	SpeakEmotesMode               string  `json:"speak_emotes_mode,omitempty"`
	DeduplicateEmotesMode         string  `json:"deduplicate_emotes_mode,omitempty"`
	MaxDuplicateWordsMode         string  `json:"max_duplicate_words_mode,omitempty"`
	StripZalgoMode                string  `json:"strip_zalgo_mode,omitempty"`
	StripASCIIArtMode             string  `json:"strip_ascii_art_mode,omitempty"`
	DeduplicateCopypastaMode      string  `json:"deduplicate_copypasta_mode,omitempty"`
	SpeakPlatformNameMode         string  `json:"speak_platform_name_mode,omitempty"`
	IgnoreMentionsOverride        string  `json:"ignore_mentions_override,omitempty"`
	AllowBroadcasterMentionsMode  string  `json:"allow_broadcaster_mentions_mode,omitempty"`
	PlayAlertOnIgnoredMentionMode string  `json:"play_alert_on_ignored_mention_mode,omitempty"`
	HashChatterVoicesMode         string  `json:"hash_chatter_voices_mode,omitempty"`
	SmartGenderVoicesMode         string  `json:"smart_gender_voices_mode,omitempty"`
	TranslateMode                 string  `json:"translate_mode,omitempty"`
	VoiceOverrideMode             string  `json:"voice_override_mode,omitempty"`
	SpeakNames                    bool    `json:"speak_names"`
	NaturalizeNames               bool    `json:"naturalize_names"`
	CoalesceConsecutiveSeconds    int     `json:"coalesce_consecutive_seconds,omitempty"`
	SpeakEmotes                   bool    `json:"speak_emotes"`
	DeduplicateEmotes             bool    `json:"deduplicate_emotes"`
	MaxDuplicateWords             int     `json:"max_duplicate_words"`
	SpeakPlatformName             bool    `json:"speak_platform_name"`
	IgnoreMentionsMode            string  `json:"ignore_mentions_mode,omitempty"`
	AllowBroadcasterMentions      bool    `json:"allow_broadcaster_mentions,omitempty"`
	PlayAlertOnIgnoredMention     bool    `json:"play_alert_on_ignored_mention,omitempty"`
	HashChatterVoices             bool    `json:"hash_chatter_voices,omitempty"`
	SmartGenderVoices             bool    `json:"smart_gender_voices,omitempty"`
	DefaultVoice                  string  `json:"default_voice"`
	DefaultSpeed                  float32 `json:"default_speed"`
}

type ReadRules struct {
	AllChat         bool     `json:"all_chat"`
	Prefix          string   `json:"prefix"`
	PrefixOnly      bool     `json:"prefix_only"`
	IgnorePrefixes  bool     `json:"ignore_prefixes"`
	IgnoredPrefixes []string `json:"ignored_prefixes"`
	RoleGated       bool     `json:"role_gated"`
	Roles           []string `json:"roles"`
	MinFanClubLevel int      `json:"min_fan_club_level,omitempty"`
	PaidOnly        bool     `json:"paid_only"`
}

type NotificationEventConfig struct {
	Enabled   bool    `json:"enabled"`
	Template  string  `json:"template"`
	Voice     string  `json:"voice"`
	SoundFile string  `json:"sound_file"`
	Threshold float64 `json:"threshold"`
}

type PlatformConfig struct {
	Enabled            bool                               `json:"enabled"`
	EverEnabled        bool                               `json:"ever_enabled"`
	AccountName        string                             `json:"account_name"`
	ReadRules          ReadRules                          `json:"read_rules"`
	CustomRewardID     string                             `json:"custom_reward_id"`
	ModCommandsEnabled bool                               `json:"mod_commands_enabled"`
	Notifications      map[string]NotificationEventConfig `json:"notifications"`
	Overrides          PlatformOverrides                  `json:"overrides"`
}

type ViewerAccount struct {
	Platform string `json:"platform"`
	UserID   string `json:"user_id"`
}

type ViewerConfig struct {
	Platform string          `json:"platform"`
	UserID   string          `json:"user_id"`
	Accounts []ViewerAccount `json:"accounts,omitempty"`
	Voice    string          `json:"voice"`
	Speed    float32         `json:"speed"`
	Nickname string          `json:"nickname"`
	Ignored  bool            `json:"ignored"`
	Allowed  bool            `json:"allowed"`
}

// EffectiveAccounts returns all unique platform+userID pairs linked to this viewer profile.
func (v ViewerConfig) EffectiveAccounts() []ViewerAccount {
	var out []ViewerAccount
	seen := make(map[string]struct{})

	add := func(plat, uid string) {
		p := strings.ToLower(strings.TrimSpace(plat))
		u := strings.TrimSpace(uid)
		if u == "" {
			return
		}
		k := p + ":" + strings.ToLower(strings.TrimPrefix(u, "@"))
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, ViewerAccount{
			Platform: p,
			UserID:   u,
		})
	}

	for _, acc := range v.Accounts {
		add(acc.Platform, acc.UserID)
	}
	if v.UserID != "" {
		add(v.Platform, v.UserID)
	}
	return out
}

func normalizeHandle(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

// ViewerKey returns the canonical lowercased lookup key for a platform and user ID or username.
func ViewerKey(platform, idOrName string) string {
	return strings.ToLower(strings.TrimSpace(platform)) + ":" + strings.ToLower(strings.TrimSpace(idOrName))
}

// LookupViewerWithKey finds a ViewerConfig and its map key in viewers using case-insensitive matching
// across primary and all linked multi-platform accounts.
func LookupViewerWithKey(viewers map[string]ViewerConfig, platform, userID, userName string) (string, ViewerConfig, bool) {
	if len(viewers) == 0 {
		return "", ViewerConfig{}, false
	}

	p := strings.ToLower(strings.TrimSpace(platform))
	uID := strings.TrimSpace(userID)
	uName := strings.TrimSpace(userName)
	uIDNorm := normalizeHandle(uID)
	uNameNorm := normalizeHandle(uName)

	// 1. Direct match with canonical ViewerKey using UserID
	if uID != "" {
		k := ViewerKey(p, uID)
		if v, ok := viewers[k]; ok {
			return k, v, true
		}
	}

	// 2. Direct match with canonical ViewerKey using UserName
	if uName != "" {
		k := ViewerKey(p, uName)
		if v, ok := viewers[k]; ok {
			return k, v, true
		}
	}

	// 3. Scan all viewer profiles and their linked accounts
	targetIDKey := ""
	if uID != "" {
		targetIDKey = ViewerKey(p, uID)
	}
	targetNameKey := ""
	if uName != "" {
		targetNameKey = ViewerKey(p, uName)
	}

	for k, v := range viewers {
		kLower := strings.ToLower(k)
		if targetIDKey != "" && kLower == targetIDKey {
			return k, v, true
		}
		if targetNameKey != "" && kLower == targetNameKey {
			return k, v, true
		}

		for _, acc := range v.EffectiveAccounts() {
			if acc.Platform != "" && !strings.EqualFold(acc.Platform, p) {
				continue
			}
			accNorm := normalizeHandle(acc.UserID)
			if accNorm == "" {
				continue
			}
			if (uIDNorm != "" && accNorm == uIDNorm) || (uNameNorm != "" && accNorm == uNameNorm) {
				return k, v, true
			}
		}

		// Legacy fallback: key matches username/userID directly
		if uName != "" && strings.EqualFold(k, uName) && (strings.EqualFold(v.Platform, p) || v.Platform == "") {
			return k, v, true
		}
		if uID != "" && strings.EqualFold(k, uID) && (strings.EqualFold(v.Platform, p) || v.Platform == "") {
			return k, v, true
		}
	}

	return "", ViewerConfig{}, false
}

// LookupViewer finds a ViewerConfig in viewers using case-insensitive matching across platform, userID, and username.
func LookupViewer(viewers map[string]ViewerConfig, platform, userID, userName string) (ViewerConfig, bool) {
	_, v, ok := LookupViewerWithKey(viewers, platform, userID, userName)
	return v, ok
}

// DefaultConfig returns a new Config initialized with production defaults.
func DefaultConfig() *Config {
	return &Config{
		Version:             CurrentVersion,
		LoadedSchemaVersion: CurrentVersion,
		UIScale:             "1.0",
		Update: UpdateConfig{
			CheckOnStartup: true,
		},
		Audio: AudioConfig{
			DeviceID:       "",
			DeviceName:     "",
			Volume:         1.0,
			MicGateMode:    MicGateModeOff,
			MicDeviceID:    "",
			MicDeviceName:  "",
			MicThresholdDB: DefaultMicThresholdDB,
			MicDuckLevel:   DefaultMicDuckLevel,
			MicReleaseMs:   DefaultMicReleaseMs,
		},
		TTS: TTSConfig{
			Enabled:               true,
			OutputMode:            TTSModeAlwaysOn,
			IdleTimeoutSeconds:    DefaultIdleTimeoutSeconds,
			PlayAlertWhenDisabled: true,
			AlertSoundPath:        "",
			AlertCooldownMs:       DefaultAlertCooldownMs,
			Preset:                "fast_lean",
			EnabledModels:      []string{"supertonic"},
			ModelFamily:        "supertonic",
			ModelPath:          "",
			NumThreads:         4,
			UseGPU:             false,
			MaxClauseWords:     12,
			IdleUnloadMinutes:  0,
		},
		Safety: SafetyConfig{
			PanicHotkey:          "Ctrl+Shift+M",
			ModeToggleHotkey:     DefaultModeToggleHotkey,
			BlockDefaultBadWords: true,
			BlockedWords:         []string{},
		},
		Queue: QueueConfig{
			MaxAgeSeconds:    30,
			CatchupThreshold: 5,
			CatchupSpeed:     1.3,
		},
		Bots: BotConfig{
			IgnoreKnownBots: true,
			AutoUpdate:      false,
		},
		Logging: LoggingConfig{
			Enabled:                    true,
			Backend:                    "sqlite",
			DBPath:                     "",
			ChatRetentionDays:          30,
			EventRetentionDays:         90,
			ChatScale:                  100,
			SessionResumeWindowMinutes: 60,
			LiveLogs:                   DefaultLiveLogPresets(),
		},
		WebSocket: WebSocketConfig{
			Enabled:   false,
			Host:      DefaultWebSocketHost,
			Port:      DefaultWebSocketPort,
			AuthToken: "",
		},
		Translation: TranslationConfig{
			Enabled:              false,
			RoutingMode:          TranslationRoutingPriority,
			PlayChimeBeforeSpeak: true,
			ChimeSoundPath:       "",
			Providers:            DefaultTranslationProviders(),
			Provider:             TranslationProviderMyMemory,
			TargetLang:           DefaultTranslationTargetLang,
			MinConfidence:        DefaultTranslationMinConfidence,
		},
		GlobalChatRules: GlobalChatRules{
			SpeakNames:                        true,
			NaturalizeNames:                   true,
			CoalesceConsecutiveSeconds:        DefaultCoalesceConsecutiveSeconds,
			SpeakEmotes:                       false,
			DeduplicateEmotes:                 true,
			MaxDuplicateWords:                 2,
			StripZalgo:                        true,
			StripASCIIArt:                     true,
			DeduplicateCopypastaWindowSeconds: DefaultCopypastaWindowSeconds,
			SpeakPlatformName:                 false,
			HashChatterVoices:                 false,
			SmartGenderVoices:                 true,
			UnknownGenderPolicy:               UnknownGenderBalanced,
			DefaultVoice:                      "supertonic:f1",
			DefaultSpeed:                      1.0,
		},
		Viewers: make(map[string]ViewerConfig),
		Platforms: map[string]PlatformConfig{
			"twitch": {
				Enabled:     false,
				EverEnabled: true, // Twitch is MVP, visible by default
				AccountName: "",
				ReadRules: ReadRules{
					AllChat:         true,
					Prefix:          "!tts",
					PrefixOnly:      false,
					IgnorePrefixes:  true,
					IgnoredPrefixes: []string{"!"},
					RoleGated:       false,
					Roles:           []string{"broadcaster", "mod", "vip", "subscriber"},
					PaidOnly:        false,
				},
				CustomRewardID:     "",
				ModCommandsEnabled: false,
				Notifications: map[string]NotificationEventConfig{
					"follow": {
						Enabled:   false,
						Template:  "{user} is now following!",
						Voice:     "",
						Threshold: 0,
					},
					"sub": {
						Enabled:   true,
						Template:  "{user} just subscribed!",
						Voice:     "",
						Threshold: 0,
					},
					"resub": {
						Enabled:   true,
						Template:  "{user} resubscribed for {months} months! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"gift_sub": {
						Enabled:   true,
						Template:  "{user} gifted {count} subscriptions!",
						Voice:     "",
						Threshold: 1,
					},
					"sub_upgrade": {
						Enabled:   true,
						Template:  "{user} upgraded their subscription! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"watch_streak": {
						Enabled:   true,
						Template:  "{user} is on a {count} stream watch streak! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"cheer": {
						Enabled:   true,
						Template:  "{user} cheered {bits} bits: {message}",
						Voice:     "",
						Threshold: 100,
					},
					"bits_badge_tier": {
						Enabled:   true,
						Template:  "{user} unlocked the {amount} bits badge! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"raid": {
						Enabled:   true,
						Template:  "{user} raided with {viewers} viewers!",
						Voice:     "",
						Threshold: 5,
					},
					"redemption": {
						Enabled:   true,
						Template:  "{user} redeemed {reward}: {message}",
						Voice:     "",
						Threshold: 0,
					},
					"charity_donation": {
						Enabled:   true,
						Template:  "{user} donated {amount} {currency} to {reward}! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"announcement": {
						Enabled:   false,
						Template:  "Announcement from {user}: {message}",
						Voice:     "",
						Threshold: 0,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:              false,
					SpeakNames:                 true,
					NaturalizeNames:            true,
					CoalesceConsecutiveSeconds: DefaultCoalesceConsecutiveSeconds,
					SpeakEmotes:                false,
					DeduplicateEmotes:          true,
					MaxDuplicateWords:          2,
					SpeakPlatformName:          false,
					DefaultVoice:               "",
					DefaultSpeed:               1.0,
				},
			},
			"youtube": {
				Enabled:     false,
				EverEnabled: true,
				AccountName: "",
				ReadRules: ReadRules{
					AllChat:         true,
					Prefix:          "!tts",
					PrefixOnly:      false,
					IgnorePrefixes:  true,
					IgnoredPrefixes: []string{"!"},
					RoleGated:       false,
					Roles:           []string{"broadcaster", "mod", "vip", "subscriber"},
					PaidOnly:        false,
				},
				ModCommandsEnabled: false,
				Notifications: map[string]NotificationEventConfig{
					"super_chat": {
						Enabled:   true,
						Template:  "{user} sent {amount} {currency}! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"super_sticker": {
						Enabled:   true,
						Template:  "{user} sent a {amount} {currency} super sticker!",
						Voice:     "",
						Threshold: 0,
					},
					"gift": {
						Enabled:   true,
						Template:  "{user} sent {gift}!",
						Voice:     "",
						Threshold: 0,
					},
					"sub": {
						Enabled:   true,
						Template:  "{user} just became a member!",
						Voice:     "",
						Threshold: 0,
					},
					"resub": {
						Enabled:   true,
						Template:  "{user} has been a member for {months} months! {message}",
						Voice:     "",
						Threshold: 0,
					},
					"gift_sub": {
						Enabled:   true,
						Template:  "{user} gifted {count} memberships!",
						Voice:     "",
						Threshold: 0,
					},
					"gift_redemption": {
						Enabled:   false,
						Template:  "{user} received a gift membership from {recipient}!",
						Voice:     "",
						Threshold: 0,
					},
					"raid": {
						Enabled:   true,
						Template:  "{user} raided with a live redirect!",
						Voice:     "",
						Threshold: 0,
					},
					"charity_donation": {
						Enabled:   true,
						Template:  "{user} donated {amount} {currency}! {message}",
						Voice:     "",
						Threshold: 0,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:              false,
					SpeakNames:                 true,
					NaturalizeNames:            true,
					CoalesceConsecutiveSeconds: DefaultCoalesceConsecutiveSeconds,
					SpeakEmotes:                false,
					DeduplicateEmotes:          true,
					MaxDuplicateWords:          2,
					SpeakPlatformName:          false,
					DefaultVoice:               "",
					DefaultSpeed:               1.0,
				},
			},
			"tiktok": {
				Enabled:     false,
				EverEnabled: true,
				AccountName: "",
				ReadRules: ReadRules{
					AllChat:         true,
					Prefix:          "!tts",
					PrefixOnly:      false,
					IgnorePrefixes:  true,
					IgnoredPrefixes: []string{"!"},
					RoleGated:       false,
					Roles:           []string{"broadcaster", "mod", "vip", "subscriber"},
					MinFanClubLevel: 1,
					PaidOnly:        false,
				},
				ModCommandsEnabled: false,
				Notifications: map[string]NotificationEventConfig{
					"gift": {
						Enabled:   true,
						Template:  "{user} sent {count} {gift}!",
						Voice:     "",
						Threshold: 0,
					},
					"sub": {
						Enabled:   true,
						Template:  "{user} became a Super Fan!",
						Voice:     "",
						Threshold: 0,
					},
					"resub": {
						Enabled:   true,
						Template:  "{user} has been a Super Fan for {months} months!",
						Voice:     "",
						Threshold: 0,
					},
					"gift_sub": {
						Enabled:   true,
						Template:  "{user} gifted {count} Super Fan memberships!",
						Voice:     "",
						Threshold: 1,
					},
					"follow": {
						Enabled:   false,
						Template:  "{user} followed on TikTok!",
						Voice:     "",
						Threshold: 0,
					},
					"share": {
						Enabled:   false,
						Template:  "{user} shared the stream!",
						Voice:     "",
						Threshold: 0,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:              false,
					SpeakNames:                 true,
					NaturalizeNames:            true,
					CoalesceConsecutiveSeconds: DefaultCoalesceConsecutiveSeconds,
					SpeakEmotes:                false,
					DeduplicateEmotes:          true,
					MaxDuplicateWords:          2,
					SpeakPlatformName:          false,
					DefaultVoice:               "",
					DefaultSpeed:               1.0,
				},
			},
			"kick": {
				Enabled:     false,
				EverEnabled: true,
				AccountName: "",
				ReadRules: ReadRules{
					AllChat:         true,
					Prefix:          "!tts",
					PrefixOnly:      false,
					IgnorePrefixes:  true,
					IgnoredPrefixes: []string{"!"},
					RoleGated:       false,
					Roles:           []string{"broadcaster", "mod", "vip", "og", "subscriber"},
					PaidOnly:        false,
				},
				ModCommandsEnabled: false,
				Notifications: map[string]NotificationEventConfig{
					"follow": {
						Enabled:   false,
						Template:  "{user} followed on Kick!",
						Voice:     "",
						Threshold: 0,
					},
					"sub": {
						Enabled:   true,
						Template:  "{user} just subscribed on Kick!",
						Voice:     "",
						Threshold: 0,
					},
					"resub": {
						Enabled:   true,
						Template:  "{user} resubscribed for {months} months!",
						Voice:     "",
						Threshold: 0,
					},
					"gift_sub": {
						Enabled:   true,
						Template:  "{user} gifted {count} subscriptions!",
						Voice:     "",
						Threshold: 1,
					},
					"raid": {
						Enabled:   true,
						Template:  "{user} hosted with {viewers} viewers!",
						Voice:     "",
						Threshold: 1,
					},
					"gift": {
						Enabled:   true,
						Template:  "{user} sent {count} {gift}: {message}",
						Voice:     "",
						Threshold: 1,
					},
					"redemption": {
						Enabled:   true,
						Template:  "{user} redeemed {reward}: {message}",
						Voice:     "",
						Threshold: 0,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:              false,
					SpeakNames:                 true,
					NaturalizeNames:            true,
					CoalesceConsecutiveSeconds: DefaultCoalesceConsecutiveSeconds,
					SpeakEmotes:                false,
					DeduplicateEmotes:          true,
					MaxDuplicateWords:          2,
					SpeakPlatformName:          false,
					DefaultVoice:               "",
					DefaultSpeed:               1.0,
				},
			},
		},
	}
}

// ConfigPath returns the standard configuration file path on the host system.
func ConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "config.json"), nil
}

// EffectiveChatRules returns effective chat rules for a platform, taking per-setting platform overrides into account.
func (c *Config) EffectiveChatRules(platform string) (speakNames, speakEmotes, deduplicateEmotes bool, maxDuplicateWords int, speakPlatformName bool, voice string, speed float32) {
	speakNames = c.GlobalChatRules.SpeakNames
	speakEmotes = c.GlobalChatRules.SpeakEmotes
	deduplicateEmotes = c.GlobalChatRules.DeduplicateEmotes
	maxDuplicateWords = c.GlobalChatRules.MaxDuplicateWords
	speakPlatformName = c.GlobalChatRules.SpeakPlatformName
	voice = c.GlobalChatRules.DefaultVoice
	speed = c.GlobalChatRules.DefaultSpeed

	p, ok := c.Platforms[platform]
	if !ok {
		return speakNames, speakEmotes, deduplicateEmotes, maxDuplicateWords, speakPlatformName, voice, speed
	}
	ov := p.Overrides

	switch NormalizeBoolOverrideMode(ov.SpeakNamesMode) {
	case OverrideModeOn:
		speakNames = true
	case OverrideModeOff:
		speakNames = false
	default:
		if ov.OverrideRules {
			speakNames = ov.SpeakNames
		}
	}

	switch NormalizeBoolOverrideMode(ov.SpeakEmotesMode) {
	case OverrideModeOn:
		speakEmotes = true
	case OverrideModeOff:
		speakEmotes = false
	default:
		if ov.OverrideRules {
			speakEmotes = ov.SpeakEmotes
		}
	}

	switch NormalizeBoolOverrideMode(ov.DeduplicateEmotesMode) {
	case OverrideModeOn:
		deduplicateEmotes = true
	case OverrideModeOff:
		deduplicateEmotes = false
	default:
		if ov.OverrideRules {
			deduplicateEmotes = ov.DeduplicateEmotes
		}
	}

	switch dupMode := NormalizeDuplicateWordsOverrideMode(ov.MaxDuplicateWordsMode); dupMode {
	case OverrideModeOff:
		maxDuplicateWords = -1
	case OverrideModeGlobal:
		if ov.OverrideRules {
			maxDuplicateWords = ov.MaxDuplicateWords
		}
	default:
		if n, err := strconv.Atoi(dupMode); err == nil {
			maxDuplicateWords = n
		}
	}

	switch NormalizeBoolOverrideMode(ov.SpeakPlatformNameMode) {
	case OverrideModeOn:
		speakPlatformName = true
	case OverrideModeOff:
		speakPlatformName = false
	default:
		if ov.OverrideRules {
			speakPlatformName = ov.SpeakPlatformName
		}
	}

	if NormalizeVoiceOverrideMode(ov.VoiceOverrideMode) == OverrideModeCustom || ov.OverrideRules {
		if ov.DefaultVoice != "" {
			voice = ov.DefaultVoice
		}
		if ov.DefaultSpeed > 0 {
			speed = ov.DefaultSpeed
		}
	}

	return speakNames, speakEmotes, deduplicateEmotes, maxDuplicateWords, speakPlatformName, voice, speed
}

// EffectiveNaturalizeNames reports whether automatic username naturalization is enabled for a platform,
// taking per-setting platform overrides into account.
func (c *Config) EffectiveNaturalizeNames(platform string) bool {
	if c == nil {
		return true
	}
	val := c.GlobalChatRules.NaturalizeNames
	if p, ok := c.Platforms[platform]; ok {
		ov := p.Overrides
		switch NormalizeBoolOverrideMode(ov.NaturalizeNamesMode) {
		case OverrideModeOn:
			val = true
		case OverrideModeOff:
			val = false
		default:
			if ov.OverrideRules {
				val = ov.NaturalizeNames
			}
		}
	}
	return val
}

// EffectiveCoalesceSeconds returns the effective consecutive-speaker coalescing window in seconds
// for a platform (or 0 when disabled), taking per-setting platform overrides into account.
func (c *Config) EffectiveCoalesceSeconds(platform string) int {
	if c == nil {
		return DefaultCoalesceConsecutiveSeconds
	}
	speakNames, _, _, _, _, _, _ := c.EffectiveChatRules(platform)
	if !speakNames {
		return 0
	}
	val := c.GlobalChatRules.CoalesceConsecutiveSeconds
	if val == 0 {
		val = DefaultCoalesceConsecutiveSeconds
	}

	if p, ok := c.Platforms[platform]; ok {
		ov := p.Overrides
		switch mode := NormalizeCoalesceOverrideMode(ov.CoalesceConsecutiveMode); mode {
		case OverrideModeOff:
			val = -1
		case OverrideModeGlobal:
			if ov.OverrideRules && ov.CoalesceConsecutiveSeconds != 0 {
				val = ov.CoalesceConsecutiveSeconds
			}
		default:
			if n, err := strconv.Atoi(mode); err == nil {
				val = n
			}
		}
	}

	if val < 0 {
		return 0
	}
	return val
}

// EffectiveMentionRules returns the effective @mention and reply filtering rules for a platform,
// taking per-setting platform overrides into account.
func (c *Config) EffectiveMentionRules(platform string) (mode string, allowBroadcaster bool, playAlert bool) {
	mode = NormalizeIgnoreMentionsMode(c.GlobalChatRules.IgnoreMentionsMode)
	allowBroadcaster = c.GlobalChatRules.AllowBroadcasterMentions
	playAlert = c.GlobalChatRules.PlayAlertOnIgnoredMention

	p, ok := c.Platforms[platform]
	if !ok {
		return mode, allowBroadcaster, playAlert
	}
	ov := p.Overrides

	switch mentionMode := NormalizeIgnoreMentionsOverrideMode(ov.IgnoreMentionsOverride); mentionMode {
	case IgnoreMentionsOff, IgnoreMentionsRepliesAndLeading, IgnoreMentionsAll:
		mode = mentionMode
	default:
		if ov.OverrideRules {
			mode = NormalizeIgnoreMentionsMode(ov.IgnoreMentionsMode)
		}
	}

	switch NormalizeBoolOverrideMode(ov.AllowBroadcasterMentionsMode) {
	case OverrideModeOn:
		allowBroadcaster = true
	case OverrideModeOff:
		allowBroadcaster = false
	default:
		if ov.OverrideRules {
			allowBroadcaster = ov.AllowBroadcasterMentions
		}
	}

	switch NormalizeBoolOverrideMode(ov.PlayAlertOnIgnoredMentionMode) {
	case OverrideModeOn:
		playAlert = true
	case OverrideModeOff:
		playAlert = false
	default:
		if ov.OverrideRules {
			playAlert = ov.PlayAlertOnIgnoredMention
		}
	}

	return mode, allowBroadcaster, playAlert
}

// EffectiveChatterVoiceRules reports whether automatic deterministic chatter voice assignment and
// smart gender detection are enabled for a platform, taking per-platform overrides into account.
func (c *Config) EffectiveChatterVoiceRules(platform string) (hashVoices bool, smartGender bool) {
	if c == nil {
		return false, true
	}
	hashVoices = c.GlobalChatRules.HashChatterVoices
	smartGender = c.GlobalChatRules.SmartGenderVoices

	if p, ok := c.Platforms[platform]; ok {
		ov := p.Overrides
		switch NormalizeBoolOverrideMode(ov.HashChatterVoicesMode) {
		case OverrideModeOn:
			hashVoices = true
		case OverrideModeOff:
			hashVoices = false
		default:
			if ov.OverrideRules {
				hashVoices = ov.HashChatterVoices
			}
		}

		switch NormalizeBoolOverrideMode(ov.SmartGenderVoicesMode) {
		case OverrideModeOn:
			smartGender = true
		case OverrideModeOff:
			smartGender = false
		default:
			if ov.OverrideRules {
				smartGender = ov.SmartGenderVoices
			}
		}
	}
	return hashVoices, smartGender
}

// EffectiveTranslationEnabled reports whether foreign chat translation is enabled for a platform,
// taking per-platform TranslateMode ("global", "on", "off") into account.
func (c *Config) EffectiveTranslationEnabled(platform string) bool {
	if c == nil {
		return false
	}
	if p, ok := c.Platforms[platform]; ok {
		switch NormalizeBoolOverrideMode(p.Overrides.TranslateMode) {
		case OverrideModeOn:
			return true
		case OverrideModeOff:
			return false
		}
	}
	return c.Translation.Enabled
}

// EffectiveAntiSpamRules returns the effective Zalgo stripping, Braille/ASCII art stripping, and
// sliding-window copypasta deduplication window in seconds (or 0 when disabled) for a platform,
// taking per-platform override modes into account.
func (c *Config) EffectiveAntiSpamRules(platform string) (stripZalgo, stripASCIIArt bool, copypastaWindowSeconds int) {
	if c == nil {
		return true, true, DefaultCopypastaWindowSeconds
	}
	stripZalgo = c.GlobalChatRules.StripZalgo
	stripASCIIArt = c.GlobalChatRules.StripASCIIArt
	copypastaWindowSeconds = c.GlobalChatRules.DeduplicateCopypastaWindowSeconds
	if copypastaWindowSeconds == 0 {
		copypastaWindowSeconds = DefaultCopypastaWindowSeconds
	}

	if p, ok := c.Platforms[platform]; ok {
		ov := p.Overrides
		switch NormalizeBoolOverrideMode(ov.StripZalgoMode) {
		case OverrideModeOn:
			stripZalgo = true
		case OverrideModeOff:
			stripZalgo = false
		}

		switch NormalizeBoolOverrideMode(ov.StripASCIIArtMode) {
		case OverrideModeOn:
			stripASCIIArt = true
		case OverrideModeOff:
			stripASCIIArt = false
		}

		switch mode := NormalizeCoalesceOverrideMode(ov.DeduplicateCopypastaMode); mode {
		case OverrideModeOff:
			copypastaWindowSeconds = -1
		case OverrideModeGlobal:
			// inherit global
		default:
			if n, err := strconv.Atoi(mode); err == nil {
				copypastaWindowSeconds = n
			}
		}
	}

	if copypastaWindowSeconds < 0 {
		copypastaWindowSeconds = 0
	}
	return stripZalgo, stripASCIIArt, copypastaWindowSeconds
}

// Clone creates a deep copy of the Config without JSON serialization overhead.
func (c *Config) Clone() *Config {
	if c == nil {
		return DefaultConfig()
	}
	clone := *c
	clone.TTS.EnabledModels = slices.Clone(c.TTS.EnabledModels)
	clone.GlobalChatRules.DisabledChatterVoices = slices.Clone(c.GlobalChatRules.DisabledChatterVoices)
	clone.Safety.BlockedWords = slices.Clone(c.Safety.BlockedWords)
	clone.Bots.CustomBots = slices.Clone(c.Bots.CustomBots)
	clone.Logging.LiveLogs = slices.Clone(c.Logging.LiveLogs)
	clone.Translation.Providers = slices.Clone(c.Translation.Providers)

	if c.Viewers != nil {
		clone.Viewers = make(map[string]ViewerConfig, len(c.Viewers))
		for k, v := range c.Viewers {
			v.Accounts = slices.Clone(v.Accounts)
			clone.Viewers[k] = v
		}
	}

	if c.Platforms != nil {
		clone.Platforms = make(map[string]PlatformConfig, len(c.Platforms))
		for k, p := range c.Platforms {
			p.ReadRules.IgnoredPrefixes = slices.Clone(p.ReadRules.IgnoredPrefixes)
			p.ReadRules.Roles = slices.Clone(p.ReadRules.Roles)
			if p.Notifications != nil {
				p.Notifications = maps.Clone(p.Notifications)
			}
			clone.Platforms[k] = p
		}
	}

	return &clone
}

var fileLock sync.Mutex

// Load loads the configuration from the given file path.
// The file path must be explicitly provided and non-empty.
// If the file does not exist, DefaultConfig() is returned.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("cannot load config: explicit path must be provided")
	}

	fileLock.Lock()
	defer fileLock.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Migration fallback: check if legacy config exists
			if configDir, errDir := os.UserConfigDir(); errDir == nil {
				candidates := []string{
					filepath.Join(configDir, "TownCrier", "config.json"),
					filepath.Join(configDir, "com.github.mrpoundsign.octochorpebot", "config.json"),
					filepath.Join(configDir, "OctochorpeBot", "config.json"),
				}
				for _, legacyPath := range candidates {
					if legacyData, errLegacy := os.ReadFile(legacyPath); errLegacy == nil {
						cfg := DefaultConfig()
						cfg.TTS.Preset = ""
						cfg.TTS.EnabledModels = nil
						cfg.Translation.Providers = nil
						if errParse := json.Unmarshal(legacyData, cfg); errParse == nil {
							_ = migrate(cfg)
							// Migrate immediately to the new location
							_ = saveLocked(path, cfg)
							return cfg, nil
						}
					}
				}
			}
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := DefaultConfig()
	cfg.Version = 0
	cfg.TTS.Preset = ""
	cfg.TTS.EnabledModels = nil
	cfg.Translation.Providers = nil
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}

	rawVersion := cfg.Version
	if rawVersion <= 0 {
		rawVersion = 1
	}
	cfg.LoadedSchemaVersion = rawVersion

	if rawVersion < CurrentVersion {
		if err := createPreMigrationConfigBackup(path, data, rawVersion); err != nil {
			return nil, fmt.Errorf("backup config before schema migration: %w", err)
		}
	}

	if err := migrate(cfg); err != nil {
		return nil, fmt.Errorf("migrate config: %w", err)
	}

	if rawVersion < CurrentVersion {
		_ = saveLocked(path, cfg)
	}

	return cfg, nil
}

// Save atomically writes the configuration to the specified path.
// It writes to a temporary file in the same directory first, then renames.
func Save(path string, cfg *Config) error {
	fileLock.Lock()
	defer fileLock.Unlock()
	return saveLocked(path, cfg)
}

func saveLocked(path string, cfg *Config) error {
	if path == "" {
		return errors.New("cannot save config: explicit path must be provided")
	}
	if testing.Testing() {
		// During unit tests, never write to the host user config.
		// Only writes inside os.TempDir() are permitted.
		tempDir := filepath.Clean(os.TempDir())
		cleanPath := filepath.Clean(path)
		rel, err := filepath.Rel(tempDir, cleanPath)
		isTemp := err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
		if !isTemp {
			return errors.New("refusing to save configuration outside of temporary directory during unit tests")
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".config.tmp.%d", os.Getpid()))
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("atomic rename config: %w", err)
	}

	if cfg != nil && cfg.Version > 0 {
		cfg.LoadedSchemaVersion = cfg.Version
	}

	return nil
}

func voiceFamilyFromID(voiceID string) string {
	id := strings.ToLower(strings.TrimSpace(voiceID))
	if id == "" {
		return ""
	}
	if prefix, _, ok := strings.Cut(id, ":"); ok {
		switch prefix {
		case "supertonic", "kokoro", "kokoro_intl", "kitten_mini", "kitten_nano", "piper":
			return prefix
		}
	}
	raw := strings.TrimPrefix(id, "kokoro:")
	if len(raw) >= 3 && raw[2] == '_' {
		switch raw[:2] {
		case "ef", "em", "ff", "hf", "hm", "if", "im", "jf", "jm", "pf", "pm", "zf", "zm":
			return "kokoro_intl"
		}
	}
	return "kokoro"
}

func migrate(cfg *Config) error {
	// Baseline migration runner: normalize in-memory schema version to CurrentVersion
	cfg.Version = CurrentVersion

	if len(cfg.TTS.EnabledModels) == 0 {
		cfg.TTS.EnabledModels = cfg.TTS.EffectiveEnabledModels()
	}

	// Ensure any model family referenced by assigned voices (global, viewers, platforms) is enabled
	enabledSet := make(map[string]bool, len(cfg.TTS.EnabledModels))
	for _, m := range cfg.TTS.EnabledModels {
		enabledSet[strings.ToLower(strings.TrimSpace(m))] = true
	}
	ensureVoiceFamily := func(voiceID string) {
		fam := voiceFamilyFromID(voiceID)
		if fam != "" && !enabledSet[fam] {
			enabledSet[fam] = true
			cfg.TTS.EnabledModels = append(cfg.TTS.EnabledModels, fam)
		}
	}
	ensureVoiceFamily(cfg.GlobalChatRules.DefaultVoice)
	for _, v := range cfg.Viewers {
		if !v.Ignored {
			ensureVoiceFamily(v.Voice)
		}
	}
	for _, p := range cfg.Platforms {
		if p.Overrides.OverrideRules || NormalizeVoiceOverrideMode(p.Overrides.VoiceOverrideMode) == OverrideModeCustom {
			ensureVoiceFamily(p.Overrides.DefaultVoice)
		}
		for _, n := range p.Notifications {
			if n.Enabled {
				ensureVoiceFamily(n.Voice)
			}
		}
	}

	if cfg.TTS.Preset == "" {
		switch {
		case cfg.TTS.UseGPU || len(cfg.TTS.EnabledModels) > 1:
			cfg.TTS.Preset = "custom"
		case len(cfg.TTS.EnabledModels) == 1 && cfg.TTS.EnabledModels[0] == "supertonic":
			cfg.TTS.Preset = "fast_lean"
		default:
			cfg.TTS.Preset = "balanced"
		}
	}
	if cfg.TTS.MaxClauseWords <= 0 {
		cfg.TTS.MaxClauseWords = 12
	}
	cfg.TTS.OutputMode = NormalizeTTSOutputMode(cfg.TTS.OutputMode)
	if cfg.TTS.IdleTimeoutSeconds <= 0 {
		cfg.TTS.IdleTimeoutSeconds = DefaultIdleTimeoutSeconds
	}
	cfg.Audio.MicGateMode = NormalizeMicGateMode(cfg.Audio.MicGateMode)
	if cfg.Audio.MicThresholdDB == 0 || cfg.Audio.MicThresholdDB < -58.0 || cfg.Audio.MicThresholdDB > -5.0 {
		cfg.Audio.MicThresholdDB = DefaultMicThresholdDB
	}
	if cfg.Audio.MicDuckLevel <= 0 || cfg.Audio.MicDuckLevel > 1.0 {
		cfg.Audio.MicDuckLevel = DefaultMicDuckLevel
	}
	if cfg.Audio.MicReleaseMs <= 0 {
		cfg.Audio.MicReleaseMs = DefaultMicReleaseMs
	}
	if cfg.TTS.AlertCooldownMs <= 0 {
		cfg.TTS.AlertCooldownMs = DefaultAlertCooldownMs
		cfg.TTS.PlayAlertWhenDisabled = true
	}
	if strings.TrimSpace(cfg.Safety.ModeToggleHotkey) == "" {
		cfg.Safety.ModeToggleHotkey = DefaultModeToggleHotkey
	}
	if strings.TrimSpace(cfg.WebSocket.Host) == "" {
		cfg.WebSocket.Host = DefaultWebSocketHost
	}
	if cfg.WebSocket.Port <= 0 || cfg.WebSocket.Port > 65535 {
		cfg.WebSocket.Port = DefaultWebSocketPort
	}

	if strings.TrimSpace(cfg.UIScale) == "" {
		cfg.UIScale = "1.0"
	}
	if strings.TrimSpace(cfg.Logging.Backend) == "" {
		cfg.Logging.Backend = "sqlite"
	}
	if cfg.Logging.ChatRetentionDays < 0 {
		cfg.Logging.ChatRetentionDays = 0
	}
	if cfg.Logging.EventRetentionDays < 0 {
		cfg.Logging.EventRetentionDays = 0
	}
	switch {
	case cfg.Logging.ChatScale <= 0:
		cfg.Logging.ChatScale = 100
	case cfg.Logging.ChatScale < 50:
		cfg.Logging.ChatScale = 50
	case cfg.Logging.ChatScale > 300:
		cfg.Logging.ChatScale = 300
	}
	if len(cfg.Logging.LiveLogs) == 0 {
		cfg.Logging.LiveLogs = DefaultLiveLogPresets()
	}
	for i := range cfg.Logging.LiveLogs {
		sc := cfg.Logging.LiveLogs[i].ChatScale
		switch {
		case sc < 0:
			cfg.Logging.LiveLogs[i].ChatScale = 0
		case sc > 0 && sc < 50:
			cfg.Logging.LiveLogs[i].ChatScale = 50
		case sc > 300:
			cfg.Logging.LiveLogs[i].ChatScale = 300
		}
	}

	// Normalize viewer keys and populate multi-platform Accounts slice
	if len(cfg.Viewers) > 0 {
		normalized := make(map[string]ViewerConfig, len(cfg.Viewers))
		for k, v := range cfg.Viewers {
			accs := v.EffectiveAccounts()
			v.Accounts = accs
			if len(accs) > 0 && v.UserID == "" {
				v.Platform = accs[0].Platform
				v.UserID = accs[0].UserID
			}
			key := ViewerKey(v.Platform, v.UserID)
			if key == ":" || v.UserID == "" {
				key = strings.ToLower(k)
			}
			normalized[key] = v
		}
		cfg.Viewers = normalized
	}

	// Backfill any newly added notification event defaults per platform
	defaults := DefaultConfig()
	if cfg.Platforms == nil {
		cfg.Platforms = defaults.Platforms
	} else {
		for platKey, defPlat := range defaults.Platforms {
			p, ok := cfg.Platforms[platKey]
			if !ok {
				cfg.Platforms[platKey] = defPlat
				continue
			}
			if p.Notifications == nil {
				p.Notifications = make(map[string]NotificationEventConfig, len(defPlat.Notifications))
			}
			for evKey, defNotif := range defPlat.Notifications {
				existing, exists := p.Notifications[evKey]
				if !exists {
					p.Notifications[evKey] = defNotif
					continue
				}
				// Repair legacy placeholder fallback templates ("{user}: {message}") on events without messages
				if (evKey == "follow" || evKey == "gift_sub") && existing.Template == "{user}: {message}" {
					existing.Template = defNotif.Template
					if evKey == "follow" {
						existing.Enabled = defNotif.Enabled
					}
					p.Notifications[evKey] = existing
				}
			}
			if p.Overrides.MaxDuplicateWords == 0 {
				p.Overrides.MaxDuplicateWords = 2
			}
			if p.Overrides.CoalesceConsecutiveSeconds == 0 {
				p.Overrides.CoalesceConsecutiveSeconds = DefaultCoalesceConsecutiveSeconds
			}
			p.Overrides.IgnoreMentionsMode = NormalizeIgnoreMentionsMode(p.Overrides.IgnoreMentionsMode)
			if p.Overrides.OverrideRules {
				if p.Overrides.SpeakNamesMode == "" {
					p.Overrides.SpeakNamesMode = BoolToOverrideMode(p.Overrides.SpeakNames)
				}
				if p.Overrides.NaturalizeNamesMode == "" {
					p.Overrides.NaturalizeNamesMode = BoolToOverrideMode(p.Overrides.NaturalizeNames)
				}
				if p.Overrides.CoalesceConsecutiveMode == "" && p.Overrides.CoalesceConsecutiveSeconds != DefaultCoalesceConsecutiveSeconds {
					p.Overrides.CoalesceConsecutiveMode = CoalesceSecondsToOverrideMode(p.Overrides.CoalesceConsecutiveSeconds)
				}
				if p.Overrides.SpeakEmotesMode == "" {
					p.Overrides.SpeakEmotesMode = BoolToOverrideMode(p.Overrides.SpeakEmotes)
				}
				if p.Overrides.DeduplicateEmotesMode == "" {
					p.Overrides.DeduplicateEmotesMode = BoolToOverrideMode(p.Overrides.DeduplicateEmotes)
				}
				if p.Overrides.MaxDuplicateWordsMode == "" {
					p.Overrides.MaxDuplicateWordsMode = DuplicateWordsToOverrideMode(p.Overrides.MaxDuplicateWords)
				}
				if p.Overrides.SpeakPlatformNameMode == "" {
					p.Overrides.SpeakPlatformNameMode = BoolToOverrideMode(p.Overrides.SpeakPlatformName)
				}
				if p.Overrides.IgnoreMentionsOverride == "" {
					p.Overrides.IgnoreMentionsOverride = p.Overrides.IgnoreMentionsMode
				}
				if p.Overrides.AllowBroadcasterMentionsMode == "" {
					p.Overrides.AllowBroadcasterMentionsMode = BoolToOverrideMode(p.Overrides.AllowBroadcasterMentions)
				}
				if p.Overrides.PlayAlertOnIgnoredMentionMode == "" {
					p.Overrides.PlayAlertOnIgnoredMentionMode = BoolToOverrideMode(p.Overrides.PlayAlertOnIgnoredMention)
				}
				if p.Overrides.VoiceOverrideMode == "" && (strings.TrimSpace(p.Overrides.DefaultVoice) != "" || (p.Overrides.DefaultSpeed > 0 && p.Overrides.DefaultSpeed != 1.0)) {
					p.Overrides.VoiceOverrideMode = OverrideModeCustom
				}
				p.Overrides.OverrideRules = false
			}
			cfg.Platforms[platKey] = p
		}
	}

	if cfg.GlobalChatRules.MaxDuplicateWords == 0 {
		cfg.GlobalChatRules.MaxDuplicateWords = 2
	}
	if cfg.GlobalChatRules.CoalesceConsecutiveSeconds == 0 {
		cfg.GlobalChatRules.CoalesceConsecutiveSeconds = DefaultCoalesceConsecutiveSeconds
	}
	if cfg.GlobalChatRules.DeduplicateCopypastaWindowSeconds == 0 {
		cfg.GlobalChatRules.DeduplicateCopypastaWindowSeconds = DefaultCopypastaWindowSeconds
	}
	cfg.GlobalChatRules.IgnoreMentionsMode = NormalizeIgnoreMentionsMode(cfg.GlobalChatRules.IgnoreMentionsMode)
	NormalizeTranslationProviders(&cfg.Translation)

	return nil
}
