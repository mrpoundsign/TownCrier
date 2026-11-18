package translate

import (
	"container/list"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ProviderMyMemory       = "mymemory"
	ProviderGoogle         = "google"
	ProviderDeepL          = "deepl"
	ProviderLibreTranslate = "libretranslate"
	ProviderOllama         = "ollama"

	RoutingPriority   = "priority"
	RoutingRoundRobin = "round_robin"

	DefaultTargetLang        = "en"
	DefaultMinConfidence     = 0.65
	DefaultLibreTranslateURL = "http://localhost:5000"
	DefaultOllamaURL         = "http://localhost:11434"
	DefaultOllamaModel       = "qwen2.5:3b"

	defaultCacheCapacity = 256
	defaultHTTPTimeout   = 2500 * time.Millisecond

	// DefaultTimeout is the maximum per-message translation latency before falling back to original text.
	DefaultTimeout = defaultHTTPTimeout
)

// AllProviders is the canonical ordered list of all supported translation provider IDs.
var AllProviders = []string{
	ProviderMyMemory,
	ProviderGoogle,
	ProviderDeepL,
	ProviderLibreTranslate,
	ProviderOllama,
}

// NormalizeRoutingMode returns a canonical routing mode ("priority" or "round_robin").
func NormalizeRoutingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case RoutingRoundRobin, "rr", "round-robin", "roundrobin":
		return RoutingRoundRobin
	default:
		return RoutingPriority
	}
}

// NormalizeProvider returns a canonical translation provider ("mymemory", "google", "deepl", "libretranslate", or "ollama").
func NormalizeProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case ProviderGoogle, "google_cloud":
		return ProviderGoogle
	case ProviderDeepL:
		return ProviderDeepL
	case ProviderLibreTranslate, "libre":
		return ProviderLibreTranslate
	case ProviderOllama, "llm", "openai":
		return ProviderOllama
	default:
		return ProviderMyMemory
	}
}

// NormalizeTargetLang returns a canonical lowercased ISO-639-1 language code, defaulting to "en".
func NormalizeTargetLang(lang string) string {
	clean := NormalizeLangCode(lang)
	switch clean {
	case "en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "ru", "hi", "tl":
		return clean
	default:
		return DefaultTargetLang
	}
}

// NormalizeMinConfidence clamps confidence to [0.1, 0.99], defaulting to 0.65 when <= 0.
func NormalizeMinConfidence(c float64) float64 {
	if c <= 0 {
		return DefaultMinConfidence
	}
	if c < 0.10 {
		return 0.10
	}
	if c > 0.99 {
		return 0.99
	}
	return c
}

// ErrHardQuotaOrAuth indicates a non-transient quota exhaustion or authentication error
// that should immediately place the provider node into maximum (1 hour) backoff.
var ErrHardQuotaOrAuth = errors.New("hard quota or auth error")

func markHardQuotaOrAuth(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrHardQuotaOrAuth, err)
}

var backoffSchedule = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

// ComputeBackoffDuration returns the exponential backoff cooldown for a failing provider node,
// capped at 1 hour (or immediately 1 hour for hard quota / authentication errors).
func ComputeBackoffDuration(consecutiveFails int, hardQuotaOrAuth bool) time.Duration {
	if hardQuotaOrAuth {
		return time.Hour
	}
	if consecutiveFails <= 0 {
		return 0
	}
	idx := consecutiveFails - 1
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	return backoffSchedule[idx]
}

// ProviderStatus tracks live health, latency, and backoff cooldown state for a translation provider node.
type ProviderStatus struct {
	Provider        string
	SuccessCount    int
	FailureCount    int
	ConsecutiveFail int
	LastLatency     time.Duration
	CooldownUntil   time.Time
	LastError       string
}

// InBackoff reports whether the provider node is currently in a backoff cooldown window at time now.
func (ps ProviderStatus) InBackoff(now time.Time) bool {
	return !ps.CooldownUntil.IsZero() && now.Before(ps.CooldownUntil)
}

