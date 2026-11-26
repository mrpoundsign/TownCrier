package ui

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/bot/obs"
	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
	"github.com/mrpoundsign/TownCrier/pkg/tts"
)

func TestBotTabViewBuildTestRunAndOBSSecret(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Commands = []config.BotCommandDef{
		{
			ID:        "cmd-1",
			Name:      "Hello",
			Enabled:   true,
			Trigger:   "!hello",
			MatchMode: "prefix",
			ActionID:  "act-1",
		},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-1",
			Name:    "Set Welcome Var",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{
					ID:      "sa-1",
					Kind:    "set_var",
					Enabled: true,
					Scope:   "global_persistent",
					Key:     "welcome_test",
					VarKind: "string",
					Value:   "ok-%user%",
				},
			},
		},
	}

	secStore := secrets.NewMemoryStore()
	sess := app.NewSession(cfg, secStore, config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()
	require.NoError(t, sess.Start())

	win := fyneApp.NewWindow("Bot Tab Test")
	defer win.Close()

	var lastStatus string
	view := NewBotTabView(win, sess, func(msg string) {
		lastStatus = msg
	})
	obj := view.Build()
	tabs, ok := obj.(*container.AppTabs)
	require.True(t, ok, "expected *container.AppTabs from BotTabView.Build()")
	require.Len(t, tabs.Items, 5)
	require.Equal(t, "Commands & Voice", tabs.Items[0].Text)
	require.Equal(t, "Actions & Sub-Actions", tabs.Items[1].Text)
	require.Equal(t, "Triggers & Timers", tabs.Items[2].Text)
	require.Equal(t, "Queues & Variables", tabs.Items[3].Text)
	require.Equal(t, "OBS Studio", tabs.Items[4].Text)

	// Verify Test Run executes the selected action via BotEngine and writes to Store
	view.selectedActionID = "act-1"
	view.runSelectedActionTest()
	require.Contains(t, lastStatus, "Executed Test Run")
	require.Eventually(t, func() bool {
		v, found := sess.BotEngine().Store().GetGlobalVar("welcome_test", false)
		return found && v.Value == "ok-tester"
	}, 2*time.Second, 20*time.Millisecond)

	// Verify OBS credentials save host/port to Config and password to OS keyring secret
	view.obsHostEntry.SetText("192.168.1.50")
	view.obsPortEntry.SetText("4466")
	view.obsPasswordEntry.SetText("supersecret")
	view.obsAutoReconnChk.SetChecked(true)
	_, _, _ = view.saveOBSSettings()

	gotCfg := sess.Config()
	require.True(t, gotCfg.Bot.OBS.Enabled)
	require.Equal(t, "192.168.1.50", gotCfg.Bot.OBS.Host)
	require.Equal(t, 4466, gotCfg.Bot.OBS.Port)

	pw, err := secStore.Get(config.SecretOBSPassword)
	require.NoError(t, err)
	require.Equal(t, "supersecret", pw)
}

func TestBotTabViewVoiceCommandAutoEnablesAndRespectsMasterToggle(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{
		{ID: "act-1", Name: "Wave", Enabled: true},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{ID: "vc-1", Name: "Wave Cmd", Enabled: false, Phrase: "say hello", MatchMode: "contains", ActionID: "act-1"},
	}

	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()

	win := fyneApp.NewWindow("Bot Voice Command Test")
	defer win.Close()

	var configSyncCount int
	view := NewBotTabView(win, sess, nil)
	view.SetOnConfigChanged(func() {
		configSyncCount++
	})
	_ = view.Build()

	require.False(t, sess.Config().EffectiveVoiceDetectionEnabled())

	// Enable the voice command checkbox -> auto-enables Voice Detection
	var vcCheck *widget.Check
	for _, obj := range view.voiceCmdsBox.Objects {
		if row, ok := obj.(*fyne.Container); ok {
			for _, child := range row.Objects {
				if chk, okChk := child.(*widget.Check); okChk {
					vcCheck = chk
					break
				}
			}
		}
	}
	require.NotNil(t, vcCheck, "expected voice command Enabled checkbox")
	vcCheck.SetChecked(true)

	require.True(t, sess.Config().EffectiveVoiceDetectionEnabled(), "enabling a voice command should auto-enable Voice Detection")
	require.True(t, sess.Config().EffectiveVoiceCommandsEnabled())
	require.Greater(t, configSyncCount, 0)

	// Master disable of Voice Detection shows paused banner & Enable button while keeping vc-1 enabled
	require.NoError(t, sess.UpdateConfig(func(c *config.Config) {
		c.SetVoiceDetectionEnabled(false)
	}))
	view.Refresh()
	require.False(t, sess.Config().EffectiveVoiceCommandsEnabled())
	require.True(t, sess.Config().Bot.VoiceCommands[0].Enabled)
	require.GreaterOrEqual(t, len(view.voiceCmdsBox.Objects), 3, "expected paused banner + separator + voice command row")
}

