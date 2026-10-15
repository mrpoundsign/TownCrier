package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

// YouTubeTabView provides the Fyne UI controls for zero-config YouTube Live integration.
type YouTubeTabView struct {
	window  fyne.Window
	session *app.Session

	channelEntry      *widget.Entry
	statusBadge       *widget.Label
	enableCheck       *widget.Check
	allChatCheck      *widget.Check
	ignorePrefixCheck *widget.Check
	ignorePrefixEntry *widget.Entry
	prefixCheck       *widget.Check
	prefixEntry       *widget.Entry
	roleGatedCheck    *widget.Check
	roleChecks        map[string]*widget.Check
	paidOnlyCheck     *widget.Check
	overrideCheck     *widget.Check
	onStatus          func(msg string)
	initializing      bool
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

	return container.NewVScroll(content)
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
		case "connected":
			v.statusBadge.SetText("🟢 Connected (" + detail + ")")
		case "waiting":
			v.statusBadge.SetText("🟡 Waiting for Stream to Go Live (" + detail + ")...")
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

	v.channelEntry = widget.NewEntry()
	v.channelEntry.SetPlaceHolder("e.g. @mrpoundsign or https://www.youtube.com/watch?v=...")
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
		"✓ Zero-Config Mode: Enter your YouTube @handle, channel URL, or live video URL. Public chat, Super Chats, Super Stickers, and Memberships work automatically with no OAuth or API key needed.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	hintLabel.Wrapping = fyne.TextWrapWord

	cardContent := container.NewVBox(
		v.enableCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Channel @Handle or Video URL:"), nil, v.channelEntry),
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
		container.NewPadded(roleBoxes),
		v.paidOnlyCheck,
	)

	return widget.NewCard("Read Rules", "Control which YouTube messages are spoken aloud", cardContent)
}

func (v *YouTubeTabView) buildNotificationsCard(pCfg config.PlatformConfig) *widget.Card {
	notifsList := container.NewVBox()

	events := []struct {
		key      string
		label    string
		fallback string
	}{
		{"super_chat", "Super Chat", "{user} sent {amount} {currency}! {message}"},
		{"super_sticker", "Super Sticker", "{user} sent a {amount} {currency} super sticker!"},
		{"sub", "New Member", "{user} just became a member!"},
		{"resub", "Member Milestone", "{user} has been a member for {months} months! {message}"},
		{"gift_sub", "Gifted Memberships", "{user} gifted {count} memberships!"},
	}

	for _, ev := range events {
		eventKey := ev.key
		label := ev.label
		fallback := ev.fallback

		notif, ok := pCfg.Notifications[eventKey]
		if !ok {
			notif = config.NotificationEventConfig{
				Enabled:  true,
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

		tmplEntry := widget.NewEntry()
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

		row := container.NewBorder(nil, nil, enabledCheck, nil, tmplEntry)
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

	voiceLabels := tts.VoiceDisplayLabels()
	selectedVoice := ov.DefaultVoice
	if selectedVoice == "" {
		selectedVoice = "af_bella"
	}
	voiceSelect := widget.NewSelect(voiceLabels, nil)
	voiceSelect.SetSelected(tts.VoiceDisplayName(selectedVoice))
	voiceSelect.OnChanged = func(s string) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.DefaultVoice = tts.VoiceIDFromLabel(s)
			c.Platforms["youtube"] = p
		})
	}
	voiceRow := container.NewBorder(nil, nil, widget.NewLabel("YouTube Default Voice:"), nil, voiceSelect)

	sp := ov.DefaultSpeed
	if sp <= 0 {
		sp = 1.0
	}
	speedLabel := widget.NewLabel(fmt.Sprintf("%.2fx", sp))
	speedSlider := widget.NewSlider(0.5, 2.0)
	speedSlider.Step = 0.05
	speedSlider.SetValue(float64(sp))
	speedSlider.OnChanged = func(val float64) {
		speedLabel.SetText(fmt.Sprintf("%.2fx", val))
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.DefaultSpeed = float32(val)
			c.Platforms["youtube"] = p
		})
	}
	speedRow := container.NewBorder(nil, nil, widget.NewLabel("YouTube Default Speed:"), speedLabel, speedSlider)

	toggleOverrides := func(enabled bool) {
		if enabled {
			namesCheck.Enable()
			emotesCheck.Enable()
			voiceSelect.Enable()
			speedSlider.Enable()
		} else {
			namesCheck.Disable()
			emotesCheck.Disable()
			voiceSelect.Disable()
			speedSlider.Disable()
		}
	}
	toggleOverrides(ov.OverrideRules)

	v.overrideCheck = widget.NewCheck("Override Global Chat Rules for YouTube", func(b bool) {
		toggleOverrides(b)
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.OverrideRules = b
			c.Platforms["youtube"] = p
		})
	})
	v.overrideCheck.SetChecked(ov.OverrideRules)

	cardContent := container.NewVBox(
		v.overrideCheck,
		namesCheck,
		emotesCheck,
		voiceRow,
		speedRow,
	)

	return widget.NewCard("Platform Overrides", "Custom rules applied only to YouTube", cardContent)
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
		v.statusBadge.SetText("🟡 Needs @Handle or Video URL")
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
