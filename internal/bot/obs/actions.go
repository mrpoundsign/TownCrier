package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SceneItemInfo describes a scene item (or nested group child) inside an OBS scene.
type SceneItemInfo struct {
	SceneName   string
	SourceName  string
	SceneItemID int
	Enabled     bool
	IsGroup     bool
	GroupName   string
}

// InputInfo describes an OBS audio/media/text/browser input source.
type InputInfo struct {
	Name string
	Kind string
}

// FilterInfo describes a filter attached to an OBS source or scene.
type FilterInfo struct {
	Name    string
	Kind    string
	Enabled bool
}

// SceneItemTransform holds both writable OBS scene item transform parameters and read-only geometry metadata.
type SceneItemTransform struct {
	PositionX  float64
	PositionY  float64
	Rotation   float64
	ScaleX     float64
	ScaleY     float64
	CropLeft   float64
	CropRight  float64
	CropTop    float64
	CropBottom float64

	// Read-only / geometry metadata returned by OBS GetSceneItemTransform.
	SourceWidth  float64
	SourceHeight float64
	Width        float64
	Height       float64
	Alignment    int
	BoundsType   string
	BoundsWidth  float64
	BoundsHeight float64
}

// DiscoverySnapshot reports the currently cached OBS scenes, inputs, transitions, and active scenes.
type DiscoverySnapshot struct {
	CurrentScene  string
	PreviousScene string
	Scenes        []string
	Inputs        []InputInfo
	Transitions   []string
}

type clientState struct {
	currentProgramScene  string
	previousProgramScene string
	currentPreviewScene  string
	scenes               []string
	inputs               []InputInfo
	transitions          []string
	sceneItems           map[string][]SceneItemInfo
	zoomBaselines        map[string]SceneItemTransform
}

func newClientState() clientState {
	return clientState{
		sceneItems:    make(map[string][]SceneItemInfo),
		zoomBaselines: make(map[string]SceneItemTransform),
	}
}

func (c *Client) hydrateDiscovery() {
	if !c.IsConnected() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.ListScenes(ctx, true)
	_, _ = c.ListInputs(ctx, true)
	_, _ = c.ListTransitions(ctx, true)
}

func (c *Client) applyEventState(ev Event) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	switch ev.Type {
	case "CurrentProgramSceneChanged":
		next := strings.TrimSpace(stringMapVal(ev.Data, "sceneName"))
		if next != "" && next != c.state.currentProgramScene {
			if c.state.currentProgramScene != "" {
				c.state.previousProgramScene = c.state.currentProgramScene
			}
			c.state.currentProgramScene = next
		}
	case "CurrentPreviewSceneChanged":
		if next := strings.TrimSpace(stringMapVal(ev.Data, "sceneName")); next != "" {
			c.state.currentPreviewScene = next
		}
	case "SceneListChanged":
		scenes := extractNames(ev.Data["scenes"], "sceneName")
		slices.Reverse(scenes)
		c.state.scenes = scenes
	case "SceneItemCreated", "SceneItemRemoved":
		if scene := strings.TrimSpace(stringMapVal(ev.Data, "sceneName")); scene != "" {
			delete(c.state.sceneItems, scene)
		}
	case "InputCreated", "InputRemoved", "InputNameChanged":
		c.state.inputs = nil
	}
}

// DiscoverySnapshot returns a thread-safe copy of cached OBS discovery state.
func (c *Client) DiscoverySnapshot() DiscoverySnapshot {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return DiscoverySnapshot{
		CurrentScene:  c.state.currentProgramScene,
		PreviousScene: c.state.previousProgramScene,
		Scenes:        slices.Clone(c.state.scenes),
		Inputs:        slices.Clone(c.state.inputs),
		Transitions:   slices.Clone(c.state.transitions),
	}
}

// CurrentProgramScene returns the cached current program scene name.
func (c *Client) CurrentProgramScene() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.currentProgramScene
}

