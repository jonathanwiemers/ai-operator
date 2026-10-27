package webhook

import (
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
)

func newWaker(buffer int) (*Waker, *clocktesting.FakeClock) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	return NewWaker(5*time.Second, buffer, clk), clk
}

// drain returns the projects of the events waiting on the channel.
func drain(w *Waker) []types.NamespacedName {
	var got []types.NamespacedName
	for {
		select {
		case e := <-w.Events():
			got = append(got, types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()})
		default:
			return got
		}
	}
}

func expectEvents(t *testing.T, w *Waker, want ...types.NamespacedName) {
	t.Helper()
	if got := drain(w); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestWakerLeadingWake(t *testing.T) {
	w, clk := newWaker(10)
	w.Wake(app)
	expectEvents(t, w, app)

	// After the window, the next wake is a leading one again.
	clk.Step(5 * time.Second)
	expectEvents(t, w)
	w.Wake(app)
	expectEvents(t, w, app)
}

func TestWakerCoalescesBurstIntoTrailingWake(t *testing.T) {
	w, clk := newWaker(10)
	// 50 deliveries in 2 s: one event at once, one at the window's end.
	for range 50 {
		w.Wake(app)
		clk.Step(40 * time.Millisecond)
	}
	expectEvents(t, w, app)
	clk.Step(5*time.Second - 2*time.Second - time.Millisecond)
	expectEvents(t, w)
	clk.Step(time.Millisecond)
	expectEvents(t, w, app)
	clk.Step(time.Minute)
	expectEvents(t, w)
}

func TestWakerTrailingWakeStartsNextWindow(t *testing.T) {
	w, clk := newWaker(10)
	w.Wake(app)
	clk.Step(time.Second)
	w.Wake(app)
	clk.Step(4 * time.Second) // the trailing event at 5 s
	expectEvents(t, w, app, app)

	// A wake 1 s after the trailing event waits for the end of its window.
	clk.Step(time.Second)
	w.Wake(app)
	expectEvents(t, w)
	clk.Step(4 * time.Second)
	expectEvents(t, w, app)
}

func TestWakerProjectsAreIndependent(t *testing.T) {
	w, clk := newWaker(10)
	other := types.NamespacedName{Namespace: "team", Name: "other"}
	w.Wake(app)
	w.Wake(app)
	w.Wake(other)
	expectEvents(t, w, app, other)
	clk.Step(5 * time.Second)
	expectEvents(t, w, app)
}

func TestWakerFullBufferDoesNotBlock(t *testing.T) {
	w, clk := newWaker(1)
	other := types.NamespacedName{Namespace: "team", Name: "other"}
	done := make(chan struct{})
	go func() {
		w.Wake(app)
		w.Wake(other) // buffer full: dropped
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wake blocked on a full buffer")
	}
	expectEvents(t, w, app)

	// A dropped event still counts for the window; the next wake is trailing.
	w.Wake(other)
	expectEvents(t, w)
	clk.Step(5 * time.Second)
	expectEvents(t, w, other)
}
