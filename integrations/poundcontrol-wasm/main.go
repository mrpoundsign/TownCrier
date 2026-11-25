//go:build wasip1 && wasm

// Command poundcontrol-wasm builds a native PoundControl WebAssembly (.wasm) plugin
// adhering to PoundControl's WASM Plugin ABI (pc_alloc, pc_free, pc_manifest,
// pc_handle_event, pc_on_timer, pc_on_global_settings, pc_on_config_action).
//
// Build with:
//   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/bin/towncrier.wasm ./integrations/poundcontrol-wasm
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
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

const (
	pluginUUID = "com.github.mrpoundsign.towncrier.wasm"

	actMode    = "com.github.mrpoundsign.towncrier.wasm.mode"
	actPanic   = "com.github.mrpoundsign.towncrier.wasm.panic"
	actQueue   = "com.github.mrpoundsign.towncrier.wasm.queue"
	actSpeak   = "com.github.mrpoundsign.towncrier.wasm.speak"
	actVolume  = "com.github.mrpoundsign.towncrier.wasm.volume"
	actDynamic = "com.github.mrpoundsign.towncrier.wasm.action"
)

type slotInstance struct {
	Context  string
	Action   string
	Settings map[string]any
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

type actionCatalog struct {
	BotActions []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	} `json:"bot_actions"`
	PluginActions []struct {
		PluginUUID  string `json:"plugin_uuid"`
		PluginName  string `json:"plugin_name"`
		ActionID    string `json:"action_id"`
		ActionName  string `json:"action_name"`
		Description string `json:"description"`
	} `json:"plugin_actions"`
}

