package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// targets are the project's valid companion targets (crossRepoTargets): a
// task's companion gets its token (GITLAB_COMPANION_TOKEN) in the runner
// container only while its project is one of them.
func buildPod(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec, deskURL string, targets []aiv1.CrossRepoTarget) *corev1.Pod {
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
		// The agent container's memory limit as this pod has it, in bytes.
		{Name: "AI_AGENT_MEMORY_BYTES", ValueFrom: &corev1.EnvVarSource{
			ResourceFieldRef: &corev1.ResourceFieldSelector{ContainerName: AgentContainer, Resource: "limits.memory"},
		}},
	}
	runnerMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: RunnerMountPath, SubPath: "runner"},
		{Name: "exchange", MountPath: ExchangeMountPath},
		{Name: "runner-tmp", MountPath: "/tmp"},
		{Name: "kube-api-access", MountPath: ServiceAccountMountPath, ReadOnly: true},
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
	var cacheEnv []corev1.EnvVar
	if agents.CacheClaimName != "" {
		volumes = append(volumes, corev1.Volume{Name: "cache", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agents.CacheClaimName},
		}})
		agentMounts = append(agentMounts, corev1.VolumeMount{Name: "cache", MountPath: CacheMountPath})
		cacheEnv = []corev1.EnvVar{
			{Name: "GOMODCACHE", Value: CacheMountPath + "/go/mod"},
			{Name: "GOCACHE", Value: CacheMountPath + "/go/build"},
			{Name: "npm_config_cache", Value: CacheMountPath + "/npm"},
			{Name: "PIP_CACHE_DIR", Value: CacheMountPath + "/pip"},
			{Name: "YARN_CACHE_FOLDER", Value: CacheMountPath + "/yarn"},
		}
	}
	runnerEnv = append(runnerEnv, logLevelEnv(agents)...)
	annotations := map[string]string{}
	if ref, project := companionToken(task, spec, targets); ref != nil {
		// The runner only: the agent container never holds a GitLab token.
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "GITLAB_COMPANION_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: ref}})
		annotations[AnnotationCompanion] = project
	}
	if deskURL != "" {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "AI_DESK_URL", Value: deskURL})
	}

	// The runner tells the implementer what the agent container may use.
	agentRes := agentResources(task, spec)
	if q, ok := agentRes.Limits[corev1.ResourceMemory]; ok {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "AI_AGENT_MEMORY_LIMIT", Value: q.String()})
	}
	cpu, ok := agentRes.Limits[corev1.ResourceCPU]
	if !ok {
		cpu, ok = agentRes.Requests[corev1.ResourceCPU]
	}
	if ok {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "AI_AGENT_CPUS", Value: strconv.FormatFloat(float64(cpu.MilliValue())/1000, 'f', -1, 64)})
	}

	annotations[AnnotationAgentResources] = resourcesHash(agentRes)

	initResources := runnerResources(agents)
	podSpec := agentPodSpec(agents)
	podSpec.RestartPolicy = corev1.RestartPolicyAlways
	podSpec.Volumes = volumes
	// Kubelet would create missing sub-path directories as root; create them
	// as the agent user first.
	podSpec.InitContainers = []corev1.Container{{
		Name:            "init-workspace",
		Image:           agents.Image,
		ImagePullPolicy: agents.ImagePullPolicy,
		Command:         []string{"ai-runner", "init-workspace", "/data/runner", "/data/agent"},
		VolumeMounts:    []corev1.VolumeMount{{Name: "workspace", MountPath: "/data"}},
		Resources:       initResources,
		SecurityContext: containerSecurity(true),
	}}
	podSpec.Containers = []corev1.Container{
		runnerContainer(agents, []string{"ai-runner"}, runnerEnv, runnerMounts),
		agentContainer(agents, agentEnv(agents, cacheEnv...), agentMounts, agentRes),
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        task.Name,
			Namespace:   task.Namespace,
			Labels:      taskLabels(task),
			Annotations: annotations,
		},
		Spec: podSpec,
	}
}

// agentPodSpec is what the pods of tasks and research runs share: the
// project's service account and scheduling, the agent user, and no
// automatically mounted token, service links or shared process namespace.
func agentPodSpec(agents aiv1.AgentsSpec) corev1.PodSpec {
	return corev1.PodSpec{
		ServiceAccountName: agents.ServiceAccountName,
		// The token is mounted explicitly into the runner container only.
		AutomountServiceAccountToken:  ptr.To(false),
		EnableServiceLinks:            ptr.To(false),
		ShareProcessNamespace:         ptr.To(false),
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
	}
}

// runnerContainer runs ai-runner with command; it is the only container with
// the service account token.
func runnerContainer(agents aiv1.AgentsSpec, command []string, env []corev1.EnvVar, mounts []corev1.VolumeMount) corev1.Container {
	return corev1.Container{
		Name:            RunnerContainer,
		Image:           agents.Image,
		ImagePullPolicy: agents.ImagePullPolicy,
		Command:         command,
		Env:             env,
		Resources:       runnerResources(agents),
		VolumeMounts:    mounts,
		WorkingDir:      RunnerMountPath,
		SecurityContext: containerSecurity(true),
	}
}

