package chat

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// EvaluateReadRules determines whether an event passes platform ReadRules (PRD D18).
// A message is spoken if it passes the role gate (when set) AND at least one enabled mode matches.
// Returns (shouldSpeak, modifiedText). If a prefix was matched, modifiedText contains the text with
// the prefix removed. Otherwise, modifiedText is empty.
func EvaluateReadRules(event *Event, rules config.ReadRules) (bool, string) {
	if event == nil {
		return false, ""
	}

	// Stream notification events (follows, subs, cheers, raids, redemptions, gifts)
	// are governed by their dedicated toggles in Notification Events rather than chat ReadRules.
	if event.Kind != "" && event.Kind != EventKindMessage {
		return true, ""
	}

	// 1. Role Gate check (D18) for chat messages
	if rules.RoleGated {
		matchedRole := false
		minLevel := max(1, rules.MinFanClubLevel)
		for _, allowed := range rules.Roles {
			if strings.EqualFold(allowed, "fan_club") {
				if event.FanClubLevel >= minLevel || (minLevel == 1 && hasRole(event.Roles, "fan_club")) {
					matchedRole = true
					break
				}
				continue
			}
			if hasRole(event.Roles, allowed) {
				matchedRole = true
				break
			}
		}
		if !matchedRole {
			return false, ""
		}
	}

	// 2. Paid chat messages
	if event.IsPaidOrEvent() {
		if rules.PaidOnly || rules.AllChat {
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

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, target) {
			return true
		}
	}
	return false
}

func isMentionRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

func isOpeningPunct(r rune) bool {
	switch r {
	case '(', '[', '{', '"', '\'', '<', '“', '‘':
		return true
	default:
		return false
	}
}

// ExtractMentions scans text for @username tokens (where '@' is at the start of text or preceded by
// whitespace/opening punctuation) and returns deduplicated normalized usernames without the leading '@'.
// Email addresses like "user@example.com" or bare "@" symbols are ignored.
func ExtractMentions(text string) []string {
	runes := []rune(text)
	n := len(runes)
	var out []string
	seen := make(map[string]struct{})

	for i := 0; i < n; i++ {
		if runes[i] != '@' {
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			if !unicode.IsSpace(prev) && !isOpeningPunct(prev) {
				continue
			}
		}
		start := i + 1
		end := start
		for end < n && isMentionRune(runes[end]) {
			end++
		}
		if end == start {
			continue
		}
		// If immediately followed by '@', it is part of an email/malformed token
		if end < n && runes[end] == '@' {
			i = end
			continue
		}
		// Trim trailing sentence punctuation like '.' or '-'
		token := strings.TrimRight(string(runes[start:end]), ".-")
		if token == "" {
			continue
		}
		lower := strings.ToLower(token)
		if _, exists := seen[lower]; !exists {
			seen[lower] = struct{}{}
			out = append(out, token)
		}
		i = end - 1
	}
	return out
}

// ExtractLeadingMention checks whether text begins with an @username mention (optionally after leading
// whitespace or opening punctuation) and returns the username (without '@') and true if found.
func ExtractLeadingMention(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	runes := []rune(trimmed)
	idx := 0
	for idx < len(runes) && isOpeningPunct(runes[idx]) {
		idx++
	}
	if idx >= len(runes) || runes[idx] != '@' {
		return "", false
	}
	start := idx + 1
	end := start
	for end < len(runes) && isMentionRune(runes[end]) {
		end++
	}
	if end == start {
		return "", false
	}
	if end < len(runes) && runes[end] == '@' {
		return "", false
	}
	token := strings.TrimRight(string(runes[start:end]), ".-")
	if token == "" {
		return "", false
	}
	return token, true
}