// PreviousProgramScene returns the cached previous program scene name.
func (c *Client) PreviousProgramScene() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.previousProgramScene
}

// ListScenes returns OBS scenes in top-to-bottom UI order.
func (c *Client) ListScenes(ctx context.Context, refresh bool) ([]string, error) {
	if !refresh {
		c.stateMu.RLock()
		cached := slices.Clone(c.state.scenes)
		c.stateMu.RUnlock()
		if len(cached) > 0 {
			return cached, nil
		}
	}

	resp, err := c.Call(ctx, "GetSceneList", nil)
	if err != nil {
		return nil, err
	}
	scenes := extractNames(resp["scenes"], "sceneName")
	slices.Reverse(scenes)

	c.stateMu.Lock()
	c.state.scenes = scenes
	if cur := strings.TrimSpace(stringMapVal(resp, "currentProgramSceneName")); cur != "" {
		if c.state.currentProgramScene != "" && c.state.currentProgramScene != cur {
			c.state.previousProgramScene = c.state.currentProgramScene
		}
		c.state.currentProgramScene = cur
	}
	if prev := strings.TrimSpace(stringMapVal(resp, "currentPreviewSceneName")); prev != "" {
		c.state.currentPreviewScene = prev
	}
	c.stateMu.Unlock()

	return slices.Clone(scenes), nil
}

// ListSceneItems returns scene items (plus nested group children) in top-to-bottom UI order.
// When sceneName is empty, the current program scene is used.
func (c *Client) ListSceneItems(ctx context.Context, sceneName string, refresh bool) ([]SceneItemInfo, error) {
	scene := strings.TrimSpace(sceneName)
	if scene == "" {
		scene = c.CurrentProgramScene()
		if scene == "" {
			resp, err := c.Call(ctx, "GetCurrentProgramScene", nil)
			if err != nil {
				return nil, err
			}
			scene = strings.TrimSpace(stringMapVal(resp, "currentProgramSceneName"))
			if scene == "" {
				scene = strings.TrimSpace(stringMapVal(resp, "sceneName"))
			}
			if scene != "" {
				c.stateMu.Lock()
				c.state.currentProgramScene = scene
				c.stateMu.Unlock()
			}
		}
	}
	if scene == "" {
		return nil, errors.New("unable to determine target OBS scene")
	}

	if !refresh {
		c.stateMu.RLock()
		cached := slices.Clone(c.state.sceneItems[scene])
		c.stateMu.RUnlock()
		if len(cached) > 0 {
			return cached, nil
		}
	}

	resp, err := c.Call(ctx, "GetSceneItemList", map[string]any{"sceneName": scene})
	if err != nil {
		return nil, err
	}
	items := parseSceneItemList(resp["sceneItems"], scene, "")
	slices.Reverse(items)

	expanded := make([]SceneItemInfo, 0, len(items))
	for _, it := range items {
		expanded = append(expanded, it)
		if it.IsGroup && it.SourceName != "" {
			grpResp, grpErr := c.Call(ctx, "GetGroupSceneItemList", map[string]any{"sceneName": it.SourceName})
			if grpErr == nil {
				children := parseSceneItemList(grpResp["sceneItems"], scene, it.SourceName)
				slices.Reverse(children)
				expanded = append(expanded, children...)
			}
		}
	}

	c.stateMu.Lock()
	c.state.sceneItems[scene] = expanded
	c.stateMu.Unlock()

	return slices.Clone(expanded), nil
}

