package obs

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

type mockOBSRequest struct {
	Type string
	Data map[string]any
}

type mockOBSServer struct {
	srv      *httptest.Server
	host     string
	port     int
	mu       sync.Mutex
	requests []mockOBSRequest

	currentScene string
	previewScene string
	studioMode   bool
	filterOn     bool
	itemEnabled  bool
	inputVolMul  float64
	inputVolNull bool
	transforms   map[string]map[string]any
}

func startMockOBSServer(t *testing.T) *mockOBSServer {
	t.Helper()
	m := &mockOBSServer{
		currentScene: "Main",
		previewScene: "BRB",
		filterOn:     false,
		itemEnabled:  true,
		inputVolMul:  0.125, // cbrt(0.125) = 0.5 -> 50%
		transforms: map[string]map[string]any{
			"Main:Camera": {
				"positionX":    100.0,
				"positionY":    200.0,
				"rotation":     0.0,
				"scaleX":       1.0,
				"scaleY":       1.0,
				"cropLeft":     0.0,
				"cropRight":    0.0,
				"cropTop":      0.0,
				"cropBottom":   0.0,
				"sourceWidth":  1920.0,
				"sourceHeight": 1080.0,
				"width":        800.0,
				"height":       450.0,
				"alignment":    5,
				"boundsType":   "OBS_BOUNDS_NONE",
				"boundsWidth":  0.0,
				"boundsHeight": 0.0,
			},
			"Main:CenteredCam": {
				"positionX":    960.0,
				"positionY":    540.0,
				"rotation":     0.0,
				"scaleX":       1.0,
				"scaleY":       1.0,
				"cropLeft":     0.0,
				"cropRight":    0.0,
				"cropTop":      0.0,
				"cropBottom":   0.0,
				"sourceWidth":  1920.0,
				"sourceHeight": 1080.0,
				"width":        600.0,
				"height":       400.0,
				"alignment":    0,
				"boundsType":   "OBS_BOUNDS_NONE",
				"boundsWidth":  0.0,
				"boundsHeight": 0.0,
			},
			"Main:BoundedCam": {
				"positionX":    50.0,
				"positionY":    60.0,
				"rotation":     0.0,
				"scaleX":       1.0,
				"scaleY":       1.0,
				"cropLeft":     0.0,
				"cropRight":    0.0,
				"cropTop":      0.0,
				"cropBottom":   0.0,
				"sourceWidth":  1920.0,
				"sourceHeight": 1080.0,
				"width":        400.0,
				"height":       200.0,
				"alignment":    5,
				"boundsType":   "OBS_BOUNDS_SCALE_INNER",
				"boundsWidth":  400.0,
				"boundsHeight": 200.0,
			},
		},
	}

	m.srv = httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()
		_ = sendEnvelope(conn, opHello, map[string]any{
			"obsWebSocketVersion": "5.4.2",
			"rpcVersion":          1,
		})
		if env, err := recvEnvelope(conn); err != nil || env.Op != opIdentify {
			return
		}
		_ = sendEnvelope(conn, opIdentified, map[string]any{"negotiatedRpcVersion": 1})

		for {
			reqEnv, err := recvEnvelope(conn)
			if err != nil {
				return
			}
			if reqEnv.Op != opRequest {
				continue
			}
			var req requestPayload
			_ = json.Unmarshal(reqEnv.D, &req)

			m.mu.Lock()
			m.requests = append(m.requests, mockOBSRequest{Type: req.RequestType, Data: req.RequestData})
			respData, ok, code, comment := m.handleRequestLocked(req.RequestType, req.RequestData)
			m.mu.Unlock()

			_ = sendEnvelope(conn, opRequestResponse, map[string]any{
				"requestType": req.RequestType,
				"requestId":   req.RequestID,
				"requestStatus": map[string]any{
					"result":  ok,
					"code":    code,
					"comment": comment,
				},
				"responseData": respData,
			})
		}
	}))
	t.Cleanup(m.srv.Close)

	u, err := url.Parse(m.srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	host, _, _ := strings.Cut(u.Host, ":")
	m.host = host
	m.port = port
	return m
}

