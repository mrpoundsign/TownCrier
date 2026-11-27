package pluginhost

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const eventQueueSize = 64

type pluginBackend interface {
	Dispatch(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error)
	SetTimer(timerID, ctxStr string, intervalMs uint32) *pluginv1.APIError
	Close() error
}

type pluginEntry struct {
	mu           sync.RWMutex
	uuid         string
	name         string
	version      string
	author       string
	description  string
	runtime      pluginv1.RuntimeKind
	path         string
	binaryPath   string
	binarySHA256 string
	manifest      *pluginv1.Manifest
	actionVisuals map[string]*pluginv1.ActionVisualState
	state         RunState
	errMsg       string
	apiErr       *pluginv1.APIError
	logs         *logBuffer
	backend      pluginBackend

	eventMu sync.Mutex
	eventCh chan *pluginv1.OnEventRequest
	stopEv  chan struct{}
}

func (e *pluginEntry) setError(apiErr *pluginv1.APIError) {
	e.mu.Lock()
	e.state = StatusError
	e.apiErr = apiErr
	if apiErr != nil {
		e.errMsg = apiErr.GetMessage()
	}
	e.mu.Unlock()
}

func (e *pluginEntry) startEventWorker() {
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	if e.eventCh != nil {
		return
	}
	ch := make(chan *pluginv1.OnEventRequest, eventQueueSize)
	stop := make(chan struct{})
	e.eventCh = ch
	e.stopEv = stop

	go func() {
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-ch:
				if !ok || ev == nil {
					return
				}
				e.mu.RLock()
				be := e.backend
				st := e.state
				e.mu.RUnlock()
				if be == nil || st != StatusRunning {
					continue
				}
				_, err := be.Dispatch(context.Background(), &pluginv1.HostEnvelope{
					Payload: &pluginv1.HostEnvelope_OnEvent{OnEvent: ev},
				})
				if err != nil {
					e.logs.Append("event dispatch error: " + err.Error())
				}
			}
		}
	}()
}

func (e *pluginEntry) stopEventWorker() {
	e.eventMu.Lock()
	stop := e.stopEv
	e.stopEv = nil
	e.eventCh = nil
	e.eventMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (e *pluginEntry) enqueueEvent(ev *pluginv1.OnEventRequest) {
	e.eventMu.Lock()
	ch := e.eventCh
	e.eventMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
		// Non-blocking drop preserves audio/TTS latency during extreme chat bursts.
	}
}

// Manager coordinates discovery, lifecycle, permission checks, and dispatch for WASM and Native plugins.
type Manager struct {
	reloadMu sync.Mutex
	mu       sync.RWMutex

	opts       Options
	httpClient *http.Client
	kvStore    *KVStore
	closed     bool

	entries map[string]*pluginEntry // keyed by UUID (or path on pre-manifest load errors)

	subMu     sync.Mutex
	subSeq    atomic.Uint64
	listeners map[uint64]func()

	uiSubMu     sync.Mutex
	uiSubSeq    atomic.Uint64
	uiListeners map[uint64]func(PluginUIUpdate)
}

// NewManager creates a plugin Manager. No runtimes or databases are opened until Start/Reload and plugin discovery.
func NewManager(opts Options) *Manager {
	kvPath := strings.TrimSpace(opts.KVDBPath)
	if kvPath == "" && strings.TrimSpace(opts.BaseDir) != "" {
		kvPath = filepath.Join(opts.BaseDir, "plugins_kv.db")
	}
	if kvPath == "" {
		kvPath = ":memory:"
	}
	return &Manager{
		opts:       opts,
		httpClient: &http.Client{Timeout: maxHTTPTimeout},
		kvStore:    NewKVStore(kvPath),
		entries:    make(map[string]*pluginEntry),
	}
}

// BaseDir returns the configured plugins base directory.
func (m *Manager) BaseDir() string {
	return m.opts.BaseDir
}

// Start discovers and starts all enabled, trusted plugins.
func (m *Manager) Start() error {
	return m.Reload()
}

// Reload stops all running plugins and re-discovers plugins from disk and ExtraPaths.
func (m *Manager) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return errors.New("plugin manager is closed")
	}
	m.mu.RUnlock()

	m.stopAllEntries()

	cfg := m.currentConfig()
	if cfg != nil && !cfg.Plugins.Enabled {
		m.notifyStatusChange()
		return nil
	}

	var extra []string
	if cfg != nil {
		extra = cfg.Plugins.ExtraPaths
	}
	wasmPaths, nativePaths := discoverPlugins(m.opts.BaseDir, extra)
	if len(wasmPaths) == 0 && len(nativePaths) == 0 {
		m.notifyStatusChange()
		return nil
	}

	newEntries := make(map[string]*pluginEntry, len(wasmPaths)+len(nativePaths))
	for _, wp := range wasmPaths {
		ent := m.loadWasmEntry(wp, cfg)
		key := ent.uuid
		if key == "" {
			key = "err:" + wp
		}
		if _, dup := newEntries[key]; dup {
			ent.stopEventWorker()
			if ent.backend != nil {
				_ = ent.backend.Close()
			}
			continue
		}
		newEntries[key] = ent
	}

	for _, np := range nativePaths {
		ent := m.loadNativeEntry(np, cfg)
		key := ent.uuid
		if key == "" {
			key = "err:" + np
		}
		if _, dup := newEntries[key]; dup {
			ent.stopEventWorker()
			if ent.backend != nil {
				_ = ent.backend.Close()
			}
			continue
		}
		newEntries[key] = ent
	}

	m.mu.Lock()
	m.entries = newEntries
	m.mu.Unlock()

	m.notifyStatusChange()
	return nil
}

// Close shuts down all plugin runtimes and closes the KV SQLite database.
func (m *Manager) Close() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	m.stopAllEntries()
	return m.kvStore.Close()
}

func (m *Manager) stopAllEntries() {
	m.mu.Lock()
	old := m.entries
	m.entries = make(map[string]*pluginEntry)
	m.mu.Unlock()

	for _, ent := range old {
		ent.stopEventWorker()
		ent.mu.RLock()
		be := ent.backend
		ent.mu.RUnlock()
		if be != nil {
			_ = be.Close()
		}
	}
}

