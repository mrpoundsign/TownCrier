package bot

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/bot/obs"
	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// HostBridge connects the bot automation engine to TownCrier subsystems without import cycles.
type HostBridge struct {
	GetConfig        func() *config.Config
	IsStreamLive     func() bool
	SpeakTTS         func(text, voiceID string, speed float32, priority int, username string) error
	PlaySound        func(path string) error
	SendChat         func(ctx context.Context, platform, channel, text, replyToMsgID string, asAnnouncement bool, sendAs string) error
	Moderate         func(ctx context.Context, platform, action, userID, userName, messageID string, durationSec int, reason string) error
	OverlayBroadcast func(pluginUUID, eventName, jsonPayload string)
	RunPluginAction  func(ctx context.Context, pluginUUID, actionID string, args *pluginv1.ActionArgs) (bool, *pluginv1.ActionArgs, error)
	GetOBSPassword   func() string
	OnOBSStatus      func(st obs.ClientStatus)
	Logf             func(format string, args ...any)
}

type timerState struct {
	lastFired time.Time
	lastLines uint64
}

// Engine executes TownCrier's Streamer.bot-style triggers, queues, timers, Whisper voice triggers, and OBS v5 integration.
type Engine struct {
	store      *Store
	bridge     HostBridge
	queues     *queueManager
	cooldowns  *cooldownTracker
	obs        *obs.Client
	httpClient *http.Client

	mu          sync.Mutex
	closed      bool
	stopCh      chan struct{}
	chatLines   atomic.Uint64
	timerStates map[string]timerState
	lastOBSCfg  config.BotOBSConfig
}

// NewEngine constructs a Bot Automation Engine backed by store and bridge.
func NewEngine(store *Store, bridge HostBridge) *Engine {
	eng := &Engine{
		store:       store,
		bridge:      bridge,
		cooldowns:   newCooldownTracker(),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		stopCh:      make(chan struct{}),
		timerStates: make(map[string]timerState),
	}
	var qDefs []config.BotQueueDef
	if bridge.GetConfig != nil {
		if cfg := bridge.GetConfig(); cfg != nil {
			qDefs = cfg.Bot.Queues
		}
	}
	eng.queues = newQueueManager(qDefs)
	eng.obs = obs.NewClient(eng.onOBSEvent, func(st obs.ClientStatus) {
		if eng.bridge.OnOBSStatus != nil {
			eng.bridge.OnOBSStatus(st)
		}
	})
	return eng
}

// Store returns the engine's persistent/ephemeral variable and group store.
func (e *Engine) Store() *Store {
	return e.store
}

// OBSClient returns the embedded OBS WebSocket v5 client.
func (e *Engine) OBSClient() *obs.Client {
	return e.obs
}

// SetHTTPClient overrides the outbound HTTP client for sub-action testing.
func (e *Engine) SetHTTPClient(c *http.Client) {
	if c != nil {
		e.httpClient = c
	}
}

// Start launches the background timer scheduler and syncs initial OBS configuration.
func (e *Engine) Start() {
	e.SyncConfig()
	go e.timerLoop()
}

// SyncConfig updates queue workers and OBS auto-connect state from the latest Session config.
func (e *Engine) SyncConfig() {
	cfg := e.currentBotConfig()
	if e.queues != nil {
		e.queues.Sync(cfg.Queues)
	}
	e.syncOBS(cfg)
}

func (e *Engine) syncOBS(cfg config.BotAutomationConfig) {
	if e.obs == nil {
		return
	}
	host := strings.TrimSpace(cfg.OBS.Host)
	if host == "" {
		host = config.DefaultOBSHost
	}
	port := cfg.OBS.Port
	if port <= 0 {
		port = config.DefaultOBSPort
	}

	e.mu.Lock()
	shouldRun := cfg.Enabled && cfg.OBS.Enabled && cfg.OBS.AutoReconnect
	changed := e.lastOBSCfg.Host != host || e.lastOBSCfg.Port != port || e.lastOBSCfg.Enabled != cfg.OBS.Enabled || e.lastOBSCfg.AutoReconnect != cfg.OBS.AutoReconnect
	e.lastOBSCfg = config.BotOBSConfig{
		Enabled:       cfg.OBS.Enabled,
		AutoReconnect: cfg.OBS.AutoReconnect,
		Host:          host,
		Port:          port,
	}
	e.mu.Unlock()

	if !shouldRun {
		if changed && e.obs.Status().State != obs.StateDisconnected {
			e.obs.Disconnect()
		}
		return
	}
	if changed || e.obs.Status().State == obs.StateDisconnected {
		pw := ""
		if e.bridge.GetOBSPassword != nil {
			pw = e.bridge.GetOBSPassword()
		}
		e.obs.StartAutoReconnect(host, port, pw)
	}
}

