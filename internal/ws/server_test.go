package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

type mockController struct {
	mu          sync.Mutex
	mode        string
	idleTimeout int32
	panicked    bool
	paused      bool
	volume      float32
	spoken      []SpeakRequest
	queueActs   []controlv1.QueueAction
	overlaySubs []func(OverlayStreamEvent)
	customCSS   string
	styleJSON   string
}

func newMockController() *mockController {
	return &mockController{
		mode:        config.TTSModeAlwaysOn,
		idleTimeout: 15,
		volume:      1.0,
	}
}

func (m *mockController) SnapshotState() *controlv1.StateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &controlv1.StateSnapshot{
		TtsEnabled:         true,
		OutputMode:         ModeToProto(m.mode),
		IdleTimeoutSeconds: m.idleTimeout,
		SpeakingActive:     m.mode == config.TTSModeAlwaysOn,
		Paused:             m.paused,
		Panicked:           m.panicked,
		QueueDepth:         int32(len(m.spoken)),
		Volume:             m.volume,
		CurrentVoice:       "supertonic:f1",
	}
}

func (m *mockController) SetControlTTSMode(mode string, idleTimeoutSec *int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = config.NormalizeTTSOutputMode(mode)
	if idleTimeoutSec != nil && *idleTimeoutSec > 0 {
		m.idleTimeout = *idleTimeoutSec
	}
	return nil
}

func (m *mockController) CycleControlTTSMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = config.NextTTSOutputMode(m.mode)
	return m.mode
}

func (m *mockController) SpeakCustom(req SpeakRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spoken = append(m.spoken, req)
	return nil
}

func (m *mockController) ExecutePanicAction(action controlv1.PanicAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch action {
	case controlv1.PanicAction_PANIC_ACTION_MUTE:
		m.panicked = true
	case controlv1.PanicAction_PANIC_ACTION_CLEAR:
		m.panicked = false
	default:
		m.panicked = !m.panicked
	}
	return nil
}

func (m *mockController) ExecuteQueueAction(action controlv1.QueueAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueActs = append(m.queueActs, action)
	switch action {
	case controlv1.QueueAction_QUEUE_ACTION_PAUSE:
		m.paused = true
	case controlv1.QueueAction_QUEUE_ACTION_RESUME:
		m.paused = false
	}
	return nil
}

func (m *mockController) AdjustVolume(volume *float32, delta *float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if volume != nil {
		m.volume = *volume
	}
	if delta != nil {
		m.volume += *delta
	}
	return nil
}

func (m *mockController) QueryOverlayChat(_ context.Context, presetID string, limit int) (*ChatOverlaySnapshot, error) {
	if presetID == "" {
		presetID = "latest-session"
	}
	return &ChatOverlaySnapshot{
		Preset: config.LiveLogPreset{
			ID:          presetID,
			Name:        "Mock Preset",
			ShowSummary: true,
		},
		Scale: 125,
		Entries: []eventlog.Entry{
			{
				ID:          "ev-1",
				Platform:    "twitch",
				UserName:    "alice",
				DisplayName: "Alice",
				RawText:     "Hello OBS overlay!",
				Outcome:     eventlog.OutcomeSpoken,
				Spoken:      true,
			},
		},
		Summary: eventlog.QuerySummary{
			TotalSubs: 3,
		},
	}, nil
}

func (m *mockController) SubscribeOverlayChat(_ string, fn func(OverlayStreamEvent)) func() {
	m.mu.Lock()
	m.overlaySubs = append(m.overlaySubs, fn)
	idx := len(m.overlaySubs) - 1
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if idx >= 0 && idx < len(m.overlaySubs) {
			m.overlaySubs[idx] = nil
		}
		m.mu.Unlock()
	}
}

func (m *mockController) emitOverlayEvent(ev OverlayStreamEvent) {
	m.mu.Lock()
	subs := slices.Clone(m.overlaySubs)
	m.mu.Unlock()
	for _, fn := range subs {
		if fn != nil {
			fn(ev)
		}
	}
}

