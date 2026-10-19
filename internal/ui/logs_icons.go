package ui

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// Vector SVG resources for platform badges, user role tags, filter status, spoken status, and voice.
var (
	resPlatformTwitch = fyne.NewStaticResource("plat_twitch.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#9146FF"/>`+
			`<path d="M7 5L5 8v10h4v3l3-3h3l4-4V5H7zm10 8l-2 2h-3l-2 2v-2H7V7h10v6z" fill="#FFFFFF"/>`+
			`<rect x="11" y="9" width="1.8" height="4" fill="#FFFFFF"/>`+
			`<rect x="14" y="9" width="1.8" height="4" fill="#FFFFFF"/>`+
			`</svg>`))

	resPlatformYouTube = fyne.NewStaticResource("plat_youtube.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="4" width="20" height="16" rx="4" fill="#EF4444"/>`+
			`<polygon points="10,8 16.5,12 10,16" fill="#FFFFFF"/>`+
			`</svg>`))

	resPlatformTikTok = fyne.NewStaticResource("plat_tiktok.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#1E293B"/>`+
			`<path d="M16 8.2c-1.3-.3-2.3-1.3-2.6-2.6V5h-2.5v9.2c0 1.2-1 2.2-2.2 2.2s-2.2-1-2.2-2.2 1-2.2 2.2-2.2c.3 0 .5 0 .8.1V9.5c-.3 0-.5-.1-.8-.1-2.6 0-4.7 2.1-4.7 4.7S6.1 18.8 8.7 18.8s4.7-2.1 4.7-4.7V9.8c.8.6 1.8.9 2.9.9V8.2H16z" fill="#38BDF8"/>`+
			`</svg>`))

	resPlatformKick = fyne.NewStaticResource("plat_kick.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#53FC18"/>`+
			`<path d="M7 6h3v4.5L14 6h3.5l-5 5.5L17.8 18H14l-4-5v5H7V6z" fill="#0F172A"/>`+
			`</svg>`))

	resPlatformGeneric = fyne.NewStaticResource("plat_generic.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#334155"/>`+
			`<circle cx="12" cy="12" r="6" fill="#38BDF8"/>`+
			`</svg>`))

	// User Role / Tag Icons
	resRoleBroadcaster = fyne.NewStaticResource("role_broadcaster.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#EF4444"/>`+
			`<polygon points="4,17 6,8 10,12 12,6 14,12 18,8 20,17" fill="#FDE047"/>`+
			`</svg>`))

	resRoleModerator = fyne.NewStaticResource("role_moderator.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#22C55E"/>`+
			`<path d="M12 4L5 7v5c0 4.5 3 8.5 7 9.8 4-1.3 7-5.3 7-9.8V7l-7-3zm-1.2 12.2L7.3 12.7l1.4-1.4 2.1 2.1 4.5-4.5 1.4 1.4-5.9 5.9z" fill="#FFFFFF"/>`+
			`</svg>`))

	resRoleVIP = fyne.NewStaticResource("role_vip.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#EC4899"/>`+
			`<polygon points="12,4 19,10 12,20 5,10" fill="#FFFFFF"/>`+
			`</svg>`))

	resRoleSubscriber = fyne.NewStaticResource("role_subscriber.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#8B5CF6"/>`+
			`<polygon points="12,4 14.5,9.2 20,10 16,14 17,19.5 12,16.8 7,19.5 8,14 4,10 9.5,9.2" fill="#FDE047"/>`+
			`</svg>`))

	resRolePaidEvent = fyne.NewStaticResource("role_paid.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#F59E0B"/>`+
			`<circle cx="12" cy="12" r="6" fill="#FEF08A"/>`+
			`<rect x="11" y="8" width="2" height="8" fill="#B45309"/>`+
			`</svg>`))

	resEventFollow = fyne.NewStaticResource("event_follow.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#F43F5E"/>`+
			`<path d="M12 18.5l-1.1-1C6.8 13.8 4.5 11.7 4.5 9.1 4.5 7 6.2 5.5 8.3 5.5c1.2 0 2.4.6 3.7 1.8 1.3-1.2 2.5-1.8 3.7-1.8 2.1 0 3.8 1.5 3.8 3.6 0 2.6-2.3 4.7-6.4 8.4l-1.1 1z" fill="#FFFFFF"/>`+
			`</svg>`))

	resEventSub = fyne.NewStaticResource("event_sub.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#7C3AED"/>`+
			`<polygon points="12,4.5 14.3,9.2 19.5,10 15.7,13.6 16.6,18.8 12,16.3 7.4,18.8 8.3,13.6 4.5,10 9.7,9.2" fill="#FDE047"/>`+
			`</svg>`))

	resEventCheer = fyne.NewStaticResource("event_cheer.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#F59E0B"/>`+
			`<polygon points="12,4 19,11 12,20 5,11" fill="#FEF08A"/>`+
			`</svg>`))

	resEventRaid = fyne.NewStaticResource("event_raid.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#0284C7"/>`+
			`<polygon points="13,4 6,13 11,13 10,20 18,11 13,11" fill="#FDE047"/>`+
			`</svg>`))

	resEventRedemption = fyne.NewStaticResource("event_redemption.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<rect x="2" y="2" width="20" height="20" rx="4" fill="#10B981"/>`+
			`<circle cx="12" cy="12" r="6" fill="#FFFFFF"/>`+
			`<circle cx="12" cy="12" r="3" fill="#10B981"/>`+
			`</svg>`))

	// Passed Filters Icons
	resFilterPassed = fyne.NewStaticResource("filter_passed.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<circle cx="12" cy="12" r="10" fill="#22C55E"/>`+
			`<path d="M9.5 16.2L5.8 12.5l1.4-1.4 2.3 2.3 6.3-6.3 1.4 1.4-7.7 7.7z" fill="#FFFFFF"/>`+
			`</svg>`))

	resFilterBlocked = fyne.NewStaticResource("filter_blocked.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<circle cx="12" cy="12" r="10" fill="#EF4444"/>`+
			`<path d="M16.2 9.2L13.4 12l2.8 2.8-1.4 1.4L12 13.4l-2.8 2.8-1.4-1.4L10.6 12 7.8 9.2l1.4-1.4L12 10.6l2.8-2.8 1.4 1.4z" fill="#FFFFFF"/>`+
			`</svg>`))

	resFilterMod = fyne.NewStaticResource("filter_mod.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M12 2L4 5v6.1c0 5 3.4 9.7 8 10.9 4.6-1.2 8-5.9 8-10.9V5l-8-3z" fill="#38BDF8"/>`+
			`</svg>`))

	// Spoken / Queue Outcome Icons
	resSpokenYes = fyne.NewStaticResource("spoken_yes.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M3 9v6h4l5 5V4L7 9H3zm13.5 3c0-1.77-1.02-3.29-2.5-4.03v8.05c1.48-.73 2.5-2.25 2.5-4.02zM14 3.23v2.06c2.89.86 5 3.54 5 6.71s-2.11 5.85-5 6.71v2.06c4.01-.91 7-4.49 7-8.77s-2.99-7.86-7-8.77z" fill="#22C55E"/>`+
			`</svg>`))

	resSpokenQueued = fyne.NewStaticResource("spoken_queued.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<circle cx="12" cy="12" r="10" fill="#0284C7"/>`+
			`<path d="M11 6h2v6l4.2 2.5-1 1.7L11 13V6z" fill="#FFFFFF"/>`+
			`</svg>`))

	resSpokenStale = fyne.NewStaticResource("spoken_stale.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<circle cx="12" cy="12" r="10" fill="#F59E0B"/>`+
			`<path d="M11 6h2v6l4 2.4-1 1.6L11 13V6z" fill="#0F172A"/>`+
			`</svg>`))

	resSpokenPanic = fyne.NewStaticResource("spoken_panic.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M16.5 12c0-1.77-1.02-3.29-2.5-4.03v2.21l2.45 2.45c.03-.2.05-.41.05-.63zm2.5 0c0 .94-.2 1.82-.54 2.64l1.51 1.51C20.63 14.91 21 13.5 21 12c0-4.28-2.99-7.86-7-8.77v2.06c2.89.86 5 3.54 5 6.71zM4.27 3L3 4.27 7.73 9H3v6h4l5 5v-6.73l4.25 4.25c-.67.52-1.42.93-2.25 1.18v2.06c1.38-.31 2.63-.95 3.69-1.81L19.73 21 21 19.73l-9-9L4.27 3zM12 4L9.91 6.09 12 8.18V4z" fill="#EF4444"/>`+
			`</svg>`))

	resSpokenSkipped = fyne.NewStaticResource("spoken_skipped.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M6 18l8.5-6L6 6v12zM16 6v12h2V6h-2z" fill="#F59E0B"/>`+
			`</svg>`))

	resSpokenNo = fyne.NewStaticResource("spoken_no.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<circle cx="12" cy="12" r="8" fill="#334155"/>`+
			`<rect x="7" y="11" width="10" height="2" fill="#94A3B8"/>`+
			`</svg>`))

	// Voice Icons
	resVoiceActive = fyne.NewStaticResource("voice_active.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M12 14c1.66 0 3-1.34 3-3V5c0-1.66-1.34-3-3-3S9 3.34 9 5v6c0 1.66 1.34 3 3 3zm5.91-3c-.49 0-.9.36-.98.85C16.52 14.2 14.47 16 12 16s-4.52-1.8-4.93-4.15c-.08-.49-.49-.85-.98-.85-.61 0-1.09.54-1 1.14.49 3 2.89 5.35 5.91 5.78V20c0 .55.45 1 1 1s1-.45 1-1v-2.08c3.02-.43 5.42-2.78 5.91-5.78.1-.6-.39-1.14-1-1.14z" fill="#38BDF8"/>`+
			`</svg>`))

	resVoiceUnassigned = fyne.NewStaticResource("voice_unassigned.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">`+
			`<path d="M12 14c1.66 0 3-1.34 3-3V5c0-1.66-1.34-3-3-3S9 3.34 9 5v6c0 1.66 1.34 3 3 3zm5.91-3c-.49 0-.9.36-.98.85C16.52 14.2 14.47 16 12 16s-4.52-1.8-4.93-4.15c-.08-.49-.49-.85-.98-.85-.61 0-1.09.54-1 1.14.49 3 2.89 5.35 5.91 5.78V20c0 .55.45 1 1 1s1-.45 1-1v-2.08c3.02-.43 5.42-2.78 5.91-5.78.1-.6-.39-1.14-1-1.14z" fill="#64748B"/>`+
			`</svg>`))
)

