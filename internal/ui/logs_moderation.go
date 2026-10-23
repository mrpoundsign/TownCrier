package ui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/platform/twitch"
)

func (v *LogsView) buildHeldQueueBanner() *fyne.Container {
	v.heldQueueHeaderLbl = widget.NewLabelWithStyle(
		"🛡️ HELD FOR REVIEW (0) — Oldest at Top (FIFO)",
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)

	v.heldQueueItemsBox = container.NewVBox()

	v.heldQueueToggleBtn = widget.NewButton("▾ Collapse", func() {
		v.mu.Lock()
		v.heldQueueExpanded = !v.heldQueueExpanded
		expanded := v.heldQueueExpanded
		v.mu.Unlock()

		if expanded {
			v.heldQueueToggleBtn.SetText("▾ Collapse")
			if v.heldQueueItemsBox != nil {
				v.heldQueueItemsBox.Show()
			}
		} else {
			v.heldQueueToggleBtn.SetText("▸ Expand")
			if v.heldQueueItemsBox != nil {
				v.heldQueueItemsBox.Hide()
			}
		}
	})

	headerBg := canvas.NewRectangle(color.NRGBA{R: 0x3B, G: 0x1D, B: 0x06, A: 0xFF})
	headerBorder := canvas.NewRectangle(color.NRGBA{R: 0xB4, G: 0x53, B: 0x09, A: 0xFF})
	headerBorder.SetMinSize(fyne.NewSize(1, 1))

	headerRow := container.NewStack(
		headerBg,
		container.NewBorder(
			nil,
			headerBorder,
			nil,
			container.NewPadded(v.heldQueueToggleBtn),
			container.NewPadded(v.heldQueueHeaderLbl),
		),
	)

	outerBg := canvas.NewRectangle(color.NRGBA{R: 0x22, G: 0x13, B: 0x07, A: 0xFF})
	bottomAccent := canvas.NewRectangle(color.NRGBA{R: 0xD9, G: 0x77, B: 0x06, A: 0xFF})
	bottomAccent.SetMinSize(fyne.NewSize(1, 2))

	box := container.NewStack(
		outerBg,
		container.NewBorder(
			headerRow,
			bottomAccent,
			nil,
			nil,
			v.heldQueueItemsBox,
		),
	)
	box.Hide()
	return box
}

func (v *LogsView) renderHeldQueue() {
	if v.heldQueueContainer == nil || v.heldQueueItemsBox == nil || v.heldQueueHeaderLbl == nil {
		return
	}

	var held []twitch.AutoModHoldEvent
	if v.session != nil {
		held = v.session.HeldMessages()
	}

	if len(held) == 0 {
		v.heldQueueItemsBox.Objects = nil
		v.heldQueueContainer.Hide()
		v.heldQueueContainer.Refresh()
		return
	}

	v.heldQueueHeaderLbl.SetText(fmt.Sprintf("🛡️ HELD FOR REVIEW (%d) — Oldest at Top (FIFO)", len(held)))
	v.heldQueueItemsBox.Objects = nil

	for i, ev := range held {
		v.heldQueueItemsBox.Add(v.buildHeldQueueRow(i, ev))
	}

	v.mu.Lock()
	expanded := v.heldQueueExpanded
	v.mu.Unlock()
	if expanded {
		v.heldQueueItemsBox.Show()
	} else {
		v.heldQueueItemsBox.Hide()
	}

	v.heldQueueContainer.Show()
	v.heldQueueContainer.Refresh()
}

