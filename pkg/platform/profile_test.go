package platform

import "testing"

func TestProfileURL(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		username string
		userID   string
		want     string
	}{
		{
			name:     "twitch username",
			platform: "twitch",
			username: "streamer123",
			userID:   "98765",
			want:     "https://www.twitch.tv/streamer123",
		},
		{
			name:     "twitch with leading @ and uppercase platform",
			platform: "TWITCH",
			username: "@streamer_two",
			userID:   "",
			want:     "https://www.twitch.tv/streamer_two",
		},
		{
			name:     "kick username",
			platform: "Kick",
			username: "gamer_guy",
			userID:   "112233",
			want:     "https://kick.com/gamer_guy",
		},
		{
			name:     "tiktok username",
			platform: "TikTok",
			username: "dance_queen",
			userID:   "445566",
			want:     "https://www.tiktok.com/@dance_queen",
		},
		{
			name:     "tiktok with leading @",
			platform: "tiktok",
			username: "@dance_queen",
			userID:   "445566",
			want:     "https://www.tiktok.com/@dance_queen",
		},
		{
			name:     "youtube with channel ID",
			platform: "YouTube",
			username: "Igor Creator",
			userID:   "UC6391gIzzyKbri_pkMzFcoQ",
			want:     "https://www.youtube.com/channel/UC6391gIzzyKbri_pkMzFcoQ",
		},
		{
			name:     "youtube handle",
			platform: "youtube",
			username: "CoolHandle",
			userID:   "",
			want:     "https://www.youtube.com/@CoolHandle",
		},
		{
			name:     "youtube handle with @",
			platform: "youtube",
			username: "@CoolHandle",
			userID:   "",
			want:     "https://www.youtube.com/@CoolHandle",
		},
		{
			name:     "unsupported platform",
			platform: "discord",
			username: "chatter",
			userID:   "123",
			want:     "",
		},
		{
			name:     "empty identifiers",
			platform: "twitch",
			username: "",
			userID:   "",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProfileURL(tc.platform, tc.username, tc.userID)
			if got != tc.want {
				t.Errorf("ProfileURL(%q, %q, %q) = %q, want %q", tc.platform, tc.username, tc.userID, got, tc.want)
			}
		})
	}
}
