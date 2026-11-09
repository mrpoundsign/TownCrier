package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	defaultPronounsBaseURL   = "https://api.pronouns.alejo.io/v1/users"
	positivePronounCacheTTL  = 30 * 24 * time.Hour
	negativePronounCacheTTL  = 7 * 24 * time.Hour
	maxPronounCacheEntries   = 10000
	pronounNegativeSentinel  = "none"
	maxConcurrentPronounReqs = 2
)

type pronounCacheEntry struct {
	PronounID string `json:"pronoun_id"`
	UpdatedAt int64  `json:"updated_at"`
}

type pronounDiskFile struct {
	Version int                          `json:"version"`
	Entries map[string]pronounCacheEntry `json:"entries"`
}

// PronounCache provides a two-tier (memory + atomic disk JSON) cache for chatter pronoun lookups
// against api.pronouns.alejo.io with positive/negative TTL caching and background concurrency limiting.
type PronounCache struct {
	mu           sync.RWMutex
	entries      map[string]pronounCacheEntry
	inFlight     map[string]struct{}
	diskPath     string
	baseURL      string
	httpClient   *http.Client
	customClient bool
	nowFn        func() time.Time
	sem          chan struct{}
}

// NewPronounCache creates a new two-tier pronoun cache.
func NewPronounCache() *PronounCache {
	return &PronounCache{
		entries:  make(map[string]pronounCacheEntry),
		inFlight: make(map[string]struct{}),
		baseURL:  defaultPronounsBaseURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		nowFn: time.Now,
		sem:   make(chan struct{}, maxConcurrentPronounReqs),
	}
}

// SetHTTPClient overrides the HTTP client and base URL (used for unit testing).
func (c *PronounCache) SetHTTPClient(client *http.Client, baseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client != nil {
		c.httpClient = client
		c.customClient = true
	}
	if strings.TrimSpace(baseURL) != "" {
		c.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		c.customClient = true
	}
}

// SetClock overrides the time source for TTL expiration checks.
func (c *PronounCache) SetClock(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	c.mu.Lock()
	c.nowFn = fn
	c.mu.Unlock()
}

// SetDiskCachePath configures the JSON file path used to persist cached pronoun lookups across restarts.
func (c *PronounCache) SetDiskCachePath(path string) {
	clean := strings.TrimSpace(path)
	c.mu.Lock()
	c.diskPath = clean
	c.mu.Unlock()
	if clean != "" {
		c.loadFromDisk(clean)
	}
}

func normalizePronounKey(userName string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(userName), "@"))
}

// Set stores a pronoun ID (e.g. "sheher", "hehim", "theythem", or "none") for a username and persists to disk.
func (c *PronounCache) Set(userName, pronounID string) {
	key := normalizePronounKey(userName)
	if key == "" {
		return
	}
	pid := strings.ToLower(strings.TrimSpace(pronounID))
	if pid == "" {
		pid = pronounNegativeSentinel
	}
	c.mu.Lock()
	now := c.nowFn().Unix()
	c.entries[key] = pronounCacheEntry{
		PronounID: pid,
		UpdatedAt: now,
	}
	c.evictLocked()
	diskPath := c.diskPath
	c.mu.Unlock()

	if diskPath != "" {
		c.saveToDisk(diskPath)
	}
}

// Size returns the number of cached entries currently in memory.
func (c *PronounCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// LookupGender checks the in-memory pronoun cache for the chatter's preferred gender pool.
// If the chatter is not yet cached (or the entry has expired), it schedules a non-blocking
// background lookup against api.pronouns.alejo.io (for Twitch/Kick handles) and returns (GenderUnknown, false).
func (c *PronounCache) LookupGender(platform, userName string) (VoiceGender, bool) {
	if c == nil {
		return GenderUnknown, false
	}
	key := normalizePronounKey(userName)
	if key == "" {
		return GenderUnknown, false
	}

	c.mu.RLock()
	entry, ok := c.entries[key]
	now := c.nowFn()
	c.mu.RUnlock()

	if ok {
		ttl := positivePronounCacheTTL
		if entry.PronounID == "" || entry.PronounID == pronounNegativeSentinel {
			ttl = negativePronounCacheTTL
		}
		if now.Sub(time.Unix(entry.UpdatedAt, 0)) <= ttl {
			if entry.PronounID == "" || entry.PronounID == pronounNegativeSentinel {
				return GenderUnknown, false
			}
			g := GenderFromPronounID(entry.PronounID)
			return g, g != GenderUnknown
		}
	}

	// Only query external Twitch/Chatterino pronoun registry for plausible alphanumeric/underscore handles
	plat := strings.ToLower(strings.TrimSpace(platform))
	if (plat == "" || plat == "twitch" || plat == "kick") && isValidPronounHandle(key) {
		c.scheduleLookup(key)
	}
	return GenderUnknown, false
}

func isValidPronounHandle(handle string) bool {
	if len(handle) < 2 || len(handle) > 30 {
		return false
	}
	for i := range len(handle) {
		ch := handle[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

func (c *PronounCache) scheduleLookup(key string) {
	c.mu.Lock()
	if testing.Testing() && !c.customClient {
		c.mu.Unlock()
		return
	}
	if _, busy := c.inFlight[key]; busy {
		c.mu.Unlock()
		return
	}
	c.inFlight[key] = struct{}{}
	client := c.httpClient
	baseURL := c.baseURL
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inFlight, key)
			c.mu.Unlock()
		}()

		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		default:
			// Concurrency limit reached during chat burst; skip network call to avoid flooding API
			return
		}

		pronounID, ok := fetchAlejoPronoun(client, baseURL, key)
		if !ok {
			// Transient network failure; do not negative-cache so it can retry later
			return
		}
		c.Set(key, pronounID)
	}()
}