func TestBotTabViewFriendlyLabelsAndNoRawIDs(t *testing.T) {
	// 1. All human-readable option tables round-trip cleanly and contain zero underscores.
	tables := [][]botOption{
		botSubActionKindOptions,
		botOBSOperationOptions,
		botOBSSceneTargetOptions,
		botOBSVisibilityModeOptions,
		botOBSFilterModeOptions,
		botOBSMuteModeOptions,
		botOBSVolumeModeOptions,
		botOBSMediaActionOptions,
		botOBSStudioModeOptions,
		botVarScopeOptions,
		botVarKindOptions,
		botConditionOperatorOptions,
		botConditionBranchModeOptions,
		botConditionLHSPresetOptions,
		botModerationOpOptions,
		botTriggerSourceOptions,
		botCommandMatchModeOptions,
		botVoiceMatchModeOptions,
	}
	for _, tbl := range tables {
		labels := botOptionLabels(tbl)
		require.Len(t, labels, len(tbl))
		for _, opt := range tbl {
			require.NotContains(t, opt.Label, "_", "display label %q should not contain underscores", opt.Label)
			require.Equal(t, opt.Label, botOptionLabel(tbl, opt.Key))
			require.Equal(t, opt.Key, botOptionKey(tbl, opt.Label))
		}
	}

	// 2. actionDisplayMap hides raw act-... IDs for unique names and disambiguates duplicates cleanly.
	b := config.BotAutomationConfig{
		Actions: []config.BotActionDef{
			{ID: "act-1111111", Name: "Welcome Shoutout", Enabled: true},
			{ID: "act-2222222", Name: "Alert", Enabled: true},
			{ID: "act-3333333", Name: "Alert", Enabled: true},
		},
		Queues: config.DefaultBotAutomationConfig().Queues,
	}
	view := &BotTabView{}
	labels, labelToID, idToLabel := view.actionDisplayMap(b)
	require.Equal(t, []string{"Welcome Shoutout", "Alert (#1)", "Alert (#2)"}, labels)
	require.Equal(t, "act-1111111", labelToID["Welcome Shoutout"])
	require.Equal(t, "act-2222222", labelToID["Alert (#1)"])
	require.Equal(t, "act-3333333", labelToID["Alert (#2)"])
	require.Equal(t, "Welcome Shoutout", idToLabel["act-1111111"])
	for _, lbl := range labels {
		require.False(t, strings.Contains(lbl, "act-"), "label %q leaked raw action ID", lbl)
	}

	// 3. summarizeSubAction resolves friendly action names, voice names, and human labels.
	sumRun := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "run_action", TargetActionID: "act-1111111"})
	require.Equal(t, "Run Action → Welcome Shoutout", sumRun)
	require.NotContains(t, sumRun, "act-1111111")

	sumTTS := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "speak_tts", VoiceID: "af_bella", Text: "hi"})
	require.Contains(t, sumTTS, tts.VoiceDisplayName("af_bella"))

	sumOBS := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "obs", OBSOperation: "set_scene", OBSScene: "Main"})
	require.Equal(t, "OBS: Switch Scene (scene=Main)", sumOBS)

	sumOBSFilter := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "obs", OBSOperation: "set_filter_enabled", OBSSource: "Camera", OBSFilter: "EnhanceMove", OBSMode: "enable"})
	require.Contains(t, sumOBSFilter, "src=Camera")
	require.Contains(t, sumOBSFilter, "filter=EnhanceMove")

	sumOBSZoom := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "obs", OBSOperation: "zoom_source", OBSSource: "Camera", OBSZoomFactor: 1.5})
	require.Contains(t, sumOBSZoom, "factor=1.5x")

	sumOBSVol := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "obs", OBSOperation: "set_volume", OBSInput: "Mic/Aux", OBSMode: "step", OBSVolumePct: -10})
	require.Contains(t, sumOBSVol, "delta=-10%")

	sumOBSCustom := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "obs", OBSOperation: "custom_request", OBSRequestType: "TriggerHotkeyByName"})
	require.Contains(t, sumOBSCustom, "req=TriggerHotkeyByName")

	sumVar := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "set_var", Scope: "global_persistent", Key: "count", Value: "1"})
	require.Equal(t, `Set Var (Global (Saved)) count = "1"`, sumVar)

	sumCond := view.summarizeSubAction(b, config.BotSubActionDef{Kind: "if_else", LHS: "%a%", Operator: "not_empty"})
	require.Equal(t, `If "%a%" Is Not Empty ""`, sumCond)

	sumCondBranch := view.summarizeSubAction(b, config.BotSubActionDef{
		Kind:     "if_else",
		LHS:      "%obs.scene%",
		Operator: "eq",
		RHS:      "Enhance",
		ThenSubActions: []config.BotSubActionDef{
			{Kind: "run_action", TargetActionID: "act-1111111"},
			{Kind: "break"},
		},
		ElseSubActions: []config.BotSubActionDef{
			{Kind: "break"},
		},
	})
	require.Equal(t, `If "%obs.scene%" Equals "Enhance" (Then: Run Welcome Shoutout & Stop; Else: Stop)`, sumCondBranch)

	// 4. pluginActionDisplayMap maps human action names back to canonical IDs and preserves offline fallback.
	st := &pluginhost.PluginStatus{
		UUID: "com.example.dj",
		Name: "CrowdDJ",
		Actions: []*pluginv1.ActionDescriptor{
			{Id: "skip_track", Name: "Skip Current Track", Description: "Skips to next queued song"},
		},
	}
	pActLabels, pActToID, pActFromID, descByID := pluginActionDisplayMap(st, "missing_offline_action")
	require.Contains(t, pActLabels, "Skip Current Track")
	require.Contains(t, pActLabels, "missing_offline_action (Offline)")
	require.Equal(t, "skip_track", pActToID["Skip Current Track"])
	require.Equal(t, "Skip Current Track", pActFromID["skip_track"])
	require.Equal(t, "Skips to next queued song", descByID["skip_track"])

	// 5. Test Run status uses action display name rather than raw ID.
	fyneApp := test.NewApp()
	defer fyneApp.Quit()
	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{{ID: "act-999", Name: "Hype Wave", Enabled: true}}
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()
	var statusMsg string
	btv := NewBotTabView(fyneApp.NewWindow("Test"), sess, func(msg string) { statusMsg = msg })
	btv.selectedActionID = "act-999"
	btv.runSelectedActionTest()
	require.Equal(t, "✓ Executed Test Run: Hype Wave", statusMsg)
}

