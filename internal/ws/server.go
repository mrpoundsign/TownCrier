package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
)

// SpeakRequest represents a custom TTS synthesis command from a WebSocket or HTTP client.
type SpeakRequest struct {
	Text     string
	VoiceID  string
	Speed    float32
	Priority int
	Username string
}

// Controller defines the operations exposed by the application session to the control server.
type Controller interface {
	SnapshotState() *controlv1.StateSnapshot
	SetControlTTSMode(mode string, idleTimeoutSec *int32) error
	CycleControlTTSMode() string
	SpeakCustom(req SpeakRequest) error
	ExecutePanicAction(action controlv1.PanicAction) error
	ExecuteQueueAction(action controlv1.QueueAction) error
	AdjustVolume(volume *float32, delta *float32) error
}

// ModeToProto converts a config TTS output mode string to its Protobuf enum value.
func ModeToProto(mode string) controlv1.TTSOutputMode {
	switch config.NormalizeTTSOutputMode(mode) {
	case config.TTSModeAutoIdle:
		return controlv1.TTSOutputMode_TTS_OUTPUT_MODE_AUTO_IDLE
	case config.TTSModeAlwaysOff:
		return controlv1.TTSOutputMode_TTS_OUTPUT_MODE_ALWAYS_OFF
	default:
		return controlv1.TTSOutputMode_TTS_OUTPUT_MODE_ALWAYS_ON
	}
}

// ProtoToMode converts a Protobuf TTSOutputMode enum value to its canonical config string.
func ProtoToMode(mode controlv1.TTSOutputMode) string {
	switch mode {
	case controlv1.TTSOutputMode_TTS_OUTPUT_MODE_AUTO_IDLE:
		return config.TTSModeAutoIdle
	case controlv1.TTSOutputMode_TTS_OUTPUT_MODE_ALWAYS_OFF:
		return config.TTSModeAlwaysOff
	default:
		return config.TTSModeAlwaysOn
	}
}

type wsFrame struct {
	data   []byte
	binary bool
}

var rawFrameCodec = websocket.Codec{
	Marshal: func(v any) ([]byte, byte, error) {
		f, ok := v.(wsFrame)
		if !ok {
			return nil, 0, errors.New("invalid wsFrame type")
		}
		if f.binary {
			return f.data, websocket.BinaryFrame, nil
		}
		return f.data, websocket.TextFrame, nil
	},
	Unmarshal: func(data []byte, payloadType byte, v any) error {
		f, ok := v.(*wsFrame)
		if !ok {
			return errors.New("invalid wsFrame pointer")
		}
		f.data = append(f.data[:0], data...)
		f.binary = payloadType == websocket.BinaryFrame
		return nil
	},
}

type wsClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	binary bool
}

func (c *wsClient) sendEnvelope(env *controlv1.ServerEnvelope) error {
	c.mu.Lock()
	useBinary := c.binary
	c.mu.Unlock()

	return c.sendEnvelopeFormatted(env, useBinary)
}

