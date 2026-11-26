package bot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

func TestEngineCommandsQueuesFlowAndSubActions(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	require.NoError(t, st.AddUserToGroup("VIPPlus", "twitch", "u1", "Alice"))

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api-ok"))
	}))
	defer httpSrv.Close()

	var obsMu sync.Mutex
	var obsScene string
	obsSrv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		_ = sendObsTestMsg(ws, 0, map[string]any{"obsWebSocketVersion": "5.0.0", "rpcVersion": 1})
		_, _ = recvObsTestMsg(ws)
		_ = sendObsTestMsg(ws, 2, map[string]any{"negotiatedRpcVersion": 1})
		for {
			reqEnv, rErr := recvObsTestMsg(ws)
			if rErr != nil {
				return
			}
			var req struct {
				RequestType string         `json:"requestType"`
				RequestID   string         `json:"requestId"`
				RequestData map[string]any `json:"requestData"`
			}
			_ = json.Unmarshal(reqEnv.D, &req)
			if req.RequestType == "SetCurrentProgramScene" {
				obsMu.Lock()
				obsScene, _ = req.RequestData["sceneName"].(string)
				obsMu.Unlock()
			}
			_ = sendObsTestMsg(ws, 7, map[string]any{
				"requestType":   req.RequestType,
				"requestId":     req.RequestID,
				"requestStatus": map[string]any{"result": true, "code": 100},
				"responseData":  map[string]any{},
			})
		}
	}))
	defer obsSrv.Close()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Commands = []config.BotCommandDef{
		{
			ID:                "cmd-greet",
			Name:              "VIP Greeting",
			Enabled:           true,
			Trigger:           "!greet",
			Aliases:           []string{"!hi"},
			ConsumeTTS:        true,
			GlobalCooldownSec: 60,
			AllowedGroups:     []string{"VIPPlus"},
			ActionID:          "act-greet",
		},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{
			ID:       "vc-scene",
			Name:     "Switch to BRB",
			Enabled:  true,
			Phrase:   "be right back",
			ActionID: "act-brb",
		},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-greet",
			Name:    "Greet Flow",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "inc_var", Enabled: true, Scope: "global_persistent", Key: "greets", Delta: 1},
				{ID: "s2", Kind: "http_request", Enabled: true, Method: "GET", URL: httpSrv.URL, SaveVar: "apiRes"},
				{
					ID: "s3", Kind: "if_else", Enabled: true, LHS: "%global.greets%", Operator: "eq", RHS: "1",
					ThenSubActions: []config.BotSubActionDef{
						{ID: "s3a", Kind: "plugin_action", Enabled: true, PluginUUID: "p1", PluginActionID: "enrich", Params: map[string]string{"who": "%user%"}},
						{ID: "s3b", Kind: "send_chat", Enabled: true, Text: "Hello %user% #%global.greets% (%global.apiRes% %plugOut%)!"},
					},
					ElseSubActions: []config.BotSubActionDef{
						{ID: "s3c", Kind: "break", Enabled: true},
					},
				},
			},
		},
		{
			ID:      "act-brb",
			Name:    "BRB Scene",
			Enabled: true,
			QueueID: "default",
			SubActions: []config.BotSubActionDef{
				{ID: "o1", Kind: "obs", Enabled: true, OBSOperation: "set_scene", OBSScene: "BRB"},
			},
		},
	}

	var mu sync.Mutex
	var chatSent []string
	chatCh := make(chan string, 2)

	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg.Clone() },
		SendChat: func(_ context.Context, _, _, text, _ string, _ bool, _ string) error {
			mu.Lock()
			chatSent = append(chatSent, text)
			mu.Unlock()
			chatCh <- text
			return nil
		},
		RunPluginAction: func(_ context.Context, _, _ string, args *pluginv1.ActionArgs) (bool, *pluginv1.ActionArgs, error) {
			return true, &pluginv1.ActionArgs{Args: map[string]*pluginv1.ActionValue{
				"plugOut": {Kind: &pluginv1.ActionValue_StringVal{StringVal: args.GetArgs()["who"].GetStringVal() + "-ok"}},
			}}, nil
		},
	})
	eng.SetHTTPClient(httpSrv.Client())
	defer func() { _ = eng.Close() }()

	host, portStr, _ := net.SplitHostPort(obsSrv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	require.NoError(t, eng.OBSClient().Connect(context.Background(), host, port, ""))

	// Unauthorized user does NOT match group gate
	consumed := eng.OnEvent(&chat.Event{Platform: "twitch", Kind: chat.EventKindMessage, UserID: "u2", UserName: "Bob", Text: "!hi"})
	require.False(t, consumed)

	// Authorized group member triggers alias "!hi" and consumes TTS
	consumed = eng.OnEvent(&chat.Event{Platform: "twitch", Kind: chat.EventKindMessage, UserID: "u1", UserName: "Alice", Text: "!hi"})
	require.True(t, consumed)

	select {
	case msg := <-chatCh:
		require.Equal(t, "Hello Alice #1 (api-ok Alice-ok)!", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chat sub-action")
	}

	// Second call hits global cooldown (0 extra chat messages)
	consumedAgain := eng.OnEvent(&chat.Event{Platform: "twitch", Kind: chat.EventKindMessage, UserID: "u1", UserName: "Alice", Text: "!greet"})
	require.False(t, consumedAgain)
	mu.Lock()
	require.Len(t, chatSent, 1)
	mu.Unlock()

	// Voice transcript triggers OBS scene switch
	eng.OnVoiceTranscript("Hey folks I will be right back in a minute")
	require.Eventually(t, func() bool {
		obsMu.Lock()
		defer obsMu.Unlock()
		return obsScene == "BRB"
	}, 2*time.Second, 20*time.Millisecond)
}

func TestTimerMinChatLinesAndLiveOnly(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	live := false
	var fired int
	var mu sync.Mutex

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Timers = []config.BotTimerDef{
		{
			ID:              "tm1",
			Name:            "Discord Link",
			Enabled:         true,
			IntervalSeconds: 1,
			LiveOnly:        true,
			MinChatLines:    2,
			ActionID:        "act-tm",
		},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-tm",
			Name:    "Timer Chat",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{ID: "c1", Kind: "send_chat", Enabled: true, Platform: "twitch", Text: "Join Discord!"},
			},
		},
	}

	eng := NewEngine(st, HostBridge{
		GetConfig:    func() *config.Config { return cfg.Clone() },
		IsStreamLive: func() bool { return live },
		SendChat: func(_ context.Context, _, _, _ string, _ string, _ bool, _ string) error {
			mu.Lock()
			fired++
			mu.Unlock()
			return nil
		},
	})
	defer func() { _ = eng.Close() }()

	base := time.Now()
	eng.evaluateTimers(base)
	// Offline + 0 chat lines -> should not fire
	eng.evaluateTimers(base.Add(2 * time.Second))
	mu.Lock()
	require.Equal(t, 0, fired)
	mu.Unlock()

	// Go live + 2 chat lines -> fires!
	live = true
	eng.OnEvent(&chat.Event{Kind: chat.EventKindMessage, Text: "line 1"})
	eng.OnEvent(&chat.Event{Kind: chat.EventKindMessage, Text: "line 2"})
	eng.evaluateTimers(base.Add(3 * time.Second))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired == 1
	}, 2*time.Second, 20*time.Millisecond)
}

