package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// The phases of runImplement one at a time, each on its own: the happy path
// and one failure. TestImplementEndToEnd and the other run tests cover them
// together.

// phaseRun sets up an implement run of the env's task the way Step does, so
// its phases can be called one by one.
func phaseRun(t *testing.T, e *implementEnv, engine Engine) *implementRun {
	t.Helper()
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	task, project, err := r.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec := project.Spec.WithDefaults()
	r.projectSpec = spec
	r.run = newRunLog(r.Log, task)
	// The progress comment is edited by a timer goroutine: stop it, so no
	// edit outlives the test.
	t.Cleanup(func() {
		if r.progress != nil {
			_ = r.progress.close(context.Background(), "")
		}
	})
	return r.newImplementRun(e.gl, task, project, spec)
}

// phases prepares run and runs the given phases after it, in order.
func phases(t *testing.T, run *implementRun, ps ...func(context.Context) error) {
	t.Helper()
	ctx := context.Background()
	if closed, err := run.prepare(ctx); err != nil || closed {
		t.Fatalf("prepare: closed=%v err=%v", closed, err)
	}
	for i, p := range ps {
		if err := p(ctx); err != nil {
			t.Fatalf("phase %d after prepare: %v", i+1, err)
		}
	}
}

// upToPrompt are the phases up to the implementer's call.
func upToPrompt(run *implementRun) []func(context.Context) error {
	return []func(context.Context) error{run.syncBranch, run.readComments, run.buildPrompt, run.addInputs, run.mergeBase}
}

// failedSteps names the run's failed steps.
func failedSteps(run *implementRun) []string {
	var names []string
	for _, s := range run.r.run.steps {
		if s.failed {
			names = append(names, s.name)
		}
	}
	return names
}

// brokenEngine fails every implementer call; reviewers find nothing.
type brokenEngine struct{}

func (brokenEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	if inv.ReadOnly {
		return &Result{Structured: []byte(`{"summary":"fine"}`)}, nil
	}
	return nil, errors.New("engine broke")
}

// jobsDown is a GitLab whose pipeline jobs cannot be listed.
type jobsDown struct{ *gitlabfake.Fake }

func (jobsDown) ListPipelineJobs(context.Context, string, int64, string) ([]gitlab.Job, error) {
	return nil, errors.New("gitlab down")
}

func TestImplementPrepare(t *testing.T) {
	ctx := context.Background()
	t.Run("reads the issue, its milestone and the project", func(t *testing.T) {
		e := newImplementEnv(t)
		m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "Ship the new checkout."}
		e.gl.Milestones = []gitlab.Milestone{m}
		e.gl.Issues[42].Milestone = &m
		run := phaseRun(t, e, &scriptedEngine{})
		if closed, err := run.prepare(ctx); err != nil || closed {
			t.Fatalf("closed=%v err=%v", closed, err)
		}
		if run.issue.IID != 42 || run.milestone.Title != "Checkout v2" || run.base != "main" {
			t.Fatalf("issue %+v, milestone %+v, base %q", run.issue, run.milestone, run.base)
		}
		if got := stepNames(run.r.run); !slices.Equal(got, []string{"read issue", "read milestone", "read project"}) {
			t.Fatalf("steps %v", got)
		}
	})
	t.Run("a closed issue ends the run", func(t *testing.T) {
		e := newImplementEnv(t)
		e.gl.Issues[42].State = "closed"
		run := phaseRun(t, e, &scriptedEngine{})
		if closed, err := run.prepare(ctx); err != nil || !closed || run.out.Result != aiv1.RunResultSucceeded {
			t.Fatalf("closed=%v err=%v out=%+v", closed, err, run.out)
		}
	})
	t.Run("a missing issue fails", func(t *testing.T) {
		e := newImplementEnv(t)
		delete(e.gl.Issues, 42)
		run := phaseRun(t, e, &scriptedEngine{})
		if _, err := run.prepare(ctx); err == nil || !strings.Contains(err.Error(), "getting issue") {
			t.Fatalf("expected the issue lookup to fail, got %v", err)
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"read issue"}) {
			t.Fatalf("failed steps %v", got)
		}
	})
}

