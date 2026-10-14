package chat

import (
	"strings"
	"testing"
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
	// Runes:
	// 0: 👋
	// 1: ' '
	// 2..6: "Hello"
	// 7: ' '
	// 8..13: "Kappa"
	// 14: ' '
	// 15: ✨
	spans := []EmoteSpan{
		{Start: 8, End: 13, ID: "25", Name: "Kappa"},
	}

	cleaned := s.Sanitize(text, spans, false, nil)
	if cleaned != "👋 Hello ✨" {
		t.Errorf("expected '👋 Hello ✨', got %q", cleaned)
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
