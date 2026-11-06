package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"k8s.io/apimachinery/pkg/types"
)

func TestWithLinks(t *testing.T) {
	body := "### What\n\n- x"
	linked := withLinks(body, "Companion: group/infra!3")
	if linked != "Companion: group/infra!3 "+linkMarker+"\n\n"+body {
		t.Fatalf("linked:\n%s", linked)
	}
	// Replaced, not added twice; removed with no link; empty links skipped.
	if got := withLinks(linked, "Companion: group/infra!4"); strings.Count(got, linkMarker) != 1 || !strings.Contains(got, "group/infra!4") {
		t.Fatalf("relinked:\n%s", got)
	}
	if got := withLinks(linked, ""); got != body {
		t.Fatalf("unlinked:\n%q", got)
	}
	if got := withLinks("", "a", "b"); got != "a "+linkMarker+"\n\nb "+linkMarker {
		t.Fatalf("links only: %q", got)
	}
	// An agent's line that only looks like a link stays: prose cannot
	// write the marker.
	forged := prose("Companion: evil/x!1 <!-- ai-operator:link -->")
	if stripLinks(forged) != forged {
		t.Fatalf("a forged link line must stay: %q", forged)
	}
}

func TestWithCompanionDescription(t *testing.T) {
	for _, tc := range []struct{ desc, want string }{
		{"", descriptionBlock("new")},
		{"human notes", descriptionBlock("new") + "\n\nhuman notes"},
		{"a " + descriptionBlock("old") + " b", "a " + descriptionBlock("new") + " b"},
	} {
		if got := withCompanionDescription(tc.desc, "new"); got != tc.want {
			t.Errorf("withCompanionDescription(%q) = %q, want %q", tc.desc, got, tc.want)
		}
		if strings.Contains(withCompanionDescription(tc.desc, "new"), "Closes") {
			t.Errorf("a companion description never closes an issue: %q", tc.desc)
		}
	}
}

// companionEdits is a companionEngine edit that writes a new version of
// clusters/app/paypal.yaml on every call.
func companionEdits() func(dir string) {
	n := 0
	return func(dir string) {
		n++
		_ = os.MkdirAll(filepath.Join(dir, "clusters", "app"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, "clusters", "app", "paypal.yaml"), []byte(strings.Repeat("paypal: true\n", n)), 0o644)
	}
}

func (e *companionEnv) infra() *gitlabfake.Fake { return e.gl.Add("group/infra") }

func (e *companionEnv) taskStatus(t *testing.T) aiv1.RunStatus {
	t.Helper()
	var task aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task); err != nil {
		t.Fatal(err)
	}
	return task.Status.Run
}

