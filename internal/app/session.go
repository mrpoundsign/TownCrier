package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	youtubeOAuth      *youtube.OAuthManager
	onYouTubeStatus   func(status string, detail string)
	tiktokConnector   *tiktok.Connector
	onTikTokStatus    func(status string, detail string)
	kickConnector     *kick.Connector
	kickOAuth         *kick.OAuthManager
	onKickStatus      func(status string, detail string)
	onEventDispatched func(event *chat.Event, voiceID string)
	onTTSModeState    func(mode string, speakingActive bool, userIdle bool)

	idleProvider    func() time.Duration
	userIdle        bool
	lastOutputMode  string
	lastSpeakActive bool
	idleStopCh      chan struct{}
	idleWg          sync.WaitGroup

	streamMu        sync.Mutex
	livePlatforms   map[string]string
	activeSessionID int64
	configStore     config.Store

	heldMu        sync.Mutex
	heldMessages  []twitch.AutoModHoldEvent
	heldListeners map[uint64]func()
	heldSubSeq    atomic.Uint64
}

// NewSession creates an application session initialized with configuration, secret store, and config store.
func NewSession(cfg *config.Config, store secrets.Store, configStore config.Store) *Session {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if store == nil {
		store = secrets.NewMemoryStore()
	}
	if configStore == nil {
		configStore = config.NewMemoryStore(cfg)
	}

	matcher, _ := bots.NewMatcher()
	sanitizer := chat.NewSanitizer(chat.DefaultMaxRunes)
	router := chat.NewRouter()

	cfgClone := cfg.Clone()
	initMode := config.NormalizeTTSOutputMode(cfgClone.TTS.OutputMode)
	s := &Session{
		cfg:             cfgClone,
		secrets:         store,
		configStore:     configStore,
		router:          router,
		sanitizer:       sanitizer,
		bots:            matcher,
		twitchOAuth:     twitch.NewOAuthManager("", store),
		youtubeOAuth:    youtube.NewOAuthManager("", "", store),
		kickOAuth:       kick.NewOAuthManager("", "", store),
		livePlatforms:   make(map[string]string),
		idleProvider:    SystemIdleDuration,
		lastOutputMode:  initMode,
		lastSpeakActive: cfgClone.TTS.Enabled && initMode == config.TTSModeAlwaysOn,
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
	s.twitchConnector.SetOAuthManager(s.twitchOAuth)
	s.twitchConnector.SetOnAutoModHold(s.HandleAutoModHold)
	s.twitchConnector.SetOnStreamStateChange(func(live bool) {
		ch := ""
		if s.twitchConnector != nil {
			ch = s.twitchConnector.Channel()
		}
		s.UpdatePlatformStreamState("twitch", ch, live)
	})

	s.youtubeConnector = youtube.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(modEvent *chat.ModerationEvent) {
			s.DispatchModeration(modEvent)
		},
		func(status youtube.ConnectionStatus, detail string) {
			target := detail
			if s.youtubeConnector != nil && s.youtubeConnector.Target() != "" {
				target = s.youtubeConnector.Target()
			}
			s.UpdatePlatformStreamState("youtube", target, status == youtube.StatusConnected)

			s.mu.RLock()
			fn := s.onYouTubeStatus
			s.mu.RUnlock()
			if fn != nil {
				fn(string(status), detail)
			}
		},
	)
	s.youtubeConnector.SetOAuthManager(s.youtubeOAuth)

	s.tiktokConnector = tiktok.NewConnector(
		func(event *chat.Event) {
			_ = s.DispatchEvent(event)
		},
		func(status tiktok.ConnectionStatus, detail string) {
			user := detail
			if s.tiktokConnector != nil && s.tiktokConnector.Username() != "" {
				user = s.tiktokConnector.Username()
			}
			s.UpdatePlatformStreamState("tiktok", user, status == tiktok.StatusConnected)

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
	s.kickConnector.SetOAuthManager(s.kickOAuth)
	s.kickConnector.SetOnStreamStateChange(func(live bool) {
		ch := ""
		if s.kickConnector != nil {
			ch = s.kickConnector.Channel()
		}
		s.UpdatePlatformStreamState("kick", ch, live)
	})

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
	if s.running {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}

	s.running = true
	q := s.queue
	cfg := s.cfg.Clone()
	tw := s.twitchConnector
	yt := s.youtubeConnector
	tt := s.tiktokConnector
	kk := s.kickConnector
	s.mu.Unlock()

	if q != nil {
		q.Resume()
	}
	s.startIdleMonitor()
	s.EvaluateIdleState()
	syncConnectors(cfg, tw, yt, tt, kk)
	return nil
}

// Stop halts all active subsystems and transitions session to stopped state.
func (s *Session) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return ErrNotRunning
	}

	s.running = false
	q := s.queue
	tw := s.twitchConnector
	yt := s.youtubeConnector
	tt := s.tiktokConnector
	kk := s.kickConnector
	s.mu.Unlock()

	s.stopIdleMonitor()

	if q != nil {
		q.Pause()
	}
	if tw != nil {
		_ = tw.Stop()
	}
	if yt != nil {
		_ = yt.Stop()
	}
	if tt != nil {
		_ = tt.Stop()
	}
	if kk != nil {
		_ = kk.Stop()
	}

	s.endActiveStreamSession()
	return nil
}

