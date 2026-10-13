package app

import (
	"errors"
	"sync"

	"github.com/mrpoundsign/OctochorpeBot/internal/audio"
	"github.com/mrpoundsign/OctochorpeBot/internal/bots"
	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
	"github.com/mrpoundsign/OctochorpeBot/internal/config"
	"github.com/mrpoundsign/OctochorpeBot/internal/queue"
	"github.com/mrpoundsign/OctochorpeBot/internal/secrets"
	"github.com/mrpoundsign/OctochorpeBot/internal/tts"
)

var (
	ErrAlreadyStarted = errors.New("session already started")
	ErrNotRunning     = errors.New("session not running")
)

// Session coordinates application state, configuration, secrets, and subsystems.
type Session struct {
	mu      sync.RWMutex
	running bool

	cfg     *config.Config
	secrets secrets.Store

	player    audio.Player
	engine    tts.Engine
	queue     *queue.Queue
	router    *chat.Router
	sanitizer *chat.Sanitizer
	bots      *bots.Matcher
}

// NewSession creates an application session initialized with configuration and secret store.
func NewSession(cfg *config.Config, store secrets.Store) *Session {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if store == nil {
		store = secrets.NewKeyringStore("")
	}

	matcher, _ := bots.NewMatcher()
	sanitizer := chat.NewSanitizer(chat.DefaultMaxRunes)
	router := chat.NewRouter()

	s := &Session{
		cfg:       cfg.Clone(),
		secrets:   store,
		router:    router,
		sanitizer: sanitizer,
		bots:      matcher,
	}

	s.queue = queue.NewQueue(nil, nil, s.Config)
	return s
}

// Start transitions the session to running state and initializes active subsystems.
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return ErrAlreadyStarted
	}

	s.running = true
	s.queue.Resume()
	return nil
}

// Stop halts all active subsystems and transitions session to stopped state.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return ErrNotRunning
	}

	s.running = false
	s.queue.Pause()
	return nil
}

// IsRunning reports whether the session is currently active.
func (s *Session) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Config returns a thread-safe clone of the active configuration.
func (s *Session) Config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// Secrets returns the secret store.
func (s *Session) Secrets() secrets.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secrets
}

// ReloadConfig dynamically updates the active configuration.
func (s *Session) ReloadConfig(newCfg *config.Config) error {
	if newCfg == nil {
		return errors.New("new configuration cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = newCfg.Clone()
	return nil
}

// SetPlayer attaches or updates the audio playback device.
func (s *Session) SetPlayer(p audio.Player) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.player = p
	if s.queue != nil {
		s.queue.SetPlayer(p)
	}
}

// SetEngine attaches or updates the speech synthesis engine.
func (s *Session) SetEngine(e tts.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = e
	if s.queue != nil {
		s.queue.SetEngine(e)
	}
}

// Player returns the current audio player.
func (s *Session) Player() audio.Player {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.player
}

// Engine returns the current speech synthesis engine.
func (s *Session) Engine() tts.Engine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.engine
}

// Queue returns the priority speech queue.
func (s *Session) Queue() *queue.Queue {
	return s.queue
}

// Router returns the chat format and voice router.
func (s *Session) Router() *chat.Router {
	return s.router
}

// Sanitizer returns the text and emote cleaner.
func (s *Session) Sanitizer() *chat.Sanitizer {
	return s.sanitizer
}

// Bots returns the known bot and broadcaster matcher.
func (s *Session) Bots() *bots.Matcher {
	return s.bots
}

// Panic immediately halts audio playback (<50ms) and purges the speech queue (D28).
func (s *Session) Panic() {
	if s.queue != nil {
		s.queue.Panic()
	}
}

// DispatchEvent routes an inbound stream event through bot matching, routing, sanitization, and queueing.
func (s *Session) DispatchEvent(event *chat.Event) error {
	if event == nil {
		return errors.New("cannot dispatch nil event")
	}

	cfg := s.Config()

	// 1. Bot & Broadcaster Matcher
	if s.bots != nil && s.bots.ShouldIgnore(event, cfg) {
		return nil
	}

	// 2. Resolve Voice, Speed, and Template
	resolved := s.router.Resolve(event, cfg)

	// 3. Text & Emote Sanitizer
	sanitizedText := s.sanitizer.Sanitize(resolved.Text, event.EmoteSpans, resolved.SpeakEmotes, cfg.Safety.BlockedWords)
	if sanitizedText == "" {
		return nil
	}

	// 4. Priority tier assignment
	priority := 1
	if event.IsPaidOrEvent() {
		priority = 0
	}

	// 5. Enqueue for speech synthesis
	item := &queue.Item{
		ID:       event.ID,
		Event:    event,
		Text:     sanitizedText,
		VoiceID:  resolved.VoiceID,
		Speed:    resolved.Speed,
		Priority: priority,
	}

	return s.queue.Enqueue(item)
}

// DispatchModeration processes Twitch or YouTube moderation sync events (D29).
func (s *Session) DispatchModeration(modEvent *chat.ModerationEvent) {
	if modEvent == nil || s.queue == nil {
		return
	}

	switch modEvent.Action {
	case chat.ModActionDeleteMessage:
		s.queue.CancelMessage(modEvent.TargetMessageID)
	case chat.ModActionBanUser:
		s.queue.CancelUser(modEvent.Platform, modEvent.TargetUserID)
	}
}
