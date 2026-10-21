package controller

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

const (
	// WorkspaceMountPath is the agent's part of the workspace PVC: the
	// checkout, the agent's HOME (including CLI sessions, so runs can be
	// resumed) and per-task caches.
	WorkspaceMountPath = "/workspace"
	// CacheMountPath holds dependency caches shared by all tasks.
	CacheMountPath = "/cache"
	// AgentUID is the user the agent runs as.
	AgentUID = 1000
)

func taskLabels(task *aiv1.AgentTask) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ai-agent",
		"app.kubernetes.io/managed-by": "ai-operator",
		LabelProject:                   task.Spec.ProjectRef,
		LabelTask:                      task.Name,
		LabelType:                      task.Labels[LabelType],
	}
}

// buildWorkspaceClaim returns the per-task workspace PVC.
func buildWorkspaceClaim(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.Workspace(task.Name),
			Namespace: task.Namespace,
			Labels:    taskLabels(task),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: spec.Agents.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: spec.Agents.WorkspaceSize},
			},
		},
	}
}

// Container and volume layout of an agent pod.
const (
	// RunnerContainer holds the GitLab token and the Kubernetes token.
	RunnerContainer = "runner"
	// AgentContainer runs the agent CLIs with only their own credentials.
	AgentContainer = "agent"

	// RunnerMountPath is the runner's private part of the workspace PVC:
	// the git mirror and runner state.
	RunnerMountPath = "/runner"
	// ExchangeMountPath is shared by both containers: the agent server's
	// socket and git bundles in transit.
	ExchangeMountPath = "/exchange"
	// AgentSocket is where the agent server listens.
	AgentSocket = ExchangeMountPath + "/agent.sock"
	// ServiceAccountMountPath is where client-go expects the token.
	ServiceAccountMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// DefaultRunnerResources is used when agents.runnerResources is not set. The
// runner only talks to GitLab and Kubernetes and moves git bundles.
var DefaultRunnerResources = corev1.ResourceRequirements{
	Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("50m"),
		corev1.ResourceMemory: resource.MustParse("128Mi"),
	},
	Limits: corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("1Gi"),
	},
}

