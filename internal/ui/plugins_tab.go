package ui

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// PluginsTabView renders the Settings -> Plugins management view.
type PluginsTabView struct {
	window    fyne.Window
	session   *app.Session
	onStatus  func(string)
	onOpenTab func(string)

	enabledCheck *widget.Check
	reloadBtn    *widget.Button
	openDirBtn   *widget.Button
	addPathBtn   *widget.Button
	extraBox     *fyne.Container
	cardsBox     *fyne.Container
	unsub        func()
	initializing bool
}

// NewPluginsTabView constructs the Plugins management tab view.
func NewPluginsTabView(win fyne.Window, sess *app.Session, onStatus func(string)) *PluginsTabView {
	return &PluginsTabView{
		window:   win,
		session:  sess,
		onStatus: onStatus,
	}
}

// SetOnOpenTab registers a callback invoked when the user clicks "Open <View>" on a running plugin card.
func (v *PluginsTabView) SetOnOpenTab(fn func(string)) {
	v.onOpenTab = fn
}

// Build constructs the Fyne canvas hierarchy for Settings -> Plugins.
func (v *PluginsTabView) Build() fyne.CanvasObject {
	cfg := v.currentConfig()

	v.enabledCheck = widget.NewCheck("Enable Plugins (WASM & Native Subprocess)", func(checked bool) {
		if v.initializing || v.session == nil {
			return
		}
		if err := v.session.UpdateConfig(func(c *config.Config) {
			c.Plugins.Enabled = checked
		}); err != nil {
			v.reportStatus("Failed saving plugin toggle: " + err.Error())
			return
		}
		if mgr := v.session.PluginManager(); mgr != nil {
			if err := mgr.Reload(); err != nil {
				v.reportStatus("Plugin reload failed: " + err.Error())
			}
		}
		v.Refresh()
	})
	v.enabledCheck.SetChecked(cfg.Plugins.Enabled)

	v.reloadBtn = widget.NewButtonWithIcon("Reload All", theme.ViewRefreshIcon(), func() {
		if v.session == nil || v.session.PluginManager() == nil {
			return
		}
		if err := v.session.PluginManager().Reload(); err != nil {
			v.reportStatus("Reload plugins failed: " + err.Error())
			return
		}
		v.Refresh()
		v.reportStatus("Plugins reloaded")
	})

	v.openDirBtn = widget.NewButtonWithIcon("Open Plugins Folder", theme.FolderOpenIcon(), func() {
		if v.session == nil || v.session.PluginManager() == nil {
			return
		}
		base := strings.TrimSpace(v.session.PluginManager().BaseDir())
		if base == "" {
			v.reportStatus("Plugins folder unavailable for in-memory config")
			return
		}
		_ = os.MkdirAll(filepath.Join(base, "wasm"), 0o755)
		_ = os.MkdirAll(filepath.Join(base, "native"), 0o755)
		if u, err := url.Parse("file://" + filepath.ToSlash(base)); err == nil && fyne.CurrentApp() != nil {
			_ = fyne.CurrentApp().OpenURL(u)
		}
		v.reportStatus("Plugins folder: " + base)
	})

	v.addPathBtn = widget.NewButtonWithIcon("Add Folder...", theme.ContentAddIcon(), func() {
		if v.window == nil {
			return
		}
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil || v.session == nil {
				return
			}
			p := filepath.Clean(uri.Path())
			if err := v.session.UpdateConfig(func(c *config.Config) {
				if !slices.Contains(c.Plugins.ExtraPaths, p) {
					c.Plugins.ExtraPaths = append(c.Plugins.ExtraPaths, p)
				}
			}); err != nil {
				v.reportStatus("Failed adding plugin path: " + err.Error())
				return
			}
			if mgr := v.session.PluginManager(); mgr != nil {
				_ = mgr.Reload()
			}
			v.Refresh()
		}, v.window)
	})

	v.extraBox = container.NewVBox()
	v.cardsBox = container.NewVBox()

	toolbar := container.NewVBox(
		v.enabledCheck,
		container.NewHBox(v.openDirBtn, v.addPathBtn, v.reloadBtn, layout.NewSpacer()),
	)
	header := container.NewVBox(
		widget.NewCard("Plugin System", "Host sandboxed WebAssembly (.wasm) and trusted Native Subprocess plugins via Protobuf v1.0.0.", container.NewVBox(toolbar, v.extraBox)),
	)

	if v.session != nil && v.session.PluginManager() != nil {
		v.unsub = v.session.PluginManager().SubscribeStatusChange(func() {
			fyne.Do(v.Refresh)
		})
	}

	v.Refresh()
	return newPanelVScroll(container.NewVBox(header, v.cardsBox))
}

