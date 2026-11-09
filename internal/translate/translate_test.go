package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

func TestShouldTranslate(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		emotes     []chat.EmoteSpan
		targetLang string
		minConf    float64
		wantTrans  bool
		wantISO    string
	}{
		{
			name:       "english message when target is en",
			text:       "That boss fight was super close!",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  false,
		},
		{
			name:       "short english phrase testing in english",
			text:       "Testing in english",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  false,
		},
		{
			name:       "short gamer slang in english",
			text:       "gg pog lul",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  false,
		},
		{
			name:       "emote-only message",
			text:       "Kappa PogChamp",
			emotes:     []chat.EmoteSpan{{Start: 0, End: 5, Name: "Kappa"}, {Start: 6, End: 14, Name: "PogChamp"}},
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  false,
		},
		{
			name:       "spanish sentence to english",
			text:       "Hola amigos, este directo está increíble hoy!",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  true,
			wantISO:    "es",
		},
		{
			name:       "tagalog greeting to english",
			text:       "Magandang gabi po sa inyong lahat!",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  true,
			wantISO:    "tl",
		},
		{
			name:       "japanese sentence to english",
			text:       "あのボス戦めっちゃギリギリだった！",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  true,
			wantISO:    "ja",
		},
		{
			name:       "single character japanese kusa to english",
			text:       "草",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  true,
		},
		{
			name:       "russian greeting to english",
			text:       "Привет всем в чате!",
			targetLang: "en",
			minConf:    0.65,
			wantTrans:  true,
			wantISO:    "ru",
		},
		{
			name:       "japanese message when target is ja",
			text:       "こんにちは、配信楽しみにしています",
			targetLang: "ja",
			minConf:    0.65,
			wantTrans:  false,
		},
		{
			name:       "english message when target is ja",
			text:       "Hello everyone!",
			targetLang: "ja",
			minConf:    0.65,
			wantTrans:  true,
			wantISO:    "en",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTrans, gotISO, _ := ShouldTranslate(tc.text, tc.emotes, tc.targetLang, tc.minConf)
			if gotTrans != tc.wantTrans {
				t.Fatalf("ShouldTranslate(%q, target=%q) = %v (iso=%q), want %v", tc.text, tc.targetLang, gotTrans, gotISO, tc.wantTrans)
			}
			if tc.wantTrans && tc.wantISO != "" && gotISO != tc.wantISO {
				t.Fatalf("ShouldTranslate(%q) iso = %q, want %q", tc.text, gotISO, tc.wantISO)
			}
		})
	}
	if got := DetectSourceLang("Testing in english", "en"); got != "en" {
		t.Fatalf("DetectSourceLang('Testing in english', 'en') = %q, want 'en'", got)
	}
}

