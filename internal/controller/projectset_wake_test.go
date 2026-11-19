package controller

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
)

func TestMilestonesHash(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	a := gitlab.DiscoveredMilestone{IID: 3, Title: "Checkout", Description: "/agent spec", UpdatedAt: at}
	b := gitlab.DiscoveredMilestone{IID: 5, Title: "Search", UpdatedAt: at}
	base := milestonesHash([]gitlab.DiscoveredMilestone{a, b})
	if base == "" || base == milestonesHash(nil) {
		t.Fatalf("hash = %q", base)
	}
	if got := milestonesHash([]gitlab.DiscoveredMilestone{b, a}); got != base {
		t.Error("the order of the milestones changes the hash")
	}
	desc := a
	desc.Description = "other"
	if got := milestonesHash([]gitlab.DiscoveredMilestone{desc, b}); got != base {
		t.Error("the description changes the hash: its edit bumps updatedAt")
	}
	at2 := at.In(time.FixedZone("CET", 3600))
	zone := a
	zone.UpdatedAt = at2
	if got := milestonesHash([]gitlab.DiscoveredMilestone{zone, b}); got != base {
		t.Error("the time zone changes the hash")
	}
	for name, edit := range map[string]func(m *gitlab.DiscoveredMilestone){
		"title":     func(m *gitlab.DiscoveredMilestone) { m.Title = "Checkout v2" },
		"updatedAt": func(m *gitlab.DiscoveredMilestone) { m.UpdatedAt = m.UpdatedAt.Add(time.Second) },
		"iid":       func(m *gitlab.DiscoveredMilestone) { m.IID = 4 },
	} {
		m := a
		edit(&m)
		if got := milestonesHash([]gitlab.DiscoveredMilestone{m, b}); got == base {
			t.Errorf("a changed %s keeps the hash", name)
		}
	}
	if got := milestonesHash([]gitlab.DiscoveredMilestone{a}); got == base {
		t.Error("a closed milestone keeps the hash")
	}
}

// wakeEnv is a set env with one active project, "jo.wiemers/shop", whose
// child "shop" the set wakes through the test's delivery store.
type wakeEnv struct {
	*setEnv
	deliveries *webhook.Deliveries
	woken      []types.NamespacedName
}

var shopChild = types.NamespacedName{Namespace: "agents", Name: "shop"}

func newWakeEnv(t *testing.T) *wakeEnv {
	t.Helper()
	e := &wakeEnv{setEnv: newSetEnv(t, nil)}
	e.deliveries = webhook.NewDeliveries(clocktesting.NewFakeClock(planNow))
	e.r.Deliveries = e.deliveries
	e.r.Wake = func(n types.NamespacedName) { e.woken = append(e.woken, n) }
	shop := activeProject("jo.wiemers/shop")
	shop.Milestones[0].UpdatedAt = planNow.Add(-time.Hour)
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{shop, {FullPath: "jo.wiemers/idle"}}}
	return e
}

// poll runs the set's next poll, one pollInterval later.
func (e *wakeEnv) poll() {
	e.t.Helper()
	e.now = e.now.Add(2 * time.Minute)
	e.reconcile()
}

func (e *wakeEnv) wantWakes(n int) {
	e.t.Helper()
	if len(e.woken) != n {
		e.t.Fatalf("woken %v, want %d wakes", e.woken, n)
	}
	_, delivered := e.deliveries.Last(shopChild)
	if delivered != (n > 0) {
		e.t.Fatalf("delivery recorded %v, want %v", delivered, n > 0)
	}
	for _, w := range e.woken {
		if w != shopChild {
			e.t.Fatalf("woke %v, want only %v", w, shopChild)
		}
	}
}