// Close permanently shuts down the session, queue dispatcher, and platform connectors.
func (s *Session) Close() error {
	s.mu.Lock()
	s.running = false
	s.onTwitchStatus = nil
	s.onYouTubeStatus = nil
	s.onTikTokStatus = nil
	s.onKickStatus = nil
	s.onTTSModeState = nil
	tw := s.twitchConnector
	yt := s.youtubeConnector
	tt := s.tiktokConnector
	kk := s.kickConnector
	q := s.queue
	s.mu.Unlock()

	s.stopIdleMonitor()

	if tw != nil {
		_ = tw.Stop()
	}
	if yt != nil {
		_ = yt.Stop()
	}
	if tt != nil {
		_ = tt.Stop()
	}
	if kk != nil {
		_ = kk.Stop()
	}
	if q != nil {
		q.Close()
	}

	s.endActiveStreamSession()

	s.mu.Lock()
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
	cfgSnap := s.cfg.Clone()
	elog := s.eventLog
	running := s.running
	tw := s.twitchConnector
	yt := s.youtubeConnector
	tt := s.tiktokConnector
	kk := s.kickConnector
	s.mu.Unlock()

	if running {
		syncConnectors(cfgSnap, tw, yt, tt, kk)
	}

	if elog != nil {
		elog.SetEnabled(newCfg.Logging.Enabled)
		elog.SetRetentionPolicy(eventlog.RetentionPolicy{
			ChatRetentionDays:  newCfg.Logging.ChatRetentionDays,
			EventRetentionDays: newCfg.Logging.EventRetentionDays,
		})
	}
	s.EvaluateIdleState()
	return nil
}

// ConfigStore returns the configuration store attached to this session.
func (s *Session) ConfigStore() config.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configStore
}

// UpdateConfig safely mutates the session configuration, reloads subsystems, and persists via ConfigStore.
func (s *Session) UpdateConfig(fn func(c *config.Config)) error {
	if fn == nil {
		return errors.New("mutator function cannot be nil")
	}
	cfg := s.Config()
	fn(cfg)
	if err := s.ReloadConfig(cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	s.mu.RLock()
	cStore := s.configStore
	s.mu.RUnlock()
	if cStore != nil {
		if err := cStore.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
	}
	return nil
}

func syncConnectors(cfg *config.Config, tw *twitch.Connector, ytConn *youtube.Connector, ttConn *tiktok.Connector, kkConn *kick.Connector) {
	if cfg == nil {
		return
	}
	if tw != nil {
		p, ok := cfg.Platforms["twitch"]
		if ok && p.Enabled && p.AccountName != "" {
			_ = tw.Start(p.AccountName)
		} else {
			_ = tw.Stop()
		}
	}

	if ytConn != nil {
		yt, ok := cfg.Platforms["youtube"]
		if ok && yt.Enabled && yt.AccountName != "" {
			_ = ytConn.Start(yt.AccountName)
		} else {
			_ = ytConn.Stop()
		}
	}

	if ttConn != nil {
		tt, ok := cfg.Platforms["tiktok"]
		if ok && tt.Enabled && tt.AccountName != "" {
			_ = ttConn.Start(tt.AccountName)
		} else {
			_ = ttConn.Stop()
		}
	}

	if kkConn != nil {
		kc, ok := cfg.Platforms["kick"]
		if ok && kc.Enabled && kc.AccountName != "" {
			_ = kkConn.Start(kc.AccountName)
		} else {
			_ = kkConn.Stop()
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

// YouTubeOAuth returns the YouTube OAuth manager.
func (s *Session) YouTubeOAuth() *youtube.OAuthManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.youtubeOAuth
}

// KickOAuth returns the Kick OAuth manager.
func (s *Session) KickOAuth() *kick.OAuthManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kickOAuth
}

// SetOnTwitchStatus registers a callback for Twitch connection status changes.
func (s *Session) SetOnTwitchStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	s.onTwitchStatus = fn
	tw := s.twitchConnector
	s.mu.Unlock()
	if tw != nil && fn != nil {
		curStatus := tw.Status()
		curChannel := tw.Channel()
		fn(string(curStatus), curChannel)
	}
}

// SetOnYouTubeStatus registers a callback for YouTube connection status changes.
func (s *Session) SetOnYouTubeStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	s.onYouTubeStatus = fn
	yt := s.youtubeConnector
	s.mu.Unlock()
	if yt != nil && fn != nil {
		curStatus := yt.Status()
		curTarget := yt.Target()
		fn(string(curStatus), curTarget)
	}
}

