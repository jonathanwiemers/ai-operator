package webhook

import (
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Waker turns wakes into events for the AgentProject controller: at most one
// per project per Interval. A wake inside the window schedules exactly one
// trailing event at the window's end, so the last change is never missed.
type Waker struct {
	Interval time.Duration
	Clock    clock.WithDelayedExecution

	events chan event.GenericEvent
	mu     sync.Mutex
	last   map[types.NamespacedName]time.Time // when the last event was sent
	queued map[types.NamespacedName]bool      // a trailing event is scheduled
}

// NewWaker returns a Waker whose Events channel buffers up to buffer events.
func NewWaker(interval time.Duration, buffer int, clk clock.WithDelayedExecution) *Waker {
	return &Waker{
		Interval: interval,
		Clock:    clk,
		events:   make(chan event.GenericEvent, buffer),
		last:     map[types.NamespacedName]time.Time{},
		queued:   map[types.NamespacedName]bool{},
	}
}

// Events is the channel the AgentProject controller watches.
func (w *Waker) Events() <-chan event.GenericEvent { return w.events }

// Wake asks for a sync of the AgentProject n. It never blocks.
func (w *Waker) Wake(n types.NamespacedName) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.queued[n] {
		return
	}
	now := w.Clock.Now()
	last, ok := w.last[n]
	if !ok || now.Sub(last) >= w.Interval {
		w.last[n] = now
		w.send(n)
		return
	}
	at := last.Add(w.Interval)
	w.queued[n] = true
	w.Clock.AfterFunc(at.Sub(now), func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.queued, n)
		w.last[n] = at
		w.send(n)
	})
}

// send hands the event to the controller without blocking. When the buffer
// is full, the controller is behind and syncs are already queued, so the
// event is dropped.
func (w *Waker) send(n types.NamespacedName) {
	select {
	case w.events <- event.GenericEvent{Object: &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: n.Namespace, Name: n.Name}}}:
	default:
	}
}
