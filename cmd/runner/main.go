// Command ai-runner runs inside an agent pod. It waits for run requests on its
// AgentTask and executes them with the configured agent CLIs.
package main

import (
	"os"
	"os/exec"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/runner"
)

func main() {
	ctrl.SetLogger(zap.New())
	log := ctrl.Log.WithName("runner")

	scheme := runtime.NewScheme()
	utilruntime.Must(aiv1.AddToScheme(scheme))
	k8s, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "creating kubernetes client")
		os.Exit(1)
	}

	env := runner.DefaultEnv()
	engines := map[aiv1.Engine]runner.Engine{}
	if _, err := exec.LookPath("claude"); err == nil {
		engines[aiv1.EngineClaude] = &runner.ClaudeCLI{Env: env}
	}
	if _, err := exec.LookPath("cursor-agent"); err == nil {
		engines[aiv1.EngineCursor] = &runner.CursorCLI{Env: env}
	}

	r := &runner.Runner{
		K8s:          k8s,
		Namespace:    mustEnv("AI_TASK_NAMESPACE"),
		TaskName:     mustEnv("AI_TASK_NAME"),
		Workspace:    envOr("AI_WORKSPACE", "/workspace"),
		GitLabToken:  mustEnv("GITLAB_TOKEN"),
		NewGitLab:    func(url, token string) gitlab.API { return gitlab.New(url, token) },
		Engines:      engines,
		PollInterval: 10 * time.Second,
		AuthorName:   envOr("AI_GIT_AUTHOR_NAME", "AI Operator"),
		AuthorEmail:  envOr("AI_GIT_AUTHOR_EMAIL", "ai-operator@noreply.local"),
		Log:          log,
	}
	log.Info("runner started", "task", r.TaskName, "engines", len(engines))
	if err := r.Loop(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "runner stopped")
		os.Exit(1)
	}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		ctrl.Log.Error(nil, "missing required environment variable", "name", k)
		os.Exit(1)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