// platformIconAndTip returns the SVG icon resource and tooltip description for a platform.
func platformIconAndTip(platform string) (fyne.Resource, string) {
	var name string
	var res fyne.Resource
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "twitch":
		name = "Twitch"
		res = resPlatformTwitch
	case "youtube":
		name = "YouTube"
		res = resPlatformYouTube
	case "tiktok":
		name = "TikTok"
		res = resPlatformTikTok
	case "kick":
		name = "Kick"
		res = resPlatformKick
	default:
		if platform == "" {
			name = "Unknown Platform"
		} else {
			name = strings.ToUpper(platform)
		}
		res = resPlatformGeneric
	}

	return res, "Service: " + name
}

// userRoleIcons returns compact hoverable role/tag icons for a log entry.
func (v *LogsView) userRoleIcons(e eventlog.Entry) []fyne.CanvasObject {
	var icons []fyne.CanvasObject
	seen := make(map[string]bool)

	for _, rawRole := range e.Roles {
		r := strings.ToLower(strings.TrimSpace(rawRole))
		switch r {
		case "broadcaster", "streamer", "owner":
			if !seen["broadcaster"] {
				seen["broadcaster"] = true
				icons = append(icons, v.newHoverIcon(resRoleBroadcaster, fyne.NewSize(16, 16), "Tag: Broadcaster / Streamer", nil))
			}
		case "moderator", "mod":
			if !seen["moderator"] {
				seen["moderator"] = true
				icons = append(icons, v.newHoverIcon(resRoleModerator, fyne.NewSize(16, 16), "Tag: Moderator", nil))
			}
		case "vip":
			if !seen["vip"] {
				seen["vip"] = true
				icons = append(icons, v.newHoverIcon(resRoleVIP, fyne.NewSize(16, 16), "Tag: VIP", nil))
			}
		case "subscriber", "sub", "founder", "member":
			if !seen["subscriber"] {
				seen["subscriber"] = true
				icons = append(icons, v.newHoverIcon(resRoleSubscriber, fyne.NewSize(16, 16), "Tag: Subscriber / Member", nil))
			}
		}
	}

	kindLower := strings.ToLower(strings.TrimSpace(e.Kind))
	if e.IsPaidOrEvent || (kindLower != "" && kindLower != "message" && kindLower != "chat" && kindLower != "moderation") {
		evIcon := resRolePaidEvent
		switch kindLower {
		case "follow":
			evIcon = resEventFollow
		case "sub", "resub", "gift_sub", "membership", "gift_membership":
			evIcon = resEventSub
		case "cheer", "gift", "superchat", "supersticker":
			evIcon = resEventCheer
		case "raid":
			evIcon = resEventRaid
		case "redemption":
			evIcon = resEventRedemption
		}
		tip := "Stream Event: " + strings.ToUpper(e.Kind)
		if e.Amount > 0 {
			if e.Currency != "" {
				tip += fmt.Sprintf(" (%.0f %s)", e.Amount, e.Currency)
			} else {
				tip += fmt.Sprintf(" (%.0f)", e.Amount)
			}
		}
		if e.Months > 0 {
			tip += fmt.Sprintf(" (%d months)", e.Months)
		}
		if e.Count > 0 {
			tip += fmt.Sprintf(" (x%d)", e.Count)
		}
		icons = append(icons, v.newHoverIcon(evIcon, fyne.NewSize(16, 16), tip, nil))
	}

	return icons
}

