package controller

import (
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

func testPlan() *aiv1.AgentPlan {
	return &aiv1.AgentPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-20261114-abc123", Namespace: ns, UID: "uid-plan", CreationTimestamp: metav1.NewTime(planT0)},
		Spec:       aiv1.AgentPlanSpec{ProjectRef: "shop", CreatedBy: "owner"},
	}
}

func testPlanSpec() aiv1.AgentProjectSpec {
	spec := testProject().Spec.WithDefaults()
	spec.Agents.StorageClassName = new("fast")
	spec.Agents.WorkspaceSize = resource.MustParse("7Gi")
	spec.Agents.CacheClaimName = "cache"
	spec.Agents.Env = []corev1.EnvVar{{Name: "EXTRA", Value: "1"}}
	return spec
}

// planT0 is when testPlan was created.
var planT0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

var testPlanConfig = PlanConfig{IdleTimeout: 30 * time.Minute, Retention: 720 * time.Hour, MaxBudgetUSD: "5"}

func checkPlanOwner(t *testing.T, meta metav1.ObjectMeta, plan *aiv1.AgentPlan) {
	t.Helper()
	refs := meta.OwnerReferences
	if len(refs) != 1 || refs[0].Kind != "AgentPlan" || refs[0].APIVersion != aiv1.GroupVersion.String() ||
		refs[0].Name != plan.Name || refs[0].UID != plan.UID || refs[0].Controller == nil || !*refs[0].Controller {
		t.Fatalf("must be controlled by the plan: %+v", refs)
	}
}

func TestPlanLabels(t *testing.T) {
	plan := testPlan()
	want := map[string]string{
		"app.kubernetes.io/name":       "ai-agent",
		"app.kubernetes.io/managed-by": "ai-operator",
		LabelProject:                   "shop",
		LabelPlan:                      plan.Name,
	}
	pod, pvc := buildPlanPod(plan, testPlanSpec(), testPlanConfig), buildPlanClaim(plan, testPlanSpec())
	for what, got := range map[string]map[string]string{"labels": planLabels(plan), "pod": pod.Labels, "pvc": pvc.Labels} {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v, want %v (no %s)", what, got, want, LabelTask)
		}
	}
}

func TestPlanClaim(t *testing.T) {
	plan := testPlan()
	pvc := buildPlanClaim(plan, testPlanSpec())
	if pvc.Name != naming.Workspace(plan.Name) || pvc.Namespace != ns {
		t.Fatalf("claim %s/%s", pvc.Namespace, pvc.Name)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Fatalf("storage class: %v", pvc.Spec.StorageClassName)
	}
	if q := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; q.String() != "7Gi" {
		t.Fatalf("size: %s", q.String())
	}
	if !reflect.DeepEqual(pvc.Spec.AccessModes, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}) {
		t.Fatalf("access modes: %v", pvc.Spec.AccessModes)
	}
	checkPlanOwner(t, pvc.ObjectMeta, plan)
}

func TestPlanPod(t *testing.T) {
	plan := testPlan()
	pod := buildPlanPod(plan, testPlanSpec(), testPlanConfig)
	if pod.Name != plan.Name || pod.Namespace != ns {
		t.Fatalf("pod %s/%s", pod.Namespace, pod.Name)
	}
	checkPlanOwner(t, pod.ObjectMeta, plan)
	runner, agent := container(pod, RunnerContainer), container(pod, AgentContainer)
	if runner == nil || agent == nil || len(pod.Spec.Containers) != 2 {
		t.Fatalf("expected runner and agent containers, got %d", len(pod.Spec.Containers))
	}
	if !reflect.DeepEqual(runner.Command, []string{"ai-runner", "plan"}) {
		t.Fatalf("runner command: %v", runner.Command)
	}
	if !reflect.DeepEqual(agent.Command, []string{"ai-runner", "agent-server"}) {
		t.Fatalf("agent command: %v", agent.Command)
	}

	env := envOf(runner)
	if env["AI_PLAN_NAME"].Value != plan.Name {
		t.Fatalf("AI_PLAN_NAME: %+v", env["AI_PLAN_NAME"])
	}
	if f := env["AI_PLAN_NAMESPACE"].ValueFrom; f == nil || f.FieldRef == nil || f.FieldRef.FieldPath != "metadata.namespace" {
		t.Fatalf("AI_PLAN_NAMESPACE must come from the pod's namespace: %+v", env["AI_PLAN_NAMESPACE"])
	}
	for _, name := range []string{"AI_TASK_NAME", "AI_TASK_NAMESPACE", "GITLAB_COMPANION_TOKEN", "AI_DESK_URL"} {
		if _, ok := env[name]; ok {
			t.Fatalf("the plan's runner must not get %s", name)
		}
	}
	if _, ok := pod.Annotations[AnnotationCompanion]; ok {
		t.Fatal("a plan pod has no companion")
	}
	if f := env["GITLAB_TOKEN"].ValueFrom; f == nil || f.SecretKeyRef == nil || f.SecretKeyRef.Name != "gitlab" {
		t.Fatalf("the runner needs the project's GitLab token: %+v", env["GITLAB_TOKEN"])
	}
	for _, e := range agent.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil || e.Name == "GITLAB_TOKEN" {
			t.Fatalf("the agent container must hold no GitLab token: %+v", e)
		}
	}
	if len(runner.EnvFrom) != 0 || len(agent.EnvFrom) != 1 || agent.EnvFrom[0].SecretRef.Name != "agent-creds" {
		t.Fatal("agent CLI credentials belong to the agent container only")
	}
	if envOf(agent)["EXTRA"].Value != "1" {
		t.Fatal("the agent container gets the project's agents.env")
	}
}

