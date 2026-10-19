package eventlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Backend defines the pluggable contract for event log storage backends and forwarding sinks.
// Stateless or append-only sinks (such as future webhook forwarders) can embed BaseSink
// so they only need to implement Name() and Append().
type Backend interface {
	Name() string
	Append(ctx context.Context, entry *Entry) error
	UpdateOutcome(ctx context.Context, id string, update OutcomeUpdate) error
	Query(ctx context.Context, filter QueryFilter) (*QueryResult, error)
	Summarize(ctx context.Context, filter QueryFilter) (*QuerySummary, error)
	StartSession(ctx context.Context, startedAt time.Time, platform, channel string) (*StreamSession, error)
	AddSessionPlatform(ctx context.Context, sessionID int64, platform, channel string) error
	EndSession(ctx context.Context, sessionID int64, endedAt time.Time) error
	ListSessions(ctx context.Context, limit int) ([]StreamSession, error)
	DistinctChannels(ctx context.Context, platform string) ([]string, error)
	Prune(ctx context.Context, policy RetentionPolicy) (int64, error)
	Close() error
}

// BaseSink provides safe no-op default implementations for non-storage or append-only backends
// (such as webhook forwarders, websocket broadcasters, or stdout/file streams).
type BaseSink struct{}

// UpdateOutcome is a no-op returning nil for stateless sinks that do not track outcome updates.
func (BaseSink) UpdateOutcome(context.Context, string, OutcomeUpdate) error {
	return nil
}

// Query is a no-op returning an empty QueryResult and nil error for non-queryable sinks.
func (BaseSink) Query(context.Context, QueryFilter) (*QueryResult, error) {
	return &QueryResult{}, nil
}

// Summarize is a no-op returning an empty QuerySummary and nil error for non-queryable sinks.
func (BaseSink) Summarize(context.Context, QueryFilter) (*QuerySummary, error) {
	return &QuerySummary{}, nil
}

// StartSession is a no-op returning an empty StreamSession and nil error for non-session sinks.
func (BaseSink) StartSession(context.Context, time.Time, string, string) (*StreamSession, error) {
	return &StreamSession{}, nil
}

// AddSessionPlatform is a no-op returning nil error for non-session sinks.
func (BaseSink) AddSessionPlatform(context.Context, int64, string, string) error {
	return nil
}

// EndSession is a no-op returning nil error for non-session sinks.
func (BaseSink) EndSession(context.Context, int64, time.Time) error {
	return nil
}

// ListSessions is a no-op returning nil and nil error for non-session sinks.
func (BaseSink) ListSessions(context.Context, int) ([]StreamSession, error) {
	return nil, nil
}

// DistinctChannels is a no-op returning nil and nil error for non-queryable sinks.
func (BaseSink) DistinctChannels(context.Context, string) ([]string, error) {
	return nil, nil
}

// Prune is a no-op returning 0 and nil error for sinks without local retention storage.
func (BaseSink) Prune(context.Context, RetentionPolicy) (int64, error) {
	return 0, nil
}

// Close is a no-op returning nil error.
func (BaseSink) Close() error {
	return nil
}

// LiveUpdateKind distinguishes between newly appended entries and post-enqueue outcome updates.
type LiveUpdateKind string

const (
	LiveUpdateAppend  LiveUpdateKind = "append"
	LiveUpdateOutcome LiveUpdateKind = "outcome"
)

// LiveUpdate represents a real-time event pushed to Live Tail subscribers.
type LiveUpdate struct {
	Kind    LiveUpdateKind
	Entry   *Entry
	EventID string
	Update  OutcomeUpdate
}

// DefaultDBPath returns the standard on-disk SQLite event log path in the user's config directory.
func DefaultDBPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "logs", "events.db"), nil
}

// Manager coordinates one or more pluggable Backends, handles Live Tail pub/sub notifications,
// and executes periodic dynamic retention pruning.
type Manager struct {
	mu        sync.RWMutex
	backends  []Backend
	enabled   bool
	policy    RetentionPolicy
	closed    bool
	subNextID uint64
	subs      map[uint64]func(LiveUpdate)

	pruneStopCh chan struct{}
	pruneWg     sync.WaitGroup
}

// NewManager creates a Manager wrapping the provided backends.
// The first backend in the list is treated as the primary queryable store.
func NewManager(policy RetentionPolicy, backends ...Backend) *Manager {
	active := make([]Backend, 0, len(backends))
	for _, b := range backends {
		if b != nil {
			active = append(active, b)
		}
	}
	return &Manager{
		backends: active,
		enabled:  true,
		policy:   policy,
		subs:     make(map[uint64]func(LiveUpdate)),
	}
}

// SetEnabled enables or disables recording of new events.
func (m *Manager) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
}

// IsEnabled reports whether event logging is currently enabled.
func (m *Manager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// SetRetentionPolicy updates the active dynamic created_at retention policy.
func (m *Manager) SetRetentionPolicy(policy RetentionPolicy) {
	m.mu.Lock()
	m.policy = policy
	m.mu.Unlock()
}

// RetentionPolicy returns the currently active retention policy.
func (m *Manager) RetentionPolicy() RetentionPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// AddBackend registers an additional Backend (e.g., a webhook sink) at runtime.
func (m *Manager) AddBackend(b Backend) {
	if b == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.backends = append(m.backends, b)
}

// Subscribe registers a callback for real-time Live Tail updates and returns an unsubscribe function.
func (m *Manager) Subscribe(fn func(LiveUpdate)) func() {
	if fn == nil {
		return func() {}
	}
	m.mu.Lock()
	m.subNextID++
	id := m.subNextID
	m.subs[id] = fn
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

// StartAutoPrune launches a background goroutine that runs Prune immediately and at the given interval.
func (m *Manager) StartAutoPrune(interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	m.mu.Lock()
	if m.closed || m.pruneStopCh != nil {
		m.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	m.pruneStopCh = stopCh
	m.pruneWg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.pruneWg.Done()
		_, _ = m.PruneNow(context.Background())

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = m.PruneNow(context.Background())
			}
		}
	}()
}

