package ws

import "net/http"

func (s *Server) handleOverlayUnified(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.hasAnyOverlayEnabled() {
		http.Error(w, "OBS Overlays are disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	s.serveEmbeddedOverlayHTML(w, "unified.html")
}