// SentenceTrace records how an individual sentence in a chat message was resolved
// (from the two-tier sentence cache, skipped as already in target language, or translated via live API).
type SentenceTrace struct {
	Index          int
	Original       string
	Translated     string
	LiveTranslated string
	SourceLang     string
	FromCache      bool
	SkippedTarget  bool
	CacheHits      int
	PromotedToDisk bool
	Provider       string
}

// Result holds the translated text, the detected or confirmed ISO-639-1 source language code,
// the provider ID that fulfilled the translation, whether the result came from cache, and per-sentence traces.
type Result struct {
	TranslatedText string
	SourceLang     string
	Provider       string
	Cached         bool
	Sentences      []SentenceTrace
}

// Options configures a single translation call.
type Options struct {
	Provider       string
	TargetLang     string
	SourceLang     string
	ContactEmail   string
	EndpointURL    string
	Model          string
	APIKey         string
	BypassCache    bool
	ForceTranslate bool // Bypasses single-language target short-circuiting (e.g. for mixed Taglish speech)
}

// MultiTargetItem holds the resolved text for a single target language in a multi-language fan-out.
type MultiTargetItem struct {
	Lang          string `json:"lang"`
	LanguageBadge string `json:"label"`
	LanguageName  string `json:"language_name"`
	Text          string `json:"text"`
	IsOriginal    bool   `json:"is_original"`
	Provider      string `json:"provider,omitempty"`
	Cached        bool   `json:"cached,omitempty"`
	Error         string `json:"error,omitempty"`
}

type cacheEntry struct {
	key string
	val Result
}

// Service coordinates language translation across official MyMemory, Google Cloud, DeepL, LibreTranslate,
// and Local LLM providers with a two-tier sentence cache (L1 RAM LRU + L2 frequency-gated disk),
// cascading priority failover, round-robin load balancing, and per-node exponential backoff.
type Service struct {
	mu             sync.Mutex
	client         *http.Client
	myMemoryURL    string
	googleCloudURL string
	deeplURL       string
	cacheCap       int
	cacheOrder     *list.List
	cacheItems     map[string]*list.Element
	sentenceCache  *TranslationCache
	statuses       map[string]*ProviderStatus
	rrCursor       uint64
	nowFunc        func() time.Time
}

// NewService constructs a translation Service with a two-tier sentence cache and 2.5s default timeout.
func NewService() *Service {
	statuses := make(map[string]*ProviderStatus, len(AllProviders))
	for _, p := range AllProviders {
		statuses[p] = &ProviderStatus{Provider: p}
	}
	return &Service{
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		myMemoryURL:    defaultMyMemoryEndpoint,
		googleCloudURL: defaultGoogleCloudEndpoint,
		cacheCap:       defaultCacheCapacity,
		cacheOrder:     list.New(),
		cacheItems:     make(map[string]*list.Element, defaultCacheCapacity),
		sentenceCache:  NewTranslationCache(""),
		statuses:       statuses,
		nowFunc:        time.Now,
	}
}

// SetDiskCachePath configures the L2 frequency-gated persistent disk cache file path (e.g. translations.json).
func (s *Service) SetDiskCachePath(diskPath string) {
	if s.sentenceCache != nil {
		s.sentenceCache.SetDiskPath(diskPath)
	}
}

// SentenceCache returns the underlying two-tier TranslationCache.
func (s *Service) SentenceCache() *TranslationCache {
	return s.sentenceCache
}

// CacheStats returns the current L1 (RAM) and L2 (Disk) entry counts in the sentence cache.
func (s *Service) CacheStats() (memoryCount int, diskCount int) {
	if s.sentenceCache == nil {
		return 0, 0
	}
	return s.sentenceCache.Stats()
}

// PeekCache inspects whether text is cached for targetLang without modifying hit counts.
func (s *Service) PeekCache(targetLang, text string) (CachedItem, bool) {
	if s.sentenceCache == nil {
		return CachedItem{}, false
	}
	return s.sentenceCache.Peek(targetLang, text)
}

// SetClock overrides the time source used for backoff calculations (used in unit tests).
func (s *Service) SetClock(nowFn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nowFn != nil {
		s.nowFunc = nowFn
	}
}

