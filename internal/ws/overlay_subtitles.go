package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SubtitleLine represents a single translated or original language row in a multi-language subtitle card.
type SubtitleLine struct {
	Lang       string `json:"lang"`
	Label      string `json:"label"`
	Text       string `json:"text"`
	IsOriginal bool   `json:"is_original,omitempty"`
}

// SubtitleEvent represents a real-time Server-Sent Event pushed to /api/overlay/subtitles/stream subscribers.
type SubtitleEvent struct {
	Kind         string         `json:"kind"` // "subtitle", "speaking", "clear", or "reload"
	ID           string         `json:"id,omitempty"`
	Timestamp    time.Time      `json:"timestamp,omitzero"`
	Speaking     bool           `json:"speaking,omitempty"`
	SourceLang   string         `json:"source_lang,omitempty"`
	OriginalText string         `json:"original_text,omitempty"`
	Lines        []SubtitleLine `json:"lines,omitempty"`
}

// SubtitleOverlaySnapshot holds layout settings, custom CSS, and the latest subtitle state for /overlay/subtitles.
type SubtitleOverlaySnapshot struct {
	Position        string         `json:"position"`         // "bottom" or "top"
	Align           string         `json:"align"`            // "center" or "left"
	FadeSeconds     int            `json:"fade_seconds"`     // >0 auto-fade seconds, <=0 never fade
	Scale           int            `json:"scale"`            // 50..300
	TargetLanguages []string       `json:"target_languages"` // configured default language order
	CustomCSS       string         `json:"custom_css,omitempty"`
	Speaking        bool           `json:"speaking"`
	Latest          *SubtitleEvent `json:"latest,omitempty"`
}

// SubtitlesOverlayProvider is an optional interface implemented by Controller (such as app.Session)
// to provide multi-language voice subtitle configuration and active state to OBS Browser Sources.
type SubtitlesOverlayProvider interface {
	SnapshotOverlaySubtitles() SubtitleOverlaySnapshot
}

// SubtitlesStyleProvider is an optional interface implemented by Controller (such as app.Session)
// to get and persist Voice Subtitles overlay custom CSS and style JSON independently of Chat/TTS overlays.
type SubtitlesStyleProvider interface {
	GetSubtitlesOverlayStyle() (customCSS, styleJSON string)
	SaveSubtitlesOverlayStyle(customCSS, styleJSON string) error
}

func (s *Server) addSubtitleStreamChan(ch chan SubtitleEvent) uint64 {
	s.overlaySubsMu.Lock()
	defer s.overlaySubsMu.Unlock()
	if s.subtitleSubs == nil {
		s.subtitleSubs = make(map[uint64]chan SubtitleEvent)
	}
	s.subtitleSubSeq++
	id := s.subtitleSubSeq
	s.subtitleSubs[id] = ch
	return id
}

func (s *Server) removeSubtitleStreamChan(id uint64) {
	s.overlaySubsMu.Lock()
	defer s.overlaySubsMu.Unlock()
	delete(s.subtitleSubs, id)
}

func (s *Server) handleOverlaySubtitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.IsSubtitlesOverlayEnabled() {
		http.Error(w, "OBS Voice Subtitles Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	s.serveEmbeddedOverlayHTML(w, "subtitles.html")
}

func (s *Server) handleAPIOverlaySubtitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.IsSubtitlesOverlayEnabled() {
		http.Error(w, "OBS Voice Subtitles Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}

	snap := SubtitleOverlaySnapshot{
		Position:        "bottom",
		Align:           "center",
		FadeSeconds:     10,
		Scale:           100,
		TargetLanguages: []string{"en", "tl", "ja"},
	}
	if prov, ok := s.ctrl.(SubtitlesOverlayProvider); ok && prov != nil {
		snap = prov.SnapshotOverlaySubtitles()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleAPIOverlaySubtitlesStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.IsSubtitlesOverlayEnabled() {
		http.Error(w, "OBS Voice Subtitles Overlay is disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	evCh := make(chan SubtitleEvent, 64)
	subID := s.addSubtitleStreamChan(evCh)
	defer s.removeSubtitleStreamChan(subID)

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
			if !s.IsSubtitlesOverlayEnabled() {
				return
			}
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-evCh:
			if !s.IsSubtitlesOverlayEnabled() {
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