// SetOnTikTokStatus registers a callback for TikTok connection status changes.
func (s *Session) SetOnTikTokStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	s.onTikTokStatus = fn
	tt := s.tiktokConnector
	s.mu.Unlock()
	if tt != nil && fn != nil {
		curStatus := tt.Status()
		curUser := tt.Username()
		fn(string(curStatus), curUser)
	}
}

// SetOnKickStatus registers a callback for Kick connection status changes.
func (s *Session) SetOnKickStatus(fn func(status string, detail string)) {
	s.mu.Lock()
	s.onKickStatus = fn
	kk := s.kickConnector
	s.mu.Unlock()
	if kk != nil && fn != nil {
		curStatus := kk.Status()
		curChan := kk.Channel()
		fn(string(curStatus), curChan)
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

	if mgr != nil {
		s.streamMu.Lock()
		if len(s.livePlatforms) > 0 && s.activeSessionID == 0 {
			now := time.Now().UTC()
			for p, ch := range s.livePlatforms {
				if s.activeSessionID == 0 {
					if sess, err := mgr.StartSession(context.Background(), now, p, ch); err == nil && sess != nil && sess.ID > 0 {
						s.activeSessionID = sess.ID
					}
				} else {
					_ = mgr.AddSessionPlatform(context.Background(), s.activeSessionID, p, ch)
				}
			}
		}
		s.streamMu.Unlock()
	}
}

// EventLogger returns the active structured event log manager, if any.
func (s *Session) EventLogger() *eventlog.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventLog
}

// ActiveSessionID returns the currently active stream session ID, or 0 if no platform is currently live.
func (s *Session) ActiveSessionID() int64 {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	return s.activeSessionID
}

// UpdatePlatformStreamState transitions a platform's live stream state and opens or closes the unified StreamSession accordingly.
func (s *Session) UpdatePlatformStreamState(platform, channel string, live bool) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return
	}
	channel = strings.TrimSpace(channel)
	if channel == "" {
		cfg := s.Config()
		if pCfg, ok := cfg.Platforms[platform]; ok {
			channel = pCfg.AccountName
		}
	}

	elog := s.EventLogger()

	s.streamMu.Lock()
	defer s.streamMu.Unlock()

	if s.livePlatforms == nil {
		s.livePlatforms = make(map[string]string)
	}

	if live {
		prevChan, alreadyLive := s.livePlatforms[platform]
		s.livePlatforms[platform] = channel
		if alreadyLive && prevChan == channel && s.activeSessionID > 0 {
			return
		}
		if elog == nil {
			return
		}
		now := time.Now().UTC()
		if s.activeSessionID == 0 {
			resumeMinutes := 60
			cfg := s.Config()
			if cfg != nil && cfg.Logging.SessionResumeWindowMinutes > 0 {
				resumeMinutes = cfg.Logging.SessionResumeWindowMinutes
			}
			resumeWindow := time.Duration(resumeMinutes) * time.Minute

			var resumedID int64
			latestSess, lastActive, err := elog.LatestSessionLastActive(context.Background())
			if err == nil && latestSess != nil && !lastActive.IsZero() && time.Since(lastActive) <= resumeWindow {
				if resErr := elog.ResumeSession(context.Background(), latestSess.ID, platform, channel); resErr == nil {
					resumedID = latestSess.ID
					s.activeSessionID = resumedID
					log.Printf("[Session] Resumed stream session #%d (%s:%s, last message was %v ago)", latestSess.ID, platform, channel, time.Since(lastActive).Round(time.Second))
				}
			} else if latestSess != nil && latestSess.EndedAt.IsZero() {
				// Cleanly close dangling unclosed session from a previous crash older than resumeWindow
				_ = elog.EndSession(context.Background(), latestSess.ID, lastActive)
				log.Printf("[Session] Closed unclosed stream session #%d from previous crash", latestSess.ID)
			}

			if resumedID == 0 {
				sess, err := elog.StartSession(context.Background(), now, platform, channel)
				if err == nil && sess != nil && sess.ID > 0 {
					s.activeSessionID = sess.ID
					log.Printf("[Session] Started stream session #%d (%s:%s)", sess.ID, platform, channel)
				}
			}

			for otherPlat, otherChan := range s.livePlatforms {
				if otherPlat != platform && s.activeSessionID > 0 {
					_ = elog.AddSessionPlatform(context.Background(), s.activeSessionID, otherPlat, otherChan)
				}
			}
		} else {
			_ = elog.AddSessionPlatform(context.Background(), s.activeSessionID, platform, channel)
		}
		return
	}

	if _, exists := s.livePlatforms[platform]; !exists {
		return
	}
	delete(s.livePlatforms, platform)
	if len(s.livePlatforms) == 0 && s.activeSessionID > 0 {
		sid := s.activeSessionID
		s.activeSessionID = 0
		if elog != nil {
			_ = elog.EndSession(context.Background(), sid, time.Now().UTC())
			log.Printf("[Session] Ended stream session #%d (all platforms offline)", sid)
		}
	}
}

