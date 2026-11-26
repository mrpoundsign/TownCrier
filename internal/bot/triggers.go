package bot

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

type cooldownTracker struct {
	mu      sync.Mutex
	global  map[string]time.Time
	perUser map[string]time.Time
}

func newCooldownTracker() *cooldownTracker {
	return &cooldownTracker{
		global:  make(map[string]time.Time),
		perUser: make(map[string]time.Time),
	}
}

func (ct *cooldownTracker) allowAndStamp(key, userKey string, globalSec, userSec int, now time.Time) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if globalSec > 0 {
		if last, ok := ct.global[key]; ok && now.Sub(last) < time.Duration(globalSec)*time.Second {
			return false
		}
	}
	if userSec > 0 && userKey != "" {
		uk := key + "|" + userKey
		if last, ok := ct.perUser[uk]; ok && now.Sub(last) < time.Duration(userSec)*time.Second {
			return false
		}
	}

	if globalSec > 0 {
		ct.global[key] = now
	}
	if userSec > 0 && userKey != "" {
		ct.perUser[key+"|"+userKey] = now
	}
	return true
}

func matchCommandPattern(cmd config.BotCommandDef, text string) (matched bool, rawInput string, positional []string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false, "", nil
	}

	mode := strings.ToLower(strings.TrimSpace(cmd.MatchMode))
	if mode == "" {
		mode = "prefix"
	}

	candidates := make([]string, 0, 1+len(cmd.Aliases))
	if strings.TrimSpace(cmd.Trigger) != "" {
		candidates = append(candidates, strings.TrimSpace(cmd.Trigger))
	}
	for _, a := range cmd.Aliases {
		if ca := strings.TrimSpace(a); ca != "" {
			candidates = append(candidates, ca)
		}
	}
	if len(candidates) == 0 {
		return false, "", nil
	}

	for _, pat := range candidates {
		switch mode {
		case "regex":
			expr := pat
			if !cmd.CaseSensitive && !strings.HasPrefix(expr, "(?i)") {
				expr = "(?i)" + expr
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				continue
			}
			locs := re.FindStringSubmatch(trimmed)
			if locs != nil {
				var rest string
				if len(locs) > 1 {
					rest = strings.TrimSpace(locs[1])
				} else {
					locIdx := re.FindStringIndex(trimmed)
					if locIdx != nil {
						rest = strings.TrimSpace(trimmed[locIdx[1]:])
					}
				}
				return true, rest, strings.Fields(rest)
			}

		case "exact":
			ok := trimmed == pat
			if !cmd.CaseSensitive {
				ok = strings.EqualFold(trimmed, pat)
			}
			if ok {
				return true, "", nil
			}

		default: // "prefix": exact token match followed by optional arguments
			fields := strings.Fields(trimmed)
			if len(fields) == 0 {
				continue
			}
			head := fields[0]
			ok := head == pat
			if !cmd.CaseSensitive {
				ok = strings.EqualFold(head, pat)
			}
			if ok {
				rest := strings.TrimSpace(trimmed[len(head):])
				return true, rest, fields[1:]
			}
		}
	}
	return false, "", nil
}

func userAuthorizedForCommand(cmd config.BotCommandDef, ev *chat.Event, store *Store) bool {
	if len(cmd.Platforms) > 0 {
		foundPlat := false
		for _, p := range cmd.Platforms {
			if strings.EqualFold(strings.TrimSpace(p), ev.Platform) {
				foundPlat = true
				break
			}
		}
		if !foundPlat {
			return false
		}
	}

	if len(cmd.AllowedRoles) == 0 && len(cmd.AllowedGroups) == 0 {
		return true
	}

	if len(cmd.AllowedRoles) > 0 {
		for _, r := range cmd.AllowedRoles {
			cr := strings.ToLower(strings.TrimSpace(r))
			if cr == "everyone" {
				return true
			}
			for _, userRole := range ev.Roles {
				ur := strings.ToLower(strings.TrimSpace(userRole))
				if ur == cr || (cr == "mod" && ur == "moderator") || (cr == "moderator" && ur == "mod") {
					return true
				}
			}
		}
	}

	if len(cmd.AllowedGroups) > 0 && store != nil {
		uid := strings.TrimSpace(ev.UserID)
		if uid == "" {
			uid = strings.TrimSpace(ev.UserName)
		}
		for _, g := range cmd.AllowedGroups {
			if store.IsUserInGroup(g, ev.Platform, uid) {
				return true
			}
		}
	}

	return false
}

