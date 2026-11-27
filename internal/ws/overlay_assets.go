package ws

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed overlays/*.html overlays/*.js
var overlayAssetsFS embed.FS

func (s *Server) serveEmbeddedOverlayHTML(w http.ResponseWriter, filename string) {
	body, err := overlayAssetsFS.ReadFile(path.Join("overlays", filename))
	if err != nil {
		http.Error(w, "overlay template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) handleOverlayAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	raw := strings.TrimPrefix(r.URL.Path, "/overlay/assets/")
	if raw == "" || strings.Contains(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(raw, "..") || strings.ContainsRune(raw, '\x00') {
		http.NotFound(w, r)
		return
	}

	var contentType string
	switch {
	case strings.HasSuffix(raw, ".js"):
		contentType = "application/javascript; charset=utf-8"
	case strings.HasSuffix(raw, ".css"):
		contentType = "text/css; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}

	body, err := overlayAssetsFS.ReadFile(path.Join("overlays", raw))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}
