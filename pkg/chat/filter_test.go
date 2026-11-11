package chat

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeEmotes(t *testing.T) {
	s := NewSanitizer(300)

	// "Hello Kappa world" where "Kappa" is at rune index 6..11
	text := "Hello Kappa world"
	spans := []EmoteSpan{
		{Start: 6, End: 11, ID: "25", Name: "Kappa"},
	}

	// When speakEmotes is false, "Kappa" should be stripped
	cleaned := s.Sanitize(text, spans, false, nil)
	if cleaned != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", cleaned)
	}

	// When speakEmotes is true, "Kappa" should be preserved
	kept := s.Sanitize(text, spans, true, nil)
	if kept != "Hello Kappa world" {
		t.Errorf("expected 'Hello Kappa world', got %q", kept)
	}
}

func TestSanitizePureEmotes(t *testing.T) {
	s := NewSanitizer(300)
	text := "Kappa Kappa"
	spans := []EmoteSpan{
		{Start: 0, End: 5, ID: "25", Name: "Kappa"},
		{Start: 6, End: 11, ID: "25", Name: "Kappa"},
	}
	cleaned := s.Sanitize(text, spans, false, nil)
	if cleaned != "" {
		t.Errorf("expected empty string for pure emotes, got %q", cleaned)
	}
}

func TestSanitizeEmotesWithUnicode(t *testing.T) {
	s := NewSanitizer(300)

	// "👋 Hello Kappa ✨"
	text := "👋 Hello Kappa ✨"
	spans := []EmoteSpan{
		{Start: 8, End: 13, ID: "25", Name: "Kappa"},
	}

	// 1. Direct span stripper preserves surrounding Unicode characters
	strippedSpans := stripEmoteSpans(text, spans)
	if strippedSpans != "👋 Hello  ✨" {
		t.Errorf("expected '👋 Hello  ✨', got %q", strippedSpans)
	}

	// 2. When SpeakEmotes is false, both platform emotes and Unicode emojis are stripped
	cleaned := s.Sanitize(text, spans, false, nil)
	if cleaned != "Hello" {
		t.Errorf("expected 'Hello', got %q", cleaned)
	}

	// 3. When SpeakEmotes is true, both platform emotes and Unicode emojis are kept
	kept := s.Sanitize(text, spans, true, nil)
	if kept != "👋 Hello Kappa ✨" {
		t.Errorf("expected '👋 Hello Kappa ✨', got %q", kept)
	}
}

