package ui

import (
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// KickTabView provides the Fyne UI controls for Kick Pusher WebSocket integration.
type KickTabView struct {
	window  fyne.Window
	session *app.Session

	channelEntry       *ScrollPassEntry
	statusBadge        *widget.Label
	enableCheck        *widget.Check
	allChatCheck       *widget.Check
	ignorePrefixCheck  *widget.Check
	ignorePrefixEntry  *ScrollPassEntry
	prefixCheck        *widget.Check
	prefixEntry        *ScrollPassEntry
	roleGatedCheck     *widget.Check
	roleChecks         map[string]*widget.Check
	paidOnlyCheck      *widget.Check
	overrideCheck      *widget.Check
	overridesBox       *fyne.Container
	overrideVoiceField *VoiceSpeedField
	onStatus           func(msg string)
	initializing       bool
}

// NewKickTabView creates a new Kick tab view bound to the application session.
func NewKickTabView(w fyne.Window, session *app.Session) *KickTabView {
	return &KickTabView{
		window:     w,
		session:    session,
		roleChecks: make(map[string]*widget.Check),
	}
}

// Build generates the Fyne CanvasObject for the Kick tab.
func (v *KickTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	cfg := v.session.Config()
	pCfg, hasKick := cfg.Platforms["kick"]
	if !hasKick {
		pCfg = config.DefaultConfig().Platforms["kick"]
	}

	connCard := v.buildConnectionCard(pCfg)
	rulesCard := v.buildReadRulesCard(pCfg)
	notifsCard := v.buildNotificationsCard(pCfg)
	overridesCard := v.buildOverridesCard(pCfg)

	content := container.NewVBox(
		connCard,
		rulesCard,
		notifsCard,
		overridesCard,
	)

	return newPanelVScroll(content)
}

// SyncFromConfig updates the Kick tab controls to match the active session configuration.
func (v *KickTabView) SyncFromConfig() {
	if v.session == nil {
		return
	}
	pCfg, ok := v.session.Config().Platforms["kick"]
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

// UpdateStatus updates the connection status badge on the Kick tab.
func (v *KickTabView) UpdateStatus(status string, detail string) {
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

func (v *KickTabView) buildConnectionCard(pCfg config.PlatformConfig) *widget.Card {
	v.statusBadge = widget.NewLabelWithStyle("⚪ Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	v.channelEntry = NewScrollPassEntry()
	v.channelEntry.SetPlaceHolder("e.g. channelname, https://kick.com/channelname, or channelname#chatroom_id")
	v.channelEntry.SetText(pCfg.AccountName)

	v.enableCheck = widget.NewCheck("Enable Kick Integration", nil)
	v.enableCheck.SetChecked(pCfg.Enabled)
	v.enableCheck.OnChanged = func(b bool) {
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["kick"]
			if !ok {
				p = config.DefaultConfig().Platforms["kick"]
			}
			p.Enabled = b
			p.EverEnabled = true
			c.Platforms["kick"] = p
		})
		v.updateConnectionStatus(b, v.channelEntry.Text)
	}

	v.channelEntry.OnChanged = func(val string) {
		trimmed := strings.TrimSpace(val)
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["kick"]
			if !ok {
				p = config.DefaultConfig().Platforms["kick"]
			}
			p.AccountName = trimmed
			c.Platforms["kick"] = p
		})
		v.updateConnectionStatus(v.enableCheck.Checked, trimmed)
	}

	v.updateConnectionStatus(pCfg.Enabled, pCfg.AccountName)

	v.session.SetOnKickStatus(func(status string, detail string) {
		v.UpdateStatus(status, detail)
	})

	hintLabel := widget.NewLabelWithStyle(
		"⚠️ Unofficial Pusher WebSocket Connector (may break if Kick changes internal schema): Enter your Kick channel name or URL. Chat, subscriptions, gifted subs, and moderation deletions stream with zero OAuth required. Tip: If Cloudflare blocks automatic channel lookup, enter channelname#chatroom_id.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	hintLabel.Wrapping = fyne.TextWrapWord

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Kick Channel or URL:"), nil, v.channelEntry),
		v.statusBadge,
		hintLabel,
	)

	return widget.NewCard("Kick Connection", "Connects to Kick live chat & events via Pusher WebSocket", cardContent)
}

func (v *KickTabView) buildReadRulesCard(pCfg config.PlatformConfig) *widget.Card {
	rr := pCfg.ReadRules

	v.allChatCheck = widget.NewCheck("Read All Chat Messages", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.ReadRules.AllChat = b
			c.Platforms["kick"] = p
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
			p := c.Platforms["kick"]
			p.ReadRules.Prefix = strings.TrimSpace(val)
			c.Platforms["kick"] = p
		})
	}

	v.prefixCheck = widget.NewCheck("Prefix / Command Only", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.ReadRules.PrefixOnly = b
			c.Platforms["kick"] = p
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
			p := c.Platforms["kick"]
			p.ReadRules.RoleGated = b
			c.Platforms["kick"] = p
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

	roleDefs := []struct {
		key   string
		label string
	}{
		{"broadcaster", "Broadcaster"},
		{"mod", "Moderator"},
		{"vip", "VIP"},
		{"og", "OG / Founder"},
		{"subscriber", "Subscriber"},
		{"follower", "Follower"},
	}
	roleBoxes := container.NewHBox()
	for _, rd := range roleDefs {
		rKey := rd.key
		chk := widget.NewCheck(rd.label, nil)
		v.roleChecks[rKey] = chk
		chk.SetChecked(activeRoles[rKey])
		if !rr.RoleGated {
			chk.Disable()
		}
		chk.OnChanged = func(b bool) {
			v.saveRoles()
		}
		roleBoxes.Add(chk)
	}

	v.paidOnlyCheck = widget.NewCheck("Paid Only (Only read Subscriptions and Gifted Subs)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.ReadRules.PaidOnly = b
			c.Platforms["kick"] = p
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
			p := c.Platforms["kick"]
			p.ReadRules.IgnoredPrefixes = prefixes
			c.Platforms["kick"] = p
		})
	}

	v.ignorePrefixCheck = widget.NewCheck("Ignore Bot Commands / Prefixes:", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.ReadRules.IgnorePrefixes = b
			c.Platforms["kick"] = p
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

	return widget.NewCard("Read Rules", "Control which Kick messages are spoken aloud", cardContent)
}

