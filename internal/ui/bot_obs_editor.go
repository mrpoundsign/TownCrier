package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/bot/obs"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

const obsCurrentSceneOption = "(Current Scene)"

// obsSubActionEditor builds and manages the dynamic cascading OBS Studio sub-action form.
type obsSubActionEditor struct {
	win       fyne.Window
	obsClient *obs.Client
	botVars   []TemplateVar
	onStatus  func(string)
	ctx       context.Context
	cancel    context.CancelFunc

	mu          sync.RWMutex
	scenes      []string
	sceneItems  []obs.SceneItemInfo
	inputs      []obs.InputInfo
	transitions []string
	filters     []obs.FilterInfo

	syncing    bool
	itemsGen   atomic.Uint64
	filtersGen atomic.Uint64

	statusLbl  *widget.Label
	refreshBtn *widget.Button
	opSelect   *widget.Select

	sceneEntry      *widget.SelectEntry
	sourceEntry     *widget.SelectEntry
	filterEntry     *widget.SelectEntry
	inputEntry      *widget.SelectEntry
	transitionEntry *widget.SelectEntry

	sceneTargetSelect *widget.Select
	visModeSelect     *widget.Select
	filterModeSelect  *widget.Select
	muteModeSelect    *widget.Select
	volModeSelect     *widget.Select
	mediaActionSelect *widget.Select
	studioModeSelect  *widget.Select

	zoomFactorEntry *widget.Entry
	volPctEntry     *widget.Entry
	durationMsEntry *widget.Entry
	textEntry       *widget.Entry
	reqTypeEntry    *widget.Entry
	reqDataEntry    *widget.Entry

	posXEntry  *widget.Entry
	posYEntry  *widget.Entry
	scaleXEntry *widget.Entry
	scaleYEntry *widget.Entry
	rotEntry   *widget.Entry
	cropLEntry *widget.Entry
	cropREntry *widget.Entry
	cropTEntry *widget.Entry
	cropBEntry *widget.Entry

	snapshotBtn     *widget.Button
	snapshotStatLbl *widget.Label

	fieldsBox *fyne.Container
	root      *fyne.Container
}

func newOBSSubActionEditor(win fyne.Window, client *obs.Client, cur config.BotSubActionDef, botVars []TemplateVar, onStatus func(string)) *obsSubActionEditor {
	ctx, cancel := context.WithCancel(context.Background())
	if len(botVars) == 0 {
		botVars = BotTemplateVars()
	}
	ed := &obsSubActionEditor{
		win:       win,
		obsClient: client,
		botVars:   botVars,
		onStatus:  onStatus,
		ctx:       ctx,
		cancel:    cancel,
	}
	ed.initWidgets(cur)
	ed.rebuildFields()
	ed.hydrateInitial(false)
	return ed
}

// Close cancels any in-flight OBS discovery RPCs for this dialog.
func (e *obsSubActionEditor) Close() {
	if e.cancel != nil {
		e.cancel()
	}
	uiRenderMu.Lock()
	uiRenderMu.Unlock() //nolint:staticcheck // barrier ensuring any in-flight fyne.Do batch completes before Close returns
}

// CanvasObject returns the root canvas object for embedding inside the sub-action dialog.
func (e *obsSubActionEditor) CanvasObject() fyne.CanvasObject {
	return e.root
}