func TestImplementSyncBranch(t *testing.T) {
	ctx := context.Background()
	t.Run("opens the draft merge request and records it", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, run.syncBranch)
		if run.branch != "agent/42-add-paypal-button" || run.mr == nil || run.mr.IID != 1 || !run.mr.Draft || run.out.MRIID != 1 {
			t.Fatalf("branch %q, mr %+v, out %+v", run.branch, run.mr, run.out)
		}
		var task aiv1.AgentTask
		_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
		if task.Status.Run.MergeRequestIID != 1 || task.Status.Run.Branch != run.branch {
			t.Fatalf("the MR must be recorded at once: %+v", task.Status.Run)
		}
		gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", "--verify", run.branch)
	})
	t.Run("an unreachable repository fails before any merge request", func(t *testing.T) {
		e := newImplementEnv(t)
		e.gl.Project.HTTPURLToRepo = filepath.Join(e.tmp, "missing.git")
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run)
		if err := run.syncBranch(ctx); err == nil {
			t.Fatal("expected the clone to fail")
		}
		if len(e.gl.MRs) != 0 || run.mr != nil {
			t.Fatalf("no merge request may be opened: %+v", e.gl.MRs)
		}
	})
}

func TestImplementReadComments(t *testing.T) {
	ctx := context.Background()
	t.Run("reads the thread, the agent notes and the revisits", func(t *testing.T) {
		e := newImplementEnv(t)
		human := gitlab.User{ID: 7, Username: "jonathan"}
		e.gl.AddNote(42, human, "Use the sandbox account.")
		notes := e.gl.AddNote(42, e.gl.Bot, renderHandoff("PayPal half done.", "x#1", 0))
		revisit := e.gl.AddNote(42, human, "/agent revisit D42-1 use a file")
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, run.syncBranch, run.readComments)
		if run.handoffNote == nil || run.handoffNote.ID != notes.ID || run.prior.Text != "PayPal half done." {
			t.Fatalf("agent notes %+v %+v", run.handoffNote, run.prior)
		}
		// The agent notes are not a comment; the pointer to the MR is.
		if len(run.issueNotes) != 3 || len(run.newIssueNotes) != 3 || slices.ContainsFunc(run.issueNotes, func(n gitlab.Note) bool { return n.ID == notes.ID }) {
			t.Fatalf("issue notes %+v", run.issueNotes)
		}
		if len(run.revisits) != 1 || run.revisits[0].ID != "D42-1" || run.seenNote < revisit.ID || run.state == nil {
			t.Fatalf("revisits %+v, seen %d, state %v", run.revisits, run.seenNote, run.state)
		}
	})
	t.Run("a corrupt state fails before anything is read", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, run.syncBranch)
		if err := os.WriteFile(run.r.stateFile(), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run.readComments(ctx); err == nil || !strings.Contains(err.Error(), "parsing the runner state") {
			t.Fatalf("expected the state error, got %v", err)
		}
		if slices.Contains(stepNames(run.r.run), "read comments") {
			t.Fatal("no comments may be read without the state")
		}
	})
}

func TestImplementBuildPrompt(t *testing.T) {
	ctx := context.Background()
	ciRun := func(t *testing.T) (*implementEnv, *implementRun) {
		e := newImplementEnv(t)
		e.gl.AddNote(42, gitlab.User{ID: 7, Username: "jonathan"}, "Use the sandbox account.")
		e.gl.AddNote(42, gitlab.User{ID: 7, Username: "jonathan"}, "/agent revisit D42-1 use a file")
		e.gl.Jobs[5] = []gitlab.Job{{ID: 51, Name: "test", Stage: "test", Status: "failed"}}
		e.gl.Traces[51] = "FAIL TestPayPal"
		run := phaseRun(t, e, &scriptedEngine{})
		run.task.Spec.Source.CIFixPipelineID = 5
		return e, run
	}
	t.Run("builds the prompts with the revisits and the failed jobs", func(t *testing.T) {
		_, run := ciRun(t)
		phases(t, run, run.syncBranch, run.readComments, run.buildPrompt, run.addInputs)
		if !run.stepwise || run.inv.Resume != "" || run.inv.Kind != aiv1.CallKindImplement || run.inv.JSONSchema != implementStepSchema {
			t.Fatalf("a draft MR's first run works in steps, in a new session: %+v", run.inv)
		}
		for _, want := range []string{"Use the sandbox account.", "use a file", "FAIL TestPayPal"} {
			if !strings.Contains(run.fullPrompt, want) || !strings.Contains(run.inv.Prompt, want) {
				t.Fatalf("both prompts must contain %q:\n%s\n---\n%s", want, run.fullPrompt, run.inv.Prompt)
			}
		}
		if !strings.Contains(run.fullPrompt, "Implement issue #42") {
			t.Fatalf("the prompt of a new session starts with the issue:\n%s", run.fullPrompt)
		}
		if !run.ciFix || run.out.CIFixedPipelineID != 5 || run.humanInput != 2 {
			t.Fatalf("ciFix=%v out=%+v humanInput=%d", run.ciFix, run.out, run.humanInput)
		}
		if !slices.ContainsFunc(run.notices, func(n string) bool { return strings.Contains(n, "D42-1 was not found") }) {
			t.Fatalf("notices %q", run.notices)
		}
	})
	t.Run("failed jobs that cannot be read fail", func(t *testing.T) {
		e, run := ciRun(t)
		phases(t, run, run.syncBranch, run.readComments, run.buildPrompt)
		run.gl = jobsDown{e.gl}
		if err := run.addInputs(ctx); err == nil || !strings.Contains(err.Error(), "listing failed jobs of pipeline 5") {
			t.Fatalf("expected the jobs to fail, got %v", err)
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"read failed CI jobs"}) {
			t.Fatalf("failed steps %v", got)
		}
	})
}

