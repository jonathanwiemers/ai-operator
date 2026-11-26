package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research"
)

// TestAgentLanguageEnv checks that task, plan and research pods give the
// agent container AI_AGENT_LANGUAGE after AI_LOG_LEVEL and before the
// project's agents.env, never to the runner, and nothing when it is empty.
func TestAgentLanguageEnv(t *testing.T) {
	withEnv := func(spec aiv1.AgentProjectSpec) aiv1.AgentProjectSpec {
		spec.Agents.LogLevel = aiv1.LogLevelDebug
		spec.Agents.Env = append([]corev1.EnvVar{{Name: "EXTRA", Value: "1"}}, spec.Agents.Env...)
		return spec
	}
	builders := map[string]func(language string) *corev1.Pod{
		"task": func(language string) *corev1.Pod {
			return buildPod(newImplTask("x", 1), withEnv(testProject().Spec.WithDefaults()), "", nil, language, gitIdentity{})
		},
		"plan": func(language string) *corev1.Pod {
			return buildPlanPod(testPlan(), withEnv(testPlanSpec()), testPlanConfig, language, gitIdentity{})
		},
		"research": func(language string) *corev1.Pod {
			spec := withEnv(researchProject().Spec.WithDefaults())
			spec.Research.WriterSecret = &aiv1.SecretLogin{Name: "writer", UsernameKey: "user"}
			db := research.DatabaseConfig{Host: "pg.example", Port: 5432, Name: "ai", SSLMode: "require"}
			return buildResearchPod(newResearch("news", "0 7 * * *"), spec, testRunID, db, language)
		},
	}
	index := func(env []corev1.EnvVar, name string) int {
		for i, e := range env {
			if e.Name == name {
				return i
			}
		}
		return -1
	}
	for name, build := range builders {
		pod := build("en")
		agent := container(pod, AgentContainer).Env
		lang, level, extra := index(agent, "AI_AGENT_LANGUAGE"), index(agent, "AI_LOG_LEVEL"), index(agent, "EXTRA")
		if lang < 0 || agent[lang].Value != "en" {
			t.Errorf("%s: agent env %v, want AI_AGENT_LANGUAGE=en", name, agent)
		} else if !(level < lang && lang < extra) {
			t.Errorf("%s: AI_AGENT_LANGUAGE at %d, want after AI_LOG_LEVEL (%d) and before agents.env (%d)", name, lang, level, extra)
		}
		if i := index(container(pod, RunnerContainer).Env, "AI_AGENT_LANGUAGE"); i >= 0 {
			t.Errorf("%s: runner container has AI_AGENT_LANGUAGE", name)
		}
		if i := index(container(build(""), AgentContainer).Env, "AI_AGENT_LANGUAGE"); i >= 0 {
			t.Errorf("%s: empty language still sets AI_AGENT_LANGUAGE", name)
		}
	}
}