func (e *obsSubActionEditor) initWidgets(cur config.BotSubActionDef) {
	op := strings.ToLower(strings.TrimSpace(cur.OBSOperation))
	if op == "" {
		op = "set_scene"
	}

	e.statusLbl = widget.NewLabel("⚪ OBS Disconnected — enter names manually or connect in Bot → OBS Studio")
	e.statusLbl.Wrapping = fyne.TextWrapWord
	e.refreshBtn = widget.NewButtonWithIcon("Refresh OBS Lists", theme.ViewRefreshIcon(), func() {
		e.hydrateInitial(true)
	})

	e.opSelect = widget.NewSelect(botOptionLabels(botOBSOperationOptions), func(_ string) {
		if e.syncing {
			return
		}
		e.refreshInputOptionsForCurrentOp()
		e.rebuildFields()
		if e.currentOp() == "set_filter_enabled" {
			e.triggerFilterRefresh(false)
		}
	})
	e.syncing = true
	e.opSelect.SetSelected(botOptionLabel(botOBSOperationOptions, op))
	e.syncing = false

	e.sceneEntry = widget.NewSelectEntry(nil)
	e.sceneEntry.SetPlaceHolder("Select or type scene name (or %var%)")
	e.sceneEntry.SetText(cur.OBSScene)
	e.sceneEntry.OnChanged = func(val string) {
		if e.syncing {
			return
		}
		clean := strings.TrimSpace(val)
		if clean == "" || clean == obsCurrentSceneOption || e.isKnownScene(clean) {
			e.triggerSceneItemsRefresh(clean, false)
			if e.currentOp() == "set_filter_enabled" && strings.TrimSpace(e.sourceEntry.Text) == "" {
				e.triggerFilterRefresh(false)
			}
		}
	}

	e.sourceEntry = widget.NewSelectEntry(nil)
	e.sourceEntry.SetPlaceHolder("Select or type source name (or %var%)")
	e.sourceEntry.SetText(cur.OBSSource)
	e.sourceEntry.OnChanged = func(val string) {
		if e.syncing {
			return
		}
		if e.currentOp() == "set_filter_enabled" {
			clean := strings.TrimSpace(val)
			if clean == "" || e.isKnownFilterTarget(clean) {
				e.triggerFilterRefresh(false)
			}
		}
	}

	e.filterEntry = widget.NewSelectEntry(nil)
	e.filterEntry.SetPlaceHolder("Select or type filter name")
	e.filterEntry.SetText(cur.OBSFilter)

	e.inputEntry = widget.NewSelectEntry(nil)
	e.inputEntry.SetPlaceHolder("Select or type input name")
	e.inputEntry.SetText(cur.OBSInput)

	e.transitionEntry = widget.NewSelectEntry(nil)
	e.transitionEntry.SetPlaceHolder("Select or type transition name")
	e.transitionEntry.SetText(cur.OBSTransition)

	e.sceneTargetSelect = widget.NewSelect(botOptionLabels(botOBSSceneTargetOptions), nil)
	sceneMode := cur.OBSMode
	if sceneMode == "" {
		sceneMode = "program"
	}
	e.sceneTargetSelect.SetSelected(botOptionLabel(botOBSSceneTargetOptions, sceneMode))

	e.visModeSelect = widget.NewSelect(botOptionLabels(botOBSVisibilityModeOptions), nil)
	visMode := cur.OBSMode
	if visMode == "" && op == "set_source_visible" {
		if cur.OBSVisible {
			visMode = "show"
		} else {
			visMode = "hide"
		}
	}
	if visMode == "" {
		visMode = "toggle"
	}
	e.visModeSelect.SetSelected(botOptionLabel(botOBSVisibilityModeOptions, visMode))

	e.filterModeSelect = widget.NewSelect(botOptionLabels(botOBSFilterModeOptions), nil)
	fltMode := cur.OBSMode
	if fltMode == "" {
		fltMode = "toggle"
	}
	e.filterModeSelect.SetSelected(botOptionLabel(botOBSFilterModeOptions, fltMode))

	e.muteModeSelect = widget.NewSelect(botOptionLabels(botOBSMuteModeOptions), nil)
	muteMode := cur.OBSMode
	if muteMode == "" && op == "set_mute" {
		if cur.OBSMuted {
			muteMode = "mute"
		} else {
			muteMode = "unmute"
		}
	}
	if muteMode == "" {
		muteMode = "toggle"
	}
	e.muteModeSelect.SetSelected(botOptionLabel(botOBSMuteModeOptions, muteMode))

	e.volModeSelect = widget.NewSelect(botOptionLabels(botOBSVolumeModeOptions), nil)
	volMode := cur.OBSMode
	if volMode == "" {
		volMode = "set"
	}
	e.volModeSelect.SetSelected(botOptionLabel(botOBSVolumeModeOptions, volMode))

	e.mediaActionSelect = widget.NewSelect(botOptionLabels(botOBSMediaActionOptions), nil)
	mediaMode := cur.OBSMode
	if mediaMode == "" {
		mediaMode = "play"
	}
	e.mediaActionSelect.SetSelected(botOptionLabel(botOBSMediaActionOptions, mediaMode))

	e.studioModeSelect = widget.NewSelect(botOptionLabels(botOBSStudioModeOptions), nil)
	studioMode := cur.OBSMode
	if studioMode == "" {
		studioMode = "transition"
	}
	e.studioModeSelect.SetSelected(botOptionLabel(botOBSStudioModeOptions, studioMode))

	e.zoomFactorEntry = widget.NewEntry()
	e.zoomFactorEntry.SetPlaceHolder("1.25")
	if cur.OBSZoomFactor > 0 {
		e.zoomFactorEntry.SetText(strconv.FormatFloat(cur.OBSZoomFactor, 'f', -1, 64))
	} else {
		e.zoomFactorEntry.SetText("1.25")
	}

	e.volPctEntry = widget.NewEntry()
	e.volPctEntry.SetPlaceHolder("0–100 (or +/- step e.g. -10)")
	if cur.OBSVolumePct != 0 || op == "set_volume" {
		e.volPctEntry.SetText(strconv.FormatFloat(cur.OBSVolumePct, 'f', -1, 64))
	}

	e.durationMsEntry = widget.NewEntry()
	e.durationMsEntry.SetPlaceHolder("Optional duration in ms (e.g. 300)")
	if cur.OBSDurationMs > 0 {
		e.durationMsEntry.SetText(strconv.Itoa(cur.OBSDurationMs))
	}

	e.textEntry = widget.NewMultiLineEntry()
	e.textEntry.SetMinRowsVisible(2)
	e.textEntry.SetPlaceHolder("Text to display (supports %user%, %arg0%, %global.count%, etc.)")
	if op == "set_text" {
		e.textEntry.SetText(cur.Text)
	}

	e.reqTypeEntry = widget.NewEntry()
	e.reqTypeEntry.SetPlaceHolder("e.g. TriggerHotkeyByName or CallVendorRequest")
	e.reqTypeEntry.SetText(cur.OBSRequestType)

	e.reqDataEntry = widget.NewMultiLineEntry()
	e.reqDataEntry.SetMinRowsVisible(3)
	e.reqDataEntry.SetPlaceHolder(`{"hotkeyName":"OBSBasic.Screenshot"}`)
	if strings.TrimSpace(cur.OBSRequestData) != "" {
		e.reqDataEntry.SetText(cur.OBSRequestData)
	} else {
		e.reqDataEntry.SetText("{}")
	}

	e.posXEntry = widget.NewEntry()
	e.posYEntry = widget.NewEntry()
	e.scaleXEntry = widget.NewEntry()
	e.scaleYEntry = widget.NewEntry()
	e.rotEntry = widget.NewEntry()
	e.cropLEntry = widget.NewEntry()
	e.cropREntry = widget.NewEntry()
	e.cropTEntry = widget.NewEntry()
	e.cropBEntry = widget.NewEntry()
	for _, ent := range []*widget.Entry{
		e.posXEntry, e.posYEntry, e.scaleXEntry, e.scaleYEntry, e.rotEntry,
		e.cropLEntry, e.cropREntry, e.cropTEntry, e.cropBEntry,
	} {
		ent.SetPlaceHolder("unchanged")
	}
	if tr := cur.OBSTransform; tr != nil {
		setFloatPtrEntry(e.posXEntry, tr.PositionX)
		setFloatPtrEntry(e.posYEntry, tr.PositionY)
		setFloatPtrEntry(e.scaleXEntry, tr.ScaleX)
		setFloatPtrEntry(e.scaleYEntry, tr.ScaleY)
		setFloatPtrEntry(e.rotEntry, tr.Rotation)
		setFloatPtrEntry(e.cropLEntry, tr.CropLeft)
		setFloatPtrEntry(e.cropREntry, tr.CropRight)
		setFloatPtrEntry(e.cropTEntry, tr.CropTop)
		setFloatPtrEntry(e.cropBEntry, tr.CropBottom)
	}

	e.snapshotStatLbl = widget.NewLabel("")
	e.snapshotStatLbl.Wrapping = fyne.TextWrapWord
	e.snapshotBtn = widget.NewButtonWithIcon("Snapshot Current from OBS", theme.DownloadIcon(), func() {
		e.snapshotTransformFromOBS()
	})

	e.fieldsBox = container.NewVBox()
	topBar := container.NewBorder(nil, nil, nil, e.refreshBtn, e.statusLbl)
	e.root = container.NewVBox(
		topBar,
		widget.NewForm(widget.NewFormItem("OBS Operation", e.opSelect)),
		e.fieldsBox,
	)
}

