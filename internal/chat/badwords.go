package chat

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed default_badwords.json
var defaultBadWordsJSON []byte

var (
	defaultBadWordsOnce sync.Once
	defaultBadWordsList []string
)

// DefaultBadWords returns a copy of the embedded stream-safe default bad words list.
func DefaultBadWords() []string {
	defaultBadWordsOnce.Do(func() {
		var words []string
		if err := json.Unmarshal(defaultBadWordsJSON, &words); err == nil {
			cleaned := make([]string, 0, len(words))
			for _, w := range words {
				w = strings.ToLower(strings.TrimSpace(w))
				if w != "" {
					cleaned = append(cleaned, w)
				}
			}
			defaultBadWordsList = cleaned
		}
	})
	out := make([]string, len(defaultBadWordsList))
	copy(out, defaultBadWordsList)
	return out
}

// EffectiveBlockedWords combines the embedded default bad words list (when blockDefault is true)
// with any custom user-defined blocked words.
func EffectiveBlockedWords(blockDefault bool, custom []string) []string {
	if !blockDefault {
		return custom
	}
	defaults := DefaultBadWords()
	if len(custom) == 0 {
		return defaults
	}
	combined := make([]string, 0, len(defaults)+len(custom))
	combined = append(combined, defaults...)
	combined = append(combined, custom...)
	return combined
}
