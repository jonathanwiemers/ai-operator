package controller

import (
	"cmp"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// childEnv is a polling env (newPollingEnv) whose project "shop" is a child
// of the set "jo", counting the GitLab clients the reconciler creates: a
// poll that creates none sends no request.
type childEnv struct {
	*projectEnv
	clk     *clocktesting.FakeClock
	gl      *countingGitLab
	clients int
	age     time.Duration
}

func newChildEnv(t *testing.T) *childEnv {
	t.Helper()
	e, clk, gl := newPollingEnv(t)
	c := &childEnv{projectEnv: e, clk: clk, gl: gl}
	e.r.NewGitLab = func(string, string) gitlab.API { c.clients++; return gl }
	set := &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Name: "jo", Namespace: ns, UID: "set-uid"}}
	set.Status.LastDiscoveryTime = &metav1.Time{Time: clk.Now()}
	set.Status.Projects = []aiv1.ProjectSetProjectStatus{{Path: e.project().Spec.GitLab.Project, State: aiv1.ProjectSetActive, Child: "shop", Milestones: "hash"}}
	if err := e.c.Create(e.ctx, set); err != nil {
		t.Fatal(err)
	}
	p := e.project()
	if err := controllerutil.SetControllerReference(set, &p, e.r.Scheme); err != nil {
		t.Fatal(err)
	}
	metav1.SetMetaDataLabel(&p.ObjectMeta, aiv1.LabelProjectSet, "jo")
	if err := e.c.Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	return c
}

// discovered sets the set's last discovery to ago before now.
func (c *childEnv) discovered(ago time.Duration) {
	c.t.Helper()
	var set aiv1.AgentProjectSet
	if err := c.c.Get(c.ctx, types.NamespacedName{Namespace: ns, Name: "jo"}, &set); err != nil {
		c.t.Fatal(err)
	}
	set.Status.LastDiscoveryTime = &metav1.Time{Time: c.clk.Now().Add(-ago)}
	if err := c.c.Update(c.ctx, &set); err != nil {
		c.t.Fatal(err)
	}
}

// childPolls steps the clock a minute per poll, with a set discovery
// c.age old (a minute by default), and wants a full sync (F) or no GitLab
// client at all (skip).
func (c *childEnv) childPolls(want ...bool) {
	c.t.Helper()
	for i, full := range want {
		c.clk.Step(time.Minute)
		c.discovered(cmp.Or(c.age, time.Minute))
		clients, users, ms := c.clients, c.gl.users, c.gl.milestones
		c.reconcile()
		gotFull := c.gl.users > users
		switch {
		case full && !gotFull:
			c.t.Fatalf("poll %d at %s: no full sync", i+1, c.clk.Now().Format(time.TimeOnly))
		case !full && (c.clients != clients || c.gl.milestones != ms || gotFull):
			c.t.Fatalf("poll %d at %s: %d GitLab clients, %d milestone lists, full sync %v; want no request",
				i+1, c.clk.Now().Format(time.TimeOnly), c.clients-clients, c.gl.milestones-ms, gotFull)
		}
	}
}

const skip = false

func TestSetChildFullSyncsOnlyEveryFallbackInterval(t *testing.T) {
	c := newChildEnv(t)
	// The last full sync was at 12:00; the fallback interval is 10m.
	c.childPolls(skip, skip, skip, skip, skip, skip, skip, skip, skip, F, skip, skip)
	cond := meta.FindStatusCondition(c.project().Status.Conditions, aiv1.ConditionWebhookDeliveries)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "ViaProjectSet" ||
		!strings.Contains(cond.Message, "milestones watched by set jo") || !strings.Contains(cond.Message, "10m0s") {
		t.Fatalf("WebhookDeliveries = %+v", cond)
	}
}

func TestSetChildDeliverySyncsOnce(t *testing.T) {
	c := newChildEnv(t)
	c.childPolls(skip, skip)
	// What the set (or the receiver) does on a change.
	c.r.Deliveries.Record(shop)
	c.childPolls(F, skip, skip)
	c.wantDeliveries(metav1.ConditionTrue, "ViaProjectSet", c.clk.Now().Add(-3*time.Minute))
}

func TestSetChildWithStaleDiscoveryPollsOnItsOwn(t *testing.T) {
	c := newChildEnv(t)
	c.r.Deliveries.Record(shop)
	c.childPolls(F, skip)
	// Younger than twice the set's pollInterval (2m): still current.
	c.age = 4*time.Minute - time.Second
	c.childPolls(skip)

	// Twice the set's pollInterval old: the light poll as for an explicit
	// AgentProject.
	c.age = 0
	c.clk.Step(time.Minute)
	c.discovered(4 * time.Minute)
	if c.poll(c.gl) {
		t.Fatal("stale discovery while receiving: want the light poll")
	}
	c.wantDeliveries(metav1.ConditionTrue, "Receiving", c.clk.Now().Add(-4*time.Minute))
	// A current discovery again: no request.
	c.childPolls(skip)

	// A set that is gone: polled like an explicit project, full syncs once
	// the deliveries stop.
	var set aiv1.AgentProjectSet
	if err := c.c.Get(c.ctx, types.NamespacedName{Namespace: ns, Name: "jo"}, &set); err != nil {
		t.Fatal(err)
	}
	if err := c.c.Delete(c.ctx, &set); err != nil {
		t.Fatal(err)
	}
	c.wantPolls(c.gl, c.clk, L, L)
	c.r.Deliveries = nil
	c.wantPolls(c.gl, c.clk, F, F)
}