// ListInputs returns all OBS inputs (audio, media, text, browser, video capture, etc.).
func (c *Client) ListInputs(ctx context.Context, refresh bool) ([]InputInfo, error) {
	if !refresh {
		c.stateMu.RLock()
		cached := slices.Clone(c.state.inputs)
		c.stateMu.RUnlock()
		if len(cached) > 0 {
			return cached, nil
		}
	}

	resp, err := c.Call(ctx, "GetInputList", nil)
	if err != nil {
		return nil, err
	}
	rawInputs, _ := resp["inputs"].([]any)
	inputs := make([]InputInfo, 0, len(rawInputs))
	for _, raw := range rawInputs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringMapVal(m, "inputName"))
		if name == "" {
			continue
		}
		inputs = append(inputs, InputInfo{
			Name: name,
			Kind: strings.TrimSpace(stringMapVal(m, "inputKind")),
		})
	}

	c.stateMu.Lock()
	c.state.inputs = inputs
	c.stateMu.Unlock()
	return slices.Clone(inputs), nil
}

// ListTransitions returns available OBS scene transition names.
func (c *Client) ListTransitions(ctx context.Context, refresh bool) ([]string, error) {
	if !refresh {
		c.stateMu.RLock()
		cached := slices.Clone(c.state.transitions)
		c.stateMu.RUnlock()
		if len(cached) > 0 {
			return cached, nil
		}
	}

	resp, err := c.Call(ctx, "GetSceneTransitionList", nil)
	if err != nil {
		return nil, err
	}
	transitions := extractNames(resp["transitions"], "transitionName")

	c.stateMu.Lock()
	c.state.transitions = transitions
	c.stateMu.Unlock()
	return slices.Clone(transitions), nil
}

// ListSourceFilters returns all filters attached to an OBS source, input, or scene.
func (c *Client) ListSourceFilters(ctx context.Context, sourceName string) ([]FilterInfo, error) {
	src := strings.TrimSpace(sourceName)
	if src == "" {
		return nil, errors.New("sourceName is required")
	}
	resp, err := c.Call(ctx, "GetSourceFilterList", map[string]any{"sourceName": src})
	if err != nil {
		return nil, err
	}
	rawFilters, _ := resp["filters"].([]any)
	filters := make([]FilterInfo, 0, len(rawFilters))
	for _, raw := range rawFilters {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringMapVal(m, "filterName"))
		if name == "" {
			continue
		}
		filters = append(filters, FilterInfo{
			Name:    name,
			Kind:    strings.TrimSpace(stringMapVal(m, "filterKind")),
			Enabled: boolMapVal(m, "filterEnabled", false),
		})
	}
	return filters, nil
}

func (c *Client) resolveSceneItem(ctx context.Context, sceneName, sourceName string) (containerName string, itemID int, err error) {
	scene := strings.TrimSpace(sceneName)
	source := strings.TrimSpace(sourceName)
	if source == "" {
		return "", 0, errors.New("sourceName is required")
	}
	if scene == "" {
		scene = c.CurrentProgramScene()
		if scene == "" {
			resp, curErr := c.Call(ctx, "GetCurrentProgramScene", nil)
			if curErr != nil {
				return "", 0, curErr
			}
			scene = strings.TrimSpace(stringMapVal(resp, "currentProgramSceneName"))
			if scene == "" {
				scene = strings.TrimSpace(stringMapVal(resp, "sceneName"))
			}
		}
	}
	if scene == "" {
		return "", 0, errors.New("target OBS scene is empty and no current scene is known")
	}

	if idData, idErr := c.Call(ctx, "GetSceneItemId", map[string]any{
		"sceneName":  scene,
		"sourceName": source,
	}); idErr == nil {
		if id := intMapVal(idData, "sceneItemId", 0); id > 0 {
			return scene, id, nil
		}
	}

	items, listErr := c.ListSceneItems(ctx, scene, true)
	if listErr == nil {
		for _, it := range items {
			if it.SourceName == source && it.SceneItemID > 0 {
				container := it.GroupName
				if container == "" {
					container = scene
				}
				return container, it.SceneItemID, nil
			}
		}
		for _, it := range items {
			if strings.EqualFold(it.SourceName, source) && it.SceneItemID > 0 {
				container := it.GroupName
				if container == "" {
					container = scene
				}
				return container, it.SceneItemID, nil
			}
		}
	}

	return "", 0, fmt.Errorf("scene item %q not found in %q", source, scene)
}

