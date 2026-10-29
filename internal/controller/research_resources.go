package controller

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/research"
)

// researchDeadlineSlack is how much longer than its depth's timeout a run's
// pod may live, for the runner to record the outcome.
const researchDeadlineSlack = 2 * time.Minute

// researchLabels selects the pods of a research. The research label lets the
// chart's research NetworkPolicy reach the database from these pods only.
func researchLabels(r *aiv1.AgentResearch) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ai-agent",
		"app.kubernetes.io/managed-by": "ai-operator",
		LabelProject:                   r.Spec.ProjectRef,
		aiv1.LabelResearch:             naming.LabelValue(r.Name),
	}
}

// buildResearchPod returns the pod of one research run. Like a task's pod it
// has a runner and an agent container, but no git, no GitLab token and no
// workspace: its volumes are emptyDirs, and it ends with the run.
//
//   - runner: "ai-runner research" with the service account token and the
//     project's database writer login, which reads the research, calls the
//     agent and writes the result to the database.
//   - agent: "ai-runner agent-server" with the agent CLI credentials only.
func buildResearchPod(r *aiv1.AgentResearch, spec aiv1.AgentProjectSpec, runID string, db research.DatabaseConfig) *corev1.Pod {
	agents := spec.Agents
	// The controller checked the writer login before.
	login := spec.Research.WriterSecret.WithDefaults()
	secretKey := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: login.Name}, Key: key,
		}}
	}
	runnerEnv := []corev1.EnvVar{
		{Name: "AI_RESEARCH_NAME", Value: r.Name},
		{Name: "AI_RESEARCH_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "AI_RESEARCH_RUN_ID", Value: runID},
		{Name: "AI_STATE_DIR", Value: RunnerMountPath},
		{Name: "AI_EXCHANGE", Value: ExchangeMountPath},
		{Name: "AI_AGENT_SOCKET", Value: AgentSocket},
		{Name: "HOME", Value: RunnerMountPath},
		{Name: "AI_RESEARCH_DB_HOST", Value: db.Host},
		{Name: "AI_RESEARCH_DB_PORT", Value: strconv.Itoa(db.Port)},
		{Name: "AI_RESEARCH_DB_NAME", Value: db.Name},
		{Name: "AI_RESEARCH_DB_SSLMODE", Value: db.SSLMode},
		{Name: "AI_RESEARCH_DB_USER", ValueFrom: secretKey(login.UsernameKey)},
		{Name: "AI_RESEARCH_DB_PASSWORD", ValueFrom: secretKey(login.PasswordKey)},
	}
	runnerEnv = append(runnerEnv, logLevelEnv(agents)...)
	runnerMounts := []corev1.VolumeMount{
		{Name: "runner", MountPath: RunnerMountPath},
		{Name: "exchange", MountPath: ExchangeMountPath},
		{Name: "runner-tmp", MountPath: "/tmp"},
		{Name: "kube-api-access", MountPath: ServiceAccountMountPath, ReadOnly: true},
	}
	agentMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: WorkspaceMountPath},
		{Name: "exchange", MountPath: ExchangeMountPath},
		{Name: "agent-tmp", MountPath: "/tmp"},
	}
	emptyDir := func(name string) corev1.Volume {
		return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
	}

	podSpec := agentPodSpec(agents)
	podSpec.RestartPolicy = corev1.RestartPolicyNever
	podSpec.ActiveDeadlineSeconds = ptr.To(int64((r.Spec.Timeout() + researchDeadlineSlack).Seconds()))
	podSpec.Volumes = []corev1.Volume{
		emptyDir("runner"), emptyDir("workspace"), emptyDir("exchange"),
		emptyDir("runner-tmp"), emptyDir("agent-tmp"),
		serviceAccountTokenVolume(),
	}
	podSpec.Containers = []corev1.Container{
		runnerContainer(agents, []string{"ai-runner", "research"}, runnerEnv, runnerMounts),
		agentContainer(agents, agentEnv(agents), agentMounts, *agents.Resources.DeepCopy()),
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ResearchPod(r.Name, runID),
			Namespace: r.Namespace,
			Labels:    researchLabels(r),
		},
		Spec: podSpec,
	}
}
