package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

type mockPluginOverlayController struct {
	assets map[string]struct {
		ct   string
		body []byte
	}
	widgets      []PluginOverlayWidget
	widgetStyles map[string]config.PluginOverlayStyle
	overlayStyle OverlayStyleConfig
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
func (m *mockPluginOverlayController) GetOverlayStyle() OverlayStyleConfig {
	return m.overlayStyle
}
func (m *mockPluginOverlayController) SaveOverlayStyle(customCSS, styleJSON string) error {
	m.overlayStyle.CustomCSS = customCSS
	m.overlayStyle.StyleJSON = styleJSON
	return nil
}
func (m *mockPluginOverlayController) ListPluginOverlayWidgets() []PluginOverlayWidget {
	out := make([]PluginOverlayWidget, 0, len(m.widgets))
	for _, w := range m.widgets {
		cp := w
		cp.Style = m.GetPluginOverlayStyle(w.PluginUUID, w.WidgetID)
		out = append(out, cp)
	}
	return out
}
func (m *mockPluginOverlayController) GetPluginOverlayStyle(pluginUUID, widgetID string) config.PluginOverlayStyle {
	if m.widgetStyles == nil {
		return config.PluginOverlayStyle{}
	}
	st := m.widgetStyles[PluginOverlayStyleStorageKey(pluginUUID, widgetID)]
	if st.Values != nil {
		st.Values = maps.Clone(st.Values)
	}
	return st
}
func (m *mockPluginOverlayController) SavePluginOverlayStyle(pluginUUID, widgetID string, style config.PluginOverlayStyle) error {
	if m.widgetStyles == nil {
		m.widgetStyles = make(map[string]config.PluginOverlayStyle)
	}
	cp := config.PluginOverlayStyle{
		CustomCSS:    style.CustomCSS,
		UseHostTheme: style.UseHostTheme,
	}
	if style.Values != nil {
		cp.Values = maps.Clone(style.Values)
	}
	m.widgetStyles[PluginOverlayStyleStorageKey(pluginUUID, widgetID)] = cp
	return nil
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

func TestPluginOverlayWidgetsReplayStateStyleDesignerAndSDK(t *testing.T) {
	ctrl := &mockPluginOverlayController{
		assets: map[string]struct {
			ct   string
			body []byte
		}{
			"com.example.dj:now_playing.html": {ct: "text/html; charset=utf-8", body: []byte("<html>DJ Widget</html>")},
		},
		overlayStyle: OverlayStyleConfig{
			StyleJSON: `{"nameColor":"#22D3EE","bgColor":"#111827","bgOpacity":80,"cardRadius":16}`,
		},
		widgets: []PluginOverlayWidget{
			{
				Key:               PluginWidgetLayoutKey("com.example.dj", "now_playing"),
				PluginUUID:        "com.example.dj",
				PluginName:        "CrowdDJ",
				WidgetID:          "now_playing",
				Name:              "Now Playing",
				EntryAsset:        "now_playing.html",
				DefaultX:          60,
				DefaultY:          70,
				DefaultW:          35,
				DefaultH:          20,
				MinW:              15,
				MinH:              10,
				DefaultVisible:    true,
				SupportsHostTheme: true,
				AllowCustomCSS:    true,
				StyleSchema: []PluginOverlayWidgetField{
					{Key: "accent_color", Label: "Accent", Type: "color", Default: "#38BDF8"},
				},
			},
		},
		widgetStyles: map[string]config.PluginOverlayStyle{
			"com.example.dj:now_playing": {
				Values:       map[string]any{"accent_color": "#A855F7"},
				UseHostTheme: true,
			},
		},
	}

	srv := NewServer(ctrl)
	// Disable all built-in overlays to verify active plugin widgets alone unlock /overlay, /overlay/designer, and /api/overlay/style.
	srv.SetFeatures(false, false, false)
	srv.SetSubtitlesOverlayEnabled(false)
	require.NoError(t, srv.Start("127.0.0.1", 0, ""))
	defer func() { _ = srv.Stop() }()

	// Broadcast before SSE client connects to verify replay-on-connect.
	srv.BroadcastPluginEvent("com.example.dj", "now_playing", `{"title":"Before Connect"}`)

	base := "http://" + srv.Addr()

	// 1. /overlay, /overlay/designer, and /overlay/assets/sdk.js are available.
	for _, route := range []string{"/overlay", "/overlay/designer", "/overlay/assets/sdk.js"} {
		resp, err := http.Get(base + route)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "route=%s", route)
		switch route {
		case "/overlay/assets/sdk.js":
			require.Contains(t, string(body), "TownCrierOverlay")
		case "/overlay":
			require.Contains(t, string(body), "plugin-widget-frame")
		case "/overlay/designer":
			require.Contains(t, string(body), "controlsPluginWidget")
			require.Contains(t, string(body), "pluginPreviewFrame")
		}
	}

	// 2. /api/overlay/style returns plugin_widgets and resolved host_theme.
	styleResp, err := http.Get(base + "/api/overlay/style")
	require.NoError(t, err)
	var styleMeta OverlayStyleConfig
	require.NoError(t, json.NewDecoder(styleResp.Body).Decode(&styleMeta))
	_ = styleResp.Body.Close()
	require.Len(t, styleMeta.PluginWidgets, 1)
	require.Equal(t, "plugin:com.example.dj:now_playing", styleMeta.PluginWidgets[0].Key)
	require.Equal(t, "#22D3EE", styleMeta.HostTheme["--tc-accent"])
	require.Equal(t, "16px", styleMeta.HostTheme["--tc-radius"])

	// 3. /plugins/{uuid}/state & /plugins/{uuid}/widget-style return cached events + style.
	stateResp, err := http.Get(base + "/plugins/com.example.dj/state")
	require.NoError(t, err)
	var stateBody struct {
		Events map[string]json.RawMessage `json:"events"`
	}
	require.NoError(t, json.NewDecoder(stateResp.Body).Decode(&stateBody))
	_ = stateResp.Body.Close()
	require.Contains(t, string(stateBody.Events["now_playing"]), "Before Connect")

	wsResp, err := http.Get(base + "/plugins/com.example.dj/widget-style?widget=now_playing")
	require.NoError(t, err)
	var wsEv PluginOverlayStyleEvent
	require.NoError(t, json.NewDecoder(wsResp.Body).Decode(&wsEv))
	_ = wsResp.Body.Close()
	require.Equal(t, "#A855F7", wsEv.Style.Values["accent_color"])
	require.True(t, wsEv.Style.UseHostTheme)

	// 4. SSE /plugins/{uuid}/events?widget=now_playing replays cached event AND initial style,
	// then streams live style updates posted via /api/overlay/style target="plugin_widget".
	sseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, base+"/plugins/com.example.dj/events?widget=now_playing", nil)
	require.NoError(t, err)
	sseResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = sseResp.Body.Close() }()

	reader := bufio.NewReader(sseResp.Body)
	var replayBuf strings.Builder
	for range 12 {
		l, rErr := reader.ReadString('\n')
		require.NoError(t, rErr)
		replayBuf.WriteString(l)
		if strings.Contains(replayBuf.String(), "Before Connect") && strings.Contains(replayBuf.String(), "#A855F7") {
			break
		}
	}
	require.Contains(t, replayBuf.String(), "Before Connect")
	require.Contains(t, replayBuf.String(), "event: style")
	require.Contains(t, replayBuf.String(), "#A855F7")

	postPayload := map[string]any{
		"target":      "plugin_widget",
		"plugin_uuid": "com.example.dj",
		"widget_id":   "now_playing",
		"plugin_style": map[string]any{
			"values":         map[string]any{"accent_color": "#10B981"},
			"custom_css":     ".card { opacity: 0.75; }",
			"use_host_theme": false,
		},
	}
	rawPost, err := json.Marshal(postPayload)
	require.NoError(t, err)
	postResp, err := http.Post(base+"/api/overlay/style", "application/json", bytes.NewReader(rawPost))
	require.NoError(t, err)
	_ = postResp.Body.Close()
	require.Equal(t, http.StatusOK, postResp.StatusCode)
	require.Equal(t, "#10B981", ctrl.GetPluginOverlayStyle("com.example.dj", "now_playing").Values["accent_color"])

	var liveBuf strings.Builder
	for range 8 {
		l, rErr := reader.ReadString('\n')
		require.NoError(t, rErr)
		liveBuf.WriteString(l)
		if strings.Contains(liveBuf.String(), "#10B981") {
			break
		}
	}
	require.Contains(t, liveBuf.String(), "event: style")
	require.Contains(t, liveBuf.String(), "#10B981")

	// 5. Clearing widgets reverts /overlay to 404 when built-in overlays remain off.
	ctrl.widgets = nil
	res404, err := http.Get(base + "/overlay")
	require.NoError(t, err)
	_ = res404.Body.Close()
	require.Equal(t, http.StatusNotFound, res404.StatusCode)
}
