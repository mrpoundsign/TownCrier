package eventlog

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// EventOutcome represents the pipeline or speech queue fate of an inbound stream event.
type EventOutcome string

const (
	// Passed filters -> Speech Queue lifecycle outcomes
	OutcomeQueued            EventOutcome = "queued"
	OutcomeSpoken            EventOutcome = "spoken"
	OutcomeDroppedStale      EventOutcome = "dropped_stale"
	OutcomeDroppedPanic      EventOutcome = "dropped_panic"
	OutcomeDroppedModeration EventOutcome = "dropped_moderation"
	OutcomeSkipped           EventOutcome = "skipped"
	OutcomeErrorTTS          EventOutcome = "error_tts"
	OutcomeTTSDisabled       EventOutcome = "tts_disabled"

	// Rejected by pre-queue filters
	OutcomeDroppedBot       EventOutcome = "dropped_bot"
	OutcomeDroppedReadRules EventOutcome = "dropped_read_rules"
	OutcomeDroppedDisabled  EventOutcome = "dropped_disabled"
	OutcomeDroppedThreshold EventOutcome = "dropped_threshold"
	OutcomeDroppedSanitized EventOutcome = "dropped_sanitized"

	// Inbound platform moderation sync action
	OutcomeModerationEvent EventOutcome = "moderation_event"
	OutcomeHeldForReview   EventOutcome = "held_for_review"
)

// OutcomeDisplayLabel returns a concise human-readable badge label for an EventOutcome.
func OutcomeDisplayLabel(outcome EventOutcome) string {
	switch outcome {
	case OutcomeSpoken:
		return "🗣️ Spoken"
	case OutcomeQueued:
		return "⏳ Queued"
	case OutcomeDroppedStale:
		return "⏱️ Dropped (Stale >TTL)"
	case OutcomeDroppedPanic:
		return "🚨 Dropped (Panic Mute)"
	case OutcomeDroppedModeration:
		return "🛡️ Dropped (Mod Action)"
	case OutcomeSkipped:
		return "⏭️ Skipped / Cleared"
	case OutcomeErrorTTS:
		return "⚠️ TTS / Audio Error"
	case OutcomeTTSDisabled:
		return "🔇 TTS Disabled"
	case OutcomeDroppedBot:
		return "🤖 Ignored (Bot / Muted)"
	case OutcomeDroppedReadRules:
		return "📏 Filtered (Read Rules)"
	case OutcomeDroppedDisabled:
		return "🔕 Filtered (Alert Disabled)"
	case OutcomeDroppedThreshold:
		return "📉 Filtered (Below Threshold)"
	case OutcomeDroppedSanitized:
		return "🧹 Filtered (Empty / Blocked)"
	case OutcomeModerationEvent:
		return "🛡️ Moderation Action"
	case OutcomeHeldForReview:
		return "🛡️ Held for Review"
	default:
		if outcome == "" {
			return "Unknown"
		}
		return string(outcome)
	}
}

// StreamSession represents an active or completed livestreaming session across one or more platforms.
type StreamSession struct {
	ID        int64     `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"` // Zero means currently active
	Platforms []string  `json:"platforms,omitempty"`
	Channels  []string  `json:"channels,omitempty"`
}

