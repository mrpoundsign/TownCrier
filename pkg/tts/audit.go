package tts

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// VoiceUsageKind identifies the configuration target type for a VoiceUsage entry.
const (
	UsageKindGlobal           = "global"
	UsageKindViewer           = "viewer"
	UsageKindPlatformOverride = "platform_override"
	UsageKindPlatformAlert    = "platform_alert"
)

// VoiceUsage describes a single place in the user's configuration that references a voice from a specific model family.
type VoiceUsage struct {
	Location   string  // Human-readable location (e.g. "Global Default Voice (Settings)", "Chatter Voice: alice (Viewers Tab)", "Twitch Alert (sub)")
	VoiceID    string  // Raw voice ID (e.g. "af_heart", "supertonic:f1")
	VoiceLabel string  // Display label (e.g. "Heart — US Female (af_heart)")
	FamilyID   string  // Model family ID ("kokoro", "supertonic", etc.)
	FamilyName string  // Short model family name ("Kokoro-82M", "Supertonic INT8", etc.)
	Kind       string  // Target kind: UsageKindGlobal, UsageKindViewer, UsageKindPlatformOverride, or UsageKindPlatformAlert
	ViewerKey  string  // Key in cfg.Viewers (when Kind == UsageKindViewer)
	Platform   string  // Platform key in cfg.Platforms (when Kind == UsageKindPlatformOverride or UsageKindPlatformAlert)
	AlertKey   string  // Notification key in pCfg.Notifications (when Kind == UsageKindPlatformAlert)
	Speed      float32 // Current configured speed multiplier for this item (defaults to 1.0)
}

// VoiceReplacement specifies a replacement voice (and optional speed) for a single VoiceUsage.
// When VoiceID is empty ("") on a non-global usage, the override is cleared so it inherits the default voice.
type VoiceReplacement struct {
	VoiceID string
	Speed   float32
}

