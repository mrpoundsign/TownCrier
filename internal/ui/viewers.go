package ui

import (
	"errors"
	"fmt"
	"slices"
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

	searchEntry *ScrollPassEntry
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
	v.searchEntry = NewScrollPassEntry()
	v.searchEntry.SetPlaceHolder("Search chatter overrides by name or ID...")
	v.searchEntry.OnChanged = func(s string) {
		v.refreshTable()
	}

	addBtn := widget.NewButtonWithIcon("Add Chatter Override", theme.ContentAddIcon(), func() {
		v.showEditDialog("", nil)
	})
	addBtn.Importance = widget.HighImportance

	topBar := container.NewBorder(nil, nil, widget.NewLabel("Search:"), addBtn, v.searchEntry)

	v.refreshTable()

	tableScroll := newPanelVScroll(v.tableList)
	v.searchEntry.SetParentScroll(tableScroll)

	content := container.NewBorder(
		topBar,
		nil,
		nil,
		nil,
		tableScroll,
	)

	return content
}

// Refresh reloads the viewer overrides table from active session configuration.
func (v *ViewersTabView) Refresh() {
	fyne.Do(func() {
		v.refreshTable()
	})
}

func viewerMatchesQuery(key string, vc config.ViewerConfig, query string) bool {
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(key), query) ||
		strings.Contains(strings.ToLower(vc.Nickname), query) ||
		strings.Contains(strings.ToLower(vc.UserID), query) {
		return true
	}
	for _, acc := range vc.EffectiveAccounts() {
		if strings.Contains(strings.ToLower(acc.Platform), query) ||
			strings.Contains(strings.ToLower(acc.UserID), query) {
			return true
		}
	}
	return false
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
		if viewerMatchesQuery(k, vc, query) {
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

func formatViewerAccountsTitle(vc config.ViewerConfig) string {
	accs := vc.EffectiveAccounts()
	parts := make([]string, 0, len(accs))
	for _, acc := range accs {
		parts = append(parts, fmt.Sprintf("[%s] %s", strings.ToUpper(acc.Platform), acc.UserID))
	}
	title := strings.Join(parts, "  •  ")
	if title == "" {
		title = fmt.Sprintf("[%s] %s", strings.ToUpper(vc.Platform), vc.UserID)
	}
	if vc.Nickname != "" {
		title += fmt.Sprintf(" (Spoken as: %q)", vc.Nickname)
	}
	return title
}

func (v *ViewersTabView) buildViewerRow(key string, vc config.ViewerConfig) fyne.CanvasObject {
	title := formatViewerAccountsTitle(vc)

	statusText := ""
	if vc.Ignored {
		statusText = "🔇 Ignored"
	} else if vc.Allowed {
		statusText = "⭐ Allowed"
	}

	subtitle := fmt.Sprintf("Voice: %s | Speed: %.2fx %s", tts.VoiceDisplayName(vc.Voice), vc.Speed, statusText)

	label := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	sub := widget.NewLabel(subtitle)

	editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		copyVC := vc
		v.showEditDialog(key, &copyVC)
	})

	deleteBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete Override", "Remove voice override for "+title+"?", func(ok bool) {
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

