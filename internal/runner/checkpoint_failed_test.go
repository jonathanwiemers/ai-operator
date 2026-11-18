package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// proseEngine is an implementer that answers in prose, not JSON, so the run
// fails after a successful call (the #558 incident). With edit it leaves
// wip.go in the work tree; before, if set, runs first. Every other call goes
// to a scriptedEngine.
type proseEngine struct {
	edit   bool
	before func()
}

func (p proseEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if inv.ReadOnly {
		return (&scriptedEngine{}).Run(ctx, inv)
	}
	if p.before != nil {
		p.before()
	}
	if p.edit {
		_ = os.WriteFile(filepath.Join(inv.Dir, "wip.go"), []byte("package app\n\nfunc wip() {}\n"), 0o644)
	}
	return &Result{SessionID: "sess-prose", Text: "I added wip.go; all done."}, nil
}

// failedDecode checks that the run failed on the implementer's answer.
func (e *limitEnv) failedDecode() aiv1.RunStatus {
	e.t.Helper()
	if !e.step() {
		e.t.Fatal("expected a failed run")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultFailed || !strings.Contains(run.Summary, "decoding implementer answer: no JSON object") {
		e.t.Fatalf("the decode error stays the run's: %+v", run)
	}
	return run
}

func countSteps(rl *runLog, name string) int {
	n := 0
	for _, s := range stepNames(rl) {
		if s == name {
			n++
		}
	}
	return n
}

func TestCheckpointOnFailedRun(t *testing.T) {
	e := newLimitEnv(t, proseEngine{edit: true})
	pushes := e.recordPushes()
	run := e.failedDecode()

	head := e.origin1("rev-parse", cpBranch)
	msg := e.origin1("log", "-1", "--format=%B", cpBranch)
	if !strings.HasPrefix(msg, "chore(wip): checkpoint #42 step 1 (run-failed)\n") || !strings.Contains(msg, "Agent-Checkpoint: 1/1 run-failed") {
		t.Fatalf("expected a run-failed checkpoint:\n%s", msg)
	}
	if files := e.origin1("ls-tree", "--name-only", cpBranch); !strings.Contains(files, "wip.go") {
		t.Fatalf("the checkpoint misses the implementer's edits: %s", files)
	}
	parent := e.origin1("rev-parse", head+"^")
	if line := pushOf(t, pushes, head); !strings.HasPrefix(line, "[ci.skip,] "+parent+" ") {
		t.Fatalf("expected a fast-forward push with ci.skip: %s", line)
	}
	cp := run.Checkpoint
	if cp == nil || cp.SHA != head || cp.Reason != cutRunFailed || cp.Run != 1 || cp.Step != 1 || cp.Files != 1 || cp.Added != 3 || cp.Deleted != 0 {
		t.Fatalf("status.run.checkpoint %+v, head %s", cp, head)
	}
	// No call carries it: the run's checkpoints count it.
	if cps := run.RunCheckpoints; len(cps) != 1 || cps[0].Reason != cutRunFailed || cps[0].SHA != head {
		t.Fatalf("status.run.runCheckpoints = %+v", cps)
	}
	if run.UncommittedLines != 0 || run.PushedSHA == head {
		t.Fatalf("the saved work is no uncommitted lines and no push: %+v", run)
	}
	if !e.gl.MRs[1].Draft || len(e.gl.MRPipelines)+len(e.gl.BranchPipelines) != 0 {
		t.Fatalf("a checkpoint goes to a draft MR without a pipeline: %+v", e.gl.MRs[1])
	}
	line := "💾 Saved a checkpoint (`" + shortSHA(head) + "`, 1 files, +3/−0) because the run failed."
	if notes := strings.Join(e.issueNotes(), "\n"); !strings.Contains(notes, line) {
		t.Fatalf("the failure note should name the checkpoint:\n%s", notes)
	}
}

func TestFailedRunWithCleanTreeTakesNoCheckpoint(t *testing.T) {
	e := newLimitEnv(t, proseEngine{})
	run := e.failedDecode()
	if subject := e.origin1("log", "-1", "--format=%s", cpBranch); subject != "Start work on #42" {
		t.Fatalf("nothing is pushed for a clean tree, head %q", subject)
	}
	if run.Checkpoint != nil || len(run.RunCheckpoints) != 0 || run.UncommittedLines != 0 {
		t.Fatalf("no checkpoint and no lines: %+v", run)
	}
	if _, res := e.stepResult("save checkpoint"); !strings.HasPrefix(res, "nothing to save") {
		t.Fatalf("save checkpoint step: %q", res)
	}
	if notes := strings.Join(e.issueNotes(), "\n"); strings.Contains(notes, "Saved a checkpoint") {
		t.Fatalf("no checkpoint line:\n%s", notes)
	}
}

func TestFailedRunCheckpointNotPushedRecordsLines(t *testing.T) {
	var e *limitEnv
	refuse := func() {
		hook := filepath.Join(e.origin, "hooks", "pre-receive")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e = newLimitEnv(t, proseEngine{edit: true, before: refuse})
	run := e.failedDecode()
	if subject := e.origin1("log", "-1", "--format=%s", cpBranch); subject != "Start work on #42" {
		t.Fatalf("the push was refused, head %q", subject)
	}
	if run.Checkpoint != nil || run.UncommittedLines != 3 {
		t.Fatalf("the unsaved lines are recorded: checkpoint %+v, uncommittedLines %d", run.Checkpoint, run.UncommittedLines)
	}
	if _, res := e.stepResult("save checkpoint"); !strings.Contains(res, "not pushed") {
		t.Fatalf("save checkpoint step: %q", res)
	}

	// The next run starts from a reset workspace and pushes: the lines go.
	e.r = newTestRunner(t, e.tmp, e.k8s, e.gl, &scriptedEngine{}, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	_ = os.Remove(filepath.Join(e.origin, "hooks", "pre-receive"))
	e.requestRun(nil)
	if !e.step() {
		t.Fatal("the next run should happen")
	}
	if run := e.task().Status.Run; run.LastResult != aiv1.RunResultSucceeded || run.UncommittedLines != 0 {
		t.Fatalf("a pushing run clears the lines: %+v", run)
	}
}

func TestFailedRunWithCheckpointsDisabledRecordsLines(t *testing.T) {
	e := newLimitEnv(t, proseEngine{edit: true})
	e.updateProject(func(p *aiv1.AgentProject) { p.Spec.Checkpoint.Disabled = true })
	run := e.failedDecode()
	if run.Checkpoint != nil || run.UncommittedLines != 3 || countSteps(e.r.run, "save checkpoint") != 0 {
		t.Fatalf("checkpoints are off, the lines are recorded: %+v, steps %v", run, stepNames(e.r.run))
	}
}

// TestCutRunTakesNoSecondCheckpoint: a call cut off by an engine error saved
// its work already; the failed run does not try again.
func TestCutRunTakesNoSecondCheckpoint(t *testing.T) {
	e := newLimitEnv(t, &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1), fail: errors.New("claude failed: exit status 1: boom")})
	if !e.step() {
		t.Fatal("expected a failed run")
	}
	e.checkCheckpoint(1, 1, cutEngineError)
	if n := countSteps(e.r.run, "save checkpoint"); n != 1 {
		t.Fatalf("one checkpoint step, got %d: %v", n, stepNames(e.r.run))
	}
	if run := e.task().Status.Run; len(run.RunCheckpoints) != 0 || run.UncommittedLines != 0 {
		t.Fatalf("no run-failed checkpoint: %+v", run)
	}
}

// TestUsageLimitTakesNoSecondCheckpoint: the deferred run keeps its
// usage-limit checkpoint only.
func TestUsageLimitTakesNoSecondCheckpoint(t *testing.T) {
	e := newLimitEnv(t, &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1), fail: errors.New("claude reported an error: Claude AI usage limit reached")})
	if e.step() {
		t.Fatal("the run is deferred")
	}
	if n := countSteps(e.r.run, "save checkpoint"); n != 1 {
		t.Fatalf("one checkpoint step, got %d: %v", n, stepNames(e.r.run))
	}
}
