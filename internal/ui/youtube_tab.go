package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// YouTubeTabView provides the Fyne UI controls for zero-config YouTube Live integration.
type YouTubeTabView struct {
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

// NewYouTubeTabView creates a new YouTube tab view bound to the application session.
func NewYouTubeTabView(w fyne.Window, session *app.Session) *YouTubeTabView {
	return &YouTubeTabView{
		window:     w,
		session:    session,
		roleChecks: make(map[string]*widget.Check),
	}
}

// Build generates the Fyne CanvasObject for the YouTube tab.
func (v *YouTubeTabView) Build() fyne.CanvasObject {
	v.initializing = true
	defer func() { v.initializing = false }()

	cfg := v.session.Config()
	pCfg, hasYouTube := cfg.Platforms["youtube"]
	if !hasYouTube {
		pCfg = config.DefaultConfig().Platforms["youtube"]
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

// SyncFromConfig updates the YouTube tab controls to match the active session configuration.
func (v *YouTubeTabView) SyncFromConfig() {
	if v.session == nil {
		return
	}
	pCfg, ok := v.session.Config().Platforms["youtube"]
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

// UpdateStatus updates the connection status badge on the YouTube tab.
func (v *YouTubeTabView) UpdateStatus(status string, detail string) {
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
			if v.enableCheck != nil && v.enableCheck.Checked && v.channelEntry != nil && v.channelEntry.Text != "" {
				v.statusBadge.SetText("🟡 Waiting for Stream (" + v.channelEntry.Text + ")...")
			} else {
				v.statusBadge.SetText("⚪ Disconnected")
			}
		}
	})
}

func (v *YouTubeTabView) buildConnectionCard(pCfg config.PlatformConfig) *widget.Card {
	v.statusBadge = widget.NewLabelWithStyle("⚪ Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	v.channelEntry = NewScrollPassEntry()
	v.channelEntry.SetPlaceHolder("e.g. mrpoundsign or https://www.youtube.com/watch?v=...")
	v.channelEntry.SetText(pCfg.AccountName)

	v.enableCheck = widget.NewCheck("Enable YouTube Live Integration", nil)
	v.enableCheck.SetChecked(pCfg.Enabled)
	v.enableCheck.OnChanged = func(b bool) {
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["youtube"]
			if !ok {
				p = config.DefaultConfig().Platforms["youtube"]
			}
			p.Enabled = b
			p.EverEnabled = true
			c.Platforms["youtube"] = p
		})
		v.updateConnectionStatus(b, v.channelEntry.Text)
	}

	v.channelEntry.OnChanged = func(val string) {
		trimmed := strings.TrimSpace(val)
		v.updateConfig(func(c *config.Config) {
			if c.Platforms == nil {
				c.Platforms = make(map[string]config.PlatformConfig)
			}
			p, ok := c.Platforms["youtube"]
			if !ok {
				p = config.DefaultConfig().Platforms["youtube"]
			}
			p.AccountName = trimmed
			c.Platforms["youtube"] = p
		})
		v.updateConnectionStatus(v.enableCheck.Checked, trimmed)
	}

	v.updateConnectionStatus(pCfg.Enabled, pCfg.AccountName)

	v.session.SetOnYouTubeStatus(func(status string, detail string) {
		v.UpdateStatus(status, detail)
	})

	hintLabel := widget.NewLabelWithStyle(
		"✓ Zero-Config Mode: Enter your YouTube channel name, channel URL, or live video URL (no @ required). Public chat, Super Chats, Super Stickers, and Memberships work automatically with no OAuth or API key needed.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	hintLabel.Wrapping = fyne.TextWrapWord

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Channel Name or Video URL:"), nil, v.channelEntry),
		v.statusBadge,
		hintLabel,
	)

	return widget.NewCard("YouTube Live Connection", "Auto-detects live broadcasts and reads chat & paid events", cardContent)
}

func (v *YouTubeTabView) buildReadRulesCard(pCfg config.PlatformConfig) *widget.Card {
	rr := pCfg.ReadRules

	v.allChatCheck = widget.NewCheck("Read All Chat Messages", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.ReadRules.AllChat = b
			c.Platforms["youtube"] = p
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
			p := c.Platforms["youtube"]
			p.ReadRules.Prefix = strings.TrimSpace(val)
			c.Platforms["youtube"] = p
		})
	}

	v.prefixCheck = widget.NewCheck("Prefix / Command Only", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.ReadRules.PrefixOnly = b
			c.Platforms["youtube"] = p
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
			p := c.Platforms["youtube"]
			p.ReadRules.RoleGated = b
			c.Platforms["youtube"] = p
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
		{"broadcaster", "Owner / Broadcaster"},
		{"mod", "Moderator"},
		{"vip", "Verified"},
		{"subscriber", "Channel Member"},
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

	v.paidOnlyCheck = widget.NewCheck("Paid Only (Only read Super Chats, Super Stickers, and Memberships)", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.ReadRules.PaidOnly = b
			c.Platforms["youtube"] = p
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
			p := c.Platforms["youtube"]
			p.ReadRules.IgnoredPrefixes = prefixes
			c.Platforms["youtube"] = p
		})
	}

	v.ignorePrefixCheck = widget.NewCheck("Ignore Bot Commands / Prefixes:", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.ReadRules.IgnorePrefixes = b
			c.Platforms["youtube"] = p
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

	return widget.NewCard("Read Rules", "Control which YouTube messages are spoken aloud", cardContent)
}

