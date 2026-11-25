package ui

import (
	"context"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// PluginTabHost mounts a single declarative plugin view into a Fyne container,
// handling initial loading, live push updates, crash isolation, and async UIEvent dispatch.
type PluginTabHost struct {
	mgr        *pluginhost.Manager
	window     fyne.Window
	pluginUUID string
	viewID     string
	pluginName string
	onStatus   func(string)

	mu         sync.Mutex
	closed     bool
	unsubUI    func()
	unsubState func()

	root       *fyne.Container
	reconciler *PluginUIReconciler
	lastState  pluginhost.RunState
}

// NewPluginTabHost creates a mounted declarative view host for (pluginUUID, viewID).
func NewPluginTabHost(
	mgr *pluginhost.Manager,
	win fyne.Window,
	cache *PluginImageCache,
	pluginUUID string,
	pluginName string,
	viewID string,
	onStatus func(string),
) *PluginTabHost {
	allowed, enforce := mgr.AllowedHostsForPlugin(pluginUUID)
	h := &PluginTabHost{
		mgr:        mgr,
		window:     win,
		pluginUUID: pluginUUID,
		pluginName: pluginName,
		viewID:     viewID,
		onStatus:   onStatus,
		root:       container.NewStack(),
	}

	h.reconciler = NewPluginUIReconciler(win, viewID, cache, ReconcilerCallbacks{
		AllowedHosts: allowed,
		EnforceHosts: enforce,
		OnUIEvent:    h.dispatchUIEvent,
		OnResync:     h.requestRender,
		OnToast: func(msg string, _ pluginv1.SemanticTone) {
			if h.onStatus != nil {
				h.onStatus(msg)
			}
		},
	})

	loading := container.NewCenter(widget.NewLabel("Loading " + pluginName + "..."))
	h.root.Objects = []fyne.CanvasObject{loading}

	h.unsubUI = mgr.SubscribeUIUpdate(func(upd pluginhost.PluginUIUpdate) {
		if upd.PluginUUID != h.pluginUUID {
			return
		}
		if upd.ViewID != "" && upd.ViewID != h.viewID {
			return
		}
		fyne.Do(func() {
			if h.isClosed() {
				return
			}
			if upd.Snapshot != nil {
				h.reconciler.ApplySnapshot(upd.Snapshot)
				h.ensureReconcilerMounted()
			}
			if upd.Patch != nil {
				h.reconciler.ApplyPatch(upd.Patch)
			}
		})
	})

	h.unsubState = mgr.SubscribeStatusChange(func() {
		fyne.Do(h.syncPluginState)
	})

	h.syncPluginState()
	return h
}

// Root returns the container object mounted inside MainWindow or SettingsView.
func (h *PluginTabHost) Root() fyne.CanvasObject {
	return h.root
}

// Close unsubscribes host listeners.
func (h *PluginTabHost) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	unUI := h.unsubUI
	unSt := h.unsubState
	h.unsubUI = nil
	h.unsubState = nil
	h.mu.Unlock()

	if unUI != nil {
		unUI()
	}
	if unSt != nil {
		unSt()
	}
}

func (h *PluginTabHost) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *PluginTabHost) ensureReconcilerMounted() {
	if len(h.root.Objects) == 1 && h.root.Objects[0] == h.reconciler.Root() {
		return
	}
	h.root.Objects = []fyne.CanvasObject{h.reconciler.Root()}
	h.root.Refresh()
}

func (h *PluginTabHost) syncPluginState() {
	if h.isClosed() || h.mgr == nil {
		return
	}
	var found *pluginhost.PluginStatus
	for _, st := range h.mgr.Plugins() {
		if st.UUID == h.pluginUUID {
			cp := st
			found = &cp
			break
		}
	}
	if found == nil {
		h.showStatusPanel(pluginhost.StatusStopped, "Plugin is no longer loaded.")
		return
	}

	if found.State != pluginhost.StatusRunning {
		h.lastState = found.State
		msg := found.Error
		if msg == "" {
			msg = "Plugin state: " + string(found.State)
		}
		h.showStatusPanel(found.State, msg)
		return
	}

	needRender := h.lastState != pluginhost.StatusRunning || h.reconciler.Revision() == 0
	h.lastState = pluginhost.StatusRunning
	if needRender {
		h.requestRender()
	}
}

func (h *PluginTabHost) requestRender() {
	if h.isClosed() || h.mgr == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		snap, err := h.mgr.RenderView(ctx, h.pluginUUID, h.viewID)
		fyne.Do(func() {
			if h.isClosed() {
				return
			}
			if err != nil {
				h.showStatusPanel(pluginhost.StatusError, err.Error())
				return
			}
			h.reconciler.ApplySnapshot(snap)
			h.ensureReconcilerMounted()
		})
	}()
}

func (h *PluginTabHost) dispatchUIEvent(ev *pluginv1.UIEvent) {
	if h.isClosed() || h.mgr == nil || ev == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		snap, patch, err := h.mgr.SendUIEvent(ctx, h.pluginUUID, ev)
		fyne.Do(func() {
			if h.isClosed() {
				return
			}
			if err != nil {
				if h.onStatus != nil {
					h.onStatus("Plugin UI error: " + err.Error())
				}
				return
			}
			if snap != nil {
				h.reconciler.ApplySnapshot(snap)
				h.ensureReconcilerMounted()
			}
			if patch != nil {
				h.reconciler.ApplyPatch(patch)
			}
		})
	}()
}

func (h *PluginTabHost) showStatusPanel(state pluginhost.RunState, msg string) {
	title := h.pluginName + " Unavailable"
	switch state {
	case pluginhost.StatusDisabled:
		title = h.pluginName + " Disabled"
	case pluginhost.StatusAwaitingTrust:
		title = h.pluginName + " Awaiting Trust Approval"
	}

	lbl := widget.NewLabel(msg)
	lbl.Wrapping = fyne.TextWrapWord

	restartBtn := widget.NewButtonWithIcon("Restart Plugin", theme.ViewRefreshIcon(), func() {
		go func() {
			_ = h.mgr.RestartPlugin(h.pluginUUID)
		}()
	})
	restartBtn.Importance = widget.HighImportance

	logsBtn := widget.NewButtonWithIcon("View Logs", theme.DocumentIcon(), func() {
		if h.window == nil || h.mgr == nil {
			return
		}
		lines := h.mgr.PluginLogs(h.pluginUUID)
		body := "No log entries yet."
		if len(lines) > 0 {
			body = strings.Join(lines, "\n")
		}
		entry := widget.NewMultiLineEntry()
		entry.SetText(body)
		entry.Disable()
		dlg := dialog.NewCustom("Plugin Logs — "+h.pluginName, "Close", newPanelVScroll(entry), h.window)
		dlg.Resize(fyne.NewSize(620, 420))
		dlg.Show()
	})

	card := widget.NewCard(title, "Host UI kept isolated cleanly", container.NewVBox(
		lbl,
		container.NewHBox(restartBtn, logsBtn),
	))
	h.root.Objects = []fyne.CanvasObject{container.NewCenter(container.NewPadded(card))}
	h.root.Refresh()
}