func TestBotTabViewVoiceTesterAndWakeWord(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{
		{ID: "act-hello", Name: "Say Hello Action", Enabled: true},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{
			ID:        "vc-octo",
			Name:      "Octo Say Hello",
			Enabled:   true,
			Phrase:    "octo say hello",
			MatchMode: "phonetic",
			ActionID:  "act-hello",
		},
	}

	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()

	win := fyneApp.NewWindow("Voice Tester")
	defer win.Close()

	var syncCount int
	view := NewBotTabView(win, sess, nil)
	view.SetOnConfigChanged(func() { syncCount++ })
	_ = view.Build()

	require.NotNil(t, view.voiceWakeWordEntry)
	require.NotNil(t, view.voiceTranscriptFeedLbl)
	require.NotNil(t, view.voiceTestEntry)
	require.NotNil(t, view.voiceTestResultLbl)

	// 1. Live Whisper transcript update populates the feed.
	sess.BotEngine().OnVoiceTranscript("Auto, say hello.")
	view.SyncVoiceTranscript(sess.BotEngine().TestVoiceTranscript("Auto, say hello.", false))
	require.Contains(t, view.voiceTranscriptFeedLbl.Text, "Auto, say hello.")

	// 2. Interactive Phrase Tester runs dry-run phonetic match diagnostics via Submit.
	view.voiceTestEntry.SetText("Otto, say hello!")
	view.voiceTestEntry.OnSubmitted(view.voiceTestEntry.Text)
	require.Contains(t, view.voiceTestResultLbl.Text, "Octo Say Hello")
	require.Contains(t, view.voiceTestResultLbl.Text, "WOULD TRIGGER")

	// 3. Submitting a global wake word persists to config.VoiceDetection and fires onConfigChanged.
	view.voiceWakeWordEntry.OnSubmitted("octo")
	require.Equal(t, "octo", sess.Config().VoiceDetection.WakeWord)
	require.Greater(t, syncCount, 0)
	require.Equal(t, "octo", view.voiceWakeWordEntry.Text)

	// 4. Wake beep checkbox defaults to true and persists changes to Session config.
	require.NotNil(t, view.voiceWakeSoundCheck)
	require.True(t, view.voiceWakeSoundCheck.Checked)
	view.voiceWakeSoundCheck.SetChecked(false)
	require.False(t, sess.Config().VoiceDetection.EffectiveWakeSoundEnabled())

	// 5. Response voice summary reflects configured ResponseVoiceID & Speed and resets cleanly.
	require.NotNil(t, view.voiceResponseLbl)
	require.Equal(t, "Global Default TTS Voice", view.voiceResponseLbl.Text)
	require.NoError(t, sess.UpdateConfig(func(c *config.Config) {
		c.VoiceDetection.ResponseVoiceID = "bm_lewis"
		c.VoiceDetection.ResponseSpeed = 1.25
	}))
	view.Refresh()
	require.Equal(t, FormatVoiceSpeedSummary("bm_lewis", 1.25), view.voiceResponseLbl.Text)
}