// ClearQueues clears all queued and currently running bot jobs.
func (e *Engine) ClearQueues() {
	if e.queues != nil {
		e.queues.ClearQueues()
	}
}

// QueueStats returns live queue counts for the UI.
func (e *Engine) QueueStats() []QueueStat {
	if e.queues == nil {
		return nil
	}
	return e.queues.Stats()
}

// Close stops timers, queues, OBS connection, and the SQLite variable store.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.stopCh)
	e.mu.Unlock()

	if e.queues != nil {
		e.queues.Close()
	}
	if e.obs != nil {
		e.obs.Disconnect()
	}
	if e.store != nil {
		return e.store.Close()
	}
	return nil
}

func (e *Engine) currentBotConfig() config.BotAutomationConfig {
	if e.bridge.GetConfig != nil {
		if cfg := e.bridge.GetConfig(); cfg != nil {
			return cfg.Bot
		}
	}
	return config.DefaultBotAutomationConfig()
}

// OnEvent evaluates normalized stream events against enabled chat commands and platform triggers.
// Returns consumeTTS=true if a matching enabled command suppresses TTS speaking for this message.
func (e *Engine) OnEvent(ev *chat.Event) (consumeTTS bool) {
	if ev == nil {
		return false
	}
	cfg := e.currentBotConfig()
	if !cfg.Enabled {
		return false
	}

	if ev.Kind == chat.EventKindMessage {
		e.chatLines.Add(1)
	}

	now := time.Now()
	userKey := strings.ToLower(strings.TrimSpace(ev.Platform + ":" + ev.UserID + ":" + ev.UserName))

	// 1. Evaluate chat commands
	if ev.Kind == chat.EventKindMessage {
		for _, cmd := range cfg.Commands {
			if !cmd.Enabled || strings.TrimSpace(cmd.ActionID) == "" {
				continue
			}
			matched, rawInput, positional := matchCommandPattern(cmd, ev.Text)
			if !matched {
				continue
			}
			if !userAuthorizedForCommand(cmd, ev, e.store) {
				continue
			}
			if !e.cooldowns.allowAndStamp("cmd:"+cmd.ID+":"+cmd.Trigger, userKey, cmd.GlobalCooldownSec, cmd.UserCooldownSec, now) {
				continue
			}
			args := buildEventArgs(ev, rawInput, positional)
			args["command"] = cmd.Trigger
			e.dispatchActionByID(cfg, cmd.ActionID, args, ev.Platform, ev.UserID, ev.Channel, ev.ID, false)
			if cmd.ConsumeTTS {
				consumeTTS = true
			}
		}
	}

	// 2. Evaluate platform event triggers
	for _, tr := range cfg.Triggers {
		if !matchPlatformTrigger(tr, ev) || strings.TrimSpace(tr.ActionID) == "" {
			continue
		}
		if tr.CooldownSec > 0 && !e.cooldowns.allowAndStamp("trig:"+tr.ID, "", tr.CooldownSec, 0, now) {
			continue
		}
		args := buildEventArgs(ev, ev.Text, strings.Fields(ev.Text))
		args["triggerId"] = tr.ID
		e.dispatchActionByID(cfg, tr.ActionID, args, ev.Platform, ev.UserID, ev.Channel, ev.ID, false)
	}

	return consumeTTS
}

// OnVoiceTranscript evaluates finalized Whisper STT transcripts against enabled voice command triggers.
func (e *Engine) OnVoiceTranscript(transcript string) {
	if e.bridge.GetConfig != nil {
		if fullCfg := e.bridge.GetConfig(); fullCfg != nil && !fullCfg.EffectiveVoiceCommandsEnabled() {
			return
		}
	}
	cfg := e.currentBotConfig()
	if !cfg.Enabled || strings.TrimSpace(transcript) == "" {
		return
	}
	now := time.Now()
	for _, vc := range cfg.VoiceCommands {
		if !vc.Enabled || strings.TrimSpace(vc.ActionID) == "" {
			continue
		}
		ok, text := matchVoicePhrase(vc, transcript)
		if !ok {
			continue
		}
		if vc.CooldownSec > 0 && !e.cooldowns.allowAndStamp("voice:"+vc.ID+":"+vc.Phrase, "", vc.CooldownSec, 0, now) {
			continue
		}
		args := map[string]string{
			"transcript": text,
			"rawInput":   text,
			"voicePhrase": vc.Phrase,
		}
		e.dispatchActionByID(cfg, vc.ActionID, args, "", "", "", "", false)
	}
}

