package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

type fakeDiscoverer struct {
	disc  *gitlab.GroupDiscovery
	err   error
	calls int
	group string
	label string
	url   string
	token string
}

func (f *fakeDiscoverer) DiscoverGroup(_ context.Context, group, readyLabel string) (*gitlab.GroupDiscovery, error) {
	f.calls++
	f.group, f.label = group, readyLabel
	if f.err != nil {
		return nil, f.err
	}
	return f.disc, nil
}

// setTemplate is a template children accept: webhooks on.
func setTemplate(extra map[string]any) *runtime.RawExtension {
	t := map[string]any{
		"webhook": map[string]any{
			"enabled":   true,
			"url":       "https://hooks.example/gitlab",
			"secretRef": map[string]any{"name": "hook", "key": "secret"},
		},
		"agents": map[string]any{"image": "agent:1"},
	}
	for k, v := range extra {
		t[k] = v
	}
	raw, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return &runtime.RawExtension{Raw: raw}
}

type setEnv struct {
	t    *testing.T
	c    client.Client
	r    *AgentProjectSetReconciler
	disc *fakeDiscoverer
	now  time.Time
}

func newSetEnv(t *testing.T, mutate func(*aiv1.AgentProjectSet), objs ...client.Object) *setEnv {
	t.Helper()
	s := testScheme(t)
	set := planSet()
	set.Generation = 1
	set.Spec.Template = setTemplate(nil)
	if mutate != nil {
		mutate(set)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gitlab", Namespace: "agents"}, Data: map[string][]byte{"token": []byte(" tok\n")}}
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(append([]client.Object{set, secret}, objs...)...).
		WithStatusSubresource(&aiv1.AgentProjectSet{}, &aiv1.AgentProject{}).
		Build()
	e := &setEnv{t: t, c: c, disc: &fakeDiscoverer{disc: &gitlab.GroupDiscovery{}}, now: planNow}
	e.r = &AgentProjectSetReconciler{Client: c, APIReader: c, Scheme: s, Now: func() time.Time { return e.now },
		NewDiscoverer: func(url, token string) gitlab.Discoverer {
			e.disc.url, e.disc.token = url, token
			return e.disc
		}}
	return e
}

func (e *setEnv) reconcile() ctrl.Result {
	e.t.Helper()
	res, err := e.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "jo"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *setEnv) set() *aiv1.AgentProjectSet {
	e.t.Helper()
	var set aiv1.AgentProjectSet
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "jo"}, &set); err != nil {
		e.t.Fatal(err)
	}
	return &set
}

func (e *setEnv) children() []aiv1.AgentProject {
	e.t.Helper()
	var list aiv1.AgentProjectList
	if err := e.c.List(context.Background(), &list, client.MatchingLabels{aiv1.LabelProjectSet: "jo"}); err != nil {
		e.t.Fatal(err)
	}
	return list.Items
}

func (e *setEnv) child(name string) *aiv1.AgentProject {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: name}, &p); err != nil {
		e.t.Fatal(err)
	}
	return &p
}

func setEntry(set *aiv1.AgentProjectSet, path string) aiv1.ProjectSetProjectStatus {
	for _, e := range set.Status.Projects {
		if e.Path == path {
			return e
		}
	}
	return aiv1.ProjectSetProjectStatus{}
}

func setCondition(t *testing.T, set *aiv1.AgentProjectSet, typ string) metav1.Condition {
	t.Helper()
	c := meta.FindStatusCondition(set.Status.Conditions, typ)
	if c == nil {
		t.Fatalf("no condition %s in %+v", typ, set.Status.Conditions)
	}
	return *c
}