func (m *mockController) GetOverlayStyle() OverlayStyleConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return OverlayStyleConfig{
		CustomCSS:   m.customCSS,
		StyleJSON:   m.styleJSON,
		TTSPosition: "bottom",
	}
}

func (m *mockController) SaveOverlayStyle(customCSS, styleJSON string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.customCSS = customCSS
	m.styleJSON = styleJSON
	return nil
}

func TestServerDualModeProtobufAndJSONWebSocket(t *testing.T) {
	ctrl := newMockController()
	srv := NewServer(ctrl)
	if err := srv.Start("127.0.0.1", 0, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	addr := srv.Addr()

	// 1. Connect binary Protobuf client (like Stream Deck plugin)
	protoConn, err := websocket.Dial("ws://"+addr+"/ws?format=protobuf", "", "http://"+addr)
	if err != nil {
		t.Fatalf("websocket.Dial protobuf failed: %v", err)
	}
	defer func() { _ = protoConn.Close() }()

	// Read initial StateSnapshot pushed on connect
	var initFrame wsFrame
	if err := rawFrameCodec.Receive(protoConn, &initFrame); err != nil {
		t.Fatalf("Receive initial frame failed: %v", err)
	}
	if !initFrame.binary {
		t.Fatal("expected binary frame for ?format=protobuf client")
	}
	var initEnv controlv1.ServerEnvelope
	if err := proto.Unmarshal(initFrame.data, &initEnv); err != nil {
		t.Fatalf("proto.Unmarshal initial envelope failed: %v", err)
	}
	if !initEnv.GetOk() || initEnv.GetState().GetOutputMode() != controlv1.TTSOutputMode_TTS_OUTPUT_MODE_ALWAYS_ON {
		t.Fatalf("unexpected initial state: %+v", initEnv.GetState())
	}

	// 2. Connect text JSON client
	jsonConn, err := websocket.Dial("ws://"+addr+"/ws", "", "http://"+addr)
	if err != nil {
		t.Fatalf("websocket.Dial json failed: %v", err)
	}
	defer func() { _ = jsonConn.Close() }()

	var jsonInitFrame wsFrame
	if err := rawFrameCodec.Receive(jsonConn, &jsonInitFrame); err != nil {
		t.Fatalf("Receive json initial frame failed: %v", err)
	}
	if jsonInitFrame.binary {
		t.Fatal("expected text frame for default JSON client")
	}

	// 3. Send binary Protobuf CycleTTSModeCommand from protoConn
	cycleReqBytes, err := proto.Marshal(&controlv1.ClientEnvelope{
		RequestId: "req-1",
		Command: &controlv1.ClientEnvelope_CycleTtsMode{
			CycleTtsMode: &controlv1.CycleTTSModeCommand{},
		},
	})
	if err != nil {
		t.Fatalf("proto.Marshal request failed: %v", err)
	}
	if err := rawFrameCodec.Send(protoConn, wsFrame{data: cycleReqBytes, binary: true}); err != nil {
		t.Fatalf("Send binary frame failed: %v", err)
	}

	// Verify binary response on protoConn
	var respFrame wsFrame
	if err := rawFrameCodec.Receive(protoConn, &respFrame); err != nil {
		t.Fatalf("Receive binary response failed: %v", err)
	}
	var respEnv controlv1.ServerEnvelope
	if err := proto.Unmarshal(respFrame.data, &respEnv); err != nil {
		t.Fatalf("proto.Unmarshal response failed: %v", err)
	}
	if respEnv.GetRequestId() != "req-1" || respEnv.GetState().GetOutputMode() != controlv1.TTSOutputMode_TTS_OUTPUT_MODE_AUTO_IDLE {
		t.Fatalf("expected AUTO_IDLE in response, got %+v", respEnv.GetState())
	}

	// Verify broadcast arrived at jsonConn in JSON text format!
	var bcastFrame wsFrame
	if err := rawFrameCodec.Receive(jsonConn, &bcastFrame); err != nil {
		t.Fatalf("Receive broadcast on jsonConn failed: %v", err)
	}
	if bcastFrame.binary {
		t.Fatal("expected text frame broadcast on jsonConn")
	}
	var bcastEnv controlv1.ServerEnvelope
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(bcastFrame.data, &bcastEnv); err != nil {
		t.Fatalf("protojson.Unmarshal broadcast failed: %v", err)
	}
	if bcastEnv.GetState().GetOutputMode() != controlv1.TTSOutputMode_TTS_OUTPUT_MODE_AUTO_IDLE {
		t.Fatalf("expected AUTO_IDLE in JSON broadcast, got %+v", bcastEnv.GetState())
	}

	// 4. Send convenience JSON speak command from jsonConn
	speakJSON := `{"request_id":"json-speak-1","action":"speak","text":"Hello from Streamer.bot","voice_id":"af_bella","speed":1.1}`
	if err := rawFrameCodec.Send(jsonConn, wsFrame{data: []byte(speakJSON), binary: false}); err != nil {
		t.Fatalf("Send JSON speak failed: %v", err)
	}
	var jsonSpeakResp wsFrame
	if err := rawFrameCodec.Receive(jsonConn, &jsonSpeakResp); err != nil {
		t.Fatalf("Receive JSON speak response failed: %v", err)
	}
	if !strings.Contains(string(jsonSpeakResp.data), "json-speak-1") {
		t.Fatalf("expected request_id in JSON response, got %s", string(jsonSpeakResp.data))
	}
}

func TestServerHTTPRESTAndAuth(t *testing.T) {
	ctrl := newMockController()
	srv := NewServer(ctrl)
	if err := srv.Start("127.0.0.1", 0, "secret-token-123"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	baseURL := "http://" + srv.Addr()
	client := &http.Client{Timeout: 3 * time.Second}

	// 1. Request without token -> 401 Unauthorized
	resUnauth, err := client.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status failed: %v", err)
	}
	_ = resUnauth.Body.Close()
	if resUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resUnauth.StatusCode)
	}

	// 2. Request with Bearer token -> 200 OK
	reqAuth, _ := http.NewRequest(http.MethodGet, baseURL+"/api/status", nil)
	reqAuth.Header.Set("Authorization", "Bearer secret-token-123")
	resAuth, err := client.Do(reqAuth)
	if err != nil {
		t.Fatalf("authenticated GET /api/status failed: %v", err)
	}
	_ = resAuth.Body.Close()
	if resAuth.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resAuth.StatusCode)
	}

	// 3. POST /api/mode with ?token= query param
	resMode, err := client.Post(
		baseURL+"/api/mode?token=secret-token-123",
		"application/json",
		bytes.NewBufferString(`{"mode":"always_off","idle_timeout_seconds":30}`),
	)
	if err != nil {
		t.Fatalf("POST /api/mode failed: %v", err)
	}
	_ = resMode.Body.Close()
	if resMode.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/mode, got %d", resMode.StatusCode)
	}
	if snap := ctrl.SnapshotState(); snap.GetOutputMode() != controlv1.TTSOutputMode_TTS_OUTPUT_MODE_ALWAYS_OFF || snap.GetIdleTimeoutSeconds() != 30 {
		t.Fatalf("unexpected state after /api/mode: %+v", snap)
	}

	// 4. POST /api/volume with delta
	resVol, err := client.Post(
		baseURL+"/api/volume?token=secret-token-123",
		"application/json",
		bytes.NewBufferString(`{"delta":-0.25}`),
	)
	if err != nil {
		t.Fatalf("POST /api/volume failed: %v", err)
	}
	_ = resVol.Body.Close()
	if snap := ctrl.SnapshotState(); snap.GetVolume() < 0.74 || snap.GetVolume() > 0.76 {
		t.Fatalf("expected volume ~0.75, got %f", snap.GetVolume())
	}
}

