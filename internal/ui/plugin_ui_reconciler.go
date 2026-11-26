package ui

import (
	"bytes"
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

type focusAwareEntry struct {
	widget.Entry
	onFocusChanged func(focused bool)
}

func newFocusAwareEntry(onFocus func(bool)) *focusAwareEntry {
	e := &focusAwareEntry{onFocusChanged: onFocus}
	e.ExtendBaseWidget(e)
	return e
}

func (e *focusAwareEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocusChanged != nil {
		e.onFocusChanged(true)
	}
}

func (e *focusAwareEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusChanged != nil {
		e.onFocusChanged(false)
	}
}

type focusedEntryState struct {
	text       string
	cursorRow  int
	cursorCol  int
	hasPending bool
}

type reconciledNode struct {
	id       string
	kind     string
	parentID string

	obj         fyne.CanvasObject
	proto       *pluginv1.UINode
	children    []string
	borderSlots [5]string // top, bottom, left, right, center
	borderObjs  [5]fyne.CanvasObject
	tabItems    []*container.TabItem
	tabIDs      []string

	// Widget references
	boxCont      *fyne.Container
	scrollCont   *container.Scroll
	card         *widget.Card
	tabs         *container.AppTabs
	split        *container.Split
	textCanvas   *canvas.Text
	richText     *widget.RichText
	badgeBg      *canvas.Rectangle
	badgeTxt     *canvas.Text
	imgCanvas    *canvas.Image
	progressBar  *widget.ProgressBar
	meterLabel   *widget.Label
	meterProg    *widget.ProgressBar
	btn          *widget.Button
	entry        *focusAwareEntry
	check        *widget.Check
	sel          *widget.Select
	slider       *widget.Slider
	listWidget   *widget.List
	listRows     []*pluginv1.VirtualListRow
	debounceStop *time.Timer
	pendingText  string
	hasPending   bool

	// Preservation flags
	focused            bool
	dragging           bool
	dragResetTimer     *time.Timer
	suppressNextChange bool
}

// ReconcilerCallbacks supplies host interactions from a declarative UI view to TownCrier.
type ReconcilerCallbacks struct {
	OnUIEvent     func(ev *pluginv1.UIEvent)
	OnResync      func()
	OnToast       func(msg string, tone pluginv1.SemanticTone)
	OnTooltip     func(tip string)
	AllowedHosts  []string
	EnforceHosts  bool
}

// PluginUIReconciler mounts and incrementally patches a declarative UINode tree onto Fyne widgets.
// All public methods must be invoked on the Fyne UI thread (via fyne.Do).
type PluginUIReconciler struct {
	window     fyne.Window
	viewID     string
	imageCache *PluginImageCache
	cb         ReconcilerCallbacks

	rootWrap *fyne.Container
	revision uint64
	nodes    map[string]*reconciledNode

	scrollOffsets map[string]fyne.Position
	selectedTabs  map[string]string
	seq           int
}

// NewPluginUIReconciler constructs a keyed Fyne reconciler for one plugin view.
func NewPluginUIReconciler(win fyne.Window, viewID string, cache *PluginImageCache, cb ReconcilerCallbacks) *PluginUIReconciler {
	if cache == nil {
		cache = NewPluginImageCache(nil)
	}
	return &PluginUIReconciler{
		window:        win,
		viewID:        viewID,
		imageCache:    cache,
		cb:            cb,
		rootWrap:      container.NewStack(),
		nodes:         make(map[string]*reconciledNode),
		scrollOffsets: make(map[string]fyne.Position),
		selectedTabs:  make(map[string]string),
	}
}

// Root returns the top-level Fyne CanvasObject managed by the reconciler.
func (r *PluginUIReconciler) Root() fyne.CanvasObject {
	return r.rootWrap
}

// Revision returns the currently mounted UI revision.
func (r *PluginUIReconciler) Revision() uint64 {
	return r.revision
}

// ApplySnapshot replaces the active view with a full UISnapshot while preserving scroll and tab selections.
func (r *PluginUIReconciler) ApplySnapshot(snap *pluginv1.UISnapshot) {
	if snap == nil || snap.GetRoot() == nil {
		return
	}

	// Capture existing scroll positions, selected tabs, and focused input state before rebuilding.
	var canvasFocused fyne.Focusable
	if r.window != nil && r.window.Canvas() != nil {
		canvasFocused = r.window.Canvas().Focused()
	}
	oldFocused := make(map[string]focusedEntryState)
	var refocusID string
	for id, n := range r.nodes {
		if n.scrollCont != nil {
			r.scrollOffsets[id] = n.scrollCont.Offset
		}
		if n.tabs != nil {
			if sel := n.tabs.Selected(); sel != nil {
				for idx, ti := range n.tabItems {
					if ti == sel && idx < len(n.tabIDs) {
						r.selectedTabs[id] = n.tabIDs[idx]
						break
					}
				}
			}
		}
		if n.entry != nil && (n.focused || (canvasFocused != nil && canvasFocused == n.entry)) {
			oldFocused[id] = focusedEntryState{
				text:       n.entry.Text,
				cursorRow:  n.entry.CursorRow,
				cursorCol:  n.entry.CursorColumn,
				hasPending: n.hasPending,
			}
			refocusID = id
		}
		// Disarm old entry state before cleanup/Focus so FocusLost on the discarded widget
		// cannot fire a stale duplicate CHANGE event.
		n.hasPending = false
		n.focused = false
		r.cleanupNodeTimers(n)
	}

	r.nodes = make(map[string]*reconciledNode)
	rootObj := r.buildNode("", snap.GetRoot(), oldFocused)
	if rootObj == nil {
		rootObj = widget.NewLabel("")
	}
	r.revision = snap.GetRevision()
	r.rootWrap.Objects = []fyne.CanvasObject{rootObj}
	r.rootWrap.Refresh()

	// Restore scroll offsets, tab selections, and active TextInput focus/cursor.
	for id, n := range r.nodes {
		if n.scrollCont != nil {
			if pos, ok := r.scrollOffsets[id]; ok {
				n.scrollCont.Offset = pos
				n.scrollCont.Refresh()
			}
		}
	}
	if refocusID != "" && r.window != nil && r.window.Canvas() != nil {
		if rebuilt := r.nodes[refocusID]; rebuilt != nil && rebuilt.entry != nil && !rebuilt.entry.Disabled() {
			row, col := rebuilt.entry.CursorRow, rebuilt.entry.CursorColumn
			r.window.Canvas().Focus(rebuilt.entry)
			rebuilt.entry.CursorRow = row
			rebuilt.entry.CursorColumn = col
			rebuilt.entry.Refresh()
		}
	}
}

// ApplyPatch applies an incremental UIPatchBatch; returns false and requests resync on base_revision mismatch.
func (r *PluginUIReconciler) ApplyPatch(batch *pluginv1.UIPatchBatch) bool {
	if batch == nil {
		return true
	}
	if base := batch.GetBaseRevision(); base != 0 && r.revision != 0 && base != r.revision {
		if r.cb.OnResync != nil {
			r.cb.OnResync()
		}
		return false
	}

	for _, op := range batch.GetOps() {
		if op == nil {
			continue
		}
		switch kind := op.Op.(type) {
		case *pluginv1.UIPatchOp_SetProps:
			r.applySetProps(kind.SetProps)
		case *pluginv1.UIPatchOp_ReplaceSubtree:
			r.applyReplaceSubtree(kind.ReplaceSubtree)
		case *pluginv1.UIPatchOp_InsertChild:
			r.applyInsertChild(kind.InsertChild)
		case *pluginv1.UIPatchOp_RemoveChild:
			r.applyRemoveChild(kind.RemoveChild)
		case *pluginv1.UIPatchOp_ShowToast:
			if r.cb.OnToast != nil && kind.ShowToast != nil {
				r.cb.OnToast(kind.ShowToast.GetMessage(), kind.ShowToast.GetTone())
			}
		case *pluginv1.UIPatchOp_ShowDialog:
			r.showConfirmDialog(kind.ShowDialog)
		}
	}

	if batch.GetRevision() > 0 {
		r.revision = batch.GetRevision()
	}
	return true
}

func (r *PluginUIReconciler) cleanupNodeTimers(n *reconciledNode) {
	if n == nil {
		return
	}
	if n.debounceStop != nil {
		n.debounceStop.Stop()
		n.debounceStop = nil
	}
	if n.dragResetTimer != nil {
		n.dragResetTimer.Stop()
		n.dragResetTimer = nil
	}
}

func (r *PluginUIReconciler) unregisterSubtree(id string) {
	n, ok := r.nodes[id]
	if !ok {
		return
	}
	r.cleanupNodeTimers(n)
	for _, cid := range n.children {
		r.unregisterSubtree(cid)
	}
	delete(r.nodes, id)
}

func (r *PluginUIReconciler) applySetProps(sp *pluginv1.SetPropsOp) {
	if sp == nil {
		return
	}
	n := r.nodes[sp.GetTargetId()]
	if n == nil {
		return
	}

	if sp.Hidden != nil {
		if sp.GetHidden() {
			n.obj.Hide()
		} else {
			n.obj.Show()
		}
	}

	if sp.Text != nil {
		txt := sp.GetText()
		switch {
		case n.textCanvas != nil:
			n.textCanvas.Text = txt
			n.textCanvas.Refresh()
		case n.richText != nil:
			if n.kind == "markdown" {
				n.richText.ParseMarkdown(txt)
			} else {
				updateRichTextProps(n.richText, txt, sp.Tone)
			}
		case n.badgeTxt != nil:
			n.badgeTxt.Text = txt
			n.badgeTxt.Refresh()
		}
	}

	if sp.Tone != nil {
		tone := sp.GetTone()
		switch {
		case n.textCanvas != nil:
			n.textCanvas.Color = toneColor(tone)
			n.textCanvas.Refresh()
		case n.richText != nil && n.kind == "text":
			updateRichTextProps(n.richText, "", sp.Tone)
		case n.badgeBg != nil && n.badgeTxt != nil:
			bg, fg := badgeColors(tone)
			n.badgeBg.FillColor = bg
			n.badgeBg.Refresh()
			n.badgeTxt.Color = fg
			n.badgeTxt.Refresh()
		}
	}

	if sp.Label != nil {
		lbl := sp.GetLabel()
		if n.btn != nil {
			n.btn.SetText(lbl)
		}
		if n.check != nil {
			n.check.Text = lbl
			n.check.Refresh()
		}
		if n.meterLabel != nil {
			n.meterLabel.SetText(lbl)
		}
	}

	if sp.IconName != nil {
		res := resolvePluginIcon(sp.GetIconName())
		if n.btn != nil {
			n.btn.SetIcon(res)
		}
		if n.imgCanvas != nil && res != nil {
			n.imgCanvas.Image = nil
			n.imgCanvas.Resource = res
			n.imgCanvas.Refresh()
		}
	}

	if sp.ImageUrl != nil && n.imgCanvas != nil {
		r.loadRemoteImage(n.imgCanvas, sp.GetImageUrl())
	}

	if sp.Importance != nil && n.btn != nil {
		n.btn.Importance = mapButtonImportance(sp.GetImportance())
		n.btn.Refresh()
	}

	if sp.ReplaceOptions && n.sel != nil {
		n.sel.SetOptions(sp.GetOptions())
	}

	if sp.Value != nil {
		val := sp.GetValue()
		if n.entry != nil {
			if !n.focused || sp.GetForceValue() {
				n.suppressNextChange = true
				n.entry.SetText(val)
				n.suppressNextChange = false
			}
		}
		if n.sel != nil {
			n.suppressNextChange = true
			n.sel.SetSelected(val)
			n.suppressNextChange = false
		}
	}

	if sp.NumberValue != nil {
		num := sp.GetNumberValue()
		if n.slider != nil && !n.dragging {
			n.suppressNextChange = true
			n.slider.SetValue(clampFloat(num, n.slider.Min, n.slider.Max))
			n.suppressNextChange = false
		}
		if n.progressBar != nil {
			n.progressBar.SetValue(clampFloat(num, 0, 1))
		}
		if n.meterProg != nil {
			n.meterProg.SetValue(clampFloat(num, 0, 1))
		}
	}

	if sp.BoolValue != nil && n.check != nil {
		n.suppressNextChange = true
		n.check.SetChecked(sp.GetBoolValue())
		n.suppressNextChange = false
	}

	if sp.Disabled != nil {
		dis := sp.GetDisabled()
		toggleDisable := func(d fyne.Disableable) {
			if d == nil {
				return
			}
			if dis {
				d.Disable()
			} else {
				d.Enable()
			}
		}
		if n.btn != nil {
			toggleDisable(n.btn)
		}
		if n.entry != nil {
			toggleDisable(n.entry)
		}
		if n.check != nil {
			toggleDisable(n.check)
		}
		if n.sel != nil {
			toggleDisable(n.sel)
		}
		if n.slider != nil {
			toggleDisable(n.slider)
		}
	}

	if sp.SelectedTabId != nil && n.tabs != nil {
		targetTab := sp.GetSelectedTabId()
		for i, tid := range n.tabIDs {
			if tid == targetTab && i < len(n.tabItems) {
				r.selectedTabs[n.id] = targetTab
				n.suppressNextChange = true
				n.tabs.Select(n.tabItems[i])
				n.suppressNextChange = false
				break
			}
		}
	}
}

func (r *PluginUIReconciler) applyReplaceSubtree(op *pluginv1.ReplaceSubtreeOp) {
	if op == nil || op.GetNode() == nil {
		return
	}
	targetID := op.GetTargetId()
	old := r.nodes[targetID]
	if old == nil {
		return
	}
	parentID := old.parentID
	oldObj := old.obj
	r.unregisterSubtree(targetID)

	newObj := r.buildNode(parentID, op.GetNode(), nil)
	if newObj == nil {
		return
	}
	if parentID == "" {
		r.rootWrap.Objects = []fyne.CanvasObject{newObj}
		r.rootWrap.Refresh()
		return
	}
	parent := r.nodes[parentID]
	if parent == nil {
		return
	}
	r.swapParentChildObject(parent, targetID, oldObj, newObj, op.GetNode().GetId())
}

func (r *PluginUIReconciler) applyInsertChild(op *pluginv1.InsertChildOp) {
	if op == nil || op.GetNode() == nil {
		return
	}
	parent := r.nodes[op.GetParentId()]
	if parent == nil || parent.boxCont == nil {
		return
	}
	childObj := r.buildNode(parent.id, op.GetNode(), nil)
	if childObj == nil {
		return
	}
	idx := int(op.GetIndex())
	objs := parent.boxCont.Objects
	if idx < 0 || idx >= len(objs) {
		parent.boxCont.Add(childObj)
		parent.children = append(parent.children, op.GetNode().GetId())
	} else {
		newObjs := slices.Insert(objs, idx, childObj)
		parent.boxCont.Objects = newObjs
		parent.children = slices.Insert(parent.children, min(idx, len(parent.children)), op.GetNode().GetId())
	}
	parent.boxCont.Refresh()
}

func (r *PluginUIReconciler) applyRemoveChild(op *pluginv1.RemoveChildOp) {
	if op == nil {
		return
	}
	target := r.nodes[op.GetTargetId()]
	if target == nil || target.parentID == "" {
		return
	}
	parent := r.nodes[target.parentID]
	oldObj := target.obj
	r.unregisterSubtree(target.id)
	if parent == nil {
		return
	}
	if parent.boxCont != nil {
		parent.boxCont.Remove(oldObj)
		parent.children = slices.DeleteFunc(parent.children, func(s string) bool { return s == op.GetTargetId() })
		parent.boxCont.Refresh()
	}
}

func (r *PluginUIReconciler) swapParentChildObject(parent *reconciledNode, oldID string, oldObj, newObj fyne.CanvasObject, newID string) {
	if parent.kind == "border" && parent.boxCont != nil {
		for i, slot := range parent.borderObjs {
			if slot == oldObj || parent.borderSlots[i] == oldID {
				parent.borderObjs[i] = newObj
				parent.borderSlots[i] = newID
				break
			}
		}
		var objs []fyne.CanvasObject
		for _, o := range parent.borderObjs {
			if o != nil {
				objs = append(objs, o)
			}
		}
		parent.boxCont.Layout = layout.NewBorderLayout(parent.borderObjs[0], parent.borderObjs[1], parent.borderObjs[2], parent.borderObjs[3])
		parent.boxCont.Objects = objs
		for i, cid := range parent.children {
			if cid == oldID {
				parent.children[i] = newID
				break
			}
		}
		parent.boxCont.Refresh()
		return
	}
	if parent.boxCont != nil {
		for i, o := range parent.boxCont.Objects {
			if o == oldObj {
				parent.boxCont.Objects[i] = newObj
				break
			}
		}
		for i, cid := range parent.children {
			if cid == oldID {
				parent.children[i] = newID
				break
			}
		}
		parent.boxCont.Refresh()
		return
	}
	if parent.card != nil {
		parent.card.SetContent(newObj)
		parent.children = []string{newID}
		parent.card.Refresh()
		return
	}
	if parent.scrollCont != nil {
		parent.scrollCont.Content = newObj
		parent.children = []string{newID}
		parent.scrollCont.Refresh()
		return
	}
	if parent.split != nil {
		if parent.split.Leading == oldObj {
			parent.split.Leading = newObj
		} else if parent.split.Trailing == oldObj {
			parent.split.Trailing = newObj
		}
		parent.split.Refresh()
		return
	}
	if parent.tabs != nil {
		for i, ti := range parent.tabItems {
			if ti.Content == oldObj {
				ti.Content = newObj
				if i < len(parent.children) {
					parent.children[i] = newID
				}
				parent.tabs.Refresh()
				break
			}
		}
	}
}

func (r *PluginUIReconciler) showConfirmDialog(op *pluginv1.ShowDialogOp) {
	if op == nil {
		return
	}
	if r.window == nil {
		r.emitEvent(op.GetDialogId(), pluginv1.UIEventKind_UI_EVENT_KIND_DIALOG_CONFIRMED, "", 0, true)
		return
	}
	confirmLbl := strings.TrimSpace(op.GetConfirmLabel())
	if confirmLbl == "" {
		confirmLbl = "Confirm"
	}
	cancelLbl := strings.TrimSpace(op.GetCancelLabel())
	if cancelLbl == "" {
		cancelLbl = "Cancel"
	}
	dlg := dialog.NewConfirm(op.GetTitle(), op.GetBody(), func(confirmed bool) {
		kind := pluginv1.UIEventKind_UI_EVENT_KIND_DIALOG_CANCELLED
		if confirmed {
			kind = pluginv1.UIEventKind_UI_EVENT_KIND_DIALOG_CONFIRMED
		}
		r.emitEvent(op.GetDialogId(), kind, "", 0, confirmed)
	}, r.window)
	dlg.SetConfirmText(confirmLbl)
	dlg.SetDismissText(cancelLbl)
	dlg.Show()
}

func (r *PluginUIReconciler) nextAutoID(prefix string) string {
	r.seq++
	return prefix + "-" + itoa(r.seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (r *PluginUIReconciler) buildNode(parentID string, node *pluginv1.UINode, focusedTexts map[string]focusedEntryState) fyne.CanvasObject {
	if node == nil {
		return nil
	}
	id := strings.TrimSpace(node.GetId())
	if id == "" {
		id = r.nextAutoID("_anon")
	}

	rn := &reconciledNode{
		id:       id,
		parentID: parentID,
		proto:    node,
	}

	switch k := node.Kind.(type) {
	case *pluginv1.UINode_Vbox:
		rn.kind = "vbox"
		var objs []fyne.CanvasObject
		for _, ch := range k.Vbox.GetChildren() {
			if cObj := r.buildNode(id, ch, focusedTexts); cObj != nil {
				objs = append(objs, cObj)
				rn.children = append(rn.children, ch.GetId())
			}
		}
		box := container.NewVBox(objs...)
		rn.boxCont = box
		rn.obj = box

	case *pluginv1.UINode_Hbox:
		rn.kind = "hbox"
		var objs []fyne.CanvasObject
		for _, ch := range k.Hbox.GetChildren() {
			if cObj := r.buildNode(id, ch, focusedTexts); cObj != nil {
				objs = append(objs, cObj)
				rn.children = append(rn.children, ch.GetId())
			}
		}
		box := container.NewHBox(objs...)
		rn.boxCont = box
		rn.obj = box

	case *pluginv1.UINode_Border:
		rn.kind = "border"
		b := k.Border
		top := r.buildChildSlot(id, b.GetTop(), focusedTexts, &rn.children, &rn.borderSlots[0])
		bot := r.buildChildSlot(id, b.GetBottom(), focusedTexts, &rn.children, &rn.borderSlots[1])
		left := r.buildChildSlot(id, b.GetLeft(), focusedTexts, &rn.children, &rn.borderSlots[2])
		right := r.buildChildSlot(id, b.GetRight(), focusedTexts, &rn.children, &rn.borderSlots[3])
		center := r.buildChildSlot(id, b.GetCenter(), focusedTexts, &rn.children, &rn.borderSlots[4])
		rn.borderObjs = [5]fyne.CanvasObject{top, bot, left, right, center}
		border := container.NewBorder(top, bot, left, right, center)
		rn.boxCont = border
		rn.obj = border

	case *pluginv1.UINode_Split:
		rn.kind = "split"
		s := k.Split
		leading := r.buildNode(id, s.GetLeading(), focusedTexts)
		trailing := r.buildNode(id, s.GetTrailing(), focusedTexts)
		if leading == nil {
			leading = widget.NewLabel("")
		}
		if trailing == nil {
			trailing = widget.NewLabel("")
		}
		if s.GetLeading() != nil {
			rn.children = append(rn.children, s.GetLeading().GetId())
		}
		if s.GetTrailing() != nil {
			rn.children = append(rn.children, s.GetTrailing().GetId())
		}
		var sp *container.Split
		if s.GetOrientation() == pluginv1.Orientation_ORIENTATION_VERTICAL {
			sp = container.NewVSplit(leading, trailing)
		} else {
			sp = container.NewHSplit(leading, trailing)
		}
		if off := s.GetOffset(); off > 0 && off < 1 {
			sp.SetOffset(off)
		}
		rn.split = sp
		rn.obj = sp

	case *pluginv1.UINode_Tabs:
		rn.kind = "tabs"
		tNode := k.Tabs
		var items []*container.TabItem
		var tabIDs []string
		for _, ti := range tNode.GetTabs() {
			if ti == nil {
				continue
			}
			content := r.buildNode(id, ti.GetContent(), focusedTexts)
			if content == nil {
				content = widget.NewLabel("")
			}
			if ti.GetContent() != nil {
				rn.children = append(rn.children, ti.GetContent().GetId())
			}
			iconRes := resolvePluginIcon(ti.GetIcon())
			var item *container.TabItem
			if iconRes != nil {
				item = container.NewTabItemWithIcon(ti.GetTitle(), iconRes, content)
			} else {
				item = container.NewTabItem(ti.GetTitle(), content)
			}
			items = append(items, item)
			tabIDs = append(tabIDs, ti.GetId())
		}
		tabs := container.NewAppTabs(items...)
		rn.tabs = tabs
		rn.tabItems = items
		rn.tabIDs = tabIDs

		targetTab := tNode.GetSelectedId()
		if prev, ok := r.selectedTabs[id]; ok && prev != "" {
			targetTab = prev
		}
		if targetTab != "" {
			for i, tid := range tabIDs {
				if tid == targetTab && i < len(items) {
					tabs.Select(items[i])
					break
				}
			}
		}
		tabs.OnSelected = func(sel *container.TabItem) {
			if rn.suppressNextChange {
				return
			}
			for i, it := range rn.tabItems {
				if it == sel && i < len(rn.tabIDs) {
					r.selectedTabs[id] = rn.tabIDs[i]
					r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_TAB_SELECTED, rn.tabIDs[i], 0, false)
					break
				}
			}
		}
		rn.obj = tabs

	case *pluginv1.UINode_Scroll:
		rn.kind = "scroll"
		sn := k.Scroll
		inner := r.buildNode(id, sn.GetContent(), focusedTexts)
		if inner == nil {
			inner = widget.NewLabel("")
		}
		if sn.GetContent() != nil {
			rn.children = append(rn.children, sn.GetContent().GetId())
		}
		var sc *container.Scroll
		if sn.GetHorizontal() {
			sc = container.NewHScroll(inner)
		} else {
			sc = container.NewVScroll(inner)
		}
		rn.scrollCont = sc
		if sn.GetMinWidth() > 0 || sn.GetMinHeight() > 0 {
			spacer := canvas.NewRectangle(color.Transparent)
			spacer.SetMinSize(fyne.NewSize(sn.GetMinWidth(), sn.GetMinHeight()))
			rn.obj = container.NewStack(spacer, sc)
		} else {
			rn.obj = sc
		}

	case *pluginv1.UINode_Grid:
		rn.kind = "grid"
		gn := k.Grid
		cols := max(int(gn.GetColumns()), 1)
		var objs []fyne.CanvasObject
		for _, ch := range gn.GetChildren() {
			if cObj := r.buildNode(id, ch, focusedTexts); cObj != nil {
				objs = append(objs, cObj)
				rn.children = append(rn.children, ch.GetId())
			}
		}
		grid := container.NewGridWithColumns(cols, objs...)
		rn.boxCont = grid
		rn.obj = grid

	case *pluginv1.UINode_Card:
		rn.kind = "card"
		cn := k.Card
		content := r.buildNode(id, cn.GetContent(), focusedTexts)
		if cn.GetContent() != nil {
			rn.children = append(rn.children, cn.GetContent().GetId())
		}
		card := widget.NewCard(cn.GetTitle(), cn.GetSubtitle(), content)
		rn.card = card
		rn.obj = card

	case *pluginv1.UINode_Padded:
		rn.kind = "padded"
		content := r.buildNode(id, k.Padded.GetContent(), focusedTexts)
		if k.Padded.GetContent() != nil {
			rn.children = append(rn.children, k.Padded.GetContent().GetId())
		}
		if content == nil {
			content = widget.NewLabel("")
		}
		pad := container.NewPadded(content)
		rn.boxCont = pad
		rn.obj = pad

	case *pluginv1.UINode_Spacer:
		rn.kind = "spacer"
		rn.obj = layout.NewSpacer()

	case *pluginv1.UINode_Separator:
		rn.kind = "separator"
		rn.obj = widget.NewSeparator()

	case *pluginv1.UINode_Text:
		rn.kind = "text"
		tn := k.Text
		if tn.GetSize() > 0 && !tn.GetWrap() && !tn.GetTruncate() {
			ct := canvas.NewText(tn.GetText(), toneColor(tn.GetTone()))
			ct.TextSize = tn.GetSize()
			ct.TextStyle = fyne.TextStyle{
				Bold:      tn.GetBold(),
				Italic:    tn.GetItalic(),
				Monospace: tn.GetMonospace(),
			}
			ct.Alignment = mapTextAlign(tn.GetAlign())
			rn.textCanvas = ct
			rn.obj = ct
		} else {
			seg := &widget.TextSegment{
				Text: tn.GetText(),
				Style: widget.RichTextStyle{
					Alignment: mapTextAlign(tn.GetAlign()),
					ColorName: toneColorName(tn.GetTone()),
					TextStyle: fyne.TextStyle{
						Bold:      tn.GetBold(),
						Italic:    tn.GetItalic(),
						Monospace: tn.GetMonospace(),
					},
				},
			}
			rt := widget.NewRichText(seg)
			if tn.GetTruncate() {
				rt.Truncation = fyne.TextTruncateEllipsis
			} else if tn.GetWrap() {
				rt.Wrapping = fyne.TextWrapWord
			}
			rn.richText = rt
			rn.obj = rt
		}

	case *pluginv1.UINode_Badge:
		rn.kind = "badge"
		bn := k.Badge
		bgColor, fgColor := badgeColors(bn.GetTone())
		rect := canvas.NewRectangle(bgColor)
		rect.CornerRadius = 6
		txt := canvas.NewText(" "+bn.GetText()+" ", fgColor)
		txt.TextSize = 11
		txt.TextStyle = fyne.TextStyle{Bold: true}
		txt.Alignment = fyne.TextAlignCenter
		rn.badgeBg = rect
		rn.badgeTxt = txt
		rn.obj = container.NewStack(rect, txt)

	case *pluginv1.UINode_Image:
		rn.kind = "image"
		in := k.Image
		img := &canvas.Image{}
		img.FillMode = mapImageFillMode(in.GetFillMode())
		w := in.GetWidth()
		h := in.GetHeight()
		if w > 0 || h > 0 {
			img.SetMinSize(fyne.NewSize(w, h))
		}
		if res := resolvePluginIcon(in.GetIconName()); res != nil {
			img.Resource = res
		} else if len(in.GetData()) > 0 {
			if decoded, _, err := image.Decode(bytes.NewReader(in.GetData())); err == nil {
				img.Image = decoded
			}
		} else if rawURL := strings.TrimSpace(in.GetUrl()); rawURL != "" {
			r.loadRemoteImage(img, rawURL)
		}
		rn.imgCanvas = img
		rn.obj = img

	case *pluginv1.UINode_Progress:
		rn.kind = "progress"
		pn := k.Progress
		if pn.GetIndeterminate() {
			rn.obj = widget.NewProgressBarInfinite()
		} else {
			pb := widget.NewProgressBar()
			pb.SetValue(clampFloat(pn.GetValue(), 0, 1))
			rn.progressBar = pb
			rn.obj = pb
		}

	case *pluginv1.UINode_Meter:
		rn.kind = "meter"
		mn := k.Meter
		pb := widget.NewProgressBar()
		pb.SetValue(clampFloat(mn.GetValue(), 0, 1))
		rn.meterProg = pb
		if lbl := strings.TrimSpace(mn.GetLabel()); lbl != "" {
			l := widget.NewLabel(lbl)
			rn.meterLabel = l
			rn.obj = container.NewBorder(nil, nil, l, nil, pb)
		} else {
			rn.obj = pb
		}

	case *pluginv1.UINode_Markdown:
		rn.kind = "markdown"
		rt := widget.NewRichTextFromMarkdown(k.Markdown.GetMarkdown())
		rt.Wrapping = fyne.TextWrapWord
		rn.richText = rt
		rn.obj = rt

	case *pluginv1.UINode_EmptyState:
		rn.kind = "empty_state"
		es := k.EmptyState
		var parts []fyne.CanvasObject
		if iconRes := resolvePluginIcon(es.GetIconName()); iconRes != nil {
			iconImg := widget.NewIcon(iconRes)
			parts = append(parts, container.NewCenter(iconImg))
		}
		title := widget.NewLabelWithStyle(es.GetTitle(), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		parts = append(parts, title)
		if sub := strings.TrimSpace(es.GetSubtitle()); sub != "" {
			subLbl := widget.NewLabel(sub)
			subLbl.Alignment = fyne.TextAlignCenter
			subLbl.Wrapping = fyne.TextWrapWord
			parts = append(parts, subLbl)
		}
		if actLbl := strings.TrimSpace(es.GetActionLabel()); actLbl != "" {
			actID := strings.TrimSpace(es.GetActionId())
			if actID == "" {
				actID = id
			}
			actBtn := widget.NewButton(actLbl, func() {
				r.emitEvent(actID, pluginv1.UIEventKind_UI_EVENT_KIND_CLICK, "", 0, false)
			})
			actBtn.Importance = widget.HighImportance
			parts = append(parts, container.NewCenter(actBtn))
		}
		rn.obj = container.NewCenter(container.NewPadded(container.NewVBox(parts...)))

	case *pluginv1.UINode_Button:
		rn.kind = "button"
		bn := k.Button
		iconRes := resolvePluginIcon(bn.GetIconName())
		btn := widget.NewButtonWithIcon(bn.GetLabel(), iconRes, func() {
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CLICK, "", 0, false)
		})
		btn.Importance = mapButtonImportance(bn.GetImportance())
		if bn.GetDisabled() {
			btn.Disable()
		}
		rn.btn = btn
		rn.obj = btn

	case *pluginv1.UINode_TextInput:
		rn.kind = "text_input"
		in := k.TextInput
		var entry *focusAwareEntry
		entry = newFocusAwareEntry(func(focused bool) {
			rn.focused = focused
			if !focused && rn.hasPending {
				if rn.debounceStop != nil {
					rn.debounceStop.Stop()
					rn.debounceStop = nil
				}
				txt := rn.pendingText
				rn.hasPending = false
				r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, txt, 0, false)
			}
		})
		entry.MultiLine = in.GetMultiline()
		entry.Password = in.GetPassword()
		entry.SetPlaceHolder(in.GetPlaceholder())

		initialVal := in.GetValue()
		prevFocused, wasFocused := focusedTexts[id]
		if wasFocused && !in.GetForceValue() {
			initialVal = prevFocused.text
			rn.focused = true
		}
		rn.suppressNextChange = true
		entry.SetText(initialVal)
		rn.suppressNextChange = false
		if in.GetDisabled() {
			entry.Disable()
		}

		debounce := time.Duration(in.GetDebounceMs()) * time.Millisecond
		rearmDebounce := func(text string) {
			rn.pendingText = text
			rn.hasPending = true
			if rn.debounceStop != nil {
				rn.debounceStop.Stop()
			}
			rn.debounceStop = time.AfterFunc(debounce, func() {
				fyne.Do(func() {
					if !rn.hasPending {
						return
					}
					val := rn.pendingText
					rn.hasPending = false
					r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, val, 0, false)
				})
			})
		}
		if wasFocused && !in.GetForceValue() {
			runeLen := len([]rune(initialVal))
			entry.CursorRow = max(0, prevFocused.cursorRow)
			entry.CursorColumn = min(max(0, prevFocused.cursorCol), runeLen)
			if debounce > 0 && (prevFocused.hasPending || prevFocused.text != in.GetValue()) {
				rearmDebounce(initialVal)
			}
		}

		entry.OnChanged = func(text string) {
			if rn.suppressNextChange {
				return
			}
			if debounce <= 0 {
				r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, text, 0, false)
				return
			}
			rearmDebounce(text)
		}
		entry.OnSubmitted = func(text string) {
			if rn.debounceStop != nil {
				rn.debounceStop.Stop()
				rn.debounceStop = nil
			}
			rn.hasPending = false
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_SUBMIT, text, 0, false)
		}
		rn.entry = entry
		rn.obj = entry

	case *pluginv1.UINode_Checkbox:
		rn.kind = "checkbox"
		cn := k.Checkbox
		chk := widget.NewCheck(cn.GetLabel(), func(b bool) {
			if rn.suppressNextChange {
				return
			}
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, "", 0, b)
		})
		rn.suppressNextChange = true
		chk.SetChecked(cn.GetChecked())
		rn.suppressNextChange = false
		if cn.GetDisabled() {
			chk.Disable()
		}
		rn.check = chk
		rn.obj = chk

	case *pluginv1.UINode_Select:
		rn.kind = "select"
		sn := k.Select
		sel := widget.NewSelect(sn.GetOptions(), func(choice string) {
			if rn.suppressNextChange {
				return
			}
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, choice, 0, false)
		})
		if ph := strings.TrimSpace(sn.GetPlaceholder()); ph != "" {
			sel.PlaceHolder = ph
		}
		if s := sn.GetSelected(); s != "" {
			rn.suppressNextChange = true
			sel.SetSelected(s)
			rn.suppressNextChange = false
		}
		if sn.GetDisabled() {
			sel.Disable()
		}
		rn.sel = sel
		rn.obj = sel

	case *pluginv1.UINode_Slider:
		rn.kind = "slider"
		sn := k.Slider
		minV, maxV := sn.GetMin(), sn.GetMax()
		if maxV <= minV {
			maxV = minV + 100
		}
		sl := widget.NewSlider(minV, maxV)
		if sn.GetStep() > 0 {
			sl.Step = sn.GetStep()
		}
		rn.suppressNextChange = true
		sl.SetValue(clampFloat(sn.GetValue(), minV, maxV))
		rn.suppressNextChange = false
		if sn.GetDisabled() {
			sl.Disable()
		}
		sl.OnChanged = func(val float64) {
			if rn.suppressNextChange {
				return
			}
			rn.dragging = true
			if rn.dragResetTimer != nil {
				rn.dragResetTimer.Stop()
			}
			rn.dragResetTimer = time.AfterFunc(450*time.Millisecond, func() {
				fyne.Do(func() {
					rn.dragging = false
				})
			})
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE, "", val, false)
		}
		sl.OnChangeEnded = func(val float64) {
			if rn.dragResetTimer != nil {
				rn.dragResetTimer.Stop()
				rn.dragResetTimer = nil
			}
			rn.dragging = false
			if rn.suppressNextChange {
				return
			}
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CHANGE_ENDED, "", val, false)
		}
		rn.slider = sl
		rn.obj = sl

	case *pluginv1.UINode_VirtualList:
		rn.kind = "virtual_list"
		vl := k.VirtualList
		rn.listRows = slices.Clone(vl.GetRows())
		var rowHeightsMu sync.Mutex
		rowHeights := make(map[int]float32)

		var list *widget.List
		list = widget.NewList(
			func() int { return len(rn.listRows) },
			func() fyne.CanvasObject { return container.NewStack() },
			func(itemID widget.ListItemID, item fyne.CanvasObject) {
				stack, ok := item.(*fyne.Container)
				if !ok {
					return
				}
				if itemID < 0 || itemID >= len(rn.listRows) {
					stack.Objects = nil
					stack.Refresh()
					return
				}
				row := rn.listRows[itemID]
				if row == nil || row.GetContent() == nil {
					stack.Objects = nil
					stack.Refresh()
					return
				}
				cellObj := r.buildEphemeralNode(row.GetContent())
				if cellObj == nil {
					stack.Objects = nil
					stack.Refresh()
					return
				}
				stack.Objects = []fyne.CanvasObject{cellObj}
				stack.Refresh()

				if h := cellObj.MinSize().Height; h > 0 {
					rowHeightsMu.Lock()
					prev := rowHeights[itemID]
					if h != prev {
						rowHeights[itemID] = h
						rowHeightsMu.Unlock()
						list.SetItemHeight(itemID, h+4)
					} else {
						rowHeightsMu.Unlock()
					}
				}
			},
		)
		rn.listWidget = list
		minH := vl.GetMinHeight()
		if minH > 0 {
			spacer := canvas.NewRectangle(color.Transparent)
			spacer.SetMinSize(fyne.NewSize(10, minH))
			rn.obj = container.NewStack(spacer, list)
		} else {
			rn.obj = list
		}

	default:
		rn.kind = "unknown"
		rn.obj = widget.NewLabel("")
	}

	if rn.obj != nil && node.GetHidden() {
		rn.obj.Hide()
	}
	r.nodes[id] = rn
	return rn.obj
}

