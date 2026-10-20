package youtube

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

var (
	reInnertubeKey  = regexp.MustCompile(`"INNERTUBE_API_KEY"\s*:\s*"([^"]+)"`)
	reContinuation  = regexp.MustCompile(`"liveChatRenderer"\s*:\s*\{.*?"continuations"\s*:\s*\[\s*\{\s*"(?:timedContinuationData|invalidationContinuationData|reloadContinuationData)"\s*:\s*\{[^}]*?"continuation"\s*:\s*"([^"]+)"`)
	reReloadCont    = regexp.MustCompile(`"continuation"\s*:\s*"(0ofMyA[^"]+)"`)
	reAmountNumbers = regexp.MustCompile(`([0-9]+(?:[.,][0-9]+)?)`)
	reMonthsCount   = regexp.MustCompile(`([0-9]+)\s*(?:month|months|mo\b)`)
	reGiftsCount    = regexp.MustCompile(`([0-9]+)`)
)

// NormalizeTarget converts a YouTube channel name/handle, channel ID (UC...),
// or full URL into a canonical live page URL and a clean display label.
func NormalizeTarget(input string) (liveURL string, display string) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", ""
	}

	// Full URL handling
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if strings.Contains(lower, "watch?v=") || strings.Contains(lower, "/live/") {
			return s, s
		}
		trimmed := strings.TrimRight(s, "/")
		if !strings.HasSuffix(strings.ToLower(trimmed), "/live") {
			return trimmed + "/live", s
		}
		return trimmed, s
	}

	if strings.HasPrefix(lower, "youtube.com/") || strings.HasPrefix(lower, "www.youtube.com/") || strings.HasPrefix(lower, "youtu.be/") {
		return NormalizeTarget("https://" + s)
	}

	if strings.HasPrefix(s, "UC") && len(s) >= 20 && !strings.Contains(s, " ") {
		return "https://www.youtube.com/channel/" + s + "/live", s
	}

	clean := strings.TrimLeft(s, "@")
	if clean == "" {
		return "", ""
	}
	return "https://www.youtube.com/@" + clean + "/live", clean
}

// ExtractVideoDetails parses the videoDetails JSON object from YouTube HTML if present,
// returning whether the stream is actively live and whether videoDetails was found.
func ExtractVideoDetails(html string) (isLive bool, found bool) {
	search := html
	for {
		idx := strings.Index(search, `"videoDetails"`)
		if idx == -1 {
			return false, false
		}

		rest := search[idx+len(`"videoDetails"`):]
		braceIdx := strings.IndexByte(rest, '{')
		if braceIdx == -1 {
			search = rest
			continue
		}

		start := braceIdx
		depth := 0
		inString := false
		escape := false
		end := -1

		for i := start; i < len(rest); i++ {
			b := rest[i]
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if b == '{' {
				depth++
			} else if b == '}' {
				depth--
				if depth == 0 {
					end = i + 1
					break
				}
			}
		}

		if end != -1 {
			var details struct {
				IsLive        bool `json:"isLive"`
				IsLiveContent bool `json:"isLiveContent"`
			}
			if err := json.Unmarshal([]byte(rest[start:end]), &details); err == nil {
				return details.IsLive, true
			}
		}

		search = rest
	}
}

// ExtractBootstrapTokens extracts the INNERTUBE_API_KEY and initial liveChat continuation token from a YouTube HTML page.
// It verifies that the target video is actively transmitting live (videoDetails.isLive == true).
func ExtractBootstrapTokens(html string) (apiKey string, continuation string, ok bool) {
	isLive, found := ExtractVideoDetails(html)
	if !found || !isLive {
		return "", "", false
	}

	if m := reInnertubeKey.FindStringSubmatch(html); len(m) > 1 {
		apiKey = m[1]
	}
	if m := reContinuation.FindStringSubmatch(html); len(m) > 1 {
		continuation = m[1]
	} else if m := reReloadCont.FindStringSubmatch(html); len(m) > 1 {
		continuation = m[1]
	}
	return apiKey, continuation, apiKey != "" && continuation != ""
}