func (m *Manager) loadWasmEntry(path string, cfg *config.Config) *pluginEntry {
	ent := &pluginEntry{
		name:    filepath.Base(path),
		runtime: pluginv1.RuntimeKind_RUNTIME_KIND_WASM,
		path:    path,
		state:   StatusError,
		logs:    &logBuffer{},
	}

	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		ent.errMsg = fmt.Sprintf("read wasm file: %v", err)
		return ent
	}
	sum := sha256.Sum256(wasmBytes)
	ent.binarySHA256 = hex.EncodeToString(sum[:])

	be, manifest, apiErr, err := newWasmBackend(m, ent, path, wasmBytes)
	if manifest != nil {
		ent.uuid = strings.TrimSpace(manifest.GetUuid())
		ent.name = strings.TrimSpace(manifest.GetName())
		ent.version = strings.TrimSpace(manifest.GetVersion())
		ent.author = strings.TrimSpace(manifest.GetAuthor())
		ent.description = strings.TrimSpace(manifest.GetDescription())
		ent.manifest = manifest
	}
	if apiErr != nil || err != nil {
		if apiErr == nil {
			apiErr = &pluginv1.APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
				Message: err.Error(),
			}
		}
		ent.setError(apiErr)
		return ent
	}

	if isPluginDisabled(cfg, ent.uuid) {
		_ = be.Close()
		ent.state = StatusDisabled
		return ent
	}

	ent.backend = be
	if err := m.initBackend(ent, be); err != nil {
		_ = be.Close()
		ent.backend = nil
		ent.setError(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "Plugin Init failed: " + err.Error(),
		})
		return ent
	}

	ent.state = StatusRunning
	ent.startEventWorker()
	return ent
}

func (m *Manager) loadNativeEntry(jsonPath string, cfg *config.Config) *pluginEntry {
	ent := &pluginEntry{
		name:    filepath.Base(filepath.Dir(jsonPath)),
		runtime: pluginv1.RuntimeKind_RUNTIME_KIND_NATIVE,
		path:    jsonPath,
		state:   StatusError,
		logs:    &logBuffer{},
	}

	desc, binPath, shaHex, err := parseNativeDescriptor(jsonPath)
	if desc.UUID != "" {
		ent.uuid = desc.UUID
	}
	if desc.Name != "" {
		ent.name = desc.Name
	}
	ent.version = desc.Version
	ent.author = desc.Author
	ent.description = desc.Description
	ent.binaryPath = binPath
	ent.binarySHA256 = shaHex
	if err != nil {
		ent.setError(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: err.Error(),
		})
		return ent
	}

	if isPluginDisabled(cfg, ent.uuid) {
		ent.state = StatusDisabled
		return ent
	}

	if !isNativeTrusted(cfg, ent.uuid, shaHex) {
		ent.state = StatusAwaitingTrust
		return ent
	}

	be, manifest, apiErr, err := startNativeBackend(m, ent, desc, binPath)
	if manifest != nil {
		ent.manifest = manifest
		if manifest.GetUuid() != "" {
			ent.uuid = manifest.GetUuid()
		}
		if manifest.GetName() != "" {
			ent.name = manifest.GetName()
		}
		if manifest.GetVersion() != "" {
			ent.version = manifest.GetVersion()
		}
	}
	if apiErr != nil || err != nil {
		if apiErr == nil {
			apiErr = &pluginv1.APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
				Message: err.Error(),
			}
		}
		ent.setError(apiErr)
		return ent
	}

	ent.backend = be
	if err := m.initBackend(ent, be); err != nil {
		_ = be.Close()
		ent.backend = nil
		ent.setError(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "Plugin Init failed: " + err.Error(),
		})
		return ent
	}

	ent.state = StatusRunning
	ent.startEventWorker()
	return ent
}

func (m *Manager) initBackend(ent *pluginEntry, be pluginBackend) error {
	appVer := m.opts.Callbacks.AppVersion
	if appVer == "" {
		appVer = "dev"
	}
	gs := m.GetGlobalSettings(ent.uuid)
	resp, err := be.Dispatch(context.Background(), &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_Init{
			Init: &pluginv1.InitRequest{
				HostApiVersion: &pluginv1.ProtocolVersion{
					Major:        HostAPIMajor,
					Minor:        HostAPIMinor,
					Patch:        HostAPIPatch,
					MinHostMinor: HostAPIMinor,
				},
				HostAppVersion:        appVer,
				SupportedCapabilities: slices.Clone(SupportedCapabilities),
				GlobalSettings:        gs,
			},
		},
	})
	if err != nil {
		return err
	}
	if ack := resp.GetAck(); ack != nil && !ack.GetOk() {
		if ack.GetError() != nil {
			return errors.New(ack.GetError().GetMessage())
		}
		return errors.New("plugin rejected Init")
	}
	return nil
}

