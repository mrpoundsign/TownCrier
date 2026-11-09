package ui

import (
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/tts"
)

// VoiceReplacementWizard manages the interactive modal for reassigning voices when disabling an in-use TTS model pack.
type VoiceReplacementWizard struct {
	window          fyne.Window
	session         *app.Session
	cfg             *config.Config
	usages          []tts.VoiceUsage
	remainingModels []string
	replacements    []tts.VoiceReplacement
	bulkVoiceID     string
	bulkSpeed       float32

	bulkSummaryLabel  *widget.Label
	bulkPickBtn       *widget.Button
	applyAllBtn       *widget.Button
	clearOverridesBtn *widget.Button
	rowTargetLabels   []*widget.Label
	rowPickBtns       []*widget.Button
	rowUseDefaultBtns []*widget.Button
	confirmBtn        *widget.Button
	cancelBtn         *widget.Button
	dlg               dialog.Dialog

	onConfirm func(replacements []tts.VoiceReplacement)
	onCancel  func()
	onStatus  func(msg string)
}

// ShowVoiceReplacementWizard constructs and displays the Guided Replacement Voice Assignment Wizard modal.
func ShowVoiceReplacementWizard(
	w fyne.Window,
	session *app.Session,
	cfg *config.Config,
	usages []tts.VoiceUsage,
	remainingModels []string,
	onConfirm func(replacements []tts.VoiceReplacement),
	onCancel func(),
	onStatus func(msg string),
) *VoiceReplacementWizard {
	wiz := NewVoiceReplacementWizard(w, session, cfg, usages, remainingModels, onConfirm, onCancel, onStatus)
	wiz.Show()
	return wiz
}

// NewVoiceReplacementWizard constructs a VoiceReplacementWizard without immediately showing the modal.
func NewVoiceReplacementWizard(
	w fyne.Window,
	session *app.Session,
	cfg *config.Config,
	usages []tts.VoiceUsage,
	remainingModels []string,
	onConfirm func(replacements []tts.VoiceReplacement),
	onCancel func(),
	onStatus func(msg string),
) *VoiceReplacementWizard {
	if cfg == nil && session != nil {
		cfg = session.Config()
	}
	defVoice := tts.DefaultReplacementVoice(cfg, remainingModels)
	defSpeed := float32(1.0)
	if cfg != nil && cfg.GlobalChatRules.DefaultSpeed > 0 {
		defSpeed = cfg.GlobalChatRules.DefaultSpeed
	}

	replacements := make([]tts.VoiceReplacement, len(usages))
	for i, u := range usages {
		spd := u.Speed
		if spd <= 0 {
			spd = defSpeed
		}
		replacements[i] = tts.VoiceReplacement{
			VoiceID: defVoice,
			Speed:   spd,
		}
	}

	wiz := &VoiceReplacementWizard{
		window:            w,
		session:           session,
		cfg:               cfg,
		usages:            slices.Clone(usages),
		remainingModels:   slices.Clone(remainingModels),
		replacements:      replacements,
		bulkVoiceID:       defVoice,
		bulkSpeed:         defSpeed,
		rowTargetLabels:   make([]*widget.Label, len(usages)),
		rowPickBtns:       make([]*widget.Button, len(usages)),
		rowUseDefaultBtns: make([]*widget.Button, len(usages)),
		onConfirm:         onConfirm,
		onCancel:          onCancel,
		onStatus:          onStatus,
	}
	wiz.build()
	return wiz
}

func (wiz *VoiceReplacementWizard) formatRowTargetText(idx int) string {
	if idx < 0 || idx >= len(wiz.replacements) {
		return ""
	}
	rep := wiz.replacements[idx]
	if strings.TrimSpace(rep.VoiceID) == "" {
		return "Inherit Global Default (Override Cleared)"
	}
	return FormatVoiceSpeedSummary(rep.VoiceID, rep.Speed)
}

func (wiz *VoiceReplacementWizard) refreshRowUI(idx int) {
	if idx < 0 || idx >= len(wiz.usages) {
		return
	}
	if lbl := wiz.rowTargetLabels[idx]; lbl != nil {
		lbl.SetText(wiz.formatRowTargetText(idx))
		lbl.Refresh()
	}
	if defBtn := wiz.rowUseDefaultBtns[idx]; defBtn != nil {
		if strings.TrimSpace(wiz.replacements[idx].VoiceID) == "" {
			defBtn.Disable()
		} else {
			defBtn.Enable()
		}
		defBtn.Refresh()
	}
}

func (wiz *VoiceReplacementWizard) refreshAllRowsUI() {
	if wiz.bulkSummaryLabel != nil {
		wiz.bulkSummaryLabel.SetText(FormatVoiceSpeedSummary(wiz.bulkVoiceID, wiz.bulkSpeed))
		wiz.bulkSummaryLabel.Refresh()
	}
	for i := range wiz.usages {
		wiz.refreshRowUI(i)
	}
}

