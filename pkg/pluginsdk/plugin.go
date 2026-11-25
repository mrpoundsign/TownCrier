package pluginsdk

import (
	"fmt"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// Plugin defines the lifecycle and event callbacks implemented by a TownCrier plugin.
// The same implementation builds cleanly as either a WASI .wasm module or a native executable.
type Plugin interface {
	Manifest() *pluginv1.Manifest
	Init(host *Host) error
	OnEvent(ev *pluginv1.StreamEvent) error
	OnModeration(ev *pluginv1.ModerationEvent) error
	OnTTSLifecycle(ev *pluginv1.TTSSpeechLifecycleEvent) error
	OnTimer(timerID, ctx string) error
	OnGlobalSettings(settings *pluginv1.SettingsMap) error
	OnConfigAction(actionKey string, settings *pluginv1.SettingsMap) error
	OnHTTPResult(asyncID string, resp *pluginv1.HTTPResponse) error
	Shutdown() error
}

// BasePlugin provides safe no-op default implementations for optional Plugin callbacks.
type BasePlugin struct{}

func (BasePlugin) Init(_ *Host) error                                    { return nil }
func (BasePlugin) OnEvent(_ *pluginv1.StreamEvent) error                 { return nil }
func (BasePlugin) OnModeration(_ *pluginv1.ModerationEvent) error        { return nil }
func (BasePlugin) OnTTSLifecycle(_ *pluginv1.TTSSpeechLifecycleEvent) error { return nil }
func (BasePlugin) OnTimer(_, _ string) error                             { return nil }
func (BasePlugin) OnGlobalSettings(_ *pluginv1.SettingsMap) error        { return nil }
func (BasePlugin) OnConfigAction(_ string, _ *pluginv1.SettingsMap) error { return nil }
func (BasePlugin) OnHTTPResult(_ string, _ *pluginv1.HTTPResponse) error { return nil }
func (BasePlugin) Shutdown() error                                       { return nil }

var registeredPlugin Plugin

// Register sets the singleton Plugin served by this binary or Wasm module.
// Call Register from init() or main() before Run().
func Register(p Plugin) {
	registeredPlugin = p
}

// Registered returns the plugin registered via Register, or nil if none has been registered.
func Registered() Plugin {
	return registeredPlugin
}

func dispatchEnvelope(p Plugin, host *Host, env *pluginv1.HostEnvelope) *pluginv1.PluginEnvelope {
	if env == nil {
		return ackError(0, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "nil host envelope",
		})
	}
	if p == nil {
		return ackError(env.GetRequestId(), &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
			Message: "no plugin registered via pluginsdk.Register",
		})
	}

	reqID := env.GetRequestId()
	switch payload := env.Payload.(type) {
	case *pluginv1.HostEnvelope_GetManifest:
		m := p.Manifest()
		if m == nil {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_Manifest{
					Manifest: &pluginv1.ManifestResponse{
						Error: &pluginv1.APIError{
							Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
							Message: "plugin returned nil manifest",
						},
					},
				},
			}
		}
		return &pluginv1.PluginEnvelope{
			RequestId: reqID,
			Payload: &pluginv1.PluginEnvelope_Manifest{
				Manifest: &pluginv1.ManifestResponse{
					Manifest: m,
				},
			},
		}

	case *pluginv1.HostEnvelope_Init:
		if err := p.Init(host); err != nil {
			return ackError(reqID, err)
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_Shutdown:
		if err := p.Shutdown(); err != nil {
			return ackError(reqID, err)
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_OnEvent:
		if payload.OnEvent == nil {
			return ackError(reqID, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
				Message: "empty on_event payload",
			})
		}
		switch ev := payload.OnEvent.Event.(type) {
		case *pluginv1.OnEventRequest_StreamEvent:
			if err := p.OnEvent(ev.StreamEvent); err != nil {
				return ackError(reqID, err)
			}
		case *pluginv1.OnEventRequest_ModerationEvent:
			if err := p.OnModeration(ev.ModerationEvent); err != nil {
				return ackError(reqID, err)
			}
		case *pluginv1.OnEventRequest_TtsLifecycle:
			if err := p.OnTTSLifecycle(ev.TtsLifecycle); err != nil {
				return ackError(reqID, err)
			}
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_OnTimer:
		if payload.OnTimer != nil {
			if err := p.OnTimer(payload.OnTimer.GetTimerId(), payload.OnTimer.GetContext()); err != nil {
				return ackError(reqID, err)
			}
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_OnGlobalSettings:
		var settings *pluginv1.SettingsMap
		if payload.OnGlobalSettings != nil {
			settings = payload.OnGlobalSettings.GetSettings()
		}
		if err := p.OnGlobalSettings(settings); err != nil {
			return ackError(reqID, err)
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_OnConfigAction:
		if payload.OnConfigAction == nil {
			return ackError(reqID, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
				Message: "empty on_config_action payload",
			})
		}
		if err := p.OnConfigAction(payload.OnConfigAction.GetActionKey(), payload.OnConfigAction.GetSettings()); err != nil {
			return ackError(reqID, err)
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_OnHttpResult:
		if payload.OnHttpResult != nil {
			if err := p.OnHTTPResult(payload.OnHttpResult.GetAsyncId(), payload.OnHttpResult.GetResponse()); err != nil {
				return ackError(reqID, err)
			}
		}
		return ackOK(reqID)

	default:
		return ackError(reqID, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
			Message: fmt.Sprintf("plugin does not handle envelope payload %T", env.Payload),
		})
	}
}

func ackOK(reqID uint64) *pluginv1.PluginEnvelope {
	return &pluginv1.PluginEnvelope{
		RequestId: reqID,
		Payload: &pluginv1.PluginEnvelope_Ack{
			Ack: &pluginv1.AckResponse{Ok: true},
		},
	}
}

func ackError(reqID uint64, err error) *pluginv1.PluginEnvelope {
	return &pluginv1.PluginEnvelope{
		RequestId: reqID,
		Payload: &pluginv1.PluginEnvelope_Ack{
			Ack: &pluginv1.AckResponse{
				Ok:    false,
				Error: ToProto(err),
			},
		},
	}
}
