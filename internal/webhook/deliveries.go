package webhook

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
)

// Deliveries records when the last valid delivery for each AgentProject
// arrived. It lives in memory only: after a restart it is empty, and the
// projects are polled fully until their next delivery.
//
// A heartbeat (a test delivery the operator asked GitLab for) proves the
// hook works but tells of no change: it counts for Last, not for LastEvent.
type Deliveries struct {
	Clock clock.PassiveClock

	started time.Time
	mu      sync.Mutex
	last    map[types.NamespacedName]time.Time
	event   map[types.NamespacedName]time.Time
	expect  map[types.NamespacedName]time.Time
	notes   map[types.NamespacedName]map[int64]time.Time
}

// HeartbeatWindow is how long after ExpectHeartbeat the next delivery is
// taken for the heartbeat.
const HeartbeatWindow = time.Minute

// MaxNotes is how many issues' note times are kept per AgentProject; a new
// issue drops the one with the oldest note.
const MaxNotes = 100

// NewDeliveries returns an empty record that takes the time from clk.
func NewDeliveries(clk clock.PassiveClock) *Deliveries {
	return &Deliveries{Clock: clk, started: clk.Now(), last: map[types.NamespacedName]time.Time{}, event: map[types.NamespacedName]time.Time{},
		expect: map[types.NamespacedName]time.Time{}, notes: map[types.NamespacedName]map[int64]time.Time{}}
}

// Record notes a valid delivery that tells of a change for the AgentProject
// n now.
func (d *Deliveries) Record(n types.NamespacedName) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Clock.Now()
	d.last[n], d.event[n] = now, now
}

// RecordHook notes a valid hook delivery for the AgentProject n now and
// reports whether it was the heartbeat ExpectHeartbeat announced: the first
// delivery within HeartbeatWindow of it. Any other delivery is an event.
func (d *Deliveries) RecordHook(n types.NamespacedName) (heartbeat bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Clock.Now()
	d.last[n] = now
	if t, ok := d.expect[n]; ok {
		delete(d.expect, n)
		if now.Sub(t) < HeartbeatWindow {
			return true
		}
	}
	d.event[n] = now
	return false
}

// Started returns when the record was made: the operator's start.
func (d *Deliveries) Started() time.Time { return d.started }

// ExpectHeartbeat announces a test delivery for the AgentProject n, which
// the operator is about to ask GitLab for.
func (d *Deliveries) ExpectHeartbeat(n types.NamespacedName) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expect[n] = d.Clock.Now()
}

// LastEvent returns when the last delivery that told of a change (not a
// heartbeat) for the AgentProject n arrived, and false when none did since
// the operator started.
func (d *Deliveries) LastEvent(n types.NamespacedName) (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.event[n]
	return t, ok
}

// Last returns when the last valid delivery for the AgentProject n arrived,
// and false when none did since the operator started.
func (d *Deliveries) Last(n types.NamespacedName) (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.last[n]
	return t, ok
}

// RecordNote notes a note event on the issue issueIID of the AgentProject n
// now.
func (d *Deliveries) RecordNote(n types.NamespacedName, issueIID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.notes[n]
	if m == nil {
		m = map[int64]time.Time{}
		d.notes[n] = m
	}
	if _, ok := m[issueIID]; !ok && len(m) >= MaxNotes {
		oldest := int64(-1)
		for iid, t := range m {
			if oldest < 0 || t.Before(m[oldest]) {
				oldest = iid
			}
		}
		delete(m, oldest)
	}
	m[issueIID] = d.Clock.Now()
}

// NoteAt returns when the last note event on the issue issueIID of the
// AgentProject n arrived, and false when none is kept.
func (d *Deliveries) NoteAt(n types.NamespacedName, issueIID int64) (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.notes[n][issueIID]
	return t, ok
}
