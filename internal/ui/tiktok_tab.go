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

// TikTokTabView provides the Fyne UI controls for zero-config TikTok Live integration.
type TikTokTabView struct {
	window  fyne.Window
	session *app.Session

	usernameEntry      *ScrollPassEntry
	statusBadge        *widget.Label
	enableCheck        *widget.Check
	allChatCheck       *widget.Check
	ignorePrefixCheck  *widget.Check
	ignorePrefixEntry  *ScrollPassEntry
	prefixCheck        *widget.Check
	prefixEntry        *ScrollPassEntry
	roleGatedCheck     *widget.Check
	roleChecks         map[string]*widget.Check
	fanClubLevelEntry  *ScrollPassEntry
	paidOnlyCheck      *widget.Check
	overrideCheck      *widget.Check
	overridesBox       *fyne.Container
	overrideVoiceField *VoiceSpeedField
	onStatus           func(msg string)
	initializing       bool
}

// NewTikTokTabView creates a new TikTok tab view bound to the application session.
func NewTikTokTabView(w fyne.Window, session *app.Session) *TikTokTabView {
	return &TikTokTabView{
		window:     w,
		session:    session,
		roleChecks: make(map[string]*widget.Check),
	}
}

// Build generates the Fyne CanvasObject for the TikTok tab.
func (v *TikTokTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	cfg := v.session.Config()
	pCfg, hasTikTok := cfg.Platforms["tiktok"]
	if !hasTikTok {
		pCfg = config.DefaultConfig().Platforms["tiktok"]
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

// SyncFromConfig updates the TikTok tab controls to match the active session configuration.
func (v *TikTokTabView) SyncFromConfig() {
	if v.session == nil {
		return
	}
	pCfg, ok := v.session.Config().Platforms["tiktok"]
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

// UpdateStatus updates the connection status badge on the TikTok tab.
func (v *TikTokTabView) UpdateStatus(status string, detail string) {
	fyne.Do(func() {
		if v.statusBadge == nil {
			return
		}
		switch status {
		case "connected", "live":
			v.statusBadge.SetText("🟢 Live (" + detail + ")")
		case "waiting":
			v.statusBadge.SetText("🟡 Waiting for Stream to Go Live (" + detail + ")...")
		case "connecting":
			v.statusBadge.SetText("🟡 Connecting (" + detail + ")...")
		default:
			if v.enableCheck != nil && v.enableCheck.Checked && v.usernameEntry != nil && v.usernameEntry.Text != "" {
				v.statusBadge.SetText("🟡 Waiting for Stream (" + v.usernameEntry.Text + ")...")
			} else {
				v.statusBadge.SetText("⚪ Disconnected")
			}
		}
	})
}

func (v *TikTokTabView) buildConnectionCard(pCfg config.PlatformConfig) *widget.Card {
	v.statusBadge = widget.NewLabelWithStyle("⚪ Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	v.usernameEntry = NewScrollPassEntry()
	v.usernameEntry.SetPlaceHolder("e.g. username or https://www.tiktok.com/@username/live")
	v.usernameEntry.SetText(pCfg.AccountName)

	v.enableCheck = widget.NewCheck("Enable TikTok Live Integration", nil)
	v.enableCheck.SetChecked(pCfg.Enabled)
	v.enableCheck.OnChanged = func(b bool) {
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["tiktok"]
			if !ok {
				p = config.DefaultConfig().Platforms["tiktok"]
			}
			p.Enabled = b
			p.EverEnabled = true
			c.Platforms["tiktok"] = p
		})
		v.updateConnectionStatus(b, v.usernameEntry.Text)
	}

	v.usernameEntry.OnChanged = func(val string) {
		trimmed := strings.TrimSpace(val)
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["tiktok"]
			if !ok {
				p = config.DefaultConfig().Platforms["tiktok"]
			}
			p.AccountName = trimmed
			c.Platforms["tiktok"] = p
		})
		v.updateConnectionStatus(v.enableCheck.Checked, trimmed)
	}

	v.updateConnectionStatus(pCfg.Enabled, pCfg.AccountName)

	v.session.SetOnTikTokStatus(func(status string, detail string) {
		v.UpdateStatus(status, detail)
	})

	hintLabel := widget.NewLabelWithStyle(
		"✓ Zero-Config Mode: Enter a TikTok username or live URL (no @ required). Public WebCast chat, gifts (with 2-second combo streak debouncing), and follows work automatically with no OAuth or API key needed.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	hintLabel.Wrapping = fyne.TextWrapWord

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("TikTok Username or Live URL:"), nil, v.usernameEntry),
		v.statusBadge,
		hintLabel,
	)

	return widget.NewCard("TikTok Live Connection", "Auto-detects live WebCast streams and reads chat, gifts & follows", cardContent)
}

