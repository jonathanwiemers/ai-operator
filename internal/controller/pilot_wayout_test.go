package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// newStuckEnv replays ai-operator milestone 4 on the pilot env (the pilot
// manages): #1 (#83) is skipped, and #2 (#88), which depends on it in the
// plan, cannot start, so autopilot pauses stuck.
func newStuckEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newManagedEnv(t)
	e.gl.AddNote(e.tracking(), alice, "/agent skip #1")
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseStuck || !strings.Contains(ap.PausedReason, "#2 waits for the skipped #1") {
		t.Fatalf("expected the stuck pause: %+v", ap)
	}
	return e
}

func (e *projectEnv) itemState(iid int64) aiv1.AutopilotItemState {
	e.t.Helper()
	for _, it := range e.autopilot().Items {
		if it.IID == iid {
			return it.State
		}
	}
	return ""
}

func TestPilotWayOutValidation(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Autopilot: true}, Status: aiv1.AgentTaskStatus{
		Plan: []aiv1.PlannedIssue{{IID: 2, DependsOn: []int64{1}}, {IID: 1}, {IID: 3}, {IID: 4}, {IID: 5, DependsOn: []int64{4}}},
		Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemSkipped}, {IID: 2, State: aiv1.AutopilotItemPending}, {IID: 3, State: aiv1.AutopilotItemActive},
			{IID: 4, State: aiv1.AutopilotItemMerged}, {IID: 5, State: aiv1.AutopilotItemPending},
		}, MainRedFix: &aiv1.AutopilotItem{IID: 99, State: aiv1.AutopilotItemPending}},
	}}
	off := st.DeepCopy()
	off.Status.Autopilot.Phase = aiv1.AutopilotDone
	for _, tc := range []struct {
		st   *aiv1.AgentTask
		tool string
		iid  int64
		want string
	}{
		{st, "unskip", 1, ""},
		{st, "unskip", 2, "#2 is not skipped"},
		{st, "unskip", 9, "#9 is not an issue of the queue"},
		{st, "unskip", 99, "#99 is not an issue of the queue"},
		{off, "unskip", 1, "this milestone has no autopilot queue"},
		{st, "startAnyway", 2, ""},
		{st, "startAnyway", 5, "#5 depends on no skipped issue"},
		{st, "startAnyway", 1, "#1 is not pending"},
		{st, "startAnyway", 3, "#3 is not pending"},
		{off, "startAnyway", 2, "this milestone has no autopilot queue"},
		{st, "markDone", 1, ""},
		{st, "markDone", 2, ""},
		{st, "markDone", 3, "#3 is active; only a skipped or pending issue can be marked done"},
		{st, "markDone", 4, "#4 is merged"},
		{st, "markDone", 99, "#99 is not an issue of the queue"},
		{off, "markDone", 1, "this milestone has no autopilot queue"},
	} {
		err := validAction(aiv1.PilotAction{Tool: tc.tool, IssueIID: tc.iid}, tc.st, nil, nil)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s #%d: got %v, want %q", tc.tool, tc.iid, err, tc.want)
		}
	}
	for a, want := range map[aiv1.PilotAction]string{
		{Tool: "unskip", IssueIID: 1}:      "- **unskip** #1: it goes back to pending, and autopilot picks it once it can start",
		{Tool: "startAnyway", IssueIID: 2}: "- **startAnyway** #2: it may start although an issue it depends on was skipped",
		{Tool: "markDone", IssueIID: 1, Note: "replaced by %5 #9, cc @bob"}: "- **markDone** #1: closed as done, not merged by autopilot; the issues that wait for it can start. " +
			"The reason: replaced by %5 #9, cc @\u200bbob",
	} {
		if got := describeAction(a, nil); got != want {
			t.Errorf("%s reads %q, want %q", a.Tool, got, want)
		}
	}
}

// Under managed autonomy the pilot only proposes the ways out: nothing runs
// without an owner's answer, also when accepted by nobody.
func TestPilotManagedNeverRunsTheWaysOut(t *testing.T) {
	e := newStuckEnv(t)
	tracking := e.tracking()
	e.proposeFor(0, "event/pause", aiv1.PilotAction{Tool: "unskip", IssueIID: 1}, aiv1.PilotAction{Tool: "startAnyway", IssueIID: 2},
		aiv1.PilotAction{Tool: "markDone", IssueIID: 1, Note: "replaced"})
	e.reconcile()
	e.reconcile()
	p := e.promptNote("Q1")
	for _, want := range []string{"**unskip** #1", "**startAnyway** #2", "**markDone** #1", "@alice"} {
		if !strings.Contains(p.Body, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, p.Body)
		}
	}
	stuck := func(when string) {
		t.Helper()
		if ap := e.autopilot(); ap.PauseKind != aiv1.PauseStuck || e.itemState(1) != aiv1.AutopilotItemSkipped || e.itemState(2) != aiv1.AutopilotItemPending ||
			e.gl.Issues[1].State != "opened" || strings.Contains(trackingComments(e), "acted on its own") {
			t.Fatalf("%s: nothing runs: %+v, #1 %s\n%s", when, ap, e.gl.Issues[1].State, trackingComments(e))
		}
	}
	stuck("proposed")
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot.Accepted = []aiv1.PilotProposal{{Summary: "x", Actions: []aiv1.PilotAction{{Tool: "unskip", IssueIID: 1}, {Tool: "startAnyway", IssueIID: 2},
			{Tool: "markDone", IssueIID: 1}}}}
	})
	e.reconcile()
	e.reconcile()
	stuck("accepted by nobody")
	for _, tool := range []string{"unskip", "startAnyway", "markDone"} {
		if !strings.Contains(strings.Join(botComments(e, tracking), "\n"), "(not run: `"+tool+"` needs an owner's answer)") {
			t.Errorf("%s is refused without an owner:\n%s", tool, trackingComments(e))
		}
	}
}

