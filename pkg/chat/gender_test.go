package chat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

func TestClassifyVoiceGender(t *testing.T) {
	cases := []struct {
		voiceID string
		want    VoiceGender
	}{
		{"supertonic:f1", GenderFeminine},
		{"supertonic:f5", GenderFeminine},
		{"supertonic:m1", GenderMasculine},
		{"supertonic:m5", GenderMasculine},
		{"af", GenderFeminine},
		{"af_bella", GenderFeminine},
		{"bf_emma", GenderFeminine},
		{"am_adam", GenderMasculine},
		{"bm_george", GenderMasculine},
		{"kokoro_intl:ef_dora", GenderFeminine},
		{"kokoro_intl:zm_yunxi", GenderMasculine},
		{"kitten_mini:f2", GenderFeminine},
		{"kitten_mini:m3", GenderMasculine},
		{"kitten_nano:f4", GenderFeminine},
		{"kitten_nano:m1", GenderMasculine},
		{"piper:spk_0", GenderFeminine},
		{"piper:spk_1", GenderMasculine},
		{"piper:spk_10", GenderFeminine},
		{"piper:spk_20", GenderMasculine},
		{"Bella — US Female (af_bella)", GenderFeminine},
		{"Adam — US Male (am_adam)", GenderMasculine},
	}

	for _, tc := range cases {
		if got := ClassifyVoiceGender(tc.voiceID); got != tc.want {
			t.Errorf("ClassifyVoiceGender(%q) = %q, want %q", tc.voiceID, got, tc.want)
		}
	}
}

func TestTokenizeAndInferUsernameGender(t *testing.T) {
	cases := []struct {
		display string
		user    string
		want    VoiceGender
	}{
		{"MrPoundSign", "mrpoundsign", GenderMasculine},
		{"QueenSarahTV", "queensarahtv", GenderFeminine},
		{"xX_DarkLord99_Xx", "xx_darklord99_xx", GenderMasculine},
		{"LadyEmilia", "lady_emilia", GenderFeminine},
		{"sarahgaming", "sarahgaming", GenderFeminine},
		{"michaelplays", "michaelplays", GenderMasculine},
		{"RealJessica", "realjessica", GenderFeminine},
		{"TheBeardedBoi", "thebeardedboi", GenderMasculine},
		{"CatGirlMia", "catgirlmia", GenderFeminine},
		{"MoCoMade", "mocomade", GenderUnknown},
		{"PixelShader42", "pixelshader42", GenderUnknown},
	}

	for _, tc := range cases {
		if got := InferUsernameGender(tc.display, tc.user); got != tc.want {
			t.Errorf("InferUsernameGender(%q, %q) = %q, want %q", tc.display, tc.user, got, tc.want)
		}
	}
}

func TestPronounCacheMemoryDiskAndHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/alice_streams":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"1","login":"alice_streams","pronoun_id":"sheher"}]`))
		case "/bob_ttv":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"pronoun_id":"hehim","alt_pronoun_id":"theythem"}`))
		case "/nb_coder":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"3","login":"nb_coder","pronoun_id":"theythem"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cachePath := filepath.Join(t.TempDir(), "pronouns.json")
	pc := NewPronounCache()
	pc.SetHTTPClient(srv.Client(), srv.URL)
	pc.SetDiskCachePath(cachePath)

	// First lookup triggers async fetch and returns GenderUnknown immediately
	if g, ok := pc.LookupGender("twitch", "alice_streams"); ok || g != GenderUnknown {
		t.Fatalf("expected initial cache miss, got (%q, %v)", g, ok)
	}

	// Wait briefly for background lookup to complete
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if g, ok := pc.LookupGender("twitch", "alice_streams"); ok && g == GenderFeminine {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if g, ok := pc.LookupGender("twitch", "alice_streams"); !ok || g != GenderFeminine {
		t.Fatalf("expected cached GenderFeminine for alice_streams, got (%q, %v)", g, ok)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly 1 HTTP request for alice_streams, got %d", hits.Load())
	}

	// Subsequent lookups hit memory cache without extra HTTP calls
	_, _ = pc.LookupGender("twitch", "alice_streams")
	if hits.Load() != 1 {
		t.Fatalf("expected memory cache hit without new HTTP request, got %d", hits.Load())
	}

	// Verify disk persistence by loading a fresh PronounCache from cachePath
	pc2 := NewPronounCache()
	pc2.SetDiskCachePath(cachePath)
	if g, ok := pc2.LookupGender("twitch", "alice_streams"); !ok || g != GenderFeminine {
		t.Fatalf("expected persisted GenderFeminine from disk cache, got (%q, %v)", g, ok)
	}
}

func TestSelectSmartChatterVoice(t *testing.T) {
	voices := []string{
		"supertonic:f1", "supertonic:f2", "supertonic:f3", "supertonic:f4", "supertonic:f5",
		"supertonic:m1", "supertonic:m2", "supertonic:m3", "supertonic:m4", "supertonic:m5",
	}

	pc := NewPronounCache()
	pc.Set("pronoun_fem_user", "sheher")
	pc.Set("pronoun_masc_user", "hehim")

	vFem := SelectSmartChatterVoice("twitch", "pronoun_fem_user", "MysteryUser1", voices, "supertonic:f1", true, UnknownGenderBalanced, pc)
	if ClassifyVoiceGender(vFem) != GenderFeminine {
		t.Errorf("expected feminine voice for pronoun_fem_user, got %q", vFem)
	}

	vMasc := SelectSmartChatterVoice("twitch", "pronoun_masc_user", "MysteryUser2", voices, "supertonic:f1", true, UnknownGenderBalanced, pc)
	if ClassifyVoiceGender(vMasc) != GenderMasculine {
		t.Errorf("expected masculine voice for pronoun_masc_user, got %q", vMasc)
	}

	vNameMasc := SelectSmartChatterVoice("twitch", "mrpoundsign", "MrPoundSign", voices, "supertonic:f1", true, UnknownGenderBalanced, pc)
	if ClassifyVoiceGender(vNameMasc) != GenderMasculine {
		t.Errorf("expected masculine voice for MrPoundSign, got %q", vNameMasc)
	}

	vNameFem := SelectSmartChatterVoice("twitch", "queensarah", "QueenSarah", voices, "supertonic:f1", true, UnknownGenderBalanced, pc)
	if ClassifyVoiceGender(vNameFem) != GenderFeminine {
		t.Errorf("expected feminine voice for QueenSarah, got %q", vNameFem)
	}

	// Unknown gender with default_voice policy
	vDef := SelectSmartChatterVoice("twitch", "mocomade", "MoCoMade", voices, "supertonic:m4", true, UnknownGenderDefaultVoice, pc)
	if vDef != "supertonic:m4" {
		t.Errorf("expected default voice supertonic:m4 for UnknownGenderDefaultVoice, got %q", vDef)
	}
}

func TestRouterSmartGenderDoesNotMutateViewers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.HashChatterVoices = true
	cfg.GlobalChatRules.SmartGenderVoices = true
	cfg.GlobalChatRules.UnknownGenderPolicy = UnknownGenderBalanced

	r := NewRouter()
	r.PronounCache().Set("sarah_ttv", "sheher")

	ev := &Event{
		Platform:    "twitch",
		Kind:        EventKindMessage,
		UserID:      "1001",
		UserName:    "sarah_ttv",
		DisplayName: "Sarah_TTV",
		Text:        "hello stream",
	}

	res := r.Resolve(ev, cfg)
	if ClassifyVoiceGender(res.VoiceID) != GenderFeminine {
		t.Fatalf("expected feminine voice for sarah_ttv, got %q", res.VoiceID)
	}
	if len(cfg.Viewers) != 0 {
		t.Fatalf("expected cfg.Viewers to remain empty (never polluted by auto-hashing), got %d entries", len(cfg.Viewers))
	}
}
