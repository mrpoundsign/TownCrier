package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// TwitchTabView provides the full Fyne UI controls for Twitch platform integration.
type TwitchTabView struct {
	window  fyne.Window
	session *app.Session

	channelEntry        *ScrollPassEntry
	statusBadge         *widget.Label
	oauthStatusLabel    *widget.Label
	connectBtn          *widget.Button
	enableCheck         *widget.Check
	allChatCheck        *widget.Check
	ignorePrefixCheck   *widget.Check
	ignorePrefixEntry   *ScrollPassEntry
	prefixCheck         *widget.Check
	prefixEntry         *ScrollPassEntry
	roleGatedCheck      *widget.Check
	roleChecks          map[string]*widget.Check
	paidOnlyCheck       *widget.Check
	rewardEntry         *ScrollPassEntry
	rewardOAuthHint     *widget.Label
	modCmdsCheck        *widget.Check
	followCheck         *widget.Check
	followTmplEntry     *ScrollPassEntry
	redemptionCheck     *widget.Check
	redemptionTmplEntry *ScrollPassEntry
	followOAuthHint     *widget.Label
	overrideCheck       *widget.Check
	overridesBox        *fyne.Container
	overrideVoiceField  *VoiceSpeedField
	onStatus            func(msg string)
	initializing        bool
}

// NewTwitchTabView creates a new Twitch tab view bound to the application session.
func NewTwitchTabView(w fyne.Window, session *app.Session) *TwitchTabView {
	return &TwitchTabView{
		window:     w,
		session:    session,
		roleChecks: make(map[string]*widget.Check),
	}
}

// Build generates the Fyne CanvasObject for the Twitch tab.
func (v *TwitchTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	cfg := v.session.Config()
	pCfg, hasTwitch := cfg.Platforms["twitch"]
	if !hasTwitch {
		pCfg = config.DefaultConfig().Platforms["twitch"]
	}

	connCard := v.buildConnectionCard(pCfg)
	rulesCard := v.buildReadRulesCard(pCfg)
	settingsCard := v.buildTwitchSettingsCard(pCfg)
	notifsCard := v.buildNotificationsCard(pCfg)
	overridesCard := v.buildOverridesCard(pCfg)

	v.syncOAuthState()

	content := container.NewVBox(
		connCard,
		rulesCard,
		settingsCard,
		notifsCard,
		overridesCard,
	)

	return newPanelVScroll(content)
}

// SyncFromConfig updates the Twitch tab controls to match the active session configuration.
func (v *TwitchTabView) SyncFromConfig() {
	if v.session == nil {
		return
	}
	pCfg, ok := v.session.Config().Platforms["twitch"]
	if !ok {
		return
	}
	v.initializing = true
	if v.enableCheck != nil {
		v.enableCheck.SetChecked(pCfg.Enabled)
	}
	if v.overrideVoiceField != nil {
		v.overrideVoiceField.SetVoiceAndSpeed(pCfg.Overrides.DefaultVoice, pCfg.Overrides.DefaultSpeed)
	}
	v.initializing = false
	v.updateConnectionStatus(pCfg.Enabled, pCfg.AccountName)
}

// UpdateStatus updates the connection status badge on the Twitch tab.
func (v *TwitchTabView) UpdateStatus(status string, detail string) {
	fyne.Do(func() {
		if v.statusBadge == nil {
			return
		}
		switch status {
		case "connected", "live":
			v.statusBadge.SetText("🟢 Live (" + detail + ")")
		case "waiting":
			v.statusBadge.SetText("🟡 Connected to Chat (Waiting for Stream)...")
		case "connecting":
			v.statusBadge.SetText("🟡 Connecting (" + detail + ")...")
		default:
			if v.enableCheck != nil && v.enableCheck.Checked && v.channelEntry != nil && v.channelEntry.Text != "" {
				v.statusBadge.SetText("🟡 Waiting for Stream (" + v.channelEntry.Text + ")...")
			} else {
				v.statusBadge.SetText("⚪ Disconnected")
			}
		}
	})
}

func (v *TwitchTabView) hasOAuth() bool {
	if v.session == nil || v.session.TwitchOAuth() == nil {
		return false
	}
	return v.session.TwitchOAuth().HasToken()
}

