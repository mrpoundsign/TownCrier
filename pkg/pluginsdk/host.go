package pluginsdk

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

type hostTransport interface {
	Call(req *pluginv1.PluginEnvelope) (*pluginv1.HostCallResponse, error)
}

// Host exposes TownCrier's versioned Protobuf capabilities to a running plugin.
type Host struct {
	tr hostTransport
}

func newHost(tr hostTransport) *Host {
	return &Host{tr: tr}
}

func (h *Host) call(req *pluginv1.PluginEnvelope) (*pluginv1.HostCallResponse, error) {
	if h == nil || h.tr == nil {
		return nil, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
			Message: "host transport is not initialized",
		}
	}
	resp, err := h.tr.Call(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("empty host call response")
	}
	if !resp.GetOk() {
		if apiErr := FromProto(resp.GetError()); apiErr != nil {
			return resp, apiErr
		}
		return resp, errors.New("host call failed without error details")
	}
	return resp, nil
}

// Log appends an informational diagnostic message to TownCrier's plugin log buffer.
func (h *Host) Log(level pluginv1.LogLevel, msg string) {
	_, _ = h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Log{
			Log: &pluginv1.LogRequest{
				Level:   level,
				Message: msg,
			},
		},
	})
}

// Logf appends a formatted informational log line to TownCrier's plugin log buffer.
func (h *Host) Logf(format string, args ...any) {
	h.Log(pluginv1.LogLevel_LOG_LEVEL_INFO, fmt.Sprintf(format, args...))
}

// Speak enqueues a TTS utterance on TownCrier's speech queue.
func (h *Host) Speak(text string, voiceID string, speed float32, priority int32, username string) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Speak{
			Speak: &pluginv1.SpeakRequest{
				Text:     text,
				VoiceId:  voiceID,
				Speed:    speed,
				Priority: priority,
				Username: username,
			},
		},
	})
	return err
}

// GetGlobalSettings fetches the plugin's persisted settings merged with keyring secrets.
func (h *Host) GetGlobalSettings() (*pluginv1.SettingsMap, error) {
	resp, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_GetGlobalSettings{
			GetGlobalSettings: &pluginv1.GetGlobalSettingsRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	gs := resp.GetGlobalSettings()
	if gs == nil || gs.GetSettings() == nil {
		return &pluginv1.SettingsMap{Values: map[string]*pluginv1.SettingValue{}}, nil
	}
	return gs.GetSettings(), nil
}

// SaveGlobalSettings persists plugin settings, routing password fields to the OS keyring.
func (h *Host) SaveGlobalSettings(settings *pluginv1.SettingsMap) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SaveGlobalSettings{
			SaveGlobalSettings: &pluginv1.SaveGlobalSettingsRequest{
				Settings: settings,
			},
		},
	})
	return err
}

// SetTimer creates, updates, or cancels (intervalMs == 0) a named host timer.
func (h *Host) SetTimer(timerID, ctx string, intervalMs uint32) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetTimer{
			SetTimer: &pluginv1.SetTimerRequest{
				TimerId:    timerID,
				Context:    ctx,
				IntervalMs: intervalMs,
			},
		},
	})
	return err
}

// HTTPDo executes a synchronous sandboxed HTTP request on the host.
func (h *Host) HTTPDo(req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	if req == nil {
		return nil, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil HTTPRequest",
		}
	}
	cloned := &pluginv1.HTTPRequest{
		Method:    req.GetMethod(),
		Url:       req.GetUrl(),
		Headers:   req.GetHeaders(),
		Body:      req.GetBody(),
		TimeoutMs: req.GetTimeoutMs(),
		Async:     false,
	}
	resp, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_HttpRequest{
			HttpRequest: cloned,
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetHttpResponse(), nil
}

// HTTPFetchAsync schedules a non-blocking HTTP request on the host; completion arrives via Plugin.OnHTTPResult.
func (h *Host) HTTPFetchAsync(asyncID string, req *pluginv1.HTTPRequest) error {
	if req == nil {
		return &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil HTTPRequest",
		}
	}
	cloned := &pluginv1.HTTPRequest{
		Method:    req.GetMethod(),
		Url:       req.GetUrl(),
		Headers:   req.GetHeaders(),
		Body:      req.GetBody(),
		TimeoutMs: req.GetTimeoutMs(),
		Async:     true,
		AsyncId:   asyncID,
	}
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_HttpRequest{
			HttpRequest: cloned,
		},
	})
	return err
}

