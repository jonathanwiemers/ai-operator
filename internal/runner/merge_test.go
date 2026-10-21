package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// mergeWorkspace is a checkout of branch "work", which changed app.go,
// while main changed it differently (conflict) and added other.go.
func mergeWorkspace(t *testing.T, conflict bool) *Workspace {
	t.Helper()
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, seed)
	write := func(name, content string) { _ = os.WriteFile(filepath.Join(seed, name), []byte(content), 0o644) }
	write("app.go", "package app\n\nconst Name = \"old\"\n")
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")
	gitCmd(t, seed, "checkout", "-b", "work")
	write("app.go", "package app\n\nconst Name = \"branch\"\n")
	gitCmd(t, seed, "commit", "-am", "branch change")
	gitCmd(t, seed, "push", "origin", "work")
	gitCmd(t, seed, "checkout", "main")
	if conflict {
		write("app.go", "package app\n\nconst Name = \"main\"\n")
	}
	write("other.go", "package app\n")
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "main change")
	gitCmd(t, seed, "push", "origin", "main")

	dir := filepath.Join(tmp, "ws")
	gitCmd(t, tmp, "clone", "--branch", "work", origin, dir)
	return &Workspace{Dir: dir, AuthorName: "AI", AuthorEmail: "ai@example.com"}
}

func TestWorkspaceMergeBase(t *testing.T) {
	ctx := context.Background()

	t.Run("clean", func(t *testing.T) {
		w := mergeWorkspace(t, false)
		res, err := w.MergeBase(ctx, "main", "Merge main")
		if err != nil || !res.Merged || len(res.Conflicts) != 0 {
			t.Fatalf("expected a clean merge: %+v %v", res, err)
		}
		if parents := strings.Fields(gitCmd(t, w.Dir, "log", "-1", "--format=%P")); len(parents) != 2 {
			t.Fatalf("expected a merge commit, parents %v", parents)
		}
		if res, _ := w.MergeBase(ctx, "main", "Merge main"); !res.UpToDate {
			t.Fatalf("a second merge has nothing to do: %+v", res)
		}
	})

	t.Run("resolved", func(t *testing.T) {
		w := mergeWorkspace(t, true)
		res, err := w.MergeBase(ctx, "main", "Merge main")
		if err != nil || res.Merged || !slices.Equal(res.Conflicts, []string{"app.go"}) || res.From == "" {
			t.Fatalf("expected a conflict in app.go: %+v %v", res, err)
		}
		_ = os.WriteFile(filepath.Join(w.Dir, "app.go"), []byte("package app\n\nconst Name = \"both\"\n"), 0o644)
		fin, err := w.FinishMerge(ctx, FinishMerge{Base: "main", Message: "Merge main", From: res.From, Files: res.Conflicts})
		if err != nil || !fin.Merged {
			t.Fatalf("expected the merge to be concluded: %+v %v", fin, err)
		}
		gitCmd(t, w.Dir, "merge-base", "--is-ancestor", "origin/main", "HEAD")
		if got := gitCmd(t, w.Dir, "log", "-1", "--format=%s"); got != "Merge main" {
			t.Fatalf("merge commit message %q", got)
		}
	})

	t.Run("markers left", func(t *testing.T) {
		w := mergeWorkspace(t, true)
		res, _ := w.MergeBase(ctx, "main", "Merge main")
		// The agent even commits the half-resolved merge.
		gitCmd(t, w.Dir, "commit", "-am", "oops")
		fin, err := w.FinishMerge(ctx, FinishMerge{Base: "main", Message: "Merge main", From: res.From, Files: res.Conflicts})
		if err != nil || fin.Merged || !slices.Equal(fin.Conflicts, []string{"app.go"}) {
			t.Fatalf("markers must not be committed: %+v %v", fin, err)
		}
		if head := gitCmd(t, w.Dir, "rev-parse", "HEAD"); head != res.From {
			t.Fatalf("the branch must go back to %s, is at %s", res.From, head)
		}
	})

	t.Run("aborted", func(t *testing.T) {
		w := mergeWorkspace(t, true)
		res, _ := w.MergeBase(ctx, "main", "Merge main")
		gitCmd(t, w.Dir, "merge", "--abort")
		fin, err := w.FinishMerge(ctx, FinishMerge{Base: "main", Message: "Merge main", From: res.From, Files: res.Conflicts})
		if err != nil || fin.Merged {
			t.Fatalf("an aborted merge is not merged: %+v %v", fin, err)
		}
	})
}

// conflictEngine resolves conflicts in steady.go by writing resolution, or
// leaves the markers if resolution is empty. It records the implementer
// prompts.
type conflictEngine struct {
	mu         sync.Mutex
	resolution string
	prompts    []string
}