func TestMyMemoryGoogleCloudDeepLAndCache(t *testing.T) {
	var myMemoryHits atomic.Int32
	var cloudHits atomic.Int32
	var deeplHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mymemory":
			myMemoryHits.Add(1)
			if gotPair := r.URL.Query().Get("langpair"); gotPair != "es|en" {
				http.Error(w, "unexpected langpair: "+gotPair, http.StatusBadRequest)
				return
			}
			if gotEmail := r.URL.Query().Get("de"); gotEmail != "streamer@example.com" {
				http.Error(w, "unexpected email: "+gotEmail, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"responseData":{"translatedText":"Hello friends!","match":1.0},"quotaFinished":false,"responseStatus":200}`))
		case "/cloud":
			cloudHits.Add(1)
			if gotKey := r.Header.Get("X-Goog-Api-Key"); gotKey != "test-cloud-key" {
				http.Error(w, "missing key", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"That boss fight was super close!","detectedSourceLanguage":"ja"}]}}`))
		case "/deepl":
			deeplHits.Add(1)
			if gotAuth := r.Header.Get("Authorization"); gotAuth != "DeepL-Auth-Key test-deepl-key:fx" {
				http.Error(w, "missing deepl auth", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"translations":[{"detected_source_language":"TL","text":"Good evening to all of you!"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc := NewService()
	svc.SetHTTPClient(srv.Client())
	svc.SetCloudEndpoints(srv.URL+"/mymemory", srv.URL+"/cloud", srv.URL+"/deepl")

	// 1. Official MyMemory Free mode (default zero-setup, auto-detects source lang via DetectSourceLang)
	res1, err := svc.Translate(context.Background(), "¡Hola amigos!", Options{
		Provider:     config.TranslationProviderMyMemory,
		TargetLang:   "en",
		ContactEmail: "streamer@example.com",
	})
	if err != nil {
		t.Fatalf("MyMemory Translate error: %v", err)
	}
	if res1.TranslatedText != "Hello friends!" || res1.SourceLang != "es" {
		t.Fatalf("unexpected MyMemory result: %+v", res1)
	}

	// Second call with same input should hit LRU cache
	res2, err := svc.Translate(context.Background(), "¡Hola amigos!", Options{
		Provider:     config.TranslationProviderMyMemory,
		TargetLang:   "en",
		ContactEmail: "streamer@example.com",
	})
	if err != nil {
		t.Fatalf("cached MyMemory Translate error: %v", err)
	}
	if res2.TranslatedText != "Hello friends!" || myMemoryHits.Load() != 1 {
		t.Fatalf("expected 1 MyMemory HTTP hit (cached), got %d", myMemoryHits.Load())
	}

	// 2. Official Google Cloud v2 mode requires API key
	if _, err := svc.Translate(context.Background(), "あのボス戦めっちゃギリギリだった！", Options{
		Provider:   config.TranslationProviderGoogle,
		TargetLang: "en",
	}); err == nil {
		t.Fatal("expected error when calling Google Cloud v2 without an API key")
	}

	resCloud, err := svc.Translate(context.Background(), "あのボス戦めっちゃギリギリだった！", Options{
		Provider:   config.TranslationProviderGoogle,
		TargetLang: "en",
		APIKey:     "test-cloud-key",
	})
	if err != nil {
		t.Fatalf("Cloud v2 Translate error: %v", err)
	}
	if resCloud.TranslatedText != "That boss fight was super close!" || resCloud.SourceLang != "ja" || cloudHits.Load() != 1 {
		t.Fatalf("unexpected Cloud v2 result: %+v (hits=%d)", resCloud, cloudHits.Load())
	}

	// 3. Official DeepL API Free/Pro mode
	resDeepL, err := svc.Translate(context.Background(), "Magandang gabi po sa inyong lahat!", Options{
		Provider:   config.TranslationProviderDeepL,
		TargetLang: "en",
		APIKey:     "test-deepl-key:fx",
	})
	if err != nil {
		t.Fatalf("DeepL Translate error: %v", err)
	}
	if resDeepL.TranslatedText != "Good evening to all of you!" || resDeepL.SourceLang != "tl" || deeplHits.Load() != 1 {
		t.Fatalf("unexpected DeepL result: %+v (hits=%d)", resDeepL, deeplHits.Load())
	}
}

func TestLibreTranslateAndOllamaProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/translate":
			var req libreTranslateRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.APIKey != "lt-secret" {
				http.Error(w, `{"error":"invalid api key"}`, http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"translatedText":"Good game everyone","detectedLanguage":{"language":"pt","confidence":0.95}}`))
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer ollama-secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"\"LOL that was hilarious\""}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc := NewService()
	svc.SetHTTPClient(srv.Client())

	resLT, err := svc.Translate(context.Background(), "Bom jogo pessoal", Options{
		Provider:    config.TranslationProviderLibreTranslate,
		TargetLang:  "en",
		EndpointURL: srv.URL,
		APIKey:      "lt-secret",
	})
	if err != nil {
		t.Fatalf("LibreTranslate error: %v", err)
	}
	if resLT.TranslatedText != "Good game everyone" || resLT.SourceLang != "pt" {
		t.Fatalf("unexpected LibreTranslate result: %+v", resLT)
	}

	resLLM, err := svc.Translate(context.Background(), "草めっちゃ笑った", Options{
		Provider:    config.TranslationProviderOllama,
		TargetLang:  "en",
		SourceLang:  "ja",
		EndpointURL: srv.URL,
		Model:       "qwen2.5:3b",
		APIKey:      "ollama-secret",
	})
	if err != nil {
		t.Fatalf("Ollama Translate error: %v", err)
	}
	if resLLM.TranslatedText != "LOL that was hilarious" || resLLM.SourceLang != "ja" {
		t.Fatalf("unexpected Ollama result: %+v", resLLM)
	}
}

func TestUniversalTargetLanguageShortCircuit(t *testing.T) {
	var totalHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		totalHits.Add(1)
		http.Error(w, "should not be called when text is already in target language", http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc := NewService()
	svc.SetHTTPClient(srv.Client())
	svc.SetCloudEndpoints(srv.URL+"/mymemory", srv.URL+"/cloud", srv.URL+"/deepl")

	for _, prov := range config.AllTranslationProviders {
		res, err := svc.Translate(context.Background(), "Testing in english", Options{
			Provider:    prov,
			TargetLang:  "en",
			EndpointURL: srv.URL,
			APIKey:      "dummy-key",
		})
		if err != nil {
			t.Fatalf("Translate(%q) unexpected error: %v", prov, err)
		}
		if res.TranslatedText != "" || res.SourceLang != "en" {
			t.Fatalf("Translate(%q) expected empty TranslatedText and SourceLang='en', got %+v", prov, res)
		}
	}
	if totalHits.Load() != 0 {
		t.Fatalf("expected 0 HTTP hits across all providers when text is already in target language, got %d", totalHits.Load())
	}
}

func TestTranslateChainPriorityFallbackBackoffAndRoundRobin(t *testing.T) {
	// Verify backoff schedule
	if got := ComputeBackoffDuration(1, false); got != 15*time.Second {
		t.Fatalf("expected 1st transient fail backoff=15s, got %v", got)
	}
	if got := ComputeBackoffDuration(2, false); got != 30*time.Second {
		t.Fatalf("expected 2nd transient fail backoff=30s, got %v", got)
	}
	if got := ComputeBackoffDuration(10, false); got != time.Hour {
		t.Fatalf("expected max transient fail backoff=1h, got %v", got)
	}
	if got := ComputeBackoffDuration(1, true); got != time.Hour {
		t.Fatalf("expected hard quota/auth fail backoff=1h immediately, got %v", got)
	}

	var myMemoryHits atomic.Int32
	var deeplHits atomic.Int32
	var failMyMemory atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mymemory":
			myMemoryHits.Add(1)
			if failMyMemory.Load() {
				http.Error(w, "rate limit", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"responseData":{"translatedText":"Hello from MyMemory","match":1.0},"quotaFinished":false,"responseStatus":200}`))
		case "/deepl":
			deeplHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"translations":[{"detected_source_language":"ES","text":"Hello from DeepL"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	svc := NewService()
	svc.SetHTTPClient(srv.Client())
	svc.SetCloudEndpoints(srv.URL+"/mymemory", srv.URL+"/cloud", srv.URL+"/deepl")
	svc.SetClock(func() time.Time { return now })

	chain := []Options{
		{Provider: config.TranslationProviderMyMemory},
		{Provider: config.TranslationProviderDeepL, APIKey: "deepl-key:fx"},
	}

	// 1. Priority mode: MyMemory fails with 429 -> enters 15s backoff and falls back to DeepL
	failMyMemory.Store(true)
	res1, err := svc.TranslateChain(context.Background(), "¡Hola amigos!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain priority fallback error: %v", err)
	}
	if res1.Provider != config.TranslationProviderDeepL || res1.TranslatedText != "Hello from DeepL" {
		t.Fatalf("expected DeepL fallback result, got %+v", res1)
	}
	stMM := svc.ProviderStatus(config.TranslationProviderMyMemory)
	if !stMM.InBackoff(now) || stMM.ConsecutiveFail != 1 {
		t.Fatalf("expected MyMemory in backoff with ConsecutiveFail=1, got %+v", stMM)
	}

	// Next call 5s later (still in 15s backoff) should skip MyMemory without hitting it and go straight to DeepL
	now = now.Add(5 * time.Second)
	svc.ClearCache()
	res2, err := svc.TranslateChain(context.Background(), "¡Buenos días!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain during backoff error: %v", err)
	}
	if res2.Provider != config.TranslationProviderDeepL || myMemoryHits.Load() != 1 {
		t.Fatalf("expected MyMemory skipped during backoff (hits=1), got hits=%d provider=%q", myMemoryHits.Load(), res2.Provider)
	}

	// 2. Manual reset of MyMemory backoff + MyMemory healthy again -> Priority mode uses #1 MyMemory
	failMyMemory.Store(false)
	svc.ResetProviderBackoff(config.TranslationProviderMyMemory)
	svc.ClearCache()
	res3, err := svc.TranslateChain(context.Background(), "¡Buenas noches!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain after reset error: %v", err)
	}
	if res3.Provider != config.TranslationProviderMyMemory || res3.TranslatedText != "Hello from MyMemory" {
		t.Fatalf("expected MyMemory #1 after reset, got %+v", res3)
	}

	// 3. Round-Robin mode: alternates evenly between MyMemory (#1) and DeepL (#2)
	svc.ClearCache()
	rr1, err := svc.TranslateChain(context.Background(), "Frase uno en español", "en", "es", config.TranslationRoutingRoundRobin, chain)
	if err != nil {
		t.Fatalf("RoundRobin 1 error: %v", err)
	}
	svc.ClearCache()
	rr2, err := svc.TranslateChain(context.Background(), "Frase dos en español", "en", "es", config.TranslationRoutingRoundRobin, chain)
	if err != nil {
		t.Fatalf("RoundRobin 2 error: %v", err)
	}
	if rr1.Provider == rr2.Provider {
		t.Fatalf("expected Round-Robin to alternate providers across calls, got %q then %q", rr1.Provider, rr2.Provider)
	}
}