func (v *YouTubeTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

	events := []struct {
		key       string
		label     string
		fallback  string
		defaultOn bool
	}{
		{"super_chat", "Super Chat", "{user} sent {amount} {currency}! {message}", true},
		{"super_sticker", "Super Sticker", "{user} sent a {amount} {currency} super sticker!", true},
		{"sub", "New Member", "{user} just became a member!", true},
		{"resub", "Member Milestone", "{user} has been a member for {months} months! {message}", true},
		{"gift_sub", "Gifted Memberships", "{user} gifted {count} memberships!", true},
		{"gift_redemption", "Gift Membership Received", "{user} received a gift membership from {recipient}!", false},
		{"raid", "Live Redirect / Raid", "{user} raided with a live redirect!", true},
		{"charity_donation", "Charity Donation", "{user} donated {amount} {currency}! {message}", true},
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
				p := c.Platforms["youtube"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Enabled = b
				p.Notifications[eventKey] = n
				c.Platforms["youtube"] = p
			})
		})
		enabledCheck.SetChecked(notif.Enabled)

		tmplEntry := NewScrollPassEntry()
		tmplEntry.SetText(notif.Template)
		tmplEntry.OnChanged = func(val string) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["youtube"]
				if p.Notifications == nil {
					p.Notifications = make(map[string]config.NotificationEventConfig)
				}
				n := p.Notifications[eventKey]
				n.Template = val
				p.Notifications[eventKey] = n
				c.Platforms["youtube"] = p
			})
		}

		vars := EventTemplateVars("youtube", eventKey)
		helpMarker := newTemplateHelpMarker(v.window, eventKey, label, vars, v.onStatus, &tmplEntry.Entry)
		rightWidget := container.NewCenter(helpMarker)

		row := container.NewBorder(nil, nil, enabledCheck, rightWidget, tmplEntry)
		notifsList.Add(row)
	}

	return widget.NewCard("Notification Events", "Spoken alert templates for YouTube Super Chats & Memberships", notifsList)
}

func (v *YouTubeTabView) buildOverridesCard(pCfg config.PlatformConfig) *widget.Card {
	ov := pCfg.Overrides

	namesCheck := widget.NewCheck("Speak Names for YouTube Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.SpeakNames = b
			c.Platforms["youtube"] = p
		})
	})
	namesCheck.SetChecked(ov.SpeakNames)

	emotesCheck := widget.NewCheck("Speak Emotes for YouTube Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.SpeakEmotes = b
			c.Platforms["youtube"] = p
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
		"Edit YouTube Default Voice & Speed",
		selectedVoice,
		sp,
		func(voiceID string, speed float32) {
			v.updateConfig(func(c *config.Config) {
				p := c.Platforms["youtube"]
				p.Overrides.DefaultVoice = voiceID
				p.Overrides.DefaultSpeed = speed
				c.Platforms["youtube"] = p
			})
		},
		v.onStatus,
	)
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("YouTube Default Voice & Speed:"), nil, v.overrideVoiceField.Container)

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
			p := c.Platforms["youtube"]
			p.Overrides.OverrideRules = b
			c.Platforms["youtube"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent = container.NewVBox(
		v.overrideCheck,
		v.overridesBox,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to YouTube", cardContent)
}

// RefreshVoices updates the platform override voice field when enabled model packs change in Settings.
func (v *YouTubeTabView) RefreshVoices() {
	fyne.Do(func() {
		if v.overrideVoiceField == nil || v.session == nil {
			return
		}
		cfg := v.session.Config()
		ov := cfg.Platforms["youtube"].Overrides
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

func (v *YouTubeTabView) saveRoles() {
	var roles []string
	for role, chk := range v.roleChecks {
		if chk.Checked {
			roles = append(roles, role)
		}
	}
	v.updateConfig(func(c *config.Config) {
		p := c.Platforms["youtube"]
		p.ReadRules.Roles = roles
		c.Platforms["youtube"] = p
	})
}

func (v *YouTubeTabView) updateConnectionStatus(enabled bool, channel string) {
	if v.statusBadge == nil {
		return
	}
	switch {
	case !enabled:
		v.statusBadge.SetText("⚪ Disabled")
	case channel == "":
		v.statusBadge.SetText("🟡 Needs Channel Name or Video URL")
	default:
		v.statusBadge.SetText("🟡 Waiting for Stream (" + channel + ")...")
	}
}

// SetStatusHandler registers a callback invoked when configuration is updated.
func (v *YouTubeTabView) SetStatusHandler(fn func(msg string)) {
	v.onStatus = fn
}

func (v *YouTubeTabView) updateConfig(fn func(c *config.Config)) {
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