// buildPod returns the long-running pod of a task. It has two containers that
// share the task's PVC through different sub-paths:
//
//   - runner: ai-runner with the GitLab token and the only mount of the
//     service account token. It owns the git mirror and talks to GitLab.
//   - agent: "ai-runner agent-server" with the agent CLI credentials. It owns
//     the checkout the model works in and runs the agent CLIs on request.
//
// Containers of a pod do not share a process namespace, so the model cannot
// read the runner's environment either.
func buildPod(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) *corev1.Pod {
	agents := spec.Agents
	claim := naming.Workspace(task.Name)
	runnerEnv := []corev1.EnvVar{
		{Name: "AI_TASK_NAME", Value: task.Name},
		{Name: "AI_TASK_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "AI_STATE_DIR", Value: RunnerMountPath},
		{Name: "AI_EXCHANGE", Value: ExchangeMountPath},
		{Name: "AI_AGENT_SOCKET", Value: AgentSocket},
		{Name: "HOME", Value: RunnerMountPath},
		{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &spec.GitLab.TokenSecretRef}},
	}
	runnerMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: RunnerMountPath, SubPath: "runner"},
		{Name: "exchange", MountPath: ExchangeMountPath},
		{Name: "runner-tmp", MountPath: "/tmp"},
		{Name: "kube-api-access", MountPath: ServiceAccountMountPath, ReadOnly: true},
	}

	agentEnv := []corev1.EnvVar{
		{Name: "AI_WORKSPACE", Value: WorkspaceMountPath},
		{Name: "AI_EXCHANGE", Value: ExchangeMountPath},
		{Name: "AI_AGENT_SOCKET", Value: AgentSocket},
		{Name: "HOME", Value: WorkspaceMountPath + "/home"},
	}
	agentMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: WorkspaceMountPath, SubPath: "agent"},
		{Name: "exchange", MountPath: ExchangeMountPath},
		{Name: "agent-tmp", MountPath: "/tmp"},
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
		}},
		{Name: "exchange", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "runner-tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "agent-tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		serviceAccountTokenVolume(),
	}
	if agents.CacheClaimName != "" {
		volumes = append(volumes, corev1.Volume{Name: "cache", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agents.CacheClaimName},
		}})
		agentMounts = append(agentMounts, corev1.VolumeMount{Name: "cache", MountPath: CacheMountPath})
		agentEnv = append(agentEnv,
			corev1.EnvVar{Name: "GOMODCACHE", Value: CacheMountPath + "/go/mod"},
			corev1.EnvVar{Name: "GOCACHE", Value: CacheMountPath + "/go/build"},
			corev1.EnvVar{Name: "npm_config_cache", Value: CacheMountPath + "/npm"},
			corev1.EnvVar{Name: "PIP_CACHE_DIR", Value: CacheMountPath + "/pip"},
			corev1.EnvVar{Name: "YARN_CACHE_FOLDER", Value: CacheMountPath + "/yarn"},
		)
	}
	if agents.LogLevel != "" {
		level := corev1.EnvVar{Name: "AI_LOG_LEVEL", Value: string(agents.LogLevel)}
		runnerEnv = append(runnerEnv, level)
		agentEnv = append(agentEnv, level)
	}
	agentEnv = append(agentEnv, agents.Env...)

	// The runner tells the implementer what the agent container may use.
	agentResources := agents.Resources
	if q, ok := agentResources.Limits[corev1.ResourceMemory]; ok {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "AI_AGENT_MEMORY_LIMIT", Value: q.String()})
	}
	cpu, ok := agentResources.Limits[corev1.ResourceCPU]
	if !ok {
		cpu, ok = agentResources.Requests[corev1.ResourceCPU]
	}
	if ok {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "AI_AGENT_CPUS", Value: strconv.FormatFloat(cpu.AsApproximateFloat64(), 'g', 3, 64)})
	}

	runnerResources := DefaultRunnerResources
	if agents.RunnerResources != nil {
		runnerResources = *agents.RunnerResources
	}
	containerSecurity := func(readOnlyRoot bool) *corev1.SecurityContext {
		return &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(readOnlyRoot),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: task.Namespace,
			Labels:    taskLabels(task),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: agents.ServiceAccountName,
			// The token is mounted explicitly into the runner container only.
			AutomountServiceAccountToken:  ptr.To(false),
			EnableServiceLinks:            ptr.To(false),
			ShareProcessNamespace:         ptr.To(false),
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: ptr.To[int64](60),
			NodeSelector:                  agents.NodeSelector,
			Tolerations:                   agents.Tolerations,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				RunAsUser:    ptr.To[int64](AgentUID),
				RunAsGroup:   ptr.To[int64](AgentUID),
				FSGroup:      ptr.To[int64](AgentUID),
				// Pods start for every run; only fix up ownership when the
				// volume root is off, not for every file on every mount.
				FSGroupChangePolicy: ptr.To(corev1.FSGroupChangeOnRootMismatch),
				SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Volumes: volumes,
			// Kubelet would create missing sub-path directories as root;
			// create them as the agent user first.
			InitContainers: []corev1.Container{{
				Name:            "init-workspace",
				Image:           agents.Image,
				ImagePullPolicy: agents.ImagePullPolicy,
				Command:         []string{"ai-runner", "init-workspace", "/data/runner", "/data/agent"},
				VolumeMounts:    []corev1.VolumeMount{{Name: "workspace", MountPath: "/data"}},
				Resources:       runnerResources,
				SecurityContext: containerSecurity(true),
			}},
			Containers: []corev1.Container{
				{
					Name:            RunnerContainer,
					Image:           agents.Image,
					ImagePullPolicy: agents.ImagePullPolicy,
					Command:         []string{"ai-runner"},
					Env:             runnerEnv,
					Resources:       runnerResources,
					VolumeMounts:    runnerMounts,
					WorkingDir:      RunnerMountPath,
					SecurityContext: containerSecurity(true),
				},
				{
					Name:            AgentContainer,
					Image:           agents.Image,
					ImagePullPolicy: agents.ImagePullPolicy,
					Command:         []string{"ai-runner", "agent-server"},
					Env:             agentEnv,
					EnvFrom: []corev1.EnvFromSource{{
						SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: agents.CredentialsSecretName}},
					}},
					Resources:       agentResources,
					VolumeMounts:    agentMounts,
					WorkingDir:      WorkspaceMountPath,
					SecurityContext: containerSecurity(false),
				},
			},
		},
	}
}

// serviceAccountTokenVolume is the equivalent of the automatically mounted
// kube-api-access volume, so it can be mounted into one container only.
func serviceAccountTokenVolume() corev1.Volume {
	return corev1.Volume{Name: "kube-api-access", VolumeSource: corev1.VolumeSource{
		Projected: &corev1.ProjectedVolumeSource{
			DefaultMode: ptr.To[int32](0o444),
			Sources: []corev1.VolumeProjection{
				{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token", ExpirationSeconds: ptr.To[int64](3607)}},
				{ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"},
					Items:                []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
				}},
				{DownwardAPI: &corev1.DownwardAPIProjection{Items: []corev1.DownwardAPIVolumeFile{{
					Path: "namespace", FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
				}}}},
			},
		},
	}}
}
