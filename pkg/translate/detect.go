package translate

import (
	"slices"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
)

// SupportedLanguage describes a target language option in the UI and configuration.
type SupportedLanguage struct {
	Code string
	Name string
}

// TargetLanguages lists the selectable target languages in display order.
var TargetLanguages = []SupportedLanguage{
	{Code: "en", Name: "English"},
	{Code: "es", Name: "Spanish"},
	{Code: "fr", Name: "French"},
	{Code: "de", Name: "German"},
	{Code: "it", Name: "Italian"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "tl", Name: "Tagalog (Filipino)"},
	{Code: "ja", Name: "Japanese"},
	{Code: "ko", Name: "Korean"},
	{Code: "zh", Name: "Chinese (Mandarin)"},
	{Code: "ru", Name: "Russian"},
	{Code: "hi", Name: "Hindi"},
}

var languageNames = map[string]string{
	"en": "English",
	"es": "Spanish",
	"fr": "French",
	"de": "German",
	"it": "Italian",
	"pt": "Portuguese",
	"ja": "Japanese",
	"ko": "Korean",
	"zh": "Chinese",
	"ru": "Russian",
	"hi": "Hindi",
	"ar": "Arabic",
	"nl": "Dutch",
	"pl": "Polish",
	"tr": "Turkish",
	"vi": "Vietnamese",
	"id": "Indonesian",
	"uk": "Ukrainian",
	"th": "Thai",
	"el": "Greek",
	"he": "Hebrew",
	"sv": "Swedish",
	"da": "Danish",
	"no": "Norwegian",
	"fi": "Finnish",
	"cs": "Czech",
	"ro": "Romanian",
	"hu": "Hungarian",
	"tl": "Tagalog",
}

// LanguageName returns a human-readable display name for an ISO-639-1 language code.
func LanguageName(code string) string {
	clean := NormalizeLangCode(code)
	if name, ok := languageNames[clean]; ok {
		return name
	}
	if clean == "" {
		return "Unknown"
	}
	return strings.ToUpper(clean)
}

// NormalizeLangCode normalizes language tags like "zh-CN", "pt-BR", or "EN" to a 2-letter lowercase code.
func NormalizeLangCode(code string) string {
	clean := strings.ToLower(strings.TrimSpace(code))
	if idx := strings.IndexAny(clean, "-_"); idx > 0 {
		clean = clean[:idx]
	}
	if clean == "iw" {
		return "he"
	}
	return clean
}

// defaultDetectionWhitelist restricts Latin-script trigram matching to major languages
// so short noisy chat messages are not misclassified as obscure languages (e.g. Esperanto).
var defaultDetectionOptions = whatlanggo.Options{
	Whitelist: map[whatlanggo.Lang]bool{
		whatlanggo.Eng: true,
		whatlanggo.Spa: true,
		whatlanggo.Fra: true,
		whatlanggo.Deu: true,
		whatlanggo.Ita: true,
		whatlanggo.Por: true,
		whatlanggo.Nld: true,
		whatlanggo.Pol: true,
		whatlanggo.Tur: true,
		whatlanggo.Vie: true,
		whatlanggo.Ind: true,
		whatlanggo.Swe: true,
		whatlanggo.Dan: true,
		whatlanggo.Nob: true,
		whatlanggo.Fin: true,
		whatlanggo.Ces: true,
		whatlanggo.Ron: true,
		whatlanggo.Hun: true,
		whatlanggo.Tgl: true,
		whatlanggo.Rus: true,
		whatlanggo.Ukr: true,
		whatlanggo.Jpn: true,
		whatlanggo.Cmn: true,
		whatlanggo.Kor: true,
		whatlanggo.Hin: true,
		whatlanggo.Arb: true,
		whatlanggo.Tha: true,
		whatlanggo.Ell: true,
		whatlanggo.Heb: true,
	},
}