// GetSceneItemTransform retrieves the current transform for sourceName inside sceneName (or current program scene).
func (c *Client) GetSceneItemTransform(ctx context.Context, sceneName, sourceName string) (SceneItemTransform, error) {
	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return SceneItemTransform{}, err
	}
	resp, err := c.Call(ctx, "GetSceneItemTransform", map[string]any{
		"sceneName":   container,
		"sceneItemId": itemID,
	})
	if err != nil {
		return SceneItemTransform{}, err
	}
	raw, _ := resp["sceneItemTransform"].(map[string]any)
	if raw == nil {
		raw = resp
	}
	return SceneItemTransform{
		PositionX:    floatMapVal(raw, "positionX", 0),
		PositionY:    floatMapVal(raw, "positionY", 0),
		Rotation:     floatMapVal(raw, "rotation", 0),
		ScaleX:       floatMapVal(raw, "scaleX", 1),
		ScaleY:       floatMapVal(raw, "scaleY", 1),
		CropLeft:     floatMapVal(raw, "cropLeft", 0),
		CropRight:    floatMapVal(raw, "cropRight", 0),
		CropTop:      floatMapVal(raw, "cropTop", 0),
		CropBottom:   floatMapVal(raw, "cropBottom", 0),
		SourceWidth:  floatMapVal(raw, "sourceWidth", 0),
		SourceHeight: floatMapVal(raw, "sourceHeight", 0),
		Width:        floatMapVal(raw, "width", 0),
		Height:       floatMapVal(raw, "height", 0),
		Alignment:    intMapVal(raw, "alignment", 5),
		BoundsType:   stringMapVal(raw, "boundsType"),
		BoundsWidth:  floatMapVal(raw, "boundsWidth", 0),
		BoundsHeight: floatMapVal(raw, "boundsHeight", 0),
	}, nil
}

// SetCurrentProgramScene switches OBS's active program scene.
func (c *Client) SetCurrentProgramScene(ctx context.Context, sceneName string) error {
	scene := strings.TrimSpace(sceneName)
	if scene == "" {
		return errors.New("sceneName is required")
	}
	if _, err := c.Call(ctx, "SetCurrentProgramScene", map[string]any{"sceneName": scene}); err != nil {
		return err
	}
	c.stateMu.Lock()
	if c.state.currentProgramScene != "" && c.state.currentProgramScene != scene {
		c.state.previousProgramScene = c.state.currentProgramScene
	}
	c.state.currentProgramScene = scene
	c.stateMu.Unlock()
	return nil
}

// SetCurrentPreviewScene switches OBS's preview scene (Studio Mode).
func (c *Client) SetCurrentPreviewScene(ctx context.Context, sceneName string) error {
	scene := strings.TrimSpace(sceneName)
	if scene == "" {
		return errors.New("sceneName is required")
	}
	if _, err := c.Call(ctx, "SetCurrentPreviewScene", map[string]any{"sceneName": scene}); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.state.currentPreviewScene = scene
	c.stateMu.Unlock()
	return nil
}

// SwitchToPreviousScene switches OBS's active program scene back to the previously active scene.
func (c *Client) SwitchToPreviousScene(ctx context.Context) error {
	prev := c.PreviousProgramScene()
	if prev == "" {
		return errors.New("no previous OBS scene recorded yet")
	}
	return c.SetCurrentProgramScene(ctx, prev)
}

// SetSceneItemEnabled resolves sourceName inside sceneName and sets its visibility.
func (c *Client) SetSceneItemEnabled(ctx context.Context, sceneName, sourceName string, enabled bool) error {
	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return err
	}
	_, err = c.Call(ctx, "SetSceneItemEnabled", map[string]any{
		"sceneName":        container,
		"sceneItemId":      itemID,
		"sceneItemEnabled": enabled,
	})
	return err
}

