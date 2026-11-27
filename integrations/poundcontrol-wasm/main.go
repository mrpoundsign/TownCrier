//go:build wasip1 && wasm

// Command poundcontrol-wasm builds a native PoundControl WebAssembly (.wasm) plugin
// adhering to PoundControl's WASM Plugin ABI (pc_alloc, pc_free, pc_manifest,
// pc_on_start, pc_handle_event, pc_on_timer, pc_on_global_settings,
// pc_on_config_action, pc_register_actions).
//
// Build with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/bin/towncrier.wasm ./integrations/poundcontrol-wasm
package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

//go:wasmimport poundcontrol pc_update_visual
func pcUpdateVisual(ctxPtr, ctxLen, patchPtr, patchLen uint32) int32

//go:wasmimport poundcontrol pc_get_global_settings
func pcGetGlobalSettings(outPtr, outCap uint32) int32

//go:wasmimport poundcontrol pc_save_global_settings
func pcSaveGlobalSettings(jsonPtr, jsonLen uint32) int32

//go:wasmimport poundcontrol pc_log
func pcLog(msgPtr, msgLen uint32)

//go:wasmimport poundcontrol pc_http_request
func pcHTTPRequest(reqPtr, reqLen, respPtr, respCap uint32) int32

//go:wasmimport poundcontrol pc_set_timer
func pcSetTimer(idPtr, idLen, ctxPtr, ctxLen, intervalMs uint32) int32

//go:wasmimport poundcontrol pc_register_actions
func pcRegisterActions(actionsPtr, actionsLen uint32) int32

const (
	pluginUUID = "com.github.mrpoundsign.towncrier.wasm"

	actMode    = "com.github.mrpoundsign.towncrier.wasm.mode"
	actPanic   = "com.github.mrpoundsign.towncrier.wasm.panic"
	actQueue   = "com.github.mrpoundsign.towncrier.wasm.queue"
	actSpeak   = "com.github.mrpoundsign.towncrier.wasm.speak"
	actVolume  = "com.github.mrpoundsign.towncrier.wasm.volume"
	actDynamic = "com.github.mrpoundsign.towncrier.wasm.action"

	dynPluginPrefix = "com.github.mrpoundsign.towncrier.wasm.plugin."
	dynBotPrefix    = "com.github.mrpoundsign.towncrier.wasm.bot."
)

// PluginVersion is overridden at build time via -ldflags "-X main.PluginVersion=...".
var PluginVersion = "0.2.0"

type slotInstance struct {
	Context    string
	Action     string
	Controller string
	Settings   map[string]any
}

type globalSettings struct {
	Host  string
	Port  int
	Token string
}

type actionEvent struct {
	Event      string         `json:"event"`
	Context    string         `json:"context"`
	Action     string         `json:"action"`
	Controller string         `json:"controller"`
	Settings   map[string]any `json:"settings"`
	Ticks      int            `json:"ticks"`
}

type configActionEvent struct {
	Action   string         `json:"action"`
	Settings map[string]any `json:"settings"`
}

type httpResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error"`
}

type statusResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
	State *struct {
		TtsEnabled     bool    `json:"ttsEnabled"`
		OutputMode     string  `json:"outputMode"`
		SpeakingActive bool    `json:"speakingActive"`
		Paused         bool    `json:"paused"`
		Panicked       bool    `json:"panicked"`
		QueueDepth     int     `json:"queueDepth"`
		Volume         float64 `json:"volume"`
	} `json:"state"`
}

type externalActionVisual struct {
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

type externalPluginAction struct {
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
	Visual       *externalActionVisual `json:"visual,omitempty"`
}

type externalBotAction struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type actionCatalog struct {
	BotActions    []externalBotAction    `json:"bot_actions"`
	PluginActions []externalPluginAction `json:"plugin_actions"`
}

type dynTarget struct {
	UUID         string
	Kind         string // "plugin" | "bot"
	PluginUUID   string
	ActionID     string
	DisplayName  string
	DefaultTitle string
	DefaultColor string
	DefaultImage string
	HideTitle    bool
	Controllers  []string
	Visual       *externalActionVisual
}

var (
	allocMu     sync.Mutex
	allocations = make(map[uint32][]byte)

	pollMu sync.Mutex

	mu            sync.Mutex
	slots         = make(map[string]*slotInstance)
	dynTargets    = make(map[string]dynTarget)
	lastActionSig string
	gSettings     globalSettings
	gHydrated     bool
	timerArmed    bool
	lastOnline    bool
	lastStateRef  = &statusResponse{}
)

//go:wasmexport pc_alloc
func pcAlloc(size uint32) uint32 {
	if size == 0 {
		size = 1
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	allocMu.Lock()
	allocations[ptr] = buf
	allocMu.Unlock()
	return ptr
}

//go:wasmexport pc_free
func pcFree(ptr uint32) {
	allocMu.Lock()
	delete(allocations, ptr)
	allocMu.Unlock()
}

func bytesPtrLen(b []byte) (uint32, uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b))
}

func readGuestInput(ptr, length uint32) []byte {
	allocMu.Lock()
	buf := allocations[ptr]
	allocMu.Unlock()
	if len(buf) < int(length) {
		return nil
	}
	cp := make([]byte, length)
	copy(cp, buf[:length])
	return cp
}

func returnHostString(s string) uint64 {
	raw := []byte(s)
	ptr := pcAlloc(uint32(len(raw)))
	allocMu.Lock()
	copy(allocations[ptr], raw)
	allocMu.Unlock()
	return (uint64(ptr) << 32) | uint64(len(raw))
}

func hostLog(msg string) {
	raw := []byte(msg)
	p, l := bytesPtrLen(raw)
	pcLog(p, l)
}

func callHostDynamicBuffer(invoke func(ptr, cap uint32) int32) ([]byte, bool) {
	capBytes := uint32(8192)
	buf := make([]byte, capBytes)
	p, _ := bytesPtrLen(buf)
	n := invoke(p, capBytes)
	if n >= 0 {
		return buf[:n], true
	}
	if n < -1 {
		needed := uint32(-n)
		if needed > 4*1024*1024 {
			return nil, false
		}
		buf = make([]byte, needed)
		p, _ = bytesPtrLen(buf)
		n = invoke(p, needed)
		if n >= 0 {
			return buf[:n], true
		}
	}
	return nil, false
}

