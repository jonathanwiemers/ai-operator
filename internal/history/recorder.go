// Package history appends what happens in the factory to the research
// database: transitions of the AgentTasks' status, the GitLab comments the
// operator reads, and every agent call of the tasks. The UI shows them as
// the projects' streams and spend. Recording never blocks or fails a
// reconcile: a full queue or a database outage loses events.
package history

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"

	"github.com/jonathanwiemers/ai-operator/internal/pricing"
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

// Recorder writes events and agent calls in batches. It is a leader-only
// runnable: other replicas drop what they record, the leader sees the same
// changes.
type Recorder struct {
	db           Database
	retention    time.Duration
	clock        clock.Clock
	log          logr.Logger
	queue        chan store.Event
	calls        chan store.Call
	started      atomic.Bool
	dropped      atomic.Int64
	droppedCalls atomic.Int64
	notes        *seenNotes
	// existing returns the calls of every task, written when the recorder
	// starts: runners append calls while no leader records (Watch sets it).
	existing func(context.Context) ([]store.Call, error)
	// snaps is the snapshot queue: the newest snapshot (and close) per
	// milestone, written with the events.
	snapMu       sync.Mutex
	snaps        map[snapKey]snapOp
	droppedSnaps atomic.Int64
	// existingSnaps returns the snapshots of every spec task, queued when
	// the recorder starts (Watch sets it).
	existingSnaps func(context.Context) ([]store.MilestoneSnapshot, error)
	// Pricing estimates the cost of calls the engine reported none for;
	// nil estimates none.
	Pricing *pricing.Table
}

// NewRecorder returns a recorder that keeps events and calls for retention.
func NewRecorder(db Database, retention time.Duration, clk clock.Clock, log logr.Logger) *Recorder {
	return &Recorder{db: db, retention: retention, clock: clk, log: log,
		queue: make(chan store.Event, queueSize), calls: make(chan store.Call, queueSize), notes: newSeenNotes(),
		snaps: map[snapKey]snapOp{}}
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

// RecordCall queues c. It never blocks: without room the call is dropped.
func (r *Recorder) RecordCall(c store.Call) {
	if r == nil || !r.started.Load() {
		return
	}
	r.prepare(&c)
	select {
	case r.calls <- c:
	default:
		r.droppedCalls.Add(1)
	}
}

// prepare sets the call's expiry and estimates its cost when unknown.
func (r *Recorder) prepare(c *store.Call) {
	if c.At.IsZero() {
		c.At = r.clock.Now()
	}
	c.ExpiresAt = c.At.Add(r.retention)
	Estimate(c, r.Pricing)
}

// Start writes the queued events, calls and snapshots until ctx ends, once the
// database is ready. It first writes the calls of the existing tasks and
// queues the snapshots of the existing spec tasks.
func (r *Recorder) Start(ctx context.Context) error {
	r.started.Store(true)
	defer r.started.Store(false)
	select {
	case <-ctx.Done():
		return nil
	case <-r.db.Ready():
	}
	r.writeExisting(ctx)
	r.queueExisting(ctx)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	batch := make([]store.Event, 0, batchSize)
	calls := make([]store.Call, 0, batchSize)
	flush := func() {
		if len(batch) > 0 {
			if _, err := r.db.Store().InsertEvents(ctx, batch); err != nil && ctx.Err() == nil {
				r.log.Error(err, "history events lost", "events", len(batch))
			}
			batch = batch[:0]
		}
		if len(calls) > 0 {
			if _, err := r.db.Store().InsertCalls(ctx, calls); err != nil && ctx.Err() == nil {
				r.log.Error(err, "agent calls lost", "calls", len(calls))
			}
			calls = calls[:0]
		}
		r.writeSnapshots(ctx)
		if n := r.dropped.Swap(0); n > 0 {
			r.log.Info("history queue full, events dropped", "events", n)
		}
		if n := r.droppedCalls.Swap(0); n > 0 {
			r.log.Info("history queue full, agent calls dropped", "calls", n)
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
		case c := <-r.calls:
			calls = append(calls, c)
			if len(calls) == batchSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

// writeExisting writes the calls existing returns, in batches; those
// already stored are skipped by their key.
func (r *Recorder) writeExisting(ctx context.Context) {
	if r.existing == nil {
		return
	}
	calls, err := r.existing(ctx)
	if err != nil {
		r.log.Error(err, "listing the agent calls of existing tasks")
		return
	}
	var written int64
	for chunk := range slices.Chunk(calls, batchSize) {
		for i := range chunk {
			r.prepare(&chunk[i])
		}
		n, err := r.db.Store().InsertCalls(ctx, chunk)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error(err, "agent calls of existing tasks lost", "calls", len(calls))
			}
			return
		}
		written += n
	}
	if written > 0 {
		r.log.Info("wrote agent calls of existing tasks", "calls", written)
	}
}
