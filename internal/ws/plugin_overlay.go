package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// PluginOverlayProvider is implemented by Controller (e.g., app.Session) to serve plugin overlay assets.
type PluginOverlayProvider interface {
	GetPluginOverlayAsset(ctx context.Context, pluginUUID, assetPath string) (contentType string, body []byte, found bool, err error)
}

// PluginOverlaySelectOption defines one dropdown choice inside a plugin widget's style schema.
type PluginOverlaySelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// PluginOverlayWidgetField describes one editable visual setting in a plugin overlay widget's Designer form.
type PluginOverlayWidgetField struct {
	Key         string                      `json:"key"`
	Label       string                      `json:"label"`
	Type        string                      `json:"type"` // "text", "multiline", "number", "bool", "select", "color", "section"
	Section     string                      `json:"section,omitempty"`
	Default     any                         `json:"default,omitempty"`
	Placeholder string                      `json:"placeholder,omitempty"`
	Help        string                      `json:"help,omitempty"`
	Options     []PluginOverlaySelectOption `json:"options,omitempty"`
	Min         *float64                    `json:"min,omitempty"`
	Max         *float64                    `json:"max,omitempty"`
	Step        *float64                    `json:"step,omitempty"`
}

// PluginOverlayWidget describes a plugin-declared OBS overlay widget exposed to Unified Canvas and Style Designer.
type PluginOverlayWidget struct {
	Key               string                     `json:"key"` // "plugin:<uuid>:<widget_id>"
	PluginUUID        string                     `json:"plugin_uuid"`
	PluginName        string                     `json:"plugin_name"`
	WidgetID          string                     `json:"widget_id"`
	Name              string                     `json:"name"`
	Description       string                     `json:"description,omitempty"`
	IconName          string                     `json:"icon_name,omitempty"`
	EntryAsset        string                     `json:"entry_asset"`
	DefaultX          float64                    `json:"default_x"`
	DefaultY          float64                    `json:"default_y"`
	DefaultW          float64                    `json:"default_w"`
	DefaultH          float64                    `json:"default_h"`
	MinW              float64                    `json:"min_w"`
	MinH              float64                    `json:"min_h"`
	DefaultVisible    bool                       `json:"default_visible"`
	SupportsHostTheme bool                       `json:"supports_host_theme"`
	AllowCustomCSS    bool                       `json:"allow_custom_css"`
	StyleSchema       []PluginOverlayWidgetField `json:"style_schema,omitempty"`
	Style             config.PluginOverlayStyle  `json:"style"`
}

// PluginOverlayStyleEvent delivers widget style settings and resolved TownCrier host CSS variables to overlays.
type PluginOverlayStyleEvent struct {
	PluginUUID string                    `json:"plugin_uuid"`
	WidgetID   string                    `json:"widget_id"`
	Style      config.PluginOverlayStyle `json:"style"`
	HostTheme  map[string]string         `json:"host_theme,omitempty"`
}

// PluginOverlayWidgetProvider is an optional interface implemented by Controller (e.g., app.Session)
// to enumerate plugin overlay widgets and persist per-widget OBS Designer styles.
type PluginOverlayWidgetProvider interface {
	ListPluginOverlayWidgets() []PluginOverlayWidget
	GetPluginOverlayStyle(pluginUUID, widgetID string) config.PluginOverlayStyle
	SavePluginOverlayStyle(pluginUUID, widgetID string, style config.PluginOverlayStyle) error
}

// PluginOverlayStyleStorageKey returns the canonical config map key for a plugin widget's saved style.
func PluginOverlayStyleStorageKey(pluginUUID, widgetID string) string {
	return strings.TrimSpace(pluginUUID) + ":" + strings.TrimSpace(widgetID)
}

