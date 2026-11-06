package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func companionInput(diff, cdiff string) reviewInput {
	return reviewInput{project: "group/app", base: "main", stat: " a.go | 1 +\n", diff: diff,
		companion: &companionReview{project: "group/infra", paths: []string{"clusters/app/", "apps/*.yaml"},
			dir: "/workspace/repos/group-infra", base: "trunk", stat: " clusters/app/x.yaml | 1 +\n", diff: cdiff}}
}

// TestReviewPromptWithoutCompanionIsUnchanged pins the reviewer input of a
// task without a companion byte for byte (golden).
func TestReviewPromptWithoutCompanionIsUnchanged(t *testing.T) {
	is := &gitlab.Issue{IID: 7, Title: "Add it", Description: "Build it."}
	in := reviewInput{project: "group/app", base: "main", stat: " a.go | 1 +\n", diff: "+x\n"}
	want := "Review the changes made for issue #7: Add it\n\n" +
		"## Issue description\n\nBuild it.\n\n" +
		"## Decisions of the planning agent\n\nD1\n\n" +
		"## Changed files\n\n```\n a.go | 1 +\n\n```\n\n" +
		"## Diff\n\n```diff\n+x\n\n```\n"
	if got := buildReviewPrompt(is, "D1", in, defaultInlineDiff); got != want {
		t.Fatalf("reviewer input changed:\n%q\nwant\n%q", got, want)
	}
	big := in
	big.diff = strings.Repeat("x", 11)
	want = "Review the changes made for issue #7: Add it\n\n" +
		"## Issue description\n\nBuild it.\n\n" +
		"## Changed files\n\n```\n a.go | 1 +\n\n```\n\n" +
		"The diff is too large to include. Inspect it with `git diff origin/main...HEAD -- <file>`.\n"
	if got := buildReviewPrompt(is, "", big, 10); got != want {
		t.Fatalf("stat-only reviewer input changed:\n%q\nwant\n%q", got, want)
	}
	for _, kind := range []aiv1.ReviewerKind{aiv1.ReviewerBugbot, aiv1.ReviewerSecurity} {
		if note := reviewerNote(kind, in); note != "" {
			t.Fatalf("%s: no note without a companion: %q", kind, note)
		}
	}
}

func TestReviewPromptWithCompanionHasTreeHeaders(t *testing.T) {
	is := &gitlab.Issue{IID: 7, Title: "Add it", Description: "Build it."}
	got := buildReviewPrompt(is, "", companionInput("+home\n", "+infra\n"), defaultInlineDiff)
	want := "Review the changes made for issue #7: Add it\n\n" +
		"## Issue description\n\nBuild it.\n\n" +
		"## Tree: home (group/app)\n\n" +
		"### Changed files\n\n```\n a.go | 1 +\n\n```\n\n" +
		"### Diff\n\n```diff\n+home\n\n```\n\n" +
		"## Tree: companion (group/infra, allowed paths: `clusters/app/`, `apps/*.yaml`; lands after the home change is released)\n\n" +
		"Its checkout is `/workspace/repos/group-infra`.\n\n" +
		"### Changed files\n\n```\n clusters/app/x.yaml | 1 +\n\n```\n\n" +
		"### Diff\n\n```diff\n+infra\n\n```\n"
	if got != want {
		t.Fatalf("combined input:\n%s\nwant\n%s", got, want)
	}
}

