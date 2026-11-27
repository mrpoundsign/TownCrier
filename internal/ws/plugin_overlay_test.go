package ws

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
)

type mockPluginOverlayController struct {
	assets map[string]struct {
		ct   string
		body []byte
	}
}

func (m *mockPluginOverlayController) SnapshotState() *controlv1.StateSnapshot {
	return &controlv1.StateSnapshot{}
}
func (m *mockPluginOverlayController) SetControlTTSMode(string, *int32) error { return nil }
func (m *mockPluginOverlayController) CycleControlTTSMode() string            { return "always_on" }
func (m *mockPluginOverlayController) SpeakCustom(SpeakRequest) error         { return nil }
func (m *mockPluginOverlayController) ExecutePanicAction(controlv1.PanicAction) error {
	return nil
}
func (m *mockPluginOverlayController) ExecuteQueueAction(controlv1.QueueAction) error {
	return nil
}
func (m *mockPluginOverlayController) AdjustVolume(*float32, *float32) error { return nil }
func (m *mockPluginOverlayController) GetPluginOverlayAsset(_ context.Context, uuid, assetPath string) (string, []byte, bool, error) {
	if item, ok := m.assets[uuid+":"+assetPath]; ok {
		return item.ct, item.body, true, nil
	}
	return "", nil, false, nil
}

func TestPluginOverlayRoutesAndSSE(t *testing.T) {
	ctrl := &mockPluginOverlayController{
		assets: map[string]struct {
			ct   string
			body []byte
		}{
			"com.example.dj:index.html": {ct: "text/html; charset=utf-8", body: []byte("<html>DJ</html>")},
			"com.example.dj:bad.exe":    {ct: "application/octet-stream", body: []byte("MZ")},
		},
	}
	srv := NewServer(ctrl)
	require.NoError(t, srv.Start("127.0.0.1", 0, ""))
	defer func() { _ = srv.Stop() }()

	base := "http://" + srv.Addr()

	// 1. Serves index.html overlay
	resp, err := http.Get(base + "/plugins/com.example.dj/overlay")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "<html>DJ</html>", string(body))

	// 2. Blocks path traversal
	resp2, err := http.Get(base + "/plugins/com.example.dj/overlay/..%2Fsecret")
	require.NoError(t, err)
	_ = resp2.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)

	// 3. Blocks disallowed MIME
	resp3, err := http.Get(base + "/plugins/com.example.dj/overlay/bad.exe")
	require.NoError(t, err)
	_ = resp3.Body.Close()
	require.Equal(t, http.StatusForbidden, resp3.StatusCode)

	// 4. Scoped SSE /plugins/{uuid}/events filters by UUID
	sseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, base+"/plugins/com.example.dj/events", nil)
	require.NoError(t, err)
	sseResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = sseResp.Body.Close() }()
	require.Equal(t, http.StatusOK, sseResp.StatusCode)

	reader := bufio.NewReader(sseResp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, ": connected")
	_, _ = reader.ReadString('\n') // blank line

	// Push unrelated plugin event first (must be filtered out), then matching event
	srv.BroadcastPluginEvent("com.example.other", "ignored", `{"x":1}`)
	srv.BroadcastPluginEvent("com.example.dj", "now_playing", `{"title":"Track A"}`)

	var collected strings.Builder
	for range 4 {
		l, rErr := reader.ReadString('\n')
		require.NoError(t, rErr)
		collected.WriteString(l)
		if strings.Contains(collected.String(), "Track A") {
			break
		}
	}
	outStr := collected.String()
	require.Contains(t, outStr, "event: now_playing")
	require.Contains(t, outStr, "Track A")
	require.NotContains(t, outStr, "ignored")
}
