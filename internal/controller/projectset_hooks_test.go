package controller

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// hookDiscoverer is a discoverer with project hooks on many projects. calls
// logs every hook request as "verb path".
type hookDiscoverer struct {
	*fakeDiscoverer
	hooks     map[string][]gitlab.ProjectHook
	next      int64
	calls     []string
	deleteErr error
	added     map[int64]gitlab.ProjectHookOptions
}

func (h *hookDiscoverer) ListProjectHooks(_ context.Context, project string) ([]gitlab.ProjectHook, error) {
	h.calls = append(h.calls, "list "+project)
	return slices.Clone(h.hooks[project]), nil
}

func (h *hookDiscoverer) AddProjectHook(_ context.Context, project string, o gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	h.calls = append(h.calls, "add "+project)
	h.next++
	hook := gitlab.ProjectHook{ID: h.next, URL: o.URL, IssuesEvents: o.IssuesEvents, NoteEvents: o.NoteEvents,
		MergeRequestsEvents: o.MergeRequestsEvents, PipelineEvents: o.PipelineEvents, PushEvents: o.PushEvents,
		MilestoneEvents: o.MilestoneEvents, EnableSSLVerification: o.EnableSSLVerification}
	h.hooks[project] = append(h.hooks[project], hook)
	h.added[hook.ID] = o
	return &hook, nil
}

func (h *hookDiscoverer) EditProjectHook(_ context.Context, project string, id int64, _ gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	h.calls = append(h.calls, "edit "+project)
	return &gitlab.ProjectHook{ID: id}, nil
}

func (h *hookDiscoverer) DeleteProjectHook(_ context.Context, project string, id int64) error {
	h.calls = append(h.calls, "delete "+project)
	i := slices.IndexFunc(h.hooks[project], func(x gitlab.ProjectHook) bool { return x.ID == id })
	if i < 0 {
		return &gitlab.Error{StatusCode: http.StatusNotFound}
	}
	if h.deleteErr != nil {
		return h.deleteErr
	}
	h.hooks[project] = slices.Delete(h.hooks[project], i, i+1)
	return nil
}

// newHookEnv is a set env whose template's webhook secret exists and whose
// discoverer manages hooks.
func newHookEnv(t *testing.T) (*setEnv, *hookDiscoverer) {
	t.Helper()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hook", Namespace: "agents"}, Data: map[string][]byte{"secret": []byte("s3cret\n")}}
	e := newSetEnv(t, nil, secret)
	h := &hookDiscoverer{fakeDiscoverer: e.disc, hooks: map[string][]gitlab.ProjectHook{}, added: map[int64]gitlab.ProjectHookOptions{}}
	e.r.NewDiscoverer = func(string, string) gitlab.Discoverer { return h }
	return e, h
}

// takeCalls returns the hook requests since the last call.
func (h *hookDiscoverer) takeCalls() string {
	c := strings.Join(h.calls, ", ")
	h.calls = nil
	return c
}

func TestSetHookOnDormantProjectsIssuesAndMilestonesOnly(t *testing.T) {
	e, h := newHookEnv(t)
	e.discover(activeProject("jo.wiemers/shop"), gitlab.DiscoveredProject{FullPath: "jo.wiemers/idle"})
	e.reconcile()
	if got := h.takeCalls(); got != "list jo.wiemers/idle, add jo.wiemers/idle" {
		t.Fatalf("hook requests = %q", got)
	}
	o := h.added[1]
	if o.URL != "https://hooks.example/gitlab" || o.Token != "s3cret" || !o.IssuesEvents || !o.MilestoneEvents || !o.EnableSSLVerification ||
		o.PushEvents || o.NoteEvents || o.MergeRequestsEvents || o.PipelineEvents {
		t.Fatalf("hook options = %+v", o)
	}
	set := e.set()
	if en := setEntry(set, "jo.wiemers/idle"); en.HookID != 1 || en.HookCheckedAt == nil {
		t.Fatalf("idle entry = %+v", en)
	}
	if en := setEntry(set, "jo.wiemers/shop"); en.HookID != 0 {
		t.Fatalf("an active project with a child gets no set hook: %+v", en)
	}
	if !controllerutil.ContainsFinalizer(set, setHookFinalizer) {
		t.Fatal("no finalizer")
	}

	// Checked again only after an hour and its jitter.
	for range 30 {
		e.poll()
	}
	if got := h.takeCalls(); got != "" {
		t.Fatalf("within the hour: %q", got)
	}
	for range 10 {
		e.poll()
	}
	if got := h.takeCalls(); got != "list jo.wiemers/idle" {
		t.Fatalf("recheck = %q, want one list", got)
	}
}

func TestSetHooksStayWithinTheBudgetPerReconcile(t *testing.T) {
	e, h := newHookEnv(t)
	var projects []gitlab.DiscoveredProject
	for i := range 7 {
		projects = append(projects, gitlab.DiscoveredProject{FullPath: fmt.Sprintf("jo.wiemers/p%d", i)})
	}
	e.discover(projects...)
	for poll, want := range []int{3, 3, 1, 0} {
		e.poll()
		adds := strings.Count(h.takeCalls(), "add ")
		if adds != want {
			t.Fatalf("poll %d: %d hooks added, want %d", poll+1, adds, want)
		}
	}
}

