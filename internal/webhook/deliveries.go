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

	mu   sync.Mutex
	last map[types.NamespacedName]time.Time
}

// NewDeliveries returns an empty record that takes the time from clk.
func NewDeliveries(clk clock.PassiveClock) *Deliveries {
	return &Deliveries{Clock: clk, last: map[types.NamespacedName]time.Time{}}
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
