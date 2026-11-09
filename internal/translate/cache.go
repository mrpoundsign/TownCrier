package translate

import (
	"container/list"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mrpoundsign/TownCrier/internal/config"
)

const (
	// DefaultMemoryCacheCapacity is the maximum number of translated sentences held in the L1 in-memory LRU cache.
	DefaultMemoryCacheCapacity = 1000
	// DefaultDiskCacheCapacity is the maximum number of frequency-promoted sentences persisted in L2 disk cache.
	DefaultDiskCacheCapacity = 5000
	// DefaultPromoteHitThreshold is the minimum hit count (count >= 2) required to promote a sentence to L2 disk cache.
	DefaultPromoteHitThreshold = 2
)

// CachedItem represents a single cached sentence or phrase translation in the two-tier L1/L2 cache.
type CachedItem struct {
	Key            string    `json:"key"`
	OriginalText   string    `json:"original_text"`
	TranslatedText string    `json:"translated_text"`
	SourceLang     string    `json:"source_lang"`
	TargetLang     string    `json:"target_lang"`
	Provider       string    `json:"provider"`
	HitCount       int       `json:"hit_count"`
	PromotedToDisk bool      `json:"promoted_to_disk"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type diskCachePayload struct {
	Version int          `json:"version"`
	Items   []CachedItem `json:"items"`
}

// TranslationCache implements a thread-safe two-tier translation cache:
//   - L1: In-memory LRU cache (1,000 entries) capturing all translations for immediate intra-stream spam protection.
//   - L2: Frequency-gated persistent disk cache (up to 5,000 entries in translations.json) storing only phrases seen >= 2 times.
type TranslationCache struct {
	mu          sync.Mutex
	memCap      int
	diskCap     int
	promoteHits int
	diskPath    string
	memOrder    *list.List
	memItems    map[string]*list.Element
	diskItems   map[string]CachedItem
}

// NewTranslationCache creates a new two-tier TranslationCache.
// If diskPath is non-empty, previously promoted L2 entries are loaded from disk.
func NewTranslationCache(diskPath string) *TranslationCache {
	tc := &TranslationCache{
		memCap:      DefaultMemoryCacheCapacity,
		diskCap:     DefaultDiskCacheCapacity,
		promoteHits: DefaultPromoteHitThreshold,
		diskPath:    strings.TrimSpace(diskPath),
		memOrder:    list.New(),
		memItems:    make(map[string]*list.Element, DefaultMemoryCacheCapacity),
		diskItems:   make(map[string]CachedItem),
	}
	if tc.diskPath != "" {
		tc.loadFromDiskLocked()
	}
	return tc
}

// SetDiskPath configures the L2 persistent file path and loads any existing promoted entries.
func (tc *TranslationCache) SetDiskPath(diskPath string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.diskPath = strings.TrimSpace(diskPath)
	if tc.diskPath != "" {
		tc.loadFromDiskLocked()
	}
}

// NormalizeCacheText normalizes a sentence for cache key matching by trimming, collapsing whitespace, and lowercasing.
func NormalizeCacheText(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	return strings.ToLower(strings.Join(fields, " "))
}

// MakeSentenceCacheKey builds a canonical cache key for a (targetLang, sentence) pair.
func MakeSentenceCacheKey(targetLang, text string) string {
	normTarget := config.NormalizeTranslationTargetLang(targetLang)
	return normTarget + "\x00" + NormalizeCacheText(text)
}

// Peek checks whether text is present in the cache for targetLang without modifying LRU order or hit counts.
// If text consists of multiple sentences, Peek also checks if all foreign sentences can be resolved from the cache.
func (tc *TranslationCache) Peek(targetLang, text string) (CachedItem, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return CachedItem{}, false
	}
	normTarget := config.NormalizeTranslationTargetLang(targetLang)
	key := MakeSentenceCacheKey(normTarget, trimmed)

	tc.mu.Lock()
	if elem, ok := tc.memItems[key]; ok {
		item := elem.Value.(CachedItem)
		tc.mu.Unlock()
		return item, true
	}
	if item, ok := tc.diskItems[key]; ok {
		tc.mu.Unlock()
		return item, true
	}
	tc.mu.Unlock()

	// Check if multi-sentence input can be resolved sentence-by-sentence from the cache.
	sents := SplitSentences(trimmed)
	if len(sents) <= 1 {
		return CachedItem{}, false
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()

	resolved := make([]string, len(sents))
	minHits := 0
	allDisk := true
	anyCached := false
	var srcLang, provider string

	for i, s := range sents {
		sKey := MakeSentenceCacheKey(normTarget, s)
		if elem, ok := tc.memItems[sKey]; ok {
			it := elem.Value.(CachedItem)
			resolved[i] = it.TranslatedText
			if resolved[i] == "" {
				resolved[i] = s
			}
			anyCached = true
			if minHits == 0 || it.HitCount < minHits {
				minHits = it.HitCount
			}
			if !it.PromotedToDisk {
				allDisk = false
			}
			if srcLang == "" {
				srcLang = it.SourceLang
			}
			if provider == "" {
				provider = it.Provider
			}
			continue
		}
		if it, ok := tc.diskItems[sKey]; ok {
			resolved[i] = it.TranslatedText
			if resolved[i] == "" {
				resolved[i] = s
			}
			anyCached = true
			if minHits == 0 || it.HitCount < minHits {
				minHits = it.HitCount
			}
			if srcLang == "" {
				srcLang = it.SourceLang
			}
			if provider == "" {
				provider = it.Provider
			}
			continue
		}
		// Not in cache: check if this sentence is already in targetLang
		det := DetectSourceLang(s, normTarget)
		if det == normTarget || !hasTranslatableLetters(s) {
			resolved[i] = s
			continue
		}
		return CachedItem{}, false
	}

	if !anyCached {
		return CachedItem{}, false
	}
	return CachedItem{
		Key:            key,
		OriginalText:   trimmed,
		TranslatedText: strings.Join(resolved, " "),
		SourceLang:     srcLang,
		TargetLang:     normTarget,
		Provider:       provider,
		HitCount:       minHits,
		PromotedToDisk: allDisk,
	}, true
}

// Lookup retrieves a cached translation for (targetLang, text), incrementing its hit count
// and promoting it to L2 disk persistence once HitCount >= 2.
func (tc *TranslationCache) Lookup(targetLang, text string) (CachedItem, bool) {
	key := MakeSentenceCacheKey(targetLang, text)

	tc.mu.Lock()
	var (
		item      CachedItem
		found     bool
		needFlush bool
	)

	if elem, ok := tc.memItems[key]; ok {
		tc.memOrder.MoveToFront(elem)
		item = elem.Value.(CachedItem)
		found = true
	} else if dItem, ok := tc.diskItems[key]; ok {
		item = dItem
		found = true
	}

	if !found {
		tc.mu.Unlock()
		return CachedItem{}, false
	}

	item.HitCount++
	item.UpdatedAt = time.Now().UTC()
	if item.HitCount >= tc.promoteHits && tc.diskPath != "" {
		item.PromotedToDisk = true
		tc.diskItems[key] = item
		tc.evictDiskOverflowLocked()
		needFlush = true
	}
	tc.putMemoryLocked(item)
	if needFlush {
		tc.saveToDiskLocked()
	}
	tc.mu.Unlock()
	return item, true
}

// Store records a translation in the L1 cache. If the sentence already exists in the cache,
// its hit count is incremented and it is promoted to L2 disk cache once HitCount >= 2.
func (tc *TranslationCache) Store(targetLang, text, translatedText, sourceLang, provider string) CachedItem {
	trimmed := strings.TrimSpace(text)
	normTarget := config.NormalizeTranslationTargetLang(targetLang)
	key := MakeSentenceCacheKey(normTarget, trimmed)
	now := time.Now().UTC()

	tc.mu.Lock()
	defer tc.mu.Unlock()

	var existing CachedItem
	var hasExisting bool
	if elem, ok := tc.memItems[key]; ok {
		existing = elem.Value.(CachedItem)
		hasExisting = true
	} else if dItem, ok := tc.diskItems[key]; ok {
		existing = dItem
		hasExisting = true
	}

	hits := 1
	promoted := false
	if hasExisting {
		hits = existing.HitCount + 1
		promoted = existing.PromotedToDisk
	}
	if hits >= tc.promoteHits && tc.diskPath != "" {
		promoted = true
	}

	item := CachedItem{
		Key:            key,
		OriginalText:   trimmed,
		TranslatedText: strings.TrimSpace(translatedText),
		SourceLang:     NormalizeLangCode(sourceLang),
		TargetLang:     normTarget,
		Provider:       config.NormalizeTranslationProvider(provider),
		HitCount:       hits,
		PromotedToDisk: promoted,
		UpdatedAt:      now,
	}

	tc.putMemoryLocked(item)
	if promoted && tc.diskPath != "" {
		tc.diskItems[key] = item
		tc.evictDiskOverflowLocked()
		tc.saveToDiskLocked()
	}
	return item
}

// Stats returns the current number of entries in L1 RAM cache and L2 Disk cache.
func (tc *TranslationCache) Stats() (memoryCount int, diskCount int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.memOrder.Len(), len(tc.diskItems)
}

// Clear removes all entries from both the L1 in-memory cache and L2 persistent disk cache.
func (tc *TranslationCache) Clear() error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.memOrder.Init()
	clear(tc.memItems)
	clear(tc.diskItems)
	if tc.diskPath != "" {
		if err := os.Remove(tc.diskPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (tc *TranslationCache) putMemoryLocked(item CachedItem) {
	if elem, ok := tc.memItems[item.Key]; ok {
		tc.memOrder.MoveToFront(elem)
		elem.Value = item
		return
	}
	if tc.memOrder.Len() >= tc.memCap {
		if back := tc.memOrder.Back(); back != nil {
			old := back.Value.(CachedItem)
			delete(tc.memItems, old.Key)
			tc.memOrder.Remove(back)
		}
	}
	elem := tc.memOrder.PushFront(item)
	tc.memItems[item.Key] = elem
}

func (tc *TranslationCache) evictDiskOverflowLocked() {
	for len(tc.diskItems) > tc.diskCap {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range tc.diskItems {
			if oldestKey == "" || v.UpdatedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.UpdatedAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(tc.diskItems, oldestKey)
	}
}

func (tc *TranslationCache) loadFromDiskLocked() {
	if tc.diskPath == "" {
		return
	}
	data, err := os.ReadFile(tc.diskPath)
	if err != nil || len(data) == 0 {
		return
	}
	var payload diskCachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	for _, item := range payload.Items {
		if item.Key == "" || item.TranslatedText == "" {
			continue
		}
		item.PromotedToDisk = true
		if item.HitCount < tc.promoteHits {
			item.HitCount = tc.promoteHits
		}
		tc.diskItems[item.Key] = item
		tc.putMemoryLocked(item)
	}
}

func (tc *TranslationCache) saveToDiskLocked() {
	if tc.diskPath == "" {
		return
	}
	dir := filepath.Dir(tc.diskPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	items := make([]CachedItem, 0, len(tc.diskItems))
	for _, item := range tc.diskItems {
		items = append(items, item)
	}
	payload := diskCachePayload{
		Version: 1,
		Items:   items,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	tmpPath := tc.diskPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmpPath, tc.diskPath)
}

// SplitSentences splits a chat message into individual sentences while keeping trailing punctuation attached.
// It handles Western (., !, ?) and CJK (。, ！, ？) sentence terminators as well as newlines, while avoiding
// splits inside decimal numbers (e.g. 3.14) or consecutive punctuation clusters (e.g. ..., ?!, !!!).
func SplitSentences(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	runes := []rune(trimmed)
	n := len(runes)
	var out []string
	var buf strings.Builder

	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			out = append(out, s)
		}
		buf.Reset()
	}

	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '\n' || r == '\r' {
			flush()
			continue
		}
		buf.WriteRune(r)

		if !isSentenceTerminator(r) {
			continue
		}

		// Avoid splitting inside decimals or domains like "3.14" or "v1.2"
		if r == '.' && i > 0 && i+1 < n && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) && (unicode.IsLetter(runes[i+1]) || unicode.IsDigit(runes[i+1])) {
			continue
		}

		// Consume any immediately following closing quotes/brackets or repeated sentence terminators (e.g. "!!", "?!", "...", "。」")
		for i+1 < n && (isSentenceTerminator(runes[i+1]) || isClosingQuoteOrBracket(runes[i+1])) {
			i++
			buf.WriteRune(runes[i])
		}

		// For CJK terminators (。, ！, ？), split immediately even without a trailing space.
		// For Western terminators (., !, ?), split when followed by whitespace, opening inverted punctuation (¡, ¿), or end of string.
		if isCJKTerminator(r) || i+1 >= n || unicode.IsSpace(runes[i+1]) || runes[i+1] == '¡' || runes[i+1] == '¿' {
			flush()
		}
	}
	flush()
	return out
}

func isSentenceTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？', '…':
		return true
	default:
		return false
	}
}

func isCJKTerminator(r rune) bool {
	switch r {
	case '。', '！', '？':
		return true
	default:
		return false
	}
}

func isClosingQuoteOrBracket(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', '»', ')', ']', '}', '」', '』', '】':
		return true
	default:
		return false
	}
}

func hasTranslatableLetters(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
