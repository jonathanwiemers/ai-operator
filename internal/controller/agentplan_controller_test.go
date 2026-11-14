package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

type planEnv struct {
	t   *testing.T
	c   client.Client
	r   *AgentPlanReconciler
	ctx context.Context
}

func newPlanEnv(t *testing.T, objs ...client.Object) *planEnv {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentPlan{}).Build()
	return &planEnv{t: t, c: c, ctx: context.Background(),
		r: &AgentPlanReconciler{Client: c, Scheme: s, Config: testPlanConfig, DefaultAgentImage: "agent:default"}}
}

func (e *planEnv) reconcile() *aiv1.AgentPlan {
	e.t.Helper()
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: testPlan().Name}}); err != nil {
		e.t.Fatal(err)
	}
	var plan aiv1.AgentPlan
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: testPlan().Name}, &plan); err != nil {
		e.t.Fatal(err)
	}
	return &plan
}

// pod returns the plan's pod, nil when there is none.
func (e *planEnv) pod() *corev1.Pod {
	e.t.Helper()
	var pod corev1.Pod
	err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: testPlan().Name}, &pod)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return &pod
}

func (e *planEnv) claimExists() bool {
	e.t.Helper()
	err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: naming.Workspace(testPlan().Name)}, &corev1.PersistentVolumeClaim{})
	if err != nil && !apierrors.IsNotFound(err) {
		e.t.Fatal(err)
	}
	return err == nil
}

// setPod gives the plan's pod a phase and ready containers or not.
func (e *planEnv) setPod(phase corev1.PodPhase, ready bool) {
	e.t.Helper()
	pod := e.pod()
	pod.Status.Phase = phase
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.ContainersReady, Status: corev1.ConditionFalse}}
	if ready {
		pod.Status.Conditions[0].Status = corev1.ConditionTrue
	}
	if phase == corev1.PodFailed {
		pod.Status.Reason, pod.Status.Message = "Evicted", "low on memory"
	}
	if err := e.c.Status().Update(e.ctx, pod); err != nil {
		e.t.Fatal(err)
	}
}

func pendingPlan() *aiv1.AgentPlan {
	plan := testPlan()
	plan.Spec.ChatMessages = []aiv1.ChatMessage{{ID: 7, Kind: "message", Text: "plan a milestone", Author: "owner"}}
	return plan
}

func TestPlanOwnedByProjectWithoutPodWhenNoMessage(t *testing.T) {
	e := newPlanEnv(t, testProject(), testPlan())
	plan := e.reconcile()
	refs := plan.OwnerReferences
	if len(refs) != 1 || refs[0].Kind != "AgentProject" || refs[0].Name != "shop" || refs[0].UID != "uid-shop" ||
		refs[0].Controller != nil && *refs[0].Controller {
		t.Fatalf("the project must own the plan, not control it: %+v", refs)
	}
	if e.pod() != nil || e.claimExists() || plan.Status.Phase != "" {
		t.Fatalf("a plan without messages gets no pod or claim, phase %q", plan.Status.Phase)
	}
}

func TestPlanAnsweredMessageStartsNoPod(t *testing.T) {
	plan := pendingPlan()
	plan.Spec.ChatMessages = append(plan.Spec.ChatMessages, aiv1.ChatMessage{ID: 8, Kind: "interrupt", Author: "owner"})
	plan.Status.Chat = &aiv1.ChatStatus{Replies: []aiv1.ChatReply{{MessageID: 7, Text: "done"}}}
	e := newPlanEnv(t, testProject(), plan)
	e.reconcile()
	if e.pod() != nil || e.claimExists() {
		t.Fatal("answered messages and interrupts start no pod")
	}
}