// universalChatSlang lists common English/global livestream chat tokens that should not
// trigger Latin-script foreign translation on their own.
var universalChatSlang = map[string]bool{
	"gg": true, "ggs": true, "wp": true, "ggwp": true, "glhf": true,
	"pog": true, "poggers": true, "pogchamp": true, "kappa": true,
	"lul": true, "lulw": true, "omegalul": true, "kek": true, "kekw": true,
	"lol": true, "lmao": true, "lmfao": true, "rofl": true,
	"omg": true, "wtf": true, "wth": true, "brb": true, "afk": true,
	"ty": true, "tysm": true, "thx": true, "np": true, "ez": true,
	"rip": true, "oof": true, "based": true, "cringe": true, "hype": true,
	"w": true, "l": true, "f": true, "hi": true, "hey": true, "hello": true,
	"yo": true, "sup": true, "ok": true, "okay": true, "k": true,
	"nice": true, "yeah": true, "yea": true, "yep": true, "yup": true,
	"no": true, "nope": true, "nah": true, "bruh": true, "bro": true,
	"haha": true, "hahaha": true, "hahahaha": true, "hehe": true, "wow": true,
}

// StripEmoteSpans removes platform emote spans from text and collapses redundant whitespace.
func StripEmoteSpans(text string, spans []chat.EmoteSpan) string {
	if text == "" || len(spans) == 0 {
		return strings.TrimSpace(text)
	}
	runes := []rune(text)
	sorted := slices.Clone(spans)
	slices.SortFunc(sorted, func(a, b chat.EmoteSpan) int {
		return a.Start - b.Start
	})

	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	for _, sp := range sorted {
		start := sp.Start
		end := sp.End
		if start < cursor {
			start = cursor
		}
		if end > len(runes) {
			end = len(runes)
		}
		if start >= end || start >= len(runes) {
			continue
		}
		b.WriteString(string(runes[cursor:start]))
		cursor = end
	}
	if cursor < len(runes) {
		b.WriteString(string(runes[cursor:]))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func cleanProbeText(text string) string {
	tokens := strings.Fields(text)
	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		lower := strings.ToLower(tok)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.") {
			continue
		}
		if strings.HasPrefix(tok, "@") && len(tok) > 1 {
			continue
		}
		kept = append(kept, tok)
	}
	return strings.Join(kept, " ")
}

func isAllUniversalChatSlang(probe string) bool {
	words := strings.FieldsFunc(strings.ToLower(probe), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 {
		return true
	}
	for _, w := range words {
		if !universalChatSlang[w] {
			return false
		}
	}
	return true
}

func isTargetLatinScript(targetLang string) bool {
	switch NormalizeLangCode(targetLang) {
	case "ja", "ko", "zh", "ru", "uk", "hi", "ar", "th", "el", "he":
		return false
	default:
		return true
	}
}

func detectNonLatinISO(probe string) (string, bool) {
	var (
		kanaCount       int
		hanCount        int
		hangulCount     int
		cyrillicCount   int
		arabicCount     int
		devanagariCount int
		thaiCount       int
		greekCount      int
		hebrewCount     int
	)
	for _, r := range probe {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			kanaCount++
		case unicode.Is(unicode.Han, r):
			hanCount++
		case unicode.Is(unicode.Hangul, r):
			hangulCount++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillicCount++
		case unicode.Is(unicode.Arabic, r):
			arabicCount++
		case unicode.Is(unicode.Devanagari, r):
			devanagariCount++
		case unicode.Is(unicode.Thai, r):
			thaiCount++
		case unicode.Is(unicode.Greek, r):
			greekCount++
		case unicode.Is(unicode.Hebrew, r):
			hebrewCount++
		}
	}

	switch {
	case kanaCount > 0:
		return "ja", true
	case hangulCount > 0:
		return "ko", true
	case hanCount > 0:
		// Note: Single-character Japanese slang like "草" (kusa) is Han script;
		// the downstream translator with sl=auto will refine zh vs ja if needed.
		return "zh", true
	case cyrillicCount > 0:
		info := whatlanggo.DetectWithOptions(probe, defaultDetectionOptions)
		if iso := info.Lang.Iso6391(); iso != "" {
			return iso, true
		}
		return "ru", true
	case arabicCount > 0:
		return "ar", true
	case devanagariCount > 0:
		return "hi", true
	case thaiCount > 0:
		return "th", true
	case greekCount > 0:
		return "el", true
	case hebrewCount > 0:
		return "he", true
	default:
		return "", false
	}
}

