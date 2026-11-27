package pluginhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestCheckVersionAndCapabilities(t *testing.T) {
	t.Parallel()
	require.Nil(t, CheckVersion(1, 0))

	errZero := CheckVersion(0, 0)
	require.NotNil(t, errZero)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errZero.GetCode())

	errMajor := CheckVersion(2, 0)
	require.NotNil(t, errMajor)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errMajor.GetCode())
	require.Contains(t, errMajor.GetMessage(), "update TownCrier")

	errMinor := CheckVersion(1, 99)
	require.NotNil(t, errMinor)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errMinor.GetCode())
	require.Contains(t, errMinor.GetMessage(), "v1.99+")

	require.Nil(t, CheckCapabilities([]string{"core.v1", "kv.v1"}))
	errCap := CheckCapabilities([]string{"core.v1", "future.v9"})
	require.NotNil(t, errCap)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY, errCap.GetCode())
	require.Equal(t, "future.v9", errCap.GetMissingCapability())
}

func TestKVStoreLifecycle(t *testing.T) {
	t.Parallel()
	kv := NewKVStore(":memory:")
	defer func() { _ = kv.Close() }()
	ctx := context.Background()

	_, found, apiErr := kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.False(t, found)

	require.Nil(t, kv.Set(ctx, "com.example.p", "count", []byte("42")))
	require.Nil(t, kv.Set(ctx, "com.example.p", "user:alice", []byte("10")))
	require.Nil(t, kv.Set(ctx, "com.example.other", "count", []byte("99")))

	val, found, apiErr := kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.True(t, found)
	require.Equal(t, []byte("42"), val)

	keys, apiErr := kv.List(ctx, "com.example.p", "user:", 0)
	require.Nil(t, apiErr)
	require.Equal(t, []string{"user:alice"}, keys)

	require.Nil(t, kv.Delete(ctx, "com.example.p", "count"))
	_, found, apiErr = kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.False(t, found)

	// Other plugin namespace untouched
	valOther, foundOther, apiErr := kv.Get(ctx, "com.example.other", "count")
	require.Nil(t, apiErr)
	require.True(t, foundOther)
	require.Equal(t, []byte("99"), valOther)
}

func TestIsHostAllowedAndExecuteHTTP(t *testing.T) {
	t.Parallel()
	require.True(t, IsHostAllowed("https://api.twitch.tv/helix/users", []string{"api.twitch.tv"}))
	require.True(t, IsHostAllowed("https://sub.example.com/path", []string{"*.example.com"}))
	require.False(t, IsHostAllowed("https://example.com/path", []string{"*.example.com"}))
	require.False(t, IsHostAllowed("http://user:pass@api.twitch.tv/", []string{"api.twitch.tv"}))
	require.False(t, IsHostAllowed("file:///etc/passwd", []string{"*"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	_, apiErr := ExecuteHTTP(context.Background(), srv.Client(), []string{"example.com"}, true, &pluginv1.HTTPRequest{
		Method: "GET",
		Url:    srv.URL,
	})
	require.NotNil(t, apiErr)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, apiErr.GetCode())

	resp, apiErr := ExecuteHTTP(context.Background(), srv.Client(), []string{"127.0.0.1:*"}, true, &pluginv1.HTTPRequest{
		Method: "GET",
		Url:    srv.URL,
	})
	require.Nil(t, apiErr)
	require.Equal(t, int32(200), resp.GetStatus())
	require.Equal(t, []byte("pong"), resp.GetBody())
	require.Equal(t, "ok", resp.GetHeaders()["X-Test"])
}

func TestSettingsKeyringRouting(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	sec := secrets.NewMemoryStore()
	mgr := NewManager(Options{
		KVDBPath: ":memory:",
		Secrets:  sec,
		Callbacks: HostCallbacks{
			GetConfig: func() *config.Config { return cfg.Clone() },
			UpdateConfig: func(fn func(c *config.Config)) error {
				fn(cfg)
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	uuid := "com.example.secret"
	mgr.entries[uuid] = &pluginEntry{
		uuid: uuid,
		manifest: &pluginv1.Manifest{
			Uuid: uuid,
			Name: "Secret Plugin",
			GlobalSettingsSchema: []*pluginv1.SettingField{
				{Key: "greeting", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_TEXT},
				{Key: "apiKey", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD},
			},
		},
	}

	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "hi",
		"apiKey":   "super-secret",
	}, nil))

	// Secret must NOT live in config.json map, MUST live in secrets.Store
	_, inConfig := cfg.Plugins.PluginSettings[uuid]["apiKey"]
	require.False(t, inConfig)
	require.Equal(t, "hi", cfg.Plugins.PluginSettings[uuid]["greeting"])
	require.True(t, mgr.HasSecret(uuid, "apiKey"))

	merged := mgr.GetGlobalSettings(uuid)
	require.Equal(t, "hi", merged.GetValues()["greeting"].GetStringVal())
	require.Equal(t, "super-secret", merged.GetValues()["apiKey"].GetStringVal())

	// Omitting password preserves existing secret; explicit clear removes it
	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "yo",
	}, nil))
	require.True(t, mgr.HasSecret(uuid, "apiKey"))

	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "yo",
	}, []string{"apiKey"}))
	require.False(t, mgr.HasSecret(uuid, "apiKey"))
}