func (v *LogsView) buildHeldQueueRow(idx int, ev twitch.AutoModHoldEvent) fyne.CanvasObject {
	s := v.chatScaleFactor()
	iconSz := fyne.NewSize(18*s, 18*s)

	ts := ev.HeldAt
	if ts.IsZero() {
		ts = time.Now()
	}
	timeLbl := v.newTimeCellLabel(ts.Local().Format("15:04:05"), "Held at "+ts.Local().Format("2006-01-02 15:04:05"))

	platIcon := v.newHoverIcon(resPlatformTwitch, iconSz, "Service: Twitch AutoMod", nil)
	shieldIcon := v.newHoverIcon(resFilterHeld, iconSz, "Held for Review: "+ev.Reason, nil)

	displayName := ev.UserName
	if displayName == "" {
		displayName = ev.UserLogin
	}
	if displayName == "" {
		displayName = "unknown"
	}
	reasonText := ev.Reason
	if reasonText == "" {
		reasonText = "AutoMod"
	}

	userSummary := fmt.Sprintf("@%s [%s]", displayName, reasonText)
	userLbl := v.newHoverCellLabel(userSummary, "Held message from @"+displayName+" ("+reasonText+")", fyne.TextStyle{Bold: true})

	leftBox := container.NewHBox(timeLbl, platIcon, shieldIcon, userLbl)

	msgLine := strings.ReplaceAll(strings.TrimSpace(ev.Text), "\n", " ")
	msgLbl := v.newWrappedCellLabel(msgLine, "Held Text: "+msgLine, fyne.TextStyle{})

	actionsBox := v.heldQueueActionButtons(ev)

	var rowBgColor color.Color = color.NRGBA{R: 0x29, G: 0x17, B: 0x08, A: 0xFF}
	if idx%2 == 1 {
		rowBgColor = color.NRGBA{R: 0x33, G: 0x1B, B: 0x08, A: 0xFF}
	}
	rowBg := canvas.NewRectangle(rowBgColor)
	divider := canvas.NewRectangle(color.NRGBA{R: 0x78, G: 0x35, B: 0x0F, A: 0x88})
	divider.SetMinSize(fyne.NewSize(1, 1))

	rowContent := container.NewBorder(nil, nil, leftBox, actionsBox, msgLbl)
	return container.NewStack(
		rowBg,
		container.NewBorder(nil, divider, nil, nil, container.NewPadded(rowContent)),
	)
}

// heldQueueActionButtons builds the 4 action buttons for a Held-for-Review queue item:
// [0] Allow | [1] Deny | [2] Deny & Timeout | [3] Deny & Ban
func (v *LogsView) heldQueueActionButtons(ev twitch.AutoModHoldEvent) *fyne.Container {
	s := v.chatScaleFactor()
	allowSz := fyne.NewSize(58*s, 22*s)
	denySz := fyne.NewSize(56*s, 22*s)
	sqSz := fyne.NewSize(22*s, 22*s)

	uName := ev.UserName
	if uName == "" {
		uName = ev.UserLogin
	}

	evCopy := ev
	allowBtn := v.newHoverIcon(resBtnHeldAllow, allowSz, "Allow Message (Release to Chat & TTS Queue)", func() {
		v.resolveHeldMessage(evCopy, "ALLOW")
	})
	denyBtn := v.newHoverIcon(resBtnHeldDeny, denySz, "Deny Message (Block & Discard)", func() {
		v.resolveHeldMessage(evCopy, "DENY")
	})
	toBtn := v.newHoverIcon(resModTimeout, sqSz, "Deny Message & Timeout @"+uName, func() {
		v.confirmHeldTimeoutUser(evCopy)
	})
	banBtn := v.newHoverIcon(resModBan, sqSz, "Deny Message & Permanently Ban @"+uName, func() {
		v.confirmHeldBanUser(evCopy)
	})

	return container.NewCenter(container.NewHBox(allowBtn, denyBtn, toBtn, banBtn))
}

func (v *LogsView) resolveHeldMessage(ev twitch.AutoModHoldEvent, action string) {
	if v.session == nil {
		return
	}
	uName := ev.UserName
	if uName == "" {
		uName = ev.UserLogin
	}
	go func() {
		err := v.session.ResolveHeldMessage(context.Background(), ev.MessageID, action)
		if err != nil {
			v.setStatus(fmt.Sprintf("⚠️ AutoMod %s failed for @%s: %v", action, uName, err))
			return
		}
		if strings.EqualFold(action, "ALLOW") {
			v.setStatus(fmt.Sprintf("✓ Approved held message from @%s.", uName))
		} else {
			v.setStatus(fmt.Sprintf("✕ Denied held message from @%s.", uName))
		}
		fyne.Do(func() {
			v.renderHeldQueue()
			v.RefreshQuery()
		})
	}()
}

