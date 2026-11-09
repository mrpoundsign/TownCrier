package chat

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var (
	// xWrapperRegex matches symmetric xX_..._Xx or xx...xx gamer wrappers when wrapping at least 2 core characters.
	xWrapperRegex = regexp.MustCompile(`(?i)^x{2,}[_\-.]*([a-z0-9\p{L}].+?)[_\-.]*x{2,}$`)

	// singleXSepWrapperRegex matches x_..._x or X-...-X wrappers when an explicit separator is used on both sides.
	singleXSepWrapperRegex = regexp.MustCompile(`(?i)^x[_\-.]+([a-z0-9\p{L}].+?)[_\-.]+x$`)

	// prefixTagRegex matches leading platform/stream tags followed by an explicit separator (e.g. "TTV_", "YT-", "LIVE_").
	prefixTagRegex = regexp.MustCompile(`(?i)^(?:ttv|tv|yt|live)[_\-.]+(.+)$`)

	// suffixTagRegex matches trailing platform/stream tags preceded by an explicit separator (e.g. "_TV", "_TTV", "-YT", "_Live").
	suffixTagRegex = regexp.MustCompile(`(?i)^(.+?)[_\-.]+(?:ttv|tv|yt|live)$`)

	// attachedTTVPrefixRegex matches a leading "TTV" directly attached to an uppercase letter (e.g. "TTVDarkKnight").
	attachedTTVPrefixRegex = regexp.MustCompile(`^TTV([A-Z][a-zA-Z0-9].*)$`)

	// attachedTTVSuffixRegex matches a trailing "TTV" directly attached to a word of at least 3 characters (e.g. "DarkKnightTTV").
	attachedTTVSuffixRegex = regexp.MustCompile(`^([a-zA-Z0-9]{3,}?)TTV$`)

	// ytAutoHandleSuffixRegex matches YouTube's auto-generated "-x8u" style 3-4 char mixed letter+digit discriminator suffix.
	ytAutoHandleSuffixRegex = regexp.MustCompile(`^(.+?)-([a-z0-9]{3,4})$`)

	// separatorSplitRegex splits handles on underscores, hyphens, dots, and whitespace.
	separatorSplitRegex = regexp.MustCompile(`[_\-.\s]+`)

	// allDigitsRegex matches tokens composed exclusively of ASCII digits.
	allDigitsRegex = regexp.MustCompile(`^\d+$`)
)