func (e *obsSubActionEditor) currentOp() string {
	return botOptionKey(botOBSOperationOptions, e.opSelect.Selected)
}

func (e *obsSubActionEditor) normalizeSceneText() string {
	s := strings.TrimSpace(e.sceneEntry.Text)
	if s == obsCurrentSceneOption {
		return ""
	}
	return s
}

func (e *obsSubActionEditor) rebuildFields() {
	op := e.currentOp()
	var items []*widget.FormItem
	var extras []fyne.CanvasObject

	switch op {
	case "set_scene":
		items = append(items,
			widget.NewFormItem("Scene", e.sceneEntry),
			widget.NewFormItem("Target", e.sceneTargetSelect),
		)

	case "previous_scene":
		extras = append(extras, widget.NewLabel("Switches OBS back to the scene that was active prior to the current program scene."))

	case "set_source_visible":
		items = append(items,
			widget.NewFormItem("Scene", e.sceneEntry),
			widget.NewFormItem("Source", e.sourceEntry),
			widget.NewFormItem("Action", e.visModeSelect),
		)

	case "set_filter_enabled":
		items = append(items,
			widget.NewFormItem("Scene (Optional)", e.sceneEntry),
			widget.NewFormItem("Source (or blank for Scene)", e.sourceEntry),
			widget.NewFormItem("Filter", e.filterEntry),
			widget.NewFormItem("Action", e.filterModeSelect),
		)

	case "zoom_source":
		presets := container.NewHBox(
			widget.NewLabel("Presets:"),
			widget.NewButton("1.2×", func() { e.zoomFactorEntry.SetText("1.2") }),
			widget.NewButton("1.35×", func() { e.zoomFactorEntry.SetText("1.35") }),
			widget.NewButton("1.5×", func() { e.zoomFactorEntry.SetText("1.5") }),
			widget.NewButton("0.8× (Out)", func() { e.zoomFactorEntry.SetText("0.8") }),
		)
		hint := widget.NewLabelWithStyle("Zooms centered around the source's current position. Run multiple times to enhance further, or use Reset Source Transform to restore. In OBS Studio Mode, uncheck Duplicate Scene (gear menu) for live zoom updates.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
		hint.Wrapping = fyne.TextWrapWord
		items = append(items,
			widget.NewFormItem("Scene", e.sceneEntry),
			widget.NewFormItem("Source", e.sourceEntry),
			widget.NewFormItem("Zoom Factor", container.NewVBox(e.zoomFactorEntry, presets)),
		)
		extras = append(extras, hint)

	case "set_transform":
		items = append(items,
			widget.NewFormItem("Scene", e.sceneEntry),
			widget.NewFormItem("Source", e.sourceEntry),
		)
		grid := container.NewGridWithColumns(2,
			labelledEntry("Pos X", e.posXEntry),
			labelledEntry("Pos Y", e.posYEntry),
			labelledEntry("Scale X", e.scaleXEntry),
			labelledEntry("Scale Y", e.scaleYEntry),
			labelledEntry("Rotation (°)", e.rotEntry),
			layout.NewSpacer(),
			labelledEntry("Crop Left", e.cropLEntry),
			labelledEntry("Crop Right", e.cropREntry),
			labelledEntry("Crop Top", e.cropTEntry),
			labelledEntry("Crop Bottom", e.cropBEntry),
		)
		extras = append(extras,
			container.NewHBox(e.snapshotBtn, layout.NewSpacer()),
			e.snapshotStatLbl,
			grid,
		)

	case "reset_transform":
		hint := widget.NewLabelWithStyle("Restores the pre-zoom transform saved by Zoom Source (or resets scale=1, rotation=0, crop=0).", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
		hint.Wrapping = fyne.TextWrapWord
		items = append(items,
			widget.NewFormItem("Scene", e.sceneEntry),
			widget.NewFormItem("Source", e.sourceEntry),
		)
		extras = append(extras, hint)

	case "set_mute":
		items = append(items,
			widget.NewFormItem("Audio Input", e.inputEntry),
			widget.NewFormItem("Action", e.muteModeSelect),
		)

	case "set_volume":
		items = append(items,
			widget.NewFormItem("Audio Input", e.inputEntry),
			widget.NewFormItem("Mode", e.volModeSelect),
			widget.NewFormItem("Volume (%)", e.volPctEntry),
		)

	case "set_text":
		items = append(items,
			widget.NewFormItem("Text Input", e.inputEntry),
			widget.NewFormItem("Text", withBotVarHelpVars(e.win, "OBS Text Content", e.textEntry, e.botVars, e.onStatus)),
		)

	case "media_action":
		items = append(items,
			widget.NewFormItem("Media Input", e.inputEntry),
			widget.NewFormItem("Playback", e.mediaActionSelect),
		)

	case "refresh_browser":
		items = append(items, widget.NewFormItem("Browser Input", e.inputEntry))

	case "set_transition":
		items = append(items,
			widget.NewFormItem("Transition", e.transitionEntry),
			widget.NewFormItem("Duration (ms)", e.durationMsEntry),
		)

	case "studio_mode":
		items = append(items, widget.NewFormItem("Studio Action", e.studioModeSelect))

	case "toggle_record", "toggle_stream", "save_replay":
		extras = append(extras, widget.NewLabel("No extra parameters required."))

	case "custom_request":
		items = append(items,
			widget.NewFormItem("Request Type", e.reqTypeEntry),
			widget.NewFormItem("Request Data (JSON)", withBotVarHelpVars(e.win, "OBS Request Data", e.reqDataEntry, e.botVars, e.onStatus)),
		)
	}

	var objs []fyne.CanvasObject
	if len(items) > 0 {
		objs = append(objs, widget.NewForm(items...))
	}
	objs = append(objs, extras...)
	e.fieldsBox.Objects = objs
	e.fieldsBox.Refresh()
}

func labelledEntry(lbl string, ent *widget.Entry) fyne.CanvasObject {
	return container.NewBorder(nil, nil, widget.NewLabel(lbl), nil, ent)
}

func (e *obsSubActionEditor) hydrateInitial(forceRefresh bool) {
	if e.obsClient == nil || !e.obsClient.IsConnected() {
		e.updateStatusText("⚪ OBS Disconnected — enter names manually or connect in Bot → OBS Studio")
		return
	}
	e.updateStatusText("🟡 Loading OBS scenes, sources, inputs, and transitions...")

	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		defer cancel()

		scenes, sErr := e.obsClient.ListScenes(ctx, forceRefresh)
		inputs, _ := e.obsClient.ListInputs(ctx, forceRefresh)
		transitions, _ := e.obsClient.ListTransitions(ctx, forceRefresh)
		snap := e.obsClient.DiscoverySnapshot()

		e.mu.Lock()
		e.scenes = scenes
		e.inputs = inputs
		e.transitions = transitions
		e.mu.Unlock()

		fyne.Do(func() {
			uiRenderMu.Lock()
			defer uiRenderMu.Unlock()
			if e.ctx.Err() != nil {
				return
			}
			e.syncing = true
			sceneOpts := make([]string, 0, len(scenes)+1)
			sceneOpts = append(sceneOpts, obsCurrentSceneOption)
			sceneOpts = append(sceneOpts, scenes...)
			e.sceneEntry.SetOptions(sceneOpts)
			e.transitionEntry.SetOptions(transitions)
			e.refreshInputOptionsForCurrentOp()
			e.syncing = false

			if sErr != nil {
				e.updateStatusText("⚠️ OBS connected, discovery partial: " + sErr.Error())
			} else {
				cur := snap.CurrentScene
				if cur == "" {
					cur = "(unknown)"
				}
				e.updateStatusText(fmt.Sprintf("🟢 OBS Connected — %d scenes, %d inputs, %d transitions (Active: %s)", len(scenes), len(inputs), len(transitions), cur))
			}

			nextScene := e.normalizeSceneText()
			e.triggerSceneItemsRefresh(nextScene, forceRefresh)
			if e.currentOp() == "set_filter_enabled" {
				e.triggerFilterRefreshForTarget(e.resolveFilterTarget())
			}
		})
	}()
}

func (e *obsSubActionEditor) triggerSceneItemsRefresh(scene string, forceRefresh bool) {
	if e.obsClient == nil || !e.obsClient.IsConnected() {
		return
	}
	gen := e.itemsGen.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		defer cancel()

		items, err := e.obsClient.ListSceneItems(ctx, scene, forceRefresh)
		if err != nil || e.itemsGen.Load() != gen || e.ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		e.sceneItems = items
		e.mu.Unlock()

		opts := make([]string, 0, len(items))
		seen := make(map[string]struct{}, len(items))
		for _, it := range items {
			if _, ok := seen[it.SourceName]; ok {
				continue
			}
			seen[it.SourceName] = struct{}{}
			opts = append(opts, it.SourceName)
		}
		fyne.Do(func() {
			uiRenderMu.Lock()
			defer uiRenderMu.Unlock()
			if e.ctx.Err() != nil || e.itemsGen.Load() != gen {
				return
			}
			e.sourceEntry.SetOptions(opts)
		})
	}()
}

