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
	TTSPosition string `json:"tts_position"`
	CustomCSS   string `json:"custom_css"`
	StyleJSON   string `json:"style_json,omitempty"`
}

// OverlayStyleProvider is an optional interface implemented by Controller (such as app.Session)
// to get and persist custom overlay CSS and style settings in config.json.
type OverlayStyleProvider interface {
	GetOverlayStyle() OverlayStyleConfig
	SaveOverlayStyle(customCSS, styleJSON string) error
}

// OverlayStreamEvent represents a real-time Server-Sent Event pushed to /api/overlay/stream subscribers.
type OverlayStreamEvent struct {
	Kind    string                  `json:"kind"` // "append", "outcome", or "reload"
	Entry   *eventlog.Entry         `json:"entry,omitempty"`
	EventID string                  `json:"event_id,omitempty"`
	Update  *eventlog.OutcomeUpdate `json:"update,omitempty"`
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ttsOverlayHTML))
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(chatOverlayHTML))
}

func (s *Server) handleOverlayDesigner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isTTSOverlayEnabled() && !s.isChatOverlayEnabled() {
		http.Error(w, "OBS Overlays are disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(overlayDesignerHTML))
}

func (s *Server) handleAPIOverlayStyle(w http.ResponseWriter, r *http.Request) {
	if !s.isTTSOverlayEnabled() && !s.isChatOverlayEnabled() {
		http.Error(w, "OBS Overlays are disabled in TownCrier Settings", http.StatusNotFound)
		return
	}
	styleProv, hasProv := s.ctrl.(OverlayStyleProvider)
	switch r.Method {
	case http.MethodGet:
		var cfg OverlayStyleConfig
		if hasProv && styleProv != nil {
			cfg = styleProv.GetOverlayStyle()
		}
		if cfg.TTSPosition == "" {
			cfg.TTSPosition = "bottom"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(cfg)
	case http.MethodPost:
		var req OverlayStyleConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		if hasProv && styleProv != nil {
			if err := styleProv.SaveOverlayStyle(req.CustomCSS, req.StyleJSON); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			req = styleProv.GetOverlayStyle()
		}
		s.BroadcastOverlayReload()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(req)
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
	for {
		select {
		case <-ctx.Done():
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

const ttsOverlayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TownCrier — Now Speaking Overlay</title>
<style>
  :root {
    --bg: rgba(15, 23, 42, 0.92);
    --border: rgba(56, 189, 248, 0.48);
    --text: #F8FAFC;
    --muted: #94A3B8;
    --accent: #38BDF8;
    --scale: 1;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    width: 100%;
    height: 100%;
    background: transparent;
    overflow: hidden;
    font-family: 'Segoe UI', Inter, system-ui, -apple-system, sans-serif;
    color: var(--text);
  }
  .stage {
    position: fixed;
    left: 24px;
    right: 24px;
    bottom: 24px;
    display: flex;
    justify-content: flex-start;
    pointer-events: none;
  }
  .stage.top {
    bottom: auto;
    top: 24px;
  }
  .stage.center {
    justify-content: center;
  }
  .card {
    max-width: min(820px, 95vw);
    background: var(--bg);
    border: 1.5px solid var(--border);
    border-radius: 14px;
    padding: 14px 20px;
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.55), 0 0 20px rgba(56, 189, 248, 0.16);
    backdrop-filter: blur(10px);
    transform: translateY(18px) scale(var(--scale));
    transform-origin: bottom left;
    opacity: 0;
    transition: opacity 220ms cubic-bezier(0.16, 1, 0.3, 1), transform 220ms cubic-bezier(0.16, 1, 0.3, 1);
  }
  .stage.top .card {
    transform: translateY(-18px) scale(var(--scale));
    transform-origin: top left;
  }
  .card.visible {
    opacity: 1;
    transform: translateY(0) scale(var(--scale));
  }
  .header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 6px;
  }
  .badge {
    font-size: 0.72rem;
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 2px 8px;
    border-radius: 6px;
    background: #334155;
    color: #F8FAFC;
  }
  .badge.twitch { background: #9146FF; }
  .badge.youtube { background: #FF0033; }
  .badge.kick { background: #53FC18; color: #090D16; }
  .badge.tiktok { background: #00F2FE; color: #090D16; }
  .speaker {
    font-size: 1.02rem;
    font-weight: 700;
    color: var(--accent);
  }
  .voice-pill {
    font-size: 0.74rem;
    color: var(--muted);
    background: rgba(148, 163, 184, 0.14);
    padding: 2px 8px;
    border-radius: 999px;
  }
  .eq {
    display: inline-flex;
    align-items: flex-end;
    gap: 3px;
    height: 14px;
    margin-left: auto;
  }
  .eq span {
    width: 3px;
    background: var(--accent);
    border-radius: 2px;
    animation: bounce 0.85s ease-in-out infinite;
  }
  .eq span:nth-child(1) { height: 6px; animation-delay: 0s; }
  .eq span:nth-child(2) { height: 13px; animation-delay: 0.15s; }
  .eq span:nth-child(3) { height: 9px; animation-delay: 0.3s; }
  .eq span:nth-child(4) { height: 12px; animation-delay: 0.45s; }
  @keyframes bounce {
    0%, 100% { transform: scaleY(0.45); }
    50% { transform: scaleY(1); }
  }
  .caption {
    font-size: 1.22rem;
    line-height: 1.4;
    font-weight: 600;
    color: var(--text);
    text-shadow: 0 1px 3px rgba(0, 0, 0, 0.65);
    word-break: break-word;
  }
</style>
<style id="customOverlayCSS"></style>
</head>
<body>
<div id="stage" class="stage">
  <div id="card" class="card" role="status" aria-live="polite">
    <div class="header">
      <span id="platBadge" class="badge">TTS</span>
      <span id="speakerName" class="speaker">Now Speaking</span>
      <span id="voicePill" class="voice-pill"></span>
      <div class="eq" aria-hidden="true"><span></span><span></span><span></span><span></span></div>
    </div>
    <div id="captionText" class="caption"></div>
  </div>
</div>
<script>
(function() {
  const params = new URLSearchParams(window.location.search);
  const token = params.get('token') || '';
  const stage = document.getElementById('stage');
  const card = document.getElementById('card');
  const platBadge = document.getElementById('platBadge');
  const speakerName = document.getElementById('speakerName');
  const voicePill = document.getElementById('voicePill');
  const captionText = document.getElementById('captionText');
  const customStyleEl = document.getElementById('customOverlayCSS');

  const posVal = (params.get('pos') || params.get('position') || '').toLowerCase();
  if (posVal === 'top') {
    stage.classList.add('top');
  }
  if ((params.get('align') || '').toLowerCase() === 'center') {
    stage.classList.add('center');
  }
  const scalePct = parseInt(params.get('scale') || '100', 10);
  if (!isNaN(scalePct) && scalePct >= 50 && scalePct <= 300) {
    document.documentElement.style.setProperty('--scale', (scalePct / 100).toFixed(2));
  }

  function loadStyle() {
    const styleUrl = '/api/overlay/style' + (token ? '?token=' + encodeURIComponent(token) : '');
    fetch(styleUrl, { cache: 'no-store' })
      .then(function(r) { return r.json(); })
      .then(function(st) {
        if (!posVal) {
          const srvPos = (st.tts_position || 'bottom').toLowerCase();
          stage.classList.toggle('top', srvPos === 'top');
        }
        if (customStyleEl) {
          customStyleEl.textContent = st.custom_css || '';
        }
      })
      .catch(function() {});
  }
  loadStyle();

  let activeId = '';
  let hideTimer = null;

  function showSpeech(ev) {
    if (hideTimer) {
      clearTimeout(hideTimer);
      hideTimer = null;
    }
    activeId = ev.id || '';
    const plat = (ev.platform || 'tts').toLowerCase();
    platBadge.textContent = plat.toUpperCase();
    platBadge.className = 'badge ' + plat;
    speakerName.textContent = ev.username || 'Chat TTS';
    speakerName.className = 'speaker ' + plat;
    if (ev.voiceId) {
      voicePill.textContent = ev.voiceId;
      voicePill.style.display = 'inline-block';
    } else {
      voicePill.style.display = 'none';
    }
    captionText.textContent = ev.text || '';
    card.classList.add('visible');
    hideTimer = setTimeout(hideSpeech, 30000);
  }

  function hideSpeech() {
    if (hideTimer) {
      clearTimeout(hideTimer);
      hideTimer = null;
    }
    activeId = '';
    card.classList.remove('visible');
  }

  function connect() {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = proto + '//' + window.location.host + '/ws' + (token ? '?token=' + encodeURIComponent(token) : '');
    const ws = new WebSocket(wsUrl);

    ws.onmessage = function(e) {
      try {
        const env = JSON.parse(e.data);
        if (env.speechEvent) {
          const se = env.speechEvent;
          if (se.finished) {
            if (!activeId || se.id === activeId) {
              hideSpeech();
            }
          } else if (se.text) {
            showSpeech(se);
          }
        } else if (env.state) {
          loadStyle();
          const st = env.state;
          if (st.panicked || !st.currentlySpeaking) {
            hideSpeech();
          } else if (!activeId && st.currentlySpeaking) {
            showSpeech({
              id: 'state-init',
              text: st.currentlySpeaking,
              voiceId: st.currentVoice || '',
              username: 'Now Speaking',
              platform: 'tts'
            });
          }
        }
      } catch (_) {}
    };

    ws.onclose = function() {
      setTimeout(connect, 2000);
    };
  }

  connect();
})();
</script>
</body>
</html>
`

const chatOverlayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TownCrier — Live Chat Overlay</title>
<style>
  :root {
    --bg-row: rgba(15, 23, 42, 0.86);
    --bg-paid: rgba(30, 41, 59, 0.94);
    --border-row: rgba(51, 65, 85, 0.65);
    --border-paid: rgba(56, 189, 248, 0.75);
    --text: #F8FAFC;
    --muted: #94A3B8;
    --accent: #38BDF8;
    --trans-color: #BAE6FD;
    --font-scale: 1;
    --card-radius: 10px;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    width: 100%;
    height: 100%;
    background: transparent;
    overflow: hidden;
    font-family: 'Segoe UI', Inter, system-ui, -apple-system, sans-serif;
    font-size: calc(15px * var(--font-scale));
    color: var(--text);
  }
  .wrapper {
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: 12px;
    gap: 8px;
  }
  .summary-bar {
    display: none;
    flex-wrap: wrap;
    gap: 10px;
    background: rgba(15, 23, 42, 0.92);
    border: 1px solid rgba(56, 189, 248, 0.45);
    border-radius: var(--card-radius);
    padding: 6px 12px;
    font-size: 0.85em;
    font-weight: 700;
    color: var(--text);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }
  .summary-bar.visible {
    display: flex;
  }
  .feed {
    flex: 1;
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    gap: 6px;
    overflow: hidden;
  }
  .msg {
    background: var(--bg-row);
    border: 1px solid var(--border-row);
    border-radius: var(--card-radius);
    padding: 8px 12px;
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.42);
    backdrop-filter: blur(8px);
    line-height: 1.38;
    word-break: break-word;
    transition: opacity 300ms ease;
  }
  .msg.paid {
    background: var(--bg-paid);
    border-color: var(--border-paid);
  }
  .msg.fading {
    opacity: 0;
  }
  .plat {
    display: inline-block;
    font-size: 0.72em;
    font-weight: 800;
    text-transform: uppercase;
    padding: 1px 6px;
    border-radius: 5px;
    margin-right: 6px;
    vertical-align: middle;
    background: #334155;
    color: #F8FAFC;
  }
  .plat.twitch { background: #9146FF; }
  .plat.youtube { background: #FF0033; }
  .plat.kick { background: #53FC18; color: #090D16; }
  .plat.tiktok { background: #00F2FE; color: #090D16; }
  .user {
    font-weight: 700;
    color: var(--accent);
    margin-right: 6px;
  }
  .text {
    color: var(--text);
  }
  .trans {
    display: block;
    font-size: 0.9em;
    color: var(--trans-color);
    margin-top: 2px;
  }
  .status {
    float: right;
    font-size: 0.78em;
    color: var(--muted);
    margin-left: 8px;
  }
</style>
<style id="customOverlayCSS"></style>
</head>
<body>
<div class="wrapper">
  <div id="summaryBar" class="summary-bar"></div>
  <div id="feed" class="feed" role="log" aria-live="polite"></div>
</div>
<script>
(function() {
  const params = new URLSearchParams(window.location.search);
  const preset = params.get('preset') || '';
  const summaryParam = (params.get('summary') || '').toLowerCase();
  const token = params.get('token') || '';
  const maxRows = Math.min(100, Math.max(5, parseInt(params.get('limit') || '25', 10) || 25));
  const fadeSec = Math.max(0, parseInt(params.get('fade') || '0', 10) || 0);

  const summaryBar = document.getElementById('summaryBar');
  const feed = document.getElementById('feed');
  const customStyleEl = document.getElementById('customOverlayCSS');
  let showSummary = false;
  let summaryState = {};

  function applyScale(pct) {
    const clean = (!isNaN(pct) && pct >= 50 && pct <= 300) ? pct : 100;
    document.documentElement.style.setProperty('--font-scale', (clean / 100).toFixed(2));
  }

  function renderSummary(s) {
    if (!showSummary || !s) {
      summaryBar.classList.remove('visible');
      return;
    }
    const parts = [];
    if (s.total_subs > 0) parts.push('⭐ ' + s.total_subs + ' Subs');
    if (s.total_gift_subs > 0) parts.push('🎁 ' + s.total_gift_subs + ' Gift Subs');
    if (s.total_bits > 0) parts.push('🪙 ' + Math.round(s.total_bits) + ' Bits');
    if (s.total_kicks > 0) parts.push('🟢 ' + Math.round(s.total_kicks) + ' Kicks');
    if (s.total_diamonds > 0) parts.push('💎 ' + Math.round(s.total_diamonds) + ' Diamonds');
    if (s.total_jewels > 0) parts.push('💠 ' + Math.round(s.total_jewels) + ' Jewels');
    if (s.super_chat_by_curr) {
      Object.keys(s.super_chat_by_curr).sort().forEach(function(c) {
        const v = s.super_chat_by_curr[c];
        if (v > 0) parts.push('💵 ' + c + ' ' + v.toFixed(2));
      });
    }
    if (s.total_raids > 0) parts.push('🚀 ' + s.total_raids + ' Raids (' + (s.total_raid_viewers || 0) + ')');
    if (s.total_follows > 0) parts.push('❤️ ' + s.total_follows + ' Follows');
    if (parts.length === 0) {
      summaryBar.classList.remove('visible');
      return;
    }
    summaryBar.textContent = parts.join('   •   ');
    summaryBar.classList.add('visible');
  }

  function outcomeIcon(outcome, spoken) {
    if (spoken || outcome === 'spoken') return '🗣️';
    if (outcome === 'queued') return '⏳';
    return '';
  }

  function createRow(entry) {
    const div = document.createElement('div');
    div.className = 'msg' + (entry.is_paid_or_event ? ' paid' : '');
    div.dataset.id = entry.id || '';

    const st = document.createElement('span');
    st.className = 'status';
    st.textContent = outcomeIcon(entry.outcome, entry.spoken);
    div.appendChild(st);

    const plat = (entry.platform || 'chat').toLowerCase();
    const badge = document.createElement('span');
    badge.className = 'plat ' + plat;
    badge.textContent = plat;
    div.appendChild(badge);

    const user = document.createElement('span');
    user.className = 'user ' + plat;
    user.textContent = (entry.display_name || entry.user_name || 'Chat') + ':';
    div.appendChild(user);

    const hasTrans = !!(entry.translated_text && entry.translated_text !== entry.raw_text);
    const txt = document.createElement('span');
    txt.className = 'text';
    txt.textContent = hasTrans ? entry.translated_text : (entry.raw_text || entry.spoken_text || '');
    div.appendChild(txt);

    if (hasTrans && entry.raw_text) {
      const tr = document.createElement('span');
      tr.className = 'trans';
      tr.textContent = '🌐 ' + entry.raw_text;
      div.appendChild(tr);
    }

    if (fadeSec > 0) {
      setTimeout(function() {
        div.classList.add('fading');
        setTimeout(function() {
          if (div.parentNode) div.parentNode.removeChild(div);
        }, 350);
      }, fadeSec * 1000);
    }
    return div;
  }

  function appendEntry(entry) {
    if (!entry) return;
    if (entry.id && feed.querySelector('[data-id="' + CSS.escape(entry.id) + '"]')) {
      return;
    }
    const row = createRow(entry);
    feed.appendChild(row);
    while (feed.children.length > maxRows) {
      feed.removeChild(feed.firstChild);
    }
  }

  function buildQuery() {
    const q = new URLSearchParams();
    if (preset) q.set('preset', preset);
    if (summaryParam) q.set('summary', summaryParam);
    q.set('limit', String(maxRows));
    if (token) q.set('token', token);
    return q.toString();
  }

  function resolveShowSummary(snap) {
    if (summaryParam === '1' || summaryParam === 'true' || summaryParam === 'on' || summaryParam === 'yes') {
      return true;
    }
    if (summaryParam === '0' || summaryParam === 'false' || summaryParam === 'off' || summaryParam === 'no') {
      return false;
    }
    if (snap && typeof snap.show_summary === 'boolean') {
      return snap.show_summary;
    }
    return !!(snap && snap.preset && snap.preset.show_summary);
  }

  function loadSnapshot() {
    fetch('/api/overlay/chat?' + buildQuery(), { cache: 'no-store' })
      .then(function(r) { return r.json(); })
      .then(function(snap) {
        const scaleOverride = parseInt(params.get('scale') || '0', 10);
        applyScale(scaleOverride > 0 ? scaleOverride : (snap.scale || 100));
        if (customStyleEl) {
          customStyleEl.textContent = snap.custom_css || '';
        }
        showSummary = resolveShowSummary(snap);
        summaryState = snap.summary || {};
        renderSummary(summaryState);
        feed.innerHTML = '';
        const list = (snap.entries || []).slice().reverse();
        list.forEach(appendEntry);
      })
      .catch(function() {});
  }

  function refreshSummaryOnly() {
    if (!showSummary) return;
    fetch('/api/overlay/chat?' + buildQuery(), { cache: 'no-store' })
      .then(function(r) { return r.json(); })
      .then(function(snap) {
        showSummary = resolveShowSummary(snap);
        summaryState = snap.summary || {};
        renderSummary(summaryState);
      })
      .catch(function() {});
  }

  loadSnapshot();

  function connectStream() {
    const es = new EventSource('/api/overlay/stream?' + buildQuery());
    es.onmessage = function(e) {
      try {
        const ev = JSON.parse(e.data);
        if (ev.kind === 'reload') {
          loadSnapshot();
        } else if (ev.kind === 'append' && ev.entry) {
          appendEntry(ev.entry);
          if (showSummary && ev.entry.is_paid_or_event) {
            refreshSummaryOnly();
          }
        } else if (ev.kind === 'outcome' && ev.event_id && ev.update) {
          const row = feed.querySelector('[data-id="' + CSS.escape(ev.event_id) + '"]');
          if (row) {
            const st = row.querySelector('.status');
            if (st) st.textContent = outcomeIcon(ev.update.outcome, ev.update.spoken);
          }
        }
      } catch (_) {}
    };
  }

  connectStream();
})();
</script>
</body>
</html>
`

const overlayDesignerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TownCrier — OBS Overlay Style Designer</title>
<style>
  :root {
    --ui-bg: #0B1120;
    --ui-surface: #0F172A;
    --ui-border: #334155;
    --ui-text: #F8FAFC;
    --ui-muted: #94A3B8;
    --ui-accent: #38BDF8;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--ui-bg);
    color: var(--ui-text);
    font-family: 'Segoe UI', Inter, system-ui, -apple-system, sans-serif;
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  header {
    background: var(--ui-surface);
    border-bottom: 1px solid var(--ui-border);
    padding: 14px 24px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }
  header h1 {
    font-size: 1.15rem;
    font-weight: 700;
    color: var(--ui-accent);
  }
  header p {
    font-size: 0.85rem;
    color: var(--ui-muted);
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  button {
    cursor: pointer;
    border: 1px solid var(--ui-border);
    background: #1E293B;
    color: var(--ui-text);
    padding: 8px 14px;
    border-radius: 8px;
    font-size: 0.88rem;
    font-weight: 600;
    transition: all 150ms ease;
  }
  button:hover {
    border-color: var(--ui-accent);
    background: #334155;
  }
  .status-pill {
    font-size: 0.84rem;
    color: #34D399;
    font-weight: 600;
  }
  .main {
    flex: 1;
    display: grid;
    grid-template-columns: 380px 1fr;
    gap: 20px;
    padding: 20px 24px;
  }
  @media (max-width: 960px) {
    .main { grid-template-columns: 1fr; }
  }
  .panel {
    background: var(--ui-surface);
    border: 1px solid var(--ui-border);
    border-radius: 12px;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .panel h2 {
    font-size: 0.96rem;
    font-weight: 700;
    color: var(--ui-accent);
    border-bottom: 1px solid rgba(51, 65, 85, 0.6);
    padding-bottom: 6px;
  }
  .field-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    font-size: 0.88rem;
  }
  .field-row label {
    color: var(--ui-text);
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  input[type="color"] {
    width: 38px;
    height: 28px;
    border: 1px solid var(--ui-border);
    border-radius: 6px;
    background: transparent;
    cursor: pointer;
  }
  input[type="range"] {
    width: 130px;
    accent-color: var(--ui-accent);
  }
  input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--ui-accent);
    cursor: pointer;
  }
  .preview-stage {
    position: relative;
    flex: 1;
    min-height: 460px;
    border-radius: 12px;
    border: 1px solid var(--ui-border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    overflow: hidden;
  }
  .preview-stage.checker {
    background-image:
      linear-gradient(45deg, #1e293b 25%, transparent 25%),
      linear-gradient(-45deg, #1e293b 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, #1e293b 75%),
      linear-gradient(-45deg, transparent 75%, #1e293b 75%);
    background-size: 24px 24px;
    background-position: 0 0, 0 12px, 12px -12px, -12px 0;
    background-color: #0f172a;
  }
  .preview-stage.dark { background: #090D16; }
  .preview-stage.light { background: #E2E8F0; }

  /* Base Overlay Classes inside Preview */
  .preview-scope {
    --bg: rgba(15, 23, 42, 0.92);
    --border: rgba(56, 189, 248, 0.48);
    --bg-row: rgba(15, 23, 42, 0.86);
    --bg-paid: rgba(30, 41, 59, 0.94);
    --border-row: rgba(51, 65, 85, 0.65);
    --border-paid: rgba(56, 189, 248, 0.75);
    --text: #F8FAFC;
    --muted: #94A3B8;
    --accent: #38BDF8;
    --trans-color: #BAE6FD;
    --font-scale: 1;
    --card-radius: 10px;
    font-size: calc(15px * var(--font-scale));
    color: var(--text);
  }
  .preview-scope .card {
    max-width: 680px;
    background: var(--bg);
    border: 1.5px solid var(--border);
    border-radius: 14px;
    padding: 14px 20px;
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.55);
    margin-bottom: 18px;
  }
  .preview-scope .header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 6px;
  }
  .preview-scope .badge, .preview-scope .plat {
    display: inline-block;
    font-size: 0.72em;
    font-weight: 800;
    text-transform: uppercase;
    padding: 2px 7px;
    border-radius: 5px;
    background: #334155;
    color: #F8FAFC;
  }
  .preview-scope .plat { margin-right: 6px; }
  .preview-scope .badge.twitch, .preview-scope .plat.twitch { background: #9146FF; }
  .preview-scope .badge.youtube, .preview-scope .plat.youtube { background: #FF0033; }
  .preview-scope .badge.kick, .preview-scope .plat.kick { background: #53FC18; color: #090D16; }
  .preview-scope .badge.tiktok, .preview-scope .plat.tiktok { background: #00F2FE; color: #090D16; }
  .preview-scope .speaker, .preview-scope .user {
    font-weight: 700;
    color: var(--accent);
    margin-right: 6px;
  }
  .preview-scope .voice-pill {
    font-size: 0.74rem;
    color: var(--muted);
    background: rgba(148, 163, 184, 0.14);
    padding: 2px 8px;
    border-radius: 999px;
  }
  .preview-scope .caption {
    font-size: 1.15em;
    font-weight: 600;
    color: var(--text);
  }
  .preview-scope .feed {
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-width: 520px;
  }
  .preview-scope .msg {
    background: var(--bg-row);
    border: 1px solid var(--border-row);
    border-radius: var(--card-radius);
    padding: 8px 12px;
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.42);
    line-height: 1.38;
  }
  .preview-scope .msg.paid {
    background: var(--bg-paid);
    border-color: var(--border-paid);
  }
  .preview-scope .text { color: var(--text); }
  .preview-scope .trans {
    display: block;
    font-size: 0.9em;
    color: var(--trans-color);
    margin-top: 2px;
  }
  .preview-scope .status {
    float: right;
    font-size: 0.78em;
    color: var(--muted);
    margin-left: 8px;
  }
</style>
<style id="designerDynamicCSS"></style>
</head>
<body>
<header>
  <div>
    <h1>🎨 TownCrier — OBS Overlay Style Designer</h1>
    <p>Adjust colors, tags, and layout below. Changes save automatically and live-refresh your OBS Browser Sources.</p>
  </div>
  <div class="actions">
    <span id="saveStatus" class="status-pill">✓ Live-synced with OBS</span>
    <button id="resetBtn" type="button">↺ Reset to Default</button>
  </div>
</header>

<div class="main">
  <div class="panel">
    <h2>Colors & Backgrounds</h2>
    <div class="field-row">
      <label for="bgColor">Message Background</label>
      <div style="display:flex;align-items:center;gap:6px;">
        <input type="color" id="bgColor" value="#0f172a">
        <input type="range" id="bgOpacity" min="0" max="100" value="86" title="Background Opacity">
      </div>
    </div>
    <div class="field-row">
      <label for="paidBgColor">Paid / Alert Background</label>
      <div style="display:flex;align-items:center;gap:6px;">
        <input type="color" id="paidBgColor" value="#1e293b">
        <input type="range" id="paidBgOpacity" min="0" max="100" value="94" title="Paid Background Opacity">
      </div>
    </div>
    <div class="field-row">
      <label for="borderColor">Card Border Color</label>
      <input type="color" id="borderColor" value="#38bdf8">
    </div>
    <div class="field-row">
      <label for="nameColor">Username / Speaker Color</label>
      <input type="color" id="nameColor" value="#38bdf8">
    </div>
    <div class="field-row">
      <label for="textColor">Primary Message Text Color</label>
      <input type="color" id="textColor" value="#f8fafc">
    </div>
    <div class="field-row">
      <label for="transColor">Original Foreign Text (🌐) Color</label>
      <input type="color" id="transColor" value="#bae6fd">
    </div>

    <h2>Elements & Visibility</h2>
    <div class="field-row">
      <label><input type="checkbox" id="showPlatformTag" checked> Show Platform Tag (TWITCH, YOUTUBE...)</label>
    </div>
    <div class="field-row">
      <label><input type="checkbox" id="platformNameColors"> Color Usernames by Platform</label>
    </div>
    <div class="field-row">
      <label><input type="checkbox" id="showStatusIcon" checked> Show TTS Status Icons (🗣️ / ⏳)</label>
    </div>
    <div class="field-row">
      <label><input type="checkbox" id="showOriginalTrans" checked> Show Original Foreign Text Line (🌐)</label>
    </div>
    <div class="field-row">
      <label><input type="checkbox" id="showVoicePill" checked> Show Voice Badge on Now-Speaking</label>
    </div>
    <div class="field-row">
      <label><input type="checkbox" id="showCardBorders" checked> Show Card Borders & Drop Shadow</label>
    </div>

    <h2>Typography & Shape</h2>
    <div class="field-row">
      <label for="fontScale">Font Size Scale</label>
      <input type="range" id="fontScale" min="70" max="200" value="100">
    </div>
    <div class="field-row">
      <label for="cardRadius">Corner Radius</label>
      <input type="range" id="cardRadius" min="0" max="24" value="10">
    </div>
  </div>

  <div class="panel">
    <div style="display:flex;align-items:center;justify-content:space-between;">
      <h2>Live Overlay Preview</h2>
      <div style="display:flex;gap:6px;">
        <button type="button" data-bg="checker" class="bg-btn">Checkerboard</button>
        <button type="button" data-bg="dark" class="bg-btn">Dark</button>
        <button type="button" data-bg="light" class="bg-btn">Light</button>
      </div>
    </div>

    <div id="previewStage" class="preview-stage checker preview-scope">
      <div>
        <div class="card">
          <div class="header">
            <span class="badge twitch">TWITCH</span>
            <span class="speaker twitch">StreamerFan42</span>
            <span class="voice-pill">kokoro:af_heart</span>
          </div>
          <div class="caption">StreamerFan42 says: This stream overlay looks awesome!</div>
        </div>
      </div>

      <div class="feed">
        <div class="msg">
          <span class="status">🗣️</span>
          <span class="plat twitch">twitch</span>
          <span class="user twitch">NightOwl:</span>
          <span class="text">Let's gooo! Great raid earlier!</span>
        </div>
        <div class="msg">
          <span class="status">🗣️</span>
          <span class="plat youtube">youtube</span>
          <span class="user youtube">Carlos_MX:</span>
          <span class="text">Hello friends, this stream is amazing today!</span>
          <span class="trans">🌐 ¡Hola amigos, este directo está increíble hoy!</span>
        </div>
        <div class="msg paid">
          <span class="status">⏳</span>
          <span class="plat kick">kick</span>
          <span class="user kick">PixelHero:</span>
          <span class="text">Raided with 45 viewers</span>
        </div>
      </div>
    </div>
  </div>
</div>

<script>
(function() {
  const params = new URLSearchParams(window.location.search);
  const token = params.get('token') || '';
  const apiUrl = '/api/overlay/style' + (token ? '?token=' + encodeURIComponent(token) : '');

  const defaults = {
    bgColor: '#0f172a',
    bgOpacity: 86,
    paidBgColor: '#1e293b',
    paidBgOpacity: 94,
    borderColor: '#38bdf8',
    nameColor: '#38bdf8',
    textColor: '#f8fafc',
    transColor: '#bae6fd',
    showPlatformTag: true,
    platformNameColors: false,
    showStatusIcon: true,
    showOriginalTrans: true,
    showVoicePill: true,
    showCardBorders: true,
    fontScale: 100,
    cardRadius: 10
  };

  const els = {};
  Object.keys(defaults).forEach(function(k) {
    els[k] = document.getElementById(k);
  });
  const dynamicCSS = document.getElementById('designerDynamicCSS');
  const saveStatus = document.getElementById('saveStatus');
  const previewStage = document.getElementById('previewStage');
  let saveTimer = null;

  function hexToRgba(hex, alphaPct) {
    const clean = (hex || '#0f172a').replace('#', '');
    const r = parseInt(clean.substring(0, 2), 16) || 15;
    const g = parseInt(clean.substring(2, 4), 16) || 23;
    const b = parseInt(clean.substring(4, 6), 16) || 42;
    const a = (Math.max(0, Math.min(100, Number(alphaPct))) / 100).toFixed(2);
    return 'rgba(' + r + ', ' + g + ', ' + b + ', ' + a + ')';
  }

  function readState() {
    return {
      bgColor: els.bgColor.value,
      bgOpacity: Number(els.bgOpacity.value),
      paidBgColor: els.paidBgColor.value,
      paidBgOpacity: Number(els.paidBgOpacity.value),
      borderColor: els.borderColor.value,
      nameColor: els.nameColor.value,
      textColor: els.textColor.value,
      transColor: els.transColor.value,
      showPlatformTag: els.showPlatformTag.checked,
      platformNameColors: els.platformNameColors.checked,
      showStatusIcon: els.showStatusIcon.checked,
      showOriginalTrans: els.showOriginalTrans.checked,
      showVoicePill: els.showVoicePill.checked,
      showCardBorders: els.showCardBorders.checked,
      fontScale: Number(els.fontScale.value),
      cardRadius: Number(els.cardRadius.value)
    };
  }

  function applyStateToControls(st) {
    const merged = Object.assign({}, defaults, st || {});
    els.bgColor.value = merged.bgColor;
    els.bgOpacity.value = merged.bgOpacity;
    els.paidBgColor.value = merged.paidBgColor;
    els.paidBgOpacity.value = merged.paidBgOpacity;
    els.borderColor.value = merged.borderColor;
    els.nameColor.value = merged.nameColor;
    els.textColor.value = merged.textColor;
    els.transColor.value = merged.transColor;
    els.showPlatformTag.checked = !!merged.showPlatformTag;
    els.platformNameColors.checked = !!merged.platformNameColors;
    els.showStatusIcon.checked = !!merged.showStatusIcon;
    els.showOriginalTrans.checked = !!merged.showOriginalTrans;
    els.showVoicePill.checked = !!merged.showVoicePill;
    els.showCardBorders.checked = !!merged.showCardBorders;
    els.fontScale.value = merged.fontScale;
    els.cardRadius.value = merged.cardRadius;
  }

  function generateCSS(st) {
    const bgRow = hexToRgba(st.bgColor, st.bgOpacity);
    const bgPaid = hexToRgba(st.paidBgColor, st.paidBgOpacity);
    const borderRgba = hexToRgba(st.borderColor, st.showCardBorders ? 65 : 0);
    const lines = [
      ':root, .preview-scope {',
      '  --bg: ' + bgRow + ';',
      '  --bg-row: ' + bgRow + ';',
      '  --bg-paid: ' + bgPaid + ';',
      '  --border: ' + borderRgba + ';',
      '  --border-row: ' + borderRgba + ';',
      '  --border-paid: ' + hexToRgba(st.borderColor, st.showCardBorders ? 85 : 0) + ';',
      '  --accent: ' + st.nameColor + ';',
      '  --text: ' + st.textColor + ';',
      '  --trans-color: ' + st.transColor + ';',
      '  --font-scale: ' + (st.fontScale / 100).toFixed(2) + ';',
      '  --card-radius: ' + st.cardRadius + 'px;',
      '}'
    ];
    if (!st.showPlatformTag) {
      lines.push('.plat, .badge { display: none !important; }');
    }
    if (st.platformNameColors) {
      lines.push('.user.twitch, .speaker.twitch { color: #B983FF !important; }');
      lines.push('.user.youtube, .speaker.youtube { color: #FF4D6D !important; }');
      lines.push('.user.kick, .speaker.kick { color: #53FC18 !important; }');
      lines.push('.user.tiktok, .speaker.tiktok { color: #00F2FE !important; }');
    }
    if (!st.showStatusIcon) {
      lines.push('.status { display: none !important; }');
    }
    if (!st.showOriginalTrans) {
      lines.push('.trans { display: none !important; }');
    }
    if (!st.showVoicePill) {
      lines.push('.voice-pill { display: none !important; }');
    }
    if (!st.showCardBorders) {
      lines.push('.msg, .card { border-color: transparent !important; box-shadow: none !important; }');
      lines.push('.text, .user, .speaker, .caption { text-shadow: 0 2px 4px rgba(0,0,0,0.9) !important; }');
    }
    return lines.join('\n');
  }

  function saveStyle(customCSS, styleObj, statusMsg) {
    saveStatus.textContent = '⏳ Saving & updating OBS...';
    fetch(apiUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        custom_css: customCSS,
        style_json: styleObj ? JSON.stringify(styleObj) : ''
      })
    })
      .then(function(r) {
        if (!r.ok) throw new Error('Save failed');
        saveStatus.textContent = statusMsg || '✓ Auto-saved & Live-Updated OBS';
      })
      .catch(function() {
        saveStatus.textContent = '⚠️ Save failed';
      });
  }

  function syncFromControls(autoSave) {
    const st = readState();
    const css = generateCSS(st);
    dynamicCSS.textContent = css;
    if (autoSave) {
      if (saveTimer) clearTimeout(saveTimer);
      saveTimer = setTimeout(function() {
        saveStyle(css, st, '✓ Auto-saved & Live-Updated OBS');
      }, 150);
    }
  }

  Object.keys(els).forEach(function(k) {
    els[k].addEventListener('input', function() { syncFromControls(true); });
    els[k].addEventListener('change', function() { syncFromControls(true); });
  });

  document.querySelectorAll('.bg-btn').forEach(function(btn) {
    btn.addEventListener('click', function() {
      previewStage.className = 'preview-stage preview-scope ' + (btn.dataset.bg || 'checker');
    });
  });

  document.getElementById('resetBtn').addEventListener('click', function() {
    if (saveTimer) clearTimeout(saveTimer);
    applyStateToControls(defaults);
    dynamicCSS.textContent = '';
    saveStyle('', null, '✓ Reset to Default Obsidian Slate');
  });

  fetch(apiUrl, { cache: 'no-store' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.style_json) {
        try {
          applyStateToControls(JSON.parse(data.style_json));
        } catch (_) {}
      } else {
        applyStateToControls(defaults);
      }
      if (data.custom_css) {
        dynamicCSS.textContent = data.custom_css;
      } else {
        syncFromControls(false);
      }
    })
    .catch(function() {
      syncFromControls(false);
    });
})();
</script>
</body>
</html>
`