func (s *Service) now() time.Time {
	s.mu.Lock()
	fn := s.nowFunc
	s.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return time.Now()
}

// ProviderStatus returns a snapshot of the runtime health status for a single provider.
func (s *Service) ProviderStatus(provider string) ProviderStatus {
	p := NormalizeProvider(provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.statuses[p]; ok {
		return *st
	}
	return ProviderStatus{Provider: p}
}

// ProviderStatuses returns a snapshot map of runtime health statuses for all known providers.
func (s *Service) ProviderStatuses() map[string]ProviderStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ProviderStatus, len(s.statuses))
	for k, v := range s.statuses {
		out[k] = *v
	}
	return out
}

// ResetProviderBackoff clears any active backoff cooldown and consecutive failure counter for a provider.
func (s *Service) ResetProviderBackoff(provider string) {
	p := NormalizeProvider(provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.statuses[p]; ok {
		st.ConsecutiveFail = 0
		st.CooldownUntil = time.Time{}
		st.LastError = ""
	}
}

// ResetAllBackoffs clears backoff cooldowns across all providers.
func (s *Service) ResetAllBackoffs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.statuses {
		st.ConsecutiveFail = 0
		st.CooldownUntil = time.Time{}
		st.LastError = ""
	}
}

func (s *Service) recordSuccess(provider string, latency time.Duration) {
	p := NormalizeProvider(provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[p]
	if !ok {
		st = &ProviderStatus{Provider: p}
		s.statuses[p] = st
	}
	st.SuccessCount++
	st.ConsecutiveFail = 0
	st.CooldownUntil = time.Time{}
	st.LastLatency = latency
	st.LastError = ""
}

func (s *Service) recordFailure(provider string, err error) {
	p := NormalizeProvider(provider)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[p]
	if !ok {
		st = &ProviderStatus{Provider: p}
		s.statuses[p] = st
	}
	st.FailureCount++
	st.ConsecutiveFail++
	isHard := errors.Is(err, ErrHardQuotaOrAuth)
	dur := ComputeBackoffDuration(st.ConsecutiveFail, isHard)
	st.CooldownUntil = now.Add(dur)
	if err != nil {
		// Strip the joined sentinel prefix if present so UI error messages remain clean.
		msg := strings.TrimPrefix(err.Error(), ErrHardQuotaOrAuth.Error()+"\n")
		st.LastError = strings.TrimSpace(msg)
	}
}

// SetHTTPClient overrides the HTTP client (used in unit tests).
func (s *Service) SetHTTPClient(c *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c != nil {
		s.client = c
	}
}

// SetCloudEndpoints overrides the MyMemory, Google Cloud v2, and DeepL base URLs (used in unit tests).
func (s *Service) SetCloudEndpoints(myMemoryURL, googleCloudURL, deeplURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if myMemoryURL != "" {
		s.myMemoryURL = myMemoryURL
	}
	if googleCloudURL != "" {
		s.googleCloudURL = googleCloudURL
	}
	if deeplURL != "" {
		s.deeplURL = deeplURL
	}
}

// ClearCache flushes all cached translations from both L1 RAM and L2 disk.
func (s *Service) ClearCache() {
	s.mu.Lock()
	s.cacheOrder.Init()
	clear(s.cacheItems)
	s.mu.Unlock()
	if s.sentenceCache != nil {
		_ = s.sentenceCache.Clear()
	}
}

func makeCacheKey(opts Options, text string) string {
	prov := NormalizeProvider(opts.Provider)
	target := NormalizeTargetLang(opts.TargetLang)
	src := NormalizeLangCode(opts.SourceLang)
	hasKey := "nokey"
	if strings.TrimSpace(opts.APIKey) != "" {
		hasKey = "key"
	}
	return prov + "|" + hasKey + "|" + src + "|" + target + "|" + strings.TrimSpace(opts.ContactEmail) + "|" + strings.TrimSpace(opts.EndpointURL) + "|" + strings.TrimSpace(opts.Model) + "|" + strings.TrimSpace(text)
}

func (s *Service) lookupCache(key string) (Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if elem, ok := s.cacheItems[key]; ok {
		s.cacheOrder.MoveToFront(elem)
		res := elem.Value.(cacheEntry).val
		res.Cached = true
		return res, true
	}
	return Result{}, false
}

func (s *Service) storeCache(key string, res Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if elem, ok := s.cacheItems[key]; ok {
		s.cacheOrder.MoveToFront(elem)
		elem.Value = cacheEntry{key: key, val: res}
		return
	}
	if s.cacheOrder.Len() >= s.cacheCap {
		if back := s.cacheOrder.Back(); back != nil {
			old := back.Value.(cacheEntry)
			delete(s.cacheItems, old.key)
			s.cacheOrder.Remove(back)
		}
	}
	elem := s.cacheOrder.PushFront(cacheEntry{key: key, val: res})
	s.cacheItems[key] = elem
}

// Translate translates text into opts.TargetLang using the configured provider.
// If the source language matches the target language, Translate short-circuits immediately
// without making any network request.
func (s *Service) Translate(ctx context.Context, text string, opts Options) (Result, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Result{}, errors.New("translate text cannot be empty")
	}

	opts.Provider = NormalizeProvider(opts.Provider)
	opts.TargetLang = NormalizeTargetLang(opts.TargetLang)
	opts.SourceLang = NormalizeLangCode(opts.SourceLang)
	if opts.SourceLang == "" || opts.SourceLang == "auto" {
		opts.SourceLang = DetectSourceLang(trimmed, opts.TargetLang)
	}

	// Universal target-language short-circuit: never send a network request to any provider
	// when the message is already in the target language (unless ForceTranslate is set for mixed code-switching).
	if !opts.ForceTranslate && opts.SourceLang != "" && opts.SourceLang == opts.TargetLang {
		return Result{
			TranslatedText: "",
			SourceLang:     opts.TargetLang,
			Provider:       opts.Provider,
			Sentences: []SentenceTrace{
				{
					Index:         1,
					Original:      trimmed,
					Translated:    trimmed,
					SourceLang:    opts.TargetLang,
					SkippedTarget: true,
				},
			},
		}, nil
	}

	cacheKey := makeCacheKey(opts, trimmed)
	if !opts.BypassCache {
		if cached, ok := s.lookupCache(cacheKey); ok {
			return cached, nil
		}
	}

	start := s.now()
	var (
		res Result
		err error
	)
	switch opts.Provider {
	case ProviderGoogle:
		res, err = s.translateGoogle(ctx, trimmed, opts)
	case ProviderDeepL:
		res, err = s.translateDeepL(ctx, trimmed, opts)
	case ProviderLibreTranslate:
		res, err = s.translateLibreTranslate(ctx, trimmed, opts)
	case ProviderOllama:
		res, err = s.translateOllama(ctx, trimmed, opts)
	default:
		res, err = s.translateMyMemory(ctx, trimmed, opts)
	}
	if err != nil {
		s.recordFailure(opts.Provider, err)
		msg := strings.TrimPrefix(err.Error(), ErrHardQuotaOrAuth.Error()+"\n")
		return Result{}, errors.New(strings.TrimSpace(msg))
	}

	latency := max(s.now().Sub(start), 0)
	s.recordSuccess(opts.Provider, latency)

	res.TranslatedText = strings.TrimSpace(res.TranslatedText)
	res.SourceLang = NormalizeLangCode(res.SourceLang)
	if res.SourceLang == "" {
		res.SourceLang = opts.SourceLang
	}
	res.Provider = opts.Provider

	// If the provider detected that the source language is already the target language,
	// or returned identical text, return empty TranslatedText so the caller knows no translation was needed.
	// When ForceTranslate is set (e.g. mixed Taglish speech), keep any non-identical translated text even if
	// the provider guessed the dominant token language matched TargetLang.
	if strings.EqualFold(res.TranslatedText, trimmed) || (!opts.ForceTranslate && res.SourceLang == opts.TargetLang) {
		res.TranslatedText = ""
		res.SourceLang = opts.TargetLang
	}

	s.storeCache(cacheKey, res)
	return res, nil
}

