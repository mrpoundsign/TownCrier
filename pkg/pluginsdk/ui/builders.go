package ui

import "github.com/mrpoundsign/TownCrier/pkg/pluginv1"

// MarkHidden toggles visibility on any constructed UINode and returns it.
func MarkHidden(n *pluginv1.UINode, hidden bool) *pluginv1.UINode {
	if n != nil {
		n.Hidden = hidden
	}
	return n
}

func filterNodes(nodes []*pluginv1.UINode) []*pluginv1.UINode {
	out := make([]*pluginv1.UINode, 0, len(nodes))
	for _, n := range nodes {
		if n != nil {
			out = append(out, n)
		}
	}
	return out
}

// VBox constructs a vertical box container node.
func VBox(id string, children ...*pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Vbox{
			Vbox: &pluginv1.VBoxNode{Children: filterNodes(children)},
		},
	}
}

// HBox constructs a horizontal box container node.
func HBox(id string, children ...*pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Hbox{
			Hbox: &pluginv1.HBoxNode{Children: filterNodes(children)},
		},
	}
}

// Border constructs a border layout container node (top, bottom, left, right, center).
func Border(id string, top, bottom, left, right, center *pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Border{
			Border: &pluginv1.BorderNode{
				Top:    top,
				Bottom: bottom,
				Left:   left,
				Right:  right,
				Center: center,
			},
		},
	}
}

// Split constructs a resizable split container node.
func Split(id string, orientation pluginv1.Orientation, offset float64, leading, trailing *pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Split{
			Split: &pluginv1.SplitNode{
				Orientation: orientation,
				Offset:      offset,
				Leading:     leading,
				Trailing:    trailing,
			},
		},
	}
}

// Tab constructs one TabItem for use inside Tabs.
func Tab(id, title, icon string, content *pluginv1.UINode) *pluginv1.TabItem {
	return &pluginv1.TabItem{
		Id:      id,
		Title:   title,
		Icon:    icon,
		Content: content,
	}
}

// Tabs constructs a nested tabs container node.
func Tabs(id, selectedID string, tabs ...*pluginv1.TabItem) *pluginv1.UINode {
	filtered := make([]*pluginv1.TabItem, 0, len(tabs))
	for _, t := range tabs {
		if t != nil {
			filtered = append(filtered, t)
		}
	}
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Tabs{
			Tabs: &pluginv1.TabsNode{
				Tabs:       filtered,
				SelectedId: selectedID,
			},
		},
	}
}

// ScrollOption configures a ScrollNode.
type ScrollOption func(*pluginv1.ScrollNode)

// WithHorizontalScroll enables horizontal scrolling.
func WithHorizontalScroll() ScrollOption {
	return func(n *pluginv1.ScrollNode) { n.Horizontal = true }
}

// WithScrollMinSize sets a minimum size hint on the scroll viewport.
func WithScrollMinSize(w, h float32) ScrollOption {
	return func(n *pluginv1.ScrollNode) {
		n.MinWidth = w
		n.MinHeight = h
	}
}

// Scroll constructs a scrollable container node.
func Scroll(id string, content *pluginv1.UINode, opts ...ScrollOption) *pluginv1.UINode {
	sn := &pluginv1.ScrollNode{Content: content}
	for _, opt := range opts {
		if opt != nil {
			opt(sn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Scroll{Scroll: sn},
	}
}

// Grid constructs a fixed-column grid container node.
func Grid(id string, cols int32, children ...*pluginv1.UINode) *pluginv1.UINode {
	if cols <= 0 {
		cols = 1
	}
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Grid{
			Grid: &pluginv1.GridNode{
				Columns:  cols,
				Children: filterNodes(children),
			},
		},
	}
}

// Card constructs a themed card container node.
func Card(id, title, subtitle string, content *pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Card{
			Card: &pluginv1.CardNode{
				Title:    title,
				Subtitle: subtitle,
				Content:  content,
			},
		},
	}
}

