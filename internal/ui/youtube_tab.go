package ui

import (
	"context"
	"net/url"
	"strings"
	"time"

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
	channelRow         *fyne.Container
	statusBadge        *widget.Label
	enableCheck        *widget.Check
	hintLabel          *widget.Label
	modHintLabel       *widget.Label
	profileSelect      *widget.Select
	profileRow         *fyne.Container
	oauthStatusLabel   *widget.Label
	oauthConnectBtn    *widget.Button
	oauthDisconnectBtn *widget.Button
	oauthCancel        context.CancelFunc
	syncingProfile     bool
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
	namesCheck         *widget.Check
	emotesCheck        *widget.Check
	connCard           *widget.Card
	rulesCard          *widget.Card
	notifsCard         *widget.Card
	overridesCard      *widget.Card
	notifChecks        []*widget.Check
	notifEntries       []*ScrollPassEntry
	notifHelpMarkers   []*templateHelpMarker
	notifMinEntries    []*ScrollPassEntry
	ttsEnabled         bool
	onStatus           func(msg string)
	initializing       bool
}

// NewYouTubeTabView creates a new YouTube tab view bound to the application session.
func NewYouTubeTabView(w fyne.Window, session *app.Session) *YouTubeTabView {
	ttsOn := true
	if session != nil {
		ttsOn = session.Config().TTS.Enabled
	}
	return &YouTubeTabView{
		window:     w,
		session:    session,
		roleChecks: make(map[string]*widget.Check),
		ttsEnabled: ttsOn,
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

	v.connCard = v.buildConnectionCard(pCfg)
	v.rulesCard = v.buildReadRulesCard(pCfg)
	v.notifsCard = v.buildNotificationsCard(pCfg)
	v.overridesCard = v.buildOverridesCard(pCfg)

	if !v.ttsEnabled {
		v.rulesCard.Hide()
		v.notifsCard.Hide()
		v.overridesCard.Hide()
	}

	v.SyncTTSState(v.ttsEnabled)

	content := container.NewVBox(
		v.connCard,
		v.rulesCard,
		v.notifsCard,
		v.overridesCard,
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
	v.refreshOAuthState()
	v.SyncTTSState(v.session.Config().TTS.Enabled)
}

// SyncTTSState updates all YouTube TTS-dependent controls to reflect whether TTS is enabled or disabled.
func (v *YouTubeTabView) SyncTTSState(enabled bool) {
	v.ttsEnabled = enabled
	fyne.Do(func() {
		if v.rulesCard != nil {
			if enabled {
				v.rulesCard.Show()
			} else {
				v.rulesCard.Hide()
			}
		}
		if v.notifsCard != nil {
			if enabled {
				v.notifsCard.Show()
			} else {
				v.notifsCard.Hide()
			}
		}
		if v.overridesCard != nil {
			if enabled {
				v.overridesCard.Show()
			} else {
				v.overridesCard.Hide()
			}
		}
	})
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

	v.hintLabel = widget.NewLabelWithStyle(
		"✓ Zero-Config Mode: Enter your YouTube channel name, channel URL, or live video URL (no @ required). Public chat, Super Chats, Super Stickers, Gifts (Jewels), and Memberships work automatically with no OAuth or API key needed.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	v.hintLabel.Wrapping = fyne.TextWrapWord

	v.modHintLabel = widget.NewLabelWithStyle(
		"🛡️ Optional OAuth: Sign in with Google/YouTube to enable direct broadcast detection (including Unlisted streams) and Chat Log moderation (Delete, Timeout, Ban).",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	v.modHintLabel.Wrapping = fyne.TextWrapWord

	v.oauthStatusLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.oauthConnectBtn = widget.NewButton("Connect YouTube OAuth", func() {
		v.startOAuthFlow()
	})
	v.oauthConnectBtn.Importance = widget.HighImportance

	v.oauthDisconnectBtn = widget.NewButton("Disconnect Profile", func() {
		if v.oauthCancel != nil {
			v.oauthCancel()
			v.oauthCancel = nil
		}
		if v.session != nil && v.session.YouTubeOAuth() != nil {
			_ = v.session.YouTubeOAuth().Disconnect()
			if nextTok, err := v.session.YouTubeOAuth().LoadToken(); err == nil && nextTok != nil && v.channelEntry != nil {
				target := strings.TrimPrefix(nextTok.ChannelHandle, "@")
				if target == "" {
					target = nextTok.ChannelID
				}
				if target != "" {
					v.channelEntry.SetText(target)
				}
			}
		}
		v.refreshOAuthState()
		if v.onStatus != nil {
			v.onStatus("✓ Disconnected YouTube profile.")
		}
	})

	v.profileSelect = widget.NewSelect(nil, func(selected string) {
		if v.syncingProfile || v.initializing || selected == "" {
			return
		}
		if v.session == nil || v.session.YouTubeOAuth() == nil {
			return
		}
		prof, err := v.session.YouTubeOAuth().SelectProfile(selected)
		if err != nil || prof == nil {
			return
		}
		if v.channelEntry != nil {
			target := strings.TrimPrefix(prof.ChannelHandle, "@")
			if target == "" {
				target = prof.ChannelID
			}
			if target != "" {
				v.channelEntry.SetText(target)
			}
		}
		v.refreshOAuthState()
		if v.onStatus != nil {
			v.onStatus("✓ Switched YouTube profile to " + prof.DisplayLabel())
		}
	})
	v.profileRow = container.NewBorder(nil, nil, widget.NewLabel("Connected Profile:"), nil, v.profileSelect)
	v.channelRow = container.NewBorder(nil, nil, widget.NewLabel("Channel Name or Video URL:"), nil, v.channelEntry)

	if v.session != nil && v.session.YouTubeOAuth() != nil {
		v.session.YouTubeOAuth().SetOnAuthChange(func() {
			fyne.Do(v.refreshOAuthState)
		})
	}

	v.refreshOAuthState()

	oauthRow := container.NewHBox(v.oauthStatusLabel, v.oauthConnectBtn, v.oauthDisconnectBtn)

	cardContent := container.NewVBox(
		v.enableCheck,
		v.channelRow,
		v.statusBadge,
		v.hintLabel,
		widget.NewSeparator(),
		oauthRow,
		v.profileRow,
		v.modHintLabel,
	)

	return widget.NewCard("YouTube Live Connection", "Auto-detects live broadcasts and reads chat & paid events", cardContent)
}

func (v *YouTubeTabView) refreshOAuthState() {
	hasTok := v.session != nil && v.session.YouTubeOAuth() != nil && v.session.YouTubeOAuth().HasToken()
	var channelIdent string
	var activeLabel string
	var profileLabels []string
	if hasTok {
		if tok, err := v.session.YouTubeOAuth().LoadToken(); err == nil && tok != nil {
			activeLabel = tok.DisplayLabel()
			switch {
			case tok.ChannelHandle != "":
				channelIdent = tok.ChannelHandle
			case tok.ChannelTitle != "":
				channelIdent = tok.ChannelTitle
			default:
				// Backfill channel identity asynchronously for tokens saved before channel metadata was stored
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					if _, _, handle, fErr := v.session.YouTubeOAuth().FetchAuthenticatedChannel(ctx); fErr == nil && handle != "" {
						fyne.Do(v.refreshOAuthState)
					}
				}()
			}
			if v.channelEntry != nil && strings.TrimSpace(v.channelEntry.Text) == "" && tok.ChannelHandle != "" {
				v.channelEntry.SetText(strings.TrimPrefix(tok.ChannelHandle, "@"))
			}
		}
		for _, p := range v.session.YouTubeOAuth().ListProfiles() {
			profileLabels = append(profileLabels, p.DisplayLabel())
		}
	}

	if v.profileSelect != nil {
		v.syncingProfile = true
		v.profileSelect.Options = profileLabels
		if activeLabel != "" {
			v.profileSelect.SetSelected(activeLabel)
		} else {
			v.profileSelect.ClearSelected()
		}
		v.profileSelect.Refresh()
		v.syncingProfile = false
	}
	if v.profileRow != nil {
		if hasTok && len(profileLabels) > 0 {
			v.profileRow.Show()
		} else {
			v.profileRow.Hide()
		}
	}

	if v.channelRow != nil {
		if hasTok {
			v.channelRow.Hide()
		} else {
			v.channelRow.Show()
		}
	}
	if v.hintLabel != nil {
		if hasTok {
			v.hintLabel.Hide()
		} else {
			v.hintLabel.Show()
		}
	}
	if v.modHintLabel != nil {
		if hasTok {
			v.modHintLabel.SetText("✓ Authenticated Mode: Direct broadcast & Unlisted stream detection, automatic liveChatId resolution, and Chat Log moderation (Delete, Timeout, Ban) are active.")
		} else {
			v.modHintLabel.SetText("🛡️ Optional OAuth: Sign in with Google/YouTube to enable direct broadcast detection (including Unlisted streams) and Chat Log moderation (Delete, Timeout, Ban).")
		}
	}
	if v.oauthStatusLabel != nil {
		if hasTok {
			if channelIdent != "" {
				v.oauthStatusLabel.SetText("🟢 YouTube OAuth: Connected (" + channelIdent + ")")
			} else {
				v.oauthStatusLabel.SetText("🟢 YouTube OAuth: Connected")
			}
		} else {
			v.oauthStatusLabel.SetText("⚪ YouTube OAuth: Not Connected")
		}
	}
	if v.oauthConnectBtn != nil {
		if hasTok {
			v.oauthConnectBtn.SetText("Add / Switch Profile (OAuth)")
			v.oauthConnectBtn.Importance = widget.MediumImportance
		} else {
			v.oauthConnectBtn.SetText("Connect YouTube OAuth")
			v.oauthConnectBtn.Importance = widget.HighImportance
		}
		v.oauthConnectBtn.Enable()
		v.oauthConnectBtn.Refresh()
	}
	if v.oauthDisconnectBtn != nil {
		if hasTok {
			v.oauthDisconnectBtn.Show()
		} else {
			v.oauthDisconnectBtn.Hide()
		}
	}
}

func (v *YouTubeTabView) startOAuthFlow() {
	if v.session == nil || v.session.YouTubeOAuth() == nil {
		return
	}
	if v.oauthCancel != nil {
		v.oauthCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	v.oauthCancel = cancel

	if v.oauthConnectBtn != nil {
		v.oauthConnectBtn.SetText("Waiting for Google Sign-In...")
		v.oauthConnectBtn.Disable()
	}
	if v.oauthStatusLabel != nil {
		v.oauthStatusLabel.SetText("🟡 Select your Google account & YouTube profile in browser...")
	}

	go func() {
		defer cancel()
		tok, err := v.session.YouTubeOAuth().AuthenticatePKCE(ctx, func(authURL string) error {
			u, err := url.Parse(authURL)
			if err != nil {
				return err
			}
			appInst := fyne.CurrentApp()
			if appInst != nil {
				return appInst.OpenURL(u)
			}
			return nil
		})
		fyne.Do(func() {
			if err == nil && tok != nil && v.channelEntry != nil {
				target := strings.TrimPrefix(tok.ChannelHandle, "@")
				if target == "" {
					target = tok.ChannelID
				}
				if target != "" {
					v.channelEntry.SetText(target)
				}
			}
			v.refreshOAuthState()
			if err != nil {
				if v.onStatus != nil {
					v.onStatus("⚠️ YouTube OAuth error: " + err.Error())
				}
				return
			}
			if v.onStatus != nil {
				v.onStatus("✓ Connected YouTube profile!")
			}
		})
	}()
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

	v.paidOnlyCheck = widget.NewCheck("Paid Only (Only read Super Chats, Super Stickers, Gifts, and Memberships)", func(b bool) {
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
	v.notifChecks = nil
	v.notifEntries = nil
	v.notifHelpMarkers = nil
	v.notifMinEntries = nil

	notifsList := container.NewVBox()

	events := []struct {
		key       string
		label     string
		fallback  string
		defaultOn bool
	}{
		{"super_chat", "Super Chat", "{user} sent {amount} {currency}! {message}", true},
		{"super_sticker", "Super Sticker", "{user} sent a {amount} {currency} super sticker!", true},
		{"gift", "Gifts (Jewels)", "{user} sent {gift}!", true},
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

		v.notifChecks = append(v.notifChecks, enabledCheck)
		v.notifEntries = append(v.notifEntries, tmplEntry)
		v.notifHelpMarkers = append(v.notifHelpMarkers, helpMarker)

		row := container.NewBorder(nil, nil, enabledCheck, rightWidget, tmplEntry)
		notifsList.Add(row)
	}

	return widget.NewCard("Notification Events", "Spoken alert templates for YouTube Super Chats, Gifts & Memberships", notifsList)
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
	v.namesCheck = namesCheck

	emotesCheck := widget.NewCheck("Speak Emotes & Emojis for YouTube Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.SpeakEmotes = b
			c.Platforms["youtube"] = p
		})
	})
	emotesCheck.SetChecked(ov.SpeakEmotes)
	v.emotesCheck = emotesCheck

	dedupeCheck := widget.NewCheck("Deduplicate Emotes & Emojis for YouTube Chat", func(b bool) {
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.DeduplicateEmotes = b
			c.Platforms["youtube"] = p
		})
	})
	dedupeCheck.SetChecked(ov.DeduplicateEmotes)

	dupWordsSelect := widget.NewSelect(DuplicateWordsOptions, func(s string) {
		val := OptionToDuplicateWords(s)
		v.updateConfig(func(c *config.Config) {
			p := c.Platforms["youtube"]
			p.Overrides.MaxDuplicateWords = val
			c.Platforms["youtube"] = p
		})
	})
	dupWordsSelect.SetSelected(DuplicateWordsToOption(ov.MaxDuplicateWords))
	dupWordsRow := container.NewHBox(
		widget.NewLabel("Max Consecutive Duplicate Words:"),
		dupWordsSelect,
	)

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
		dedupeCheck,
		dupWordsRow,
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