func (v *LogsView) toggleMuteUserTTS(e eventlog.Entry) {
	if v.session == nil {
		return
	}
	uName := e.UserName
	if uName == "" {
		uName = e.DisplayName
	}
	muted, err := v.session.ToggleLocalMuteUserTTS(e.Platform, e.UserID, uName)
	if err != nil {
		v.setStatus("⚠️ Failed to update local TTS mute: " + err.Error())
		return
	}
	if muted {
		v.setStatus(fmt.Sprintf("🔇 Muted @%s in local TTS only (queued speech purged).", uName))
	} else {
		v.setStatus(fmt.Sprintf("🔊 Unmuted @%s in local TTS.", uName))
	}
	fyne.Do(func() {
		v.renderEntries()
	})
}

func (v *LogsView) confirmDeleteMessage(e eventlog.Entry) {
	if v.window == nil || v.session == nil {
		return
	}
	uName := e.DisplayName
	if uName == "" {
		uName = e.UserName
	}
	preview := strings.TrimSpace(e.RawText)
	if len(preview) > 140 {
		preview = preview[:140] + "…"
	}

	infoLbl := widget.NewLabel(fmt.Sprintf("Delete message from @%s on Twitch and immediately abort/purge it from the TTS speech queue?", uName))
	infoLbl.Wrapping = fyne.TextWrapWord

	quoteLbl := widget.NewLabelWithStyle(fmt.Sprintf("“%s”", preview), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	quoteLbl.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(infoLbl, widget.NewSeparator(), quoteLbl)
	d := dialog.NewCustomConfirm("Delete Chat Message", "Delete Message", "Cancel", container.NewPadded(body), func(ok bool) {
		if !ok {
			return
		}
		go func() {
			if err := v.session.ModerateDeleteMessage(context.Background(), e); err != nil {
				v.setStatus(fmt.Sprintf("⚠️ Failed to delete message from @%s: %v", uName, err))
				return
			}
			v.setStatus(fmt.Sprintf("🗑️ Deleted message from @%s and purged from TTS.", uName))
			fyne.Do(func() {
				v.RefreshQuery()
			})
		}()
	}, v.window)
	d.Resize(fyne.NewSize(460, 220))
	d.Show()
}

func (v *LogsView) confirmTimeoutUser(e eventlog.Entry) {
	v.showTimeoutDialog(e, nil)
}

func (v *LogsView) confirmHeldTimeoutUser(ev twitch.AutoModHoldEvent) {
	entry := eventlog.Entry{
		ID:          "held-" + ev.MessageID,
		Platform:    "twitch",
		Channel:     ev.Channel,
		UserID:      ev.UserID,
		UserName:    ev.UserLogin,
		DisplayName: ev.UserName,
		RawText:     ev.Text,
	}
	v.showTimeoutDialog(entry, &ev)
}

func (v *LogsView) showTimeoutDialog(e eventlog.Entry, heldEv *twitch.AutoModHoldEvent) {
	if v.window == nil || v.session == nil {
		return
	}
	uName := e.DisplayName
	if uName == "" {
		uName = e.UserName
	}

	selectedSec := 600 // default 10m
	type durPreset struct {
		label string
		sec   int
	}
	presets := []durPreset{
		{"1m", 60},
		{"5m", 300},
		{"10m", 600},
		{"1h", 3600},
		{"24h", 86400},
	}

	var presetBtns []*widget.Button
	updateBtnStyles := func() {
		for i, p := range presets {
			if p.sec == selectedSec {
				presetBtns[i].Importance = widget.HighImportance
			} else {
				presetBtns[i].Importance = widget.MediumImportance
			}
			presetBtns[i].Refresh()
		}
	}

	btnObjs := make([]fyne.CanvasObject, 0, len(presets))
	for _, p := range presets {
		pr := p
		btn := widget.NewButton(pr.label, func() {
			selectedSec = pr.sec
			updateBtnStyles()
		})
		presetBtns = append(presetBtns, btn)
		btnObjs = append(btnObjs, btn)
	}
	updateBtnStyles()

	reasonEntry := widget.NewEntry()
	reasonEntry.SetPlaceHolder("Optional timeout reason...")

	desc := fmt.Sprintf("Timeout @%s on Twitch and purge all queued TTS messages from this user:", uName)
	if heldEv != nil {
		desc = fmt.Sprintf("Deny held message and timeout @%s on Twitch:", uName)
	}
	descLbl := widget.NewLabel(desc)
	descLbl.Wrapping = fyne.TextWrapWord

	preview := strings.TrimSpace(e.RawText)
	if len(preview) > 140 {
		preview = preview[:140] + "…"
	}
	quoteLbl := widget.NewLabelWithStyle(fmt.Sprintf("Offending message: “%s”", preview), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	quoteLbl.Wrapping = fyne.TextWrapWord

	durRow := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Duration:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, container.NewGridWithColumns(len(presets), btnObjs...))
	reasonRow := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Reason:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, reasonEntry)

	body := container.NewVBox(
		descLbl,
		quoteLbl,
		widget.NewSeparator(),
		durRow,
		reasonRow,
	)

	d := dialog.NewCustomConfirm("Timeout User — @"+uName, "Timeout User", "Cancel", container.NewPadded(body), func(ok bool) {
		if !ok {
			return
		}
		dur := selectedSec
		reason := strings.TrimSpace(reasonEntry.Text)
		go func() {
			if heldEv != nil {
				_ = v.session.ResolveHeldMessage(context.Background(), heldEv.MessageID, "DENY")
			}
			if err := v.session.ModerateTimeoutUser(context.Background(), e, dur, reason); err != nil {
				v.setStatus(fmt.Sprintf("⚠️ Failed to timeout @%s: %v", uName, err))
				return
			}
			v.setStatus(fmt.Sprintf("⏱ Timed out @%s for %ds and purged queued TTS.", uName, dur))
			fyne.Do(func() {
				v.renderHeldQueue()
				v.RefreshQuery()
			})
		}()
	}, v.window)
	d.Resize(fyne.NewSize(500, 290))
	d.Show()
}

