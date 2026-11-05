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
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
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
	// AgentMemoryLimit and AgentCPUs are what the agent container may use
	// (e.g. "6Gi", "2"), if it has limits; the implementer is told.
	AgentMemoryLimit, AgentCPUs string
	// DeskURL is the Desk's base URL (AI_DESK_URL); empty: no links.
	DeskURL string
	// AgentMemoryBytes is the agent container's memory limit in this pod
	// (0 if unknown).
	AgentMemoryBytes int64
	Log              logr.Logger
	Now              func() time.Time

	// projectStatus is the project's status at the start of the current
	// run; it carries the project-wide usage limits.
	projectStatus aiv1.AgentProjectStatus
	// run records the steps of the current (or last) run.
	run *runLog
	// progress is the live progress comment of the current run, if any.
	progress *progress
	// pushedHead is the commit this run last pushed, e.g. a step.
	pushedHead string
	// changedPins are the pin files the run's branch changes; with any, every
	// agent call of the run gets per-run toolchain directories.
	changedPins []string
	// logPrompts is the project's agents.logPrompts during a run.
	logPrompts bool
	// pilot reports whether a comment is addressed to the milestone's pilot
	// during a run.
	pilot func(gitlab.Note) bool
	// blamedRestarts is the agent container's restart count of the last
	// termination a lost call was put down to.
	blamedRestarts int32
	// projectSpec is the project's spec (with defaults) during a run.
	projectSpec aiv1.AgentProjectSpec
	// usage is the task's newest usage reading per engine (recordUsage).
	usage []aiv1.EngineUsage
	// routed are the limits this task's runs went around with a fallback.
	routed []aiv1.UsageLimit
	// usageMu guards usage, routed, sessionEngines and the limits and
	// readings in projectStatus: reviewers call engines in parallel.
	usageMu sync.Mutex
	// sessionEngines maps the sessions of the current run to their engine,
	// so a fallback resumes only its own engine's sessions.
	sessionEngines map[string]aiv1.Engine
	// chatSessionKey is the "<role>/<engine>" of the open chat session, or
	// empty when none is open. Cleared when CloseSession runs before a run.
	chatSessionKey string
	// pendingCalls are engine calls not yet in status.run.calls; callsMu
	// guards them and serialises their status patches.
	pendingCalls []aiv1.AgentCall
	callsMu      sync.Mutex
	// runNumber is the run in progress (status.run.runCount after it).
	runNumber int64
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
	// ReviewedFor is set by runs asked to review a head again.
	ReviewedFor string
	// Reviewed is set when the reviewers ran; BlockingFindings and
	// ReviewErrors go with it.
	Reviewed              bool
	BlockingFindings      int32
	ReviewErrors          int32
	ReviewErrorsTransient bool
	ReviewErrorKinds      []aiv1.ReviewError
	ReviewedSHA           string
	// Questions are the agent's normalised questions (Implement runs).
	Questions []aiv1.QuestionDraft
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
	// DescribedDiff is the hash of the diff the merge request description
	// was last written for.
	DescribedDiff string `json:"describedDiff,omitempty"`
	// ImplementEngine is the engine of the last implementer session; a run
	// on another engine starts that engine's session fresh.
	ImplementEngine aiv1.Engine `json:"implementEngine,omitempty"`
	// PilotAnswered are the pilot's last answered messages (note IDs).
	PilotAnswered []int64 `json:"pilotAnswered,omitempty"`
	// PilotAssessed are the pilot's last assessed events ("key@request").
	PilotAssessed []string `json:"pilotAssessed,omitempty"`
	// PilotRefined are the pilot's last refined answers
	// ("prompt/message@request").
	PilotRefined []string `json:"pilotRefined,omitempty"`
	// ChatInFlight is the chat turn being sent (or last attempted), so a
	// restart can resend it with the stored session.
	ChatInFlight *chatInFlight `json:"chatInFlight,omitempty"`
	// ChatAnswered are the last answered chat message IDs, so a message
	// the operator hands over again is not answered twice.
	ChatAnswered []int64 `json:"chatAnswered,omitempty"`
}