func (m *mockOBSServer) handleRequestLocked(reqType string, data map[string]any) (map[string]any, bool, int, string) {
	switch reqType {
	case "GetVersion":
		return map[string]any{"obsVersion": "30.2.3", "obsWebSocketVersion": "5.4.2"}, true, 100, ""
	case "GetSceneList":
		// OBS returns scenes bottom-to-top.
		return map[string]any{
			"currentProgramSceneName": m.currentScene,
			"currentPreviewSceneName": m.previewScene,
			"scenes": []any{
				map[string]any{"sceneName": "BRB"},
				map[string]any{"sceneName": "Enhance"},
				map[string]any{"sceneName": "Main"},
			},
		}, true, 100, ""
	case "GetCurrentProgramScene":
		return map[string]any{"currentProgramSceneName": m.currentScene}, true, 100, ""
	case "SetCurrentProgramScene":
		m.currentScene = stringMapVal(data, "sceneName")
		return map[string]any{}, true, 100, ""
	case "SetCurrentPreviewScene":
		m.previewScene = stringMapVal(data, "sceneName")
		return map[string]any{}, true, 100, ""
	case "GetSceneItemList":
		scene := stringMapVal(data, "sceneName")
		if scene == "Main" {
			return map[string]any{
				"sceneItems": []any{
					map[string]any{"sourceName": "Alerts", "sceneItemId": 10, "sceneItemEnabled": true, "isGroup": false},
					map[string]any{"sourceName": "CamGroup", "sceneItemId": 11, "sceneItemEnabled": true, "isGroup": true},
					map[string]any{"sourceName": "Camera", "sceneItemId": 12, "sceneItemEnabled": m.itemEnabled, "isGroup": false},
				},
			}, true, 100, ""
		}
		return map[string]any{"sceneItems": []any{}}, true, 100, ""
	case "GetGroupSceneItemList":
		if stringMapVal(data, "sceneName") == "CamGroup" {
			return map[string]any{
				"sceneItems": []any{
					map[string]any{"sourceName": "NestedCam", "sceneItemId": 99, "sceneItemEnabled": m.itemEnabled, "isGroup": false},
				},
			}, true, 100, ""
		}
		return map[string]any{"sceneItems": []any{}}, true, 100, ""
	case "GetSceneItemId":
		scene := stringMapVal(data, "sceneName")
		src := stringMapVal(data, "sourceName")
		switch scene + ":" + src {
		case "Main:Camera":
			return map[string]any{"sceneItemId": 12}, true, 100, ""
		case "Main:CenteredCam":
			return map[string]any{"sceneItemId": 13}, true, 100, ""
		case "Main:BoundedCam":
			return map[string]any{"sceneItemId": 14}, true, 100, ""
		default:
			return nil, false, 600, "not found"
		}
	case "GetSceneItemEnabled":
		return map[string]any{"sceneItemEnabled": m.itemEnabled}, true, 100, ""
	case "SetSceneItemEnabled":
		m.itemEnabled = boolMapVal(data, "sceneItemEnabled", false)
		return map[string]any{}, true, 100, ""
	case "GetInputList":
		return map[string]any{
			"inputs": []any{
				map[string]any{"inputName": "Mic/Aux", "inputKind": "wasapi_input_capture"},
				map[string]any{"inputName": "BannerText", "inputKind": "text_gdiplus_v3"},
				map[string]any{"inputName": "IntroClip", "inputKind": "ffmpeg_source"},
			},
		}, true, 100, ""
	case "GetSceneTransitionList":
		return map[string]any{
			"transitions": []any{
				map[string]any{"transitionName": "Fade"},
				map[string]any{"transitionName": "Move"},
			},
		}, true, 100, ""
	case "GetSourceFilterList":
		return map[string]any{
			"filters": []any{
				map[string]any{"filterName": "EnhanceMove", "filterKind": "move_source_filter", "filterEnabled": m.filterOn},
				map[string]any{"filterName": "DramaticRed", "filterKind": "color_filter_v2", "filterEnabled": false},
			},
		}, true, 100, ""
	case "GetSourceFilter":
		return map[string]any{"filterEnabled": m.filterOn}, true, 100, ""
	case "SetSourceFilterEnabled":
		m.filterOn = boolMapVal(data, "filterEnabled", false)
		return map[string]any{}, true, 100, ""
	case "GetSceneItemTransform":
		scene := stringMapVal(data, "sceneName")
		id := intMapVal(data, "sceneItemId", 0)
		key := ""
		switch {
		case scene == "Main" && id == 12:
			key = "Main:Camera"
		case scene == "Main" && id == 13:
			key = "Main:CenteredCam"
		case scene == "Main" && id == 14:
			key = "Main:BoundedCam"
		}
		if tr, ok := m.transforms[key]; ok {
			return map[string]any{"sceneItemTransform": tr}, true, 100, ""
		}
		return nil, false, 600, "missing transform"
	case "SetSceneItemTransform":
		tr, _ := data["sceneItemTransform"].(map[string]any)
		// OBS v5 strictly forbids read-only geometry fields inside SetSceneItemTransform.
		for _, forbidden := range []string{"width", "height", "sourceWidth", "sourceHeight"} {
			if _, bad := tr[forbidden]; bad {
				return nil, false, 400, "read-only transform field sent: " + forbidden
			}
		}
		return map[string]any{}, true, 100, ""
	case "GetInputVolume":
		if m.inputVolNull {
			return map[string]any{"inputVolumeDb": nil}, true, 100, ""
		}
		return map[string]any{"inputVolumeMul": m.inputVolMul}, true, 100, ""
	case "SetInputVolume":
		m.inputVolMul = floatMapVal(data, "inputVolumeMul", m.inputVolMul)
		return map[string]any{}, true, 100, ""
	case "GetStudioModeEnabled":
		return map[string]any{"studioModeEnabled": m.studioMode}, true, 100, ""
	case "SetStudioModeEnabled":
		m.studioMode = boolMapVal(data, "studioModeEnabled", false)
		return map[string]any{}, true, 100, ""
	case "CustomEcho":
		return map[string]any{"echoed": stringMapVal(data, "who")}, true, 100, ""
	default:
		return map[string]any{}, true, 100, ""
	}
}