func activeProject(path string) gitlab.DiscoveredProject {
	return gitlab.DiscoveredProject{FullPath: path, Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec\n\nGoal")}}
}

func TestProjectSetReconcileCreate(t *testing.T) {
	e := newSetEnv(t, nil)
	e.disc.disc = &gitlab.GroupDiscovery{
		Projects: []gitlab.DiscoveredProject{
			activeProject("jo.wiemers/devops/functions/pnpm"),
			{FullPath: "jo.wiemers/ready"},
			{FullPath: "jo.wiemers/idle"},
		},
		ReadyIssues: []gitlab.DiscoveredIssue{{Project: "jo.wiemers/ready", IID: 4}},
		Complexity:  gitlab.QueryComplexity{Score: 68, Limit: 250},
		Posts:       1,
	}
	res := e.reconcile()
	if res.RequeueAfter != 2*time.Minute {
		t.Errorf("requeue = %v, want the poll interval", res.RequeueAfter)
	}
	if e.disc.group != "jo.wiemers" || e.disc.label != "agent::ready" || e.disc.token != "tok" || e.disc.url != "https://gitlab.example" {
		t.Errorf("discovery called with %+v", e.disc)
	}
	kids := e.children()
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	c := e.child("devops-functions-pnpm")
	if c.Spec.GitLab.Project != "jo.wiemers/devops/functions/pnpm" || c.Spec.GitLab.URL != "https://gitlab.example" ||
		c.Spec.GitLab.TokenSecretRef.Name != "gitlab" || c.Spec.Agents.Image != "agent:1" || !c.Spec.Webhook.Enabled {
		t.Errorf("child spec = %+v", c.Spec)
	}
	if c.Annotations[aiv1.AnnotationTemplateHash] == "" {
		t.Error("child has no template hash")
	}
	if ref := metav1.GetControllerOf(c); ref == nil || ref.Kind != "AgentProjectSet" || ref.Name != "jo" || ref.UID != "set-uid" {
		t.Errorf("controller ref = %+v", ref)
	}
	e.child("ready")

	set := e.set()
	if set.Status.Counts.Active != 2 || set.Status.Counts.Dormant != 1 {
		t.Errorf("counts = %+v", set.Status.Counts)
	}
	if got := setEntry(set, "jo.wiemers/idle"); got.State != aiv1.ProjectSetDormant || got.Child != "" {
		t.Errorf("idle entry = %+v", got)
	}
	if set.Status.LastDiscoveryTime == nil || !set.Status.LastDiscoveryTime.Time.Equal(planNow) || set.Status.ObservedGeneration != 1 {
		t.Errorf("discovery time %v, generation %d", set.Status.LastDiscoveryTime, set.Status.ObservedGeneration)
	}
	if c := set.Status.Complexity; c == nil || c.Score != 68 || c.Limit != 250 {
		t.Errorf("complexity = %+v", c)
	}
	if c := setCondition(t, set, aiv1.ConditionReady); c.Status != metav1.ConditionTrue || c.Reason != aiv1.ReasonReady {
		t.Errorf("Ready = %+v", c)
	}
	if c := setCondition(t, set, aiv1.ConditionDiscovered); c.Status != metav1.ConditionTrue {
		t.Errorf("Discovered = %+v", c)
	}
}

// A named project's child carries the template with its entry merged on
// top; the other children keep the template's spec and hash. A template
// change reaches the fields the entry does not set.
func TestProjectSetReconcileEntryMerged(t *testing.T) {
	disc := &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop"), activeProject("jo.wiemers/other")}}
	tmpl := func(image string) *runtime.RawExtension {
		return setTemplate(map[string]any{"maxConcurrentRuns": 2, "agents": map[string]any{"image": image, "imagePullPolicy": "Always"}})
	}
	plain := newSetEnv(t, func(s *aiv1.AgentProjectSet) { s.Spec.Template = tmpl("agent:1") })
	plain.disc.disc = disc
	plain.reconcile()
	named := newSetEnv(t, func(s *aiv1.AgentProjectSet) {
		s.Spec.Template = tmpl("agent:1")
		s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/shop",
			Spec: &runtime.RawExtension{Raw: []byte(`{"maxConcurrentRuns":3,"agents":{"imagePullPolicy":null}}`)}}}
	})
	named.disc.disc = disc
	named.reconcile()
	if c := setCondition(t, named.set(), aiv1.ConditionReady); c.Status != metav1.ConditionTrue {
		t.Errorf("Ready = %+v", c)
	}
	shop, other := named.child("shop"), named.child("other")
	if shop.Spec.MaxConcurrentRuns != 3 || shop.Spec.Agents.Image != "agent:1" || shop.Spec.Agents.ImagePullPolicy != "" {
		t.Errorf("shop spec = %+v", shop.Spec)
	}
	if hash := aiv1.AnnotationTemplateHash; shop.Annotations[hash] == plain.child("shop").Annotations[hash] ||
		other.Annotations[hash] != plain.child("other").Annotations[hash] || other.Spec.MaxConcurrentRuns != 2 {
		t.Errorf("hashes: shop %s, other %s", shop.Annotations[hash], other.Annotations[hash])
	}

	set := named.set()
	set.Spec.Template = tmpl("agent:2")
	if err := named.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	named.now = named.now.Add(time.Hour)
	named.reconcile()
	if got := named.child("shop").Spec; got.Agents.Image != "agent:2" || got.MaxConcurrentRuns != 3 {
		t.Errorf("shop after template change = %+v", got)
	}
}