func (v *TwitchTabView) syncOAuthState() {
	authed := v.hasOAuth()

	if v.oauthStatusLabel != nil {
		if authed {
			v.oauthStatusLabel.SetText("🔑 OAuth: Connected (Channel Points & Follow alerts unlocked)")
		} else {
			v.oauthStatusLabel.SetText("🔒 OAuth: Not Connected (Public chat, Subs, Cheers & Raids work without OAuth)")
		}
	}

	if v.connectBtn != nil {
		if authed {
			v.connectBtn.SetText("Disconnect Twitch OAuth")
			v.connectBtn.SetIcon(theme.LogoutIcon())
			v.connectBtn.Importance = widget.MediumImportance
		} else {
			v.connectBtn.SetText("Connect with Twitch (OAuth)")
			v.connectBtn.SetIcon(theme.LoginIcon())
			v.connectBtn.Importance = widget.HighImportance
		}
		v.connectBtn.Refresh()
	}

	if v.rewardEntry != nil {
		if authed {
			v.rewardEntry.Enable()
		} else {
			v.rewardEntry.Disable()
		}
	}
	if v.rewardOAuthHint != nil {
		if authed {
			v.rewardOAuthHint.Hide()
		} else {
			v.rewardOAuthHint.Show()
		}
	}

	if v.followCheck != nil {
		if authed {
			v.followCheck.SetText("Follow")
			v.followCheck.Enable()
		} else {
			v.followCheck.SetText("Follow (🔒 Requires OAuth)")
			v.followCheck.Disable()
		}
	}
	if v.followTmplEntry != nil {
		if authed {
			v.followTmplEntry.Enable()
		} else {
			v.followTmplEntry.Disable()
		}
	}
	if v.redemptionCheck != nil {
		if authed {
			v.redemptionCheck.SetText("Channel Point Redemption")
			v.redemptionCheck.Enable()
		} else {
			v.redemptionCheck.SetText("Channel Point Redemption (🔒 Requires OAuth)")
			v.redemptionCheck.Disable()
		}
	}
	if v.redemptionTmplEntry != nil {
		if authed {
			v.redemptionTmplEntry.Enable()
		} else {
			v.redemptionTmplEntry.Disable()
		}
	}
	if v.followOAuthHint != nil {
		if authed {
			v.followOAuthHint.Hide()
		} else {
			v.followOAuthHint.Show()
		}
	}
}

func (v *TwitchTabView) buildConnectionCard(pCfg config.PlatformConfig) *widget.Card {
	v.statusBadge = widget.NewLabelWithStyle("⚪ Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.oauthStatusLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	v.channelEntry = NewScrollPassEntry()
	v.channelEntry.SetPlaceHolder("e.g. mrpoundsign")
	v.channelEntry.SetText(pCfg.AccountName)

	v.enableCheck = widget.NewCheck("Enable Twitch Integration", nil)
	v.enableCheck.SetChecked(pCfg.Enabled)
	v.enableCheck.OnChanged = func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Enabled = b
			p.EverEnabled = true
			c.Platforms["twitch"] = p
		})
		v.updateConnectionStatus(b, v.channelEntry.Text)
	}

	v.channelEntry.OnChanged = func(val string) {
		trimmed := strings.TrimSpace(val)
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.AccountName = trimmed
			c.Platforms["twitch"] = p
		})
		v.updateConnectionStatus(v.enableCheck.Checked, trimmed)
	}

	v.updateConnectionStatus(pCfg.Enabled, pCfg.AccountName)

	v.session.SetOnTwitchStatus(func(status string, detail string) {
		v.UpdateStatus(status, detail)
	})

	v.connectBtn = widget.NewButtonWithIcon("Connect with Twitch (OAuth)", theme.LoginIcon(), func() {
		if v.hasOAuth() {
			v.disconnectOAuth()
			return
		}
		v.startOAuthFlow()
	})
	v.connectBtn.Importance = widget.HighImportance

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Channel Name:"), nil, v.channelEntry),
		container.NewBorder(nil, nil, v.statusBadge, nil, v.connectBtn),
		v.oauthStatusLabel,
	)

	return widget.NewCard("Twitch Connection", "Public IRC chat works immediately; OAuth unlocks EventSub features", cardContent)
}

func (v *TwitchTabView) disconnectOAuth() {
	dialog.ShowConfirm("Disconnect Twitch OAuth", "Remove stored Twitch OAuth credentials? Public chat reading will continue to work.", func(ok bool) {
		if !ok {
			return
		}
		if v.session != nil && v.session.TwitchOAuth() != nil {
			_ = v.session.TwitchOAuth().ClearTokens()
		}
		if v.session != nil {
			v.session.RefreshTwitchOAuth()
		}
		v.syncOAuthState()
		if v.onStatus != nil {
			v.onStatus("Twitch OAuth disconnected.")
		}
	}, v.window)
}