// FindVoiceUsages returns all configuration assignments that use a voice belonging to any of the targetFamilies.
func FindVoiceUsages(cfg *config.Config, targetFamilies []string) []VoiceUsage {
	if cfg == nil || len(targetFamilies) == 0 {
		return nil
	}
	targetSet := make(map[string]bool, len(targetFamilies))
	for _, f := range targetFamilies {
		clean := strings.ToLower(strings.TrimSpace(f))
		if clean != "" {
			targetSet[clean] = true
		}
	}
	if len(targetSet) == 0 {
		return nil
	}

	var usages []VoiceUsage
	addIfMatches := func(u VoiceUsage) {
		vid := strings.TrimSpace(u.VoiceID)
		if vid == "" {
			return
		}
		fam := VoiceFamily(vid)
		if !targetSet[fam] {
			return
		}
		famName := fam
		if spec, ok := ModelSpecByID(fam); ok {
			famName = spec.ShortName
		}
		if u.Speed <= 0 {
			u.Speed = 1.0
		}
		u.VoiceID = VoiceIDFromLabel(vid)
		u.VoiceLabel = VoiceDisplayName(vid)
		u.FamilyID = fam
		u.FamilyName = famName
		usages = append(usages, u)
	}

	// 1. Global Default Voice
	globalSpeed := cfg.GlobalChatRules.DefaultSpeed
	if globalSpeed <= 0 {
		globalSpeed = 1.0
	}
	addIfMatches(VoiceUsage{
		Location: "Global Default Voice (Settings)",
		VoiceID:  cfg.GlobalChatRules.DefaultVoice,
		Kind:     UsageKindGlobal,
		Speed:    globalSpeed,
	})

	// 2. Custom Viewer Overrides (sorted for deterministic display)
	viewerKeys := make([]string, 0, len(cfg.Viewers))
	for k := range cfg.Viewers {
		viewerKeys = append(viewerKeys, k)
	}
	slices.Sort(viewerKeys)
	for _, k := range viewerKeys {
		v := cfg.Viewers[k]
		if v.Ignored || strings.TrimSpace(v.Voice) == "" {
			continue
		}
		displayUser := strings.TrimSpace(v.UserID)
		if displayUser == "" {
			displayUser = k
		}
		if v.Nickname != "" {
			displayUser = fmt.Sprintf("%s (%s)", displayUser, v.Nickname)
		}
		spd := v.Speed
		if spd <= 0 {
			spd = globalSpeed
		}
		addIfMatches(VoiceUsage{
			Location:  fmt.Sprintf("Chatter Voice: %s (Viewers Tab)", displayUser),
			VoiceID:   v.Voice,
			Kind:      UsageKindViewer,
			ViewerKey: k,
			Speed:     spd,
		})
	}

	// 3. Platform Overrides & Alert Notifications
	platKeys := make([]string, 0, len(cfg.Platforms))
	for p := range cfg.Platforms {
		platKeys = append(platKeys, p)
	}
	slices.Sort(platKeys)
	for _, pKey := range platKeys {
		pCfg := cfg.Platforms[pKey]
		platTitle := pKey
		if len(pKey) > 0 {
			platTitle = strings.ToUpper(pKey[:1]) + pKey[1:]
		}
		isCustomVoiceOverride := config.NormalizeVoiceOverrideMode(pCfg.Overrides.VoiceOverrideMode) == config.OverrideModeCustom || pCfg.Overrides.OverrideRules
		if isCustomVoiceOverride && strings.TrimSpace(pCfg.Overrides.DefaultVoice) != "" {
			spd := pCfg.Overrides.DefaultSpeed
			if spd <= 0 {
				spd = globalSpeed
			}
			addIfMatches(VoiceUsage{
				Location: platTitle + " Platform Default Voice Override",
				VoiceID:  pCfg.Overrides.DefaultVoice,
				Kind:     UsageKindPlatformOverride,
				Platform: pKey,
				Speed:    spd,
			})
		}

		notifKeys := make([]string, 0, len(pCfg.Notifications))
		for nKey := range pCfg.Notifications {
			notifKeys = append(notifKeys, nKey)
		}
		slices.Sort(notifKeys)
		for _, nKey := range notifKeys {
			nCfg := pCfg.Notifications[nKey]
			if nCfg.Enabled && strings.TrimSpace(nCfg.Voice) != "" {
				addIfMatches(VoiceUsage{
					Location: fmt.Sprintf("%s Alert (%s)", platTitle, nKey),
					VoiceID:  nCfg.Voice,
					Kind:     UsageKindPlatformAlert,
					Platform: pKey,
					AlertKey: nKey,
					Speed:    globalSpeed,
				})
			}
		}
	}

	return usages
}

// DefaultReplacementVoice returns the best default voice ID from remainingModels.
// If the user's current Global Default Voice belongs to one of remainingModels, it is returned;
// otherwise the first catalog voice of remainingModels is returned.
func DefaultReplacementVoice(cfg *config.Config, remainingModels []string) string {
	remSet := make(map[string]bool, len(remainingModels))
	for _, m := range remainingModels {
		remSet[strings.ToLower(strings.TrimSpace(m))] = true
	}
	if cfg != nil {
		rawGlobal := strings.TrimSpace(cfg.GlobalChatRules.DefaultVoice)
		if rawGlobal != "" {
			globalVid := VoiceIDFromLabel(rawGlobal)
			if globalVid != "" && remSet[VoiceFamily(globalVid)] {
				return globalVid
			}
		}
	}
	if labels := VoiceDisplayLabelsForModels(remainingModels); len(labels) > 0 {
		return VoiceIDFromLabel(labels[0])
	}
	return "af_heart"
}