func (s *Session) endActiveStreamSession() {
	elog := s.EventLogger()
	s.streamMu.Lock()
	sid := s.activeSessionID
	s.activeSessionID = 0
	clear(s.livePlatforms)
	s.streamMu.Unlock()

	if sid > 0 && elog != nil {
		_ = elog.EndSession(context.Background(), sid, time.Now().UTC())
		log.Printf("[Session] Ended stream session #%d on shutdown", sid)
	}
}

// DetectLiveStatus queries enabled streaming platforms concurrently to determine if any broadcast is currently live.
// If any platform is detected as live, its stream state is updated and the corresponding session is started or resumed.
func (s *Session) DetectLiveStatus(ctx context.Context) {
	cfg := s.Config()
	if cfg == nil || cfg.Platforms == nil {
		return
	}

	s.mu.RLock()
	tw := s.twitchConnector
	kk := s.kickConnector
	yt := s.youtubeConnector
	s.mu.RUnlock()

	var wg sync.WaitGroup

	if tw != nil {
		if p, ok := cfg.Platforms["twitch"]; ok && p.Enabled && strings.TrimSpace(p.AccountName) != "" {
			wg.Add(1)
			go func(ch string) {
				defer wg.Done()
				if live, err := tw.CheckLiveChannel(ctx, ch); err == nil && live {
					s.UpdatePlatformStreamState("twitch", ch, true)
				}
			}(p.AccountName)
		}
	}

	if kk != nil {
		if p, ok := cfg.Platforms["kick"]; ok && p.Enabled && strings.TrimSpace(p.AccountName) != "" {
			wg.Add(1)
			go func(ch string) {
				defer wg.Done()
				if live, err := kk.CheckLiveChannel(ctx, ch); err == nil && live {
					s.UpdatePlatformStreamState("kick", ch, true)
				}
			}(p.AccountName)
		}
	}

	if yt != nil {
		if p, ok := cfg.Platforms["youtube"]; ok && p.Enabled && strings.TrimSpace(p.AccountName) != "" {
			wg.Add(1)
			go func(ch string) {
				defer wg.Done()
				if live, err := yt.CheckLiveTarget(ctx, ch); err == nil && live {
					s.UpdatePlatformStreamState("youtube", ch, true)
				}
			}(p.AccountName)
		}
	}

	wg.Wait()
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

// SetIdleProvider overrides the keyboard/mouse inactivity duration provider (useful for unit tests).
func (s *Session) SetIdleProvider(fn func() time.Duration) {
	if fn == nil {
		fn = SystemIdleDuration
	}
	s.mu.Lock()
	s.idleProvider = fn
	s.mu.Unlock()
	s.EvaluateIdleState()
}

// CurrentIdleDuration returns the elapsed duration since the last keyboard or mouse activity.
func (s *Session) CurrentIdleDuration() time.Duration {
	s.mu.RLock()
	fn := s.idleProvider
	s.mu.RUnlock()
	if fn == nil {
		return 0
	}
	return fn()
}

// IsUserIdle reports whether keyboard/mouse inactivity has reached the configured IdleTimeoutSeconds.
func (s *Session) IsUserIdle() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userIdle
}

// TTSOutputMode returns the normalized TTS operational output mode ("always_on", "auto_idle", or "always_off").
func (s *Session) TTSOutputMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return config.TTSModeAlwaysOn
	}
	return config.NormalizeTTSOutputMode(s.cfg.TTS.OutputMode)
}

// SetTTSOutputMode updates and persists the TTS operational output mode.
func (s *Session) SetTTSOutputMode(mode string) error {
	norm := config.NormalizeTTSOutputMode(mode)
	return s.UpdateConfig(func(c *config.Config) {
		c.TTS.OutputMode = norm
	})
}

// CycleTTSOutputMode cycles the TTS output mode (Always On -> Auto-AFK -> Always Off) and returns the new mode.
func (s *Session) CycleTTSOutputMode() string {
	next := config.NextTTSOutputMode(s.TTSOutputMode())
	_ = s.SetTTSOutputMode(next)
	return next
}

// IsTTSSpeakingActive reports whether TTS speech synthesis/playback is currently active
// based on master TTS.Enabled, OutputMode, and (when in Auto-AFK mode) user inactivity.
func (s *Session) IsTTSSpeakingActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil || !s.cfg.TTS.Enabled {
		return false
	}
	switch config.NormalizeTTSOutputMode(s.cfg.TTS.OutputMode) {
	case config.TTSModeAlwaysOff:
		return false
	case config.TTSModeAutoIdle:
		return s.userIdle
	default:
		return true
	}
}

