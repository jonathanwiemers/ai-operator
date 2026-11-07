package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const rebasedHead = "sha-api-rebased"

var reviewedCommits = []gitlab.Commit{
	{ID: "c1", Title: "Add the API", AuthorEmail: "ai-operator@example.com"},
	{ID: "c2", Title: "Add its migration", AuthorEmail: "ai-operator@example.com"},
}

// lines is a diff of n added lines "<prefix> i".
func lines(prefix string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", n)
	for i := range n {
		fmt.Fprintf(&b, "+%s %d\n", prefix, i)
	}
	return b.String()
}

// newRebaseEnv is #1, reviewed and green at sha-api on main-1 with diffs and
// two commits by the bot, after the default branch moved to main-2: !1
// needs a rebase.
func newRebaseEnv(t *testing.T, diffs []gitlab.Diff) (*projectEnv, string) {
	t.Helper()
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.gl.MainSHA = "main-1"
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRDiffs = map[int64][]gitlab.Diff{1: diffs}
	e.gl.MRCommits = map[int64][]gitlab.Commit{1: slices.Clone(reviewedCommits)}
	e.gl.SetHead(1, "sha-api") // the reviewed version
	e.gl.MainSHA = "main-2"
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	return e, name
}

// rebaseNotes are the MR notes about the interdiff.
func rebaseNotes(e *projectEnv) []gitlab.Note {
	return slices.DeleteFunc(slices.Clone(e.gl.MRNotes[1]), func(n gitlab.Note) bool { return !strings.Contains(n.Body, "Interdiff (reviewed → rebased)") })
}

// The acceptance case: the rebase changed lines only in a file the default
// branch changed too, with the reviewed commits. The reviewers check the new
// head again without a pause, and it is merged once reviewed and green.
func TestRebaseChangedOnlyWhatMainChangedIsReReviewedAndMerged(t *testing.T) {
	e, name := newRebaseEnv(t, []gitlab.Diff{
		{OldPath: "app.go", NewPath: "app.go", Diff: lines("api", 50)},
		{OldPath: "migrations/0010_api.sql", NewPath: "migrations/0010_api.sql", NewFile: true, Diff: lines("CREATE", 3)},
	})
	e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {
		// main reworked app.go: 50 lines of the change are other lines now.
		{OldPath: "app.go", NewPath: "app.go", Diff: lines("api @owner", 50)},
		// and took 0010, so the migration is 0011 now.
		{OldPath: "migrations/0011_api.sql", NewPath: "migrations/0011_api.sql", NewFile: true, Diff: lines("CREATE", 3)},
	}}
	e.gl.Compares = map[string]*gitlab.Compare{"main-1..main-2": {Diffs: []gitlab.Diff{
		{OldPath: "app.go", NewPath: "app.go"}, {OldPath: "migrations/0010_other.sql", NewPath: "migrations/0010_other.sql"},
	}}}
	e.reconcile() // rebase requested
	e.reconcile() // the rebased head is judged
	ap := e.autopilot()
	task := e.tasks()[name]
	run := task.Status.Run
	if ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" || ap.Items[0].AutoReReviews != 1 || ap.Items[0].AutoReReviewHead != rebasedHead || ap.Items[0].RebasedFrom != "" {
		t.Fatalf("a mechanical rebase must not pause: %+v", ap)
	}
	if task.Spec.Source.ReviewFor != rebasedHead || !task.RunPending() || run.PushedSHA != rebasedHead || run.ReviewedSHA != "sha-api" || !e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("the reviewers must be asked for the rebased head, which is not reviewed yet: %+v %+v %v", task.Spec.Source, run, e.gl.Labels(1))
	}
	notes := rebaseNotes(e)
	if len(notes) != 1 {
		t.Fatalf("one interdiff note expected: %+v", e.gl.MRNotes[1])
	}
	body := notes[0].Body
	for _, s := range []string{"🔁 The rebase of !1 onto `main` changed the reviewed change", "every file of the interdiff", "without a pause",
		"-+api 0\n", "++api @", "+++ rebased: migrations/0011_api.sql\n# moved"} {
		if !strings.Contains(body, s) {
			t.Fatalf("note lacks %q:\n%s", s, body)
		}
	}
	if strings.Contains(body, "@owner") {
		t.Fatalf("the interdiff must be defused:\n%s", body)
	}
	if notes[0].Author.ID != e.gl.Bot.ID {
		t.Fatalf("the note is the bot's: %+v", notes[0].Author)
	}

	// While the review-only run is pending, the new head is not someone's push.
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || len(e.gl.Merged) != 0 || len(rebaseNotes(e)) != 1 {
		t.Fatalf("waits for the review: %+v, merged %v", ap, e.gl.Merged)
	}

	// The review lands clean and CI is green: merged.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = task.Spec.RunRequest
		s.Run.ReviewedSHA, s.Run.ReviewedFor = rebasedHead, rebasedHead
		s.CI = &aiv1.CIStatus{SHA: rebasedHead, State: aiv1.CIStateSuccess}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) || e.autopilot().Phase == aiv1.AutopilotPaused {
		t.Fatalf("reviewed and green, it is merged: %v, %+v", e.gl.Merged, e.autopilot())
	}
}