// chatInFlight is persisted before a chat turn is sent.
type chatInFlight struct {
	MessageID int64  `json:"messageID"`
	SessionID string `json:"sessionID,omitempty"`
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

// loadState reads the runner's state. A missing file is an empty state (a
// first run); an unreadable or corrupt one is an error, since an empty state
// would let steps post notes or decisions again.
func (r *Runner) loadState() (state, error) {
	var s state
	b, err := os.ReadFile(r.stateFile())
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading the runner state %s: %w", r.stateFile(), err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return state{}, fmt.Errorf("parsing the runner state %s: %w", r.stateFile(), err)
	}
	return s, nil
}

func (r *Runner) saveState(s state) error {
	if err := os.MkdirAll(filepath.Dir(r.stateFile()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	// Write and rename, so a crash or a concurrent read never sees half a
	// file (chat turns read the state while a turn saves it).
	tmp := r.stateFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.stateFile())
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
			if r.changedPins = r.Mirror.ChangedPins(ctx, base, branch); len(r.changedPins) > 0 {
				result += ", changes " + strings.Join(r.changedPins, ", ")
				kv = append(kv, "changedPins", r.changedPins)
			}
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
func (r *Runner) publish(ctx context.Context, branch, base string, skipCI bool) (bool, error) {
	path := filepath.Join(r.Exchange, "from-agent.bundle")
	written, err := r.Agent.Bundle(ctx, branch, base, path)
	if err != nil || !written {
		return false, err
	}
	defer os.Remove(path)
	if err := r.Mirror.Import(ctx, path, branch); err != nil {
		return false, err
	}
	// The agent's checkout does not learn about the runner's pushes, so its
	// bundle still holds commits a step of this run pushed.
	head, _ := r.Mirror.resolve(ctx, "refs/heads/"+branch)
	if head != "" && head == r.pushedHead {
		return false, nil
	}
	if err := r.Mirror.Push(ctx, branch, skipCI); err != nil {
		return false, err
	}
	r.pushedHead = head
	return true, nil
}

// Loop processes chat turns and run requests until ctx is cancelled.
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
	wake := r.watchTask(ctx)
	for {
		did, err := r.tick(ctx, wake)
		if err != nil && ctx.Err() == nil {
			// A step cut off by the pod stopping is not an error.
			r.Log.Error(err, "run step failed")
		}
		if did {
			continue // check for work that arrived during the turn/run
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-time.After(interval):
		}
	}
}

// tick handles one chat turn if any, otherwise one run request.
func (r *Runner) tick(ctx context.Context, wake <-chan struct{}) (bool, error) {
	task, project, err := r.load(ctx)
	if err != nil || task == nil {
		return false, err
	}
	if err := r.pruneChatReplies(ctx, task); err != nil {
		return false, err
	}
	st, err := r.loadState()
	if err != nil {
		// Treated as no message: the run steps below fail on it themselves.
		r.log().Error(err, "loading the state for chat messages")
	}
	if msg, ok := nextChatMessage(task, st); ok && err == nil {
		// A failed turn (e.g. the agent container lost) is retried after
		// the poll interval, not at once.
		err := r.chatTurn(ctx, task, project, msg, wake)
		return err == nil, err
	}
	return r.Step(ctx)
}

// Step executes at most one pending run. It reports whether a run happened.
func (r *Runner) Step(ctx context.Context) (bool, error) {
	task, project, err := r.load(ctx)
	if err != nil || task == nil || !task.RunPending() {
		return false, err
	}
	if task.Spec.BudgetStop {
		return false, nil // the budget is used: waiting until it is raised
	}
	// A run resumes the session through the CLI; close the live chat one first.
	if err := r.Agent.CloseSession(ctx); err != nil {
		r.Log.Error(err, "closing the chat session before a run")
	}
	r.chatSessionKey = ""
	if d := task.Status.Run.DeferredUntil; d != nil && d.After(r.now()) {
		return false, nil // waiting for a usage limit to reset
	}
	if task.Status.Run.OOMRetries > 0 && r.AgentMemoryBytes > 0 {
		// Started again after an OOM kill: only in a pod with the raised
		// limit, which the operator starts in place of this one.
		if want, ok := project.Spec.WithDefaults().Agents.EffectiveMemory(task.Status.AgentMemoryLimit); ok && r.AgentMemoryBytes < want.Value() {
			r.Log.V(1).Info("waiting for a pod with more memory", "has", r.AgentMemoryBytes, "wants", want.String())
			return false, nil
		}
	}
	r.projectStatus = project.Status
	r.usage, r.routed, r.sessionEngines = task.Status.Run.EngineUsage, task.Status.Run.UsageLimits, nil
	request := task.Spec.RunRequest
	r.run = newRunLog(r.Log, task)
	r.blamedRestarts, r.pushedHead, r.changedPins = 0, "", nil
	r.runNumber = int64(task.Status.Run.RunCount) + 1
	ctx = withRunID(ctx, r.run.id)
	log := r.run.log
	// Every way a run ends sets the active request back to the observed
	// one, so one still ahead of it was cut off: the pod stopped mid-run.
	interrupted := task.Status.Run.ActiveRunRequest > task.Status.Run.ObservedRunRequest
	interruptions := task.Status.Run.Interruptions
	if interrupted {
		interruptions++
		r.run.restart = fmt.Sprintf("↻ Started again: the previous attempt was interrupted (its pod stopped before the run finished, e.g. evicted or the node went down). Restart %d of at most %d.",
			interruptions, aiv1.MaxInterruptions)
	}
	log.Info("starting run", "request", request, "previousRuns", task.Status.Run.RunCount, "deferrals", task.Status.Run.Deferrals,
		"interruptions", interruptions)
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.Interruptions = interruptions
		s.BlockReason = "" // this attempt reports its own
		s.ActiveRunRequest = request
		s.CurrentStep = ""
		s.LastActivityTime = &metav1.Time{Time: r.now()}
	}); err != nil {
		return false, err
	}

	spec := project.Spec.WithDefaults()
	r.projectSpec = spec
	r.logPrompts = spec.Agents.LogPrompts
	r.pilot = pilotAddressed(project, spec)
	gl := r.NewGitLab(spec.GitLab.URL, r.GitLabToken)
	var out *Outcome
	var block aiv1.BlockReason
	switch {
	case interruptions > aiv1.MaxInterruptions:
		// Something in the run keeps taking its pod down: not again.
		r.run.restart = ""
		if mr := task.Status.Run.MergeRequestIID; mr != 0 {
			removeLiveProgress(ctx, gl, spec.GitLab.Project, mr, project.Status.BotUserID)
		}
		err = fmt.Errorf("giving up: the run was interrupted %d times without finishing (its pod stopped each time, e.g. evicted or the node went down); something in it may keep taking the pod down", interruptions)
		block = aiv1.BlockInfra
	case task.Spec.Type == aiv1.TaskTypeSpec:
		out, err = r.runSpec(ctx, gl, task, project, spec)
	case task.Spec.Type == aiv1.TaskTypeImplement:
		out, err = r.runImplement(ctx, gl, task, project, spec)
	case task.Spec.Type == aiv1.TaskTypeCoordinate:
		out, err = r.runPilot(ctx, gl, task, project, spec)
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
	moreMemory := r.oomRetry(err, task, spec)
	if r.progress != nil {
		// The report replaced the live view; otherwise say how it ended.
		// Also when the pod is stopping: the comment must not stay "working".
		headline := ""
		if _, ok := AsUsageLimit(err); ok && task.Status.Run.Deferrals < MaxDeferrals {
			headline = "⏸️ Paused by a usage limit; the run continues once it resets."
		} else if isBudgetStop(err) {
			headline = "⏸️ Stopped between agent calls: the budget is used. The run continues once it is raised."
		} else if moreMemory != "" {
			headline = "⏸️ The agent container ran out of memory; the run starts again with " + moreMemory + "."
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
	if moreMemory != "" {
		return false, r.retryWithMoreMemory(ctx, gl, spec, task, out, err, moreMemory)
	}
	if isBudgetStop(err) {
		return false, r.deferForBudget(ctx, out, err)
	}
	if lim, ok := AsUsageLimit(err); ok {
		if task.Status.Run.Deferrals < MaxDeferrals {
			return false, r.deferRun(ctx, gl, spec, task, out, lim)
		}
		err = fmt.Errorf("giving up after %d usage limit deferrals: %w", task.Status.Run.Deferrals, err)
		block = aiv1.BlockQuota
	}
	var lost *AgentLostError
	if errors.As(err, &lost) || task.Spec.Type == aiv1.TaskTypeSpec && block == "" && gitlab.IsTransient(err) {
		block = aiv1.BlockInfra // its pod or container was lost (or GitLab), not the work
	}
	if err != nil {
		log.Error(err, "run failed")
		out.Result = aiv1.RunResultFailed
		out.Summary = err.Error()
		switch {
		case task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil:
			r.reportFailure(ctx, gl, spec, task, out, err, block)
		case task.Spec.Type == aiv1.TaskTypeSpec:
			r.reportSpecFailure(ctx, gl, spec, task, out, err, block)
		}
	}
	log.Info("run finished", "result", out.Result, "costUSD", out.CostUSD,
		"durationMs", time.Since(r.run.start).Milliseconds(), "steps", r.run.lines())
	if out.Plan != nil {
		if err := r.patchStatus(ctx, func(s *aiv1.AgentTaskStatus) { s.Plan = out.Plan }); err != nil {
			log.Error(err, "recording the plan")
		}
	}
	return true, r.patchRunWithCalls(ctx, func(s *aiv1.RunStatus) {
		s.ObservedRunRequest = request
		s.CurrentStep = ""
		s.RunCount++
		s.DeferredUntil = nil
		s.ClearDeferringLimit()
		s.Deferrals = 0
		s.BlockReason = block
		s.OOMRetries = 0
		s.Interruptions = 0
		s.LastActivityTime = &metav1.Time{Time: r.now()}
		s.LastResult = out.Result
		s.Summary = textutil.Truncate(out.Summary, 2000)
		s.Questions = out.Questions
		s.CostUSD = aiv1.AddCost(s.CostUSD, out.CostUSD)
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
		if out.ReviewedFor != "" {
			s.ReviewedFor = out.ReviewedFor
		}
		// Runs without a review (CI fix runs, questions) keep the result of
		// the last review.
		if out.Reviewed {
			s.BlockingFindings = out.BlockingFindings
			s.ReviewErrors, s.ReviewErrorsTransient, s.ReviewErrorKinds = out.ReviewErrors, out.ReviewErrorsTransient, out.ReviewErrorKinds
			s.ReviewedSHA = out.ReviewedSHA
		}
	})
}

// deferRun postpones the current run request after a usage limit. The run is
// not counted and not reported as failed; the operator blocks the issue
// (quota) until the runner retries after DeferredUntil, and publishes the
// limit to the project so other tasks wait for it too.
func (r *Runner) deferRun(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, lim *UsageLimitError) error {
	now := r.now()
	deferrals := task.Status.Run.Deferrals + 1
	until := lim.ResetAt
	if !until.After(now) {
		until = now.Add(spec.Lifecycle.Backoff(deferrals))
	}
	// With the fallback limited too, the run resumes on whichever agent is
	// free first; the project keeps knowing both limits.
	own, resumeOn := until, agentName(lim.Engine, lim.Model)
	deferring := aiv1.UsageLimit{Engine: lim.Engine, Until: metav1.Time{Time: own}, Message: textutil.Truncate(lim.Message, 300)}
	if lim.FallbackEngine != "" {
		r.usageMu.Lock()
		r.routed = aiv1.MergeUsageLimit(r.routed, aiv1.UsageLimit{Engine: lim.FallbackEngine, Until: metav1.Time{Time: lim.FallbackUntil}})
		r.usageMu.Unlock()
		if lim.FallbackUntil.Before(until) {
			until, resumeOn = lim.FallbackUntil, agentName(lim.FallbackEngine, lim.FallbackModel)
		}
	}
	r.log().Info("usage limit reached, deferring run", "engine", lim.Engine, "until", until.UTC(), "resumeOn", resumeOn, "deferrals", deferrals,
		"message", lim.Message, "steps", r.run.lines())

	if deferrals == 1 && task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
		body := fmt.Sprintf("⏳ `%s` reached its usage limit (until %s UTC).", lim.Engine, utcMinute(own))
		if lim.FallbackEngine != "" {
			body = fmt.Sprintf("⏳ No agent has usage left: `%s` is limited until %s UTC, its fallback `%s` until %s UTC.",
				lim.Engine, utcMinute(own), lim.FallbackEngine, utcMinute(lim.FallbackUntil))
		}
		body += fmt.Sprintf(" The run is paused and resumes at %s UTC on `%s`; until then the issue is blocked (`%squota`).",
			utcMinute(until), resumeOn, spec.Workflow.BlockedReasonPrefix)
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); err != nil {
			r.log().Error(err, "posting usage limit note")
		}
	}
	routed := r.routed
	return r.patchRunWithCalls(ctx, func(s *aiv1.RunStatus) {
		s.UsageLimits = routed
		s.SetDeferringLimit(deferring)
		// The request stays pending, but nothing is in progress any more.
		s.ActiveRunRequest = s.ObservedRunRequest
		s.CurrentStep = ""
		s.DeferredUntil = &metav1.Time{Time: until}
		s.Deferrals = deferrals
		s.BlockReason = aiv1.BlockQuota
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = textutil.Truncate(lim.Error(), 2000)
		s.CostUSD = aiv1.AddCost(s.CostUSD, out.CostUSD)
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
// knows the engine is limited, the engine is not called at all. A role with a
// fallback whose engine is not limited runs on it instead (see fallBack).
// Once the operator set spec.budgetStop, no call starts: *BudgetStopError.
func (r *Runner) invoke(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	if err := r.budgetStop(ctx); err != nil {
		return nil, err
	}
	if near := r.nearLimit(role); near != nil {
		// fallBack hands the soft limit back when there is no usable
		// fallback: then the role runs on its own engine.
		if res, err := r.fallBack(ctx, role, inv, fullPrompt, nil, near, near); err != error(near) {
			return res, err
		}
	}
	res, err := r.invokeOn(ctx, role, inv, fullPrompt)
	if lim, ok := AsUsageLimit(err); ok {
		lim.Model = inv.Model
		if role.Fallback != nil {
			return r.fallBack(ctx, role, inv, fullPrompt, res, err, lim)
		}
	}
	return res, err
}

// invokeOn runs a role on its own engine (see invoke). The result says
// which engine and model ran.
func (r *Runner) invokeOn(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	inv = r.withSubagents(role, inv)
	inv.LogPrompt = r.logPrompts
	if len(r.changedPins) > 0 && r.run != nil {
		inv.PerRunToolchains = toolchainsRunName(r.run.id)
	}
	if lim, ok := r.activeLimit(role.Engine); ok {
		return nil, &UsageLimitError{Engine: role.Engine, ResetAt: lim.Until.Time, Message: "project-wide limit: " + lim.Message}
	}
	redialed, retried := false, false
	run := func(inv Invocation) (*Result, error) {
		for {
			start := r.now()
			res, err := r.Agent.Run(ctx, role.Engine, inv)
			lost := r.agentLost(ctx, err, start)
			if lost == nil || !lost.notSent {
				r.recordCall(ctx, r.newCall(role.Engine, inv, res, start))
			}
			switch {
			case lost == nil:
				if res != nil {
					res.Engine, res.Model = role.Engine, inv.Model
					r.recordUsage(ctx, role.Engine, res.Usage)
					r.rememberSession(res)
				}
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

// toolchainsRunName turns a run ID ("<task>#<request>") into the name of its
// per-run toolchain directories: [a-z0-9-] and at most 100 characters, the run
// number at the end kept.
func toolchainsRunName(id string) string {
	name := strings.Trim(strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return c
		}
		return '-'
	}, strings.ToLower(id)), "-")
	if len(name) > 100 {
		name = strings.TrimLeft(name[len(name)-100:], "-")
	}
	return name
}

// withSubagents gives a claude call the role's subagents (--agents) and the
// line telling it to delegate to them. role is what the call runs on, so a
// fallback's engine counts, not the configured one. Cursor has no subagents:
// the call runs without them, with a warning once per run and role.
func (r *Runner) withSubagents(role aiv1.RoleSpec, inv Invocation) Invocation {
	if len(role.Subagents) == 0 {
		return inv
	}
	if role.Engine != aiv1.EngineClaude {
		if r.run.warnOnce("subagents/" + inv.Role + "/" + string(role.Engine)) {
			r.log().Info(fmt.Sprintf("subagents are claude only; ignored for role %s on %s", inv.Role, role.Engine))
		}
		return inv
	}
	agents, err := role.AgentsJSON()
	if err != nil {
		r.log().Error(err, "subagents ignored", "role", inv.Role)
		return inv
	}
	names := make([]string, len(role.Subagents))
	for i, s := range role.Subagents {
		names[i] = s.Name
	}
	inv.Agents = agents
	inv.SystemPrompt += "\n\n" + subagentsNote(names)
	return inv
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

// reportFailure tells the issue why its run failed. A run that failed for an
// outside reason (block) leaves the labels to the operator, which blocks the
// issue; any other moves it to needs-input.
func (r *Runner) reportFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, runErr error, block aiv1.BlockReason) {
	wf := spec.Workflow
	// The error quotes CLIs and git: no fence breakout, quick actions or
	// mentions.
	msg := notify.Defuse(strings.ReplaceAll(textutil.Truncate(runErr.Error(), 3000), "`", "'"))
	body := fmt.Sprintf("🤖 The agent run failed:\n\n```\n%s\n```\n\n", msg)
	// The operator retries an infra failure once per run request.
	retry := block == aiv1.BlockInfra && task.Spec.Source.InfraRetry != task.Spec.RunRequest
	switch {
	case retry:
		body += fmt.Sprintf("The cause is outside the issue, so it is blocked (`%sinfra`) and the operator starts one new run at %s UTC (`lifecycle.infraRetryAfter`). To try earlier, ",
			wf.BlockedReasonPrefix, r.now().Add(spec.Lifecycle.InfraRetryAfter.Duration).UTC().Format("2006-01-02 15:04"))
	case block == aiv1.BlockInfra:
		body += fmt.Sprintf("This was the automatic retry, so the issue stays blocked (`%sinfra`) and waits for you. Once the cause is fixed, ", wf.BlockedReasonPrefix)
	case block != "":
		body += fmt.Sprintf("The issue is blocked (`%s%s`) and waits for you. To try again, ", wf.BlockedReasonPrefix, block)
	default:
		body += "To try again, "
	}
	body += fmt.Sprintf("comment `%s retry` (or move the issue back to `%s`).", wf.CommandPrefix, wf.ReadyLabel)
	// On autopilot the pause on the tracking issue mentions the owners; a
	// blocked issue does not pause it, and pings once it waits for a human.
	if block != "" && !retry && spec.Notify.Wants(aiv1.NotifyBlocked) || block == "" && spec.Notify.Wants(aiv1.NotifyFailed) && !task.Spec.Autopilot {
		body += notify.Mention(task.Spec.Owners)
	}
	body += r.run.markdown(out.CostUSD)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); err != nil {
		r.log().Error(err, "posting failure note")
	}
	if block != "" {
		return
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, task.Spec.Issue.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.NeedsInputLabel}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		r.log().Error(err, "updating labels after failure")
	}
}

// reportSpecFailure tells the milestone's owners on its autopilot tracking
// issue why planning failed. The operator plans again once after an outside
// failure (block); milestones without a tracking issue only log it.
func (r *Runner) reportSpecFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, runErr error, block aiv1.BlockReason) {
	ap := task.Status.Autopilot
	if ap == nil || ap.TrackingIssueIID == 0 {
		return
	}
	msg := notify.Defuse(strings.ReplaceAll(textutil.Truncate(runErr.Error(), 3000), "`", "'"))
	body := fmt.Sprintf("🤖 Planning failed:\n\n```\n%s\n```\n\n", msg)
	switch {
	case block == aiv1.BlockInfra && task.Spec.Source.InfraRetry != task.Spec.RunRequest:
		body += fmt.Sprintf("The cause is outside the plan, so the operator plans again at %s UTC (`lifecycle.infraRetryAfter`), once.",
			r.now().Add(spec.Lifecycle.InfraRetryAfter.Duration).UTC().Format("2006-01-02 15:04"))
	case block == aiv1.BlockInfra:
		body += "This was the automatic retry. Once the cause is fixed, change the milestone's spec to plan again."
	default:
		body += "To plan again, change the milestone's spec."
	}
	// No autopilot pause covers planning: the owners hear of it here.
	if spec.Notify.Wants(aiv1.NotifyFailed) {
		body += notify.Mention(task.Spec.Owners)
	}
	body += r.run.markdown(out.CostUSD)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, ap.TrackingIssueIID, body); err != nil {
		r.log().Error(err, "posting planning failure note", "issue", ap.TrackingIssueIID)
	}
}
