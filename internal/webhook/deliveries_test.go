package webhook

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestDeliveriesRecordsLastPerProject(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	d := NewDeliveries(clk)
	other := types.NamespacedName{Namespace: "team", Name: "other"}
	if _, ok := d.Last(app); ok {
		t.Fatal("an empty record has no delivery")
	}
	d.Record(app)
	first := clk.Now()
	clk.Step(time.Minute)
	d.Record(other)
	clk.Step(time.Minute)
	d.Record(app)
	if got, ok := d.Last(app); !ok || !got.Equal(first.Add(2*time.Minute)) {
		t.Errorf("Last(app) = %v, %v, want the later delivery", got, ok)
	}
	if got, ok := d.Last(other); !ok || !got.Equal(first.Add(time.Minute)) {
		t.Errorf("Last(other) = %v, %v, want its own delivery", got, ok)
	}
}

// TestDeliveriesConcurrent is meant for -race: the receiver records while
// the controller reads.
func TestDeliveriesConcurrent(t *testing.T) {
	d := NewDeliveries(clocktesting.NewFakeClock(time.Now()))
	var wg sync.WaitGroup
	for i := range 8 {
		n := types.NamespacedName{Namespace: "team", Name: fmt.Sprintf("p%d", i%3)}
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				d.Record(n)
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				d.Last(n)
			}
		}()
	}
	wg.Wait()
	for i := range 3 {
		if _, ok := d.Last(types.NamespacedName{Namespace: "team", Name: fmt.Sprintf("p%d", i)}); !ok {
			t.Errorf("p%d has no delivery", i)
		}
	}
}

func TestDeliveriesRecordsNotesPerIssue(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	d := NewDeliveries(clk)
	other := types.NamespacedName{Namespace: "team", Name: "other"}
	d.RecordNote(app, 7)
	first := clk.Now()
	clk.Step(time.Minute)
	d.RecordNote(app, 8)
	if got, ok := d.NoteAt(app, 7); !ok || !got.Equal(first) {
		t.Errorf("NoteAt(app, 7) = %v, %v, want its note", got, ok)
	}
	if got, ok := d.NoteAt(app, 8); !ok || !got.Equal(first.Add(time.Minute)) {
		t.Errorf("NoteAt(app, 8) = %v, %v, want its note", got, ok)
	}
	if _, ok := d.NoteAt(app, 9); ok {
		t.Error("an issue without a note has none")
	}
	if _, ok := d.NoteAt(other, 7); ok {
		t.Error("notes are kept per project")
	}
	if _, ok := d.Last(app); ok {
		t.Error("RecordNote is not a delivery of its own")
	}
}

func TestDeliveriesKeepTheNewestNotes(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	d := NewDeliveries(clk)
	for iid := range int64(MaxNotes) {
		d.RecordNote(app, iid+1)
		clk.Step(time.Second)
	}
	d.RecordNote(app, 1) // issue 1 is the newest again: 2 is the oldest
	clk.Step(time.Second)
	d.RecordNote(app, MaxNotes+1)
	if _, ok := d.NoteAt(app, 2); ok {
		t.Error("the oldest note is dropped for a new issue")
	}
	for _, iid := range []int64{1, 3, MaxNotes, MaxNotes + 1} {
		if _, ok := d.NoteAt(app, iid); !ok {
			t.Errorf("the note on #%d is kept", iid)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := len(d.notes[app]); n != MaxNotes {
		t.Errorf("%d notes kept, want %d", n, MaxNotes)
	}
}

func TestDeliveriesHeartbeatIsTheFirstDeliveryWithinTheWindow(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	d := NewDeliveries(clk)
	if !d.Started().Equal(clk.Now()) {
		t.Fatalf("Started() = %v", d.Started())
	}
	d.ExpectHeartbeat(app)
	clk.Step(10 * time.Second)
	if !d.RecordHook(app) {
		t.Fatal("the announced delivery is not a heartbeat")
	}
	if _, ok := d.LastEvent(app); ok {
		t.Fatal("a heartbeat is an event")
	}
	if got, ok := d.Last(app); !ok || !got.Equal(clk.Now()) {
		t.Fatalf("Last(app) = %v, %v: a heartbeat shows the hook works", got, ok)
	}
	// The second delivery in the window is a real one.
	if d.RecordHook(app) {
		t.Fatal("a second delivery is a heartbeat")
	}
	if got, ok := d.LastEvent(app); !ok || !got.Equal(clk.Now()) {
		t.Fatalf("LastEvent(app) = %v, %v", got, ok)
	}
	// A heartbeat that does not arrive within the window: the next delivery
	// is an event.
	d.ExpectHeartbeat(app)
	clk.Step(HeartbeatWindow)
	if d.RecordHook(app) {
		t.Fatal("a delivery after the window is a heartbeat")
	}
	// Record is always an event.
	d.ExpectHeartbeat(app)
	clk.Step(time.Second)
	d.Record(app)
	if got, _ := d.LastEvent(app); !got.Equal(clk.Now()) {
		t.Fatalf("LastEvent(app) = %v", got)
	}
}
