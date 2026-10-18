package bots

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

//go:embed known_bots.json
var defaultKnownBotsJSON []byte

// Matcher identifies stream bots and checks ignore/allow rules for chat messages.
type Matcher struct {
	mu        sync.RWMutex
	knownBots map[string]struct{}
}

// NewMatcher creates a new bot matcher initialized with embedded known bots.
func NewMatcher() (*Matcher, error) {
	var bots []string
	if err := json.Unmarshal(defaultKnownBotsJSON, &bots); err != nil {
		return nil, fmt.Errorf("unmarshal known bots: %w", err)
	}

	botMap := make(map[string]struct{}, len(bots))
	for _, b := range bots {
		botMap[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
	}

	return &Matcher{
		knownBots: botMap,
	}, nil
}

// ShouldIgnore determines whether a chat message should be discarded due to bot or broadcaster filtering.
func (m *Matcher) ShouldIgnore(event *chat.Event, cfg *config.Config) bool {
	if event == nil || cfg == nil {
		return false
	}

	userNameLower := strings.ToLower(strings.TrimSpace(event.UserName))

	// 1. Viewer Table Overrides (Allowed vs Ignored)
	// Case-insensitive lookup by platform, userID, and username
	if len(cfg.Viewers) > 0 {
		if v, ok := config.LookupViewer(cfg.Viewers, event.Platform, event.UserID, event.UserName); ok {
			if v.Allowed {
				return false
			}
			if v.Ignored {
				return true
			}
		}
	}

	// 2. Platform Bot Signal
	if event.IsBotSignal {
		return true
	}

	// 3. User-Configured Custom Bots List
	uIDLower := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(event.UserID), "@"))
	uNameClean := strings.TrimPrefix(userNameLower, "@")
	for _, cb := range cfg.Bots.CustomBots {
		cbNorm := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cb), "@"))
		if cbNorm != "" && (cbNorm == uNameClean || cbNorm == userNameLower || (uIDLower != "" && cbNorm == uIDLower)) {
			return true
		}
	}

	// 4. Known Bots List
	if cfg.Bots.IgnoreKnownBots {
		m.mu.RLock()
		_, isBot := m.knownBots[userNameLower]
		if !isBot && uNameClean != userNameLower {
			_, isBot = m.knownBots[uNameClean]
		}
		m.mu.RUnlock()
		if isBot {
			return true
		}
	}

	return false
}

// ExcludedUserNames returns the deduplicated list of lowercased bot and ignored usernames
// from the active configuration (known bots if enabled, custom bots, and ignored viewers,
// minus any explicitly allowed viewer accounts).
func ExcludedUserNames(cfg *config.Config) []string {
	allowed := make(map[string]struct{})
	seen := make(map[string]struct{})
	var result []string

	add := func(name string) {
		n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@"))
		if n == "" {
			return
		}
		if _, isAllowed := allowed[n]; isAllowed {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		result = append(result, n)
	}

	if cfg != nil {
		for _, v := range cfg.Viewers {
			if v.Allowed {
				for _, acc := range v.EffectiveAccounts() {
					n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(acc.UserID), "@"))
					if n != "" {
						allowed[n] = struct{}{}
					}
				}
			}
		}
		for _, v := range cfg.Viewers {
			if v.Ignored && !v.Allowed {
				for _, acc := range v.EffectiveAccounts() {
					add(acc.UserID)
				}
			}
		}
		for _, cb := range cfg.Bots.CustomBots {
			add(cb)
		}
	}

	if cfg == nil || cfg.Bots.IgnoreKnownBots {
		var known []string
		if err := json.Unmarshal(defaultKnownBotsJSON, &known); err == nil {
			for _, kb := range known {
				add(kb)
			}
		}
	}

	return result
}

// IsKnownBot reports whether the given name is in the known bots set.
func (m *Matcher) IsKnownBot(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.knownBots[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// AddBot adds a bot username to the matcher.
func (m *Matcher) AddBot(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.knownBots[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
}

// RemoveBot removes a bot username from the matcher.
func (m *Matcher) RemoveBot(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.knownBots, strings.ToLower(strings.TrimSpace(name)))
}

