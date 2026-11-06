package runner

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// foldWorkspace is checkpointWorkspace with a pushed step commit on the
// branch. It returns the step commit.
func foldWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	w := checkpointWorkspace(t)
	writeFile(t, w.Dir, "model.go", "package app\n\ntype Model struct{}\n")
	gitCmd(t, w.Dir, "add", ".")
	gitCmd(t, w.Dir, "commit", "-m", "feat: add the model")
	gitCmd(t, w.Dir, "push", "origin", "agent/7-work")
	return w, gitCmd(t, w.Dir, "rev-parse", "HEAD")
}

// saveCheckpoint writes name and commits it as a checkpoint; push also
// pushes it, which moves the checkout's origin/agent/7-work like Prepare.
func saveCheckpoint(t *testing.T, w *Workspace, name string, push bool) string {
	t.Helper()
	writeFile(t, w.Dir, name, "package app // "+name+"\n")
	res, err := w.Checkpoint(context.Background(), checkpointReq(w, t))
	if err != nil || res.SHA == "" {
		t.Fatalf("checkpoint: %+v %v", res, err)
	}
	if push {
		gitCmd(t, w.Dir, "push", "origin", "agent/7-work")
	}
	return res.SHA
}

func stepReq(msg string) CommitRequest {
	return CommitRequest{Message: msg, Branch: "agent/7-work", Base: "main"}
}

// checkFold checks that HEAD is res's fold commit with msg on parent,
// holding files, and that no checkpoint is left on the branch.
func checkFold(t *testing.T, w *Workspace, res CommitResult, msg, parent string, files ...string) {
	t.Helper()
	head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	if !res.Committed || res.SHA != head {
		t.Fatalf("result %+v, HEAD %s", res, head)
	}
	if got := gitCmd(t, w.Dir, "log", "-1", "--format=%B"); got != msg {
		t.Fatalf("message %q, want the agent's %q", got, msg)
	}
	if got := gitCmd(t, w.Dir, "rev-parse", "HEAD^"); got != parent {
		t.Fatalf("the fold is on %s, want %s", got, parent)
	}
	if log := gitCmd(t, w.Dir, "log", "--format=%B", "main..HEAD"); strings.Contains(log, "Agent-Checkpoint") {
		t.Fatalf("a checkpoint is left:\n%s", log)
	}
	tree := strings.Split(gitCmd(t, w.Dir, "ls-tree", "--name-only", "HEAD"), "\n")
	for _, f := range files {
		if !slices.Contains(tree, f) {
			t.Fatalf("the fold misses %s: %v", f, tree)
		}
	}
	if st := gitCmd(t, w.Dir, "status", "--porcelain"); st != "" {
		t.Fatalf("the work tree is committed: %s", st)
	}
}

func TestCommitStepFoldsACheckpoint(t *testing.T) {
	w, step := foldWorkspace(t)
	cp := saveCheckpoint(t, w, "wip.go", true)
	writeFile(t, w.Dir, "api.go", "package app // api\n")
	res, err := w.CommitStep(context.Background(), stepReq("feat: add the API\n\nRefs #7"))
	if err != nil {
		t.Fatal(err)
	}
	if res.FoldedFrom != cp || res.Folded != 1 {
		t.Fatalf("expected a fold of %s: %+v", cp, res)
	}
	checkFold(t, w, res, "feat: add the API\n\nRefs #7", step, "wip.go", "api.go", "model.go")
}

// Two checkpoints in a row fold too, also when the step changed nothing.
func TestCommitStepFoldsTwoCheckpoints(t *testing.T) {
	w, step := foldWorkspace(t)
	saveCheckpoint(t, w, "wip.go", true)
	cp := saveCheckpoint(t, w, "more.go", true)
	res, err := w.CommitStep(context.Background(), stepReq("feat: add the API"))
	if err != nil {
		t.Fatal(err)
	}
	if res.FoldedFrom != cp || res.Folded != 2 {
		t.Fatalf("expected a fold of both checkpoints up to %s: %+v", cp, res)
	}
	checkFold(t, w, res, "feat: add the API", step, "wip.go", "more.go")
}

