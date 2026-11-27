package bot

import (
	"slices"
	"strings"
	"time"
	"unicode"
)

// VoiceCommandMatch describes how a configured BotVoiceCommandDef evaluated against a transcript.
type VoiceCommandMatch struct {
	CommandID   string
	CommandName string
	Phrase      string
	MatchMode   string
	ActionID    string
	ActionName  string
	MatchedText string
	Fired       bool
	SkipReason  string
}

// VoiceTranscriptEvent records a Whisper transcription (or interactive test phrase)
// along with wake-word state and command matching diagnostics.
type VoiceTranscriptEvent struct {
	Timestamp    time.Time
	RawText      string
	Normalized   string
	DetectedLang string
	DecodeMs     int64
	WakeWord     string
	WakeState    string // "", "armed", "same_utterance", "armed_window", "waiting"
	Effective    string
	Matches      []VoiceCommandMatch
	Note         string
}

// NormalizeVoiceText lowercases raw speech text, strips apostrophes and punctuation,
// and collapses runs of whitespace into single spaces.
func NormalizeVoiceText(raw string) string {
	return strings.Join(voiceTokens(raw), " ")
}

func voiceTokens(raw string) []string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range strings.ToLower(raw) {
		switch {
		case r == '\'' || r == '’':
			// Join contractions ("don't" -> "dont", "octo's" -> "octos").
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

func isASCIIShortVowel(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y':
		return true
	default:
		return false
	}
}

// phoneticKey produces a compact consonant/vowel-initial signature so common
// proper-noun STT drifts ("octo" vs "auto" / "otto" / "octa" / "okto") compare closely.
func phoneticKey(word string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(w))
	last := byte(0)
	emit := func(ch byte) {
		if ch != 0 && ch != last {
			b.WriteByte(ch)
			last = ch
		}
	}

	i := 0
	if isASCIIShortVowel(w[0]) {
		emit('A')
		for i < len(w) && isASCIIShortVowel(w[i]) {
			i++
		}
	}

	for i < len(w) {
		rest := w[i:]
		switch {
		case i == 0 && (strings.HasPrefix(rest, "kn") || strings.HasPrefix(rest, "gn")):
			emit('n')
			i += 2
		case i == 0 && strings.HasPrefix(rest, "wr"):
			emit('r')
			i += 2
		case strings.HasPrefix(rest, "tch"):
			emit('x')
			i += 3
		case strings.HasPrefix(rest, "sh") || strings.HasPrefix(rest, "ch"):
			emit('x')
			i += 2
		case strings.HasPrefix(rest, "ph"):
			emit('f')
			i += 2
		case strings.HasPrefix(rest, "th"):
			emit('t')
			i += 2
		case strings.HasPrefix(rest, "wh"):
			emit('w')
			i += 2
		case strings.HasPrefix(rest, "ck"):
			emit('k')
			i += 2
		default:
			c := w[i]
			switch {
			case isASCIIShortVowel(c):
				// Interior vowels are omitted after the leading vowel group.
			case c == 'c':
				if i+1 < len(w) && (w[i+1] == 'e' || w[i+1] == 'i' || w[i+1] == 'y') {
					emit('s')
				} else {
					emit('k')
				}
			case c == 'q':
				emit('k')
			case c == 'z':
				emit('s')
			case c == 'v':
				emit('f')
			default:
				emit(c)
			}
			i++
		}
	}
	return b.String()
}

func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, min(ins, sub))
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func tokensMatchPhonetic(spoken, target string) bool {
	if spoken == "" || target == "" {
		return false
	}
	if spoken == target {
		return true
	}
	keyT := phoneticKey(target)
	keyS := phoneticKey(spoken)
	if len(keyT) >= 2 && keyS == keyT {
		return true
	}
	rawDist := levenshtein(spoken, target)
	if len(target) >= 4 && rawDist <= 1 {
		return true
	}
	if len(target) >= 7 && rawDist <= 2 {
		return true
	}
	if len(target) >= 4 && len(spoken) >= 3 && len(keyT) >= 2 && len(keyS) >= 2 &&
		keyT[0] == keyS[0] && levenshtein(keyS, keyT) <= 1 && rawDist <= 2 {
		return true
	}
	return false
}

func matchTokensContains(spoken, target []string) (bool, string) {
	if len(spoken) == 0 || len(target) == 0 || len(spoken) < len(target) {
		return false, ""
	}
	for i := 0; i <= len(spoken)-len(target); i++ {
		if slices.Equal(spoken[i:i+len(target)], target) {
			return true, strings.Join(spoken[i:i+len(target)], " ")
		}
	}
	return false, ""
}

func matchTokensPhoneticAt(spoken []string, start int, target []string) (consumed int, ok bool) {
	endIdx, matched := matchTokensPhoneticRec(spoken, start, target, 0)
	if !matched {
		return 0, false
	}
	return endIdx - start, true
}

func matchTokensPhoneticRec(spoken []string, si int, target []string, ti int) (int, bool) {
	if ti == len(target) {
		return si, true
	}
	if si >= len(spoken) {
		return 0, false
	}
	want := target[ti]
	if tokensMatchPhonetic(spoken[si], want) {
		if endIdx, ok := matchTokensPhoneticRec(spoken, si+1, target, ti+1); ok {
			return endIdx, true
		}
	}
	if si+1 < len(spoken) && tokensMatchPhonetic(spoken[si]+spoken[si+1], want) {
		if endIdx, ok := matchTokensPhoneticRec(spoken, si+2, target, ti+1); ok {
			return endIdx, true
		}
	}
	return 0, false
}

func matchTokensPhonetic(spoken, target []string) (bool, string) {
	if len(spoken) == 0 || len(target) == 0 {
		return false, ""
	}
	for i := range spoken {
		if consumed, ok := matchTokensPhoneticAt(spoken, i, target); ok {
			return true, strings.Join(spoken[i:i+consumed], " ")
		}
	}
	return false, ""
}

func stripWakeWord(transcript, wakeWord string) (matched bool, remainder string) {
	spoken := voiceTokens(transcript)
	if len(spoken) == 0 {
		return false, ""
	}
	for alias := range strings.SplitSeq(wakeWord, ",") {
		target := voiceTokens(alias)
		if len(target) == 0 {
			continue
		}
		for i := range spoken {
			if consumed, ok := matchTokensPhoneticAt(spoken, i, target); ok {
				return true, strings.Join(spoken[i+consumed:], " ")
			}
		}
	}
	return false, ""
}