func TestProjectSetReconcileSkips(t *testing.T) {
	explicit := agentProject("infra", "shop", "Jo.Wiemers/Shop", nil)
	foreign := agentProject("agents", "taken", "jo.wiemers/elsewhere", nil)
	e := newSetEnv(t, nil, &explicit, &foreign)
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{
		activeProject("jo.wiemers/shop"),
		activeProject("jo.wiemers/archived/old"),
		{FullPath: "jo.wiemers/frozen", Archived: true, Milestones: activeProject("").Milestones},
		activeProject("jo.wiemers/taken"),
	}}
	e.reconcile()
	if kids := e.children(); len(kids) != 0 {
		t.Fatalf("children = %+v, want none", kids)
	}
	set := e.set()
	for path, want := range map[string]aiv1.ProjectSetState{
		"jo.wiemers/shop":         aiv1.ProjectSetExplicit,
		"jo.wiemers/archived/old": aiv1.ProjectSetExcluded,
		"jo.wiemers/frozen":       aiv1.ProjectSetExcluded,
		"jo.wiemers/taken":        aiv1.ProjectSetNameTaken,
	} {
		if got := setEntry(set, path); got.State != want || got.Reason == "" {
			t.Errorf("%s = %+v, want %s with a reason", path, got, want)
		}
	}
	if got := setEntry(set, "jo.wiemers/frozen").Reason; got != "archived" {
		t.Errorf("archived reason = %q", got)
	}
	// The explicit and the foreign AgentProject are untouched.
	var p aiv1.AgentProject
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "taken"}, &p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.GitLab.Project != "jo.wiemers/elsewhere" || len(p.OwnerReferences) != 0 {
		t.Errorf("foreign AgentProject changed: %+v", p)
	}
}

func TestProjectSetReconcileCreateRace(t *testing.T) {
	e := newSetEnv(t, nil)
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*aiv1.AgentProject); ok {
				return apierrors.NewAlreadyExists(schema.GroupResource{Group: "ai.wiemers.me", Resource: "agentprojects"}, obj.GetName())
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")}}
	e.reconcile()
	set := e.set()
	if got := setEntry(set, "jo.wiemers/shop"); got.State != aiv1.ProjectSetNameTaken || got.Child != "" {
		t.Errorf("entry = %+v, want nameTaken", got)
	}
	if set.Status.Counts.NameTaken != 1 || set.Status.Counts.Active != 0 {
		t.Errorf("counts = %+v", set.Status.Counts)
	}
}

func TestProjectSetReconcileTemplateChange(t *testing.T) {
	e := newSetEnv(t, nil)
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/a"), activeProject("jo.wiemers/b")}}
	e.reconcile()
	a, b := e.child("a"), e.child("b")

	// Unchanged template, next poll: nothing is written.
	e.now = e.now.Add(2 * time.Minute)
	e.reconcile()
	if e.disc.calls != 2 {
		t.Fatalf("discovery calls = %d, want 2", e.disc.calls)
	}
	if got := e.child("a"); got.ResourceVersion != a.ResourceVersion {
		t.Errorf("unchanged child was updated: %s -> %s", a.ResourceVersion, got.ResourceVersion)
	}

	// The template changes: the children get it at once.
	set := e.set()
	set.Spec.Template = setTemplate(map[string]any{"agents": map[string]any{"image": "agent:2"}})
	set.Generation = 2
	if err := e.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(10 * time.Second)
	e.reconcile()
	if e.disc.calls != 3 {
		t.Fatalf("a spec change did not discover at once: %d calls", e.disc.calls)
	}
	got := e.child("a")
	if got.Spec.Agents.Image != "agent:2" || got.Annotations[aiv1.AnnotationTemplateHash] == a.Annotations[aiv1.AnnotationTemplateHash] {
		t.Errorf("child a not updated: image %q", got.Spec.Agents.Image)
	}
	if got := e.child("b"); got.Spec.Agents.Image != "agent:2" || got.ResourceVersion == b.ResourceVersion {
		t.Errorf("child b not updated: image %q", got.Spec.Agents.Image)
	}
	if e.set().Status.ObservedGeneration != 2 {
		t.Errorf("observedGeneration = %d", e.set().Status.ObservedGeneration)
	}
}

func TestProjectSetReconcileInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*aiv1.AgentProjectSet)
		reason string
	}{
		"unknown field": {func(s *aiv1.AgentProjectSet) { s.Spec.Template = setTemplate(map[string]any{"bogus": 1}) }, aiv1.ReasonInvalidTemplate},
		"gitlab": {func(s *aiv1.AgentProjectSet) {
			s.Spec.Template = setTemplate(map[string]any{"gitlab": map[string]any{"project": "x/y"}})
		}, aiv1.ReasonInvalidTemplate},
		"webhook missing": {func(s *aiv1.AgentProjectSet) {
			s.Spec.Template = &runtime.RawExtension{Raw: []byte(`{"agents":{"image":"agent:1"}}`)}
		}, aiv1.ReasonWebhookRequired},
		"empty include": {func(s *aiv1.AgentProjectSet) { s.Spec.Include = nil }, aiv1.ReasonInvalidSpec},
		"entry outside group": {func(s *aiv1.AgentProjectSet) {
			s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "other/shop"}}
		}, aiv1.ReasonInvalidSpec},
		"entry unknown field": {func(s *aiv1.AgentProjectSet) {
			s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/shop", Spec: setTemplate(map[string]any{"bogus": 1})}}
		}, aiv1.ReasonInvalidTemplate},
		"entry webhook off": {func(s *aiv1.AgentProjectSet) {
			s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/shop", Spec: &runtime.RawExtension{Raw: []byte(`{"webhook":{"enabled":false}}`)}}}
		}, aiv1.ReasonWebhookRequired},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSetEnv(t, tc.mutate)
			e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")}}
			if res := e.reconcile(); res.RequeueAfter != 2*time.Minute {
				t.Errorf("requeue = %v", res.RequeueAfter)
			}
			if e.disc.calls != 0 {
				t.Errorf("discovery calls = %d, want 0", e.disc.calls)
			}
			if kids := e.children(); len(kids) != 0 {
				t.Errorf("children = %d, want 0", len(kids))
			}
			c := setCondition(t, e.set(), aiv1.ConditionReady)
			if c.Status != metav1.ConditionFalse || c.Reason != tc.reason || c.Message == "" {
				t.Errorf("Ready = %+v, want False %s", c, tc.reason)
			}
			if strings.HasPrefix(name, "entry") && !strings.HasPrefix(c.Message, "projects[0]") {
				t.Errorf("message %q does not name projects[0]", c.Message)
			}
		})
	}
}

func TestProjectSetReconcileDroppedTarget(t *testing.T) {
	e := newSetEnv(t, func(s *aiv1.AgentProjectSet) {
		s.Spec.Template = setTemplate(map[string]any{"crossRepo": map[string]any{"targets": []any{
			map[string]any{"project": "jo.wiemers/infra/cluster", "paths": []any{"apps/"}},
			map[string]any{"project": "jo.wiemers/archived/infra", "paths": []any{"apps/"}},
		}}})
	})
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")}}
	e.reconcile()
	c := e.child("shop")
	if c.Spec.CrossRepo == nil || len(c.Spec.CrossRepo.Targets) != 1 || c.Spec.CrossRepo.Targets[0].Project != "jo.wiemers/infra/cluster" {
		t.Errorf("child crossRepo = %+v", c.Spec.CrossRepo)
	}
	if got := e.set().Status.DroppedTargets; len(got) != 1 || got[0] != "jo.wiemers/archived/infra" {
		t.Errorf("droppedTargets = %v", got)
	}
}

func TestProjectSetReconcileDiscoveryError(t *testing.T) {
	e := newSetEnv(t, nil)
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")}}
	e.reconcile()
	before := e.set()

	e.now = e.now.Add(2 * time.Minute)
	e.disc.err = errors.New(strings.Repeat("boom ", 400))
	if res := e.reconcile(); res.RequeueAfter != 2*time.Minute {
		t.Errorf("requeue = %v", res.RequeueAfter)
	}
	set := e.set()
	c := setCondition(t, set, aiv1.ConditionDiscovered)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1.ReasonDiscoveryFailed || !strings.HasSuffix(c.Message, "…") || len(c.Message) > 1024+len("…") {
		t.Errorf("Discovered = %+v (message %d bytes)", c, len(c.Message))
	}
	if len(set.Status.Projects) != 1 || set.Status.Projects[0].Child != "shop" {
		t.Errorf("entries not kept: %+v", set.Status.Projects)
	}
	if !set.Status.LastDiscoveryTime.Equal(before.Status.LastDiscoveryTime) {
		t.Errorf("lastDiscoveryTime moved: %v -> %v", before.Status.LastDiscoveryTime, set.Status.LastDiscoveryTime)
	}
	if len(e.children()) != 1 {
		t.Error("child removed on a failed discovery")
	}
}

