package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const greeterUUID = "com.github.mrpoundsign.towncrier.examples.wasm-greeter"

type greeterPlugin struct {
	pluginsdk.BasePlugin
	mu   sync.Mutex
	host *pluginsdk.Host
}

func init() {
	pluginsdk.Register(&greeterPlugin{})
}

func (p *greeterPlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{
		Uuid:                 greeterUUID,
		Name:                 "WASM Greeter",
		Version:              "1.0.0",
		Author:               "TownCrier",
		Description:          "Greets new followers and chatters on their first message each live stream using persistent KV tracking and TTS speech.",
		ApiVersion:           &pluginv1.ProtocolVersion{Major: 1, Minor: 0, Patch: 0, MinHostMinor: 0},
		RequiredCapabilities: []string{"core.v1", "events.v1", "kv.v1", "tts.v1"},
		SubscribedEvents:     []string{"follow", "sub", "message"},
		Permissions: &pluginv1.Permissions{
			SpeakTts:   true,
			ReadEvents: true,
			KvStorage:  true,
		},
		GlobalSettingsSchema: []*pluginv1.SettingField{
			{
				Key:          "greet_follows",
				Label:        "Greet New Followers",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: true}},
			},
			{
				Key:          "template",
				Label:        "Follow Greeting Template",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_TEXT,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_StringVal{StringVal: "Welcome to the stream, {user}!"}},
				Help:         "Supports {user}, {count}, and {platform} placeholders.",
			},
			{
				Key:          "greet_first_message",
				Label:        "Greet Chatters on First Message Each Live Stream",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: true}},
			},
			{
				Key:          "first_message_template",
				Label:        "First Message Greeting Template",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_TEXT,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_StringVal{StringVal: "Hey {user}, welcome in!"}},
				Help:         "Supports {user}, {count}, and {platform} placeholders.",
			},
			{
				Key:          "require_live_stream",
				Label:        "Only Greet First Messages While Live",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: true}},
				Help:         "Uncheck to also greet first-time chatters while offline or testing.",
			},
			{
				Key:          "ignore_broadcaster",
				Label:        "Ignore Broadcaster Messages",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: true}},
			},
			{
				Key:          "ignore_bots",
				Label:        "Ignore Known Bot Messages",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: true}},
			},
			{
				Key:     "status",
				Label:   "Status",
				Type:    pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO,
				Section: "General",
			},
			{
				Key:          "testGreet",
				Label:        "Test Follow Greeting",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_ButtonVal{ButtonVal: &pluginv1.ButtonValue{Label: "Test Follow Greeting"}}},
			},
			{
				Key:          "testFirstMessage",
				Label:        "Test First Message Greeting",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_ButtonVal{ButtonVal: &pluginv1.ButtonValue{Label: "Test First Message Greeting"}}},
			},
			{
				Key:          "resetStreamGreets",
				Label:        "Reset Greeted Chatters",
				Type:         pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON,
				Section:      "General",
				DefaultValue: &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_ButtonVal{ButtonVal: &pluginv1.ButtonValue{Label: "Reset Greeted Chatters"}}},
			},
			{
				Key:         "ignored_users",
				Label:       "Ignored Usernames",
				Type:        pluginv1.SettingFieldType_SETTING_FIELD_TYPE_MULTILINE,
				Section:     "Advanced",
				Placeholder: "nightbot, streamelements",
				Help:        "Comma or newline-separated usernames to never greet.",
			},
			{
				Key:         "apiSecret",
				Label:       "Optional Secret Token",
				Type:        pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD,
				Section:     "Advanced",
				Placeholder: "Stored securely in OS keyring",
			},
		},
	}
}

func (p *greeterPlugin) Init(h *pluginsdk.Host) error {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
	h.Logf("WASM Greeter initialized")
	return nil
}

func (p *greeterPlugin) OnEvent(ev *pluginv1.StreamEvent) error {
	if ev == nil {
		return nil
	}
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil {
		return nil
	}

	switch ev.GetKind() {
	case "follow":
		settings, err := h.GetGlobalSettings()
		if err != nil {
			return err
		}
		if !pluginsdk.GetSettingBool(settings, "greet_follows", true) {
			return nil
		}
		user := displayUserName(ev)
		if user == "" || isIgnoredUser(settings, ev) {
			return nil
		}
		return p.speakGreeting(h, settings, "template", "Welcome to the stream, {user}!", user, ev.GetPlatform(), "follow")

	case "message":
		return p.handleChatMessage(h, ev)

	default:
		return nil
	}
}