func hydrateGlobalSettings(force bool) globalSettings {
	mu.Lock()
	if gHydrated && !force {
		gs := gSettings
		mu.Unlock()
		return gs
	}
	mu.Unlock()

	raw, ok := callHostDynamicBuffer(pcGetGlobalSettings)
	gs := globalSettings{Host: "127.0.0.1", Port: 8765}
	if ok && len(raw) > 0 {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			gs = parseGlobalMap(m)
		}
	}

	mu.Lock()
	gSettings = gs
	gHydrated = true
	mu.Unlock()
	return gs
}

func parseGlobalMap(m map[string]any) globalSettings {
	gs := globalSettings{Host: "127.0.0.1", Port: 8765}
	if h, ok := m["host"].(string); ok && strings.TrimSpace(h) != "" {
		gs.Host = strings.TrimSpace(h)
	}
	switch pv := m["port"].(type) {
	case float64:
		if int(pv) > 0 && int(pv) <= 65535 {
			gs.Port = int(pv)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(pv)); err == nil && n > 0 && n <= 65535 {
			gs.Port = n
		}
	}
	if tok, ok := m["token"].(string); ok {
		gs.Token = strings.TrimSpace(tok)
	}
	return gs
}

func doHTTP(method, path string, body any, gs globalSettings) (httpResponse, error) {
	var bodyStr string
	headers := map[string]string{}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return httpResponse{}, err
		}
		bodyStr = string(b)
		headers["Content-Type"] = "application/json"
	}
	if gs.Token != "" {
		headers["Authorization"] = "Bearer " + gs.Token
	}

	reqObj := map[string]any{
		"method":    method,
		"url":       fmt.Sprintf("http://%s:%d%s", gs.Host, gs.Port, path),
		"headers":   headers,
		"body":      bodyStr,
		"timeoutMs": 5000,
	}
	reqBytes, _ := json.Marshal(reqObj)
	rp, rl := bytesPtrLen(reqBytes)

	respBytes, ok := callHostDynamicBuffer(func(outPtr, outCap uint32) int32 {
		return pcHTTPRequest(rp, rl, outPtr, outCap)
	})
	if !ok {
		return httpResponse{}, fmt.Errorf("pc_http_request failed")
	}
	var out httpResponse
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return httpResponse{}, err
	}
	if out.Error != "" {
		return out, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}

func armPollTimerLocked() {
	if timerArmed {
		return
	}
	timerArmed = true
	id := []byte("poll-state")
	ip, il := bytesPtrLen(id)
	pcSetTimer(ip, il, 0, 0, 2000)
}

func svgDataURI(svg string) string {
	encoded := url.PathEscape(strings.TrimSpace(svg))
	encoded = strings.ReplaceAll(encoded, "+", "%2B")
	return "data:image/svg+xml;utf8," + encoded
}

