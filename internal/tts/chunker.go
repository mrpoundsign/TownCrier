package tts

import (
	"strings"
)

// DefaultMaxClauseWords is the default maximum number of words per prosodic clause
// passed to a single ONNX Runtime forward pass. Keeping clauses around 10–12 words
// preserves complete sentences for natural prosody while bounding Kokoro-82M / VITS
// intermediate tensor allocations and reducing CPU Time-To-First-Audio.
const DefaultMaxClauseWords = 12

var primaryConjunctions = map[string]struct{}{
	"and":      {},
	"but":      {},
	"because":  {},
	"so":       {},
	"or":       {},
	"when":     {},
	"while":    {},
	"if":       {},
	"where":    {},
	"since":    {},
	"until":    {},
	"although": {},
	"though":   {},
	"unless":   {},
}

var secondaryComplementizers = map[string]struct{}{
	"that":  {},
	"which": {},
	"who":   {},
	"with":  {},
}

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

// SplitIntoClauses divides input text into natural prosodic clauses of at most maxWords words.
//  1. Hard sentence boundaries (., !, ?, ;, :) always split first (even after a single-word
//     greeting like "Hello!"), so adjacent sentences never steal each other's word budget.
//  2. Any sentence of <= maxWords words is kept completely intact as a single clause.
//  3. Sentences longer than maxWords are split using lookahead within [minClause..limit]
//     preferring commas/dashes, then conjunctions (handling compound conjunctions like
//     "even when" / "even if" / "so that" without stranding the first word), and finally
//     balanced word splits that never leave a 1–3 word trailing orphan.
func SplitIntoClauses(text string, maxWords int) []string {
	if maxWords <= 0 {
		maxWords = DefaultMaxClauseWords
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	// Pass 1: Split into sentences on hard punctuation (., !, ?, ;, :)
	var sentences [][]string
	var curSent []string
	for _, w := range words {
		curSent = append(curSent, w)
		if endsWithHardPunct(w) {
			sentences = append(sentences, curSent)
			curSent = nil
		}
	}
	if len(curSent) > 0 {
		sentences = append(sentences, curSent)
	}

	// Pass 2: Split any sentence that exceeds maxWords at the best prosodic boundary
	var clauses []string
	for _, sent := range sentences {
		clauses = append(clauses, splitLongSentence(sent, maxWords)...)
	}
	return clauses
}

func splitLongSentence(words []string, maxWords int) []string {
	if len(words) <= maxWords {
		// Even within maxWords, if the author placed an explicit comma/dash with >= 4 words
		// on both sides, splitting at that comma cuts CPU TTFA nearly in half with natural prosody.
		for k := 4; k <= len(words)-4; k++ {
			if endsWithSoftPunct(words[k-1]) {
				left := strings.Join(words[:k], " ")
				right := splitLongSentence(words[k:], maxWords)
				return append([]string{left}, right...)
			}
		}
		return []string{strings.Join(words, " ")}
	}

	minTail := max(min(4, maxWords/2), 1)

	var out []string
	rem := words

	for len(rem) > maxWords {
		// Ensure we leave at least minTail words for the next clause so we never orphan 1-3 words at the end
		limit := max(min(maxWords, len(rem)-minTail), 1)

		splitLen := chooseProsodicSplit(rem, limit, maxWords)
		out = append(out, strings.Join(rem[:splitLen], " "))
		rem = rem[splitLen:]
	}

	if len(rem) > 0 {
		out = append(out, strings.Join(rem, " "))
	}
	return out
}

// chooseProsodicSplit returns the number of words (1..limit) to include in the current clause.
func chooseProsodicSplit(rem []string, limit int, maxWords int) int {
	minSoft := max(3, min(4, limit))

	// Priority 1: Soft punctuation (comma, em-dash, closing paren) — pick the latest one up to limit
	bestPunct := 0
	for k := minSoft; k <= limit; k++ {
		if endsWithSoftPunct(rem[k-1]) {
			bestPunct = k
		}
	}
	if bestPunct > 0 {
		return bestPunct
	}

	// Priority 2: Coordinating/subordinating conjunction before rem[k]
	minConj := max(4, min(limit, maxWords/2))
	bestConj := 0
	for k := minConj; k <= limit; k++ {
		if k < len(rem) && isCompoundConjunctionStart(rem, k) {
			bestConj = k
		} else if k < len(rem) && isPrimaryConjunction(rem[k]) && !isCompoundModifier(rem[k-1], rem[k]) {
			bestConj = k
		}
	}
	if bestConj > 0 {
		return bestConj
	}

	// Priority 3: Secondary complementizer / preposition (that, which, who, with)
	bestSec := 0
	for k := minConj; k <= limit; k++ {
		if k < len(rem) && isSecondarySplitWord(rem[k]) {
			bestSec = k
		}
	}
	if bestSec > 0 {
		return bestSec
	}

	// Priority 4: Balanced split if only two clauses remain, otherwise limit
	if len(rem) <= maxWords*2 {
		half := (len(rem) + 1) / 2
		if half >= minSoft && half <= limit {
			return half
		}
	}
	return limit
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

func endsWithSoftPunct(w string) bool {
	if w == "" {
		return false
	}
	if strings.HasSuffix(w, "—") || strings.HasSuffix(w, "--") {
		return true
	}
	last := w[len(w)-1]
	return last == ',' || last == ')' || last == ']'
}

func cleanToken(w string) string {
	return strings.ToLower(strings.Trim(w, "\"'([{,.!?;:—"))
}

// isCompoundConjunctionStart detects 2-word conjunctions like "even when", "even if",
// "even though", "so that", "as if", "as though" so we split BEFORE the modifier ("even")
// rather than between "even" and "when".
func isCompoundConjunctionStart(words []string, idx int) bool {
	if idx+1 >= len(words) {
		return false
	}
	w1 := cleanToken(words[idx])
	w2 := cleanToken(words[idx+1])
	if w1 == "even" && (w2 == "when" || w2 == "if" || w2 == "though" || w2 == "after" || w2 == "before") {
		return true
	}
	if w1 == "so" && w2 == "that" {
		return true
	}
	if w1 == "as" && (w2 == "if" || w2 == "though" || w2 == "soon") {
		return true
	}
	return false
}

func isCompoundModifier(prevWord, nextWord string) bool {
	w1 := cleanToken(prevWord)
	w2 := cleanToken(nextWord)
	if w1 == "even" && (w2 == "when" || w2 == "if" || w2 == "though" || w2 == "after" || w2 == "before") {
		return true
	}
	if w1 == "so" && w2 == "that" {
		return true
	}
	if w1 == "as" && (w2 == "if" || w2 == "though") {
		return true
	}
	return false
}

func isPrimaryConjunction(w string) bool {
	_, ok := primaryConjunctions[cleanToken(w)]
	return ok
}

func isSecondarySplitWord(w string) bool {
	_, ok := secondaryComplementizers[cleanToken(w)]
	return ok
}
