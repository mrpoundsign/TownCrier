package app

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/bots"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/platform/kick"
	"github.com/mrpoundsign/TownCrier/internal/platform/tiktok"
	"github.com/mrpoundsign/TownCrier/internal/platform/twitch"
	"github.com/mrpoundsign/TownCrier/internal/platform/youtube"
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
	twitchOAuth       *twitch.OAuthManager
	onTwitchStatus    func(status string, detail string)
	youtubeConnector  *youtube.Connector
	onYouTubeStatus   func(status string, detail string)
	tiktokConnector   *tiktok.Connector
	onTikTokStatus    func(status string, detail string)
	kickConnector     *kick.Connector
	onKickStatus      func(status string, detail string)
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
		cfg:         cfg.Clone(),
		secrets:     store,
		router:      router,
		sanitizer:   sanitizer,
		bots:        matcher,
		twitchOAuth: twitch.NewOAuthManager("", store),
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

	s.youtubeConnector = youtube.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(modEvent *chat.ModerationEvent) {
			s.DispatchModeration(modEvent)
		},
		func(status youtube.ConnectionStatus, detail string) {
			s.mu.RLock()
			fn := s.onYouTubeStatus
			s.mu.RUnlock()
			if fn != nil {
				fn(string(status), detail)
			}
		},
	)

	s.tiktokConnector = tiktok.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(status tiktok.ConnectionStatus, detail string) {
			s.mu.RLock()
			fn := s.onTikTokStatus
			s.mu.RUnlock()
			if fn != nil {
				fn(string(status), detail)
			}
		},
	)

	s.kickConnector = kick.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(modEvent *chat.ModerationEvent) {
			s.DispatchModeration(modEvent)
		},
		func(status kick.ConnectionStatus, detail string) {
			s.mu.RLock()
			fn := s.onKickStatus
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
	if s.youtubeConnector != nil {
		_ = s.youtubeConnector.Stop()
	}
	if s.tiktokConnector != nil {
		_ = s.tiktokConnector.Stop()
	}
	if s.kickConnector != nil {
		_ = s.kickConnector.Stop()
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
	if s.youtubeConnector != nil {
		_ = s.youtubeConnector.Stop()
	}
	if s.tiktokConnector != nil {
		_ = s.tiktokConnector.Stop()
	}
	if s.kickConnector != nil {
		_ = s.kickConnector.Stop()
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

// UpdateConfig safely mutates the session configuration, reloads subsystems, and persists to disk.
func (s *Session) UpdateConfig(fn func(c *config.Config)) error {
	if fn == nil {
		return errors.New("mutator function cannot be nil")
	}
	cfg := s.Config()
	fn(cfg)
	if err := s.ReloadConfig(cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	if err := config.Save("", cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (s *Session) syncConnectorsLocked() {
	if s.twitchConnector != nil {
		p, ok := s.cfg.Platforms["twitch"]
		if ok && p.Enabled && p.AccountName != "" {
			_ = s.twitchConnector.Start(p.AccountName)
		} else {
			_ = s.twitchConnector.Stop()
		}
	}

	if s.youtubeConnector != nil {
		yt, ok := s.cfg.Platforms["youtube"]
		if ok && yt.Enabled && yt.AccountName != "" {
			_ = s.youtubeConnector.Start(yt.AccountName)
		} else {
			_ = s.youtubeConnector.Stop()
		}
	}

	if s.tiktokConnector != nil {
		tt, ok := s.cfg.Platforms["tiktok"]
		if ok && tt.Enabled && tt.AccountName != "" {
			_ = s.tiktokConnector.Start(tt.AccountName)
		} else {
			_ = s.tiktokConnector.Stop()
		}
	}

	if s.kickConnector != nil {
		kc, ok := s.cfg.Platforms["kick"]
		if ok && kc.Enabled && kc.AccountName != "" {
			_ = s.kickConnector.Start(kc.AccountName)
		} else {
			_ = s.kickConnector.Stop()
		}
	}
}

// TwitchConnector returns the underlying Twitch connector.
func (s *Session) TwitchConnector() *twitch.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.twitchConnector
}

// YouTubeConnector returns the underlying YouTube connector.
func (s *Session) YouTubeConnector() *youtube.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.youtubeConnector
}

// TikTokConnector returns the underlying TikTok connector.
func (s *Session) TikTokConnector() *tiktok.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tiktokConnector
}

// KickConnector returns the underlying Kick connector.
func (s *Session) KickConnector() *kick.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kickConnector
}

// TwitchOAuth returns the Twitch OAuth manager.
func (s *Session) TwitchOAuth() *twitch.OAuthManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.twitchOAuth
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

// SetOnYouTubeStatus registers a callback for YouTube connection status changes.
func (s *Session) SetOnYouTubeStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onYouTubeStatus = fn
	if s.youtubeConnector != nil && fn != nil {
		curStatus := s.youtubeConnector.Status()
		curTarget := s.youtubeConnector.Target()
		go fn(string(curStatus), curTarget)
	}
}

// SetOnTikTokStatus registers a callback for TikTok connection status changes.
func (s *Session) SetOnTikTokStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTikTokStatus = fn
	if s.tiktokConnector != nil && fn != nil {
		curStatus := s.tiktokConnector.Status()
		curUser := s.tiktokConnector.Username()
		go fn(string(curStatus), curUser)
	}
}

// SetOnKickStatus registers a callback for Kick connection status changes.
func (s *Session) SetOnKickStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onKickStatus = fn
	if s.kickConnector != nil && fn != nil {
		curStatus := s.kickConnector.Status()
		curChan := s.kickConnector.Channel()
		go fn(string(curStatus), curChan)
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

// Panic immediately halts audio playback (<50ms), purges the speech queue, and latches Panic Mode (D28).
func (s *Session) Panic() {
	if s.queue != nil {
		s.queue.Panic()
	}
}

// ClearPanic unlatches Panic Mode and resumes speech processing.
func (s *Session) ClearPanic() {
	if s.queue != nil {
		s.queue.ClearPanic()
	}
}

// TogglePanic toggles Emergency Panic Mute mode on or off, returning the new panicked state.
func (s *Session) TogglePanic() bool {
	if s.queue != nil {
		return s.queue.TogglePanic()
	}
	return false
}

// IsPanicked returns true if Emergency Panic Mute mode is currently latched active.
func (s *Session) IsPanicked() bool {
	if s.queue != nil {
		return s.queue.IsPanicked()
	}
	return false
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
		if event.Kind != chat.EventKindMessage && pCfg.Notifications != nil {
			if notif, hasNotif := pCfg.Notifications[string(event.Kind)]; hasNotif {
				if !notif.Enabled {
					log.Printf("[Session] Notification event dropped (disabled): kind=%s user=%q", event.Kind, event.UserName)
					return nil
				}
				if notif.Threshold > 0 {
					val := event.Amount
					if event.Kind == chat.EventKindRaid {
						val = float64(event.Count)
					}
					if val < notif.Threshold {
						log.Printf("[Session] Notification event below threshold (%.2f < %.2f): kind=%s user=%q", val, notif.Threshold, event.Kind, event.UserName)
						return nil
					}
				}
				if notif.Voice != "" && event.VoiceOverride == "" {
					event.VoiceOverride = notif.Voice
				}
			}
		}

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
