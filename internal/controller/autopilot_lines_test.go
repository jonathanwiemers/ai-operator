package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// mergeFirstIssue runs #1 of the autopilot milestone to its merge, as !1.
func mergeFirstIssue(t *testing.T, e *projectEnv) *aiv1.AutopilotItem {
	t.Helper()
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", nil)
	for range 4 {
		if len(e.gl.Merged) > 0 {
			break
		}
		e.reconcile()
	}
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected !1 merged: %v %+v", e.gl.Merged, e.autopilot())
	}
	return firstItem(t, e)
}

func firstItem(t *testing.T, e *projectEnv) *aiv1.AutopilotItem {
	t.Helper()
	for i, it := range e.autopilot().Items {
		if it.IID == 1 {
			return &e.autopilot().Items[i]
		}
	}
	t.Fatal("no item #1")
	return nil
}

func TestAutopilotRecordsMergedLines(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.MRDiffs = map[int64][]gitlab.Diff{1: {
		{OldPath: "internal/foo.go", NewPath: "internal/foo.go", Diff: patch(40, 5)},
		{NewPath: "internal/foo_test.go", NewFile: true, Diff: patch(60, 0)},
		{NewPath: "docs/foo.md", NewFile: true, Diff: patch(10, 0)},
	}}
	it := mergeFirstIssue(t, e)
	want := aiv1.MergedLines{
		Production: aiv1.LineCount{Added: 40, Deleted: 5},
		Tests:      aiv1.LineCount{Added: 60},
		Docs:       aiv1.LineCount{Added: 10},
	}
	if it.State != aiv1.AutopilotItemMerged || it.MergedLines == nil || *it.MergedLines != want {
		t.Fatalf("mergedLines = %+v, want %+v (item %+v)", it.MergedLines, want, it)
	}
	calls := e.gl.DiffLists[1]
	e.reconcile()
	e.reconcile()
	if n := e.gl.DiffLists[1]; n != calls {
		t.Fatalf("later polls listed the diffs of !1 again: %d calls, then %d", calls, n)
	}
}

func TestAutopilotMergesWhenTheLineCountFails(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.DiffErrors = map[int64]error{1: errors.New("boom")}
	it := mergeFirstIssue(t, e)
	if it.State != aiv1.AutopilotItemMerged || it.MergedLines != nil {
		t.Fatalf("a failed count still merges and records no lines: %+v", it)
	}
	if e.gl.Issues[1].State != "closed" {
		t.Fatalf("the issue must be closed: %s", e.gl.Issues[1].State)
	}
}

// merged counts the lines once: a second call on the same item does not
// list the diffs again.
func TestAutopilotMergedCountsLinesOnce(t *testing.T) {
	gl := gitlabfake.New()
	gl.AddIssue(1, "API")
	mr := &gitlab.MergeRequest{IID: 1, State: "merged", SHA: "sha-1"}
	gl.MRs[1] = mr
	a := &autopilot{r: &AgentProjectReconciler{Now: time.Now}, gl: gl, spec: aiv1.AgentProjectSpec{}.WithDefaults(),
		project: &aiv1.AgentProject{}, st: &aiv1.AgentTask{}, ap: &aiv1.AutopilotStatus{}, log: logr.Discard()}
	it := &aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive}
	ctx := context.Background()
	for range 2 {
		if err := a.merged(ctx, it, mr); err != nil {
			t.Fatal(err)
		}
	}
	if n := gl.DiffLists[1]; n != 1 || it.MergedLines == nil || it.MergedLines.Production != (aiv1.LineCount{Added: 1, Deleted: 1}) {
		t.Fatalf("want one diffs call and the fake's +1/-1: %d calls, %+v", n, it.MergedLines)
	}
}

// patch is one hunk that deletes del lines and adds add lines.
func patch(add, del int) string {
	s := fmt.Sprintf("@@ -1,%d +1,%d @@\n", del, add)
	for range del {
		s += "-old\n"
	}
	for range add {
		s += "+new\n"
	}
	return s
}