func (c *wsClient) sendEnvelopeFormatted(env *controlv1.ServerEnvelope, useBinary bool) error {
	var (
		payload []byte
		err     error
	)
	if useBinary {
		payload, err = proto.Marshal(env)
	} else {
		payload, err = (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(env)
	}
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return rawFrameCodec.Send(c.conn, wsFrame{
		data:   payload,
		binary: useBinary,
	})
}

// Server provides a dual-mode (binary Protobuf + JSON text) WebSocket server and HTTP REST API.
type Server struct {
	mu        sync.RWMutex
	ctrl      Controller
	host      string
	port      int
	authToken string
	listener  net.Listener
	httpSrv   *http.Server
	clients   map[*wsClient]struct{}
}

// NewServer creates a new WebSocket & HTTP control server bound to the given Controller.
func NewServer(ctrl Controller) *Server {
	return &Server{
		ctrl:    ctrl,
		host:    config.DefaultWebSocketHost,
		port:    config.DefaultWebSocketPort,
		clients: make(map[*wsClient]struct{}),
	}
}

// Start begins listening on host:port (use port 0 for an ephemeral OS port in tests).
func (s *Server) Start(host string, port int, authToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return errors.New("websocket control server already running")
	}
	if strings.TrimSpace(host) == "" {
		host = config.DefaultWebSocketHost
	}
	if port < 0 || port > 65535 {
		port = config.DefaultWebSocketPort
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	s.host = host
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.authToken = strings.TrimSpace(authToken)
	s.listener = ln

	mux := http.NewServeMux()
	wsServer := websocket.Server{
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			// Allow connections from local apps, browsers, and Elgato Stream Deck plugins
			return nil
		},
		Handler: s.handleWebSocket,
	}
	mux.Handle("/ws", s.withCORSAndAuth(wsServer))
	mux.Handle("/api/status", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPStatus)))
	mux.Handle("/api/speak", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPSpeak)))
	mux.Handle("/api/mode", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPMode)))
	mux.Handle("/api/panic", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPPanic)))
	mux.Handle("/api/queue", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPQueue)))
	mux.Handle("/api/volume", s.withCORSAndAuth(http.HandlerFunc(s.handleHTTPVolume)))

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.httpSrv = srv

	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("[WS] Server stopped with error: %v", serveErr)
		}
	}()

	log.Printf("[WS] Dual-mode Protobuf/JSON control server listening on http://%s (ws://%s/ws)", ln.Addr().String(), ln.Addr().String())
	return nil
}

// Stop gracefully closes all active WebSocket connections and shuts down the HTTP listener.
func (s *Server) Stop() error {
	s.mu.Lock()
	srv := s.httpSrv
	ln := s.listener
	s.httpSrv = nil
	s.listener = nil
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	clear(s.clients)
	s.mu.Unlock()

	for _, c := range clients {
		_ = c.conn.Close()
	}

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	} else if ln != nil {
		_ = ln.Close()
	}
	return nil
}

// IsRunning reports whether the control server is actively listening.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listener != nil
}

// Addr returns the bound TCP address ("host:port") if running, or empty string otherwise.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// ClientCount returns the number of currently connected WebSocket clients.
func (s *Server) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// BroadcastState sends the latest StateSnapshot to all connected WebSocket clients.
func (s *Server) BroadcastState() {
	if s.ctrl == nil {
		return
	}
	snap := s.ctrl.SnapshotState()
	env := &controlv1.ServerEnvelope{
		Ok: true,
		Payload: &controlv1.ServerEnvelope_State{
			State: snap,
		},
	}
	s.broadcastEnvelope(env)
}

// BroadcastSpeechEvent sends a live SpeechEvent (started or finished) to all connected WebSocket clients.
func (s *Server) BroadcastSpeechEvent(ev *controlv1.SpeechEvent) {
	if ev == nil {
		return
	}
	env := &controlv1.ServerEnvelope{
		Ok: true,
		Payload: &controlv1.ServerEnvelope_SpeechEvent{
			SpeechEvent: ev,
		},
	}
	s.broadcastEnvelope(env)
}

func (s *Server) broadcastEnvelope(env *controlv1.ServerEnvelope) {
	s.mu.RLock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.RUnlock()

	for _, c := range clients {
		if err := c.sendEnvelope(env); err != nil {
			_ = c.conn.Close()
		}
	}
}

func (s *Server) checkAuth(r *http.Request) bool {
	s.mu.RLock()
	expected := s.authToken
	s.mu.RUnlock()

	if expected == "" {
		return true
	}

	// Check ?token= query parameter
	if qToken := strings.TrimSpace(r.URL.Query().Get("token")); qToken != "" {
		if subtle.ConstantTimeCompare([]byte(qToken), []byte(expected)) == 1 {
			return true
		}
	}

	// Check Authorization: Bearer <token>
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if rest, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
		token := strings.TrimSpace(rest)
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
			return true
		}
	}
	if subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) == 1 {
		return true
	}

	return false
}

