package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
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
		Provider:     ProviderMyMemory,
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
		Provider:     ProviderMyMemory,
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
		Provider:   ProviderGoogle,
		TargetLang: "en",
	}); err == nil {
		t.Fatal("expected error when calling Google Cloud v2 without an API key")
	}

	resCloud, err := svc.Translate(context.Background(), "あのボス戦めっちゃギリギリだった！", Options{
		Provider:   ProviderGoogle,
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
		Provider:   ProviderDeepL,
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
		Provider:    ProviderLibreTranslate,
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
		Provider:    ProviderOllama,
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

	for _, prov := range AllProviders {
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
		{Provider: ProviderMyMemory},
		{Provider: ProviderDeepL, APIKey: "deepl-key:fx"},
	}

	// 1. Priority mode: MyMemory fails with 429 -> enters 15s backoff and falls back to DeepL
	failMyMemory.Store(true)
	res1, err := svc.TranslateChain(context.Background(), "¡Hola amigos!", "en", "es", RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain priority fallback error: %v", err)
	}
	if res1.Provider != ProviderDeepL || res1.TranslatedText != "Hello from DeepL" {
		t.Fatalf("expected DeepL fallback result, got %+v", res1)
	}
	stMM := svc.ProviderStatus(ProviderMyMemory)
	if !stMM.InBackoff(now) || stMM.ConsecutiveFail != 1 {
		t.Fatalf("expected MyMemory in backoff with ConsecutiveFail=1, got %+v", stMM)
	}

	// Next call 5s later (still in 15s backoff) should skip MyMemory without hitting it and go straight to DeepL
	now = now.Add(5 * time.Second)
	svc.ClearCache()
	res2, err := svc.TranslateChain(context.Background(), "¡Buenos días!", "en", "es", RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain during backoff error: %v", err)
	}
	if res2.Provider != ProviderDeepL || myMemoryHits.Load() != 1 {
		t.Fatalf("expected MyMemory skipped during backoff (hits=1), got hits=%d provider=%q", myMemoryHits.Load(), res2.Provider)
	}

	// 2. Manual reset of MyMemory backoff + MyMemory healthy again -> Priority mode uses #1 MyMemory
	failMyMemory.Store(false)
	svc.ResetProviderBackoff(ProviderMyMemory)
	svc.ClearCache()
	res3, err := svc.TranslateChain(context.Background(), "¡Buenas noches!", "en", "es", RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateChain after reset error: %v", err)
	}
	if res3.Provider != ProviderMyMemory || res3.TranslatedText != "Hello from MyMemory" {
		t.Fatalf("expected MyMemory #1 after reset, got %+v", res3)
	}

	// 3. Round-Robin mode: alternates evenly between MyMemory (#1) and DeepL (#2)
	svc.ClearCache()
	rr1, err := svc.TranslateChain(context.Background(), "Frase uno en español", "en", "es", RoutingRoundRobin, chain)
	if err != nil {
		t.Fatalf("RoundRobin 1 error: %v", err)
	}
	svc.ClearCache()
	rr2, err := svc.TranslateChain(context.Background(), "Frase dos en español", "en", "es", RoutingRoundRobin, chain)
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

	chain := []Options{{Provider: ProviderMyMemory, TargetLang: "en"}}

	// Step A: Translate 1st sentence -> 1 API call, stored in L1 RAM (hits=1, disk=0)
	res1, err := svc.TranslateChain(context.Background(), "Hola amigos, este directo está increíble hoy!", "en", "es", RoutingPriority, chain)
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
	res2, err := svc.TranslateChain(context.Background(), "Hola amigos, este directo está increíble hoy! Hoy!", "en", "es", RoutingPriority, chain)
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
	res3, err := svc.TranslateChain(context.Background(), "GG bro! Hola amigos, este directo está increíble hoy!", "en", "es", RoutingPriority, chain)
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

func TestIsMixedTaglishAndTranslateMulti(t *testing.T) {
	if IsMixedTaglish("Hello everyone, welcome back to the stream!") {
		t.Fatal("expected pure English not to be marked mixed Taglish")
	}
	if IsMixedTaglish("Magandang gabi po sa inyong lahat!") {
		t.Fatal("expected pure Tagalog not to be marked mixed Taglish")
	}
	if !IsMixedTaglish("Wait lang guys, mag-heal muna ako before the boss fight!") {
		t.Fatal("expected Taglish phrase to be detected by IsMixedTaglish")
	}
	if lang, ok := DetectCodeSwitching("Wait amigos, estamos jugando this boss fight!"); !ok || lang != "es" {
		t.Fatalf("expected Spanglish code-switching to detect 'es', got lang=%q ok=%v", lang, ok)
	}

	var (
		mu       sync.Mutex
		pairHits = make(map[string]int)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lp := r.URL.Query().Get("langpair")
		q := r.URL.Query().Get("q")
		mu.Lock()
		pairHits[lp]++
		mu.Unlock()

		var out string
		switch lp {
		case "en|tl":
			if strings.Contains(q, "mag-heal") {
				out = "Sandali lang mga kasama, magpapagaling muna ako bago ang laban sa boss!"
			} else {
				out = "Maligayang pagbabalik sa stream!"
			}
		case "en|ja":
			out = "配信にお帰りなさい！"
		case "tl|en":
			if strings.Contains(q, "mag-heal") {
				out = "Wait a second guys, I will heal first before the boss fight!"
			} else {
				out = "Good evening to you all!"
			}
		case "tl|ja":
			if strings.Contains(q, "mag-heal") {
				out = "みんなちょっと待って、ボス戦の前にまず回復するね！"
			} else {
				out = "皆さん、こんばんは！"
			}
		default:
			out = "tx:" + lp
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseData":{"translatedText":"` + out + `","match":1.0},"responseStatus":200}`))
	}))
	defer srv.Close()

	svc := NewService()
	svc.SetHTTPClient(srv.Client())
	svc.SetCloudEndpoints(srv.URL, srv.URL, srv.URL)
	chain := []Options{{Provider: ProviderMyMemory}}
	targets := []string{"fil", "ja", "en"} // "fil" normalizes to "tl" with badge "FIL"

	// Case 1: Pure English spoken -> 0 calls for "en", 2 concurrent calls ("en|tl" and "en|ja")
	itemsEN, err := svc.TranslateMulti(context.Background(), "Welcome back to the stream!", "en", targets, RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateMulti pure EN failed: %v", err)
	}
	if len(itemsEN) != 3 {
		t.Fatalf("expected 3 items, got %d", len(itemsEN))
	}
	if itemsEN[0].Lang != "tl" || itemsEN[0].LanguageBadge != "FIL" || itemsEN[0].IsOriginal || itemsEN[0].Text != "Maligayang pagbabalik sa stream!" {
		t.Errorf("unexpected itemsEN[0]: %+v", itemsEN[0])
	}
	if itemsEN[1].Lang != "ja" || itemsEN[1].LanguageBadge != "JA" || itemsEN[1].Text != "配信にお帰りなさい！" {
		t.Errorf("unexpected itemsEN[1]: %+v", itemsEN[1])
	}
	if itemsEN[2].Lang != "en" || !itemsEN[2].IsOriginal || itemsEN[2].Text != "Welcome back to the stream!" {
		t.Errorf("unexpected itemsEN[2]: %+v", itemsEN[2])
	}
	mu.Lock()
	if pairHits["en|en"] != 0 || pairHits["en|tl"] != 1 || pairHits["en|ja"] != 1 {
		t.Errorf("unexpected pairHits after Case 1: %v", pairHits)
	}
	pairHits = make(map[string]int)
	mu.Unlock()

	// Case 2: Pure Tagalog spoken -> 0 calls for "tl", 2 concurrent calls ("tl|ja" and "tl|en")
	itemsTL, err := svc.TranslateMulti(context.Background(), "Magandang gabi po sa inyong lahat!", "tl", targets, RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateMulti pure TL failed: %v", err)
	}
	if !itemsTL[0].IsOriginal || itemsTL[0].Text != "Magandang gabi po sa inyong lahat!" {
		t.Errorf("expected original Tagalog in slot 0, got %+v", itemsTL[0])
	}
	if itemsTL[1].Text != "皆さん、こんばんは！" || itemsTL[2].Text != "Good evening to you all!" {
		t.Errorf("unexpected TL translations: ja=%q en=%q", itemsTL[1].Text, itemsTL[2].Text)
	}
	mu.Lock()
	if pairHits["tl|tl"] != 0 || pairHits["tl|ja"] != 1 || pairHits["tl|en"] != 1 {
		t.Errorf("unexpected pairHits after Case 2: %v", pairHits)
	}
	pairHits = make(map[string]int)
	mu.Unlock()

	// Case 3: Mixed Taglish spoken -> translates ALL 3 targets cleanly without false short-circuit!
	taglish := "Wait lang guys, mag-heal muna ako before the boss fight!"
	itemsMix, err := svc.TranslateMulti(context.Background(), taglish, "tl", targets, RoutingPriority, chain)
	if err != nil {
		t.Fatalf("TranslateMulti Taglish failed: %v", err)
	}
	if itemsMix[0].Text != "Sandali lang mga kasama, magpapagaling muna ako bago ang laban sa boss!" {
		t.Errorf("expected clean Tagalog normalization on FIL row, got %q", itemsMix[0].Text)
	}
	if itemsMix[1].Text != "みんなちょっと待って、ボス戦の前にまず回復するね！" {
		t.Errorf("expected Japanese translation on JA row, got %q", itemsMix[1].Text)
	}
	if itemsMix[2].Text != "Wait a second guys, I will heal first before the boss fight!" {
		t.Errorf("expected clean English normalization on EN row, got %q", itemsMix[2].Text)
	}
	mu.Lock()
	if pairHits["en|tl"] != 1 || pairHits["tl|ja"] != 1 || pairHits["tl|en"] != 1 {
		t.Errorf("expected all 3 pairs queried for mixed Taglish, got %v", pairHits)
	}
	mu.Unlock()
}

func TestTranslateMultiSpanishAndRoundRobinPivot(t *testing.T) {
	var (
		mu       sync.Mutex
		pairHits = make(map[string]int)
		deeplTL  atomic.Int32
	)

	// MyMemory echoes untranslated Spanish on sparse direct "es|tl", but succeeds on "es|en", "en|tl", and "es|ja".
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lp := r.URL.Query().Get("langpair")
		q := r.URL.Query().Get("q")
		mu.Lock()
		pairHits[lp]++
		mu.Unlock()

		var out string
		switch lp {
		case "es|en":
			out = "Hello friends, how are you today?"
		case "es|tl":
			// Echo identical Spanish to simulate sparse direct non-English pair
			out = q
		case "en|tl":
			out = "Kumusta mga kaibigan, kumusta kayo ngayon?"
		case "es|ja":
			out = "こんにちは友達、今日はお元気ですか？"
		default:
			out = "mm:" + lp
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseData":{"translatedText":"` + out + `","match":1.0},"responseStatus":200}`))
	}))
	defer mmSrv.Close()

	// DeepL supports EN and JA, and would return HTTP 400 if ever called with unsupported target "TL".
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req deepLRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.TargetLang == "TL" {
			deeplTL.Add(1)
			http.Error(w, `{"message":"Value for 'target_lang' not supported."}`, http.StatusBadRequest)
			return
		}
		var out string
		switch req.TargetLang {
		case "EN":
			out = "Hello friends, how are you today?"
		case "JA":
			out = "こんにちは友達、今日はお元気ですか？"
		default:
			out = "dl:" + req.TargetLang
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"translations":[{"detected_source_language":"ES","text":"` + out + `"}]}`))
	}))
	defer dlSrv.Close()

	svc := NewService()
	svc.SetHTTPClient(mmSrv.Client())
	svc.SetCloudEndpoints(mmSrv.URL, "", dlSrv.URL)

	chain := []Options{
		{Provider: ProviderMyMemory},
		{Provider: ProviderDeepL, APIKey: "test-key:fx"},
	}
	spanish := "Hola amigos, ¿cómo están hoy?"

	// Verify Spanish -> ["en", "tl", "ja"] in round_robin mode:
	// - Never skips "tl" due to false whatlanggo Spa/Tgl confusion
	// - Skips DeepL on unsupported "tl" without putting DeepL into backoff
	// - Pivots "es -> en -> tl" cleanly when MyMemory echoes untranslated Spanish on "es|tl"
	items, err := svc.TranslateMulti(context.Background(), spanish, "es", []string{"en", "tl", "ja"}, RoutingRoundRobin, chain)
	if err != nil {
		t.Fatalf("TranslateMulti Spanish failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Lang != "en" || items[0].IsOriginal || items[0].Text != "Hello friends, how are you today?" {
		t.Errorf("unexpected EN item: %+v", items[0])
	}
	if items[1].Lang != "tl" || items[1].LanguageBadge != "FIL" || items[1].IsOriginal || items[1].Text != "Kumusta mga kaibigan, kumusta kayo ngayon?" {
		t.Errorf("expected translated Tagalog via English pivot (not raw Spanish), got %+v", items[1])
	}
	if items[2].Lang != "ja" || items[2].IsOriginal || items[2].Text != "こんにちは友達、今日はお元気ですか？" {
		t.Errorf("unexpected JA item: %+v", items[2])
	}
	if deeplTL.Load() != 0 {
		t.Errorf("expected DeepL never to be called with unsupported TL target, got %d calls", deeplTL.Load())
	}
	if st := svc.ProviderStatus(ProviderDeepL); st.FailureCount != 0 {
		t.Errorf("expected 0 DeepL failures, got %+v", st)
	}
}

