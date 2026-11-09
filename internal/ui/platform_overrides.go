package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

const (
	boolOnSpeakNames      = "On (Speak Names)"
	boolOffSpeakNames     = "Off (Don't Speak Names)"
	boolOnSpeakEmotes     = "On (Speak Emotes)"
	boolOffSpeakEmotes    = "Off (Strip Emotes)"
	boolOnDedupeEmotes    = "On (Once per Sentence)"
	boolOffDedupeEmotes   = "Off (Allow Repeats)"
	boolOnPlatformPrefix  = "On (Speak Platform)"
	boolOffPlatformPrefix = "Off (Don't Speak Platform)"
	boolOnAllowBcaster    = "On (Read Broadcaster @)"
	boolOffAllowBcaster   = "Off (Ignore Broadcaster @)"
	boolOnAlertMention    = "On (Play Alert Sound)"
	boolOffAlertMention   = "Off (No Alert Sound)"
	boolOnTranslateChat   = "On (Translate Foreign Chat)"
	boolOffTranslateChat  = "Off (Don't Translate)"
)

// PlatformOverridesControl manages the per-setting global/override pulldowns for a streaming platform tab.
type PlatformOverridesControl struct {
	PlatformKey   string
	PlatformTitle string
	Card          *widget.Card

	NamesSelect                   *widget.Select
	CoalesceSelect                *widget.Select
	EmotesSelect                  *widget.Select
	DedupeSelect                  *widget.Select
	DupWordsSelect                *widget.Select
	PlatformNameSelect            *widget.Select
	TranslateSelect               *widget.Select
	MentionsModeSelect            *widget.Select
	AllowBroadcasterSelect        *widget.Select
	PlayAlertIgnoredMentionSelect *widget.Select
	VoiceSelect                   *widget.Select
	VoiceEditBtn                  *widget.Button

	window       fyne.Window
	session      *app.Session
	updateConfig func(fn func(c *config.Config))
	onStatus     func(msg string)
	syncing      bool
}

// NewPlatformOverridesControl builds the Platform Overrides card with per-setting Global/Override pulldowns.
func NewPlatformOverridesControl(
	w fyne.Window,
	session *app.Session,
	platformKey string,
	platformTitle string,
	updateConfig func(fn func(c *config.Config)),
	onStatus func(msg string),
) *PlatformOverridesControl {
	ctrl := &PlatformOverridesControl{
		PlatformKey:   platformKey,
		PlatformTitle: platformTitle,
		window:        w,
		session:       session,
		updateConfig:  updateConfig,
		onStatus:      onStatus,
	}
	ctrl.build()
	return ctrl
}

func (c *PlatformOverridesControl) currentConfig() *config.Config {
	if c.session != nil {
		return c.session.Config()
	}
	return config.DefaultConfig()
}

func formatBoolGlobalOption(globalVal bool, onLabel, offLabel string) string {
	if globalVal {
		return "Global (" + onLabel + ")"
	}
	return "Global (" + offLabel + ")"
}

func boolOptionsForGlobal(globalVal bool, onLabel, offLabel string) []string {
	return []string{
		formatBoolGlobalOption(globalVal, onLabel, offLabel),
		onLabel,
		offLabel,
	}
}

func selectedBoolOption(mode string, legacyOverride bool, legacyVal bool, globalVal bool, onLabel, offLabel string) string {
	switch config.NormalizeBoolOverrideMode(mode) {
	case config.OverrideModeOn:
		return onLabel
	case config.OverrideModeOff:
		return offLabel
	default:
		if legacyOverride {
			if legacyVal {
				return onLabel
			}
			return offLabel
		}
		return formatBoolGlobalOption(globalVal, onLabel, offLabel)
	}
}

func parseSelectedBoolMode(selected, onLabel string) (string, bool) {
	if strings.HasPrefix(selected, "Global (") {
		return config.OverrideModeGlobal, false
	}
	if selected == onLabel {
		return config.OverrideModeOn, true
	}
	return config.OverrideModeOff, false
}