func (m *mockOBSServer) findRequests(reqType string) []mockOBSRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mockOBSRequest
	for _, r := range m.requests {
		if r.Type == reqType {
			out = append(out, r)
		}
	}
	return out
}

func TestOBSDiscoverySceneHistoryAndGroupItems(t *testing.T) {
	srv := startMockOBSServer(t)
	client := NewClient(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx, srv.host, srv.port, ""))
	defer client.Disconnect()

	scenes, err := client.ListScenes(ctx, true)
	require.NoError(t, err)
	require.Equal(t, []string{"Main", "Enhance", "BRB"}, scenes)
	require.Equal(t, "Main", client.CurrentProgramScene())

	items, err := client.ListSceneItems(ctx, "", true)
	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, "Camera", items[0].SourceName)
	require.Equal(t, "CamGroup", items[1].SourceName)
	require.True(t, items[1].IsGroup)
	require.Equal(t, "NestedCam", items[2].SourceName)
	require.Equal(t, "CamGroup", items[2].GroupName)
	require.Equal(t, "Alerts", items[3].SourceName)

	inputs, err := client.ListInputs(ctx, true)
	require.NoError(t, err)
	require.Len(t, inputs, 3)
	require.Equal(t, "BannerText", inputs[1].Name)
	require.Equal(t, "text_gdiplus_v3", inputs[1].Kind)

	transitions, err := client.ListTransitions(ctx, true)
	require.NoError(t, err)
	require.Equal(t, []string{"Fade", "Move"}, transitions)

	filters, err := client.ListSourceFilters(ctx, "Camera")
	require.NoError(t, err)
	require.Len(t, filters, 2)
	require.Equal(t, "EnhanceMove", filters[0].Name)

	snap := client.DiscoverySnapshot()
	require.Equal(t, "Main", snap.CurrentScene)
	require.Equal(t, []string{"Main", "Enhance", "BRB"}, snap.Scenes)

	// Switching scenes records previous scene and allows SwitchToPreviousScene.
	require.NoError(t, client.SetCurrentProgramScene(ctx, "Enhance"))
	require.Equal(t, "Enhance", client.CurrentProgramScene())
	require.Equal(t, "Main", client.PreviousProgramScene())
	require.NoError(t, client.SwitchToPreviousScene(ctx))
	require.Equal(t, "Main", client.CurrentProgramScene())
	require.Equal(t, "Enhance", client.PreviousProgramScene())

	require.NoError(t, client.SetCurrentPreviewScene(ctx, "BRB"))

	// Toggle visibility on a nested group item resolves CamGroup + sceneItemId 99.
	require.NoError(t, client.SetSceneItemEnabledMode(ctx, "", "NestedCam", "toggle"))
	visReqs := srv.findRequests("SetSceneItemEnabled")
	require.NotEmpty(t, visReqs)
	lastVis := visReqs[len(visReqs)-1]
	require.Equal(t, "CamGroup", lastVis.Data["sceneName"])
	require.Equal(t, float64(99), lastVis.Data["sceneItemId"])
	require.Equal(t, false, lastVis.Data["sceneItemEnabled"])

	// Toggle filter on Camera.
	require.NoError(t, client.SetSourceFilterEnabledMode(ctx, "Camera", "EnhanceMove", "toggle"))
	fltReqs := srv.findRequests("SetSourceFilterEnabled")
	require.Len(t, fltReqs, 1)
	require.Equal(t, true, fltReqs[0].Data["filterEnabled"])
}