// SetSceneItemEnabledMode sets or toggles visibility of sourceName inside sceneName ("show", "hide", or "toggle").
func (c *Client) SetSceneItemEnabledMode(ctx context.Context, sceneName, sourceName, mode string) error {
	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return err
	}
	var desired bool
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "show", "enable", "true":
		desired = true
	case "hide", "disable", "false":
		desired = false
	default:
		resp, getErr := c.Call(ctx, "GetSceneItemEnabled", map[string]any{
			"sceneName":   container,
			"sceneItemId": itemID,
		})
		if getErr != nil {
			return getErr
		}
		desired = !boolMapVal(resp, "sceneItemEnabled", false)
	}
	_, err = c.Call(ctx, "SetSceneItemEnabled", map[string]any{
		"sceneName":        container,
		"sceneItemId":      itemID,
		"sceneItemEnabled": desired,
	})
	return err
}

// SetSourceFilterEnabledMode sets or toggles a filter on a source or scene ("enable", "disable", or "toggle").
func (c *Client) SetSourceFilterEnabledMode(ctx context.Context, sourceName, filterName, mode string) error {
	src := strings.TrimSpace(sourceName)
	flt := strings.TrimSpace(filterName)
	if src == "" || flt == "" {
		return errors.New("sourceName and filterName are required")
	}
	var desired bool
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "enable", "show", "true":
		desired = true
	case "disable", "hide", "false":
		desired = false
	default:
		resp, getErr := c.Call(ctx, "GetSourceFilter", map[string]any{
			"sourceName": src,
			"filterName": flt,
		})
		if getErr != nil {
			return getErr
		}
		desired = !boolMapVal(resp, "filterEnabled", false)
	}
	_, err := c.Call(ctx, "SetSourceFilterEnabled", map[string]any{
		"sourceName":    src,
		"filterName":    flt,
		"filterEnabled": desired,
	})
	return err
}

func writableTransformMap(tr SceneItemTransform) map[string]any {
	m := map[string]any{
		"positionX":  tr.PositionX,
		"positionY":  tr.PositionY,
		"rotation":   tr.Rotation,
		"scaleX":     tr.ScaleX,
		"scaleY":     tr.ScaleY,
		"cropLeft":   tr.CropLeft,
		"cropRight":  tr.CropRight,
		"cropTop":    tr.CropTop,
		"cropBottom": tr.CropBottom,
	}
	if tr.BoundsType != "" && tr.BoundsType != "OBS_BOUNDS_NONE" && tr.BoundsWidth > 0 && tr.BoundsHeight > 0 {
		m["boundsWidth"] = tr.BoundsWidth
		m["boundsHeight"] = tr.BoundsHeight
	}
	return m
}

// SetSceneItemTransform applies writable transform properties to sourceName inside sceneName.
func (c *Client) SetSceneItemTransform(ctx context.Context, sceneName, sourceName string, tr SceneItemTransform) error {
	return c.SetSceneItemTransformFields(ctx, sceneName, sourceName, writableTransformMap(tr))
}

// SetSceneItemTransformFields applies a partial map of writable OBS scene item transform fields.
func (c *Client) SetSceneItemTransformFields(ctx context.Context, sceneName, sourceName string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return err
	}
	_, err = c.Call(ctx, "SetSceneItemTransform", map[string]any{
		"sceneName":          container,
		"sceneItemId":        itemID,
		"sceneItemTransform": fields,
	})
	return err
}

