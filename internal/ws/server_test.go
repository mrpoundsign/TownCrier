package ws

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
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