// TestReviewPromptWithCompanionCapsTheCombinedDiff: the cap counts both
// diffs; when they exceed it together, both trees are stat-only, also when
// each alone would fit.
func TestReviewPromptWithCompanionCapsTheCombinedDiff(t *testing.T) {
	is := &gitlab.Issue{IID: 7, Title: "Add it"}
	in := companionInput(strings.Repeat("h", 30), strings.Repeat("c", 30))
	for _, tc := range []struct {
		cap    int
		inline bool
	}{{60, true}, {59, false}, {31, false}} {
		got := buildReviewPrompt(is, "", in, tc.cap)
		if n := strings.Count(got, "### Diff"); tc.inline && n != 2 || !tc.inline && n != 0 {
			t.Fatalf("cap %d: %d diffs inline:\n%s", tc.cap, n, got)
		}
		if strings.Contains(got, "hhh") != tc.inline || strings.Contains(got, "ccc") != tc.inline {
			t.Fatalf("cap %d: a diff is cut, not dropped:\n%s", tc.cap, got)
		}
		if !strings.Contains(got, "a.go | 1 +") || !strings.Contains(got, "clusters/app/x.yaml | 1 +") {
			t.Fatalf("cap %d: both stats stay:\n%s", tc.cap, got)
		}
		if !tc.inline && (!strings.Contains(got, "Inspect this one with `git diff origin/main...HEAD -- <file>`") ||
			!strings.Contains(got, "Inspect this one with `git -C /workspace/repos/group-infra diff origin/trunk...HEAD -- <file>`")) {
			t.Fatalf("cap %d: both trees say how to inspect them:\n%s", tc.cap, got)
		}
	}
}

func TestReviewerNoteOnlyForSecurity(t *testing.T) {
	in := companionInput("+h", "+c")
	note := reviewerNote(aiv1.ReviewerSecurity, in)
	for _, want := range []string{"`group/infra`", "applied to production by Flux", "secrets", "plaintext credentials", "RBAC widening", "before the home change is released"} {
		if !strings.Contains(note, want) {
			t.Errorf("security note lacks %q: %s", want, note)
		}
	}
	if n := reviewerNote(aiv1.ReviewerBugbot, in); n != "" {
		t.Fatalf("bugbot gets no note: %q", n)
	}
}

func TestReviewedHeads(t *testing.T) {
	if got := reviewedHeads("0123456789"); got != "`01234567`" {
		t.Fatal(got)
	}
	if got := reviewedHeads(aiv1.ReviewHeads("0123456789", "abcdefabcd")); got != "`01234567` and the companion's `abcdefab`" {
		t.Fatal(got)
	}
}

// reviewerPrompts are the prompts of the reviewer calls, by reviewer.
func reviewerPrompts(s *scriptedEngine) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for _, inv := range s.calls {
		if inv.ReadOnly && inv.JSONSchema == reviewSchema {
			out[inv.Role] = append(out[inv.Role], inv.Prompt)
		}
	}
	return out
}

// TestImplementCompanionReviewCoversBothTrees: the reviewers see the
// companion diff under its tree header, only the security reviewer gets the
// note, and status.run.companion.reviewedSHA is the reviewed companion
// head.
func TestImplementCompanionReviewCoversBothTrees(t *testing.T) {
	e := newCompanionEnv(t)
	r, engine := e.companionRunner(t, nil)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("failed steps %v", failed)
	}
	prompts := reviewerPrompts(&engine.scriptedEngine)
	if len(prompts["bugbot"]) == 0 || len(prompts["security"]) == 0 {
		t.Fatalf("both reviewers run: %v", prompts)
	}
	for kind, ps := range prompts {
		for _, p := range ps {
			home, comp := strings.Index(p, "## Tree: home (group/app)"), strings.Index(p, "## Tree: companion (group/infra, allowed paths: `clusters/app/`")
			if home < 0 || comp < home || !strings.Contains(p[home:comp], "paypal.go") || !strings.Contains(p[comp:], "+paypal: true") {
				t.Fatalf("%s: both diffs under their tree headers:\n%s", kind, p)
			}
			if has := strings.Contains(p, "applied to production by Flux"); has != (kind == "security") {
				t.Fatalf("%s: the note is the security reviewer's only:\n%s", kind, p)
			}
		}
	}
	st := e.taskStatus(t)
	pushed := gitCmd(t, e.tmp, "--git-dir", e.infraOrigin, "rev-parse", companionBranch)
	if st.Companion == nil || st.Companion.ReviewedSHA != pushed || st.Companion.PushedSHA != pushed || st.ReviewedSHA != st.PushedSHA {
		t.Fatalf("both trees reviewed at their pushed heads (%s): %+v, home %s/%s", pushed, st.Companion, st.ReviewedSHA, st.PushedSHA)
	}
	if st.CompanionUnreviewed() {
		t.Fatal("the companion is reviewed")
	}
}