// translateRawChain executes a single (already-prepared) string against the provider chain without sentence splitting.
func (s *Service) translateRawChain(ctx context.Context, text, normTarget, normSource, routingMode string, chain []Options, bypassCache bool) (Result, error) {
	if len(chain) == 0 {
		return Result{}, errors.New("no translation providers are enabled (enable at least one provider node in Settings -> Translation)")
	}

	mode := NormalizeRoutingMode(routingMode)
	n := len(chain)
	startIdx := 0
	if mode == RoutingRoundRobin && n > 1 {
		s.mu.Lock()
		startIdx = int(s.rrCursor % uint64(n))
		s.rrCursor++
		s.mu.Unlock()
	}

	var lastErr error
	var emptyOKRes *Result
	now := s.now()
	attempted := 0

	hasNonDeepL := false
	for _, c := range chain {
		if NormalizeProvider(c.Provider) != ProviderDeepL {
			hasNonDeepL = true
			break
		}
	}

	for offset := range n {
		idx := (startIdx + offset) % n
		cand := chain[idx]
		cand.Provider = NormalizeProvider(cand.Provider)
		cand.TargetLang = normTarget
		cand.SourceLang = normSource
		cand.BypassCache = bypassCache

		// DeepL API v2 does not support Tagalog ("tl") or Hindi ("hi"); skip it when other providers
		// are enabled in the chain so we avoid avoidable HTTP 400 errors and false backoff cooldowns.
		if hasNonDeepL && cand.Provider == ProviderDeepL &&
			(normTarget == "tl" || normTarget == "hi" || normSource == "tl" || normSource == "hi") {
			continue
		}

		st := s.ProviderStatus(cand.Provider)
		if st.InBackoff(now) {
			if lastErr == nil && st.LastError != "" {
				lastErr = errors.New(st.LastError)
			}
			continue
		}

		attempted++
		res, err := s.Translate(ctx, text, cand)
		if err == nil {
			// When ForceTranslate is set (known foreign utterance), if this provider echoed identical untranslated
			// text (e.g. sparse direct pairs on MyMemory like es|tl), try remaining enabled providers first.
			if cand.ForceTranslate && strings.TrimSpace(res.TranslatedText) == "" && n > 1 {
				if emptyOKRes == nil {
					cp := res
					emptyOKRes = &cp
				}
				continue
			}
			return res, nil
		}
		lastErr = err
		now = s.now()
	}

	if emptyOKRes != nil {
		return *emptyOKRes, nil
	}

	if attempted == 0 {
		if lastErr != nil {
			return Result{}, errors.New("all enabled translation providers are in backoff cooldown (last error: " + lastErr.Error() + ")")
		}
		return Result{}, errors.New("all enabled translation providers are currently in backoff cooldown")
	}
	if lastErr != nil {
		return Result{}, lastErr
	}
	return Result{}, errors.New("all enabled translation providers failed")
}