// SetOnTTSModeStateChanged registers a callback invoked whenever TTS output mode, effective speaking state,
// or user idle status changes.
func (s *Session) SetOnTTSModeStateChanged(fn func(mode string, speakingActive bool, userIdle bool)) {
	s.mu.Lock()
	s.onTTSModeState = fn
	mode := s.lastOutputMode
	if s.cfg != nil {
		mode = config.NormalizeTTSOutputMode(s.cfg.TTS.OutputMode)
	}
	active := s.lastSpeakActive
	idle := s.userIdle
	s.mu.Unlock()

	if fn != nil {
		fn(mode, active, idle)
	}
}

// EvaluateIdleState checks the current keyboard/mouse idle duration, updates userIdle and speaking state,
// flushes pending queue items when transitioning from speaking to inactive, and notifies listeners.
func (s *Session) EvaluateIdleState() {
	s.mu.RLock()
	provider := s.idleProvider
	cfg := s.cfg
	q := s.queue
	s.mu.RUnlock()

	if cfg == nil {
		return
	}

	var idleDur time.Duration
	if provider != nil {
		idleDur = provider()
	}

	timeoutSec := cfg.TTS.IdleTimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = config.DefaultIdleTimeoutSeconds
	}
	nowIdle := idleDur >= time.Duration(timeoutSec)*time.Second
	mode := config.NormalizeTTSOutputMode(cfg.TTS.OutputMode)

	nowSpeakActive := false
	if cfg.TTS.Enabled {
		switch mode {
		case config.TTSModeAlwaysOn:
			nowSpeakActive = true
		case config.TTSModeAutoIdle:
			nowSpeakActive = nowIdle
		case config.TTSModeAlwaysOff:
			nowSpeakActive = false
		}
	}

	s.mu.Lock()
	changed := (mode != s.lastOutputMode) || (nowIdle != s.userIdle) || (nowSpeakActive != s.lastSpeakActive)
	deactivated := s.lastSpeakActive && !nowSpeakActive
	s.lastOutputMode = mode
	s.userIdle = nowIdle
	s.lastSpeakActive = nowSpeakActive
	cb := s.onTTSModeState
	s.mu.Unlock()

	if deactivated && q != nil {
		flushed := q.FlushPendingForActivity()
		if flushed > 0 {
			log.Printf("[Session] Flushed %d pending speech items on transition to TTS inactive (mode=%s, idle=%v)", flushed, mode, nowIdle)
		}
	}

	if changed && cb != nil {
		cb(mode, nowSpeakActive, nowIdle)
	}
}

func (s *Session) startIdleMonitor() {
	s.mu.Lock()
	if s.idleStopCh != nil {
		s.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	s.idleStopCh = stopCh
	s.idleWg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.idleWg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				s.EvaluateIdleState()
			}
		}
	}()
}

