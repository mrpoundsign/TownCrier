package ui

import "strings"

// botOption maps a canonical config.json key to a natural-English UI label.
type botOption struct {
	Key   string
	Label string
}

func botOptionLabels(opts []botOption) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Label
	}
	return out
}

func botOptionLabel(opts []botOption, key string) string {
	clean := strings.TrimSpace(key)
	for _, o := range opts {
		if strings.EqualFold(o.Key, clean) {
			return o.Label
		}
	}
	if len(opts) > 0 && clean == "" {
		return opts[0].Label
	}
	return clean
}

func botOptionKey(opts []botOption, label string) string {
	clean := strings.TrimSpace(label)
	for _, o := range opts {
		if o.Label == clean || strings.EqualFold(o.Key, clean) {
			return o.Key
		}
	}
	if len(opts) > 0 && clean == "" {
		return opts[0].Key
	}
	return clean
}

var botSubActionKindOptions = []botOption{
	{Key: "send_chat", Label: "Send Chat Message"},
	{Key: "speak_tts", Label: "Speak Text-to-Speech (TTS)"},
	{Key: "play_sound", Label: "Play Sound File"},
	{Key: "moderate", Label: "Moderate User / Message"},
	{Key: "set_var", Label: "Set Variable"},
	{Key: "inc_var", Label: "Increment Variable"},
	{Key: "delay", Label: "Wait / Delay"},
	{Key: "http_request", Label: "Send HTTP Request"},
	{Key: "obs", Label: "Control OBS Studio"},
	{Key: "plugin_action", Label: "Run Plugin Action"},
	{Key: "overlay_broadcast", Label: "Send Overlay Event"},
	{Key: "run_action", Label: "Run Another Action"},
	{Key: "if_else", Label: "If / Else Condition"},
	{Key: "break", Label: "Stop Action (Break)"},
}

var botOBSOperationOptions = []botOption{
	{Key: "set_scene", Label: "Switch Scene"},
	{Key: "set_source_visible", Label: "Show / Hide Source"},
	{Key: "set_mute", Label: "Mute / Unmute Input"},
	{Key: "toggle_record", Label: "Toggle Recording"},
	{Key: "toggle_stream", Label: "Toggle Streaming"},
	{Key: "save_replay", Label: "Save Replay Buffer"},
	{Key: "refresh_browser", Label: "Refresh Browser Source"},
}

var botVarScopeOptions = []botOption{
	{Key: "global_persistent", Label: "Global (Saved)"},
	{Key: "global_ephemeral", Label: "Global (Session Only)"},
	{Key: "user_persistent", Label: "Per-User (Saved)"},
}

var botGlobalVarScopeOptions = botVarScopeOptions[:2]

var botVarKindOptions = []botOption{
	{Key: "string", Label: "Text"},
	{Key: "int", Label: "Whole Number (Int)"},
	{Key: "float", Label: "Decimal (Float)"},
	{Key: "bool", Label: "True / False (Bool)"},
}

var botConditionOperatorOptions = []botOption{
	{Key: "eq", Label: "Equals"},
	{Key: "neq", Label: "Does Not Equal"},
	{Key: "contains", Label: "Contains"},
	{Key: "gt", Label: "Greater Than (>)"},
	{Key: "gte", Label: "Greater Than or Equal (≥)"},
	{Key: "lt", Label: "Less Than (<)"},
	{Key: "lte", Label: "Less Than or Equal (≤)"},
	{Key: "empty", Label: "Is Empty"},
	{Key: "not_empty", Label: "Is Not Empty"},
}

var botModerationOpOptions = []botOption{
	{Key: "delete", Label: "Delete Message"},
	{Key: "timeout", Label: "Timeout User"},
	{Key: "ban", Label: "Ban User"},
	{Key: "unban", Label: "Unban User"},
}

var botTriggerSourceOptions = []botOption{
	{Key: "platform_event", Label: "Platform Event"},
	{Key: "plugin_trigger", Label: "Plugin Trigger"},
	{Key: "obs_event", Label: "OBS Studio Event"},
}

var botCommandMatchModeOptions = []botOption{
	{Key: "prefix", Label: "Starts With (Prefix)"},
	{Key: "exact", Label: "Exact Match"},
	{Key: "regex", Label: "Regular Expression (Regex)"},
}

var botVoiceMatchModeOptions = []botOption{
	{Key: "contains", Label: "Contains Phrase"},
	{Key: "phonetic", Label: "Phonetic / Sound-Alike"},
	{Key: "exact", Label: "Exact Match"},
	{Key: "regex", Label: "Regular Expression (Regex)"},
}