func sendObsTestMsg(ws *websocket.Conn, op int, d any) error {
	rawD, _ := json.Marshal(d)
	env, _ := json.Marshal(map[string]any{"op": op, "d": json.RawMessage(rawD)})
	return websocket.Message.Send(ws, string(env))
}

func recvObsTestMsg(ws *websocket.Conn) (struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}, error) {
	var raw string
	var out struct {
		Op int             `json:"op"`
		D  json.RawMessage `json:"d"`
	}
	if err := websocket.Message.Receive(ws, &raw); err != nil {
		return out, err
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

func TestEngineExpandedOBSSubActions(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	var mu sync.Mutex
	var calls []string
	var lastText string
	var lastVisible *bool
	var lastFilterEnabled *bool
	var lastVolMul float64

	obsSrv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		_ = sendObsTestMsg(ws, 0, map[string]any{"obsWebSocketVersion": "5.4.2", "rpcVersion": 1})
		_, _ = recvObsTestMsg(ws)
		_ = sendObsTestMsg(ws, 2, map[string]any{"negotiatedRpcVersion": 1})
		for {
			reqEnv, rErr := recvObsTestMsg(ws)
			if rErr != nil {
				return
			}
			var req struct {
				RequestType string         `json:"requestType"`
				RequestID   string         `json:"requestId"`
				RequestData map[string]any `json:"requestData"`
			}
			_ = json.Unmarshal(reqEnv.D, &req)
			mu.Lock()
			calls = append(calls, req.RequestType)
			mu.Unlock()

			resp := map[string]any{}
			switch req.RequestType {
			case "GetVersion":
				resp = map[string]any{"obsVersion": "30.2.3", "obsWebSocketVersion": "5.4.2"}
			case "GetSceneList":
				resp = map[string]any{
					"currentProgramSceneName": "Main",
					"scenes":                  []any{map[string]any{"sceneName": "Main"}},
				}
			case "GetSceneItemId":
				resp = map[string]any{"sceneItemId": 12}
			case "GetSourceFilter":
				resp = map[string]any{"filterEnabled": false}
			case "SetSourceFilterEnabled":
				v, _ := req.RequestData["filterEnabled"].(bool)
				mu.Lock()
				lastFilterEnabled = &v
				mu.Unlock()
			case "GetSceneItemTransform":
				resp = map[string]any{
					"sceneItemTransform": map[string]any{
						"positionX": 100.0, "positionY": 100.0, "rotation": 0.0,
						"scaleX": 1.0, "scaleY": 1.0, "width": 800.0, "height": 450.0,
						"alignment": 5, "boundsType": "OBS_BOUNDS_NONE",
					},
				}
			case "SetInputSettings":
				if settings, ok := req.RequestData["inputSettings"].(map[string]any); ok {
					mu.Lock()
					lastText, _ = settings["text"].(string)
					mu.Unlock()
				}
			case "GetInputVolume":
				resp = map[string]any{"inputVolumeMul": 0.125}
			case "SetInputVolume":
				v, _ := req.RequestData["inputVolumeMul"].(float64)
				mu.Lock()
				lastVolMul = v
				mu.Unlock()
			case "SetSceneItemEnabled":
				v, _ := req.RequestData["sceneItemEnabled"].(bool)
				mu.Lock()
				lastVisible = &v
				mu.Unlock()
			case "CustomEcho":
				who, _ := req.RequestData["who"].(string)
				resp = map[string]any{"echoed": who}
			}
			_ = sendObsTestMsg(ws, 7, map[string]any{
				"requestType":   req.RequestType,
				"requestId":     req.RequestID,
				"requestStatus": map[string]any{"result": true, "code": 100},
				"responseData":  resp,
			})
		}
	}))
	defer obsSrv.Close()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-enhance",
			Name:    "Enhance Bit",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "obs", Enabled: true, OBSOperation: "set_filter_enabled", OBSSource: "Camera", OBSFilter: "EnhanceMove"},
				{ID: "s2", Kind: "obs", Enabled: true, OBSOperation: "zoom_source", OBSScene: "Main", OBSSource: "Camera", OBSZoomFactor: 1.5},
				{ID: "s3", Kind: "obs", Enabled: true, OBSOperation: "reset_transform", OBSScene: "Main", OBSSource: "Camera"},
				{ID: "s4", Kind: "obs", Enabled: true, OBSOperation: "set_text", OBSInput: "BannerText", Text: "Enhance by %user%"},
				{ID: "s5", Kind: "obs", Enabled: true, OBSOperation: "set_volume", OBSInput: "Mic/Aux", OBSMode: "step", OBSVolumePct: 25},
				{ID: "s6", Kind: "obs", Enabled: true, OBSOperation: "custom_request", OBSRequestType: "CustomEcho", OBSRequestData: `{"who":"%user%"}`},
				{ID: "s7", Kind: "obs", Enabled: true, OBSOperation: "set_source_visible", OBSScene: "Main", OBSSource: "Camera", OBSVisible: false},
				{ID: "s8", Kind: "send_chat", Enabled: true, Platform: "twitch", Text: "resp=%obsResponse%"},
			},
		},
	}

	chatCh := make(chan string, 1)
	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg.Clone() },
		SendChat: func(_ context.Context, _, _, text, _ string, _ bool, _ string) error {
			chatCh <- text
			return nil
		},
	})
	defer func() { _ = eng.Close() }()

	host, portStr, _ := net.SplitHostPort(obsSrv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	require.NoError(t, eng.OBSClient().Connect(context.Background(), host, port, ""))

	require.NoError(t, eng.RunActionByID("act-enhance", map[string]string{"user": "Alice"}, false))

	select {
	case msg := <-chatCh:
		require.Equal(t, `resp={"echoed":"Alice"}`, msg)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for expanded OBS action chain")
	}

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, lastFilterEnabled)
	require.True(t, *lastFilterEnabled)
	require.Equal(t, "Enhance by Alice", lastText)
	require.InDelta(t, 0.421875, lastVolMul, 0.0001)
	require.NotNil(t, lastVisible)
	require.False(t, *lastVisible)
	require.Contains(t, calls, "SetSceneItemTransform")
}