func TestProjectSetReconcileTooComplex(t *testing.T) {
	e := newSetEnv(t, nil)
	e.disc.disc = &gitlab.GroupDiscovery{
		Projects:   []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")},
		Complexity: gitlab.QueryComplexity{Score: 210, Limit: 250},
	}
	e.reconcile()
	c := setCondition(t, e.set(), aiv1.ConditionReady)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1.ReasonTooComplex ||
		!strings.Contains(c.Message, "210") || !strings.Contains(c.Message, "250") {
		t.Errorf("Ready = %+v", c)
	}
	if len(e.children()) != 1 {
		t.Error("a too complex discovery must still apply its children")
	}
}

// GitLab's complexity score is an int: one past the int32 range must not
// wrap to a small score that hides a too complex discovery.
func TestProjectSetReconcileComplexityBeyondInt32(t *testing.T) {
	e := newSetEnv(t, nil)
	e.disc.disc = &gitlab.GroupDiscovery{
		Projects:   []gitlab.DiscoveredProject{activeProject("jo.wiemers/shop")},
		Complexity: gitlab.QueryComplexity{Score: 1<<32 + 1, Limit: 1<<32 + 250},
	}
	e.reconcile()
	if c := e.set().Status.Complexity; c == nil || c.Score != math.MaxInt32 || c.Limit != math.MaxInt32 {
		t.Errorf("complexity = %+v, want both saturated at %d", c, math.MaxInt32)
	}
	if c := setCondition(t, e.set(), aiv1.ConditionReady); c.Status != metav1.ConditionFalse || c.Reason != aiv1.ReasonTooComplex {
		t.Errorf("Ready = %+v, want too complex", c)
	}
}

func TestProjectSetReconcileIdleWithinPoll(t *testing.T) {
	e := newSetEnv(t, nil)
	e.reconcile()
	e.now = e.now.Add(90 * time.Second)
	res := e.reconcile()
	if e.disc.calls != 1 {
		t.Errorf("discovery calls = %d, want 1", e.disc.calls)
	}
	if res.RequeueAfter != 30*time.Second {
		t.Errorf("requeue = %v, want the remainder 30s", res.RequeueAfter)
	}
}

func TestProjectSetReconcileTaskKeepsActive(t *testing.T) {
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "agents", Labels: map[string]string{LabelProject: "quiet"}}}
	e := newSetEnv(t, nil, task)
	e.disc.disc = &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/quiet"}}}
	e.reconcile()
	if got := setEntry(e.set(), "jo.wiemers/quiet"); got.State != aiv1.ProjectSetActive || got.Child != "quiet" {
		t.Errorf("entry = %+v, want active through its task", got)
	}
	e.child("quiet")
}

// adoptEnv is a set that names jo.wiemers/shop, and the annotated explicit
// AgentProject shop that watches it, with a registered hook and a task.
func adoptEnv(t *testing.T) (*setEnv, *aiv1.AgentProject) {
	t.Helper()
	own := annotated(agentProject("agents", "shop", "jo.wiemers/shop", nil), "jo")
	own.UID = "shop-uid"
	own.Spec.MaxConcurrentRuns = 7
	own.Status.Webhook = &aiv1.WebhookStatus{HookID: 42}
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "agents", Labels: map[string]string{LabelProject: "shop"},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "ai.wiemers.me/v1alpha1", Kind: "AgentProject", Name: "shop", UID: own.UID, Controller: ptr.To(true)}}}}
	e := newSetEnv(t, func(s *aiv1.AgentProjectSet) {
		s.Spec.Projects = []aiv1.ProjectSetEntry{{Path: "jo.wiemers/shop", Spec: &runtime.RawExtension{Raw: []byte(`{"maxConcurrentRuns":3}`)}}}
	}, &own, task)
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"})
	return e, &own
}