func TestNativeTrustGateAndTamperDetection(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()
	plugDir := filepath.Join(baseDir, "native", "demo")
	require.NoError(t, os.MkdirAll(plugDir, 0o755))

	binName := "demo.bin"
	binPath := filepath.Join(plugDir, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("binary-v1"), 0o755))

	manifestJSON := `{
		"uuid": "com.example.native.demo",
		"name": "Demo Native",
		"version": "1.0.0",
		"binaries": {
			"linux/amd64": "demo.bin",
			"windows/amd64": "demo.bin",
			"darwin/arm64": "demo.bin",
			"darwin/amd64": "demo.bin"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(plugDir, "plugin.json"), []byte(manifestJSON), 0o644))

	cfg := config.DefaultConfig()
	mgr := NewManager(Options{
		BaseDir:  baseDir,
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			GetConfig: func() *config.Config { return cfg.Clone() },
			UpdateConfig: func(fn func(c *config.Config)) error {
				fn(cfg)
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.Start())
	list := mgr.Plugins()
	require.Len(t, list, 1)
	require.Equal(t, StatusAwaitingTrust, list[0].State)
	origHash := list[0].BinarySHA256
	require.NotEmpty(t, origHash)

	// Record trust manually for origHash, then tamper with binary -> must revert to StatusAwaitingTrust
	cfg.Plugins.TrustedNative["com.example.native.demo"] = config.TrustedNativePlugin{SHA256: origHash}
	require.NoError(t, os.WriteFile(binPath, []byte("binary-tampered"), 0o755))
	require.NoError(t, mgr.Reload())
	list = mgr.Plugins()
	require.Len(t, list, 1)
	require.Equal(t, StatusAwaitingTrust, list[0].State)
	require.NotEqual(t, origHash, list[0].BinarySHA256)
}

func TestNativeLinkDiscoveryAndSecurityBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	baseDir := filepath.Join(root, "towncrier-plugins")
	nativeDir := filepath.Join(baseDir, "native")
	require.NoError(t, os.MkdirAll(nativeDir, 0o755))

	externalDir := filepath.Join(root, "CrowdDJ")
	require.NoError(t, os.MkdirAll(externalDir, 0o755))
	binPath := filepath.Join(externalDir, "demo.bin")
	require.NoError(t, os.WriteFile(binPath, []byte("crowddj-bin"), 0o755))

	manifestJSON := `{
		"uuid": "com.example.native.linked",
		"name": "Linked Native",
		"version": "1.0.0",
		"binaries": {
			"linux/amd64": "demo.bin",
			"windows/amd64": "demo.bin",
			"darwin/arm64": "demo.bin",
			"darwin/amd64": "demo.bin"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(externalDir, "plugin.json"), []byte(manifestJSON), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(nativeDir, "crowddj.link.json"),
		[]byte(`{"plugin_dir":"`+filepath.ToSlash(externalDir)+`"}`),
		0o644,
	))

	// Invalid links (non-absolute, malformed JSON, missing target) must be ignored without breaking discovery.
	require.NoError(t, os.WriteFile(filepath.Join(nativeDir, "rel.link.json"), []byte(`{"plugin_dir":"relative/dir"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nativeDir, "bad.link.json"), []byte(`{not-json`), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(nativeDir, "missing.link.json"),
		[]byte(`{"plugin_dir":"`+filepath.ToSlash(filepath.Join(root, "MissingApp"))+`"}`),
		0o644,
	))

	// Escaping binary path inside a linked plugin.json must still fail validation.
	escapeDir := filepath.Join(root, "EscapePlugin")
	require.NoError(t, os.MkdirAll(escapeDir, 0o755))
	escapeManifest := `{
		"uuid": "com.example.native.escape",
		"name": "Escape Native",
		"version": "1.0.0",
		"binaries": {
			"linux/amd64": "../CrowdDJ/demo.bin",
			"windows/amd64": "../CrowdDJ/demo.bin",
			"darwin/arm64": "../CrowdDJ/demo.bin",
			"darwin/amd64": "../CrowdDJ/demo.bin"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(escapeDir, "plugin.json"), []byte(escapeManifest), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(nativeDir, "escape.link.json"),
		[]byte(`{"plugin_dir":"`+filepath.ToSlash(escapeDir)+`"}`),
		0o644,
	))

	cfg := config.DefaultConfig()
	mgr := NewManager(Options{
		BaseDir:  baseDir,
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			GetConfig: func() *config.Config { return cfg.Clone() },
		},
	})
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.Start())
	list := mgr.Plugins()
	require.Len(t, list, 2)

	byUUID := make(map[string]PluginStatus, len(list))
	for _, st := range list {
		byUUID[st.UUID] = st
	}

	linked := byUUID["com.example.native.linked"]
	require.Equal(t, StatusAwaitingTrust, linked.State)
	require.NotEmpty(t, linked.BinarySHA256)

	escaped := byUUID["com.example.native.escape"]
	require.Equal(t, StatusError, escaped.State)
	require.Contains(t, escaped.Error, "escapes plugin directory")
}

func TestValidateManifestUIViewsPermission(t *testing.T) {
	t.Parallel()
	apiErr := ValidateManifest(&pluginv1.Manifest{
		Uuid: "com.example.noui",
		Name: "No UI Perm",
		Views: []*pluginv1.ViewDescriptor{
			{Id: "main", Title: "Main", Placement: pluginv1.ViewPlacement_VIEW_PLACEMENT_MAIN_TAB},
		},
	})
	require.NotNil(t, apiErr)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, apiErr.GetCode())

	require.Nil(t, ValidateManifest(&pluginv1.Manifest{
		Uuid: "com.example.ui",
		Name: "UI Perm",
		Permissions: &pluginv1.Permissions{
			UiViews: true,
		},
		Views: []*pluginv1.ViewDescriptor{
			{Id: "main", Title: "Main", Placement: pluginv1.ViewPlacement_VIEW_PLACEMENT_MAIN_TAB},
		},
	}))
}