// Padded wraps content in standard Fyne padding.
func Padded(id string, content *pluginv1.UINode) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Padded{
			Padded: &pluginv1.PaddedNode{Content: content},
		},
	}
}

// Spacer constructs a flexible layout spacer node.
func Spacer(id string) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Spacer{Spacer: &pluginv1.SpacerNode{}},
	}
}

// Separator constructs a horizontal/vertical visual separator node.
func Separator(id string) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Separator{Separator: &pluginv1.SeparatorNode{}},
	}
}

// TextOption configures a TextNode.
type TextOption func(*pluginv1.TextNode)

// WithSize sets custom point size (> 0).
func WithSize(size float32) TextOption {
	return func(n *pluginv1.TextNode) { n.Size = size }
}

// WithBold renders text in bold style.
func WithBold() TextOption {
	return func(n *pluginv1.TextNode) { n.Bold = true }
}

// WithItalic renders text in italic style.
func WithItalic() TextOption {
	return func(n *pluginv1.TextNode) { n.Italic = true }
}

// WithMonospace renders text in monospace font.
func WithMonospace() TextOption {
	return func(n *pluginv1.TextNode) { n.Monospace = true }
}

// WithAlign sets horizontal text alignment.
func WithAlign(align pluginv1.TextAlign) TextOption {
	return func(n *pluginv1.TextNode) { n.Align = align }
}

// WithWrap enables word wrapping.
func WithWrap() TextOption {
	return func(n *pluginv1.TextNode) { n.Wrap = true }
}

// WithTruncate enables ellipsis truncation on overflow.
func WithTruncate() TextOption {
	return func(n *pluginv1.TextNode) { n.Truncate = true }
}

// WithTone sets the semantic theme tone on a TextNode.
func WithTone(tone pluginv1.SemanticTone) TextOption {
	return func(n *pluginv1.TextNode) { n.Tone = tone }
}

// Text constructs a styled text/label display node.
func Text(id, text string, opts ...TextOption) *pluginv1.UINode {
	tn := &pluginv1.TextNode{Text: text}
	for _, opt := range opts {
		if opt != nil {
			opt(tn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Text{Text: tn},
	}
}

// Badge constructs a pill badge display node.
func Badge(id, text string, tone pluginv1.SemanticTone) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Badge{
			Badge: &pluginv1.BadgeNode{
				Text: text,
				Tone: tone,
			},
		},
	}
}

// ImageOption configures an ImageNode.
type ImageOption func(*pluginv1.ImageNode)

// WithFillMode configures image fill scaling.
func WithFillMode(mode pluginv1.ImageFillMode) ImageOption {
	return func(n *pluginv1.ImageNode) { n.FillMode = mode }
}

// ImageURL constructs an HTTPS image thumbnail node resolved by the host cache.
func ImageURL(id, rawURL string, w, h float32, opts ...ImageOption) *pluginv1.UINode {
	in := &pluginv1.ImageNode{
		Url:    rawURL,
		Width:  w,
		Height: h,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(in)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Image{Image: in},
	}
}

// ImageBytes constructs an inline image node from encoded image bytes.
func ImageBytes(id string, data []byte, w, h float32, opts ...ImageOption) *pluginv1.UINode {
	in := &pluginv1.ImageNode{
		Data:   data,
		Width:  w,
		Height: h,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(in)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Image{Image: in},
	}
}

// ImageIcon constructs an image node backed by a named TownCrier/Fyne built-in icon.
func ImageIcon(id, iconName string, w, h float32) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Image{
			Image: &pluginv1.ImageNode{
				IconName: iconName,
				Width:    w,
				Height:   h,
			},
		},
	}
}

// ProgressOption configures a ProgressNode.
type ProgressOption func(*pluginv1.ProgressNode)

// WithIndeterminate switches a ProgressNode to indeterminate animation.
func WithIndeterminate() ProgressOption {
	return func(n *pluginv1.ProgressNode) { n.Indeterminate = true }
}