// Plugins returns a sorted snapshot of all discovered plugins and their current state.
func (m *Manager) Plugins() []PluginStatus {
	m.mu.RLock()
	out := make([]PluginStatus, 0, len(m.entries))
	for _, ent := range m.entries {
		ent.mu.RLock()
		var perms *pluginv1.Permissions
		var allowed []string
		var schema []*pluginv1.SettingField
		var actions []*pluginv1.ActionDescriptor
		var triggers []*pluginv1.TriggerDescriptor
		var views []*pluginv1.ViewDescriptor
		var overlayWidgets []*pluginv1.OverlayWidgetDescriptor
		apiVer := ""
		if ent.manifest != nil {
			perms = ent.manifest.GetPermissions()
			if perms != nil {
				allowed = slices.Clone(perms.GetAllowedHosts())
			}
			schema = ent.manifest.GetGlobalSettingsSchema()
			actions = slices.Clone(ent.manifest.GetActions())
			triggers = slices.Clone(ent.manifest.GetTriggers())
			views = slices.Clone(ent.manifest.GetViews())
			overlayWidgets = slices.Clone(ent.manifest.GetOverlayWidgets())
			apiVer = FormatVersion(ent.manifest.GetApiVersion())
		}
		var visuals map[string]*pluginv1.ActionVisualState
		if len(ent.actionVisuals) > 0 {
			visuals = make(map[string]*pluginv1.ActionVisualState, len(ent.actionVisuals))
			for k, av := range ent.actionVisuals {
				if av != nil {
					if cloned, ok := proto.Clone(av).(*pluginv1.ActionVisualState); ok {
						visuals[k] = cloned
					}
				}
			}
		}
		out = append(out, PluginStatus{
			UUID:              ent.uuid,
			Name:              ent.name,
			Version:           ent.version,
			Author:            ent.author,
			Description:       ent.description,
			Runtime:           ent.runtime,
			Path:              ent.path,
			BinaryPath:        ent.binaryPath,
			BinarySHA256:      ent.binarySHA256,
			APIVersion:        apiVer,
			Permissions:       perms,
			AllowedHosts:      allowed,
			HasGlobalSettings: len(schema) > 0,
			GlobalSchema:      schema,
			ActionCount:       len(actions),
			Actions:           actions,
			ActionVisuals:     visuals,
			Triggers:          triggers,
			Views:             views,
			OverlayWidgets:    overlayWidgets,
			State:             ent.state,
			Error:             ent.errMsg,
			APIError:          ent.apiErr,
		})
		ent.mu.RUnlock()
	}
	m.mu.RUnlock()

	slices.SortFunc(out, func(a, b PluginStatus) int {
		if c := cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

// PluginLogs returns the latest buffered log lines for pluginUUID.
func (m *Manager) PluginLogs(pluginUUID string) []string {
	m.mu.RLock()
	ent := m.entries[strings.TrimSpace(pluginUUID)]
	m.mu.RUnlock()
	if ent == nil || ent.logs == nil {
		return nil
	}
	return ent.logs.Snapshot()
}

// SetPluginEnabled enables or disables a plugin in config and reloads the manager.
func (m *Manager) SetPluginEnabled(pluginUUID string, enabled bool) error {
	uuid := strings.TrimSpace(pluginUUID)
	if uuid == "" {
		return errors.New("plugin UUID cannot be empty")
	}
	if m.opts.Callbacks.UpdateConfig != nil {
		if err := m.opts.Callbacks.UpdateConfig(func(c *config.Config) {
			filtered := make([]string, 0, len(c.Plugins.DisabledPlugins))
			for _, id := range c.Plugins.DisabledPlugins {
				if id != uuid {
					filtered = append(filtered, id)
				}
			}
			if !enabled {
				filtered = append(filtered, uuid)
			}
			c.Plugins.DisabledPlugins = filtered
		}); err != nil {
			return err
		}
	}
	return m.Reload()
}

// TrustNativePlugin records user approval for a native plugin's current binary SHA-256 and reloads.
func (m *Manager) TrustNativePlugin(pluginUUID string) error {
	uuid := strings.TrimSpace(pluginUUID)
	m.mu.RLock()
	ent := m.entries[uuid]
	m.mu.RUnlock()
	if ent == nil {
		return fmt.Errorf("unknown plugin %q", uuid)
	}
	ent.mu.RLock()
	rt := ent.runtime
	hash := ent.binarySHA256
	binPath := ent.binaryPath
	ent.mu.RUnlock()
	if rt != pluginv1.RuntimeKind_RUNTIME_KIND_NATIVE {
		return errors.New("only native plugins require trust approval")
	}
	if hash == "" {
		return errors.New("native plugin binary hash unavailable")
	}

	if m.opts.Callbacks.UpdateConfig != nil {
		if err := m.opts.Callbacks.UpdateConfig(func(c *config.Config) {
			if c.Plugins.TrustedNative == nil {
				c.Plugins.TrustedNative = make(map[string]config.TrustedNativePlugin)
			}
			c.Plugins.TrustedNative[uuid] = config.TrustedNativePlugin{
				SHA256:     hash,
				ApprovedAt: time.Now().UTC().Format(time.RFC3339),
				BinaryPath: binPath,
			}
		}); err != nil {
			return err
		}
	}
	return m.Reload()
}

// RevokeNativeTrust removes trust approval for a native plugin and reloads.
func (m *Manager) RevokeNativeTrust(pluginUUID string) error {
	uuid := strings.TrimSpace(pluginUUID)
	if m.opts.Callbacks.UpdateConfig != nil {
		if err := m.opts.Callbacks.UpdateConfig(func(c *config.Config) {
			delete(c.Plugins.TrustedNative, uuid)
		}); err != nil {
			return err
		}
	}
	return m.Reload()
}

// InvokeConfigAction dispatches an interactive button action from Settings -> Plugins -> Configure.
func (m *Manager) InvokeConfigAction(ctx context.Context, pluginUUID, actionKey string, settings *pluginv1.SettingsMap) error {
	uuid := strings.TrimSpace(pluginUUID)
	m.mu.RLock()
	ent := m.entries[uuid]
	m.mu.RUnlock()
	if ent == nil {
		return fmt.Errorf("unknown plugin %q", uuid)
	}
	ent.mu.RLock()
	be := ent.backend
	st := ent.state
	ent.mu.RUnlock()
	if be == nil || st != StatusRunning {
		return fmt.Errorf("plugin %q is not running", ent.name)
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_OnConfigAction{
			OnConfigAction: &pluginv1.OnConfigActionRequest{
				ActionKey: actionKey,
				Settings:  settings,
			},
		},
	})
	if err != nil {
		return err
	}
	if ack := resp.GetAck(); ack != nil && !ack.GetOk() {
		if ack.GetError() != nil {
			return pluginsdk.FromProto(ack.GetError())
		}
		return errors.New("config action failed")
	}
	return nil
}

func (m *Manager) notifyGlobalSettings(pluginUUID string, settings *pluginv1.SettingsMap) error {
	m.mu.RLock()
	ent := m.entries[pluginUUID]
	m.mu.RUnlock()
	if ent == nil {
		return nil
	}
	ent.mu.RLock()
	be := ent.backend
	st := ent.state
	ent.mu.RUnlock()
	if be == nil || st != StatusRunning {
		return nil
	}
	resp, err := be.Dispatch(context.Background(), &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_OnGlobalSettings{
			OnGlobalSettings: &pluginv1.OnGlobalSettingsRequest{Settings: settings},
		},
	})
	if err != nil {
		return err
	}
	if ack := resp.GetAck(); ack != nil && !ack.GetOk() && ack.GetError() != nil {
		return pluginsdk.FromProto(ack.GetError())
	}
	return nil
}

// DispatchStreamEvent fans out a normalized chat.Event to all running plugins with read_events permission.
func (m *Manager) DispatchStreamEvent(ev *chat.Event) {
	m.DispatchStreamEventWithState(ev, 0, false)
}

// DispatchStreamEventWithState fans out a normalized chat.Event along with active live stream session metadata.
func (m *Manager) DispatchStreamEventWithState(ev *chat.Event, streamSessionID int64, streamIsLive bool) {
	if ev == nil {
		return
	}
	pbEv := chatEventToProto(ev)
	pbEv.StreamSessionId = streamSessionID
	pbEv.StreamIsLive = streamIsLive
	req := &pluginv1.OnEventRequest{
		Event: &pluginv1.OnEventRequest_StreamEvent{StreamEvent: pbEv},
	}

	m.mu.RLock()
	for _, ent := range m.entries {
		ent.mu.RLock()
		ok := ent.state == StatusRunning && ent.manifest != nil && ent.manifest.GetPermissions().GetReadEvents() &&
			eventSubscribed(ent.manifest.GetSubscribedEvents(), string(ev.Kind))
		ent.mu.RUnlock()
		if ok {
			ent.enqueueEvent(req)
		}
	}
	m.mu.RUnlock()
}

// DispatchModerationEvent fans out a moderation sync event to subscribed plugins.
func (m *Manager) DispatchModerationEvent(ev *chat.ModerationEvent) {
	if ev == nil {
		return
	}
	req := &pluginv1.OnEventRequest{
		Event: &pluginv1.OnEventRequest_ModerationEvent{
			ModerationEvent: &pluginv1.ModerationEvent{
				Platform:        ev.Platform,
				Channel:         ev.Channel,
				Action:          string(ev.Action),
				TargetMessageId: ev.TargetMessageID,
				TargetUserId:    ev.TargetUserID,
				TimestampUnixMs: ev.Timestamp.UnixMilli(),
			},
		},
	}
	m.mu.RLock()
	for _, ent := range m.entries {
		ent.mu.RLock()
		ok := ent.state == StatusRunning && ent.manifest != nil && ent.manifest.GetPermissions().GetReadEvents() &&
			eventSubscribed(ent.manifest.GetSubscribedEvents(), "moderation")
		ent.mu.RUnlock()
		if ok {
			ent.enqueueEvent(req)
		}
	}
	m.mu.RUnlock()
}

// DispatchTTSLifecycle fans out TTS start/finish notifications to subscribed plugins (for music ducking, etc.).
func (m *Manager) DispatchTTSLifecycle(ev *pluginv1.TTSSpeechLifecycleEvent) {
	if ev == nil {
		return
	}
	req := &pluginv1.OnEventRequest{
		Event: &pluginv1.OnEventRequest_TtsLifecycle{TtsLifecycle: ev},
	}
	m.mu.RLock()
	for _, ent := range m.entries {
		ent.mu.RLock()
		ok := ent.state == StatusRunning && ent.manifest != nil && ent.manifest.GetPermissions().GetReadEvents() &&
			eventSubscribed(ent.manifest.GetSubscribedEvents(), "tts_lifecycle")
		ent.mu.RUnlock()
		if ok {
			ent.enqueueEvent(req)
		}
	}
	m.mu.RUnlock()
}

func eventSubscribed(subs []string, kind string) bool {
	if len(subs) == 0 {
		return true
	}
	for _, s := range subs {
		if s == "*" || strings.EqualFold(strings.TrimSpace(s), kind) {
			return true
		}
	}
	return false
}

func subscribesBeforeTTS(man *pluginv1.Manifest) bool {
	if man == nil {
		return false
	}
	for _, s := range man.GetSubscribedEvents() {
		if strings.EqualFold(strings.TrimSpace(s), "before_tts") || s == "*" {
			return true
		}
	}
	return slices.Contains(man.GetRequiredCapabilities(), "bot.before_tts.v1")
}

// InvokeBeforeTTS runs synchronous pre-TTS middleware across subscribed plugins within a strict <= 500ms budget.
// Any plugin returning consume=true halts the pipeline and suppresses TTS for the utterance.
func (m *Manager) InvokeBeforeTTS(
	ctx context.Context,
	ev *chat.Event,
	proposedText string,
	proposedVoice string,
	proposedSpeed float32,
	streamSessionID int64,
	streamIsLive bool,
) (consume bool, finalText string, finalVoice string, finalSpeed float32) {
	finalText = proposedText
	finalVoice = proposedVoice
	finalSpeed = proposedSpeed
	if ev == nil {
		return false, finalText, finalVoice, finalSpeed
	}

	type target struct {
		ent *pluginEntry
		be  pluginBackend
	}
	var targets []target
	m.mu.RLock()
	for _, ent := range m.entries {
		ent.mu.RLock()
		ok := ent.state == StatusRunning && ent.backend != nil && subscribesBeforeTTS(ent.manifest)
		be := ent.backend
		ent.mu.RUnlock()
		if ok {
			targets = append(targets, target{ent: ent, be: be})
		}
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return false, finalText, finalVoice, finalSpeed
	}

	budgetCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	pbEv := chatEventToProto(ev)
	pbEv.StreamSessionId = streamSessionID
	pbEv.StreamIsLive = streamIsLive

	for _, t := range targets {
		if budgetCtx.Err() != nil {
			break
		}
		resp, err := t.be.Dispatch(budgetCtx, &pluginv1.HostEnvelope{
			Payload: &pluginv1.HostEnvelope_BeforeTts{
				BeforeTts: &pluginv1.BeforeTTSRequest{
					Event:         pbEv,
					ProposedText:  finalText,
					ProposedVoice: finalVoice,
					ProposedSpeed: finalSpeed,
				},
			},
		})
		if err != nil {
			if t.ent != nil && t.ent.logs != nil {
				t.ent.logs.Append("before_tts error: " + err.Error())
			}
			continue
		}
		if bResp := resp.GetBeforeTtsResponse(); bResp != nil {
			if txt := bResp.GetOverrideText(); txt != "" {
				finalText = txt
			}
			if v := strings.TrimSpace(bResp.GetOverrideVoice()); v != "" {
				finalVoice = v
			}
			if sp := bResp.GetOverrideSpeed(); sp > 0 {
				finalSpeed = sp
			}
			if bResp.GetConsume() {
				return true, finalText, finalVoice, finalSpeed
			}
		}
	}
	return false, finalText, finalVoice, finalSpeed
}

// RunPluginAction invokes an action declared by pluginUUID and returns updated arguments and continuation flag.
func (m *Manager) RunPluginAction(ctx context.Context, pluginUUID, actionID string, args *pluginv1.ActionArgs) (bool, *pluginv1.ActionArgs, error) {
	_, be, man, err := m.runningBackend(pluginUUID)
	if err != nil {
		return false, args, err
	}
	hasCap := slices.Contains(man.GetRequiredCapabilities(), "bot.actions.v1")
	if len(man.GetActions()) == 0 && !hasCap {
		return false, args, pluginsdk.FromProto(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
			Message: "plugin does not declare any automatable actions",
		})
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_RunAction{
			RunAction: &pluginv1.RunActionRequest{
				ActionId: actionID,
				Args:     args,
			},
		},
	})
	if err != nil {
		return false, args, err
	}
	if ack := resp.GetAck(); ack != nil {
		if !ack.GetOk() && ack.GetError() != nil {
			return false, args, pluginsdk.FromProto(ack.GetError())
		}
		return ack.GetOk(), args, nil
	}
	ra := resp.GetRunActionResponse()
	if ra == nil {
		return false, args, errors.New("plugin returned unexpected response for RunAction")
	}
	if ra.GetError() != nil {
		return false, ra.GetUpdatedArgs(), pluginsdk.FromProto(ra.GetError())
	}
	updated := ra.GetUpdatedArgs()
	if updated == nil {
		updated = args
	}
	return ra.GetContinueChain(), updated, nil
}

