package runner

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// hookEngine calls hook once, before the first implementer call it passes
// on to next.
type hookEngine struct {
	next Engine
	hook func()
}

func (h *hookEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if !inv.ReadOnly && h.hook != nil {
		h.hook()
		h.hook = nil
	}
	return h.next.Run(ctx, inv)
}

// TestFoldLeaseRejected: someone pushes on top of the checkpoint while the
// next run's implementer works. The lease of the fold fails, so the
// checkpoint is kept, the step's commit goes on top of it, the normal push
// fails as any push after someone else's does, and the report says so.
func TestFoldLeaseRejected(t *testing.T) {
	reset := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	cut := &cutEngine{next: &scriptedEngine{}, cutIf: cutAt(1),
		fail: errors.New("claude reported an error: Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10))}
	engine := &hookEngine{next: cut}
	e := newLimitEnv(t, engine)
	if e.step() {
		t.Fatal("the run is deferred")
	}
	cp := e.checkCheckpoint(1, 1, cutUsageLimit)

	var human string
	engine.hook = func() {
		seed := filepath.Join(e.tmp, "seed")
		gitCmd(t, seed, "fetch", "origin", cpBranch)
		gitCmd(t, seed, "checkout", "-B", "human", "FETCH_HEAD")
		writeFile(t, seed, "human.go", "package app\n")
		gitCmd(t, seed, "add", ".")
		gitCmd(t, seed, "commit", "-m", "Human fix")
		gitCmd(t, seed, "push", "origin", "HEAD:"+cpBranch)
		human = gitCmd(t, seed, "rev-parse", "HEAD")
	}
	e.now = reset.Add(time.Minute)
	if !e.step() {
		t.Fatal("the run should continue after the reset")
	}
	if human == "" {
		t.Fatal("the human push did not happen")
	}
	// Nothing was forced: the remote has the human commit on the checkpoint.
	if head := e.origin1("rev-parse", cpBranch); head != human || e.origin1("rev-parse", human+"^") != cp {
		t.Fatalf("the remote must keep the human commit on the checkpoint:\n%s", e.origin1("log", "--format=%h %s", cpBranch))
	}
	// The step's commits are on the checkpoint in the checkout.
	ws := filepath.Join(e.tmp, "agent", "repo")
	if log := strings.Split(gitCmd(t, ws, "log", "--format=%s", "-3"), "\n"); !slices.Equal(log, []string{"Fix nil deref", "Add PayPal button", "chore(wip): checkpoint #42 step 1 (usage-limit)"}) {
		t.Fatalf("checkout history %q", log)
	}
	if got := gitCmd(t, ws, "rev-parse", "HEAD~2"); got != cp {
		t.Fatalf("the step's commit goes on the checkpoint %s, got %s", cp, got)
	}
	if _, res := e.stepResult("fold checkpoints"); res != "checkpoint kept: someone pushed to the branch" {
		t.Fatalf("fold checkpoints step: %q (%v)", res, stepNames(e.r.run))
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultFailed || run.Checkpoint == nil || run.Checkpoint.SHA != cp {
		t.Fatalf("the push fails as before and the checkpoint stays recorded: %+v", run)
	}
	notice := "The checkpoint `" + shortSHA(cp) + "` was kept: someone pushed to the branch, so it was not folded into the step's commit."
	if notes := strings.Join(e.issueNotes(), "\n"); !strings.Contains(notes, notice) {
		t.Fatalf("the report should say the checkpoint was kept:\n%s", notes)
	}
}
