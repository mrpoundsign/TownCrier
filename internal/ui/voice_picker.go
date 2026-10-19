package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

const defaultPreviewSampleText = "Hello! I am reading your livestream chat with natural neural speech."

// VoiceGroup represents a model family section and its selectable voices inside the Voice & Speed modal picker.
type VoiceGroup struct {
	FamilyID  string
	ShortName string
	Voices    []tts.VoiceOption
}

// BuildVoiceGroupsForModels returns voice options grouped by enabled model families.
// If currentVoiceID belongs to a model family not currently enabled, that voice is included
// so existing assignments remain visible and selectable.
func BuildVoiceGroupsForModels(enabledModels []string, currentVoiceID string) []VoiceGroup {
	if len(enabledModels) == 0 {
		enabledModels = []string{tts.ModelKokoro}
	}
	enabledSet := make(map[string]bool, len(enabledModels))
	for _, m := range enabledModels {
		clean := strings.ToLower(strings.TrimSpace(m))
		if clean != "" {
			enabledSet[clean] = true
		}
	}

	var groups []VoiceGroup
	foundCurrent := false
	canonCurrent := ""
	if strings.TrimSpace(currentVoiceID) != "" {
		canonCurrent = tts.VoiceIDFromLabel(currentVoiceID)
	}

	for _, spec := range tts.AllModelFamilies {
		if !enabledSet[spec.ID] {
			continue
		}
		voices := slices.Clone(spec.Voices)
		for _, vo := range voices {
			if vo.ID == canonCurrent {
				foundCurrent = true
			}
		}
		groups = append(groups, VoiceGroup{
			FamilyID:  spec.ID,
			ShortName: spec.ShortName,
			Voices:    voices,
		})
	}

	if len(groups) == 0 {
		if spec, ok := tts.ModelSpecByID(tts.ModelKokoro); ok {
			voices := slices.Clone(spec.Voices)
			for _, vo := range voices {
				if vo.ID == canonCurrent {
					foundCurrent = true
				}
			}
			groups = append(groups, VoiceGroup{
				FamilyID:  spec.ID,
				ShortName: spec.ShortName,
				Voices:    voices,
			})
		}
	}

	if canonCurrent != "" && !foundCurrent {
		fam := tts.VoiceFamily(canonCurrent)
		shortName := fam + " (Currently Assigned)"
		if spec, ok := tts.ModelSpecByID(fam); ok {
			shortName = spec.ShortName + " (Currently Assigned)"
		}
		groups = append([]VoiceGroup{
			{
				FamilyID:  fam,
				ShortName: shortName,
				Voices: []tts.VoiceOption{
					{
						ID:    canonCurrent,
						Label: tts.VoiceDisplayName(canonCurrent),
					},
				},
			},
		}, groups...)
	}

	return groups
}

// FormatVoiceSpeedSummary formats a voice ID and speed multiplier for inline display next to an Edit button.
func FormatVoiceSpeedSummary(voiceID string, speed float32) string {
	id := tts.VoiceIDFromLabel(voiceID)
	if speed <= 0 {
		speed = 1.0
	}
	return fmt.Sprintf("%s  •  %.2fx", tts.VoiceDisplayName(id), speed)
}

// VoiceSpeedField provides an inline summary label and an Edit button that opens ShowVoiceSpeedDialog.
type VoiceSpeedField struct {
	Container *fyne.Container
	Label     *widget.Label
	EditBtn   *widget.Button

	window      fyne.Window
	session     *app.Session
	dialogTitle string
	voiceID     string
	speed       float32
	onChanged   func(voiceID string, speed float32)
	onStatus    func(msg string)
}

// NewVoiceSpeedField creates an inline voice and speed display paired with an Edit button.
func NewVoiceSpeedField(
	w fyne.Window,
	session *app.Session,
	dialogTitle string,
	initialVoice string,
	initialSpeed float32,
	onChanged func(voiceID string, speed float32),
	onStatus func(msg string),
) *VoiceSpeedField {
	if initialSpeed <= 0 {
		initialSpeed = 1.0
	}
	canonVoice := tts.VoiceIDFromLabel(initialVoice)
	if strings.TrimSpace(initialVoice) == "" && session != nil {
		labels := tts.VoiceDisplayLabelsForModels(session.Config().TTS.EffectiveEnabledModels())
		if len(labels) > 0 {
			canonVoice = tts.VoiceIDFromLabel(labels[0])
		}
	}

	f := &VoiceSpeedField{
		window:      w,
		session:     session,
		dialogTitle: dialogTitle,
		voiceID:     canonVoice,
		speed:       initialSpeed,
		onChanged:   onChanged,
		onStatus:    onStatus,
	}

	f.Label = widget.NewLabel(FormatVoiceSpeedSummary(f.voiceID, f.speed))
	f.Label.Truncation = fyne.TextTruncateEllipsis

	f.EditBtn = widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		ShowVoiceSpeedDialog(
			f.window,
			f.session,
			f.dialogTitle,
			f.voiceID,
			f.speed,
			func(newVoiceID string, newSpeed float32) {
				f.SetVoiceAndSpeed(newVoiceID, newSpeed)
				if f.onChanged != nil {
					f.onChanged(f.voiceID, f.speed)
				}
			},
			f.onStatus,
		)
	})

	f.Container = container.NewBorder(nil, nil, nil, f.EditBtn, f.Label)
	return f
}

