package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/audio"
	"github.com/mrpoundsign/TownCrier/internal/bots"
	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
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
	eventLog  *eventlog.Manager
	eventSeq  atomic.Uint64

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
	s.queue.SetOnItemOutcome(func(item *queue.Item, spoken bool, outcome string, detail string) {
		if item == nil || item.ID == "" {
			return
		}
		elog := s.EventLogger()
		if elog == nil {
			return
		}
		_ = elog.UpdateOutcome(context.Background(), item.ID, eventlog.OutcomeUpdate{
			Spoken:        spoken,
			Outcome:       eventlog.EventOutcome(outcome),
			OutcomeDetail: detail,
			Speed:         item.Speed,
		})
	})
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
	elog := s.eventLog
	s.eventLog = nil
	s.mu.Unlock()

	if elog != nil {
		_ = elog.Close()
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
	s.cfg = newCfg.Clone()
	elog := s.eventLog
	if s.running {
		s.syncConnectorsLocked()
	}
	s.mu.Unlock()

	if elog != nil {
		elog.SetEnabled(newCfg.Logging.Enabled)
		elog.SetRetentionPolicy(eventlog.RetentionPolicy{
			ChatRetentionDays:  newCfg.Logging.ChatRetentionDays,
			EventRetentionDays: newCfg.Logging.EventRetentionDays,
		})
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

// SetEventLogger attaches or updates the structured event log manager.
func (s *Session) SetEventLogger(mgr *eventlog.Manager) {
	s.mu.Lock()
	s.eventLog = mgr
	cfg := s.cfg.Clone()
	s.mu.Unlock()

	if mgr != nil && cfg != nil {
		mgr.SetEnabled(cfg.Logging.Enabled)
		mgr.SetRetentionPolicy(eventlog.RetentionPolicy{
			ChatRetentionDays:  cfg.Logging.ChatRetentionDays,
			EventRetentionDays: cfg.Logging.EventRetentionDays,
		})
	}
}

// EventLogger returns the active structured event log manager, if any.
func (s *Session) EventLogger() *eventlog.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventLog
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

func (s *Session) buildLogEntry(event *chat.Event, rawText string, rawEmotes []chat.EmoteSpan, spokenText, voiceID string, speed float32, passedFilters bool, outcome eventlog.EventOutcome, detail string) *eventlog.Entry {
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	displayName := event.DisplayName
	if displayName == "" {
		displayName = event.UserName
	}
	var emotesCopy []chat.EmoteSpan
	if len(rawEmotes) > 0 {
		emotesCopy = make([]chat.EmoteSpan, len(rawEmotes))
		copy(emotesCopy, rawEmotes)
	}
	return &eventlog.Entry{
		ID:            event.ID,
		CreatedAt:     ts,
		UpdatedAt:     time.Now().UTC(),
		Platform:      event.Platform,
		Channel:       event.Channel,
		Kind:          string(event.Kind),
		IsPaidOrEvent: event.IsPaidOrEvent(),
		UserID:        event.UserID,
		UserName:      event.UserName,
		DisplayName:   displayName,
		Roles:         event.Roles,
		RawText:       rawText,
		Emotes:        emotesCopy,
		SpokenText:    spokenText,
		VoiceID:       voiceID,
		Speed:         speed,
		Amount:        event.Amount,
		Currency:      event.Currency,
		Months:        event.Months,
		Count:         event.Count,
		PassedFilters: passedFilters,
		Spoken:        false,
		Outcome:       outcome,
		OutcomeDetail: detail,
	}
}

func (s *Session) recordEventLog(entry *eventlog.Entry) {
	elog := s.EventLogger()
	if elog == nil || entry == nil {
		return
	}
	_ = elog.Append(context.Background(), entry)
}

// DispatchEvent routes an inbound stream event through bot matching, routing, sanitization, and queueing.
func (s *Session) DispatchEvent(event *chat.Event) error {
	if event == nil {
		return errors.New("cannot dispatch nil event")
	}

	cfg := s.Config()
	if event.ID == "" {
		event.ID = fmt.Sprintf("ev-%d-%d", time.Now().UTC().UnixNano(), s.eventSeq.Add(1))
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Kind == "" {
		event.Kind = chat.EventKindMessage
	}
	if event.Channel == "" {
		if pCfg, ok := cfg.Platforms[event.Platform]; ok && pCfg.AccountName != "" {
			event.Channel = pCfg.AccountName
		}
	}
	rawText := event.Text
	var rawEmotes []chat.EmoteSpan
	if len(event.EmoteSpans) > 0 {
		rawEmotes = make([]chat.EmoteSpan, len(event.EmoteSpans))
		copy(rawEmotes, event.EmoteSpans)
	}

	log.Printf("[Session] DispatchEvent: user=%q platform=%s channel=%q text=%q roles=%v", event.UserName, event.Platform, event.Channel, event.Text, event.Roles)

	// 1. Bot Matcher (known bots, platform bot signals, viewer blocklist/allowlist)
	if s.bots != nil && s.bots.ShouldIgnore(event, cfg) {
		log.Printf("[Session] Event dropped by bot matcher: user=%q", event.UserName)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedBot, "Ignored by bot matcher or viewer blocklist"))
		return nil
	}

	// 2. Platform Read Rules & Role Gating (PRD D18)
	if pCfg, ok := cfg.Platforms[event.Platform]; ok {
		if event.Kind != chat.EventKindMessage && pCfg.Notifications != nil {
			if notif, hasNotif := pCfg.Notifications[string(event.Kind)]; hasNotif {
				if !notif.Enabled {
					log.Printf("[Session] Notification event dropped (disabled): kind=%s user=%q", event.Kind, event.UserName)
					s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedDisabled, fmt.Sprintf("Notification kind %q disabled", event.Kind)))
					return nil
				}
				if notif.Threshold > 0 {
					val := event.Amount
					if event.Kind == chat.EventKindRaid {
						val = float64(event.Count)
					}
					if val < notif.Threshold {
						log.Printf("[Session] Notification event below threshold (%.2f < %.2f): kind=%s user=%q", val, notif.Threshold, event.Kind, event.UserName)
						s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedThreshold, fmt.Sprintf("Below threshold (%.2f < %.2f)", val, notif.Threshold)))
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
			s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedReadRules, "Rejected by platform read rules"))
			return nil
		}
		if strippedText != "" {
			event.Text = strippedText
		}
	}

	// 3. Text & Emote Sanitizer on raw message text
	// Emote offsets are relative to event.Text before chatter name prefixing.
	_, speakEmotes, _, _, _ := cfg.EffectiveChatRules(event.Platform)
	blockedWords := chat.EffectiveBlockedWords(cfg.Safety.BlockDefaultBadWords, cfg.Safety.BlockedWords)
	if event.Text != "" {
		event.Text = s.sanitizer.Sanitize(event.Text, event.EmoteSpans, speakEmotes, blockedWords)
		event.EmoteSpans = nil // already applied to event.Text
		if event.Text == "" && event.Kind == chat.EventKindMessage {
			log.Printf("[Session] Chat message sanitized to empty text: user=%q", event.UserName)
			s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedSanitized, "Sanitized to empty text (emotes or blocked words)"))
			return nil
		}
	}

	// 4. Resolve Voice, Speed, and Template
	resolved := s.router.Resolve(event, cfg)
	if resolved.Text == "" {
		log.Printf("[Session] Event resolved to empty text: user=%q", event.UserName)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedSanitized, "Resolved template text is empty"))
		return nil
	}

	// 5. Final sanitation pass on formatted text (length clamping, blocked words in templates/names)
	sanitizedText := s.sanitizer.Sanitize(resolved.Text, nil, true, blockedWords)
	if sanitizedText == "" {
		log.Printf("[Session] Event sanitized to empty text: user=%q original=%q", event.UserName, resolved.Text)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedSanitized, "Formatted speech text sanitized to empty"))
		return nil
	}

	// 6. Priority tier assignment
	priority := 1
	if event.IsPaidOrEvent() {
		priority = 0
	}

	// 7. Record initial PassedFilters=true / Queued log entry before enqueueing so fast queue callbacks find the row
	s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, sanitizedText, resolved.VoiceID, resolved.Speed, true, eventlog.OutcomeQueued, ""))

	// 8. Enqueue for speech synthesis
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
		evCopy := *event
		if rawText != "" {
			evCopy.Text = rawText
		}
		evCopy.EmoteSpans = rawEmotes
		go onDispatched(&evCopy, resolved.VoiceID)
	}

	return nil
}