func (v *ViewersTabView) showEditDialog(existingKey string, existing *config.ViewerConfig) {
	isEdit := existing != nil

	var accounts []config.ViewerAccount
	if isEdit {
		accounts = append(accounts, existing.EffectiveAccounts()...)
	}

	accountsList := container.NewVBox()
	var refreshAccountsUI func()
	refreshAccountsUI = func() {
		accountsList.Objects = nil
		if len(accounts) == 0 {
			accountsList.Add(widget.NewLabelWithStyle("No linked platform accounts yet — enter one below.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
		} else {
			for i, acc := range accounts {
				idx := i
				accText := fmt.Sprintf("• [%s] %s", strings.ToUpper(acc.Platform), acc.UserID)
				rmBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
					accounts = append(accounts[:idx], accounts[idx+1:]...)
					refreshAccountsUI()
				})
				rmBtn.Importance = widget.LowImportance
				row := container.NewBorder(nil, nil, widget.NewLabelWithStyle(accText, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), rmBtn)
				accountsList.Add(row)
			}
		}
		accountsList.Refresh()
	}
	refreshAccountsUI()

	platformSelect := widget.NewSelect([]string{"twitch", "youtube", "kick", "tiktok"}, nil)
	platformSelect.SetSelected("twitch")
	if isEdit && len(accounts) == 1 && accounts[0].Platform == "twitch" {
		platformSelect.SetSelected("youtube")
	}

	userEntry := NewScrollPassEntry()
	userEntry.SetPlaceHolder("Username or User ID (no @ needed)")

	addAccount := func() bool {
		uid := strings.TrimLeft(strings.TrimSpace(userEntry.Text), "@")
		if uid == "" {
			return false
		}
		plat := strings.ToLower(strings.TrimSpace(platformSelect.Selected))
		if plat == "" {
			plat = "twitch"
		}
		for _, a := range accounts {
			if strings.EqualFold(a.Platform, plat) && strings.EqualFold(strings.TrimPrefix(a.UserID, "@"), uid) {
				userEntry.SetText("")
				return true
			}
		}
		accounts = append(accounts, config.ViewerAccount{
			Platform: plat,
			UserID:   uid,
		})
		userEntry.SetText("")
		refreshAccountsUI()
		return true
	}

	addAccBtn := widget.NewButtonWithIcon("Add Platform", theme.ContentAddIcon(), func() {
		_ = addAccount()
	})

	addAccountRow := container.NewBorder(
		nil,
		nil,
		platformSelect,
		addAccBtn,
		userEntry,
	)

	nickEntry := NewScrollPassEntry()
	nickEntry.SetPlaceHolder("Spoken nickname (leave blank for actual name)")

	cfg := v.session.Config()
	voiceLabels := tts.VoiceDisplayLabelsForModels(cfg.TTS.EffectiveEnabledModels())
	if isEdit && existing.Voice != "" {
		curLabel := tts.VoiceDisplayName(existing.Voice)
		if !slices.Contains(voiceLabels, curLabel) {
			voiceLabels = append([]string{curLabel}, voiceLabels...)
		}
	}
	voiceSelect := widget.NewSelect(voiceLabels, nil)
	if len(voiceLabels) > 0 {
		voiceSelect.SetSelected(voiceLabels[0])
	}

	speedLabel := widget.NewLabel("1.00x")
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.Step = 0.05
	speedSlider.SetValue(1.0)

	ignoredCheck := widget.NewCheck("Ignore Chatter (silence completely)", nil)
	allowedCheck := widget.NewCheck("Always Allow (bypass bot filter)", nil)

	if isEdit {
		nickEntry.SetText(existing.Nickname)
		curLabel := tts.VoiceDisplayName(existing.Voice)
		voiceSelect.SetSelected(curLabel)
		if existing.Speed > 0 {
			speedSlider.SetValue(float64(existing.Speed))
			speedLabel.SetText(fmt.Sprintf("%.2fx", existing.Speed))
		}
		ignoredCheck.SetChecked(existing.Ignored)
		allowedCheck.SetChecked(existing.Allowed)
	}

	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.2fx", val))
	}

	form := container.NewVBox(
		widget.NewLabelWithStyle("Linked Platform Accounts:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		accountsList,
		widget.NewLabel("Add Platform Username / Handle:"),
		addAccountRow,
		widget.NewSeparator(),
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

	dlg := dialog.NewCustomConfirm(title, "Save", "Cancel", newPanelVScroll(form), func(confirmed bool) {
		if !confirmed {
			return
		}
		// Include any pending text typed into userEntry before Save was clicked
		if strings.TrimSpace(userEntry.Text) != "" {
			_ = addAccount()
		}
		if len(accounts) == 0 {
			dialog.ShowError(errors.New("at least one platform username or user ID is required"), v.window)
			return
		}

		primary := accounts[0]
		key := config.ViewerKey(primary.Platform, primary.UserID)

		vc := config.ViewerConfig{
			Platform: primary.Platform,
			UserID:   primary.UserID,
			Accounts: accounts,
			Voice:    tts.VoiceIDFromLabel(voiceSelect.Selected),
			Speed:    float32(speedSlider.Value),
			Nickname: strings.TrimSpace(nickEntry.Text),
			Ignored:  ignoredCheck.Checked,
			Allowed:  allowedCheck.Checked,
		}

		_ = v.session.UpdateConfig(func(cfg *config.Config) {
			if cfg.Viewers == nil {
				cfg.Viewers = make(map[string]config.ViewerConfig)
			}
			if existingKey != "" && existingKey != key {
				delete(cfg.Viewers, existingKey)
			}
			cfg.Viewers[key] = vc
		})

		v.refreshTable()
		v.setStatus("✓ Saved override for " + formatViewerAccountsTitle(vc))
	}, v.window)
	dlg.Resize(fyne.NewSize(580, 480))
	dlg.Show()
}

func (v *ViewersTabView) deleteViewer(key string) {
	_ = v.session.UpdateConfig(func(cfg *config.Config) {
		delete(cfg.Viewers, key)
		delete(cfg.Viewers, strings.ToLower(key))
	})
	v.refreshTable()
	v.setStatus("✓ Override removed.")
}