func (r *PluginUIReconciler) buildChildSlot(parentID string, child *pluginv1.UINode, focusedTexts map[string]focusedEntryState, children *[]string, slotID *string) fyne.CanvasObject {
	if child == nil {
		return nil
	}
	obj := r.buildNode(parentID, child, focusedTexts)
	if obj != nil {
		cid := child.GetId()
		*children = append(*children, cid)
		*slotID = cid
	}
	return obj
}

// buildEphemeralNode builds a Fyne subtree for a VirtualList row without registering IDs in r.nodes,
// preventing pooled cell widgets from colliding across virtualized rows.
func (r *PluginUIReconciler) buildEphemeralNode(node *pluginv1.UINode) fyne.CanvasObject {
	if node == nil {
		return nil
	}
	id := node.GetId()
	var obj fyne.CanvasObject
	switch k := node.Kind.(type) {
	case *pluginv1.UINode_Vbox:
		var objs []fyne.CanvasObject
		for _, ch := range k.Vbox.GetChildren() {
			if c := r.buildEphemeralNode(ch); c != nil {
				objs = append(objs, c)
			}
		}
		obj = container.NewVBox(objs...)
	case *pluginv1.UINode_Hbox:
		var objs []fyne.CanvasObject
		for _, ch := range k.Hbox.GetChildren() {
			if c := r.buildEphemeralNode(ch); c != nil {
				objs = append(objs, c)
			}
		}
		obj = container.NewHBox(objs...)
	case *pluginv1.UINode_Border:
		b := k.Border
		obj = container.NewBorder(
			r.buildEphemeralNode(b.GetTop()),
			r.buildEphemeralNode(b.GetBottom()),
			r.buildEphemeralNode(b.GetLeft()),
			r.buildEphemeralNode(b.GetRight()),
			r.buildEphemeralNode(b.GetCenter()),
		)
	case *pluginv1.UINode_Padded:
		if inner := r.buildEphemeralNode(k.Padded.GetContent()); inner != nil {
			obj = container.NewPadded(inner)
		}
	case *pluginv1.UINode_Card:
		cn := k.Card
		obj = widget.NewCard(cn.GetTitle(), cn.GetSubtitle(), r.buildEphemeralNode(cn.GetContent()))
	case *pluginv1.UINode_Spacer:
		obj = layout.NewSpacer()
	case *pluginv1.UINode_Separator:
		obj = widget.NewSeparator()
	case *pluginv1.UINode_Text:
		tn := k.Text
		if tn.GetSize() > 0 && !tn.GetWrap() && !tn.GetTruncate() {
			ct := canvas.NewText(tn.GetText(), toneColor(tn.GetTone()))
			ct.TextSize = tn.GetSize()
			ct.TextStyle = fyne.TextStyle{Bold: tn.GetBold(), Italic: tn.GetItalic(), Monospace: tn.GetMonospace()}
			ct.Alignment = mapTextAlign(tn.GetAlign())
			obj = ct
		} else {
			rt := widget.NewRichText(&widget.TextSegment{
				Text: tn.GetText(),
				Style: widget.RichTextStyle{
					Alignment: mapTextAlign(tn.GetAlign()),
					ColorName: toneColorName(tn.GetTone()),
					TextStyle: fyne.TextStyle{Bold: tn.GetBold(), Italic: tn.GetItalic(), Monospace: tn.GetMonospace()},
				},
			})
			if tn.GetTruncate() {
				rt.Truncation = fyne.TextTruncateEllipsis
			} else if tn.GetWrap() {
				rt.Wrapping = fyne.TextWrapWord
			}
			obj = rt
		}
	case *pluginv1.UINode_Badge:
		bn := k.Badge
		bg, fg := badgeColors(bn.GetTone())
		rect := canvas.NewRectangle(bg)
		rect.CornerRadius = 6
		txt := canvas.NewText(" "+bn.GetText()+" ", fg)
		txt.TextSize = 11
		txt.TextStyle = fyne.TextStyle{Bold: true}
		txt.Alignment = fyne.TextAlignCenter
		obj = container.NewStack(rect, txt)
	case *pluginv1.UINode_Image:
		in := k.Image
		img := &canvas.Image{}
		img.FillMode = mapImageFillMode(in.GetFillMode())
		if in.GetWidth() > 0 || in.GetHeight() > 0 {
			img.SetMinSize(fyne.NewSize(in.GetWidth(), in.GetHeight()))
		}
		if res := resolvePluginIcon(in.GetIconName()); res != nil {
			img.Resource = res
		} else if len(in.GetData()) > 0 {
			if decoded, _, err := image.Decode(bytes.NewReader(in.GetData())); err == nil {
				img.Image = decoded
			}
		} else if rawURL := strings.TrimSpace(in.GetUrl()); rawURL != "" {
			r.loadRemoteImage(img, rawURL)
		}
		obj = img
	case *pluginv1.UINode_Button:
		bn := k.Button
		btn := widget.NewButtonWithIcon(bn.GetLabel(), resolvePluginIcon(bn.GetIconName()), func() {
			r.emitEvent(id, pluginv1.UIEventKind_UI_EVENT_KIND_CLICK, "", 0, false)
		})
		btn.Importance = mapButtonImportance(bn.GetImportance())
		if bn.GetDisabled() {
			btn.Disable()
		}
		obj = btn
	default:
		obj = widget.NewLabel("")
	}
	if obj != nil && node.GetHidden() {
		obj.Hide()
	}
	return obj
}