// KVGet retrieves a namespaced byte slice from plugin SQLite KV storage.
func (h *Host) KVGet(key string) ([]byte, bool, error) {
	resp, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_KvGet{
			KvGet: &pluginv1.KVGetRequest{Key: key},
		},
	})
	if err != nil {
		return nil, false, err
	}
	kv := resp.GetKvGet()
	if kv == nil {
		return nil, false, nil
	}
	return kv.GetValue(), kv.GetFound(), nil
}

// KVSet stores a namespaced byte slice in plugin SQLite KV storage.
func (h *Host) KVSet(key string, value []byte) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_KvSet{
			KvSet: &pluginv1.KVSetRequest{
				Key:   key,
				Value: value,
			},
		},
	})
	return err
}

// KVDelete deletes a namespaced key from plugin SQLite KV storage.
func (h *Host) KVDelete(key string) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_KvDelete{
			KvDelete: &pluginv1.KVDeleteRequest{Key: key},
		},
	})
	return err
}

// KVList lists stored keys matching prefix (up to limit; 0 uses host default).
func (h *Host) KVList(prefix string, limit uint32) ([]string, error) {
	resp, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_KvList{
			KvList: &pluginv1.KVListRequest{
				Prefix: prefix,
				Limit:  limit,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if kv := resp.GetKvList(); kv != nil {
		return kv.GetKeys(), nil
	}
	return nil, nil
}

// OverlayBroadcast pushes a JSON event payload to TownCrier's local overlay SSE/WS server.
func (h *Host) OverlayBroadcast(eventName, jsonPayload string) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_OverlayBroadcast{
			OverlayBroadcast: &pluginv1.OverlayBroadcastRequest{
				EventName:   eventName,
				JsonPayload: jsonPayload,
			},
		},
	})
	return err
}

// SendUISnapshot pushes a full declarative UI view snapshot to TownCrier.
func (h *Host) SendUISnapshot(snap *pluginv1.UISnapshot) error {
	if snap == nil {
		return &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil UISnapshot",
		}
	}
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_PushUiSnapshot{
			PushUiSnapshot: snap,
		},
	})
	return err
}

// SendUIPatch pushes incremental UI mutations to an active declarative view in TownCrier.
func (h *Host) SendUIPatch(batch *pluginv1.UIPatchBatch) error {
	if batch == nil {
		return &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil UIPatchBatch",
		}
	}
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_PushUiPatch{
			PushUiPatch: batch,
		},
	})
	return err
}

// SendChat sends an outbound chat message or announcement on a connected platform.
func (h *Host) SendChat(req *pluginv1.SendChatRequest) error {
	if req == nil {
		return &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil SendChatRequest",
		}
	}
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SendChat{
			SendChat: req,
		},
	})
	return err
}

// Moderate performs an outbound moderation action (delete, timeout, ban) on a connected platform.
func (h *Host) Moderate(req *pluginv1.ModerateRequest) error {
	if req == nil {
		return &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil ModerateRequest",
		}
	}
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Moderate{
			Moderate: req,
		},
	})
	return err
}

// GetVar retrieves a global or user bot variable from TownCrier.
func (h *Host) GetVar(scope pluginv1.VarScope, platform, userID, key string) (*pluginv1.ActionValue, bool, error) {
	resp, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_GetVar{
			GetVar: &pluginv1.GetVarRequest{
				Scope:    scope,
				Platform: platform,
				UserId:   userID,
				Key:      key,
			},
		},
	})
	if err != nil {
		return nil, false, err
	}
	gv := resp.GetGetVar()
	if gv == nil {
		return nil, false, nil
	}
	return gv.GetValue(), gv.GetFound(), nil
}

// SetVar stores a global or user bot variable in TownCrier.
func (h *Host) SetVar(scope pluginv1.VarScope, platform, userID, key string, val *pluginv1.ActionValue) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetVar{
			SetVar: &pluginv1.SetVarRequest{
				Scope:    scope,
				Platform: platform,
				UserId:   userID,
				Key:      key,
				Value:    val,
			},
		},
	})
	return err
}

// DeleteVar deletes a global or user bot variable in TownCrier.
func (h *Host) DeleteVar(scope pluginv1.VarScope, platform, userID, key string) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetVar{
			SetVar: &pluginv1.SetVarRequest{
				Scope:    scope,
				Platform: platform,
				UserId:   userID,
				Key:      key,
				Delete:   true,
			},
		},
	})
	return err
}

// EmitTrigger fires a custom trigger event into TownCrier's bot automation engine.
func (h *Host) EmitTrigger(triggerID string, args *pluginv1.ActionArgs) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_EmitTrigger{
			EmitTrigger: &pluginv1.EmitTriggerRequest{
				TriggerId: triggerID,
				Args:      args,
			},
		},
	})
	return err
}

