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
type Deliveries struct {
	Clock clock.PassiveClock

	mu    sync.Mutex
	last  map[types.NamespacedName]time.Time
	notes map[types.NamespacedName]map[int64]time.Time
}

// MaxNotes is how many issues' note times are kept per AgentProject; a new
// issue drops the one with the oldest note.
const MaxNotes = 100

// NewDeliveries returns an empty record that takes the time from clk.
func NewDeliveries(clk clock.PassiveClock) *Deliveries {
	return &Deliveries{Clock: clk, last: map[types.NamespacedName]time.Time{}, notes: map[types.NamespacedName]map[int64]time.Time{}}
}

// Record notes a valid delivery for the AgentProject n now.
func (d *Deliveries) Record(n types.NamespacedName) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.last[n] = d.Clock.Now()
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
