package ui

import "github.com/mrpoundsign/TownCrier/pkg/pluginv1"

// Snapshot constructs a UISnapshot for viewID at revision.
func Snapshot(viewID string, revision uint64, root *pluginv1.UINode) *pluginv1.UISnapshot {
	return &pluginv1.UISnapshot{
		ViewId:   viewID,
		Revision: revision,
		Root:     root,
	}
}

// PatchBatch constructs a UIPatchBatch targeting viewID from baseRevision to nextRevision.
func PatchBatch(viewID string, baseRevision, nextRevision uint64, ops ...*pluginv1.UIPatchOp) *pluginv1.UIPatchBatch {
	filtered := make([]*pluginv1.UIPatchOp, 0, len(ops))
	for _, op := range ops {
		if op != nil {
			filtered = append(filtered, op)
		}
	}
	return &pluginv1.UIPatchBatch{
		ViewId:       viewID,
		BaseRevision: baseRevision,
		Revision:     nextRevision,
		Ops:          filtered,
	}
}

// PropOption mutates a SetPropsOp.
type PropOption func(*pluginv1.SetPropsOp)

// PropHidden sets visibility on the target node.
func PropHidden(hidden bool) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Hidden = &hidden }
}

// PropText sets text content on Text/Markdown/Badge/EmptyState nodes.
func PropText(text string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Text = &text }
}

// PropLabel sets label text on Button/Checkbox/Meter nodes.
func PropLabel(label string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Label = &label }
}

// PropValue sets string value on TextInput/Select nodes (ignored while TextInput focused).
func PropValue(val string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Value = &val }
}

// PropForceValue sets string value even while TextInput holds focus.
func PropForceValue(val string) PropOption {
	return func(p *pluginv1.SetPropsOp) {
		p.Value = &val
		p.ForceValue = true
	}
}

// PropNumber sets numeric value on Slider/Progress/Meter nodes.
func PropNumber(val float64) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.NumberValue = &val }
}

// PropBool sets boolean state on Checkbox nodes.
func PropBool(val bool) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.BoolValue = &val }
}

// PropDisabled toggles disabled state on interactive nodes.
func PropDisabled(disabled bool) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Disabled = &disabled }
}

// PropTone updates SemanticTone on Text/Badge/Meter nodes.
func PropTone(tone pluginv1.SemanticTone) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Tone = &tone }
}

// PropImportance updates ButtonImportance on a Button node.
func PropImportance(imp pluginv1.ButtonImportance) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Importance = &imp }
}

// PropIcon updates icon_name on Button/Image nodes.
func PropIcon(iconName string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.IconName = &iconName }
}

// PropImageURL updates URL on an Image node.
func PropImageURL(rawURL string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.ImageUrl = &rawURL }
}

// PropTooltip updates tooltip on a Button node.
func PropTooltip(tip string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.Tooltip = &tip }
}

// PropSelectedTab switches the active tab inside a Tabs node.
func PropSelectedTab(tabID string) PropOption {
	return func(p *pluginv1.SetPropsOp) { p.SelectedTabId = &tabID }
}

// PropOptions replaces the dropdown options on a Select node.
func PropOptions(options []string) PropOption {
	return func(p *pluginv1.SetPropsOp) {
		p.Options = append([]string(nil), options...)
		p.ReplaceOptions = true
	}
}

// SetProps builds a multi-property update patch op for targetID.
func SetProps(targetID string, opts ...PropOption) *pluginv1.UIPatchOp {
	sp := &pluginv1.SetPropsOp{TargetId: targetID}
	for _, opt := range opts {
		if opt != nil {
			opt(sp)
		}
	}
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_SetProps{SetProps: sp},
	}
}

// SetText updates text on targetID.
func SetText(targetID, text string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropText(text))
}

// SetLabel updates label on targetID.
func SetLabel(targetID, label string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropLabel(label))
}

// SetValue updates string value on targetID (TextInput/Select).
func SetValue(targetID, value string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropValue(value))
}

// SetForceValue updates string value on targetID even when focused.
func SetForceValue(targetID, value string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropForceValue(value))
}

// SetProgress updates numeric value on a Progress node.
func SetProgress(targetID string, val float64) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropNumber(val))
}

// SetSlider updates numeric value on a Slider node.
func SetSlider(targetID string, val float64) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropNumber(val))
}

// SetChecked updates checked state on a Checkbox node.
func SetChecked(targetID string, checked bool) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropBool(checked))
}

// SetDisabled updates disabled state on targetID.
func SetDisabled(targetID string, disabled bool) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropDisabled(disabled))
}

// SetHidden updates visibility on targetID.
func SetHidden(targetID string, hidden bool) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropHidden(hidden))
}

// SetTone updates semantic tone on targetID.
func SetTone(targetID string, tone pluginv1.SemanticTone) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropTone(tone))
}

// SetImportance updates ButtonImportance on targetID.
func SetImportance(targetID string, imp pluginv1.ButtonImportance) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropImportance(imp))
}

// SetIcon updates icon_name on targetID.
func SetIcon(targetID, iconName string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropIcon(iconName))
}

// SetImageURL updates image_url on targetID.
func SetImageURL(targetID, rawURL string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropImageURL(rawURL))
}

// SetTooltip updates tooltip on targetID.
func SetTooltip(targetID, tooltip string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropTooltip(tooltip))
}

// SetOptions replaces options on a Select node.
func SetOptions(targetID string, options []string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropOptions(options))
}

// SelectTab switches the active tab inside a Tabs node.
func SelectTab(targetID, tabID string) *pluginv1.UIPatchOp {
	return SetProps(targetID, PropSelectedTab(tabID))
}

// ReplaceNode replaces targetID's entire subtree with node.
func ReplaceNode(targetID string, node *pluginv1.UINode) *pluginv1.UIPatchOp {
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_ReplaceSubtree{
			ReplaceSubtree: &pluginv1.ReplaceSubtreeOp{
				TargetId: targetID,
				Node:     node,
			},
		},
	}
}

// InsertChild inserts node under container parentID at index (<0 appends).
func InsertChild(parentID string, index int32, node *pluginv1.UINode) *pluginv1.UIPatchOp {
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_InsertChild{
			InsertChild: &pluginv1.InsertChildOp{
				ParentId: parentID,
				Index:    index,
				Node:     node,
			},
		},
	}
}

// RemoveChild removes targetID from its parent container.
func RemoveChild(targetID string) *pluginv1.UIPatchOp {
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_RemoveChild{
			RemoveChild: &pluginv1.RemoveChildOp{
				TargetId: targetID,
			},
		},
	}
}

// Toast displays a transient status/banner notification in TownCrier.
func Toast(message string, tone pluginv1.SemanticTone) *pluginv1.UIPatchOp {
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_ShowToast{
			ShowToast: &pluginv1.ShowToastOp{
				Message: message,
				Tone:    tone,
			},
		},
	}
}

// Confirm opens a native modal confirmation dialog returning DIALOG_CONFIRMED / DIALOG_CANCELLED.
func Confirm(dialogID, title, body, confirmLabel, cancelLabel string) *pluginv1.UIPatchOp {
	return &pluginv1.UIPatchOp{
		Op: &pluginv1.UIPatchOp_ShowDialog{
			ShowDialog: &pluginv1.ShowDialogOp{
				DialogId:     dialogID,
				Title:        title,
				Body:         body,
				ConfirmLabel: confirmLabel,
				CancelLabel:  cancelLabel,
			},
		},
	}
}