// filterStatusIconAndTip returns the icon and mouse-over tooltip for the Passed Filters column.
func filterStatusIconAndTip(e eventlog.Entry) (fyne.Resource, string) {
	if e.Kind == "moderation" || e.Outcome == eventlog.OutcomeModerationEvent {
		tip := "Moderation Sync Event"
		if e.OutcomeDetail != "" {
			tip += ": " + e.OutcomeDetail
		}
		return resFilterMod, tip
	}
	if e.PassedFilters {
		return resFilterPassed, "Passed Filters: Yes (Eligible for TTS Queue)"
	}
	tip := "Passed Filters: No — " + eventlog.OutcomeDisplayLabel(e.Outcome)
	if e.OutcomeDetail != "" {
		tip += " (" + e.OutcomeDetail + ")"
	}
	return resFilterBlocked, tip
}

// spokenStatusIconAndTip returns the icon and mouse-over tooltip for the Spoken / Queue Fate column.
func spokenStatusIconAndTip(e eventlog.Entry) (fyne.Resource, string) {
	if e.Spoken || e.Outcome == eventlog.OutcomeSpoken {
		tip := "Spoken: Yes"
		if e.Speed > 0 {
			tip += fmt.Sprintf(" (%.2fx speed)", e.Speed)
		}
		return resSpokenYes, tip
	}

	switch e.Outcome {
	case eventlog.OutcomeQueued:
		return resSpokenQueued, "Spoken: Pending in Speech Queue"
	case eventlog.OutcomeDroppedStale:
		tip := "Spoken: No — Catch-Up Dropped (>30s stale TTL in queue)"
		if e.OutcomeDetail != "" {
			tip += ": " + e.OutcomeDetail
		}
		return resSpokenStale, tip
	case eventlog.OutcomeDroppedPanic:
		return resSpokenPanic, "Spoken: No — Aborted by Emergency Panic Mute"
	case eventlog.OutcomeErrorTTS:
		tip := "Spoken: No — TTS / Audio Playback Error"
		if e.OutcomeDetail != "" {
			tip += ": " + e.OutcomeDetail
		}
		return resSpokenPanic, tip
	case eventlog.OutcomeDroppedModeration, eventlog.OutcomeSkipped:
		tip := "Spoken: No — " + eventlog.OutcomeDisplayLabel(e.Outcome)
		if e.OutcomeDetail != "" {
			tip += " (" + e.OutcomeDetail + ")"
		}
		return resSpokenSkipped, tip
	default:
		return resSpokenNo, "Spoken: No (Filtered before reaching queue)"
	}
}

