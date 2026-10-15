package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// ViewersTabView provides chatter voice override management, nickname mapping, and ignore rules.
type ViewersTabView struct {
	window  fyne.Window
	session *app.Session

	searchEntry *widget.Entry
	tableList   *fyne.Container
	onStatus    func(msg string)
}

// NewViewersTabView constructs a ViewersTabView.
func NewViewersTabView(w fyne.Window, session *app.Session) *ViewersTabView {
	return &ViewersTabView{
		window:    w,
		session:   session,
		tableList: container.NewVBox(),
	}
}

// SetStatusHandler registers a callback invoked when viewer overrides are added, edited, or deleted.
func (v *ViewersTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *ViewersTabView) setStatus(msg string) {
	fyne.Do(func() {
		if v.onStatus != nil {
			v.onStatus(msg)
		}
	})
}

// Build creates the Fyne CanvasObject for the Viewers tab.
func (v *ViewersTabView) Build() fyne.CanvasObject {
	v.searchEntry = widget.NewEntry()
	v.searchEntry.SetPlaceHolder("Search chatter overrides by name or ID...")
	v.searchEntry.OnChanged = func(s string) {
		v.refreshTable()
	}

	addBtn := widget.NewButtonWithIcon("Add Chatter Override", theme.ContentAddIcon(), func() {
		v.showEditDialog(nil)
	})
	addBtn.Importance = widget.HighImportance

	topBar := container.NewBorder(nil, nil, widget.NewLabel("Search:"), addBtn, v.searchEntry)

	v.refreshTable()

	content := container.NewBorder(
		topBar,
		nil,
		nil,
		nil,
		container.NewVScroll(v.tableList),
	)

	return content
}

// Refresh reloads the viewer overrides table from active session configuration.
func (v *ViewersTabView) Refresh() {
	fyne.Do(func() {
		v.refreshTable()
	})
}

func (v *ViewersTabView) refreshTable() {
	v.tableList.Objects = nil

	cfg := v.session.Config()
	query := ""
	if v.searchEntry != nil {
		query = strings.ToLower(strings.TrimSpace(v.searchEntry.Text))
	}

	var keys []string
	for k := range cfg.Viewers {
		vc := cfg.Viewers[k]
		if query == "" || strings.Contains(strings.ToLower(k), query) ||
			strings.Contains(strings.ToLower(vc.Nickname), query) ||
			strings.Contains(strings.ToLower(vc.UserID), query) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	if len(keys) == 0 {
		v.tableList.Add(widget.NewLabelWithStyle("No viewer overrides configured.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
		v.tableList.Refresh()
		return
	}

	for _, k := range keys {
		key := k
		vc := cfg.Viewers[key]
		row := v.buildViewerRow(key, vc)
		v.tableList.Add(row)
	}

	v.tableList.Refresh()
}

func (v *ViewersTabView) buildViewerRow(key string, vc config.ViewerConfig) fyne.CanvasObject {
	title := fmt.Sprintf("[%s] %s", strings.ToUpper(vc.Platform), vc.UserID)
	if vc.Nickname != "" {
		title += fmt.Sprintf(" (Spoken as: %q)", vc.Nickname)
	}

	statusText := ""
	if vc.Ignored {
		statusText = "🔇 Ignored"
	} else if vc.Allowed {
		statusText = "⭐ Allowed"
	}

	subtitle := fmt.Sprintf("Voice: %s | Speed: %.1fx %s", vc.Voice, vc.Speed, statusText)

	label := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	sub := widget.NewLabel(subtitle)

	editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		copyVC := vc
		v.showEditDialog(&copyVC)
	})

	deleteBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete Override", "Remove voice override for "+vc.UserID+"?", func(ok bool) {
			if !ok {
				return
			}
			v.deleteViewer(key)
		}, v.window)
	})

	actions := container.NewHBox(editBtn, deleteBtn)
	info := container.NewVBox(label, sub)

	return container.NewVBox(
		container.NewBorder(nil, nil, info, actions),
		widget.NewSeparator(),
	)
}

func (v *ViewersTabView) showEditDialog(existing *config.ViewerConfig) {
	isEdit := existing != nil

	platformSelect := widget.NewSelect([]string{"twitch", "youtube", "kick", "tiktok"}, nil)
	platformSelect.SetSelected("twitch")

	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("Chatter username or stable user ID")

	nickEntry := widget.NewEntry()
	nickEntry.SetPlaceHolder("Spoken nickname (leave blank for actual name)")

	voices := tts.KokoroVoiceNames
	voiceSelect := widget.NewSelect(voices, nil)
	voiceSelect.SetSelected("af_bella")

	speedLabel := widget.NewLabel("1.0x")
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.SetValue(1.0)
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.1fx", val))
	}

	ignoredCheck := widget.NewCheck("Ignore Chatter (silence completely)", nil)
	allowedCheck := widget.NewCheck("Always Allow (bypass bot filter)", nil)

	if isEdit {
		platformSelect.SetSelected(existing.Platform)
		userEntry.SetText(existing.UserID)
		userEntry.Disable()
		nickEntry.SetText(existing.Nickname)
		voiceSelect.SetSelected(existing.Voice)
		if existing.Speed > 0 {
			speedSlider.SetValue(float64(existing.Speed))
			speedLabel.SetText(fmt.Sprintf("%.1fx", existing.Speed))
		}
		ignoredCheck.SetChecked(existing.Ignored)
		allowedCheck.SetChecked(existing.Allowed)
	}

	form := container.NewVBox(
		widget.NewLabel("Platform:"),
		platformSelect,
		widget.NewLabel("Chatter Username / User ID:"),
		userEntry,
		widget.NewLabel("Spoken Nickname:"),
		nickEntry,
		widget.NewLabel("Assigned Voice:"),
		voiceSelect,
		container.NewBorder(nil, nil, widget.NewLabel("Speed:"), speedLabel, speedSlider),
		ignoredCheck,
		allowedCheck,
	)

	title := "Add Chatter Override"
	if isEdit {
		title = "Edit Chatter Override"
	}

	dialog.ShowCustomConfirm(title, "Save", "Cancel", form, func(confirmed bool) {
		if !confirmed {
			return
		}
		userID := strings.TrimSpace(userEntry.Text)
		if userID == "" {
			dialog.ShowError(errors.New("username or user ID cannot be empty"), v.window)
			return
		}

		plat := platformSelect.Selected
		key := config.ViewerKey(plat, userID)

		vc := config.ViewerConfig{
			Platform: plat,
			UserID:   userID,
			Voice:    voiceSelect.Selected,
			Speed:    float32(speedSlider.Value),
			Nickname: strings.TrimSpace(nickEntry.Text),
			Ignored:  ignoredCheck.Checked,
			Allowed:  allowedCheck.Checked,
		}

		_ = v.session.UpdateConfig(func(cfg *config.Config) {
			if cfg.Viewers == nil {
				cfg.Viewers = make(map[string]config.ViewerConfig)
			}
			cfg.Viewers[key] = vc
		})

		v.refreshTable()
		v.setStatus("✓ Saved override for " + userID)
	}, v.window)
}

func (v *ViewersTabView) deleteViewer(key string) {
	_ = v.session.UpdateConfig(func(cfg *config.Config) {
		delete(cfg.Viewers, key)
		delete(cfg.Viewers, strings.ToLower(key))
	})
	v.refreshTable()
	v.setStatus("✓ Override removed.")
}