// Progress constructs a progress bar node (0.0–1.0).
func Progress(id string, val float64, opts ...ProgressOption) *pluginv1.UINode {
	pn := &pluginv1.ProgressNode{Value: val}
	for _, opt := range opts {
		if opt != nil {
			opt(pn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Progress{Progress: pn},
	}
}

// MeterOption configures a MeterNode.
type MeterOption func(*pluginv1.MeterNode)

// WithPeak sets peak level on a MeterNode.
func WithPeak(peak float64) MeterOption {
	return func(n *pluginv1.MeterNode) { n.Peak = peak }
}

// WithMeterLabel sets label text on a MeterNode.
func WithMeterLabel(label string) MeterOption {
	return func(n *pluginv1.MeterNode) { n.Label = label }
}

// WithMeterTone sets semantic tone on a MeterNode.
func WithMeterTone(tone pluginv1.SemanticTone) MeterOption {
	return func(n *pluginv1.MeterNode) { n.Tone = tone }
}

// Meter constructs a level meter node (0.0–1.0).
func Meter(id string, val float64, opts ...MeterOption) *pluginv1.UINode {
	mn := &pluginv1.MeterNode{Value: val}
	for _, opt := range opts {
		if opt != nil {
			opt(mn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Meter{Meter: mn},
	}
}

// Markdown constructs a rich markdown text node.
func Markdown(id, md string) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_Markdown{
			Markdown: &pluginv1.MarkdownNode{Markdown: md},
		},
	}
}

// EmptyState constructs a centered empty state display with an optional action button.
func EmptyState(id, title, subtitle, iconName, actionID, actionLabel string) *pluginv1.UINode {
	return &pluginv1.UINode{
		Id: id,
		Kind: &pluginv1.UINode_EmptyState{
			EmptyState: &pluginv1.EmptyStateNode{
				Title:       title,
				Subtitle:    subtitle,
				IconName:    iconName,
				ActionId:    actionID,
				ActionLabel: actionLabel,
			},
		},
	}
}

// ButtonOption configures a ButtonNode.
type ButtonOption func(*pluginv1.ButtonNode)

// WithIcon sets a named built-in icon on a button.
func WithIcon(iconName string) ButtonOption {
	return func(n *pluginv1.ButtonNode) { n.IconName = iconName }
}

// WithImportance sets semantic emphasis on a button.
func WithImportance(imp pluginv1.ButtonImportance) ButtonOption {
	return func(n *pluginv1.ButtonNode) { n.Importance = imp }
}

// WithTooltip sets hover/status tooltip text on a button.
func WithTooltip(tip string) ButtonOption {
	return func(n *pluginv1.ButtonNode) { n.Tooltip = tip }
}

// WithButtonDisabled sets the initial disabled state on a button.
func WithButtonDisabled(disabled bool) ButtonOption {
	return func(n *pluginv1.ButtonNode) { n.Disabled = disabled }
}

// Button constructs an interactive button node.
func Button(id, label string, opts ...ButtonOption) *pluginv1.UINode {
	bn := &pluginv1.ButtonNode{Label: label}
	for _, opt := range opts {
		if opt != nil {
			opt(bn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Button{Button: bn},
	}
}

// TextInputOption configures a TextInputNode.
type TextInputOption func(*pluginv1.TextInputNode)

// WithPlaceholder sets placeholder text on a TextInputNode.
func WithPlaceholder(placeholder string) TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.Placeholder = placeholder }
}

// WithValue sets initial text value on a TextInputNode.
func WithValue(val string) TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.Value = val }
}

// WithForceValue forces replacement even if the TextInput currently has focus.
func WithForceValue(force bool) TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.ForceValue = force }
}

// WithMultiline enables multiline editing.
func WithMultiline() TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.Multiline = true }
}

// WithPassword masks input text as a password entry.
func WithPassword() TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.Password = true }
}