func (v *TwitchTabView) startOAuthFlow() {
	if v.session == nil || v.session.TwitchOAuth() == nil {
		return
	}
	oauthMgr := v.session.TwitchOAuth()
	v.connectBtn.Disable()
	if v.onStatus != nil {
		v.onStatus("Requesting Twitch Device Code...")
	}

	go func() {
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 15*time.Second)
		dResp, err := oauthMgr.RequestDeviceCode(reqCtx, nil)
		reqCancel()

		if err != nil {
			fyne.Do(func() {
				v.connectBtn.Enable()
				if v.onStatus != nil {
					v.onStatus(fmt.Sprintf("Twitch OAuth error: %v", err))
				}
				dialog.ShowError(fmt.Errorf("failed to start Twitch OAuth: %w", err), v.window)
			})
			return
		}

		pollCtx, pollCancel := context.WithTimeout(context.Background(), time.Duration(dResp.ExpiresIn)*time.Second)

		fyne.Do(func() {
			v.connectBtn.Enable()

			codeLabel := widget.NewLabelWithStyle(dResp.UserCode, fyne.TextAlignCenter, fyne.TextStyle{Bold: true, Monospace: true})
			waitLabel := widget.NewLabelWithStyle("⏳ Waiting for authorization in browser...", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

			verifyURL := dResp.VerificationURI
			if verifyURL == "" {
				verifyURL = "https://www.twitch.tv/activate"
			}

			openBtn := widget.NewButtonWithIcon("Copy Code & Open Twitch Activation Page", theme.ContentCopyIcon(), func() {
				if app := fyne.CurrentApp(); app != nil && app.Clipboard() != nil {
					app.Clipboard().SetContent(dResp.UserCode)
				}
				if parsed, parseErr := url.Parse(verifyURL); parseErr == nil {
					_ = fyne.CurrentApp().OpenURL(parsed)
				}
				waitLabel.SetText("✓ Code copied! Paste it on twitch.tv/activate and click Authorize.")
			})
			openBtn.Importance = widget.HighImportance

			content := container.NewVBox(
				widget.NewLabel("1. Click below to copy your activation code and open Twitch:"),
				codeLabel,
				openBtn,
				widget.NewSeparator(),
				waitLabel,
			)

			var modal dialog.Dialog
			cancelBtn := widget.NewButton("Cancel", func() {
				pollCancel()
				if modal != nil {
					modal.Hide()
				}
				if v.onStatus != nil {
					v.onStatus("Twitch OAuth cancelled.")
				}
			})

			modal = dialog.NewCustomWithoutButtons("Authorize TownCrier on Twitch", container.NewVBox(content, cancelBtn), v.window)
			modal.Show()

			go func() {
				_, pollErr := oauthMgr.PollToken(pollCtx, dResp.DeviceCode, dResp.Interval)
				pollCancel()

				fyne.Do(func() {
					if modal != nil {
						modal.Hide()
					}
					if pollErr != nil {
						if errors.Is(pollErr, context.Canceled) {
							return
						}
						if v.onStatus != nil {
							v.onStatus(fmt.Sprintf("Twitch OAuth failed: %v", pollErr))
						}
						dialog.ShowError(fmt.Errorf("twitch authorization failed: %w", pollErr), v.window)
						return
					}

					if v.session != nil {
						v.session.RefreshTwitchOAuth()
					}
					v.syncOAuthState()
					if v.onStatus != nil {
						v.onStatus("✓ Twitch OAuth authorized and saved!")
					}
				})
			}()
		})
	}()
}