// voiceStatusIconAndTip returns the icon and mouse-over tooltip for the Voice column.
func voiceStatusIconAndTip(e eventlog.Entry) (fyne.Resource, string) {
	canAssign := e.UserName != "" && e.Kind != "moderation"
	if e.VoiceID != "" {
		voiceName := tts.VoiceDisplayName(e.VoiceID)
		tip := "Voice: " + voiceName
		if e.Speed > 0 {
			tip += fmt.Sprintf(" @ %.2fx", e.Speed)
		}
		if canAssign {
			tip += " — Click to assign chatter voice"
		}
		return resVoiceActive, tip
	}
	if canAssign {
		return resVoiceUnassigned, "Voice: None (Filtered) — Click to assign chatter voice"
	}
	return resVoiceUnassigned, "Voice: N/A"
}

// hoverIconWidget is a fixed-size SVG icon widget with mouse-over tooltip and optional click handler.
type hoverIconWidget struct {
	widget.BaseWidget
	res     fyne.Resource
	minSize fyne.Size
	tooltip string
	onHover func(obj fyne.CanvasObject, tip string, hovered bool)
	onTap   func()
}

var (
	_ fyne.Widget       = (*hoverIconWidget)(nil)
	_ desktop.Hoverable = (*hoverIconWidget)(nil)
	_ desktop.Cursorable = (*hoverIconWidget)(nil)
	_ fyne.Tappable     = (*hoverIconWidget)(nil)
)

