package chat

import (
	"strings"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

// EvaluateReadRules determines whether an event passes platform ReadRules (PRD D18).
// A message is spoken if it passes the role gate (when set) AND at least one enabled mode matches.
// Returns (shouldSpeak, modifiedText). If a prefix was matched, modifiedText contains the text with
// the prefix removed. Otherwise, modifiedText is empty.
func EvaluateReadRules(event *Event, rules config.ReadRules) (bool, string) {
	if event == nil {
		return false, ""
	}

	// 1. Role Gate check (D18)
	if rules.RoleGated {
		matchedRole := false
		for _, r := range event.Roles {
			for _, allowed := range rules.Roles {
				if strings.EqualFold(r, allowed) {
					matchedRole = true
					break
				}
			}
			if matchedRole {
				break
			}
		}
		if !matchedRole {
			return false, ""
		}
	}

	// 2. Stream notification and paid events (subs, cheers, raids, etc.)
	if event.IsPaidOrEvent() {
		if rules.PaidOnly || rules.AllChat || event.Kind != EventKindMessage {
			return true, ""
		}
	}

	// Standard chat message mode evaluation
	// If only PaidOnly is enabled, normal messages don't match
	if rules.PaidOnly && !rules.AllChat && !rules.PrefixOnly {
		return false, ""
	}

	// Mode: PrefixOnly
	if rules.PrefixOnly {
		prefix := strings.TrimSpace(rules.Prefix)
		if prefix == "" {
			prefix = "!tts"
		}
		trimmedText := strings.TrimSpace(event.Text)
		if strings.HasPrefix(strings.ToLower(trimmedText), strings.ToLower(prefix)) {
			remaining := strings.TrimSpace(trimmedText[len(prefix):])
			return true, remaining
		}
		if !rules.AllChat {
			return false, ""
		}
	}

	// Mode: AllChat
	if rules.AllChat {
		if rules.IgnorePrefixes {
			prefixes := rules.IgnoredPrefixes
			if len(prefixes) == 0 {
				prefixes = []string{"!"}
			}
			trimmedText := strings.TrimSpace(event.Text)
			lowerText := strings.ToLower(trimmedText)
			for _, p := range prefixes {
				p = strings.TrimSpace(strings.ToLower(p))
				if p != "" && strings.HasPrefix(lowerText, p) {
					return false, ""
				}
			}
		}
		return true, ""
	}

	return false, ""
}
