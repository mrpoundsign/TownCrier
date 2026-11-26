package pluginhost

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func repoRootFromPluginHost(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func TestEndToEndWasmAndNativeReferencePlugins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Go compiler e2e plugin build in -short mode")
	}
	t.Parallel()

	root := repoRootFromPluginHost(t)
	baseDir := t.TempDir()
	wasmDir := filepath.Join(baseDir, "wasm", "wasm-greeter")
	nativeDir := filepath.Join(baseDir, "native", "native-echo")
	require.NoError(t, os.MkdirAll(wasmDir, 0o755))
	require.NoError(t, os.MkdirAll(nativeDir, 0o755))

	// Build wasm-greeter and native-echo concurrently to cut compiler wait time in half.
	wasmOut := filepath.Join(wasmDir, "wasm-greeter.wasm")
	binName := "native-echo"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	nativeBinOut := filepath.Join(nativeDir, binName)

	var (
		wasmErr, nativeErr error
		wasmOutLog, nativeOutLog []byte
		buildWG sync.WaitGroup
	)
	buildWG.Add(2)
	go func() {
		defer buildWG.Done()
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmOut, "./examples/plugins/wasm-greeter")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		wasmOutLog, wasmErr = cmd.CombinedOutput()
	}()
	go func() {
		defer buildWG.Done()
		cmd := exec.Command("go", "build", "-o", nativeBinOut, "./examples/plugins/native-echo")
		cmd.Dir = root
		nativeOutLog, nativeErr = cmd.CombinedOutput()
	}()
	buildWG.Wait()
	require.NoError(t, wasmErr, "build wasm-greeter failed: %s", string(wasmOutLog))
	require.NoError(t, nativeErr, "build native-echo failed: %s", string(nativeOutLog))

	pjBytes, err := os.ReadFile(filepath.Join(root, "examples", "plugins", "native-echo", "plugin.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(nativeDir, "plugin.json"), pjBytes, 0o644))

	// Setup Manager with isolated memory config + secrets + :memory: KV
	var mu sync.Mutex
	cfg := config.DefaultConfig()
	var spoken []SpeakRequest
	var broadcasts []string

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("async-pong"))
	}))
	defer httpSrv.Close()

	mgr := NewManager(Options{
		BaseDir:  baseDir,
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			AppVersion: "v1.0.0-test",
			OnSpeak: func(req SpeakRequest) error {
				mu.Lock()
				spoken = append(spoken, req)
				mu.Unlock()
				return nil
			},
			OnOverlayBroadcast: func(uuid, eventName, payload string) {
				mu.Lock()
				broadcasts = append(broadcasts, uuid+":"+eventName+":"+payload)
				mu.Unlock()
			},
			GetConfig: func() *config.Config {
				mu.Lock()
				defer mu.Unlock()
				return cfg.Clone()
			},
			UpdateConfig: func(fn func(c *config.Config)) error {
				mu.Lock()
				fn(cfg)
				mu.Unlock()
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.Start())

	// Native plugin starts in StatusAwaitingTrust; WASM starts StatusRunning immediately
	statuses := mgr.Plugins()
	require.Len(t, statuses, 2)
	byUUID := make(map[string]PluginStatus, 2)
	for _, s := range statuses {
		byUUID[s.UUID] = s
	}
	wasmSt := byUUID["com.github.mrpoundsign.towncrier.examples.wasm-greeter"]
	nativeSt := byUUID["com.github.mrpoundsign.towncrier.examples.native-echo"]
	require.Equal(t, StatusRunning, wasmSt.State)
	require.Equal(t, StatusAwaitingTrust, nativeSt.State)

	// Approve native plugin trust -> both should be StatusRunning
	require.NoError(t, mgr.TrustNativePlugin(nativeSt.UUID))
	for _, s := range mgr.Plugins() {
		require.Equal(t, StatusRunning, s.State, "plugin %s error: %s", s.UUID, s.Error)
	}

	// Trigger WASM config action -> invokes KV + SaveGlobalSettings + Speak
	gs := mgr.GetGlobalSettings(wasmSt.UUID)
	gs = pluginsdk.SetSettingString(gs, "template", "Hello {user} (#{count})")
	require.NoError(t, mgr.InvokeConfigAction(context.Background(), wasmSt.UUID, "testGreet", gs))

	mu.Lock()
	require.Len(t, spoken, 1)
	require.Equal(t, "Hello TestViewer (#1)", spoken[0].Text)
	mu.Unlock()

	// Dispatch follow event -> WASM greets #2 using persisted KV count; Dispatch message -> Native echoes via OverlayBroadcast
	mgr.DispatchStreamEvent(&chat.Event{
		ID:          "ev-follow-1",
		Platform:    "twitch",
		Kind:        chat.EventKindFollow,
		UserName:    "bob",
		DisplayName: "Bob",
	})
	mgr.DispatchStreamEvent(&chat.Event{
		ID:          "ev-msg-1",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "ping",
	})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spoken) >= 2 && len(broadcasts) >= 1
	}, 5*time.Second, 25*time.Millisecond)

	mu.Lock()
	require.Equal(t, "Hello Bob (#2)", spoken[1].Text)
	require.Contains(t, broadcasts[0], `"text":"ping"`)
	mu.Unlock()

	// Trigger Native async HTTP via OnConfigAction
	pingSettings := pluginsdk.SetSettingString(nil, "ping_url", httpSrv.URL)
	require.NoError(t, mgr.InvokeConfigAction(context.Background(), nativeSt.UUID, "ping", pingSettings))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, b := range broadcasts {
			if strings.Contains(b, "async-pong") {
				return true
			}
		}
		return false
	}, 5*time.Second, 25*time.Millisecond)

	// Verify native stdout was captured in logs without corrupting TCP Protobuf frames
	logs := mgr.PluginLogs(nativeSt.UUID)
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, "\n"), "native-echo stdout diagnostic ready")

	// Hot reload cleanly tears down and restarts both plugins, preserving KV count (#3)
	require.NoError(t, mgr.Reload())
	for _, s := range mgr.Plugins() {
		require.Equal(t, StatusRunning, s.State, "after reload plugin %s err=%s", s.UUID, s.Error)
	}
	require.NoError(t, mgr.InvokeConfigAction(context.Background(), wasmSt.UUID, "testGreet", nil))
	mu.Lock()
	require.Len(t, spoken, 3)
	require.Equal(t, "Hello TestViewer (#3)", spoken[2].Text)
	mu.Unlock()

	// Per-stream first-message greeting:
	// 1. Offline chat message is ignored when require_live_stream=true (default)
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-offline",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-alice",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "hello offline",
	}, 0, false)

	// 2. First message in live session #101 greets Alice (#4)
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-s101-1",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-alice",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "hey chat!",
	}, 101, true)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spoken) >= 4
	}, 5*time.Second, 25*time.Millisecond)
	mu.Lock()
	require.Equal(t, "Hey Alice, welcome in!", spoken[3].Text)
	mu.Unlock()

	// 3. Second message from Alice, broadcaster message, and bot message in #101 do NOT greet;
	// switching to live session #102 greets Alice again (#5)!
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-s101-2",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-alice",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "second message same stream",
	}, 101, true)
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-broadcaster",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-streamer",
		UserName:    "streamer",
		DisplayName: "Streamer",
		Roles:       []string{"broadcaster"},
		Text:        "testing",
	}, 101, true)
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-bot",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-bot",
		UserName:    "nightbot",
		DisplayName: "Nightbot",
		IsBotSignal: true,
		Text:        "!commands",
	}, 101, true)
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-s102-1",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-alice",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "new stream hi!",
	}, 102, true)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spoken) >= 5
	}, 5*time.Second, 25*time.Millisecond)
	mu.Lock()
	require.Len(t, spoken, 5)
	require.Equal(t, "Hey Alice, welcome in!", spoken[4].Text)
	mu.Unlock()

	// 4. Reset greeted chatters via config button -> Alice is greeted again in #102 (#6)
	require.NoError(t, mgr.InvokeConfigAction(context.Background(), wasmSt.UUID, "resetStreamGreets", nil))
	mgr.DispatchStreamEventWithState(&chat.Event{
		ID:          "ev-msg-s102-after-reset",
		Platform:    "twitch",
		Kind:        chat.EventKindMessage,
		UserID:      "u-alice",
		UserName:    "alice",
		DisplayName: "Alice",
		Text:        "hello after reset",
	}, 102, true)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(spoken) >= 6
	}, 5*time.Second, 25*time.Millisecond)
	mu.Lock()
	require.Len(t, spoken, 6)
	require.Equal(t, "Hey Alice, welcome in!", spoken[5].Text)
	mu.Unlock()

	// Simulate unexpected native plugin process crash -> StatusError -> RestartPlugin recovers to StatusRunning
	mgr.mu.RLock()
	nativeEnt := mgr.entries[nativeSt.UUID]
	mgr.mu.RUnlock()
	require.NotNil(t, nativeEnt)
	nativeEnt.mu.RLock()
	nb, isNative := nativeEnt.backend.(*nativeBackend)
	nativeEnt.mu.RUnlock()
	require.True(t, isNative)
	require.NotNil(t, nb.cmd)
	require.NotNil(t, nb.cmd.Process)
	require.NoError(t, nb.cmd.Process.Kill())

	require.Eventually(t, func() bool {
		for _, s := range mgr.Plugins() {
			if s.UUID == nativeSt.UUID && s.State == StatusError {
				return true
			}
		}
		return false
	}, 5*time.Second, 25*time.Millisecond)

	require.NoError(t, mgr.RestartPlugin(nativeSt.UUID))
	for _, s := range mgr.Plugins() {
		require.Equal(t, StatusRunning, s.State, "after restart plugin %s err=%s", s.UUID, s.Error)
	}
}

