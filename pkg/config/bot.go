package config

import (
	"maps"
	"slices"
	"strings"
)

const (
	DefaultOBSHost = "127.0.0.1"
	DefaultOBSPort = 4455

	SecretOBSPassword = "bot_obs_password"
)

// BotAutomationConfig configures TownCrier's Streamer.bot-class automation engine:
// triggers, commands, timed actions, Whisper voice commands, execution queues, and OBS WebSocket v5.
type BotAutomationConfig struct {
	Enabled       bool                 `json:"enabled"`
	Commands      []BotCommandDef      `json:"commands,omitempty"`
	Actions       []BotActionDef       `json:"actions,omitempty"`
	Triggers      []BotTriggerDef      `json:"triggers,omitempty"`
	Timers        []BotTimerDef        `json:"timers,omitempty"`
	VoiceCommands []BotVoiceCommandDef `json:"voice_commands,omitempty"`
	Queues        []BotQueueDef        `json:"queues,omitempty"`
	OBS           BotOBSConfig         `json:"obs"`
}

// BotCommandDef defines a chat command trigger (e.g., !so, !lurk, !hug) and its permissions/cooldowns.
type BotCommandDef struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Enabled           bool     `json:"enabled"`
	Trigger           string   `json:"trigger"`
	Aliases           []string `json:"aliases,omitempty"`
	MatchMode         string   `json:"match_mode,omitempty"` // "prefix" (default), "exact", "regex"
	CaseSensitive     bool     `json:"case_sensitive,omitempty"`
	ConsumeTTS        bool     `json:"consume_tts,omitempty"`
	GlobalCooldownSec int      `json:"global_cooldown_sec,omitempty"`
	UserCooldownSec   int      `json:"user_cooldown_sec,omitempty"`
	AllowedRoles      []string `json:"allowed_roles,omitempty"`  // empty = everyone; "broadcaster", "mod", "vip", "subscriber"
	AllowedGroups     []string `json:"allowed_groups,omitempty"` // optional named user groups
	Platforms         []string `json:"platforms,omitempty"`      // empty = all platforms
	ActionID          string   `json:"action_id"`
}

// BotTriggerDef binds a normalized stream, OBS, webhook, or plugin event to a BotActionDef.
type BotTriggerDef struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	Source       string  `json:"source"`               // "platform_event", "plugin_trigger", "obs_event", "hotkey"
	EventKind    string  `json:"event_kind,omitempty"` // e.g. "follow", "sub", "cheer", "raid", "redemption", or plugin trigger_id
	Platform     string  `json:"platform,omitempty"`   // empty = any platform
	MinAmount    float64 `json:"min_amount,omitempty"`
	RewardFilter string  `json:"reward_filter,omitempty"`
	CooldownSec  int     `json:"cooldown_sec,omitempty"`
	ActionID     string  `json:"action_id"`
}

// BotTimerDef runs an action periodically when interval/stream/chat-line thresholds are met.
type BotTimerDef struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	LiveOnly        bool   `json:"live_only,omitempty"`
	MinChatLines    int    `json:"min_chat_lines,omitempty"`
	ActionID        string `json:"action_id"`
}

// BotVoiceCommandDef binds a local Whisper STT transcript match to a BotActionDef.
type BotVoiceCommandDef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Phrase      string `json:"phrase"`
	MatchMode   string `json:"match_mode,omitempty"` // "contains" (default), "exact", "regex"
	CooldownSec int    `json:"cooldown_sec,omitempty"`
	ActionID    string `json:"action_id"`
}

// BotQueueDef defines a named action execution queue.
type BotQueueDef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Blocking   bool   `json:"blocking"`
	MaxPending int    `json:"max_pending,omitempty"`
}

// BotActionDef defines a named ordered workflow of sub-actions executed in a queue.
type BotActionDef struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Enabled    bool              `json:"enabled"`
	QueueID    string            `json:"queue_id,omitempty"` // empty -> "default"
	SubActions []BotSubActionDef `json:"sub_actions,omitempty"`
}

