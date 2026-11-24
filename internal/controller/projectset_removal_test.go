package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// poll moves the clock one pollInterval on and reconciles.
func (e *setEnv) poll() {
	e.t.Helper()
	e.now = e.now.Add(2 * time.Minute)
	e.reconcile()
}

// hook sets a child's webhook status as its own reconciler would.
func (e *setEnv) hook(name string, hookID int64, reason, msg string) {
	e.t.Helper()
	p := e.child(name)
	p.Status.Webhook, p.Status.Conditions = nil, nil
	if hookID != 0 {
		p.Status.Webhook = &aiv1.WebhookStatus{HookID: hookID}
	}
	if reason != "" {
		status := metav1.ConditionFalse
		if reason == "Registered" {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
			Type: aiv1.ConditionWebhookRegistered, Status: status, Reason: reason, Message: msg,
		})
	}
	if err := e.c.Status().Update(context.Background(), p); err != nil {
		e.t.Fatal(err)
	}
}

func (e *setEnv) gone(name string) bool {
	e.t.Helper()
	var p aiv1.AgentProject
	err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: name}, &p)
	if err != nil && !apierrors.IsNotFound(err) {
		e.t.Fatal(err)
	}
	return apierrors.IsNotFound(err)
}

func (e *setEnv) discover(projects ...gitlab.DiscoveredProject) {
	e.disc.disc = &gitlab.GroupDiscovery{Projects: projects}
}

// removing reports whether a child is in removal: webhook off, annotated.
func removing(p *aiv1.AgentProject) bool {
	return !p.Spec.Webhook.Enabled && p.Annotations[aiv1.AnnotationRemoving] != ""
}

// dormantShop sets up a set whose child shop has a registered hook and has
// been idle for dormantAfter.
func dormantShop(t *testing.T) *setEnv {
	t.Helper()
	e := newSetEnv(t, nil)
	e.discover(activeProject("jo.wiemers/shop"))
	e.reconcile()
	e.hook("shop", 7, "Registered", "project hook 7 delivers")
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"})
	e.now = e.now.Add(168 * time.Hour)
	e.reconcile()
	return e
}

func TestProjectSetRemovalWaitsForTheHook(t *testing.T) {
	e := dormantShop(t)
	c := e.child("shop")
	if !removing(c) {
		t.Fatalf("dormant child not switched off: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	if got := c.Annotations[aiv1.AnnotationRemoving]; got != e.now.UTC().Format(time.RFC3339) {
		t.Errorf("removing annotation = %q", got)
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.State != aiv1.ProjectSetDormant || got.Child != "shop" || got.Reason != removingReason {
		t.Errorf("entry = %+v", got)
	}

	// The hook is still registered: the child stays, however many polls.
	e.poll()
	e.poll()
	if e.gone("shop") {
		t.Fatal("child deleted while its hook is registered")
	}
	// The child deleted its hook: the next poll deletes it.
	e.hook("shop", 0, "", "")
	e.poll()
	if !e.gone("shop") {
		t.Fatal("child not deleted after its hook was gone")
	}
	set := e.set()
	if got := setEntry(set, "jo.wiemers/shop"); got.State != aiv1.ProjectSetDormant || got.Child != "" {
		t.Errorf("entry after delete = %+v", got)
	}
	if set.Status.Counts.Dormant != 1 {
		t.Errorf("counts = %+v", set.Status.Counts)
	}

	// Active again after the delete: the child comes back with its hook.
	e.discover(activeProject("jo.wiemers/shop"))
	e.poll()
	if c := e.child("shop"); removing(c) || !c.Spec.Webhook.Enabled {
		t.Errorf("recreated child = webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.State != aiv1.ProjectSetActive || got.Child != "shop" {
		t.Errorf("entry after recreate = %+v", got)
	}
}

func TestProjectSetRemovalKeepsChildrenWithTasks(t *testing.T) {
	e := newSetEnv(t, nil)
	e.discover(activeProject("jo.wiemers/shop"), activeProject("jo.wiemers/quiet"))
	e.reconcile()
	for _, name := range []string{"shop", "quiet"} {
		task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: name + "-1", Namespace: "agents", Labels: map[string]string{LabelProject: name}}}
		if err := e.c.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	// shop leaves the scope, quiet is idle far beyond dormantAfter.
	set := e.set()
	set.Spec.Exclude = append(set.Spec.Exclude, "jo.wiemers/shop")
	set.Generation = 2
	if err := e.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	e.discover(activeProject("jo.wiemers/shop"), gitlab.DiscoveredProject{FullPath: "jo.wiemers/quiet"})
	e.now = e.now.Add(1000 * time.Hour)
	e.reconcile()
	e.poll()

	for _, name := range []string{"shop", "quiet"} {
		if c := e.child(name); removing(c) || !c.Spec.Webhook.Enabled {
			t.Errorf("%s with a task touched: webhook %v, annotations %v", name, c.Spec.Webhook.Enabled, c.Annotations)
		}
	}
	got := e.set()
	if en := setEntry(got, "jo.wiemers/shop"); en.State != aiv1.ProjectSetExcluded || en.Child != "shop" ||
		!strings.HasPrefix(en.Reason, "kept: 1 open task; matches exclude jo.wiemers/shop") {
		t.Errorf("shop entry = %+v", en)
	}
	if en := setEntry(got, "jo.wiemers/quiet"); en.State != aiv1.ProjectSetActive || en.Child != "quiet" {
		t.Errorf("quiet entry = %+v", en)
	}
}

func TestProjectSetRemovalLiveTaskCheck(t *testing.T) {
	e := dormantShop(t)
	e.hook("shop", 0, "", "")
	// The cache has no task yet, the API server has.
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "agents", Labels: map[string]string{LabelProject: "shop"}}}
	e.r.APIReader = fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(task).Build()
	e.poll()
	if e.gone("shop") {
		t.Fatal("child deleted although the live read found a task")
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); !strings.HasPrefix(got.Reason, "kept: 1 open task") || got.Child != "shop" {
		t.Errorf("entry = %+v", got)
	}
}

