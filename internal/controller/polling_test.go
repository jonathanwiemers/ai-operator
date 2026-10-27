package controller

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
)

var shop = types.NamespacedName{Namespace: ns, Name: "shop"}

// newDeliveryEnv is a webhook env (newWebhookEnv) whose reconciler reads the
// deliveries the test records, on a fake clock it moves.
func newDeliveryEnv(t *testing.T) (*projectEnv, *clocktesting.FakeClock) {
	t.Helper()
	e, now := newWebhookEnv(t)
	clk := clocktesting.NewFakeClock(*now)
	e.r.Now = clk.Now
	e.r.Deliveries = webhook.NewDeliveries(clk)
	return e, clk
}

func (e *projectEnv) wantDeliveries(status metav1.ConditionStatus, reason string, last time.Time) {
	e.t.Helper()
	p := e.project()
	c := meta.FindStatusCondition(p.Status.Conditions, aiv1.ConditionWebhookDeliveries)
	if c == nil || c.Status != status || c.Reason != reason {
		e.t.Fatalf("want WebhookDeliveries=%s/%s, got %+v", status, reason, c)
	}
	var got time.Time
	if p.Status.Webhook != nil && p.Status.Webhook.LastDeliveryTime != nil {
		got = p.Status.Webhook.LastDeliveryTime.Time
	}
	if !got.Equal(last) {
		e.t.Fatalf("lastDeliveryTime = %v, want %v", got, last)
	}
}

func TestDeliveriesConditionAndLastDeliveryTime(t *testing.T) {
	e, clk := newDeliveryEnv(t)
	e.reconcile()
	e.wantDeliveries(metav1.ConditionFalse, "Silent", time.Time{})

	e.r.Deliveries.Record(shop)
	delivered := clk.Now()
	clk.Step(time.Minute)
	e.reconcile()
	e.wantDeliveries(metav1.ConditionTrue, "Receiving", delivered)
	if p := e.project(); p.Status.Webhook.HookID == 0 {
		t.Fatalf("the registered hook is kept next to the delivery: %+v", p.Status.Webhook)
	}

	// Just inside the threshold still receiving, at it silent.
	clk.SetTime(delivered.Add(aiv1.DefaultWebhookSilence - time.Second))
	e.reconcile()
	e.wantDeliveries(metav1.ConditionTrue, "Receiving", delivered)
	clk.SetTime(delivered.Add(aiv1.DefaultWebhookSilence))
	e.reconcile()
	e.wantDeliveries(metav1.ConditionFalse, "Silent", delivered)

	// A restart forgets the deliveries; the status still shows the last one.
	e.r.Deliveries = webhook.NewDeliveries(clk)
	e.reconcile()
	e.wantDeliveries(metav1.ConditionFalse, "Silent", delivered)

	// Registration switched off keeps the deliveries' status.
	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled = false })
	e.reconcile()
	e.wantDeliveries(metav1.ConditionFalse, "Silent", delivered)

	// Without a secretRef no delivery counts: both are gone.
	e.setWebhook(func(w *aiv1.WebhookSpec) { w.SecretRef = nil })
	e.reconcile()
	p := e.project()
	if c := meta.FindStatusCondition(p.Status.Conditions, aiv1.ConditionWebhookDeliveries); c != nil || p.Status.Webhook != nil {
		t.Fatalf("without a secretRef: condition %+v, status %+v", c, p.Status.Webhook)
	}
}

func TestDeliveriesWithoutReceiver(t *testing.T) {
	e, _ := newDeliveryEnv(t)
	e.r.Deliveries = nil
	e.reconcile()
	e.wantDeliveries(metav1.ConditionFalse, "Silent", time.Time{})
}

// countingGitLab counts the polls: ListMilestones is called by both kinds,
// CurrentUser once by every full sync (pilot.mention defaults to true).
type countingGitLab struct {
	*gitlabfake.Fake
	milestones, users int
	duringSync        func()
}

func (c *countingGitLab) ListMilestones(ctx context.Context, project string) ([]gitlab.Milestone, error) {
	c.milestones++
	return c.Fake.ListMilestones(ctx, project)
}

func (c *countingGitLab) CurrentUser(ctx context.Context) (*gitlab.User, error) {
	c.users++
	if c.duringSync != nil {
		c.duringSync()
	}
	return c.Fake.CurrentUser(ctx)
}

const planned = "shop-spec-m3-checkout-v2"

// newPollingEnv is a delivery env with one planned milestone, counting the
// GitLab calls, after its first (full) sync.
func newPollingEnv(t *testing.T) (*projectEnv, *clocktesting.FakeClock, *countingGitLab) {
	t.Helper()
	e, clk := newDeliveryEnv(t)
	e.gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\nBuild a new checkout."}}
	gl := &countingGitLab{Fake: e.gl}
	e.r.NewGitLab = func(string, string) gitlab.API { return gl }
	e.reconcile()
	if _, ok := e.tasks()[planned]; !ok {
		t.Fatalf("milestone not planned: %v", keys(e.tasks()))
	}
	return e, clk, gl
}

// poll reconciles once and reports whether it was a full sync. A light poll
// lists the milestones once; a full sync it starts lists them again.
func (e *projectEnv) poll(gl *countingGitLab) bool {
	e.t.Helper()
	m, u := gl.milestones, gl.users
	e.reconcile()
	full := gl.users > u
	if n := gl.milestones - m; n < 1 || n > 1 && !full || n > 2 {
		e.t.Fatalf("poll listed the milestones %d times (full sync %v)", n, full)
	}
	return full
}