// ZoomSceneItem scales sourceName around its current visual center by factor (e.g. 1.25 zooms in 25%),
// saving the original transform on first zoom so ResetSceneItemTransform can restore it cleanly.
func (c *Client) ZoomSceneItem(ctx context.Context, sceneName, sourceName string, factor float64) error {
	if math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 {
		factor = 1.25
	}
	factor = min(max(factor, 0.1), 20.0)

	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return err
	}
	cur, err := c.GetSceneItemTransform(ctx, container, sourceName)
	if err != nil {
		return err
	}

	key := container + "\x00" + strconv.Itoa(itemID)
	c.stateMu.Lock()
	if _, exists := c.state.zoomBaselines[key]; !exists {
		c.state.zoomBaselines[key] = cur
	}
	c.stateMu.Unlock()

	deltaW := cur.Width * (factor - 1.0)
	deltaH := cur.Height * (factor - 1.0)
	newPosX := cur.PositionX
	if cur.Alignment&1 != 0 {
		newPosX = cur.PositionX - deltaW/2.0
	} else if cur.Alignment&2 != 0 {
		newPosX = cur.PositionX + deltaW/2.0
	}
	newPosY := cur.PositionY
	if cur.Alignment&4 != 0 {
		newPosY = cur.PositionY - deltaH/2.0
	} else if cur.Alignment&8 != 0 {
		newPosY = cur.PositionY + deltaH/2.0
	}

	fields := map[string]any{
		"positionX": newPosX,
		"positionY": newPosY,
	}
	if cur.BoundsType != "" && cur.BoundsType != "OBS_BOUNDS_NONE" && cur.BoundsWidth > 0 && cur.BoundsHeight > 0 {
		fields["boundsWidth"] = cur.BoundsWidth * factor
		fields["boundsHeight"] = cur.BoundsHeight * factor
	} else {
		fields["scaleX"] = cur.ScaleX * factor
		fields["scaleY"] = cur.ScaleY * factor
	}

	_, err = c.Call(ctx, "SetSceneItemTransform", map[string]any{
		"sceneName":          container,
		"sceneItemId":        itemID,
		"sceneItemTransform": fields,
	})
	return err
}

// ResetSceneItemTransform restores a previously saved zoom baseline if one exists,
// or resets scale/rotation/crop to default 1.0/0 values.
func (c *Client) ResetSceneItemTransform(ctx context.Context, sceneName, sourceName string) error {
	container, itemID, err := c.resolveSceneItem(ctx, sceneName, sourceName)
	if err != nil {
		return err
	}
	key := container + "\x00" + strconv.Itoa(itemID)
	c.stateMu.Lock()
	baseline, hasBaseline := c.state.zoomBaselines[key]
	if hasBaseline {
		delete(c.state.zoomBaselines, key)
	}
	c.stateMu.Unlock()

	var fields map[string]any
	if hasBaseline {
		fields = writableTransformMap(baseline)
	} else {
		fields = map[string]any{
			"scaleX":     1.0,
			"scaleY":     1.0,
			"rotation":   0.0,
			"cropLeft":   0.0,
			"cropRight":  0.0,
			"cropTop":    0.0,
			"cropBottom": 0.0,
		}
	}

	_, err = c.Call(ctx, "SetSceneItemTransform", map[string]any{
		"sceneName":          container,
		"sceneItemId":        itemID,
		"sceneItemTransform": fields,
	})
	return err
}

// SetInputMute sets mute state on an OBS audio input source.
func (c *Client) SetInputMute(ctx context.Context, inputName string, muted bool) error {
	_, err := c.Call(ctx, "SetInputMute", map[string]any{
		"inputName":  inputName,
		"inputMuted": muted,
	})
	return err
}

// SetInputMuteMode sets or toggles mute state on an OBS audio input ("mute", "unmute", or "toggle").
func (c *Client) SetInputMuteMode(ctx context.Context, inputName, mode string) error {
	input := strings.TrimSpace(inputName)
	if input == "" {
		return errors.New("inputName is required")
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "mute", "true":
		return c.SetInputMute(ctx, input, true)
	case "unmute", "false":
		return c.SetInputMute(ctx, input, false)
	default:
		_, err := c.Call(ctx, "ToggleInputMute", map[string]any{"inputName": input})
		return err
	}
}

