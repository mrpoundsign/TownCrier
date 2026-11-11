package platform

import (
	"net/url"
	"strings"
)

// ProfileURL returns the canonical web profile/channel URL for a given platform user.
// Returns an empty string if the platform or identity is empty or unsupported.
func ProfileURL(platformName, username, userID string) string {
	plat := strings.ToLower(strings.TrimSpace(platformName))
	user := strings.TrimSpace(username)
	uid := strings.TrimSpace(userID)

	cleanUser := strings.TrimPrefix(user, "@")

	switch plat {
	case "twitch":
		if cleanUser != "" {
			return "https://www.twitch.tv/" + url.PathEscape(cleanUser)
		}
		if uid != "" {
			return "https://www.twitch.tv/" + url.PathEscape(uid)
		}
	case "kick":
		if cleanUser != "" {
			return "https://kick.com/" + url.PathEscape(cleanUser)
		}
		if uid != "" {
			return "https://kick.com/" + url.PathEscape(uid)
		}
	case "tiktok":
		if cleanUser != "" {
			return "https://www.tiktok.com/@" + url.PathEscape(cleanUser)
		}
		if uid != "" {
			return "https://www.tiktok.com/@" + url.PathEscape(uid)
		}
	case "youtube":
		// Canonical YouTube channel IDs begin with "UC" (typically 24 chars).
		if strings.HasPrefix(uid, "UC") && len(uid) >= 20 {
			return "https://www.youtube.com/channel/" + url.PathEscape(uid)
		}
		if cleanUser != "" {
			return "https://www.youtube.com/@" + url.PathEscape(cleanUser)
		}
		if uid != "" {
			return "https://www.youtube.com/channel/" + url.PathEscape(uid)
		}
	}
	return ""
}
