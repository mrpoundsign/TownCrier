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

// UIPlugin is an optional interface implemented by plugins that expose declarative Fyne UI views ("ui.v1").
type UIPlugin interface {
	RenderView(viewID string) (*pluginv1.UISnapshot, error)
	OnUIEvent(ev *pluginv1.UIEvent) (*pluginv1.UIPatchBatch, error)
}

// OverlayAssetPlugin is an optional interface implemented by plugins serving OBS browser overlay assets ("overlay.routes.v1").
type OverlayAssetPlugin interface {
	GetOverlayAsset(assetPath string) (contentType string, body []byte, found bool, err error)
}

// OverlayStylePlugin is an optional interface implemented by plugins that react to Designer overlay style updates ("overlay.widgets.v1").
type OverlayStylePlugin interface {
	OnOverlayStyleChanged(req *pluginv1.OnOverlayStyleChangedRequest) error
}

// BeforeTTSPlugin is an optional interface implemented by plugins that intercept or rewrite TTS utterances ("bot.before_tts.v1").
type BeforeTTSPlugin interface {
	BeforeTTS(req *pluginv1.BeforeTTSRequest) (*pluginv1.BeforeTTSResponse, error)
}

// ActionPlugin is an optional interface implemented by plugins that expose automatable actions ("bot.actions.v1").
type ActionPlugin interface {
	RunAction(req *pluginv1.RunActionRequest) (*pluginv1.RunActionResponse, error)
}

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

	case *pluginv1.HostEnvelope_RenderView:
		up, ok := p.(UIPlugin)
		if !ok {
			return ackError(reqID, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
				Message: "plugin does not implement declarative UI views",
			})
		}
		viewID := ""
		if payload.RenderView != nil {
			viewID = payload.RenderView.GetViewId()
		}
		snap, err := up.RenderView(viewID)
		if err != nil {
			return ackError(reqID, err)
		}
		if snap == nil {
			return ackError(reqID, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
				Message: "plugin returned nil UISnapshot",
			})
		}
		if snap.GetViewId() == "" {
			snap.ViewId = viewID
		}
		return &pluginv1.PluginEnvelope{
			RequestId: reqID,
			Payload:   &pluginv1.PluginEnvelope_UiSnapshot{UiSnapshot: snap},
		}

	case *pluginv1.HostEnvelope_OnUiEvent:
		up, ok := p.(UIPlugin)
		if !ok {
			return ackError(reqID, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
				Message: "plugin does not implement declarative UI views",
			})
		}
		batch, err := up.OnUIEvent(payload.OnUiEvent)
		if err != nil {
			return ackError(reqID, err)
		}
		if batch != nil {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload:   &pluginv1.PluginEnvelope_UiPatch{UiPatch: batch},
			}
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_GetOverlayAsset:
		op, ok := p.(OverlayAssetPlugin)
		if !ok {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_GetOverlayAssetResponse{
					GetOverlayAssetResponse: &pluginv1.GetOverlayAssetResponse{
						Found: false,
						Error: &pluginv1.APIError{
							Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
							Message: "plugin does not expose OBS overlay routes",
						},
					},
				},
			}
		}
		assetPath := ""
		if payload.GetOverlayAsset != nil {
			assetPath = payload.GetOverlayAsset.GetPath()
		}
		ct, body, found, err := op.GetOverlayAsset(assetPath)
		if err != nil {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_GetOverlayAssetResponse{
					GetOverlayAssetResponse: &pluginv1.GetOverlayAssetResponse{
						Found: false,
						Error: ToProto(err),
					},
				},
			}
		}
		return &pluginv1.PluginEnvelope{
			RequestId: reqID,
			Payload: &pluginv1.PluginEnvelope_GetOverlayAssetResponse{
				GetOverlayAssetResponse: &pluginv1.GetOverlayAssetResponse{
					Found:       found,
					ContentType: ct,
					Body:        body,
				},
			},
		}

	case *pluginv1.HostEnvelope_OnOverlayStyleChanged:
		if sp, ok := p.(OverlayStylePlugin); ok {
			req := payload.OnOverlayStyleChanged
			if req == nil {
				req = &pluginv1.OnOverlayStyleChangedRequest{}
			}
			if err := sp.OnOverlayStyleChanged(req); err != nil {
				return ackError(reqID, err)
			}
		}
		return ackOK(reqID)

	case *pluginv1.HostEnvelope_BeforeTts:
		bp, ok := p.(BeforeTTSPlugin)
		if !ok {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_BeforeTtsResponse{
					BeforeTtsResponse: &pluginv1.BeforeTTSResponse{},
				},
			}
		}
		resp, err := bp.BeforeTTS(payload.BeforeTts)
		if err != nil {
			return ackError(reqID, err)
		}
		if resp == nil {
			resp = &pluginv1.BeforeTTSResponse{}
		}
		return &pluginv1.PluginEnvelope{
			RequestId: reqID,
			Payload: &pluginv1.PluginEnvelope_BeforeTtsResponse{
				BeforeTtsResponse: resp,
			},
		}

	case *pluginv1.HostEnvelope_RunAction:
		ap, ok := p.(ActionPlugin)
		if !ok {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_RunActionResponse{
					RunActionResponse: &pluginv1.RunActionResponse{
						ContinueChain: true,
						Error: &pluginv1.APIError{
							Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
							Message: "plugin does not implement RunAction",
						},
					},
				},
			}
		}
		resp, err := ap.RunAction(payload.RunAction)
		if err != nil {
			return &pluginv1.PluginEnvelope{
				RequestId: reqID,
				Payload: &pluginv1.PluginEnvelope_RunActionResponse{
					RunActionResponse: &pluginv1.RunActionResponse{
						ContinueChain: false,
						Error:         ToProto(err),
					},
				},
			}
		}
		if resp == nil {
			resp = &pluginv1.RunActionResponse{ContinueChain: true}
		}
		return &pluginv1.PluginEnvelope{
			RequestId: reqID,
			Payload: &pluginv1.PluginEnvelope_RunActionResponse{
				RunActionResponse: resp,
			},
		}

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