func (p *greeterPlugin) handleChatMessage(h *pluginsdk.Host, ev *pluginv1.StreamEvent) error {
	if strings.TrimSpace(ev.GetText()) == "" {
		return nil
	}
	settings, err := h.GetGlobalSettings()
	if err != nil {
		return err
	}
	if !pluginsdk.GetSettingBool(settings, "greet_first_message", true) {
		return nil
	}
	if pluginsdk.GetSettingBool(settings, "ignore_bots", true) && ev.GetIsBotSignal() {
		return nil
	}
	if pluginsdk.GetSettingBool(settings, "ignore_broadcaster", true) && hasRole(ev.GetRoles(), "broadcaster") {
		return nil
	}
	if isIgnoredUser(settings, ev) {
		return nil
	}

	streamKey := resolveStreamSessionKey(ev, pluginsdk.GetSettingBool(settings, "require_live_stream", true))
	if streamKey == "" {
		return nil
	}

	userKey := stableUserKey(ev)
	displayUser := displayUserName(ev)
	if userKey == "" || displayUser == "" {
		return nil
	}

	plat := strings.ToLower(strings.TrimSpace(ev.GetPlatform()))
	if plat == "" {
		plat = "unknown"
	}
	seenKey := fmt.Sprintf("seen:%s:%s:%s", streamKey, plat, userKey)
	if _, found, kvErr := h.KVGet(seenKey); kvErr == nil && found {
		return nil
	}

	if err := h.KVSet(seenKey, []byte("1")); err != nil {
		return err
	}
	return p.speakGreeting(h, settings, "first_message_template", "Hey {user}, welcome in!", displayUser, ev.GetPlatform(), "first message")
}

func (p *greeterPlugin) OnConfigAction(actionKey string, settings *pluginv1.SettingsMap) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil {
		return nil
	}

	if settings == nil {
		var err error
		settings, err = h.GetGlobalSettings()
		if err != nil {
			return err
		}
	}

	switch actionKey {
	case "testGreet":
		return p.speakGreeting(h, settings, "template", "Welcome to the stream, {user}!", "TestViewer", "twitch", "follow test")
	case "testFirstMessage":
		return p.speakGreeting(h, settings, "first_message_template", "Hey {user}, welcome in!", "TestViewer", "twitch", "first message test")
	case "resetStreamGreets":
		keys, err := h.KVList("seen:", 2000)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if delErr := h.KVDelete(k); delErr != nil {
				return delErr
			}
		}
		updated := pluginsdk.SetSettingInfo(settings, "status", fmt.Sprintf("Cleared %d greeted chatter record(s)", len(keys)), false)
		_ = h.SaveGlobalSettings(updated)
		return nil
	default:
		return fmt.Errorf("unknown action %q", actionKey)
	}
}

func (p *greeterPlugin) speakGreeting(
	h *pluginsdk.Host,
	settings *pluginv1.SettingsMap,
	tmplKey string,
	defaultTmpl string,
	user string,
	platform string,
	reason string,
) error {
	count := 0
	if raw, found, err := h.KVGet("greet_count"); err == nil && found {
		if n, convErr := strconv.Atoi(string(raw)); convErr == nil {
			count = n
		}
	}
	count++
	if err := h.KVSet("greet_count", []byte(strconv.Itoa(count))); err != nil {
		return err
	}

	plat := strings.TrimSpace(platform)
	if plat == "" {
		plat = "stream"
	}
	tmpl := pluginsdk.GetSettingString(settings, tmplKey, defaultTmpl)
	if strings.TrimSpace(tmpl) == "" {
		tmpl = defaultTmpl
	}
	msg := strings.ReplaceAll(tmpl, "{user}", user)
	msg = strings.ReplaceAll(msg, "{count}", strconv.Itoa(count))
	msg = strings.ReplaceAll(msg, "{platform}", plat)

	updated := pluginsdk.SetSettingInfo(settings, "status", fmt.Sprintf("Greeted %s (%s, #%d)", user, reason, count), false)
	_ = h.SaveGlobalSettings(updated)
	return h.Speak(msg, "", 1.0, 0, "WASM Greeter")
}

func resolveStreamSessionKey(ev *pluginv1.StreamEvent, requireLive bool) string {
	if ev.GetStreamIsLive() {
		if sid := ev.GetStreamSessionId(); sid > 0 {
			return fmt.Sprintf("sess_%d", sid)
		}
		if stID := strings.TrimSpace(ev.GetStreamId()); stID != "" {
			return "stream_" + stID
		}
		return "live"
	}
	if requireLive {
		return ""
	}
	if sid := ev.GetStreamSessionId(); sid > 0 {
		return fmt.Sprintf("sess_%d", sid)
	}
	return "offline"
}

func stableUserKey(ev *pluginv1.StreamEvent) string {
	if id := strings.TrimSpace(ev.GetUserId()); id != "" {
		return id
	}
	if u := strings.ToLower(strings.TrimSpace(ev.GetUserName())); u != "" {
		return u
	}
	return strings.ToLower(strings.TrimSpace(ev.GetDisplayName()))
}

func displayUserName(ev *pluginv1.StreamEvent) string {
	if d := strings.TrimSpace(ev.GetDisplayName()); d != "" {
		return d
	}
	return strings.TrimSpace(ev.GetUserName())
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if strings.EqualFold(strings.TrimSpace(r), target) {
			return true
		}
	}
	return false
}

func isIgnoredUser(settings *pluginv1.SettingsMap, ev *pluginv1.StreamEvent) bool {
	raw := pluginsdk.GetSettingString(settings, "ignored_users", "")
	if strings.TrimSpace(raw) == "" {
		return false
	}
	uName := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ev.GetUserName()), "@"))
	dName := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ev.GetDisplayName()), "@"))
	var blocked []string
	for part := range strings.FieldsFuncSeq(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "@"))
		if clean != "" {
			blocked = append(blocked, clean)
		}
	}
	return (uName != "" && slices.Contains(blocked, uName)) || (dName != "" && slices.Contains(blocked, dName))
}

func main() {
	_ = pluginsdk.Run()
}
