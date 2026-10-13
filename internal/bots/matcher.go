package bots

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
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
	// Keyed by platform:user_id, with fallback to username
	if len(cfg.Viewers) > 0 {
		keyWithID := event.Platform + ":" + event.UserID
		if v, ok := cfg.Viewers[keyWithID]; ok {
			if v.Allowed {
				return false
			}
			if v.Ignored {
				return true
			}
		}
		if v, ok := cfg.Viewers[userNameLower]; ok {
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

	// 3. Known Bots List
	if cfg.Bots.IgnoreKnownBots {
		m.mu.RLock()
		_, isBot := m.knownBots[userNameLower]
		m.mu.RUnlock()
		if isBot {
			return true
		}
	}

	return false
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