func TestEngineOBSSceneConditionBranching(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	var mu sync.Mutex
	currentScene := "Main"
	zoomedCount := 0
	sceneSwitchCount := 0

	obsSrv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		_ = sendObsTestMsg(ws, 0, map[string]any{"obsWebSocketVersion": "5.4.2", "rpcVersion": 1})
		_, _ = recvObsTestMsg(ws)
		_ = sendObsTestMsg(ws, 2, map[string]any{"negotiatedRpcVersion": 1})
		for {
			reqEnv, rErr := recvObsTestMsg(ws)
			if rErr != nil {
				return
			}
			var req struct {
				RequestType string         `json:"requestType"`
				RequestID   string         `json:"requestId"`
				RequestData map[string]any `json:"requestData"`
			}
			_ = json.Unmarshal(reqEnv.D, &req)
			resp := map[string]any{}
			switch req.RequestType {
			case "GetVersion":
				resp = map[string]any{"obsVersion": "30.2.3", "obsWebSocketVersion": "5.4.2"}
			case "GetSceneList", "GetCurrentProgramScene":
				mu.Lock()
				resp = map[string]any{
					"currentProgramSceneName": currentScene,
					"scenes": []any{
						map[string]any{"sceneName": "Enhance"},
						map[string]any{"sceneName": "Main"},
					},
				}
				mu.Unlock()
			case "SetCurrentProgramScene":
				mu.Lock()
				currentScene, _ = req.RequestData["sceneName"].(string)
				sceneSwitchCount++
				mu.Unlock()
			case "GetSceneItemId":
				resp = map[string]any{"sceneItemId": 12}
			case "GetSceneItemTransform":
				resp = map[string]any{
					"sceneItemTransform": map[string]any{
						"positionX": 0.0, "positionY": 0.0,
						"scaleX": 1.0, "scaleY": 1.0,
						"width": 1920.0, "height": 1080.0,
						"alignment": 5, "boundsType": "OBS_BOUNDS_NONE",
					},
				}
			case "SetSceneItemTransform":
				mu.Lock()
				zoomedCount++
				mu.Unlock()
			}
			_ = sendObsTestMsg(ws, 7, map[string]any{
				"requestType":   req.RequestType,
				"requestId":     req.RequestID,
				"requestStatus": map[string]any{"result": true, "code": 100},
				"responseData":  resp,
			})
		}
	}))
	defer obsSrv.Close()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-zoom",
			Name:    "Enhance Zoom",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{ID: "z1", Kind: "obs", Enabled: true, OBSOperation: "zoom_source", OBSScene: "Enhance", OBSSource: "Camera", OBSZoomFactor: 1.35},
			},
		},
		{
			ID:      "act-enhance",
			Name:    "Smart Enhance",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{
					ID:       "c1",
					Kind:     "if_else",
					Enabled:  true,
					LHS:      "%obs.scene%",
					Operator: "eq",
					RHS:      "Enhance",
					ThenSubActions: []config.BotSubActionDef{
						{ID: "t1", Kind: "run_action", Enabled: true, TargetActionID: "act-zoom"},
						{ID: "t2", Kind: "break", Enabled: true},
					},
				},
				{ID: "s1", Kind: "obs", Enabled: true, OBSOperation: "set_scene", OBSScene: "Enhance"},
			},
		},
	}

	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg.Clone() },
	})
	defer func() { _ = eng.Close() }()

	host, portStr, _ := net.SplitHostPort(obsSrv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	require.NoError(t, eng.OBSClient().Connect(context.Background(), host, port, ""))

	// Run 1: OBS starts on "Main" -> condition false -> switches to "Enhance", does NOT zoom.
	require.NoError(t, eng.RunActionByID("act-enhance", nil, true))
	mu.Lock()
	require.Equal(t, "Enhance", currentScene)
	require.Equal(t, 1, sceneSwitchCount)
	require.Equal(t, 0, zoomedCount)
	mu.Unlock()

	// Run 2: OBS is now on "Enhance" -> condition true -> runs "Enhance Zoom" and breaks (no extra scene switch).
	require.NoError(t, eng.RunActionByID("act-enhance", nil, true))
	mu.Lock()
	require.Equal(t, 1, sceneSwitchCount)
	require.Equal(t, 1, zoomedCount)
	mu.Unlock()
}

