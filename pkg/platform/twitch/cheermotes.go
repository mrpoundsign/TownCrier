package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
)

// CheermoteTier defines a single bit-threshold tier for a Twitch cheermote prefix.
type CheermoteTier struct {
	MinBits     int
	ID          string
	Color       string
	AnimatedURL string
	StaticURL   string
}

// CheermoteAction defines a cheermote prefix and its ascending bit tiers.
type CheermoteAction struct {
	Prefix string
	Tiers  []CheermoteTier
}

// CheermoteCatalog stores Twitch global and channel-specific cheermote definitions
// keyed by lowercase prefix.
type CheermoteCatalog struct {
	actions map[string]CheermoteAction
}

var standardTwitchCheermotes = map[string]string{
	"cheer":         "Cheer",
	"biblethump":    "BibleThump",
	"cheerwhal":     "cheerwhal",
	"corgo":         "Corgo",
	"uni":           "uni",
	"showlove":      "ShowLove",
	"party":         "Party",
	"seemsgood":     "SeemsGood",
	"pride":         "Pride",
	"kappa":         "Kappa",
	"frankerz":      "FrankerZ",
	"heyguys":       "HeyGuys",
	"dansgame":      "DansGame",
	"elegiggle":     "EleGiggle",
	"trihard":       "TriHard",
	"kreygasm":      "Kreygasm",
	"4head":         "4Head",
	"swiftrage":     "SwiftRage",
	"notlikethis":   "NotLikeThis",
	"failfish":      "FailFish",
	"vohiyo":        "VoHiYo",
	"pjsalt":        "PJSalt",
	"mrdestructoid": "MrDestructoid",
	"bday":          "bday",
	"ripcheer":      "RIPCheer",
	"shamrock":      "Shamrock",
	"bitboss":       "BitBoss",
	"streamlabs":    "Streamlabs",
	"muxy":          "Muxy",
	"holidaycheer":  "HolidayCheer",
	"goal":          "Goal",
	"anon":          "Anon",
}

type helixCheermotesResponse struct {
	Data []struct {
		Prefix string `json:"prefix"`
		Tiers  []struct {
			MinBits int    `json:"min_bits"`
			ID      string `json:"id"`
			Color   string `json:"color"`
			Images  struct {
				Dark struct {
					Animated map[string]string `json:"animated"`
					Static   map[string]string `json:"static"`
				} `json:"dark"`
			} `json:"images"`
		} `json:"tiers"`
	} `json:"data"`
}

func pickCheermoteImageScale(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	for _, scale := range []string{"2", "1", "3", "4"} {
		if u := strings.TrimSpace(m[scale]); u != "" {
			return u
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if u := strings.TrimSpace(m[k]); u != "" {
			return u
		}
	}
	return ""
}

// ParseHelixCheermotes parses a Twitch Helix GET /bits/cheermotes JSON payload into a CheermoteCatalog.
func ParseHelixCheermotes(data []byte) (*CheermoteCatalog, error) {
	var resp helixCheermotesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal helix cheermotes: %w", err)
	}
	actions := make(map[string]CheermoteAction, len(resp.Data))
	for _, item := range resp.Data {
		prefix := strings.TrimSpace(item.Prefix)
		lower := strings.ToLower(prefix)
		if lower == "" || len(item.Tiers) == 0 {
			continue
		}
		tiers := make([]CheermoteTier, 0, len(item.Tiers))
		for _, t := range item.Tiers {
			tiers = append(tiers, CheermoteTier{
				MinBits:     t.MinBits,
				ID:          strings.TrimSpace(t.ID),
				Color:       strings.TrimSpace(t.Color),
				AnimatedURL: pickCheermoteImageScale(t.Images.Dark.Animated),
				StaticURL:   pickCheermoteImageScale(t.Images.Dark.Static),
			})
		}
		slices.SortFunc(tiers, func(a, b CheermoteTier) int {
			return a.MinBits - b.MinBits
		})
		actions[lower] = CheermoteAction{
			Prefix: prefix,
			Tiers:  tiers,
		}
	}
	return &CheermoteCatalog{actions: actions}, nil
}

func defaultCheermoteTierAndColor(bits int) (tierID string, color string) {
	switch {
	case bits >= 10000:
		return "10000", "#f43021"
	case bits >= 5000:
		return "5000", "#0099fe"
	case bits >= 1000:
		return "1000", "#1db2a5"
	case bits >= 100:
		return "100", "#9c3ee8"
	default:
		return "1", "#979797"
	}
}

