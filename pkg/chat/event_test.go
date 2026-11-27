package chat

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTwitchEmoteURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "legacy static dark 1.0",
			in:   "https://static-cdn.jtvnw.net/emoticons/v2/25/static/dark/1.0",
			want: "https://static-cdn.jtvnw.net/emoticons/v2/25/default/dark/1.0",
		},
		{
			name: "legacy static light 2.0",
			in:   "  https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_abc123/static/light/2.0  ",
			want: "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_abc123/default/light/2.0",
		},
		{
			name: "already default",
			in:   "https://static-cdn.jtvnw.net/emoticons/v2/25/default/dark/1.0",
			want: "https://static-cdn.jtvnw.net/emoticons/v2/25/default/dark/1.0",
		},
		{
			name: "non-twitch url untouched",
			in:   "https://files.kick.com/emotes/12345/fullsize",
			want: "https://files.kick.com/emotes/12345/fullsize",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeTwitchEmoteURL(tc.in); got != tc.want {
				t.Fatalf("NormalizeTwitchEmoteURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEmoteSpanImageURL(t *testing.T) {
	spanFallback := EmoteSpan{ID: "25", Name: "Kappa"}
	if got, want := spanFallback.ImageURL("twitch"), "https://static-cdn.jtvnw.net/emoticons/v2/25/default/dark/1.0"; got != want {
		t.Fatalf("twitch fallback = %q, want %q", got, want)
	}

	spanLegacy := EmoteSpan{ID: "25", Name: "Kappa", URL: "https://static-cdn.jtvnw.net/emoticons/v2/25/static/dark/1.0"}
	if got, want := spanLegacy.ImageURL("twitch"), "https://static-cdn.jtvnw.net/emoticons/v2/25/default/dark/1.0"; got != want {
		t.Fatalf("twitch legacy = %q, want %q", got, want)
	}

	spanKick := EmoteSpan{ID: "42", Name: "KEKW"}
	if got, want := spanKick.ImageURL("kick"), "https://files.kick.com/emotes/42/fullsize"; got != want {
		t.Fatalf("kick fallback = %q, want %q", got, want)
	}
}

func TestEmoteSpanCheermoteJSONRoundTrip(t *testing.T) {
	orig := EmoteSpan{
		Start: 6,
		End:   14,
		ID:    "cheermote:cheer",
		Name:  "Cheer100",
		URL:   "https://d3aqoihi2n8ty8.cloudfront.net/actions/cheer/dark/animated/100/2.gif",
		Bits:  100,
		Color: "#9c3ee8",
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded EmoteSpan
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded != orig {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", decoded, orig)
	}
}