func (v *LogsView) confirmBanUser(e eventlog.Entry) {
	v.showBanDialog(e, nil)
}

func (v *LogsView) confirmHeldBanUser(ev twitch.AutoModHoldEvent) {
	entry := eventlog.Entry{
		ID:          "held-" + ev.MessageID,
		Platform:    "twitch",
		Channel:     ev.Channel,
		UserID:      ev.UserID,
		UserName:    ev.UserLogin,
		DisplayName: ev.UserName,
		RawText:     ev.Text,
	}
	v.showBanDialog(entry, &ev)
}

func (v *LogsView) showBanDialog(e eventlog.Entry, heldEv *twitch.AutoModHoldEvent) {
	if v.window == nil || v.session == nil {
		return
	}
	uName := e.DisplayName
	if uName == "" {
		uName = e.UserName
	}

	reasonEntry := widget.NewEntry()
	reasonEntry.SetPlaceHolder("Optional ban reason...")

	desc := fmt.Sprintf("Permanently ban @%s from Twitch chat, purge their queued TTS speech, and mark them Ignored in TownCrier?", uName)
	if heldEv != nil {
		desc = fmt.Sprintf("Deny held message, permanently ban @%s from Twitch chat, and mark them Ignored in TownCrier?", uName)
	}
	descLbl := widget.NewLabel(desc)
	descLbl.Wrapping = fyne.TextWrapWord

	preview := strings.TrimSpace(e.RawText)
	if len(preview) > 140 {
		preview = preview[:140] + "…"
	}
	quoteLbl := widget.NewLabelWithStyle(fmt.Sprintf("Offending message: “%s”", preview), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	quoteLbl.Wrapping = fyne.TextWrapWord

	reasonRow := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Reason:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, reasonEntry)

	body := container.NewVBox(
		descLbl,
		quoteLbl,
		widget.NewSeparator(),
		reasonRow,
	)

	d := dialog.NewCustomConfirm("Permanently Ban User — @"+uName, "Ban User", "Cancel", container.NewPadded(body), func(ok bool) {
		if !ok {
			return
		}
		reason := strings.TrimSpace(reasonEntry.Text)
		go func() {
			if heldEv != nil {
				_ = v.session.ResolveHeldMessage(context.Background(), heldEv.MessageID, "DENY")
			}
			if err := v.session.ModerateBanUser(context.Background(), e, reason); err != nil {
				v.setStatus(fmt.Sprintf("⚠️ Failed to ban @%s: %v", uName, err))
				return
			}
			v.setStatus(fmt.Sprintf("🔨 Permanently banned @%s and muted in TTS.", uName))
			fyne.Do(func() {
				v.renderHeldQueue()
				v.RefreshQuery()
			})
		}()
	}, v.window)
	d.Resize(fyne.NewSize(500, 260))
	d.Show()
}