// conflictPhaseRun runs the task once, lets main change steady.go and sets
// up a conflict run for the MR head.
func conflictPhaseRun(t *testing.T, engine *conflictEngine) (*implementEnv, *implementRun) {
	t.Helper()
	e := newImplementEnv(t)
	if _, err := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name).Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.seed, "steady.go"), []byte("package app // main\n"), 0o644)
	gitCmd(t, e.seed, "add", ".")
	gitCmd(t, e.seed, "commit", "-m", "main moves on")
	gitCmd(t, e.seed, "push", "origin", "HEAD:main")
	run := phaseRun(t, e, engine)
	run.task.Spec.Source.MergeBaseFor = run.task.Status.Run.PushedSHA
	return e, run
}

func TestImplementMergeBase(t *testing.T) {
	ctx := context.Background()
	t.Run("conflicts go to the implementer, who resolves them", func(t *testing.T) {
		_, run := conflictPhaseRun(t, &conflictEngine{resolution: "package app // both\n"})
		phases(t, run, upToPrompt(run)...)
		if !slices.Equal(run.merge.Conflicts, []string{"steady.go"}) || !run.runAgent || run.stepwise || !strings.Contains(run.inv.Prompt, "## Merge conflicts") {
			t.Fatalf("merge %+v, runAgent=%v, stepwise=%v, prompt:\n%s", run.merge, run.runAgent, run.stepwise, run.inv.Prompt)
		}
		if err := run.implement(ctx); err != nil || run.inv.Kind != aiv1.CallKindConflict {
			t.Fatalf("implement: %v, kind %q", err, run.inv.Kind)
		}
		if err := run.concludeMerge(ctx); err != nil || run.answer.Status != "done" {
			t.Fatalf("concludeMerge: %v, status %q", err, run.answer.Status)
		}
		if !slices.ContainsFunc(run.notices, func(n string) bool { return strings.Contains(n, "resolved the conflicts in `steady.go`") }) {
			t.Fatalf("notices %q", run.notices)
		}
	})
	t.Run("an unresolvable conflict undoes the merge and needs input", func(t *testing.T) {
		e, run := conflictPhaseRun(t, &conflictEngine{}) // leaves the markers
		phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge)...)
		if run.answer.Status != "needs_input" || !slices.ContainsFunc(run.notices, func(n string) bool { return strings.Contains(n, "Could not resolve the merge conflicts") }) {
			t.Fatalf("status %q, notices %q", run.answer.Status, run.notices)
		}
		checkout := filepath.Join(e.tmp, "agent", "repo")
		if head := gitCmd(t, checkout, "rev-parse", "HEAD"); head != run.task.Spec.Source.MergeBaseFor {
			t.Fatalf("the branch must be back where it was: %s, want %s", head, run.task.Spec.Source.MergeBaseFor)
		}
	})
}

