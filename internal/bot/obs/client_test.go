package obs

import (
	"context"
	"encoding/json"
	"net/http"
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

func TestOBSClientAuthCallsAndEvents(t *testing.T) {
	const (
		password  = "secret123"
		salt      = "saltABC"
		challenge = "chalXYZ"
	)

	var mu sync.Mutex
	var gotRequests []string
	var receivedEvent = make(chan Event, 1)

	srv := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()
		_ = sendEnvelope(conn, opHello, map[string]any{
			"obsWebSocketVersion": "5.4.2",
			"rpcVersion":          1,
			"authentication": map[string]string{
				"salt":      salt,
				"challenge": challenge,
			},
		})

		env, err := recvEnvelope(conn)
		if err != nil || env.Op != opIdentify {
			return
		}
		var id identifyPayload
		_ = json.Unmarshal(env.D, &id)
		if id.Authentication != ComputeAuth(password, salt, challenge) {
			return
		}
		_ = sendEnvelope(conn, opIdentified, map[string]any{"negotiatedRpcVersion": 1})

		// Push one async OBS event right after Identify
		_ = sendEnvelope(conn, opEvent, map[string]any{
			"eventType": "CurrentProgramSceneChanged",
			"eventData": map[string]any{"sceneName": "Gaming"},
		})

		for {
			reqEnv, rErr := recvEnvelope(conn)
			if rErr != nil {
				return
			}
			if reqEnv.Op != opRequest {
				continue
			}
			var req requestPayload
			_ = json.Unmarshal(reqEnv.D, &req)
			mu.Lock()
			gotRequests = append(gotRequests, req.RequestType)
			mu.Unlock()

			respData := map[string]any{}
			switch req.RequestType {
			case "GetVersion":
				respData = map[string]any{"obsVersion": "30.2.0", "obsWebSocketVersion": "5.4.2"}
			case "GetSceneItemId":
				respData = map[string]any{"sceneItemId": 42}
			}
			_ = sendEnvelope(conn, opRequestResponse, map[string]any{
				"requestType": req.RequestType,
				"requestId":   req.RequestID,
				"requestStatus": map[string]any{
					"result": true,
					"code":   100,
				},
				"responseData": respData,
			})
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	host, _, _ := strings.Cut(u.Host, ":")

	client := NewClient(func(ev Event) {
		select {
		case receivedEvent <- ev:
		default:
		}
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, client.Connect(ctx, host, port, password))
	defer client.Disconnect()

	st := client.Status()
	require.Equal(t, StateConnected, st.State)
	require.Equal(t, "30.2.0", st.OBSVersion)

	select {
	case ev := <-receivedEvent:
		require.Equal(t, "CurrentProgramSceneChanged", ev.Type)
		require.Equal(t, "Gaming", ev.Data["sceneName"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OBS event")
	}

	require.NoError(t, client.SetCurrentProgramScene(ctx, "StartingSoon"))
	require.NoError(t, client.SetSceneItemEnabled(ctx, "Main", "Camera", false))
	require.NoError(t, client.SetInputMute(ctx, "Mic/Aux", true))
	require.NoError(t, client.ToggleRecord(ctx))
	require.NoError(t, client.ToggleStream(ctx))
	require.NoError(t, client.SaveReplayBuffer(ctx))
	require.NoError(t, client.RefreshBrowserSource(ctx, "ChatOverlay"))

	mu.Lock()
	reqs := append([]string(nil), gotRequests...)
	mu.Unlock()
	require.Contains(t, reqs, "SetCurrentProgramScene")
	require.Contains(t, reqs, "GetSceneItemId")
	require.Contains(t, reqs, "SetSceneItemEnabled")
	require.Contains(t, reqs, "SetInputMute")
	require.Contains(t, reqs, "ToggleRecord")
	require.Contains(t, reqs, "ToggleStream")
	require.Contains(t, reqs, "SaveReplayBuffer")
	require.Contains(t, reqs, "PressInputPropertiesButton")
	_ = http.StatusOK
}
