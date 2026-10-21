package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

type taskEnv struct {
	t   *testing.T
	c   client.Client
	r   *AgentTaskReconciler
	now time.Time
	ctx context.Context
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTaskEnv(t *testing.T, objs ...client.Object) *taskEnv {
	s := testScheme(t)
	project := testProject()
	project.Spec.MaxConcurrentRuns = 1
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(append([]client.Object{project}, objs...)...).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).
		Build()
	e := &taskEnv{t: t, c: c, now: t0, ctx: context.Background()}
	e.r = &AgentTaskReconciler{Client: c, Scheme: s, Now: func() time.Time { return e.now }}
	return e
}

func newImplTask(name string, runRequest int64) *aiv1.AgentTask {
	return &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, UID: types.UID("uid-" + name),
			CreationTimestamp: metav1.NewTime(t0),
			Labels:            map[string]string{LabelProject: "shop", LabelType: "implement"},
		},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: runRequest,
			Issue: &aiv1.IssueRef{IID: 42, Title: "Add PayPal"}},
	}
}

func (e *taskEnv) reconcile(name string) ctrl.Result {
	e.t.Helper()
	res, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *taskEnv) task(name string) *aiv1.AgentTask {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &task); err != nil {
		e.t.Fatal(err)
	}
	return &task
}

func (e *taskEnv) exists(obj client.Object, name string) bool {
	e.t.Helper()
	err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, obj)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return true
}