func TestSanitizeURLs(t *testing.T) {
	s := NewSanitizer(300)

	tests := []struct {
		input    string
		expected string
	}{
		{"Check this out https://twitch.tv/example it is cool", "Check this out link it is cool"},
		{"Visit www.example.com for info", "Visit link for info"},
		{"Multiple http://foo.com and https://bar.org links", "Multiple link and link links"},
	}

	for _, tt := range tests {
		got := s.Sanitize(tt.input, nil, false, nil)
		if got != tt.expected {
			t.Errorf("input %q: expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}

func TestSanitizeSpam(t *testing.T) {
	s := NewSanitizer(300)

	tests := []struct {
		input    string
		expected string
	}{
		{"What is this????", "What is this?"},
		{"Wait WHAT!?!?!?", "Wait WHAT?!"},
		{"Hello.....", "Hello."},
		{"hahahahah wwwwwww that was funny", "hahahahah w that was funny"},
		{"looooooooool so coolllllll", "lool so cooll"},
	}

	for _, tt := range tests {
		got := s.Sanitize(tt.input, nil, false, nil)
		if got != tt.expected {
			t.Errorf("input %q: expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}

func TestSanitizeBlocklist(t *testing.T) {
	s := NewSanitizer(300)
	blocked := []string{"badword", "curse"}

	got := s.Sanitize("This contains badword and CuRsE words!", nil, false, blocked)
	expected := "This contains *** and *** words!"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSanitizeTruncation(t *testing.T) {
	s := NewSanitizer(20)
	longText := strings.Repeat("a", 30)

	got := s.Sanitize(longText, nil, false, nil)
	if !strings.HasSuffix(got, "…") && len(got) > 20 {
		t.Errorf("expected truncated string with ellipsis, got %q", got)
	}

	diverseText := "abcdefghijklmnopqrstuvwxyz1234567890"
	gotDiverse := s.Sanitize(diverseText, nil, false, nil)
	expectedDiverse := "abcdefghijklmnopqrst…"
	if gotDiverse != expectedDiverse {
		t.Errorf("expected %q, got %q", expectedDiverse, gotDiverse)
	}
}

func TestDefaultBadWordsAndEffectiveBlockedWords(t *testing.T) {
	defaults := DefaultBadWords()
	if len(defaults) < 50 {
		t.Fatalf("expected embedded default bad words list to have >= 50 entries, got %d", len(defaults))
	}

	s := NewSanitizer(300)

	// 1. With blockDefault = true, embedded words and custom words are both masked
	effective := EffectiveBlockedWords(true, []string{"spoilerword"})
	got := s.Sanitize("What the fuck is spoilerword in Scunthorpe class?", nil, false, effective)
	expected := "What the *** is *** in Scunthorpe class?"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}

	// 2. With blockDefault = false, only custom words are masked
	customOnly := EffectiveBlockedWords(false, []string{"spoilerword"})
	gotCustom := s.Sanitize("What the fuck is spoilerword?", nil, false, customOnly)
	expectedCustom := "What the fuck is ***?"
	if gotCustom != expectedCustom {
		t.Errorf("expected %q, got %q", expectedCustom, gotCustom)
	}
}

func TestSanitizeDuplicateWords(t *testing.T) {
	s := NewSanitizer(300)

	tests := []struct {
		name     string
		input    string
		maxDup   int
		expected string
	}{
		{
			name:     "consecutive 4 duplicate words default max 2",
			input:    "what what what what",
			maxDup:   2,
			expected: "what what",
		},
		{
			name:     "consecutive duplicates with punctuation",
			input:    "what, what, what, what!",
			maxDup:   2,
			expected: "what, what!",
		},
		{
			name:     "mixed casing duplicate words",
			input:    "No NO no No no NO!",
			maxDup:   2,
			expected: "No NO!",
		},
		{
			name:     "single duplicate limit",
			input:    "stop stop stop stop",
			maxDup:   1,
			expected: "stop",
		},
		{
			name:     "duplicate word limit disabled (0)",
			input:    "what what what what",
			maxDup:   0,
			expected: "what what what what",
		},
		{
			name:     "natural repeated words allowed under limit",
			input:    "it is what it is",
			maxDup:   2,
			expected: "it is what it is",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.SanitizeWithOptions(tt.input, SanitizeOptions{
				SpeakEmotes:       true,
				DeduplicateEmotes: false,
				MaxDuplicateWords: tt.maxDup,
			})
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestSanitizeEmoteAndEmojiDeduplication(t *testing.T) {
	s := NewSanitizer(300)

	spans := []EmoteSpan{
		{Start: 0, End: 5, ID: "25", Name: "Kappa"},
		{Start: 6, End: 11, ID: "25", Name: "Kappa"},
		{Start: 12, End: 17, ID: "25", Name: "Kappa"},
		{Start: 18, End: 23, ID: "25", Name: "Kappa"},
	}

	// 1. Spammed identical emote collapsed to once per sentence
	got := s.Sanitize("Kappa Kappa Kappa Kappa", spans, true, nil)
	if got != "Kappa" {
		t.Errorf("expected 'Kappa', got %q", got)
	}

	// 2. Spammed emoji burst collapsed to once
	gotEmoji := s.Sanitize("😂😂😂😂😂😂", nil, true, nil)
	if gotEmoji != "😂" {
		t.Errorf("expected '😂', got %q", gotEmoji)
	}

	// 3. Alternating emoji pattern collapsed to once
	gotPattern := s.Sanitize("😂🔥😂🔥😂🔥", nil, true, nil)
	if gotPattern != "😂🔥" {
		t.Errorf("expected '😂🔥', got %q", gotPattern)
	}

	// 4. Repeated emojis within single sentence with text
	gotSentence := s.Sanitize("Hello 😂😂😂 how are you 😂😂😂", nil, true, nil)
	if gotSentence != "Hello 😂 how are you" {
		t.Errorf("expected 'Hello 😂 how are you', got %q", gotSentence)
	}

	// 5. Multiple sentences allow once per sentence
	gotMultiSentence := s.Sanitize("Hello 😂! Good to see you 😂!", nil, true, nil)
	if gotMultiSentence != "Hello 😂! Good to see you 😂!" {
		t.Errorf("expected 'Hello 😂! Good to see you 😂!', got %q", gotMultiSentence)
	}

	// 6. Mixed emote and emoji deduplication
	mixedSpans := []EmoteSpan{
		{Start: 0, End: 5, ID: "25", Name: "Kappa"},
		{Start: 9, End: 14, ID: "25", Name: "Kappa"},
	}
	gotMixed := s.Sanitize("Kappa 😂 Kappa 😂", mixedSpans, true, nil)
	if gotMixed != "Kappa 😂" {
		t.Errorf("expected 'Kappa 😂', got %q", gotMixed)
	}
}

func TestSanitizeZalgoAndInternationalText(t *testing.T) {
	s := NewSanitizer(300)

	// 1. Stacked Zalgo combining marks should be capped at 2 per base rune, and orphaned leading marks removed
	zalgoInput := "\u0300\u0301H\u0300\u0301\u0302\u0303\u0304\u0305e\u0310\u0311\u0312\u0313\u0314llo"
	gotZalgo := s.SanitizeWithOptions(zalgoInput, SanitizeOptions{
		StripZalgo: true,
	})
	expectedZalgo := "H\u0300\u0301e\u0310\u0311llo"
	if gotZalgo != expectedZalgo {
		t.Errorf("expected %q, got %q", expectedZalgo, gotZalgo)
	}

	// 2. Legitimate accented international text (NFD Vietnamese with 2 combining marks, Spanish, French) is preserved
	intlInput := "Ti\u0065\u0302\u0301ng Vi\u0065\u0323\u0302t, Café, Español, Über"
	gotIntl := s.SanitizeWithOptions(intlInput, SanitizeOptions{
		StripZalgo:    true,
		StripASCIIArt: true,
	})
	if gotIntl != intlInput {
		t.Errorf("expected international text %q to be preserved, got %q", intlInput, gotIntl)
	}
}

func TestSanitizeBrailleAndASCIIArt(t *testing.T) {
	s := NewSanitizer(300)

	// 1. Pure Braille art flood -> stripped to empty
	brailleArt := "⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿"
	if got := s.Sanitize(brailleArt, nil, false, nil); got != "" {
		t.Errorf("expected empty string for Braille art, got %q", got)
	}

	// 2. Box-drawing and block-element frame around text -> frame stripped, text kept
	boxArt := "┌──────────┐ █ Hello stream █ └──────────┘"
	if got := s.Sanitize(boxArt, nil, false, nil); got != "Hello stream" {
		t.Errorf("expected 'Hello stream', got %q", got)
	}

	// 3. High-density ASCII symbol flood -> suppressed to empty
	symbolFlood := `/\_/\_/\_/\_/\_/\_/\_/\_/\_/\`
	if got := s.Sanitize(symbolFlood, nil, false, nil); got != "" {
		t.Errorf("expected empty string for symbol flood, got %q", got)
	}

	// 4. Normal short punctuation or kaomoji with words should be preserved when under symbol density or length threshold
	normalChat := "Wait really?! That was awesome!"
	if got := s.Sanitize(normalChat, nil, false, nil); got != "Wait really?! That was awesome!" {
		t.Errorf("expected normal chat to be preserved, got %q", got)
	}
}

func TestCopypastaFilter(t *testing.T) {
	f := NewCopypastaFilter()
	baseTime := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	window := 30 * time.Second

	// 1. Short normal chat reactions (< 4 words and < 20 runes) are never flagged as copypasta
	for i := range 5 {
		if f.IsDuplicate("twitch", "GGs!", window, baseTime.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("short reaction 'GGs!' should never be flagged as copypasta (iteration %d)", i)
		}
		if f.IsDuplicate("twitch", "let's go", window, baseTime.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("short reaction 'let's go' should never be flagged as copypasta (iteration %d)", i)
		}
	}

	// 2. Multi-user copypasta paragraph: first occurrence allowed, subsequent within window suppressed
	pasta := "I saw the streamer at the grocery store yesterday and he told me to subscribe!"
	if f.IsDuplicate("twitch", pasta, window, baseTime) {
		t.Fatalf("first occurrence of copypasta should not be flagged as duplicate")
	}

	// Variation with different casing and extra exclamation marks within 10s -> duplicate!
	pastaVariant := "i SAW the streamer at the grocery store yesterday, and he told me to SUBSCRIBE!!!"
	if !f.IsDuplicate("twitch", pastaVariant, window, baseTime.Add(10*time.Second)) {
		t.Fatalf("second occurrence of copypasta within window should be flagged as duplicate")
	}

	// Different platform scope is independent
	if f.IsDuplicate("youtube", pasta, window, baseTime.Add(12*time.Second)) {
		t.Fatalf("first occurrence on a different scope should not be flagged as duplicate")
	}

	// 3. After window expires (> 30s after last seen), message is allowed again
	if f.IsDuplicate("twitch", pasta, window, baseTime.Add(45*time.Second)) {
		t.Fatalf("occurrence after sliding window expiration should not be flagged as duplicate")
	}
}