func (r *PluginUIReconciler) loadRemoteImage(img *canvas.Image, rawURL string) {
	if r.imageCache == nil || img == nil {
		return
	}
	r.imageCache.Fetch(rawURL, r.cb.AllowedHosts, r.cb.EnforceHosts, func(decoded image.Image, err error) {
		if err != nil || decoded == nil {
			return
		}
		fyne.Do(func() {
			img.Resource = nil
			img.Image = decoded
			img.Refresh()
		})
	})
}

func (r *PluginUIReconciler) emitEvent(nodeID string, kind pluginv1.UIEventKind, strVal string, numVal float64, boolVal bool) {
	if r.cb.OnUIEvent == nil {
		return
	}
	r.cb.OnUIEvent(&pluginv1.UIEvent{
		ViewId:      r.viewID,
		NodeId:      nodeID,
		Kind:        kind,
		StringValue: strVal,
		NumberValue: numVal,
		BoolValue:   boolVal,
	})
}

func updateRichTextProps(rt *widget.RichText, text string, tone *pluginv1.SemanticTone) {
	if rt == nil || len(rt.Segments) == 0 {
		return
	}
	if seg, ok := rt.Segments[0].(*widget.TextSegment); ok {
		if text != "" {
			seg.Text = text
		}
		if tone != nil {
			seg.Style.ColorName = toneColorName(*tone)
		}
		rt.Refresh()
	}
}