func (v *KickTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

	events := []struct {
		key          string
		label        string
		fallback     string
		hasThreshold bool
	}{
		{"sub", "New Subscriber", "{user} just subscribed on Kick!", false},
		{"resub", "Resubscription", "{user} resubscribed for {months} months!", false},
		{"gift_sub", "Gifted Subscriptions", "{user} gifted {count} subscriptions!", true},
		{"gift", "Gifts / Kicks", "{user} sent {count} {reward}: {message}", true},
		{"redemption", "Channel Reward Redemption", "{user} redeemed {reward}: {message}", false},
		{"raid", "Host / Raid", "{user} hosted with {viewers} viewers!", true},
		{"follow", "New Followers", "{user} followed on Kick!", false},
	}

	for _, ev := range events {
		eventKey := ev.key
		label := ev.label
		fallback := ev.fallback

		notif, ok := pCfg.Notifications[eventKey]
		if !ok {
			notif = config.NotificationEventConfig{
				Enabled:  eventKey != "follow",
				Template: fallback,
			}
		}

		enabledCheck := widget.NewCheck(label, func(b bool) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["kick"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Enabled = b
				p.Notifications[eventKey] = n
				c.Platforms["kick"] = p
			})
		})
		enabledCheck.SetChecked(notif.Enabled)

		tmplEntry := NewScrollPassEntry()
		tmplEntry.SetText(notif.Template)
		tmplEntry.OnChanged = func(val string) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["kick"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Template = val
				p.Notifications[eventKey] = n
				c.Platforms["kick"] = p
			})
		}

		var rightWidget fyne.CanvasObject
		vars := EventTemplateVars("kick", eventKey)
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
					p := c.Platforms["kick"]
					if p.Notifications == nil {
						p.Notifications = make(map[string]config.NotificationEventConfig)
					}
					n := p.Notifications[eventKey]
					n.Threshold = parsed
					p.Notifications[eventKey] = n
					c.Platforms["kick"] = p
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

	return widget.NewCard("Notification Events", "Spoken alert templates and minimum thresholds for Kick Subscriptions, Gifted Subs, Gifts, Hosts & Follows", notifsList)
}

func (v *KickTabView) buildOverridesCard(pCfg config.PlatformConfig) *widget.Card {
	ov := pCfg.Overrides

	namesCheck := widget.NewCheck("Speak Names for Kick Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.Overrides.SpeakNames = b
			c.Platforms["kick"] = p
		})
	})
	namesCheck.SetChecked(ov.SpeakNames)

	emotesCheck := widget.NewCheck("Speak Emotes for Kick Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["kick"]
			p.Overrides.SpeakEmotes = b
			c.Platforms["kick"] = p
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
		"Edit Kick Default Voice & Speed",
		selectedVoice,
		sp,
		func(voiceID string, speed float32) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["kick"]
				p.Overrides.DefaultVoice = voiceID
				p.Overrides.DefaultSpeed = speed
				c.Platforms["kick"] = p
			})
		},
		v.onStatus,
	)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("Kick Default Voice & Speed:"), nil, v.overrideVoiceField.Container)

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
			p := c.Platforms["kick"]
			p.Overrides.OverrideRules = b
			c.Platforms["kick"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent = container.NewVBox(
		v.overrideCheck,
		v.overridesBox,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to Kick", cardContent)
}

// RefreshVoices updates the platform override voice field when enabled model packs change in Settings.
func (v *KickTabView) RefreshVoices() {
	fyne.Do(func() {
		if v.overrideVoiceField == nil || v.session == nil {
			return
		}
		cfg := v.session.Config()
		ov := cfg.Platforms["kick"].Overrides
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

func (v *KickTabView) saveRoles() {
	var roles []string
	for role, chk := range v.roleChecks {
		if chk.Checked {
			roles = append(roles, role)
		}
	}
	v.updateConfig(func(c *config.Config) {
		p := c.Platforms["kick"]
		p.ReadRules.Roles = roles
		c.Platforms["kick"] = p
	})
}

func (v *KickTabView) updateConnectionStatus(enabled bool, channel string) {
	if v.statusBadge == nil {
		return
	}
	switch {
	case !enabled:
		v.statusBadge.SetText("⚪ Disabled")
	case channel == "":
		v.statusBadge.SetText("🟡 Needs Kick Channel or URL")
	default:
		v.statusBadge.SetText("🟡 Waiting for Stream (" + channel + ")...")
	}
}

// SetStatusHandler registers a callback invoked when configuration is updated.
func (v *KickTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *KickTabView) updateConfig(fn func(c *config.Config)) {
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