// WithDebounce sets debounce delay in milliseconds for CHANGE events.
func WithDebounce(ms uint32) TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.DebounceMs = ms }
}

// WithInputDisabled sets disabled state on a TextInputNode.
func WithInputDisabled(disabled bool) TextInputOption {
	return func(n *pluginv1.TextInputNode) { n.Disabled = disabled }
}

// TextInput constructs an interactive text entry node.
func TextInput(id string, opts ...TextInputOption) *pluginv1.UINode {
	in := &pluginv1.TextInputNode{}
	for _, opt := range opts {
		if opt != nil {
			opt(in)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_TextInput{TextInput: in},
	}
}

// CheckboxOption configures a CheckboxNode.
type CheckboxOption func(*pluginv1.CheckboxNode)

// WithCheckboxDisabled disables the checkbox.
func WithCheckboxDisabled(disabled bool) CheckboxOption {
	return func(n *pluginv1.CheckboxNode) { n.Disabled = disabled }
}

// Checkbox constructs a toggle checkbox node.
func Checkbox(id, label string, checked bool, opts ...CheckboxOption) *pluginv1.UINode {
	cn := &pluginv1.CheckboxNode{
		Label:   label,
		Checked: checked,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Checkbox{Checkbox: cn},
	}
}

// SelectOption configures a SelectNode.
type SelectOption func(*pluginv1.SelectNode)

// WithSelectPlaceholder sets placeholder text on a SelectNode.
func WithSelectPlaceholder(placeholder string) SelectOption {
	return func(n *pluginv1.SelectNode) { n.Placeholder = placeholder }
}

// WithSelectDisabled disables a SelectNode.
func WithSelectDisabled(disabled bool) SelectOption {
	return func(n *pluginv1.SelectNode) { n.Disabled = disabled }
}

// Select constructs a dropdown select node.
func Select(id string, options []string, selected string, opts ...SelectOption) *pluginv1.UINode {
	sn := &pluginv1.SelectNode{
		Options:  append([]string(nil), options...),
		Selected: selected,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Select{Select: sn},
	}
}

// SliderOption configures a SliderNode.
type SliderOption func(*pluginv1.SliderNode)

// WithSliderDisabled disables a SliderNode.
func WithSliderDisabled(disabled bool) SliderOption {
	return func(n *pluginv1.SliderNode) { n.Disabled = disabled }
}

// Slider constructs a numeric slider node.
func Slider(id string, minVal, maxVal, step, value float64, opts ...SliderOption) *pluginv1.UINode {
	sn := &pluginv1.SliderNode{
		Min:   minVal,
		Max:   maxVal,
		Step:  step,
		Value: value,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sn)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_Slider{Slider: sn},
	}
}

// ListRow constructs a keyed item row for VirtualList.
func ListRow(key string, content *pluginv1.UINode) *pluginv1.VirtualListRow {
	return &pluginv1.VirtualListRow{
		Key:     key,
		Content: content,
	}
}

// VirtualListOption configures a VirtualListNode.
type VirtualListOption func(*pluginv1.VirtualListNode)

// WithListMinHeight sets minimum height in DIPs for a VirtualList container.
func WithListMinHeight(h float32) VirtualListOption {
	return func(n *pluginv1.VirtualListNode) { n.MinHeight = h }
}

// VirtualList constructs a virtualized scrollable list of keyed rows.
func VirtualList(id string, rows []*pluginv1.VirtualListRow, opts ...VirtualListOption) *pluginv1.UINode {
	filtered := make([]*pluginv1.VirtualListRow, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			filtered = append(filtered, r)
		}
	}
	vl := &pluginv1.VirtualListNode{Rows: filtered}
	for _, opt := range opts {
		if opt != nil {
			opt(vl)
		}
	}
	return &pluginv1.UINode{
		Id:   id,
		Kind: &pluginv1.UINode_VirtualList{VirtualList: vl},
	}
}
