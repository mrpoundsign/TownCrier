package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// TwitchTabView provides the full Fyne UI controls for Twitch platform integration.
type TwitchTabView struct {
	window  fyne.Window
	session *app.Session

	channelEntry      *widget.Entry
	statusBadge       *widget.Label
	oauthStatusLabel  *widget.Label
	connectBtn        *widget.Button
	enableCheck       *widget.Check
	allChatCheck      *widget.Check
	ignorePrefixCheck *widget.Check
	ignorePrefixEntry *widget.Entry
	prefixCheck       *widget.Check
	prefixEntry       *widget.Entry
	roleGatedCheck    *widget.Check
	roleChecks        map[string]*widget.Check
	paidOnlyCheck     *widget.Check
	rewardEntry       *widget.Entry
	rewardOAuthHint   *widget.Label
	modCmdsCheck      *widget.Check
	followCheck       *widget.Check
	followTmplEntry   *widget.Entry
	followOAuthHint   *widget.Label
	overrideCheck     *widget.Check
	onStatus          func(msg string)
	initializing      bool
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

	return container.NewVScroll(content)
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

	v.channelEntry = widget.NewEntry()
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
		fyne.Do(func() {
			if v.statusBadge == nil {
				return
			}
			switch status {
			case "connected":
				v.statusBadge.SetText("🟢 Connected (" + detail + ")")
			case "connecting":
				v.statusBadge.SetText("🟡 Connecting (" + detail + ")...")
			default:
				if v.enableCheck != nil && v.enableCheck.Checked && v.channelEntry != nil && v.channelEntry.Text != "" {
					v.statusBadge.SetText("🟢 Ready (" + v.channelEntry.Text + ")")
				} else {
					v.statusBadge.SetText("⚪ Disconnected")
				}
			}
		})
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

	v.prefixEntry = widget.NewEntry()
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

	roles := []string{"broadcaster", "mod", "vip", "subscriber"}
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

	v.ignorePrefixEntry = widget.NewEntry()
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
		container.NewPadded(roleBoxes),
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

	v.rewardEntry = widget.NewEntry()
	v.rewardEntry.SetPlaceHolder("Leave blank to read all text redemptions")
	v.rewardEntry.SetText(pCfg.CustomRewardID)
	v.rewardEntry.OnChanged = func(val string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.CustomRewardID = strings.TrimSpace(val)
			c.Platforms["twitch"] = p
		})
	}
	rewardRow := container.NewBorder(nil, nil, widget.NewLabel("Designated TTS Reward ID:"), nil, v.rewardEntry)

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
		"🔒 Note: Subscriptions, Resubs, Gift Subs, Cheers, and Raids work via public IRC. Follow alerts require Twitch OAuth.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	notifsList.Add(v.followOAuthHint)

	events := []struct {
		key   string
		label string
	}{
		{"sub", "Subscription"},
		{"resub", "Resubscription"},
		{"gift_sub", "Gift Sub"},
		{"cheer", "Cheer (Bits)"},
		{"raid", "Raid"},
		{"follow", "Follow"},
	}

	for _, ev := range events {
		eventKey := ev.key
		label := ev.label

		notif, ok := pCfg.Notifications[eventKey]
		if !ok {
			notif = config.NotificationEventConfig{
				Enabled:  true,
				Template: "{user}: {message}",
			}
		}

		enabledCheck := widget.NewCheck(label, func(b bool) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["twitch"]
				n := p.Notifications[eventKey]
				n.Enabled = b
				p.Notifications[eventKey] = n
				c.Platforms["twitch"] = p
			})
		})
		enabledCheck.SetChecked(notif.Enabled)

		tmplEntry := widget.NewEntry()
		tmplEntry.SetText(notif.Template)
		tmplEntry.OnChanged = func(val string) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["twitch"]
				n := p.Notifications[eventKey]
				n.Template = val
				p.Notifications[eventKey] = n
				c.Platforms["twitch"] = p
			})
		}

		if eventKey == "follow" {
			v.followCheck = enabledCheck
			v.followTmplEntry = tmplEntry
		}

		row := container.NewBorder(nil, nil, enabledCheck, nil, tmplEntry)
		notifsList.Add(row)
	}

	return widget.NewCard("Notification Events", "Spoken alert templates for stream events", notifsList)
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

	voices := tts.KokoroVoiceNames
	if len(voices) == 0 {
		voices = []string{"af_bella", "am_adam"}
	}

	selectedVoice := ov.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = voices[0]
	}
	voiceSelect := widget.NewSelect(voices, func(s string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.DefaultVoice = s
			c.Platforms["twitch"] = p
		})
	})
	voiceSelect.SetSelected(selectedVoice)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("Twitch Default Voice:"), nil, voiceSelect)

	toggleOverrides := func(enabled bool) {
		if enabled {
			namesCheck.Enable()
			emotesCheck.Enable()
			voiceSelect.Enable()
		} else {
			namesCheck.Disable()
			emotesCheck.Disable()
			voiceSelect.Disable()
		}
	}
	toggleOverrides(ov.OverrideRules)

	v.overrideCheck = widget.NewCheck("Override Global Chat Rules for Twitch", func(b bool) {
		toggleOverrides(b)
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.OverrideRules = b
			c.Platforms["twitch"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent := container.NewVBox(
		v.overrideCheck,
		namesCheck,
		emotesCheck,
		voiceRow,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to Twitch", cardContent)
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
		v.statusBadge.SetText("🟢 Ready (" + channel + ")")
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
