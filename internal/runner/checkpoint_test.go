package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const cpBranch = "agent/42-add-paypal-button"

// cutEngine cuts the implementer off: in the implementer calls cutIf picks
// (n counts them from 1) it edits wip.go and then fails with fail. Every
// other call goes to next.
type cutEngine struct {
	next  Engine
	cutIf func(n int, inv Invocation) bool
	fail  error

	mu      sync.Mutex
	n       int
	prompts []string
}

func (c *cutEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if !inv.ReadOnly {
		c.mu.Lock()
		c.n++
		c.prompts = append(c.prompts, inv.Prompt)
		hit := c.cutIf(c.n, inv)
		c.mu.Unlock()
		if hit {
			_ = os.WriteFile(filepath.Join(inv.Dir, "wip.go"), []byte("package app\n\nfunc wip() {}\n"), 0o644)
			// With a session: a resumed call is not tried again fresh.
			return &Result{IsError: true, SessionID: "sess-cut", Text: "Working on wip.go: 2 of 3 todos done"}, c.fail
		}
	}
	return c.next.Run(ctx, inv)
}

func cutAt(at int) func(int, Invocation) bool {
	return func(n int, _ Invocation) bool { return n == at }
}

// quietEngine is done without changing anything; reviewers find nothing.
type quietEngine struct{}

func (quietEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	var v any = ImplementAnswer{Status: "done", Summary: "Nothing left to do.", CommitMessage: "Nothing"}
	switch {
	case inv.JSONSchema == describeSchema:
		v = DescribeAnswer{Why: "w", What: "w", How: "h", Verification: "v"}
	case inv.ReadOnly:
		v = ReviewAnswer{Summary: "fine"}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-quiet", Structured: b}, nil
}

// recordPushes makes the origin take push options and log every ref update
// as "<options> <old> <new> <ref>".
func (e *limitEnv) recordPushes() string {
	e.t.Helper()
	log := filepath.Join(e.tmp, "pushes.log")
	gitCmd(e.t, e.tmp, "--git-dir", e.origin, "config", "receive.advertisePushOptions", "true")
	hook := "#!/bin/sh\nopts=\"\"\ni=0\nwhile [ \"$i\" -lt \"${GIT_PUSH_OPTION_COUNT:-0}\" ]; do eval \"opts=\\\"$opts\\$GIT_PUSH_OPTION_$i,\\\"\"; i=$((i+1)); done\n" +
		"while read old new ref; do echo \"[$opts] $old $new $ref\" >> " + log + "; done\n"
	if err := os.WriteFile(filepath.Join(e.origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		e.t.Fatal(err)
	}
	return log
}

// pushOf returns the logged push that moved the branch to sha.
func pushOf(t *testing.T, log, sha string) string {
	t.Helper()
	b, _ := os.ReadFile(log)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, " "+sha+" refs/heads/"+cpBranch) {
			return line
		}
	}
	t.Fatalf("no push of %s in:\n%s", sha, b)
	return ""
}

func (e *limitEnv) origin1(args ...string) string {
	e.t.Helper()
	return gitCmd(e.t, e.tmp, append([]string{"--git-dir", e.origin}, args...)...)
}

func (e *limitEnv) updateProject(mutate func(*aiv1.AgentProject)) {
	e.t.Helper()
	var project aiv1.AgentProject
	ctx := context.Background()
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project); err != nil {
		e.t.Fatal(err)
	}
	mutate(&project)
	if err := e.k8s.Update(ctx, &project); err != nil {
		e.t.Fatal(err)
	}
}

// requestRun asks for the next run, with mutate applied to the task.
func (e *limitEnv) requestRun(mutate func(*aiv1.AgentTask)) {
	e.t.Helper()
	task := e.task()
	orig := task.DeepCopy()
	task.Spec.RunRequest++
	if mutate != nil {
		mutate(&task)
	}
	if err := e.k8s.Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *limitEnv) stepResult(name string) (int, string) {
	for i, s := range e.r.run.steps {
		if s.name == name {
			return i, s.result
		}
	}
	return -1, ""
}