// TranslateChain translates text using sentence splitting, the two-tier sentence cache, and the ordered list of
// enabled provider nodes according to routingMode ("priority" top-to-bottom cascading fallback or "round_robin"
// load balancing), skipping nodes in active backoff.
func (s *Service) TranslateChain(ctx context.Context, text, targetLang, sourceLang, routingMode string, chain []Options) (Result, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Result{}, errors.New("translate text cannot be empty")
	}

	normTarget := NormalizeTargetLang(targetLang)
	normSource := NormalizeLangCode(sourceLang)
	if normSource == "" || normSource == "auto" {
		normSource = DetectSourceLang(trimmed, normTarget)
	}

	sentences := SplitSentences(trimmed)
	if len(sentences) == 0 {
		sentences = []string{trimmed}
	}

	bypassAllCache := len(chain) > 0 && chain[0].BypassCache

	traces := make([]SentenceTrace, len(sentences))
	resolved := make([]string, len(sentences))
	var pendingIndices []int
	var pendingSentences []string
	var pendingLangs []string

	anyTranslated := false
	allFromCache := true
	var primaryLang, primaryProvider string

	for i, sent := range sentences {
		traces[i] = SentenceTrace{
			Index:    i + 1,
			Original: sent,
		}

		if !bypassAllCache && s.sentenceCache != nil {
			if cachedItem, ok := s.sentenceCache.Lookup(normTarget, sent); ok && cachedItem.TranslatedText != "" {
				traces[i].Translated = cachedItem.TranslatedText
				traces[i].SourceLang = cachedItem.SourceLang
				traces[i].FromCache = true
				traces[i].CacheHits = cachedItem.HitCount
				traces[i].PromotedToDisk = cachedItem.PromotedToDisk
				traces[i].Provider = cachedItem.Provider
				resolved[i] = cachedItem.TranslatedText
				anyTranslated = true
				if primaryLang == "" {
					primaryLang = cachedItem.SourceLang
				}
				if primaryProvider == "" {
					primaryProvider = cachedItem.Provider
				}
				continue
			}
		}

		sentLang := DetectSentenceLang(sent, normTarget, normSource)
		if sentLang == "" || sentLang == normTarget || !hasTranslatableLetters(sent) {
			traces[i].Translated = sent
			traces[i].SourceLang = normTarget
			traces[i].SkippedTarget = true
			resolved[i] = sent
			continue
		}

		allFromCache = false
		traces[i].SourceLang = sentLang
		pendingIndices = append(pendingIndices, i)
		pendingSentences = append(pendingSentences, sent)
		pendingLangs = append(pendingLangs, sentLang)
	}

	if len(pendingSentences) == 0 {
		if !anyTranslated {
			return Result{
				TranslatedText: "",
				SourceLang:     normTarget,
				Sentences:      traces,
			}, nil
		}
		if primaryLang == "" {
			primaryLang = normSource
		}
		return Result{
			TranslatedText: strings.Join(resolved, " "),
			SourceLang:     primaryLang,
			Provider:       primaryProvider,
			Cached:         allFromCache,
			Sentences:      traces,
		}, nil
	}

	batchLang := pendingLangs[0]
	for _, l := range pendingLangs[1:] {
		if l != batchLang {
			batchLang = "auto"
			break
		}
	}

	batchInput := strings.Join(pendingSentences, "\n")
	apiRes, err := s.translateRawChain(ctx, batchInput, normTarget, batchLang, routingMode, chain, true)
	if err != nil {
		return Result{}, err
	}

	primaryProvider = apiRes.Provider
	if primaryLang == "" {
		primaryLang = apiRes.SourceLang
		if primaryLang == "" {
			primaryLang = pendingLangs[0]
		}
	}

	if strings.TrimSpace(apiRes.TranslatedText) == "" {
		for k, idx := range pendingIndices {
			resolved[idx] = pendingSentences[k]
			traces[idx].Translated = pendingSentences[k]
			traces[idx].SkippedTarget = true
		}
		if !anyTranslated {
			return Result{
				TranslatedText: "",
				SourceLang:     normTarget,
				Provider:       apiRes.Provider,
				Sentences:      traces,
			}, nil
		}
		return Result{
			TranslatedText: strings.Join(resolved, " "),
			SourceLang:     primaryLang,
			Provider:       primaryProvider,
			Cached:         allFromCache,
			Sentences:      traces,
		}, nil
	}

	rawLines := strings.Split(apiRes.TranslatedText, "\n")
	var transLines []string
	for _, ln := range rawLines {
		t := strings.TrimSpace(ln)
		if t != "" {
			transLines = append(transLines, t)
		}
	}

	if len(transLines) == len(pendingSentences) {
		for k, idx := range pendingIndices {
			origSent := pendingSentences[k]
			transSent := transLines[k]
			sLang := pendingLangs[k]
			if apiRes.SourceLang != "" && apiRes.SourceLang != "auto" && len(pendingSentences) == 1 {
				sLang = apiRes.SourceLang
			}
			resolved[idx] = transSent
			traces[idx].Translated = transSent
			traces[idx].SourceLang = sLang
			traces[idx].Provider = apiRes.Provider
			if !strings.EqualFold(transSent, origSent) {
				anyTranslated = true
			}
			if s.sentenceCache != nil && !strings.EqualFold(transSent, origSent) {
				stored := s.sentenceCache.Store(normTarget, origSent, transSent, sLang, apiRes.Provider)
				traces[idx].CacheHits = stored.HitCount
				traces[idx].PromotedToDisk = stored.PromotedToDisk
			}
		}
	} else {
		// Provider returned a merged translation for the multi-sentence batch; split or assign to the first pending slot.
		splitTrans := SplitSentences(apiRes.TranslatedText)
		if len(splitTrans) == len(pendingSentences) {
			for k, idx := range pendingIndices {
				origSent := pendingSentences[k]
				transSent := splitTrans[k]
				sLang := pendingLangs[k]
				resolved[idx] = transSent
				traces[idx].Translated = transSent
				traces[idx].SourceLang = sLang
				traces[idx].Provider = apiRes.Provider
				if !strings.EqualFold(transSent, origSent) {
					anyTranslated = true
				}
				if s.sentenceCache != nil && !strings.EqualFold(transSent, origSent) {
					stored := s.sentenceCache.Store(normTarget, origSent, transSent, sLang, apiRes.Provider)
					traces[idx].CacheHits = stored.HitCount
					traces[idx].PromotedToDisk = stored.PromotedToDisk
				}
			}
		} else {
			for k, idx := range pendingIndices {
				if k == 0 {
					resolved[idx] = apiRes.TranslatedText
					traces[idx].Translated = apiRes.TranslatedText
				} else {
					resolved[idx] = ""
					traces[idx].Translated = "(merged above)"
				}
				traces[idx].Provider = apiRes.Provider
			}
			anyTranslated = true
		}
	}

	var nonEmptyResolved []string
	for _, r := range resolved {
		if strings.TrimSpace(r) != "" {
			nonEmptyResolved = append(nonEmptyResolved, strings.TrimSpace(r))
		}
	}
	finalText := strings.Join(nonEmptyResolved, " ")
	if !anyTranslated || strings.EqualFold(finalText, trimmed) {
		return Result{
			TranslatedText: "",
			SourceLang:     normTarget,
			Provider:       primaryProvider,
			Sentences:      traces,
		}, nil
	}

	return Result{
		TranslatedText: finalText,
		SourceLang:     primaryLang,
		Provider:       primaryProvider,
		Cached:         false,
		Sentences:      traces,
	}, nil
}

