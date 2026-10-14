package app

import (
	"errors"
	"log"
	"sync"

	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/bots"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/platform/twitch"
	"github.com/mrpoundsign/TownCrier/internal/queue"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"github.com/mrpoundsign/TownCrier/internal/tts"
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

	twitchConnector   *twitch.Connector
	onTwitchStatus    func(status string, detail string)
	onEventDispatched func(event *chat.Event, voiceID string)
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

	s.twitchConnector = twitch.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(modEvent *chat.ModerationEvent) {
			s.DispatchModeration(modEvent)
		},
		func(status twitch.ConnectionStatus, detail string) {
			s.mu.RLock()
			fn := s.onTwitchStatus
			s.mu.RUnlock()
			if fn != nil {
				fn(string(status), detail)
			}
		},
	)

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
	s.syncConnectorsLocked()
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
	if s.twitchConnector != nil {
		_ = s.twitchConnector.Stop()
	}
	return nil
}

// Close permanently shuts down the session, queue dispatcher, and platform connectors.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.running = false
	if s.twitchConnector != nil {
		_ = s.twitchConnector.Stop()
	}
	if s.queue != nil {
		s.queue.Close()
	}
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
	if s.running {
		s.syncConnectorsLocked()
	}
	return nil
}

func (s *Session) syncConnectorsLocked() {
	if s.twitchConnector == nil {
		return
	}

	p, ok := s.cfg.Platforms["twitch"]
	if ok && p.Enabled && p.AccountName != "" {
		_ = s.twitchConnector.Start(p.AccountName)
	} else {
		_ = s.twitchConnector.Stop()
	}
}

// TwitchConnector returns the underlying Twitch connector.
func (s *Session) TwitchConnector() *twitch.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.twitchConnector
}

// SetOnTwitchStatus registers a callback for Twitch connection status changes.
func (s *Session) SetOnTwitchStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTwitchStatus = fn
	if s.twitchConnector != nil && fn != nil {
		curStatus := s.twitchConnector.Status()
		curChannel := s.twitchConnector.Channel()
		go fn(string(curStatus), curChannel)
	}
}

// SetOnEventDispatched registers a callback fired whenever a stream event is enqueued for speech.
func (s *Session) SetOnEventDispatched(fn func(event *chat.Event, voiceID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEventDispatched = fn
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
	log.Printf("[Session] DispatchEvent: user=%q platform=%s text=%q roles=%v", event.UserName, event.Platform, event.Text, event.Roles)

	// 1. Bot Matcher (known bots, platform bot signals, viewer blocklist/allowlist)
	if s.bots != nil && s.bots.ShouldIgnore(event, cfg) {
		log.Printf("[Session] Event dropped by bot matcher: user=%q", event.UserName)
		return nil
	}

	// 2. Platform Read Rules & Role Gating (PRD D18)
	if pCfg, ok := cfg.Platforms[event.Platform]; ok {
		shouldSpeak, strippedText := chat.EvaluateReadRules(event, pCfg.ReadRules)
		if !shouldSpeak {
			log.Printf("[Session] Event dropped by ReadRules: user=%q roles=%v rules=%+v", event.UserName, event.Roles, pCfg.ReadRules)
			return nil
		}
		if strippedText != "" {
			event.Text = strippedText
		}
	}

	// 3. Text & Emote Sanitizer on raw message text
	// Emote offsets are relative to event.Text before chatter name prefixing.
	_, speakEmotes, _, _, _ := cfg.EffectiveChatRules(event.Platform)
	if event.Text != "" {
		event.Text = s.sanitizer.Sanitize(event.Text, event.EmoteSpans, speakEmotes, cfg.Safety.BlockedWords)
		event.EmoteSpans = nil // already applied to event.Text
		if event.Text == "" && event.Kind == chat.EventKindMessage {
			log.Printf("[Session] Chat message sanitized to empty text: user=%q", event.UserName)
			return nil
		}
	}

	// 4. Resolve Voice, Speed, and Template
	resolved := s.router.Resolve(event, cfg)
	if resolved.Text == "" {
		log.Printf("[Session] Event resolved to empty text: user=%q", event.UserName)
		return nil
	}

	// 5. Final sanitation pass on formatted text (length clamping, blocked words in templates/names)
	sanitizedText := s.sanitizer.Sanitize(resolved.Text, nil, true, cfg.Safety.BlockedWords)
	if sanitizedText == "" {
		log.Printf("[Session] Event sanitized to empty text: user=%q original=%q", event.UserName, resolved.Text)
		return nil
	}

	// 5. Priority tier assignment
	priority := 1
	if event.IsPaidOrEvent() {
		priority = 0
	}

	// 6. Enqueue for speech synthesis
	item := &queue.Item{
		ID:       event.ID,
		Event:    event,
		Text:     sanitizedText,
		VoiceID:  resolved.VoiceID,
		Speed:    resolved.Speed,
		Priority: priority,
	}

	log.Printf("[Session] Enqueued speech item: %q (voice=%s, speed=%.2f, priority=%d)", sanitizedText, item.VoiceID, item.Speed, priority)
	if err := s.queue.Enqueue(item); err != nil {
		return err
	}

	s.mu.RLock()
	onDispatched := s.onEventDispatched
	s.mu.RUnlock()
	if onDispatched != nil {
		go onDispatched(event, resolved.VoiceID)
	}

	return nil
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