func TestVersionMismatchHandshakeError(t *testing.T) {
	t.Parallel()
	// Verify native TCRP preamble over a stream rejects incompatible major API version with structured APIError.
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	done := make(chan error, 1)
	go func() {
		maj, minHost, tok, err := pluginsdk.ReadPreamble(serverConn)
		if err != nil {
			done <- err
			return
		}
		if tok != "tok" {
			done <- os.ErrInvalid
			return
		}
		verErr := CheckVersion(uint32(maj), uint32(minHost))
		done <- pluginsdk.WriteFrame(serverConn, NewHandshakeResponse(verErr))
	}()

	require.NoError(t, pluginsdk.WritePreamble(clientConn, 2, 0, "tok"))
	var resp pluginv1.HandshakeResponse
	require.NoError(t, pluginsdk.ReadFrame(clientConn, &resp))
	require.NoError(t, <-done)

	require.False(t, resp.GetOk())
	require.NotNil(t, resp.GetError())
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, resp.GetError().GetCode())
	require.Contains(t, resp.GetError().GetMessage(), "API v2")
}

func TestEndToEndNativeUIAndOverlayAssetPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Go compiler e2e plugin build in -short mode")
	}
	t.Parallel()

	root := repoRootFromPluginHost(t)
	baseDir := t.TempDir()
	plugDir := filepath.Join(baseDir, "native", "ui-demo")
	require.NoError(t, os.MkdirAll(plugDir, 0o755))

	binName := "ui-demo"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binOut := filepath.Join(plugDir, binName)
	buildCmd := exec.Command("go", "build", "-o", binOut, "./examples/plugins/ui-demo")
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "build ui-demo failed: %s", string(out))

	pjBytes, err := os.ReadFile(filepath.Join(root, "examples", "plugins", "ui-demo", "plugin.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(plugDir, "plugin.json"), pjBytes, 0o644))

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

	var pushed []PluginUIUpdate
	var pmu sync.Mutex
	unsub := mgr.SubscribeUIUpdate(func(u PluginUIUpdate) {
		pmu.Lock()
		pushed = append(pushed, u)
		pmu.Unlock()
	})
	defer unsub()

	require.NoError(t, mgr.Start())
	require.NoError(t, mgr.TrustNativePlugin("com.example.uidemo"))

	snap, err := mgr.RenderView(context.Background(), "com.example.uidemo", "main")
	require.NoError(t, err)
	require.Equal(t, "main", snap.GetViewId())
	require.Equal(t, uint64(1), snap.GetRevision())

	_, patch, err := mgr.SendUIEvent(context.Background(), "com.example.uidemo", &pluginv1.UIEvent{
		ViewId: "main",
		NodeId: "inc",
		Kind:   pluginv1.UIEventKind_UI_EVENT_KIND_CLICK,
	})
	require.NoError(t, err)
	require.NotNil(t, patch)
	require.Equal(t, uint64(2), patch.GetRevision())

	require.Eventually(t, func() bool {
		pmu.Lock()
		defer pmu.Unlock()
		return len(pushed) >= 1
	}, 3*time.Second, 25*time.Millisecond)

	ct, body, found, err := mgr.GetOverlayAsset(context.Background(), "com.example.uidemo", "index.html")
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, ct, "text/html")
	require.Equal(t, "<h1>Demo</h1>", string(body))
}
