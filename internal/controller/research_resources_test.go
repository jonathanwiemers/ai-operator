package controller

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research"
)

const testRunID = "1a2b3c4d-0000-4000-8000-000000000000"

func testResearchPod(t *testing.T, depth aiv1.ResearchDepth, mutate func(*aiv1.AgentProjectSpec)) *corev1.Pod {
	t.Helper()
	spec := researchProject().Spec.WithDefaults()
	spec.Research.WriterSecret = &aiv1.SecretLogin{Name: "writer", UsernameKey: "user"}
	if mutate != nil {
		mutate(&spec)
	}
	r := newResearch("morning-news", "0 7 * * *")
	r.Spec.Depth = depth
	db := research.DatabaseConfig{Host: "pg.example", Port: 5433, Name: "ai_operator", SSLMode: "verify-full"}
	return buildResearchPod(r, spec, testRunID, db)
}

func envOf(c *corev1.Container) map[string]corev1.EnvVar {
	m := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		m[e.Name] = e
	}
	return m
}

// TestResearchPodCredentials pins down who gets which login: the runner the
// project's database writer, the agent only its CLI credentials, nobody a
// GitLab token.
func TestResearchPodCredentials(t *testing.T) {
	pod := testResearchPod(t, aiv1.ResearchDepthQuick, nil)
	runner, agent := container(pod, RunnerContainer), container(pod, AgentContainer)
	if runner == nil || agent == nil || len(pod.Spec.Containers) != 2 {
		t.Fatalf("expected runner and agent containers, got %d", len(pod.Spec.Containers))
	}
	if got := runner.Command; !reflect.DeepEqual(got, []string{"ai-runner", "research"}) {
		t.Errorf("runner command %v", got)
	}

	env := envOf(runner)
	for name, want := range map[string]string{
		"AI_RESEARCH_NAME": "morning-news", "AI_RESEARCH_RUN_ID": testRunID, "AI_AGENT_SOCKET": AgentSocket,
		"AI_RESEARCH_DB_HOST": "pg.example", "AI_RESEARCH_DB_PORT": "5433",
		"AI_RESEARCH_DB_NAME": "ai_operator", "AI_RESEARCH_DB_SSLMODE": "verify-full",
	} {
		if env[name].Value != want {
			t.Errorf("runner %s = %q, want %q", name, env[name].Value, want)
		}
	}
	if f := env["AI_RESEARCH_NAMESPACE"].ValueFrom; f == nil || f.FieldRef == nil || f.FieldRef.FieldPath != "metadata.namespace" {
		t.Errorf("namespace %+v", env["AI_RESEARCH_NAMESPACE"])
	}
	for name, key := range map[string]string{"AI_RESEARCH_DB_USER": "user", "AI_RESEARCH_DB_PASSWORD": "password"} {
		v := env[name]
		if v.Value != "" || v.ValueFrom == nil || v.ValueFrom.SecretKeyRef == nil ||
			v.ValueFrom.SecretKeyRef.Name != "writer" || v.ValueFrom.SecretKeyRef.Key != key {
			t.Errorf("runner %s must come from the writer Secret's key %q: %+v", name, key, v)
		}
	}
	if len(runner.EnvFrom) != 0 {
		t.Error("the runner gets no agent credentials")
	}

	for _, c := range []*corev1.Container{runner, agent} {
		if _, ok := envOf(c)["GITLAB_TOKEN"]; ok {
			t.Errorf("%s has a GitLab token", c.Name)
		}
	}
	for _, e := range agent.Env {
		if strings.HasPrefix(e.Name, "AI_RESEARCH_") || e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			t.Errorf("the agent container must not get %s", e.Name)
		}
	}
	for _, m := range agent.VolumeMounts {
		if m.Name == "kube-api-access" || m.Name == "runner" {
			t.Errorf("the agent container must not mount %s", m.Name)
		}
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("the service account token is mounted into the runner only")
	}
}

// TestResearchPodAgentLikeTask: the agent container is a task's, without the
// cache.
func TestResearchPodAgentLikeTask(t *testing.T) {
	mutate := func(spec *aiv1.AgentProjectSpec) {
		spec.Agents.CacheClaimName = "cache"
		spec.Agents.LogLevel = aiv1.LogLevelDebug
		spec.Agents.Env = []corev1.EnvVar{{Name: "EXTRA", Value: "1"}}
		spec.Agents.Resources = corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")}}
	}
	pod := testResearchPod(t, aiv1.ResearchDepthQuick, mutate)
	agent := container(pod, AgentContainer)
	spec := researchProject().Spec.WithDefaults()
	mutate(&spec)
	task := container(buildPod(newImplTask("x", 1), spec, "", nil), AgentContainer)

	if agent.Image != task.Image || !reflect.DeepEqual(agent.EnvFrom, task.EnvFrom) ||
		!reflect.DeepEqual(agent.Resources, task.Resources) || !reflect.DeepEqual(agent.Command, task.Command) ||
		!reflect.DeepEqual(agent.SecurityContext, task.SecurityContext) {
		t.Errorf("agent container differs from a task's:\n%+v\n%+v", agent, task)
	}
	env := envOf(agent)
	if _, ok := env["GOMODCACHE"]; ok || env["EXTRA"].Value != "1" || env["AI_LOG_LEVEL"].Value != "debug" {
		t.Errorf("agent env %v", agent.Env)
	}
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			t.Errorf("volume %s is a PVC; research pods use emptyDirs", v.Name)
		}
	}
	if len(pod.Spec.InitContainers) != 0 {
		t.Error("no workspace to prepare")
	}
}

func TestResearchPodLifecycle(t *testing.T) {
	for depth, want := range map[aiv1.ResearchDepth]int64{
		aiv1.ResearchDepthQuick: 22 * 60,
		aiv1.ResearchDepthDeep:  122 * 60,
	} {
		pod := testResearchPod(t, depth, nil)
		if d := pod.Spec.ActiveDeadlineSeconds; d == nil || *d != want {
			t.Errorf("%s: deadline %v, want %d", depth, d, want)
		}
	}
	pod := testResearchPod(t, aiv1.ResearchDepthQuick, nil)
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy %s", pod.Spec.RestartPolicy)
	}
	if pod.Name != "morning-news-1a2b3c4d" || pod.Namespace != ns {
		t.Errorf("pod %s/%s", pod.Namespace, pod.Name)
	}
	want := map[string]string{"app.kubernetes.io/name": "ai-agent", "app.kubernetes.io/managed-by": "ai-operator",
		LabelProject: "shop", aiv1.LabelResearch: "morning-news"}
	if !reflect.DeepEqual(pod.Labels, want) {
		t.Errorf("labels %v", pod.Labels)
	}
	if _, ok := pod.Labels[LabelTask]; ok {
		t.Error("a research pod is no task pod")
	}
}