func (c *PlatformOverridesControl) build() {
	c.NamesSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnSpeakNames)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.SpeakNamesMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.SpeakNames = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
		c.syncCoalesceSubControl()
	})

	c.CoalesceSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		if strings.HasPrefix(selected, "Global (") {
			c.updateConfig(func(cfg *config.Config) {
				p := cfg.Platforms[c.PlatformKey]
				p.Overrides.CoalesceConsecutiveMode = config.OverrideModeGlobal
				cfg.Platforms[c.PlatformKey] = p
			})
			return
		}
		val := OptionToCoalesceSeconds(selected)
		mode := config.CoalesceSecondsToOverrideMode(val)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.CoalesceConsecutiveMode = mode
			p.Overrides.CoalesceConsecutiveSeconds = val
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.EmotesSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnSpeakEmotes)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.SpeakEmotesMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.SpeakEmotes = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.DedupeSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnDedupeEmotes)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.DeduplicateEmotesMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.DeduplicateEmotes = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.DupWordsSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		if strings.HasPrefix(selected, "Global (") {
			c.updateConfig(func(cfg *config.Config) {
				p := cfg.Platforms[c.PlatformKey]
				p.Overrides.MaxDuplicateWordsMode = config.OverrideModeGlobal
				cfg.Platforms[c.PlatformKey] = p
			})
			return
		}
		val := OptionToDuplicateWords(selected)
		mode := config.DuplicateWordsToOverrideMode(val)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.MaxDuplicateWordsMode = mode
			p.Overrides.MaxDuplicateWords = val
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.PlatformNameSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnPlatformPrefix)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.SpeakPlatformNameMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.SpeakPlatformName = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.TranslateSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, _ := parseSelectedBoolMode(selected, boolOnTranslateChat)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.TranslateMode = mode
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.AllowBroadcasterSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnAllowBcaster)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.AllowBroadcasterMentionsMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.AllowBroadcasterMentions = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.PlayAlertIgnoredMentionSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		mode, bVal := parseSelectedBoolMode(selected, boolOnAlertMention)
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.PlayAlertOnIgnoredMentionMode = mode
			if mode != config.OverrideModeGlobal {
				p.Overrides.PlayAlertOnIgnoredMention = bVal
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.MentionsModeSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		var ovMode string
		if strings.HasPrefix(selected, "Global (") {
			ovMode = config.OverrideModeGlobal
		} else {
			ovMode = OptionToIgnoreMentionsMode(selected)
		}
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.IgnoreMentionsOverride = ovMode
			if ovMode != config.OverrideModeGlobal {
				p.Overrides.IgnoreMentionsMode = ovMode
			}
			cfg.Platforms[c.PlatformKey] = p
		})
		c.syncMentionSubControls()
	})

	c.VoiceSelect = widget.NewSelect(nil, func(selected string) {
		if c.syncing {
			return
		}
		if strings.HasPrefix(selected, "Global (") {
			c.updateConfig(func(cfg *config.Config) {
				p := cfg.Platforms[c.PlatformKey]
				p.Overrides.VoiceOverrideMode = config.OverrideModeGlobal
				cfg.Platforms[c.PlatformKey] = p
			})
			return
		}
		c.updateConfig(func(cfg *config.Config) {
			p := cfg.Platforms[c.PlatformKey]
			p.Overrides.VoiceOverrideMode = config.OverrideModeCustom
			if strings.TrimSpace(p.Overrides.DefaultVoice) == "" {
				p.Overrides.DefaultVoice = resolveInitialVoiceID(c.session, cfg.GlobalChatRules.DefaultVoice)
			}
			if p.Overrides.DefaultSpeed <= 0 {
				sp := cfg.GlobalChatRules.DefaultSpeed
				if sp <= 0 {
					sp = 1.0
				}
				p.Overrides.DefaultSpeed = sp
			}
			cfg.Platforms[c.PlatformKey] = p
		})
	})

	c.VoiceEditBtn = widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		cfg := c.currentConfig()
		ov := cfg.Platforms[c.PlatformKey].Overrides
		initVoice := ov.DefaultVoice
		if strings.TrimSpace(initVoice) == "" {
			initVoice = resolveInitialVoiceID(c.session, cfg.GlobalChatRules.DefaultVoice)
		}
		initSpeed := ov.DefaultSpeed
		if initSpeed <= 0 {
			initSpeed = cfg.GlobalChatRules.DefaultSpeed
			if initSpeed <= 0 {
				initSpeed = 1.0
			}
		}
		ShowVoiceSpeedDialog(
			c.window,
			c.session,
			"Edit "+c.PlatformTitle+" Default Voice & Speed",
			initVoice,
			initSpeed,
			func(newVoiceID string, newSpeed float32) {
				c.updateConfig(func(cfg *config.Config) {
					p := cfg.Platforms[c.PlatformKey]
					p.Overrides.VoiceOverrideMode = config.OverrideModeCustom
					p.Overrides.DefaultVoice = newVoiceID
					p.Overrides.DefaultSpeed = newSpeed
					cfg.Platforms[c.PlatformKey] = p
				})
				c.Refresh()
			},
			c.onStatus,
		)
	})

	makeRow := func(label string, control fyne.CanvasObject) *fyne.Container {
		lbl := widget.NewLabel(label)
		return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(260, 36), lbl), nil, control)
	}

	voiceControl := container.NewBorder(nil, nil, nil, c.VoiceEditBtn, c.VoiceSelect)

	c.Refresh()

	cardContent := container.NewVBox(
		makeRow("Speak Chatter Names:", c.NamesSelect),
		makeRow("Coalesce Consecutive Speaker:", c.CoalesceSelect),
		makeRow("Speak Emotes & Emojis:", c.EmotesSelect),
		makeRow("Deduplicate Emotes & Emojis:", c.DedupeSelect),
		makeRow("Max Consecutive Duplicate Words:", c.DupWordsSelect),
		makeRow("Speak Platform Name Prefix:", c.PlatformNameSelect),
		makeRow("Translate Foreign Chat:", c.TranslateSelect),
		widget.NewSeparator(),
		makeRow("@ Mentions & Replies:", c.MentionsModeSelect),
		makeRow("Allow Broadcaster @ Mentions:", c.AllowBroadcasterSelect),
		makeRow("Alert Sound on Ignored Mention:", c.PlayAlertIgnoredMentionSelect),
		widget.NewSeparator(),
		makeRow(c.PlatformTitle+" Default Voice & Speed:", voiceControl),
	)

	c.Card = widget.NewCard(
		"Chat Rules & Voice Overrides",
		"Choose Global to inherit global settings or set an explicit override for "+c.PlatformTitle,
		cardContent,
	)
}