func (v *LogsView) newHoverIcon(res fyne.Resource, size fyne.Size, tooltip string, onTap func()) *hoverIconWidget {
	w := &hoverIconWidget{
		res:     res,
		minSize: size,
		tooltip: tooltip,
		onHover: v.showTooltipForObject,
		onTap:   onTap,
	}
	w.ExtendBaseWidget(w)
	return w
}

func (w *hoverIconWidget) CreateRenderer() fyne.WidgetRenderer {
	img := canvas.NewImageFromResource(w.res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(w.minSize)
	return &hoverIconRenderer{w: w, img: img}
}

func (w *hoverIconWidget) MouseIn(*desktop.MouseEvent) {
	if w.onHover != nil && w.tooltip != "" {
		w.onHover(w, w.tooltip, true)
	}
}

func (w *hoverIconWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *hoverIconWidget) MouseOut() {
	if w.onHover != nil {
		w.onHover(w, "", false)
	}
}

func (w *hoverIconWidget) Cursor() desktop.Cursor {
	if w.onTap != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (w *hoverIconWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

type hoverIconRenderer struct {
	w   *hoverIconWidget
	img *canvas.Image
}

func (r *hoverIconRenderer) Destroy() {}

func (r *hoverIconRenderer) Layout(size fyne.Size) {
	w := r.w.minSize.Width
	h := r.w.minSize.Height
	x := (size.Width - w) / 2
	y := (size.Height - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	r.img.Move(fyne.NewPos(x, y))
	r.img.Resize(fyne.NewSize(w, h))
}

func (r *hoverIconRenderer) MinSize() fyne.Size {
	return r.w.minSize
}

func (r *hoverIconRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.img}
}

func (r *hoverIconRenderer) Refresh() {
	r.img.Resource = r.w.res
	r.img.Refresh()
}

// hoverCellLabel is a single-line truncated table cell label that displays a tooltip on mouse hover
// without expanding its minimum width to the full un-truncated text length.
type hoverCellLabel struct {
	widget.Label
	tooltip string
	onHover func(obj fyne.CanvasObject, tip string, hovered bool)
}

var (
	_ fyne.Widget       = (*hoverCellLabel)(nil)
	_ desktop.Hoverable = (*hoverCellLabel)(nil)
)

func (v *LogsView) newHoverCellLabel(text, tooltip string, style fyne.TextStyle) *hoverCellLabel {
	l := &hoverCellLabel{
		tooltip: tooltip,
		onHover: v.showTooltipForObject,
	}
	l.Text = text
	l.TextStyle = style
	l.Truncation = fyne.TextTruncateEllipsis
	l.ExtendBaseWidget(l)
	return l
}

func (v *LogsView) newTimeCellLabel(text, tooltip string) *hoverCellLabel {
	l := &hoverCellLabel{
		tooltip: tooltip,
		onHover: v.showTooltipForObject,
	}
	l.Text = text
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.Truncation = fyne.TextTruncateOff
	l.ExtendBaseWidget(l)
	return l
}

func (l *hoverCellLabel) MinSize() fyne.Size {
	ms := l.Label.MinSize()
	return fyne.NewSize(24, ms.Height)
}

func (l *hoverCellLabel) MouseIn(*desktop.MouseEvent) {
	if l.onHover != nil && l.tooltip != "" {
		l.onHover(l, l.tooltip, true)
	}
}

func (l *hoverCellLabel) MouseMoved(*desktop.MouseEvent) {}

func (l *hoverCellLabel) MouseOut() {
	if l.onHover != nil {
		l.onHover(l, "", false)
	}
}

// wrappedCellLabel is a multi-line word-wrapping table cell label whose MinSize width is clamped
// so long messages wrap vertically inside their column instead of expanding the window horizontally.
type wrappedCellLabel struct {
	widget.Label
	tooltip string
	onHover func(obj fyne.CanvasObject, tip string, hovered bool)
}

var (
	_ fyne.Widget       = (*wrappedCellLabel)(nil)
	_ desktop.Hoverable = (*wrappedCellLabel)(nil)
)

func newClampedWrapLabel(text string, style fyne.TextStyle) *wrappedCellLabel {
	l := &wrappedCellLabel{}
	l.Text = text
	l.TextStyle = style
	l.Wrapping = fyne.TextWrapBreak
	l.ExtendBaseWidget(l)
	return l
}

func (v *LogsView) newWrappedCellLabel(text, tooltip string, style fyne.TextStyle) *wrappedCellLabel {
	l := &wrappedCellLabel{
		tooltip: tooltip,
		onHover: v.showTooltipForObject,
	}
	l.Text = text
	l.TextStyle = style
	l.Wrapping = fyne.TextWrapBreak
	l.ExtendBaseWidget(l)
	return l
}

func (l *wrappedCellLabel) MinSize() fyne.Size {
	ms := l.Label.MinSize()
	return fyne.NewSize(80, ms.Height)
}

func (l *wrappedCellLabel) MouseIn(*desktop.MouseEvent) {
	if l.onHover != nil && l.tooltip != "" {
		l.onHover(l, l.tooltip, true)
	}
}

func (l *wrappedCellLabel) MouseMoved(*desktop.MouseEvent) {}

func (l *wrappedCellLabel) MouseOut() {
	if l.onHover != nil {
		l.onHover(l, "", false)
	}
}

// logTableLayout aligns the 7 columns of the Event Logs table across the sticky header and all rows:
// [0] Time | [1] Svc | [2] User & Tags | [3] Message (flex) | [4] Filter | [5] Spoken | [6] Voice
type logTableLayout struct {
	getWidth func() float32
}

const (
	colWidthTime    float32 = 88
	colWidthService float32 = 36
	colWidthUser    float32 = 195
	colWidthFilter  float32 = 48
	colWidthSpoken  float32 = 48
	colWidthVoice   float32 = 48
	colGap          float32 = 6
	rowMinHeight    float32 = 30
)

func computeMsgColWidth(totalWidth float32) float32 {
	fixedTotal := colWidthTime + colWidthService + colWidthUser +
		colWidthFilter + colWidthSpoken + colWidthVoice + (6 * colGap)
	msgWidth := totalWidth - fixedTotal
	if msgWidth < 160 {
		return 340
	}
	return msgWidth
}

func (l logTableLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 7 {
		return
	}

	msgWidth := computeMsgColWidth(size.Width)

	widths := [7]float32{
		colWidthTime,
		colWidthService,
		colWidthUser,
		msgWidth,
		colWidthFilter,
		colWidthSpoken,
		colWidthVoice,
	}

	var x float32
	for i := range 7 {
		obj := objects[i]
		w := widths[i]
		obj.Move(fyne.NewPos(x, 0))
		obj.Resize(fyne.NewSize(w, size.Height))
		x += w + colGap
	}
}

func (l logTableLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) >= 4 && objects[3] != nil {
		var totalW float32
		if l.getWidth != nil {
			totalW = l.getWidth()
		}
		msgW := computeMsgColWidth(totalW)
		cur := objects[3].Size()
		if cur.Width != msgW {
			objects[3].Resize(fyne.NewSize(msgW, cur.Height))
		}
	}

	maxH := rowMinHeight
	for _, obj := range objects {
		if obj != nil {
			ms := obj.MinSize()
			if ms.Height > maxH {
				maxH = ms.Height
			}
		}
	}
	minW := colWidthTime + colWidthService + colWidthUser +
		140 + colWidthFilter + colWidthSpoken + colWidthVoice + (6 * colGap)
	return fyne.NewSize(minW, maxH)
}

// buildTooltipBubble constructs the floating non-interactive tooltip box and returns its container and text element.
func buildTooltipBubble() (*fyne.Container, *canvas.Text) {
	bg := canvas.NewRectangle(color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xF8})
	bg.StrokeColor = colorSkyBlue
	bg.StrokeWidth = 1
	bg.CornerRadius = 4

	txt := canvas.NewText("", colorChromeWhite)
	txt.TextSize = 12
	txt.TextStyle = fyne.TextStyle{Bold: true}

	box := container.NewStack(
		bg,
		container.NewPadded(txt),
	)
	box.Hide()
	return box, txt
}
