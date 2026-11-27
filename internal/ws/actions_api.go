package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ExternalBotAction describes a configured TownCrier bot action selectable from hardware decks.
type ExternalBotAction struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	QueueID string `json:"queue_id,omitempty"`
}

// ExternalActionVisual carries live dynamic visual state for a plugin action tile or dial.
type ExternalActionVisual struct {
	Title           string `json:"title,omitempty"`
	Subtitle        string `json:"subtitle,omitempty"`
	Color           string `json:"color,omitempty"`
	BackgroundColor string `json:"background_color,omitempty"`
	Image           string `json:"image,omitempty"`
	State           int    `json:"state,omitempty"`
	HasProgress     bool   `json:"has_progress,omitempty"`
	ProgressPercent int    `json:"progress_percent,omitempty"`
	FeedbackValue   string `json:"feedback_value,omitempty"`
}

// ExternalPluginAction describes an exported v1.0.0 action registered by an active TownCrier plugin.
type ExternalPluginAction struct {
	PluginUUID   string                `json:"plugin_uuid"`
	PluginName   string                `json:"plugin_name"`
	ActionID     string                `json:"action_id"`
	ActionName   string                `json:"action_name"`
	Description  string                `json:"description,omitempty"`
	Controllers  []string              `json:"controllers,omitempty"`
	DefaultTitle string                `json:"default_title,omitempty"`
	DefaultColor string                `json:"default_color,omitempty"`
	DefaultImage string                `json:"default_image,omitempty"`
	HideTitle    bool                  `json:"hide_title,omitempty"`
	Visual       *ExternalActionVisual `json:"visual,omitempty"`
}

// ExternalActionCatalog lists all Bot Automation actions and Plugin actions currently runnable.
type ExternalActionCatalog struct {
	BotActions    []ExternalBotAction    `json:"bot_actions"`
	PluginActions []ExternalPluginAction `json:"plugin_actions"`
}

// ActionProvider is optionally implemented by Controller (e.g. app.Session) to expose
// dynamic Bot Automation and Plugin actions over HTTP REST to Stream Deck and PoundControl.
type ActionProvider interface {
	ListExternalActions() ExternalActionCatalog
	RunExternalBotAction(actionIDOrName string, args map[string]string, runImmediately bool) error
	RunExternalPluginAction(ctx context.Context, pluginUUID, actionID string, args map[string]string) error
}

type actionAPIResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeJSONResponse(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleHTTPListActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONResponse(w, http.StatusMethodNotAllowed, actionAPIResponse{Ok: false, Error: "method not allowed"})
		return
	}
	prov, ok := s.ctrl.(ActionProvider)
	if !ok || prov == nil {
		writeJSONResponse(w, http.StatusOK, ExternalActionCatalog{
			BotActions:    []ExternalBotAction{},
			PluginActions: []ExternalPluginAction{},
		})
		return
	}
	cat := prov.ListExternalActions()
	if cat.BotActions == nil {
		cat.BotActions = []ExternalBotAction{}
	}
	if cat.PluginActions == nil {
		cat.PluginActions = []ExternalPluginAction{}
	}
	writeJSONResponse(w, http.StatusOK, cat)
}

func (s *Server) handleHTTPRunBotAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONResponse(w, http.StatusMethodNotAllowed, actionAPIResponse{Ok: false, Error: "method not allowed"})
		return
	}
	prov, ok := s.ctrl.(ActionProvider)
	if !ok || prov == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, actionAPIResponse{Ok: false, Error: "action provider unavailable"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32*1024))
	var req struct {
		ActionID       string            `json:"action_id"`
		ActionIDOrName string            `json:"action_id_or_name"`
		Args           map[string]string `json:"args"`
		RunImmediately bool              `json:"run_immediately"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: "invalid JSON payload"})
			return
		}
	}
	target := strings.TrimSpace(req.ActionID)
	if target == "" {
		target = strings.TrimSpace(req.ActionIDOrName)
	}
	if target == "" {
		writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: "action_id is required"})
		return
	}
	if err := prov.RunExternalBotAction(target, req.Args, req.RunImmediately); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, actionAPIResponse{Ok: true})
}

func (s *Server) handleHTTPRunPluginAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONResponse(w, http.StatusMethodNotAllowed, actionAPIResponse{Ok: false, Error: "method not allowed"})
		return
	}
	prov, ok := s.ctrl.(ActionProvider)
	if !ok || prov == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, actionAPIResponse{Ok: false, Error: "action provider unavailable"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32*1024))
	var req struct {
		PluginUUID string            `json:"plugin_uuid"`
		ActionID   string            `json:"action_id"`
		Args       map[string]string `json:"args"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: "invalid JSON payload"})
			return
		}
	}
	pluginUUID := strings.TrimSpace(req.PluginUUID)
	actionID := strings.TrimSpace(req.ActionID)
	if pluginUUID == "" || actionID == "" {
		writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: "plugin_uuid and action_id are required"})
		return
	}
	if err := prov.RunExternalPluginAction(r.Context(), pluginUUID, actionID, req.Args); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, actionAPIResponse{Ok: false, Error: err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, actionAPIResponse{Ok: true})
}
