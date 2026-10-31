package streamdeck_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
	"github.com/mrpoundsign/TownCrier/internal/ws"
	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
)

func TestInstallPlugin(t *testing.T) {
	baseDir := t.TempDir()
	if streamdeck.IsPluginInstalled(baseDir) {
		t.Fatalf("expected IsPluginInstalled=false before installation")
	}

	installedDir, err := streamdeck.InstallPlugin(baseDir)
	if err != nil {
		t.Fatalf("InstallPlugin failed: %v", err)
	}
	wantDir := filepath.Join(baseDir, streamdeck.PluginDirName)
	if installedDir != wantDir {
		t.Fatalf("installedDir = %q, want %q", installedDir, wantDir)
	}
	if !streamdeck.IsPluginInstalled(baseDir) {
		t.Fatalf("expected IsPluginInstalled=true after installation")
	}

	requiredFiles := []string{
		"manifest.json",
		filepath.Join("bin", "plugin.js"),
		filepath.Join("property-inspector", "index.html"),
		filepath.Join("property-inspector", "pi.js"),
		filepath.Join("imgs", "plugin.svg"),
		filepath.Join("imgs", "mode.svg"),
		filepath.Join("imgs", "panic.svg"),
		filepath.Join("imgs", "queue.svg"),
		filepath.Join("imgs", "speak.svg"),
		filepath.Join("imgs", "volume.svg"),
	}
	for _, rel := range requiredFiles {
		full := filepath.Join(installedDir, rel)
		info, statErr := os.Stat(full)
		if statErr != nil {
			t.Errorf("missing expected file %s: %v", rel, statErr)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected non-empty file %s", rel)
		}
	}
}

type mockController struct {
	mu          sync.Mutex
	mode        string
	idleTimeout int32
	panicked    bool
	paused      bool
	volume      float32
	spoken      []ws.SpeakRequest
}

func newMockController() *mockController {
	return &mockController{
		mode:        config.TTSModeAlwaysOn,
		idleTimeout: 15,
		volume:      0.8,
	}
}