func (m *Manager) handleHostCall(ctx context.Context, ent *pluginEntry, pEnv *pluginv1.PluginEnvelope) *pluginv1.HostCallResponse {
	if ent == nil || pEnv == nil {
		return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid host call")
	}

	ent.mu.RLock()
	uuid := ent.uuid
	manifest := ent.manifest
	rt := ent.runtime
	be := ent.backend
	ent.mu.RUnlock()

	perms := manifest.GetPermissions()

	switch pay := pEnv.Payload.(type) {
	case *pluginv1.PluginEnvelope_Log:
		if pay.Log != nil {
			ent.logs.Append(fmt.Sprintf("[%s] %s", strings.TrimPrefix(pay.Log.GetLevel().String(), "LOG_LEVEL_"), pay.Log.GetMessage()))
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_GetGlobalSettings:
		return &pluginv1.HostCallResponse{
			Ok: true,
			Result: &pluginv1.HostCallResponse_GlobalSettings{
				GlobalSettings: &pluginv1.GetGlobalSettingsResponse{
					Settings: m.GetGlobalSettings(uuid),
				},
			},
		}

	case *pluginv1.PluginEnvelope_SaveGlobalSettings:
		if pay.SaveGlobalSettings == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing save_global_settings payload")
		}
		if err := m.SaveGlobalSettings(uuid, pay.SaveGlobalSettings.GetSettings()); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_SetTimer:
		if pay.SetTimer == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing set_timer payload")
		}
		if be == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "plugin backend is not active")
		}
		if apiErr := be.SetTimer(pay.SetTimer.GetTimerId(), pay.SetTimer.GetContext(), pay.SetTimer.GetIntervalMs()); apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_HttpRequest:
		if pay.HttpRequest == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing http_request payload")
		}
		enforce := rt == pluginv1.RuntimeKind_RUNTIME_KIND_WASM || len(perms.GetAllowedHosts()) > 0
		var allowed []string
		if perms != nil {
			allowed = perms.GetAllowedHosts()
		}
		if pay.HttpRequest.GetAsync() {
			if enforce && !IsHostAllowed(pay.HttpRequest.GetUrl(), allowed) {
				return &pluginv1.HostCallResponse{
					Ok: false,
					Error: &pluginv1.APIError{
						Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
						Message: fmt.Sprintf("HTTP request to %q blocked by plugin allowed_hosts sandbox", pay.HttpRequest.GetUrl()),
					},
				}
			}
			reqCopy := pay.HttpRequest
			go func() {
				resp, _ := ExecuteHTTP(context.Background(), m.httpClient, allowed, enforce, reqCopy)
				if be != nil {
					_, _ = be.Dispatch(context.Background(), &pluginv1.HostEnvelope{
						Payload: &pluginv1.HostEnvelope_OnHttpResult{
							OnHttpResult: &pluginv1.OnHTTPResultRequest{
								AsyncId:  reqCopy.GetAsyncId(),
								Response: resp,
							},
						},
					})
				}
			}()
			return &pluginv1.HostCallResponse{Ok: true}
		}

		resp, apiErr := ExecuteHTTP(ctx, m.httpClient, allowed, enforce, pay.HttpRequest)
		if apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{
			Ok:     true,
			Result: &pluginv1.HostCallResponse_HttpResponse{HttpResponse: resp},
		}

	case *pluginv1.PluginEnvelope_Speak:
		if !perms.GetSpeakTts() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare speak_tts permission")
		}
		if pay.Speak == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing speak payload")
		}
		if m.opts.Callbacks.OnSpeak == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "TTS speak callback unavailable")
		}
		err := m.opts.Callbacks.OnSpeak(SpeakRequest{
			PluginUUID: uuid,
			Text:       pay.Speak.GetText(),
			VoiceID:    pay.Speak.GetVoiceId(),
			Speed:      pay.Speak.GetSpeed(),
			Priority:   int(pay.Speak.GetPriority()),
			Username:   pay.Speak.GetUsername(),
		})
		if err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_KvGet:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.KvGet == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing kv_get payload")
		}
		val, found, apiErr := m.kvStore.Get(ctx, uuid, pay.KvGet.GetKey())
		if apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{
			Ok:     true,
			Result: &pluginv1.HostCallResponse_KvGet{KvGet: &pluginv1.KVGetResponse{Found: found, Value: val}},
		}

	case *pluginv1.PluginEnvelope_KvSet:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.KvSet == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing kv_set payload")
		}
		if apiErr := m.kvStore.Set(ctx, uuid, pay.KvSet.GetKey(), pay.KvSet.GetValue()); apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_KvDelete:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.KvDelete == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing kv_delete payload")
		}
		if apiErr := m.kvStore.Delete(ctx, uuid, pay.KvDelete.GetKey()); apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_KvList:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.KvList == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing kv_list payload")
		}
		keys, apiErr := m.kvStore.List(ctx, uuid, pay.KvList.GetPrefix(), pay.KvList.GetLimit())
		if apiErr != nil {
			return &pluginv1.HostCallResponse{Ok: false, Error: apiErr}
		}
		return &pluginv1.HostCallResponse{
			Ok:     true,
			Result: &pluginv1.HostCallResponse_KvList{KvList: &pluginv1.KVListResponse{Keys: keys}},
		}

	case *pluginv1.PluginEnvelope_OverlayBroadcast:
		if !perms.GetOverlayBroadcast() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare overlay_broadcast permission")
		}
		if pay.OverlayBroadcast == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing overlay_broadcast payload")
		}
		if m.opts.Callbacks.OnOverlayBroadcast != nil {
			m.opts.Callbacks.OnOverlayBroadcast(uuid, pay.OverlayBroadcast.GetEventName(), pay.OverlayBroadcast.GetJsonPayload())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_PushUiSnapshot:
		if !perms.GetUiViews() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare ui_views permission")
		}
		if pay.PushUiSnapshot == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing push_ui_snapshot payload")
		}
		m.notifyUIUpdate(PluginUIUpdate{
			PluginUUID: uuid,
			ViewID:     pay.PushUiSnapshot.GetViewId(),
			Snapshot:   pay.PushUiSnapshot,
		})
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_PushUiPatch:
		if !perms.GetUiViews() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare ui_views permission")
		}
		if pay.PushUiPatch == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing push_ui_patch payload")
		}
		m.notifyUIUpdate(PluginUIUpdate{
			PluginUUID: uuid,
			ViewID:     pay.PushUiPatch.GetViewId(),
			Patch:      pay.PushUiPatch,
		})
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_SendChat:
		if !perms.GetSendChat() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare send_chat permission")
		}
		if pay.SendChat == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing send_chat payload")
		}
		if m.opts.Callbacks.OnSendChat == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "send_chat callback unavailable")
		}
		if err := m.opts.Callbacks.OnSendChat(ctx, uuid, pay.SendChat); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_Moderate:
		if !perms.GetModerate() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare moderate permission")
		}
		if pay.Moderate == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing moderate payload")
		}
		if m.opts.Callbacks.OnModerate == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "moderate callback unavailable")
		}
		if err := m.opts.Callbacks.OnModerate(ctx, uuid, pay.Moderate); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_GetVar:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.GetVar == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing get_var payload")
		}
		if m.opts.Callbacks.OnGetVar == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "get_var callback unavailable")
		}
		val, found, err := m.opts.Callbacks.OnGetVar(ctx, uuid, pay.GetVar)
		if err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{
			Ok:     true,
			Result: &pluginv1.HostCallResponse_GetVar{GetVar: &pluginv1.GetVarResponse{Found: found, Value: val}},
		}

	case *pluginv1.PluginEnvelope_SetVar:
		if !perms.GetKvStorage() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare kv_storage permission")
		}
		if pay.SetVar == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing set_var payload")
		}
		if m.opts.Callbacks.OnSetVar == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "set_var callback unavailable")
		}
		if err := m.opts.Callbacks.OnSetVar(ctx, uuid, pay.SetVar); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_EmitTrigger:
		if !perms.GetRunActions() && !perms.GetReadEvents() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare run_actions or read_events permission")
		}
		if pay.EmitTrigger == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing emit_trigger payload")
		}
		if m.opts.Callbacks.OnEmitTrigger == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "emit_trigger callback unavailable")
		}
		if err := m.opts.Callbacks.OnEmitTrigger(ctx, uuid, pay.EmitTrigger); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_RunBotAction:
		if !perms.GetRunActions() {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "plugin does not declare run_actions permission")
		}
		if pay.RunBotAction == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing run_bot_action payload")
		}
		if m.opts.Callbacks.OnRunBotAction == nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY, "run_bot_action callback unavailable")
		}
		if err := m.opts.Callbacks.OnRunBotAction(ctx, uuid, pay.RunBotAction); err != nil {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		return &pluginv1.HostCallResponse{Ok: true}

	case *pluginv1.PluginEnvelope_UpdateActionVisual:
		if pay.UpdateActionVisual == nil || strings.TrimSpace(pay.UpdateActionVisual.GetActionId()) == "" {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "missing action_id in update_action_visual")
		}
		cloned, ok := proto.Clone(pay.UpdateActionVisual).(*pluginv1.ActionVisualState)
		if !ok {
			return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_INTERNAL, "failed cloning action visual state")
		}
		ent.mu.Lock()
		if ent.actionVisuals == nil {
			ent.actionVisuals = make(map[string]*pluginv1.ActionVisualState)
		}
		ent.actionVisuals[strings.TrimSpace(cloned.GetActionId())] = cloned
		ent.mu.Unlock()
		return &pluginv1.HostCallResponse{Ok: true}

	default:
		return hostErrResp(pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY, fmt.Sprintf("unsupported host call %T in API v%s", pEnv.Payload, HostVersionString()))
	}
}