// InnerTube request & response models

type getLiveChatRequest struct {
	Context      innerTubeContext `json:"context"`
	Continuation string           `json:"continuation"`
}

type innerTubeContext struct {
	Client innerTubeClient `json:"client"`
}

type innerTubeClient struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	Hl            string `json:"hl"`
	Gl            string `json:"gl"`
}

type getLiveChatResponse struct {
	ContinuationContents *struct {
		LiveChatContinuation *liveChatContinuation `json:"liveChatContinuation"`
	} `json:"continuationContents"`
}

type liveChatContinuation struct {
	Continuations []continuationEntry `json:"continuations"`
	Actions       []json.RawMessage   `json:"actions"`
}

type continuationEntry struct {
	TimedContinuationData *struct {
		Continuation string `json:"continuation"`
		TimeoutMs    int    `json:"timeoutMs"`
	} `json:"timedContinuationData"`
	InvalidationContinuationData *struct {
		Continuation string `json:"continuation"`
		TimeoutMs    int    `json:"timeoutMs"`
	} `json:"invalidationContinuationData"`
	ReloadContinuationData *struct {
		Continuation string `json:"continuation"`
	} `json:"reloadContinuationData"`
}

type textRuns struct {
	SimpleText string    `json:"simpleText"`
	Runs       []textRun `json:"runs"`
}

type textRun struct {
	Text  string `json:"text"`
	Emoji *struct {
		EmojiID       string   `json:"emojiId"`
		Shortcuts     []string `json:"shortcuts"`
		IsCustomEmoji bool     `json:"isCustomEmoji"`
		Image         *struct {
			Thumbnails []struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		} `json:"image"`
	} `json:"emoji"`
}

type authorBadge struct {
	LiveChatAuthorBadgeRenderer *struct {
		CustomThumbnail *json.RawMessage `json:"customThumbnail"`
		Icon            *struct {
			IconType string `json:"iconType"`
		} `json:"icon"`
		Tooltip string `json:"tooltip"`
	} `json:"liveChatAuthorBadgeRenderer"`
}

type chatAction struct {
	AddChatItemAction *struct {
		Item struct {
			LiveChatTextMessageRenderer                          *textMessageRenderer    `json:"liveChatTextMessageRenderer"`
			LiveChatPaidMessageRenderer                          *paidMessageRenderer    `json:"liveChatPaidMessageRenderer"`
			LiveChatPaidStickerRenderer                          *paidStickerRenderer    `json:"liveChatPaidStickerRenderer"`
			LiveChatMembershipItemRenderer                       *membershipItemRenderer `json:"liveChatMembershipItemRenderer"`
			LiveChatSponsorshipsGiftPurchaseAnnouncementRenderer *giftPurchaseRenderer   `json:"liveChatSponsorshipsGiftPurchaseAnnouncementRenderer"`
		} `json:"item"`
	} `json:"addChatItemAction"`
	MarkChatItemAsDeletedAction *struct {
		TargetItemID string `json:"targetItemId"`
	} `json:"markChatItemAsDeletedAction"`
	MarkChatItemsByAuthorAsDeletedAction *struct {
		ExternalChannelID string `json:"externalChannelId"`
	} `json:"markChatItemsByAuthorAsDeletedAction"`
}

type textMessageRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	Message                 textRuns      `json:"message"`
}

type paidMessageRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	PurchaseAmountText      textRuns      `json:"purchaseAmountText"`
	Message                 textRuns      `json:"message"`
}

type paidStickerRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	PurchaseAmountText      textRuns      `json:"purchaseAmountText"`
	Sticker                 *struct {
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
		Accessibility *struct {
			AccessibilityData *struct {
				Label string `json:"label"`
			} `json:"accessibilityData"`
		} `json:"accessibility"`
	} `json:"sticker"`
}

type membershipItemRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	HeaderPrimaryText       textRuns      `json:"headerPrimaryText"`
	HeaderSubtext           textRuns      `json:"headerSubtext"`
	Message                 textRuns      `json:"message"`
}