var (
	allocMu     sync.Mutex
	allocations = make(map[uint32][]byte)

	mu           sync.Mutex
	slots        = make(map[string]*slotInstance)
	gSettings    globalSettings
	gHydrated    bool
	timerArmed   bool
	lastStateRef = &statusResponse{}
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

func syncStatusTimerLocked() {
	shouldRun := len(slots) > 0
	id := []byte("poll-state")
	ip, il := bytesPtrLen(id)
	if shouldRun && !timerArmed {
		timerArmed = true
		pcSetTimer(ip, il, 0, 0, 2000)
	} else if !shouldRun && timerArmed {
		timerArmed = false
		pcSetTimer(ip, il, 0, 0, 0)
	}
}

func updateVisual(ctxID, title, subtitle, color, flash string, pct *int) {
	patch := map[string]any{
		"title":    title,
		"subtitle": subtitle,
		"color":    color,
	}
	if flash != "" {
		patch["flash"] = flash
	}
	if pct != nil {
		patch["feedback"] = map[string]any{
			"value": subtitle,
			"indicator": map[string]any{
				"value": *pct,
			},
		}
	}
	pBytes, _ := json.Marshal(patch)
	cBytes := []byte(ctxID)
	cp, cl := bytesPtrLen(cBytes)
	pp, pl := bytesPtrLen(pBytes)
	pcUpdateVisual(cp, cl, pp, pl)
}

func renderSlot(s *slotInstance, st *statusResponse) {
	if s == nil {
		return
	}
	switch s.Action {
	case actMode:
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
		updateVisual(s.Context, "TTS MODE", mode, color, "", nil)

	case actPanic:
		panicked := st != nil && st.State != nil && st.State.Panicked
		if panicked {
			updateVisual(s.Context, "PANIC", "MUTED", "#EF4444", "", nil)
		} else {
			updateVisual(s.Context, "AUDIO", "SAFE", "#22C55E", "", nil)
		}

	case actQueue:
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
		updateVisual(s.Context, "QUEUE", sub, color, "", nil)

	case actSpeak:
		lbl := strSetting(s.Settings, "label", "SPEAK")
		updateVisual(s.Context, "TOWNCRIER", lbl, "#38BDF8", "", nil)

	case actVolume:
		pct := 80
		if st != nil && st.State != nil {
			pct = int(st.State.Volume*100 + 0.5)
		}
		updateVisual(s.Context, "VOLUME", fmt.Sprintf("%d%%", pct), "#38BDF8", "", &pct)

	case actDynamic:
		tType := strings.ToUpper(strSetting(s.Settings, "targetType", "plugin"))
		lbl := strSetting(s.Settings, "label", strSetting(s.Settings, "actionId", "ACTION"))
		color := "#34D399"
		title := "PLUGIN"
		if tType == "BOT" {
			color = "#A855F7"
			title = "BOT"
		}
		updateVisual(s.Context, title, lbl, color, "", nil)
	}
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

//go:wasmexport pc_manifest
func pcManifest() uint64 {
	manifest := map[string]any{
		"uuid":        pluginUUID,
		"name":        "TownCrier & Plugins",
		"version":     "1.0.0",
		"author":      "mrpoundsign",
		"description": "Control TownCrier neural TTS, Panic Mute, Bot Actions, and installed Plugins (including CrowdDJ) over WASM HTTP.",
		"permissions": map[string]any{
			"allowedHosts": []string{"127.0.0.1:8765", "localhost:8765"},
		},
		"globalSettingsSchema": []map[string]any{
			{
				"key":     "host",
				"label":   "TownCrier Host",
				"type":    "text",
				"section": "General",
				"default": "127.0.0.1",
			},
			{
				"key":     "port",
				"label":   "Control API Port",
				"type":    "number",
				"section": "General",
				"default": 8765,
			},
			{
				"key":         "token",
				"label":       "API Bearer Token",
				"type":        "password",
				"section":     "General",
				"placeholder": "Optional — only needed if Auth Token is set in TownCrier",
			},
			{
				"key":     "refreshActions",
				"label":   "Discover Live Bot & Plugin Actions",
				"type":    "button",
				"section": "General",
				"default": map[string]any{"label": "Refresh Actions from TownCrier"},
			},
			{
				"key":     "discoveredActions",
				"label":   "Available Bot & Plugin Action IDs",
				"type":    "info",
				"section": "General",
				"default": "Click 'Refresh Actions from TownCrier' to list live CrowdDJ & Bot Action IDs.",
			},
		},
		"actions": []map[string]any{
			{
				"uuid":         actDynamic,
				"name":         "Run Bot / Plugin Action",
				"category":     "TownCrier",
				"tooltip":      "Trigger any TownCrier Bot Action or Plugin Action (e.g. CrowdDJ play_pause, skip, stop, toggle_requests)",
				"defaultTitle": "PLUGIN",
				"defaultColor": "#34D399",
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
					{
						"key":         "pluginUuid",
						"label":       "Plugin UUID (for Plugin Action)",
						"type":        "text",
						"default":     "com.github.mrpoundsign.crowddj",
						"placeholder": "com.github.mrpoundsign.crowddj",
					},
					{
						"key":         "actionId",
						"label":       "Action ID (or Bot Action Name)",
						"type":        "text",
						"default":     "play_pause",
						"placeholder": "play_pause, skip, stop, toggle_requests, save_backup...",
					},
					{
						"key":         "label",
						"label":       "Button Label",
						"type":        "text",
						"default":     "PLAY/PAUSE",
					},
					{
						"key":         "argsJson",
						"label":       "Optional JSON Args",
						"type":        "text",
						"placeholder": `{"volume":"60"}`,
					},
				},
			},
			{
				"uuid":         actMode,
				"name":         "TTS Output Mode",
				"category":     "TownCrier",
				"tooltip":      "Cycle or set TownCrier TTS mode",
				"defaultTitle": "TTS MODE",
				"defaultColor": "#38BDF8",
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
				"controllers":  []string{"Keypad", "Encoder"},
				"settingsSchema": []map[string]any{
					{"key": "step", "label": "Step % per Press/Tick", "type": "number", "default": 5},
				},
			},
		},
	}
	raw, _ := json.Marshal(manifest)
	return returnHostString(string(raw))
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
		inst := &slotInstance{Context: ev.Context, Action: ev.Action, Settings: ev.Settings}
		slots[ev.Context] = inst
		st := lastStateRef
		syncStatusTimerLocked()
		mu.Unlock()
		renderSlot(inst, st)
		return 0

	case "willDisappear":
		mu.Lock()
		delete(slots, ev.Context)
		syncStatusTimerLocked()
		mu.Unlock()
		return 0

	case "keyDown", "dialPress":
		return handlePress(ev, gs)

	case "dialRotate":
		if ev.Action == actVolume && ev.Ticks != 0 {
			step := numSetting(ev.Settings, "step", 5)
			delta := (float64(ev.Ticks) * step) / 100.0
			resp, err := doHTTP("POST", "/api/volume", map[string]any{"delta": delta}, gs)
			if err == nil && resp.Status >= 200 && resp.Status < 300 {
				applyStateResponse(resp.Body)
				return 0
			}
			return -3
		}
	}
	return 0
}