func TestOBSZoomTransformAndResetBaseline(t *testing.T) {
	srv := startMockOBSServer(t)
	client := NewClient(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx, srv.host, srv.port, ""))
	defer client.Disconnect()

	// 1. Top-left alignment (5): pos=(100,200), size=(800,450), factor=1.5 -> delta=(400,225) -> pos=(-100, 87.5), scale=1.5
	require.NoError(t, client.ZoomSceneItem(ctx, "Main", "Camera", 1.5))
	reqs := srv.findRequests("SetSceneItemTransform")
	require.Len(t, reqs, 1)
	tr, _ := reqs[0].Data["sceneItemTransform"].(map[string]any)
	require.InDelta(t, -100.0, tr["positionX"], 0.001)
	require.InDelta(t, 87.5, tr["positionY"], 0.001)
	require.InDelta(t, 1.5, tr["scaleX"], 0.001)
	require.InDelta(t, 1.5, tr["scaleY"], 0.001)

	// 2. Reset restores exact baseline (100, 200, scale 1.0) and clears baseline.
	require.NoError(t, client.ResetSceneItemTransform(ctx, "Main", "Camera"))
	reqs = srv.findRequests("SetSceneItemTransform")
	require.Len(t, reqs, 2)
	restored, _ := reqs[1].Data["sceneItemTransform"].(map[string]any)
	require.InDelta(t, 100.0, restored["positionX"], 0.001)
	require.InDelta(t, 200.0, restored["positionY"], 0.001)
	require.InDelta(t, 1.0, restored["scaleX"], 0.001)

	// 3. Center alignment (0): pos stays unchanged at (960, 540).
	require.NoError(t, client.ZoomSceneItem(ctx, "Main", "CenteredCam", 1.25))
	reqs = srv.findRequests("SetSceneItemTransform")
	require.Len(t, reqs, 3)
	centered, _ := reqs[2].Data["sceneItemTransform"].(map[string]any)
	require.InDelta(t, 960.0, centered["positionX"], 0.001)
	require.InDelta(t, 540.0, centered["positionY"], 0.001)
	require.InDelta(t, 1.25, centered["scaleX"], 0.001)

	// 4. Bounds mode scales boundsWidth/boundsHeight instead of scaleX/scaleY.
	require.NoError(t, client.ZoomSceneItem(ctx, "Main", "BoundedCam", 1.5))
	reqs = srv.findRequests("SetSceneItemTransform")
	require.Len(t, reqs, 4)
	bounded, _ := reqs[3].Data["sceneItemTransform"].(map[string]any)
	require.InDelta(t, 600.0, bounded["boundsWidth"], 0.001)
	require.InDelta(t, 300.0, bounded["boundsHeight"], 0.001)
	require.NotContains(t, bounded, "scaleX")

	// 5. Reset without prior baseline sends clean default reset.
	require.NoError(t, client.ResetSceneItemTransform(ctx, "Main", "Camera"))
	reqs = srv.findRequests("SetSceneItemTransform")
	require.Len(t, reqs, 5)
	fallback, _ := reqs[4].Data["sceneItemTransform"].(map[string]any)
	require.InDelta(t, 1.0, fallback["scaleX"], 0.001)
	require.InDelta(t, 0.0, fallback["rotation"], 0.001)
}