func TestPlanPodMounts(t *testing.T) {
	plan := testPlan()
	pod := buildPlanPod(plan, testPlanSpec(), testPlanConfig)
	mounts := func(c *corev1.Container) map[string]string {
		m := map[string]string{}
		for _, v := range c.VolumeMounts {
			m[v.Name] = v.MountPath + ":" + v.SubPath
		}
		return m
	}
	if got, want := mounts(container(pod, RunnerContainer)), map[string]string{
		"workspace": RunnerMountPath + ":runner", "exchange": ExchangeMountPath + ":",
		"runner-tmp": "/tmp:", "kube-api-access": ServiceAccountMountPath + ":",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runner mounts: got %v, want %v", got, want)
	}
	if got, want := mounts(container(pod, AgentContainer)), map[string]string{
		"workspace": WorkspaceMountPath + ":agent", "exchange": ExchangeMountPath + ":",
		"agent-tmp": "/tmp:", "cache": CacheMountPath + ":",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agent mounts: got %v, want %v (no service account token)", got, want)
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == "workspace" && v.PersistentVolumeClaim.ClaimName != naming.Workspace(plan.Name) {
			t.Fatalf("workspace claim: %s", v.PersistentVolumeClaim.ClaimName)
		}
	}
}

// TestPlanPodSharesTaskLayout keeps plan and task pods from drifting apart:
// everything but the runner's command and identity env is the same.
func TestPlanPodSharesTaskLayout(t *testing.T) {
	plan := testPlan()
	spec := testPlanSpec()
	planPod := buildPlanPod(plan, spec, testPlanConfig)
	taskPod := buildPod(newImplTask(plan.Name, 1), spec, "", nil)
	if !reflect.DeepEqual(planPod.Spec.Volumes, taskPod.Spec.Volumes) ||
		!reflect.DeepEqual(planPod.Spec.InitContainers, taskPod.Spec.InitContainers) ||
		!reflect.DeepEqual(*container(planPod, AgentContainer), *container(taskPod, AgentContainer)) {
		t.Fatal("plan and task pods must share volumes, init and agent containers")
	}
	pr, tr := *container(planPod, RunnerContainer), *container(taskPod, RunnerContainer)
	if !reflect.DeepEqual(pr.VolumeMounts, tr.VolumeMounts) || !reflect.DeepEqual(pr.SecurityContext, tr.SecurityContext) {
		t.Fatal("plan and task runners must share mounts and security context")
	}
	if planPod.Spec.ServiceAccountName != taskPod.Spec.ServiceAccountName ||
		!reflect.DeepEqual(planPod.Spec.SecurityContext, taskPod.Spec.SecurityContext) ||
		planPod.Spec.RestartPolicy != taskPod.Spec.RestartPolicy {
		t.Fatal("plan and task pods must share the pod spec")
	}
}

func TestPlanPodBudget(t *testing.T) {
	plan := testPlan()
	budget := func() string {
		return envOf(container(buildPlanPod(plan, testPlanSpec(), testPlanConfig), RunnerContainer))["AI_PLAN_MAX_BUDGET_USD"].Value
	}
	if got := budget(); got != "5" {
		t.Fatalf("without spec.maxBudgetUSD the operator default applies, got %q", got)
	}
	plan.Spec.MaxBudgetUSD = "12.50"
	if got := budget(); got != "12.50" {
		t.Fatalf("a raised budget applies, got %q", got)
	}
}
