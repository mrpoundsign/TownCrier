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
	{Key: "previous_scene", Label: "Switch to Previous Scene"},
	{Key: "set_source_visible", Label: "Show / Hide / Toggle Source"},
	{Key: "set_filter_enabled", Label: "Enable / Disable / Toggle Filter"},
	{Key: "zoom_source", Label: "Zoom Source (Relative Step)"},
	{Key: "set_transform", Label: "Set Source Transform"},
	{Key: "reset_transform", Label: "Reset Source Transform"},
	{Key: "set_mute", Label: "Mute / Unmute / Toggle Input"},
	{Key: "set_volume", Label: "Set / Step Input Volume"},
	{Key: "set_text", Label: "Set Text Source"},
	{Key: "media_action", Label: "Control Media Playback"},
	{Key: "refresh_browser", Label: "Refresh Browser Source"},
	{Key: "set_transition", Label: "Set Scene Transition"},
	{Key: "studio_mode", Label: "Studio Mode Action"},
	{Key: "toggle_record", Label: "Toggle Recording"},
	{Key: "toggle_stream", Label: "Toggle Streaming"},
	{Key: "save_replay", Label: "Save Replay Buffer"},
	{Key: "custom_request", Label: "Custom OBS WebSocket Request"},
}

var botOBSSceneTargetOptions = []botOption{
	{Key: "program", Label: "Program (Live)"},
	{Key: "preview", Label: "Preview (Studio Mode)"},
}

var botOBSVisibilityModeOptions = []botOption{
	{Key: "toggle", Label: "Toggle"},
	{Key: "show", Label: "Show"},
	{Key: "hide", Label: "Hide"},
}

var botOBSFilterModeOptions = []botOption{
	{Key: "toggle", Label: "Toggle"},
	{Key: "enable", Label: "Enable"},
	{Key: "disable", Label: "Disable"},
}

var botOBSMuteModeOptions = []botOption{
	{Key: "toggle", Label: "Toggle"},
	{Key: "mute", Label: "Mute"},
	{Key: "unmute", Label: "Unmute"},
}

var botOBSVolumeModeOptions = []botOption{
	{Key: "set", Label: "Set Exact Volume (%)"},
	{Key: "step", Label: "Step Relative (+/- %)"},
}

var botOBSMediaActionOptions = []botOption{
	{Key: "play", Label: "Play"},
	{Key: "pause", Label: "Pause"},
	{Key: "restart", Label: "Restart"},
	{Key: "stop", Label: "Stop"},
}

var botOBSStudioModeOptions = []botOption{
	{Key: "transition", Label: "Trigger Transition"},
	{Key: "toggle", Label: "Toggle Studio Mode"},
	{Key: "enable", Label: "Enable Studio Mode"},
	{Key: "disable", Label: "Disable Studio Mode"},
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

var botConditionBranchModeOptions = []botOption{
	{Key: "continue", Label: "Continue to Next Step"},
	{Key: "break", Label: "Stop Action (Break)"},
	{Key: "run_action", Label: "Run Another Action"},
	{Key: "run_action_break", Label: "Run Action & Stop"},
}

var botConditionLHSPresetOptions = []botOption{
	{Key: "%obs.scene%", Label: "Current OBS Scene (%obs.scene%)"},
	{Key: "%obs.previousScene%", Label: "Previous OBS Scene (%obs.previousScene%)"},
	{Key: "%obs.previewScene%", Label: "Preview OBS Scene (%obs.previewScene%)"},
	{Key: "%obs.connected%", Label: "OBS Connected (%obs.connected%)"},
	{Key: "%user%", Label: "Triggering User (%user%)"},
	{Key: "%arg0%", Label: "First Command Arg (%arg0%)"},
	{Key: "%rawInput%", Label: "Full Message Input (%rawInput%)"},
	{Key: "%platform%", Label: "Platform (%platform%)"},
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