func TestProjectSetRemovalConflictTriesAgain(t *testing.T) {
	e := dormantShop(t)
	e.hook("shop", 0, "", "")
	conflicts := 0
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if conflicts == 0 {
				conflicts++
				return apierrors.NewConflict(schema.GroupResource{Group: "ai.wiemers.me", Resource: "agentprojects"}, obj.GetName(), nil)
			}
			return c.Delete(ctx, obj, opts...)
		},
	})
	e.poll()
	if e.gone("shop") || setEntry(e.set(), "jo.wiemers/shop").Reason != removingReason {
		t.Fatalf("conflict: gone %v, entry %+v", e.gone("shop"), setEntry(e.set(), "jo.wiemers/shop"))
	}
	e.poll()
	if !e.gone("shop") {
		t.Fatal("child not deleted on the poll after the conflict")
	}
}

func TestProjectSetRemovalStaleResourceVersion(t *testing.T) {
	e := dormantShop(t)
	e.hook("shop", 0, "", "")
	stale := e.child("shop")
	e.hook("shop", 9, "Registered", "a sync still running registered it")
	entry := aiv1.ProjectSetProjectStatus{Child: "shop"}
	deleted, err := e.r.removeChild(context.Background(), stale, &entry, e.now)
	if err != nil || deleted || e.gone("shop") {
		t.Fatalf("stale child deleted: %v, %v", deleted, err)
	}
}