func TestSplitSentencesAndTwoTierCache(t *testing.T) {
	// 1. Verify SplitSentences splits on Western and CJK punctuation without breaking decimals
	sents := SplitSentences("Hola amigos, este directo está increíble hoy! Hoy! 今日の配信最高ですね！ Version 3.14 works.")
	wantSents := []string{
		"Hola amigos, este directo está increíble hoy!",
		"Hoy!",
		"今日の配信最高ですね！",
		"Version 3.14 works.",
	}
	if len(sents) != len(wantSents) {
		t.Fatalf("SplitSentences len = %d (%v), want %d (%v)", len(sents), sents, len(wantSents), wantSents)
	}
	for i := range wantSents {
		if sents[i] != wantSents[i] {
			t.Errorf("SplitSentences[%d] = %q, want %q", i, sents[i], wantSents[i])
		}
	}

	// 2. Verify two-tier L1 RAM + L2 frequency-gated disk cache and partial sentence caching
	var apiCalls atomic.Int32
	var lastQueriedText string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		q := r.URL.Query().Get("q")
		mu.Lock()
		lastQueriedText = q
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		trans := "Translated: " + q
		switch q {
		case "Hola amigos, este directo está increíble hoy!":
			trans = "Hello friends, this stream is incredible today!"
		case "Hoy!":
			trans = "Today!"
		}
		_, _ = w.Write([]byte(`{"responseData":{"translatedText":"` + trans + `","match":1.0},"responseStatus":200}`))
	}))
	defer srv.Close()

	svc := NewService()
	svc.SetHTTPClient(srv.Client())
	svc.SetCloudEndpoints(srv.URL, srv.URL, srv.URL)
	diskFile := t.TempDir() + "/cache/translations.json"
	svc.SetDiskCachePath(diskFile)

	chain := []Options{{Provider: config.TranslationProviderMyMemory, TargetLang: "en"}}

	// Step A: Translate 1st sentence -> 1 API call, stored in L1 RAM (hits=1, disk=0)
	res1, err := svc.TranslateChain(context.Background(), "Hola amigos, este directo está increíble hoy!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("Step A failed: %v", err)
	}
	if res1.TranslatedText != "Hello friends, this stream is incredible today!" || apiCalls.Load() != 1 {
		t.Fatalf("Step A unexpected result=%+v apiCalls=%d", res1, apiCalls.Load())
	}
	memCnt, diskCnt := svc.CacheStats()
	if memCnt != 1 || diskCnt != 0 {
		t.Fatalf("Step A CacheStats = (%d, %d), want (1, 0)", memCnt, diskCnt)
	}

	// Step B: Translate "Hola amigos, este directo está increíble hoy! Hoy!"
	// Sentence 1 should come from cache (promoting it to L2 Disk at hit #2!) and ONLY "Hoy!" should be sent to the API!
	res2, err := svc.TranslateChain(context.Background(), "Hola amigos, este directo está increíble hoy! Hoy!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("Step B failed: %v", err)
	}
	if apiCalls.Load() != 2 {
		t.Fatalf("expected exactly 1 additional API call for 'Hoy!', got total apiCalls=%d", apiCalls.Load())
	}
	mu.Lock()
	queried := lastQueriedText
	mu.Unlock()
	if queried != "Hoy!" {
		t.Errorf("expected API to only be queried with uncached 2nd sentence 'Hoy!', got %q", queried)
	}
	if res2.TranslatedText != "Hello friends, this stream is incredible today! Today!" {
		t.Errorf("Step B TranslatedText = %q, want 'Hello friends, this stream is incredible today! Today!'", res2.TranslatedText)
	}
	if len(res2.Sentences) != 2 || !res2.Sentences[0].FromCache || res2.Sentences[1].FromCache {
		t.Errorf("unexpected sentence traces: %+v", res2.Sentences)
	}
	memCnt, diskCnt = svc.CacheStats()
	if memCnt != 2 || diskCnt != 1 {
		t.Errorf("Step B CacheStats = (%d, %d), want (2 in RAM, 1 promoted to Disk)", memCnt, diskCnt)
	}

	// Step C: Bilingual message "GG bro! Hola amigos, este directo está increíble hoy!" -> 0 API calls!
	// Sentence 1 ("GG bro!") is skipped as target language, Sentence 2 hits cache!
	res3, err := svc.TranslateChain(context.Background(), "GG bro! Hola amigos, este directo está increíble hoy!", "en", "es", config.TranslationRoutingPriority, chain)
	if err != nil {
		t.Fatalf("Step C failed: %v", err)
	}
	if apiCalls.Load() != 2 {
		t.Errorf("expected 0 API calls for bilingual cached message, got total apiCalls=%d", apiCalls.Load())
	}
	if res3.TranslatedText != "GG bro! Hello friends, this stream is incredible today!" {
		t.Errorf("Step C TranslatedText = %q", res3.TranslatedText)
	}
}