func TestSetHookGoesWhenTheProjectTurnsActive(t *testing.T) {
	e, h := newHookEnv(t)
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"}, gitlab.DiscoveredProject{FullPath: "jo.wiemers/cart"})
	e.reconcile()
	h.takeCalls()

	// Both get work: the children are created, the hooks stay until they
	// register their own.
	e.discover(activeProject("jo.wiemers/shop"), activeProject("jo.wiemers/cart"))
	e.poll()
	if got := h.takeCalls(); got != "" {
		t.Fatalf("children not registered yet: %q", got)
	}
	// shop's child took the set's hook over by URL; cart's registered another.
	shop, cart := setEntry(e.set(), "jo.wiemers/shop"), setEntry(e.set(), "jo.wiemers/cart")
	e.hook(shop.Child, shop.HookID, "Registered", "")
	e.hook(cart.Child, 99, "Registered", "")
	e.poll()
	if got := h.takeCalls(); got != "delete jo.wiemers/cart" {
		t.Fatalf("hook requests = %q, want only cart's delete", got)
	}
	set := e.set()
	for _, path := range []string{"jo.wiemers/shop", "jo.wiemers/cart"} {
		if en := setEntry(set, path); en.HookID != 0 || en.HookCheckedAt != nil {
			t.Fatalf("%s entry = %+v", path, en)
		}
	}
}

func TestSetHookGoesWhenTheProjectIsExcludedOrGone(t *testing.T) {
	e, h := newHookEnv(t)
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/old"}, gitlab.DiscoveredProject{FullPath: "jo.wiemers/moved"})
	e.reconcile()
	h.takeCalls()

	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/old", Archived: true})
	h.deleteErr = &gitlab.Error{StatusCode: http.StatusForbidden}
	e.poll()
	if got := h.takeCalls(); got != "delete jo.wiemers/old, delete jo.wiemers/moved" {
		t.Fatalf("hook requests = %q", got)
	}
	// Kept while the delete fails: the gone project as an entry of its own.
	if en := setEntry(e.set(), "jo.wiemers/moved"); en.HookID == 0 || en.State != aiv1.ProjectSetExcluded {
		t.Fatalf("moved entry = %+v", en)
	}
	h.deleteErr = nil
	e.poll()
	set := e.set()
	if setEntry(set, "jo.wiemers/old").HookID != 0 || setEntry(set, "jo.wiemers/moved").Path != "" {
		t.Fatalf("entries = %+v", set.Status.Projects)
	}
}

func TestDeletedSetDeletesItsHooksAndGivesUpAfterThreeFailures(t *testing.T) {
	e, h := newHookEnv(t)
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/a"}, gitlab.DiscoveredProject{FullPath: "jo.wiemers/b"})
	e.reconcile()
	h.takeCalls()
	h.hooks["jo.wiemers/b"] = nil // gone already: 404 counts as deleted

	set := e.set()
	if err := e.c.Delete(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	// a fails three times; then the set lets go.
	h.deleteErr = &gitlab.Error{StatusCode: http.StatusInternalServerError}
	for i := range 3 {
		res := e.reconcile()
		if i < 2 && res.RequeueAfter != setHookRetry {
			t.Fatalf("attempt %d: requeue %v", i+1, res.RequeueAfter)
		}
	}
	if got := h.takeCalls(); got != "delete jo.wiemers/a, delete jo.wiemers/b, delete jo.wiemers/a, delete jo.wiemers/a" {
		t.Fatalf("hook requests = %q", got)
	}
	var gone aiv1.AgentProjectSet
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "jo"}, &gone); client.IgnoreNotFound(err) != nil || err == nil {
		t.Fatalf("set still there: %v %+v", err, gone.Finalizers)
	}
}

func TestWokenSetDiscoversAtOnce(t *testing.T) {
	e, _ := newHookEnv(t)
	var sent []types.NamespacedName
	e.r.SendWake = func(n types.NamespacedName) { sent = append(sent, n) }
	e.reconcile()
	calls := e.disc.calls
	e.now = e.now.Add(time.Minute)
	e.reconcile()
	if e.disc.calls != calls {
		t.Fatal("an idle set discovered before its poll")
	}
	n := types.NamespacedName{Namespace: "agents", Name: "jo"}
	e.r.WakeSet(n)
	e.reconcile()
	if e.disc.calls != calls+1 || len(sent) != 1 || sent[0] != n {
		t.Fatalf("discoveries %d, wakes sent %v", e.disc.calls-calls, sent)
	}
	// Once only: the next reconcile is idle again.
	e.reconcile()
	if e.disc.calls != calls+1 {
		t.Fatal("the wake discovered twice")
	}
}

// A named project missing from the group is dormant without a child, and
// gets no set hook.
func TestSetHookNotForNamedProjectNotFound(t *testing.T) {
	e, h := newHookEnv(t)
	set := e.set()
	set.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/missing"}}
	if err := e.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/idle"})
	e.reconcile()
	if got := h.takeCalls(); got != "list jo.wiemers/idle, add jo.wiemers/idle" {
		t.Fatalf("hook requests = %q", got)
	}
	if en := setEntry(e.set(), "jo.wiemers/missing"); en.State != aiv1.ProjectSetDormant || en.Child != "" || en.HookID != 0 {
		t.Errorf("missing entry = %+v", en)
	}
}