// Resolve looks up the animated/static image URL and tier color for a cheermote prefix and bit amount.
// If cat is nil or lacks the prefix, it falls back to standard global Twitch cheermote prefixes.
func (cat *CheermoteCatalog) Resolve(prefix string, bits int) (imgURL string, color string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(prefix))
	if lower == "" || bits <= 0 {
		return "", "", false
	}

	if cat != nil && len(cat.actions) > 0 {
		if act, found := cat.actions[lower]; found && len(act.Tiers) > 0 {
			chosen := act.Tiers[0]
			for _, t := range act.Tiers {
				if t.MinBits <= bits {
					chosen = t
				} else {
					break
				}
			}
			u := chosen.AnimatedURL
			if u == "" {
				u = chosen.StaticURL
			}
			col := chosen.Color
			if col == "" {
				_, col = defaultCheermoteTierAndColor(bits)
			}
			if u != "" {
				return u, col, true
			}
		}
	}

	if _, isStd := standardTwitchCheermotes[lower]; isStd {
		tierID, col := defaultCheermoteTierAndColor(bits)
		return fmt.Sprintf("https://d3aqoihi2n8ty8.cloudfront.net/actions/%s/dark/animated/%s/2.gif", lower, tierID), col, true
	}

	return "", "", false
}

func (c *Connector) cheermoteCatalog() *CheermoteCatalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cheermotes
}

func (c *Connector) fetchCheermotes(ctx context.Context, token, broadcasterID string) {
	c.mu.Lock()
	httpClient := c.httpClient
	helixBase := strings.TrimRight(c.helixBaseURL, "/")
	oauth := c.oauth
	channel := c.channel
	c.mu.Unlock()

	if token == "" || oauth == nil {
		return
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	clientID := DefaultTwitchPublicClientID
	if oauth.ClientID() != "" {
		clientID = oauth.ClientID()
	}

	reqURL := helixBase + "/bits/cheermotes"
	if broadcasterID != "" {
		reqURL += "?broadcaster_id=" + url.QueryEscape(broadcasterID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", clientID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var raw []byte
	buf := make([]byte, 0, 16*1024)
	tmp := make([]byte, 4096)
	const maxCheermoteBytes = 2 << 20
	for len(buf) < maxCheermoteBytes {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	raw = buf

	cat, err := ParseHelixCheermotes(raw)
	if err != nil {
		log.Printf("[Twitch] Failed to parse Helix cheermotes: %v", err)
		return
	}

	c.mu.Lock()
	if c.channel == channel {
		c.cheermotes = cat
	}
	c.mu.Unlock()
}

func splitCheermoteToken(tok string) (prefix string, bits int, ok bool) {
	if len(tok) < 2 {
		return "", 0, false
	}
	digitStart := len(tok)
	for digitStart > 0 && tok[digitStart-1] >= '0' && tok[digitStart-1] <= '9' {
		digitStart--
	}
	if digitStart == 0 || digitStart == len(tok) {
		return "", 0, false
	}
	prefix = tok[:digitStart]
	hasLetter := false
	for i := 0; i < len(prefix); i++ {
		ch := prefix[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			hasLetter = true
		} else if (ch < '0' || ch > '9') && ch != '_' {
			return "", 0, false
		}
	}
	if !hasLetter {
		return "", 0, false
	}
	n, err := strconv.Atoi(tok[digitStart:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return prefix, n, true
}

func overlapsAnySpan(start, end int, spans []chat.EmoteSpan) bool {
	for _, sp := range spans {
		if start < sp.End && end > sp.Start {
			return true
		}
	}
	return false
}

// ExtractIRCCheermoteSpans scans a Twitch IRC chat message (when msg.Bits > 0) for cheermote tokens
// such as "Cheer100" or "Kappa500", returning rune-indexed EmoteSpans that do not overlap existing emotes.
func ExtractIRCCheermoteSpans(text string, existing []chat.EmoteSpan, catalog *CheermoteCatalog) []chat.EmoteSpan {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	runes := []rune(text)
	var out []chat.EmoteSpan
	i := 0
	for i < len(runes) {
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		for i < len(runes) && !unicode.IsSpace(runes[i]) {
			i++
		}
		end := i
		trimmedEnd := end
		for trimmedEnd > start && strings.ContainsRune(".,!?;:\"'()[]{}", runes[trimmedEnd-1]) {
			trimmedEnd--
		}
		if trimmedEnd <= start {
			continue
		}
		tok := string(runes[start:trimmedEnd])
		prefix, bits, ok := splitCheermoteToken(tok)
		if !ok {
			continue
		}
		if overlapsAnySpan(start, trimmedEnd, existing) {
			continue
		}
		imgURL, col, resolved := catalog.Resolve(prefix, bits)
		if !resolved {
			continue
		}
		out = append(out, chat.EmoteSpan{
			Start: start,
			End:   trimmedEnd,
			ID:    "cheermote:" + strings.ToLower(prefix),
			Name:  tok,
			URL:   imgURL,
			Bits:  bits,
			Color: col,
		})
	}
	return out
}