// Close unsubscribes any plugin status listener.
func (v *PluginsTabView) Close() {
	if v.unsub != nil {
		v.unsub()
		v.unsub = nil
	}
}

// Refresh reloads plugin cards and extra paths from Session/PluginManager.
func (v *PluginsTabView) Refresh() {
	if v.cardsBox == nil {
		return
	}
	cfg := v.currentConfig()
	v.initializing = true
	if v.enabledCheck != nil {
		v.enabledCheck.SetChecked(cfg.Plugins.Enabled)
	}
	v.initializing = false

	v.refreshExtraPaths(cfg)

	var mgr *pluginhost.Manager
	if v.session != nil {
		mgr = v.session.PluginManager()
	}
	if mgr == nil {
		v.cardsBox.Objects = []fyne.CanvasObject{widget.NewLabel("Plugin manager unavailable.")}
		v.cardsBox.Refresh()
		return
	}

	plugins := mgr.Plugins()
	if len(plugins) == 0 {
		dirMsg := "Place .wasm modules in plugins/wasm/ or native plugin.json bundles in plugins/native/."
		if base := mgr.BaseDir(); base != "" {
			dirMsg = fmt.Sprintf("No plugins discovered yet.\n\nWASM folder: %s\nNative folder: %s", filepath.Join(base, "wasm"), filepath.Join(base, "native"))
		}
		empty := widget.NewLabel(dirMsg)
		empty.Wrapping = fyne.TextWrapWord
		v.cardsBox.Objects = []fyne.CanvasObject{widget.NewCard("Installed Plugins", "", empty)}
		v.cardsBox.Refresh()
		return
	}

	cards := make([]fyne.CanvasObject, 0, len(plugins))
	for _, p := range plugins {
		cards = append(cards, v.buildPluginCard(mgr, p))
	}
	v.cardsBox.Objects = cards
	v.cardsBox.Refresh()
}

func (v *PluginsTabView) refreshExtraPaths(cfg *config.Config) {
	if v.extraBox == nil {
		return
	}
	if cfg == nil || len(cfg.Plugins.ExtraPaths) == 0 {
		v.extraBox.Objects = nil
		v.extraBox.Refresh()
		return
	}
	rows := make([]fyne.CanvasObject, 0, len(cfg.Plugins.ExtraPaths))
	for _, p := range cfg.Plugins.ExtraPaths {
		pathCopy := p
		lbl := widget.NewLabel("Extra Path: " + pathCopy)
		lbl.Truncation = fyne.TextTruncateEllipsis
		rmBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			if v.session == nil {
				return
			}
			_ = v.session.UpdateConfig(func(c *config.Config) {
				filtered := make([]string, 0, len(c.Plugins.ExtraPaths))
				for _, ep := range c.Plugins.ExtraPaths {
					if ep != pathCopy {
						filtered = append(filtered, ep)
					}
				}
				c.Plugins.ExtraPaths = filtered
			})
			if mgr := v.session.PluginManager(); mgr != nil {
				_ = mgr.Reload()
			}
			v.Refresh()
		})
		rows = append(rows, container.NewBorder(nil, nil, nil, rmBtn, lbl))
	}
	v.extraBox.Objects = rows
	v.extraBox.Refresh()
}

