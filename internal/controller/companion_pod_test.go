package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// TestCompanionTokenOnlyInTheRunner: a task with spec.companion gets
// GITLAB_COMPANION_TOKEN from its target's secret, else the project's, in
// the runner container only; without a companion, or with a target that is
// no longer valid, there is none.
func TestCompanionTokenOnlyInTheRunner(t *testing.T) {
	allowed := []string{"group/infra", "group/other"}
	own := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "infra-token"}, Key: "token"}
	spec := testProject().Spec.WithDefaults()
	spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
		{Project: "group/infra", Paths: []string{"clusters/app/"}, TokenSecretRef: own},
		{Project: "group/other", Paths: []string{"apps/"}},
	}}
	targets := crossRepoTargets(spec, allowed)
	withCompanion := func(project string) *aiv1.AgentTask {
		task := newImplTask("x", 1)
		task.Spec.Companion = &aiv1.CompanionSpec{Project: project, Paths: []string{"clusters/app/"}, Branch: "agent/app-1-x"}
		return task
	}
	token := func(c *corev1.Container) *corev1.EnvVar {
		for i, e := range c.Env {
			if e.Name == "GITLAB_COMPANION_TOKEN" {
				return &c.Env[i]
			}
		}
		return nil
	}

	for _, tc := range []struct {
		name, project string
		want          *corev1.SecretKeySelector
	}{
		{"the target's own secret", "group/infra", own},
		{"the project's token by default", "Group/Other", &spec.GitLab.TokenSecretRef},
	} {
		pod := buildPod(withCompanion(tc.project), spec, "", targets)
		env := token(container(pod, RunnerContainer))
		if env == nil || env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil || *env.ValueFrom.SecretKeyRef != *tc.want {
			t.Fatalf("%s: runner GITLAB_COMPANION_TOKEN = %+v, want %+v", tc.name, env, tc.want)
		}
		if token(container(pod, AgentContainer)) != nil {
			t.Fatalf("%s: the agent container must never get the companion token", tc.name)
		}
		for _, c := range append(pod.Spec.Containers, pod.Spec.InitContainers...) {
			if c.Name != RunnerContainer && token(&c) != nil {
				t.Fatalf("%s: container %s has the companion token", tc.name, c.Name)
			}
		}
		if pod.Annotations[AnnotationCompanion] == "" || pod.Annotations[AnnotationAgentResources] == "" {
			t.Fatalf("%s: annotations %v", tc.name, pod.Annotations)
		}
	}

	// Without a companion, and with a target that is no longer valid (the
	// cluster dropped it): no token, no annotation.
	for name, pod := range map[string]*corev1.Pod{
		"no companion":   buildPod(newImplTask("x", 1), spec, "", targets),
		"invalid target": buildPod(withCompanion("group/infra"), spec, "", crossRepoTargets(spec, []string{"group/other"})),
		"unknown target": buildPod(withCompanion("group/elsewhere"), spec, "", targets),
	} {
		for _, c := range pod.Spec.Containers {
			if token(&c) != nil {
				t.Fatalf("%s: container %s has GITLAB_COMPANION_TOKEN", name, c.Name)
			}
		}
		if _, ok := pod.Annotations[AnnotationCompanion]; ok {
			t.Fatalf("%s: companion annotation %v", name, pod.Annotations)
		}
	}
}

// TestPodOutdatedByCompanion: a pod is replaced between runs when the
// companion token it needs changed; pods without a companion stay.
func TestPodOutdatedByCompanion(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{{Project: "group/infra", Paths: []string{"clusters/app/"}}}}
	targets := crossRepoTargets(spec, []string{"group/infra"})
	plain := newImplTask("x", 1)
	companion := newImplTask("x", 1)
	companion.Spec.Companion = &aiv1.CompanionSpec{Project: "group/infra", Paths: []string{"clusters/app/"}, Branch: "agent/app-1-x"}

	if podOutdated(buildPod(plain, spec, "", targets), plain, spec, targets) {
		t.Fatal("a pod without a companion is current")
	}
	if podOutdated(buildPod(companion, spec, "", targets), companion, spec, targets) {
		t.Fatal("a pod with the companion's token is current")
	}
	if !podOutdated(buildPod(plain, spec, "", targets), companion, spec, targets) {
		t.Fatal("a companion added after the pod started needs a pod with its token")
	}
	if !podOutdated(buildPod(companion, spec, "", targets), plain, spec, targets) {
		t.Fatal("a companion removed needs a pod without its token")
	}
	if !podOutdated(buildPod(companion, spec, "", targets), companion, spec, nil) {
		t.Fatal("a target no longer valid needs a pod without its token")
	}
}

// TestCompanionTokenKeptForTheRecordedCompanion: a task that lost
// spec.companion while status.run.companion still records its merge request
// keeps the token, so its next run can close the merge request; once the
// runner cleared the record, or the target is no longer valid, it goes.
func TestCompanionTokenKeptForTheRecordedCompanion(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{{Project: "group/infra", Paths: []string{"clusters/app/"}}}}
	targets := crossRepoTargets(spec, []string{"group/infra"})
	task := newImplTask("x", 1)
	task.Status.Run.Companion = &aiv1.CompanionRun{Project: "group/infra", MRIID: 3, Branch: "agent/app-1-x"}

	if ref, project := companionToken(task, spec, targets); ref == nil || project != "group/infra" {
		t.Fatalf("the recorded companion keeps its token: %v %q", ref, project)
	}
	if podOutdated(buildPod(task, spec, "", targets), task, spec, targets) {
		t.Fatal("a pod with the recorded companion's token is current")
	}
	if ref, _ := companionToken(task, spec, nil); ref != nil {
		t.Fatal("no token for a recorded companion whose target is no longer valid")
	}
	task.Status.Run.Companion = nil
	if ref, _ := companionToken(task, spec, targets); ref != nil {
		t.Fatal("no token once the record is cleared")
	}
}