type giftPurchaseRenderer struct {
	ID                      string `json:"id"`
	TimestampUsec           string `json:"timestampUsec"`
	AuthorExternalChannelID string `json:"authorExternalChannelId"`
	Header                  *struct {
		LiveChatSponsorshipsHeaderRenderer *struct {
			AuthorName   textRuns      `json:"authorName"`
			PrimaryText  textRuns      `json:"primaryText"`
			AuthorBadges []authorBadge `json:"authorBadges"`
		} `json:"liveChatSponsorshipsHeaderRenderer"`
	} `json:"header"`
}

// ParseLiveChatResponse parses a raw InnerTube get_live_chat JSON payload into normalized chat events,
// moderation events, the next continuation token, and suggested poll delay.
func ParseLiveChatResponse(data []byte) (events []*chat.Event, modEvents []*chat.ModerationEvent, nextContinuation string, pollDelay time.Duration, err error) {
	var resp getLiveChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, "", 0, fmt.Errorf("unmarshal get_live_chat response: %w", err)
	}

	if resp.ContinuationContents == nil || resp.ContinuationContents.LiveChatContinuation == nil {
		return nil, nil, "", 0, nil
	}

	lc := resp.ContinuationContents.LiveChatContinuation

	// 1. Extract next continuation token & timeout
	pollDelay = 2 * time.Second
	for _, c := range lc.Continuations {
		if c.TimedContinuationData != nil && c.TimedContinuationData.Continuation != "" {
			nextContinuation = c.TimedContinuationData.Continuation
			if c.TimedContinuationData.TimeoutMs > 0 {
				pollDelay = time.Duration(c.TimedContinuationData.TimeoutMs) * time.Millisecond
			}
			break
		}
		if c.InvalidationContinuationData != nil && c.InvalidationContinuationData.Continuation != "" {
			nextContinuation = c.InvalidationContinuationData.Continuation
			if c.InvalidationContinuationData.TimeoutMs > 0 {
				pollDelay = time.Duration(c.InvalidationContinuationData.TimeoutMs) * time.Millisecond
			}
			break
		}
		if c.ReloadContinuationData != nil && c.ReloadContinuationData.Continuation != "" {
			nextContinuation = c.ReloadContinuationData.Continuation
			break
		}
	}

	// Clamp poll delay to sensible bounds (1s to 10s)
	if pollDelay < time.Second {
		pollDelay = time.Second
	} else if pollDelay > 10*time.Second {
		pollDelay = 10 * time.Second
	}

	// 2. Parse actions
	for _, rawAction := range lc.Actions {
		var act chatAction
		if err := json.Unmarshal(rawAction, &act); err != nil {
			continue
		}

		if act.MarkChatItemAsDeletedAction != nil && act.MarkChatItemAsDeletedAction.TargetItemID != "" {
			modEvents = append(modEvents, &chat.ModerationEvent{
				Platform:        "youtube",
				Action:          chat.ModActionDeleteMessage,
				TargetMessageID: act.MarkChatItemAsDeletedAction.TargetItemID,
				Timestamp:       time.Now(),
			})
			continue
		}

		if act.MarkChatItemsByAuthorAsDeletedAction != nil && act.MarkChatItemsByAuthorAsDeletedAction.ExternalChannelID != "" {
			modEvents = append(modEvents, &chat.ModerationEvent{
				Platform:     "youtube",
				Action:       chat.ModActionBanUser,
				TargetUserID: act.MarkChatItemsByAuthorAsDeletedAction.ExternalChannelID,
				Timestamp:    time.Now(),
			})
			continue
		}

		if act.AddChatItemAction == nil {
			continue
		}

		item := act.AddChatItemAction.Item
		switch {
		case item.LiveChatTextMessageRenderer != nil:
			if ev := parseTextMessage(item.LiveChatTextMessageRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatPaidMessageRenderer != nil:
			if ev := parsePaidMessage(item.LiveChatPaidMessageRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatPaidStickerRenderer != nil:
			if ev := parsePaidSticker(item.LiveChatPaidStickerRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatMembershipItemRenderer != nil:
			if ev := parseMembershipItem(item.LiveChatMembershipItemRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatSponsorshipsGiftPurchaseAnnouncementRenderer != nil:
			if ev := parseGiftPurchase(item.LiveChatSponsorshipsGiftPurchaseAnnouncementRenderer); ev != nil {
				events = append(events, ev)
			}
		}
	}

	return events, modEvents, nextContinuation, pollDelay, nil
}

func parseTextMessage(r *textMessageRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	text, emotes := extractTextAndEmotes(r.Message)
	if text == "" {
		return nil
	}
	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindMessage,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		Text:        text,
		EmoteSpans:  emotes,
	}
}

func parsePaidMessage(r *paidMessageRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	text, emotes := extractTextAndEmotes(r.Message)
	amountStr := flattenText(r.PurchaseAmountText)
	amount, currency := parsePurchaseAmount(amountStr)

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindSuperChat,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		Text:        text,
		EmoteSpans:  emotes,
		Count:       1,
		UnitAmount:  amount,
		Amount:      amount,
		Currency:    currency,
	}
}

func parsePaidSticker(r *paidStickerRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	amountStr := flattenText(r.PurchaseAmountText)
	amount, currency := parsePurchaseAmount(amountStr)

	stickerLabel := "Super Sticker"
	var stickerURL string
	if r.Sticker != nil {
		for _, v := range slices.Backward(r.Sticker.Thumbnails) {
			u := strings.TrimSpace(v.URL)
			if u != "" {
				stickerURL = u
				if strings.HasPrefix(stickerURL, "//") {
					stickerURL = "https:" + stickerURL
				}
				break
			}
		}
		if r.Sticker.Accessibility != nil && r.Sticker.Accessibility.AccessibilityData != nil {
			if lbl := strings.TrimSpace(r.Sticker.Accessibility.AccessibilityData.Label); lbl != "" {
				stickerLabel = lbl
			}
		}
	}

	var spans []chat.EmoteSpan
	if stickerURL != "" {
		spans = []chat.EmoteSpan{
			{
				Start: 0,
				End:   utf8.RuneCountInString(stickerLabel),
				ID:    "super_sticker",
				Name:  stickerLabel,
				URL:   stickerURL,
			},
		}
	}

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindSuperSticker,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		ItemName:    stickerLabel,
		RewardTitle: stickerLabel,
		EmoteSpans:  spans,
		Count:       1,
		UnitAmount:  amount,
		Amount:      amount,
		Currency:    currency,
	}
}