func (v *TwitchTabView) buildReadRulesCard(pCfg config.PlatformConfig) *widget.Card {
	rr := pCfg.ReadRules

	v.allChatCheck = widget.NewCheck("Read All Chat Messages", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.AllChat = b
			c.Platforms["twitch"] = p
		})
	})
	v.allChatCheck.SetChecked(rr.AllChat)

	v.prefixEntry = NewScrollPassEntry()
	v.prefixEntry.SetPlaceHolder("!tts")
	pref := rr.Prefix
	if pref == "" {
		pref = "!tts"
	}
	v.prefixEntry.SetText(pref)
	if !rr.PrefixOnly {
		v.prefixEntry.Disable()
	}
	v.prefixEntry.OnChanged = func(val string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.Prefix = strings.TrimSpace(val)
			c.Platforms["twitch"] = p
		})
	}

	v.prefixCheck = widget.NewCheck("Prefix / Command Only", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.PrefixOnly = b
			c.Platforms["twitch"] = p
		})
		if b {
			v.prefixEntry.Enable()
		} else {
			v.prefixEntry.Disable()
		}
	})
	v.prefixCheck.SetChecked(rr.PrefixOnly)
	prefixRow := container.NewBorder(nil, nil, v.prefixCheck, nil, v.prefixEntry)

	v.roleGatedCheck = widget.NewCheck("Role Gated (Only read messages from checked roles):", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.RoleGated = b
			c.Platforms["twitch"] = p
		})
		for _, chk := range v.roleChecks {
			if b {
				chk.Enable()
			} else {
				chk.Disable()
			}
		}
	})
	v.roleGatedCheck.SetChecked(rr.RoleGated)

	activeRoles := make(map[string]bool)
	for _, r := range rr.Roles {
		activeRoles[strings.ToLower(r)] = true
	}

	roles := []string{"broadcaster", "mod", "vip", "subscriber", "follower"}
	roleBoxes := container.NewHBox()
	for _, role := range roles {
		rName := role
		chk := widget.NewCheck(capitalizeRole(rName), nil)
		v.roleChecks[rName] = chk
		chk.SetChecked(activeRoles[rName])
		if !rr.RoleGated {
			chk.Disable()
		}
		chk.OnChanged = func(b bool) {
			v.saveRoles()
		}
		roleBoxes.Add(chk)
	}

	v.paidOnlyCheck = widget.NewCheck("Paid Only (Only read Cheers, Subs, and Channel Point redemptions)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.PaidOnly = b
			c.Platforms["twitch"] = p
		})
	})
	v.paidOnlyCheck.SetChecked(rr.PaidOnly)

	v.ignorePrefixEntry = NewScrollPassEntry()
	v.ignorePrefixEntry.SetPlaceHolder("!, ?")
	ignPrefStr := strings.Join(rr.IgnoredPrefixes, ", ")
	if ignPrefStr == "" && rr.IgnorePrefixes {
		ignPrefStr = "!"
	}
	v.ignorePrefixEntry.SetText(ignPrefStr)
	if !rr.IgnorePrefixes {
		v.ignorePrefixEntry.Disable()
	}
	v.ignorePrefixEntry.OnChanged = func(val string) {
		var prefixes []string
		for part := range strings.SplitSeq(val, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				prefixes = append(prefixes, p)
			}
		}
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.IgnoredPrefixes = prefixes
			c.Platforms["twitch"] = p
		})
	}

	v.ignorePrefixCheck = widget.NewCheck("Ignore Bot Commands / Prefixes:", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ReadRules.IgnorePrefixes = b
			c.Platforms["twitch"] = p
		})
		if b {
			v.ignorePrefixEntry.Enable()
		} else {
			v.ignorePrefixEntry.Disable()
		}
	})
	v.ignorePrefixCheck.SetChecked(rr.IgnorePrefixes)
	ignorePrefixRow := container.NewBorder(nil, nil, v.ignorePrefixCheck, nil, v.ignorePrefixEntry)

	cardContent := container.NewVBox(
		v.allChatCheck,
		ignorePrefixRow,
		prefixRow,
		v.roleGatedCheck,
		indentLeft(28, roleBoxes),
		v.paidOnlyCheck,
	)

	return widget.NewCard("Read Rules", "Control which messages are spoken aloud", cardContent)
}

func (v *TwitchTabView) buildTwitchSettingsCard(pCfg config.PlatformConfig) *widget.Card {
	v.rewardOAuthHint = widget.NewLabelWithStyle(
		"🔒 Channel Point redemptions require Twitch OAuth. Connect with Twitch OAuth above to enable this field.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)

	v.rewardEntry = NewScrollPassEntry()
	v.rewardEntry.SetPlaceHolder("Leave blank to speak all redemptions, or enter a Reward Title / ID")
	v.rewardEntry.SetText(pCfg.CustomRewardID)
	v.rewardEntry.OnChanged = func(val string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.CustomRewardID = strings.TrimSpace(val)
			c.Platforms["twitch"] = p
		})
	}
	rewardRow := container.NewBorder(nil, nil, widget.NewLabel("Designated TTS Reward (Title or ID):"), nil, v.rewardEntry)

	v.modCmdsCheck = widget.NewCheck("Allow Moderators to control TTS via chat (!tts skip, !tts pause, !tts clear)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.ModCommandsEnabled = b
			c.Platforms["twitch"] = p
		})
	})
	v.modCmdsCheck.SetChecked(pCfg.ModCommandsEnabled)

	cardContent := container.NewVBox(
		v.rewardOAuthHint,
		rewardRow,
		v.modCmdsCheck,
	)

	return widget.NewCard("Platform Features", "Twitch channel points and moderator commands", cardContent)
}