// checkCheckpoint checks that the branch head is a pushed checkpoint of
// run runNo and step with reason, holding wip.go, recorded in the task status.
func (e *limitEnv) checkCheckpoint(runNo, step int, reason string) string {
	e.t.Helper()
	head := e.origin1("rev-parse", cpBranch)
	msg := e.origin1("log", "-1", "--format=%B", cpBranch)
	subject := "chore(wip): checkpoint #42 step " + strconv.Itoa(step) + " (" + reason + ")"
	if !strings.HasPrefix(msg, subject+"\n") || !strings.Contains(msg, "Agent-Checkpoint: "+strconv.Itoa(runNo)+"/"+strconv.Itoa(step)+" "+reason) ||
		!strings.Contains(msg, "2 of 3 todos done") || !strings.Contains(msg, "wip.go") {
		e.t.Fatalf("the head is no checkpoint %q:\n%s", subject, msg)
	}
	if files := e.origin1("ls-tree", "--name-only", cpBranch); !strings.Contains(files, "wip.go") {
		e.t.Fatalf("the checkpoint misses the step's edits: %s", files)
	}
	run := e.task().Status.Run
	cp := run.Checkpoint
	if cp == nil || cp.SHA != head || cp.Branch != cpBranch || cp.Reason != reason || cp.Run != int64(runNo) || cp.Step != int32(step) ||
		cp.Files != 1 || cp.Added != 3 || cp.Deleted != 0 || cp.At.IsZero() {
		e.t.Fatalf("status.run.checkpoint %+v, head %s", cp, head)
	}
	if run.PushedSHA == head || run.ReviewedSHA == head {
		e.t.Fatalf("PushedSHA %q and ReviewedSHA %q must not be the checkpoint", run.PushedSHA, run.ReviewedSHA)
	}
	if len(e.gl.MRPipelines)+len(e.gl.BranchPipelines) != 0 {
		e.t.Fatalf("no pipeline for a checkpoint: MR %v, branch %v", e.gl.MRPipelines, e.gl.BranchPipelines)
	}
	if mr := e.gl.MRs[1]; !mr.Draft {
		e.t.Fatalf("a checkpoint is pushed to a draft MR: %+v", mr)
	}
	if _, res := e.stepResult("save checkpoint"); !strings.Contains(res, shortSHA(head)+" pushed without CI: 1 files, +3/−0") {
		e.t.Fatalf("run log: save checkpoint %q", res)
	}
	return head
}

func TestCheckpointOnUsageLimit(t *testing.T) {
	reset := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	engine := &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1),
		fail: errors.New("claude reported an error: Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10))}
	e := newLimitEnv(t, engine)
	pushes := e.recordPushes()

	if e.step() {
		t.Fatal("the run is deferred as before")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultDeferred || run.DeferredUntil == nil || !run.DeferredUntil.Time.Equal(reset) {
		t.Fatalf("the usage limit must still defer the run: %+v", run)
	}
	head := e.checkCheckpoint(1, 1, cutUsageLimit)
	// On top of the start commit, pushed with ci.skip and without force.
	if log := strings.Split(e.origin1("log", "--format=%s", cpBranch), "\n"); len(log) != 3 || log[1] != "Start work on #42" || log[2] != "init" {
		t.Fatalf("history %q", log)
	}
	parent := e.origin1("rev-parse", head+"^")
	if line := pushOf(t, pushes, head); !strings.HasPrefix(line, "[ci.skip,] "+parent+" ") {
		t.Fatalf("expected a fast-forward push with ci.skip from %s: %s", parent, line)
	}

	// The next run continues from the checkpoint; its regular push clears it.
	e.now = reset.Add(time.Minute)
	if !e.step() {
		t.Fatal("the run should continue after the reset")
	}
	run = e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.Checkpoint != nil || run.PushedSHA == "" {
		t.Fatalf("a regular push clears the checkpoint: %+v", run)
	}
	if log := e.origin1("log", "--format=%s", cpBranch); !strings.Contains(log, "chore(wip): checkpoint #42 step 1 (usage-limit)") ||
		!strings.Contains(e.origin1("ls-tree", "--name-only", cpBranch), "wip.go") {
		t.Fatalf("the next run builds on the checkpoint:\n%s", log)
	}
}

// TestCheckpointCountsSteps: the second step of a draft MR is cut off; its
// checkpoint follows the pushed first step.
func TestCheckpointCountsSteps(t *testing.T) {
	e := newLimitEnv(t, nil)
	engine := &cutEngine{next: &stepEngine{gl: e.gl, lastChanges: true}, cutIf: cutAt(2), fail: errors.New("claude reported an error: Claude AI usage limit reached")}
	e.r = newTestRunner(t, e.tmp, e.k8s, e.gl, engine, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	if e.step() {
		t.Fatal("the run is deferred")
	}
	head := e.checkCheckpoint(1, 2, cutUsageLimit)
	if parent := e.origin1("log", "-1", "--format=%s", head+"^"); parent != "Add the payment model" {
		t.Fatalf("the checkpoint follows step 1, got %q", parent)
	}
}

func TestCheckpointOnEngineErrorAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		err    string
		reason string
	}{
		{"claude failed: exit status 1: boom", cutEngineError},
		{"claude failed: context deadline exceeded", cutTimeout},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			e := newLimitEnv(t, &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1), fail: errors.New(tc.err)})
			if !e.step() {
				t.Fatal("expected a failed run")
			}
			run := e.task().Status.Run
			if run.LastResult != aiv1.RunResultFailed || !strings.Contains(run.Summary, tc.err) {
				t.Fatalf("the call's error stays the run's: %+v", run)
			}
			e.checkCheckpoint(1, 1, tc.reason)
			// The failure report carries the run log with the checkpoint.
			if notes := strings.Join(e.issueNotes(), "\n"); !strings.Contains(notes, "save checkpoint") {
				t.Fatalf("the failure note should show the checkpoint step:\n%s", notes)
			}
		})
	}
}

