package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// ChatOverlaySnapshot holds the resolved LiveLogPreset metadata, initial matching entries,
// and aggregated summary metrics for the OBS Chat Overlay Browser Source.
type ChatOverlaySnapshot struct {
	Preset      config.LiveLogPreset  `json:"preset"`
	ShowSummary bool                  `json:"show_summary"`
	Scale       int                   `json:"scale"`
	CustomCSS   string                `json:"custom_css,omitempty"`
	Entries     []eventlog.Entry      `json:"entries"`
	Summary     eventlog.QuerySummary `json:"summary"`
}

// OverlayStyleConfig holds the persisted visual style and custom CSS for OBS Browser Source overlays.
type OverlayStyleConfig struct {
	TTSEnabled         bool     `json:"tts_enabled"`
	ChatEnabled        bool     `json:"chat_enabled"`
	SubtitlesEnabled   bool     `json:"subtitles_enabled"`
	TargetLanguages    []string `json:"target_languages,omitempty"`
	TTSPosition        string   `json:"tts_position"`
	CustomCSS          string   `json:"custom_css"`
	StyleJSON          string   `json:"style_json,omitempty"`
	SubtitlesCustomCSS string   `json:"subtitles_custom_css,omitempty"`
	SubtitlesStyleJSON string   `json:"subtitles_style_json,omitempty"`
	UnifiedWidth       int      `json:"unified_width,omitempty"`
	UnifiedHeight      int      `json:"unified_height,omitempty"`
	UnifiedLayoutJSON  string   `json:"unified_layout_json,omitempty"`
	Target             string   `json:"target,omitempty"`
}

// OverlayStyleProvider is an optional interface implemented by Controller (such as app.Session)
// to get and persist custom overlay CSS and style settings in config.json.
type OverlayStyleProvider interface {
	GetOverlayStyle() OverlayStyleConfig
	SaveOverlayStyle(customCSS, styleJSON string) error
}

// UnifiedLayoutProvider is an optional interface implemented by Controller (such as app.Session)
// to persist the Unified OBS Canvas resolution and per-widget layout.
type UnifiedLayoutProvider interface {
	SaveUnifiedLayout(width, height int, layoutJSON string) error
}