// BotSubActionDef configures a single atomic step inside a BotActionDef.
// Built-in kinds:
//   - "speak_tts": text, voice_id, speed, priority
//   - "play_sound": sound_path
//   - "send_chat": platform, channel, text, as_announcement
//   - "moderate": platform, operation ("delete"|"timeout"|"ban"|"unban"), target, duration_seconds, reason
//   - "set_var": scope ("global_persistent"|"global_ephemeral"|"user_persistent"), key, var_kind, value, delete
//   - "inc_var": scope, key, delta
//   - "if_else": lhs, operator ("eq"|"neq"|"contains"|"gt"|"gte"|"lt"|"lte"|"empty"|"not_empty"), rhs, then_sub_actions, else_sub_actions
//   - "break": (halts current action chain)
//   - "delay": delay_ms
//   - "http_request": method, url, body, headers, save_var
//   - "run_action": target_action_id
//   - "overlay_broadcast": plugin_uuid, event_name, payload
//   - "obs": obs_operation, scene, source, item_enabled, input, muted, text
//   - "plugin_action": plugin_uuid, plugin_action_id, params
type BotSubActionDef struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`

	// Shared / Speech / Chat
	Text           string  `json:"text,omitempty"`
	VoiceID        string  `json:"voice_id,omitempty"`
	Speed          float32 `json:"speed,omitempty"`
	Priority       int     `json:"priority,omitempty"`
	SoundPath      string  `json:"sound_path,omitempty"`
	Platform       string  `json:"platform,omitempty"`
	Channel        string  `json:"channel,omitempty"`
	AsAnnouncement bool    `json:"as_announcement,omitempty"`
	SendAs         string  `json:"send_as,omitempty"` // "", "auto", "bot", "broadcaster"

	// Moderation
	Operation       string `json:"operation,omitempty"`
	Target          string `json:"target,omitempty"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
	Reason          string `json:"reason,omitempty"`

	// Variables
	Scope   string  `json:"scope,omitempty"`
	Key     string  `json:"key,omitempty"`
	VarKind string  `json:"var_kind,omitempty"`
	Value   string  `json:"value,omitempty"`
	Delta   float64 `json:"delta,omitempty"`
	Delete  bool    `json:"delete,omitempty"`

	// Flow control & timing
	LHS            string            `json:"lhs,omitempty"`
	Operator       string            `json:"operator,omitempty"`
	RHS            string            `json:"rhs,omitempty"`
	ThenSubActions []BotSubActionDef `json:"then_sub_actions,omitempty"`
	ElseSubActions []BotSubActionDef `json:"else_sub_actions,omitempty"`
	DelayMs        int               `json:"delay_ms,omitempty"`
	TargetActionID string            `json:"target_action_id,omitempty"`

	// HTTP request
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	SaveVar string            `json:"save_var,omitempty"`

	// Overlay & Plugins
	PluginUUID     string            `json:"plugin_uuid,omitempty"`
	PluginActionID string            `json:"plugin_action_id,omitempty"`
	EventName      string            `json:"event_name,omitempty"`
	Payload        string            `json:"payload,omitempty"`
	Params         map[string]string `json:"params,omitempty"`

	// OBS Studio WebSocket v5
	OBSOperation string `json:"obs_operation,omitempty"` // "set_scene", "set_source_visible", "set_mute", "toggle_record", "toggle_stream", "save_replay", "refresh_browser"
	OBSScene     string `json:"obs_scene,omitempty"`
	OBSSource    string `json:"obs_source,omitempty"`
	OBSVisible   bool   `json:"obs_visible,omitempty"`
	OBSInput     string `json:"obs_input,omitempty"`
	OBSMuted     bool   `json:"obs_muted,omitempty"`
}

