package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var resHelpQuestion = fyne.NewStaticResource("help_question.svg", []byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="20" height="20">`+
		`<circle cx="8" cy="8" r="7.2" fill="#1E293B" stroke="#38BDF8" stroke-width="1.3"/>`+
		`<path d="M5.255 5.786a.237.237 0 0 0 .241.247h.825c.138 0 .248-.113.266-.25.09-.656.54-1.134 1.342-1.134.686 0 1.314.343 1.314 1.168 0 .635-.374.927-.965 1.371-.673.489-1.206 1.06-1.168 1.987l.003.217a.25.25 0 0 0 .25.246h.811a.25.25 0 0 0 .25-.25v-.105c0-.718.273-.927 1.01-1.486.609-.463 1.244-.977 1.244-2.056 0-1.511-1.276-2.241-2.673-2.241-1.267 0-2.655.59-2.75 2.286zm1.557 5.763c0 .533.425.927 1.01.927.609 0 1.028-.394 1.028-.927 0-.552-.42-.94-1.029-.94-.584 0-1.009.388-1.009.94z" fill="#38BDF8"/>`+
		`</svg>`))

// TemplateVar defines a single template placeholder and its human-readable explanation.
type TemplateVar struct {
	Var  string
	Desc string
}

type templateHelpMarker struct {
	widget.BaseWidget
	win      fyne.Window
	eventKey string
	title    string
	vars     []TemplateVar
	onStatus func(string)
	target   *widget.Entry
	minSize  fyne.Size
	iconImg  *canvas.Image
	disabled bool
}

var (
	_ fyne.Widget        = (*templateHelpMarker)(nil)
	_ fyne.Disableable   = (*templateHelpMarker)(nil)
	_ desktop.Hoverable  = (*templateHelpMarker)(nil)
	_ desktop.Cursorable = (*templateHelpMarker)(nil)
	_ fyne.Tappable      = (*templateHelpMarker)(nil)
)

// newTemplateHelpMarker creates a compact '?' indicator that opens a centered template variable dialog on click and displays variable summaries on hover.
func newTemplateHelpMarker(win fyne.Window, eventKey, title string, vars []TemplateVar, onStatus func(string), target *widget.Entry) *templateHelpMarker {
	m := &templateHelpMarker{
		win:      win,
		eventKey: eventKey,
		title:    title,
		vars:     vars,
		onStatus: onStatus,
		target:   target,
		minSize:  fyne.NewSize(20, 20),
	}
	m.iconImg = canvas.NewImageFromResource(resHelpQuestion)
	m.iconImg.FillMode = canvas.ImageFillContain
	m.iconImg.SetMinSize(m.minSize)
	m.ExtendBaseWidget(m)
	return m
}

func (m *templateHelpMarker) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(m.iconImg)
}

func (m *templateHelpMarker) Cursor() desktop.Cursor {
	if m.Disabled() {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (m *templateHelpMarker) Enable() {
	m.disabled = false
	m.Refresh()
}

func (m *templateHelpMarker) Disable() {
	m.disabled = true
	m.Refresh()
}

func (m *templateHelpMarker) Disabled() bool {
	return m.disabled
}

func (m *templateHelpMarker) summaryText() string {
	parts := make([]string, 0, len(m.vars))
	for _, v := range m.vars {
		parts = append(parts, v.Var+" ("+v.Desc+")")
	}
	return strings.Join(parts, ", ")
}

func insertVariable(entry *widget.Entry, variable string) {
	if entry == nil {
		return
	}
	if entry.Text == "" {
		entry.SetText(variable)
		entry.CursorColumn = len([]rune(variable))
		return
	}
	runes := []rune(entry.Text)
	col := entry.CursorColumn
	if col < 0 || col > len(runes) {
		col = len(runes)
	}

	toInsert := variable
	if col > 0 && runes[col-1] != ' ' {
		toInsert = " " + toInsert
	}
	if col < len(runes) && runes[col] != ' ' {
		toInsert += " "
	}

	toInsertRunes := []rune(toInsert)
	newRunes := make([]rune, 0, len(runes)+len(toInsertRunes))
	newRunes = append(newRunes, runes[:col]...)
	newRunes = append(newRunes, toInsertRunes...)
	newRunes = append(newRunes, runes[col:]...)

	entry.SetText(string(newRunes))
	entry.CursorColumn = col + len(toInsertRunes)
}

func (m *templateHelpMarker) showDialog() {
	if m.win == nil {
		return
	}

	headerLabel := widget.NewLabelWithStyle(
		"Click a variable to insert it into the template and copy to clipboard:",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)

	statusLabel := widget.NewLabel("")
	statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	varRows := container.NewVBox()

	for _, v := range m.vars {
		variable := v.Var
		btn := widget.NewButton(variable, func() {
			if m.target != nil {
				insertVariable(m.target, variable)
			}
			if app := fyne.CurrentApp(); app != nil && app.Clipboard() != nil {
				app.Clipboard().SetContent(variable)
			}
			if m.onStatus != nil {
				m.onStatus("Copied " + variable + " to clipboard")
			}
			if m.target != nil {
				statusLabel.SetText("✓ Inserted " + variable + " (copied to clipboard)")
			} else {
				statusLabel.SetText("✓ Copied " + variable + " to clipboard")
			}
		})
		btn.Importance = widget.LowImportance

		descLabel := widget.NewLabel(v.Desc)
		row := container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(140, 36), btn), nil, descLabel)
		varRows.Add(row)
	}

	topBox := container.NewVBox(
		headerLabel,
		widget.NewSeparator(),
	)
	bottomBox := container.NewVBox(
		widget.NewSeparator(),
		statusLabel,
	)

	scroll := container.NewVScroll(varRows)
	content := container.NewBorder(topBox, bottomBox, nil, nil, scroll)

	dlg := dialog.NewCustom(m.title+" Template Variables", "Close", content, m.win)
	dlg.Resize(fyne.NewSize(480, 340))
	dlg.Show()
}

func (m *templateHelpMarker) MouseIn(_ *desktop.MouseEvent) {
	if m.Disabled() {
		return
	}
	if m.onStatus != nil {
		m.onStatus("Template variables for " + m.title + ": " + m.summaryText())
	}
}

func (m *templateHelpMarker) MouseMoved(_ *desktop.MouseEvent) {}

func (m *templateHelpMarker) MouseOut() {}

func (m *templateHelpMarker) Tapped(_ *fyne.PointEvent) {
	if m.Disabled() {
		return
	}
	m.showDialog()
}

// EventTemplateVars returns the list of available template variables for each platform notification event key.
func EventTemplateVars(platform, eventKey string) []TemplateVar {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "twitch":
		switch eventKey {
		case "sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Subscriber username"},
			}
		case "resub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Subscriber username"},
				{Var: "{months}", Desc: "Cumulative months subscribed"},
				{Var: "{message}", Desc: "Resubscription message"},
			}
		case "gift_sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Gifter username"},
				{Var: "{count}", Desc: "Number of subscriptions gifted"},
				{Var: "{recipient}", Desc: "Recipient username (if single gift)"},
			}
		case "sub_upgrade":
			return []TemplateVar{
				{Var: "{user}", Desc: "User who upgraded / paid forward"},
				{Var: "{recipient}", Desc: "Recipient or original gifter"},
				{Var: "{message}", Desc: "Optional user message"},
			}
		case "watch_streak":
			return []TemplateVar{
				{Var: "{user}", Desc: "Chatter username"},
				{Var: "{count}", Desc: "Consecutive stream streak count"},
				{Var: "{amount}", Desc: "Channel points awarded (if any)"},
				{Var: "{message}", Desc: "Viewer's custom streak message"},
			}
		case "cheer":
			return []TemplateVar{
				{Var: "{user}", Desc: "Cheerer username"},
				{Var: "{amount}", Desc: "Number of bits cheered ({bits})"},
				{Var: "{message}", Desc: "Cheer message"},
			}
		case "bits_badge_tier":
			return []TemplateVar{
				{Var: "{user}", Desc: "Chatter username"},
				{Var: "{amount}", Desc: "Bits milestone unlocked (e.g. 1000)"},
				{Var: "{message}", Desc: "Optional message"},
			}
		case "raid":
			return []TemplateVar{
				{Var: "{user}", Desc: "Raiding streamer username"},
				{Var: "{count}", Desc: "Number of viewers in the raid ({viewers})"},
			}
		case "redemption":
			return []TemplateVar{
				{Var: "{user}", Desc: "Redeemer username"},
				{Var: "{reward}", Desc: "Channel point reward title"},
				{Var: "{amount}", Desc: "Channel point cost"},
				{Var: "{message}", Desc: "User message / input"},
			}
		case "charity_donation":
			return []TemplateVar{
				{Var: "{user}", Desc: "Donor username"},
				{Var: "{amount}", Desc: "Donation amount"},
				{Var: "{currency}", Desc: "Currency code (e.g. USD)"},
				{Var: "{reward}", Desc: "Charity name"},
				{Var: "{message}", Desc: "Donor message"},
			}
		case "announcement":
			return []TemplateVar{
				{Var: "{user}", Desc: "Broadcaster or moderator username"},
				{Var: "{message}", Desc: "Announcement text"},
			}
		case "follow":
			return []TemplateVar{
				{Var: "{user}", Desc: "Follower username"},
			}
		}

	case "youtube":
		switch eventKey {
		case "super_chat":
			return []TemplateVar{
				{Var: "{user}", Desc: "Sender username"},
				{Var: "{amount}", Desc: "Super Chat amount"},
				{Var: "{currency}", Desc: "Currency code (e.g. USD)"},
				{Var: "{message}", Desc: "Super Chat message"},
			}
		case "super_sticker":
			return []TemplateVar{
				{Var: "{user}", Desc: "Sender username"},
				{Var: "{amount}", Desc: "Super Sticker amount"},
				{Var: "{currency}", Desc: "Currency code (e.g. USD)"},
			}
		case "gift":
			return []TemplateVar{
				{Var: "{user}", Desc: "Sender username"},
				{Var: "{gift}", Desc: "Gift item name (e.g. 100, Donut)"},
				{Var: "{count}", Desc: "Number of gifts sent"},
				{Var: "{amount}", Desc: "Jewel amount (if provided by YouTube)"},
			}
		case "sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "New member username"},
			}
		case "resub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Member username"},
				{Var: "{months}", Desc: "Months as a member"},
				{Var: "{message}", Desc: "Member milestone message"},
			}
		case "gift_sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Gifter username"},
				{Var: "{count}", Desc: "Number of memberships gifted"},
			}
		case "gift_redemption":
			return []TemplateVar{
				{Var: "{user}", Desc: "Recipient username"},
				{Var: "{recipient}", Desc: "Gifter username"},
			}
		case "raid":
			return []TemplateVar{
				{Var: "{user}", Desc: "Redirecting channel username"},
			}
		case "charity_donation":
			return []TemplateVar{
				{Var: "{user}", Desc: "Donor username"},
				{Var: "{amount}", Desc: "Donation amount"},
				{Var: "{currency}", Desc: "Currency code"},
				{Var: "{message}", Desc: "Donor message"},
			}
		}

	case "tiktok":
		switch eventKey {
		case "gift":
			return []TemplateVar{
				{Var: "{user}", Desc: "Sender username"},
				{Var: "{count}", Desc: "Number of gifts sent"},
				{Var: "{gift}", Desc: "Gift name"},
				{Var: "{amount}", Desc: "Diamond value ({diamonds})"},
				{Var: "{message}", Desc: "Message (if any)"},
			}
		case "sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Super Fan username"},
			}
		case "resub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Super Fan username"},
				{Var: "{months}", Desc: "Months subscribed"},
			}
		case "gift_sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Gifter username"},
				{Var: "{count}", Desc: "Number of Super Fans gifted"},
			}
		case "follow":
			return []TemplateVar{
				{Var: "{user}", Desc: "Follower username"},
			}
		case "share":
			return []TemplateVar{
				{Var: "{user}", Desc: "User who shared the stream"},
			}
		}

	case "kick":
		switch eventKey {
		case "sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Subscriber username"},
			}
		case "resub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Subscriber username"},
				{Var: "{months}", Desc: "Months subscribed"},
			}
		case "gift_sub":
			return []TemplateVar{
				{Var: "{user}", Desc: "Gifter username"},
				{Var: "{count}", Desc: "Number of subscriptions gifted"},
				{Var: "{recipient}", Desc: "Recipient username(s)"},
			}
		case "gift":
			return []TemplateVar{
				{Var: "{user}", Desc: "Sender username"},
				{Var: "{count}", Desc: "Number of Kicks / gifts sent"},
				{Var: "{reward}", Desc: "Gift name ({gift})"},
				{Var: "{message}", Desc: "Sender message"},
			}
		case "redemption":
			return []TemplateVar{
				{Var: "{user}", Desc: "Redeemer username"},
				{Var: "{reward}", Desc: "Channel reward title"},
				{Var: "{amount}", Desc: "Point cost"},
				{Var: "{message}", Desc: "User input / message"},
			}
		case "raid":
			return []TemplateVar{
				{Var: "{user}", Desc: "Host / raid channel username"},
				{Var: "{count}", Desc: "Viewer count ({viewers})"},
			}
		case "follow":
			return []TemplateVar{
				{Var: "{user}", Desc: "Follower username"},
			}
		}
	}

	return []TemplateVar{
		{Var: "{user}", Desc: "Chatter / actor username"},
		{Var: "{message}", Desc: "Chat or event message"},
	}
}