func (v *PluginsTabView) buildPluginCard(mgr *pluginhost.Manager, p pluginhost.PluginStatus) fyne.CanvasObject {
	runtimeBadge := "WASM • Sandboxed"
	if p.Runtime == pluginv1.RuntimeKind_RUNTIME_KIND_NATIVE {
		runtimeBadge = "Native • Unsandboxed"
	}

	title := p.Name
	if p.Version != "" {
		title += " v" + p.Version
	}
	subParts := []string{runtimeBadge, "State: " + string(p.State)}
	if p.APIVersion != "" && p.APIVersion != "unspecified" {
		subParts = append(subParts, "API "+p.APIVersion)
	}
	if p.Author != "" {
		subParts = append(subParts, "by "+p.Author)
	}

	var body []fyne.CanvasObject
	if p.Description != "" {
		desc := widget.NewLabel(p.Description)
		desc.Wrapping = fyne.TextWrapWord
		body = append(body, desc)
	}

	meta := widget.NewLabel("UUID: " + p.UUID + "\nPath: " + p.Path)
	meta.Wrapping = fyne.TextWrapWord
	body = append(body, meta)

	if permLine := formatPermissionsSummary(p); permLine != "" {
		pLbl := widget.NewLabel(permLine)
		pLbl.Wrapping = fyne.TextWrapWord
		body = append(body, pLbl)
	}

	if p.Error != "" {
		errText := p.Error
		if p.APIError != nil && p.APIError.GetDetail() != "" {
			errText += "\n" + p.APIError.GetDetail()
		}
		errLbl := widget.NewLabel("Error: " + errText)
		errLbl.Wrapping = fyne.TextWrapWord
		body = append(body, errLbl)
	}

	var actions []fyne.CanvasObject
	if p.State == pluginhost.StatusAwaitingTrust {
		trustBtn := widget.NewButtonWithIcon("Approve & Trust", theme.ConfirmIcon(), func() {
			msg := fmt.Sprintf("Native plugin %q runs outside the WebAssembly sandbox with your user permissions.\n\nBinary: %s\nSHA-256: %s\n\nTrust and launch this binary?", p.Name, p.BinaryPath, p.BinarySHA256)
			if v.window != nil {
				dialog.ShowConfirm("Trust Native Plugin", msg, func(ok bool) {
					if !ok {
						return
					}
					if err := mgr.TrustNativePlugin(p.UUID); err != nil {
						v.reportStatus("Trust failed: " + err.Error())
					}
					v.Refresh()
				}, v.window)
				return
			}
			_ = mgr.TrustNativePlugin(p.UUID)
			v.Refresh()
		})
		trustBtn.Importance = widget.HighImportance
		actions = append(actions, trustBtn)
	} else if p.Runtime == pluginv1.RuntimeKind_RUNTIME_KIND_NATIVE && p.State != pluginhost.StatusDisabled {
		revokeBtn := widget.NewButton("Revoke Trust", func() {
			_ = mgr.RevokeNativeTrust(p.UUID)
			v.Refresh()
		})
		actions = append(actions, revokeBtn)
	}

	if p.UUID != "" && p.State != pluginhost.StatusAwaitingTrust {
		isDisabled := p.State == pluginhost.StatusDisabled
		toggleLbl := "Disable"
		if isDisabled {
			toggleLbl = "Enable"
		}
		toggleBtn := widget.NewButton(toggleLbl, func() {
			_ = mgr.SetPluginEnabled(p.UUID, isDisabled)
			v.Refresh()
		})
		actions = append(actions, toggleBtn)
	}

	if p.HasGlobalSettings && p.State == pluginhost.StatusRunning {
		cfgBtn := widget.NewButtonWithIcon("Configure", theme.SettingsIcon(), func() {
			v.openPluginSettingsDialog(mgr, p)
		})
		cfgBtn.Importance = widget.HighImportance
		actions = append(actions, cfgBtn)
	}

	if p.State == pluginhost.StatusRunning && v.onOpenTab != nil {
		for _, vd := range p.Views {
			if vd == nil || vd.GetPlacement() != pluginv1.ViewPlacement_VIEW_PLACEMENT_MAIN_TAB {
				continue
			}
			viewTitle := strings.TrimSpace(vd.GetTitle())
			if viewTitle == "" {
				viewTitle = p.Name
			}
			viewKey := p.UUID + ":" + vd.GetId()
			openViewBtn := widget.NewButtonWithIcon("Open "+viewTitle, theme.NavigateNextIcon(), func() {
				v.onOpenTab(viewKey)
			})
			actions = append(actions, openViewBtn)
		}
	}

	if p.State == pluginhost.StatusError && p.UUID != "" {
		restartBtn := widget.NewButtonWithIcon("Restart", theme.ViewRefreshIcon(), func() {
			_ = mgr.RestartPlugin(p.UUID)
			v.Refresh()
		})
		actions = append(actions, restartBtn)
	}

	if p.UUID != "" {
		logsBtn := widget.NewButtonWithIcon("Logs", theme.DocumentIcon(), func() {
			v.openPluginLogsDialog(mgr, p)
		})
		actions = append(actions, logsBtn)
	}

	if len(actions) > 0 {
		body = append(body, container.NewHBox(actions...))
	}

	return widget.NewCard(title, strings.Join(subParts, " • "), container.NewVBox(body...))
}

