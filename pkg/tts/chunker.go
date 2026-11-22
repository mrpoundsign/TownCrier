package tts

import (
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxClauseWords is the default maximum number of words per prosodic clause
// passed to a single ONNX Runtime forward pass. Keeping clauses around 10–12 words
// preserves complete sentences for natural prosody while bounding Kokoro-82M / VITS
// intermediate tensor allocations and reducing CPU Time-To-First-Audio.
const DefaultMaxClauseWords = 12

const (
	// DefaultPausePunctuationMs is the target silence gap at 1.0x speed for pause punctuation
	// such as em-dashes ("—"), ellipses ("…", "..."), double hyphens ("--"), and spaced en-dashes (" – ").
	DefaultPausePunctuationMs = 220

	minPausePunctuationMs = 80
	maxPausePunctuationMs = 400
	pauseEdgeKeepMs       = 20
	pauseEdgeFadeMs       = 5
	emptyPauseFallbackMs  = 120
)

var (
	// pauseDelimiterRegex matches mid-sentence pause punctuation:
	// em-dashes ("—"), Unicode ellipses ("…"), double+ hyphens ("--"), 2+ dots ("..", "..."),
	// or whitespace-surrounded en-dashes (" – "), collapsing adjacent runs into one break.
	pauseDelimiterRegex = regexp.MustCompile(`(?:\s*(?:—+|…+|-{2,}|\.{2,})\s*|\s+–+\s+)+`)

	// strayDotRegex matches an isolated period preceded by whitespace (e.g. "Wait . what")
	// that espeak-ng would otherwise vocalize as "dot".
	strayDotRegex        = regexp.MustCompile(`(\S)\s+\.(\s|$)`)
	leadingStrayDotRegex = regexp.MustCompile(`^\s*\.(\s+|$)`)
)

const closingQuoteBracketRunes = "\"”’')}]»"

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
	if strings.HasSuffix(clean, "..") {
		return false
	}
	last := clean[len(clean)-1]
	return last == '.' || last == '!' || last == '?' || last == ';' || last == ':'
}

func hasSpeakableContent(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.IsPunct(r) {
			return true
		}
	}
	return false
}

func stripLeadingOrphanClosingPunct(s string) string {
	trimmed := strings.TrimSpace(s)
	i := 0
	for i < len(trimmed) {
		r, sz := utf8.DecodeRuneInString(trimmed[i:])
		if !strings.ContainsRune(closingQuoteBracketRunes, r) {
			break
		}
		i += sz
	}
	if i == 0 {
		return trimmed
	}
	if i == len(trimmed) {
		return ""
	}
	next, _ := utf8.DecodeRuneInString(trimmed[i:])
	if unicode.IsSpace(next) || next == ',' || next == ';' || next == ':' || next == '.' || next == '!' || next == '?' {
		return strings.TrimSpace(trimmed[i:])
	}
	return trimmed
}

// CleanEspeakStrayDots removes space-flanked isolated dots (e.g. "Wait . what" -> "Wait. what",
// ". what" -> "what") that espeak-ng would speak as "dot", while keeping ".net" and "3.14" intact.
func CleanEspeakStrayDots(s string) string {
	for {
		next := strayDotRegex.ReplaceAllString(s, "$1.$2")
		if next == s {
			break
		}
		s = next
	}
	for {
		next := leadingStrayDotRegex.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	return strings.TrimSpace(s)
}

// SplitPauseSegments splits a sentence on mid-sentence pause delimiters (em-dash, ellipsis,
// "--", "..", "...", and spaced en-dash) for espeak-ng model families while skipping pure-punctuation fragments.
func SplitPauseSegments(clause string) []string {
	trimmed := strings.TrimSpace(clause)
	if trimmed == "" {
		return nil
	}
	if !pauseDelimiterRegex.MatchString(trimmed) {
		return []string{trimmed}
	}

	rawParts := pauseDelimiterRegex.Split(trimmed, -1)
	segments := make([]string, 0, len(rawParts))
	for i, part := range rawParts {
		p := strings.TrimSpace(part)
		if i > 0 {
			p = stripLeadingOrphanClosingPunct(p)
		}
		if hasSpeakableContent(p) {
			segments = append(segments, p)
		}
	}
	if len(segments) == 0 {
		return nil
	}
	return segments
}

func ensureContinuationComma(seg string) string {
	trimmed := strings.TrimSpace(seg)
	if trimmed == "" {
		return seg
	}
	core := strings.TrimRight(trimmed, closingQuoteBracketRunes)
	if core == "" {
		return trimmed
	}
	suffix := trimmed[len(core):]
	lastRune, _ := utf8.DecodeLastRuneInString(core)
	switch lastRune {
	case ',', ';', ':', '.', '!', '?':
		return trimmed
	default:
		return core + "," + suffix
	}
}

func extractLeadingClosingPunct(s string) (quote string, rest string, ok bool) {
	i := 0
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		if !strings.ContainsRune(closingQuoteBracketRunes, r) {
			break
		}
		i += sz
	}
	if i == 0 {
		return "", s, false
	}
	if i == len(s) {
		return s, "", true
	}
	next, _ := utf8.DecodeRuneInString(s[i:])
	if unicode.IsSpace(next) {
		return s[:i], strings.TrimSpace(s[i:]), true
	}
	return "", s, false
}

