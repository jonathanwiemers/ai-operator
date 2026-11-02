// Package history appends what happens in the factory to the research
// database: transitions of the AgentTasks' status and the GitLab comments
// the operator reads. The UI shows them as the projects' streams. Recording
// never blocks or fails a reconcile: a full queue or a database outage
// loses events.
package history

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const (
	queueSize  = 2000
	batchSize  = 200
	flushEvery = time.Second
	// DefaultRetention is how long events are kept by default.
	DefaultRetention = 365 * 24 * time.Hour
)

// Database is the research database setup: the store once it is migrated.
type Database interface {
	Ready() <-chan struct{}
	Store() *store.Store
}

// Recorder writes events in batches. It is a leader-only runnable: other
// replicas drop what they record, the leader sees the same changes.
type Recorder struct {
	db        Database
	retention time.Duration
	clock     clock.Clock
	log       logr.Logger
	queue     chan store.Event
	started   atomic.Bool
	dropped   atomic.Int64
	notes     *seenNotes
}

// NewRecorder returns a recorder that keeps events for retention.
func NewRecorder(db Database, retention time.Duration, clk clock.Clock, log logr.Logger) *Recorder {
	return &Recorder{db: db, retention: retention, clock: clk, log: log,
		queue: make(chan store.Event, queueSize), notes: newSeenNotes()}
}

// NeedLeaderElection makes it a leader-only runnable.
func (r *Recorder) NeedLeaderElection() bool { return true }

// Record queues e. It never blocks: without room the event is dropped.
func (r *Recorder) Record(e store.Event) {
	if r == nil || !r.started.Load() {
		return
	}
	if e.At.IsZero() {
		e.At = r.clock.Now()
	}
	e.ExpiresAt = e.At.Add(r.retention)
	select {
	case r.queue <- e:
	default:
		r.dropped.Add(1)
	}
}

// Start writes the queued events until ctx ends, once the database is ready.
func (r *Recorder) Start(ctx context.Context) error {
	r.started.Store(true)
	defer r.started.Store(false)
	select {
	case <-ctx.Done():
		return nil
	case <-r.db.Ready():
	}
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	batch := make([]store.Event, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if _, err := r.db.Store().InsertEvents(ctx, batch); err != nil && ctx.Err() == nil {
			r.log.Error(err, "history events lost", "events", len(batch))
		}
		batch = batch[:0]
		if n := r.dropped.Swap(0); n > 0 {
			r.log.Info("history queue full, events dropped", "events", n)
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case e := <-r.queue:
			batch = append(batch, e)
			if len(batch) == batchSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