// BotOBSConfig stores non-secret OBS Studio WebSocket v5 connection settings.
// The OBS WebSocket password lives in the OS keyring via secrets.Store.
type BotOBSConfig struct {
	Enabled       bool   `json:"enabled"`
	AutoReconnect bool   `json:"auto_reconnect"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
}

// DefaultBotAutomationConfig returns sensible default bot automation settings with Disabled state.
func DefaultBotAutomationConfig() BotAutomationConfig {
	return BotAutomationConfig{
		Enabled: false,
		Queues: []BotQueueDef{
			{ID: "default", Name: "Default (Blocking)", Blocking: true, MaxPending: 64},
			{ID: "concurrent", Name: "Concurrent (Instant)", Blocking: false, MaxPending: 128},
		},
		OBS: BotOBSConfig{
			Enabled:       false,
			AutoReconnect: true,
			Host:          DefaultOBSHost,
			Port:          DefaultOBSPort,
		},
	}
}

// NormalizeBotAutomationConfig ensures required queues and OBS defaults are populated.
func NormalizeBotAutomationConfig(b *BotAutomationConfig) {
	if b == nil {
		return
	}
	if strings.TrimSpace(b.OBS.Host) == "" {
		b.OBS.Host = DefaultOBSHost
	}
	if b.OBS.Port <= 0 || b.OBS.Port > 65535 {
		b.OBS.Port = DefaultOBSPort
	}
	hasDefault := false
	hasConcurrent := false
	for i := range b.Queues {
		if strings.EqualFold(b.Queues[i].ID, "default") {
			hasDefault = true
		}
		if strings.EqualFold(b.Queues[i].ID, "concurrent") {
			hasConcurrent = true
		}
	}
	if !hasDefault {
		b.Queues = append([]BotQueueDef{{ID: "default", Name: "Default (Blocking)", Blocking: true, MaxPending: 64}}, b.Queues...)
	}
	if !hasConcurrent {
		b.Queues = append(b.Queues, BotQueueDef{ID: "concurrent", Name: "Concurrent (Instant)", Blocking: false, MaxPending: 128})
	}
}

func cloneSubActions(src []BotSubActionDef) []BotSubActionDef {
	if len(src) == 0 {
		return nil
	}
	out := make([]BotSubActionDef, len(src))
	for i, s := range src {
		out[i] = s
		if s.Headers != nil {
			h := make(map[string]string, len(s.Headers))
			maps.Copy(h, s.Headers)
			out[i].Headers = h
		}
		if s.Params != nil {
			p := make(map[string]string, len(s.Params))
			maps.Copy(p, s.Params)
			out[i].Params = p
		}
		out[i].ThenSubActions = cloneSubActions(s.ThenSubActions)
		out[i].ElseSubActions = cloneSubActions(s.ElseSubActions)
	}
	return out
}

func cloneBotAutomationConfig(b BotAutomationConfig) BotAutomationConfig {
	out := b
	out.Commands = slices.Clone(b.Commands)
	for i := range out.Commands {
		out.Commands[i].Aliases = slices.Clone(b.Commands[i].Aliases)
		out.Commands[i].AllowedRoles = slices.Clone(b.Commands[i].AllowedRoles)
		out.Commands[i].AllowedGroups = slices.Clone(b.Commands[i].AllowedGroups)
		out.Commands[i].Platforms = slices.Clone(b.Commands[i].Platforms)
	}
	out.Triggers = slices.Clone(b.Triggers)
	out.Timers = slices.Clone(b.Timers)
	out.VoiceCommands = slices.Clone(b.VoiceCommands)
	out.Queues = slices.Clone(b.Queues)
	if len(b.Actions) > 0 {
		out.Actions = make([]BotActionDef, len(b.Actions))
		for i, a := range b.Actions {
			out.Actions[i] = a
			out.Actions[i].SubActions = cloneSubActions(a.SubActions)
		}
	}
	return out
}

// HasEnabledVoiceCommands reports whether Bot Automation and at least one Whisper voice command are enabled.
func (b BotAutomationConfig) HasEnabledVoiceCommands() bool {
	if !b.Enabled {
		return false
	}
	for _, vc := range b.VoiceCommands {
		if vc.Enabled && strings.TrimSpace(vc.Phrase) != "" && strings.TrimSpace(vc.ActionID) != "" {
			return true
		}
	}
	return false
}