func TestImplementImplement(t *testing.T) {
	ctx := context.Background()
	t.Run("runs the implementer and keeps its session", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, append(upToPrompt(run), run.implement)...)
		if run.answer.Status != "done" || run.answer.Summary != "Added PayPal." || run.res.SessionID != "sess-impl" || run.out.CostUSD != 0.5 {
			t.Fatalf("answer %+v, res %+v, out %+v", run.answer, run.res, run.out)
		}
		st, err := run.r.loadState()
		if err != nil || st.session("implement", aiv1.EngineClaude) != "sess-impl" || st.LastIssueNoteID == 0 {
			t.Fatalf("the session must be saved: %+v %v", st, err)
		}
		// Fix rounds finish in one go.
		if run.inv.JSONSchema != implementSchema || strings.Contains(run.inv.SystemPrompt, stepsNote) {
			t.Fatal("later calls must not work in steps")
		}
	})
	t.Run("an engine error fails the run without a session", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, brokenEngine{})
		phases(t, run, upToPrompt(run)...)
		if err := run.implement(ctx); err == nil || !strings.Contains(err.Error(), "engine broke") {
			t.Fatalf("expected the engine error, got %v", err)
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"run implementer"}) {
			t.Fatalf("failed steps %v", got)
		}
		if st, err := run.r.loadState(); err != nil || len(st.Sessions) != 0 {
			t.Fatalf("no session may be saved: %+v %v", st, err)
		}
	})
}

func TestImplementReviewLoop(t *testing.T) {
	t.Run("a blocker is fixed in a fix round", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge, run.reviewLoop)...)
		want := []string{"run implementer", "commit", "review round 1", "fix round 1", "commit", "review round 2"}
		if got := stepNames(run.r.run); !slices.Equal(got[len(got)-len(want):], want) {
			t.Fatalf("steps %v", got)
		}
		if !run.out.Reviewed || run.out.BlockingFindings != 0 || !run.reviewedLast || len(run.remaining) != 1 || run.answer.Summary != "Added PayPal, fixed nil deref." {
			t.Fatalf("out %+v, reviewedLast=%v, remaining %+v, answer %+v", run.out, run.reviewedLast, run.remaining, run.answer)
		}
	})
	t.Run("at the maximum iterations the blocker stays and the MR a draft", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		zero := int32(0)
		run.spec.Agents.Review.MaxIterations = &zero
		phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge, run.reviewLoop, run.push)...)
		if slices.Contains(stepNames(run.r.run), "fix round 1") || run.out.BlockingFindings != 1 || len(run.remaining) != 1 || !run.remaining[0].Blocking() {
			t.Fatalf("steps %v, out %+v, remaining %+v", stepNames(run.r.run), run.out, run.remaining)
		}
		if !e.gl.MRs[1].Draft || run.out.PushedSHA == "" {
			t.Fatalf("the work is pushed, the MR stays a draft: %+v, out %+v", e.gl.MRs[1], run.out)
		}
	})
}

func TestImplementPush(t *testing.T) {
	ctx := context.Background()
	upToPush := func(run *implementRun) []func(context.Context) error {
		return append(upToPrompt(run), run.implement, run.concludeMerge, run.reviewLoop)
	}
	t.Run("marks the reviewed work ready and pushes it", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, append(upToPush(run), run.push)...)
		head := gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", run.branch)
		if !run.pushed || run.out.PushedSHA != head || run.out.ReviewedSHA != head || e.gl.MRs[1].Draft {
			t.Fatalf("pushed=%v out %+v head %s, MR %+v", run.pushed, run.out, head, e.gl.MRs[1])
		}
	})
	t.Run("a rejected push fails and puts the MR back to draft", func(t *testing.T) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, upToPush(run)...)
		// Meanwhile a human pushes to the branch.
		gitCmd(t, e.seed, "fetch", "origin")
		gitCmd(t, e.seed, "checkout", "-b", run.branch, "origin/"+run.branch)
		_ = os.WriteFile(filepath.Join(e.seed, "HUMAN.md"), []byte("by hand\n"), 0o644)
		gitCmd(t, e.seed, "add", ".")
		gitCmd(t, e.seed, "commit", "-m", "Human tweak")
		gitCmd(t, e.seed, "push", "origin", "HEAD:"+run.branch)
		if err := run.push(ctx); err == nil {
			t.Fatal("expected the push to be rejected")
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"push"}) || !e.gl.MRs[1].Draft || run.out.PushedSHA != "" {
			t.Fatalf("failed steps %v, MR %+v, out %+v", got, e.gl.MRs[1], run.out)
		}
	})
}