// finishRun simulates the runner completing all requested runs.
func (e *taskEnv) finishRun(name string) {
	e.t.Helper()
	task := e.task(name)
	orig := task.DeepCopy()
	task.Status.Run.ObservedRunRequest = task.Spec.RunRequest
	task.Status.Run.ActiveRunRequest = task.Spec.RunRequest
	task.Status.Run.RunCount++
	task.Status.Run.LastActivityTime = &metav1.Time{Time: e.now}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func TestTaskLifecycle(t *testing.T) {
	const name = "shop-42-add-paypal"
	e := newTaskEnv(t, newImplTask(name, 1))

	// Run requested: workspace and pod are created.
	e.reconcile(name)
	var pod corev1.Pod
	var pvc corev1.PersistentVolumeClaim
	if !e.exists(&pod, name) || !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("expected pod and workspace")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseRunning {
		t.Fatalf("phase %s", got)
	}
	if c := container(&pod, AgentContainer); c == nil || c.EnvFrom[0].SecretRef.Name != "agent-creds" {
		t.Fatal("engine credentials must be injected into the agent container")
	}
	if !metav1.IsControlledBy(&pod, e.task(name)) || !metav1.IsControlledBy(&pvc, e.task(name)) {
		t.Fatal("pod and pvc must be owned by the task")
	}

	// Run finished: the pod waits briefly for a follow-up request.
	e.now = e.now.Add(10 * time.Minute)
	e.finishRun(name)
	res := e.reconcile(name)
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseIdle {
		t.Fatalf("phase %s", got)
	}
	if !e.exists(&pod, name) {
		t.Fatal("idle pod must be kept until the idle timeout")
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > aiv1.DefaultIdleTimeout+time.Second {
		t.Fatalf("should requeue at the idle timeout, got %v", res.RequeueAfter)
	}

	// Idle timeout (a minute by default): pod removed, workspace kept.
	e.now = e.now.Add(aiv1.DefaultIdleTimeout + time.Second)
	e.reconcile(name)
	if e.exists(&pod, name) {
		t.Fatal("pod should be hibernated")
	}
	if !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("workspace must survive hibernation")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseHibernated {
		t.Fatalf("phase %s", got)
	}

	// New input wakes the task up on the same workspace.
	task := e.task(name)
	task.Spec.RunRequest = 2
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile(name)
	if !e.exists(&pod, name) || e.task(name).Status.Phase != aiv1.TaskPhaseRunning {
		t.Fatal("pod should be back")
	}
	e.finishRun(name)
	e.reconcile(name)

	// Long inactivity: hibernate, then archive the workspace.
	e.now = e.now.Add(3 * time.Hour)
	e.reconcile(name)
	e.now = e.now.Add(15 * 24 * time.Hour)
	e.reconcile(name)
	if e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("workspace should be archived")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseArchived {
		t.Fatalf("phase %s", got)
	}

	// Even an archived task can be resumed: fresh workspace and pod.
	task = e.task(name)
	task.Spec.RunRequest = 3
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile(name)
	if !e.exists(&pod, name) || !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("archived task should be rehydrated")
	}
}

func TestTaskQueueing(t *testing.T) {
	e := newTaskEnv(t, newImplTask("a", 1), newImplTask("b", 1))
	e.reconcile("a")
	res := e.reconcile("b")
	if got := e.task("b").Status.Phase; got != aiv1.TaskPhaseQueued {
		t.Fatalf("second task should queue (maxConcurrentRuns=1), phase %s", got)
	}
	if res.RequeueAfter != queueRetry {
		t.Fatalf("queued task should retry, got %v", res.RequeueAfter)
	}
	var pod corev1.Pod
	if e.exists(&pod, "b") {
		t.Fatal("queued task must not get a pod")
	}
	e.finishRun("a")
	e.reconcile("a")
	e.reconcile("b")
	if got := e.task("b").Status.Phase; got != aiv1.TaskPhaseRunning {
		t.Fatalf("b should run once a is idle, phase %s", got)
	}
}

func TestTaskBlockedAfterMaxRuns(t *testing.T) {
	task := newImplTask("x", 21)
	task.Status.Run.RunCount = 20
	task.Status.Run.ObservedRunRequest = 20
	task.Status.Run.ActiveRunRequest = 20
	e := newTaskEnv(t, task)
	e.reconcile("x")
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseBlocked {
		t.Fatalf("phase %s", got)
	}
	var pod corev1.Pod
	if e.exists(&pod, "x") {
		t.Fatal("blocked task must not start a pod")
	}
}

func TestDefaultAgentImage(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	project := &aiv1.AgentProject{}
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, project); err != nil {
		t.Fatal(err)
	}
	project.Spec.Agents.Image = ""
	if err := e.c.Update(e.ctx, project); err != nil {
		t.Fatal(err)
	}

	// Neither the project nor the operator has an image: wait, no pod.
	e.reconcile("x")
	var pod corev1.Pod
	if e.exists(&pod, "x") || e.task("x").Status.Phase != aiv1.TaskPhaseQueued {
		t.Fatal("without an image the task must wait")
	}

	e.r.DefaultAgentImage = "ghcr.io/jonathanwiemers/ai-operator-agent:2026.10.1"
	e.reconcile("x")
	if !e.exists(&pod, "x") || pod.Spec.Containers[0].Image != e.r.DefaultAgentImage {
		t.Fatalf("pod should use the operator's default image, got %+v", pod.Spec.Containers)
	}
}

func TestFailedRunKeepsPodForLogs(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	e.finishRun("x")
	task := e.task("x")
	orig := task.DeepCopy()
	task.Status.Run.LastResult = aiv1.RunResultFailed
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	e.now = e.now.Add(10 * time.Minute)
	e.reconcile("x")
	if !e.exists(&pod, "x") {
		t.Fatal("the pod of a failed run must stay so its logs can be read")
	}
	e.now = e.now.Add(failedRunIdleTimeout)
	e.reconcile("x")
	if e.exists(&pod, "x") {
		t.Fatal("the pod of a failed run hibernates after failedRunIdleTimeout")
	}
}

func TestIdlePodKeptForRequestTheCacheMissed(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	e.finishRun("x")
	// The API server already has a new run request the cached client has
	// not seen yet.
	newer := e.task("x")
	newer.Spec.RunRequest = 2
	e.r.APIReader = fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(newer).Build()

	e.now = e.now.Add(aiv1.DefaultIdleTimeout + time.Second)
	res := e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") || res.RequeueAfter == 0 {
		t.Fatalf("pod must be kept and the task re-checked, requeue %v", res.RequeueAfter)
	}

	e.r.APIReader = e.c
	e.reconcile("x")
	if e.exists(&pod, "x") {
		t.Fatal("without a pending run the idle pod hibernates")
	}
}

