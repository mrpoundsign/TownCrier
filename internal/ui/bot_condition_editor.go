package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/bot/obs"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

const keepCustomBranchPrefix = "Keep Custom Steps"

type conditionBranchUI struct {
	modeSelect  *widget.Select
	actSelect   *widget.Select
	actionRow   *fyne.Container
	customLabel string
	customSteps []config.BotSubActionDef
}

// botConditionEditor builds the If / Else Condition form with %values% help,
// live OBS scene dropdown suggestions, and Then/Else branch controls.
type botConditionEditor struct {
	win       fyne.Window
	obsClient *obs.Client
	onStatus  func(string)
	ctx       context.Context
	cancel    context.CancelFunc

	mu         sync.RWMutex
	obsScenes  []string
	syncing    bool
	scenesGen  atomic.Uint64
	actToID    map[string]string
	idToAct    map[string]string
	actLabels  []string

	lhsEntry      *widget.SelectEntry
	opSelect      *widget.Select
	rhsEntry      *widget.SelectEntry
	rhsRefreshBtn *widget.Button
	rhsHintLbl    *widget.Label

	thenBranch *conditionBranchUI
	elseBranch *conditionBranchUI
	root       *fyne.Container
}

func newBotConditionEditor(
	win fyne.Window,
	obsClient *obs.Client,
	cur config.BotSubActionDef,
	actLabels []string,
	actLabelToID, actIDToLabel map[string]string,
	onStatus func(string),
) *botConditionEditor {
	ctx, cancel := context.WithCancel(context.Background())
	ed := &botConditionEditor{
		win:       win,
		obsClient: obsClient,
		onStatus:  onStatus,
		ctx:       ctx,
		cancel:    cancel,
		actLabels: actLabels,
		actToID:   actLabelToID,
		idToAct:   actIDToLabel,
	}
	ed.initWidgets(cur)
	ed.syncRightValueOptions(false)
	return ed
}

// Close cancels any in-flight OBS scene discovery call.
func (e *botConditionEditor) Close() {
	if e.cancel != nil {
		e.cancel()
	}
}

// CanvasObject returns the condition editor UI container.
func (e *botConditionEditor) CanvasObject() fyne.CanvasObject {
	return e.root
}

func (e *botConditionEditor) initWidgets(cur config.BotSubActionDef) {
	e.lhsEntry = widget.NewSelectEntry(botOptionLabels(botConditionLHSPresetOptions))
	e.lhsEntry.SetPlaceHolder("e.g. %obs.scene%, %arg0%, %global.count%")
	e.lhsEntry.SetText(cur.LHS)

	e.opSelect = widget.NewSelect(botOptionLabels(botConditionOperatorOptions), nil)
	e.opSelect.SetSelected(botOptionLabel(botConditionOperatorOptions, cur.Operator))

	e.rhsEntry = widget.NewSelectEntry(nil)
	e.rhsEntry.SetPlaceHolder("Comparison value, OBS scene, or %value%")
	e.rhsEntry.SetText(cur.RHS)

	e.rhsRefreshBtn = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		e.syncRightValueOptions(true)
	})
	e.rhsRefreshBtn.Importance = widget.LowImportance

	e.rhsHintLbl = widget.NewLabel("")
	e.rhsHintLbl.TextStyle = fyne.TextStyle{Italic: true}
	e.rhsHintLbl.Wrapping = fyne.TextWrapWord

	e.lhsEntry.OnChanged = func(val string) {
		if e.syncing {
			return
		}
		if mapped, ok := matchLHSPresetLabel(val); ok {
			e.syncing = true
			e.lhsEntry.SetText(mapped)
			e.syncing = false
		}
		e.syncRightValueOptions(false)
	}

	e.opSelect.OnChanged = func(sel string) {
		opKey := botOptionKey(botConditionOperatorOptions, sel)
		if opKey == "empty" || opKey == "not_empty" {
			e.rhsEntry.Disable()
		} else {
			e.rhsEntry.Enable()
		}
	}
	if opKey := botOptionKey(botConditionOperatorOptions, e.opSelect.Selected); opKey == "empty" || opKey == "not_empty" {
		e.rhsEntry.Disable()
	}

	e.thenBranch = e.buildBranchUI(cur.ThenSubActions)
	e.elseBranch = e.buildBranchUI(cur.ElseSubActions)

	lhsRow := withBotVarHelpForSelectEntry(e.win, "Left Value", e.lhsEntry, e.onStatus)
	rhsHelp := newTemplateHelpMarker(e.win, "bot_vars", "Right Value", BotTemplateVars(), e.onStatus, &e.rhsEntry.Entry)
	rhsRow := container.NewBorder(nil, nil, nil, container.NewHBox(e.rhsRefreshBtn, container.NewCenter(rhsHelp)), e.rhsEntry)

	form := widget.NewForm(
		widget.NewFormItem("Left Value", lhsRow),
		widget.NewFormItem("Operator", e.opSelect),
		widget.NewFormItem("Right Value", rhsRow),
		widget.NewFormItem("", e.rhsHintLbl),
		widget.NewFormItem("If True (Then)", e.thenBranch.modeSelect),
		widget.NewFormItem("Then Action", e.thenBranch.actionRow),
		widget.NewFormItem("If False (Else)", e.elseBranch.modeSelect),
		widget.NewFormItem("Else Action", e.elseBranch.actionRow),
	)
	e.root = container.NewVBox(form)
}