func fetchAlejoPronoun(client *http.Client, baseURL, login string) (string, bool) {
	if client == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	endpoint := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), url.PathEscape(login))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "TownCrier-Hearsay/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return pronounNegativeSentinel, true
	}
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return pronounNegativeSentinel, true
	}

	// Support both array response `[{"id":"...","login":"...","pronoun_id":"sheher"}]`
	// and object response `{"pronoun_id":"sheher","alt_pronoun_id":"theythem"}`
	if strings.HasPrefix(trimmed, "[") {
		var arr []struct {
			PronounID    string `json:"pronoun_id"`
			AltPronounID string `json:"alt_pronoun_id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			if len(arr) == 0 || strings.TrimSpace(arr[0].PronounID) == "" {
				return pronounNegativeSentinel, true
			}
			return combinePronounIDs(arr[0].PronounID, arr[0].AltPronounID), true
		}
	}

	var obj struct {
		PronounID    string `json:"pronoun_id"`
		AltPronounID string `json:"alt_pronoun_id"`
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		if strings.TrimSpace(obj.PronounID) == "" {
			return pronounNegativeSentinel, true
		}
		return combinePronounIDs(obj.PronounID, obj.AltPronounID), true
	}

	return pronounNegativeSentinel, true
}

func combinePronounIDs(primary, alt string) string {
	p := strings.ToLower(strings.TrimSpace(primary))
	a := strings.ToLower(strings.TrimSpace(alt))
	if p == "sheher" && a == "theythem" {
		return "shethey"
	}
	if p == "hehim" && a == "theythem" {
		return "hethey"
	}
	return p
}

// GenderFromPronounID maps an api.pronouns.alejo.io pronoun_id to a VoiceGender.
func GenderFromPronounID(pronounID string) VoiceGender {
	switch strings.ToLower(strings.TrimSpace(pronounID)) {
	case "sheher", "shethey", "fae", "faefaer", "vever", "ziehir":
		return GenderFeminine
	case "hehim", "hethey", "heshe":
		return GenderMasculine
	case "theythem", "shehe", "itits", "any", "other", "ask", "avoid", "xexem", "eyem":
		return GenderNeutral
	default:
		return GenderUnknown
	}
}

func (c *PronounCache) evictLocked() {
	if len(c.entries) <= maxPronounCacheEntries {
		return
	}
	type kv struct {
		key       string
		updatedAt int64
	}
	all := make([]kv, 0, len(c.entries))
	for k, v := range c.entries {
		all = append(all, kv{key: k, updatedAt: v.UpdatedAt})
	}
	slices.SortFunc(all, func(a, b kv) int {
		if a.updatedAt < b.updatedAt {
			return -1
		}
		if a.updatedAt > b.updatedAt {
			return 1
		}
		return strings.Compare(a.key, b.key)
	})
	toRemove := len(c.entries) - maxPronounCacheEntries
	for i := range toRemove {
		delete(c.entries, all[i].key)
	}
}

func (c *PronounCache) loadFromDisk(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var disk pronounDiskFile
	if err := json.Unmarshal(data, &disk); err != nil || len(disk.Entries) == 0 {
		return
	}
	c.mu.Lock()
	for k, v := range disk.Entries {
		norm := normalizePronounKey(k)
		if norm != "" && v.PronounID != "" {
			c.entries[norm] = v
		}
	}
	c.evictLocked()
	c.mu.Unlock()
}

func (c *PronounCache) saveToDisk(path string) {
	c.mu.RLock()
	snapshot := make(map[string]pronounCacheEntry, len(c.entries))
	maps.Copy(snapshot, c.entries)
	c.mu.RUnlock()

	disk := pronounDiskFile{
		Version: 1,
		Entries: snapshot,
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