// NormalizeSupertonicPausePunctuation rewrites mid-sentence pause punctuation ("—", "…", "--", "...",
// spaced " – ") into Supertonic's native "... " pause cue in a single synthesis pass, and suppresses
// pure-punctuation utterances that would otherwise trigger Supertonic filler noise.
func NormalizeSupertonicPausePunctuation(clause string) string {
	trimmed := strings.TrimSpace(clause)
	if trimmed == "" {
		return ""
	}
	if !pauseDelimiterRegex.MatchString(trimmed) {
		return trimmed
	}

	matches := pauseDelimiterRegex.FindAllStringIndex(trimmed, -1)
	rawParts := make([]string, 0, len(matches)+1)
	lastIdx := 0
	for _, m := range matches {
		rawParts = append(rawParts, trimmed[lastIdx:m[0]])
		lastIdx = m[1]
	}
	rawParts = append(rawParts, trimmed[lastIdx:])

	speakableIdx := make([]int, 0, len(rawParts))
	for i, part := range rawParts {
		if hasSpeakableContent(part) {
			speakableIdx = append(speakableIdx, i)
		}
	}
	if len(speakableIdx) == 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(len(trimmed) + 8)
	for pos, partIdx := range speakableIdx {
		part := strings.TrimSpace(rawParts[partIdx])
		if pos == 0 {
			b.WriteString(part)
			continue
		}

		// Strip weak trailing punctuation on the left side so we don't emit "Wait,... what"
		curStr := strings.TrimRight(b.String(), " \t\n\r")
		curStr = strings.TrimRight(curStr, ",;:")
		b.Reset()
		b.WriteString(curStr)

		if quote, rest, ok := extractLeadingClosingPunct(part); ok {
			b.WriteString("...")
			b.WriteString(quote)
			if rest != "" {
				b.WriteByte(' ')
				b.WriteString(rest)
			}
		} else {
			b.WriteString("... ")
			b.WriteString(part)
		}
	}

	lastSpeakIdx := speakableIdx[len(speakableIdx)-1]
	if lastSpeakIdx < len(rawParts)-1 {
		trail := strings.TrimSpace(strings.Join(rawParts[lastSpeakIdx+1:], ""))
		curStr := strings.TrimRight(b.String(), " \t\n\r")
		curStr = strings.TrimRight(curStr, ",;:")
		b.Reset()
		b.WriteString(curStr)
		b.WriteString("...")
		if trail != "" {
			b.WriteString(trail)
		}
	}

	return strings.Join(strings.Fields(b.String()), " ")
}

// PauseGapDurationMs computes the target pause gap in milliseconds scaled inversely with speed
// and clamped to [minPausePunctuationMs, maxPausePunctuationMs].
func PauseGapDurationMs(baseMs int, speed float32) int {
	if baseMs <= 0 {
		baseMs = DefaultPausePunctuationMs
	}
	if speed <= 0 {
		speed = 1.0
	}
	ms := int(math.Round(float64(baseMs) / float64(speed)))
	if ms < minPausePunctuationMs {
		return minPausePunctuationMs
	}
	if ms > maxPausePunctuationMs {
		return maxPausePunctuationMs
	}
	return ms
}

func signalPeak(samples []float32) float64 {
	var peak float64
	for _, s := range samples {
		if v := math.Abs(float64(s)); v > peak {
			peak = v
		}
	}
	return peak
}

