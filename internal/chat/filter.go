package chat

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	urlRegex        = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
	mixedPunctRegex = regexp.MustCompile(`[!?]{2,}`)
)

const DefaultMaxRunes = 300

// Sanitizer processes incoming message text to remove gibberish, collapse spam, and enforce safety rules.
type Sanitizer struct {
	maxRunes int
}

// NewSanitizer creates a new Sanitizer with the specified max rune limit (default 300 if <= 0).
func NewSanitizer(maxRunes int) *Sanitizer {
	if maxRunes <= 0 {
		maxRunes = DefaultMaxRunes
	}
	return &Sanitizer{
		maxRunes: maxRunes,
	}
}

// Sanitize applies emote handling, URL replacement, spam collapsing, word blocklist, and length clamping.
func (s *Sanitizer) Sanitize(text string, emoteSpans []EmoteSpan, speakEmotes bool, blockedWords []string) string {
	cleaned := text

	// 1. Emote handling
	if !speakEmotes && len(emoteSpans) > 0 {
		cleaned = stripEmoteSpans(cleaned, emoteSpans)
	}

	// 2. URL replacement
	cleaned = urlRegex.ReplaceAllString(cleaned, "link")

	// 3. Repeated punctuation and character spam collapse
	cleaned = collapseRepeatedRunes(cleaned)
	cleaned = mixedPunctRegex.ReplaceAllString(cleaned, "?!")

	// 5. Word blocklist masking
	if len(blockedWords) > 0 {
		cleaned = maskBlockedWords(cleaned, blockedWords)
	}

	// 6. Whitespace cleanup
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	// 7. Max length truncation
	if utf8.RuneCountInString(cleaned) > s.maxRunes {
		runes := []rune(cleaned)
		cleaned = string(runes[:s.maxRunes]) + "…"
	}

	return cleaned
}

// stripEmoteSpans removes emote text by rune offsets in descending order.
func stripEmoteSpans(text string, spans []EmoteSpan) string {
	runes := []rune(text)
	totalRunes := len(runes)

	// Sort spans descending by Start index to preserve offsets during removal
	sorted := make([]EmoteSpan, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start > sorted[j].Start
	})

	for _, span := range sorted {
		if span.Start < 0 || span.End > totalRunes || span.Start >= span.End {
			continue
		}
		// Splice out the emote span
		runes = append(runes[:span.Start], runes[span.End:]...)
		totalRunes = len(runes)
	}

	return string(runes)
}

// maskBlockedWords replaces blocked words with "***" using case-insensitive whole-word matching.
func maskBlockedWords(text string, blockedWords []string) string {
	escaped := make([]string, 0, len(blockedWords))
	for _, word := range blockedWords {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		escaped = append(escaped, regexp.QuoteMeta(word))
	}
	if len(escaped) == 0 {
		return text
	}
	// Sort longest first so longer words/phrases match before shorter prefixes
	sort.Slice(escaped, func(i, j int) bool {
		return len(escaped[i]) > len(escaped[j])
	})
	pattern := `(?i)\b(?:` + strings.Join(escaped, "|") + `)\b`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return text
	}
	return re.ReplaceAllString(text, "***")
}

func isCollapsiblePunct(r rune) bool {
	switch r {
	case '?', '!', '.', ',', ':', ';':
		return true
	default:
		return false
	}
}

// collapseRepeatedRunes collapses repeated punctuation, consonant runs ("wwwww"), and character repetitions.
func collapseRepeatedRunes(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	var last rune
	repeatCount := 0

	for _, r := range text {
		isPunct := isCollapsiblePunct(r)
		lowerR := unicode.ToLower(r)

		if lowerR == unicode.ToLower(last) {
			repeatCount++
		} else {
			last = r
			repeatCount = 1
		}

		// Japanese laughter spam "wwwww" -> collapse to single "w"
		if lowerR == 'w' && repeatCount > 1 {
			continue
		}

		// Punctuation spam "?????" or "....." -> collapse to 1
		if isPunct && repeatCount > 1 {
			continue
		}

		// General character repetition (e.g. "loooooool" -> max 2: "lool")
		if repeatCount > 2 {
			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}
