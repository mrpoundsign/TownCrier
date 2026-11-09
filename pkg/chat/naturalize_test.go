package chat

import (
	"testing"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

func TestNaturalizeUsername(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "wrapper camelcase and 2-digit suffix",
			input: "xX_DarkKnight99_Xx",
			want:  "Dark Knight 99",
		},
		{
			name:  "leet speak camelcase and TV suffix",
			input: "Sn1perWolf_TV",
			want:  "Sniper Wolf",
		},
		{
			name:  "trailing 4-digit discriminator stripped",
			input: "Player9482",
			want:  "Player",
		},
		{
			name:  "camelcase with trailing 4-digit discriminator",
			input: "JohnDoe4829",
			want:  "John Doe",
		},
		{
			name:  "snake_case with separated 4-digit discriminator",
			input: "john_doe_4829",
			want:  "john doe",
		},
		{
			name:  "kebab-case and TTV prefix",
			input: "TTV-night-owl",
			want:  "night owl",
		},
		{
			name:  "attached TTV suffix and prefix",
			input: "DarkKnightTTV",
			want:  "Dark Knight",
		},
		{
			name:  "attached TTV prefix",
			input: "TTVDarkKnight",
			want:  "Dark Knight",
		},
		{
			name:  "YT and Live suffixes",
			input: "Sh4d0w_YT",
			want:  "Shadow",
		},
		{
			name:  "Live suffix case-insensitive",
			input: "CoolStreamer_Live",
			want:  "Cool Streamer",
		},
		{
			name:  "Live prefix in normal word not stripped without separator",
			input: "LiveWire",
			want:  "Live Wire",
		},
		{
			name:  "acronym followed by word",
			input: "TTSPlayer",
			want:  "TTS Player",
		},
		{
			name:  "double leet digits inside word",
			input: "L33tG4m3r",
			want:  "Leet Gamer",
		},
		{
			name:  "uppercase leet word",
			input: "SN1PER",
			want:  "SNIPER",
		},
		{
			name:  "all-numeric username preserved",
			input: "123456",
			want:  "123456",
		},
		{
			name:  "short handle preserved",
			input: "xX",
			want:  "xX",
		},
		{
			name:  "tag-only handle preserved",
			input: "_TV",
			want:  "TV",
		},
		{
			name:  "CJK username untouched",
			input: "さくら",
			want:  "さくら",
		},
		{
			name:  "CJK username with TV suffix",
			input: "さくら_TV",
			want:  "さくら",
		},
		{
			name:  "Korean username untouched",
			input: "김철수",
			want:  "김철수",
		},
		{
			name:  "leading at-sign stripped",
			input: "@MrPoundSign",
			want:  "Mr Pound Sign",
		},
		{
			name:  "YouTube auto-generated handle discriminator stripped",
			input: "IgorB-x8u",
			want:  "Igor B",
		},
		{
			name:  "punctuation-only fallback",
			input: "___",
			want:  "___",
		},
		{
			name:  "empty string",
			input: "   ",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NaturalizeUsername(tc.input)
			if got != tc.want {
				t.Errorf("NaturalizeUsername(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRouterNaturalizeNamesAndNicknamePrecedence(t *testing.T) {
	router := NewRouter()
	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.SpeakNames = true
	cfg.GlobalChatRules.NaturalizeNames = true

	ev := &Event{
		Platform:    "twitch",
		UserID:      "u-dk",
		UserName:    "xx_darkknight99_xx",
		DisplayName: "xX_DarkKnight99_Xx",
		Kind:        EventKindMessage,
		Text:        "hello chat",
	}

	// 1. NaturalizeNames enabled globally -> splits and cleans raw username
	res := router.Resolve(ev, cfg)
	if res.Text != "Dark Knight 99 says: hello chat" {
		t.Errorf("expected naturalized name 'Dark Knight 99 says: hello chat', got %q", res.Text)
	}

	// 2. Platform override Off -> uses raw DisplayName
	router.ResetSpeaker()
	tw := cfg.Platforms["twitch"]
	tw.Overrides.NaturalizeNamesMode = config.OverrideModeOff
	cfg.Platforms["twitch"] = tw

	resRaw := router.Resolve(ev, cfg)
	if resRaw.Text != "xX_DarkKnight99_Xx says: hello chat" {
		t.Errorf("expected raw name 'xX_DarkKnight99_Xx says: hello chat' when platform override is off, got %q", resRaw.Text)
	}

	// 3. Explicit ViewerConfig.Nickname always takes precedence over NaturalizeUsername
	router.ResetSpeaker()
	tw.Overrides.NaturalizeNamesMode = config.OverrideModeOn
	cfg.Platforms["twitch"] = tw
	cfg.Viewers["twitch:u-dk"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "u-dk",
		Nickname: "DK_The_Legend",
	}

	resNick := router.Resolve(ev, cfg)
	if resNick.Text != "DK_The_Legend says: hello chat" {
		t.Errorf("expected explicit viewer nickname 'DK_The_Legend says: hello chat', got %q", resNick.Text)
	}
}