func TestSetChildOfAnotherSetUIDPollsOnItsOwn(t *testing.T) {
	c := newChildEnv(t)
	// The set was deleted and created anew: the child's owner is gone.
	var set aiv1.AgentProjectSet
	if err := c.c.Get(c.ctx, types.NamespacedName{Namespace: ns, Name: "jo"}, &set); err != nil {
		t.Fatal(err)
	}
	if err := c.c.Delete(c.ctx, &set); err != nil {
		t.Fatal(err)
	}
	fresh := &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Name: "jo", Namespace: ns, UID: "other-uid"}}
	fresh.Status.LastDiscoveryTime = set.Status.LastDiscoveryTime
	if err := c.c.Create(c.ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if !c.poll(c.gl) {
		t.Fatal("no deliveries and no current set: want a full sync")
	}
}

func TestExplicitProjectNextToACurrentSetPollsAsBefore(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	set := &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Name: "jo", Namespace: ns}}
	set.Status.LastDiscoveryTime = &metav1.Time{Time: clk.Now()}
	if err := e.c.Create(e.ctx, set); err != nil {
		t.Fatal(err)
	}
	// The label alone does not make a child.
	p := e.project()
	metav1.SetMetaDataLabel(&p.ObjectMeta, aiv1.LabelProjectSet, "jo")
	if err := e.c.Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	e.wantPolls(gl, clk, F, F)
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L, L)
	e.wantDeliveries(metav1.ConditionTrue, "Receiving", clk.Now().Add(-3*time.Minute))
}

// settling gives the child the counts of a project whose only milestone
// settles, as its last full sync left them.
func (c *childEnv) settling() {
	c.t.Helper()
	p := c.project()
	p.Status.ActiveMilestones, p.Status.SettlingMilestones = 0, 1
	if err := c.c.Status().Update(c.ctx, &p); err != nil {
		c.t.Fatal(err)
	}
}

func TestSetChildWithOnlySettlingMilestonesSyncsEverySettleInterval(t *testing.T) {
	c := newChildEnv(t)
	c.settling()
	// The last full sync was at 12:00: no request at 12:10, the fallback
	// interval, a full sync at 12:15, settleInterval.
	c.childPolls(skip, skip, skip, skip, skip, skip, skip, skip, skip, skip, skip, skip, skip, skip, F)
}

func TestSetChildWithOnlySettlingMilestonesSyncsADeliveryAtOnce(t *testing.T) {
	c := newChildEnv(t)
	c.settling()
	c.childPolls(skip, skip)
	c.r.Deliveries.Record(shop)
	c.childPolls(F)
}

// setEntries replaces the set's status entries.
func (c *childEnv) setEntries(entries ...aiv1.ProjectSetProjectStatus) {
	c.t.Helper()
	var set aiv1.AgentProjectSet
	if err := c.c.Get(c.ctx, types.NamespacedName{Namespace: ns, Name: "jo"}, &set); err != nil {
		c.t.Fatal(err)
	}
	set.Status.Projects = entries
	if err := c.c.Update(c.ctx, &set); err != nil {
		c.t.Fatal(err)
	}
}

func TestSetChildWithTruncatedMilestonesPollsOnItsOwn(t *testing.T) {
	c := newChildEnv(t)
	c.childPolls(skip)
	path := c.project().Spec.GitLab.Project
	c.setEntries(aiv1.ProjectSetProjectStatus{Path: path, State: aiv1.ProjectSetActive, Child: "shop", MilestonesTruncated: true})
	c.clk.Step(time.Minute)
	c.discovered(time.Minute)
	if !c.poll(c.gl) {
		t.Fatal("the set cannot see all milestones: want the project's own full sync")
	}
}

func TestExplicitProjectWatchedByTheSetSkipsPolls(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	c := &childEnv{projectEnv: e, clk: clk, gl: gl}
	e.r.NewGitLab = func(string, string) gitlab.API { c.clients++; return gl }
	set := &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Name: "jo", Namespace: ns, UID: "set-uid"}}
	set.Status.LastDiscoveryTime = &metav1.Time{Time: clk.Now()}
	// The set normalizes paths: case does not matter.
	set.Status.Projects = []aiv1.ProjectSetProjectStatus{{Path: strings.ToUpper(e.project().Spec.GitLab.Project),
		State: aiv1.ProjectSetExplicit, Milestones: "hash"}}
	if err := e.c.Create(e.ctx, set); err != nil {
		t.Fatal(err)
	}
	c.childPolls(skip, skip, skip)
	cond := meta.FindStatusCondition(c.project().Status.Conditions, aiv1.ConditionWebhookDeliveries)
	if cond == nil || cond.Reason != "ViaProjectSet" {
		t.Fatalf("WebhookDeliveries = %+v", cond)
	}
}