func (v *TikTokTabView) buildReadRulesCard(pCfg config.PlatformConfig) *widget.Card {
	rr := pCfg.ReadRules

	v.allChatCheck = widget.NewCheck("Read All Chat Messages", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.AllChat = b
			c.Platforms["tiktok"] = p
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
			p := c.Platforms["tiktok"]
			p.ReadRules.Prefix = strings.TrimSpace(val)
			c.Platforms["tiktok"] = p
		})
	}

	v.prefixCheck = widget.NewCheck("Prefix / Command Only", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.PrefixOnly = b
			c.Platforms["tiktok"] = p
		})
		if b {
			v.prefixEntry.Enable()
		} else {
			v.prefixEntry.Disable()
		}
	})
	v.prefixCheck.SetChecked(rr.PrefixOnly)
	prefixRow := container.NewBorder(nil, nil, v.prefixCheck, nil, v.prefixEntry)

	updateFanClubLevelEntryState := func() {
		if v.fanClubLevelEntry == nil {
			return
		}
		fcChk := v.roleChecks["fan_club"]
		if v.roleGatedCheck != nil && v.roleGatedCheck.Checked && fcChk != nil && fcChk.Checked {
			v.fanClubLevelEntry.Enable()
		} else {
			v.fanClubLevelEntry.Disable()
		}
	}

	v.roleGatedCheck = widget.NewCheck("Role Gated (Only read messages from checked roles):", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.RoleGated = b
			c.Platforms["tiktok"] = p
		})
		for _, chk := range v.roleChecks {
			if b {
				chk.Enable()
			} else {
				chk.Disable()
			}
		}
		updateFanClubLevelEntryState()
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
		{"broadcaster", "Host / Broadcaster"},
		{"mod", "Moderator"},
		{"vip", "Top Gifter / VIP"},
		{"subscriber", "Super Fan / Subscriber"},
		{"fan_club", "Fan Club"},
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
			updateFanClubLevelEntryState()
		}
		roleBoxes.Add(chk)
	}

	minLvl := max(rr.MinFanClubLevel, 1)
	v.fanClubLevelEntry = NewScrollPassEntry()
	v.fanClubLevelEntry.SetPlaceHolder("1")
	v.fanClubLevelEntry.SetText(strconv.Itoa(minLvl))
	v.fanClubLevelEntry.OnChanged = func(val string) {
		lvl, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || lvl < 1 {
			lvl = 1
		}
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.MinFanClubLevel = lvl
			c.Platforms["tiktok"] = p
		})
	}
	updateFanClubLevelEntryState()

	roleBoxes.Add(widget.NewLabel("Min Lv:"))
	roleBoxes.Add(container.NewGridWrap(fyne.NewSize(55, v.fanClubLevelEntry.MinSize().Height), v.fanClubLevelEntry))

	v.paidOnlyCheck = widget.NewCheck("Paid Only (Only read TikTok Gifts)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.PaidOnly = b
			c.Platforms["tiktok"] = p
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
			p := c.Platforms["tiktok"]
			p.ReadRules.IgnoredPrefixes = prefixes
			c.Platforms["tiktok"] = p
		})
	}

	v.ignorePrefixCheck = widget.NewCheck("Ignore Bot Commands / Prefixes:", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.ReadRules.IgnorePrefixes = b
			c.Platforms["tiktok"] = p
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

	return widget.NewCard("Read Rules", "Control which TikTok messages are spoken aloud", cardContent)
}