// SetBulkVoice updates the bulk replacement voice and speed in the wizard.
func (wiz *VoiceReplacementWizard) SetBulkVoice(voiceID string, speed float32) {
	if speed <= 0 {
		speed = 1.0
	}
	wiz.bulkVoiceID = tts.VoiceIDFromLabel(voiceID)
	wiz.bulkSpeed = speed
	if wiz.bulkSummaryLabel != nil {
		wiz.bulkSummaryLabel.SetText(FormatVoiceSpeedSummary(wiz.bulkVoiceID, wiz.bulkSpeed))
		wiz.bulkSummaryLabel.Refresh()
	}
}

// SetRowReplacement updates the replacement voice and speed for a specific usage row index.
func (wiz *VoiceReplacementWizard) SetRowReplacement(idx int, voiceID string, speed float32) {
	if idx < 0 || idx >= len(wiz.replacements) {
		return
	}
	if speed <= 0 {
		speed = wiz.replacements[idx].Speed
		if speed <= 0 {
			speed = 1.0
		}
	}
	vid := strings.TrimSpace(voiceID)
	if vid != "" {
		vid = tts.VoiceIDFromLabel(vid)
	}
	wiz.replacements[idx] = tts.VoiceReplacement{
		VoiceID: vid,
		Speed:   speed,
	}
	wiz.refreshRowUI(idx)
}

// Replacements returns a copy of the currently configured voice replacements.
func (wiz *VoiceReplacementWizard) Replacements() []tts.VoiceReplacement {
	return slices.Clone(wiz.replacements)
}