func TestOBSAudioTextMediaTransitionStudioAndCustomRequest(t *testing.T) {
	srv := startMockOBSServer(t)
	client := NewClient(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx, srv.host, srv.port, ""))
	defer client.Disconnect()

	require.NoError(t, client.SetInputMuteMode(ctx, "Mic/Aux", "toggle"))
	require.Len(t, srv.findRequests("ToggleInputMute"), 1)

	// Relative step: 50% + 25% = 75% -> mul = 0.75^3 = 0.421875
	require.NoError(t, client.SetInputVolumePct(ctx, "Mic/Aux", 25, true))
	volReqs := srv.findRequests("SetInputVolume")
	require.Len(t, volReqs, 1)
	require.InDelta(t, 0.421875, volReqs[0].Data["inputVolumeMul"], 0.0001)

	// Null dB (-inf) treated as 0% + 40% -> 40% -> mul = 0.064
	srv.mu.Lock()
	srv.inputVolNull = true
	srv.mu.Unlock()
	require.NoError(t, client.SetInputVolumePct(ctx, "Mic/Aux", 40, true))
	volReqs = srv.findRequests("SetInputVolume")
	require.Len(t, volReqs, 2)
	require.InDelta(t, 0.064, volReqs[1].Data["inputVolumeMul"], 0.0001)

	require.NoError(t, client.SetTextSource(ctx, "BannerText", "Hello Stream!"))
	txtReqs := srv.findRequests("SetInputSettings")
	require.Len(t, txtReqs, 1)
	require.Equal(t, "BannerText", txtReqs[0].Data["inputName"])

	require.NoError(t, client.TriggerMediaAction(ctx, "IntroClip", "restart"))
	medReqs := srv.findRequests("TriggerMediaInputAction")
	require.Len(t, medReqs, 1)
	require.Equal(t, "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_RESTART", medReqs[0].Data["mediaAction"])

	require.NoError(t, client.SetCurrentSceneTransition(ctx, "Move", 450))
	require.Len(t, srv.findRequests("SetCurrentSceneTransition"), 1)
	durReqs := srv.findRequests("SetCurrentSceneTransitionDuration")
	require.Len(t, durReqs, 1)
	require.Equal(t, float64(450), durReqs[0].Data["transitionDuration"])

	require.NoError(t, client.StudioModeAction(ctx, "toggle"))
	require.Len(t, srv.findRequests("SetStudioModeEnabled"), 1)
	require.NoError(t, client.StudioModeAction(ctx, "transition"))
	require.Len(t, srv.findRequests("TriggerStudioModeTransition"), 1)

	resp, err := client.CustomRequest(ctx, "CustomEcho", `{"who":"Alice"}`)
	require.NoError(t, err)
	require.Equal(t, "Alice", resp["echoed"])
}