func TestOBSSubActionEditorLiveDiscoverySnapshotAndApply(t *testing.T) {
	obsSrv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		sendMsg := func(op int, d any) {
			rawD, _ := json.Marshal(d)
			env, _ := json.Marshal(map[string]any{"op": op, "d": json.RawMessage(rawD)})
			_ = websocket.Message.Send(ws, string(env))
		}
		sendMsg(0, map[string]any{"obsWebSocketVersion": "5.4.2", "rpcVersion": 1})
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return
		}
		sendMsg(2, map[string]any{"negotiatedRpcVersion": 1})
		for {
			if err := websocket.Message.Receive(ws, &raw); err != nil {
				return
			}
			var env struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			_ = json.Unmarshal([]byte(raw), &env)
			var req struct {
				RequestType string         `json:"requestType"`
				RequestID   string         `json:"requestId"`
				RequestData map[string]any `json:"requestData"`
			}
			_ = json.Unmarshal(env.D, &req)

			resp := map[string]any{}
			switch req.RequestType {
			case "GetVersion":
				resp = map[string]any{"obsVersion": "30.2.3", "obsWebSocketVersion": "5.4.2"}
			case "GetSceneList":
				resp = map[string]any{
					"currentProgramSceneName": "Main",
					"scenes": []any{
						map[string]any{"sceneName": "Enhance"},
						map[string]any{"sceneName": "Main"},
					},
				}
			case "GetSceneItemList":
				resp = map[string]any{
					"sceneItems": []any{
						map[string]any{"sourceName": "Camera", "sceneItemId": 12, "sceneItemEnabled": true, "isGroup": false},
					},
				}
			case "GetInputList":
				resp = map[string]any{
					"inputs": []any{
						map[string]any{"inputName": "Mic/Aux", "inputKind": "wasapi_input_capture"},
						map[string]any{"inputName": "BannerText", "inputKind": "text_gdiplus_v3"},
					},
				}
			case "GetSceneTransitionList":
				resp = map[string]any{
					"transitions": []any{
						map[string]any{"transitionName": "Move"},
					},
				}
			case "GetSourceFilterList":
				resp = map[string]any{
					"filters": []any{
						map[string]any{"filterName": "EnhanceMove", "filterKind": "move_source_filter", "filterEnabled": false},
					},
				}
			case "GetSceneItemId":
				resp = map[string]any{"sceneItemId": 12}
			case "GetSceneItemTransform":
				resp = map[string]any{
					"sceneItemTransform": map[string]any{
						"positionX": 140.5, "positionY": 80.0,
						"scaleX": 1.35, "scaleY": 1.35,
						"rotation": 2.5,
						"cropLeft": 10.0, "cropRight": 10.0, "cropTop": 0.0, "cropBottom": 0.0,
						"width": 1080.0, "height": 607.5,
					},
				}
			}
			sendMsg(7, map[string]any{
				"requestType":   req.RequestType,
				"requestId":     req.RequestID,
				"requestStatus": map[string]any{"result": true, "code": 100},
				"responseData":  resp,
			})
		}
	}))
	defer obsSrv.Close()

	client := obs.NewClient(nil, nil)
	host, portStr, _ := net.SplitHostPort(obsSrv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	require.NoError(t, client.Connect(context.Background(), host, port, ""))
	defer client.Disconnect()

	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	ed := newOBSSubActionEditor(nil, client, config.BotSubActionDef{
		Kind:         "obs",
		OBSOperation: "set_filter_enabled",
		OBSScene:     "Main",
		OBSSource:    "Camera",
	}, nil, nil)
	defer ed.Close()

	win := fyneApp.NewWindow("OBS Editor Test")
	win.SetContent(ed.CanvasObject())
	defer win.Close()

	require.Eventually(t, func() bool {
		ed.mu.RLock()
		nScenes := len(ed.scenes)
		nItems := len(ed.sceneItems)
		nFilters := len(ed.filters)
		nInputs := len(ed.inputs)
		nTrans := len(ed.transitions)
		ed.mu.RUnlock()
		return strings.Contains(ed.statusLbl.Text, "OBS Connected") &&
			nScenes >= 2 &&
			nItems >= 1 &&
			nFilters >= 1 &&
			nInputs >= 2 &&
			nTrans >= 1
	}, 3*time.Second, 20*time.Millisecond)

	ed.opSelect.SetSelected(botOptionLabel(botOBSOperationOptions, "set_transform"))
	ed.snapshotTransformFromOBS()

	require.Eventually(t, func() bool {
		return ed.posXEntry.Text == "140.5" && ed.scaleXEntry.Text == "1.35"
	}, 3*time.Second, 20*time.Millisecond)

	var saved config.BotSubActionDef
	saved.Kind = "obs"
	ed.ApplyTo(&saved)
	require.Equal(t, "set_transform", saved.OBSOperation)
	require.Equal(t, "Main", saved.OBSScene)
	require.Equal(t, "Camera", saved.OBSSource)
	require.NotNil(t, saved.OBSTransform)
	require.NotNil(t, saved.OBSTransform.PositionX)
	require.InDelta(t, 140.5, *saved.OBSTransform.PositionX, 0.001)
	require.NotNil(t, saved.OBSTransform.ScaleX)
	require.InDelta(t, 1.35, *saved.OBSTransform.ScaleX, 0.001)
}