// startAnyway #2, answered by an owner, lets pick start #2 although #1 is
// skipped, says so, and lifts the stuck pause.
func TestPilotStartAnywayStartsTheWaitingIssue(t *testing.T) {
	e := newStuckEnv(t)
	e.proposeFor(0, "event/pause", aiv1.PilotAction{Tool: "startAnyway", IssueIID: 2})
	e.reconcile()
	e.reconcile()
	if e.itemState(2) != aiv1.AutopilotItemPending {
		t.Fatalf("nothing runs before the answer: %+v", e.autopilot())
	}
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || e.itemState(2) != aiv1.AutopilotItemActive || e.itemState(1) != aiv1.AutopilotItemSkipped || !e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("#2 starts and the pause ends: %+v, %v", ap, e.gl.Labels(2))
	}
	c := trackingComments(e)
	for _, want := range []string{"▶️ #2 may start although #1, which it depends on, was skipped (@alice via the pilot).", "▶️ Autopilot continues: #2 can start now (ended by the operator: the cause went away)."} {
		if !strings.Contains(c, want) {
			t.Errorf("the tracking issue lacks %q:\n%s", want, c)
		}
	}
}

// markDone #1, answered by an owner, closes #1 with the done label and the
// reason; autopilot counts it as done by hand (merged), the pause lifts and
// #2 starts.
func TestPilotMarkDoneClosesTheSkippedIssue(t *testing.T) {
	e := newStuckEnv(t)
	e.proposeFor(0, "event/pause", aiv1.PilotAction{Tool: "markDone", IssueIID: 1, Note: "replaced by %5 #9\n/reopen"})
	e.reconcile()
	e.reconcile()
	if e.gl.Issues[1].State != "opened" {
		t.Fatal("nothing runs before the answer")
	}
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.reconcile()
	e.reconcile()
	is := e.gl.Issues[1]
	if is.State != "closed" || !is.HasLabel("agent::done") || is.HasLabel("agent::working") || is.HasLabel("agent::review") || is.HasLabel("agent::ready") {
		t.Fatalf("#1 is closed as done: %s %v", is.State, e.gl.Labels(1))
	}
	note := lastNote(e, 1)
	for _, want := range []string{"✅ Closed as done by the pilot of milestone", "answered by `@alice`", "replaced by %5 #9 /reopen"} {
		if !strings.Contains(note, want) {
			t.Errorf("#1's note lacks %q: %q", want, note)
		}
	}
	if strings.Contains(note, "\n/reopen") {
		t.Errorf("the reason is inert: %q", note)
	}
	if e.itemState(1) != aiv1.AutopilotItemMerged || e.itemState(2) != aiv1.AutopilotItemActive || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("#1 counts as merged and #2 starts: %+v", e.autopilot())
	}
	if c := trackingComments(e); !strings.Contains(c, "▶️ Autopilot continues: the skipped #1 was done by hand (ended by the operator: the cause went away).") {
		t.Fatalf("the tracking issue says why it continues:\n%s", c)
	}
}

