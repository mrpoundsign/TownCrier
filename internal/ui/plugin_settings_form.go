package ui

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

type pluginFormBinding struct {
	field      *pluginv1.SettingField
	getValue   func() any
	applyValue func(*pluginv1.SettingValue)
	clearCheck *widget.Check
	secretBox  *widget.Entry
}

type pluginSettingsForm struct {
	win        fyne.Window
	mgr        *pluginhost.Manager
	pluginUUID string
	schema     []*pluginv1.SettingField
	onStatus   func(string)

	hostBox  *fyne.Container
	bindings map[string]*pluginFormBinding
}

func newPluginSettingsForm(
	win fyne.Window,
	mgr *pluginhost.Manager,
	pluginUUID string,
	schema []*pluginv1.SettingField,
	onStatus func(string),
) *pluginSettingsForm {
	return &pluginSettingsForm{
		win:        win,
		mgr:        mgr,
		pluginUUID: pluginUUID,
		schema:     schema,
		onStatus:   onStatus,
		hostBox:    container.NewVBox(),
		bindings:   make(map[string]*pluginFormBinding),
	}
}

func (f *pluginSettingsForm) Build() fyne.CanvasObject {
	f.Rebuild()

	saveBtn := widget.NewButtonWithIcon("Save Settings", theme.DocumentSaveIcon(), func() {
		vals, clears := f.collect()
		if err := f.mgr.SaveGlobalSettingsFromUI(f.pluginUUID, vals, clears); err != nil {
			f.reportStatus("Save failed: " + err.Error())
			return
		}
		f.reportStatus("Saved settings for " + f.pluginUUID)
		f.RefreshFromHost()
	})
	saveBtn.Importance = widget.HighImportance

	resetBtn := widget.NewButtonWithIcon("Reset Defaults", theme.DeleteIcon(), func() {
		vals := make(map[string]any, len(f.schema))
		var clears []string
		for _, sf := range f.schema {
			if sf == nil || sf.GetKey() == "" {
				continue
			}
			switch sf.GetType() {
			case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD:
				clears = append(clears, sf.GetKey())
			case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO,
				pluginv1.SettingFieldType_SETTING_FIELD_TYPE_LINK,
				pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON:
				continue
			default:
				if dv := sf.GetDefaultValue(); dv != nil {
					vals[sf.GetKey()] = settingValueToAny(dv)
				}
			}
		}
		if err := f.mgr.SaveGlobalSettingsFromUI(f.pluginUUID, vals, clears); err != nil {
			f.reportStatus("Reset failed: " + err.Error())
			return
		}
		f.Rebuild()
		f.reportStatus("Reset plugin settings to defaults")
	})

	btnBar := container.NewHBox(layout.NewSpacer(), resetBtn, saveBtn)
	return container.NewBorder(nil, container.NewVBox(widget.NewSeparator(), btnBar), nil, nil, newPanelVScroll(f.hostBox))
}

func (f *pluginSettingsForm) Rebuild() {
	clear(f.bindings)
	settings := f.mgr.GetGlobalSettings(f.pluginUUID)

	var (
		general  []*pluginv1.SettingField
		advanced []*pluginv1.SettingField
		custom   = make(map[string][]*pluginv1.SettingField)
		order    []string
	)
	for _, sf := range f.schema {
		if sf == nil {
			continue
		}
		sec := strings.TrimSpace(sf.GetSection())
		switch strings.ToLower(sec) {
		case "", "general":
			general = append(general, sf)
		case "advanced":
			advanced = append(advanced, sf)
		default:
			if _, ok := custom[sec]; !ok {
				order = append(order, sec)
			}
			custom[sec] = append(custom[sec], sf)
		}
	}

	var objs []fyne.CanvasObject
	objs = append(objs, f.buildFields(general, settings)...)

	var accItems []*widget.AccordionItem
	for _, sec := range order {
		secObjs := f.buildFields(custom[sec], settings)
		if len(secObjs) > 0 {
			item := widget.NewAccordionItem(sec, container.NewPadded(container.NewVBox(secObjs...)))
			item.Open = true
			accItems = append(accItems, item)
		}
	}
	if len(advanced) > 0 {
		advObjs := f.buildFields(advanced, settings)
		item := widget.NewAccordionItem("Advanced", container.NewPadded(container.NewVBox(advObjs...)))
		item.Open = false
		accItems = append(accItems, item)
	}
	if len(accItems) > 0 {
		acc := widget.NewAccordion(accItems...)
		acc.MultiOpen = true
		objs = append(objs, acc)
	}
	f.hostBox.Objects = objs
	f.hostBox.Refresh()
}