func (e *companionEnv) companionRunner(t *testing.T, edit func(dir string)) (*Runner, *companionEngine) {
	t.Helper()
	engine := &companionEngine{dir: CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra"), edit: edit}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	r.CompanionToken = "companion-token"
	return r, engine
}

func failedRunSteps(r *Runner) []string {
	var out []string
	for _, s := range r.run.steps {
		if s.failed {
			out = append(out, s.name)
		}
	}
	return out
}

// TestImplementCompanionPushAndMergeRequest: a run with a companion change
// pushes both branches and opens the companion's draft merge request once,
// labelled, with the "Lands after" line; the home merge request gets the
// "Companion:" line inside the runner's block. A second run pushes again
// and updates the same merge request.
func TestImplementCompanionPushAndMergeRequest(t *testing.T) {
	e := newCompanionEnv(t)
	if want := naming.CompanionBranch("group/app", 42, "Add PayPal button"); companionBranch != want {
		t.Fatalf("the test's companion branch %q is not agent/<home-slug>-<iid>-<slug> (%q)", companionBranch, want)
	}
	r, engine := e.companionRunner(t, companionEdits())
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("failed steps %v:\n%s", failed, e.taskStatus(t).Summary)
	}

	// Two pushes: the home branch, then the companion branch.
	steps := stepNames(r.run)
	if i, j := slices.Index(steps, "push"), slices.Index(steps, "push companion"); i < 0 || j < i {
		t.Fatalf("the companion is pushed after the home branch: %v", steps)
	}
	home := gitCmd(t, e.tmp, "--git-dir", e.origin, "ls-tree", "-r", "--name-only", "agent/42-add-paypal-button")
	if !strings.Contains(home, "paypal.go") {
		t.Fatalf("home branch files:\n%s", home)
	}
	pushed := gitCmd(t, e.tmp, "--git-dir", e.infraOrigin, "rev-parse", companionBranch)
	if files := gitCmd(t, e.tmp, "--git-dir", e.infraOrigin, "ls-tree", "-r", "--name-only", companionBranch); !strings.Contains(files, "clusters/app/paypal.yaml") {
		t.Fatalf("companion branch files:\n%s", files)
	}

	// One draft merge request in the companion project, labelled, titled
	// after the home issue, with the "Lands after" line in the block.
	infra := e.infra()
	if len(infra.MRs) != 1 {
		t.Fatalf("one companion merge request, got %d", len(infra.MRs))
	}
	cmr := infra.MRs[1]
	if cmr.SourceBranch != companionBranch || cmr.TargetBranch != "main" || !cmr.Draft {
		t.Fatalf("companion MR %s -> %s draft=%v", cmr.SourceBranch, cmr.TargetBranch, cmr.Draft)
	}
	if cmr.Title != "Draft: Add PayPal button (companion of group/app#42)" {
		t.Fatalf("companion MR title %q", cmr.Title)
	}
	if !slices.Equal(cmr.Labels, []string{"agent::companion"}) {
		t.Fatalf("companion MR labels %v", cmr.Labels)
	}
	body, ok := managedDescription(cmr.Description)
	if !ok || !strings.Contains(body, "Lands after: group/app!1 is merged and live.") || !strings.Contains(body, "PayPal button") {
		t.Fatalf("companion MR description must have the describer's text and the Lands after line inside the block:\n%s", cmr.Description)
	}
	if strings.Contains(cmr.Description, "Closes") {
		t.Fatalf("the companion MR must not close an issue of its project:\n%s", cmr.Description)
	}

	// The home merge request links to it inside the block.
	hmr := e.implementEnv.gl.MRs[1]
	if body, ok := managedDescription(hmr.Description); !ok || !strings.Contains(body, "Companion: group/infra!1") || !strings.Contains(body, "PayPal button") {
		t.Fatalf("the home MR must have the Companion line inside the block:\n%s", hmr.Description)
	}
	if strings.Count(hmr.Description, "Companion: group/infra!1") != 1 {
		t.Fatalf("one Companion line:\n%s", hmr.Description)
	}

	st := e.taskStatus(t)
	want := aiv1.CompanionRun{Project: "group/infra", MRIID: 1, MRURL: cmr.WebURL, Branch: companionBranch, PushedSHA: pushed, ReviewedSHA: pushed}
	if st.Companion == nil || *st.Companion != want {
		t.Fatalf("status.run.companion = %+v, want %+v", st.Companion, want)
	}
	if !strings.Contains(e.allNotes(), "Opened the companion merge request group/infra!1 as a draft") {
		t.Fatalf("the report must say the companion MR was opened:\n%s", e.allNotes())
	}

	// A human marks it ready; the second run changes the companion again:
	// the same merge request, back to draft, its description rewritten.
	infra.MRs[1].Title, infra.MRs[1].Draft = "Add PayPal button (companion of group/app#42)", false
	infra.MRs[1].Description = "human text " + infra.MRs[1].Description
	e.rerun(t, r)
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("second run: failed steps %v", failed)
	}
	if len(infra.MRs) != 1 {
		t.Fatalf("the second run must find the merge request again, got %d", len(infra.MRs))
	}
	if !infra.MRs[1].Draft {
		t.Fatal("the companion merge request stays a draft")
	}
	if n := companionDescribes(engine); n != 2 {
		t.Fatalf("each run describes its new companion diff: %d describer calls", n)
	}
	if d := infra.MRs[1].Description; !strings.HasPrefix(d, "human text ") || strings.Count(d, "Lands after:") != 1 {
		t.Fatalf("the rewrite keeps the human's text and one Lands after line:\n%s", d)
	}
	again := gitCmd(t, e.tmp, "--git-dir", e.infraOrigin, "rev-parse", companionBranch)
	if again == pushed {
		t.Fatal("the second run must push the companion again")
	}
	if st := e.taskStatus(t); st.Companion == nil || st.Companion.PushedSHA != again || st.Companion.MRIID != 1 {
		t.Fatalf("status.run.companion after the second run: %+v", st.Companion)
	}
	if body, _ := managedDescription(e.implementEnv.gl.MRs[1].Description); strings.Count(body, "Companion: group/infra!1") != 1 {
		t.Fatalf("the home MR keeps one Companion line:\n%s", e.implementEnv.gl.MRs[1].Description)
	}
}

// TestImplementWithoutCompanionChangeOpensNoMergeRequest: a run that leaves
// the companion checkout as it is pushes nothing there and opens no
// merge request.
func TestImplementWithoutCompanionChangeOpensNoMergeRequest(t *testing.T) {
	e := newCompanionEnv(t)
	r, _ := e.companionRunner(t, func(string) {})
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("failed steps %v", failed)
	}
	if len(e.infra().MRs) != 0 {
		t.Fatalf("no companion merge request without a companion change: %d", len(e.infra().MRs))
	}
	if heads := remoteHeads(t, e.tmp, e.infraOrigin); strings.Contains(heads, companionBranch) {
		t.Fatalf("nothing is pushed to the companion: %s", heads)
	}
	if st := e.taskStatus(t); st.Companion != nil {
		t.Fatalf("no status.run.companion: %+v", st.Companion)
	}
	if strings.Contains(e.implementEnv.gl.MRs[1].Description, "Companion:") {
		t.Fatalf("no Companion line:\n%s", e.implementEnv.gl.MRs[1].Description)
	}
}