func (v *TikTokTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

	giftNotif, ok := pCfg.Notifications["gift"]
	if !ok {
		giftNotif = config.NotificationEventConfig{
			Enabled:   true,
			Template:  "{user} sent {count} {gift}!",
			Threshold: 0,
		}
	}

	giftCheck := widget.NewCheck("TikTok Gifts (2s Combo Debounced)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			if p.Notifications == nil {
				p.Notifications = make(map[string]config.NotificationEventConfig)
			}
			n := p.Notifications["gift"]
			n.Enabled = b
			p.Notifications["gift"] = n
			c.Platforms["tiktok"] = p
		})
	})
	giftCheck.SetChecked(giftNotif.Enabled)

	giftTmplEntry := NewScrollPassEntry()
	giftTmplEntry.SetPlaceHolder("{user} sent {count} {gift}!")
	giftTmplEntry.SetText(giftNotif.Template)
	giftTmplEntry.OnChanged = func(val string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			if p.Notifications == nil {
				p.Notifications = make(map[string]config.NotificationEventConfig)
			}
			n := p.Notifications["gift"]
			n.Template = val
			p.Notifications["gift"] = n
			c.Platforms["tiktok"] = p
		})
	}

	minDiamondsEntry := NewScrollPassEntry()
	minDiamondsEntry.SetPlaceHolder("0")
	minDiamondsEntry.SetText(strconv.FormatInt(int64(giftNotif.Threshold), 10))
	minDiamondsEntry.OnChanged = func(val string) {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil || parsed < 0 {
			parsed = 0
		}
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			if p.Notifications == nil {
				p.Notifications = make(map[string]config.NotificationEventConfig)
			}
			n := p.Notifications["gift"]
			n.Threshold = parsed
			p.Notifications["gift"] = n
			c.Platforms["tiktok"] = p
		})
	}

	giftVars := EventTemplateVars("tiktok", "gift")
	giftHelp := newTemplateHelpMarker(v.window, "gift", "TikTok Gifts", giftVars, v.onStatus, &giftTmplEntry.Entry)
	minBox := container.NewHBox(container.NewCenter(giftHelp), widget.NewLabel("Min Diamonds:"), minDiamondsEntry)
	giftRow := container.NewBorder(nil, nil, giftCheck, minBox, giftTmplEntry)
	notifsList.Add(giftRow)

	followNotif, ok := pCfg.Notifications["follow"]
	if !ok {
		followNotif = config.NotificationEventConfig{
			Enabled:  false,
			Template: "{user} followed on TikTok!",
		}
	}

	followCheck := widget.NewCheck("New Followers", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			if p.Notifications == nil {
				p.Notifications = make(map[string]config.NotificationEventConfig)
			}
			n := p.Notifications["follow"]
			n.Enabled = b
			p.Notifications["follow"] = n
			c.Platforms["tiktok"] = p
		})
	})
	followCheck.SetChecked(followNotif.Enabled)

	followTmplEntry := NewScrollPassEntry()
	followTmplEntry.SetPlaceHolder("{user} followed on TikTok!")
	followTmplEntry.SetText(followNotif.Template)
	followTmplEntry.OnChanged = func(val string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			if p.Notifications == nil {
				p.Notifications = make(map[string]config.NotificationEventConfig)
			}
			n := p.Notifications["follow"]
			n.Template = val
			p.Notifications["follow"] = n
			c.Platforms["tiktok"] = p
		})
	}

	followVars := EventTemplateVars("tiktok", "follow")
	followHelp := newTemplateHelpMarker(v.window, "follow", "Followers", followVars, v.onStatus, &followTmplEntry.Entry)
	followRight := container.NewHBox(container.NewCenter(followHelp), container.NewGridWrap(fyne.NewSize(155, 36), widget.NewLabel("")))
	followRow := container.NewBorder(nil, nil, followCheck, followRight, followTmplEntry)
	notifsList.Add(followRow)

	extraEvents := []struct {
		key       string
		label     string
		fallback  string
		defaultOn bool
	}{
		{"sub", "New Super Fan (Sub)", "{user} became a Super Fan!", true},
		{"resub", "Super Fan Renewal", "{user} has been a Super Fan for {months} months!", true},
		{"gift_sub", "Gifted Super Fans", "{user} gifted {count} Super Fan memberships!", true},
		{"share", "Stream Shares", "{user} shared the stream!", false},
	}

	for _, ev := range extraEvents {
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

		chk := widget.NewCheck(label, func(b bool) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["tiktok"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Enabled = b
				p.Notifications[eventKey] = n
				c.Platforms["tiktok"] = p
			})
		})
		chk.SetChecked(notif.Enabled)

		tmplEntry := NewScrollPassEntry()
		tmplEntry.SetPlaceHolder(fallback)
		tmplEntry.SetText(notif.Template)
		tmplEntry.OnChanged = func(val string) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["tiktok"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Template = val
				p.Notifications[eventKey] = n
				c.Platforms["tiktok"] = p
			})
		}

		vars := EventTemplateVars("tiktok", eventKey)
		helpMarker := newTemplateHelpMarker(v.window, eventKey, label, vars, v.onStatus, &tmplEntry.Entry)
		rightWidget := container.NewHBox(container.NewCenter(helpMarker), container.NewGridWrap(fyne.NewSize(155, 36), widget.NewLabel("")))

		notifsList.Add(container.NewBorder(nil, nil, chk, rightWidget, tmplEntry))
	}

	return widget.NewCard("Notification Events", "Spoken alert templates for TikTok Gifts, Super Fans, Follows & Shares", notifsList)
}