func (f *pluginSettingsForm) RefreshFromHost() {
	settings := f.mgr.GetGlobalSettings(f.pluginUUID)
	if settings == nil {
		return
	}
	for k, b := range f.bindings {
		if b.applyValue == nil {
			continue
		}
		if sv, ok := settings.GetValues()[k]; ok {
			b.applyValue(sv)
		} else if b.field.GetDefaultValue() != nil {
			b.applyValue(b.field.GetDefaultValue())
		}
		if b.clearCheck != nil && b.secretBox != nil {
			if f.mgr.HasSecret(f.pluginUUID, k) {
				b.secretBox.SetPlaceHolder("•••••••• (saved in OS keyring — enter to replace)")
			} else {
				b.secretBox.SetPlaceHolder(b.field.GetPlaceholder())
			}
			b.clearCheck.SetChecked(false)
		}
	}
}

func (f *pluginSettingsForm) buildFields(fields []*pluginv1.SettingField, settings *pluginv1.SettingsMap) []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, 0, len(fields)*2)
	for _, sf := range fields {
		out = append(out, f.buildField(sf, settings)...)
	}
	return out
}

func (f *pluginSettingsForm) buildField(sf *pluginv1.SettingField, settings *pluginv1.SettingsMap) []fyne.CanvasObject {
	key := sf.GetKey()
	label := sf.GetLabel()
	if label == "" {
		label = key
	}

	var curVal *pluginv1.SettingValue
	if settings != nil {
		curVal = settings.GetValues()[key]
	}
	if curVal == nil {
		curVal = sf.GetDefaultValue()
	}

	var rows []fyne.CanvasObject
	switch sf.GetType() {
	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL:
		chk := widget.NewCheck(label, nil)
		chk.SetChecked(curVal.GetBoolVal())
		f.bindings[key] = &pluginFormBinding{
			field:      sf,
			getValue:   func() any { return chk.Checked },
			applyValue: func(sv *pluginv1.SettingValue) { chk.SetChecked(sv.GetBoolVal()) },
		}
		rows = append(rows, chk)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_MULTILINE:
		ent := widget.NewMultiLineEntry()
		ent.SetMinRowsVisible(4)
		ent.SetPlaceHolder(sf.GetPlaceholder())
		ent.SetText(settingStringValue(curVal))
		f.bindings[key] = &pluginFormBinding{
			field:      sf,
			getValue:   func() any { return ent.Text },
			applyValue: func(sv *pluginv1.SettingValue) { ent.SetText(settingStringValue(sv)) },
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ent)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD:
		ent := widget.NewPasswordEntry()
		if f.mgr.HasSecret(f.pluginUUID, key) {
			ent.SetPlaceHolder("•••••••• (saved in OS keyring — enter to replace)")
		} else {
			ent.SetPlaceHolder(sf.GetPlaceholder())
		}
		clr := widget.NewCheck("Clear stored secret on save", nil)
		f.bindings[key] = &pluginFormBinding{
			field:      sf,
			clearCheck: clr,
			secretBox:  ent,
			getValue:   func() any { return ent.Text },
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ent, clr)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_NUMBER:
		ent := widget.NewEntry()
		ent.SetPlaceHolder(sf.GetPlaceholder())
		if curVal != nil {
			ent.SetText(strconv.FormatFloat(curVal.GetNumberVal(), 'f', -1, 64))
		}
		f.bindings[key] = &pluginFormBinding{
			field: sf,
			getValue: func() any {
				if v, err := strconv.ParseFloat(strings.TrimSpace(ent.Text), 64); err == nil {
					return v
				}
				return 0.0
			},
			applyValue: func(sv *pluginv1.SettingValue) {
				ent.SetText(strconv.FormatFloat(sv.GetNumberVal(), 'f', -1, 64))
			},
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ent)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_SELECT:
		opts := sf.GetOptions()
		labels := make([]string, 0, len(opts))
		valToLabel := make(map[string]string, len(opts))
		labelToVal := make(map[string]string, len(opts))
		for _, o := range opts {
			lbl := o.GetLabel()
			if lbl == "" {
				lbl = o.GetValue()
			}
			labels = append(labels, lbl)
			valToLabel[o.GetValue()] = lbl
			labelToVal[lbl] = o.GetValue()
		}
		sel := widget.NewSelect(labels, nil)
		cur := settingStringValue(curVal)
		if lbl, ok := valToLabel[cur]; ok {
			sel.SetSelected(lbl)
		} else if len(labels) > 0 {
			sel.SetSelected(labels[0])
		}
		f.bindings[key] = &pluginFormBinding{
			field: sf,
			getValue: func() any {
				if v, ok := labelToVal[sel.Selected]; ok {
					return v
				}
				return sel.Selected
			},
			applyValue: func(sv *pluginv1.SettingValue) {
				if lbl, ok := valToLabel[settingStringValue(sv)]; ok {
					sel.SetSelected(lbl)
				}
			},
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), sel)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO:
		lbl := widget.NewLabel(infoText(curVal))
		lbl.Wrapping = fyne.TextWrapWord
		var copyBtn *widget.Button
		copyBtn = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
			if a := fyne.CurrentApp(); a != nil && a.Clipboard() != nil {
				a.Clipboard().SetContent(lbl.Text)
				f.reportStatus("Copied to clipboard")
			}
		})
		if curVal.GetInfoVal() == nil || !curVal.GetInfoVal().GetCopyable() {
			copyBtn.Hide()
		}
		row := container.NewBorder(nil, nil, nil, copyBtn, lbl)
		if strings.TrimSpace(lbl.Text) == "" {
			row.Hide()
		}
		f.bindings[key] = &pluginFormBinding{
			field: sf,
			applyValue: func(sv *pluginv1.SettingValue) {
				txt := infoText(sv)
				lbl.SetText(txt)
				if sv.GetInfoVal() != nil && sv.GetInfoVal().GetCopyable() {
					copyBtn.Show()
				} else {
					copyBtn.Hide()
				}
				if strings.TrimSpace(txt) == "" {
					row.Hide()
				} else {
					row.Show()
				}
			},
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), row)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_LINK:
		rawURL, linkLbl := linkParts(curVal, sf.GetPlaceholder())
		hl := widget.NewHyperlink(linkLbl, nil)
		updateLink := func(u, l string) {
			parsed, err := url.Parse(strings.TrimSpace(u))
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				hl.Hide()
				return
			}
			hl.SetText(l)
			hl.SetURL(parsed)
			hl.Show()
		}
		updateLink(rawURL, linkLbl)
		f.bindings[key] = &pluginFormBinding{
			field: sf,
			applyValue: func(sv *pluginv1.SettingValue) {
				u, l := linkParts(sv, sf.GetPlaceholder())
				updateLink(u, l)
			},
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), hl)

	case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON:
		btnText, disabled := buttonParts(curVal, label)
		var btn *widget.Button
		btn = widget.NewButton(btnText, func() {
			btn.Disable()
			f.reportStatus("Running " + btn.Text + "...")
			snapshot := f.collectAsProto()
			go func() {
				err := f.mgr.InvokeConfigAction(context.Background(), f.pluginUUID, key, snapshot)
				fyne.Do(func() {
					btn.Enable()
					f.RefreshFromHost()
					if err != nil {
						f.reportStatus("Action failed: " + err.Error())
						if f.win != nil {
							dialog.ShowError(err, f.win)
						}
						return
					}
					f.reportStatus(btn.Text + " completed")
				})
			}()
		})
		if disabled {
			btn.Disable()
		}
		f.bindings[key] = &pluginFormBinding{
			field: sf,
			applyValue: func(sv *pluginv1.SettingValue) {
				t, dis := buttonParts(sv, label)
				btn.SetText(t)
				if dis {
					btn.Disable()
				} else {
					btn.Enable()
				}
			},
		}
		rows = append(rows, btn)

	default:
		ent := widget.NewEntry()
		ent.SetPlaceHolder(sf.GetPlaceholder())
		ent.SetText(settingStringValue(curVal))
		f.bindings[key] = &pluginFormBinding{
			field:      sf,
			getValue:   func() any { return ent.Text },
			applyValue: func(sv *pluginv1.SettingValue) { ent.SetText(settingStringValue(sv)) },
		}
		rows = append(rows, widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ent)
	}

	if h := strings.TrimSpace(sf.GetHelp()); h != "" {
		helpLbl := widget.NewLabel(h)
		helpLbl.Wrapping = fyne.TextWrapWord
		rows = append(rows, helpLbl)
	}
	return rows
}