func toneColorName(t pluginv1.SemanticTone) fyne.ThemeColorName {
	switch t {
	case pluginv1.SemanticTone_TONE_MUTED:
		return theme.ColorNamePlaceHolder
	case pluginv1.SemanticTone_TONE_ACCENT:
		return theme.ColorNamePrimary
	case pluginv1.SemanticTone_TONE_SUCCESS:
		return theme.ColorNameSuccess
	case pluginv1.SemanticTone_TONE_WARNING:
		return theme.ColorNameWarning
	case pluginv1.SemanticTone_TONE_DANGER:
		return theme.ColorNameError
	default:
		return theme.ColorNameForeground
	}
}

func toneColor(t pluginv1.SemanticTone) color.Color {
	switch t {
	case pluginv1.SemanticTone_TONE_MUTED:
		return colorSlateMuted
	case pluginv1.SemanticTone_TONE_ACCENT:
		return colorSkyBlue
	case pluginv1.SemanticTone_TONE_SUCCESS:
		return colorSuccessGreen
	case pluginv1.SemanticTone_TONE_WARNING:
		return colorWarningAmber
	case pluginv1.SemanticTone_TONE_DANGER:
		return colorDangerRed
	default:
		return colorChromeWhite
	}
}

func badgeColors(t pluginv1.SemanticTone) (bg color.Color, fg color.Color) {
	switch t {
	case pluginv1.SemanticTone_TONE_ACCENT:
		return color.NRGBA{R: 0x02, G: 0x84, B: 0xC7, A: 0x66}, colorSkyBlue
	case pluginv1.SemanticTone_TONE_SUCCESS:
		return color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0x44}, colorSuccessGreen
	case pluginv1.SemanticTone_TONE_WARNING:
		return color.NRGBA{R: 0xF5, G: 0x9E, B: 0x0B, A: 0x44}, colorWarningAmber
	case pluginv1.SemanticTone_TONE_DANGER:
		return color.NRGBA{R: 0xEF, G: 0x44, B: 0x44, A: 0x44}, colorDangerRed
	default:
		return colorSlateSurface, colorChromeWhite
	}
}