func TestServerOverlayEndpointsAndFeatureGating(t *testing.T) {
	ctrl := newMockController()
	srv := NewServer(ctrl)
	// Enable only TTS Overlay initially (Control API false, Chat Overlay false)
	srv.SetFeatures(false, true, false)

	if err := srv.Start("127.0.0.1", 0, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	baseURL := "http://" + srv.Addr()
	client := &http.Client{Timeout: 3 * time.Second}

	// 1. /overlay/tts is enabled -> 200 OK
	resTTS, err := client.Get(baseURL + "/overlay/tts")
	if err != nil {
		t.Fatalf("GET /overlay/tts failed: %v", err)
	}
	bodyTTS, _ := io.ReadAll(resTTS.Body)
	_ = resTTS.Body.Close()
	if resTTS.StatusCode != http.StatusOK || !strings.Contains(string(bodyTTS), "TownCrier — Now Speaking Overlay") {
		t.Fatalf("unexpected /overlay/tts response: status=%d", resTTS.StatusCode)
	}

	// 2. /overlay/chat and /api/overlay/chat are disabled -> 404 NotFound
	resChatDisabled, err := client.Get(baseURL + "/overlay/chat")
	if err != nil {
		t.Fatalf("GET /overlay/chat failed: %v", err)
	}
	_ = resChatDisabled.Body.Close()
	if resChatDisabled.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for disabled /overlay/chat, got %d", resChatDisabled.StatusCode)
	}

	// 3. Control API /api/status is disabled -> 403 Forbidden
	resCtrlDisabled, err := client.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status failed: %v", err)
	}
	_ = resCtrlDisabled.Body.Close()
	if resCtrlDisabled.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for disabled /api/status, got %d", resCtrlDisabled.StatusCode)
	}

	// 4. WebSocket /ws is allowed in read-only mode when TTSOverlayEnabled is true, but rejects mutating commands
	wsConn, err := websocket.Dial("ws://"+srv.Addr()+"/ws", "", baseURL)
	if err != nil {
		t.Fatalf("websocket.Dial failed when TTSOverlayEnabled=true: %v", err)
	}
	defer func() { _ = wsConn.Close() }()

	var initFrame wsFrame
	if err := rawFrameCodec.Receive(wsConn, &initFrame); err != nil {
		t.Fatalf("Receive initial frame failed: %v", err)
	}
	speakJSON := `{"request_id":"ro-1","action":"speak","text":"Should be rejected"}`
	if err := rawFrameCodec.Send(wsConn, wsFrame{data: []byte(speakJSON), binary: false}); err != nil {
		t.Fatalf("Send JSON speak failed: %v", err)
	}
	var roResp wsFrame
	if err := rawFrameCodec.Receive(wsConn, &roResp); err != nil {
		t.Fatalf("Receive read-only error response failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(roResp.data)), "external control api is disabled") {
		t.Fatalf("expected read-only rejection message, got %s", string(roResp.data))
	}

	// 5. Enable Chat Overlay -> /overlay/chat, /api/overlay/chat, and /api/overlay/stream work
	srv.SetFeatures(false, true, true)

	resChatHTML, err := client.Get(baseURL + "/overlay/chat")
	if err != nil {
		t.Fatalf("GET /overlay/chat failed: %v", err)
	}
	bodyChatHTML, _ := io.ReadAll(resChatHTML.Body)
	_ = resChatHTML.Body.Close()
	if resChatHTML.StatusCode != http.StatusOK || !strings.Contains(string(bodyChatHTML), "TownCrier — Live Chat Overlay") {
		t.Fatalf("unexpected /overlay/chat response: status=%d", resChatHTML.StatusCode)
	}

	resSnap, err := client.Get(baseURL + "/api/overlay/chat?preset=twitch-alerts")
	if err != nil {
		t.Fatalf("GET /api/overlay/chat failed: %v", err)
	}
	var snap ChatOverlaySnapshot
	if err := json.NewDecoder(resSnap.Body).Decode(&snap); err != nil {
		_ = resSnap.Body.Close()
		t.Fatalf("decode ChatOverlaySnapshot failed: %v", err)
	}
	_ = resSnap.Body.Close()
	if snap.Preset.ID != "twitch-alerts" || len(snap.Entries) != 1 || snap.Entries[0].RawText != "Hello OBS overlay!" {
		t.Fatalf("unexpected ChatOverlaySnapshot: %+v", snap)
	}

	// Verify ?summary=0 and ?summary=1 overrides
	resSnapNoSum, err := client.Get(baseURL + "/api/overlay/chat?preset=twitch-alerts&summary=0")
	if err != nil {
		t.Fatalf("GET /api/overlay/chat?summary=0 failed: %v", err)
	}
	var snapNoSum ChatOverlaySnapshot
	_ = json.NewDecoder(resSnapNoSum.Body).Decode(&snapNoSum)
	_ = resSnapNoSum.Body.Close()
	if snapNoSum.ShowSummary || snapNoSum.Preset.ShowSummary {
		t.Fatalf("expected ShowSummary=false with ?summary=0, got %+v", snapNoSum)
	}

	resSnapWithSum, err := client.Get(baseURL + "/api/overlay/chat?preset=twitch-alerts&summary=1")
	if err != nil {
		t.Fatalf("GET /api/overlay/chat?summary=1 failed: %v", err)
	}
	var snapWithSum ChatOverlaySnapshot
	_ = json.NewDecoder(resSnapWithSum.Body).Decode(&snapWithSum)
	_ = resSnapWithSum.Body.Close()
	if !snapWithSum.ShowSummary || !snapWithSum.Preset.ShowSummary {
		t.Fatalf("expected ShowSummary=true with ?summary=1, got %+v", snapWithSum)
	}

	// 6. Test SSE /api/overlay/stream
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, baseURL+"/api/overlay/stream?preset=latest-session", nil)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /api/overlay/stream failed: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()

	go func() {
		time.Sleep(30 * time.Millisecond)
		ctrl.emitOverlayEvent(OverlayStreamEvent{
			Kind: "append",
			Entry: &eventlog.Entry{
				ID:       "ev-sse-1",
				Platform: "youtube",
				UserName: "bob",
				RawText:  "Live via SSE!",
			},
		})
	}()

	scanner := bufio.NewScanner(sseResp.Body)
	var gotData string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			gotData = after
			break
		}
	}
	if !strings.Contains(gotData, "Live via SSE!") {
		t.Fatalf("expected SSE data containing 'Live via SSE!', got %q", gotData)
	}

	// 7. Test /overlay/designer and /api/overlay/style
	resDesigner, err := client.Get(baseURL + "/overlay/designer")
	if err != nil {
		t.Fatalf("GET /overlay/designer failed: %v", err)
	}
	bodyDesigner, _ := io.ReadAll(resDesigner.Body)
	_ = resDesigner.Body.Close()
	if resDesigner.StatusCode != http.StatusOK || !strings.Contains(string(bodyDesigner), "OBS Overlay Style Designer") {
		t.Fatalf("unexpected /overlay/designer response: status=%d", resDesigner.StatusCode)
	}

	savePayload := `{"custom_css":".plat-badge{display:none!important;}","style_json":"{\"showPlatBadge\":false}"}`
	resSaveStyle, err := client.Post(baseURL+"/api/overlay/style", "application/json", bytes.NewBufferString(savePayload))
	if err != nil {
		t.Fatalf("POST /api/overlay/style failed: %v", err)
	}
	_ = resSaveStyle.Body.Close()
	if resSaveStyle.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/overlay/style, got %d", resSaveStyle.StatusCode)
	}

	resGetStyle, err := client.Get(baseURL + "/api/overlay/style")
	if err != nil {
		t.Fatalf("GET /api/overlay/style failed: %v", err)
	}
	var gotStyle OverlayStyleConfig
	if err := json.NewDecoder(resGetStyle.Body).Decode(&gotStyle); err != nil {
		_ = resGetStyle.Body.Close()
		t.Fatalf("decode OverlayStyleConfig failed: %v", err)
	}
	_ = resGetStyle.Body.Close()
	if gotStyle.CustomCSS != ".plat-badge{display:none!important;}" {
		t.Fatalf("unexpected CustomCSS after save: %q", gotStyle.CustomCSS)
	}
}