// PluginWidgetLayoutKey returns the canonical UnifiedLayoutJSON key for a plugin widget slot.
func PluginWidgetLayoutKey(pluginUUID, widgetID string) string {
	return "plugin:" + strings.TrimSpace(pluginUUID) + ":" + strings.TrimSpace(widgetID)
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
	case sub == "state":
		s.handlePluginOverlayState(w, r, pluginUUID)
	case sub == "widget-style":
		s.handlePluginOverlayWidgetStyle(w, r, pluginUUID)
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

func (s *Server) resolvePluginWidgetStyle(pluginUUID, widgetID string) PluginOverlayStyleEvent {
	widgetID = strings.TrimSpace(widgetID)
	var style config.PluginOverlayStyle
	if pwProv, ok := s.ctrl.(PluginOverlayWidgetProvider); ok && pwProv != nil {
		if widgetID == "" {
			for _, w := range pwProv.ListPluginOverlayWidgets() {
				if w.PluginUUID == pluginUUID {
					widgetID = w.WidgetID
					break
				}
			}
		}
		if widgetID != "" {
			style = pwProv.GetPluginOverlayStyle(pluginUUID, widgetID)
		}
	}
	return PluginOverlayStyleEvent{
		PluginUUID: pluginUUID,
		WidgetID:   widgetID,
		Style:      style,
		HostTheme:  s.resolvedHostTheme(),
	}
}

func (s *Server) handlePluginOverlayState(w http.ResponseWriter, _ *http.Request, pluginUUID string) {
	cached := s.snapshotPluginEvents(pluginUUID)
	events := make(map[string]json.RawMessage, len(cached))
	for _, ev := range cached {
		payload := ev.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		events[ev.EventName] = payload
	}
	styles := make(map[string]config.PluginOverlayStyle)
	if pwProv, ok := s.ctrl.(PluginOverlayWidgetProvider); ok && pwProv != nil {
		for _, widget := range pwProv.ListPluginOverlayWidgets() {
			if widget.PluginUUID != pluginUUID {
				continue
			}
			styles[widget.WidgetID] = widget.Style
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"events":     events,
		"styles":     styles,
		"host_theme": s.resolvedHostTheme(),
	})
}

func (s *Server) handlePluginOverlayWidgetStyle(w http.ResponseWriter, r *http.Request, pluginUUID string) {
	widgetID := strings.TrimSpace(r.URL.Query().Get("widget"))
	stEv := s.resolvePluginWidgetStyle(pluginUUID, widgetID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(stEv)
}

func (s *Server) handlePluginOverlayEvents(w http.ResponseWriter, r *http.Request, pluginUUID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	widgetFilter := strings.TrimSpace(r.URL.Query().Get("widget"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	evCh := make(chan OverlayStreamEvent, 64)
	subID := s.addOverlayStreamChan(evCh)
	defer s.removeOverlayStreamChan(subID)

	// Replay the latest cached payload per event name immediately so OBS browser refreshes stay populated.
	for _, cachedEv := range s.snapshotPluginEvents(pluginUUID) {
		raw, err := json.Marshal(cachedEv)
		if err != nil {
			continue
		}
		if cachedEv.EventName != "" {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", cachedEv.EventName, raw)
		} else {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}
	if widgetFilter != "" {
		initStyle := s.resolvePluginWidgetStyle(pluginUUID, widgetFilter)
		if rawStyle, err := json.Marshal(initStyle); err == nil {
			_, _ = fmt.Fprintf(w, "event: style\ndata: %s\n\n", rawStyle)
		}
	}
	flusher.Flush()

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
			if ev.Kind == "plugin_style" && ev.PluginStyle != nil && ev.PluginStyle.PluginUUID == pluginUUID {
				if widgetFilter != "" && ev.PluginStyle.WidgetID != "" && ev.PluginStyle.WidgetID != widgetFilter {
					continue
				}
				payload := PluginOverlayStyleEvent{
					PluginUUID: ev.PluginStyle.PluginUUID,
					WidgetID:   ev.PluginStyle.WidgetID,
					Style:      ev.PluginStyle.Style,
					HostTheme:  maps.Clone(ev.PluginStyle.HostTheme),
				}
				if raw, err := json.Marshal(payload); err == nil {
					_, _ = fmt.Fprintf(w, "event: style\ndata: %s\n\n", raw)
					flusher.Flush()
				}
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