// Entry represents a single structured chat, alert, or moderation log document.
type Entry struct {
	ID            string           `json:"id"`
	SessionID     int64            `json:"session_id,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Platform      string           `json:"platform"`
	Channel       string           `json:"channel"`
	Kind          string           `json:"kind"`
	IsPaidOrEvent bool             `json:"is_paid_or_event"`
	UserID        string           `json:"user_id"`
	UserName      string           `json:"user_name"`
	DisplayName   string           `json:"display_name"`
	Roles         []string         `json:"roles,omitempty"`
	RawText        string           `json:"raw_text"`
	Emotes         []chat.EmoteSpan `json:"emotes,omitempty"`
	TranslatedText string           `json:"translated_text,omitempty"`
	SourceLang     string           `json:"source_lang,omitempty"`
	SpokenText     string           `json:"spoken_text,omitempty"`
	VoiceID       string           `json:"voice_id,omitempty"`
	Speed         float32          `json:"speed,omitempty"`
	Amount        float64          `json:"amount,omitempty"`
	UnitAmount    float64          `json:"unit_amount,omitempty"`
	Currency      string           `json:"currency,omitempty"`
	ItemName      string           `json:"item_name,omitempty"`
	SubTier       string           `json:"sub_tier,omitempty"`
	Recipient     string           `json:"recipient,omitempty"`
	RewardID      string           `json:"reward_id,omitempty"`
	RewardTitle   string           `json:"reward_title,omitempty"`
	Months        int              `json:"months,omitempty"`
	Count         int              `json:"count,omitempty"`
	PassedFilters bool             `json:"passed_filters"`
	Spoken        bool             `json:"spoken"`
	Outcome       EventOutcome     `json:"outcome"`
	OutcomeDetail string           `json:"outcome_detail,omitempty"`
}

// Clone returns a deep copy of the Entry.
func (e *Entry) Clone() *Entry {
	if e == nil {
		return nil
	}
	cp := *e
	if len(e.Roles) > 0 {
		cp.Roles = make([]string, len(e.Roles))
		copy(cp.Roles, e.Roles)
	}
	if len(e.Emotes) > 0 {
		cp.Emotes = make([]chat.EmoteSpan, len(e.Emotes))
		copy(cp.Emotes, e.Emotes)
	}
	return &cp
}

// OutcomeUpdate describes a post-enqueue status transition (e.g. spoken or dropped in queue).
type OutcomeUpdate struct {
	Spoken        bool         `json:"spoken"`
	Outcome       EventOutcome `json:"outcome"`
	OutcomeDetail string       `json:"outcome_detail,omitempty"`
	Speed         float32      `json:"speed,omitempty"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// OutcomeFilterMode categorizes high-level status filters in the search UI.
type OutcomeFilterMode string

const (
	FilterOutcomeAll           OutcomeFilterMode = "all"
	FilterOutcomeSpokenOnly    OutcomeFilterMode = "spoken_only"
	FilterOutcomePassedFilters OutcomeFilterMode = "passed_filters"
	FilterOutcomeDroppedQueue  OutcomeFilterMode = "dropped_queue"
	FilterOutcomeFilteredOut   OutcomeFilterMode = "filtered_out"
	FilterOutcomeModeration    OutcomeFilterMode = "moderation"
)

// RetentionPolicy defines dynamic created_at-based retention windows for chat vs. paid/stream events.
// A value of 0 means keep forever (no expiration). Negative values are treated as 0.
type RetentionPolicy struct {
	ChatRetentionDays  int       `json:"chat_retention_days"`
	EventRetentionDays int       `json:"event_retention_days"`
	Now                time.Time `json:"-"`
}

// Cutoffs computes the UTC cutoff timestamps for regular chat and paid/stream events.
// A zero time.Time indicates no expiration cutoff is active for that category.
func (p RetentionPolicy) Cutoffs() (chatCutoff time.Time, eventCutoff time.Time) {
	now := p.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	if p.ChatRetentionDays > 0 {
		chatCutoff = now.AddDate(0, 0, -p.ChatRetentionDays)
	}
	if p.EventRetentionDays > 0 {
		eventCutoff = now.AddDate(0, 0, -p.EventRetentionDays)
	}
	return chatCutoff, eventCutoff
}

// QueryFilter specifies search, date-range, session, attribute, and pagination parameters.
type QueryFilter struct {
	SessionID     int64             `json:"session_id,omitempty"`
	SessionIDs    []int64           `json:"session_ids,omitempty"`
	From          *time.Time        `json:"from,omitempty"`
	To            *time.Time        `json:"to,omitempty"`
	Platform      string            `json:"platform,omitempty"`
	Channel       string            `json:"channel,omitempty"`
	KindFilter    string            `json:"kind_filter,omitempty"`
	UserQuery     string            `json:"user_query,omitempty"`
	TextQuery     string            `json:"text_query,omitempty"`
	OutcomeMode   OutcomeFilterMode `json:"outcome_mode,omitempty"`
	Outcome       EventOutcome      `json:"outcome,omitempty"`
	HideBots      bool              `json:"hide_bots,omitempty"`
	ExcludedUsers []string          `json:"excluded_users,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	Offset        int               `json:"offset,omitempty"`
	SkipSummary   bool              `json:"skip_summary,omitempty"`
	Retention     RetentionPolicy   `json:"-"`
}

// QuerySummary holds aggregated stream event and monetization metrics for a QueryFilter.
type QuerySummary struct {
	TotalSubs        int                `json:"total_subs"`
	TotalGiftSubs    int                `json:"total_gift_subs"`
	TotalBits        float64            `json:"total_bits"`
	TotalDiamonds    float64            `json:"total_diamonds"`
	TotalJewels      float64            `json:"total_jewels"`
	TotalKicks       float64            `json:"total_kicks"`
	SuperChatByCurr  map[string]float64 `json:"super_chat_by_curr,omitempty"`
	TotalRaids       int                `json:"total_raids"`
	TotalRaidViewers int                `json:"total_raid_viewers"`
	TotalFollows     int                `json:"total_follows"`
	TotalRedemptions int                `json:"total_redemptions"`
}

// Clone returns a deep copy of QuerySummary.
func (s QuerySummary) Clone() QuerySummary {
	clone := s
	if s.SuperChatByCurr != nil {
		clone.SuperChatByCurr = make(map[string]float64, len(s.SuperChatByCurr))
		maps.Copy(clone.SuperChatByCurr, s.SuperChatByCurr)
	}
	return clone
}

// AddEntry accumulates a single Entry into QuerySummary if it is an alert or monetization event.
func (s *QuerySummary) AddEntry(e Entry) {
	kind := strings.ToLower(strings.TrimSpace(e.Kind))
	if kind == "" || kind == "message" || kind == "moderation" {
		return
	}
	plat := strings.ToLower(strings.TrimSpace(e.Platform))
	curr := strings.ToUpper(strings.TrimSpace(e.Currency))
	cnt := e.Count
	if cnt <= 0 {
		cnt = 1
	}
	s.AccumulateRow(kind, plat, curr, 1, e.Amount, cnt)
}

// AccumulateRow adds aggregated metrics for a kind/platform/currency group to QuerySummary.
func (s *QuerySummary) AccumulateRow(kind, platform, currency string, rowCount int, sumAmount float64, sumCount int) {
	switch kind {
	case "sub", "resub", "sub_upgrade", "membership":
		s.TotalSubs += rowCount
	case "gift_sub", "gift_membership":
		s.TotalGiftSubs += sumCount
	case "cheer":
		s.TotalBits += sumAmount
	case "gift":
		val := sumAmount
		if val <= 0 {
			val = float64(sumCount)
		}
		switch {
		case platform == "kick" || currency == "KICKS":
			s.TotalKicks += val
		case platform == "youtube" || currency == "JEWELS" || currency == "JEWEL":
			s.TotalJewels += val
		default:
			s.TotalDiamonds += val
		}
	case "super_chat", "super_sticker", "superchat", "supersticker", "charity_donation":
		curr := currency
		if curr == "" {
			curr = "USD"
		}
		if s.SuperChatByCurr == nil {
			s.SuperChatByCurr = make(map[string]float64)
		}
		s.SuperChatByCurr[curr] += sumAmount
	case "raid":
		s.TotalRaids += rowCount
		s.TotalRaidViewers += sumCount
	case "follow":
		s.TotalFollows += rowCount
	case "redemption":
		s.TotalRedemptions += rowCount
	}
}

// QueryResult holds a page of matching log entries, the total matching row count, and aggregated summary totals.
type QueryResult struct {
	Entries    []Entry      `json:"entries"`
	TotalCount int          `json:"total_count"`
	Summary    QuerySummary `json:"summary"`
}

// FilterFromPreset converts a saved config.LiveLogPreset into a QueryFilter.
func FilterFromPreset(preset config.LiveLogPreset, sessionID int64, excludedUsers []string, limit int) QueryFilter {
	platClean := strings.ToLower(strings.TrimSpace(preset.Platform))
	switch platClean {
	case "", "all", "all platforms":
		platClean = ""
	}

	kindFilter := ""
	switch strings.ToLower(strings.TrimSpace(preset.EventType)) {
	case "chat messages only", "chat":
		kindFilter = "chat"
	case "stream alerts & paid only", "alerts":
		kindFilter = "alerts"
	case "follows", "follow":
		kindFilter = "follow"
	case "subs & giftsubs", "sub":
		kindFilter = "sub"
	case "cheers / super chats / gifts", "cheer":
		kindFilter = "cheer"
	case "raids", "raid":
		kindFilter = "raid"
	case "channel point redemptions", "redemption":
		kindFilter = "redemption"
	case "moderation actions", "moderation":
		kindFilter = "moderation"
	}

	mode := FilterOutcomeAll
	switch strings.ToLower(strings.TrimSpace(preset.Outcome)) {
	case "spoken only", "spoken_only":
		mode = FilterOutcomeSpokenOnly
	case "passed filters (spoken + queued)", "passed_filters":
		mode = FilterOutcomePassedFilters
	case "dropped in queue (stale / panic / skipped)", "dropped_queue":
		mode = FilterOutcomeDroppedQueue
	case "filtered out (bots / rules / thresholds)", "filtered_out":
		mode = FilterOutcomeFilteredOut
	case "moderation actions", "moderation":
		mode = FilterOutcomeModeration
	}

	sid := sessionID
	if strings.EqualFold(strings.TrimSpace(preset.SessionMode), "all") {
		sid = 0
	}
	if limit <= 0 {
		limit = 50
	}

	var ex []string
	if preset.HideBots {
		ex = slices.Clone(excludedUsers)
	}

	return QueryFilter{
		SessionID:     sid,
		Platform:      platClean,
		KindFilter:    kindFilter,
		UserQuery:     strings.TrimSpace(preset.UserQuery),
		TextQuery:     strings.TrimSpace(preset.TextQuery),
		OutcomeMode:   mode,
		HideBots:      preset.HideBots,
		ExcludedUsers: ex,
		Limit:         limit,
	}
}

// EntryMatchesFilter evaluates whether an in-memory Entry satisfies a QueryFilter.
func EntryMatchesFilter(e Entry, f QueryFilter) bool {
	if len(f.SessionIDs) > 0 {
		if !slices.Contains(f.SessionIDs, e.SessionID) {
			return false
		}
	} else if f.SessionID > 0 && e.SessionID != f.SessionID {
		return false
	}
	if f.From != nil && !f.From.IsZero() && e.CreatedAt.Before(*f.From) {
		return false
	}
	if f.To != nil && !f.To.IsZero() && e.CreatedAt.After(*f.To) {
		return false
	}
	if f.Platform != "" && f.Platform != "all" && !strings.EqualFold(e.Platform, f.Platform) {
		return false
	}
	if f.Channel != "" && !strings.EqualFold(f.Channel, "all") && !strings.EqualFold(e.Channel, f.Channel) {
		return false
	}
	kindLower := strings.ToLower(strings.TrimSpace(e.Kind))
	switch strings.ToLower(strings.TrimSpace(f.KindFilter)) {
	case "", "all":
	case "chat", "message":
		if kindLower != "message" {
			return false
		}
	case "alerts":
		if kindLower == "message" || kindLower == "moderation" || kindLower == "" {
			return false
		}
	case "follow":
		if kindLower != "follow" {
			return false
		}
	case "sub":
		switch kindLower {
		case "sub", "resub", "gift_sub", "gift_redemption", "sub_upgrade", "membership", "gift_membership":
		default:
			return false
		}
	case "cheer":
		switch kindLower {
		case "cheer", "bits_badge_tier", "gift", "superchat", "supersticker", "super_chat", "super_sticker", "charity_donation":
		default:
			return false
		}
	case "raid":
		if kindLower != "raid" {
			return false
		}
	case "redemption":
		if kindLower != "redemption" {
			return false
		}
	case "moderation":
		if kindLower != "moderation" {
			return false
		}
	default:
		if !strings.EqualFold(kindLower, strings.TrimSpace(f.KindFilter)) {
			return false
		}
	}
	if f.HideBots && f.Outcome != OutcomeDroppedBot {
		if e.Outcome == OutcomeDroppedBot {
			return false
		}
		for _, r := range e.Roles {
			if strings.EqualFold(strings.TrimSpace(r), "bot") {
				return false
			}
		}
		uName := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e.UserName), "@"))
		uID := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e.UserID), "@"))
		for _, ex := range f.ExcludedUsers {
			exClean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ex), "@"))
			if exClean != "" && (exClean == uName || (uID != "" && exClean == uID)) {
				return false
			}
		}
	}
	if f.UserQuery != "" {
		uq := strings.ToLower(f.UserQuery)
		if !strings.Contains(strings.ToLower(e.UserID), uq) &&
			!strings.Contains(strings.ToLower(e.UserName), uq) &&
			!strings.Contains(strings.ToLower(e.DisplayName), uq) {
			return false
		}
	}
	if f.TextQuery != "" {
		tq := strings.ToLower(f.TextQuery)
		if !strings.Contains(strings.ToLower(e.RawText), tq) &&
			!strings.Contains(strings.ToLower(e.TranslatedText), tq) &&
			!strings.Contains(strings.ToLower(e.SpokenText), tq) &&
			!strings.Contains(strings.ToLower(e.ItemName), tq) &&
			!strings.Contains(strings.ToLower(e.RewardTitle), tq) &&
			!strings.Contains(strings.ToLower(e.Recipient), tq) {
			return false
		}
	}
	switch f.OutcomeMode {
	case FilterOutcomeSpokenOnly:
		return e.Spoken
	case FilterOutcomePassedFilters:
		return e.PassedFilters
	case FilterOutcomeDroppedQueue:
		return e.PassedFilters && !e.Spoken && e.Outcome != OutcomeQueued
	case FilterOutcomeFilteredOut:
		return !e.PassedFilters && e.Outcome != OutcomeModerationEvent
	case FilterOutcomeModeration:
		return e.Outcome == OutcomeModerationEvent || e.Outcome == OutcomeDroppedModeration
	}
	return true
}