func (c *PlatformOverridesControl) syncCoalesceSubControl() {
	if c.CoalesceSelect == nil {
		return
	}
	cfg := c.currentConfig()
	speakNames, _, _, _, _, _, _ := cfg.EffectiveChatRules(c.PlatformKey)
	if speakNames {
		c.CoalesceSelect.Enable()
	} else {
		c.CoalesceSelect.Disable()
	}
}

func (c *PlatformOverridesControl) syncMentionSubControls() {
	cfg := c.currentConfig()
	effMode, _, _ := cfg.EffectiveMentionRules(c.PlatformKey)
	if effMode != config.IgnoreMentionsOff {
		c.AllowBroadcasterSelect.Enable()
		c.PlayAlertIgnoredMentionSelect.Enable()
	} else {
		c.AllowBroadcasterSelect.Disable()
		c.PlayAlertIgnoredMentionSelect.Disable()
	}
}

// Refresh synchronizes all pulldown options and selected states with the current global and platform config.
func (c *PlatformOverridesControl) Refresh() {
	cfg := c.currentConfig()
	g := cfg.GlobalChatRules
	p, ok := cfg.Platforms[c.PlatformKey]
	if !ok {
		p = config.DefaultConfig().Platforms[c.PlatformKey]
	}
	ov := p.Overrides

	c.syncing = true
	defer func() { c.syncing = false }()

	// 1. Speak Names
	c.NamesSelect.Options = boolOptionsForGlobal(g.SpeakNames, boolOnSpeakNames, boolOffSpeakNames)
	c.NamesSelect.SetSelected(selectedBoolOption(ov.SpeakNamesMode, ov.OverrideRules, ov.SpeakNames, g.SpeakNames, boolOnSpeakNames, boolOffSpeakNames))
	c.NamesSelect.Refresh()

	// 1b. Coalesce Consecutive Speaker
	globalCoalesceOpt := fmt.Sprintf("Global (%s)", CoalesceSecondsToOption(g.CoalesceConsecutiveSeconds))
	coalesceOpts := make([]string, 0, 1+len(CoalesceSpeakerOptions))
	coalesceOpts = append(coalesceOpts, globalCoalesceOpt)
	coalesceOpts = append(coalesceOpts, CoalesceSpeakerOptions...)
	c.CoalesceSelect.Options = coalesceOpts

	coalMode := config.NormalizeCoalesceOverrideMode(ov.CoalesceConsecutiveMode)
	switch coalMode {
	case config.OverrideModeOff:
		c.CoalesceSelect.SetSelected("Off (Always Speak Name)")
	case config.OverrideModeGlobal:
		if ov.OverrideRules && ov.CoalesceConsecutiveSeconds != 0 {
			c.CoalesceSelect.SetSelected(CoalesceSecondsToOption(ov.CoalesceConsecutiveSeconds))
		} else {
			c.CoalesceSelect.SetSelected(globalCoalesceOpt)
		}
	default:
		c.CoalesceSelect.SetSelected(CoalesceSecondsToOption(OptionToCoalesceSeconds(coalMode)))
	}
	c.CoalesceSelect.Refresh()
	c.syncCoalesceSubControl()

	// 2. Speak Emotes
	c.EmotesSelect.Options = boolOptionsForGlobal(g.SpeakEmotes, boolOnSpeakEmotes, boolOffSpeakEmotes)
	c.EmotesSelect.SetSelected(selectedBoolOption(ov.SpeakEmotesMode, ov.OverrideRules, ov.SpeakEmotes, g.SpeakEmotes, boolOnSpeakEmotes, boolOffSpeakEmotes))
	c.EmotesSelect.Refresh()

	// 3. Deduplicate Emotes
	c.DedupeSelect.Options = boolOptionsForGlobal(g.DeduplicateEmotes, boolOnDedupeEmotes, boolOffDedupeEmotes)
	c.DedupeSelect.SetSelected(selectedBoolOption(ov.DeduplicateEmotesMode, ov.OverrideRules, ov.DeduplicateEmotes, g.DeduplicateEmotes, boolOnDedupeEmotes, boolOffDedupeEmotes))
	c.DedupeSelect.Refresh()

	// 4. Max Consecutive Duplicate Words
	globalDupOpt := fmt.Sprintf("Global (%s)", DuplicateWordsToOption(g.MaxDuplicateWords))
	dupOpts := make([]string, 0, 1+len(DuplicateWordsOptions))
	dupOpts = append(dupOpts, globalDupOpt)
	dupOpts = append(dupOpts, DuplicateWordsOptions...)
	c.DupWordsSelect.Options = dupOpts

	dupMode := config.NormalizeDuplicateWordsOverrideMode(ov.MaxDuplicateWordsMode)
	switch dupMode {
	case config.OverrideModeOff:
		c.DupWordsSelect.SetSelected("Off (Unlimited)")
	case config.OverrideModeGlobal:
		if ov.OverrideRules {
			c.DupWordsSelect.SetSelected(DuplicateWordsToOption(ov.MaxDuplicateWords))
		} else {
			c.DupWordsSelect.SetSelected(globalDupOpt)
		}
	default:
		c.DupWordsSelect.SetSelected(DuplicateWordsToOption(OptionToDuplicateWords(dupMode)))
	}
	c.DupWordsSelect.Refresh()

	// 5. Speak Platform Name Prefix
	c.PlatformNameSelect.Options = boolOptionsForGlobal(g.SpeakPlatformName, boolOnPlatformPrefix, boolOffPlatformPrefix)
	c.PlatformNameSelect.SetSelected(selectedBoolOption(ov.SpeakPlatformNameMode, ov.OverrideRules, ov.SpeakPlatformName, g.SpeakPlatformName, boolOnPlatformPrefix, boolOffPlatformPrefix))
	c.PlatformNameSelect.Refresh()

	// 5b. Translate Foreign Chat
	c.TranslateSelect.Options = boolOptionsForGlobal(cfg.Translation.Enabled, boolOnTranslateChat, boolOffTranslateChat)
	c.TranslateSelect.SetSelected(selectedBoolOption(ov.TranslateMode, false, false, cfg.Translation.Enabled, boolOnTranslateChat, boolOffTranslateChat))
	c.TranslateSelect.Refresh()

	// 6. @ Mentions & Replies
	globalMentionsOpt := fmt.Sprintf("Global (%s)", IgnoreMentionsModeToOption(g.IgnoreMentionsMode))
	mentionOpts := make([]string, 0, 1+len(IgnoreMentionsOptions))
	mentionOpts = append(mentionOpts, globalMentionsOpt)
	mentionOpts = append(mentionOpts, IgnoreMentionsOptions...)
	c.MentionsModeSelect.Options = mentionOpts

	mMode := config.NormalizeIgnoreMentionsOverrideMode(ov.IgnoreMentionsOverride)
	switch mMode {
	case config.IgnoreMentionsOff, config.IgnoreMentionsRepliesAndLeading, config.IgnoreMentionsAll:
		c.MentionsModeSelect.SetSelected(IgnoreMentionsModeToOption(mMode))
	default:
		if ov.OverrideRules {
			c.MentionsModeSelect.SetSelected(IgnoreMentionsModeToOption(ov.IgnoreMentionsMode))
		} else {
			c.MentionsModeSelect.SetSelected(globalMentionsOpt)
		}
	}
	c.MentionsModeSelect.Refresh()

	// 7. Allow Broadcaster Mentions
	c.AllowBroadcasterSelect.Options = boolOptionsForGlobal(g.AllowBroadcasterMentions, boolOnAllowBcaster, boolOffAllowBcaster)
	c.AllowBroadcasterSelect.SetSelected(selectedBoolOption(ov.AllowBroadcasterMentionsMode, ov.OverrideRules, ov.AllowBroadcasterMentions, g.AllowBroadcasterMentions, boolOnAllowBcaster, boolOffAllowBcaster))
	c.AllowBroadcasterSelect.Refresh()

	// 8. Play Alert Sound on Ignored Mention
	c.PlayAlertIgnoredMentionSelect.Options = boolOptionsForGlobal(g.PlayAlertOnIgnoredMention, boolOnAlertMention, boolOffAlertMention)
	c.PlayAlertIgnoredMentionSelect.SetSelected(selectedBoolOption(ov.PlayAlertOnIgnoredMentionMode, ov.OverrideRules, ov.PlayAlertOnIgnoredMention, g.PlayAlertOnIgnoredMention, boolOnAlertMention, boolOffAlertMention))
	c.PlayAlertIgnoredMentionSelect.Refresh()

	c.syncMentionSubControls()

	// 9. Default Voice & Speed
	globalVoice := resolveInitialVoiceID(c.session, g.DefaultVoice)
	globalSpeed := g.DefaultSpeed
	if globalSpeed <= 0 {
		globalSpeed = 1.0
	}
	globalVoiceOpt := fmt.Sprintf("Global (%s)", FormatVoiceSpeedSummary(globalVoice, globalSpeed))

	customVoice := strings.TrimSpace(ov.DefaultVoice)
	if customVoice == "" {
		customVoice = globalVoice
	} else {
		customVoice = resolveInitialVoiceID(c.session, customVoice)
	}
	customSpeed := ov.DefaultSpeed
	if customSpeed <= 0 {
		customSpeed = globalSpeed
	}
	customVoiceOpt := fmt.Sprintf("Custom (%s)", FormatVoiceSpeedSummary(customVoice, customSpeed))

	c.VoiceSelect.Options = []string{globalVoiceOpt, customVoiceOpt}
	if config.NormalizeVoiceOverrideMode(ov.VoiceOverrideMode) == config.OverrideModeCustom || (ov.OverrideRules && strings.TrimSpace(ov.DefaultVoice) != "") {
		c.VoiceSelect.SetSelected(customVoiceOpt)
	} else {
		c.VoiceSelect.SetSelected(globalVoiceOpt)
	}
	c.VoiceSelect.Refresh()
}