var isoToWhatlang = map[string]whatlanggo.Lang{
	"en": whatlanggo.Eng,
	"es": whatlanggo.Spa,
	"fr": whatlanggo.Fra,
	"de": whatlanggo.Deu,
	"it": whatlanggo.Ita,
	"pt": whatlanggo.Por,
	"ja": whatlanggo.Jpn,
	"ko": whatlanggo.Kor,
	"zh": whatlanggo.Cmn,
	"ru": whatlanggo.Rus,
	"hi": whatlanggo.Hin,
	"ar": whatlanggo.Arb,
	"nl": whatlanggo.Nld,
	"pl": whatlanggo.Pol,
	"tr": whatlanggo.Tur,
	"vi": whatlanggo.Vie,
	"id": whatlanggo.Ind,
	"uk": whatlanggo.Ukr,
	"th": whatlanggo.Tha,
	"el": whatlanggo.Ell,
	"he": whatlanggo.Heb,
	"tl": whatlanggo.Tgl,
}

func refineLatinISO(probe, iso string) string {
	lower := strings.ToLower(probe)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	for _, w := range words {
		switch w {
		case "kamusta", "kumusta", "magandang", "salamat", "mabuhay", "opo", "kuya", "mga", "naman", "talaga", "kaibigan", "inyong", "gabi", "umaga", "hapon", "walang", "ngayon":
			return "tl"
		}
	}
	for _, w := range words {
		switch w {
		case "the", "and", "you", "that", "this", "with", "have", "are", "was", "what", "when", "where", "who", "why", "how", "from", "they", "would", "could", "should", "about", "there", "their", "testing", "english", "everyone", "guys", "playing", "watching", "awesome", "thanks", "thank", "please", "hello", "world", "today", "tonight", "really", "stream", "game":
			return "en"
		}
	}
	if strings.ContainsAny(lower, "ñ¿¡") {
		return "es"
	}
	if strings.ContainsAny(lower, "ãõç") {
		return "pt"
	}
	for _, w := range words {
		switch w {
		case "hola", "hoy", "muy", "buen", "bueno", "buenos", "buenas", "gracias", "directo", "increíble", "increible", "qué", "hermano", "amigo", "amigos":
			return "es"
		case "olá", "hoje", "muito", "bom", "boa", "obrigado", "obrigada", "você", "voce", "não", "nao", "irmão", "kkkk", "kkkkk":
			return "pt"
		}
	}
	return iso
}

// DetectSentenceLang detects the ISO-639-1 language of an individual sentence within a message.
// Short 1-word continuations (like "Hoy!") that are not universal chat slang or target-language words
// inherit fallbackSourceLang when present.
func DetectSentenceLang(sentence, targetLang, fallbackSourceLang string) string {
	target := NormalizeTargetLang(targetLang)
	fallback := NormalizeLangCode(fallbackSourceLang)
	probe := cleanProbeText(sentence)
	if probe == "" || !hasTranslatableLetters(probe) {
		return target
	}
	if isAllUniversalChatSlang(probe) {
		return target
	}
	if nonLatinISO, hasNonLatin := detectNonLatinISO(probe); hasNonLatin {
		return nonLatinISO
	}
	if hinted := refineLatinISO(probe, ""); hinted != "" {
		return hinted
	}
	shouldTrans, iso, _ := ShouldTranslate(sentence, nil, target, DefaultMinConfidence)
	if shouldTrans && iso != "" {
		return iso
	}
	words := strings.Fields(probe)
	letterCount := 0
	for _, r := range probe {
		if unicode.IsLetter(r) {
			letterCount++
		}
	}
	if len(words) < 2 && letterCount < 8 && fallback != "" && fallback != "auto" && fallback != target {
		return fallback
	}
	return target
}