// NormalizeBroadcasterHandle extracts the canonical lowercase username/handle from a platform account setting,
// stripping URL schemes/hosts, "/live" paths, Kick "#chatroom_id" suffixes, and leading "@".
func NormalizeBroadcasterHandle(accountName string) string {
	s := strings.TrimSpace(accountName)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Path != "" {
			s = u.Path
		}
	} else {
		lower := strings.ToLower(s)
		for _, dom := range []string{"twitch.tv/", "www.twitch.tv/", "youtube.com/", "www.youtube.com/", "kick.com/", "www.kick.com/", "tiktok.com/", "www.tiktok.com/"} {
			if idx := strings.Index(lower, dom); idx >= 0 {
				s = s[idx+len(dom):]
				break
			}
		}
	}
	if hashIdx := strings.IndexByte(s, '#'); hashIdx >= 0 {
		s = s[:hashIdx]
	}
	if qIdx := strings.IndexByte(s, '?'); qIdx >= 0 {
		s = s[:qIdx]
	}
	s = strings.Trim(s, "/")
	if strings.HasSuffix(strings.ToLower(s), "/live") {
		s = s[:len(s)-5]
	}
	lowerPath := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lowerPath, "c/"):
		s = s[2:]
	case strings.HasPrefix(lowerPath, "channel/"):
		s = s[8:]
	case strings.HasPrefix(lowerPath, "user/"):
		s = s[5:]
	}
	if slashIdx := strings.IndexByte(s, '/'); slashIdx >= 0 {
		s = s[:slashIdx]
	}
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	return strings.ToLower(strings.TrimSpace(s))
}

// ShouldIgnoreMentionOrReply reports whether a chat message should be skipped based on the effective
// IgnoreMentionsMode ("off", "replies_and_leading", "all") and AllowBroadcasterMentions setting.
// Stream notification events and paid chat messages are never ignored by this rule.
func ShouldIgnoreMentionOrReply(event *Event, mode string, allowBroadcaster bool, broadcasterAccount string) bool {
	if event == nil {
		return false
	}
	if event.Kind != "" && event.Kind != EventKindMessage {
		return false
	}
	if event.IsPaidOrEvent() {
		return false
	}

	normMode := config.NormalizeIgnoreMentionsMode(mode)
	if normMode == config.IgnoreMentionsOff {
		return false
	}

	broadcaster := NormalizeBroadcasterHandle(broadcasterAccount)
	mentions := event.Mentions
	if len(mentions) == 0 {
		mentions = ExtractMentions(event.Text)
	}
	leadingUser, hasLeading := ExtractLeadingMention(event.Text)
	replyUser := NormalizeBroadcasterHandle(event.ReplyToUser)

	switch normMode {
	case config.IgnoreMentionsRepliesAndLeading:
		if !event.IsReply && !hasLeading {
			return false
		}
		if allowBroadcaster && broadcaster != "" {
			if event.IsReply {
				if replyUser != "" {
					if strings.EqualFold(replyUser, broadcaster) && (!hasLeading || strings.EqualFold(leadingUser, broadcaster)) {
						return false
					}
					return true
				}
				if hasLeading && strings.EqualFold(leadingUser, broadcaster) {
					return false
				}
				return true
			}
			if hasLeading && strings.EqualFold(leadingUser, broadcaster) {
				return false
			}
		}
		return true

	case config.IgnoreMentionsAll:
		if !event.IsReply && len(mentions) == 0 && !hasLeading {
			return false
		}
		if allowBroadcaster && broadcaster != "" {
			var targets []string
			if event.IsReply && replyUser != "" {
				targets = append(targets, replyUser)
			}
			if hasLeading {
				targets = append(targets, leadingUser)
			}
			for _, m := range mentions {
				if clean := NormalizeBroadcasterHandle(m); clean != "" {
					targets = append(targets, clean)
				}
			}
			if len(targets) > 0 {
				allBroadcaster := true
				for _, t := range targets {
					if !strings.EqualFold(t, broadcaster) {
						allBroadcaster = false
						break
					}
				}
				if allBroadcaster {
					return false
				}
			}
		}
		return true
	}

	return false
}