func parseMembershipItem(r *membershipItemRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	primaryHeader := flattenText(r.HeaderPrimaryText)
	subHeader := flattenText(r.HeaderSubtext)
	msgText, emotes := extractTextAndEmotes(r.Message)

	kind := chat.EventKindSub
	months := 1

	combinedHeader := strings.ToLower(primaryHeader + " " + subHeader)
	if m := reMonthsCount.FindStringSubmatch(combinedHeader); len(m) > 1 {
		if parsed, err := strconv.Atoi(m[1]); err == nil && parsed > 0 {
			months = parsed
			kind = chat.EventKindResub
		}
	} else if msgText != "" && primaryHeader != "" {
		kind = chat.EventKindResub
	}

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        kind,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		Text:        msgText,
		EmoteSpans:  emotes,
		Months:      months,
	}
}

func parseGiftPurchase(r *giftPurchaseRenderer) *chat.Event {
	if r.Header == nil || r.Header.LiveChatSponsorshipsHeaderRenderer == nil {
		return nil
	}
	hdr := r.Header.LiveChatSponsorshipsHeaderRenderer
	author := strings.TrimLeft(flattenText(hdr.AuthorName), "@")
	primary := flattenText(hdr.PrimaryText)

	count := 1
	if m := reGiftsCount.FindStringSubmatch(primary); len(m) > 1 {
		if parsed, err := strconv.Atoi(m[1]); err == nil && parsed > 0 {
			count = parsed
		}
	}

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindGiftSub,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(hdr.AuthorBadges),
		Count:       count,
	}
}