func (v *PluginsTabView) openPluginSettingsDialog(mgr *pluginhost.Manager, p pluginhost.PluginStatus) {
	if v.window == nil {
		return
	}
	form := newPluginSettingsForm(v.window, mgr, p.UUID, p.GlobalSchema, v.reportStatus)
	content := form.Build()
	content.Resize(fyne.NewSize(560, 480))
	dlg := dialog.NewCustom("Configure — "+p.Name, "Close", content, v.window)
	dlg.Resize(fyne.NewSize(600, 540))
	dlg.Show()
}

func (v *PluginsTabView) openPluginLogsDialog(mgr *pluginhost.Manager, p pluginhost.PluginStatus) {
	if v.window == nil {
		return
	}
	lines := mgr.PluginLogs(p.UUID)
	text := "No log entries yet."
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	entry := widget.NewMultiLineEntry()
	entry.SetText(text)
	entry.Disable()
	dlg := dialog.NewCustom("Plugin Logs — "+p.Name, "Close", newPanelVScroll(entry), v.window)
	dlg.Resize(fyne.NewSize(620, 420))
	dlg.Show()
}

func formatPermissionsSummary(p pluginhost.PluginStatus) string {
	var caps []string
	if perms := p.Permissions; perms != nil {
		if perms.GetReadEvents() {
			caps = append(caps, "events")
		}
		if perms.GetSpeakTts() {
			caps = append(caps, "speak_tts")
		}
		if perms.GetKvStorage() {
			caps = append(caps, "kv_storage")
		}
		if perms.GetOverlayBroadcast() {
			caps = append(caps, "overlay")
		}
		if perms.GetSendChat() {
			caps = append(caps, "send_chat")
		}
		if perms.GetModerate() {
			caps = append(caps, "moderate")
		}
	}
	var parts []string
	if len(caps) > 0 {
		parts = append(parts, "Permissions: "+strings.Join(caps, ", "))
	}
	if len(p.AllowedHosts) > 0 {
		parts = append(parts, "Allowed Hosts: "+strings.Join(p.AllowedHosts, ", "))
	}
	return strings.Join(parts, " | ")
}

func (v *PluginsTabView) currentConfig() *config.Config {
	if v.session != nil {
		return v.session.Config()
	}
	return config.DefaultConfig()
}

func (v *PluginsTabView) reportStatus(msg string) {
	if v.onStatus != nil {
		v.onStatus(msg)
	}
}
