// Command ai-runner runs inside an agent pod.
//
//	ai-runner                       the runner (runner container: GitLab + Kubernetes credentials)
//	ai-runner agent-server          the agent server (agent container: agent CLI credentials only)
//	ai-runner research              one research run (runner container: Kubernetes credentials and the research database writer login, no GitLab token)
//	ai-runner init-workspace DIR…   create the PVC sub-paths (init container)
//	ai-runner read-gate [--min-lines N] [--subagents a,b]
//	                                the read gate, a claude PreToolUse hook (agent container): hook JSON on stdin, deny JSON or nothing on stdout, always exit 0
//
// Without AI_AGENT_SOCKET the runner runs the agent in-process (single
// container, for development only: the model can then reach the runner's
// credentials).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/readgate"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/runner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "read-gate" {
		// Before the logger and signal handler: the hook runs before every
		// Read and Bash call of claude.
		readGate(os.Args[2:])
		return
	}
	ctrl.SetLogger(newLogger())
	ctx := ctrl.SetupSignalHandler()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "agent-server":
			agentServer(ctx)
			return
		case "research":
			runResearch(ctx)
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

// readGate runs the read gate hook. Bad flags allow the call like every
// other error of the gate: one line on stderr, exit 0.
func readGate(args []string) {
	fs := flag.NewFlagSet("read-gate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	minLines := fs.Int("min-lines", 350, "files over this many lines are gated")
	subagents := fs.String("subagents", "", "comma-separated subagent names for the deny message")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "read-gate: allowed after error: %v\n", err)
		return
	}
	var names []string
	for _, name := range strings.Split(*subagents, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	readgate.Run(os.Stdin, os.Stdout, os.Stderr, *minLines, names)
}

// newLogger returns the logger at the level in AI_LOG_LEVEL: debug, info or
// error, set by the operator from agents.logLevel. An invalid value is
// ignored and the default (info) is used.
func newLogger() logr.Logger {
	opts := zap.Options{}
	if level := os.Getenv("AI_LOG_LEVEL"); level != "" {
		fs := flag.NewFlagSet("log", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		opts.BindFlags(fs)
		if err := fs.Parse([]string{"--zap-log-level=" + level}); err != nil {
			fmt.Fprintf(os.Stderr, "ignoring invalid AI_LOG_LEVEL %q: %v\n", level, err)
		}
	}
	return zap.New(zap.UseFlagOptions(&opts))
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
	if err := (&runner.AgentServer{Agent: agent, Log: log}).Serve(ctx, socket); err != nil {
		log.Error(err, "agent server stopped")
		os.Exit(1)
	}
}

func runRunner(ctx context.Context) {
	log := ctrl.Log.WithName("runner")
	scheme := runtime.NewScheme()
	utilruntime.Must(aiv1.AddToScheme(scheme))
	k8s, err := client.NewWithWatch(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
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
		K8s:         k8s,
		Namespace:   mustEnv("AI_TASK_NAMESPACE"),
		TaskName:    mustEnv("AI_TASK_NAME"),
		GitLabToken: token,
		NewGitLab:   func(url, token string) gitlab.API { return gitlab.New(url, token) },
		Agent:       agent,
		Mirror: &runner.Mirror{Dir: filepath.Join(stateDir, "mirror.git"), Token: token, Log: log.WithName("mirror"),
			AuthorName: envOr("AI_GIT_AUTHOR_NAME", "AI Operator"), AuthorEmail: envOr("AI_GIT_AUTHOR_EMAIL", "ai-operator@noreply.local")},
		StateDir:         stateDir,
		Exchange:         exchange,
		PollInterval:     10 * time.Second,
		AgentMemoryLimit: os.Getenv("AI_AGENT_MEMORY_LIMIT"),
		AgentCPUs:        os.Getenv("AI_AGENT_CPUS"),
		DeskURL:          os.Getenv("AI_DESK_URL"),
		AgentMemoryBytes: func() int64 {
			n, _ := strconv.ParseInt(os.Getenv("AI_AGENT_MEMORY_BYTES"), 10, 64)
			return n
		}(),
		Log: log,
	}
	log.Info("runner started", "task", r.TaskName)
	if err := r.Loop(ctx); err != nil {
		log.Error(err, "runner stopped")
		os.Exit(1)
	}
}

// runResearch runs one research run. It has no GitLab token, and its
// database login is the project's writer, which cannot migrate. The run
// itself is bounded by its depth's timeout.
func runResearch(ctx context.Context) {
	log := ctrl.Log.WithName("research")
	scheme := runtime.NewScheme()
	utilruntime.Must(aiv1.AddToScheme(scheme))
	k8s, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "creating kubernetes client")
		os.Exit(1)
	}
	port, err := strconv.Atoi(envOr("AI_RESEARCH_DB_PORT", "5432"))
	if err != nil {
		log.Error(err, "invalid AI_RESEARCH_DB_PORT")
		os.Exit(1)
	}
	cfg := store.Config{
		Host: mustEnv("AI_RESEARCH_DB_HOST"), Port: port, Database: mustEnv("AI_RESEARCH_DB_NAME"),
		SSLMode: os.Getenv("AI_RESEARCH_DB_SSLMODE"),
		User:    mustEnv("AI_RESEARCH_DB_USER"), Password: os.Getenv("AI_RESEARCH_DB_PASSWORD"),
	}
	var db *store.Store
	rr := &runner.ResearchRun{
		K8s:       k8s,
		Namespace: mustEnv("AI_RESEARCH_NAMESPACE"),
		Name:      mustEnv("AI_RESEARCH_NAME"),
		RunID:     mustEnv("AI_RESEARCH_RUN_ID"),
		Agent:     runner.NewAgentClient(mustEnv("AI_AGENT_SOCKET")),
		OpenStore: func(ctx context.Context) (*store.Store, error) {
			var err error
			db, err = store.Open(ctx, cfg)
			return db, err
		},
		Log: log,
	}
	log.Info("research run started", "research", rr.Name, "run", rr.RunID)
	err = rr.Run(ctx)
	if db != nil {
		db.Close()
	}
	if err != nil {
		log.Error(err, "research run not recorded")
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
