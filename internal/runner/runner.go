// Package runner implements the two processes of an agent pod.
//
// The runner container holds the credentials: it waits for run requests on
// its AgentTask, prepares context from GitLab, keeps a bare mirror of the
// repository and pushes, opens merge requests and reports back. The agent
// container holds only the agent CLI credentials: its AgentServer runs the
// agent CLIs and all git commands in the checkout the model works in. The
// two talk over a unix socket and exchange commits as git bundles, so the
// model can neither read the GitLab or Kubernetes credentials nor make the
// runner execute anything.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

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
	// GitLabToken is used for the GitLab API and git over HTTPS.
	GitLabToken string
	NewGitLab   func(baseURL, token string) gitlab.API
	// Agent runs the agent CLIs and owns the checkout.
	Agent Agent
	// Mirror is the runner's own copy of the repository.
	Mirror *Mirror
	// StateDir holds runner state; the agent cannot read or modify it.
	StateDir string
	// Exchange is the directory shared with the agent for bundles.
	Exchange string
	// PollInterval between checks for new run requests.
	PollInterval time.Duration
	Log          logr.Logger
	Now          func() time.Time

	// projectStatus is the project's status at the start of the current
	// run; it carries the project-wide usage limits.
	projectStatus aiv1.AgentProjectStatus
	// run records the steps of the current (or last) run.
	run *runLog
	// progress is the live progress comment of the current run, if any.
	progress *progress
	// blamedRestarts is the agent container's restart count of the last
	// termination a lost call was put down to.
	blamedRestarts int32
}

// Outcome is the result of one run.
type Outcome struct {
	Result  aiv1.RunResult
	Summary string
	CostUSD float64
	Branch  string
	MRIID   int64
	MRURL   string
	// PushedSHA is set when the run pushed; CIRound goes with it.
	PushedSHA string
	CIRound   int32
	// CIFixedPipelineID is set by CI fix runs.
	CIFixedPipelineID int64
	// MergedBaseFor is set by runs that merged the default branch.
	MergedBaseFor string
	// Reviewed is set when the reviewers ran; BlockingFindings and
	// ReviewErrors go with it.
	Reviewed         bool
	BlockingFindings int32
	ReviewErrors     int32
	ReviewedSHA      string
	// Plan is set by spec runs: the plan in plan order.
	Plan []aiv1.PlannedIssue
}

// state is persisted on the runner's part of the workspace PVC, so it survives
// pod restarts and hibernation (but not archiving, after which everything
// starts fresh).
type state struct {
	// Sessions maps "<role>/<engine>" to the engine's session ID. Switching
	// a role to another engine simply starts a new session.
	Sessions        map[string]string `json:"sessions,omitempty"`
	LastIssueNoteID int64             `json:"lastIssueNoteID,omitempty"`
	LastMRNoteID    int64             `json:"lastMRNoteID,omitempty"`
	// RevisitNoteID is the newest comment whose revisits are handled; it
	// only moves once the run's decisions are posted.
	RevisitNoteID int64 `json:"revisitNoteID,omitempty"`
	// Ready is set when the runner last left the MR ready, so a draft after
	// that is a human holding it.
	Ready bool `json:"ready,omitempty"`
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

func (r *Runner) stateFile() string { return filepath.Join(r.StateDir, "state.json") }

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

// syncWorkspace brings the mirror up to date and hands the agent's checkout
// exactly the commits it needs, then checks out branch.
func (r *Runner) syncWorkspace(ctx context.Context, url, branch, base string) error {
	branches := []string{base}
	if branch != base {
		branches = append(branches, branch)
	}
	name := "fetch repository"
	if !r.Mirror.exists() {
		name = "clone repository"
	}
	s := r.step(ctx, name, "branches", branches)
	if err := r.Mirror.Sync(ctx, url, branches...); err != nil {
		return s.fail(err)
	}
	baseSHA, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+base)
	result := fmt.Sprintf("%s at %s", base, shortSHA(baseSHA))
	kv := []any{"baseSHA", baseSHA}
	if branch != base {
		if sha, ok := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch); ok {
			result += fmt.Sprintf(", %s at %s", branch, shortSHA(sha))
			kv = append(kv, "branchSHA", sha)
		} else {
			result += fmt.Sprintf(", %s is new", branch)
		}
	}
	s.done(result, kv...)
	return r.prepareCheckout(ctx, branch, base)
}