func (v *TwitchTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

	v.followOAuthHint = widget.NewLabelWithStyle(
		"🔒 Note: Subscriptions, Resubs, Gift Subs, Cheers, and Raids work via public IRC. Follow and Channel Point Redemption alerts require Twitch OAuth.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	notifsList.Add(v.followOAuthHint)

	events := []struct {
		key          string
		label        string
		fallback     string
		hasThreshold bool
		defaultOn    bool
	}{
		{"sub", "Subscription", "{user} subscribed!", false, true},
		{"resub", "Resubscription", "{user} resubscribed for {months} months! {message}", false, true},
		{"gift_sub", "Gift Sub", "{user} gifted {count} subscriptions!", true, true},
		{"sub_upgrade", "Sub Upgrade / Pay-It-Forward", "{user} upgraded their subscription! {message}", false, true},
		{"watch_streak", "Watch Streak", "{user} is on a {count} stream watch streak! {message}", true, true},
		{"cheer", "Cheer (Bits)", "{user} cheered {amount} bits: {message}", true, true},
		{"bits_badge_tier", "Bits Badge Tier", "{user} unlocked the {amount} bits badge! {message}", true, true},
		{"raid", "Raid", "{user} raided with {count} viewers!", true, true},
		{"redemption", "Channel Point Redemption", "{user} redeemed {reward}: {message}", false, true},
		{"charity_donation", "Charity Donation", "{user} donated {amount} {currency} to {reward}! {message}", true, true},
		{"announcement", "Chat Announcement (/announce)", "Announcement from {user}: {message}", false, false},
		{"follow", "Follow", "{user} followed the stream!", false, false},
	}

	for _, ev := range events {
		eventKey := ev.key
		label := ev.label
		fallback := ev.fallback

		notif, ok := pCfg.Notifications[eventKey]
		if !ok {
			notif = config.NotificationEventConfig{
				Enabled:  ev.defaultOn,
				Template: fallback,
			}
		}

		enabledCheck := widget.NewCheck(label, func(b bool) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["twitch"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Enabled = b
				p.Notifications[eventKey] = n
				c.Platforms["twitch"] = p
			})
		})
		enabledCheck.SetChecked(notif.Enabled)

		tmplEntry := NewScrollPassEntry()
		tmplEntry.SetText(notif.Template)
		tmplEntry.OnChanged = func(val string) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["twitch"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Template = val
				p.Notifications[eventKey] = n
				c.Platforms["twitch"] = p
			})
		}

		switch eventKey {
		case "follow":
			v.followCheck = enabledCheck
			v.followTmplEntry = tmplEntry
		case "redemption":
			v.redemptionCheck = enabledCheck
			v.redemptionTmplEntry = tmplEntry
		}

		var rightWidget fyne.CanvasObject
		vars := EventTemplateVars("twitch", eventKey)
		helpMarker := newTemplateHelpMarker(v.window, eventKey, label, vars, v.onStatus, &tmplEntry.Entry)

		if ev.hasThreshold {
			minEntry := NewScrollPassEntry()
			minEntry.SetPlaceHolder("0")
			if notif.Threshold > 0 {
				minEntry.SetText(strconv.FormatFloat(notif.Threshold, 'f', -1, 64))
			}
			minEntry.OnChanged = func(val string) {
				parsed, _ := strconv.ParseFloat(strings.TrimSpace(val), 64)
				if parsed < 0 {
					parsed = 0
				}
				v.updateConfig(func(c *config.Config) {
					p := c.Platforms["twitch"]
					if p.Notifications == nil {
						p.Notifications = make(map[string]config.NotificationEventConfig)
					}
					n := p.Notifications[eventKey]
					n.Threshold = parsed
					p.Notifications[eventKey] = n
					c.Platforms["twitch"] = p
				})
			}
			rightWidget = container.NewHBox(
				container.NewCenter(helpMarker),
				widget.NewLabel("Min:"),
				container.NewGridWrap(fyne.NewSize(70, 36), minEntry),
			)
		} else {
			rightWidget = container.NewHBox(
				container.NewCenter(helpMarker),
				container.NewGridWrap(fyne.NewSize(108, 36), widget.NewLabel("")),
			)
		}

		row := container.NewBorder(nil, nil, enabledCheck, rightWidget, tmplEntry)
		notifsList.Add(row)
	}

	return widget.NewCard("Notification Events", "Spoken alert templates and minimum thresholds for stream events", notifsList)
}