func TestBotConditionEditorOBSScenesAndBranches(t *testing.T) {
	obsSrv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		sendMsg := func(op int, d any) {
			rawD, _ := json.Marshal(d)
			env, _ := json.Marshal(map[string]any{"op": op, "d": json.RawMessage(rawD)})
			_ = websocket.Message.Send(ws, string(env))
		}
		sendMsg(0, map[string]any{"obsWebSocketVersion": "5.4.2", "rpcVersion": 1})
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return
		}
		sendMsg(2, map[string]any{"negotiatedRpcVersion": 1})
		for {
			if err := websocket.Message.Receive(ws, &raw); err != nil {
				return
			}
			var env struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			_ = json.Unmarshal([]byte(raw), &env)
			var req struct {
				RequestType string `json:"requestType"`
				RequestID   string `json:"requestId"`
			}
			_ = json.Unmarshal(env.D, &req)
			resp := map[string]any{}
			switch req.RequestType {
			case "GetVersion":
				resp = map[string]any{"obsVersion": "30.2.3", "obsWebSocketVersion": "5.4.2"}
			case "GetSceneList":
				resp = map[string]any{
					"currentProgramSceneName": "Main",
					"scenes": []any{
						map[string]any{"sceneName": "Enhance"},
						map[string]any{"sceneName": "Main"},
					},
				}
			}
			sendMsg(7, map[string]any{
				"requestType":   req.RequestType,
				"requestId":     req.RequestID,
				"requestStatus": map[string]any{"result": true, "code": 100},
				"responseData":  resp,
			})
		}
	}))
	defer obsSrv.Close()

	client := obs.NewClient(nil, nil)
	host, portStr, _ := net.SplitHostPort(obsSrv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	require.NoError(t, client.Connect(context.Background(), host, port, ""))
	defer client.Disconnect()

	fyneApp := test.NewApp()
	defer fyneApp.Quit()
	win := fyneApp.NewWindow("Condition Editor Test")
	defer win.Close()

	actLabels := []string{"Enhance Zoom"}
	actToID := map[string]string{"Enhance Zoom": "act-zoom"}
	idToAct := map[string]string{"act-zoom": "Enhance Zoom"}

	ed := newBotConditionEditor(win, client, config.BotSubActionDef{Kind: "if_else"}, actLabels, actToID, idToAct, nil, nil, nil)
	defer ed.Close()
	win.SetContent(ed.CanvasObject())

	// Picking Current OBS Scene preset normalizes LHS to %obs.scene% and loads OBS scenes for RHS.
	ed.lhsEntry.SetText(botOptionLabel(botConditionLHSPresetOptions, "%obs.scene%"))
	require.Equal(t, "%obs.scene%", ed.lhsEntry.Text)
	require.Eventually(t, func() bool {
		ed.mu.RLock()
		n := len(ed.obsScenes)
		ed.mu.RUnlock()
		return n == 2 && strings.Contains(ed.rhsHintLbl.Text, "2 OBS scenes")
	}, 3*time.Second, 20*time.Millisecond)

	ed.rhsEntry.SetText("Enhance")
	ed.thenBranch.modeSelect.SetSelected(botOptionLabel(botConditionBranchModeOptions, "run_action_break"))
	ed.elseBranch.modeSelect.SetSelected(botOptionLabel(botConditionBranchModeOptions, "continue"))

	var saved config.BotSubActionDef
	saved.Kind = "if_else"
	ed.ApplyTo(&saved)
	require.Equal(t, "%obs.scene%", saved.LHS)
	require.Equal(t, "eq", saved.Operator)
	require.Equal(t, "Enhance", saved.RHS)
	require.Len(t, saved.ThenSubActions, 2)
	require.Equal(t, "run_action", saved.ThenSubActions[0].Kind)
	require.Equal(t, "act-zoom", saved.ThenSubActions[0].TargetActionID)
	require.Equal(t, "break", saved.ThenSubActions[1].Kind)
	require.Empty(t, saved.ElseSubActions)

	// Re-opening preserves branch mode and target action; custom multi-step branches stay intact.
	ed2 := newBotConditionEditor(win, client, saved, actLabels, actToID, idToAct, nil, nil, nil)
	defer ed2.Close()
	require.Equal(t, botOptionLabel(botConditionBranchModeOptions, "run_action_break"), ed2.thenBranch.modeSelect.Selected)
	require.Equal(t, "Enhance Zoom", ed2.thenBranch.actSelect.Selected)

	customStep := saved
	customStep.ThenSubActions = []config.BotSubActionDef{
		{ID: "x1", Kind: "delay", Enabled: true, DelayMs: 250},
		{ID: "x2", Kind: "break", Enabled: true},
	}
	ed3 := newBotConditionEditor(win, client, customStep, actLabels, actToID, idToAct, nil, nil, nil)
	defer ed3.Close()
	require.True(t, strings.HasPrefix(ed3.thenBranch.modeSelect.Selected, keepCustomBranchPrefix))
	var roundTripped config.BotSubActionDef
	ed3.ApplyTo(&roundTripped)
	require.Len(t, roundTripped.ThenSubActions, 2)
	require.Equal(t, "delay", roundTripped.ThenSubActions[0].Kind)
	require.Equal(t, 250, roundTripped.ThenSubActions[0].DelayMs)

	// Declared variable presets normalize cleanly to %global.*%, %session.*%, %action.*%.
	presets := declaredConditionPresets(
		config.BotAutomationConfig{
			Variables: []config.BotVariableDef{
				{Name: "wins", Scope: "global", Kind: "int"},
				{Name: "deaths", Scope: "session", Kind: "int"},
			},
		},
		&config.BotActionDef{
			Variables: []config.BotVariableDef{
				{Name: "tmp", Scope: "action", Kind: "string"},
			},
		},
	)
	require.Len(t, presets, 3)
	ed4 := newBotConditionEditor(win, nil, config.BotSubActionDef{Kind: "if_else"}, actLabels, actToID, idToAct, nil, presets, nil)
	defer ed4.Close()
	ed4.lhsEntry.SetText(presets[1].Label)
	require.Equal(t, "%session.deaths%", ed4.lhsEntry.Text)
	ed4.lhsEntry.SetText(presets[2].Label)
	require.Equal(t, "%action.tmp%", ed4.lhsEntry.Text)
}