// SetInputVolumePct sets or steps an OBS input's volume using OBS's perceptual cubic fader curve (0–100%).
func (c *Client) SetInputVolumePct(ctx context.Context, inputName string, volumePct float64, relative bool) error {
	input := strings.TrimSpace(inputName)
	if input == "" {
		return errors.New("inputName is required")
	}
	if math.IsNaN(volumePct) || math.IsInf(volumePct, 0) {
		volumePct = 0
	}

	targetPct := volumePct
	if relative {
		resp, err := c.Call(ctx, "GetInputVolume", map[string]any{"inputName": input})
		if err != nil {
			return err
		}
		curPct, _ := extractInputVolumePct(resp)
		targetPct = curPct + volumePct
	}
	targetPct = min(max(targetPct, 0), 100)

	_, err := c.Call(ctx, "SetInputVolume", map[string]any{
		"inputName":      input,
		"inputVolumeMul": faderPctToMul(targetPct),
	})
	return err
}

// SetTextSource updates the text content of an OBS Text (GDI+ / FreeType2) input source.
func (c *Client) SetTextSource(ctx context.Context, inputName, text string) error {
	input := strings.TrimSpace(inputName)
	if input == "" {
		return errors.New("inputName is required")
	}
	_, err := c.Call(ctx, "SetInputSettings", map[string]any{
		"inputName":     input,
		"inputSettings": map[string]any{"text": text},
		"overlay":       true,
	})
	return err
}

// TriggerMediaAction controls playback on an OBS media source ("play", "pause", "restart", or "stop").
func (c *Client) TriggerMediaAction(ctx context.Context, inputName, action string) error {
	input := strings.TrimSpace(inputName)
	if input == "" {
		return errors.New("inputName is required")
	}
	var mediaAction string
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "pause":
		mediaAction = "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PAUSE"
	case "restart":
		mediaAction = "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_RESTART"
	case "stop":
		mediaAction = "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_STOP"
	default:
		mediaAction = "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PLAY"
	}
	_, err := c.Call(ctx, "TriggerMediaInputAction", map[string]any{
		"inputName":   input,
		"mediaAction": mediaAction,
	})
	return err
}

// SetCurrentSceneTransition switches the active OBS transition and optionally sets its duration in ms.
func (c *Client) SetCurrentSceneTransition(ctx context.Context, transitionName string, durationMs int) error {
	tr := strings.TrimSpace(transitionName)
	if tr == "" {
		return errors.New("transitionName is required")
	}
	if _, err := c.Call(ctx, "SetCurrentSceneTransition", map[string]any{"transitionName": tr}); err != nil {
		return err
	}
	if durationMs > 0 {
		if _, err := c.Call(ctx, "SetCurrentSceneTransitionDuration", map[string]any{"transitionDuration": durationMs}); err != nil {
			return err
		}
	}
	return nil
}

// StudioModeAction triggers a Studio Mode transition or enables/disables/toggles Studio Mode.
func (c *Client) StudioModeAction(ctx context.Context, mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "enable":
		_, err := c.Call(ctx, "SetStudioModeEnabled", map[string]any{"studioModeEnabled": true})
		return err
	case "disable":
		_, err := c.Call(ctx, "SetStudioModeEnabled", map[string]any{"studioModeEnabled": false})
		return err
	case "toggle":
		resp, err := c.Call(ctx, "GetStudioModeEnabled", nil)
		if err != nil {
			return err
		}
		next := !boolMapVal(resp, "studioModeEnabled", false)
		_, err = c.Call(ctx, "SetStudioModeEnabled", map[string]any{"studioModeEnabled": next})
		return err
	default:
		_, err := c.Call(ctx, "TriggerStudioModeTransition", nil)
		return err
	}
}