// RunBotAction invokes a configured TownCrier bot action by ID or name.
func (h *Host) RunBotAction(actionIDOrName string, args *pluginv1.ActionArgs, runImmediately bool) error {
	_, err := h.call(&pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_RunBotAction{
			RunBotAction: &pluginv1.RunBotActionRequest{
				ActionIdOrName: actionIDOrName,
				Args:           args,
				RunImmediately: runImmediately,
			},
		},
	})
	return err
}

// ActionValueString constructs a string ActionValue.
func ActionValueString(v string) *pluginv1.ActionValue {
	return &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_StringVal{StringVal: v}}
}

// ActionValueInt constructs an int64 ActionValue.
func ActionValueInt(v int64) *pluginv1.ActionValue {
	return &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_IntVal{IntVal: v}}
}

// ActionValueFloat constructs a float64 ActionValue.
func ActionValueFloat(v float64) *pluginv1.ActionValue {
	return &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_DoubleVal{DoubleVal: v}}
}

// ActionValueBool constructs a bool ActionValue.
func ActionValueBool(v bool) *pluginv1.ActionValue {
	return &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_BoolVal{BoolVal: v}}
}

// ActionArgsFromMap converts a Go map[string]any into a protobuf ActionArgs message.
func ActionArgsFromMap(m map[string]any) *pluginv1.ActionArgs {
	out := &pluginv1.ActionArgs{Args: make(map[string]*pluginv1.ActionValue, len(m))}
	for k, v := range m {
		switch tv := v.(type) {
		case string:
			out.Args[k] = ActionValueString(tv)
		case int:
			out.Args[k] = ActionValueInt(int64(tv))
		case int64:
			out.Args[k] = ActionValueInt(tv)
		case int32:
			out.Args[k] = ActionValueInt(int64(tv))
		case float64:
			out.Args[k] = ActionValueFloat(tv)
		case float32:
			out.Args[k] = ActionValueFloat(float64(tv))
		case bool:
			out.Args[k] = ActionValueBool(tv)
		case *pluginv1.ActionValue:
			if tv != nil {
				out.Args[k] = tv
			}
		default:
			if tv != nil {
				out.Args[k] = ActionValueString(fmt.Sprint(tv))
			}
		}
	}
	return out
}

// ActionArgsToMap converts a protobuf ActionArgs message into a Go map[string]any.
func ActionArgsToMap(a *pluginv1.ActionArgs) map[string]any {
	if a == nil || len(a.GetArgs()) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(a.GetArgs()))
	for k, v := range a.GetArgs() {
		if v == nil {
			continue
		}
		switch tv := v.Kind.(type) {
		case *pluginv1.ActionValue_StringVal:
			out[k] = tv.StringVal
		case *pluginv1.ActionValue_IntVal:
			out[k] = tv.IntVal
		case *pluginv1.ActionValue_DoubleVal:
			out[k] = tv.DoubleVal
		case *pluginv1.ActionValue_BoolVal:
			out[k] = tv.BoolVal
		}
	}
	return out
}

// GetArgString reads a string argument from ActionArgs with fallback.
func GetArgString(a *pluginv1.ActionArgs, key, fallback string) string {
	if a == nil || a.Args == nil {
		return fallback
	}
	av := a.Args[key]
	if av == nil {
		return fallback
	}
	switch v := av.Kind.(type) {
	case *pluginv1.ActionValue_StringVal:
		return v.StringVal
	case *pluginv1.ActionValue_IntVal:
		return strconv.FormatInt(v.IntVal, 10)
	case *pluginv1.ActionValue_DoubleVal:
		return fmt.Sprintf("%g", v.DoubleVal)
	case *pluginv1.ActionValue_BoolVal:
		if v.BoolVal {
			return "true"
		}
		return "false"
	}
	return fallback
}

// EnsureActionArgs initializes args (and its map) if nil and returns it.
func EnsureActionArgs(a *pluginv1.ActionArgs) *pluginv1.ActionArgs {
	if a == nil {
		return &pluginv1.ActionArgs{Args: make(map[string]*pluginv1.ActionValue)}
	}
	if a.Args == nil {
		a.Args = make(map[string]*pluginv1.ActionValue)
	}
	return a
}

// SetArgString sets a string argument inside ActionArgs.
func SetArgString(a *pluginv1.ActionArgs, key, val string) *pluginv1.ActionArgs {
	a = EnsureActionArgs(a)
	a.Args[key] = &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_StringVal{StringVal: val}}
	return a
}

// SetArgInt sets an int64 argument inside ActionArgs.
func SetArgInt(a *pluginv1.ActionArgs, key string, val int64) *pluginv1.ActionArgs {
	a = EnsureActionArgs(a)
	a.Args[key] = &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_IntVal{IntVal: val}}
	return a
}