func TestCommitStepDoesNotFold(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		// setup prepares the branch and returns the commit request.
		setup func(t *testing.T, w *Workspace) CommitRequest
	}{
		{"no checkpoint", func(t *testing.T, w *Workspace) CommitRequest { return stepReq("feat: x") }},
		{"checkpoint below a regular commit", func(t *testing.T, w *Workspace) CommitRequest {
			saveCheckpoint(t, w, "wip.go", true)
			writeFile(t, w.Dir, "human.go", "package app\n")
			gitCmd(t, w.Dir, "add", ".")
			gitCmd(t, w.Dir, "commit", "-m", "Human fix")
			gitCmd(t, w.Dir, "push", "origin", "agent/7-work")
			return stepReq("feat: x")
		}},
		{"checkpoint not pushed", func(t *testing.T, w *Workspace) CommitRequest {
			saveCheckpoint(t, w, "wip.go", false)
			return stepReq("feat: x")
		}},
		{"base branch", func(t *testing.T, w *Workspace) CommitRequest {
			saveCheckpoint(t, w, "wip.go", true)
			req := stepReq("feat: x")
			req.Base = req.Branch
			return req
		}},
		{"not an agent branch", func(t *testing.T, w *Workspace) CommitRequest {
			saveCheckpoint(t, w, "wip.go", true)
			gitCmd(t, w.Dir, "branch", "-m", "feature/7-work")
			gitCmd(t, w.Dir, "push", "origin", "feature/7-work")
			req := stepReq("feat: x")
			req.Branch = "feature/7-work"
			return req
		}},
		{"another branch checked out", func(t *testing.T, w *Workspace) CommitRequest {
			saveCheckpoint(t, w, "wip.go", true)
			gitCmd(t, w.Dir, "checkout", "-q", "-b", "agent/8-other")
			return stepReq("feat: x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := foldWorkspace(t)
			req := tc.setup(t, w)
			before := gitCmd(t, w.Dir, "rev-parse", "HEAD")
			history := gitCmd(t, w.Dir, "rev-list", "HEAD")
			writeFile(t, w.Dir, "api.go", "package app // api\n")
			res, err := w.CommitStep(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if res.FoldedFrom != "" || res.Folded != 0 || !res.Committed {
				t.Fatalf("expected a plain commit: %+v", res)
			}
			if parent := gitCmd(t, w.Dir, "rev-parse", "HEAD^"); parent != before {
				t.Fatalf("the commit goes on top of %s, got %s", before, parent)
			}
			if got := gitCmd(t, w.Dir, "rev-list", "HEAD^"); got != history {
				t.Fatal("the history below the commit changed")
			}
		})
	}
}

// A checkpoint this run pushed is not in the checkout's origin ref; the
// runner names it in Pushed.
func TestCommitStepFoldsTheRunsPushedCheckpoint(t *testing.T) {
	w, step := foldWorkspace(t)
	cp := saveCheckpoint(t, w, "wip.go", false)
	req := stepReq("feat: add the API")
	req.Pushed = cp
	res, err := w.CommitStep(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.FoldedFrom != cp {
		t.Fatalf("expected a fold: %+v", res)
	}
	checkFold(t, w, res, "feat: add the API", step, "wip.go")
}

// Checkpoints whose changes cancel out leave nothing to fold into: a plain
// commit (here none) instead of an empty one.
func TestCommitStepWithoutChangesSinceTheCheckpointsBase(t *testing.T) {
	ctx := context.Background()
	w, _ := foldWorkspace(t)
	saveCheckpoint(t, w, "wip.go", true)
	gitCmd(t, w.Dir, "rm", "-q", "wip.go")
	if cp, err := w.Checkpoint(ctx, checkpointReq(w, t)); err != nil || cp.SHA == "" {
		t.Fatalf("checkpoint of the deletion: %+v %v", cp, err)
	}
	gitCmd(t, w.Dir, "push", "origin", "agent/7-work")
	before := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	res, err := w.CommitStep(ctx, stepReq("feat: x"))
	if err != nil || res.Committed || res.FoldedFrom != "" || gitCmd(t, w.Dir, "rev-parse", "HEAD") != before {
		t.Fatalf("expected nothing: %+v %v", res, err)
	}
}

func TestUnfold(t *testing.T) {
	ctx := context.Background()
	w, _ := foldWorkspace(t)
	cp := saveCheckpoint(t, w, "wip.go", true)
	writeFile(t, w.Dir, "api.go", "package app // api\n")
	res, err := w.CommitStep(ctx, stepReq("feat: add the API\n\nRefs #7"))
	if err != nil || res.FoldedFrom != cp {
		t.Fatalf("fold: %+v %v", res, err)
	}
	// A later step's commit on the fold moves along.
	writeFile(t, w.Dir, "docs.md", "docs\n")
	if ok, err := w.Commit(ctx, "docs: describe the API"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	tree := gitCmd(t, w.Dir, "rev-parse", "HEAD^{tree}")
	head, err := w.Unfold(ctx, UnfoldRequest{Tip: cp, Fold: res.SHA})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitCmd(t, w.Dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD %s, want %s", got, head)
	}
	if got := gitCmd(t, w.Dir, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Fatal("the files must stay as they are")
	}
	if log := strings.Split(gitCmd(t, w.Dir, "log", "--format=%s", "-3"), "\n"); !slices.Equal(log, []string{"docs: describe the API", "feat: add the API", "chore(wip): checkpoint #7 step 2 (usage limit)"}) {
		t.Fatalf("history %q", log)
	}
	if got := gitCmd(t, w.Dir, "rev-parse", "HEAD~2"); got != cp {
		t.Fatalf("the step's commit goes on the checkpoint %s, got %s", cp, got)
	}
	if got := gitCmd(t, w.Dir, "log", "-1", "--format=%B", "HEAD^"); got != "feat: add the API\n\nRefs #7" {
		t.Fatalf("the fold's message stays: %q", got)
	}
	if st := gitCmd(t, w.Dir, "status", "--porcelain"); st != "" {
		t.Fatalf("index and work tree stay: %s", st)
	}
	// Anything that is not a fold of the checkpoint is refused.
	if _, err := w.Unfold(ctx, UnfoldRequest{Tip: cp, Fold: head}); err == nil {
		t.Fatal("a commit on the checkpoint is no fold of it")
	}
}

// TestCommitStepThroughAgentServer checks the RPC wiring.
func TestCommitStepThroughAgentServer(t *testing.T) {
	ctx := context.Background()
	w, _ := foldWorkspace(t)
	cp := saveCheckpoint(t, w, "wip.go", true)
	client := serveAgent(t, &AgentServer{Log: logr.Discard(), Agent: &LocalAgent{Workspace: w, Exchange: filepath.Join(t.TempDir(), "exchange")}})
	res, err := client.CommitStep(ctx, stepReq("feat: x"))
	if err != nil || res.FoldedFrom != cp || res.Folded != 1 || res.SHA != gitCmd(t, w.Dir, "rev-parse", "HEAD") {
		t.Fatalf("fold over the socket: %+v %v", res, err)
	}
	head, err := client.Unfold(ctx, UnfoldRequest{Tip: cp, Fold: res.SHA})
	if err != nil || head != gitCmd(t, w.Dir, "rev-parse", "HEAD") || gitCmd(t, w.Dir, "rev-parse", "HEAD^") != cp {
		t.Fatalf("unfold over the socket: %s %v", head, err)
	}
	if _, err := client.Unfold(ctx, UnfoldRequest{Tip: cp, Fold: strings.Repeat("0", 40)}); err == nil {
		t.Fatal("errors must come back")
	}
}