func hostErrResp(code pluginv1.ErrorCode, msg string) *pluginv1.HostCallResponse {
	return &pluginv1.HostCallResponse{
		Ok: false,
		Error: &pluginv1.APIError{
			Code:    code,
			Message: msg,
		},
	}
}

func (m *Manager) currentConfig() *config.Config {
	if m.opts.Callbacks.GetConfig != nil {
		return m.opts.Callbacks.GetConfig()
	}
	return nil
}

// RestartPlugin restarts plugins cleanly (currently delegates to Reload).
func (m *Manager) RestartPlugin(_ string) error {
	return m.Reload()
}

// AllowedHostsForPlugin returns the configured allowed_hosts list and whether host allowlist enforcement applies.
func (m *Manager) AllowedHostsForPlugin(pluginUUID string) ([]string, bool) {
	uuid := strings.TrimSpace(pluginUUID)
	m.mu.RLock()
	ent := m.entries[uuid]
	m.mu.RUnlock()
	if ent == nil {
		return nil, false
	}
	ent.mu.RLock()
	defer ent.mu.RUnlock()
	var allowed []string
	if ent.manifest != nil && ent.manifest.GetPermissions() != nil {
		allowed = slices.Clone(ent.manifest.GetPermissions().GetAllowedHosts())
	}
	enforce := ent.runtime == pluginv1.RuntimeKind_RUNTIME_KIND_WASM || len(allowed) > 0
	return allowed, enforce
}

