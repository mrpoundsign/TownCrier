package bot

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mrpoundsign/TownCrier/pkg/config"
)

// QueueStat summarizes runtime activity for a named bot execution queue.
type QueueStat struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Blocking bool   `json:"blocking"`
	Pending  int    `json:"pending"`
	Running  int    `json:"running"`
}

type queueJob struct {
	fn func(ctx context.Context)
}

type execQueue struct {
	id         string
	name       string
	blocking   bool
	maxPending int

	mu       sync.Mutex
	gen      uint64
	runCtx   context.Context
	cancel   context.CancelFunc
	jobs     chan queueJob
	stopOnce sync.Once
	stopCh   chan struct{}
	running  atomic.Int32
}

type queueManager struct {
	mu     sync.RWMutex
	queues map[string]*execQueue
	closed bool
}

func newQueueManager(defs []config.BotQueueDef) *queueManager {
	qm := &queueManager{
		queues: make(map[string]*execQueue),
	}
	qm.Sync(defs)
	return qm
}

// Sync aligns running queue workers with configured BotQueueDef definitions.
func (qm *queueManager) Sync(defs []config.BotQueueDef) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	if qm.closed {
		return
	}

	desired := make(map[string]config.BotQueueDef, len(defs)+2)
	for _, d := range defs {
		id := strings.ToLower(strings.TrimSpace(d.ID))
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(d.Name))
		}
		if id == "" {
			continue
		}
		d.ID = id
		if d.MaxPending <= 0 {
			d.MaxPending = 64
		}
		desired[id] = d
	}
	if _, ok := desired["default"]; !ok {
		desired["default"] = config.BotQueueDef{ID: "default", Name: "Default (Blocking)", Blocking: true, MaxPending: 64}
	}
	if _, ok := desired["concurrent"]; !ok {
		desired["concurrent"] = config.BotQueueDef{ID: "concurrent", Name: "Concurrent (Instant)", Blocking: false, MaxPending: 128}
	}

	// Stop removed or mode-changed queues
	for id, q := range qm.queues {
		want, ok := desired[id]
		if !ok || q.blocking != want.Blocking {
			q.closeQueue()
			delete(qm.queues, id)
		}
	}

	for id, want := range desired {
		if q, ok := qm.queues[id]; ok {
			q.mu.Lock()
			q.name = want.Name
			q.maxPending = want.MaxPending
			q.mu.Unlock()
			continue
		}
		qm.queues[id] = startExecQueue(want)
	}
}

func startExecQueue(def config.BotQueueDef) *execQueue {
	capSize := def.MaxPending
	if capSize <= 0 {
		capSize = 64
	}
	runCtx, cancel := context.WithCancel(context.Background())
	q := &execQueue{
		id:         def.ID,
		name:       def.Name,
		blocking:   def.Blocking,
		maxPending: capSize,
		runCtx:     runCtx,
		cancel:     cancel,
		jobs:       make(chan queueJob, capSize),
		stopCh:     make(chan struct{}),
	}
	go q.loop()
	return q
}

func (q *execQueue) loop() {
	for {
		select {
		case <-q.stopCh:
			return
		case job := <-q.jobs:
			if job.fn == nil {
				continue
			}
			q.mu.Lock()
			ctx := q.runCtx
			q.mu.Unlock()
			if ctx == nil || ctx.Err() != nil {
				continue
			}
			if q.blocking {
				q.running.Add(1)
				job.fn(ctx)
				q.running.Add(-1)
			} else {
				q.running.Add(1)
				fn := job.fn
				go func() {
					defer q.running.Add(-1)
					fn(ctx)
				}()
			}
		}
	}
}

func (q *execQueue) enqueue(fn func(ctx context.Context)) bool {
	if fn == nil {
		return false
	}
	select {
	case <-q.stopCh:
		return false
	default:
	}
	select {
	case q.jobs <- queueJob{fn: fn}:
		return true
	default:
		return false
	}
}

func (q *execQueue) clear() {
	q.mu.Lock()
	if q.cancel != nil {
		q.cancel()
	}
	q.gen++
	q.runCtx, q.cancel = context.WithCancel(context.Background())
drainLoop:
	for {
		select {
		case <-q.jobs:
		default:
			break drainLoop
		}
	}
	q.mu.Unlock()
}

func (q *execQueue) closeQueue() {
	q.stopOnce.Do(func() {
		close(q.stopCh)
	})
	q.mu.Lock()
	if q.cancel != nil {
		q.cancel()
	}
	q.mu.Unlock()
}

// Enqueue schedules fn on queueID (defaulting to "default" when empty or missing).
func (qm *queueManager) Enqueue(queueID string, fn func(ctx context.Context)) bool {
	target := strings.ToLower(strings.TrimSpace(queueID))
	if target == "" {
		target = "default"
	}

	qm.mu.RLock()
	if qm.closed {
		qm.mu.RUnlock()
		return false
	}
	q := qm.queues[target]
	if q == nil {
		q = qm.queues["default"]
	}
	qm.mu.RUnlock()
	if q == nil {
		return false
	}
	return q.enqueue(fn)
}

// ClearQueues cancels currently executing queue jobs and drains pending work (used on Panic Mute or manual clear).
func (qm *queueManager) ClearQueues() {
	qm.mu.RLock()
	for _, q := range qm.queues {
		q.clear()
	}
	qm.mu.RUnlock()
}

// Stats returns a snapshot of queue counts.
func (qm *queueManager) Stats() []QueueStat {
	qm.mu.RLock()
	out := make([]QueueStat, 0, len(qm.queues))
	for _, q := range qm.queues {
		q.mu.Lock()
		st := QueueStat{
			ID:       q.id,
			Name:     q.name,
			Blocking: q.blocking,
			Pending:  len(q.jobs),
			Running:  int(q.running.Load()),
		}
		q.mu.Unlock()
		out = append(out, st)
	}
	qm.mu.RUnlock()
	return out
}

// Close shuts down all queue workers.
func (qm *queueManager) Close() {
	qm.mu.Lock()
	if qm.closed {
		qm.mu.Unlock()
		return
	}
	qm.closed = true
	qs := qm.queues
	qm.queues = make(map[string]*execQueue)
	qm.mu.Unlock()

	for _, q := range qs {
		q.closeQueue()
	}
}