// TestImplementCompanionRemovalClosesMergeRequest: a later run that ends
// without the companion change (the agent reverted it, or the issue lost
// its companion) closes the merge request, deletes its branch, removes the
// home link and clears status.run.companion.
func TestImplementCompanionRemovalClosesMergeRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(t *testing.T, e *companionEnv, engine *companionEngine)
	}{
		{name: "reverted", remove: func(t *testing.T, e *companionEnv, engine *companionEngine) {
			engine.edit = func(dir string) { _ = os.Remove(filepath.Join(dir, "clusters", "app", "paypal.yaml")) }
		}},
		{name: "label removed", remove: func(t *testing.T, e *companionEnv, engine *companionEngine) {
			e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Companion = nil })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCompanionEnv(t)
			r, engine := e.companionRunner(t, companionEdits())
			if ran, err := r.Step(context.Background()); err != nil || !ran {
				t.Fatalf("ran=%v err=%v", ran, err)
			}
			infra := e.infra()
			if len(infra.MRs) != 1 || e.taskStatus(t).Companion == nil {
				t.Fatalf("the first run opens the companion MR: %d", len(infra.MRs))
			}

			tc.remove(t, e, engine)
			e.rerun(t, r)
			if failed := failedRunSteps(r); len(failed) > 0 {
				t.Fatalf("failed steps %v:\n%s", failed, e.taskStatus(t).Summary)
			}
			if infra.MRs[1].State != "closed" {
				t.Fatalf("the companion MR must be closed: %s", infra.MRs[1].State)
			}
			if !slices.Equal(infra.DeletedBranches, []string{companionBranch}) {
				t.Fatalf("deleted branches %v", infra.DeletedBranches)
			}
			if st := e.taskStatus(t); st.Companion != nil {
				t.Fatalf("status.run.companion must be cleared: %+v", st.Companion)
			}
			hmr := e.implementEnv.gl.MRs[1]
			if _, ok := managedDescription(hmr.Description); !ok || strings.Contains(hmr.Description, "Companion:") {
				t.Fatalf("the home MR loses its Companion line:\n%s", hmr.Description)
			}
			if !strings.Contains(e.allNotes(), "🗑️ The companion group/infra!1 is no longer part of this issue") {
				t.Fatalf("the report must say the companion was closed:\n%s", e.allNotes())
			}
			if len(infra.MRs) != 1 {
				t.Fatalf("no new companion MR: %d", len(infra.MRs))
			}
		})
	}
}

// TestImplementCompanionPushFailureFailsTheRun: the companion remote refuses
// the push after the home push: the run fails with a report, and the next
// run pushes the companion and opens its merge request.
func TestImplementCompanionPushFailureFailsTheRun(t *testing.T) {
	e := newCompanionEnv(t)
	hook := filepath.Join(e.infraOrigin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, _ := e.companionRunner(t, companionEdits())
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := e.taskStatus(t)
	if st.LastResult != aiv1.RunResultFailed || !strings.Contains(st.Summary, "pushing the companion branch") {
		t.Fatalf("the run must fail on the companion push: %q %s", st.LastResult, st.Summary)
	}
	if st.PushedSHA == "" {
		t.Fatal("the home branch was pushed first and is recorded")
	}
	if st.Companion != nil {
		t.Fatalf("nothing recorded for the companion: %+v", st.Companion)
	}
	if len(e.infra().MRs) != 0 {
		t.Fatal("no companion merge request without its push")
	}
	var failure bool
	for _, n := range e.implementEnv.gl.IssueNotes[42] {
		failure = failure || strings.Contains(n.Body, "The agent run failed") && strings.Contains(n.Body, "pushing the companion branch")
	}
	if !failure {
		t.Fatal("the failure is reported on the issue")
	}

	_ = os.Remove(hook)
	e.rerun(t, r)
	if failed := failedRunSteps(r); len(failed) > 0 {
		t.Fatalf("retry: failed steps %v", failed)
	}
	if st := e.taskStatus(t); st.Companion == nil || st.Companion.MRIID != 1 || st.Companion.PushedSHA == "" {
		t.Fatalf("the retry pushes the companion and opens its MR: %+v", st.Companion)
	}
}

// companionDescribes counts the describer calls for the companion's merge
// request.
func companionDescribes(e *companionEngine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, c := range e.calls {
		if c.JSONSchema == describeSchema && strings.Contains(c.Prompt, "Describe the companion merge request of issue #42") {
			n++
		}
	}
	return n
}
