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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(subtitlesOverlayHTML))
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

const subtitlesOverlayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TownCrier — Multi-Language Voice Subtitles</title>
<style>
  :root {
    --bg: rgba(15, 23, 42, 0.90);
    --border: rgba(56, 189, 248, 0.50);
    --text: #F8FAFC;
    --orig-text: #F8FAFC;
    --muted: #94A3B8;
    --accent: #38BDF8;
    --badge-bg: rgba(56, 189, 248, 0.18);
    --badge-border: rgba(56, 189, 248, 0.42);
    --orig-badge-bg: rgba(56, 189, 248, 0.32);
    --orig-badge-text: #F8FAFC;
    --row-divider: rgba(51, 65, 85, 0.55);
    --pill-bg: rgba(15, 23, 42, 0.85);
    --pill-text: #38BDF8;
    --card-radius: 14px;
    --scale: 1;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    width: 100%;
    height: 100%;
    background: transparent;
    overflow: hidden;
    font-family: 'Segoe UI', 'Yu Gothic UI', 'Meiryo', 'Hiragino Kaku Gothic ProN', 'Noto Sans JP', Inter, system-ui, -apple-system, sans-serif;
    color: var(--text);
  }
  .stage {
    position: fixed;
    left: 28px;
    right: 28px;
    bottom: 28px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    pointer-events: none;
  }
  .stage.top {
    bottom: auto;
    top: 28px;
  }
  .stage.left {
    align-items: flex-start;
  }
  .mic-pill {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    background: var(--pill-bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 5px 14px;
    font-size: calc(0.80rem * var(--scale));
    font-weight: 700;
    color: var(--pill-text);
    box-shadow: 0 6px 18px rgba(0, 0, 0, 0.45);
    opacity: 0;
    transform: translateY(6px);
    transition: opacity 160ms ease, transform 160ms ease;
  }
  .mic-pill.visible {
    opacity: 1;
    transform: translateY(0);
  }
  .eq {
    display: inline-flex;
    align-items: flex-end;
    gap: 2.5px;
    height: 12px;
  }
  .eq span {
    width: 3px;
    background: var(--pill-text);
    border-radius: 2px;
    animation: bounce 0.8s ease-in-out infinite;
  }
  .eq span:nth-child(1) { height: 5px; animation-delay: 0s; }
  .eq span:nth-child(2) { height: 12px; animation-delay: 0.15s; }
  .eq span:nth-child(3) { height: 8px; animation-delay: 0.30s; }
  @keyframes bounce {
    0%, 100% { transform: scaleY(0.45); }
    50% { transform: scaleY(1); }
  }
  .card {
    width: min(980px, 94vw);
    background: var(--bg);
    border: 1.5px solid var(--border);
    border-radius: var(--card-radius);
    padding: 14px 20px;
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.58), 0 0 22px rgba(56, 189, 248, 0.14);
    backdrop-filter: blur(10px);
    transform: translateY(14px);
    opacity: 0;
    transition: opacity 220ms cubic-bezier(0.16, 1, 0.3, 1), transform 220ms cubic-bezier(0.16, 1, 0.3, 1);
  }
  .stage.top .card {
    transform: translateY(-14px);
  }
  .card.visible {
    opacity: 1;
    transform: translateY(0);
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .lang-row {
    display: flex;
    align-items: baseline;
    gap: 12px;
    padding-top: 8px;
    border-top: 1px solid var(--row-divider);
  }
  .lang-row:first-child {
    padding-top: 0;
    border-top: none;
  }
  .lang-badge {
    flex: 0 0 auto;
    min-width: calc(42px * var(--scale));
    text-align: center;
    font-size: calc(0.74rem * var(--scale));
    font-weight: 800;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    padding: 2px 8px;
    border-radius: 6px;
    background: var(--badge-bg);
    border: 1px solid var(--badge-border);
    color: var(--accent);
  }
  .lang-row.original .lang-badge {
    background: var(--orig-badge-bg);
    color: var(--orig-badge-text);
  }
  .lang-text {
    flex: 1 1 auto;
    font-size: calc(1.24rem * var(--scale));
    line-height: 1.42;
    font-weight: 600;
    color: var(--text);
    text-shadow: 0 1px 4px rgba(0, 0, 0, 0.72);
    word-break: break-word;
  }
  .lang-row.original .lang-text {
    color: var(--orig-text);
  }
</style>
<style id="customOverlayCSS"></style>
</head>
<body>
<div id="stage" class="stage">
  <div id="micPill" class="mic-pill" aria-hidden="true">
    <div class="eq"><span></span><span></span><span></span></div>
    <span>Listening</span>
  </div>
  <div id="card" class="card" role="region" aria-live="polite">
    <div id="rows" class="rows"></div>
  </div>
</div>
<script>
(function() {
  const params = new URLSearchParams(window.location.search);
  const token = params.get('token') || '';
  const posOverride = (params.get('pos') || params.get('position') || '').toLowerCase();
  const alignOverride = (params.get('align') || '').toLowerCase();
  const fadeOverride = params.has('fade') ? parseInt(params.get('fade'), 10) : null;
  const scaleOverride = params.has('scale') ? parseInt(params.get('scale'), 10) : null;
  const langsOverride = (params.get('langs') || '')
    .split(',')
    .map(function(s) { return s.trim().toLowerCase(); })
    .filter(Boolean)
    .map(function(c) { return (c === 'fil' || c === 'tgl') ? 'tl' : c; });

  const stage = document.getElementById('stage');
  const micPill = document.getElementById('micPill');
  const card = document.getElementById('card');
  const rowsEl = document.getElementById('rows');
  const customStyleEl = document.getElementById('customOverlayCSS');

  let fadeSeconds = 10;
  let fadeTimer = null;

  function applyLayout(snap) {
    const pos = posOverride || (snap && snap.position) || 'bottom';
    const align = alignOverride || (snap && snap.align) || 'center';
    stage.classList.toggle('top', pos === 'top');
    stage.classList.toggle('left', align === 'left');

    const rawScale = (scaleOverride !== null && !isNaN(scaleOverride)) ? scaleOverride : ((snap && snap.scale) || 100);
    const cleanScale = Math.min(300, Math.max(50, rawScale));
    document.documentElement.style.setProperty('--scale', (cleanScale / 100).toFixed(2));

    if (fadeOverride !== null && !isNaN(fadeOverride)) {
      fadeSeconds = fadeOverride;
    } else if (snap && typeof snap.fade_seconds === 'number') {
      fadeSeconds = snap.fade_seconds;
    }
    if (customStyleEl && snap && typeof snap.custom_css === 'string') {
      customStyleEl.textContent = snap.custom_css;
    }
  }

  function clearFadeTimer() {
    if (fadeTimer) {
      clearTimeout(fadeTimer);
      fadeTimer = null;
    }
  }

  function hideCard() {
    clearFadeTimer();
    card.classList.remove('visible');
  }

  function setSpeaking(active) {
    micPill.classList.toggle('visible', !!active);
  }

  function selectLines(lines) {
    if (!Array.isArray(lines) || lines.length === 0) return [];
    if (langsOverride.length === 0) return lines;
    const byLang = {};
    lines.forEach(function(ln) {
      if (ln && ln.lang) byLang[ln.lang.toLowerCase()] = ln;
    });
    const out = [];
    langsOverride.forEach(function(code) {
      if (byLang[code]) out.push(byLang[code]);
    });
    return out;
  }

  function renderSubtitle(ev) {
    const chosen = selectLines(ev && ev.lines);
    if (chosen.length === 0) return;

    clearFadeTimer();
    rowsEl.innerHTML = '';
    chosen.forEach(function(ln) {
      const row = document.createElement('div');
      row.className = 'lang-row' + (ln.is_original ? ' original' : '');

      const badge = document.createElement('span');
      badge.className = 'lang-badge';
      badge.textContent = ln.label || (ln.lang || '').toUpperCase();

      const txt = document.createElement('span');
      txt.className = 'lang-text';
      txt.textContent = ln.text || '';

      row.appendChild(badge);
      row.appendChild(txt);
      rowsEl.appendChild(row);
    });

    card.classList.add('visible');
    if (fadeSeconds > 0) {
      fadeTimer = setTimeout(hideCard, fadeSeconds * 1000);
    }
  }

  function loadSnapshot() {
    const url = '/api/overlay/subtitles' + (token ? '?token=' + encodeURIComponent(token) : '');
    fetch(url, { cache: 'no-store' })
      .then(function(r) { return r.json(); })
      .then(function(snap) {
        applyLayout(snap);
        setSpeaking(snap && snap.speaking);
        if (snap && snap.latest && Array.isArray(snap.latest.lines) && snap.latest.lines.length > 0) {
          renderSubtitle(snap.latest);
        }
      })
      .catch(function() {});
  }

  function connectSSE() {
    const url = '/api/overlay/subtitles/stream' + (token ? '?token=' + encodeURIComponent(token) : '');
    const es = new EventSource(url);
    es.onmessage = function(e) {
      try {
        const ev = JSON.parse(e.data);
        switch (ev.kind) {
          case 'speaking':
            setSpeaking(ev.speaking);
            break;
          case 'subtitle':
            setSpeaking(false);
            renderSubtitle(ev);
            break;
          case 'clear':
            setSpeaking(false);
            hideCard();
            rowsEl.innerHTML = '';
            break;
          case 'reload':
            loadSnapshot();
            break;
        }
      } catch (_) {}
    };
    es.onerror = function() {
      es.close();
      setTimeout(connectSSE, 2000);
    };
  }

  loadSnapshot();
  connectSSE();
})();
</script>
</body>
</html>
`