// prepareCheckout hands the agent's checkout the commits of the mirror it
// lacks and checks out branch.
func (r *Runner) prepareCheckout(ctx context.Context, branch, base string) error {
	branches := []string{base}
	if branch != base {
		branches = append(branches, branch)
	}
	s := r.step(ctx, "prepare checkout", "branch", branch)
	known, err := r.Agent.RemoteRefs(ctx)
	if err != nil {
		return s.fail(err)
	}
	path := filepath.Join(r.Exchange, "to-agent.bundle")
	refs, written, err := r.Mirror.Bundle(ctx, path, known, branches...)
	if err != nil {
		return s.fail(err)
	}
	req := PrepareRequest{Refs: refs, Branch: branch, Base: base}
	if written {
		req.Bundle = path
		defer os.Remove(path)
	}
	if err := r.Agent.Prepare(ctx, req); err != nil {
		return s.fail(err)
	}
	result := "checked out " + branch + ", already up to date"
	if written {
		result = "checked out " + branch + ", new commits sent"
	}
	s.done(result, "bundle", written)
	return nil
}

// publish moves the agent's new commits on branch to the remote. It reports
// whether anything was pushed.
func (r *Runner) publish(ctx context.Context, branch, base string) (bool, error) {
	path := filepath.Join(r.Exchange, "from-agent.bundle")
	written, err := r.Agent.Bundle(ctx, branch, base, path)
	if err != nil || !written {
		return false, err
	}
	defer os.Remove(path)
	if err := r.Mirror.Import(ctx, path, branch); err != nil {
		return false, err
	}
	return true, r.Mirror.Push(ctx, branch)
}