// SetVoiceAndSpeed updates the field's current voice ID and speed and refreshes its summary label.
func (f *VoiceSpeedField) SetVoiceAndSpeed(voiceID string, speed float32) {
	if speed <= 0 {
		speed = 1.0
	}
	f.voiceID = tts.VoiceIDFromLabel(voiceID)
	f.speed = speed
	if f.Label != nil {
		f.Label.SetText(FormatVoiceSpeedSummary(f.voiceID, f.speed))
		f.Label.Refresh()
	}
}

// VoiceID returns the currently selected canonical voice ID.
func (f *VoiceSpeedField) VoiceID() string {
	return f.voiceID
}

// Speed returns the currently selected speech speed multiplier.
func (f *VoiceSpeedField) Speed() float32 {
	if f.speed <= 0 {
		return 1.0
	}
	return f.speed
}

// Enable enables the Edit button.
func (f *VoiceSpeedField) Enable() {
	if f.EditBtn != nil {
		f.EditBtn.Enable()
	}
}

// Disable disables the Edit button.
func (f *VoiceSpeedField) Disable() {
	if f.EditBtn != nil {
		f.EditBtn.Disable()
	}
}

// Disabled reports whether the Edit button is currently disabled.
func (f *VoiceSpeedField) Disabled() bool {
	if f.EditBtn == nil {
		return false
	}
	return f.EditBtn.Disabled()
}

// ShowVoiceSpeedDialog opens a modal dialog to select a voice (grouped by enabled model packs with no dropdowns),
// adjust speech speed, and preview the voice with sample text before saving.
func ShowVoiceSpeedDialog(
	w fyne.Window,
	session *app.Session,
	title string,
	initialVoice string,
	initialSpeed float32,
	onSave func(voiceID string, speed float32),
	onStatus func(msg string),
) {
	if w == nil {
		return
	}
	if title == "" {
		title = "Edit Voice & Speed"
	}
	if initialSpeed <= 0 {
		initialSpeed = 1.0
	}

	var enabledModels []string
	if session != nil {
		enabledModels = session.Config().TTS.EffectiveEnabledModels()
	}

	groups := BuildVoiceGroupsForModels(enabledModels, initialVoice)
	selectedVoice := tts.VoiceIDFromLabel(initialVoice)
	if strings.TrimSpace(initialVoice) == "" && len(groups) > 0 && len(groups[0].Voices) > 0 {
		selectedVoice = groups[0].Voices[0].ID
	}

	selectedHeader := widget.NewLabelWithStyle(
		"Selected Voice: "+tts.VoiceDisplayName(selectedVoice),
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)

	type voiceBtnEntry struct {
		id  string
		btn *widget.Button
	}
	var allVoiceBtns []voiceBtnEntry

	refreshSelectionUI := func() {
		selectedHeader.SetText("Selected Voice: " + tts.VoiceDisplayName(selectedVoice))
		for _, entry := range allVoiceBtns {
			if entry.id == selectedVoice {
				entry.btn.SetIcon(theme.RadioButtonCheckedIcon())
				entry.btn.Importance = widget.HighImportance
			} else {
				entry.btn.SetIcon(theme.RadioButtonIcon())
				entry.btn.Importance = widget.LowImportance
			}
			entry.btn.Refresh()
		}
	}

	voiceListBox := container.NewVBox()
	for gi, grp := range groups {
		if gi > 0 {
			voiceListBox.Add(widget.NewSeparator())
		}
		voiceListBox.Add(NewModelColorBadge(grp.FamilyID, grp.ShortName, true))
		for _, vo := range grp.Voices {
			opt := vo
			btn := widget.NewButtonWithIcon(opt.Label, theme.RadioButtonIcon(), nil)
			btn.Alignment = widget.ButtonAlignLeading
			btn.OnTapped = func() {
				selectedVoice = opt.ID
				refreshSelectionUI()
			}
			allVoiceBtns = append(allVoiceBtns, voiceBtnEntry{id: opt.ID, btn: btn})
			voiceListBox.Add(btn)
		}
	}
	refreshSelectionUI()

	voiceScroll := newPanelVScroll(voiceListBox)
	voiceScroll.SetMinSize(fyne.NewSize(500, 230))

	speedLabel := widget.NewLabel(fmt.Sprintf("%.2fx", initialSpeed))
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.Step = 0.05
	speedSlider.SetValue(float64(initialSpeed))
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.2fx", val))
	}

	sampleEntry := NewScrollPassEntry()
	sampleEntry.SetText(defaultPreviewSampleText)

	var previewBtn *widget.Button
	previewBtn = widget.NewButtonWithIcon("Preview Voice", theme.MediaPlayIcon(), func() {
		SynthesizeAndPlayPreview(
			session,
			sampleEntry.Text,
			selectedVoice,
			float32(speedSlider.Value),
			previewBtn,
			onStatus,
		)
	})
	previewBtn.Importance = widget.HighImportance

	bottomControls := container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, widget.NewLabelWithStyle("Speed:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), speedLabel, speedSlider),
		widget.NewLabelWithStyle("Preview Sample Text:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		sampleEntry,
		previewBtn,
	)

	content := container.NewBorder(
		container.NewVBox(selectedHeader, widget.NewSeparator()),
		bottomControls,
		nil,
		nil,
		voiceScroll,
	)

	dlg := dialog.NewCustomConfirm(title, "Save", "Cancel", content, func(confirmed bool) {
		if !confirmed {
			return
		}
		if onSave != nil {
			onSave(selectedVoice, float32(speedSlider.Value))
		}
	}, w)
	dlg.Resize(fyne.NewSize(580, 540))
	dlg.Show()
}