// TestCheckpointInCIRoundDraftsReadyMR: a CI fix run on a ready MR that is
// cut off sets the MR to draft before the checkpoint is pushed.
func TestCheckpointInCIRoundDraftsReadyMR(t *testing.T) {
	// Implement and fix round, then the CI fix call.
	engine := &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(3), fail: errors.New("claude failed: exit status 1: boom")}
	e := newLimitEnv(t, engine)
	if !e.step() {
		t.Fatal("first run should happen")
	}
	first := e.task().Status.Run
	if e.gl.MRs[1].Draft || first.PushedSHA == "" {
		t.Fatalf("the first run leaves a ready MR: %+v", e.gl.MRs[1])
	}
	e.gl.MRPipelines, e.gl.BranchPipelines = nil, nil
	pid := e.gl.AddPipeline(first.PushedSHA, cpBranch, "failed", gitlab.Job{Name: "test", Stage: "test", Status: "failed"})
	e.requestRun(func(task *aiv1.AgentTask) { task.Spec.Source.CIFixPipelineID = pid })
	if !e.step() {
		t.Fatal("CI fix run should happen")
	}
	if !strings.Contains(engine.prompts[2], "CI pipeline for your last push failed") {
		t.Fatalf("expected the CI round to be cut off: %q", engine.prompts[2])
	}
	// The CI run's first implementer call is its step 1.
	e.checkCheckpoint(2, 1, cutEngineError)
	if mr := e.gl.MRs[1]; !strings.HasPrefix(mr.Title, "Draft: ") {
		t.Fatalf("the ready MR must be set to draft: %+v", mr)
	}
	cp, _ := e.stepResult("save checkpoint")
	if draft, _ := e.stepResult("mark draft"); draft < cp {
		t.Fatalf("mark draft (%d) belongs to the checkpoint (%d): %v", draft, cp, stepNames(e.r.run))
	}
	if run := e.task().Status.Run; run.PushedSHA != first.PushedSHA || run.ReviewedSHA != first.ReviewedSHA {
		t.Fatalf("PushedSHA and ReviewedSHA stay the first run's: %+v", run)
	}
}