func (c *conflictEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var v any = ReviewAnswer{Summary: "fine"}
	if !inv.ReadOnly {
		c.prompts = append(c.prompts, inv.Prompt)
		switch {
		case strings.Contains(inv.Prompt, "## Merge conflicts") && c.resolution != "":
			_ = os.WriteFile(filepath.Join(inv.Dir, "steady.go"), []byte(c.resolution), 0o644)
		case !strings.Contains(inv.Prompt, "## Merge conflicts"):
			_ = os.WriteFile(filepath.Join(inv.Dir, "steady.go"), []byte("package app // branch\n"), 0o644)
		}
		v = ImplementAnswer{Status: "done", Summary: "Done.", CommitMessage: "Work", Handoff: "done"}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-conflict", Structured: b}, nil
}

// conflictRun runs once, then lets main change steady.go (conflict) or add
// another file, and runs again with a merge request for the MR head.
func conflictRun(t *testing.T, engine *conflictEngine, conflict bool) (*implementEnv, aiv1.AgentTask) {
	t.Helper()
	e := newImplementEnv(t)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	ctx := context.Background()
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	name, content := "steady.go", "package app // main\n"
	if !conflict {
		name = "other.go"
	}
	_ = os.WriteFile(filepath.Join(e.seed, name), []byte(content), 0o644)
	gitCmd(t, e.seed, "add", ".")
	gitCmd(t, e.seed, "commit", "-m", "main moves on")
	gitCmd(t, e.seed, "push", "origin", "HEAD:main")

	var got aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	orig := got.DeepCopy()
	got.Spec.RunRequest = 2
	got.Spec.Source.MergeBaseFor = got.Status.Run.PushedSHA
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.prompts = nil
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	return e, got
}

func lastMRNote(e *implementEnv) string {
	notes := e.gl.MRNotes[1]
	if len(notes) == 0 {
		return ""
	}
	return notes[len(notes)-1].Body
}

func TestRunResolvesMergeConflicts(t *testing.T) {
	engine := &conflictEngine{resolution: "package app // branch and main\n"}
	e, task := conflictRun(t, engine, true)
	branch := "agent/42-add-paypal-button"
	head := gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", branch)
	if task.Status.Run.PushedSHA != head || task.Status.Run.MergedBaseFor != task.Spec.Source.MergeBaseFor {
		t.Fatalf("the merge should be pushed and recorded: %+v, head %s", task.Status.Run, head)
	}
	gitCmd(t, e.tmp, "--git-dir", e.origin, "merge-base", "--is-ancestor", "main", branch)
	if got := gitCmd(t, e.tmp, "--git-dir", e.origin, "show", branch+":steady.go"); got != "package app // branch and main" {
		t.Fatalf("unexpected resolution %q", got)
	}
	if len(engine.prompts) != 1 || !strings.Contains(engine.prompts[0], "- `steady.go`") || !strings.Contains(engine.prompts[0], "the runner concludes the merge") {
		t.Fatalf("the implementer should get the conflicts: %q", engine.prompts)
	}
	// Reviewed like any push, and waiting for CI.
	if task.Status.Run.ReviewedSHA != head || !slices.Equal(e.gl.Labels(42), []string{"agent::working"}) {
		t.Fatalf("the merge must be reviewed and wait for CI: %+v %v", task.Status.Run, e.gl.Labels(42))
	}
	if !strings.Contains(lastMRNote(e), "resolved the conflicts in `steady.go`") {
		t.Fatalf("the report should say what was merged: %s", lastMRNote(e))
	}
}

func TestRunUndoesAnUnresolvedMerge(t *testing.T) {
	engine := &conflictEngine{} // leaves the markers
	e, task := conflictRun(t, engine, true)
	head := gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", "agent/42-add-paypal-button")
	if head != task.Spec.Source.MergeBaseFor || task.Status.Run.LastResult != aiv1.RunResultNeedsInput {
		t.Fatalf("nothing may be pushed and a human is needed: head %s, %+v", head, task.Status.Run)
	}
	if !slices.Equal(e.gl.Labels(42), []string{"agent::needs-input"}) || !strings.Contains(lastMRNote(e), "Could not resolve the merge conflicts") {
		t.Fatalf("the issue should need input with an explanation: %v\n%s", e.gl.Labels(42), lastMRNote(e))
	}
}

func TestRunMergesCleanlyWithoutTheAgent(t *testing.T) {
	engine := &conflictEngine{}
	e, task := conflictRun(t, engine, false)
	branch := "agent/42-add-paypal-button"
	head := gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", branch)
	if len(engine.prompts) != 0 || task.Status.Run.PushedSHA != head {
		t.Fatalf("a clean merge needs no implementer, only the push: %d prompts, %+v", len(engine.prompts), task.Status.Run)
	}
	gitCmd(t, e.tmp, "--git-dir", e.origin, "merge-base", "--is-ancestor", "main", branch)
	if !strings.Contains(lastMRNote(e), "there were no conflicts") {
		t.Fatalf("the report should mention the merge: %s", lastMRNote(e))
	}
}