// agentContainer runs the agent server with only the agent CLI credentials.
func agentContainer(agents aiv1.AgentsSpec, env []corev1.EnvVar, mounts []corev1.VolumeMount, res corev1.ResourceRequirements) corev1.Container {
	return corev1.Container{
		Name:            AgentContainer,
		Image:           agents.Image,
		ImagePullPolicy: agents.ImagePullPolicy,
		Command:         []string{"ai-runner", "agent-server"},
		Env:             env,
		EnvFrom: []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: agents.CredentialsSecretName}},
		}},
		Resources:       res,
		VolumeMounts:    mounts,
		WorkingDir:      WorkspaceMountPath,
		SecurityContext: containerSecurity(false),
	}
}

// agentEnv is the agent container's environment: extra goes before the log
// level and the project's agents.env.
func agentEnv(agents aiv1.AgentsSpec, extra ...corev1.EnvVar) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "AI_WORKSPACE", Value: WorkspaceMountPath},
		{Name: "AI_EXCHANGE", Value: ExchangeMountPath},
		{Name: "AI_AGENT_SOCKET", Value: AgentSocket},
		{Name: "HOME", Value: WorkspaceMountPath + "/home"},
	}
	env = append(env, extra...)
	env = append(env, logLevelEnv(agents)...)
	return append(env, agents.Env...)
}

func logLevelEnv(agents aiv1.AgentsSpec) []corev1.EnvVar {
	if agents.LogLevel == "" {
		return nil
	}
	return []corev1.EnvVar{{Name: "AI_LOG_LEVEL", Value: string(agents.LogLevel)}}
}

func runnerResources(agents aiv1.AgentsSpec) corev1.ResourceRequirements {
	if agents.RunnerResources != nil {
		return *agents.RunnerResources
	}
	return DefaultRunnerResources
}

func containerSecurity(readOnlyRoot bool) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(readOnlyRoot),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
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

// AnnotationAgentResources holds a hash of the agent container's resources,
// so a pod started with other ones is replaced between runs: the resources of
// a running container cannot change.
const AnnotationAgentResources = "ai.wiemers.me/agent-resources"

func resourcesHash(r corev1.ResourceRequirements) string {
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// AnnotationCompanion on a task's pod is the companion project whose token
// the pod has; absent without one.
const AnnotationCompanion = "ai.wiemers.me/companion"

// companionToken is the secret of the task's companion token and the
// companion's project: the target's tokenSecretRef, else the project's
// token. Nil without spec.companion, and when its project is no longer one
// of the valid targets: the pod then has no companion token, and the
// runner leaves the companion out of the run with a notice.
func companionToken(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec, targets []aiv1.CrossRepoTarget) (*corev1.SecretKeySelector, string) {
	c := task.Spec.Companion
	if c == nil {
		return nil, ""
	}
	t, ok := aiv1.FindCrossRepoTarget(targets, c.Project)
	if !ok {
		return nil, ""
	}
	project := aiv1.NormalizeProject(t.Project)
	if t.TokenSecretRef != nil {
		return t.TokenSecretRef.DeepCopy(), project
	}
	return spec.GitLab.TokenSecretRef.DeepCopy(), project
}

// podOutdated reports whether pod was started with other agent resources than
// spec asks for now (pods from before the annotation count as current), or
// with another companion token than the task needs now.
func podOutdated(pod *corev1.Pod, task *aiv1.AgentTask, spec aiv1.AgentProjectSpec, targets []aiv1.CrossRepoTarget) bool {
	_, companion := companionToken(task, spec, targets)
	if pod.Annotations[AnnotationCompanion] != companion {
		return true
	}
	h, ok := pod.Annotations[AnnotationAgentResources]
	return ok && h != resourcesHash(agentResources(task, spec))
}

// agentResources are the agent container's resources for task: the
// project's, with the memory limit the operator raised for the task after OOM
// kills while that is higher. A memory request grows in proportion.
func agentResources(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) corev1.ResourceRequirements {
	res := *spec.Agents.Resources.DeepCopy()
	// The cap applies as it is now: lowering it (or the limit) lowers a
	// raise made before.
	raised, ok := spec.Agents.EffectiveMemory(task.Status.AgentMemoryLimit)
	if !ok {
		return res
	}
	limit := res.Limits[corev1.ResourceMemory]
	res.Limits[corev1.ResourceMemory] = raised
	if req, ok := res.Requests[corev1.ResourceMemory]; ok && limit.Value() > 0 {
		scaled := int64(float64(req.Value()) * float64(raised.Value()) / float64(limit.Value()))
		res.Requests[corev1.ResourceMemory] = *resource.NewQuantity(scaled/(1<<20)*(1<<20), resource.BinarySI)
	}
	return res
}