// CustomRequest executes an arbitrary OBS WebSocket v5 requestType with optional JSON requestData.
func (c *Client) CustomRequest(ctx context.Context, requestType, requestDataJSON string) (map[string]any, error) {
	reqType := strings.TrimSpace(requestType)
	if reqType == "" {
		return nil, errors.New("requestType is required")
	}
	trimmed := strings.TrimSpace(requestDataJSON)
	var reqData map[string]any
	if trimmed != "" && trimmed != "{}" {
		if err := json.Unmarshal([]byte(trimmed), &reqData); err != nil {
			return nil, fmt.Errorf("invalid OBS requestData JSON: %w", err)
		}
	}
	return c.Call(ctx, reqType, reqData)
}

// ToggleRecord toggles OBS recording output.
func (c *Client) ToggleRecord(ctx context.Context) error {
	_, err := c.Call(ctx, "ToggleRecord", nil)
	return err
}

// ToggleStream toggles OBS streaming output.
func (c *Client) ToggleStream(ctx context.Context) error {
	_, err := c.Call(ctx, "ToggleStream", nil)
	return err
}

// SaveReplayBuffer saves the active OBS replay buffer.
func (c *Client) SaveReplayBuffer(ctx context.Context) error {
	_, err := c.Call(ctx, "SaveReplayBuffer", nil)
	return err
}

// RefreshBrowserSource triggers OBS's refreshnocache button on a browser source input.
func (c *Client) RefreshBrowserSource(ctx context.Context, inputName string) error {
	_, err := c.Call(ctx, "PressInputPropertiesButton", map[string]any{
		"inputName":    inputName,
		"propertyName": "refreshnocache",
	})
	return err
}

func parseSceneItemList(raw any, sceneName, groupName string) []SceneItemInfo {
	rawItems, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]SceneItemInfo, 0, len(rawItems))
	for _, entry := range rawItems {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		src := strings.TrimSpace(stringMapVal(item, "sourceName"))
		if src == "" {
			continue
		}
		out = append(out, SceneItemInfo{
			SceneName:   sceneName,
			SourceName:  src,
			SceneItemID: intMapVal(item, "sceneItemId", 0),
			Enabled:     boolMapVal(item, "sceneItemEnabled", true),
			IsGroup:     boolMapVal(item, "isGroup", false),
			GroupName:   groupName,
		})
	}
	return out
}

func extractNames(raw any, key string) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name := strings.TrimSpace(stringMapVal(m, key)); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func faderPctToMul(pct float64) float64 {
	if math.IsNaN(pct) || pct <= 0 {
		return 0
	}
	if pct >= 100 {
		return 1
	}
	def := min(max(pct, 0), 100) / 100.0
	return def * def * def
}

func mulToFaderPct(mul float64) float64 {
	if math.IsNaN(mul) || mul <= 0 {
		return 0
	}
	if mul >= 1 {
		return 100
	}
	return math.Round(math.Cbrt(mul)*100.0*2.0) / 2.0
}

func dbToFaderPct(db float64) float64 {
	if math.IsNaN(db) || math.IsInf(db, -1) || db <= -100 {
		return 0
	}
	if db >= 0 {
		return 100
	}
	return mulToFaderPct(math.Pow(10, db/20.0))
}

func extractInputVolumePct(m map[string]any) (float64, bool) {
	if m == nil {
		return 0, false
	}
	if rawMul, exists := m["inputVolumeMul"]; exists && rawMul != nil {
		if mul, ok := numericValue(rawMul); ok {
			return mulToFaderPct(mul), true
		}
	}
	if rawDb, exists := m["inputVolumeDb"]; exists {
		if rawDb == nil {
			return 0, true
		}
		if db, ok := numericValue(rawDb); ok {
			return dbToFaderPct(db), true
		}
	}
	return 0, false
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case float32:
		f := float64(n)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}

func stringMapVal(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func boolMapVal(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func intMapVal(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	default:
		return def
	}
}

func floatMapVal(m map[string]any, key string, def float64) float64 {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return def
	}
}