func TestPushUIUpdateHostCallAndSubscription(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{KVDBPath: ":memory:", Secrets: secrets.NewMemoryStore()})
	defer func() { _ = mgr.Close() }()

	var got []PluginUIUpdate
	unsub := mgr.SubscribeUIUpdate(func(u PluginUIUpdate) {
		got = append(got, u)
	})
	defer unsub()

	entNoPerm := &pluginEntry{
		uuid: "com.example.noperm",
		manifest: &pluginv1.Manifest{
			Uuid: "com.example.noperm",
			Permissions: &pluginv1.Permissions{
				UiViews: false,
			},
		},
	}
	resp := mgr.handleHostCall(context.Background(), entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_PushUiPatch{
			PushUiPatch: &pluginv1.UIPatchBatch{ViewId: "main", BaseRevision: 1, Revision: 2},
		},
	})
	require.False(t, resp.GetOk())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, resp.GetError().GetCode())

	entOK := &pluginEntry{
		uuid: "com.example.ui",
		manifest: &pluginv1.Manifest{
			Uuid: "com.example.ui",
			Permissions: &pluginv1.Permissions{
				UiViews: true,
			},
		},
	}
	resp = mgr.handleHostCall(context.Background(), entOK, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_PushUiSnapshot{
			PushUiSnapshot: &pluginv1.UISnapshot{ViewId: "main", Revision: 1},
		},
	})
	require.True(t, resp.GetOk())
	resp = mgr.handleHostCall(context.Background(), entOK, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_PushUiPatch{
			PushUiPatch: &pluginv1.UIPatchBatch{ViewId: "main", BaseRevision: 1, Revision: 2},
		},
	})
	require.True(t, resp.GetOk())
	require.Len(t, got, 2)
	require.NotNil(t, got[0].Snapshot)
	require.NotNil(t, got[1].Patch)
}