func TestCheckpointOnBudgetStop(t *testing.T) {
	ctx := context.Background()
	e := newLimitEnv(t, &scriptedEngine{})
	if !e.step() {
		t.Fatal("first run should happen")
	}
	e.gl.MRPipelines, e.gl.BranchPipelines = nil, nil
	// The implementer left an edit, then the budget is used.
	if err := os.WriteFile(filepath.Join(e.tmp, "agent", "repo", "wip.go"), []byte("package app\n\nfunc wip() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := e.task()
	task.Spec.BudgetStop = true
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.r.cut = &cutTarget{gl: e.gl, glProject: "group/app", branch: cpBranch, base: "main", issue: 42, mr: &gitlab.MergeRequest{IID: 1}, st: &state{}}
	e.r.cutStep(false)
	e.r.cutStep(false)
	// A later step resumes its session; the budget stop still saves it.
	_, err := e.r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineClaude}, Invocation{Role: "implement", Kind: aiv1.CallKindImplement, Resume: "sess-impl"}, "")
	if !isBudgetStop(err) {
		t.Fatalf("the budget stop stays the error: %v", err)
	}
	head := e.origin1("rev-parse", cpBranch)
	msg := e.origin1("log", "-1", "--format=%B", cpBranch)
	if !strings.HasPrefix(msg, "chore(wip): checkpoint #42 step 2 (budget-stop)\n") || !strings.Contains(msg, "Agent-Checkpoint: 1/2 budget-stop") {
		t.Fatalf("expected a budget-stop checkpoint:\n%s", msg)
	}
	if cp := e.task().Status.Run.Checkpoint; cp == nil || cp.SHA != head || cp.Reason != cutBudgetStop || cp.Step != 2 {
		t.Fatalf("status.run.checkpoint %+v", cp)
	}
	if !e.gl.MRs[1].Draft {
		t.Fatal("the ready MR must be set to draft")
	}

	// A reviewer's call is no implementer call: nothing is saved.
	_ = os.WriteFile(filepath.Join(e.tmp, "agent", "repo", "more.go"), []byte("package app\n"), 0o644)
	if _, err := e.r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineClaude}, Invocation{Role: "bugbot", Kind: aiv1.CallKindReview}, ""); !isBudgetStop(err) {
		t.Fatal(err)
	}
	if now := e.origin1("rev-parse", cpBranch); now != head {
		t.Fatal("no checkpoint for a reviewer")
	}
}

func TestFixRoundUsageLimitMakesNoCheckpoint(t *testing.T) {
	engine := &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(2), fail: errors.New("claude reported an error: Claude AI usage limit reached")}
	e := newLimitEnv(t, engine)
	if !e.step() {
		t.Fatal("the run should complete")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.Checkpoint != nil || run.PushedSHA == "" {
		t.Fatalf("a fix-round limit publishes the work as before: %+v", run)
	}
	if !strings.Contains(engine.prompts[1], "Automated reviewers found") {
		t.Fatalf("expected the fix round to be cut off: %q", engine.prompts[1])
	}
	if log := e.origin1("log", "--format=%s", cpBranch); strings.Contains(log, "checkpoint") {
		t.Fatalf("no checkpoint commit:\n%s", log)
	}
	if slices.Contains(stepNames(e.r.run), "save checkpoint") {
		t.Fatalf("no checkpoint step: %v", stepNames(e.r.run))
	}
}

func TestCheckpointDisabled(t *testing.T) {
	engine := &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1), fail: errors.New("claude reported an error: Claude AI usage limit reached")}
	e := newLimitEnv(t, engine)
	e.updateProject(func(p *aiv1.AgentProject) { p.Spec.Checkpoint.Disabled = true })
	if e.step() {
		t.Fatal("the run is deferred")
	}
	if log := e.origin1("log", "--format=%s", cpBranch); strings.Contains(log, "checkpoint") || strings.Split(log, "\n")[0] != "Start work on #42" {
		t.Fatalf("checkpoints are off:\n%s", log)
	}
	if run := e.task().Status.Run; run.Checkpoint != nil || slices.Contains(stepNames(e.r.run), "save checkpoint") {
		t.Fatalf("checkpoints are off: %+v, steps %v", run.Checkpoint, stepNames(e.r.run))
	}
}

// TestCheckpointHeadIsNoPush: the fallback continues from the checkpoint and
// changes nothing more, so the checkpoint stays the head. push() must not
// take it for the push of a step.
func TestCheckpointHeadIsNoPush(t *testing.T) {
	engine := &cutEngine{next: quietEngine{}, cutIf: func(_ int, inv Invocation) bool { return inv.Model == "auto" }, fail: errors.New(cursorLimit)}
	e := newLimitEnv(t, engine)
	e.updateProject(func(p *aiv1.AgentProject) {
		p.Spec.Agents.Implement.RoleSpec = aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: sonnetFallback()}
	})
	if !e.step() {
		t.Fatal("the fallback finishes the run")
	}
	head := e.checkCheckpoint(1, 1, cutUsageLimit)
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.PushedSHA != "" || run.ReviewedSHA != "" || run.Checkpoint.SHA != head {
		t.Fatalf("the checkpoint head is no push: %+v", run)
	}
	if _, res := e.stepResult("push"); res != "nothing new to push" {
		t.Fatalf("push step: %q", res)
	}
}