func TestPlanPendingMessageStartsPod(t *testing.T) {
	e := newPlanEnv(t, testProject(), pendingPlan())
	plan := e.reconcile()
	pod := e.pod()
	if pod == nil || !e.claimExists() {
		t.Fatal("a pending message needs the pod and its claim")
	}
	if plan.Status.Phase != aiv1.AgentPlanStarting || plan.Status.PodName != pod.Name {
		t.Fatalf("phase %q, pod %q", plan.Status.Phase, plan.Status.PodName)
	}
	if img := container(pod, AgentContainer).Image; img != "agent:latest" {
		t.Fatalf("agent image %q", img)
	}

	e.setPod(corev1.PodRunning, false)
	if plan = e.reconcile(); plan.Status.Phase != aiv1.AgentPlanStarting {
		t.Fatalf("not ready yet: phase %q", plan.Status.Phase)
	}
	e.setPod(corev1.PodRunning, true)
	if plan = e.reconcile(); plan.Status.Phase != aiv1.AgentPlanReady {
		t.Fatalf("containers ready: phase %q", plan.Status.Phase)
	}

	orig := plan.DeepCopy()
	plan.Status.Phase = aiv1.AgentPlanWorking
	if err := e.c.Status().Patch(e.ctx, plan, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if plan = e.reconcile(); plan.Status.Phase != aiv1.AgentPlanWorking {
		t.Fatalf("the runner's Working must stay: phase %q", plan.Status.Phase)
	}
}

func TestPlanCreatedOrFailedStartsNoPod(t *testing.T) {
	for _, phase := range []aiv1.AgentPlanPhase{aiv1.AgentPlanCreated, aiv1.AgentPlanFailed} {
		plan := pendingPlan()
		plan.Status.Phase = phase
		e := newPlanEnv(t, testProject(), plan)
		if e.reconcile(); e.pod() != nil {
			t.Fatalf("%s: no pod", phase)
		}
	}
}

func TestPlanFailedPodsFailThePlan(t *testing.T) {
	e := newPlanEnv(t, testProject(), pendingPlan())
	e.reconcile()
	for i := int32(1); i <= aiv1.MaxPlanPodFailures; i++ {
		e.setPod(corev1.PodFailed, false)
		plan := e.reconcile()
		if e.pod() != nil || plan.Status.PodFailures != i {
			t.Fatalf("failure %d: the pod must be deleted and counted, got %d", i, plan.Status.PodFailures)
		}
		want := aiv1.AgentPlanStarting
		if i == aiv1.MaxPlanPodFailures {
			want = aiv1.AgentPlanFailed
		}
		if plan.Status.Phase != want || !strings.Contains(plan.Status.Message, "Evicted") {
			t.Fatalf("failure %d: phase %q, message %q", i, plan.Status.Phase, plan.Status.Message)
		}
		if e.reconcile(); (e.pod() != nil) != (want == aiv1.AgentPlanStarting) {
			t.Fatalf("failure %d: a new pod only while the plan has not failed", i)
		}
	}
}

func TestPlanReadyPodResetsFailures(t *testing.T) {
	plan := pendingPlan()
	plan.Status.PodFailures = 2
	e := newPlanEnv(t, testProject(), plan)
	e.reconcile()
	e.setPod(corev1.PodRunning, true)
	if plan = e.reconcile(); plan.Status.PodFailures != 0 || plan.Status.Phase != aiv1.AgentPlanReady {
		t.Fatalf("a ready pod resets the count: %d, phase %q", plan.Status.PodFailures, plan.Status.Phase)
	}
}

func TestPlanMissingProjectFails(t *testing.T) {
	e := newPlanEnv(t, pendingPlan())
	plan := e.reconcile()
	if plan.Status.Phase != aiv1.AgentPlanFailed || plan.Status.Message != "project not found" || e.pod() != nil {
		t.Fatalf("phase %q, message %q", plan.Status.Phase, plan.Status.Message)
	}
}

func TestPlanWake(t *testing.T) {
	r := &AgentPlanReconciler{}
	r.Wake(types.NamespacedName{Namespace: ns, Name: "p"}) // before setup: dropped, never blocks
	r.wakes = make(chan event.GenericEvent, 1)
	r.Wake(types.NamespacedName{Namespace: ns, Name: "p"})
	r.Wake(types.NamespacedName{Namespace: ns, Name: "q"}) // full: dropped
	if ev := <-r.wakes; ev.Object.GetNamespace() != ns || ev.Object.GetName() != "p" {
		t.Fatalf("event for %s/%s", ev.Object.GetNamespace(), ev.Object.GetName())
	}
}