// Loop processes run requests until ctx is cancelled.
func (r *Runner) Loop(ctx context.Context) error {
	interval := r.PollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	// Do not pick up work before the agent container is up, otherwise the
	// first run of a fresh pod would fail.
	for {
		err := r.Agent.Ready(ctx)
		if err == nil {
			break
		}
		r.Log.Info("waiting for the agent container", "error", err.Error())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	for {
		ran, err := r.Step(ctx)
		if err != nil && ctx.Err() == nil {
			// A step cut off by the pod stopping is not an error.
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
	if d := task.Status.Run.DeferredUntil; d != nil && d.After(r.now()) {
		return false, nil // waiting for a usage limit to reset
	}
	r.projectStatus = project.Status
	request := task.Spec.RunRequest
	r.run = newRunLog(r.Log, task)
	r.blamedRestarts = 0
	ctx = withRunID(ctx, r.run.id)
	log := r.run.log
	log.Info("starting run", "request", request, "previousRuns", task.Status.Run.RunCount, "deferrals", task.Status.Run.Deferrals)
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.ActiveRunRequest = request
		s.CurrentStep = ""
		s.LastActivityTime = &metav1.Time{Time: r.now()}
	}); err != nil {
		return false, err
	}

	spec := project.Spec.WithDefaults()
	gl := r.NewGitLab(spec.GitLab.URL, r.GitLabToken)
	var out *Outcome
	switch task.Spec.Type {
	case aiv1.TaskTypeSpec:
		out, err = r.runSpec(ctx, gl, task, project, spec)
	case aiv1.TaskTypeImplement:
		out, err = r.runImplement(ctx, gl, task, project, spec)
	default:
		err = fmt.Errorf("unknown task type %q", task.Spec.Type)
	}
	if out == nil {
		out = &Outcome{}
	}
	if lost := r.agentLost(ctx, err, r.run.start); lost != nil {
		// Lost outside an agent run (git calls, between calls): say why too.
		err = lost
	}
	r.run.finish(err)
	if r.progress != nil {
		// The report replaced the live view; otherwise say how it ended.
		// Also when the pod is stopping: the comment must not stay "working".
		headline := ""
		if _, ok := AsUsageLimit(err); ok && task.Status.Run.Deferrals < MaxDeferrals {
			headline = "⏸️ Paused by a usage limit; the run continues once it resets."
		} else if err != nil {
			headline = "❌ The run failed; the issue has the details."
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		if cerr := r.progress.close(cctx, headline); cerr != nil {
			log.Info("could not close the progress comment", "error", cerr.Error())
		}
		cancel()
		r.progress = nil
	}
	if lim, ok := AsUsageLimit(err); ok {
		if task.Status.Run.Deferrals < MaxDeferrals {
			return false, r.deferRun(ctx, gl, spec, task, out, lim)
		}
		err = fmt.Errorf("giving up after %d usage limit deferrals: %w", task.Status.Run.Deferrals, err)
	}
	if err != nil {
		log.Error(err, "run failed")
		out.Result = aiv1.RunResultFailed
		out.Summary = err.Error()
		if task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
			r.reportFailure(ctx, gl, spec, task, out, err)
		}
	}
	log.Info("run finished", "result", out.Result, "costUSD", out.CostUSD,
		"durationMs", time.Since(r.run.start).Milliseconds(), "steps", r.run.lines())
	if out.Plan != nil {
		if err := r.patchStatus(ctx, func(s *aiv1.AgentTaskStatus) { s.Plan = out.Plan }); err != nil {
			log.Error(err, "recording the plan")
		}
	}
	return true, r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.ObservedRunRequest = request
		s.CurrentStep = ""
		s.RunCount++
		s.DeferredUntil = nil
		s.LimitedEngine = ""
		s.Deferrals = 0
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
		if out.PushedSHA != "" {
			s.PushedSHA = out.PushedSHA
			s.PushedAt = &metav1.Time{Time: r.now()}
			s.CIRound = out.CIRound
		}
		if out.CIFixedPipelineID != 0 {
			s.CIFixedPipelineID = out.CIFixedPipelineID
		}
		if out.MergedBaseFor != "" {
			s.MergedBaseFor = out.MergedBaseFor
		}
		// Runs without a review (CI fix runs, questions) keep the result of
		// the last review.
		if out.Reviewed {
			s.BlockingFindings = out.BlockingFindings
			s.ReviewErrors = out.ReviewErrors
			s.ReviewedSHA = out.ReviewedSHA
		}
	})
}