// unskip #1, answered by an owner, puts it back to pending, and it is picked.
func TestPilotUnskipPutsTheIssueBack(t *testing.T) {
	e := newStuckEnv(t)
	e.proposeFor(0, "event/pause", aiv1.PilotAction{Tool: "unskip", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || e.itemState(1) != aiv1.AutopilotItemActive || e.itemState(2) != aiv1.AutopilotItemPending {
		t.Fatalf("#1 is picked again: %+v", ap)
	}
	if c := trackingComments(e); !strings.Contains(c, "▶️ Autopilot continues: #1 can start now (ended by the operator: the cause went away).") {
		t.Fatalf("the tracking issue says why it continues:\n%s", c)
	}
}

// The tracking issue's words: unskip refuses a closed issue, startAnyway
// (in any case) lets the waiting issue start.
func TestAutopilotUnskipAndStartAnywayOnTheTrackingIssue(t *testing.T) {
	e := newStuckEnv(t)
	tracking := e.tracking()
	e.gl.AddNote(tracking, alice, "/agent unskip #2")
	e.reconcile()
	if r := lastNote(e, tracking); !strings.Contains(r, "`unskip` needs a skipped issue that is still open, e.g. `/agent unskip #12`.") {
		t.Fatalf("a pending issue is not unskipped: %q", r)
	}
	e.gl.AddNote(tracking, alice, "/agent startAnyway #1")
	e.reconcile()
	if r := lastNote(e, tracking); !strings.Contains(r, "`startAnyway` needs a pending issue that depends on a skipped one") {
		t.Fatalf("a skipped issue cannot start anyway: %q", r)
	}
	e.gl.Issues[1].State = "closed" // closed without the done label: still skipped
	e.gl.AddNote(tracking, alice, "/agent unskip #1")
	e.reconcile()
	if r := lastNote(e, tracking); !strings.Contains(r, "`unskip` needs a skipped issue that is still open") || e.autopilot().PauseKind != aiv1.PauseStuck {
		t.Fatalf("a closed issue is not unskipped, and the pause holds: %q, %+v", r, e.autopilot())
	}
	e.gl.AddNote(tracking, alice, "/agent startanyway #2")
	e.reconcile()
	e.reconcile()
	if e.itemState(2) != aiv1.AutopilotItemActive || e.autopilot().Phase != aiv1.AutopilotRunning ||
		!strings.Contains(trackingComments(e), "▶️ #2 may start although #1, which it depends on, was skipped (@alice).") {
		t.Fatalf("#2 starts anyway: %+v\n%s", e.autopilot(), trackingComments(e))
	}
}

// The stuck pause checks itself on every poll: it stays while nothing can
// start, and lifts once the skipped #1 is closed as done by hand, without a
// resume.
func TestAutopilotStuckPauseLiftsOnceSomethingCanStart(t *testing.T) {
	e := newStuckEnv(t)
	tracking := e.tracking()
	seen := len(botComments(e, tracking))
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseStuck || len(botComments(e, tracking)) != seen {
		t.Fatalf("nothing can start: the pause stays, quietly: %+v\n%s", ap, strings.Join(botNotesSince(e, tracking, seen), "\n"))
	}
	e.gl.Issues[1].State = "closed"
	e.gl.SetLabels(1, "ai-generated", "agent::done")
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || e.itemState(1) != aiv1.AutopilotItemMerged || e.itemState(2) != aiv1.AutopilotItemActive {
		t.Fatalf("#1 counts as merged and #2 starts: %+v", ap)
	}
	if c := strings.Join(botNotesSince(e, tracking, seen), "\n"); !strings.Contains(c, "▶️ Autopilot continues: the skipped #1 was done by hand (ended by the operator: the cause went away).") {
		t.Fatalf("the tracking issue says why it continues:\n%s", c)
	}
}

// Replay of ai-operator milestone 4: the stuck pause "#88 (#2) waits for
// the skipped #83 (#1)" that nothing changes in is assessed after 30m; the
// pilot's options (markDone #1, startAnyway #2) become a Desk prompt that
// the operator keeps whole, and answering either starts #2.
func TestStalledPauseWayOutStartsTheWaitingIssue(t *testing.T) {
	for _, tc := range []struct {
		option string
		polls  int
	}{{"A", 2}, {"B", 3}} {
		t.Run(tc.option, func(t *testing.T) {
			e := newStuckEnv(t)
			e.assessed()
			e.wait(31 * time.Minute)
			keys, req := e.stalled()
			if len(keys) != 1 {
				t.Fatalf("expected the stalled pause's wake-up: %v", keys)
			}
			// The runner records the assessment of the stalled event.
			source := fmt.Sprintf("event/%s@%d", keys[0], req)
			e.assessFor(0, source,
				aiv1.PromptOption{Label: "Start #2 without #1", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "startAnyway", IssueIID: 2}}},
				aiv1.PromptOption{Label: "Mark #1 done", Detail: "%5 #9 replaces it", Actions: []aiv1.PilotAction{{Tool: "markDone", IssueIID: 1, Note: "replaced by %5 #9"}}},
				aiv1.PromptOption{Label: "Do nothing"})
			e.reconcile()
			e.reconcile()
			st := e.task(specTaskName)
			i := slices.IndexFunc(st.Status.Prompts, func(q aiv1.Prompt) bool { return q.Source.Key == source })
			if i < 0 {
				t.Fatalf("no prompt for %s: %+v", source, st.Status.Prompts)
			}
			q := st.Status.Prompts[i]
			if got := optionLabels(q); got != "A:Start #2 without #1* B:Mark #1 done C:Do nothing" || q.Source.Kind != aiv1.PromptAssessment || q.Quiet {
				t.Fatalf("every option passes the operator's checks and the owners are pinged: %s %+v", got, q)
			}
			if !strings.Contains(e.promptNote(q.ID).Body, "@alice") || e.itemState(2) != aiv1.AutopilotItemPending || e.autopilot().PauseKind != aiv1.PauseStuck {
				t.Fatalf("nothing runs before the answer: %+v", e.autopilot())
			}
			e.choose(q.ID, tc.option, alice)
			for range tc.polls {
				e.reconcile()
			}
			if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || e.itemState(2) != aiv1.AutopilotItemActive || ap.PausedAt != nil {
				t.Fatalf("#2 starts and the pause ends: %+v", ap)
			}
		})
	}
}