func handlePress(ev actionEvent, gs globalSettings) int32 {
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
			updateVisual(ev.Context, "MISSING", "SET ID", "#EF4444", "alert", nil)
			return -4
		}
		var args map[string]string
		if rawArgs := strSetting(ev.Settings, "argsJson", ""); rawArgs != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
				updateVisual(ev.Context, "BAD JSON", "ARGS", "#EF4444", "alert", nil)
				return -5
			}
			args = make(map[string]string, len(parsed))
			for k, v := range parsed {
				args[k] = fmt.Sprint(v)
			}
		}
		if tType == "bot" {
			path = "/api/bot/run"
			body = map[string]any{"action_id": actionID, "args": args}
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
		updateVisual(ev.Context, "ERROR", fmt.Sprintf("HTTP %d", resp.Status), "#EF4444", "alert", nil)
		return -6
	}
	if ev.Action == actDynamic {
		mu.Lock()
		inst := slots[ev.Context]
		mu.Unlock()
		if inst != nil {
			renderSlot(inst, nil)
		}
		updateVisual(ev.Context, "OK", strSetting(ev.Settings, "label", "DONE"), "#22C55E", "ok", nil)
		return 0
	}
	applyStateResponse(resp.Body)
	return 0
}

func applyStateResponse(body string) {
	var sr statusResponse
	if json.Unmarshal([]byte(body), &sr) != nil || sr.State == nil {
		return
	}
	mu.Lock()
	lastStateRef = &sr
	list := make([]*slotInstance, 0, len(slots))
	for _, s := range slots {
		list = append(list, s)
	}
	mu.Unlock()
	for _, s := range list {
		renderSlot(s, &sr)
	}
}

//go:wasmexport pc_on_timer
func pcOnTimer(_, _ uint32) int32 {
	gs := hydrateGlobalSettings(false)
	resp, err := doHTTP("GET", "/api/status", nil, gs)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		return 0
	}
	applyStateResponse(resp.Body)
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
	resp, err := doHTTP("GET", "/api/actions", nil, gs)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		msg := fmt.Sprintf("Failed querying TownCrier /api/actions at %s:%d (HTTP %d, err=%v)", gs.Host, gs.Port, resp.Status, err)
		saveInfoText(msg)
		return 1
	}
	var cat actionCatalog
	if err := json.Unmarshal([]byte(resp.Body), &cat); err != nil {
		saveInfoText("Failed decoding TownCrier action catalog: " + err.Error())
		return 1
	}
	var lines []string
	for _, pa := range cat.PluginActions {
		lines = append(lines, fmt.Sprintf("[Plugin: %s] pluginUuid=%s actionId=%s (%s)", pa.PluginName, pa.PluginUUID, pa.ActionID, pa.ActionName))
	}
	for _, ba := range cat.BotActions {
		lines = append(lines, fmt.Sprintf("[Bot] actionId=%s (%s)", ba.ID, ba.Name))
	}
	if len(lines) == 0 {
		saveInfoText("Connected cleanly to TownCrier! No Bot or Plugin actions are currently active.")
	} else {
		saveInfoText(strings.Join(lines, "\n"))
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
	// WASI reactor initializes once; keep URL package reference minimal.
	_ = url.QueryEscape("")
}