// DetectSourceLang detects the most likely ISO-639-1 source language code for text relative to targetLang.
func DetectSourceLang(text, targetLang string) string {
	target := NormalizeTargetLang(targetLang)
	shouldTrans, iso, _ := ShouldTranslate(text, nil, target, DefaultMinConfidence)
	if shouldTrans && iso != "" {
		return iso
	}
	return target
}

// ShouldTranslate evaluates whether a chat message is in a foreign language relative to targetLang
// and returns (shouldTranslate, detectedISOLang, cleanTextWithoutEmotes).
func ShouldTranslate(text string, emoteSpans []chat.EmoteSpan, targetLang string, minConfidence float64) (bool, string, string) {
	target := NormalizeTargetLang(targetLang)
	minConf := NormalizeMinConfidence(minConfidence)

	cleanText := StripEmoteSpans(text, emoteSpans)
	probe := cleanProbeText(cleanText)
	if probe == "" {
		return false, "", ""
	}

	letterCount := 0
	latinCount := 0
	for _, r := range probe {
		if unicode.IsLetter(r) {
			letterCount++
			if unicode.Is(unicode.Latin, r) {
				latinCount++
			}
		}
	}
	if letterCount == 0 {
		return false, "", ""
	}

	// 1. Check non-Latin scripts first (works reliably even on 1-character messages like "草" or "안녕")
	if nonLatinISO, hasNonLatin := detectNonLatinISO(probe); hasNonLatin {
		if nonLatinISO == target {
			return false, nonLatinISO, ""
		}
		// Special case: Japanese often uses pure Kanji (Han) without Kana
		if target == "ja" && nonLatinISO == "zh" {
			info := whatlanggo.DetectWithOptions(probe, defaultDetectionOptions)
			if info.Lang == whatlanggo.Jpn {
				return false, "ja", ""
			}
		}
		return true, nonLatinISO, cleanText
	}

	// 2. Probe is Latin script. If target language is non-Latin (e.g. target="ja" and viewer wrote English/Spanish),
	// translate as long as there are at least 2 letters.
	if !isTargetLatinScript(target) && latinCount >= 2 {
		info := whatlanggo.DetectWithOptions(probe, defaultDetectionOptions)
		iso := refineLatinISO(probe, NormalizeLangCode(info.Lang.Iso6391()))
		if iso == "" || iso == target {
			iso = "en"
		}
		return true, iso, cleanText
	}

	// 3. Both message and target language use Latin script (e.g. Spanish/French/Portuguese -> English).
	if isAllUniversalChatSlang(probe) {
		return false, target, ""
	}

	words := strings.Fields(probe)
	if len(words) < 2 && letterCount < 8 {
		return false, "", ""
	}

	info := whatlanggo.DetectWithOptions(probe, defaultDetectionOptions)
	rawISO := NormalizeLangCode(info.Lang.Iso6391())
	iso := refineLatinISO(probe, rawISO)
	if iso == "" || iso == target {
		return false, iso, ""
	}

	// Compute pairwise confidence against the target language so closely related foreign
	// languages (e.g. Spanish vs Portuguese, Tagalog vs Indonesian) do not artificially depress confidence against English.
	conf := info.Confidence
	if iso != rawISO && conf < 0.85 {
		conf = 0.85
	}
	if targetWL, ok := isoToWhatlang[target]; ok && info.Lang != targetWL {
		wl := map[whatlanggo.Lang]bool{
			targetWL:  true,
			info.Lang: true,
		}
		if candWL, okCand := isoToWhatlang[iso]; okCand {
			wl[candWL] = true
		}
		pair := whatlanggo.DetectWithOptions(probe, whatlanggo.Options{
			Whitelist: wl,
		})
		if pair.Lang != targetWL && pair.Confidence > conf {
			conf = pair.Confidence
		}
	}

	if conf < minConf {
		return false, iso, ""
	}

	return true, iso, cleanText
}