func (s *Server) withCORSAndAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.checkAuth(r) {
			s.writeHTTPEnvelope(w, r, http.StatusUnauthorized, &controlv1.ServerEnvelope{
				Ok:    false,
				Error: "unauthorized: invalid or missing auth token",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleWebSocket(conn *websocket.Conn) {
	defer func() { _ = conn.Close() }()

	req := conn.Request()
	defaultBinary := false
	if req != nil {
		qFmt := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("format")))
		if qFmt == "protobuf" || qFmt == "proto" || qFmt == "bin" || qFmt == "binary" {
			defaultBinary = true
		}
		if strings.Contains(strings.ToLower(req.Header.Get("Sec-WebSocket-Protocol")), "protobuf") {
			defaultBinary = true
		}
	}

	client := &wsClient{
		conn:   conn,
		binary: defaultBinary,
	}

	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
	}()

	// Send initial StateSnapshot immediately on connection
	if s.ctrl != nil {
		initEnv := &controlv1.ServerEnvelope{
			Ok: true,
			Payload: &controlv1.ServerEnvelope_State{
				State: s.ctrl.SnapshotState(),
			},
		}
		_ = client.sendEnvelope(initEnv)
	}

	var frame wsFrame
	for {
		if err := rawFrameCodec.Receive(conn, &frame); err != nil {
			return
		}

		client.mu.Lock()
		client.binary = frame.binary
		client.mu.Unlock()

		var cmdEnv controlv1.ClientEnvelope
		var parseErr error
		if frame.binary {
			parseErr = proto.Unmarshal(frame.data, &cmdEnv)
		} else {
			parseErr = parseJSONClientEnvelope(frame.data, &cmdEnv)
		}

		if parseErr != nil {
			_ = client.sendEnvelopeFormatted(&controlv1.ServerEnvelope{
				Ok:    false,
				Error: fmt.Sprintf("invalid command payload: %v", parseErr),
			}, frame.binary)
			continue
		}

		respEnv, mutated := s.executeCommand(&cmdEnv)
		_ = client.sendEnvelopeFormatted(respEnv, frame.binary)

		if mutated {
			s.broadcastEnvelopeExcept(respEnv, client)
		}
	}
}

func (s *Server) broadcastEnvelopeExcept(env *controlv1.ServerEnvelope, skip *wsClient) {
	s.mu.RLock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		if c != skip {
			clients = append(clients, c)
		}
	}
	s.mu.RUnlock()

	for _, c := range clients {
		if err := c.sendEnvelope(env); err != nil {
			_ = c.conn.Close()
		}
	}
}