// SynthesizeAndPlayPreview synthesizes sample text with the given voice and speed and plays it through the session audio player.
func SynthesizeAndPlayPreview(
	session *app.Session,
	text string,
	voice string,
	speed float32,
	previewBtn *widget.Button,
	onStatus func(msg string),
) {
	reportStatus := func(msg string) {
		if onStatus != nil {
			fyne.Do(func() {
				onStatus(msg)
			})
		}
	}

	if session == nil {
		reportStatus("TTS session not initialized.")
		return
	}
	eng := session.Engine()
	if eng == nil {
		reportStatus("TTS engine not loaded.")
		return
	}

	player := session.Player()
	if previewBtn != nil {
		previewBtn.Disable()
	}
	reportStatus(fmt.Sprintf("Synthesizing preview with '%s'...", voice))

	go func() {
		t0 := time.Now()
		if player != nil {
			player.ResetPanic()
		}

		if streamEng, ok := eng.(tts.StreamingEngine); ok {
			startedPlaying := false
			var preBuffer []float32
			preBufferSR := 24000
			var ttfa time.Duration

			err := streamEng.SynthesizeStream(text, voice, speed, func(samples []float32, sampleRate int) bool {
				if len(samples) == 0 {
					return true
				}
				if sampleRate > 0 {
					preBufferSR = sampleRate
				}
				minPreBufferSamples := preBufferSR * 9 / 10
				if !startedPlaying {
					preBuffer = append(preBuffer, samples...)
					if len(preBuffer) >= minPreBufferSamples {
						startedPlaying = true
						ttfa = time.Since(t0)
						reportStatus(fmt.Sprintf("First audio in %v — playing preview...", ttfa.Round(time.Millisecond)))
						if player != nil {
							_ = player.Play(preBuffer, preBufferSR)
						}
						preBuffer = nil
					}
					return true
				}
				if player != nil {
					_ = player.Play(samples, sampleRate)
				}
				return true
			})
			totalDur := time.Since(t0)
			if !startedPlaying && len(preBuffer) > 0 {
				ttfa = totalDur
				if player != nil && err == nil {
					_ = player.Play(preBuffer, preBufferSR)
				}
			}
			fyne.Do(func() {
				if previewBtn != nil {
					previewBtn.Enable()
				}
				if err != nil {
					if onStatus != nil {
						onStatus(fmt.Sprintf("Synthesis failed: %v", err))
					}
					return
				}
				if onStatus != nil {
					onStatus(fmt.Sprintf("Synthesized in %v (first audio %v). Playing preview...", totalDur.Round(time.Millisecond), ttfa.Round(time.Millisecond)))
				}
			})
			return
		}

		samples, sampleRate, err := eng.Synthesize(text, voice, speed)
		dur := time.Since(t0)

		fyne.Do(func() {
			if previewBtn != nil {
				previewBtn.Enable()
			}
			if err != nil {
				if onStatus != nil {
					onStatus(fmt.Sprintf("Synthesis failed: %v", err))
				}
				return
			}

			if onStatus != nil {
				onStatus(fmt.Sprintf("Synthesized in %v. Playing preview...", dur.Round(time.Millisecond)))
			}
			if player != nil {
				_ = player.Play(samples, sampleRate)
			}
		})
	}()
}
