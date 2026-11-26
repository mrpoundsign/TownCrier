package bot

import (
	"context"
	"encoding/json"
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
	args           map[string]string
	actionVars     map[string]string
	actionVarKinds map[string]string
	platform       string
	userID         string
	channel        string
	replyID        string
	depth          int
	broken         bool
}

func (e *Engine) executeAction(ctx context.Context, cfg config.BotAutomationConfig, action config.BotActionDef, st *execState) {
	if !action.Enabled || ctx.Err() != nil || st.depth >= maxActionDepth {
		return
	}
	if st.actionVars == nil {
		st.actionVars = make(map[string]string, len(action.Variables))
		st.actionVarKinds = make(map[string]string, len(action.Variables))
		for _, v := range action.Variables {
			name := strings.TrimSpace(v.Name)
			if name == "" {
				continue
			}
			st.actionVars[name] = v.DefaultValue
			st.actionVarKinds[name] = v.Kind
		}
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
		Args:       st.args,
		ActionVars: st.actionVars,
		Store:      e.store,
		Platform:   st.platform,
		UserID:     st.userID,
		LookupOBS: func(key string) (string, bool) {
			switch key {
			case "connected":
				if e.obs != nil && e.obs.IsConnected() {
					return "true", true
				}
				return "false", true
			case "scene":
				if e.obs == nil {
					return "", true
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				return e.obs.ResolveCurrentProgramScene(ctx), true
			case "previous_scene":
				if e.obs == nil {
					return "", true
				}
				return e.obs.PreviousProgramScene(), true
			case "preview_scene":
				if e.obs == nil {
					return "", true
				}
				return e.obs.CurrentPreviewScene(), true
			default:
				return "", false
			}
		},
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
			voiceID := strings.TrimSpace(sa.VoiceID)
			speed := sa.Speed
			if voiceID == "" && st.args["voicePhrase"] != "" && e.bridge.GetConfig != nil {
				if fullCfg := e.bridge.GetConfig(); fullCfg != nil {
					if respVoice := strings.TrimSpace(fullCfg.VoiceDetection.ResponseVoiceID); respVoice != "" {
						voiceID = respVoice
						if speed <= 0 && fullCfg.VoiceDetection.ResponseSpeed > 0 {
							speed = fullCfg.VoiceDetection.ResponseSpeed
						}
					} else if speed <= 0 && fullCfg.VoiceDetection.ResponseSpeed > 0 {
						speed = fullCfg.VoiceDetection.ResponseSpeed
					}
				}
			}
			_ = e.bridge.SpeakTTS(text, voiceID, speed, sa.Priority, user)
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
		key := strings.TrimSpace(e.interpolate(sa.Key, st))
		val := e.interpolate(sa.Value, st)
		scope := strings.ToLower(strings.TrimSpace(sa.Scope))
		switch scope {
		case "action", "local", "per_action":
			if canon, ok := resolveActionVarKey(st.actionVars, key); ok {
				if sa.Delete {
					st.actionVars[canon] = ""
				} else {
					st.actionVars[canon] = val
				}
			}
		case "user", "user_persistent":
			if e.store == nil {
				return
			}
			if sa.Delete {
				_ = e.store.DeleteUserVar(st.platform, st.userID, key)
			} else {
				_ = e.store.SetUserVar(st.platform, st.userID, key, sa.VarKind, val)
			}
		case "session", "global_ephemeral", "ephemeral":
			if e.store == nil {
				return
			}
			if sa.Delete {
				_ = e.store.DeleteGlobalVar(key, true)
			} else {
				kind := sa.VarKind
				if strings.TrimSpace(kind) == "" {
					kind = declaredVarKind(cfg.Variables, "session", key)
				}
				_ = e.store.SetGlobalVar(key, kind, val, true)
			}
		default:
			if e.store == nil {
				return
			}
			if sa.Delete {
				_ = e.store.DeleteGlobalVar(key, false)
			} else {
				kind := sa.VarKind
				if strings.TrimSpace(kind) == "" {
					kind = declaredVarKind(cfg.Variables, "global", key)
				}
				_ = e.store.SetGlobalVar(key, kind, val, false)
			}
		}

	case "inc_var":
		key := strings.TrimSpace(e.interpolate(sa.Key, st))
		delta := sa.Delta
		if delta == 0 {
			delta = 1
		}
		scope := strings.ToLower(strings.TrimSpace(sa.Scope))
		switch scope {
		case "action", "local", "per_action":
			if canon, ok := resolveActionVarKey(st.actionVars, key); ok {
				curVal, _ := strconv.ParseFloat(strings.TrimSpace(st.actionVars[canon]), 64)
				newVal := curVal + delta
				formatted := formatActionIncNumber(newVal, st.actionVarKinds[canon], delta)
				st.actionVars[canon] = formatted
				st.args[canon] = formatted
			}
		case "user", "user_persistent":
			if e.store == nil {
				return
			}
			if newVal, err := e.store.IncrementUserVar(st.platform, st.userID, key, delta); err == nil {
				st.args[key] = strconv.FormatFloat(newVal, 'f', -1, 64)
			}
		case "session", "global_ephemeral", "ephemeral":
			if e.store == nil {
				return
			}
			if newVal, err := e.store.IncrementGlobalVar(key, delta, true); err == nil {
				st.args[key] = strconv.FormatFloat(newVal, 'f', -1, 64)
			}
		default:
			if e.store == nil {
				return
			}
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

	op := strings.ToLower(strings.TrimSpace(sa.OBSOperation))
	mode := strings.ToLower(strings.TrimSpace(sa.OBSMode))
	scene := strings.TrimSpace(e.interpolate(sa.OBSScene, st))
	source := strings.TrimSpace(e.interpolate(sa.OBSSource, st))
	filter := strings.TrimSpace(e.interpolate(sa.OBSFilter, st))
	input := strings.TrimSpace(e.interpolate(sa.OBSInput, st))
	transition := strings.TrimSpace(e.interpolate(sa.OBSTransition, st))

	var err error
	switch op {
	case "set_scene":
		if scene != "" {
			if mode == "preview" {
				err = e.obs.SetCurrentPreviewScene(opCtx, scene)
			} else {
				err = e.obs.SetCurrentProgramScene(opCtx, scene)
			}
		}
	case "previous_scene":
		err = e.obs.SwitchToPreviousScene(opCtx)
	case "set_source_visible":
		if source != "" {
			visMode := mode
			if visMode == "" {
				if sa.OBSVisible {
					visMode = "show"
				} else {
					visMode = "hide"
				}
			}
			err = e.obs.SetSceneItemEnabledMode(opCtx, scene, source, visMode)
		}
	case "set_filter_enabled":
		target := source
		if target == "" {
			target = scene
		}
		if target != "" && filter != "" {
			if mode == "" {
				mode = "toggle"
			}
			err = e.obs.SetSourceFilterEnabledMode(opCtx, target, filter, mode)
		}
	case "zoom_source":
		if source != "" {
			err = e.obs.ZoomSceneItem(opCtx, scene, source, sa.OBSZoomFactor)
		}
	case "set_transform":
		if source != "" && sa.OBSTransform != nil {
			fields := transformFieldsFromConfig(sa.OBSTransform)
			if len(fields) > 0 {
				err = e.obs.SetSceneItemTransformFields(opCtx, scene, source, fields)
			}
		}
	case "reset_transform":
		if source != "" {
			err = e.obs.ResetSceneItemTransform(opCtx, scene, source)
		}
	case "set_mute":
		if input != "" {
			muteMode := mode
			if muteMode == "" {
				if sa.OBSMuted {
					muteMode = "mute"
				} else {
					muteMode = "unmute"
				}
			}
			err = e.obs.SetInputMuteMode(opCtx, input, muteMode)
		}
	case "set_volume":
		if input != "" {
			err = e.obs.SetInputVolumePct(opCtx, input, sa.OBSVolumePct, mode == "step")
		}
	case "set_text":
		if input != "" {
			err = e.obs.SetTextSource(opCtx, input, e.interpolate(sa.Text, st))
		}
	case "media_action":
		if input != "" {
			err = e.obs.TriggerMediaAction(opCtx, input, mode)
		}
	case "refresh_browser":
		if input != "" {
			err = e.obs.RefreshBrowserSource(opCtx, input)
		}
	case "set_transition":
		if transition != "" {
			err = e.obs.SetCurrentSceneTransition(opCtx, transition, sa.OBSDurationMs)
		}
	case "studio_mode":
		err = e.obs.StudioModeAction(opCtx, mode)
	case "toggle_record":
		err = e.obs.ToggleRecord(opCtx)
	case "toggle_stream":
		err = e.obs.ToggleStream(opCtx)
	case "save_replay":
		err = e.obs.SaveReplayBuffer(opCtx)
	case "custom_request":
		reqType := strings.TrimSpace(e.interpolate(sa.OBSRequestType, st))
		if reqType != "" {
			reqData := e.interpolate(sa.OBSRequestData, st)
			var resp map[string]any
			resp, err = e.obs.CustomRequest(opCtx, reqType, reqData)
			if err == nil && len(resp) > 0 {
				if raw, mErr := json.Marshal(resp); mErr == nil {
					st.args["obsResponse"] = string(raw)
				}
			}
		}
	}
	if err != nil && e.bridge.Logf != nil {
		e.bridge.Logf("OBS sub-action %q failed: %v", op, err)
	}
}

func transformFieldsFromConfig(t *config.BotOBSTransform) map[string]any {
	if t == nil {
		return nil
	}
	fields := make(map[string]any)
	if t.PositionX != nil {
		fields["positionX"] = *t.PositionX
	}
	if t.PositionY != nil {
		fields["positionY"] = *t.PositionY
	}
	if t.ScaleX != nil {
		fields["scaleX"] = *t.ScaleX
	}
	if t.ScaleY != nil {
		fields["scaleY"] = *t.ScaleY
	}
	if t.Rotation != nil {
		fields["rotation"] = *t.Rotation
	}
	if t.CropLeft != nil {
		fields["cropLeft"] = *t.CropLeft
	}
	if t.CropRight != nil {
		fields["cropRight"] = *t.CropRight
	}
	if t.CropTop != nil {
		fields["cropTop"] = *t.CropTop
	}
	if t.CropBottom != nil {
		fields["cropBottom"] = *t.CropBottom
	}
	return fields
}

func resolveActionVarKey(m map[string]string, key string) (string, bool) {
	if m == nil || key == "" {
		return "", false
	}
	if _, ok := m[key]; ok {
		return key, true
	}
	for k := range m {
		if strings.EqualFold(k, key) {
			return k, true
		}
	}
	return "", false
}

func declaredVarKind(vars []config.BotVariableDef, scope, key string) string {
	for _, v := range vars {
		if strings.EqualFold(v.Scope, scope) && strings.EqualFold(v.Name, key) {
			return v.Kind
		}
	}
	return ""
}

func formatActionIncNumber(val float64, kind string, delta float64) string {
	if (kind == "" || kind == "int") && delta == float64(int64(delta)) && val == float64(int64(val)) {
		return strconv.FormatInt(int64(val), 10)
	}
	return strconv.FormatFloat(val, 'f', -1, 64)
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