func matchVoicePhrase(vc config.BotVoiceCommandDef, transcript string) (bool, string) {
	trimmed := strings.TrimSpace(transcript)
	phrase := strings.TrimSpace(vc.Phrase)
	if trimmed == "" || phrase == "" {
		return false, ""
	}
	mode := strings.ToLower(strings.TrimSpace(vc.MatchMode))
	switch mode {
	case "regex":
		expr := phrase
		if !strings.HasPrefix(expr, "(?i)") {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return false, ""
		}
		if re.MatchString(trimmed) {
			return true, trimmed
		}
	case "exact":
		normTranscript := NormalizeVoiceText(trimmed)
		normPhrase := NormalizeVoiceText(phrase)
		if normPhrase == "" {
			if strings.EqualFold(trimmed, phrase) {
				return true, trimmed
			}
			return false, ""
		}
		if normTranscript == normPhrase {
			return true, trimmed
		}
	case "phonetic":
		spoken := voiceTokens(trimmed)
		target := voiceTokens(phrase)
		if len(target) == 0 {
			if strings.Contains(strings.ToLower(trimmed), strings.ToLower(phrase)) {
				return true, trimmed
			}
			return false, ""
		}
		if ok, _ := matchTokensPhonetic(spoken, target); ok {
			return true, trimmed
		}
	default: // "contains"
		spoken := voiceTokens(trimmed)
		target := voiceTokens(phrase)
		if len(target) == 0 {
			if strings.Contains(strings.ToLower(trimmed), strings.ToLower(phrase)) {
				return true, trimmed
			}
			return false, ""
		}
		if ok, _ := matchTokensContains(spoken, target); ok {
			return true, trimmed
		}
	}
	return false, ""
}

func matchPlatformTrigger(tr config.BotTriggerDef, ev *chat.Event) bool {
	if !tr.Enabled || !strings.EqualFold(strings.TrimSpace(tr.Source), "platform_event") {
		return false
	}
	if p := strings.TrimSpace(tr.Platform); p != "" && !strings.EqualFold(p, ev.Platform) {
		return false
	}
	if k := strings.TrimSpace(tr.EventKind); k != "" && !strings.EqualFold(k, string(ev.Kind)) {
		return false
	}
	if tr.MinAmount > 0 {
		val := ev.Amount
		if ev.Kind == chat.EventKindRaid || ev.Kind == chat.EventKindGiftSub || ev.Kind == chat.EventKindWatchStreak || (ev.Kind == chat.EventKindGift && val == 0) {
			val = float64(ev.Count)
		}
		if val < tr.MinAmount {
			return false
		}
	}
	if rf := strings.TrimSpace(tr.RewardFilter); rf != "" && ev.Kind == chat.EventKindRedemption {
		if !strings.EqualFold(rf, strings.TrimSpace(ev.RewardTitle)) && !strings.EqualFold(rf, strings.TrimSpace(ev.RewardID)) {
			return false
		}
	}
	return true
}

func buildEventArgs(ev *chat.Event, rawInput string, positional []string) map[string]string {
	user := strings.TrimSpace(ev.UserName)
	disp := strings.TrimSpace(ev.DisplayName)
	if disp == "" {
		disp = user
	}
	if user == "" {
		user = disp
	}
	targetUser := ""
	if len(positional) > 0 {
		targetUser = strings.TrimPrefix(strings.TrimSpace(positional[0]), "@")
	} else if ev.Recipient != "" {
		targetUser = strings.TrimPrefix(strings.TrimSpace(ev.Recipient), "@")
	}

	amountStr := ""
	switch {
	case ev.Amount > 0:
		amountStr = strconv.FormatFloat(ev.Amount, 'f', -1, 64)
	case ev.Count > 0:
		amountStr = strconv.Itoa(ev.Count)
	case ev.Months > 0:
		amountStr = strconv.Itoa(ev.Months)
	}

	args := map[string]string{
		"user":        user,
		"userId":      ev.UserID,
		"displayName": disp,
		"platform":    ev.Platform,
		"channel":     ev.Channel,
		"kind":        string(ev.Kind),
		"message":     ev.Text,
		"messageId":   ev.ID,
		"rawInput":    rawInput,
		"targetUser":  targetUser,
		"amount":      amountStr,
		"currency":    ev.Currency,
		"reward":      ev.RewardTitle,
		"rewardId":    ev.RewardID,
		"roles":       strings.Join(slices.Clone(ev.Roles), ","),
	}
	for i, p := range positional {
		args[fmt.Sprintf("arg%d", i)] = p
	}
	return args
}