func mapTextAlign(a pluginv1.TextAlign) fyne.TextAlign {
	switch a {
	case pluginv1.TextAlign_TEXT_ALIGN_CENTER:
		return fyne.TextAlignCenter
	case pluginv1.TextAlign_TEXT_ALIGN_TRAILING:
		return fyne.TextAlignTrailing
	default:
		return fyne.TextAlignLeading
	}
}

func mapButtonImportance(imp pluginv1.ButtonImportance) widget.Importance {
	switch imp {
	case pluginv1.ButtonImportance_BUTTON_IMPORTANCE_HIGH:
		return widget.HighImportance
	case pluginv1.ButtonImportance_BUTTON_IMPORTANCE_LOW:
		return widget.LowImportance
	case pluginv1.ButtonImportance_BUTTON_IMPORTANCE_WARNING:
		return widget.WarningImportance
	case pluginv1.ButtonImportance_BUTTON_IMPORTANCE_DANGER:
		return widget.DangerImportance
	default:
		return widget.MediumImportance
	}
}

func mapImageFillMode(m pluginv1.ImageFillMode) canvas.ImageFill {
	switch m {
	case pluginv1.ImageFillMode_IMAGE_FILL_COVER:
		return canvas.ImageFillCover
	case pluginv1.ImageFillMode_IMAGE_FILL_ORIGINAL:
		return canvas.ImageFillOriginal
	default:
		return canvas.ImageFillContain
	}
}

func clampFloat(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}