const (
	defaultMultiRESTTimeout   = 3500 * time.Millisecond
	defaultMultiOllamaTimeout = 8000 * time.Millisecond
)

// TranslateMulti translates text concurrently into an ordered list of target languages (e.g. ["en", "es", "ja"]),
// preserving the caller's target order, short-circuiting pure source-language matches with 0 network calls,
// and normalizing bilingual code-switched utterances across all configured target languages.
func (s *Service) TranslateMulti(
	ctx context.Context,
	text string,
	sourceLang string,
	targetLangs []string,
	routingMode string,
	chain []Options,
) ([]MultiTargetItem, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("translate text cannot be empty")
	}

	targets := NormalizeTargetLangList(targetLangs, []string{DefaultTargetLang})
	if len(targets) == 0 {
		return nil, errors.New("no valid target languages configured")
	}

	mixedLang, isCodeSwitched := DetectCodeSwitching(trimmed)
	normSource := NormalizeLangCode(sourceLang)
	if !isCodeSwitched && (normSource == "" || normSource == "auto") {
		normSource = DetectSourceLang(trimmed, DefaultTargetLang)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := defaultMultiRESTTimeout
		for _, opt := range chain {
			p := NormalizeProvider(opt.Provider)
			if p == ProviderOllama || p == ProviderLibreTranslate {
				timeout = defaultMultiOllamaTimeout
				break
			}
		}
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	items := make([]MultiTargetItem, len(targets))
	errs := make([]error, len(targets))

	var wg sync.WaitGroup
	for i, tgt := range targets {
		idx := i
		targetCode := tgt
		wg.Go(func() {
			baseItem := MultiTargetItem{
				Lang:          targetCode,
				LanguageBadge: LanguageBadge(targetCode),
				LanguageName:  LanguageName(targetCode),
			}

			if !isCodeSwitched && normSource != "" && normSource == targetCode {
				baseItem.Text = trimmed
				baseItem.IsOriginal = true
				items[idx] = baseItem
				return
			}

			res, err := s.translateSubtitleTarget(runCtx, trimmed, targetCode, normSource, mixedLang, isCodeSwitched, routingMode, chain)
			if err != nil {
				baseItem.Text = trimmed
				baseItem.IsOriginal = true
				baseItem.Error = err.Error()
				items[idx] = baseItem
				errs[idx] = err
				return
			}
			if strings.TrimSpace(res.TranslatedText) == "" {
				baseItem.Text = trimmed
				baseItem.IsOriginal = true
			} else {
				baseItem.Text = res.TranslatedText
			}
			baseItem.Provider = res.Provider
			baseItem.Cached = res.Cached
			items[idx] = baseItem
		})
	}
	wg.Wait()

	// Return an overall error only when every target that required translation failed.
	var neededCount, failedCount int
	var firstErr error
	for i := range items {
		if errs[i] != nil {
			neededCount++
			failedCount++
			if firstErr == nil {
				firstErr = errs[i]
			}
		} else if !items[i].IsOriginal {
			neededCount++
		}
	}
	if neededCount > 0 && failedCount == neededCount {
		return items, firstErr
	}
	return items, nil
}