func truncateTileLabel(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

func renderTileSVG(bg, _, accent, iconSVG, title, subtitle string) string {
	safeTitle := html.EscapeString(title)
	safeSub := html.EscapeString(subtitle)
	subElem := ""
	if safeSub != "" {
		subElem = fmt.Sprintf(`<text x="72" y="128" text-anchor="middle" font-family="Segoe UI,system-ui,sans-serif" font-size="13" font-weight="600" fill="#94A3B8">%s</text>`, safeSub)
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="144" viewBox="0 0 144 144">
  <rect width="144" height="144" rx="14" fill="%s"/>
  %s
  <text x="72" y="108" text-anchor="middle" font-family="Segoe UI,system-ui,sans-serif" font-size="15" font-weight="700" fill="%s">%s</text>
  %s
</svg>`, bg, iconSVG, accent, safeTitle, subElem)
	return svgDataURI(svg)
}

func renderOfflineSVG(label string) string {
	icon := `<circle cx="72" cy="48" r="20" fill="none" stroke="#52525B" stroke-width="4"/><line x1="58" y1="34" x2="86" y2="62" stroke="#52525B" stroke-width="4" stroke-linecap="round"/>`
	return renderTileSVG("#18181B", "#3F3F46", "#A1A1AA", icon, truncateTileLabel(label, 14), "OFFLINE")
}

func renderModeSVG(mode, color string) string {
	icon := fmt.Sprintf(`<path d="M40,28 H104 A8,8 0 0 1 112,36 V62 A8,8 0 0 1 104,70 H64 L48,82 V70 H40 A8,8 0 0 1 32,62 V36 A8,8 0 0 1 40,28 Z" fill="none" stroke="%s" stroke-width="4"/><circle cx="58" cy="49" r="4" fill="%s"/><circle cx="72" cy="49" r="4" fill="%s"/><circle cx="86" cy="49" r="4" fill="%s"/>`, color, color, color, color)
	return renderTileSVG("#0F172A", color, "#F8FAFC", icon, "TTS MODE", mode)
}

func renderPanicSVG(panicked bool) string {
	if panicked {
		icon := `<path d="M72,22 L102,34 V56 C102,74 72,84 72,84 C72,84 42,74 42,56 V34 Z" fill="none" stroke="#EF4444" stroke-width="4"/><line x1="58" y1="42" x2="86" y2="68" stroke="#EF4444" stroke-width="4" stroke-linecap="round"/><line x1="86" y1="42" x2="58" y2="68" stroke="#EF4444" stroke-width="4" stroke-linecap="round"/>`
		return renderTileSVG("#0F172A", "#EF4444", "#F8FAFC", icon, "PANIC", "MUTED")
	}
	icon := `<path d="M72,22 L102,34 V56 C102,74 72,84 72,84 C72,84 42,74 42,56 V34 Z" fill="none" stroke="#22C55E" stroke-width="4"/><polyline points="58,53 68,63 87,43" fill="none" stroke="#22C55E" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>`
	return renderTileSVG("#0F172A", "#22C55E", "#F8FAFC", icon, "AUDIO", "SAFE")
}

func renderQueueSVG(sub, color string, paused bool) string {
	var icon string
	if paused {
		icon = fmt.Sprintf(`<rect x="52" y="28" width="12" height="42" rx="3" fill="%s"/><rect x="80" y="28" width="12" height="42" rx="3" fill="%s"/>`, color, color)
	} else {
		icon = fmt.Sprintf(`<line x1="42" y1="34" x2="102" y2="34" stroke="%s" stroke-width="5" stroke-linecap="round"/><line x1="42" y1="50" x2="90" y2="50" stroke="%s" stroke-width="5" stroke-linecap="round"/><line x1="42" y1="66" x2="78" y2="66" stroke="%s" stroke-width="5" stroke-linecap="round"/>`, color, color, color)
	}
	return renderTileSVG("#0F172A", color, "#F8FAFC", icon, "QUEUE", sub)
}

func renderSpeakSVG(label string) string {
	icon := `<polygon points="38,44 54,44 74,28 74,72 54,56 38,56" fill="#38BDF8"/><path d="M84,38 Q96,50 84,62" fill="none" stroke="#38BDF8" stroke-width="4" stroke-linecap="round"/><path d="M94,30 Q110,50 94,70" fill="none" stroke="#38BDF8" stroke-width="4" stroke-linecap="round"/>`
	return renderTileSVG("#0F172A", "#38BDF8", "#F8FAFC", icon, "SPEAK", truncateTileLabel(label, 14))
}

func renderVolumeSVG(pct int) string {
	pct = min(max(pct, 0), 100)
	barW := max(int(math.Round(float64(pct)*0.96)), 4)
	icon := fmt.Sprintf(`<polygon points="38,38 50,38 64,24 64,66 50,52 38,52" fill="#38BDF8"/><path d="M74,35 Q84,45 74,55" fill="none" stroke="#38BDF8" stroke-width="4" stroke-linecap="round"/><path d="M84,27 Q98,45 84,63" fill="none" stroke="#38BDF8" stroke-width="4" stroke-linecap="round"/><rect x="24" y="76" width="96" height="8" rx="4" fill="#1E293B"/><rect x="24" y="76" width="%d" height="8" rx="4" fill="#38BDF8"/>`, barW)
	return renderTileSVG("#0F172A", "#38BDF8", "#F8FAFC", icon, "TTS VOL", fmt.Sprintf("%d%%", pct))
}

func renderBotActionSVG(name string, online bool) string {
	if !online {
		return renderOfflineSVG(name)
	}
	icon := `<polygon points="78,22 50,56 70,56 64,82 94,46 74,46" fill="#A855F7"/>`
	return renderTileSVG("#0F172A", "#A855F7", "#F8FAFC", icon, "BOT", truncateTileLabel(name, 14))
}

func renderGenericDynamicSVG(title, sub, color string) string {
	if color == "" {
		color = "#34D399"
	}
	icon := fmt.Sprintf(`<polygon points="56,28 96,50 56,72" fill="%s"/>`, color)
	return renderTileSVG("#0F172A", color, "#F8FAFC", icon, truncateTileLabel(title, 12), truncateTileLabel(sub, 14))
}

func updateVisualPatch(ctxID string, patch map[string]any) {
	pBytes, _ := json.Marshal(patch)
	cBytes := []byte(ctxID)
	cp, cl := bytesPtrLen(cBytes)
	pp, pl := bytesPtrLen(pBytes)
	pcUpdateVisual(cp, cl, pp, pl)
}

func attachWatchFeedback(patch map[string]any, label, sublabel string) {
	lbl := strings.TrimSpace(label)
	sub := strings.TrimSpace(sublabel)
	if lbl == "" && sub == "" {
		return
	}
	fb, _ := patch["feedback"].(map[string]any)
	if fb == nil {
		fb = map[string]any{}
		patch["feedback"] = fb
	}
	if lbl != "" {
		fb["label"] = lbl
	}
	if sub != "" {
		fb["sublabel"] = sub
	}
}

func updateSlotVisual(s *slotInstance, online bool, title, subtitle, color, imageURI, flash string, pct *int) {
	if s == nil {
		return
	}
	bgColor := "#0F172A"
	if !online {
		bgColor = "#18181B"
	}
	patch := map[string]any{
		"color":           color,
		"backgroundColor": bgColor,
	}
	isEncoder := strings.EqualFold(s.Controller, "Encoder")
	if isEncoder || imageURI == "" {
		patch["title"] = title
		patch["subtitle"] = subtitle
	} else {
		attachWatchFeedback(patch, title, subtitle)
	}
	if imageURI != "" {
		patch["image"] = imageURI
	}
	if flash != "" {
		patch["flash"] = flash
	}
	if pct != nil && isEncoder {
		fb, _ := patch["feedback"].(map[string]any)
		if fb == nil {
			fb = map[string]any{}
			patch["feedback"] = fb
		}
		fb["value"] = fmt.Sprintf("%d%%", *pct)
		fb["indicator"] = map[string]any{"value": *pct}
	}
	updateVisualPatch(s.Context, patch)
}

func renderSlot(s *slotInstance, online bool, st *statusResponse, target *dynTarget) {
	if s == nil {
		return
	}
	switch s.Action {
	case actMode:
		if !online {
			updateSlotVisual(s, false, "TTS MODE", "OFFLINE", "#3F3F46", renderOfflineSVG("TTS MODE"), "", nil)
			return
		}
		mode := "ALWAYS ON"
		color := "#38BDF8"
		if st != nil && st.State != nil {
			switch st.State.OutputMode {
			case "TTS_OUTPUT_MODE_AUTO_IDLE":
				mode = "AUTO-AFK"
				if st.State.SpeakingActive {
					color = "#22C55E"
				} else {
					color = "#F59E0B"
				}
			case "TTS_OUTPUT_MODE_ALWAYS_OFF":
				mode = "ALWAYS OFF"
				color = "#64748B"
			}
		}
		updateSlotVisual(s, true, "TTS MODE", mode, color, renderModeSVG(mode, color), "", nil)

	case actPanic:
		if !online {
			updateSlotVisual(s, false, "PANIC", "OFFLINE", "#3F3F46", renderOfflineSVG("PANIC"), "", nil)
			return
		}
		panicked := st != nil && st.State != nil && st.State.Panicked
		if panicked {
			updateSlotVisual(s, true, "PANIC", "MUTED", "#EF4444", renderPanicSVG(true), "", nil)
		} else {
			updateSlotVisual(s, true, "AUDIO", "SAFE", "#22C55E", renderPanicSVG(false), "", nil)
		}

	case actQueue:
		if !online {
			updateSlotVisual(s, false, "QUEUE", "OFFLINE", "#3F3F46", renderOfflineSVG("QUEUE"), "", nil)
			return
		}
		paused := st != nil && st.State != nil && st.State.Paused
		depth := 0
		if st != nil && st.State != nil {
			depth = st.State.QueueDepth
		}
		color := "#38BDF8"
		sub := fmt.Sprintf("Q:%d LIVE", depth)
		if paused {
			color = "#F59E0B"
			sub = fmt.Sprintf("Q:%d PAUSE", depth)
		}
		updateSlotVisual(s, true, "QUEUE", sub, color, renderQueueSVG(sub, color, paused), "", nil)

	case actSpeak:
		lbl := strSetting(s.Settings, "label", "SPEAK")
		if !online {
			updateSlotVisual(s, false, "SPEAK", "OFFLINE", "#3F3F46", renderOfflineSVG(lbl), "", nil)
			return
		}
		updateSlotVisual(s, true, "TOWNCRIER", lbl, "#38BDF8", renderSpeakSVG(lbl), "", nil)

	case actVolume:
		if !online {
			pct := 0
			updateSlotVisual(s, false, "VOLUME", "OFFLINE", "#3F3F46", renderOfflineSVG("TTS VOL"), "", &pct)
			return
		}
		pct := 80
		if st != nil && st.State != nil {
			pct = int(st.State.Volume*100 + 0.5)
		}
		updateSlotVisual(s, true, "VOLUME", fmt.Sprintf("%d%%", pct), "#38BDF8", renderVolumeSVG(pct), "", &pct)

	case actDynamic:
		tType := strings.ToUpper(strSetting(s.Settings, "targetType", "plugin"))
		lbl := strSetting(s.Settings, "label", strSetting(s.Settings, "actionId", "ACTION"))
		if !online {
			updateSlotVisual(s, false, "ACTION", "OFFLINE", "#3F3F46", renderOfflineSVG(lbl), "", nil)
			return
		}
		color := "#34D399"
		title := "PLUGIN"
		if tType == "BOT" {
			color = "#A855F7"
			title = "BOT"
		}
		updateSlotVisual(s, true, title, lbl, color, renderGenericDynamicSVG(title, lbl, color), "", nil)

	default:
		if target != nil {
			renderDynamicTargetSlot(s, online, target)
		} else if strings.HasPrefix(s.Action, dynPluginPrefix) || strings.HasPrefix(s.Action, dynBotPrefix) {
			updateSlotVisual(s, false, "OFFLINE", "CONNECT", "#3F3F46", renderOfflineSVG("TownCrier"), "", nil)
		}
	}
}

func renderDynamicTargetSlot(s *slotInstance, online bool, target *dynTarget) {
	if s == nil {
		return
	}
	ctxID := s.Context
	isEncoder := strings.EqualFold(s.Controller, "Encoder")
	if !online {
		label := target.DefaultTitle
		if label == "" {
			label = target.DisplayName
		}
		patch := map[string]any{
			"color":           "#3F3F46",
			"backgroundColor": "#18181B",
			"image":           renderOfflineSVG(label),
			"state":           0,
		}
		if isEncoder {
			patch["title"] = label
			patch["subtitle"] = "OFFLINE"
		} else {
			attachWatchFeedback(patch, label, "OFFLINE")
		}
		updateVisualPatch(ctxID, patch)
		return
	}

	if target.Kind == "bot" {
		patch := map[string]any{
			"color":           "#A855F7",
			"backgroundColor": "#0F172A",
			"image":           renderBotActionSVG(target.DefaultTitle, true),
		}
		if isEncoder {
			patch["title"] = target.DefaultTitle
			patch["subtitle"] = "BOT ACTION"
		} else {
			attachWatchFeedback(patch, target.DefaultTitle, "BOT")
		}
		updateVisualPatch(ctxID, patch)
		return
	}

	v := target.Visual
	title := target.DefaultTitle
	color := target.DefaultColor
	img := target.DefaultImage
	if v != nil {
		patch := map[string]any{
			"state": v.State,
		}
		if v.Title != "" {
			title = v.Title
		}
		if v.Color != "" {
			color = v.Color
		}
		if color != "" {
			patch["color"] = color
		}
		if v.BackgroundColor != "" {
			patch["backgroundColor"] = v.BackgroundColor
		} else {
			patch["backgroundColor"] = "#0F172A"
		}
		if v.Image != "" {
			img = v.Image
		}
		if img == "" {
			img = renderGenericDynamicSVG(target.ActionID, target.DefaultTitle, color)
		}
		patch["image"] = img
		omitText := !isEncoder && (target.HideTitle || strings.HasPrefix(img, "data:image/"))
		if !omitText {
			if title != "" {
				patch["title"] = title
			}
			if v.Subtitle != "" {
				patch["subtitle"] = v.Subtitle
			}
		} else {
			attachWatchFeedback(patch, title, v.Subtitle)
		}
		if isEncoder && (v.HasProgress || v.FeedbackValue != "") {
			fbVal := v.FeedbackValue
			if fbVal == "" {
				fbVal = title
			}
			fb, _ := patch["feedback"].(map[string]any)
			if fb == nil {
				fb = map[string]any{}
				patch["feedback"] = fb
			}
			fb["value"] = fbVal
			if v.HasProgress {
				fb["indicator"] = map[string]any{"value": v.ProgressPercent}
			}
		}
		updateVisualPatch(ctxID, patch)
		return
	}

	if img == "" {
		img = renderGenericDynamicSVG(target.ActionID, title, color)
	}
	patch := map[string]any{
		"color":           color,
		"backgroundColor": "#0F172A",
		"image":           img,
	}
	if isEncoder || !(target.HideTitle || strings.HasPrefix(img, "data:image/")) {
		patch["title"] = title
	} else {
		attachWatchFeedback(patch, title, "")
	}
	updateVisualPatch(ctxID, patch)
}

func strSetting(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	v, ok := m[key].(string)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func numSetting(m map[string]any, key string, fallback float64) float64 {
	if m == nil {
		return fallback
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return fallback
}

func builtinGuestActions() []map[string]any {
	return []map[string]any{
		{
			"uuid":         actMode,
			"name":         "TTS Output Mode",
			"category":     "TownCrier",
			"tooltip":      "Cycle or set TownCrier TTS mode",
			"defaultTitle": "TTS MODE",
			"defaultColor": "#38BDF8",
			"defaultImage": renderModeSVG("ALWAYS ON", "#38BDF8"),
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
			"settingsSchema": []map[string]any{
				{
					"key":     "targetMode",
					"label":   "Press Behavior",
					"type":    "select",
					"default": "cycle",
					"options": []map[string]string{
						{"label": "Cycle Mode", "value": "cycle"},
						{"label": "Always On", "value": "always_on"},
						{"label": "Auto-AFK", "value": "auto_afk"},
						{"label": "Always Off", "value": "always_off"},
					},
				},
			},
		},
		{
			"uuid":         actPanic,
			"name":         "Emergency Panic Mute",
			"category":     "TownCrier",
			"tooltip":      "Instantly mute or unmute TownCrier audio (<50ms)",
			"defaultTitle": "AUDIO",
			"defaultColor": "#22C55E",
			"defaultImage": renderPanicSVG(false),
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
			"settingsSchema": []map[string]any{
				{
					"key":     "operation",
					"label":   "Operation",
					"type":    "select",
					"default": "toggle",
					"options": []map[string]string{
						{"label": "Toggle Mute / Unmute", "value": "toggle"},
						{"label": "Force Mute", "value": "mute"},
						{"label": "Force Unmute", "value": "unmute"},
					},
				},
			},
		},
		{
			"uuid":         actQueue,
			"name":         "Speech Queue Control",
			"category":     "TownCrier",
			"tooltip":      "Pause, Resume, Skip, or Clear speech queue",
			"defaultTitle": "QUEUE",
			"defaultColor": "#38BDF8",
			"defaultImage": renderQueueSVG("Q:0 LIVE", "#38BDF8", false),
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
			"settingsSchema": []map[string]any{
				{
					"key":     "queueOperation",
					"label":   "Queue Operation",
					"type":    "select",
					"default": "toggle_pause",
					"options": []map[string]string{
						{"label": "Toggle Pause / Resume", "value": "toggle_pause"},
						{"label": "Skip Current", "value": "skip"},
						{"label": "Clear Queue", "value": "clear"},
					},
				},
			},
		},
		{
			"uuid":         actSpeak,
			"name":         "Custom TTS Announcement",
			"category":     "TownCrier",
			"tooltip":      "Speak a custom phrase through TownCrier TTS",
			"defaultTitle": "SPEAK",
			"defaultColor": "#38BDF8",
			"defaultImage": renderSpeakSVG("SPEAK"),
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
			"settingsSchema": []map[string]any{
				{"key": "label", "label": "Button Label", "type": "text", "default": "SPEAK"},
				{"key": "text", "label": "Phrase", "type": "text", "default": "Hello chat!"},
				{"key": "voiceId", "label": "Voice ID (optional)", "type": "text"},
				{"key": "speed", "label": "Speed (0.5 - 2.0)", "type": "number", "default": 1.0},
			},
		},
		{
			"uuid":         actVolume,
			"name":         "TTS Master Volume",
			"category":     "TownCrier",
			"tooltip":      "Adjust TownCrier master volume via keypad or dial",
			"defaultTitle": "VOLUME",
			"defaultColor": "#38BDF8",
			"defaultImage": renderVolumeSVG(80),
			"hideTitle":    true,
			"controllers":  []string{"Keypad", "Encoder"},
			"settingsSchema": []map[string]any{
				{"key": "step", "label": "Step % per Press/Tick", "type": "number", "default": 5},
			},
		},
		{
			"uuid":         actDynamic,
			"name":         "Run Bot / Plugin Action",
			"category":     "TownCrier",
			"tooltip":      "Trigger any TownCrier Bot Action or Plugin Action manually by ID",
			"defaultTitle": "PLUGIN",
			"defaultColor": "#34D399",
			"defaultImage": renderGenericDynamicSVG("PLUGIN", "CUSTOM", "#34D399"),
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
			"settingsSchema": []map[string]any{
				{
					"key":     "targetType",
					"label":   "Target Type",
					"type":    "select",
					"default": "plugin",
					"options": []map[string]string{
						{"label": "Plugin Action (e.g. CrowdDJ)", "value": "plugin"},
						{"label": "TownCrier Bot Action", "value": "bot"},
					},
				},
				{"key": "pluginUuid", "label": "Plugin UUID (for Plugin Action)", "type": "text", "default": "com.github.mrpoundsign.crowddj"},
				{"key": "actionId", "label": "Action ID (or Bot Action Name)", "type": "text", "default": "play_pause"},
				{"key": "label", "label": "Button Label", "type": "text", "default": "PLAY/PAUSE"},
				{"key": "argsJson", "label": "Optional JSON Args", "type": "text", "placeholder": `{"volume":"60"}`},
			},
		},
	}
}

//go:wasmexport pc_manifest
func pcManifest() uint64 {
	manifest := map[string]any{
		"uuid":        pluginUUID,
		"name":        "TownCrier & Plugins",
		"version":     strings.TrimPrefix(strings.TrimSpace(PluginVersion), "v"),
		"author":      "mrpoundsign",
		"description": "Control TownCrier neural TTS, Panic Mute, Bot Actions, and installed Plugins (such as CrowdDJ) as first-class buttons & dials.",
		"permissions": map[string]any{
			"allowedHosts": []string{"127.0.0.1:8765", "localhost:8765"},
		},
		"globalSettingsSchema": []map[string]any{
			{"key": "host", "label": "TownCrier Host", "type": "text", "section": "General", "default": "127.0.0.1"},
			{"key": "port", "label": "Control API Port", "type": "number", "section": "General", "default": 8765},
			{"key": "token", "label": "API Bearer Token", "type": "password", "section": "General", "placeholder": "Optional — only needed if Auth Token is set in TownCrier"},
			{"key": "refreshActions", "label": "Discover Live Bot & Plugin Actions", "type": "button", "section": "General", "default": map[string]any{"label": "Refresh Actions from TownCrier"}},
			{"key": "discoveredActions", "label": "Available Bot & Plugin Action IDs", "type": "info", "section": "General", "default": "TownCrier dynamically registers Bot & Plugin actions automatically when connected."},
		},
		"actions": builtinGuestActions(),
	}
	raw, _ := json.Marshal(manifest)
	return returnHostString(string(raw))
}

//go:wasmexport pc_on_start
func pcOnStart() int32 {
	gs := hydrateGlobalSettings(false)
	mu.Lock()
	armPollTimerLocked()
	mu.Unlock()
	pollAndSync(gs)
	return 0
}

//go:wasmexport pc_handle_event
func pcHandleEvent(ptr, length uint32) int32 {
	raw := readGuestInput(ptr, length)
	if len(raw) == 0 {
		return -1
	}
	var ev actionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return -2
	}

	gs := hydrateGlobalSettings(false)

	switch ev.Event {
	case "willAppear", "didReceiveSettings":
		mu.Lock()
		inst := &slotInstance{
			Context:    ev.Context,
			Action:     ev.Action,
			Controller: ev.Controller,
			Settings:   ev.Settings,
		}
		slots[ev.Context] = inst
		armPollTimerLocked()
		online := lastOnline
		st := lastStateRef
		var targetPtr *dynTarget
		if t, ok := dynTargets[ev.Action]; ok {
			tc := t
			targetPtr = &tc
		}
		mu.Unlock()
		renderSlot(inst, online, st, targetPtr)
		return 0

	case "willDisappear":
		mu.Lock()
		delete(slots, ev.Context)
		mu.Unlock()
		return 0

	case "keyDown", "dialPress":
		return handlePress(ev, gs)

	case "dialRotate":
		return handleDialRotate(ev, gs)
	}
	return 0
}

func handleDialRotate(ev actionEvent, gs globalSettings) int32 {
	if ev.Ticks == 0 {
		return 0
	}
	if ev.Action == actVolume {
		step := numSetting(ev.Settings, "step", 5)
		delta := (float64(ev.Ticks) * step) / 100.0
		resp, err := doHTTP("POST", "/api/volume", map[string]any{"delta": delta}, gs)
		if err == nil && resp.Status >= 200 && resp.Status < 300 {
			applyStatusAndRefresh(resp.Body, gs)
			return 0
		}
		return -3
	}

	mu.Lock()
	target, ok := dynTargets[ev.Action]
	mu.Unlock()
	if !ok || target.Kind != "plugin" {
		return 0
	}

	delta := float64(ev.Ticks) * 0.05
	resp, err := doHTTP("POST", "/api/plugins/run", map[string]any{
		"plugin_uuid": target.PluginUUID,
		"action_id":   target.ActionID,
		"args":        map[string]string{"delta": fmt.Sprintf("%.2f", delta)},
	}, gs)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		hostLog(fmt.Sprintf("Dial rotate %s failed: err=%v status=%d", ev.Action, err, resp.Status))
		return -3
	}
	pollAndSync(gs)
	return 0
}

func handlePress(ev actionEvent, gs globalSettings) int32 {
	mu.Lock()
	target, isDynTarget := dynTargets[ev.Action]
	mu.Unlock()
	if isDynTarget {
		var (
			path string
			body map[string]any
		)
		if target.Kind == "bot" {
			path = "/api/bot/run"
			body = map[string]any{"action_id": target.ActionID, "run_immediately": true}
		} else {
			var args map[string]string
			if ev.Event == "dialPress" {
				args = map[string]string{"mute_toggle": "true"}
			}
			path = "/api/plugins/run"
			body = map[string]any{
				"plugin_uuid": target.PluginUUID,
				"action_id":   target.ActionID,
				"args":        args,
			}
		}
		resp, err := doHTTP("POST", path, body, gs)
		if err != nil || resp.Status < 200 || resp.Status >= 300 {
			hostLog(fmt.Sprintf("Dynamic action %s failed: err=%v status=%d body=%s", ev.Action, err, resp.Status, resp.Body))
			updateVisualPatch(ev.Context, map[string]any{"flash": "alert"})
			return -6
		}
		if target.Kind == "bot" {
			updateVisualPatch(ev.Context, map[string]any{"flash": "ok"})
		}
		pollAndSync(gs)
		return 0
	}

	var (
		path string
		body map[string]any
	)
	switch ev.Action {
	case actMode:
		mode := strSetting(ev.Settings, "targetMode", "cycle")
		if mode == "cycle" {
			path = "/api/mode"
			body = map[string]any{"action": "cycle"}
		} else {
			path = "/api/mode"
			body = map[string]any{"mode": mode}
		}
	case actPanic:
		op := strSetting(ev.Settings, "operation", "toggle")
		path = "/api/panic"
		body = map[string]any{"action": op}
	case actQueue:
		op := strSetting(ev.Settings, "queueOperation", "toggle_pause")
		if op == "toggle_pause" {
			mu.Lock()
			paused := lastStateRef != nil && lastStateRef.State != nil && lastStateRef.State.Paused
			mu.Unlock()
			if paused {
				op = "resume"
			} else {
				op = "pause"
			}
		}
		path = "/api/queue"
		body = map[string]any{"action": op}
	case actSpeak:
		path = "/api/speak"
		body = map[string]any{
			"text":     strSetting(ev.Settings, "text", "Hello chat!"),
			"voice_id": strSetting(ev.Settings, "voiceId", ""),
			"speed":    numSetting(ev.Settings, "speed", 1.0),
		}
	case actVolume:
		step := numSetting(ev.Settings, "step", 5)
		path = "/api/volume"
		body = map[string]any{"delta": step / 100.0}
	case actDynamic:
		tType := strings.ToLower(strSetting(ev.Settings, "targetType", "plugin"))
		actionID := strSetting(ev.Settings, "actionId", "")
		if actionID == "" {
			updateSlotVisual(&slotInstance{Context: ev.Context, Action: ev.Action, Controller: ev.Controller}, true, "MISSING", "SET ID", "#EF4444", renderGenericDynamicSVG("MISSING", "SET ID", "#EF4444"), "alert", nil)
			return -4
		}
		var args map[string]string
		if rawArgs := strSetting(ev.Settings, "argsJson", ""); rawArgs != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
				updateSlotVisual(&slotInstance{Context: ev.Context, Action: ev.Action, Controller: ev.Controller}, true, "BAD JSON", "ARGS", "#EF4444", renderGenericDynamicSVG("BAD JSON", "ARGS", "#EF4444"), "alert", nil)
				return -5
			}
			args = make(map[string]string, len(parsed))
			for k, v := range parsed {
				args[k] = fmt.Sprint(v)
			}
		}
		if tType == "bot" {
			path = "/api/bot/run"
			body = map[string]any{"action_id": actionID, "args": args, "run_immediately": true}
		} else {
			path = "/api/plugins/run"
			body = map[string]any{
				"plugin_uuid": strSetting(ev.Settings, "pluginUuid", ""),
				"action_id":   actionID,
				"args":        args,
			}
		}
	default:
		return 0
	}

	resp, err := doHTTP("POST", path, body, gs)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		hostLog(fmt.Sprintf("Action %s failed: err=%v status=%d body=%s", ev.Action, err, resp.Status, resp.Body))
		updateVisualPatch(ev.Context, map[string]any{"flash": "alert"})
		return -6
	}
	if ev.Action == actDynamic {
		lbl := strSetting(ev.Settings, "label", "DONE")
		updateSlotVisual(&slotInstance{Context: ev.Context, Action: ev.Action, Controller: ev.Controller}, true, "OK", lbl, "#22C55E", renderGenericDynamicSVG("OK", lbl, "#22C55E"), "ok", nil)
		return 0
	}
	applyStatusAndRefresh(resp.Body, gs)
	return 0
}

func applyStatusAndRefresh(body string, gs globalSettings) {
	var sr statusResponse
	if json.Unmarshal([]byte(body), &sr) == nil && sr.State != nil {
		mu.Lock()
		lastOnline = true
		lastStateRef = &sr
		list := make([]*slotInstance, 0, len(slots))
		for _, s := range slots {
			list = append(list, s)
		}
		targetsCopy := cloneTargetsLocked()
		mu.Unlock()
		for _, s := range list {
			var tp *dynTarget
			if t, ok := targetsCopy[s.Action]; ok {
				tc := t
				tp = &tc
			}
			renderSlot(s, true, &sr, tp)
		}
		return
	}
	pollAndSync(gs)
}

func cloneTargetsLocked() map[string]dynTarget {
	out := make(map[string]dynTarget, len(dynTargets))
	for k, v := range dynTargets {
		out[k] = v
	}
	return out
}

func buildDynamicCatalogEntries(cat actionCatalog) ([]map[string]any, map[string]dynTarget, string) {
	allActions := builtinGuestActions()
	targets := make(map[string]dynTarget, len(cat.PluginActions)+len(cat.BotActions))
	var sigParts []string

	plugins := slices.Clone(cat.PluginActions)
	slices.SortFunc(plugins, func(a, b externalPluginAction) int {
		if c := cmp.Compare(strings.ToLower(a.PluginName), strings.ToLower(b.PluginName)); c != 0 {
			return c
		}
		return cmp.Compare(a.PluginUUID+"."+a.ActionID, b.PluginUUID+"."+b.ActionID)
	})

	for _, pa := range plugins {
		if strings.TrimSpace(pa.PluginUUID) == "" || strings.TrimSpace(pa.ActionID) == "" {
			continue
		}
		uuid := dynPluginPrefix + pa.PluginUUID + "." + pa.ActionID
		pName := strings.TrimSpace(pa.PluginName)
		if pName == "" {
			pName = pa.PluginUUID
		}
		aName := strings.TrimSpace(pa.ActionName)
		if aName == "" {
			aName = pa.ActionID
		}
		fullName := pName + ": " + aName

		ctrls := pa.Controllers
		if len(ctrls) == 0 {
			ctrls = []string{"Keypad"}
		}
		defTitle := strings.TrimSpace(pa.DefaultTitle)
		if defTitle == "" {
			defTitle = aName
		}
		defColor := strings.TrimSpace(pa.DefaultColor)
		if defColor == "" {
			defColor = "#34D399"
		}
		defImg := strings.TrimSpace(pa.DefaultImage)
		if defImg == "" {
			defImg = renderGenericDynamicSVG(pa.ActionID, defTitle, defColor)
		}
		hideTitle := pa.HideTitle || strings.HasPrefix(defImg, "data:image/")
		tooltip := strings.TrimSpace(pa.Description)
		if tooltip == "" {
			tooltip = fullName
		}

		allActions = append(allActions, map[string]any{
			"uuid":         uuid,
			"name":         fullName,
			"category":     "TownCrier",
			"tooltip":      tooltip,
			"defaultTitle": defTitle,
			"defaultColor": defColor,
			"defaultImage": defImg,
			"hideTitle":    hideTitle,
			"controllers":  ctrls,
		})
		targets[uuid] = dynTarget{
			UUID:         uuid,
			Kind:         "plugin",
			PluginUUID:   pa.PluginUUID,
			ActionID:     pa.ActionID,
			DisplayName:  fullName,
			DefaultTitle: defTitle,
			DefaultColor: defColor,
			DefaultImage: defImg,
			HideTitle:    hideTitle,
			Controllers:  ctrls,
			Visual:       pa.Visual,
		}
		sigParts = append(sigParts, fmt.Sprintf("%s|%s|%s|%v", uuid, fullName, strings.Join(ctrls, ","), hideTitle))
	}

	bots := slices.Clone(cat.BotActions)
	slices.SortFunc(bots, func(a, b externalBotAction) int {
		if c := cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})

	for _, ba := range bots {
		if !ba.Enabled || strings.TrimSpace(ba.ID) == "" {
			continue
		}
		uuid := dynBotPrefix + ba.ID
		bName := strings.TrimSpace(ba.Name)
		if bName == "" {
			bName = ba.ID
		}
		fullName := "Bot: " + bName
		defImg := renderBotActionSVG(bName, true)
		allActions = append(allActions, map[string]any{
			"uuid":         uuid,
			"name":         fullName,
			"category":     "TownCrier",
			"tooltip":      "Trigger TownCrier Bot Action: " + bName,
			"defaultTitle": bName,
			"defaultColor": "#A855F7",
			"defaultImage": defImg,
			"hideTitle":    true,
			"controllers":  []string{"Keypad"},
		})
		targets[uuid] = dynTarget{
			UUID:         uuid,
			Kind:         "bot",
			ActionID:     ba.ID,
			DisplayName:  fullName,
			DefaultTitle: bName,
			DefaultColor: "#A855F7",
			DefaultImage: defImg,
			HideTitle:    true,
			Controllers:  []string{"Keypad"},
		}
		sigParts = append(sigParts, fmt.Sprintf("%s|%s", uuid, fullName))
	}

	return allActions, targets, strings.Join(sigParts, ";")
}

func pollAndSync(gs globalSettings) (actionCatalog, bool) {
	pollMu.Lock()
	defer pollMu.Unlock()

	var (
		online = false
		st     *statusResponse
		cat    actionCatalog
		hasCat bool
	)

	if sResp, err := doHTTP("GET", "/api/status", nil, gs); err == nil && sResp.Status >= 200 && sResp.Status < 300 {
		var parsed statusResponse
		if json.Unmarshal([]byte(sResp.Body), &parsed) == nil && parsed.State != nil {
			st = &parsed
			online = true
		}
	}

	if aResp, err := doHTTP("GET", "/api/actions", nil, gs); err == nil && aResp.Status >= 200 && aResp.Status < 300 {
		if json.Unmarshal([]byte(aResp.Body), &cat) == nil {
			hasCat = true
			online = true
		}
	}

	var registerPayload []byte
	if hasCat {
		allActions, newTargets, sig := buildDynamicCatalogEntries(cat)
		mu.Lock()
		changed := sig != lastActionSig
		if changed {
			lastActionSig = sig
		}
		dynTargets = newTargets
		mu.Unlock()
		if changed {
			registerPayload, _ = json.Marshal(allActions)
		}
	}

	if len(registerPayload) > 0 {
		rp, rl := bytesPtrLen(registerPayload)
		if rc := pcRegisterActions(rp, rl); rc != 0 {
			hostLog(fmt.Sprintf("pc_register_actions returned %d", rc))
		}
	}

	mu.Lock()
	lastOnline = online
	if st != nil {
		lastStateRef = st
	}
	curState := lastStateRef
	list := make([]*slotInstance, 0, len(slots))
	for _, s := range slots {
		list = append(list, s)
	}
	targetsCopy := cloneTargetsLocked()
	mu.Unlock()

	for _, s := range list {
		var tp *dynTarget
		if t, ok := targetsCopy[s.Action]; ok {
			tc := t
			tp = &tc
		}
		renderSlot(s, online, curState, tp)
	}

	return cat, online
}

//go:wasmexport pc_on_timer
func pcOnTimer(_, _ uint32) int32 {
	gs := hydrateGlobalSettings(false)
	pollAndSync(gs)
	return 0
}

//go:wasmexport pc_on_global_settings
func pcOnGlobalSettings(ptr, length uint32) int32 {
	raw := readGuestInput(ptr, length)
	if len(raw) == 0 {
		return 0
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		mu.Lock()
		gSettings = parseGlobalMap(m)
		gHydrated = true
		mu.Unlock()
		pollAndSync(gSettings)
	}
	return 0
}

//go:wasmexport pc_on_config_action
func pcOnConfigAction(ptr, length uint32) int32 {
	raw := readGuestInput(ptr, length)
	if len(raw) == 0 {
		return -1
	}
	var ev configActionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return -2
	}
	if ev.Action != "refreshActions" {
		return 0
	}
	gs := parseGlobalMap(ev.Settings)
	cat, online := pollAndSync(gs)
	if !online {
		msg := fmt.Sprintf("Failed querying TownCrier at %s:%d", gs.Host, gs.Port)
		saveInfoText(msg)
		return 1
	}
	var lines []string
	for _, pa := range cat.PluginActions {
		lines = append(lines, fmt.Sprintf("[Plugin: %s] %s (%s)", pa.PluginName, pa.ActionName, pa.ActionID))
	}
	for _, ba := range cat.BotActions {
		if ba.Enabled {
			lines = append(lines, fmt.Sprintf("[Bot] %s (%s)", ba.Name, ba.ID))
		}
	}
	if len(lines) == 0 {
		saveInfoText("Connected cleanly to TownCrier! No Bot or Plugin actions are currently active.")
	} else {
		saveInfoText("Registered into TownCrier category:\n" + strings.Join(lines, "\n"))
	}
	return 0
}

func saveInfoText(text string) {
	payload, _ := json.Marshal(map[string]any{
		"discoveredActions": text,
	})
	p, l := bytesPtrLen(payload)
	pcSaveGlobalSettings(p, l)
}

func main() {
	// WASI reactor initializes once; keep imports linked cleanly.
	_ = url.QueryEscape("")
}