func (m *Manager) runningBackend(pluginUUID string) (*pluginEntry, pluginBackend, *pluginv1.Manifest, error) {
	uuid := strings.TrimSpace(pluginUUID)
	m.mu.RLock()
	ent := m.entries[uuid]
	m.mu.RUnlock()
	if ent == nil {
		return nil, nil, nil, fmt.Errorf("unknown plugin %q", uuid)
	}
	ent.mu.RLock()
	be := ent.backend
	st := ent.state
	man := ent.manifest
	name := ent.name
	ent.mu.RUnlock()
	if be == nil || st != StatusRunning {
		return ent, nil, man, fmt.Errorf("plugin %q is not running (state=%s)", name, st)
	}
	return ent, be, man, nil
}

// RenderView requests an initial or resync UISnapshot for viewID from a running plugin.
func (m *Manager) RenderView(ctx context.Context, pluginUUID, viewID string) (*pluginv1.UISnapshot, error) {
	_, be, man, err := m.runningBackend(pluginUUID)
	if err != nil {
		return nil, err
	}
	if !man.GetPermissions().GetUiViews() {
		return nil, pluginsdk.FromProto(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
			Message: "plugin does not declare ui_views permission",
		})
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_RenderView{
			RenderView: &pluginv1.RenderViewRequest{ViewId: viewID},
		},
	})
	if err != nil {
		return nil, err
	}
	if ack := resp.GetAck(); ack != nil {
		if !ack.GetOk() {
			if ack.GetError() != nil {
				return nil, pluginsdk.FromProto(ack.GetError())
			}
			return nil, errors.New("plugin rejected RenderView")
		}
		return nil, errors.New("plugin returned empty snapshot for RenderView")
	}
	snap := resp.GetUiSnapshot()
	if snap == nil {
		return nil, errors.New("plugin returned unexpected response for RenderView")
	}
	return snap, nil
}