func TestCurrentStepClearedWithThePod(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("expected pod")
	}
	// The runner is in the middle of a step when the task is suspended.
	task := e.task("x")
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = 1
	task.Status.Run.CurrentStep = "9 run implementer"
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	task = e.task("x")
	task.Spec.Suspend = true
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	if e.exists(&pod, "x") {
		t.Fatal("suspended task must not keep its pod")
	}
	if got := e.task("x").Status.Run.CurrentStep; got != "" {
		t.Fatalf("current step must be cleared with the pod, got %q", got)
	}
}

func TestAgentLogLevel(t *testing.T) {
	logLevel := func(c *corev1.Container) string {
		for _, e := range c.Env {
			if e.Name == "AI_LOG_LEVEL" {
				return e.Value
			}
		}
		return ""
	}
	e := newTaskEnv(t, newImplTask("x", 1), newImplTask("y", 1))
	e.r.DefaultAgentLogLevel = aiv1.LogLevelInfo

	// The operator-wide level reaches both containers.
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("expected pod")
	}
	for _, name := range []string{RunnerContainer, AgentContainer} {
		if got := logLevel(container(&pod, name)); got != "info" {
			t.Fatalf("%s container: AI_LOG_LEVEL=%q, want info", name, got)
		}
	}

	// The project overrides it.
	project := &aiv1.AgentProject{}
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, project); err != nil {
		t.Fatal(err)
	}
	project.Spec.Agents.LogLevel = aiv1.LogLevelDebug
	project.Spec.MaxConcurrentRuns = 2
	if err := e.c.Update(e.ctx, project); err != nil {
		t.Fatal(err)
	}
	e.reconcile("y")
	if !e.exists(&pod, "y") {
		t.Fatal("expected pod")
	}
	for _, name := range []string{RunnerContainer, AgentContainer} {
		if got := logLevel(container(&pod, name)); got != "debug" {
			t.Fatalf("%s container: AI_LOG_LEVEL=%q, want debug", name, got)
		}
	}
}

func container(pod *corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

// TestPodIsolatesCredentials pins down the security properties of the pod:
// the agent container (where the model runs) gets no GitLab token, no
// service account token and no view of the runner's state.
func TestPodIsolatesCredentials(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	spec.Agents.Env = []corev1.EnvVar{{Name: "EXTRA", Value: "1"}}
	spec.Agents.CacheClaimName = "cache"
	pod := buildPod(newImplTask("x", 1), spec)

	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("the service account token must not be auto-mounted into every container")
	}
	if pod.Spec.ShareProcessNamespace != nil && *pod.Spec.ShareProcessNamespace {
		t.Fatal("containers must not share a process namespace")
	}
	runner, agent := container(pod, RunnerContainer), container(pod, AgentContainer)
	if runner == nil || agent == nil || len(pod.Spec.Containers) != 2 {
		t.Fatalf("expected runner and agent containers, got %d", len(pod.Spec.Containers))
	}

	hasEnv := func(c *corev1.Container, name string) bool {
		for _, e := range c.Env {
			if e.Name == name {
				return true
			}
		}
		return false
	}
	mountedAt := func(c *corev1.Container, volume string) (string, bool) {
		for _, m := range c.VolumeMounts {
			if m.Name == volume {
				return m.SubPath, true
			}
		}
		return "", false
	}

	if !hasEnv(runner, "GITLAB_TOKEN") || hasEnv(agent, "GITLAB_TOKEN") {
		t.Fatal("GITLAB_TOKEN must be in the runner container only")
	}
	if len(runner.EnvFrom) != 0 || len(agent.EnvFrom) != 1 {
		t.Fatal("agent CLI credentials belong to the agent container only")
	}
	if _, ok := mountedAt(runner, "kube-api-access"); !ok {
		t.Fatal("runner needs the service account token")
	}
	if _, ok := mountedAt(agent, "kube-api-access"); ok {
		t.Fatal("agent must not get the service account token")
	}
	if sub, _ := mountedAt(runner, "workspace"); sub != "runner" {
		t.Fatalf("runner must mount its own sub-path, got %q", sub)
	}
	if sub, _ := mountedAt(agent, "workspace"); sub != "agent" {
		t.Fatalf("agent must mount its own sub-path, got %q", sub)
	}
	if _, ok := mountedAt(runner, "cache"); ok {
		t.Fatal("dependency caches belong to the agent container")
	}
	if !hasEnv(agent, "EXTRA") || hasEnv(runner, "EXTRA") {
		t.Fatal("agents.env is for the agent container")
	}
	for _, c := range []*corev1.Container{runner, agent} {
		if _, ok := mountedAt(c, "exchange"); !ok {
			t.Fatalf("%s must mount the exchange volume", c.Name)
		}
	}
	if !*runner.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("runner root filesystem should be read-only")
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Command[1] != "init-workspace" {
		t.Fatal("sub-paths must be created by an init container running as the agent user")
	}
	if got := agent.Command; len(got) != 2 || got[1] != "agent-server" {
		t.Fatalf("agent container must run the agent server, got %v", got)
	}
}

