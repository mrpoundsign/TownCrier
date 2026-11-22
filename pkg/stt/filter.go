package stt

import (
	"slices"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
)

// exactHallucinations lists normalized lowercase punctuation-stripped phrases that Whisper
// notoriously hallucinates on silence, sighs, mic clicks, or background fan noise.
var exactHallucinations = map[string]struct{}{
	"thank you":                        {},
	"thank you very much":              {},
	"thank you for watching":           {},
	"thanks for watching":              {},
	"thank you so much for watching":   {},
	"thank you very much for watching": {},
	"please subscribe":                 {},
	"like and subscribe":               {},
	"dont forget to subscribe":         {},
	"bye":                              {},
	"bye bye":                          {},
	"you":                              {},
	"the":                              {},
	"amaraorg":                         {},
	"salamat sa panonood":              {},
	"maraming salamat sa panonood":     {},
	"ご視聴ありがとうございました":                   {},
	"ご視聴ありがとうございます":                    {},
	"チャンネル登録お願いします":                    {},
	"チャンネル登録よろしくお願いします":                 {},
	"おやすみなさい":                          {},
	"字幕":                               {},
	"gracias por ver":                  {},
	"gracias por ver el video":         {},
	"merci davoir regarde":             {},
	"untertitel im auftrag des zdf":    {},
}

var prefixHallucinations = []string{
	"subtitles by ",
	"captions by ",
	"transcribed by ",
	"translated by ",
	"amara.org",
	"www.",
	"http://",
	"https://",
}

var tagalogHintWords = map[string]struct{}{
	"ako": {}, "ikaw": {}, "ka": {}, "siya": {}, "kami": {}, "tayo": {}, "kayo": {}, "sila": {},
	"ko": {}, "mo": {}, "niya": {}, "namin": {}, "natin": {}, "ninyo": {}, "nila": {},
	"ito": {}, "iyan": {}, "iyon": {}, "dito": {}, "diyan": {}, "doon": {},
	"mga": {}, "ang": {}, "ng": {}, "nang": {}, "sa": {}, "kay": {}, "kina": {},
	"po": {}, "opo": {}, "ho": {}, "oho": {}, "ba": {}, "pa": {}, "na": {},
	"lang": {}, "lamang": {}, "muna": {}, "kasi": {}, "naman": {}, "talaga": {},
	"rin": {}, "din": {}, "daw": {}, "raw": {}, "pala": {}, "nga": {}, "yung": {}, "ung": {},
	"hindi": {}, "oo": {}, "wala": {}, "walang": {}, "meron": {}, "mayroon": {},
	"pero": {}, "kaya": {}, "kapag": {}, "kung": {}, "dahil": {}, "para": {},
	"kamusta": {}, "kumusta": {}, "magandang": {}, "salamat": {}, "mabuhay": {},
	"umaga": {}, "hapon": {}, "gabi": {}, "ngayon": {}, "bukas": {}, "kahapon": {},
	"kuya": {}, "ate": {}, "kaibigan": {}, "lahat": {}, "inyong": {}, "atin": {},
	"masaya": {}, "mahal": {}, "laro": {}, "tara": {}, "sandali": {}, "teka": {},
}