// NaturalizeUsername cleans and splits a raw platform username or display name into natural spoken words.
// It strips decorative wrappers (e.g. "xX_..._Xx"), streaming tags ("_TV", "_TTV", "_YT", "_Live"),
// trailing 3+ digit discriminators ("Player9482" -> "Player"), translates basic leet-speak embedded
// inside alphabetic words ("Sn1per" -> "Sniper"), and splits snake_case, kebab-case, and CamelCase
// boundaries ("DarkKnight" -> "Dark Knight").
// If cleaning would produce an empty string (e.g. all-punctuation or degenerate handles), it safely
// falls back to the trimmed original handle.
func NaturalizeUsername(username string) string {
	orig := strings.TrimLeft(strings.TrimSpace(username), "@")
	if orig == "" {
		return ""
	}

	s := strings.Trim(orig, "_-.~| ")
	if s == "" {
		return orig
	}

	// 1. Strip decorative xX_..._Xx or x_..._x wrappers
	if m := xWrapperRegex.FindStringSubmatch(s); len(m) == 2 {
		if inner := strings.Trim(m[1], "_-. "); inner != "" {
			s = inner
		}
	} else if m := singleXSepWrapperRegex.FindStringSubmatch(s); len(m) == 2 {
		if inner := strings.Trim(m[1], "_-. "); inner != "" {
			s = inner
		}
	}

	// 2. Strip streaming prefix/suffix tags (_TV, _TTV, _YT, _Live)
	for {
		changed := false
		if m := suffixTagRegex.FindStringSubmatch(s); len(m) == 2 {
			if inner := strings.Trim(m[1], "_-. "); inner != "" {
				s = inner
				changed = true
			}
		}
		if m := prefixTagRegex.FindStringSubmatch(s); len(m) == 2 {
			if inner := strings.Trim(m[1], "_-. "); inner != "" {
				s = inner
				changed = true
			}
		}
		if m := attachedTTVSuffixRegex.FindStringSubmatch(s); len(m) == 2 {
			if inner := strings.Trim(m[1], "_-. "); inner != "" {
				s = inner
				changed = true
			}
		}
		if m := attachedTTVPrefixRegex.FindStringSubmatch(s); len(m) == 2 {
			if inner := strings.Trim(m[1], "_-. "); inner != "" {
				s = inner
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// 2b. Strip YouTube auto-generated "-x8u" style 3-4 char mixed lowercase letter+digit handle suffix
	if m := ytAutoHandleSuffixRegex.FindStringSubmatch(s); len(m) == 3 {
		core := strings.Trim(m[1], "_-. ")
		suf := m[2]
		if core != "" && containsLetter(core) && containsLetter(suf) && containsDigit(suf) {
			s = core
		}
	}

	// 3. Split on separators (snake_case, kebab-case, dot.case)
	rawTokens := separatorSplitRegex.Split(s, -1)
	tokens := make([]string, 0, len(rawTokens))
	for _, tok := range rawTokens {
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	if len(tokens) == 0 {
		return orig
	}

	// If the final token is a standalone 3+ digit discriminator (e.g. "John_Doe_4829")
	// and at least one earlier token contains letters, drop the trailing discriminator token.
	if len(tokens) > 1 && len(tokens[len(tokens)-1]) >= 3 && allDigitsRegex.MatchString(tokens[len(tokens)-1]) {
		if slices.ContainsFunc(tokens[:len(tokens)-1], containsLetter) {
			tokens = tokens[:len(tokens)-1]
		}
	}

	// 4. Process each token: strip trailing 3+ digit discriminators on the last token,
	// translate internal leet-speak, and split CamelCase / letter-digit boundaries.
	var words []string
	for i, tok := range tokens {
		if i == len(tokens)-1 {
			tok = stripTrailingDiscriminator(tok)
		}
		tok = translateInternalLeet(tok)
		subwords := splitCamelAndDigits(tok)
		for _, w := range subwords {
			if w != "" {
				words = append(words, w)
			}
		}
	}

	if len(words) == 0 {
		return orig
	}
	return strings.Join(words, " ")
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// stripTrailingDiscriminator removes 3+ trailing ASCII digits from a token when preceded by at least 2 characters
// containing at least one letter (e.g. "Player9482" -> "Player", while "12345" -> "12345").
func stripTrailingDiscriminator(tok string) string {
	runes := []rune(tok)
	n := len(runes)
	digitStart := n
	for digitStart > 0 && runes[digitStart-1] >= '0' && runes[digitStart-1] <= '9' {
		digitStart--
	}
	trailingDigits := n - digitStart
	if trailingDigits >= 3 && digitStart >= 2 {
		prefix := string(runes[:digitStart])
		if containsLetter(prefix) {
			return prefix
		}
	}
	return tok
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func leetRune(r rune, upper bool) (rune, bool) {
	var mapped rune
	switch r {
	case '0':
		mapped = 'o'
	case '1':
		mapped = 'i'
	case '3':
		mapped = 'e'
	case '4':
		mapped = 'a'
	case '5':
		mapped = 's'
	case '7':
		mapped = 't'
	default:
		return 0, false
	}
	if upper {
		mapped = unicode.ToUpper(mapped)
	}
	return mapped, true
}

// translateInternalLeet converts sequences of 1-2 leet digits (0, 1, 3, 4, 5, 7) that are flanked
// on both sides by ASCII letters (e.g. "Sn1per" -> "Sniper", "L33t" -> "Leet", "Sh4d0w" -> "Shadow").
func translateInternalLeet(tok string) string {
	runes := []rune(tok)
	n := len(runes)
	if n < 3 {
		return tok
	}

	i := 1
	for i < n-1 {
		if _, ok := leetRune(runes[i], false); !ok {
			i++
			continue
		}
		// Find the run of consecutive leet digits starting at i
		start := i
		end := i
		for end < n && end-start <= 2 {
			if _, ok := leetRune(runes[end], false); ok {
				end++
			} else {
				break
			}
		}
		runLen := end - start
		// Must be 1 or 2 leet digits flanked by ASCII letters on both sides,
		// and not adjacent to other non-leet digits.
		if runLen >= 1 && runLen <= 2 && start > 0 && end < n &&
			isASCIILetter(runes[start-1]) && isASCIILetter(runes[end]) {
			upper := unicode.IsUpper(runes[start-1]) && unicode.IsUpper(runes[end])
			for k := start; k < end; k++ {
				if mapped, ok := leetRune(runes[k], upper); ok {
					runes[k] = mapped
				}
			}
		}
		i = end
	}
	return string(runes)
}

// splitCamelAndDigits splits a token on CamelCase boundaries (e.g. "DarkKnight" -> ["Dark", "Knight"],
// "TTSPlayer" -> ["TTS", "Player"]) and letter-to-digit boundaries ("Knight99" -> ["Knight", "99"]).
// CJK and non-cased Unicode letters are preserved together without false splits.
func splitCamelAndDigits(tok string) []string {
	runes := []rune(tok)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= 2 {
		return []string{tok}
	}

	var out []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		split := false
		switch {
		// 1. Lowercase letter followed by Uppercase letter: "Dark|Knight"
		case unicode.IsLower(prev) && unicode.IsUpper(curr):
			split = true
		// 2. Uppercase acronym followed by Uppercase+Lowercase word: "TTS|Player"
		case unicode.IsUpper(prev) && unicode.IsUpper(curr) && next != 0 && unicode.IsLower(next):
			split = true
		// 3. Letter followed by Digit or Digit followed by Letter: "Knight|99"
		case (unicode.IsLetter(prev) && unicode.IsDigit(curr)) || (unicode.IsDigit(prev) && unicode.IsLetter(curr)):
			split = true
		}

		if split {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	out = append(out, string(runes[start:]))
	return out
}