func (wiz *VoiceReplacementWizard) build() {
	disabledNames := make([]string, 0, 2)
	seenFam := make(map[string]bool)
	hasNonGlobal := false
	for _, u := range wiz.usages {
		if !seenFam[u.FamilyName] {
			seenFam[u.FamilyName] = true
			disabledNames = append(disabledNames, u.FamilyName)
		}
		if u.Kind != tts.UsageKindGlobal {
			hasNonGlobal = true
		}
	}

	remainingNames := make([]string, 0, len(wiz.remainingModels))
	for _, m := range wiz.remainingModels {
		if spec, ok := tts.ModelSpecByID(m); ok {
			remainingNames = append(remainingNames, spec.ShortName)
		} else {
			remainingNames = append(remainingNames, m)
		}
	}

	introText := fmt.Sprintf(
		"%d assigned voice(s) currently use %s. Reassign them below to voices from your remaining enabled pack(s) (%s), then click 'Reassign & Disable' to finish.",
		len(wiz.usages),
		strings.Join(disabledNames, ", "),
		strings.Join(remainingNames, ", "),
	)
	introLbl := widget.NewLabel(introText)
	introLbl.Wrapping = fyne.TextWrapWord

	// --- Bulk Reassignment Bar ---
	wiz.bulkSummaryLabel = widget.NewLabelWithStyle(
		FormatVoiceSpeedSummary(wiz.bulkVoiceID, wiz.bulkSpeed),
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	wiz.bulkSummaryLabel.Truncation = fyne.TextTruncateEllipsis

	wiz.bulkPickBtn = widget.NewButtonWithIcon("Choose & Preview...", theme.VolumeUpIcon(), func() {
		ShowVoiceSpeedDialogForModels(
			wiz.window,
			wiz.session,
			wiz.remainingModels,
			"Select Bulk Replacement Voice",
			wiz.bulkVoiceID,
			wiz.bulkSpeed,
			func(newVoiceID string, newSpeed float32) {
				wiz.SetBulkVoice(newVoiceID, newSpeed)
			},
			wiz.onStatus,
		)
	})

	wiz.applyAllBtn = widget.NewButtonWithIcon("Apply to All", theme.ConfirmIcon(), func() {
		for i := range wiz.replacements {
			wiz.replacements[i] = tts.VoiceReplacement{
				VoiceID: wiz.bulkVoiceID,
				Speed:   wiz.bulkSpeed,
			}
		}
		wiz.refreshAllRowsUI()
	})
	wiz.applyAllBtn.Importance = widget.HighImportance

	bulkButtons := []fyne.CanvasObject{wiz.bulkPickBtn, wiz.applyAllBtn}

	if hasNonGlobal {
		wiz.clearOverridesBtn = widget.NewButtonWithIcon("Clear Overrides (Use Default)", theme.ContentUndoIcon(), func() {
			for i, u := range wiz.usages {
				if u.Kind == tts.UsageKindGlobal {
					wiz.replacements[i] = tts.VoiceReplacement{
						VoiceID: wiz.bulkVoiceID,
						Speed:   wiz.bulkSpeed,
					}
				} else {
					wiz.replacements[i].VoiceID = ""
				}
			}
			wiz.refreshAllRowsUI()
		})
		bulkButtons = append(bulkButtons, wiz.clearOverridesBtn)
	}

	bulkRow := container.NewBorder(
		nil,
		nil,
		widget.NewLabelWithStyle("Bulk Voice:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(bulkButtons...),
		wiz.bulkSummaryLabel,
	)
	bulkCard := widget.NewCard(
		"Quick Bulk Reassignment",
		"Assign one replacement voice to all affected items or clear custom overrides to inherit the Global Default Voice",
		bulkRow,
	)

	// --- Per-Item Reassignment List ---
	rowsBox := container.NewVBox()
	for i, u := range wiz.usages {
		rowIdx := i
		usage := u

		locLabel := widget.NewLabelWithStyle(usage.Location, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		fromLabel := widget.NewLabelWithStyle("Current: "+usage.VoiceLabel, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
		arrowLabel := widget.NewLabelWithStyle("Replacement:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

		targetLabel := widget.NewLabel(wiz.formatRowTargetText(rowIdx))
		targetLabel.Truncation = fyne.TextTruncateEllipsis
		wiz.rowTargetLabels[rowIdx] = targetLabel

		pickBtn := widget.NewButtonWithIcon("Change & Preview...", theme.DocumentCreateIcon(), func() {
			initVoice := wiz.replacements[rowIdx].VoiceID
			if strings.TrimSpace(initVoice) == "" {
				initVoice = wiz.bulkVoiceID
			}
			initSpeed := wiz.replacements[rowIdx].Speed
			if initSpeed <= 0 {
				initSpeed = wiz.bulkSpeed
			}
			ShowVoiceSpeedDialogForModels(
				wiz.window,
				wiz.session,
				wiz.remainingModels,
				"Replacement Voice — "+usage.Location,
				initVoice,
				initSpeed,
				func(newVoiceID string, newSpeed float32) {
					wiz.SetRowReplacement(rowIdx, newVoiceID, newSpeed)
				},
				wiz.onStatus,
			)
		})
		wiz.rowPickBtns[rowIdx] = pickBtn

		actionObjs := []fyne.CanvasObject{pickBtn}
		if usage.Kind != tts.UsageKindGlobal {
			useDefBtn := widget.NewButtonWithIcon("Use Default", theme.ContentUndoIcon(), func() {
				wiz.SetRowReplacement(rowIdx, "", wiz.replacements[rowIdx].Speed)
			})
			wiz.rowUseDefaultBtns[rowIdx] = useDefBtn
			actionObjs = append(actionObjs, useDefBtn)
		}

		leftInfo := container.NewVBox(
			locLabel,
			fromLabel,
			container.NewHBox(arrowLabel, targetLabel),
		)
		rowContent := container.NewBorder(
			nil,
			nil,
			nil,
			container.NewVBox(layout.NewSpacer(), container.NewHBox(actionObjs...), layout.NewSpacer()),
			leftInfo,
		)
		rowsBox.Add(rowContent)
		if rowIdx < len(wiz.usages)-1 {
			rowsBox.Add(widget.NewSeparator())
		}
	}

	listScroll := newPanelVScroll(rowsBox)
	listScroll.SetMinSize(fyne.NewSize(640, 240))

	wiz.cancelBtn = widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
		if wiz.dlg != nil {
			wiz.dlg.Hide()
		}
		if wiz.onCancel != nil {
			wiz.onCancel()
		}
	})

	wiz.confirmBtn = widget.NewButtonWithIcon("Reassign & Disable", theme.ConfirmIcon(), func() {
		if wiz.dlg != nil {
			wiz.dlg.Hide()
		}
		if wiz.onConfirm != nil {
			wiz.onConfirm(wiz.Replacements())
		}
	})
	wiz.confirmBtn.Importance = widget.HighImportance

	footer := container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(layout.NewSpacer(), wiz.cancelBtn, wiz.confirmBtn),
	)

	header := container.NewVBox(
		introLbl,
		bulkCard,
		widget.NewLabelWithStyle("Affected Voice Assignments:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	)

	body := container.NewBorder(header, footer, nil, nil, listScroll)

	if wiz.window != nil {
		d := dialog.NewCustomWithoutButtons("Reassign Voices to Disable Model Pack", body, wiz.window)
		d.Resize(fyne.NewSize(720, 560))
		wiz.dlg = d
	}
}

// Show displays the wizard modal dialog on the parent window.
func (wiz *VoiceReplacementWizard) Show() {
	if wiz.dlg != nil {
		wiz.dlg.Show()
	}
}
