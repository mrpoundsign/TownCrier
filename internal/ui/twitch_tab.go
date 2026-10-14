package ui

import (
	"strings"

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
	modCmdsCheck      *widget.Check
	overrideCheck     *widget.Check
	saveNotice        *widget.Label
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

	v.saveNotice = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	connCard := v.buildConnectionCard(pCfg)
	rulesCard := v.buildReadRulesCard(pCfg)
	settingsCard := v.buildTwitchSettingsCard(pCfg)
	notifsCard := v.buildNotificationsCard(pCfg)
	overridesCard := v.buildOverridesCard(pCfg)

	content := container.NewVBox(
		connCard,
		rulesCard,
		settingsCard,
		notifsCard,
		overridesCard,
		v.saveNotice,
	)

	return container.NewVScroll(content)
}

func (v *TwitchTabView) buildConnectionCard(pCfg config.PlatformConfig) *widget.Card {
	v.statusBadge = widget.NewLabelWithStyle("⚪ Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

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
		if strings.TrimSpace(v.channelEntry.Text) == "" {
			dialog.ShowInformation("Channel Required", "Please enter your Twitch channel name first.", v.window)
			return
		}
		dialog.ShowInformation("Twitch OAuth",
			"Device Code OAuth will launch in Phase 6.\nChannel configured as: "+v.channelEntry.Text, v.window)
	})
	v.connectBtn.Importance = widget.HighImportance

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Channel Name:"), nil, v.channelEntry),
		container.NewBorder(nil, nil, v.statusBadge, nil, v.connectBtn),
	)

	return widget.NewCard("Twitch Connection", "Channel and authorization", cardContent)
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
		rewardRow,
		v.modCmdsCheck,
	)

	return widget.NewCard("Platform Features", "Twitch channel points and moderator commands", cardContent)
}

func (v *TwitchTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

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

		row := container.NewBorder(nil, nil, enabledCheck, nil, tmplEntry)
		notifsList.Add(row)
	}

	return widget.NewCard("Notification Events", "Spoken alert templates for stream events", notifsList)
}

func (v *TwitchTabView) buildOverridesCard(pCfg config.PlatformConfig) *widget.Card {
	ov := pCfg.Overrides

	v.overrideCheck = widget.NewCheck("Override Global Chat Rules for Twitch", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["twitch"]
			p.Overrides.OverrideRules = b
			c.Platforms["twitch"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

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

func (v *TwitchTabView) updateConfig(fn func(c *config.Config)) {
	if v.initializing {
		return
	}
	cfg := v.session.Config()
	fn(cfg)
	_ = v.session.ReloadConfig(cfg)
	_ = config.Save("", cfg)
	v.saveNotice.SetText("✓ Settings saved.")
}