func TestProjectChangesRequeueItsTasks(t *testing.T) {
	other := newImplTask("other-project-task", 1)
	other.Labels[LabelProject] = "other"
	e := newTaskEnv(t, newImplTask("a", 1), newImplTask("b", 1), other)
	reqs := e.r.tasksOfProject(e.ctx, testProject())
	var names []string
	for _, r := range reqs {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("expected the project's tasks a and b, got %v", names)
	}
}

// Raising maxRunsPerTask on the project unblocks a blocked task.
func TestRaisingRunLimitUnblocks(t *testing.T) {
	task := newImplTask("x", 2)
	task.Status.Run.RunCount = 1
	task.Status.Run.ObservedRunRequest = 1
	task.Status.Run.ActiveRunRequest = 1
	e := newTaskEnv(t, task)
	project := &aiv1.AgentProject{}
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, project); err != nil {
		t.Fatal(err)
	}
	project.Spec.Lifecycle.MaxRunsPerTask = 1
	if err := e.c.Update(e.ctx, project); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseBlocked {
		t.Fatalf("phase %s", got)
	}
	project.Spec.Lifecycle.MaxRunsPerTask = 5
	if err := e.c.Update(e.ctx, project); err != nil {
		t.Fatal(err)
	}
	for _, req := range e.r.tasksOfProject(e.ctx, project) {
		e.reconcile(req.Name)
	}
	var pod corev1.Pod
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseRunning || !e.exists(&pod, "x") {
		t.Fatalf("task should run after raising the limit, phase %s", got)
	}
}

func deferredTask(name string, until time.Time) *aiv1.AgentTask {
	task := newImplTask(name, 1)
	task.Status.Run.DeferredUntil = &metav1.Time{Time: until}
	task.Status.Run.LimitedEngine = aiv1.EngineClaude
	task.Status.Run.Deferrals = 1
	task.Status.Run.LastActivityTime = &metav1.Time{Time: t0}
	return task
}

func TestDeferredTaskWaits(t *testing.T) {
	until := t0.Add(30 * time.Minute)
	e := newTaskEnv(t, deferredTask("x", until))

	res := e.reconcile("x")
	var pod corev1.Pod
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseWaiting {
		t.Fatalf("phase %s", got)
	}
	if e.exists(&pod, "x") {
		t.Fatal("a waiting task must not start a pod")
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > 31*time.Minute {
		t.Fatalf("should requeue at the retry time, got %v", res.RequeueAfter)
	}

	// A waiting task does not take the project's only run slot.
	other := newImplTask("y", 1)
	if err := e.c.Create(e.ctx, other); err != nil {
		t.Fatal(err)
	}
	e.reconcile("y")
	if got := e.task("y").Status.Phase; got != aiv1.TaskPhaseRunning {
		t.Fatalf("other task should run while x waits, phase %s", got)
	}

	// After the retry time the task runs again (once capacity is free).
	e.finishRun("y")
	e.reconcile("y")
	e.now = until.Add(time.Second)
	e.reconcile("x")
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseRunning || !e.exists(&pod, "x") {
		t.Fatalf("task should run after the retry time, phase %s", got)
	}
}

func TestLongWaitHibernatesPod(t *testing.T) {
	until := t0.Add(5 * time.Hour)
	task := deferredTask("x", until)
	e := newTaskEnv(t, task)
	// Simulate the pod that was running when the limit hit.
	if err := e.c.Create(e.ctx, func() *corev1.Pod {
		p := buildPod(task, testProject().Spec.WithDefaults())
		_ = controllerutil.SetControllerReference(e.task("x"), p, e.c.Scheme())
		return p
	}()); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") || e.task("x").Status.Phase != aiv1.TaskPhaseWaiting {
		t.Fatal("the pod stays while idle time is short")
	}
	e.now = t0.Add(3 * time.Hour) // past idleTimeout, before the retry time
	e.reconcile("x")
	if e.exists(&pod, "x") || e.task("x").Status.Phase != aiv1.TaskPhaseWaiting {
		t.Fatal("a long wait should hibernate the pod and keep the task waiting")
	}
}

