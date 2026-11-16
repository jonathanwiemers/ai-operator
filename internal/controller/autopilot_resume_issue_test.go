package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// heldOf is the hold of iid in the queue, nil when it is not held.
func (e *projectEnv) heldOf(iid int64) *aiv1.ItemHold {
	e.t.Helper()
	for _, it := range e.autopilot().Items {
		if it.IID == iid {
			return it.Hold
		}
	}
	return nil
}

func TestAutopilotResumeIssueLiftsOneHold(t *testing.T) {
	e := newParallelEnv(t, 3, false)
	e.reconcile()
	e.reconcile()
	e.holdItems(1, 2)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	human := gitlab.User{ID: 1, Username: "jonathan"}

	e.gl.AddNote(tracking, human, "/agent resume #2")
	e.reconcile()
	e.reconcile()
	if e.heldOf(2) != nil || e.heldOf(1) == nil {
		t.Fatalf("only #2's hold is lifted: %+v", e.autopilot().Items)
	}
	e.wantUnheld("resume #2", 2, "▶️ #2 goes on: resumed (ended by @")
	if c := trackingComments(e); strings.Contains(c, "#1 goes on") || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("#1 stays held, the milestone runs: %+v\n%s", e.autopilot(), c)
	}
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "Held:\n- #1 API: CI of !1 is red (`/agent resume #1`)\n") ||
		strings.Contains(body, "- #2 UI:") || !strings.Contains(body, "`/agent resume [#<issue>]`") {
		t.Fatalf("the tracking issue lists #1's hold only:\n%s", body)
	}

	// Not held (any more), or not planned: the usage, nothing changes.
	usage := "🤖 `resume #N` needs a held issue, e.g. `/agent resume #12`."
	for _, cmd := range []string{"/agent resume #9", "/agent resume #2"} {
		before := len(commandReplies(e, tracking))
		e.gl.AddNote(tracking, human, cmd)
		e.reconcile()
		got := commandReplies(e, tracking)
		if len(got) != before+1 || !strings.HasPrefix(got[len(got)-1], usage) {
			t.Fatalf("%s: want the usage once: %q", cmd, got)
		}
		if e.heldOf(1) == nil || strings.Count(trackingComments(e), "▶️") != 1 {
			t.Fatalf("%s changes nothing: %+v\n%s", cmd, e.autopilot().Items, trackingComments(e))
		}
	}
}

// At a limit of 1 a problem pauses the milestone for the issue; `resume #N`
// for that issue is a plain `resume`.
func TestAutopilotResumeIssueOverAPauseForIt(t *testing.T) {
	e := newParallelEnv(t, 1, false)
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 {
		t.Fatalf("paused for #1 at limit 1: %+v", ap)
	}
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume #1")
	e.reconcile()
	if c := trackingComments(e); !strings.Contains(c, "▶️ Autopilot continues: resumed (ended by @") {
		t.Fatalf("resume #1 resumes the milestone:\n%s", c)
	}
}

func TestAutopilotDeskResumeIssue(t *testing.T) {
	e := newParallelEnv(t, 3, false)
	e.reconcile()
	e.reconcile()
	e.holdItems(1, 2)
	e.reconcile()
	st := e.tasks()[specTaskName]
	st.Spec.Source.ChatMessages = []aiv1.ChatMessage{
		{ID: 101, Kind: "command", Text: `{"cmd":"resume","issue":2}`, Author: "ada@x", AuthorName: "Ada"},
		{ID: 102, Kind: "command", Text: `{"cmd":"resume","issue":9}`, Author: "ada@x", AuthorName: "Ada"},
	}
	if err := e.c.Update(e.ctx, &st); err != nil {
		t.Fatal(err)
	}
	e.reconcile()

	st = e.tasks()[specTaskName]
	var got []string
	for _, a := range st.Spec.Source.ChatAnswers {
		got = append(got, a.Text)
	}
	if want := []string{"#2 goes on.", "`resume #N` needs a held issue, e.g. `/agent resume #12`."}; !slices.Equal(got, want) {
		t.Fatalf("answers = %q", got)
	}
	if e.heldOf(2) != nil || e.heldOf(1) == nil {
		t.Fatalf("only #2's hold is lifted: %+v", e.autopilot().Items)
	}
	e.wantUnheld("Desk resume #2", 2, "▶️ #2 goes on: resumed (ended by Ada via the Desk).")
}

func TestOwnerMergeEndsAFindingsHold(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description = "/agent owners @alice\n" + e.gl.Milestones[0].Description
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 1 })
	e.reconcile()
	e.wantHeld("findings hold #1", 1, aiv1.PauseFindings)

	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
	for range 4 {
		e.reconcile()
	}
	if !slices.Contains(e.gl.Merged, int64(1)) || e.states()[1] != merged {
		t.Fatalf("the override ends the hold and #1 lands: merged %v, %+v\n%s", e.gl.Merged, e.autopilot(), trackingComments(e))
	}
	e.wantUnheld("merge #1", 1, "▶️ #1 goes on: it may merge without a full review (ended by @")
}

func TestExitLinesNameTheHeldIssue(t *testing.T) {
	a := &autopilot{spec: aiv1.AgentProjectSpec{Workflow: aiv1.WorkflowSpec{CommandPrefix: "/agent"}}}
	for _, kind := range []aiv1.PauseKind{aiv1.PauseMergeRequest, aiv1.PauseConfig, aiv1.PauseCompanion} {
		if got := a.exitLines(kind, 2, true); !strings.Contains(got, "`/agent resume #2` here") || strings.Contains(got, "`/agent resume` here") {
			t.Fatalf("%s hold: resume names #2:\n%s", kind, got)
		}
		if got := a.exitLines(kind, 2, false); !strings.Contains(got, "`/agent resume` here") {
			t.Fatalf("%s pause: plain resume:\n%s", kind, got)
		}
	}
}
