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

	"github.com/mrpoundsign/TownCrier/pkg/chat"
)

var (
	reInnertubeKey  = regexp.MustCompile(`"INNERTUBE_API_KEY"\s*:\s*"([^"]+)"`)
	reContinuation  = regexp.MustCompile(`"liveChatRenderer"\s*:\s*\{.*?"continuations"\s*:\s*\[\s*\{\s*"(?:timedContinuationData|invalidationContinuationData|reloadContinuationData)"\s*:\s*\{[^}]*?"continuation"\s*:\s*"([^"]+)"`)
	reReloadCont    = regexp.MustCompile(`"continuation"\s*:\s*"(0ofMyA[^"]+)"`)
	reAmountNumbers     = regexp.MustCompile(`([0-9]+(?:[.,][0-9]+)?)`)
	reMonthsCount       = regexp.MustCompile(`([0-9]+)\s*(?:month|months|mo\b)`)
	reGiftsCount        = regexp.MustCompile(`([0-9]+)`)
	reGiftSentForJewels = regexp.MustCompile(`(?i)^sent\s+(?:a\s+gift:?\s*)?(.+?)\s+for\s+([0-9,]+)\s+jewels?$`)
	reGiftSentSimple    = regexp.MustCompile(`(?i)^sent\s+(?:a\s+gift:?\s*|a\s+)?(.+)$`)
	reGiftA11y          = regexp.MustCompile(`(?i)^(?:@?([^\s]+)\s+)?sent\s+a\s+gift,\s*(.+)$`)
)

// knownYouTubeGiftJewels provides fallback Jewel valuations for standard YouTube Gifts
// when InnerTube's giftMessageViewModel omits the explicit "for N Jewels" suffix.
var knownYouTubeGiftJewels = map[string]float64{
	"100":           2,
	"heart":         2,
	"party popper":  2,
	"thumbs up":     2,
	"smile":         2,
	"clap":          2,
	"fire":          2,
	"star":          5,
	"rose":          5,
	"donut":         10,
	"doughnut":      10,
	"gold coin":     10,
	"coffee":        10,
	"stay hydrated": 10,
	"ramen":         20,
	"boba":          20,
	"pizza":         20,
	"trophy":        50,
	"crown":         100,
	"diamond":       100,
	"rocket":        200,
}

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
	_, isLive, found = ExtractVideoIDAndDetails(html)
	return isLive, found
}

// ExtractVideoIDAndDetails parses the videoDetails JSON object from YouTube HTML if present,
// returning the videoId, whether the stream is actively live, and whether videoDetails was found.
func ExtractVideoIDAndDetails(html string) (videoID string, isLive bool, found bool) {
	search := html
	for {
		idx := strings.Index(search, `"videoDetails"`)
		if idx == -1 {
			return "", false, false
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
				VideoID       string `json:"videoId"`
				IsLive        bool   `json:"isLive"`
				IsLiveContent bool   `json:"isLiveContent"`
			}
			if err := json.Unmarshal([]byte(rest[start:end]), &details); err == nil {
				return strings.TrimSpace(details.VideoID), details.IsLive, true
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
			LiveChatTextMessageRenderer                            *textMessageRenderer        `json:"liveChatTextMessageRenderer"`
			LiveChatPaidMessageRenderer                            *paidMessageRenderer        `json:"liveChatPaidMessageRenderer"`
			LiveChatPaidStickerRenderer                            *paidStickerRenderer        `json:"liveChatPaidStickerRenderer"`
			LiveChatMembershipItemRenderer                         *membershipItemRenderer     `json:"liveChatMembershipItemRenderer"`
			LiveChatSponsorshipsGiftPurchaseAnnouncementRenderer   *giftPurchaseRenderer       `json:"liveChatSponsorshipsGiftPurchaseAnnouncementRenderer"`
			LiveChatSponsorshipsGiftRedemptionAnnouncementRenderer *giftRedemptionRenderer     `json:"liveChatSponsorshipsGiftRedemptionAnnouncementRenderer"`
			LiveChatDonationAnnouncementRenderer                   *donationRenderer           `json:"liveChatDonationAnnouncementRenderer"`
			LiveChatViewerEngagementMessageRenderer                *engagementMessageRenderer  `json:"liveChatViewerEngagementMessageRenderer"`
			LiveChatBannerRedirectRenderer                         *bannerRedirectRenderer     `json:"liveChatBannerRedirectRenderer"`
			GiftMessageViewModel                                   *giftMessageViewModel       `json:"giftMessageViewModel"`
		} `json:"item"`
	} `json:"addChatItemAction"`
	AddBannerToLiveChatCommand *struct {
		BannerRenderer struct {
			LiveChatBannerRenderer *struct {
				Contents struct {
					LiveChatBannerRedirectRenderer *bannerRedirectRenderer `json:"liveChatBannerRedirectRenderer"`
				} `json:"contents"`
			} `json:"liveChatBannerRenderer"`
			LiveChatBannerRedirectRenderer *bannerRedirectRenderer `json:"liveChatBannerRedirectRenderer"`
		} `json:"bannerRenderer"`
	} `json:"addBannerToLiveChatCommand"`
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

type giftRedemptionRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	Message                 textRuns      `json:"message"`
}