func TestRunnerKnowsTheAgentLimits(t *testing.T) {
	env := func(pod *corev1.Pod) map[string]string {
		m := map[string]string{}
		for _, e := range container(pod, RunnerContainer).Env {
			m[e.Name] = e.Value
		}
		return m
	}
	spec := testProject().Spec.WithDefaults()
	if e := env(buildPod(newImplTask("x", 1), spec)); e["AI_AGENT_MEMORY_LIMIT"] != "" || e["AI_AGENT_CPUS"] != "" {
		t.Fatalf("no limits, nothing to tell: %v", e)
	}
	spec.Agents.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1125m")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")},
	}
	if e := env(buildPod(newImplTask("x", 1), spec)); e["AI_AGENT_MEMORY_LIMIT"] != "6Gi" || e["AI_AGENT_CPUS"] != "1.125" {
		t.Fatalf("the runner must know the agent's memory limit and CPUs: %v", e)
	}
}

func TestAgentTerminationIsRecorded(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("expected pod")
	}
	container(&pod, AgentContainer).Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}
	if err := e.c.Update(e.ctx, &pod); err != nil {
		t.Fatal(err)
	}
	// The kubelet restarted the agent container after an OOM kill.
	finished := metav1.NewTime(t0.Add(88 * time.Minute))
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: RunnerContainer, Ready: true},
		{Name: AgentContainer, RestartCount: 1, Ready: true,
			State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: finished}}},
	}
	if err := e.c.Status().Update(e.ctx, &pod); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	got := e.task("x").Status.AgentTermination
	if got == nil || !got.OOMKilled() || got.ExitCode != 137 || got.Restarts != 1 || got.MemoryLimit != "4Gi" || !got.FinishedAt.Equal(&finished) {
		t.Fatalf("the agent container's OOM kill must be recorded: %+v", got)
	}
}

func TestPodReplacedWhenAgentResourcesChange(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	e.finishRun("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("expected pod")
	}
	before := pod.Annotations[AnnotationAgentResources]

	// More memory after an OOM kill; the idle pod still has the old limit.
	var project aiv1.AgentProject
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project)
	project.Spec.Agents.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("8Gi")}
	if err := e.c.Update(e.ctx, &project); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	if e.exists(&pod, "x") {
		t.Fatal("an idle pod with old resources must be replaced")
	}
	// The retry starts a pod with the new resources.
	task := e.task("x")
	task.Spec.RunRequest++
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile("x")
	if !e.exists(&pod, "x") || pod.Annotations[AnnotationAgentResources] == before ||
		container(&pod, AgentContainer).Resources.Limits.Memory().String() != "8Gi" {
		t.Fatalf("expected a new pod with 8Gi: %+v", pod.Annotations)
	}
	// Not while a run is in progress.
	task = e.task("x")
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = task.Spec.RunRequest
	_ = e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig))
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project)
	project.Spec.Agents.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("12Gi")}
	_ = e.c.Update(e.ctx, &project)
	e.reconcile("x")
	if !e.exists(&pod, "x") {
		t.Fatal("a pod in a run must not be replaced")
	}
}