// SendUIEvent forwards a user interaction event from TownCrier's UI reconciler to a running plugin.
func (m *Manager) SendUIEvent(ctx context.Context, pluginUUID string, ev *pluginv1.UIEvent) (*pluginv1.UISnapshot, *pluginv1.UIPatchBatch, error) {
	if ev == nil {
		return nil, nil, errors.New("nil UIEvent")
	}
	_, be, man, err := m.runningBackend(pluginUUID)
	if err != nil {
		return nil, nil, err
	}
	if !man.GetPermissions().GetUiViews() {
		return nil, nil, pluginsdk.FromProto(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
			Message: "plugin does not declare ui_views permission",
		})
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_OnUiEvent{OnUiEvent: ev},
	})
	if err != nil {
		return nil, nil, err
	}
	if ack := resp.GetAck(); ack != nil {
		if !ack.GetOk() {
			if ack.GetError() != nil {
				return nil, nil, pluginsdk.FromProto(ack.GetError())
			}
			return nil, nil, errors.New("plugin rejected UIEvent")
		}
		return nil, nil, nil
	}
	return resp.GetUiSnapshot(), resp.GetUiPatch(), nil
}

// GetOverlayAsset requests a plugin-served OBS browser overlay asset (HTML/CSS/JS/image).
func (m *Manager) GetOverlayAsset(ctx context.Context, pluginUUID, assetPath string) (string, []byte, bool, error) {
	_, be, man, err := m.runningBackend(pluginUUID)
	if err != nil {
		return "", nil, false, err
	}
	hasCap := slices.Contains(man.GetRequiredCapabilities(), "overlay.routes.v1") ||
		slices.Contains(man.GetRequiredCapabilities(), "overlay.widgets.v1") ||
		len(man.GetOverlayWidgets()) > 0
	if !man.GetPermissions().GetOverlayBroadcast() && !hasCap {
		return "", nil, false, pluginsdk.FromProto(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
			Message: "plugin does not declare overlay_broadcast, overlay.routes.v1, or overlay.widgets.v1",
		})
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_GetOverlayAsset{
			GetOverlayAsset: &pluginv1.GetOverlayAssetRequest{Path: assetPath},
		},
	})
	if err != nil {
		return "", nil, false, err
	}
	if ack := resp.GetAck(); ack != nil {
		if !ack.GetOk() && ack.GetError() != nil {
			return "", nil, false, pluginsdk.FromProto(ack.GetError())
		}
		return "", nil, false, errors.New("plugin returned unexpected ack for GetOverlayAsset")
	}
	assetResp := resp.GetGetOverlayAssetResponse()
	if assetResp == nil {
		return "", nil, false, errors.New("plugin returned empty GetOverlayAsset response")
	}
	if assetResp.GetError() != nil {
		return "", nil, false, pluginsdk.FromProto(assetResp.GetError())
	}
	return assetResp.GetContentType(), assetResp.GetBody(), assetResp.GetFound(), nil
}

// HasRunningOverlayWidgets reports whether any currently running plugin declares OBS overlay widgets.
func (m *Manager) HasRunningOverlayWidgets() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ent := range m.entries {
		if ent == nil {
			continue
		}
		ent.mu.RLock()
		has := ent.state == StatusRunning && ent.manifest != nil && len(ent.manifest.GetOverlayWidgets()) > 0
		ent.mu.RUnlock()
		if has {
			return true
		}
	}
	return false
}

// NotifyOverlayStyleChanged notifies a running plugin that a declared overlay widget's Designer style changed.
func (m *Manager) NotifyOverlayStyleChanged(ctx context.Context, pluginUUID, widgetID string, values map[string]any, customCSS string, useHostTheme bool) error {
	_, be, man, err := m.runningBackend(pluginUUID)
	if err != nil {
		return err
	}
	var schema []*pluginv1.SettingField
	for _, w := range man.GetOverlayWidgets() {
		if w != nil && w.GetId() == widgetID {
			schema = w.GetStyleSchema()
			break
		}
	}
	resp, err := be.Dispatch(ctx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_OnOverlayStyleChanged{
			OnOverlayStyleChanged: &pluginv1.OnOverlayStyleChangedRequest{
				WidgetId:     widgetID,
				Values:       mapToSettingsProto(values, schema),
				CustomCss:    customCSS,
				UseHostTheme: useHostTheme,
			},
		},
	})
	if err != nil {
		return err
	}
	if ack := resp.GetAck(); ack != nil && !ack.GetOk() {
		if ack.GetError() != nil {
			return pluginsdk.FromProto(ack.GetError())
		}
		return errors.New("plugin rejected overlay style change")
	}
	return nil
}

// SubscribeUIUpdate registers a listener invoked whenever a plugin pushes a UI snapshot or patch batch.
func (m *Manager) SubscribeUIUpdate(fn func(PluginUIUpdate)) func() {
	if fn == nil {
		return func() {}
	}
	id := m.uiSubSeq.Add(1)
	m.uiSubMu.Lock()
	if m.uiListeners == nil {
		m.uiListeners = make(map[uint64]func(PluginUIUpdate))
	}
	m.uiListeners[id] = fn
	m.uiSubMu.Unlock()
	return func() {
		m.uiSubMu.Lock()
		delete(m.uiListeners, id)
		m.uiSubMu.Unlock()
	}
}