type fakeBotBackend struct {
	dispatchFn func(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error)
}

func (f *fakeBotBackend) Dispatch(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
	return f.dispatchFn(ctx, env)
}

func (f *fakeBotBackend) SetTimer(_, _ string, _ uint32) *pluginv1.APIError { return nil }
func (f *fakeBotBackend) Close() error                                      { return nil }

func TestBeforeTTSAndRunPluginActionAndBotHostCalls(t *testing.T) {
	t.Parallel()
	var sentChat *pluginv1.SendChatRequest
	var modReq *pluginv1.ModerateRequest
	var setVarReq *pluginv1.SetVarRequest
	var emittedTrig *pluginv1.EmitTriggerRequest
	var ranAction *pluginv1.RunBotActionRequest

	mgr := NewManager(Options{
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			OnSendChat: func(_ context.Context, _ string, req *pluginv1.SendChatRequest) error {
				sentChat = req
				return nil
			},
			OnModerate: func(_ context.Context, _ string, req *pluginv1.ModerateRequest) error {
				modReq = req
				return nil
			},
			OnGetVar: func(_ context.Context, _ string, req *pluginv1.GetVarRequest) (*pluginv1.ActionValue, bool, error) {
				if req.GetKey() == "points" {
					return &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_IntVal{IntVal: 77}}, true, nil
				}
				return nil, false, nil
			},
			OnSetVar: func(_ context.Context, _ string, req *pluginv1.SetVarRequest) error {
				setVarReq = req
				return nil
			},
			OnEmitTrigger: func(_ context.Context, _ string, req *pluginv1.EmitTriggerRequest) error {
				emittedTrig = req
				return nil
			},
			OnRunBotAction: func(_ context.Context, _ string, req *pluginv1.RunBotActionRequest) error {
				ranAction = req
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	ent := &pluginEntry{
		uuid:  "com.example.bot",
		name:  "Bot Plugin",
		state: StatusRunning,
		manifest: &pluginv1.Manifest{
			Uuid:             "com.example.bot",
			Name:             "Bot Plugin",
			SubscribedEvents: []string{"before_tts"},
			Permissions: &pluginv1.Permissions{
				ReadEvents: true,
				SendChat:   true,
				Moderate:   true,
				KvStorage:  true,
				RunActions: true,
			},
			Actions: []*pluginv1.ActionDescriptor{
				{Id: "shout", Name: "Shout"},
			},
		},
		logs: &logBuffer{},
		backend: &fakeBotBackend{
			dispatchFn: func(_ context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
				switch p := env.Payload.(type) {
				case *pluginv1.HostEnvelope_BeforeTts:
					if p.BeforeTts.GetProposedText() == "secret command" {
						return &pluginv1.PluginEnvelope{
							Payload: &pluginv1.PluginEnvelope_BeforeTtsResponse{
								BeforeTtsResponse: &pluginv1.BeforeTTSResponse{Consume: true},
							},
						}, nil
					}
					return &pluginv1.PluginEnvelope{
						Payload: &pluginv1.PluginEnvelope_BeforeTtsResponse{
							BeforeTtsResponse: &pluginv1.BeforeTTSResponse{
								OverrideText:  "rewritten text",
								OverrideVoice: "af_heart",
								OverrideSpeed: 1.25,
							},
						},
					}, nil
				case *pluginv1.HostEnvelope_RunAction:
					return &pluginv1.PluginEnvelope{
						Payload: &pluginv1.PluginEnvelope_RunActionResponse{
							RunActionResponse: &pluginv1.RunActionResponse{
								ContinueChain: true,
								UpdatedArgs: &pluginv1.ActionArgs{
									Args: map[string]*pluginv1.ActionValue{
										"result": {Kind: &pluginv1.ActionValue_StringVal{StringVal: "ok-" + p.RunAction.GetActionId()}},
									},
								},
							},
						},
					}, nil
				}
				return &pluginv1.PluginEnvelope{}, nil
			},
		},
	}
	mgr.entries[ent.uuid] = ent

	// InvokeBeforeTTS rewrite + consume paths
	consume, outTxt, outVoice, outSpd := mgr.InvokeBeforeTTS(context.Background(), &chat.Event{UserName: "alice"}, "hello", "af_bella", 1.0, 1, true)
	require.False(t, consume)
	require.Equal(t, "rewritten text", outTxt)
	require.Equal(t, "af_heart", outVoice)
	require.InDelta(t, 1.25, outSpd, 0.01)

	consume, _, _, _ = mgr.InvokeBeforeTTS(context.Background(), &chat.Event{UserName: "alice"}, "secret command", "af_bella", 1.0, 1, true)
	require.True(t, consume)

	// RunPluginAction
	cont, updatedArgs, err := mgr.RunPluginAction(context.Background(), ent.uuid, "shout", nil)
	require.NoError(t, err)
	require.True(t, cont)
	require.Equal(t, "ok-shout", updatedArgs.GetArgs()["result"].GetStringVal())

	// Bot Host calls permitted
	ctx := context.Background()
	require.True(t, mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SendChat{SendChat: &pluginv1.SendChatRequest{Platform: "twitch", Text: "hi"}},
	}).GetOk())
	require.Equal(t, "hi", sentChat.GetText())

	require.True(t, mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Moderate{Moderate: &pluginv1.ModerateRequest{Platform: "twitch", Action: "timeout", UserName: "troll", DurationSeconds: 60}},
	}).GetOk())
	require.Equal(t, "troll", modReq.GetUserName())

	gvResp := mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_GetVar{GetVar: &pluginv1.GetVarRequest{Key: "points"}},
	})
	require.True(t, gvResp.GetOk())
	require.True(t, gvResp.GetGetVar().GetFound())
	require.Equal(t, int64(77), gvResp.GetGetVar().GetValue().GetIntVal())

	require.True(t, mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetVar{SetVar: &pluginv1.SetVarRequest{Key: "points", Value: &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_IntVal{IntVal: 80}}}},
	}).GetOk())
	require.Equal(t, int64(80), setVarReq.GetValue().GetIntVal())

	require.True(t, mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_EmitTrigger{EmitTrigger: &pluginv1.EmitTriggerRequest{TriggerId: "on_custom"}},
	}).GetOk())
	require.Equal(t, "on_custom", emittedTrig.GetTriggerId())

	require.True(t, mgr.handleHostCall(ctx, ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_RunBotAction{RunBotAction: &pluginv1.RunBotActionRequest{ActionIdOrName: "act1", RunImmediately: true}},
	}).GetOk())
	require.Equal(t, "act1", ranAction.GetActionIdOrName())

	// Permission denied check
	entNoPerm := &pluginEntry{
		uuid:     "com.example.denied",
		manifest: &pluginv1.Manifest{Uuid: "com.example.denied", Permissions: &pluginv1.Permissions{}},
	}
	denyResp := mgr.handleHostCall(ctx, entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SendChat{SendChat: &pluginv1.SendChatRequest{Text: "hi"}},
	})
	require.False(t, denyResp.GetOk())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, denyResp.GetError().GetCode())
}