// A small interdiff (a renumbered migration, a line the rebase adapted) is
// mechanical wherever it is.
func TestRebaseWithASmallInterdiffIsReReviewed(t *testing.T) {
	e, name := newRebaseEnv(t, []gitlab.Diff{
		{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"},
		{OldPath: "migrations/0010_api.sql", NewPath: "migrations/0010_api.sql", NewFile: true, Diff: lines("CREATE", 30)},
	})
	e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {
		{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -7 +7 @@\n-older\n+new\n"},
		{OldPath: "migrations/0011_api.sql", NewPath: "migrations/0011_api.sql", NewFile: true, Diff: lines("CREATE", 30)},
	}}
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].AutoReReviews != 1 || e.tasks()[name].Spec.Source.ReviewFor != rebasedHead {
		t.Fatalf("a small interdiff is re-reviewed: %+v", ap)
	}
	if notes := rebaseNotes(e); len(notes) != 1 || !strings.Contains(notes[0].Body, "has 3 changed lines (at most 40)") || !strings.Contains(notes[0].Body, "# moved") {
		t.Fatalf("note: %+v", notes)
	}
}

// A commit by someone else during the rebase window pauses as before, with
// the interdiff on the MR.
func TestRebaseWithSomeoneElsesCommitPauses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		commits []gitlab.Commit
	}{
		{"an extra commit", append([]gitlab.Commit{{ID: "h", Title: "Quick fix", AuthorEmail: "human@example.com"}}, reviewedCommits...)},
		{"another author", []gitlab.Commit{reviewedCommits[0], {ID: "c2", Title: "Add its migration", AuthorEmail: "human@example.com"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, name := newRebaseEnv(t, []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"}})
			e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1,2 @@\n-old\n+new\n+quick fix\n"}}}
			e.gl.RebaseCommits = map[int64][]gitlab.Commit{1: tc.commits}
			e.reconcile()
			e.reconcile()
			ap := e.autopilot()
			if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseMergeRequest || !ap.ResumeWhenReady ||
				!strings.Contains(ap.PausedReason, "not the one that was reviewed (someone pushed, or the rebase changed lines)") ||
				!strings.Contains(ap.PausedReason, "the interdiff (reviewed → rebased) is on !1") || ap.Items[0].AutoReReviews != 0 {
				t.Fatalf("someone's push pauses: %+v", ap)
			}
			task := e.tasks()[name]
			if task.Spec.Source.ReviewFor != rebasedHead || task.Status.Run.PushedSHA != "sha-api" || len(e.gl.Merged) != 0 {
				t.Fatalf("the reviewers take the new head over, as before: %+v %+v", task.Spec.Source, task.Status.Run)
			}
			if notes := rebaseNotes(e); len(notes) != 1 || !strings.Contains(notes[0].Body, "someone pushed to the branch") || !strings.Contains(notes[0].Body, "++quick fix") ||
				!strings.Contains(notes[0].Body, "autopilot pauses") {
				t.Fatalf("note: %+v", notes)
			}
		})
	}
}

// A large interdiff in files the default branch did not change pauses.
func TestRebaseWithALargeInterdiffElsewherePauses(t *testing.T) {
	e, _ := newRebaseEnv(t, []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: lines("api", 30)}})
	e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: lines("other", 30)}}}
	e.gl.Compares = map[string]*gitlab.Compare{"main-1..main-2": {Diffs: []gitlab.Diff{{OldPath: "web.go", NewPath: "web.go"}}}}
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseMergeRequest || !strings.Contains(ap.PausedReason, "60 changed lines (more than 40)") || ap.Items[0].AutoReReviews != 0 {
		t.Fatalf("a large interdiff elsewhere pauses: %+v", ap)
	}
	if notes := rebaseNotes(e); len(notes) != 1 || !strings.Contains(notes[0].Body, "in files `main` did not change") {
		t.Fatalf("note: %+v", notes)
	}
}