// Append records a new Entry across all configured backends and notifies Live Tail subscribers.
func (m *Manager) Append(ctx context.Context, entry *Entry) error {
	if entry == nil {
		return errors.New("cannot append nil entry")
	}

	m.mu.RLock()
	if m.closed || !m.enabled {
		m.mu.RUnlock()
		return nil
	}
	backends := make([]Backend, len(m.backends))
	copy(backends, m.backends)
	subs := make([]func(LiveUpdate), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.mu.RUnlock()

	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = entry.CreatedAt
	}

	var firstErr error
	for _, b := range backends {
		if err := b.Append(ctx, entry); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if len(subs) > 0 {
		cloned := entry.Clone()
		ev := LiveUpdate{
			Kind:    LiveUpdateAppend,
			Entry:   cloned,
			EventID: cloned.ID,
		}
		for _, fn := range subs {
			fn(ev)
		}
	}

	return firstErr
}

// UpdateOutcome updates the playback/queue outcome of an existing Entry across all backends
// and broadcasts the transition to Live Tail subscribers.
func (m *Manager) UpdateOutcome(ctx context.Context, id string, update OutcomeUpdate) error {
	if id == "" {
		return nil
	}

	m.mu.RLock()
	if m.closed || !m.enabled {
		m.mu.RUnlock()
		return nil
	}
	backends := make([]Backend, len(m.backends))
	copy(backends, m.backends)
	subs := make([]func(LiveUpdate), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.mu.RUnlock()

	if update.UpdatedAt.IsZero() {
		update.UpdatedAt = time.Now().UTC()
	}

	var firstErr error
	for _, b := range backends {
		if err := b.UpdateOutcome(ctx, id, update); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if len(subs) > 0 {
		ev := LiveUpdate{
			Kind:    LiveUpdateOutcome,
			EventID: id,
			Update:  update,
		}
		for _, fn := range subs {
			fn(ev)
		}
	}

	return firstErr
}

// Query executes a filtered search against the primary queryable backend, applying the active retention policy.
func (m *Manager) Query(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return &QueryResult{}, nil
	}
	primary := m.backends[0]
	policy := m.policy
	m.mu.RUnlock()

	if filter.Retention.ChatRetentionDays == 0 && filter.Retention.EventRetentionDays == 0 {
		filter.Retention = policy
	}

	return primary.Query(ctx, filter)
}

// Summarize computes aggregated stream event and monetization metrics across all rows matching filter.
func (m *Manager) Summarize(ctx context.Context, filter QueryFilter) (*QuerySummary, error) {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return &QuerySummary{}, nil
	}
	primary := m.backends[0]
	policy := m.policy
	m.mu.RUnlock()

	if filter.Retention.ChatRetentionDays == 0 && filter.Retention.EventRetentionDays == 0 {
		filter.Retention = policy
	}

	return primary.Summarize(ctx, filter)
}

// StartSession starts a new StreamSession in the primary backend.
func (m *Manager) StartSession(ctx context.Context, startedAt time.Time, platform, channel string) (*StreamSession, error) {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return &StreamSession{}, nil
	}
	primary := m.backends[0]
	m.mu.RUnlock()

	return primary.StartSession(ctx, startedAt, platform, channel)
}

// AddSessionPlatform merges an active platform and channel into an open StreamSession.
func (m *Manager) AddSessionPlatform(ctx context.Context, sessionID int64, platform, channel string) error {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return nil
	}
	primary := m.backends[0]
	m.mu.RUnlock()

	return primary.AddSessionPlatform(ctx, sessionID, platform, channel)
}

// EndSession marks a StreamSession as ended at endedAt.
func (m *Manager) EndSession(ctx context.Context, sessionID int64, endedAt time.Time) error {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return nil
	}
	primary := m.backends[0]
	m.mu.RUnlock()

	return primary.EndSession(ctx, sessionID, endedAt)
}

// ListSessions returns the most recent stream sessions ordered newest-first.
func (m *Manager) ListSessions(ctx context.Context, limit int) ([]StreamSession, error) {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return nil, nil
	}
	primary := m.backends[0]
	m.mu.RUnlock()

	return primary.ListSessions(ctx, limit)
}

// DistinctChannels returns the sorted list of distinct channel names recorded in the primary backend.
func (m *Manager) DistinctChannels(ctx context.Context, platform string) ([]string, error) {
	m.mu.RLock()
	if m.closed || len(m.backends) == 0 {
		m.mu.RUnlock()
		return nil, nil
	}
	primary := m.backends[0]
	m.mu.RUnlock()

	return primary.DistinctChannels(ctx, platform)
}

// PruneNow executes retention pruning across all backends using the Manager's current RetentionPolicy.
func (m *Manager) PruneNow(ctx context.Context) (int64, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return 0, nil
	}
	backends := make([]Backend, len(m.backends))
	copy(backends, m.backends)
	policy := m.policy
	m.mu.RUnlock()

	var totalDeleted int64
	var firstErr error
	for _, b := range backends {
		n, err := b.Prune(ctx, policy)
		totalDeleted += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return totalDeleted, firstErr
}

// Close stops background pruning and closes all registered backends.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	stopCh := m.pruneStopCh
	m.pruneStopCh = nil
	backends := m.backends
	m.backends = nil
	m.subs = make(map[uint64]func(LiveUpdate))
	m.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
		m.pruneWg.Wait()
	}

	var firstErr error
	for _, b := range backends {
		if err := b.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
