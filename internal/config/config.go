package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const CurrentVersion = 1

// Config represents the application configuration hierarchy.
type Config struct {
	Version         int                       `json:"version"`
	UIScale         string                    `json:"ui_scale,omitempty"`
	Update          UpdateConfig              `json:"update"`
	Audio           AudioConfig               `json:"audio"`
	TTS             TTSConfig                 `json:"tts"`
	Safety          SafetyConfig              `json:"safety"`
	Queue           QueueConfig               `json:"queue"`
	Bots            BotConfig                 `json:"bots"`
	Logging         LoggingConfig             `json:"logging"`
	WebSocket       WebSocketConfig           `json:"websocket"`
	GlobalChatRules GlobalChatRules           `json:"global_chat_rules"`
	Viewers         map[string]ViewerConfig   `json:"viewers"`
	Platforms       map[string]PlatformConfig `json:"platforms"`
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

type AudioConfig struct {
	DeviceID   string  `json:"device_id"`
	DeviceName string  `json:"device_name"`
	Volume     float32 `json:"volume"` // 0.0 to 1.0
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
)

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
	SpeakNames                bool    `json:"speak_names"`
	SpeakEmotes               bool    `json:"speak_emotes"`
	DeduplicateEmotes         bool    `json:"deduplicate_emotes"`
	MaxDuplicateWords         int     `json:"max_duplicate_words"`
	SpeakPlatformName         bool    `json:"speak_platform_name"`
	IgnoreMentionsMode        string  `json:"ignore_mentions_mode,omitempty"`
	AllowBroadcasterMentions  bool    `json:"allow_broadcaster_mentions,omitempty"`
	PlayAlertOnIgnoredMention bool    `json:"play_alert_on_ignored_mention,omitempty"`
	DefaultVoice              string  `json:"default_voice"`
	DefaultSpeed              float32 `json:"default_speed"`
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
	SpeakEmotesMode               string  `json:"speak_emotes_mode,omitempty"`
	DeduplicateEmotesMode         string  `json:"deduplicate_emotes_mode,omitempty"`
	MaxDuplicateWordsMode         string  `json:"max_duplicate_words_mode,omitempty"`
	SpeakPlatformNameMode         string  `json:"speak_platform_name_mode,omitempty"`
	IgnoreMentionsOverride        string  `json:"ignore_mentions_override,omitempty"`
	AllowBroadcasterMentionsMode  string  `json:"allow_broadcaster_mentions_mode,omitempty"`
	PlayAlertOnIgnoredMentionMode string  `json:"play_alert_on_ignored_mention_mode,omitempty"`
	VoiceOverrideMode             string  `json:"voice_override_mode,omitempty"`
	SpeakNames                    bool    `json:"speak_names"`
	SpeakEmotes                   bool    `json:"speak_emotes"`
	DeduplicateEmotes             bool    `json:"deduplicate_emotes"`
	MaxDuplicateWords             int     `json:"max_duplicate_words"`
	SpeakPlatformName             bool    `json:"speak_platform_name"`
	IgnoreMentionsMode            string  `json:"ignore_mentions_mode,omitempty"`
	AllowBroadcasterMentions      bool    `json:"allow_broadcaster_mentions,omitempty"`
	PlayAlertOnIgnoredMention     bool    `json:"play_alert_on_ignored_mention,omitempty"`
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
		Version: CurrentVersion,
		UIScale: "1.0",
		Update: UpdateConfig{
			CheckOnStartup: true,
		},
		Audio: AudioConfig{
			DeviceID:   "",
			DeviceName: "",
			Volume:     1.0,
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
		GlobalChatRules: GlobalChatRules{
			SpeakNames:        true,
			SpeakEmotes:       false,
			DeduplicateEmotes: true,
			MaxDuplicateWords: 2,
			SpeakPlatformName: false,
			DefaultVoice:      "supertonic:f1",
			DefaultSpeed:      1.0,
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
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
					DeduplicateEmotes: true,
					MaxDuplicateWords: 2,
					SpeakPlatformName: false,
					DefaultVoice:      "",
					DefaultSpeed:      1.0,
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
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
					DeduplicateEmotes: true,
					MaxDuplicateWords: 2,
					SpeakPlatformName: false,
					DefaultVoice:      "",
					DefaultSpeed:      1.0,
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
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
					DeduplicateEmotes: true,
					MaxDuplicateWords: 2,
					SpeakPlatformName: false,
					DefaultVoice:      "",
					DefaultSpeed:      1.0,
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
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
					DeduplicateEmotes: true,
					MaxDuplicateWords: 2,
					SpeakPlatformName: false,
					DefaultVoice:      "",
					DefaultSpeed:      1.0,
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

// Clone creates a deep copy of the Config.
func (c *Config) Clone() *Config {
	data, err := json.Marshal(c)
	if err != nil {
		return DefaultConfig()
	}
	var clone Config
	if err := json.Unmarshal(data, &clone); err != nil {
		return DefaultConfig()
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
	cfg.TTS.Preset = ""
	cfg.TTS.EnabledModels = nil
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}

	if err := migrate(cfg); err != nil {
		return nil, fmt.Errorf("migrate config: %w", err)
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
	// Baseline migration runner: upgrade configs older than CurrentVersion
	if cfg.Version < 1 {
		cfg.Version = 1
	}

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
			p.Overrides.IgnoreMentionsMode = NormalizeIgnoreMentionsMode(p.Overrides.IgnoreMentionsMode)
			if p.Overrides.OverrideRules {
				if p.Overrides.SpeakNamesMode == "" {
					p.Overrides.SpeakNamesMode = BoolToOverrideMode(p.Overrides.SpeakNames)
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
	cfg.GlobalChatRules.IgnoreMentionsMode = NormalizeIgnoreMentionsMode(cfg.GlobalChatRules.IgnoreMentionsMode)

	return nil
}
