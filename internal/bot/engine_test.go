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