func (s *Session) stopIdleMonitor() {
	s.mu.Lock()
	stopCh := s.idleStopCh
	s.idleStopCh = nil
	s.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
		s.idleWg.Wait()
	}
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
	itemName := strings.TrimSpace(event.ItemName)
	if itemName == "" && event.Kind == chat.EventKindGift {
		itemName = strings.TrimSpace(event.RewardTitle)
	}
	return &eventlog.Entry{
		ID:            event.ID,
		SessionID:     s.ActiveSessionID(),
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
		UnitAmount:    event.UnitAmount,
		Currency:      event.Currency,
		ItemName:      itemName,
		SubTier:       event.SubTier,
		Recipient:     event.Recipient,
		RewardID:      event.RewardID,
		RewardTitle:   event.RewardTitle,
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

// RefreshTwitchOAuth notifies the Twitch connector to start or stop EventSub based on OAuth token status.
func (s *Session) RefreshTwitchOAuth() {
	if s.twitchConnector != nil {
		s.twitchConnector.RefreshOAuth()
	}
}

func (s *Session) syncAndEnrichFollowerRole(event *chat.Event) {
	if event == nil {
		return
	}
	plat := strings.ToLower(strings.TrimSpace(event.Platform))
	ch := strings.TrimSpace(event.Channel)
	if plat == "" || ch == "" || (event.UserID == "" && event.UserName == "") {
		return
	}

	// 1. Live follow event -> immediately mark user as following in the 30-min TTL cache and tag as follower
	if event.Kind == chat.EventKindFollow {
		switch plat {
		case "twitch":
			if s.twitchConnector != nil {
				s.twitchConnector.MarkUserFollowing(event.UserID, event.UserName)
			}
		case "kick":
			if s.kickConnector != nil {
				s.kickConnector.MarkUserFollowing(ch, event.UserName)
			}
		}
		if !slices.ContainsFunc(event.Roles, func(r string) bool { return strings.EqualFold(r, "follower") }) {
			event.Roles = append(event.Roles, "follower")
		}
		return
	}

	if plat != "twitch" && plat != "kick" {
		return
	}
	if slices.ContainsFunc(event.Roles, func(r string) bool { return strings.EqualFold(r, "follower") }) {
		return
	}
	if s.bots != nil && s.bots.ShouldIgnore(event, s.Config()) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	isFol := false
	switch plat {
	case "twitch":
		if s.twitchConnector != nil && s.twitchConnector.CanCheckFollowers() {
			if ok, err := s.twitchConnector.CheckUserFollowing(ctx, event.UserID, event.UserName); err == nil && ok {
				isFol = true
			}
		}
	case "kick":
		if s.kickConnector != nil && s.kickConnector.CanCheckFollowers() {
			if ok, _, err := s.kickConnector.CheckUserFollowing(ctx, ch, event.UserName); err == nil && ok {
				isFol = true
			}
		}
	}

	if isFol {
		event.Roles = append(event.Roles, "follower")
	}
}

func shiftEmoteSpans(spans []chat.EmoteSpan, deltaRunes int) []chat.EmoteSpan {
	if len(spans) == 0 {
		return nil
	}
	out := make([]chat.EmoteSpan, len(spans))
	for i, sp := range spans {
		sp.Start += deltaRunes
		sp.End += deltaRunes
		out[i] = sp
	}
	return out
}

func formatLogCurrencyAmount(amount float64, currency string) string {
	cur := strings.TrimSpace(currency)
	var amtStr string
	if amount == float64(int64(amount)) {
		amtStr = fmt.Sprintf("%.0f", amount)
	} else {
		amtStr = fmt.Sprintf("%.2f", amount)
	}
	if cur != "" {
		return amtStr + " " + cur
	}
	return amtStr
}

func formatEventLogRawTextAndEmotes(event *chat.Event) (string, []chat.EmoteSpan) {
	if event == nil {
		return "", nil
	}
	var rawEmotes []chat.EmoteSpan
	if len(event.EmoteSpans) > 0 {
		rawEmotes = make([]chat.EmoteSpan, len(event.EmoteSpans))
		copy(rawEmotes, event.EmoteSpans)
	}

	msg := strings.TrimSpace(event.Text)
	switch event.Kind {
	case chat.EventKindFollow:
		if msg != "" {
			return msg, rawEmotes
		}
		return "Followed the channel", nil
	case chat.EventKindSub:
		if msg != "" {
			prefix := "Subscribed: "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return "Subscribed", nil
	case chat.EventKindResub:
		months := event.Months
		if months <= 0 {
			months = 1
		}
		if msg != "" {
			prefix := fmt.Sprintf("Resubscribed (%d months): ", months)
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return fmt.Sprintf("Resubscribed (%d months)", months), nil
	case chat.EventKindGiftSub:
		if msg != "" {
			return msg, rawEmotes
		}
		count := event.Count
		if count <= 0 {
			count = 1
		}
		if recipient := strings.TrimSpace(event.Recipient); recipient != "" {
			if count == 1 {
				return "Gifted a subscription to " + recipient, nil
			}
			return fmt.Sprintf("Gifted %d subscriptions to %s", count, recipient), nil
		}
		return fmt.Sprintf("Gifted %d subscriptions", count), nil
	case chat.EventKindGiftRedemption:
		if msg != "" {
			return msg, rawEmotes
		}
		if gifter := strings.TrimSpace(event.Recipient); gifter != "" {
			return "Received a gift membership from " + gifter, nil
		}
		return "Received a gift membership", nil
	case chat.EventKindSubUpgrade:
		base := strings.TrimSpace(event.ItemName)
		if base == "" {
			if recipient := strings.TrimSpace(event.Recipient); recipient != "" {
				base = "Paid forward a gift subscription to " + recipient
			} else if tier := strings.TrimSpace(event.SubTier); tier != "" {
				base = fmt.Sprintf("Upgraded to a %s subscription", tier)
			} else {
				base = "Upgraded subscription"
			}
		}
		if msg != "" && !strings.EqualFold(msg, base) {
			prefix := base + ": "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return base, nil
	case chat.EventKindWatchStreak:
		streak := event.Count
		if streak <= 0 {
			streak = 1
		}
		base := fmt.Sprintf("Shared a %d-stream watch streak", streak)
		if event.Amount > 0 {
			base = fmt.Sprintf("Shared a %d-stream watch streak (+%.0f pts)", streak, event.Amount)
		}
		if msg != "" {
			prefix := base + ": "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return base, nil
	case chat.EventKindCheer:
		if msg != "" {
			return msg, rawEmotes
		}
		return fmt.Sprintf("Cheered %.0f bits", event.Amount), nil
	case chat.EventKindBitsBadgeTier:
		bits := event.Amount
		if bits <= 0 && event.Count > 0 {
			bits = float64(event.Count)
		}
		base := fmt.Sprintf("Unlocked the %.0f bits badge", bits)
		if msg != "" {
			prefix := base + ": "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return base, nil
	case chat.EventKindRaid:
		if msg != "" {
			return msg, rawEmotes
		}
		count := event.Count
		if count <= 0 {
			count = 1
		}
		return fmt.Sprintf("Raided with %d viewers", count), nil
	case chat.EventKindRedemption:
		reward := strings.TrimSpace(event.RewardTitle)
		if reward == "" {
			reward = strings.TrimSpace(event.RewardID)
		}
		if reward == "" {
			reward = "Channel Point Reward"
		}
		if msg != "" {
			prefix := fmt.Sprintf("[%s] ", reward)
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return "Redeemed " + reward, nil
	case chat.EventKindSuperChat:
		amtStr := formatLogCurrencyAmount(event.Amount, event.Currency)
		if msg != "" {
			prefix := fmt.Sprintf("Super Chat (%s): ", amtStr)
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return fmt.Sprintf("Super Chat (%s)", amtStr), nil
	case chat.EventKindSuperSticker:
		amtStr := formatLogCurrencyAmount(event.Amount, event.Currency)
		base := fmt.Sprintf("Super Sticker (%s)", amtStr)
		if len(rawEmotes) > 0 && strings.TrimSpace(rawEmotes[0].URL) != "" {
			stickerName := strings.TrimSpace(event.ItemName)
			if stickerName == "" {
				stickerName = strings.TrimSpace(rawEmotes[0].Name)
			}
			if stickerName == "" {
				stickerName = "Super Sticker"
			}
			token := ":" + stickerName + ":"
			start := utf8.RuneCountInString(base) + 1
			sp := rawEmotes[0]
			sp.Start = start
			sp.End = start + utf8.RuneCountInString(token)
			sp.Name = stickerName
			return base + " " + token, []chat.EmoteSpan{sp}
		}
		return base, nil
	case chat.EventKindGift:
		giftName := strings.TrimSpace(event.ItemName)
		if giftName == "" {
			giftName = strings.TrimSpace(event.RewardTitle)
		}
		if giftName == "" {
			giftName = msg
		}
		if giftName == "" {
			giftName = "Gift"
		}
		if strings.EqualFold(msg, giftName) {
			msg = ""
		}
		if strings.EqualFold(event.Platform, "youtube") || strings.EqualFold(event.Currency, "Jewels") || strings.EqualFold(event.Currency, "Jewel") {
			base := "Sent " + giftName
			if event.Amount > 0 {
				base = fmt.Sprintf("Sent %s (%.0f Jewels)", giftName, event.Amount)
			}
			if len(rawEmotes) > 0 && strings.TrimSpace(rawEmotes[0].URL) != "" {
				token := ":" + giftName + ":"
				start := utf8.RuneCountInString(base) + 1
				sp := rawEmotes[0]
				sp.Start = start
				sp.End = start + utf8.RuneCountInString(token)
				sp.Name = giftName
				return base + " " + token, []chat.EmoteSpan{sp}
			}
			return base, nil
		}
		count := event.Count
		if count <= 0 {
			count = int(event.Amount)
		}
		if count <= 0 {
			count = 1
		}
		if msg != "" {
			prefix := fmt.Sprintf("Sent %d %s: ", count, giftName)
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		base := fmt.Sprintf("Sent %d %s", count, giftName)
		if len(rawEmotes) > 0 && strings.TrimSpace(rawEmotes[0].URL) != "" {
			token := ":" + giftName + ":"
			start := utf8.RuneCountInString(base) + 1
			sp := rawEmotes[0]
			sp.Start = start
			sp.End = start + utf8.RuneCountInString(token)
			sp.Name = giftName
			return base + " " + token, []chat.EmoteSpan{sp}
		}
		return base, nil
	case chat.EventKindCharityDonation:
		amtStr := formatLogCurrencyAmount(event.Amount, event.Currency)
		charity := strings.TrimSpace(event.RewardTitle)
		if charity == "" {
			charity = strings.TrimSpace(event.ItemName)
		}
		base := "Donated " + amtStr
		if charity != "" {
			base = "Donated " + amtStr + " to " + charity
		}
		if msg != "" {
			prefix := base + ": "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return base, nil
	case chat.EventKindAnnouncement:
		if msg != "" {
			prefix := "[Announcement] "
			return prefix + msg, shiftEmoteSpans(rawEmotes, utf8.RuneCountInString(prefix))
		}
		return "Announcement", nil
	case chat.EventKindShare:
		if msg != "" {
			return msg, rawEmotes
		}
		return "Shared the stream", nil
	default:
		return event.Text, rawEmotes
	}
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
	s.syncAndEnrichFollowerRole(event)
	rawText, rawEmotes := formatEventLogRawTextAndEmotes(event)

	log.Printf("[Session] DispatchEvent: user=%q platform=%s channel=%q kind=%s text=%q roles=%v", event.UserName, event.Platform, event.Channel, event.Kind, event.Text, event.Roles)

	// 1. Bot Matcher (known bots, platform bot signals, viewer blocklist/allowlist)
	if s.bots != nil && s.bots.ShouldIgnore(event, cfg) {
		log.Printf("[Session] Event dropped by bot matcher: user=%q", event.UserName)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedBot, "Ignored by bot matcher or viewer blocklist"))
		return nil
	}

	// 2. Platform Notifications & Read Rules (PRD D18)
	if pCfg, ok := cfg.Platforms[event.Platform]; ok {
		if event.Kind != chat.EventKindMessage && pCfg.Notifications != nil {
			if notif, hasNotif := pCfg.Notifications[string(event.Kind)]; hasNotif {
				if !notif.Enabled {
					log.Printf("[Session] Notification event dropped (disabled): kind=%s user=%q", event.Kind, event.UserName)
					s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedDisabled, fmt.Sprintf("Notification kind %q disabled", event.Kind)))
					return nil
				}
				if event.Kind == chat.EventKindRedemption {
					if targetReward := strings.TrimSpace(pCfg.CustomRewardID); targetReward != "" {
						if !strings.EqualFold(strings.TrimSpace(event.RewardID), targetReward) &&
							!strings.EqualFold(strings.TrimSpace(event.RewardTitle), targetReward) {
							log.Printf("[Session] Redemption filtered by CustomRewardID (%q != %q / %q): user=%q", targetReward, event.RewardID, event.RewardTitle, event.UserName)
							s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedDisabled, fmt.Sprintf("Reward %q does not match designated reward filter %q", event.RewardTitle, targetReward)))
							return nil
						}
					}
				}
				if notif.Threshold > 0 {
					val := event.Amount
					if event.Kind == chat.EventKindRaid || event.Kind == chat.EventKindGiftSub || event.Kind == chat.EventKindWatchStreak || (event.Kind == chat.EventKindGift && val == 0) || (event.Kind == chat.EventKindBitsBadgeTier && val == 0) {
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

		if event.Kind == chat.EventKindMessage {
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
	}

	// 3. Text & Emote Sanitizer on raw message text
	// Emote offsets are relative to event.Text before chatter name prefixing.
	_, speakEmotes, deduplicateEmotes, maxDuplicateWords, _, _, _ := cfg.EffectiveChatRules(event.Platform)
	blockedWords := chat.EffectiveBlockedWords(cfg.Safety.BlockDefaultBadWords, cfg.Safety.BlockedWords)
	if event.Text != "" {
		spansForSanitize := event.EmoteSpans
		if event.Kind == chat.EventKindGift && (strings.EqualFold(strings.TrimSpace(event.Text), strings.TrimSpace(event.ItemName)) || strings.EqualFold(strings.TrimSpace(event.Text), strings.TrimSpace(event.RewardTitle))) {
			// Gift icon EmoteSpans are for inline visual rendering in logs; never strip the gift name when SpeakEmotes is false.
			spansForSanitize = nil
		}
		event.Text = s.sanitizer.SanitizeWithOptions(event.Text, chat.SanitizeOptions{
			EmoteSpans:        spansForSanitize,
			SpeakEmotes:       speakEmotes,
			DeduplicateEmotes: deduplicateEmotes,
			MaxDuplicateWords: maxDuplicateWords,
			BlockedWords:      blockedWords,
		})
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
	sanitizedText := s.sanitizer.SanitizeWithOptions(resolved.Text, chat.SanitizeOptions{
		SpeakEmotes:       true,
		DeduplicateEmotes: false,
		MaxDuplicateWords: 0,
		BlockedWords:      blockedWords,
	})
	if sanitizedText == "" {
		log.Printf("[Session] Event sanitized to empty text: user=%q original=%q", event.UserName, resolved.Text)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, "", "", 0, false, eventlog.OutcomeDroppedSanitized, "Formatted speech text sanitized to empty"))
		return nil
	}

	// 6. Check if TTS is disabled or paused by TTS Output Mode / Auto-AFK
	if !cfg.TTS.Enabled {
		log.Printf("[Session] Event passed filters but TTS is disabled: user=%q", event.UserName)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, sanitizedText, resolved.VoiceID, resolved.Speed, true, eventlog.OutcomeTTSDisabled, "TTS disabled in settings"))

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

	s.EvaluateIdleState()
	if !s.IsTTSSpeakingActive() {
		mode := config.NormalizeTTSOutputMode(cfg.TTS.OutputMode)
		reason := "TTS output mode set to Always Off"
		if mode == config.TTSModeAutoIdle {
			reason = "TTS paused while user is active at computer (Auto-AFK mode)"
		}
		log.Printf("[Session] Event passed filters but TTS output is inactive (%s): user=%q", mode, event.UserName)
		s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, sanitizedText, resolved.VoiceID, resolved.Speed, true, eventlog.OutcomeTTSDisabled, reason))

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

	// 7. Priority tier assignment
	priority := 1
	if event.IsPaidOrEvent() {
		priority = 0
	}

	// 8. Record initial PassedFilters=true / Queued log entry before enqueueing so fast queue callbacks find the row
	s.recordEventLog(s.buildLogEntry(event, rawText, rawEmotes, sanitizedText, resolved.VoiceID, resolved.Speed, true, eventlog.OutcomeQueued, ""))

	// 9. Enqueue for speech synthesis
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