func (e *obsSubActionEditor) resolveFilterTarget() string {
	target := strings.TrimSpace(e.sourceEntry.Text)
	if target == "" {
		target = e.normalizeSceneText()
	}
	if target == "" && e.obsClient != nil {
		target = e.obsClient.CurrentProgramScene()
	}
	return target
}

func (e *obsSubActionEditor) triggerFilterRefresh(_ bool) {
	if e.obsClient == nil || !e.obsClient.IsConnected() {
		return
	}
	e.triggerFilterRefreshForTarget(e.resolveFilterTarget())
}

func (e *obsSubActionEditor) triggerFilterRefreshForTarget(target string) {
	if e.obsClient == nil || !e.obsClient.IsConnected() || target == "" {
		return
	}

	gen := e.filtersGen.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		defer cancel()

		filters, err := e.obsClient.ListSourceFilters(ctx, target)
		if err != nil || e.filtersGen.Load() != gen || e.ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		e.filters = filters
		e.mu.Unlock()

		opts := make([]string, 0, len(filters))
		for _, f := range filters {
			opts = append(opts, f.Name)
		}
		fyne.Do(func() {
			uiRenderMu.Lock()
			defer uiRenderMu.Unlock()
			if e.ctx.Err() != nil || e.filtersGen.Load() != gen {
				return
			}
			e.filterEntry.SetOptions(opts)
		})
	}()
}