func (v *TwitchTabView) buildOverridesCard(pCfg config.PlatformConfig) *widget.Card {
	ov := pCfg.Overrides

	namesCheck := widget.NewCheck("Speak Names for Twitch Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.SpeakNames = b
			c.Platforms["twitch"] = p
		})
	})
	namesCheck.SetChecked(ov.SpeakNames)

	emotesCheck := widget.NewCheck("Speak Emotes for Twitch Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.SpeakEmotes = b
			c.Platforms["twitch"] = p
		})
	})
	emotesCheck.SetChecked(ov.SpeakEmotes)

	selectedVoice := ov.DefaultVoice
	sp := ov.DefaultSpeed
	if sp <= 0 {
		sp = 1.0
	}

	v.overrideVoiceField = NewVoiceSpeedField(
		v.window,
		v.session,
		"Edit Twitch Default Voice & Speed",
		selectedVoice,
		sp,
		func(voiceID string, speed float32) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["twitch"]
				p.Overrides.DefaultVoice = voiceID
				p.Overrides.DefaultSpeed = speed
				c.Platforms["twitch"] = p
			})
		},
		v.onStatus,
	)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("Twitch Default Voice & Speed:"), nil, v.overrideVoiceField.Container)

	v.overridesBox = container.NewVBox(
		namesCheck,
		emotesCheck,
		voiceRow,
	)

	var cardContent *fyne.Container
	toggleOverrides := func(enabled bool) {
		if enabled {
			v.overridesBox.Show()
		} else {
			v.overridesBox.Hide()
		}
		if cardContent != nil {
			cardContent.Refresh()
		}
	}
	toggleOverrides(ov.OverrideRules)

	v.overrideCheck = widget.NewCheck("Override Global Defaults", func(b bool) {
		toggleOverrides(b)
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.OverrideRules = b
			c.Platforms["twitch"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent = container.NewVBox(
		v.overrideCheck,
		v.overridesBox,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to Twitch", cardContent)
}

// RefreshVoices updates the platform override voice field when enabled model packs change in Settings.
func (v *TwitchTabView) RefreshVoices() {
	fyne.Do(func() {
		if v.overrideVoiceField == nil || v.session == nil {
			return
		}
		cfg := v.session.Config()
		ov := cfg.Platforms["twitch"].Overrides
		voice := ov.DefaultVoice
		if voice == "" {
			voice = cfg.GlobalChatRules.DefaultVoice
		}
		sp := ov.DefaultSpeed
		if sp <= 0 {
			sp = 1.0
		}
		v.overrideVoiceField.SetVoiceAndSpeed(voice, sp)
	})
}

func (v *TwitchTabView) saveRoles() {
	var roles []string
	for role, chk := range v.roleChecks {
		if chk.Checked {
			roles = append(roles, role)
		}
	}
	v.updateConfig(func(c *config.Config) {
		p := c.Platforms["twitch"]
		p.ReadRules.Roles = roles
		c.Platforms["twitch"] = p
	})
}

func (v *TwitchTabView) updateConnectionStatus(enabled bool, channel string) {
	if v.statusBadge == nil {
		return
	}
	switch {
	case !enabled:
		v.statusBadge.SetText("⚪ Disabled")
	case channel == "":
		v.statusBadge.SetText("🟡 Needs Channel Name")
	default:
		v.statusBadge.SetText("🟡 Waiting for Stream (" + channel + ")...")
	}
}

func capitalizeRole(r string) string {
	if r == "" {
		return ""
	}
	return strings.ToUpper(r[:1]) + r[1:]
}

// SetStatusHandler registers a callback invoked when configuration is updated.
func (v *TwitchTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *TwitchTabView) updateConfig(fn func(c *config.Config)) {
	if v.initializing {
		return
	}
	if v.session != nil {
		_ = v.session.UpdateConfig(fn)
	}
	if v.onStatus != nil {
		v.onStatus("✓ Settings saved.")
	}
}