type donationRenderer struct {
	ID                      string        `json:"id"`
	TimestampUsec           string        `json:"timestampUsec"`
	AuthorName              textRuns      `json:"authorName"`
	AuthorExternalChannelID string        `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge `json:"authorBadges"`
	Subtext                 textRuns      `json:"subtext"`
	Message                 textRuns      `json:"message"`
	PurchaseAmountText      textRuns      `json:"purchaseAmountText"`
}

type engagementMessageRenderer struct {
	ID            string   `json:"id"`
	TimestampUsec string   `json:"timestampUsec"`
	Message       textRuns `json:"message"`
	Icon          *struct {
		IconType string `json:"iconType"`
	} `json:"icon"`
}

type bannerRedirectRenderer struct {
	ID            string   `json:"id"`
	BannerMessage textRuns `json:"bannerMessage"`
	AuthorName    textRuns `json:"authorName"`
}

type viewModelText struct {
	Content    string    `json:"content"`
	SimpleText string    `json:"simpleText"`
	Runs       []textRun `json:"runs"`
}

type viewModelImage struct {
	Sources []struct {
		URL string `json:"url"`
	} `json:"sources"`
	Thumbnails []struct {
		URL string `json:"url"`
	} `json:"thumbnails"`
}

type giftMessageViewModel struct {
	ID                      string          `json:"id"`
	TimestampUsec           string          `json:"timestampUsec"`
	AuthorName              viewModelText   `json:"authorName"`
	AuthorExternalChannelID string          `json:"authorExternalChannelId"`
	AuthorBadges            []authorBadge   `json:"authorBadges"`
	Text                    viewModelText   `json:"text"`
	GiftImage               *viewModelImage `json:"giftImage"`
	GiftImageA11yLabel      string          `json:"giftImageA11yLabel"`
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

		if act.AddBannerToLiveChatCommand != nil {
			var br *bannerRedirectRenderer
			if act.AddBannerToLiveChatCommand.BannerRenderer.LiveChatBannerRedirectRenderer != nil {
				br = act.AddBannerToLiveChatCommand.BannerRenderer.LiveChatBannerRedirectRenderer
			} else if act.AddBannerToLiveChatCommand.BannerRenderer.LiveChatBannerRenderer != nil {
				br = act.AddBannerToLiveChatCommand.BannerRenderer.LiveChatBannerRenderer.Contents.LiveChatBannerRedirectRenderer
			}
			if br != nil {
				if ev := parseBannerRedirect(br); ev != nil {
					events = append(events, ev)
				}
			}
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
		case item.LiveChatSponsorshipsGiftRedemptionAnnouncementRenderer != nil:
			if ev := parseGiftRedemption(item.LiveChatSponsorshipsGiftRedemptionAnnouncementRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatDonationAnnouncementRenderer != nil:
			if ev := parseDonation(item.LiveChatDonationAnnouncementRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatBannerRedirectRenderer != nil:
			if ev := parseBannerRedirect(item.LiveChatBannerRedirectRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.LiveChatViewerEngagementMessageRenderer != nil:
			if ev := parseEngagementRedirect(item.LiveChatViewerEngagementMessageRenderer); ev != nil {
				events = append(events, ev)
			}
		case item.GiftMessageViewModel != nil:
			if ev := parseGiftMessage(item.GiftMessageViewModel); ev != nil {
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
		Mentions:    chat.ExtractMentions(text),
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

func parseGiftRedemption(r *giftRedemptionRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	if author == "" {
		return nil
	}
	msgText := flattenText(r.Message)
	var gifter string
	if idx := strings.LastIndex(strings.ToLower(msgText), " by "); idx >= 0 {
		gifter = strings.TrimLeft(strings.TrimRight(strings.TrimSpace(msgText[idx+4:]), ".!"), "@")
	} else if idx := strings.LastIndex(strings.ToLower(msgText), " from "); idx >= 0 {
		gifter = strings.TrimLeft(strings.TrimRight(strings.TrimSpace(msgText[idx+6:]), ".!"), "@")
	}

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindGiftRedemption,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      r.AuthorExternalChannelID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		Recipient:   gifter,
		Count:       1,
	}
}

func parseDonation(r *donationRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	if author == "" {
		author = "Donor"
	}
	text, emotes := extractTextAndEmotes(r.Message)
	amountStr := flattenText(r.PurchaseAmountText)
	if amountStr == "" {
		amountStr = flattenText(r.Subtext)
	}
	amount, currency := parsePurchaseAmount(amountStr)

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindCharityDonation,
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

func parseBannerRedirect(r *bannerRedirectRenderer) *chat.Event {
	author := strings.TrimLeft(flattenText(r.AuthorName), "@")
	bannerMsg := flattenText(r.BannerMessage)
	if author == "" && len(r.BannerMessage.Runs) > 0 {
		author = strings.TrimLeft(strings.TrimSpace(r.BannerMessage.Runs[0].Text), "@")
	}
	if author == "" {
		return nil
	}
	id := strings.TrimSpace(r.ID)
	if id == "" {
		id = fmt.Sprintf("yt-raid-%s-%d", strings.ToLower(author), time.Now().UnixNano())
	}
	return &chat.Event{
		ID:          id,
		Platform:    "youtube",
		Kind:        chat.EventKindRaid,
		Timestamp:   time.Now(),
		UserID:      author,
		UserName:    author,
		DisplayName: author,
		Text:        bannerMsg,
		Count:       1,
	}
}

func parseEngagementRedirect(r *engagementMessageRenderer) *chat.Event {
	msgText := flattenText(r.Message)
	lower := strings.ToLower(msgText)
	if !strings.Contains(lower, "redirect") && !strings.Contains(lower, "raided") {
		return nil
	}
	var author string
	if len(r.Message.Runs) > 0 {
		author = strings.TrimLeft(strings.TrimSpace(r.Message.Runs[0].Text), "@")
	}
	if author == "" {
		author = "Channel"
	}
	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindRaid,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      author,
		UserName:    author,
		DisplayName: author,
		Text:        msgText,
		Count:       1,
	}
}

func parseGiftMessage(r *giftMessageViewModel) *chat.Event {
	author := strings.TrimSpace(strings.TrimLeft(flattenViewModelText(r.AuthorName), "@"))
	rawText := flattenViewModelText(r.Text)

	var giftName string
	var jewels float64

	if m := reGiftSentForJewels.FindStringSubmatch(rawText); len(m) > 2 {
		giftName = strings.TrimSpace(m[1])
		numStr := strings.ReplaceAll(m[2], ",", "")
		if parsed, err := strconv.ParseFloat(numStr, 64); err == nil && parsed > 0 {
			jewels = parsed
		}
	} else if m := reGiftSentSimple.FindStringSubmatch(rawText); len(m) > 1 {
		giftName = strings.TrimSpace(m[1])
	}

	if author == "" || giftName == "" {
		if m := reGiftA11y.FindStringSubmatch(strings.TrimSpace(r.GiftImageA11yLabel)); len(m) > 2 {
			if author == "" && m[1] != "" {
				author = strings.TrimSpace(strings.TrimLeft(m[1], "@"))
			}
			if giftName == "" && m[2] != "" {
				giftName = strings.TrimSpace(m[2])
			}
		}
	}

	if author == "" {
		return nil
	}
	if giftName == "" {
		giftName = "Gift"
	}

	if jewels <= 0 {
		if known, ok := knownYouTubeGiftJewels[strings.ToLower(giftName)]; ok {
			jewels = known
		}
	}

	var giftURL string
	if r.GiftImage != nil {
		for _, v := range slices.Backward(r.GiftImage.Sources) {
			u := strings.TrimSpace(v.URL)
			if u != "" {
				giftURL = u
				break
			}
		}
		if giftURL == "" {
			for _, v := range slices.Backward(r.GiftImage.Thumbnails) {
				u := strings.TrimSpace(v.URL)
				if u != "" {
					giftURL = u
					break
				}
			}
		}
		if strings.HasPrefix(giftURL, "//") {
			giftURL = "https:" + giftURL
		}
	}

	var spans []chat.EmoteSpan
	if giftURL != "" {
		spans = []chat.EmoteSpan{
			{
				Start: 0,
				End:   utf8.RuneCountInString(giftName),
				ID:    "youtube_gift_" + strings.ToLower(strings.ReplaceAll(giftName, " ", "_")),
				Name:  giftName,
				URL:   giftURL,
			},
		}
	}

	userID := strings.TrimSpace(r.AuthorExternalChannelID)
	if userID == "" {
		userID = author
	}

	return &chat.Event{
		ID:          r.ID,
		Platform:    "youtube",
		Kind:        chat.EventKindGift,
		Timestamp:   parseUsecTimestamp(r.TimestampUsec),
		UserID:      userID,
		UserName:    author,
		DisplayName: author,
		Roles:       extractAuthorRoles(r.AuthorBadges),
		ItemName:    giftName,
		RewardTitle: giftName,
		EmoteSpans:  spans,
		Count:       1,
		UnitAmount:  jewels,
		Amount:      jewels,
		Currency:    "Jewels",
	}
}

func flattenViewModelText(vt viewModelText) string {
	if vt.Content != "" {
		return strings.TrimSpace(vt.Content)
	}
	return flattenText(textRuns{
		SimpleText: vt.SimpleText,
		Runs:       vt.Runs,
	})
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

// YouTube Data API v3 liveChatMessages.list models & parser

type dataAPILiveChatResponse struct {
	NextPageToken         string                `json:"nextPageToken"`
	PollingIntervalMillis int                   `json:"pollingIntervalMillis"`
	OfflineAt             string                `json:"offlineAt"`
	Items                 []dataAPILiveChatItem `json:"items"`
}

type dataAPILiveChatItem struct {
	ID      string `json:"id"`
	Snippet *struct {
		Type               string `json:"type"`
		LiveChatID         string `json:"liveChatId"`
		AuthorChannelID    string `json:"authorChannelId"`
		PublishedAt        string `json:"publishedAt"`
		DisplayMessage     string `json:"displayMessage"`
		TextMessageDetails *struct {
			MessageText string `json:"messageText"`
		} `json:"textMessageDetails"`
		SuperChatDetails *struct {
			AmountMicros        string `json:"amountMicros"`
			Currency            string `json:"currency"`
			AmountDisplayString string `json:"amountDisplayString"`
			UserComment         string `json:"userComment"`
		} `json:"superChatDetails"`
		SuperStickerDetails *struct {
			AmountMicros        string `json:"amountMicros"`
			Currency            string `json:"currency"`
			AmountDisplayString string `json:"amountDisplayString"`
			SuperStickerMetadata *struct {
				StickerID  string `json:"stickerId"`
				AltText    string `json:"altText"`
			} `json:"superStickerMetadata"`
		} `json:"superStickerDetails"`
		NewSponsorDetails *struct {
			MemberLevelName string `json:"memberLevelName"`
			IsUpgrade       bool   `json:"isUpgrade"`
		} `json:"newSponsorDetails"`
		MemberMilestoneChatDetails *struct {
			MemberLevelName string `json:"memberLevelName"`
			MemberMonth     int    `json:"memberMonth"`
			UserComment     string `json:"userComment"`
		} `json:"memberMilestoneChatDetails"`
		MembershipGiftingDetails *struct {
			GiftMembershipsCount int    `json:"giftMembershipsCount"`
			GiftMembershipsLevel string `json:"giftMembershipsLevelName"`
		} `json:"membershipGiftingDetails"`
		GiftMembershipReceivedDetails *struct {
			MemberLevelName                  string `json:"memberLevelName"`
			GifterChannelID                  string `json:"gifterChannelId"`
			AssociatedMembershipGiftingMsgID string `json:"associatedMembershipGiftingMessageId"`
		} `json:"giftMembershipReceivedDetails"`
		MessageDeletedDetails *struct {
			DeletedMessageID string `json:"deletedMessageId"`
		} `json:"messageDeletedDetails"`
		UserBannedDetails *struct {
			BannedUserDetails *struct {
				ChannelID   string `json:"channelId"`
				DisplayName string `json:"displayName"`
			} `json:"bannedUserDetails"`
			BanType            string `json:"banType"`
			BanDurationSeconds int    `json:"banDurationSeconds"`
		} `json:"userBannedDetails"`
	} `json:"snippet"`
	AuthorDetails *struct {
		ChannelID       string `json:"channelId"`
		DisplayName     string `json:"displayName"`
		IsVerified      bool   `json:"isVerified"`
		IsChatOwner     bool   `json:"isChatOwner"`
		IsChatSponsor   bool   `json:"isChatSponsor"`
		IsChatModerator bool   `json:"isChatModerator"`
	} `json:"authorDetails"`
}

// ParseDataAPILiveChatResponse parses a YouTube Data API v3 GET /liveChat/messages JSON payload into
// normalized chat events, moderation events, the next page token, and suggested poll delay.
func ParseDataAPILiveChatResponse(data []byte) (events []*chat.Event, modEvents []*chat.ModerationEvent, nextPageToken string, pollDelay time.Duration, err error) {
	var resp dataAPILiveChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, "", 0, fmt.Errorf("unmarshal liveChat/messages response: %w", err)
	}

	if strings.TrimSpace(resp.OfflineAt) != "" {
		return nil, nil, "", 0, nil
	}

	nextPageToken = strings.TrimSpace(resp.NextPageToken)
	pollDelay = 5 * time.Second
	if resp.PollingIntervalMillis > 0 {
		pollDelay = time.Duration(resp.PollingIntervalMillis) * time.Millisecond
	}
	if pollDelay < 2*time.Second {
		pollDelay = 2 * time.Second
	} else if pollDelay > 15*time.Second {
		pollDelay = 15 * time.Second
	}

	for _, item := range resp.Items {
		if item.Snippet == nil {
			continue
		}
		sn := item.Snippet
		msgType := strings.TrimSpace(sn.Type)

		switch msgType {
		case "messageDeletedEvent":
			if sn.MessageDeletedDetails != nil && strings.TrimSpace(sn.MessageDeletedDetails.DeletedMessageID) != "" {
				modEvents = append(modEvents, &chat.ModerationEvent{
					Platform:        "youtube",
					Action:          chat.ModActionDeleteMessage,
					TargetMessageID: strings.TrimSpace(sn.MessageDeletedDetails.DeletedMessageID),
					Timestamp:       parseRFC3339Timestamp(sn.PublishedAt),
				})
			}
			continue

		case "userBannedEvent":
			if sn.UserBannedDetails != nil && sn.UserBannedDetails.BannedUserDetails != nil {
				targetUID := strings.TrimSpace(sn.UserBannedDetails.BannedUserDetails.ChannelID)
				if targetUID != "" {
					modEvents = append(modEvents, &chat.ModerationEvent{
						Platform:     "youtube",
						Action:       chat.ModActionBanUser,
						TargetUserID: targetUID,
						Timestamp:    parseRFC3339Timestamp(sn.PublishedAt),
					})
				}
			}
			continue
		}

		author := ""
		userID := strings.TrimSpace(sn.AuthorChannelID)
		var roles []string
		if item.AuthorDetails != nil {
			author = strings.TrimLeft(strings.TrimSpace(item.AuthorDetails.DisplayName), "@")
			if userID == "" {
				userID = strings.TrimSpace(item.AuthorDetails.ChannelID)
			}
			if item.AuthorDetails.IsChatOwner {
				roles = append(roles, "broadcaster")
			}
			if item.AuthorDetails.IsChatModerator {
				roles = append(roles, "mod")
			}
			if item.AuthorDetails.IsVerified {
				roles = append(roles, "vip")
			}
			if item.AuthorDetails.IsChatSponsor {
				roles = append(roles, "subscriber")
			}
		}
		if author == "" {
			author = userID
		}
		ts := parseRFC3339Timestamp(sn.PublishedAt)

		switch msgType {
		case "textMessageEvent":
			text := strings.TrimSpace(sn.DisplayMessage)
			if sn.TextMessageDetails != nil && strings.TrimSpace(sn.TextMessageDetails.MessageText) != "" {
				text = strings.TrimSpace(sn.TextMessageDetails.MessageText)
			}
			if text == "" {
				continue
			}
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindMessage,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Mentions:    chat.ExtractMentions(text),
				Text:        text,
			})

		case "superChatEvent":
			var text, amountDisplay string
			if sn.SuperChatDetails != nil {
				text = strings.TrimSpace(sn.SuperChatDetails.UserComment)
				amountDisplay = sn.SuperChatDetails.AmountDisplayString
			}
			if text == "" {
				text = strings.TrimSpace(sn.DisplayMessage)
			}
			amount, currency := parsePurchaseAmount(amountDisplay)
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindSuperChat,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Text:        text,
				Count:       1,
				UnitAmount:  amount,
				Amount:      amount,
				Currency:    currency,
			})

		case "superStickerEvent":
			var amountDisplay string
			stickerLabel := "Super Sticker"
			if sn.SuperStickerDetails != nil {
				amountDisplay = sn.SuperStickerDetails.AmountDisplayString
				if sn.SuperStickerDetails.SuperStickerMetadata != nil && strings.TrimSpace(sn.SuperStickerDetails.SuperStickerMetadata.AltText) != "" {
					stickerLabel = strings.TrimSpace(sn.SuperStickerDetails.SuperStickerMetadata.AltText)
				}
			}
			amount, currency := parsePurchaseAmount(amountDisplay)
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindSuperSticker,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				ItemName:    stickerLabel,
				RewardTitle: stickerLabel,
				Count:       1,
				UnitAmount:  amount,
				Amount:      amount,
				Currency:    currency,
			})

		case "newSponsorEvent":
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindSub,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Months:      1,
			})

		case "memberMilestoneChatEvent":
			months := 1
			var text string
			if sn.MemberMilestoneChatDetails != nil {
				if sn.MemberMilestoneChatDetails.MemberMonth > 0 {
					months = sn.MemberMilestoneChatDetails.MemberMonth
				}
				text = strings.TrimSpace(sn.MemberMilestoneChatDetails.UserComment)
			}
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindResub,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Text:        text,
				Months:      months,
			})

		case "membershipGiftingEvent":
			count := 1
			if sn.MembershipGiftingDetails != nil && sn.MembershipGiftingDetails.GiftMembershipsCount > 0 {
				count = sn.MembershipGiftingDetails.GiftMembershipsCount
			}
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindGiftSub,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Count:       count,
			})

		case "giftMembershipReceivedEvent":
			var gifter string
			if sn.GiftMembershipReceivedDetails != nil {
				gifter = strings.TrimSpace(sn.GiftMembershipReceivedDetails.GifterChannelID)
			}
			events = append(events, &chat.Event{
				ID:          item.ID,
				Platform:    "youtube",
				Kind:        chat.EventKindGiftRedemption,
				Timestamp:   ts,
				UserID:      userID,
				UserName:    author,
				DisplayName: author,
				Roles:       roles,
				Recipient:   gifter,
				Count:       1,
			})
		}
	}

	return events, modEvents, nextPageToken, pollDelay, nil
}

func parseRFC3339Timestamp(tsStr string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tsStr)); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(tsStr)); err == nil {
		return t
	}
	return time.Now()
}
