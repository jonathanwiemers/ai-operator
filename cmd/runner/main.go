// Command ai-runner runs inside an agent pod.
//
//	ai-runner                       the runner (runner container: GitLab + Kubernetes credentials)
//	ai-runner agent-server          the agent server (agent container: agent CLI credentials only)
//	ai-runner init-workspace DIR…   create the PVC sub-paths (init container)
//
// Without AI_AGENT_SOCKET the runner runs the agent in-process (single
// container, for development only: the model can then reach the runner's
// credentials).
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	ctx := ctrl.SetupSignalHandler()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "agent-server":
			agentServer(ctx)
			return
		case "init-workspace":
			// Create the PVC sub-paths of the runner and agent containers.
			for _, dir := range os.Args[2:] {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					ctrl.Log.Error(err, "creating workspace directory", "dir", dir)
					os.Exit(1)
				}
			}
			return
		}
	}
	runRunner(ctx)
}

// localAgent builds the agent from the environment of the current container.
func localAgent() *runner.LocalAgent {
	env := runner.DefaultEnv()
	engines := map[aiv1.Engine]runner.Engine{}
	if _, err := exec.LookPath("claude"); err == nil {
		engines[aiv1.EngineClaude] = &runner.ClaudeCLI{Env: env}
	}
	if _, err := exec.LookPath("cursor-agent"); err == nil {
		engines[aiv1.EngineCursor] = &runner.CursorCLI{Env: env}
	}
	workspace := envOr("AI_WORKSPACE", "/workspace")
	return &runner.LocalAgent{
		Workspace: &runner.Workspace{
			Dir:         filepath.Join(workspace, "repo"),
			AuthorName:  envOr("AI_GIT_AUTHOR_NAME", "AI Operator"),
			AuthorEmail: envOr("AI_GIT_AUTHOR_EMAIL", "ai-operator@noreply.local"),
		},
		Engines:  engines,
		Exchange: envOr("AI_EXCHANGE", "/exchange"),
	}
}

func agentServer(ctx context.Context) {
	log := ctrl.Log.WithName("agent-server")
	agent := localAgent()
	socket := mustEnv("AI_AGENT_SOCKET")
	log.Info("agent server started", "socket", socket, "engines", len(agent.Engines))
	if err := (&runner.AgentServer{Agent: agent}).Serve(ctx, socket); err != nil {
		log.Error(err, "agent server stopped")
		os.Exit(1)
	}
}

func runRunner(ctx context.Context) {
	log := ctrl.Log.WithName("runner")
	scheme := runtime.NewScheme()
	utilruntime.Must(aiv1.AddToScheme(scheme))
	k8s, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "creating kubernetes client")
		os.Exit(1)
	}

	token := mustEnv("GITLAB_TOKEN")
	stateDir := envOr("AI_STATE_DIR", "/runner")
	exchange := envOr("AI_EXCHANGE", "/exchange")
	var agent runner.Agent
	if socket := os.Getenv("AI_AGENT_SOCKET"); socket != "" {
		agent = runner.NewAgentClient(socket)
	} else {
		log.Info("AI_AGENT_SOCKET not set: running the agent in-process (development mode)")
		agent = localAgent()
	}
	if err := os.MkdirAll(exchange, 0o755); err != nil {
		log.Error(err, "creating exchange directory")
		os.Exit(1)
	}

	r := &runner.Runner{
		K8s:          k8s,
		Namespace:    mustEnv("AI_TASK_NAMESPACE"),
		TaskName:     mustEnv("AI_TASK_NAME"),
		GitLabToken:  token,
		NewGitLab:    func(url, token string) gitlab.API { return gitlab.New(url, token) },
		Agent:        agent,
		Mirror:       &runner.Mirror{Dir: filepath.Join(stateDir, "mirror.git"), Token: token},
		StateDir:     stateDir,
		Exchange:     exchange,
		PollInterval: 10 * time.Second,
		Log:          log,
	}
	log.Info("runner started", "task", r.TaskName)
	if err := r.Loop(ctx); err != nil {
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