func TestProjectSetRemovalRevive(t *testing.T) {
	e := dormantShop(t)
	before := e.child("shop")
	if !removing(before) {
		t.Fatal("removal did not start")
	}
	e.discover(activeProject("jo.wiemers/shop"))
	e.poll()
	c := e.child("shop")
	if removing(c) || !c.Spec.Webhook.Enabled || c.Annotations[aiv1.AnnotationRemoving] != "" {
		t.Errorf("not revived: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	if c.Annotations[aiv1.AnnotationTemplateHash] != before.Annotations[aiv1.AnnotationTemplateHash] {
		t.Error("template hash changed on revive")
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.State != aiv1.ProjectSetActive || got.Reason != "" {
		t.Errorf("entry = %+v", got)
	}
	// A revived child is not written again on the next poll.
	e.poll()
	if got := e.child("shop"); got.ResourceVersion != c.ResourceVersion {
		t.Errorf("revived child written again: %s -> %s", c.ResourceVersion, got.ResourceVersion)
	}
}

func TestProjectSetRemovalExcludedWithoutDormantWait(t *testing.T) {
	e := newSetEnv(t, nil)
	e.discover(activeProject("jo.wiemers/shop"))
	e.reconcile()
	set := e.set()
	set.Spec.Exclude = append(set.Spec.Exclude, "jo.wiemers/shop")
	set.Generation = 2
	if err := e.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(10 * time.Second)
	e.reconcile()
	if c := e.child("shop"); !removing(c) {
		t.Fatalf("excluded child not switched off: webhook %v", c.Spec.Webhook.Enabled)
	}
	// It never registered a hook: the next poll deletes it.
	e.poll()
	if !e.gone("shop") {
		t.Fatal("excluded child not deleted")
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); got.State != aiv1.ProjectSetExcluded || got.Child != "" {
		t.Errorf("entry = %+v", got)
	}
}

func TestProjectSetRemovalForbiddenHook(t *testing.T) {
	e := dormantShop(t)
	e.hook("shop", 7, "Forbidden", "the bot needs the Maintainer role to manage project webhooks")
	for range 3 {
		e.poll()
	}
	if e.gone("shop") {
		t.Fatal("child deleted while its hook deletion is forbidden")
	}
	got := setEntry(e.set(), "jo.wiemers/shop")
	if !strings.HasPrefix(got.Reason, removingReason) || !strings.Contains(got.Reason, "Maintainer role") {
		t.Errorf("reason = %q", got.Reason)
	}
}

// A child whose only milestone settles is idle and turns dormant, but its
// tasks keep it until the milestone closes and they are gone.
func TestProjectSetRemovalKeepsSettlingChildWithTasks(t *testing.T) {
	e := newSetEnv(t, nil)
	e.discover(activeProject("jo.wiemers/shop"))
	e.reconcile()
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "agents", Labels: map[string]string{LabelProject: "shop"}}}
	if err := e.c.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	c := e.child("shop")
	c.Status.ActiveMilestones, c.Status.SettlingMilestones = 0, 1
	if err := e.c.Status().Update(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	// The spec milestone is still open, the task still there.
	e.now = e.now.Add(168 * time.Hour)
	e.reconcile()
	e.poll()
	if c := e.child("shop"); removing(c) || !c.Spec.Webhook.Enabled {
		t.Errorf("settling child with a task touched: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	if en := setEntry(e.set(), "jo.wiemers/shop"); en.State != aiv1.ProjectSetDormant || en.Child != "shop" ||
		!strings.HasPrefix(en.Reason, "kept: 1 open task; no work for 168h") {
		t.Errorf("entry = %+v", en)
	}

	// The milestone closed and its task went: removed without another wait.
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"})
	if err := e.c.Delete(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if c := e.child("shop"); !removing(c) {
		t.Fatalf("child not switched off: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	e.poll()
	if !e.gone("shop") {
		t.Fatal("child not deleted after its tasks were gone")
	}
}

// namedShop sets up a set that names jo.wiemers/shop, idle, with its child.
func namedShop(t *testing.T) *setEnv {
	t.Helper()
	e := newSetEnv(t, func(s *aiv1.AgentProjectSet) { s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/shop"}} })
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"})
	e.reconcile()
	return e
}

// A named child without work is never removed.
func TestProjectSetRemovalKeepsNamedChild(t *testing.T) {
	e := namedShop(t)
	e.now = e.now.Add(1000 * time.Hour)
	e.reconcile()
	e.poll()
	if c := e.child("shop"); removing(c) || !c.Spec.Webhook.Enabled {
		t.Fatalf("named child touched: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	set := e.set()
	if en := setEntry(set, "jo.wiemers/shop"); en.State != aiv1.ProjectSetActive || en.Child != "shop" || en.Reason != namedReason ||
		!en.LastActiveTime.Time.Equal(e.now) {
		t.Errorf("entry = %+v", en)
	}
	if set.Status.Counts != (aiv1.ProjectSetCounts{Active: 1}) {
		t.Errorf("counts = %+v", set.Status.Counts)
	}
}

// A named child whose project leaves the scope or the group is removed
// through the usual paths, and the set stays Ready.
func TestProjectSetRemovalNamedChildOutOfScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*setEnv)
		state  aiv1.ProjectSetState
		reason string
	}{
		{"excluded", func(e *setEnv) {
			set := e.set()
			set.Spec.Exclude = append(set.Spec.Exclude, "jo.wiemers/shop")
			set.Generation = 2
			if err := e.c.Update(context.Background(), set); err != nil {
				t.Fatal(err)
			}
		}, aiv1.ProjectSetExcluded, "named in spec.projects, but matches exclude jo.wiemers/shop"},
		{"not found", func(e *setEnv) { e.discover() }, aiv1.ProjectSetDormant, "named in spec.projects, but not found in the group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := namedShop(t)
			tc.change(e)
			e.poll()
			if c := e.child("shop"); !removing(c) {
				t.Fatalf("child not switched off: webhook %v", c.Spec.Webhook.Enabled)
			}
			e.poll()
			if !e.gone("shop") {
				t.Fatal("child not deleted")
			}
			set := e.set()
			if en := setEntry(set, "jo.wiemers/shop"); en.State != tc.state || en.Child != "" || en.Reason != tc.reason {
				t.Errorf("entry = %+v", en)
			}
			if c := setCondition(t, set, aiv1.ConditionReady); c.Status != metav1.ConditionTrue {
				t.Errorf("ready = %+v", c)
			}
			// No child comes back on later polls.
			e.poll()
			if len(e.children()) != 0 {
				t.Errorf("children = %+v", e.children())
			}
		})
	}
}