func windowRMS(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// TrimTrailingSilence trims excess trailing silence from samples while keeping keepMs milliseconds
// of natural tail and applying a short linear fade-out to avoid seam clicks.
func TrimTrailingSilence(samples []float32, sampleRate int, keepMs int) ([]float32, int) {
	if len(samples) == 0 || sampleRate <= 0 {
		return samples, 0
	}
	peak := signalPeak(samples)
	if peak <= 0.002 {
		return samples, len(samples)
	}
	thresh := max(peak*0.015, 0.002)
	win := max(1, sampleRate*5/1000)

	activeEnd := 0
	for end := len(samples); end > 0; end -= win {
		start := max(0, end-win)
		if windowRMS(samples[start:end]) > thresh {
			activeEnd = end
			break
		}
	}
	if activeEnd == 0 {
		return samples, len(samples)
	}

	trailing := len(samples) - activeEnd
	keepSamples := 0
	if keepMs > 0 {
		keepSamples = sampleRate * keepMs / 1000
	}
	if trailing <= keepSamples {
		return samples, trailing
	}

	cut := activeEnd + keepSamples
	out := make([]float32, cut)
	copy(out, samples[:cut])
	fadeSamples := min(sampleRate*pauseEdgeFadeMs/1000, len(out))
	for i := range fadeSamples {
		gain := float32(fadeSamples-1-i) / float32(max(1, fadeSamples))
		out[len(out)-fadeSamples+i] *= gain
	}
	return out, keepSamples
}

// TrimLeadingSilence trims excess leading silence from samples while keeping keepMs milliseconds
// of natural lead-in and applying a short linear fade-in to avoid seam clicks.
func TrimLeadingSilence(samples []float32, sampleRate int, keepMs int) ([]float32, int) {
	if len(samples) == 0 || sampleRate <= 0 {
		return samples, 0
	}
	peak := signalPeak(samples)
	if peak <= 0.002 {
		return samples, len(samples)
	}
	thresh := max(peak*0.015, 0.002)
	win := max(1, sampleRate*5/1000)

	activeStart := len(samples)
	for start := 0; start < len(samples); start += win {
		end := min(len(samples), start+win)
		if windowRMS(samples[start:end]) > thresh {
			activeStart = start
			break
		}
	}
	if activeStart >= len(samples) {
		return samples, len(samples)
	}

	keepSamples := 0
	if keepMs > 0 {
		keepSamples = sampleRate * keepMs / 1000
	}
	if activeStart <= keepSamples {
		return samples, activeStart
	}

	cut := activeStart - keepSamples
	out := make([]float32, len(samples)-cut)
	copy(out, samples[cut:])
	fadeSamples := min(sampleRate*pauseEdgeFadeMs/1000, len(out))
	for i := range fadeSamples {
		gain := float32(i) / float32(max(1, fadeSamples))
		out[i] *= gain
	}
	return out, keepSamples
}

// JoinPauseSegments concatenates synthesized sub-clause segments with seam-trimmed edges
// and calibrated silence padding matching PauseGapDurationMs(basePauseMs, speed).
func JoinPauseSegments(segments [][]float32, sampleRate int, basePauseMs int, speed float32) []float32 {
	if len(segments) == 0 {
		return nil
	}
	if len(segments) == 1 {
		return segments[0]
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}

	targetGapSamples := sampleRate * PauseGapDurationMs(basePauseMs, speed) / 1000

	type trimmedSeg struct {
		samples   []float32
		keptLead  int
		keptTrail int
	}

	trimmed := make([]trimmedSeg, len(segments))
	for k, seg := range segments {
		cur := seg
		var keptLead, keptTrail int
		if k > 0 {
			cur, keptLead = TrimLeadingSilence(cur, sampleRate, pauseEdgeKeepMs)
		}
		if k < len(segments)-1 {
			cur, keptTrail = TrimTrailingSilence(cur, sampleRate, pauseEdgeKeepMs)
		}
		trimmed[k] = trimmedSeg{
			samples:   cur,
			keptLead:  keptLead,
			keptTrail: keptTrail,
		}
	}

	totalLen := 0
	pads := make([]int, len(trimmed))
	for k := range trimmed {
		if k > 0 {
			pad := targetGapSamples - trimmed[k-1].keptTrail - trimmed[k].keptLead
			if pad > 0 {
				pads[k] = pad
				totalLen += pad
			}
		}
		totalLen += len(trimmed[k].samples)
	}

	out := make([]float32, totalLen)
	pos := 0
	for k := range trimmed {
		if pads[k] > 0 {
			pos += pads[k]
		}
		pos += copy(out[pos:], trimmed[k].samples)
	}
	return out
}