// ApplyVoiceUsageReplacements mutates cfg in-place to apply the chosen replacement voices for each usage.
func ApplyVoiceUsageReplacements(cfg *config.Config, usages []VoiceUsage, replacements []VoiceReplacement) {
	if cfg == nil {
		return
	}
	for i, u := range usages {
		if i >= len(replacements) {
			break
		}
		rep := replacements[i]
		vid := strings.TrimSpace(rep.VoiceID)
		if vid != "" {
			vid = VoiceIDFromLabel(vid)
		}
		spd := rep.Speed

		switch u.Kind {
		case UsageKindGlobal:
			if vid != "" {
				cfg.GlobalChatRules.DefaultVoice = vid
			}
			if spd > 0 {
				cfg.GlobalChatRules.DefaultSpeed = spd
			}
		case UsageKindViewer:
			if cfg.Viewers == nil {
				continue
			}
			vc, ok := cfg.Viewers[u.ViewerKey]
			if !ok {
				continue
			}
			vc.Voice = vid
			if spd > 0 {
				vc.Speed = spd
			}
			if vid == "" && strings.TrimSpace(vc.Nickname) == "" && !vc.Ignored && !vc.Allowed {
				delete(cfg.Viewers, u.ViewerKey)
			} else {
				cfg.Viewers[u.ViewerKey] = vc
			}
		case UsageKindPlatformOverride:
			if cfg.Platforms == nil {
				continue
			}
			pCfg, ok := cfg.Platforms[u.Platform]
			if !ok {
				continue
			}
			if vid == "" {
				pCfg.Overrides.VoiceOverrideMode = config.OverrideModeGlobal
				pCfg.Overrides.DefaultVoice = ""
				pCfg.Overrides.OverrideRules = false
			} else {
				pCfg.Overrides.VoiceOverrideMode = config.OverrideModeCustom
				pCfg.Overrides.DefaultVoice = vid
				if spd > 0 {
					pCfg.Overrides.DefaultSpeed = spd
				}
			}
			cfg.Platforms[u.Platform] = pCfg
		case UsageKindPlatformAlert:
			if cfg.Platforms == nil {
				continue
			}
			pCfg, ok := cfg.Platforms[u.Platform]
			if !ok || pCfg.Notifications == nil {
				continue
			}
			nCfg, nOk := pCfg.Notifications[u.AlertKey]
			if !nOk {
				continue
			}
			nCfg.Voice = vid
			pCfg.Notifications[u.AlertKey] = nCfg
			cfg.Platforms[u.Platform] = pCfg
		}
	}
}

// DisabledModelFamilies returns the model families that are present in currentEnabled but absent in nextEnabled.
func DisabledModelFamilies(currentEnabled, nextEnabled []string) []string {
	nextSet := make(map[string]bool, len(nextEnabled))
	for _, m := range nextEnabled {
		nextSet[strings.ToLower(strings.TrimSpace(m))] = true
	}
	var removed []string
	for _, m := range currentEnabled {
		clean := strings.ToLower(strings.TrimSpace(m))
		if clean != "" && !nextSet[clean] {
			removed = append(removed, clean)
		}
	}
	return removed
}

// FormatVoiceUsagesWarning formats a human-friendly blocking explanation listing all assigned voices preventing a model pack from being disabled.
func FormatVoiceUsagesWarning(usages []VoiceUsage) string {
	if len(usages) == 0 {
		return ""
	}
	familyNames := make([]string, 0, 2)
	seenFam := make(map[string]bool)
	for _, u := range usages {
		if !seenFam[u.FamilyName] {
			seenFam[u.FamilyName] = true
			familyNames = append(familyNames, u.FamilyName)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Cannot disable %s because %d assigned voice(s) currently use it:\n\n",
		strings.Join(familyNames, ", "), len(usages))

	const maxShown = 8
	for i, u := range usages {
		if i >= maxShown {
			fmt.Fprintf(&b, "• ...and %d more assigned voice(s)\n", len(usages)-maxShown)
			break
		}
		fmt.Fprintf(&b, "• %s → %s\n", u.Location, u.VoiceLabel)
	}
	b.WriteString("\nTo disable this voice pack, keep your desired replacement pack enabled, reassign the voice(s) listed above in the Voices or Viewers tab, and then disable this pack.")
	return b.String()
}