func (e *obsSubActionEditor) snapshotTransformFromOBS() {
	if e.obsClient == nil || !e.obsClient.IsConnected() {
		e.snapshotStatLbl.SetText("⚠️ Connect to OBS Studio first to snapshot a transform.")
		return
	}
	scene := e.normalizeSceneText()
	source := strings.TrimSpace(e.sourceEntry.Text)
	if source == "" {
		e.snapshotStatLbl.SetText("⚠️ Select or enter a Source name first.")
		return
	}
	e.snapshotStatLbl.SetText("⏳ Reading current transform from OBS...")

	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		defer cancel()

		tr, err := e.obsClient.GetSceneItemTransform(ctx, scene, source)
		fyne.Do(func() {
			uiRenderMu.Lock()
			defer uiRenderMu.Unlock()
			if e.ctx.Err() != nil {
				return
			}
			if err != nil {
				e.snapshotStatLbl.SetText("⚠️ Snapshot failed: " + err.Error())
				return
			}
			e.posXEntry.SetText(formatTransformNum(tr.PositionX))
			e.posYEntry.SetText(formatTransformNum(tr.PositionY))
			e.scaleXEntry.SetText(formatTransformNum(tr.ScaleX))
			e.scaleYEntry.SetText(formatTransformNum(tr.ScaleY))
			e.rotEntry.SetText(formatTransformNum(tr.Rotation))
			e.cropLEntry.SetText(formatTransformNum(tr.CropLeft))
			e.cropREntry.SetText(formatTransformNum(tr.CropRight))
			e.cropTEntry.SetText(formatTransformNum(tr.CropTop))
			e.cropBEntry.SetText(formatTransformNum(tr.CropBottom))
			e.snapshotStatLbl.SetText(fmt.Sprintf("✓ Captured %q transform (%.0f×%.0f px).", source, tr.Width, tr.Height))
		})
	}()
}

