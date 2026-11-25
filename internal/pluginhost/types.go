package pluginhost

import (
	"slices"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

// RunState describes the lifecycle state of a discovered plugin.
type RunState string

const (
	StatusRunning       RunState = "running"
	StatusDisabled      RunState = "disabled"
	StatusAwaitingTrust RunState = "awaiting_trust"
	StatusError         RunState = "error"
	StatusStopped       RunState = "stopped"

	maxLogLines = 200
)

// SpeakRequest carries a TTS synthesis request issued by a plugin.
type SpeakRequest struct {
	PluginUUID string
	Text       string
	VoiceID    string
	Speed      float32
	Priority   int
	Username   string
}

// HostCallbacks integrates pluginhost with TownCrier's Session, Config, Secrets, and WebSocket server.
type HostCallbacks struct {
	AppVersion         string
	OnSpeak            func(req SpeakRequest) error
	OnOverlayBroadcast func(pluginUUID, eventName, jsonPayload string)
	OnStatusChange     func()
	GetConfig          func() *config.Config
	UpdateConfig       func(fn func(c *config.Config)) error
}

// Options configures a pluginhost.Manager.
type Options struct {
	BaseDir    string // Directory containing wasm/ and native/ plugin subfolders; empty disables default disk scan unless ExtraPaths is set.
	KVDBPath   string // Path to plugins_kv.db (":memory:" in tests; empty disables lazy disk open until BaseDir is set).
	Secrets    secrets.Store
	Callbacks  HostCallbacks
}

// PluginStatus summarizes one discovered plugin for UI rendering and diagnostics.
type PluginStatus struct {
	UUID              string
	Name              string
	Version           string
	Author            string
	Description       string
	Runtime           pluginv1.RuntimeKind
	Path              string
	BinaryPath        string
	BinarySHA256      string
	APIVersion        string
	Permissions       *pluginv1.Permissions
	AllowedHosts      []string
	HasGlobalSettings bool
	GlobalSchema      []*pluginv1.SettingField
	ActionCount       int
	Views             []*pluginv1.ViewDescriptor
	State             RunState
	Error             string
	APIError          *pluginv1.APIError
}

// PluginUIUpdate delivers a plugin-initiated snapshot or incremental patch for an active view.
type PluginUIUpdate struct {
	PluginUUID string
	ViewID     string
	Snapshot   *pluginv1.UISnapshot
	Patch      *pluginv1.UIPatchBatch
}

type logBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *logBuffer) Append(line string) {
	ts := time.Now().Format("15:04:05")
	entry := ts + " " + line
	b.mu.Lock()
	b.lines = append(b.lines, entry)
	if len(b.lines) > maxLogLines {
		copy(b.lines, b.lines[len(b.lines)-maxLogLines:])
		b.lines = b.lines[:maxLogLines]
	}
	b.mu.Unlock()
}

func (b *logBuffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.lines)
}