// deferRun postpones the current run request after a usage limit. The run is
// not counted, not reported as failed, and the issue stays "working"; the
// runner retries after DeferredUntil, and the operator publishes the limit
// to the project so other tasks wait for it too.
func (r *Runner) deferRun(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, lim *UsageLimitError) error {
	now := r.now()
	deferrals := task.Status.Run.Deferrals + 1
	until := lim.ResetAt
	if !until.After(now) {
		until = now.Add(spec.Lifecycle.Backoff(deferrals))
	}
	r.log().Info("usage limit reached, deferring run", "engine", lim.Engine, "until", until.UTC(), "deferrals", deferrals,
		"message", lim.Message, "steps", r.run.lines())

	if deferrals == 1 && task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
		body := fmt.Sprintf("⏳ The `%s` engine reached a usage limit. The run is paused and retried automatically at %s UTC.",
			lim.Engine, until.UTC().Format("2006-01-02 15:04"))
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); err != nil {
			r.log().Error(err, "posting usage limit note")
		}
	}
	return r.patchRun(ctx, func(s *aiv1.RunStatus) {
		// The request stays pending, but nothing is in progress any more.
		s.ActiveRunRequest = s.ObservedRunRequest
		s.CurrentStep = ""
		s.DeferredUntil = &metav1.Time{Time: until}
		s.LimitedEngine = lim.Engine
		s.Deferrals = deferrals
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = truncate(lim.Error(), 2000)
		prev, _ := strconv.ParseFloat(s.CostUSD, 64)
		s.CostUSD = strconv.FormatFloat(prev+out.CostUSD, 'f', 4, 64)
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
	return r.patchStatus(ctx, func(s *aiv1.AgentTaskStatus) { mutate(&s.Run) })
}

// patchStatus merge-patches the fields of the task status changed by mutate.
func (r *Runner) patchStatus(ctx context.Context, mutate func(*aiv1.AgentTaskStatus)) error {
	var task aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task); err != nil {
		return err
	}
	orig := task.DeepCopy()
	mutate(&task.Status)
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

// invoke runs a role, resuming the given session when possible. If the
// session cannot be resumed (e.g. the workspace was archived), it starts a
// fresh session with the full prompt.
//
// Usage and rate limits come back as *UsageLimitError. If the project already
// knows the engine is limited, the engine is not called at all.
func (r *Runner) invoke(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	if lim, ok := r.projectStatus.ActiveUsageLimit(role.Engine, r.now()); ok {
		return nil, &UsageLimitError{Engine: role.Engine, ResetAt: lim.Until.Time, Message: "project-wide limit: " + lim.Message}
	}
	redialed, retried := false, false
	run := func(inv Invocation) (*Result, error) {
		for {
			start := r.now()
			res, err := r.Agent.Run(ctx, role.Engine, inv)
			lost := r.agentLost(ctx, err, start)
			switch {
			case lost == nil:
				return res, classifyLimit(role.Engine, err, res)
			case !lost.back:
				return nil, lost
			case lost.notSent && !redialed:
				// The agent was down before the call, nothing ran: call again.
				redialed = true
			case !retried && lost.Term != nil && !lost.Term.OOMKilled():
				// A crash may not happen again; running out of memory would.
				// The killed call's edits (and a stale git lock) go first.
				retried = true
				r.log().Info("agent container restarted during the call, trying once more", "reason", lost.Term.Reason, "exitCode", lost.Term.ExitCode)
				if err := r.Agent.Reset(ctx); err != nil {
					return nil, lost
				}
			default:
				return nil, lost
			}
		}
	}
	if inv.Resume == "" {
		inv.Prompt = fullPrompt
		return run(inv)
	}
	res, err := run(inv)
	var lost *AgentLostError
	if _, limited := AsUsageLimit(err); err == nil || limited || errors.As(err, &lost) || (res != nil && res.SessionID != "") {
		return res, err
	}
	r.log().Info("could not resume session, starting fresh", "session", inv.Resume, "error", err.Error())
	inv.Resume = ""
	inv.Prompt = fullPrompt
	return run(inv)
}

func roleInvocation(role aiv1.RoleSpec, system string, tools []string, schema string, readOnly bool) Invocation {
	if len(role.AllowedTools) > 0 {
		tools = role.AllowedTools
	}
	return Invocation{
		SystemPrompt: withInstructions(system, role.Instructions),
		Model:        role.Model,
		Effort:       role.Effort,
		MaxBudgetUSD: role.MaxBudgetUSD,
		AllowedTools: tools,
		JSONSchema:   schema,
		ReadOnly:     readOnly,
	}
}

func (r *Runner) reportFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, runErr error) {
	wf := spec.Workflow
	body := fmt.Sprintf("🤖 The agent run failed:\n\n```\n%s\n```\n\nComment `%s retry` (or move the issue back to `%s`) to try again.",
		truncate(runErr.Error(), 3000), wf.CommandPrefix, wf.ReadyLabel)
	body += r.run.markdown(out.CostUSD)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); err != nil {
		r.log().Error(err, "posting failure note")
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, task.Spec.Issue.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.NeedsInputLabel}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		r.log().Error(err, "updating labels after failure")
	}
}

// truncate cuts s to at most n bytes (plus an ellipsis) without splitting a
// UTF-8 character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