// OnPluginTrigger dispatches a custom plugin trigger emitted via Host.EmitTrigger.
func (e *Engine) OnPluginTrigger(triggerID string, args map[string]string) {
	cfg := e.currentBotConfig()
	if !cfg.Enabled || strings.TrimSpace(triggerID) == "" {
		return
	}
	now := time.Now()
	for _, tr := range cfg.Triggers {
		if !tr.Enabled || !strings.EqualFold(strings.TrimSpace(tr.Source), "plugin_trigger") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tr.EventKind), strings.TrimSpace(triggerID)) {
			continue
		}
		if tr.CooldownSec > 0 && !e.cooldowns.allowAndStamp("plugtrig:"+tr.ID, "", tr.CooldownSec, 0, now) {
			continue
		}
		merged := maps.Clone(args)
		if merged == nil {
			merged = make(map[string]string)
		}
		merged["triggerId"] = triggerID
		e.dispatchActionByID(cfg, tr.ActionID, merged, merged["platform"], merged["userId"], merged["channel"], "", false)
	}
}

func (e *Engine) onOBSEvent(ev obs.Event) {
	cfg := e.currentBotConfig()
	if !cfg.Enabled || strings.TrimSpace(ev.Type) == "" {
		return
	}
	now := time.Now()
	for _, tr := range cfg.Triggers {
		if !tr.Enabled || !strings.EqualFold(strings.TrimSpace(tr.Source), "obs_event") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tr.EventKind), strings.TrimSpace(ev.Type)) {
			continue
		}
		if tr.CooldownSec > 0 && !e.cooldowns.allowAndStamp("obstrig:"+tr.ID, "", tr.CooldownSec, 0, now) {
			continue
		}
		args := map[string]string{"obsEvent": ev.Type}
		for k, v := range ev.Data {
			args[k] = fmt.Sprint(v)
		}
		e.dispatchActionByID(cfg, tr.ActionID, args, "", "", "", "", false)
	}
}

// RunActionByID invokes an action by ID or name (used by UI "Test Run", plugins, and sub-actions).
func (e *Engine) RunActionByID(actionIDOrName string, args map[string]string, runImmediately bool) error {
	cfg := e.currentBotConfig()
	target := strings.TrimSpace(actionIDOrName)
	if target == "" {
		return errors.New("action ID or name cannot be empty")
	}
	for _, act := range cfg.Actions {
		if act.ID == target || strings.EqualFold(act.Name, target) {
			e.dispatchActionByID(cfg, act.ID, args, args["platform"], args["userId"], args["channel"], args["messageId"], runImmediately)
			return nil
		}
	}
	return fmt.Errorf("bot action %q not found", actionIDOrName)
}

func (e *Engine) dispatchActionByID(
	cfg config.BotAutomationConfig,
	actionID string,
	args map[string]string,
	platform, userID, channel, replyID string,
	runImmediately bool,
) {
	var found *config.BotActionDef
	for i := range cfg.Actions {
		if cfg.Actions[i].ID == actionID || strings.EqualFold(cfg.Actions[i].Name, actionID) {
			cp := cfg.Actions[i]
			found = &cp
			break
		}
	}
	if found == nil || !found.Enabled {
		return
	}

	st := &execState{
		args:     maps.Clone(args),
		platform: platform,
		userID:   userID,
		channel:  channel,
		replyID:  replyID,
	}
	if st.args == nil {
		st.args = make(map[string]string)
	}

	if runImmediately {
		e.executeAction(context.Background(), cfg, *found, st)
		return
	}
	if e.queues != nil {
		actionCopy := *found
		cfgCopy := cfg
		e.queues.Enqueue(actionCopy.QueueID, func(ctx context.Context) {
			e.executeAction(ctx, cfgCopy, actionCopy, st)
		})
	}
}

func (e *Engine) timerLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case now := <-ticker.C:
			e.evaluateTimers(now)
		}
	}
}

func (e *Engine) evaluateTimers(now time.Time) {
	cfg := e.currentBotConfig()
	if !cfg.Enabled || len(cfg.Timers) == 0 {
		return
	}
	live := false
	if e.bridge.IsStreamLive != nil {
		live = e.bridge.IsStreamLive()
	}
	lines := e.chatLines.Load()

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, tm := range cfg.Timers {
		if !tm.Enabled || tm.IntervalSeconds <= 0 || strings.TrimSpace(tm.ActionID) == "" {
			continue
		}
		st, ok := e.timerStates[tm.ID]
		if !ok {
			e.timerStates[tm.ID] = timerState{lastFired: now, lastLines: lines}
			continue
		}
		if tm.LiveOnly && !live {
			continue
		}
		if now.Sub(st.lastFired) < time.Duration(tm.IntervalSeconds)*time.Second {
			continue
		}
		if tm.MinChatLines > 0 && int(lines-st.lastLines) < tm.MinChatLines {
			continue
		}
		e.timerStates[tm.ID] = timerState{lastFired: now, lastLines: lines}
		args := map[string]string{
			"timerName": tm.Name,
			"chatLines": strconv.FormatUint(lines, 10),
		}
		e.dispatchActionByID(cfg, tm.ActionID, args, "", "", "", "", false)
	}
}