// DispatchModeration processes Twitch, YouTube, or Kick moderation sync events (D29).
func (s *Session) DispatchModeration(modEvent *chat.ModerationEvent) {
	if modEvent == nil || s.queue == nil {
		return
	}

	cfg := s.Config()
	ch := modEvent.Channel
	if ch == "" {
		if pCfg, ok := cfg.Platforms[modEvent.Platform]; ok && pCfg.AccountName != "" {
			ch = pCfg.AccountName
		}
	}
	ts := modEvent.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	detail := string(modEvent.Action)
	if modEvent.TargetMessageID != "" {
		detail += " (msg=" + modEvent.TargetMessageID + ")"
	}
	if modEvent.TargetUserID != "" {
		detail += " (user=" + modEvent.TargetUserID + ")"
	}

	s.recordEventLog(&eventlog.Entry{
		ID:            fmt.Sprintf("mod-%d-%d", ts.UTC().UnixNano(), s.eventSeq.Add(1)),
		CreatedAt:     ts,
		UpdatedAt:     ts,
		Platform:      modEvent.Platform,
		Channel:       ch,
		Kind:          "moderation",
		IsPaidOrEvent: false,
		UserID:        modEvent.TargetUserID,
		UserName:      modEvent.TargetUserID,
		DisplayName:   modEvent.TargetUserID,
		RawText:       fmt.Sprintf("Moderation action: %s", modEvent.Action),
		PassedFilters: false,
		Spoken:        false,
		Outcome:       eventlog.OutcomeModerationEvent,
		OutcomeDetail: detail,
	})

	switch modEvent.Action {
	case chat.ModActionDeleteMessage:
		s.queue.CancelMessage(modEvent.TargetMessageID)
	case chat.ModActionBanUser:
		s.queue.CancelUser(modEvent.Platform, modEvent.TargetUserID)
	case chat.ModActionClearChat:
		s.queue.CancelAllForPlatform(modEvent.Platform)
	}
}
