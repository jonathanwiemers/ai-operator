// Package runner is the process running inside an agent pod. It waits for run
// requests on its AgentTask, prepares context from GitLab, drives the claude
// CLI for the spec / implement / review roles and writes results back to
// GitLab. The model itself never sees GitLab or Kubernetes credentials.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Runner waits for and executes run requests of one AgentTask.
type Runner struct {
	K8s       client.Client
	Namespace string
	TaskName  string
	// Workspace is the mount path of the workspace PVC.
	Workspace string
	// GitLabToken is used for the GitLab API and git over HTTPS.
	GitLabToken string
	NewGitLab   func(baseURL, token string) gitlab.API
	// Engines available in this pod, keyed by engine name.
	Engines map[aiv1.Engine]Engine
	// PollInterval between checks for new run requests.
	PollInterval time.Duration
	AuthorName   string
	AuthorEmail  string
	Log          logr.Logger
	Now          func() time.Time
}

// Outcome is the result of one run.
type Outcome struct {
	Result  aiv1.RunResult
	Summary string
	CostUSD float64
	Branch  string
	MRIID   int64
	MRURL   string
}

// state is persisted on the workspace PVC so it survives pod restarts and
// hibernation (but not archiving, after which everything starts fresh).
type state struct {
	// Sessions maps "<role>/<engine>" to the engine's session ID. Switching
	// a role to another engine simply starts a new session.
	Sessions        map[string]string `json:"sessions,omitempty"`
	LastIssueNoteID int64             `json:"lastIssueNoteID,omitempty"`
	LastMRNoteID    int64             `json:"lastMRNoteID,omitempty"`
}

func sessionKey(role string, engine aiv1.Engine) string { return role + "/" + string(engine) }

func (s *state) session(role string, engine aiv1.Engine) string {
	return s.Sessions[sessionKey(role, engine)]
}

func (s *state) setSession(role string, engine aiv1.Engine, id string) {
	if s.Sessions == nil {
		s.Sessions = map[string]string{}
	}
	s.Sessions[sessionKey(role, engine)] = id
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) repoDir() string   { return filepath.Join(r.Workspace, "repo") }
func (r *Runner) stateFile() string { return filepath.Join(r.Workspace, ".ai-operator", "state.json") }

func (r *Runner) loadState() state {
	var s state
	if b, err := os.ReadFile(r.stateFile()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (r *Runner) saveState(s state) error {
	if err := os.MkdirAll(filepath.Dir(r.stateFile()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(r.stateFile(), b, 0o600)
}

func (r *Runner) git() *Git {
	return &Git{Dir: r.repoDir(), Token: r.GitLabToken, AuthorName: r.AuthorName, AuthorEmail: r.AuthorEmail}
}

// Loop processes run requests until ctx is cancelled.
func (r *Runner) Loop(ctx context.Context) error {
	interval := r.PollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	for {
		ran, err := r.Step(ctx)
		if err != nil {
			r.Log.Error(err, "run step failed")
		}
		if ran {
			continue // check for requests that arrived during the run
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// Step executes at most one pending run. It reports whether a run happened.
func (r *Runner) Step(ctx context.Context) (bool, error) {
	task, project, err := r.load(ctx)
	if err != nil || task == nil || !task.RunPending() {
		return false, err
	}
	request := task.Spec.RunRequest
	r.Log.Info("starting run", "request", request, "type", task.Spec.Type)
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.ActiveRunRequest = request
		s.LastActivityTime = &metav1.Time{Time: r.now()}
	}); err != nil {
		return false, err
	}

	spec := project.Spec.WithDefaults()
	gl := r.NewGitLab(spec.GitLab.URL, r.GitLabToken)
	var out *Outcome
	switch task.Spec.Type {
	case aiv1.TaskTypeSpec:
		out, err = r.runSpec(ctx, gl, task, spec)
	case aiv1.TaskTypeImplement:
		out, err = r.runImplement(ctx, gl, task, project, spec)
	default:
		err = fmt.Errorf("unknown task type %q", task.Spec.Type)
	}
	if out == nil {
		out = &Outcome{}
	}
	if err != nil {
		r.Log.Error(err, "run failed", "request", request)
		out.Result = aiv1.RunResultFailed
		out.Summary = err.Error()
		if task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
			r.reportFailure(ctx, gl, spec, task, err)
		}
	}
	r.Log.Info("run finished", "request", request, "result", out.Result, "costUSD", out.CostUSD)
	return true, r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.ObservedRunRequest = request
		s.RunCount++
		s.LastActivityTime = &metav1.Time{Time: r.now()}
		s.LastResult = out.Result
		s.Summary = truncate(out.Summary, 2000)
		prev, _ := strconv.ParseFloat(s.CostUSD, 64)
		s.CostUSD = strconv.FormatFloat(prev+out.CostUSD, 'f', 4, 64)
		if out.Branch != "" {
			s.Branch = out.Branch
		}
		if out.MRIID != 0 {
			s.MergeRequestIID = out.MRIID
			s.MergeRequestURL = out.MRURL
		}
	})
}

func (r *Runner) load(ctx context.Context) (*aiv1.AgentTask, *aiv1.AgentProject, error) {
	var task aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var project aiv1.AgentProject
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: task.Spec.ProjectRef}, &project); err != nil {
		return nil, nil, err
	}
	return &task, &project, nil
}