func TestProjectSetReconcileAdopt(t *testing.T) {
	e, own := adoptEnv(t)
	e.reconcile()
	got := e.child("shop")
	if got.UID != own.UID || !metav1.IsControlledBy(got, e.set()) || got.Labels[aiv1.LabelProjectSet] != "jo" ||
		got.Annotations[aiv1.AnnotationTemplateHash] == "" || got.Annotations[aiv1.AnnotationProjectSet] != "jo" {
		t.Fatalf("not adopted: %+v", got.ObjectMeta)
	}
	if got.Spec.MaxConcurrentRuns != 3 || got.Spec.Agents.Image != "agent:1" || !got.Spec.Webhook.Enabled {
		t.Errorf("spec = %+v, want the entry's merged spec", got.Spec)
	}
	if got.Status.Webhook == nil || got.Status.Webhook.HookID != 42 {
		t.Errorf("webhook status = %+v, want hook 42 kept", got.Status.Webhook)
	}
	var task aiv1.AgentTask
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop-1"}, &task); err != nil {
		t.Fatalf("task: %v", err)
	}
	if entry := setEntry(e.set(), "jo.wiemers/shop"); entry.State != aiv1.ProjectSetActive || entry.Child != "shop" ||
		entry.Reason != "adopted from AgentProject agents/shop" {
		t.Errorf("entry = %+v", entry)
	}

	// The next poll sees an ordinary named child and writes nothing.
	e.poll()
	if again := e.child("shop"); again.ResourceVersion != got.ResourceVersion {
		t.Errorf("adopted child rewritten: %s -> %s", got.ResourceVersion, again.ResourceVersion)
	}
	if entry := setEntry(e.set(), "jo.wiemers/shop"); entry.Reason != namedReason {
		t.Errorf("entry after adoption = %+v", entry)
	}
}

func TestProjectSetReconcileAdoptConflict(t *testing.T) {
	e, _ := adoptEnv(t)
	conflicts := 1
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*aiv1.AgentProject); ok && conflicts > 0 {
				conflicts--
				return apierrors.NewConflict(schema.GroupResource{Group: "ai.wiemers.me", Resource: "agentprojects"}, obj.GetName(), nil)
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	e.reconcile()
	if got := e.child("shop"); len(got.OwnerReferences) != 0 || got.Spec.MaxConcurrentRuns != 7 {
		t.Fatalf("a conflicting adoption changed the object: %+v", got)
	}
	e.poll()
	if got := e.child("shop"); !metav1.IsControlledBy(got, e.set()) || got.UID != "shop-uid" {
		t.Errorf("not adopted on the next poll: %+v", got.ObjectMeta)
	}
}

func TestProjectSetReconcileEntryRemoved(t *testing.T) {
	e, _ := adoptEnv(t)
	e.reconcile()
	adopted := e.child("shop")

	// The entry goes: the child stays owned and gets the template alone.
	set := e.set()
	set.Spec.Projects, set.Generation = nil, 2
	if err := e.c.Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	e.poll()
	got := e.child("shop")
	if got.UID != adopted.UID || !metav1.IsControlledBy(got, e.set()) || got.Spec.MaxConcurrentRuns != 0 ||
		got.Annotations[aiv1.AnnotationTemplateHash] == adopted.Annotations[aiv1.AnnotationTemplateHash] {
		t.Fatalf("after the entry is removed: %+v %+v", got.ObjectMeta, got.Spec)
	}

	// No milestone past dormantAfter: its task keeps it active.
	dormantAfter := e.set().Spec.WithDefaults().DormantAfter.Duration
	e.now = e.now.Add(dormantAfter)
	e.poll()
	if entry := setEntry(e.set(), "jo.wiemers/shop"); entry.State != aiv1.ProjectSetActive || removing(e.child("shop")) {
		t.Fatalf("entry = %+v, want a child with a task active and kept", entry)
	}

	// Without the task the dormant rules remove it.
	if err := e.c.Delete(context.Background(), &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if removing(e.child("shop")) {
		t.Fatal("removed before dormantAfter")
	}
	e.now = e.now.Add(dormantAfter)
	e.poll()
	if entry := setEntry(e.set(), "jo.wiemers/shop"); entry.State != aiv1.ProjectSetDormant {
		t.Errorf("entry = %+v, want dormant", entry)
	}
	if !removing(e.child("shop")) {
		t.Error("dormant child without tasks is not being removed")
	}
}
