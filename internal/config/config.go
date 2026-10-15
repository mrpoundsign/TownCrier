package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const CurrentVersion = 1

// Config represents the application configuration hierarchy.
type Config struct {
	Version         int                       `json:"version"`
	Audio           AudioConfig               `json:"audio"`
	TTS             TTSConfig                 `json:"tts"`
	Safety          SafetyConfig              `json:"safety"`
	Queue           QueueConfig               `json:"queue"`
	Bots            BotConfig                 `json:"bots"`
	GlobalChatRules GlobalChatRules           `json:"global_chat_rules"`
	Viewers         map[string]ViewerConfig   `json:"viewers"`
	Platforms       map[string]PlatformConfig `json:"platforms"`
}

type AudioConfig struct {
	DeviceID   string  `json:"device_id"`
	DeviceName string  `json:"device_name"`
	Volume     float32 `json:"volume"` // 0.0 to 1.0
}

type TTSConfig struct {
	ModelFamily string `json:"model_family"` // "kokoro"
	ModelPath   string `json:"model_path"`
	NumThreads  int    `json:"num_threads"`
	UseGPU      bool   `json:"use_gpu"`
}

type SafetyConfig struct {
	PanicHotkey  string   `json:"panic_hotkey"`
	BlockedWords []string `json:"blocked_words"`
}

type QueueConfig struct {
	MaxAgeSeconds    int     `json:"max_age_seconds"`
	CatchupThreshold int     `json:"catchup_threshold"`
	CatchupSpeed     float32 `json:"catchup_speed"`
}

type BotConfig struct {
	IgnoreKnownBots bool `json:"ignore_known_bots"`
	AutoUpdate      bool `json:"auto_update"`
}

type GlobalChatRules struct {
	SpeakNames        bool    `json:"speak_names"`
	SpeakEmotes       bool    `json:"speak_emotes"`
	SpeakPlatformName bool    `json:"speak_platform_name"`
	DefaultVoice      string  `json:"default_voice"`
	DefaultSpeed      float32 `json:"default_speed"`
}

type PlatformOverrides struct {
	OverrideRules     bool    `json:"override_rules"`
	SpeakNames        bool    `json:"speak_names"`
	SpeakEmotes       bool    `json:"speak_emotes"`
	SpeakPlatformName bool    `json:"speak_platform_name"`
	DefaultVoice      string  `json:"default_voice"`
	DefaultSpeed      float32 `json:"default_speed"`
}

type ReadRules struct {
	AllChat         bool     `json:"all_chat"`
	Prefix          string   `json:"prefix"`
	PrefixOnly      bool     `json:"prefix_only"`
	IgnorePrefixes  bool     `json:"ignore_prefixes"`
	IgnoredPrefixes []string `json:"ignored_prefixes"`
	RoleGated       bool     `json:"role_gated"`
	Roles           []string `json:"roles"`
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
		Audio: AudioConfig{
			DeviceID:   "",
			DeviceName: "",
			Volume:     1.0,
		},
		TTS: TTSConfig{
			ModelFamily: "kokoro",
			ModelPath:   "",
			NumThreads:  4,
			UseGPU:      true,
		},
		Safety: SafetyConfig{
			PanicHotkey:  "Ctrl+Shift+M",
			BlockedWords: []string{},
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
		GlobalChatRules: GlobalChatRules{
			SpeakNames:        true,
			SpeakEmotes:       false,
			SpeakPlatformName: false,
			DefaultVoice:      "af_bella",
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
					"sub": {
						Enabled:   true,
						Template:  "{user} just subscribed!",
						Voice:     "",
						Threshold: 0,
					},
					"resub": {
						Enabled:   true,
						Template:  "{user} resubscribed for {months} months!",
						Voice:     "",
						Threshold: 0,
					},
					"cheer": {
						Enabled:   true,
						Template:  "{user} cheered {bits} bits: {message}",
						Voice:     "",
						Threshold: 100,
					},
					"raid": {
						Enabled:   true,
						Template:  "{user} raided with {viewers} viewers!",
						Voice:     "",
						Threshold: 5,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
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
				},
				Overrides: PlatformOverrides{
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
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
					"follow": {
						Enabled:   false,
						Template:  "{user} followed on TikTok!",
						Voice:     "",
						Threshold: 0,
					},
				},
				Overrides: PlatformOverrides{
					OverrideRules:     false,
					SpeakNames:        true,
					SpeakEmotes:       false,
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

// EffectiveChatRules returns effective chat rules for a platform, taking platform overrides into account.
func (c *Config) EffectiveChatRules(platform string) (speakNames, speakEmotes, speakPlatformName bool, voice string, speed float32) {
	speakNames = c.GlobalChatRules.SpeakNames
	speakEmotes = c.GlobalChatRules.SpeakEmotes
	speakPlatformName = c.GlobalChatRules.SpeakPlatformName
	voice = c.GlobalChatRules.DefaultVoice
	speed = c.GlobalChatRules.DefaultSpeed

	if p, ok := c.Platforms[platform]; ok && p.Overrides.OverrideRules {
		speakNames = p.Overrides.SpeakNames
		speakEmotes = p.Overrides.SpeakEmotes
		speakPlatformName = p.Overrides.SpeakPlatformName
		if p.Overrides.DefaultVoice != "" {
			voice = p.Overrides.DefaultVoice
		}
		if p.Overrides.DefaultSpeed > 0 {
			speed = p.Overrides.DefaultSpeed
		}
	}
	return speakNames, speakEmotes, speakPlatformName, voice, speed
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
// If path is empty, it uses the default path from ConfigPath().
// If the file does not exist, DefaultConfig() is returned.
func Load(path string) (*Config, error) {
	fileLock.Lock()
	defer fileLock.Unlock()

	if path == "" {
		var err error
		path, err = ConfigPath()
		if err != nil {
			return nil, err
		}
	}

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
		var err error
		path, err = ConfigPath()
		if err != nil {
			return err
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

func migrate(cfg *Config) error {
	// Baseline migration runner: upgrade configs older than CurrentVersion
	if cfg.Version < 1 {
		cfg.Version = 1
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

	return nil
}
