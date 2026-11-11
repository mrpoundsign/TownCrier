package chat

import (
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultCopypastaWindowSeconds is the default sliding window for suppressing duplicate copypasta bursts.
	DefaultCopypastaWindowSeconds = 30

	// MinCopypastaWords is the minimum word count for a message to be evaluated as copypasta.
	MinCopypastaWords = 4

	// MinCopypastaRunes is the minimum normalized character length for a message to be evaluated as copypasta.
	MinCopypastaRunes = 20

	// maxCopypastaEntries bounds memory usage of the sliding-window deduplicator.
	maxCopypastaEntries = 256
)

// CopypastaFilter tracks normalized recent chat messages within a sliding time window
// to suppress multi-user copypasta waves while allowing normal short chat reactions.
type CopypastaFilter struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

// NewCopypastaFilter creates a new thread-safe CopypastaFilter.
func NewCopypastaFilter() *CopypastaFilter {
	return &CopypastaFilter{
		entries: make(map[string]time.Time),
	}
}

// NormalizeCopypastaKey converts a chat message into a canonical lowercase alphanumeric fingerprint
// and reports whether it meets the minimum length/word threshold to be considered copypasta.
func NormalizeCopypastaKey(text string) (string, bool) {
	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(unicode.ToLower(r))
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			// Replace punctuation/symbols with space so "word1/word2" or "hello!!!" normalize cleanly
			b.WriteRune(' ')
		}
	}

	words := strings.Fields(b.String())
	if len(words) == 0 {
		return "", false
	}

	normalized := strings.Join(words, " ")
	if len(words) < MinCopypastaWords && utf8.RuneCountInString(normalized) < MinCopypastaRunes {
		return normalized, false
	}

	return normalized, true
}

// IsDuplicate checks whether the given message text has already been seen within the sliding window
// for the given scope (e.g. platform or slot ID). If it is not a duplicate, it records the message
// timestamp and returns false. If it has already been seen within window, it refreshes the timestamp
// and returns true.
func (f *CopypastaFilter) IsDuplicate(scope, text string, window time.Duration, now time.Time) bool {
	if f == nil || window <= 0 {
		return false
	}

	normKey, eligible := NormalizeCopypastaKey(text)
	if !eligible {
		return false
	}

	if now.IsZero() {
		now = time.Now()
	}

	fullKey := strings.ToLower(strings.TrimSpace(scope)) + "\x00" + normKey

	f.mu.Lock()
	defer f.mu.Unlock()

	// Prune expired entries
	for k, ts := range f.entries {
		if now.Sub(ts) > window {
			delete(f.entries, k)
		}
	}

	if lastSeen, ok := f.entries[fullKey]; ok && now.Sub(lastSeen) <= window {
		f.entries[fullKey] = now
		return true
	}

	// Enforce bounded map capacity by evicting the oldest entry if full
	if len(f.entries) >= maxCopypastaEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, ts := range f.entries {
			if oldestKey == "" || ts.Before(oldestTime) {
				oldestKey = k
				oldestTime = ts
			}
		}
		if oldestKey != "" {
			delete(f.entries, oldestKey)
		}
	}

	f.entries[fullKey] = now
	return false
}

// Reset clears all tracked copypasta fingerprints.
func (f *CopypastaFilter) Reset() {
	if f == nil {
		return
	}
	f.mu.Lock()
	clear(f.entries)
	f.mu.Unlock()
}