// patchRun merge-patches status.run. Only the fields changed by mutate are
// sent, so the operator's status fields are never overwritten.
func (r *Runner) patchRun(ctx context.Context, mutate func(*aiv1.RunStatus)) error {
	var task aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task); err != nil {
		return err
	}
	orig := task.DeepCopy()
	mutate(&task.Status.Run)
	return r.K8s.Status().Patch(ctx, &task, client.MergeFrom(orig))
}

// morePending reports whether another run was requested while this one ran.
func (r *Runner) morePending(ctx context.Context, request int64) bool {
	var task aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task); err != nil {
		return false
	}
	return task.Spec.RunRequest > request
}

// engine returns the engine configured for a role.
func (r *Runner) engine(role aiv1.RoleSpec) (Engine, error) {
	e, ok := r.Engines[role.Engine]
	if !ok {
		return nil, fmt.Errorf("engine %q is not available in this agent image", role.Engine)
	}
	return e, nil
}

// invoke runs a role, resuming the given session when possible. If the
// session cannot be resumed (e.g. the workspace was archived), it starts a
// fresh session with the full prompt.
func (r *Runner) invoke(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	eng, err := r.engine(role)
	if err != nil {
		return nil, err
	}
	if inv.Resume == "" {
		inv.Prompt = fullPrompt
		return eng.Run(ctx, inv)
	}
	res, err := eng.Run(ctx, inv)
	if err == nil || (res != nil && res.SessionID != "") {
		return res, err
	}
	r.Log.Info("could not resume session, starting fresh", "session", inv.Resume, "error", err.Error())
	inv.Resume = ""
	inv.Prompt = fullPrompt
	return eng.Run(ctx, inv)
}

func roleInvocation(dir string, role aiv1.RoleSpec, system string, tools []string, schema string, readOnly bool) Invocation {
	if len(role.AllowedTools) > 0 {
		tools = role.AllowedTools
	}
	return Invocation{
		Dir:          dir,
		SystemPrompt: withInstructions(system, role.Instructions),
		Model:        role.Model,
		Effort:       role.Effort,
		MaxBudgetUSD: role.MaxBudgetUSD,
		AllowedTools: tools,
		JSONSchema:   schema,
		ReadOnly:     readOnly,
	}
}

func (r *Runner) reportFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, runErr error) {
	wf := spec.Workflow
	body := fmt.Sprintf("🤖 The agent run failed:\n\n```\n%s\n```\n\nComment `%s retry` (or move the issue back to `%s`) to try again.",
		truncate(runErr.Error(), 3000), wf.CommandPrefix, wf.ReadyLabel)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); err != nil {
		r.Log.Error(err, "posting failure note")
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, task.Spec.Issue.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.NeedsInputLabel}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		r.Log.Error(err, "updating labels after failure")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