// parseJSONClientEnvelope supports both canonical protojson ClientEnvelope payloads and
// flat convenience JSON payloads (e.g. {"action":"cycle_tts_mode"} or {"action":"speak","text":"hello"}).
func parseJSONClientEnvelope(data []byte, out *controlv1.ClientEnvelope) error {
	unmarshaler := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshaler.Unmarshal(data, out); err == nil && out.GetCommand() != nil {
		return nil
	}

	var raw struct {
		RequestID          string   `json:"request_id"`
		Action             string   `json:"action"`
		Command            string   `json:"command"`
		Mode               string   `json:"mode"`
		IdleTimeoutSeconds *int32   `json:"idle_timeout_seconds"`
		Text               string   `json:"text"`
		VoiceID            string   `json:"voice_id"`
		Speed              float32  `json:"speed"`
		Priority           int32    `json:"priority"`
		Username           string   `json:"username"`
		Volume             *float32 `json:"volume"`
		Delta              *float32 `json:"delta"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	out.RequestId = raw.RequestID
	act := strings.ToLower(strings.TrimSpace(raw.Action))
	if act == "" {
		act = strings.ToLower(strings.TrimSpace(raw.Command))
	}

	switch act {
	case "get_state", "state", "status":
		out.Command = &controlv1.ClientEnvelope_GetState{GetState: &controlv1.GetStateCommand{}}
	case "cycle_tts_mode", "cycle_mode", "toggle_mode":
		out.Command = &controlv1.ClientEnvelope_CycleTtsMode{CycleTtsMode: &controlv1.CycleTTSModeCommand{}}
	case "set_tts_mode", "set_mode", "mode":
		out.Command = &controlv1.ClientEnvelope_SetTtsMode{
			SetTtsMode: &controlv1.SetTTSModeCommand{
				Mode:               ModeToProto(raw.Mode),
				IdleTimeoutSeconds: raw.IdleTimeoutSeconds,
			},
		}
	case "speak", "tts":
		out.Command = &controlv1.ClientEnvelope_Speak{
			Speak: &controlv1.SpeakCommand{
				Text:     raw.Text,
				VoiceId:  raw.VoiceID,
				Speed:    raw.Speed,
				Priority: raw.Priority,
				Username: raw.Username,
			},
		}
	case "panic", "panic_toggle", "toggle_panic":
		out.Command = &controlv1.ClientEnvelope_Panic{
			Panic: &controlv1.PanicCommand{Action: controlv1.PanicAction_PANIC_ACTION_TOGGLE},
		}
	case "panic_mute", "mute":
		out.Command = &controlv1.ClientEnvelope_Panic{
			Panic: &controlv1.PanicCommand{Action: controlv1.PanicAction_PANIC_ACTION_MUTE},
		}
	case "panic_clear", "unmute":
		out.Command = &controlv1.ClientEnvelope_Panic{
			Panic: &controlv1.PanicCommand{Action: controlv1.PanicAction_PANIC_ACTION_CLEAR},
		}
	case "pause", "queue_pause":
		out.Command = &controlv1.ClientEnvelope_QueueAction{
			QueueAction: &controlv1.QueueActionCommand{Action: controlv1.QueueAction_QUEUE_ACTION_PAUSE},
		}
	case "resume", "queue_resume":
		out.Command = &controlv1.ClientEnvelope_QueueAction{
			QueueAction: &controlv1.QueueActionCommand{Action: controlv1.QueueAction_QUEUE_ACTION_RESUME},
		}
	case "skip", "queue_skip":
		out.Command = &controlv1.ClientEnvelope_QueueAction{
			QueueAction: &controlv1.QueueActionCommand{Action: controlv1.QueueAction_QUEUE_ACTION_SKIP},
		}
	case "clear", "queue_clear":
		out.Command = &controlv1.ClientEnvelope_QueueAction{
			QueueAction: &controlv1.QueueActionCommand{Action: controlv1.QueueAction_QUEUE_ACTION_CLEAR},
		}
	case "set_volume", "volume":
		out.Command = &controlv1.ClientEnvelope_SetVolume{
			SetVolume: &controlv1.SetVolumeCommand{
				Volume: raw.Volume,
				Delta:  raw.Delta,
			},
		}
	default:
		return fmt.Errorf("unrecognized command action %q", act)
	}
	return nil
}

func (s *Server) executeCommand(cmdEnv *controlv1.ClientEnvelope) (*controlv1.ServerEnvelope, bool) {
	if s.ctrl == nil {
		return &controlv1.ServerEnvelope{
			RequestId: cmdEnv.GetRequestId(),
			Ok:        false,
			Error:     "controller unavailable",
		}, false
	}

	var (
		err     error
		mutated bool
	)

	switch c := cmdEnv.GetCommand().(type) {
	case *controlv1.ClientEnvelope_GetState:
		// Read-only state query
	case *controlv1.ClientEnvelope_SetTtsMode:
		modeStr := ProtoToMode(c.SetTtsMode.GetMode())
		var timeoutPtr *int32
		if c.SetTtsMode.IdleTimeoutSeconds != nil {
			v := c.SetTtsMode.GetIdleTimeoutSeconds()
			timeoutPtr = &v
		}
		err = s.ctrl.SetControlTTSMode(modeStr, timeoutPtr)
		mutated = err == nil
	case *controlv1.ClientEnvelope_CycleTtsMode:
		_ = s.ctrl.CycleControlTTSMode()
		mutated = true
	case *controlv1.ClientEnvelope_Speak:
		err = s.ctrl.SpeakCustom(SpeakRequest{
			Text:     c.Speak.GetText(),
			VoiceID:  c.Speak.GetVoiceId(),
			Speed:    c.Speak.GetSpeed(),
			Priority: int(c.Speak.GetPriority()),
			Username: c.Speak.GetUsername(),
		})
		mutated = err == nil
	case *controlv1.ClientEnvelope_Panic:
		err = s.ctrl.ExecutePanicAction(c.Panic.GetAction())
		mutated = err == nil
	case *controlv1.ClientEnvelope_QueueAction:
		err = s.ctrl.ExecuteQueueAction(c.QueueAction.GetAction())
		mutated = err == nil
	case *controlv1.ClientEnvelope_SetVolume:
		var volPtr, deltaPtr *float32
		if c.SetVolume.Volume != nil {
			v := c.SetVolume.GetVolume()
			volPtr = &v
		}
		if c.SetVolume.Delta != nil {
			d := c.SetVolume.GetDelta()
			deltaPtr = &d
		}
		err = s.ctrl.AdjustVolume(volPtr, deltaPtr)
		mutated = err == nil
	default:
		err = errors.New("missing or unsupported command in ClientEnvelope")
	}

	resp := &controlv1.ServerEnvelope{
		RequestId: cmdEnv.GetRequestId(),
		Ok:        err == nil,
		Payload: &controlv1.ServerEnvelope_State{
			State: s.ctrl.SnapshotState(),
		},
	}
	if err != nil {
		resp.Error = err.Error()
	}
	return resp, mutated
}

func (s *Server) writeHTTPEnvelope(w http.ResponseWriter, r *http.Request, status int, env *controlv1.ServerEnvelope) {
	accept := strings.ToLower(r.Header.Get("Accept"))
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(accept, "application/x-protobuf") || strings.Contains(contentType, "application/x-protobuf") {
		raw, err := proto.Marshal(env)
		if err == nil {
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.WriteHeader(status)
			_, _ = w.Write(raw)
			return
		}
	}

	raw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(env)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (s *Server) handleHTTPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	env := &controlv1.ServerEnvelope{
		Ok: true,
	}
	if s.ctrl != nil {
		env.Payload = &controlv1.ServerEnvelope_State{State: s.ctrl.SnapshotState()}
	}
	s.writeHTTPEnvelope(w, r, http.StatusOK, env)
}

func (s *Server) handleHTTPSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		s.writeHTTPEnvelope(w, r, http.StatusBadRequest, &controlv1.ServerEnvelope{Ok: false, Error: err.Error()})
		return
	}

	var speakCmd controlv1.SpeakCommand
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/x-protobuf") {
		err = proto.Unmarshal(body, &speakCmd)
	} else if len(body) > 0 {
		err = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, &speakCmd)
	}
	if err != nil {
		s.writeHTTPEnvelope(w, r, http.StatusBadRequest, &controlv1.ServerEnvelope{Ok: false, Error: err.Error()})
		return
	}
	if strings.TrimSpace(speakCmd.GetText()) == "" {
		speakCmd.Text = strings.TrimSpace(r.URL.Query().Get("text"))
	}

	resp, mutated := s.executeCommand(&controlv1.ClientEnvelope{
		Command: &controlv1.ClientEnvelope_Speak{Speak: &speakCmd},
	})
	if mutated {
		s.broadcastEnvelope(resp)
	}
	status := http.StatusOK
	if !resp.GetOk() {
		status = http.StatusBadRequest
	}
	s.writeHTTPEnvelope(w, r, status, resp)
}

func (s *Server) handleHTTPMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	var payload struct {
		Mode               string `json:"mode"`
		IdleTimeoutSeconds *int32 `json:"idle_timeout_seconds"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	if payload.Mode == "" {
		payload.Mode = r.URL.Query().Get("mode")
	}

	var cmdEnv controlv1.ClientEnvelope
	if strings.EqualFold(strings.TrimSpace(payload.Mode), "cycle") || strings.EqualFold(strings.TrimSpace(payload.Mode), "toggle") {
		cmdEnv.Command = &controlv1.ClientEnvelope_CycleTtsMode{CycleTtsMode: &controlv1.CycleTTSModeCommand{}}
	} else {
		cmdEnv.Command = &controlv1.ClientEnvelope_SetTtsMode{
			SetTtsMode: &controlv1.SetTTSModeCommand{
				Mode:               ModeToProto(payload.Mode),
				IdleTimeoutSeconds: payload.IdleTimeoutSeconds,
			},
		}
	}

	resp, mutated := s.executeCommand(&cmdEnv)
	if mutated {
		s.broadcastEnvelope(resp)
	}
	s.writeHTTPEnvelope(w, r, http.StatusOK, resp)
}

func (s *Server) handleHTTPPanic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	var payload struct {
		Action string `json:"action"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	if payload.Action == "" {
		payload.Action = r.URL.Query().Get("action")
	}

	action := controlv1.PanicAction_PANIC_ACTION_TOGGLE
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "mute", "on", "panic":
		action = controlv1.PanicAction_PANIC_ACTION_MUTE
	case "clear", "unmute", "off", "reset":
		action = controlv1.PanicAction_PANIC_ACTION_CLEAR
	}

	resp, mutated := s.executeCommand(&controlv1.ClientEnvelope{
		Command: &controlv1.ClientEnvelope_Panic{
			Panic: &controlv1.PanicCommand{Action: action},
		},
	})
	if mutated {
		s.broadcastEnvelope(resp)
	}
	s.writeHTTPEnvelope(w, r, http.StatusOK, resp)
}

func (s *Server) handleHTTPQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	var payload struct {
		Action string `json:"action"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	if payload.Action == "" {
		payload.Action = r.URL.Query().Get("action")
	}

	var action controlv1.QueueAction
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "pause":
		action = controlv1.QueueAction_QUEUE_ACTION_PAUSE
	case "resume":
		action = controlv1.QueueAction_QUEUE_ACTION_RESUME
	case "skip":
		action = controlv1.QueueAction_QUEUE_ACTION_SKIP
	case "clear":
		action = controlv1.QueueAction_QUEUE_ACTION_CLEAR
	}

	resp, mutated := s.executeCommand(&controlv1.ClientEnvelope{
		Command: &controlv1.ClientEnvelope_QueueAction{
			QueueAction: &controlv1.QueueActionCommand{Action: action},
		},
	})
	if mutated {
		s.broadcastEnvelope(resp)
	}
	status := http.StatusOK
	if !resp.GetOk() {
		status = http.StatusBadRequest
	}
	s.writeHTTPEnvelope(w, r, status, resp)
}

func (s *Server) handleHTTPVolume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPEnvelope(w, r, http.StatusMethodNotAllowed, &controlv1.ServerEnvelope{Ok: false, Error: "method not allowed"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	var payload struct {
		Volume *float32 `json:"volume"`
		Delta  *float32 `json:"delta"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}

	resp, mutated := s.executeCommand(&controlv1.ClientEnvelope{
		Command: &controlv1.ClientEnvelope_SetVolume{
			SetVolume: &controlv1.SetVolumeCommand{
				Volume: payload.Volume,
				Delta:  payload.Delta,
			},
		},
	})
	if mutated {
		s.broadcastEnvelope(resp)
	}
	status := http.StatusOK
	if !resp.GetOk() {
		status = http.StatusBadRequest
	}
	s.writeHTTPEnvelope(w, r, status, resp)
}