// TestCompanionOnlyPushNeedsAReReview: a companion head pushed after the
// review (by a run without reviewers) leaves the companion unreviewed; the
// re-review asked for both heads runs only the reviewers, over both trees,
// and records the new companion head as reviewed.
func TestCompanionOnlyPushNeedsAReReview(t *testing.T) {
	e := newCompanionEnv(t)
	r, engine := e.companionRunner(t, nil)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	first := e.taskStatus(t)
	if first.Companion == nil || first.Companion.ReviewedSHA == "" {
		t.Fatalf("the first run reviews the companion: %+v", first.Companion)
	}

	// The companion moves on alone, as a CI fix run without reviewers
	// would push it.
	gitCmd(t, e.infraSeed, "fetch", "origin")
	gitCmd(t, e.infraSeed, "checkout", "-B", "work", "origin/"+companionBranch)
	_ = os.WriteFile(filepath.Join(e.infraSeed, "clusters", "app", "paypal.yaml"), []byte("paypal: false\n"), 0o644)
	gitCmd(t, e.infraSeed, "commit", "-am", "companion fix")
	gitCmd(t, e.infraSeed, "push", "origin", "HEAD:"+companionBranch)
	head := strings.TrimSpace(gitCmd(t, e.infraSeed, "rev-parse", "HEAD"))
	ctx := context.Background()
	var task aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task); err != nil {
		t.Fatal(err)
	}
	orig := task.DeepCopy()
	task.Status.Run.Companion.PushedSHA = head
	if err := e.k8s.Status().Patch(ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if !task.Status.Run.CompanionUnreviewed() || task.Status.Run.ReviewedSHA != task.Status.Run.PushedSHA {
		t.Fatalf("a companion-only push leaves only the companion unreviewed: %+v", task.Status.Run)
	}
	key := task.Status.Run.ReviewKey()
	if key == first.ReviewKey() || key != first.PushedSHA+"+"+head {
		t.Fatalf("the review key names both heads: %q", key)
	}

	// "/agent review": the operator asks for the key.
	engine.mu.Lock()
	engine.calls = nil
	engine.mu.Unlock()
	orig = task.DeepCopy()
	task.Spec.RunRequest++
	task.Spec.Source.ReviewFor = key
	if err := e.k8s.Patch(ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if ran, err := r.Step(ctx); err != nil || !ran {
		t.Fatalf("re-review: ran=%v err=%v", ran, err)
	}
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("failed steps %v", failed)
	}
	if n := implementerCalls(&engine.scriptedEngine); n != 0 {
		t.Fatalf("the implementer must not run, got %d calls", n)
	}
	prompts := reviewerPrompts(&engine.scriptedEngine)
	if len(prompts["bugbot"]) != 1 || !strings.Contains(prompts["bugbot"][0], "+paypal: false") || !strings.Contains(prompts["bugbot"][0], "## Tree: home (group/app)") {
		t.Fatalf("the re-review sees the new companion head with the home tree: %v", prompts)
	}
	st := e.taskStatus(t)
	if st.PushedSHA != first.PushedSHA || st.Companion.PushedSHA != head || st.Companion.ReviewedSHA != head || st.ReviewedFor != key || st.CompanionUnreviewed() {
		t.Fatalf("the re-review records the companion head as reviewed: %+v companion %+v", st, st.Companion)
	}
	if !strings.Contains(e.allNotes(), "Reviewed `"+shortSHA(first.PushedSHA)+"` and the companion's `"+shortSHA(head)+"` again") {
		t.Fatalf("the report names both heads:\n%s", e.allNotes())
	}
}