func (e *botConditionEditor) buildBranchUI(steps []config.BotSubActionDef) *conditionBranchUI {
	mode, targetID, customSteps := decodeConditionBranch(steps)
	b := &conditionBranchUI{
		customSteps: customSteps,
	}

	opts := botOptionLabels(botConditionBranchModeOptions)
	if mode == "custom" {
		b.customLabel = fmt.Sprintf("%s (%d)", keepCustomBranchPrefix, len(customSteps))
		opts = append([]string{b.customLabel}, opts...)
	}

	b.actSelect = widget.NewSelect(e.actLabels, nil)
	if lbl, ok := e.idToAct[targetID]; ok {
		b.actSelect.SetSelected(lbl)
	} else if len(e.actLabels) > 0 {
		b.actSelect.SetSelected(e.actLabels[0])
	}
	if len(e.actLabels) == 0 {
		b.actSelect.PlaceHolder = "No actions created yet"
		b.actSelect.Disable()
	}
	b.actionRow = container.NewVBox(b.actSelect)

	b.modeSelect = widget.NewSelect(opts, func(sel string) {
		key := e.branchModeKey(b, sel)
		if key == "run_action" || key == "run_action_break" {
			b.actionRow.Show()
		} else {
			b.actionRow.Hide()
		}
	})

	if mode == "custom" {
		b.modeSelect.SetSelected(b.customLabel)
	} else {
		b.modeSelect.SetSelected(botOptionLabel(botConditionBranchModeOptions, mode))
	}
	if mode == "run_action" || mode == "run_action_break" {
		b.actionRow.Show()
	} else {
		b.actionRow.Hide()
	}
	return b
}

func (e *botConditionEditor) branchModeKey(b *conditionBranchUI, selected string) string {
	if b.customLabel != "" && selected == b.customLabel {
		return "custom"
	}
	return botOptionKey(botConditionBranchModeOptions, selected)
}

func (e *botConditionEditor) normalizedLHS() string {
	raw := strings.TrimSpace(e.lhsEntry.Text)
	if mapped, ok := matchLHSPresetLabel(raw); ok {
		return mapped
	}
	return raw
}

func (e *botConditionEditor) syncRightValueOptions(forceRefresh bool) {
	lhs := e.normalizedLHS()
	switch {
	case isOBSSceneToken(lhs):
		e.rhsRefreshBtn.Show()
		if e.obsClient == nil {
			e.rhsRefreshBtn.Disable()
			e.rhsEntry.SetOptions(nil)
			e.rhsHintLbl.SetText("⚪ Connect OBS in Bot → OBS Studio to populate scene dropdowns (or type a scene name / %value%).")
			return
		}
		snap := e.obsClient.DiscoverySnapshot()
		if len(snap.Scenes) > 0 {
			e.mu.Lock()
			e.obsScenes = slices.Clone(snap.Scenes)
			e.mu.Unlock()
			e.rhsEntry.SetOptions(snap.Scenes)
		}
		if !e.obsClient.IsConnected() {
			e.rhsRefreshBtn.Disable()
			if len(snap.Scenes) == 0 {
				e.rhsEntry.SetOptions(nil)
			}
			e.rhsHintLbl.SetText("⚪ OBS disconnected — connect in Bot → OBS Studio to list scenes, or type a scene name.")
			return
		}
		e.rhsRefreshBtn.Enable()
		gen := e.scenesGen.Add(1)
		go func() {
			reqCtx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
			defer cancel()
			scenes, err := e.obsClient.ListScenes(reqCtx, forceRefresh)
			if err != nil || e.ctx.Err() != nil || e.scenesGen.Load() != gen {
				return
			}
			e.mu.Lock()
			e.obsScenes = slices.Clone(scenes)
			e.mu.Unlock()
			fyne.Do(func() {
				if e.ctx.Err() != nil || e.scenesGen.Load() != gen {
					return
				}
				e.rhsEntry.SetOptions(scenes)
				e.rhsHintLbl.SetText(fmt.Sprintf("🟢 Pick from %d OBS scenes or type a scene name / %%value%%.", len(scenes)))
			})
		}()
		if len(snap.Scenes) > 0 {
			e.rhsHintLbl.SetText(fmt.Sprintf("🟢 Pick from %d OBS scenes or type a scene name / %%value%%.", len(snap.Scenes)))
		} else {
			e.rhsHintLbl.SetText("🟡 Loading OBS scenes...")
		}

	case isOBSConnectedToken(lhs):
		e.rhsRefreshBtn.Hide()
		e.rhsEntry.SetOptions([]string{"true", "false"})
		e.rhsHintLbl.SetText("Choose true/false or enter a %value%.")

	default:
		e.rhsRefreshBtn.Hide()
		e.rhsEntry.SetOptions(nil)
		e.rhsHintLbl.SetText("Enter a comparison value or click ? to insert %values%.")
	}
}