func flattenText(tr textRuns) string {
	if tr.SimpleText != "" {
		return strings.TrimSpace(tr.SimpleText)
	}
	var sb strings.Builder
	for _, run := range tr.Runs {
		if run.Text != "" {
			sb.WriteString(run.Text)
		} else if run.Emoji != nil && len(run.Emoji.Shortcuts) > 0 {
			sb.WriteString(run.Emoji.Shortcuts[0])
		}
	}
	return strings.TrimSpace(sb.String())
}

func extractTextAndEmotes(tr textRuns) (string, []chat.EmoteSpan) {
	if tr.SimpleText != "" && len(tr.Runs) == 0 {
		return tr.SimpleText, nil
	}
	var sb strings.Builder
	var spans []chat.EmoteSpan
	runeOffset := 0

	for _, run := range tr.Runs {
		if run.Emoji != nil {
			token := run.Text
			if token == "" && len(run.Emoji.Shortcuts) > 0 {
				token = run.Emoji.Shortcuts[0]
			}
			if token == "" {
				token = ":emoji:"
			}
			var imgURL string
			if run.Emoji.Image != nil && len(run.Emoji.Image.Thumbnails) > 0 {
				imgURL = strings.TrimSpace(run.Emoji.Image.Thumbnails[0].URL)
				if strings.HasPrefix(imgURL, "//") {
					imgURL = "https:" + imgURL
				}
			}
			tokenRunes := utf8.RuneCountInString(token)
			sb.WriteString(token)
			spans = append(spans, chat.EmoteSpan{
				Start: runeOffset,
				End:   runeOffset + tokenRunes,
				ID:    run.Emoji.EmojiID,
				Name:  token,
				URL:   imgURL,
			})
			runeOffset += tokenRunes
			continue
		}
		if run.Text != "" {
			sb.WriteString(run.Text)
			runeOffset += utf8.RuneCountInString(run.Text)
		}
	}

	return sb.String(), spans
}

func extractAuthorRoles(badges []authorBadge) []string {
	var roles []string
	seen := make(map[string]bool)
	addRole := func(r string) {
		if !seen[r] {
			seen[r] = true
			roles = append(roles, r)
		}
	}

	for _, b := range badges {
		r := b.LiveChatAuthorBadgeRenderer
		if r == nil {
			continue
		}
		iconType := ""
		if r.Icon != nil {
			iconType = strings.ToUpper(r.Icon.IconType)
		}
		tooltip := strings.ToLower(r.Tooltip)

		switch {
		case iconType == "OWNER" || strings.Contains(tooltip, "owner"):
			addRole("broadcaster")
		case iconType == "MODERATOR" || strings.Contains(tooltip, "moderator"):
			addRole("mod")
		case iconType == "VERIFIED" || strings.Contains(tooltip, "verified"):
			addRole("vip")
		case r.CustomThumbnail != nil || strings.Contains(tooltip, "member"):
			addRole("subscriber")
		}
	}
	return roles
}

func parsePurchaseAmount(raw string) (float64, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 1, "USD"
	}
	m := reAmountNumbers.FindStringSubmatch(s)
	if len(m) < 2 {
		return 1, s
	}
	numStr := strings.ReplaceAll(m[1], ",", "")
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val <= 0 {
		val = 1
	}
	currency := strings.TrimSpace(strings.Replace(s, m[1], "", 1))
	switch currency {
	case "$", "US$":
		currency = "dollars"
	case "€":
		currency = "euros"
	case "£":
		currency = "pounds"
	case "¥":
		currency = "yen"
	case "":
		currency = "USD"
	}
	return val, currency
}

func parseUsecTimestamp(usecStr string) time.Time {
	if usec, err := strconv.ParseInt(usecStr, 10, 64); err == nil && usec > 0 {
		return time.UnixMicro(usec)
	}
	return time.Now()
}