// PluginOverlayEvent represents a custom JSON payload broadcast by a plugin to overlay subscribers.
type PluginOverlayEvent struct {
	PluginUUID string          `json:"plugin_uuid"`
	EventName  string          `json:"event_name"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// OverlayStreamEvent represents a real-time Server-Sent Event pushed to /api/overlay/stream subscribers.
type OverlayStreamEvent struct {
	Kind    string                  `json:"kind"` // "append", "outcome", "reload", or "plugin"
	Entry   *eventlog.Entry         `json:"entry,omitempty"`
	EventID string                  `json:"event_id,omitempty"`
	Update  *eventlog.OutcomeUpdate `json:"update,omitempty"`
	Plugin  *PluginOverlayEvent     `json:"plugin,omitempty"`
}

// OverlayProvider is an optional interface implemented by Controller (such as app.Session)
// to serve filtered live chat history and real-time event streams to OBS Browser Sources.
type OverlayProvider interface {
	QueryOverlayChat(ctx context.Context, presetID string, limit int) (*ChatOverlaySnapshot, error)
	SubscribeOverlayChat(presetID string, fn func(OverlayStreamEvent)) func()
}

func (s *Server) handleOverlayTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isTTSOverlayEnabled() {
		http.Error(w, "OBS Now-Speaking Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	s.serveEmbeddedOverlayHTML(w, "tts.html")
}

func (s *Server) handleOverlayChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isChatOverlayEnabled() {
		http.Error(w, "OBS Live Chat Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	s.serveEmbeddedOverlayHTML(w, "chat.html")
}

func (s *Server) handleOverlayDesigner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isTTSOverlayEnabled() && !s.isChatOverlayEnabled() && !s.IsSubtitlesOverlayEnabled() {
		http.Error(w, "OBS Overlays are disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	s.serveEmbeddedOverlayHTML(w, "designer.html")
}

func (s *Server) populateOverlayStyleMeta(cfg *OverlayStyleConfig) {
	if cfg == nil {
		return
	}
	cfg.TTSEnabled = s.isTTSOverlayEnabled()
	cfg.ChatEnabled = s.isChatOverlayEnabled()
	cfg.SubtitlesEnabled = s.IsSubtitlesOverlayEnabled()
	if cfg.TTSPosition == "" {
		cfg.TTSPosition = "bottom"
	}
	cfg.UnifiedWidth, cfg.UnifiedHeight = config.NormalizeUnifiedOverlaySize(cfg.UnifiedWidth, cfg.UnifiedHeight)
	if subStyleProv, ok := s.ctrl.(SubtitlesStyleProvider); ok && subStyleProv != nil {
		cfg.SubtitlesCustomCSS, cfg.SubtitlesStyleJSON = subStyleProv.GetSubtitlesOverlayStyle()
	}
	if subProv, ok := s.ctrl.(SubtitlesOverlayProvider); ok && subProv != nil {
		snap := subProv.SnapshotOverlaySubtitles()
		if len(snap.TargetLanguages) > 0 {
			cfg.TargetLanguages = snap.TargetLanguages
		}
	}
}

func (s *Server) handleAPIOverlayStyle(w http.ResponseWriter, r *http.Request) {
	if !s.isTTSOverlayEnabled() && !s.isChatOverlayEnabled() && !s.IsSubtitlesOverlayEnabled() {
		http.Error(w, "OBS Overlays are disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	styleProv, hasProv := s.ctrl.(OverlayStyleProvider)
	subStyleProv, hasSubProv := s.ctrl.(SubtitlesStyleProvider)
	layoutProv, hasLayoutProv := s.ctrl.(UnifiedLayoutProvider)
	switch r.Method {
	case http.MethodGet:
		var cfg OverlayStyleConfig
		if hasProv && styleProv != nil {
			cfg = styleProv.GetOverlayStyle()
		}
		s.populateOverlayStyleMeta(&cfg)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(cfg)
	case http.MethodPost:
		var req OverlayStyleConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		switch strings.ToLower(strings.TrimSpace(req.Target)) {
		case "subtitles":
			if hasSubProv && subStyleProv != nil {
				if err := subStyleProv.SaveSubtitlesOverlayStyle(req.SubtitlesCustomCSS, req.SubtitlesStyleJSON); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			} else if hasProv && styleProv != nil {
				if err := styleProv.SaveOverlayStyle(req.SubtitlesCustomCSS, req.SubtitlesStyleJSON); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		case "unified":
			if hasLayoutProv && layoutProv != nil {
				if err := layoutProv.SaveUnifiedLayout(req.UnifiedWidth, req.UnifiedHeight, req.UnifiedLayoutJSON); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		default:
			if hasProv && styleProv != nil {
				if err := styleProv.SaveOverlayStyle(req.CustomCSS, req.StyleJSON); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		var out OverlayStyleConfig
		if hasProv && styleProv != nil {
			out = styleProv.GetOverlayStyle()
		} else {
			out = req
		}
		s.populateOverlayStyleMeta(&out)
		s.BroadcastOverlayReload()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIOverlayChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isChatOverlayEnabled() {
		http.Error(w, "OBS Live Chat Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}

	presetID := strings.TrimSpace(r.URL.Query().Get("preset"))
	summaryParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("summary")))
	limit := 25
	if lStr := strings.TrimSpace(r.URL.Query().Get("limit")); lStr != "" {
		if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	prov, ok := s.ctrl.(OverlayProvider)
	if !ok || prov == nil {
		defPreset := config.DefaultLiveLogPresets()[0]
		defPreset.ShowSummary = false
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&ChatOverlaySnapshot{
			Preset:      defPreset,
			ShowSummary: false,
			Scale:       100,
			Entries:     []eventlog.Entry{},
		})
		return
	}

	snap, err := prov.QueryOverlayChat(r.Context(), presetID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if snap != nil {
		switch summaryParam {
		case "1", "true", "on", "yes":
			snap.ShowSummary = true
			snap.Preset.ShowSummary = true
		case "0", "false", "off", "no":
			snap.ShowSummary = false
			snap.Preset.ShowSummary = false
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleAPIOverlayStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isChatOverlayEnabled() {
		http.Error(w, "OBS Live Chat Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	prov, hasProv := s.ctrl.(OverlayProvider)
	if !hasProv || prov == nil {
		http.Error(w, "overlay provider unavailable", http.StatusServiceUnavailable)
		return
	}

	presetID := strings.TrimSpace(r.URL.Query().Get("preset"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	evCh := make(chan OverlayStreamEvent, 64)
	subID := s.addOverlayStreamChan(evCh)
	defer s.removeOverlayStreamChan(subID)

	unsub := prov.SubscribeOverlayChat(presetID, func(ev OverlayStreamEvent) {
		select {
		case evCh <- ev:
		default:
		}
	})
	defer unsub()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	ctx := r.Context()
	stopCh := s.stopChan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-keepAlive.C:
			if !s.isChatOverlayEnabled() {
				return
			}
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-evCh:
			if !s.isChatOverlayEnabled() {
				return
			}
			raw, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
	}
}
