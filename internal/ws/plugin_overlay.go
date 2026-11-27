package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"
)

// PluginOverlayProvider is implemented by Controller (e.g., app.Session) to serve plugin overlay assets.
type PluginOverlayProvider interface {
	GetPluginOverlayAsset(ctx context.Context, pluginUUID, assetPath string) (contentType string, body []byte, found bool, err error)
}

var allowedPluginOverlayMIMEs = map[string]bool{
	"text/html":              true,
	"text/css":               true,
	"application/javascript": true,
	"text/javascript":        true,
	"image/png":              true,
	"image/jpeg":             true,
	"image/webp":             true,
	"image/gif":              true,
	"image/svg+xml":          true,
	"application/json":       true,
}

func isAllowedOverlayMIME(ct string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return allowedPluginOverlayMIMEs[base]
}

func sanitizeOverlayAssetPath(raw string) (string, bool) {
	if strings.ContainsRune(raw, '\x00') || strings.Contains(raw, "\\") {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "/" {
		return "index.html", true
	}
	for seg := range strings.SplitSeq(trimmed, "/") {
		if seg == ".." {
			return "", false
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+trimmed), "/")
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", false
	}
	return cleaned, true
}

func (s *Server) handlePluginRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		http.NotFound(w, r)
		return
	}
	pluginUUID := strings.TrimSpace(rest[:slash])
	sub := rest[slash+1:]
	if pluginUUID == "" || strings.Contains(pluginUUID, "..") {
		http.Error(w, "invalid plugin uuid", http.StatusBadRequest)
		return
	}

	switch {
	case sub == "events":
		s.handlePluginOverlayEvents(w, r, pluginUUID)
	case sub == "overlay":
		s.servePluginOverlayAsset(w, r, pluginUUID, "index.html")
	case strings.HasPrefix(sub, "overlay/"):
		asset := strings.TrimPrefix(sub, "overlay/")
		s.servePluginOverlayAsset(w, r, pluginUUID, asset)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) servePluginOverlayAsset(w http.ResponseWriter, r *http.Request, pluginUUID, rawAsset string) {
	cleanAsset, ok := sanitizeOverlayAssetPath(rawAsset)
	if !ok {
		http.Error(w, "invalid overlay asset path", http.StatusBadRequest)
		return
	}

	prov, hasProv := s.ctrl.(PluginOverlayProvider)
	if !hasProv || prov == nil {
		http.Error(w, "plugin overlay provider unavailable", http.StatusServiceUnavailable)
		return
	}

	ct, body, found, err := prov.GetPluginOverlayAsset(r.Context(), pluginUUID, cleanAsset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if strings.TrimSpace(ct) == "" && strings.HasSuffix(strings.ToLower(cleanAsset), ".html") {
		ct = "text/html; charset=utf-8"
	}
	if !isAllowedOverlayMIME(ct) {
		http.Error(w, "disallowed overlay asset content-type: "+ct, http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) handlePluginOverlayEvents(w http.ResponseWriter, r *http.Request, pluginUUID string) {
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

	evCh := make(chan OverlayStreamEvent, 64)
	subID := s.addOverlayStreamChan(evCh)
	defer s.removeOverlayStreamChan(subID)

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
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-evCh:
			if ev.Kind == "reload" {
				_, _ = fmt.Fprintf(w, "event: reload\ndata: {}\n\n")
				flusher.Flush()
				continue
			}
			if ev.Kind != "plugin" || ev.Plugin == nil || ev.Plugin.PluginUUID != pluginUUID {
				continue
			}
			raw, err := json.Marshal(ev.Plugin)
			if err != nil {
				continue
			}
			eventName := strings.TrimSpace(ev.Plugin.EventName)
			if eventName != "" {
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, raw)
			} else {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			flusher.Flush()
		}
	}
}