func (e *projectEnv) wantPolls(gl *countingGitLab, clk *clocktesting.FakeClock, want ...bool) {
	e.t.Helper()
	for i, w := range want {
		clk.Step(time.Minute)
		if got := e.poll(gl); got != w {
			e.t.Fatalf("poll %d at %s: full sync %v, want %v", i+1, clk.Now().Format(time.TimeOnly), got, w)
		}
	}
}

const L, F = false, true // light poll, full sync

func TestReceivingFullSyncEveryFallbackInterval(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	// A delivery syncs at once, then only the milestones until the
	// fallback interval (10m) is up.
	e.r.Deliveries.Record(shop)
	if !e.poll(gl) {
		t.Fatal("a delivery must sync fully at once")
	}
	e.wantPolls(gl, clk, L, L, L, L, L, L, L, L, L, F, L)

	// Another delivery: a full sync at once, and the fallback counts anew.
	clk.Step(30 * time.Second)
	e.r.Deliveries.Record(shop)
	if !e.poll(gl) {
		t.Fatal("a delivery must sync fully at once")
	}
	e.wantPolls(gl, clk, L, L, L, L, L, L, L, L, L, F)
	// The silence threshold, 30m after the delivery at 12:11:30: still
	// receiving at 12:40:30, silent from 12:41:30 on.
	e.wantPolls(gl, clk, L, L, L, L, L, L, L, L, L, F, L, L, L, L, L, L, L, L, L)
	e.wantPolls(gl, clk, F, F, F)
	e.wantDeliveries(metav1.ConditionFalse, "Silent", time.Date(2026, 10, 27, 12, 11, 30, 0, time.UTC))
}

func TestNotReceivingSyncsFullyEveryPoll(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.wantPolls(gl, clk, F, F) // no delivery yet
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L)
	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled, w.SecretRef = false, nil })
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, F, F)
}

func TestReceivingMilestoneChangesSyncWithinPollInterval(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L)

	// A new spec is planned on the next poll.
	e.gl.Milestones[0].Description += "\nAlso support PayPal."
	e.wantPolls(gl, clk, F, L)
	if got := e.tasks()[planned].Spec.RunRequest; got != 2 {
		t.Fatalf("milestone change not planned: run request %d", got)
	}
	// So are a new title, new owners, autopilot and rules.
	edits := []func(m *gitlab.Milestone){
		func(m *gitlab.Milestone) { m.Title = "Checkout v3" },
		func(m *gitlab.Milestone) { m.Description += "\n/agent owners @alice" },
		func(m *gitlab.Milestone) { m.Description += "\n/agent autopilot" },
		func(m *gitlab.Milestone) { m.Description += "\n/agent rule R1: prompt keep functions short" },
	}
	for i, edit := range edits {
		edit(&e.gl.Milestones[0])
		clk.Step(time.Minute)
		if !e.poll(gl) {
			t.Fatalf("edit %d: no full sync", i)
		}
	}
	// A milestone that appears, and one that vanishes.
	e.gl.Milestones = append(e.gl.Milestones, gitlab.Milestone{ID: 12, IID: 4, Title: "Search", State: "active", Description: "/agent spec\nSearch."})
	e.wantPolls(gl, clk, F, L)
	if len(e.tasks()) < 2 {
		t.Fatalf("new milestone not planned: %v", keys(e.tasks()))
	}
	e.gl.Milestones = e.gl.Milestones[1:]
	e.wantPolls(gl, clk, F, L)
	if _, ok := e.tasks()[planned]; ok {
		t.Fatalf("vanished milestone still planned: %v", keys(e.tasks()))
	}
	// An unselected milestone changes nothing.
	e.gl.Milestones = append(e.gl.Milestones, gitlab.Milestone{ID: 13, IID: 5, Title: "Ideas", State: "active", Description: "no marker"})
	e.wantPolls(gl, clk, L)
}

func TestReceivingGenerationChangeSyncsFully(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L)
	p := e.project()
	gen := p.Generation
	p.Spec.MaxConcurrentRuns = 5
	p.Generation++ // the fake client does not
	if err := e.c.Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	if e.project().Generation == gen {
		t.Fatal("the fake client did not bump the generation")
	}
	e.wantPolls(gl, clk, F, L)
}

func TestReceivingAfterFailedSyncSyncsFully(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L)
	p := e.project()
	orig := p.DeepCopy()
	meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{Type: "Synced", Status: metav1.ConditionFalse, Reason: "SyncFailed", Message: "boom"})
	if err := e.c.Status().Patch(e.ctx, &p, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.wantPolls(gl, clk, F, L)
}

// TestDeliveryDuringSyncSyncsAgain: the status keeps whole seconds, so the
// delivery comes in the second the sync started in.
func TestDeliveryDuringSyncSyncsAgain(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.r.Deliveries.Record(shop)
	clk.Step(time.Second)
	gl.duringSync = func() { e.r.Deliveries.Record(shop); gl.duringSync = nil }
	if !e.poll(gl) {
		t.Fatal("a delivery must sync fully at once")
	}
	if !e.poll(gl) {
		t.Fatal("the delivery during the sync must sync again")
	}
	if e.poll(gl) {
		t.Fatal("covered deliveries must not sync again")
	}
}
