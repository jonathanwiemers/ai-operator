package controller

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakeRequirements records SetMilestoneRequirements calls.
type fakeRequirements struct {
	mu     sync.Mutex
	writes []int64 // milestone IIDs, in order
	counts map[int64]int
	err    error
}

func (f *fakeRequirements) SetMilestoneRequirements(_ context.Context, key store.ProjectKey, iid int64, n int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key != (store.ProjectKey{Namespace: ns, Name: "shop"}) {
		return false, errors.New("wrong project")
	}
	f.writes = append(f.writes, iid)
	if f.err != nil {
		return false, f.err
	}
	if f.counts == nil {
		f.counts = map[int64]int{}
	}
	f.counts[iid] = n
	return true, nil
}

func (f *fakeRequirements) take() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.writes
	f.writes = nil
	return w
}

func requirementsEnv(t *testing.T) (*projectEnv, *fakeRequirements) {
	e := newProjectEnv(t)
	st := &fakeRequirements{}
	e.r.Requirements = st
	e.gl.Milestones = []gitlab.Milestone{
		{ID: 11, IID: 3, Title: "Checkout", State: "active", Description: "/agent spec\n- **R1** cart\n- **R2** pay"},
		{ID: 12, IID: 4, Title: "Unplanned", State: "active", Description: "no marker\n- R1 x"},
		{ID: 13, IID: 2, Title: "Old", State: "closed", Description: "/agent spec\nR1 a\nR2 b\nR3 c"},
	}
	return e, st
}

// Open planned milestones are written once their spec task exists, and
// again only when the count changes; the newest closed milestones once per
// start.
func TestRecordRequirements(t *testing.T) {
	e, st := requirementsEnv(t)
	e.reconcile() // plans milestone 3; the sweep covers the closed one
	if got := st.take(); len(got) != 1 || got[0] != 2 || st.counts[2] != 3 {
		t.Fatalf("first poll wrote %v (counts %v), want closed milestone 2 with 3", got, st.counts)
	}
	e.reconcile()
	if got := st.take(); len(got) != 1 || got[0] != 3 || st.counts[3] != 2 {
		t.Fatalf("second poll wrote %v (counts %v), want open milestone 3 with 2", got, st.counts)
	}
	e.reconcile()
	if got := st.take(); len(got) != 0 {
		t.Fatalf("unchanged poll wrote %v, want nothing", got)
	}
	e.gl.Milestones[0].Description += "\n- **R3** ship"
	e.reconcile()
	e.reconcile()
	if got := st.take(); len(got) != 1 || got[0] != 3 || st.counts[3] != 3 {
		t.Fatalf("after the edit wrote %v (counts %v), want milestone 3 with 3", got, st.counts)
	}
}

// A store error neither fails the reconcile nor counts as written.
func TestRecordRequirementsStoreError(t *testing.T) {
	e, st := requirementsEnv(t)
	st.err = errors.New("database down")
	e.reconcile() // fails the test on a reconcile error
	e.reconcile()
	if got := st.take(); len(got) != 2 {
		t.Fatalf("writes %v, want the closed sweep and milestone 3 tried", got)
	}
	st.err = nil
	e.reconcile()
	if got := st.take(); len(got) != 1 || got[0] != 3 {
		t.Fatalf("after recovery wrote %v, want milestone 3 again (the sweep runs once)", got)
	}
}