func (f *pluginSettingsForm) collect() (map[string]any, []string) {
	vals := make(map[string]any, len(f.bindings))
	var clears []string
	for k, b := range f.bindings {
		if b.clearCheck != nil && b.clearCheck.Checked {
			clears = append(clears, k)
			continue
		}
		if b.getValue != nil {
			v := b.getValue()
			if b.secretBox != nil && strings.TrimSpace(fmt.Sprint(v)) == "" {
				continue
			}
			vals[k] = v
		}
	}
	slices.Sort(clears)
	return vals, clears
}

func (f *pluginSettingsForm) collectAsProto() *pluginv1.SettingsMap {
	m := f.mgr.GetGlobalSettings(f.pluginUUID)
	m = pluginsdk.EnsureSettingsMap(m)
	vals, clears := f.collect()
	for _, k := range clears {
		delete(m.Values, k)
	}
	for k, v := range vals {
		switch x := v.(type) {
		case bool:
			pluginsdk.SetSettingBool(m, k, x)
		case float64:
			pluginsdk.SetSettingNumber(m, k, x)
		default:
			pluginsdk.SetSettingString(m, k, fmt.Sprint(x))
		}
	}
	return m
}

func (f *pluginSettingsForm) reportStatus(msg string) {
	if f.onStatus != nil {
		f.onStatus(msg)
	}
}

