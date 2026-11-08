package tts

import (
	"strings"
)

// DefaultMaxClauseWords is the default maximum number of words per prosodic clause
// passed to a single ONNX Runtime forward pass. Keeping clauses around 10–12 words
// preserves complete sentences for natural prosody while bounding Kokoro-82M / VITS
// intermediate tensor allocations and reducing CPU Time-To-First-Audio.
const DefaultMaxClauseWords = 12

var commonAbbreviations = map[string]struct{}{
	"mr.":   {},
	"mrs.":  {},
	"ms.":   {},
	"dr.":   {},
	"st.":   {},
	"prof.": {},
	"vs.":   {},
	"etc.":  {},
	"e.g.":  {},
	"i.e.":  {},
}

// SplitIntoSentences divides input text into complete sentences on hard punctuation (., !, ?, ;, :)
// without splitting any sentence mid-clause.
func SplitIntoSentences(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var sentences []string
	var curSent []string
	for _, w := range words {
		curSent = append(curSent, w)
		if endsWithHardPunct(w) {
			sentences = append(sentences, strings.Join(curSent, " "))
			curSent = nil
		}
	}
	if len(curSent) > 0 {
		sentences = append(sentences, strings.Join(curSent, " "))
	}
	return sentences
}

func endsWithHardPunct(w string) bool {
	if w == "" {
		return false
	}
	clean := strings.TrimRight(w, "\"')]}»”")
	if clean == "" {
		return false
	}
	if _, isAbbr := commonAbbreviations[strings.ToLower(clean)]; isAbbr {
		return false
	}
	last := clean[len(clean)-1]
	return last == '.' || last == '!' || last == '?' || last == ';' || last == ':'
}