func TestProjectSetFirstDiscoveryWakesNothing(t *testing.T) {
	e := newWakeEnv(t)
	e.reconcile()
	e.child("shop")
	e.wantWakes(0)
	set := e.set()
	if got := setEntry(set, "jo.wiemers/shop").Milestones; got != milestonesHash(e.disc.disc.Projects[0].Milestones) {
		t.Errorf("stored hash %q", got)
	}
	if got := setEntry(set, "jo.wiemers/idle"); got.Milestones != "" {
		t.Errorf("an entry without a child stores a hash: %+v", got)
	}

	// Status lost (a restart restores none): the hash is stored again, and
	// still nothing wakes.
	set.Status.Projects[0].Milestones = ""
	if err := e.c.Status().Update(t.Context(), set); err != nil {
		t.Fatal(err)
	}
	e.poll()
	e.wantWakes(0)
	if got := setEntry(e.set(), "jo.wiemers/shop").Milestones; got == "" {
		t.Error("hash not stored again")
	}
}

func TestProjectSetUnchangedMilestonesWakeNothing(t *testing.T) {
	e := newWakeEnv(t)
	e.reconcile()
	e.poll()
	e.poll()
	e.wantWakes(0)
}

func TestProjectSetChangedMilestoneWakesTheChildOnce(t *testing.T) {
	e := newWakeEnv(t)
	e.reconcile()
	e.poll()
	ms := e.disc.disc.Projects[0].Milestones
	ms[0].UpdatedAt = e.now
	e.poll()
	e.wantWakes(1)
	if last, _ := e.deliveries.Last(shopChild); !last.Equal(planNow) {
		t.Errorf("delivery at %v, want the store's clock %v", last, planNow)
	}
	e.poll()
	e.wantWakes(1)

	// A new title, and a new milestone, wake it again.
	ms[0].Title = "Checkout v2"
	e.poll()
	e.wantWakes(2)
	e.disc.disc.Projects[0].Milestones = append(ms, gitlab.DiscoveredMilestone{IID: 9, Title: "Search", UpdatedAt: e.now})
	e.poll()
	e.wantWakes(3)
}

func TestProjectSetWakeWithoutStoreOrWaker(t *testing.T) {
	e := newWakeEnv(t)
	e.r.Deliveries, e.r.Wake = nil, nil
	e.reconcile()
	e.disc.disc.Projects[0].Milestones[0].Title = "Checkout v2"
	e.poll() // must not panic
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.State != aiv1.ProjectSetActive {
		t.Errorf("entry = %+v", got)
	}
}

func TestProjectSetChangedMilestoneWakesTheExplicitAgentProject(t *testing.T) {
	explicit := agentProject("infra", "my-shop", "Jo.Wiemers/Shop", nil)
	e := &wakeEnv{setEnv: newSetEnv(t, nil, &explicit)}
	e.deliveries = webhook.NewDeliveries(clocktesting.NewFakeClock(planNow))
	e.r.Deliveries = e.deliveries
	e.r.Wake = func(n types.NamespacedName) { e.woken = append(e.woken, n) }
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")}}
	e.reconcile()
	got := setEntry(e.set(), "jo.wiemers/shop")
	if got.State != aiv1.ProjectSetExplicit || got.Milestones == "" {
		t.Fatalf("the explicit entry stores no hash: %+v", got)
	}
	e.disc.disc.Projects[0].Milestones[0].Title = "Checkout v2"
	e.poll()
	want := types.NamespacedName{Namespace: "infra", Name: "my-shop"}
	if len(e.woken) != 1 || e.woken[0] != want {
		t.Fatalf("woken %v, want %v", e.woken, want)
	}
	if _, ok := e.deliveries.Last(want); !ok {
		t.Error("no delivery recorded for the explicit AgentProject")
	}
}

func TestProjectSetTruncatedMilestonesStoreNoHash(t *testing.T) {
	e := newWakeEnv(t)
	e.reconcile()
	e.poll()
	shop := &e.disc.disc.Projects[0]
	shop.MilestonesTruncated = true
	shop.Milestones[0].Title = "Checkout v2"
	e.poll()
	e.wantWakes(0)
	got := setEntry(e.set(), "jo.wiemers/shop")
	if got.Milestones != "" || !got.MilestonesTruncated {
		t.Fatalf("truncated entry = %+v, want no hash", got)
	}
	// Back under the cap: the hash is stored again without a wake.
	shop.MilestonesTruncated = false
	e.poll()
	e.wantWakes(0)
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.Milestones == "" || got.MilestonesTruncated {
		t.Fatalf("entry = %+v", got)
	}
}