func TestEngineDeclaredGlobalSessionAndPerActionVariables(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Variables = []config.BotVariableDef{
		{Name: "lifetime", Scope: "global", Kind: "int", DefaultValue: "10"},
		{Name: "deaths", Scope: "session", Kind: "int", DefaultValue: "0"},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-child",
			Name:    "Child Action",
			Enabled: true,
			Variables: []config.BotVariableDef{
				{Name: "tmp", Scope: "action", Kind: "string", DefaultValue: "child-def"},
			},
			SubActions: []config.BotSubActionDef{
				{ID: "ch1", Kind: "send_chat", Enabled: true, Platform: "twitch", Text: "child=%action.tmp%"},
			},
		},
		{
			ID:      "act-main",
			Name:    "Main Action",
			Enabled: true,
			Variables: []config.BotVariableDef{
				{Name: "tmp", Scope: "action", Kind: "int", DefaultValue: "5"},
			},
			SubActions: []config.BotSubActionDef{
				{ID: "m1", Kind: "inc_var", Enabled: true, Scope: "global", Key: "lifetime", Delta: 1},
				{ID: "m2", Kind: "inc_var", Enabled: true, Scope: "session", Key: "deaths", Delta: 2},
				{ID: "m3", Kind: "inc_var", Enabled: true, Scope: "action", Key: "tmp", Delta: 3},
				{ID: "m4", Kind: "run_action", Enabled: true, TargetActionID: "act-child"},
				{ID: "m5", Kind: "send_chat", Enabled: true, Platform: "twitch", Text: "g=%global.lifetime% s=%session.deaths% a=%action.tmp%"},
			},
		},
	}

	var sent []string
	var mu sync.Mutex
	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg.Clone() },
		SendChat: func(_ context.Context, _, _, text, _ string, _ bool, _ string) error {
			mu.Lock()
			sent = append(sent, text)
			mu.Unlock()
			return nil
		},
	})
	defer func() { _ = eng.Close() }()

	eng.SyncConfig()
	gv, ok := st.GetGlobalVar("lifetime", false)
	require.True(t, ok)
	require.Equal(t, "10", gv.Value)
	sv, ok := st.GetGlobalVar("deaths", true)
	require.True(t, ok)
	require.Equal(t, "0", sv.Value)

	// Run 1: lifetime 10->11, deaths 0->2, per-action tmp 5->8 (child has its own tmp="child-def").
	require.NoError(t, eng.RunActionByID("act-main", nil, true))
	mu.Lock()
	require.Equal(t, []string{"child=child-def", "g=11 s=2 a=8"}, sent)
	sent = nil
	mu.Unlock()

	// Run 2: lifetime 11->12, deaths 2->4, per-action tmp resets to 5 -> 5+3 = 8!
	require.NoError(t, eng.RunActionByID("act-main", nil, true))
	mu.Lock()
	require.Equal(t, []string{"child=child-def", "g=12 s=4 a=8"}, sent)
	sent = nil
	mu.Unlock()

	// Re-syncing config must NOT overwrite live global/session values.
	eng.SyncConfig()
	gv, _ = st.GetGlobalVar("lifetime", false)
	require.Equal(t, "12", gv.Value)
	sv, _ = st.GetGlobalVar("deaths", true)
	require.Equal(t, "4", sv.Value)

	// Stream session start resets Session vars back to default (0) while keeping Global vars (12).
	eng.OnStreamSessionStart()
	gv, _ = st.GetGlobalVar("lifetime", false)
	require.Equal(t, "12", gv.Value)
	sv, _ = st.GetGlobalVar("deaths", true)
	require.Equal(t, "0", sv.Value)
}
