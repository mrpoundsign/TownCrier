package tts

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// VoiceUsage describes a single place in the user's configuration that references a voice from a specific model family.
type VoiceUsage struct {
	Location   string // Human-readable location (e.g. "Global Default Voice", "Viewer @alice (twitch)", "Twitch Alert (sub)")
	VoiceID    string // Raw voice ID (e.g. "af_heart", "supertonic:f1")
	VoiceLabel string // Display label (e.g. "af_heart — Heart (American Female, Warm) [Kokoro]")
	FamilyID   string // Model family ID ("kokoro", "supertonic", etc.)
	FamilyName string // Short model family name ("Kokoro-82M", "Supertonic INT8", etc.)
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
	addIfMatches := func(location string, voiceID string) {
		vid := strings.TrimSpace(voiceID)
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
		usages = append(usages, VoiceUsage{
			Location:   location,
			VoiceID:    vid,
			VoiceLabel: VoiceDisplayName(vid),
			FamilyID:   fam,
			FamilyName: famName,
		})
	}

	// 1. Global Default Voice
	addIfMatches("Global Default Voice (Voices Tab)", cfg.GlobalChatRules.DefaultVoice)

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
		addIfMatches(fmt.Sprintf("Chatter Voice: %s (Viewers Tab)", displayUser), v.Voice)
	}

	// 3. Platform Overrides & Alert Notifications
	platKeys := make([]string, 0, len(cfg.Platforms))
	for p := range cfg.Platforms {
		platKeys = append(platKeys, p)
	}
	slices.Sort(platKeys)
	for _, pKey := range platKeys {
		pCfg := cfg.Platforms[pKey]
		platTitle := strings.ToUpper(pKey[:1]) + pKey[1:]
		if pCfg.Overrides.OverrideRules && strings.TrimSpace(pCfg.Overrides.DefaultVoice) != "" {
			addIfMatches(platTitle+" Platform Default Voice Override", pCfg.Overrides.DefaultVoice)
		}

		notifKeys := make([]string, 0, len(pCfg.Notifications))
		for nKey := range pCfg.Notifications {
			notifKeys = append(notifKeys, nKey)
		}
		slices.Sort(notifKeys)
		for _, nKey := range notifKeys {
			nCfg := pCfg.Notifications[nKey]
			if nCfg.Enabled && strings.TrimSpace(nCfg.Voice) != "" {
				addIfMatches(fmt.Sprintf("%s Alert (%s)", platTitle, nKey), nCfg.Voice)
			}
		}
	}

	return usages
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