// The third rebase of an item that changes lines pauses.
func TestThirdLineChangingRebasePauses(t *testing.T) {
	e, name := newRebaseEnv(t, []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"}})
	for round := 1; round <= 3; round++ {
		head := e.gl.MRs[1].SHA
		e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: fmt.Sprintf("@@ -1 +1 @@\n-old %d\n+new\n", round)}}}
		e.gl.MRs[1].DetailedMergeStatus = "need_rebase"
		e.reconcile() // rebase requested
		e.reconcile() // judged
		ap := e.autopilot()
		if round == 3 {
			if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "re-reviewed after 2 rebases without a pause already (at most 2)") || ap.Items[0].AutoReReviews != 2 {
				t.Fatalf("the third line-changing rebase pauses: %+v", ap)
			}
			if notes := rebaseNotes(e); len(notes) != 3 || !strings.Contains(notes[2].Body, "autopilot pauses") {
				t.Fatalf("notes: %+v", notes)
			}
			return
		}
		task := e.tasks()[name]
		if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].AutoReReviews != int32(round) || task.Spec.Source.ReviewFor != head+"-rebased" {
			t.Fatalf("round %d is re-reviewed: %+v %+v", round, ap, task.Spec.Source)
		}
		// The review lands clean and CI is green on the new head, the default
		// branch moves on again.
		e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
			s.Run.ObservedRunRequest = task.Spec.RunRequest
			s.Run.ReviewedSHA, s.Run.ReviewedFor = task.Status.Run.PushedSHA, task.Status.Run.PushedSHA
			s.CI = &aiv1.CIStatus{SHA: task.Status.Run.PushedSHA, State: aiv1.CIStateSuccess}
		})
		e.gl.SetLabels(1, "ai-generated", "agent::review")
		e.gl.MainSHA = fmt.Sprintf("main-%d", round+2)
	}
}

// A rebase that keeps the change keeps the fast path: no versions read.
func TestUnchangedRebaseDoesNotReadVersions(t *testing.T) {
	e, name := newRebaseEnv(t, []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"}})
	e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -9 +9 @@\n ctx\n-old\n+new\n"}}}
	e.reconcile()
	e.reconcile()
	run := e.tasks()[name].Status.Run
	if e.gl.VersionLists != 0 || run.ReviewedSHA != rebasedHead || run.PushedSHA != rebasedHead || len(rebaseNotes(e)) != 0 || e.autopilot().Items[0].AutoReReviews != 0 {
		t.Fatalf("the fast path holds the review: versions read %d, %+v", e.gl.VersionLists, run)
	}
}

// A change GitLab does not return in full cannot be judged: it pauses
// without a note.
func TestRebaseOfATooLargeChangePauses(t *testing.T) {
	e, _ := newRebaseEnv(t, []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"}, {NewPath: "big.go", Diff: "+x\n"}})
	e.gl.RebaseDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-older\n+new\n"}, {NewPath: "big.go", Collapsed: true}}}
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.Items[0].AutoReReviews != 0 || len(rebaseNotes(e)) != 0 {
		t.Fatalf("an incomplete change pauses: %+v", ap)
	}
}

func TestPilotHearsOfTheReReview(t *testing.T) {
	st := &aiv1.AgentTask{}
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemActive, MergeRequestIID: 5, AutoReReviews: 1, AutoReReviewHead: "head2"},
	}}
	task := &aiv1.AgentTask{}
	task.Status.Run.PushedSHA, task.Status.Run.ReviewedSHA = "head2", "head1"
	tasks := map[int64]*aiv1.AgentTask{1: task}
	evs := pilotEvents(aiv1.AgentProjectSpec{}, nil, st, nil, tasks, false, time.Time{})
	if len(evs) != 1 || evs[0].Key != "rereview/1/head2" || !strings.Contains(evs[0].Text, "re-reviews the rebased head") {
		t.Fatalf("events %+v", evs)
	}
	task.Status.Run.ReviewedSHA = "head2"
	if evs := pilotEvents(aiv1.AgentProjectSpec{}, nil, st, nil, tasks, false, time.Time{}); len(evs) != 0 {
		t.Fatalf("reviewed, the event ends: %+v", evs)
	}
}
