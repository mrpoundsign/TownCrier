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

	// emojiRegex matches Unicode emojis, regional indicator flags, keycaps, and modifier/ZWJ sequences.
	emojiRegex = regexp.MustCompile(
		`(?:[\x{1F1E6}-\x{1F1FF}]{2}` +
			`|[\x{1F300}-\x{1F5FF}\x{1F600}-\x{1F64F}\x{1F680}-\x{1F6FF}\x{1F900}-\x{1F9FF}\x{1FA70}-\x{1FAFF}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{2300}-\x{23FF}\x{2B50}\x{2B55}\x{1F170}-\x{1F251}]` +
			`(?:\x{FE0F}|\x{FE0E}|[\x{1F3FB}-\x{1F3FF}]|(?:\x{200D}[\x{1F300}-\x{1F5FF}\x{1F600}-\x{1F64F}\x{1F680}-\x{1F6FF}\x{1F900}-\x{1F9FF}\x{1FA70}-\x{1FAFF}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{2300}-\x{23FF}\x{2B50}\x{2B55}\x{1F170}-\x{1F251}](?:\x{FE0F}|\x{FE0E}|[\x{1F3FB}-\x{1F3FF}])?))*` +
			`|[\x{0023}\x{002A}\x{0030}-\x{0039}]\x{FE0F}?\x{20E3})`,
	)
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

// SanitizeOptions defines fine-grained configuration for text sanitization and anti-spam handling.
type SanitizeOptions struct {
	EmoteSpans        []EmoteSpan
	SpeakEmotes       bool
	DeduplicateEmotes bool
	MaxDuplicateWords int
	BlockedWords      []string
}