func (m *Manager) notifyUIUpdate(upd PluginUIUpdate) {
	m.uiSubMu.Lock()
	fns := make([]func(PluginUIUpdate), 0, len(m.uiListeners))
	for _, fn := range m.uiListeners {
		if fn != nil {
			fns = append(fns, fn)
		}
	}
	m.uiSubMu.Unlock()
	for _, fn := range fns {
		fn(upd)
	}
}

// SubscribeStatusChange registers a listener invoked whenever plugin state or settings change.
func (m *Manager) SubscribeStatusChange(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	id := m.subSeq.Add(1)
	m.subMu.Lock()
	if m.listeners == nil {
		m.listeners = make(map[uint64]func())
	}
	m.listeners[id] = fn
	m.subMu.Unlock()
	return func() {
		m.subMu.Lock()
		delete(m.listeners, id)
		m.subMu.Unlock()
	}
}

func (m *Manager) notifyStatusChange() {
	if m.opts.Callbacks.OnStatusChange != nil {
		m.opts.Callbacks.OnStatusChange()
	}
	m.subMu.Lock()
	fns := make([]func(), 0, len(m.listeners))
	for _, fn := range m.listeners {
		if fn != nil {
			fns = append(fns, fn)
		}
	}
	m.subMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func isPluginDisabled(cfg *config.Config, uuid string) bool {
	if cfg == nil || uuid == "" {
		return false
	}
	return slices.Contains(cfg.Plugins.DisabledPlugins, uuid)
}

func isNativeTrusted(cfg *config.Config, uuid, shaHex string) bool {
	if cfg == nil || uuid == "" || shaHex == "" {
		return false
	}
	t, ok := cfg.Plugins.TrustedNative[uuid]
	return ok && strings.EqualFold(strings.TrimSpace(t.SHA256), shaHex)
}

func discoverPlugins(baseDir string, extraPaths []string) (wasmPaths, nativePaths []string) {
	seenWasm := make(map[string]bool)
	seenNative := make(map[string]bool)

	addWasm := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seenWasm[abs] {
			seenWasm[abs] = true
			wasmPaths = append(wasmPaths, abs)
		}
	}
	addNative := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seenNative[abs] {
			seenNative[abs] = true
			nativePaths = append(nativePaths, abs)
		}
	}

	scanWasmDir := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, ent := range entries {
			full := filepath.Join(dir, ent.Name())
			if !ent.IsDir() {
				if strings.HasSuffix(strings.ToLower(ent.Name()), ".wasm") {
					addWasm(full)
				}
				continue
			}
			sub, subErr := os.ReadDir(full)
			if subErr != nil {
				continue
			}
			for _, se := range sub {
				if !se.IsDir() && strings.HasSuffix(strings.ToLower(se.Name()), ".wasm") {
					addWasm(filepath.Join(full, se.Name()))
				}
			}
		}
	}

	scanNativeDir := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, ent := range entries {
			full := filepath.Join(dir, ent.Name())
			if !ent.IsDir() {
				low := strings.ToLower(ent.Name())
				switch {
				case low == "plugin.json":
					addNative(full)
				case strings.HasSuffix(low, ".link.json"):
					resolveNativeLink(full, addNative)
				}
				continue
			}
			cand := filepath.Join(full, "plugin.json")
			if st, statErr := os.Stat(cand); statErr == nil && !st.IsDir() {
				addNative(cand)
			}
		}
	}

	if strings.TrimSpace(baseDir) != "" {
		scanWasmDir(filepath.Join(baseDir, "wasm"))
		scanNativeDir(filepath.Join(baseDir, "native"))
	}
	for _, raw := range extraPaths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			low := strings.ToLower(filepath.Base(p))
			switch {
			case strings.HasSuffix(low, ".wasm"):
				addWasm(p)
			case low == "plugin.json":
				addNative(p)
			case strings.HasSuffix(low, ".link.json"):
				resolveNativeLink(p, addNative)
			}
			continue
		}
		scanWasmDir(p)
		scanNativeDir(p)
	}

	slices.Sort(wasmPaths)
	slices.Sort(nativePaths)
	return wasmPaths, nativePaths
}

type nativeLinkDescriptor struct {
	PluginDir string `json:"plugin_dir"`
}

func resolveNativeLink(linkPath string, addNative func(string)) {
	f, err := os.Open(linkPath)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return
	}
	var desc nativeLinkDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return
	}
	pluginDir := strings.TrimSpace(desc.PluginDir)
	if pluginDir == "" || !filepath.IsAbs(pluginDir) {
		return
	}
	cand := filepath.Join(filepath.Clean(pluginDir), "plugin.json")
	if st, statErr := os.Stat(cand); statErr == nil && !st.IsDir() {
		addNative(cand)
	}
}

func chatEventToProto(ev *chat.Event) *pluginv1.StreamEvent {
	spans := make([]*pluginv1.EmoteSpan, 0, len(ev.EmoteSpans))
	for _, sp := range ev.EmoteSpans {
		spans = append(spans, &pluginv1.EmoteSpan{
			Start: int32(sp.Start),
			End:   int32(sp.End),
			Id:    sp.ID,
			Name:  sp.Name,
			Url:   sp.URL,
		})
	}
	return &pluginv1.StreamEvent{
		Id:                ev.ID,
		Platform:          ev.Platform,
		Channel:           ev.Channel,
		StreamId:          ev.StreamID,
		StreamTitle:       ev.StreamTitle,
		StreamOrientation: ev.StreamOrientation,
		Kind:              string(ev.Kind),
		TimestampUnixMs:   ev.Timestamp.UnixMilli(),
		UserId:            ev.UserID,
		UserName:          ev.UserName,
		DisplayName:       ev.DisplayName,
		Roles:             slices.Clone(ev.Roles),
		FanClubLevel:      int32(ev.FanClubLevel),
		IsBotSignal:       ev.IsBotSignal,
		IsReply:           ev.IsReply,
		ReplyToUser:       ev.ReplyToUser,
		Mentions:          slices.Clone(ev.Mentions),
		Text:              ev.Text,
		EmoteSpans:        spans,
		TranslatedText:    ev.TranslatedText,
		SourceLang:        ev.SourceLang,
		Amount:            ev.Amount,
		UnitAmount:        ev.UnitAmount,
		Currency:          ev.Currency,
		ItemName:          ev.ItemName,
		SubTier:           ev.SubTier,
		Recipient:         ev.Recipient,
		Months:            int32(ev.Months),
		Count:             int32(ev.Count),
		RewardId:          ev.RewardID,
		RewardTitle:       ev.RewardTitle,
	}
}