func settingValueToAny(sv *pluginv1.SettingValue) any {
	if sv == nil {
		return ""
	}
	switch v := sv.Kind.(type) {
	case *pluginv1.SettingValue_BoolVal:
		return v.BoolVal
	case *pluginv1.SettingValue_NumberVal:
		return v.NumberVal
	case *pluginv1.SettingValue_StringVal:
		return v.StringVal
	default:
		return ""
	}
}

func settingStringValue(sv *pluginv1.SettingValue) string {
	if sv == nil {
		return ""
	}
	if s := sv.GetStringVal(); s != "" {
		return s
	}
	if i := sv.GetInfoVal(); i != nil {
		return i.GetText()
	}
	if l := sv.GetLinkVal(); l != nil {
		return l.GetUrl()
	}
	return ""
}

func infoText(sv *pluginv1.SettingValue) string {
	if sv == nil {
		return ""
	}
	if i := sv.GetInfoVal(); i != nil {
		return i.GetText()
	}
	return sv.GetStringVal()
}

func linkParts(sv *pluginv1.SettingValue, fallback string) (string, string) {
	if sv == nil {
		return "", fallback
	}
	if l := sv.GetLinkVal(); l != nil {
		lbl := l.GetLabel()
		if lbl == "" {
			lbl = fallback
		}
		if lbl == "" {
			lbl = l.GetUrl()
		}
		return l.GetUrl(), lbl
	}
	u := sv.GetStringVal()
	lbl := fallback
	if lbl == "" {
		lbl = u
	}
	return u, lbl
}

func buttonParts(sv *pluginv1.SettingValue, fallback string) (string, bool) {
	if sv == nil {
		return fallback, false
	}
	if b := sv.GetButtonVal(); b != nil {
		lbl := b.GetLabel()
		if lbl == "" {
			lbl = fallback
		}
		return lbl, b.GetDisabled()
	}
	if s := sv.GetStringVal(); s != "" {
		return s, false
	}
	return fallback, false
}
