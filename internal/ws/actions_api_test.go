package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/ws/controlv1"
)

type mockActionController struct {
	catalog    ExternalActionCatalog
	lastBot    string
	lastBotArg map[string]string
	lastPlug   string
	lastPlugAc string
	lastPlugAr map[string]string
}

func (m *mockActionController) SnapshotState() *controlv1.StateSnapshot {
	return &controlv1.StateSnapshot{}
}
func (m *mockActionController) SetControlTTSMode(string, *int32) error          { return nil }
func (m *mockActionController) CycleControlTTSMode() string                     { return "always_on" }
func (m *mockActionController) SpeakCustom(SpeakRequest) error                  { return nil }
func (m *mockActionController) ExecutePanicAction(controlv1.PanicAction) error  { return nil }
func (m *mockActionController) ExecuteQueueAction(controlv1.QueueAction) error  { return nil }
func (m *mockActionController) AdjustVolume(*float32, *float32) error           { return nil }
func (m *mockActionController) ListExternalActions() ExternalActionCatalog      { return m.catalog }
func (m *mockActionController) RunExternalBotAction(id string, args map[string]string, _ bool) error {
	if id == "missing" {
		return errors.New("not found")
	}
	m.lastBot = id
	m.lastBotArg = args
	return nil
}
func (m *mockActionController) RunExternalPluginAction(_ context.Context, uuid, act string, args map[string]string) error {
	if uuid == "" || act == "" {
		return errors.New("missing ids")
	}
	m.lastPlug = uuid
	m.lastPlugAc = act
	m.lastPlugAr = args
	return nil
}

func TestHTTPExternalActionsEndpoints(t *testing.T) {
	ctrl := &mockActionController{
		catalog: ExternalActionCatalog{
			BotActions: []ExternalBotAction{{ID: "act-1", Name: "Announce Live", Enabled: true, QueueID: "default"}},
			PluginActions: []ExternalPluginAction{{
				PluginUUID: "com.github.mrpoundsign.crowddj",
				PluginName: "CrowdDJ",
				ActionID:   "skip",
				ActionName: "Skip Current Track",
			}},
		},
	}
	srv := NewServer(ctrl)

	mux := http.NewServeMux()
	mux.Handle("/api/actions", srv.withCORSAndAuth(srv.withControlRequired(http.HandlerFunc(srv.handleHTTPListActions))))
	mux.Handle("/api/bot/run", srv.withCORSAndAuth(srv.withControlRequired(http.HandlerFunc(srv.handleHTTPRunBotAction))))
	mux.Handle("/api/plugins/run", srv.withCORSAndAuth(srv.withControlRequired(http.HandlerFunc(srv.handleHTTPRunPluginAction))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/actions")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var cat ExternalActionCatalog
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cat))
	require.Len(t, cat.BotActions, 1)
	require.Len(t, cat.PluginActions, 1)
	require.Equal(t, "skip", cat.PluginActions[0].ActionID)

	botResp, err := http.Post(ts.URL+"/api/bot/run", "application/json", bytes.NewBufferString(`{"action_id":"act-1","args":{"user":"tester"}}`))
	require.NoError(t, err)
	_ = botResp.Body.Close()
	require.Equal(t, http.StatusOK, botResp.StatusCode)
	require.Equal(t, "act-1", ctrl.lastBot)
	require.Equal(t, "tester", ctrl.lastBotArg["user"])

	plugResp, err := http.Post(ts.URL+"/api/plugins/run", "application/json", bytes.NewBufferString(`{"plugin_uuid":"com.github.mrpoundsign.crowddj","action_id":"skip","args":{"reason":"deck"}}`))
	require.NoError(t, err)
	_ = plugResp.Body.Close()
	require.Equal(t, http.StatusOK, plugResp.StatusCode)
	require.Equal(t, "com.github.mrpoundsign.crowddj", ctrl.lastPlug)
	require.Equal(t, "skip", ctrl.lastPlugAc)
	require.Equal(t, "deck", ctrl.lastPlugAr["reason"])
}