// SetArgFloat sets a float64 argument inside ActionArgs.
func SetArgFloat(a *pluginv1.ActionArgs, key string, val float64) *pluginv1.ActionArgs {
	a = EnsureActionArgs(a)
	a.Args[key] = &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_DoubleVal{DoubleVal: val}}
	return a
}

// SetArgBool sets a bool argument inside ActionArgs.
func SetArgBool(a *pluginv1.ActionArgs, key string, val bool) *pluginv1.ActionArgs {
	a = EnsureActionArgs(a)
	a.Args[key] = &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_BoolVal{BoolVal: val}}
	return a
}

// EnsureSettingsMap initializes m (and its map) if nil and returns it.
func EnsureSettingsMap(m *pluginv1.SettingsMap) *pluginv1.SettingsMap {
	if m == nil {
		return &pluginv1.SettingsMap{Values: make(map[string]*pluginv1.SettingValue)}
	}
	if m.Values == nil {
		m.Values = make(map[string]*pluginv1.SettingValue)
	}
	return m
}

// GetSettingString reads a string setting from m (handling string_val and info_val/link_val fallbacks).
func GetSettingString(m *pluginv1.SettingsMap, key, fallback string) string {
	if m == nil || m.Values == nil {
		return fallback
	}
	sv, ok := m.Values[key]
	if !ok || sv == nil {
		return fallback
	}
	switch v := sv.Kind.(type) {
	case *pluginv1.SettingValue_StringVal:
		return v.StringVal
	case *pluginv1.SettingValue_InfoVal:
		if v.InfoVal != nil {
			return v.InfoVal.GetText()
		}
	case *pluginv1.SettingValue_LinkVal:
		if v.LinkVal != nil {
			return v.LinkVal.GetUrl()
		}
	case *pluginv1.SettingValue_NumberVal:
		return fmt.Sprintf("%g", v.NumberVal)
	case *pluginv1.SettingValue_BoolVal:
		if v.BoolVal {
			return "true"
		}
		return "false"
	}
	return fallback
}

// GetSettingNumber reads a numeric setting from m, returning fallback when missing.
func GetSettingNumber(m *pluginv1.SettingsMap, key string, fallback float64) float64 {
	if m == nil || m.Values == nil {
		return fallback
	}
	sv, ok := m.Values[key]
	if !ok || sv == nil {
		return fallback
	}
	if v, ok := sv.Kind.(*pluginv1.SettingValue_NumberVal); ok {
		return v.NumberVal
	}
	return fallback
}

// GetSettingBool reads a boolean setting from m, returning fallback when missing.
func GetSettingBool(m *pluginv1.SettingsMap, key string, fallback bool) bool {
	if m == nil || m.Values == nil {
		return fallback
	}
	sv, ok := m.Values[key]
	if !ok || sv == nil {
		return fallback
	}
	if v, ok := sv.Kind.(*pluginv1.SettingValue_BoolVal); ok {
		return v.BoolVal
	}
	return fallback
}

// SetSettingString sets a string value in m, allocating m if nil.
func SetSettingString(m *pluginv1.SettingsMap, key, val string) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_StringVal{StringVal: val},
	}
	return m
}

// SetSettingNumber sets a numeric value in m, allocating m if nil.
func SetSettingNumber(m *pluginv1.SettingsMap, key string, val float64) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_NumberVal{NumberVal: val},
	}
	return m
}

// SetSettingBool sets a boolean value in m, allocating m if nil.
func SetSettingBool(m *pluginv1.SettingsMap, key string, val bool) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_BoolVal{BoolVal: val},
	}
	return m
}

// SetSettingInfo sets a read-only info field value in m.
func SetSettingInfo(m *pluginv1.SettingsMap, key, text string, copyable bool) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_InfoVal{
			InfoVal: &pluginv1.InfoValue{Text: text, Copyable: copyable},
		},
	}
	return m
}

// SetSettingLink sets a clickable link field value in m.
func SetSettingLink(m *pluginv1.SettingsMap, key, rawURL, label string) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_LinkVal{
			LinkVal: &pluginv1.LinkValue{Url: rawURL, Label: label},
		},
	}
	return m
}

// SetSettingButton sets a button field's label and disabled state in m.
func SetSettingButton(m *pluginv1.SettingsMap, key, label string, disabled bool) *pluginv1.SettingsMap {
	m = EnsureSettingsMap(m)
	m.Values[key] = &pluginv1.SettingValue{
		Kind: &pluginv1.SettingValue_ButtonVal{
			ButtonVal: &pluginv1.ButtonValue{Label: label, Disabled: disabled},
		},
	}
	return m
}