type stubBackend struct {
	dispatchFn func(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error)
}

func (s *stubBackend) Dispatch(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
	return s.dispatchFn(ctx, env)
}

func (s *stubBackend) SetTimer(_, _ string, _ uint32) *pluginv1.APIError { return nil }
func (s *stubBackend) Close() error                                        { return nil }

func TestBeforeTTSMiddlewareAndTimeoutBudget(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{KVDBPath: ":memory:", Secrets: secrets.NewMemoryStore()})
	defer func() { _ = mgr.Close() }()

	mgr.entries["com.example.rewriter"] = &pluginEntry{
		uuid:  "com.example.rewriter",
		name:  "Rewriter",
		state: StatusRunning,
		manifest: &pluginv1.Manifest{
			Uuid:                 "com.example.rewriter",
			Name:                 "Rewriter",
			RequiredCapabilities: []string{"bot.before_tts.v1"},
			SubscribedEvents:     []string{"before_tts"},
			Permissions:          &pluginv1.Permissions{ReadEvents: true},
		},
		backend: &stubBackend{
			dispatchFn: func(_ context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
				req := env.GetBeforeTts()
				return &pluginv1.PluginEnvelope{
					Payload: &pluginv1.PluginEnvelope_BeforeTtsResponse{
						BeforeTtsResponse: &pluginv1.BeforeTTSResponse{
							Consume:       req.GetEvent().GetText() == "suppress me",
							OverrideText:  "Rewritten: " + req.GetProposedText(),
							OverrideVoice: "bf_emma",
							OverrideSpeed: 1.25,
						},
					},
				}, nil
			},
		},
	}

	ev := &chat.Event{Platform: "twitch", Kind: chat.EventKindMessage, UserName: "alice", Text: "hello world"}
	consume, text, voice, speed := mgr.InvokeBeforeTTS(context.Background(), ev, "hello world", "af_heart", 1.0, 7, true)
	require.False(t, consume)
	require.Equal(t, "Rewritten: hello world", text)
	require.Equal(t, "bf_emma", voice)
	require.InDelta(t, 1.25, speed, 0.001)

	evSuppress := &chat.Event{Platform: "twitch", Kind: chat.EventKindMessage, UserName: "alice", Text: "suppress me"}
	consume, _, _, _ = mgr.InvokeBeforeTTS(context.Background(), evSuppress, "suppress me", "af_heart", 1.0, 7, true)
	require.True(t, consume)

	// Slow plugin must respect <= 500ms budget without blocking caller indefinitely.
	mgr.entries["com.example.rewriter"].backend = &stubBackend{
		dispatchFn: func(ctx context.Context, _ *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	start := time.Now()
	consume, text, _, _ = mgr.InvokeBeforeTTS(context.Background(), ev, "original", "af_heart", 1.0, 7, true)
	require.False(t, consume)
	require.Equal(t, "original", text)
	require.Less(t, time.Since(start), 900*time.Millisecond)
}

func TestRunPluginActionAndBotHostCalls(t *testing.T) {
	t.Parallel()
	var sentChat *pluginv1.SendChatRequest
	var modReq *pluginv1.ModerateRequest
	var emitted *pluginv1.EmitTriggerRequest
	var ranAction *pluginv1.RunBotActionRequest
	vars := map[string]*pluginv1.ActionValue{}

	mgr := NewManager(Options{
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			OnSendChat: func(_ context.Context, _ string, req *pluginv1.SendChatRequest) error {
				sentChat = req
				return nil
			},
			OnModerate: func(_ context.Context, _ string, req *pluginv1.ModerateRequest) error {
				modReq = req
				return nil
			},
			OnGetVar: func(_ context.Context, _ string, req *pluginv1.GetVarRequest) (*pluginv1.ActionValue, bool, error) {
				v, ok := vars[req.GetKey()]
				return v, ok, nil
			},
			OnSetVar: func(_ context.Context, _ string, req *pluginv1.SetVarRequest) error {
				if req.GetDelete() {
					delete(vars, req.GetKey())
				} else {
					vars[req.GetKey()] = req.GetValue()
				}
				return nil
			},
			OnEmitTrigger: func(_ context.Context, _ string, req *pluginv1.EmitTriggerRequest) error {
				emitted = req
				return nil
			},
			OnRunBotAction: func(_ context.Context, _ string, req *pluginv1.RunBotActionRequest) error {
				ranAction = req
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	entNoPerm := &pluginEntry{
		uuid:  "com.example.locked",
		name:  "Locked",
		state: StatusRunning,
		manifest: &pluginv1.Manifest{
			Uuid:        "com.example.locked",
			Permissions: &pluginv1.Permissions{},
		},
	}
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, mgr.handleHostCall(context.Background(), entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SendChat{SendChat: &pluginv1.SendChatRequest{Platform: "twitch", Text: "hi"}},
	}).GetError().GetCode())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, mgr.handleHostCall(context.Background(), entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Moderate{Moderate: &pluginv1.ModerateRequest{Platform: "twitch", Action: "timeout"}},
	}).GetError().GetCode())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, mgr.handleHostCall(context.Background(), entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetVar{SetVar: &pluginv1.SetVarRequest{Key: "x"}},
	}).GetError().GetCode())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, mgr.handleHostCall(context.Background(), entNoPerm, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_RunBotAction{RunBotAction: &pluginv1.RunBotActionRequest{ActionIdOrName: "act1"}},
	}).GetError().GetCode())

	ent := &pluginEntry{
		uuid:  "com.example.botplug",
		name:  "BotPlugin",
		state: StatusRunning,
		manifest: &pluginv1.Manifest{
			Uuid:                 "com.example.botplug",
			Name:                 "BotPlugin",
			RequiredCapabilities: []string{"bot.actions.v1", "bot.chat.v1", "bot.moderate.v1", "bot.variables.v1"},
			Permissions: &pluginv1.Permissions{
				SendChat:   true,
				Moderate:   true,
				KvStorage:  true,
				RunActions: true,
			},
			Actions: []*pluginv1.ActionDescriptor{{Id: "do_thing", Name: "Do Thing"}},
		},
		backend: &stubBackend{
			dispatchFn: func(_ context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
				ra := env.GetRunAction()
				return &pluginv1.PluginEnvelope{
					Payload: &pluginv1.PluginEnvelope_RunActionResponse{
						RunActionResponse: &pluginv1.RunActionResponse{
							ContinueChain: true,
							UpdatedArgs: pluginsdk.ActionArgsFromMap(map[string]any{
								"in":  pluginsdk.GetArgString(ra.GetArgs(), "in", ""),
								"out": "done",
							}),
						},
					},
				}, nil
			},
		},
	}
	mgr.entries[ent.uuid] = ent

	cont, outArgs, err := mgr.RunPluginAction(context.Background(), ent.uuid, "do_thing", pluginsdk.ActionArgsFromMap(map[string]any{"in": "abc"}))
	require.NoError(t, err)
	require.True(t, cont)
	require.Equal(t, "done", pluginsdk.GetArgString(outArgs, "out", ""))

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SendChat{SendChat: &pluginv1.SendChatRequest{Platform: "twitch", Text: "yo"}},
	}).GetOk())
	require.Equal(t, "yo", sentChat.GetText())

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_Moderate{Moderate: &pluginv1.ModerateRequest{Platform: "twitch", Action: "delete", MessageId: "m1"}},
	}).GetOk())
	require.Equal(t, "m1", modReq.GetMessageId())

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_SetVar{SetVar: &pluginv1.SetVarRequest{Scope: pluginv1.VarScope_VAR_SCOPE_GLOBAL_PERSISTENT, Key: "score", Value: pluginsdk.ActionValueInt(99)}},
	}).GetOk())
	gvResp := mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_GetVar{GetVar: &pluginv1.GetVarRequest{Scope: pluginv1.VarScope_VAR_SCOPE_GLOBAL_PERSISTENT, Key: "score"}},
	})
	require.True(t, gvResp.GetOk())
	require.True(t, gvResp.GetGetVar().GetFound())
	require.Equal(t, int64(99), gvResp.GetGetVar().GetValue().GetIntVal())

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_EmitTrigger{EmitTrigger: &pluginv1.EmitTriggerRequest{TriggerId: "custom_ev"}},
	}).GetOk())
	require.Equal(t, "custom_ev", emitted.GetTriggerId())

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_RunBotAction{RunBotAction: &pluginv1.RunBotActionRequest{ActionIdOrName: "welcome"}},
	}).GetOk())
	require.Equal(t, "welcome", ranAction.GetActionIdOrName())

	require.True(t, mgr.handleHostCall(context.Background(), ent, &pluginv1.PluginEnvelope{
		Payload: &pluginv1.PluginEnvelope_UpdateActionVisual{
			UpdateActionVisual: &pluginv1.ActionVisualState{
				ActionId:     "do_thing",
				Title:        "PLAY",
				Subtitle:     "PLAYING",
				Color:        "#22C55E",
				ImageDataUri: "data:image/svg+xml;utf8,abc",
			},
		},
	}).GetOk())
	plugins := mgr.Plugins()
	require.Len(t, plugins, 1)
	require.NotNil(t, plugins[0].ActionVisuals["do_thing"])
	require.Equal(t, "PLAYING", plugins[0].ActionVisuals["do_thing"].GetSubtitle())
}