// ApplyTo writes the condition comparison and Then/Else branch sub-actions onto dst.
func (e *botConditionEditor) ApplyTo(dst *config.BotSubActionDef) {
	if dst == nil {
		return
	}
	dst.LHS = e.normalizedLHS()
	dst.Operator = botOptionKey(botConditionOperatorOptions, e.opSelect.Selected)
	if dst.Operator == "empty" || dst.Operator == "not_empty" {
		dst.RHS = ""
	} else {
		dst.RHS = e.rhsEntry.Text
	}
	dst.ThenSubActions = e.encodeBranch(e.thenBranch)
	dst.ElseSubActions = e.encodeBranch(e.elseBranch)
}

func (e *botConditionEditor) encodeBranch(b *conditionBranchUI) []config.BotSubActionDef {
	if b == nil {
		return nil
	}
	mode := e.branchModeKey(b, b.modeSelect.Selected)
	targetID := e.actToID[b.actSelect.Selected]
	switch mode {
	case "custom":
		return slices.Clone(b.customSteps)
	case "break":
		return []config.BotSubActionDef{
			{ID: newBotItemID("step"), Kind: "break", Enabled: true},
		}
	case "run_action":
		if strings.TrimSpace(targetID) == "" {
			return nil
		}
		return []config.BotSubActionDef{
			{ID: newBotItemID("step"), Kind: "run_action", Enabled: true, TargetActionID: targetID},
		}
	case "run_action_break":
		var out []config.BotSubActionDef
		if strings.TrimSpace(targetID) != "" {
			out = append(out, config.BotSubActionDef{ID: newBotItemID("step"), Kind: "run_action", Enabled: true, TargetActionID: targetID})
		}
		out = append(out, config.BotSubActionDef{ID: newBotItemID("step"), Kind: "break", Enabled: true})
		return out
	default:
		return nil
	}
}

func decodeConditionBranch(steps []config.BotSubActionDef) (mode, targetActionID string, custom []config.BotSubActionDef) {
	switch len(steps) {
	case 0:
		return "continue", "", nil
	case 1:
		if strings.EqualFold(steps[0].Kind, "break") {
			return "break", "", nil
		}
		if strings.EqualFold(steps[0].Kind, "run_action") {
			return "run_action", steps[0].TargetActionID, nil
		}
	case 2:
		if strings.EqualFold(steps[0].Kind, "run_action") && strings.EqualFold(steps[1].Kind, "break") {
			return "run_action_break", steps[0].TargetActionID, nil
		}
	}
	return "custom", "", slices.Clone(steps)
}

func matchLHSPresetLabel(val string) (string, bool) {
	trimmed := strings.TrimSpace(val)
	for _, opt := range botConditionLHSPresetOptions {
		if opt.Label == trimmed {
			return opt.Key, true
		}
	}
	return "", false
}

func isOBSSceneToken(lhs string) bool {
	switch strings.ToLower(strings.TrimSpace(lhs)) {
	case "%obs.scene%", "%obsscene%",
		"%obs.previousscene%", "%obspreviousscene%",
		"%obs.previewscene%", "%obspreviewscene%":
		return true
	default:
		return false
	}
}

func isOBSConnectedToken(lhs string) bool {
	switch strings.ToLower(strings.TrimSpace(lhs)) {
	case "%obs.connected%", "%obsconnected%":
		return true
	default:
		return false
	}
}