func (e *obsSubActionEditor) refreshInputOptionsForCurrentOp() {
	e.mu.RLock()
	inputs := slices.Clone(e.inputs)
	e.mu.RUnlock()

	if len(inputs) == 0 {
		return
	}
	op := e.currentOp()
	var preferred, others []string
	for _, in := range inputs {
		if inputMatchesOp(op, in.Kind) {
			preferred = append(preferred, in.Name)
		} else {
			others = append(others, in.Name)
		}
	}
	ordered := make([]string, 0, len(inputs))
	ordered = append(ordered, preferred...)
	ordered = append(ordered, others...)
	e.inputEntry.SetOptions(ordered)
}

func inputMatchesOp(op, kind string) bool {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch op {
	case "set_text":
		return strings.Contains(k, "text") || strings.Contains(k, "freetype")
	case "media_action":
		return strings.Contains(k, "ffmpeg") || strings.Contains(k, "vlc") || strings.Contains(k, "media")
	case "refresh_browser":
		return strings.Contains(k, "browser")
	default:
		return true
	}
}

func (e *obsSubActionEditor) isKnownScene(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, s := range e.scenes {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

func (e *obsSubActionEditor) isKnownFilterTarget(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, it := range e.sceneItems {
		if strings.EqualFold(it.SourceName, name) {
			return true
		}
	}
	for _, in := range e.inputs {
		if strings.EqualFold(in.Name, name) {
			return true
		}
	}
	for _, s := range e.scenes {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

func (e *obsSubActionEditor) updateStatusText(msg string) {
	if e.statusLbl != nil && e.statusLbl.Text != msg {
		e.statusLbl.SetText(msg)
	}
}

// ApplyTo writes the configured OBS fields onto dst while preserving backwards compatibility.
func (e *obsSubActionEditor) ApplyTo(dst *config.BotSubActionDef) {
	if dst == nil {
		return
	}
	op := e.currentOp()
	dst.OBSOperation = op
	dst.OBSScene = e.normalizeSceneText()
	dst.OBSSource = strings.TrimSpace(e.sourceEntry.Text)
	dst.OBSFilter = strings.TrimSpace(e.filterEntry.Text)
	dst.OBSInput = strings.TrimSpace(e.inputEntry.Text)
	dst.OBSTransition = strings.TrimSpace(e.transitionEntry.Text)
	dst.OBSDurationMs, _ = strconv.Atoi(strings.TrimSpace(e.durationMsEntry.Text))
	dst.OBSRequestType = strings.TrimSpace(e.reqTypeEntry.Text)
	dst.OBSRequestData = strings.TrimSpace(e.reqDataEntry.Text)

	if zf, err := strconv.ParseFloat(strings.TrimSpace(e.zoomFactorEntry.Text), 64); err == nil && zf > 0 {
		dst.OBSZoomFactor = zf
	} else if op == "zoom_source" {
		dst.OBSZoomFactor = 1.25
	} else {
		dst.OBSZoomFactor = 0
	}

	if vp, err := strconv.ParseFloat(strings.TrimSpace(e.volPctEntry.Text), 64); err == nil {
		dst.OBSVolumePct = vp
	} else {
		dst.OBSVolumePct = 0
	}

	switch op {
	case "set_scene":
		dst.OBSMode = botOptionKey(botOBSSceneTargetOptions, e.sceneTargetSelect.Selected)
	case "set_source_visible":
		dst.OBSMode = botOptionKey(botOBSVisibilityModeOptions, e.visModeSelect.Selected)
		dst.OBSVisible = dst.OBSMode != "hide"
	case "set_filter_enabled":
		dst.OBSMode = botOptionKey(botOBSFilterModeOptions, e.filterModeSelect.Selected)
	case "set_mute":
		dst.OBSMode = botOptionKey(botOBSMuteModeOptions, e.muteModeSelect.Selected)
		dst.OBSMuted = dst.OBSMode == "mute"
	case "set_volume":
		dst.OBSMode = botOptionKey(botOBSVolumeModeOptions, e.volModeSelect.Selected)
	case "media_action":
		dst.OBSMode = botOptionKey(botOBSMediaActionOptions, e.mediaActionSelect.Selected)
	case "studio_mode":
		dst.OBSMode = botOptionKey(botOBSStudioModeOptions, e.studioModeSelect.Selected)
	default:
		dst.OBSMode = ""
	}

	if op == "set_text" {
		dst.Text = e.textEntry.Text
	}

	if op == "set_transform" {
		dst.OBSTransform = e.buildTransformConfig()
	} else {
		dst.OBSTransform = nil
	}
}

func (e *obsSubActionEditor) buildTransformConfig() *config.BotOBSTransform {
	tr := &config.BotOBSTransform{
		PositionX:  parseFloatPtrEntry(e.posXEntry),
		PositionY:  parseFloatPtrEntry(e.posYEntry),
		ScaleX:     parseFloatPtrEntry(e.scaleXEntry),
		ScaleY:     parseFloatPtrEntry(e.scaleYEntry),
		Rotation:   parseFloatPtrEntry(e.rotEntry),
		CropLeft:   parseFloatPtrEntry(e.cropLEntry),
		CropRight:  parseFloatPtrEntry(e.cropREntry),
		CropTop:    parseFloatPtrEntry(e.cropTEntry),
		CropBottom: parseFloatPtrEntry(e.cropBEntry),
	}
	if tr.PositionX == nil && tr.PositionY == nil &&
		tr.ScaleX == nil && tr.ScaleY == nil &&
		tr.Rotation == nil &&
		tr.CropLeft == nil && tr.CropRight == nil &&
		tr.CropTop == nil && tr.CropBottom == nil {
		return nil
	}
	return tr
}

func setFloatPtrEntry(ent *widget.Entry, v *float64) {
	if ent == nil || v == nil {
		return
	}
	ent.SetText(formatTransformNum(*v))
}

func parseFloatPtrEntry(ent *widget.Entry) *float64 {
	if ent == nil {
		return nil
	}
	raw := strings.TrimSpace(ent.Text)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

func formatTransformNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