func (s *Service) translateSubtitleTarget(
	ctx context.Context,
	trimmed string,
	targetCode string,
	normSource string,
	mixedLang string,
	isCodeSwitched bool,
	routingMode string,
	chain []Options,
) (Result, error) {
	var effSource string
	if isCodeSwitched {
		switch targetCode {
		case "en":
			effSource = mixedLang
		case mixedLang:
			effSource = "en"
		default:
			if normSource != "" && normSource != "auto" && normSource != targetCode {
				effSource = normSource
			} else {
				effSource = mixedLang
			}
		}
	} else {
		effSource = normSource
		if effSource == "" || effSource == "auto" || effSource == targetCode {
			effSource = DetectSourceLang(trimmed, targetCode)
		}
	}

	bypassCache := len(chain) > 0 && chain[0].BypassCache
	if !bypassCache && s.sentenceCache != nil {
		if cached, ok := s.sentenceCache.Lookup(targetCode, trimmed); ok && cached.TranslatedText != "" {
			if cached.SourceLang == "" || cached.SourceLang == effSource {
				return Result{
					TranslatedText: cached.TranslatedText,
					SourceLang:     cached.SourceLang,
					Provider:       cached.Provider,
					Cached:         true,
				}, nil
			}
		}
	}

	forcedChain := make([]Options, len(chain))
	for i, c := range chain {
		c.ForceTranslate = true
		forcedChain[i] = c
	}

	res, err := s.translateRawChain(ctx, trimmed, targetCode, effSource, routingMode, forcedChain, bypassCache)
	// If a direct non-English pair (e.g. es -> tl on free MyMemory) failed or returned identical untranslated text,
	// pivot via English (effSource -> en -> targetCode) so every target language still translates cleanly.
	if (err != nil || strings.TrimSpace(res.TranslatedText) == "") &&
		!isCodeSwitched && effSource != "" && effSource != "en" && targetCode != "en" {
		enRes, enErr := s.translateRawChain(ctx, trimmed, "en", effSource, routingMode, forcedChain, bypassCache)
		if enErr == nil && strings.TrimSpace(enRes.TranslatedText) != "" {
			pivRes, pivErr := s.translateRawChain(ctx, enRes.TranslatedText, targetCode, "en", routingMode, forcedChain, bypassCache)
			if pivErr == nil && strings.TrimSpace(pivRes.TranslatedText) != "" {
				res = pivRes
				res.SourceLang = effSource
				err = nil
			}
		}
	}
	if err != nil {
		return Result{}, err
	}

	if s.sentenceCache != nil && res.TranslatedText != "" && !strings.EqualFold(res.TranslatedText, trimmed) {
		s.sentenceCache.Store(targetCode, trimmed, res.TranslatedText, effSource, res.Provider)
	}
	return res, nil
}