func (v *TikTokTabView) buildOverridesCard(pCfg config.PlatformConfig) *widget.Card {
	ov := pCfg.Overrides

	namesCheck := widget.NewCheck("Speak Names for TikTok Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.Overrides.SpeakNames = b
			c.Platforms["tiktok"] = p
		})
	})
	namesCheck.SetChecked(ov.SpeakNames)

	emotesCheck := widget.NewCheck("Speak Emotes for TikTok Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["tiktok"]
			p.Overrides.SpeakEmotes = b
			c.Platforms["tiktok"] = p
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
		"Edit TikTok Default Voice & Speed",
		selectedVoice,
		sp,
		func(voiceID string, speed float32) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["tiktok"]
				p.Overrides.DefaultVoice = voiceID
				p.Overrides.DefaultSpeed = speed
				c.Platforms["tiktok"] = p
			})
		},
		v.onStatus,
	)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("TikTok Default Voice & Speed:"), nil, v.overrideVoiceField.Container)

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
			p := c.Platforms["tiktok"]
			p.Overrides.OverrideRules = b
			c.Platforms["tiktok"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent = container.NewVBox(
		v.overrideCheck,
		v.overridesBox,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to TikTok", cardContent)
}

// RefreshVoices updates the platform override voice field when enabled model packs change in Settings.
func (v *TikTokTabView) RefreshVoices() {
	fyne.Do(func() {
		if v.overrideVoiceField == nil || v.session == nil {
			return
		}
		cfg := v.session.Config()
		ov := cfg.Platforms["tiktok"].Overrides
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

func (v *TikTokTabView) saveRoles() {
	var roles []string
	for role, chk := range v.roleChecks {
		if chk.Checked {
			roles = append(roles, role)
		}
	}
	v.updateConfig(func(c *config.Config) {
		p := c.Platforms["tiktok"]
		p.ReadRules.Roles = roles
		c.Platforms["tiktok"] = p
	})
}

func (v *TikTokTabView) updateConnectionStatus(enabled bool, username string) {
	if v.statusBadge == nil {
		return
	}
	switch {
	case !enabled:
		v.statusBadge.SetText("⚪ Disabled")
	case username == "":
		v.statusBadge.SetText("🟡 Needs Username or Live URL")
	default:
		v.statusBadge.SetText("🟡 Waiting for Stream (" + username + ")...")
	}
}

// SetStatusHandler registers a callback invoked when configuration is updated.
func (v *TikTokTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *TikTokTabView) updateConfig(fn func(c *config.Config)) {
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