// stripAudioAnnotations removes Whisper non-speech bracketed/parenthesized/musical annotations
// such as "[BLANK_AUDIO]", "[Music]", "(laughing)", "*sigh*", and "♪ ... ♪".
func stripAudioAnnotations(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	runes := []rune(s)
	n := len(runes)
	for i := 0; i < n; {
		r := runes[i]
		if r == '[' {
			if end := findClosingRune(runes, i+1, ']'); end >= 0 {
				i = end + 1
				b.WriteRune(' ')
				continue
			}
		}
		if r == '(' {
			if end := findClosingRune(runes, i+1, ')'); end >= 0 {
				inner := strings.ToLower(strings.TrimSpace(string(runes[i+1 : end])))
				if isAnnotationTag(inner) {
					i = end + 1
					b.WriteRune(' ')
					continue
				}
			}
		}
		if r == '*' {
			if end := findClosingRune(runes, i+1, '*'); end > i+1 && end-i <= 24 {
				i = end + 1
				b.WriteRune(' ')
				continue
			}
		}
		if r == '♪' || r == '♫' {
			i++
			for i < n && (runes[i] == '♪' || runes[i] == '♫' || unicode.IsSpace(runes[i])) {
				i++
			}
			// If the entire remaining clause until another note symbol is music lyrics/tags, skip up to matching note
			if end := findClosingMusicNote(runes, i); end >= 0 {
				i = end + 1
			}
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
		i++
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func findClosingRune(runes []rune, start int, target rune) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

func findClosingMusicNote(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == '♪' || runes[i] == '♫' {
			return i
		}
	}
	return -1
}

func isAnnotationTag(inner string) bool {
	if inner == "" {
		return true
	}
	keywords := []string{
		"music", "applause", "laughter", "laughing", "sigh", "sighs", "cough", "coughing",
		"clears throat", "blank_audio", "silence", "noise", "inaudible", "speaking ",
		"foreign", "singing", "upbeat", "cheering", "groans", "gasps", "static", "buzzing",
	}
	for _, kw := range keywords {
		if strings.Contains(inner, kw) {
			return true
		}
	}
	return false
}

func normalizeForHallucinationCheck(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if unicode.IsSpace(r) && !lastSpace {
			b.WriteRune(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// isDegenerateRepetition detects Whisper decoder repetition loops (e.g. "po po po po po"
// or "thank you thank you thank you thank you").
func isDegenerateRepetition(norm string) bool {
	words := strings.Fields(norm)
	if len(words) < 5 {
		return false
	}
	// 1. Check 5+ identical consecutive single words
	consec := 1
	for i := 1; i < len(words); i++ {
		if words[i] == words[i-1] {
			consec++
			if consec >= 5 {
				return true
			}
		} else {
			consec = 1
		}
	}
	// 2. Check dominant single word accounting for >= 80% of 6+ word output
	if len(words) >= 6 {
		counts := make(map[string]int, len(words))
		for _, w := range words {
			counts[w]++
			if counts[w]*5 >= len(words)*4 {
				return true
			}
		}
	}
	// 3. Check 4+ consecutive repeated 2-word or 3-word phrases
	for span := 2; span <= 3; span++ {
		if len(words) < span*4 {
			continue
		}
		repeats := 1
		for i := span; i+span <= len(words); i += span {
			match := true
			for k := range span {
				if words[i+k] != words[i-span+k] {
					match = false
					break
				}
			}
			if match {
				repeats++
				if repeats >= 4 {
					return true
				}
			} else {
				repeats = 1
			}
		}
	}
	return false
}

// CleanTranscript strips non-speech Whisper annotations and rejects empty, punctuation-only,
// repetition-looped, or known silence-hallucinated transcriptions.
func CleanTranscript(raw string) (string, bool) {
	cleaned := stripAudioAnnotations(raw)
	if cleaned == "" {
		return "", false
	}

	hasAlnum := false
	for _, r := range cleaned {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			hasAlnum = true
			break
		}
	}
	if !hasAlnum {
		return "", false
	}

	lowerCleaned := strings.ToLower(cleaned)
	for _, p := range prefixHallucinations {
		if strings.HasPrefix(lowerCleaned, p) || strings.Contains(lowerCleaned, "amara.org") {
			return "", false
		}
	}

	norm := normalizeForHallucinationCheck(cleaned)
	if norm == "" {
		return "", false
	}
	if _, bad := exactHallucinations[norm]; bad {
		return "", false
	}
	if isDegenerateRepetition(norm) {
		return "", false
	}

	return cleaned, true
}

func hasTagalogLexicalSignal(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '-'
	})
	hits := 0
	for _, w := range words {
		clean := strings.Trim(w, "-")
		if clean == "" {
			continue
		}
		if _, ok := tagalogHintWords[clean]; ok {
			hits++
			if hits >= 1 && (clean == "kamusta" || clean == "kumusta" || clean == "salamat" || clean == "magandang" || clean == "mabuhay" || clean == "opo" || clean == "naman" || clean == "talaga" || clean == "muna" || clean == "kasi" || clean == "mga" || clean == "walang" || clean == "hindi") {
				return true
			}
		}
		if strings.HasPrefix(clean, "mag-") || strings.HasPrefix(clean, "nag-") || strings.HasPrefix(clean, "pag-") {
			return true
		}
	}
	return hits >= 2
}

var sttWhatlangByISO = map[string]whatlanggo.Lang{
	"en": whatlanggo.Eng,
	"tl": whatlanggo.Tgl,
	"ja": whatlanggo.Jpn,
	"es": whatlanggo.Spa,
	"fr": whatlanggo.Fra,
	"de": whatlanggo.Deu,
	"it": whatlanggo.Ita,
	"pt": whatlanggo.Por,
	"ko": whatlanggo.Kor,
	"zh": whatlanggo.Cmn,
	"ru": whatlanggo.Rus,
	"hi": whatlanggo.Hin,
	"id": whatlanggo.Ind,
	"vi": whatlanggo.Vie,
	"th": whatlanggo.Tha,
	"ar": whatlanggo.Arb,
	"nl": whatlanggo.Nld,
	"pl": whatlanggo.Pol,
	"tr": whatlanggo.Tur,
	"uk": whatlanggo.Ukr,
}

func hasSpanishLexicalSignal(text string) bool {
	lower := strings.ToLower(text)
	if strings.ContainsAny(lower, "ñ¿¡") {
		return true
	}
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	hits := 0
	for _, w := range words {
		switch w {
		case "hola", "buenos", "buenas", "gracias", "cómo", "estás", "dónde", "qué", "español",
			"hablando", "estoy", "quiero", "puedo", "vamos", "nosotros", "ustedes", "amigo", "amigos":
			return true
		case "como", "estas", "donde", "esta", "está", "muy", "bien", "hoy", "por", "para", "con", "una", "pero", "todo", "todos":
			hits++
		}
	}
	return hits >= 2
}

func detectTextLangInAllowed(text string, allowed []string) string {
	if (len(allowed) == 0 || slices.Contains(allowed, "es")) && hasSpanishLexicalSignal(text) {
		return "es"
	}
	wl := make(map[whatlanggo.Lang]bool, len(allowed))
	if len(allowed) > 0 {
		for _, iso := range allowed {
			if l, ok := sttWhatlangByISO[iso]; ok {
				wl[l] = true
			}
		}
	} else {
		for _, l := range sttWhatlangByISO {
			wl[l] = true
		}
	}
	if len(wl) == 0 {
		return ""
	}
	info := whatlanggo.DetectWithOptions(text, whatlanggo.Options{Whitelist: wl})
	iso := NormalizeSpokenLang(info.Lang.Iso6391())
	if iso != "" && info.Confidence >= 0.35 && (len(allowed) == 0 || slices.Contains(allowed, iso)) {
		return iso
	}
	return ""
}

// ResolveSpokenLang reconciles Whisper's acoustic language tag with script/lexical signals
// and constrains the resolved ISO-639-1 code to the user's configured spokenLangs when applicable.
func ResolveSpokenLang(whisperLang string, text string, spokenLangs []string) string {
	allowed := NormalizeSpokenLanguageList(spokenLangs, nil)
	wLang := NormalizeSpokenLang(whisperLang)

	// 1. Non-Latin script override (unambiguous script evidence in decoded text)
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			return "ja"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		case unicode.Is(unicode.Cyrillic, r):
			if wLang == "uk" {
				return "uk"
			}
			return "ru"
		}
	}

	// 2. Strong Tagalog lexical evidence when Tagalog is among allowed spoken languages (or unrestricted)
	if (len(allowed) == 0 || slices.Contains(allowed, "tl")) && hasTagalogLexicalSignal(text) {
		return "tl"
	}

	// 3. Single configured spoken language lock
	if len(allowed) == 1 {
		return allowed[0]
	}

	// 4. Whisper acoustic language tag if valid and permitted
	if wLang != "" {
		if len(allowed) == 0 || slices.Contains(allowed, wLang) {
			return wLang
		}
	}

	// 5. Text-level language identification constrained to allowed spoken languages (e.g. UI Test phrase or missing Whisper lang tag)
	if det := detectTextLangInAllowed(text, allowed); det != "" {
		return det
	}

	// 6. Fallback to first allowed spoken language (or "en")
	if len(allowed) > 0 {
		return allowed[0]
	}
	return "en"
}
