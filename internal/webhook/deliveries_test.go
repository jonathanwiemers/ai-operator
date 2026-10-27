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