// SanitizeWithOptions applies emote handling, duplicate word collapsing, spam protection, and safety filtering.
func (s *Sanitizer) SanitizeWithOptions(text string, opts SanitizeOptions) string {
	cleaned := text

	// 1. Emote & Emoji handling
	if !opts.SpeakEmotes {
		if len(opts.EmoteSpans) > 0 {
			cleaned = stripEmoteSpans(cleaned, opts.EmoteSpans)
		}
		cleaned = stripEmojis(cleaned)
	} else if opts.DeduplicateEmotes {
		// Build lookup of platform emote token names from emote spans
		var knownEmotes map[string]bool
		if len(opts.EmoteSpans) > 0 {
			knownEmotes = make(map[string]bool, len(opts.EmoteSpans))
			runes := []rune(text)
			totalRunes := len(runes)
			for _, span := range opts.EmoteSpans {
				if span.Name != "" {
					knownEmotes[strings.ToLower(span.Name)] = true
				} else if span.Start >= 0 && span.End <= totalRunes && span.Start < span.End {
					knownEmotes[strings.ToLower(string(runes[span.Start:span.End]))] = true
				}
			}
		}
		cleaned = deduplicateEmotesAndEmojisPerSentence(cleaned, knownEmotes)
	}

	// 2. URL replacement
	cleaned = urlRegex.ReplaceAllString(cleaned, "link")

	// 3. Consecutive duplicate word collapse
	if opts.MaxDuplicateWords > 0 {
		cleaned = collapseDuplicateWords(cleaned, opts.MaxDuplicateWords)
	}

	// 4. Repeated punctuation and character spam collapse
	cleaned = collapseRepeatedRunes(cleaned)
	cleaned = mixedPunctRegex.ReplaceAllString(cleaned, "?!")

	// 5. Word blocklist masking
	if len(opts.BlockedWords) > 0 {
		cleaned = maskBlockedWords(cleaned, opts.BlockedWords)
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

// Sanitize applies emote handling, URL replacement, spam collapsing, word blocklist, and length clamping
// with default anti-spam rules (deduplicate emotes enabled, max duplicate words = 2).
func (s *Sanitizer) Sanitize(text string, emoteSpans []EmoteSpan, speakEmotes bool, blockedWords []string) string {
	return s.SanitizeWithOptions(text, SanitizeOptions{
		EmoteSpans:        emoteSpans,
		SpeakEmotes:       speakEmotes,
		DeduplicateEmotes: true,
		MaxDuplicateWords: 2,
		BlockedWords:      blockedWords,
	})
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

// stripEmojis removes Unicode emojis from text.
func stripEmojis(text string) string {
	return emojiRegex.ReplaceAllString(text, "")
}

// splitSentences partitions text by sentence terminators (. ! ? ; \n) followed by whitespace or EOF.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	runes := []rune(text)
	inDelim := false

	for i := range runes {
		r := runes[i]
		current.WriteRune(r)
		switch {
		case r == '.' || r == '!' || r == '?' || r == ';' || r == '\n':
			inDelim = true
		case inDelim && unicode.IsSpace(r):
			sentences = append(sentences, current.String())
			current.Reset()
			inDelim = false
		default:
			inDelim = false
		}
	}
	if current.Len() > 0 {
		sentences = append(sentences, current.String())
	}
	return sentences
}

// deduplicateEmotesAndEmojisPerSentence ensures any given emote or emoji is spoken at most once per sentence.
func deduplicateEmotesAndEmojisPerSentence(text string, knownEmotes map[string]bool) string {
	sentences := splitSentences(text)
	processed := make([]string, 0, len(sentences))

	for _, s := range sentences {
		seenInSentence := make(map[string]bool)

		// 1. Deduplicate Unicode emojis within this sentence
		s = emojiRegex.ReplaceAllStringFunc(s, func(emoji string) string {
			if seenInSentence[emoji] {
				return ""
			}
			seenInSentence[emoji] = true
			return emoji
		})

		// 2. Deduplicate platform emotes within this sentence
		if len(knownEmotes) > 0 {
			words := strings.Fields(s)
			var keptWords []string
			for _, w := range words {
				norm := strings.ToLower(strings.Trim(w, ",.!?:;\"'()[]{}~`*-_"))
				if knownEmotes[norm] {
					if seenInSentence[norm] {
						trailing := getSentencePunctuation(w)
						if trailing != "" && len(keptWords) > 0 {
							prev := keptWords[len(keptWords)-1]
							if !strings.HasSuffix(prev, trailing) {
								keptWords[len(keptWords)-1] = strings.TrimRight(prev, ",;:") + trailing
							}
						}
						continue
					}
					seenInSentence[norm] = true
				}
				keptWords = append(keptWords, w)
			}
			s = strings.Join(keptWords, " ")
		}

		processed = append(processed, s)
	}

	return strings.Join(processed, " ")
}

// collapseDuplicateWords collapses consecutive identical words down to maxConsecutive occurrences.
func collapseDuplicateWords(text string, maxConsecutive int) string {
	if maxConsecutive <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) <= maxConsecutive {
		return text
	}

	result := make([]string, 0, len(words))
	var lastNorm string
	repeatCount := 0

	for _, w := range words {
		norm := strings.ToLower(strings.Trim(w, ",.!?:;\"'()[]{}~`*-_"))
		if norm == "" {
			result = append(result, w)
			lastNorm = ""
			repeatCount = 0
			continue
		}

		if norm == lastNorm {
			repeatCount++
			if repeatCount > maxConsecutive {
				trailing := getSentencePunctuation(w)
				if trailing != "" && len(result) > 0 {
					prev := result[len(result)-1]
					if !strings.HasSuffix(prev, trailing) {
						result[len(result)-1] = strings.TrimRight(prev, ",;:") + trailing
					}
				}
				continue
			}
		} else {
			lastNorm = norm
			repeatCount = 1
		}
		result = append(result, w)
	}

	return strings.Join(result, " ")
}

// getSentencePunctuation extracts trailing sentence terminator runes (., !, ?) from a token.
func getSentencePunctuation(w string) string {
	for i := len(w) - 1; i >= 0; i-- {
		r := rune(w[i])
		if r == '!' || r == '?' || r == '.' {
			return w[i:]
		}
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
			break
		}
	}
	return ""
}
