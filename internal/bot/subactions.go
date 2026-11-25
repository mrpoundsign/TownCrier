package bot

import (
	"context"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const maxActionDepth = 8

type execState struct {
	args     map[string]string
	platform string
	userID   string
	channel  string
	replyID  string
	depth    int
	broken   bool
}

func (e *Engine) executeAction(ctx context.Context, cfg config.BotAutomationConfig, action config.BotActionDef, st *execState) {
	if !action.Enabled || ctx.Err() != nil || st.depth >= maxActionDepth {
		return
	}
	for _, sa := range action.SubActions {
		if ctx.Err() != nil || st.broken {
			return
		}
		if !sa.Enabled {
			continue
		}
		e.executeSubAction(ctx, cfg, sa, st)
	}
}

func (e *Engine) templateCtx(st *execState) TemplateContext {
	return TemplateContext{
		Args:     st.args,
		Store:    e.store,
		Platform: st.platform,
		UserID:   st.userID,
	}
}

func (e *Engine) interpolate(raw string, st *execState) string {
	return Interpolate(raw, e.templateCtx(st))
}

func (e *Engine) executeSubAction(ctx context.Context, cfg config.BotAutomationConfig, sa config.BotSubActionDef, st *execState) {
	switch strings.ToLower(strings.TrimSpace(sa.Kind)) {
	case "speak_tts":
		text := strings.TrimSpace(e.interpolate(sa.Text, st))
		if text != "" && e.bridge.SpeakTTS != nil {
			user := st.args["displayName"]
			if user == "" {
				user = st.args["user"]
			}
			_ = e.bridge.SpeakTTS(text, sa.VoiceID, sa.Speed, sa.Priority, user)
		}

	case "play_sound":
		soundPath := strings.TrimSpace(e.interpolate(sa.SoundPath, st))
		if soundPath != "" && e.bridge.PlaySound != nil {
			_ = e.bridge.PlaySound(soundPath)
		}

	case "send_chat":
		if e.bridge.SendChat == nil {
			return
		}
		plat := strings.TrimSpace(sa.Platform)
		if plat == "" {
			plat = st.platform
		}
		ch := strings.TrimSpace(sa.Channel)
		if ch == "" {
			ch = st.channel
		}
		text := strings.TrimSpace(e.interpolate(sa.Text, st))
		if text != "" && plat != "" {
			_ = e.bridge.SendChat(ctx, plat, ch, text, "", sa.AsAnnouncement, sa.SendAs)
		}

	case "moderate":
		if e.bridge.Moderate == nil {
			return
		}
		plat := strings.TrimSpace(sa.Platform)
		if plat == "" {
			plat = st.platform
		}
		target := strings.TrimSpace(e.interpolate(sa.Target, st))
		reason := strings.TrimSpace(e.interpolate(sa.Reason, st))
		msgID := st.replyID
		if strings.EqualFold(sa.Operation, "delete") && target != "" {
			msgID = target
		}
		_ = e.bridge.Moderate(ctx, plat, sa.Operation, st.userID, target, msgID, sa.DurationSeconds, reason)

	case "set_var":
		if e.store == nil {
			return
		}
		key := strings.TrimSpace(e.interpolate(sa.Key, st))
		val := e.interpolate(sa.Value, st)
		scope := strings.ToLower(strings.TrimSpace(sa.Scope))
		switch scope {
		case "user", "user_persistent":
			if sa.Delete {
				_ = e.store.DeleteUserVar(st.platform, st.userID, key)
			} else {
				_ = e.store.SetUserVar(st.platform, st.userID, key, sa.VarKind, val)
			}
		case "global_ephemeral", "ephemeral":
			if sa.Delete {
				_ = e.store.DeleteGlobalVar(key, true)
			} else {
				_ = e.store.SetGlobalVar(key, sa.VarKind, val, true)
			}
		default:
			if sa.Delete {
				_ = e.store.DeleteGlobalVar(key, false)
			} else {
				_ = e.store.SetGlobalVar(key, sa.VarKind, val, false)
			}
		}

	case "inc_var":
		if e.store == nil {
			return
		}
		key := strings.TrimSpace(e.interpolate(sa.Key, st))
		delta := sa.Delta
		if delta == 0 {
			delta = 1
		}
		scope := strings.ToLower(strings.TrimSpace(sa.Scope))
		switch scope {
		case "user", "user_persistent":
			if newVal, err := e.store.IncrementUserVar(st.platform, st.userID, key, delta); err == nil {
				st.args[key] = strconv.FormatFloat(newVal, 'f', -1, 64)
			}
		case "global_ephemeral", "ephemeral":
			if newVal, err := e.store.IncrementGlobalVar(key, delta, true); err == nil {
				st.args[key] = strconv.FormatFloat(newVal, 'f', -1, 64)
			}
		default:
			if newVal, err := e.store.IncrementGlobalVar(key, delta, false); err == nil {
				st.args[key] = strconv.FormatFloat(newVal, 'f', -1, 64)
			}
		}

	case "if_else":
		lhs := e.interpolate(sa.LHS, st)
		rhs := e.interpolate(sa.RHS, st)
		branch := sa.ElseSubActions
		if evalCondition(lhs, sa.Operator, rhs) {
			branch = sa.ThenSubActions
		}
		for _, child := range branch {
			if ctx.Err() != nil || st.broken {
				return
			}
			if child.Enabled {
				e.executeSubAction(ctx, cfg, child, st)
			}
		}

	case "break":
		st.broken = true

	case "delay":
		if sa.DelayMs > 0 {
			timer := time.NewTimer(time.Duration(sa.DelayMs) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}

	case "http_request":
		e.execHTTPSubAction(ctx, sa, st)

	case "run_action":
		targetID := strings.TrimSpace(sa.TargetActionID)
		if targetID == "" {
			return
		}
		for _, candidate := range cfg.Actions {
			if candidate.ID == targetID || strings.EqualFold(candidate.Name, targetID) {
				childState := &execState{
					args:     maps.Clone(st.args),
					platform: st.platform,
					userID:   st.userID,
					channel:  st.channel,
					replyID:  st.replyID,
					depth:    st.depth + 1,
				}
				e.executeAction(ctx, cfg, candidate, childState)
				maps.Copy(st.args, childState.args)
				break
			}
		}

	case "overlay_broadcast":
		if e.bridge.OverlayBroadcast != nil {
			payload := e.interpolate(sa.Payload, st)
			e.bridge.OverlayBroadcast(sa.PluginUUID, sa.EventName, payload)
		}

	case "obs":
		e.execOBSSubAction(ctx, sa, st)

	case "plugin_action":
		if e.bridge.RunPluginAction == nil || strings.TrimSpace(sa.PluginUUID) == "" || strings.TrimSpace(sa.PluginActionID) == "" {
			return
		}
		merged := make(map[string]any, len(st.args)+len(sa.Params))
		for k, v := range st.args {
			merged[k] = v
		}
		for k, v := range sa.Params {
			merged[k] = e.interpolate(v, st)
		}
		pbArgs := &pluginv1.ActionArgs{Args: make(map[string]*pluginv1.ActionValue, len(merged))}
		for k, v := range merged {
			if sv, ok := v.(string); ok {
				pbArgs.Args[k] = &pluginv1.ActionValue{Kind: &pluginv1.ActionValue_StringVal{StringVal: sv}}
			}
		}
		cont, updated, err := e.bridge.RunPluginAction(ctx, sa.PluginUUID, sa.PluginActionID, pbArgs)
		if err == nil && updated != nil {
			for k, v := range updated.GetArgs() {
				if v != nil {
					switch tv := v.Kind.(type) {
					case *pluginv1.ActionValue_StringVal:
						st.args[k] = tv.StringVal
					case *pluginv1.ActionValue_IntVal:
						st.args[k] = strconv.FormatInt(tv.IntVal, 10)
					case *pluginv1.ActionValue_DoubleVal:
						st.args[k] = strconv.FormatFloat(tv.DoubleVal, 'f', -1, 64)
					case *pluginv1.ActionValue_BoolVal:
						st.args[k] = strconv.FormatBool(tv.BoolVal)
					}
				}
			}
		}
		if err == nil && !cont {
			st.broken = true
		}
	}
}

func (e *Engine) execHTTPSubAction(ctx context.Context, sa config.BotSubActionDef, st *execState) {
	rawURL := strings.TrimSpace(e.interpolate(sa.URL, st))
	if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return
	}
	method := strings.ToUpper(strings.TrimSpace(sa.Method))
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if sa.Body != "" {
		bodyReader = strings.NewReader(e.interpolate(sa.Body, st))
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, bodyReader)
	if err != nil {
		return
	}
	for hk, hv := range sa.Headers {
		req.Header.Set(hk, e.interpolate(hv, st))
	}
	client := e.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	respText := strings.TrimSpace(string(raw))
	st.args["httpStatus"] = strconv.Itoa(resp.StatusCode)
	st.args["httpResponse"] = respText
	if key := strings.TrimSpace(sa.SaveVar); key != "" {
		st.args[key] = respText
		if e.store != nil {
			_ = e.store.SetGlobalVar(key, "string", respText, true)
		}
	}
}

func (e *Engine) execOBSSubAction(ctx context.Context, sa config.BotSubActionDef, st *execState) {
	if e.obs == nil || !e.obs.IsConnected() {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	scene := strings.TrimSpace(e.interpolate(sa.OBSScene, st))
	source := strings.TrimSpace(e.interpolate(sa.OBSSource, st))
	input := strings.TrimSpace(e.interpolate(sa.OBSInput, st))

	switch strings.ToLower(strings.TrimSpace(sa.OBSOperation)) {
	case "set_scene":
		if scene != "" {
			_ = e.obs.SetCurrentProgramScene(opCtx, scene)
		}
	case "set_source_visible":
		if scene != "" && source != "" {
			_ = e.obs.SetSceneItemEnabled(opCtx, scene, source, sa.OBSVisible)
		}
	case "set_mute":
		if input != "" {
			_ = e.obs.SetInputMute(opCtx, input, sa.OBSMuted)
		}
	case "toggle_record":
		_ = e.obs.ToggleRecord(opCtx)
	case "toggle_stream":
		_ = e.obs.ToggleStream(opCtx)
	case "save_replay":
		_ = e.obs.SaveReplayBuffer(opCtx)
	case "refresh_browser":
		if input != "" {
			_ = e.obs.RefreshBrowserSource(opCtx, input)
		}
	}
}

func evalCondition(lhs, op, rhs string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "neq", "!=":
		return lhs != rhs
	case "contains":
		return strings.Contains(strings.ToLower(lhs), strings.ToLower(rhs))
	case "empty":
		return strings.TrimSpace(lhs) == ""
	case "not_empty":
		return strings.TrimSpace(lhs) != ""
	case "gt", ">", "gte", ">=", "lt", "<", "lte", "<=":
		lf, err1 := strconv.ParseFloat(strings.TrimSpace(lhs), 64)
		rf, err2 := strconv.ParseFloat(strings.TrimSpace(rhs), 64)
		if err1 != nil || err2 != nil {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(op)) {
		case "gt", ">":
			return lf > rf
		case "gte", ">=":
			return lf >= rf
		case "lt", "<":
			return lf < rf
		case "lte", "<=":
			return lf <= rf
		}
	default: // "eq", "=="
		return strings.EqualFold(lhs, rhs)
	}
	return false
}