func TestImplementPostDecisionsAndHandoff(t *testing.T) {
	ctx := context.Background()
	decided := func(t *testing.T) (*implementEnv, *implementRun) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		phases(t, run, append(upToPrompt(run), run.implement)...)
		run.answer.Decisions = []Decision{{Title: "Storage", Question: "Where?", Choice: "a table", Why: "simple"}}
		return e, run
	}
	t.Run("stores the notes, posts the decisions and saves the state", func(t *testing.T) {
		e, run := decided(t)
		if err := run.postDecisionsAndHandoff(ctx); err != nil {
			t.Fatal(err)
		}
		var bodies string
		for _, n := range e.gl.IssueNotes[42] {
			bodies += n.Body + "\n"
		}
		if !strings.Contains(bodies, "PayPal button in paypal.go.") || !strings.Contains(bodies, "D42-1") || !strings.Contains(bodies, "Storage") {
			t.Fatalf("issue notes:\n%s", bodies)
		}
		if st, err := run.r.loadState(); err != nil || st.RevisitNoteID != run.seenNote || st.RevisitNoteID == 0 {
			t.Fatalf("the revisits must count as handled: %+v %v (seen %d)", st, err, run.seenNote)
		}
	})
	t.Run("a GitLab error is reported but does not fail the run", func(t *testing.T) {
		e, run := decided(t)
		e.gl.NoteErrors = map[int64]error{42: errors.New("gitlab down")}
		if err := run.postDecisionsAndHandoff(ctx); err != nil {
			t.Fatalf("notes and decisions are best effort: %v", err)
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"update agent notes", "post decisions"}) {
			t.Fatalf("failed steps %v", got)
		}
	})
}

// reports are the MR comments besides the live progress, which Step removes
// after the run.
func reports(e *implementEnv, run *implementRun) []string {
	var bodies []string
	// Listed under the fake's lock: the progress timer may edit a note
	// meanwhile.
	notes, _ := e.gl.ListMergeRequestNotes(context.Background(), "", 1)
	for _, n := range notes {
		if !isProgress(n, run.botID) {
			bodies = append(bodies, n.Body)
		}
	}
	return bodies
}

func TestImplementReport(t *testing.T) {
	ctx := context.Background()
	reported := func(t *testing.T) (*implementEnv, *implementRun) {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		run.spec.CI.Disabled = true
		phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge, run.reviewLoop, run.push, run.postDecisionsAndHandoff)...)
		return e, run
	}
	t.Run("describes, comments and moves the issue to review", func(t *testing.T) {
		e, run := reported(t)
		if err := run.report(ctx); err != nil {
			t.Fatal(err)
		}
		if reports := reports(e, run); len(reports) != 1 || !strings.Contains(reports[0], "Described the change") || !strings.Contains(reports[0], "Run log") {
			t.Fatalf("reports %q", reports)
		}
		if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
			t.Fatalf("labels %v", labels)
		}
		if out := run.outcome(ctx); out.Result != aiv1.RunResultSucceeded || out.Summary != "Added PayPal, fixed nil deref." {
			t.Fatalf("outcome %+v", out)
		}
	})
	t.Run("a label update error fails after the comment", func(t *testing.T) {
		e, run := reported(t)
		e.gl.UpdateErrors = map[int64]error{42: errors.New("gitlab down")}
		if err := run.report(ctx); err == nil || !strings.Contains(err.Error(), "updating issue labels") {
			t.Fatalf("expected the label update to fail, got %v", err)
		}
		if got := failedSteps(run); !slices.Equal(got, []string{"update issue labels"}) || len(reports(e, run)) != 1 {
			t.Fatalf("failed steps %v, reports %q", got, reports(e, run))
		}
	})
}

func TestImplementOutcome(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	run := phaseRun(t, e, &scriptedEngine{})
	run.needsInput, run.reviewAsked, run.reviewFor = true, true, "abc123"
	run.answer.Summary = "Which provider?"
	if out := run.outcome(ctx); out.Result != aiv1.RunResultNeedsInput || out.ReviewedFor != "abc123" || out.Summary != "Which provider?" {
		t.Fatalf("outcome %+v", out)
	}
	// A request that came meanwhile still sees the review.
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	task.Spec.RunRequest++
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	run.out.ReviewedFor = ""
	if out := run.outcome(ctx); out.ReviewedFor != "" {
		t.Fatalf("a pending request keeps the review: %+v", out)
	}
}
