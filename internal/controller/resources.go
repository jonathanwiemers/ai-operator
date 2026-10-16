package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

const (
	// WorkspaceMountPath holds the checkout, the agent's HOME (including its
	// session history, so runs can be resumed) and per-task caches.
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

// buildPod returns the long-running agent pod of a task. The pod runs the
// ai-runner, which waits for run requests on its AgentTask and drives the
// claude CLI for the spec, implement and review roles.
func buildPod(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) *corev1.Pod {
	agents := spec.Agents
	env := []corev1.EnvVar{
		{Name: "AI_TASK_NAME", Value: task.Name},
		{Name: "AI_TASK_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "AI_WORKSPACE", Value: WorkspaceMountPath},
		{Name: "HOME", Value: WorkspaceMountPath + "/home"},
		{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &spec.GitLab.TokenSecretRef}},
	}
	envFrom := []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: agents.CredentialsSecretName}},
	}}
	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: naming.Workspace(task.Name)},
		}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	mounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: WorkspaceMountPath},
		{Name: "tmp", MountPath: "/tmp"},
	}
	if agents.CacheClaimName != "" {
		volumes = append(volumes, corev1.Volume{Name: "cache", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agents.CacheClaimName},
		}})
		mounts = append(mounts, corev1.VolumeMount{Name: "cache", MountPath: CacheMountPath})
		env = append(env,
			corev1.EnvVar{Name: "GOMODCACHE", Value: CacheMountPath + "/go/mod"},
			corev1.EnvVar{Name: "GOCACHE", Value: CacheMountPath + "/go/build"},
			corev1.EnvVar{Name: "npm_config_cache", Value: CacheMountPath + "/npm"},
			corev1.EnvVar{Name: "PIP_CACHE_DIR", Value: CacheMountPath + "/pip"},
			corev1.EnvVar{Name: "YARN_CACHE_FOLDER", Value: CacheMountPath + "/yarn"},
		)
	}
	env = append(env, agents.Env...)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: task.Namespace,
			Labels:    taskLabels(task),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:            agents.ServiceAccountName,
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: ptr.To[int64](60),
			NodeSelector:                  agents.NodeSelector,
			Tolerations:                   agents.Tolerations,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To[int64](AgentUID),
				RunAsGroup:     ptr.To[int64](AgentUID),
				FSGroup:        ptr.To[int64](AgentUID),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            "agent",
				Image:           agents.Image,
				ImagePullPolicy: agents.ImagePullPolicy,
				Command:         []string{"ai-runner"},
				Env:             env,
				EnvFrom:         envFrom,
				Resources:       agents.Resources,
				VolumeMounts:    mounts,
				WorkingDir:      WorkspaceMountPath,
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}