func (m *mockController) SnapshotState() *controlv1.StateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &controlv1.StateSnapshot{
		TtsEnabled:         true,
		OutputMode:         ws.ModeToProto(m.mode),
		IdleTimeoutSeconds: m.idleTimeout,
		SpeakingActive:     m.mode != config.TTSModeAlwaysOff,
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

func (m *mockController) SpeakCustom(req ws.SpeakRequest) error {
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
	switch action {
	case controlv1.QueueAction_QUEUE_ACTION_PAUSE:
		m.paused = true
	case controlv1.QueueAction_QUEUE_ACTION_RESUME:
		m.paused = false
	case controlv1.QueueAction_QUEUE_ACTION_CLEAR:
		m.spoken = nil
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

func findNodeBinary() string {
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".asdf", "installs", "nodejs", "22.11.0", "bin", "node"),
		filepath.Join(home, ".asdf", "installs", "nodejs", "25.9.0", "bin", "node"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

type sdHostFrame struct {
	Event   string         `json:"event"`
	UUID    string         `json:"uuid,omitempty"`
	Context string         `json:"context,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

func TestStreamDeckPluginEndToEndWithHostSimulator(t *testing.T) {
	nodeBin := findNodeBinary()
	if nodeBin == "" {
		t.Skip("node binary not found in PATH; skipping Stream Deck host simulator integration test")
	}

	baseDir := t.TempDir()
	installedDir, err := streamdeck.InstallPlugin(baseDir)
	if err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	pluginJS := filepath.Join(installedDir, "bin", "plugin.js")

	ctrl := newMockController()
	tcServer := ws.NewServer(ctrl)
	if err := tcServer.Start("127.0.0.1", 0, ""); err != nil {
		t.Fatalf("tcServer.Start: %v", err)
	}
	defer func() { _ = tcServer.Stop() }()

	_, tcPortStr, err := net.SplitHostPort(tcServer.Addr())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	tcPort, err := strconv.Atoi(tcPortStr)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}

	// Start Mock Elgato Stream Deck Host WebSocket server
	sdFramesCh := make(chan sdHostFrame, 64)
	sdConnCh := make(chan *websocket.Conn, 1)

	sdListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = sdListener.Close() }()

	_, sdPortStr, _ := net.SplitHostPort(sdListener.Addr().String())

	sdMux := http.NewServeMux()
	sdServer := &websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			return nil
		},
		Handler: websocket.Handler(func(conn *websocket.Conn) {
			select {
			case sdConnCh <- conn:
			default:
			}
			for {
				var msg string
				if readErr := websocket.Message.Receive(conn, &msg); readErr != nil {
					return
				}
				var f sdHostFrame
				if jsonErr := json.Unmarshal([]byte(msg), &f); jsonErr == nil {
					sdFramesCh <- f
				}
			}
		}),
	}
	sdMux.Handle("/", sdServer)
	sdHTTP := &http.Server{
		Handler:           sdMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = sdHTTP.Serve(sdListener) }()
	defer func() { _ = sdHTTP.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	tcURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?format=protobuf", tcPort)
	cmd := exec.CommandContext(
		ctx,
		nodeBin,
		pluginJS,
		"-port", sdPortStr,
		"-pluginUUID", "plugin-uuid-1",
		"-registerEvent", "registerPlugin",
		"-tcUrl", tcURL,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start plugin.js: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	var sdConn *websocket.Conn
	select {
	case sdConn = <-sdConnCh:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for plugin.js to connect to mock Stream Deck host")
	}

	// Wait for registerPlugin event
	registered := false
	for !registered {
		select {
		case f := <-sdFramesCh:
			if f.Event == "registerPlugin" && f.UUID == "plugin-uuid-1" {
				registered = true
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for registerPlugin frame")
		}
	}

	sendSD := func(msg map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(msg)
		if err := websocket.Message.Send(sdConn, string(raw)); err != nil {
			t.Fatalf("websocket.Message.Send: %v", err)
		}
	}

	// 1. Send willAppear for mode, panic, and volume actions
	sendSD(map[string]any{
		"event":   "willAppear",
		"action":  "com.github.mrpoundsign.towncrier.mode",
		"context": "ctx-mode",
		"payload": map[string]any{"settings": map[string]any{}},
	})
	sendSD(map[string]any{
		"event":   "willAppear",
		"action":  "com.github.mrpoundsign.towncrier.panic",
		"context": "ctx-panic",
		"payload": map[string]any{"settings": map[string]any{}},
	})
	sendSD(map[string]any{
		"event":   "willAppear",
		"action":  "com.github.mrpoundsign.towncrier.volume",
		"context": "ctx-vol",
		"payload": map[string]any{"settings": map[string]any{"step": 5}},
	})

	// Wait until initial setImage for ctx-mode arrives
	gotModeImage := false
	for !gotModeImage {
		select {
		case f := <-sdFramesCh:
			if f.Event == "setImage" && f.Context == "ctx-mode" {
				img, _ := f.Payload["image"].(string)
				decoded, _ := url.PathUnescape(img)
				if strings.Contains(decoded, "ALWAYS ON") {
					gotModeImage = true
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for initial setImage on ctx-mode")
		}
	}

	// 2. Press Mode button -> should cycle from ALWAYS_ON to AUTO_IDLE via binary Protobuf over WebSocket
	sendSD(map[string]any{
		"event":   "keyDown",
		"action":  "com.github.mrpoundsign.towncrier.mode",
		"context": "ctx-mode",
		"payload": map[string]any{"settings": map[string]any{}},
	})

	gotAFKImage := false
	for !gotAFKImage {
		select {
		case f := <-sdFramesCh:
			if f.Event == "setImage" && f.Context == "ctx-mode" {
				img, _ := f.Payload["image"].(string)
				decoded, _ := url.PathUnescape(img)
				if strings.Contains(decoded, "AUTO-AFK") {
					gotAFKImage = true
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for AUTO-AFK image update on ctx-mode")
		}
	}
	if got := ctrl.SnapshotState().GetOutputMode(); got != controlv1.TTSOutputMode_TTS_OUTPUT_MODE_AUTO_IDLE {
		t.Fatalf("controller TTS mode = %v, want AUTO_IDLE", got)
	}

	// 3. Press Panic Mute button -> should toggle Panicked=true and send PANIC MUTED SVG
	sendSD(map[string]any{
		"event":   "keyDown",
		"action":  "com.github.mrpoundsign.towncrier.panic",
		"context": "ctx-panic",
		"payload": map[string]any{"settings": map[string]any{}},
	})

	gotPanicMuted := false
	for !gotPanicMuted {
		select {
		case f := <-sdFramesCh:
			if f.Event == "setImage" && f.Context == "ctx-panic" {
				img, _ := f.Payload["image"].(string)
				decoded, _ := url.PathUnescape(img)
				if strings.Contains(decoded, "MUTED") {
					gotPanicMuted = true
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for PANIC MUTED image on ctx-panic")
		}
	}
	if !ctrl.SnapshotState().GetPanicked() {
		t.Fatalf("expected controller Panicked=true")
	}

	// 4. Rotate Volume Dial (+2 ticks * 5% = +0.10 -> 90%)
	sendSD(map[string]any{
		"event":   "dialRotate",
		"action":  "com.github.mrpoundsign.towncrier.volume",
		"context": "ctx-vol",
		"payload": map[string]any{
			"ticks":    2,
			"settings": map[string]any{"step": 5},
		},
	})

	gotVol90 := false
	for !gotVol90 {
		select {
		case f := <-sdFramesCh:
			if f.Event == "setFeedback" && f.Context == "ctx-vol" {
				val, _ := f.Payload["value"].(string)
				if val == "90%" {
					gotVol90 = true
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for setFeedback value=90%% on ctx-vol")
		}
	}
}