func TestOverlayWidgetsCapabilityValidationAndStyleNotify(t *testing.T) {
	t.Parallel()
	require.Nil(t, CheckVersion(1, 1))
	require.Nil(t, CheckCapabilities([]string{"core.v1", "overlay.widgets.v1"}))

	// Declaring overlay_widgets without overlay permission/capability is rejected.
	errNoPerm := ValidateManifest(&pluginv1.Manifest{
		Uuid: "com.example.noperm",
		Name: "NoPerm",
		OverlayWidgets: []*pluginv1.OverlayWidgetDescriptor{
			{Id: "now_playing", Name: "Now Playing"},
		},
	})
	require.NotNil(t, errNoPerm)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, errNoPerm.GetCode())

	// Invalid widget id is rejected.
	errBadID := ValidateManifest(&pluginv1.Manifest{
		Uuid:                 "com.example.badid",
		Name:                 "BadID",
		RequiredCapabilities: []string{"overlay.widgets.v1"},
		OverlayWidgets: []*pluginv1.OverlayWidgetDescriptor{
			{Id: "../evil", Name: "Bad"},
		},
	})
	require.NotNil(t, errBadID)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, errBadID.GetCode())

	// Duplicate widget id is rejected.
	errDup := ValidateManifest(&pluginv1.Manifest{
		Uuid:                 "com.example.dup",
		Name:                 "Dup",
		RequiredCapabilities: []string{"overlay.widgets.v1"},
		OverlayWidgets: []*pluginv1.OverlayWidgetDescriptor{
			{Id: "card", Name: "Card 1"},
			{Id: "card", Name: "Card 2"},
		},
	})
	require.NotNil(t, errDup)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, errDup.GetCode())

	// Valid manifest passes.
	validMan := &pluginv1.Manifest{
		Uuid:                 "com.example.widget",
		Name:                 "WidgetPlugin",
		RequiredCapabilities: []string{"overlay.widgets.v1"},
		Permissions:          &pluginv1.Permissions{OverlayBroadcast: true},
		OverlayWidgets: []*pluginv1.OverlayWidgetDescriptor{
			{
				Id:                "now_playing",
				Name:              "Now Playing",
				SupportsHostTheme: true,
				StyleSchema: []*pluginv1.SettingField{
					{Key: "accent", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_COLOR},
					{Key: "opacity", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_NUMBER, MinValue: 0, MaxValue: 100, Step: 5},
				},
			},
		},
	}
	require.Nil(t, ValidateManifest(validMan))

	mgr := NewManager(Options{})
	defer func() { _ = mgr.Close() }()
	require.False(t, mgr.HasRunningOverlayWidgets())

	var gotStyleReq *pluginv1.OnOverlayStyleChangedRequest
	mgr.entries[validMan.GetUuid()] = &pluginEntry{
		uuid:     validMan.GetUuid(),
		name:     validMan.GetName(),
		state:    StatusRunning,
		manifest: validMan,
		backend: &stubBackend{
			dispatchFn: func(_ context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
				gotStyleReq = env.GetOnOverlayStyleChanged()
				return &pluginv1.PluginEnvelope{
					RequestId: env.GetRequestId(),
					Payload:   &pluginv1.PluginEnvelope_Ack{Ack: &pluginv1.AckResponse{Ok: true}},
				}, nil
			},
		},
	}

	require.True(t, mgr.HasRunningOverlayWidgets())
	listed := mgr.Plugins()
	require.Len(t, listed, 1)
	require.Len(t, listed[0].OverlayWidgets, 1)
	require.Equal(t, "now_playing", listed[0].OverlayWidgets[0].GetId())

	err := mgr.NotifyOverlayStyleChanged(context.Background(), validMan.GetUuid(), "now_playing", map[string]any{
		"accent":  "#38BDF8",
		"opacity": 85,
	}, ".card { color: red; }", true)
	require.NoError(t, err)
	require.NotNil(t, gotStyleReq)
	require.Equal(t, "now_playing", gotStyleReq.GetWidgetId())
	require.Equal(t, ".card { color: red; }", gotStyleReq.GetCustomCss())
	require.True(t, gotStyleReq.GetUseHostTheme())
	require.Equal(t, "#38BDF8", pluginsdk.GetSettingString(gotStyleReq.GetValues(), "accent", ""))
	require.Equal(t, 85.0, pluginsdk.GetSettingNumber(gotStyleReq.GetValues(), "opacity", 0))
}
