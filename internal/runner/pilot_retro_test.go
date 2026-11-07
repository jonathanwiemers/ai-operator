package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// newRetroEnv is the pilot env once autopilot is done: #1 merged after a
// pause, #2 skipped, three hours after the tracking issue #5 was opened.
// The operator asked the pilot for the retrospective with request 2.
func newRetroEnv(t *testing.T, costUSD string) *pilotEnv {
	t.Helper()
	e := newPilotEnv(t, costUSD)
	ctx := context.Background()
	e.gl.Users = []gitlab.User{{ID: 21, Username: "alice"}}
	e.gl.AddNote(5, e.gl.Bot, "🤖 ⏸️ Autopilot paused: both reviewers of #1 failed.")
	e.gl.AddNote(5, e.gl.Bot, "🤖 ▶️ Autopilot continues: #1 is merged.")
	e.gl.AddNote(5, e.gl.Bot, "🤖 ✅ Autopilot is done: 1 of 2 issues merged.")
	tracking := e.gl.Issues[5]
	closed := tracking.CreatedAt.Add(3 * time.Hour)
	tracking.State, tracking.ClosedAt = "closed", &closed

	var st aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-spec-m3-checkout-v2"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, TrackingIssueIID: 5, Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemMerged, MergeRequestIID: 9, Decisions: []aiv1.DecisionRef{{ID: "D1-P1", Title: "Use REST"}}},
		{IID: 2, State: aiv1.AutopilotItemSkipped, RevisitOf: "D1-P1"},
	}}
	if err := e.k8s.Status().Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	var impl aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-1-api"}, &impl); err != nil {
		t.Fatal(err)
	}
	impl.Status.Run.CostUSD = "1.5000"
	if err := e.k8s.Status().Update(ctx, &impl); err != nil {
		t.Fatal(err)
	}
	e.patchStatus(t, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = 1 })
	task := e.task(t)
	task.Spec.RunRequest, task.Spec.Source.Retrospective = 2, 2
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.engine.retro = &RetroAnswer{Summary: "Merged #1 after a review outage. Ask @bob.\n/merge"}
	for i := 1; i <= 10; i++ {
		e.engine.retro.FollowUps = append(e.engine.retro.FollowUps, RetroFollowUp{Title: fmt.Sprintf("Follow-up %d for @bob", i), Body: "Because.\n/close"})
	}
	return e
}

func TestRetrospectiveIsPostedOnce(t *testing.T) {
	e := newRetroEnv(t, "")
	e.patchProject(t, func(p *aiv1.AgentProject) {
		p.Spec.Agents.Implement.RoleSpec = aiv1.RoleSpec{Model: "claude-strong", AllowedTools: []string{"Bash", "Edit"}, Instructions: "Write code."}
	})
	e.engine.retro.Summary += ` <img src=x onerror="alert(1)">Fixed <b>it</b> in ` + "`<b>`."
	e.step(t)
	if len(e.engine.calls) != 1 {
		t.Fatalf("expected one retrospective call, got %d", len(e.engine.calls))
	}
	inv := e.engine.calls[0]
	if !inv.ReadOnly || inv.Model != "claude-strong" || inv.JSONSchema != retroSchema || !slices.Equal(inv.AllowedTools, readOnlyTools) ||
		strings.Contains(inv.SystemPrompt, "Write code.") || inv.MaxBudgetUSD != "20.00" {
		t.Fatalf("a read-only call on the implement role's model, without its tools or instructions: %+v", inv)
	}
	for _, want := range []string{`"state": "Merged"`, `"mergeRequestIID": 9`, `"id": "D1-P1"`, `"revisitOf": "D1-P1"`, "Autopilot paused: both reviewers",
		`"costUSD": "1.50"`, `"duration": "3h0m0s"`, "Build it."} {
		if !strings.Contains(inv.Prompt, want) {
			t.Errorf("the facts miss %s", want)
		}
	}
	notes := botNotes(e.gl, 5)
	c := notes[len(notes)-1]
	if len(notes) != 4 || !strings.HasPrefix(c, "🧭 **Retrospective**\n\nMerged #1 after a review outage.") ||
		!strings.Contains(c, "1. **Follow-up 1 for @​bob**: Because. /close") || !strings.Contains(c, "9. **Follow-up 9") || strings.Contains(c, "10. ") ||
		!strings.Contains(c, "An owner opens them with `create 1 2` as a reply in this thread.") || !strings.HasSuffix(c, marker.Retrospective) {
		t.Fatalf("expected one retrospective with numbered follow-ups:\n%s", c)
	}
	if strings.Contains(c, " @bob") || strings.Contains(c, "\n/merge") {
		t.Fatalf("the retrospective is defused:\n%s", c)
	}
	task := e.task(t)
	retro := task.Status.Pilot.Retrospective
	if retro == nil || retro.NoteID != e.gl.IssueNotes[5][len(e.gl.IssueNotes[5])-1].ID || retro.PostedAt == nil || len(retro.FollowUps) != 9 ||
		retro.FollowUps[0].Title != "Follow-up 1 for @bob" || retro.FollowUps[0].Body != "Because.\n/close" || !slices.Equal(retro.OwnerIDs, []int64{21}) || retro.Failed != "" {
		t.Fatalf("the retrospective is recorded for the operator: %+v", retro)
	}
	// The Desk's copy is the comment without HTML; code spans stay.
	if !strings.Contains(c, "<img src=x") || retro.Text != retroText(c) || !strings.HasPrefix(retro.Text, "🧭 **Retrospective**\n\nMerged #1 after a review outage.") ||
		!strings.Contains(retro.Text, "Fixed it in `<b>`.") || strings.Contains(retro.Text, "<img") || strings.Contains(retro.Text, "<!--") ||
		!strings.HasSuffix(retro.Text, "as a reply in this thread.") {
		t.Fatalf("the retrospective's text:\n%s", retro.Text)
	}
	if r := task.Status.Run; r.ObservedRunRequest != 2 || r.LastResult != aiv1.RunResultSucceeded || len(task.Status.Pilot.Proposed) != 0 {
		t.Fatalf("run status %+v, proposed %+v", r, task.Status.Pilot.Proposed)
	}

	// A run started again after posting (pod lost) does not post twice.
	e.patchStatus(t, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest, s.Pilot.Retrospective = 1, nil })
	e.step(t)
	cut := e.task(t).Status.Pilot.Retrospective
	if len(e.engine.calls) != 1 || len(botNotes(e.gl, 5)) != 4 || cut.NoteID != retro.NoteID {
		t.Fatalf("one retrospective: %d calls, %d comments, %+v", len(e.engine.calls), len(botNotes(e.gl, 5)), cut)
	}
	// The found comment gives the text.
	if cut.Text != retro.Text || !strings.Contains(cut.Failed, "cut off") {
		t.Fatalf("the cut-off retrospective's text:\n%s", cut.Text)
	}
}

func TestRetrospectiveWithoutBudgetCallsNoModel(t *testing.T) {
	e := newRetroEnv(t, "20.0000")
	e.step(t)
	notes := botNotes(e.gl, 5)
	c := notes[len(notes)-1]
	if len(e.engine.calls) != 0 || len(notes) != 4 || !strings.Contains(c, "Autopilot merged 1 of 2 issues in 3h0m0s") ||
		!strings.Contains(c, "$21.50") || !strings.Contains(c, "with 1 pauses") || !strings.Contains(c, "budget for this milestone ($20") ||
		strings.Contains(c, "create 1 2") || !strings.HasSuffix(c, marker.Retrospective) {
		t.Fatalf("a used-up budget posts the facts without a model call: %d calls\n%s", len(e.engine.calls), c)
	}
	if retro := e.task(t).Status.Pilot.Retrospective; retro == nil || retro.NoteID == 0 || len(retro.FollowUps) != 0 {
		t.Fatalf("recorded without follow-ups: %+v", retro)
	}
}

func TestRetrospectiveFailureIsRecorded(t *testing.T) {
	e := newRetroEnv(t, "")
	e.engine.err = errors.New("engine crashed")
	e.step(t)
	task := e.task(t)
	if len(botNotes(e.gl, 5)) != 3 || task.Status.Run.LastResult != aiv1.RunResultFailed || task.Status.Run.ObservedRunRequest != 2 {
		t.Fatalf("a failed retrospective posts nothing and ends the run: %+v", task.Status.Run)
	}
	if retro := task.Status.Pilot.Retrospective; retro == nil || !strings.Contains(retro.Failed, "engine crashed") || retro.NoteID != 0 {
		t.Fatalf("the failure is recorded: %+v", retro)
	}
}

func TestRetrospectiveAnswersWaitingMessages(t *testing.T) {
	e := newRetroEnv(t, "")
	e.patchStatus(t, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = 0 }) // the question of request 1 is open
	e.step(t)
	notes := botNotes(e.gl, 5)
	if len(e.engine.calls) != 1 || len(notes) != 5 || !strings.Contains(notes[3], "retrospective follows on #5") {
		t.Fatalf("an open question is told the milestone is done, not answered: %d calls, %q", len(e.engine.calls), notes)
	}
}

func TestRetrospectiveReportsTheSpendFromTheDatabase(t *testing.T) {
	e := newRetroEnv(t, "")
	task := e.task(t)
	task.Spec.Source.RetroSpend = &aiv1.SpendSummary{CostUSD: "42.1234", CostKnown: true, SubagentCalls: 3, UnmeteredCalls: 2,
		Tokens:    aiv1.SpendTokens{Input: 1200, Output: 34000, CacheRead: 2_500_000, CacheWrite: 50000},
		TopIssues: []aiv1.SpendDriver{{Key: "1", Label: "#1", CostUSD: "30.0000", Tokens: 2_000_000}, {Key: "2", Label: "#2", CostUSD: "9.0000", Tokens: 500_000}},
		TopKinds:  []aiv1.SpendDriver{{Key: "implement", Label: "implement", CostUSD: "35.0000", Tokens: 2_100_000}},
		TopModels: []aiv1.SpendDriver{{Key: "claude-opus", Label: "claude-opus", CostUSD: "40.0000", Tokens: 2_400_000}, {Key: "auto", Label: "auto", Unmetered: true}},
	}
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.step(t)
	if len(e.engine.calls) != 1 {
		t.Fatalf("expected one retrospective call, got %d", len(e.engine.calls))
	}
	prompt := e.engine.calls[0].Prompt
	for _, want := range []string{`"costUSD": "42.12"`, `"costFrom": "agent calls"`, `"subagentCalls": 3`, `"topIssues": [`, `"label": "claude-opus"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the facts miss %s", want)
		}
	}
	notes := botNotes(e.gl, 5)
	c := notes[len(notes)-1]
	for _, want := range []string{
		"**Cost drivers**\n\n- Total: $42.12, 2.59M tokens (input 1.2k, output 34.0k, cache read 2.50M, cache write 50.0k), 3 subagent calls, 2 calls not metered\n",
		"- Issue: #1 $30.00 (2.00M tokens); then #2 $9.00 (500.0k tokens)\n",
		"- Kind: implement $35.00 (2.10M tokens)\n",
		"- Model: claude-opus $40.00 (2.40M tokens); then cursor auto (unmetered)\n",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("the retrospective misses %q:\n%s", want, c)
		}
	}
}

func TestRetrospectiveWithoutDatabaseSumsLiveTasks(t *testing.T) {
	e := newRetroEnv(t, "20.0000")
	e.step(t)
	notes := botNotes(e.gl, 5)
	c := notes[len(notes)-1]
	if !strings.Contains(c, "for $21.50 over the milestone's runs (from live tasks)") || strings.Contains(c, "Cost drivers") {
		t.Fatalf("without the database, the cost is the live tasks' sum:\n%s", c)
	}
}

// The facts list the milestone's companions: how each landed, its release
// and who merged it, and a companion whose home merge request never merged.
func TestRetrospectiveListsTheCompanions(t *testing.T) {
	for _, budget := range []string{"", "20.0000"} {
		t.Run("budget used "+budget, func(t *testing.T) {
			e := newRetroEnv(t, budget)
			ctx := context.Background()
			var p aiv1.AgentProject
			if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &p); err != nil {
				t.Fatal(err)
			}
			started := metav1.Now()
			p.Status.Companions = []aiv1.CompanionStatus{
				{HomeIssueIID: 1, MilestoneIID: 3, Project: "group/infra", MRIID: 4, State: aiv1.LandingMerged, Release: "2026.11.3", MergeStartedAt: &started},
				{HomeIssueIID: 8, MilestoneIID: 9, Project: "group/infra", MRIID: 7, State: aiv1.LandingReady},
				{HomeIssueIID: 2, MilestoneIID: 3, Project: "group/other", MRIID: 5, State: aiv1.LandingMerged},
			}
			if err := e.k8s.Update(ctx, &p); err != nil {
				t.Fatal(err)
			}
			var impl aiv1.AgentTask
			if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-1-api"}, &impl); err != nil {
				t.Fatal(err)
			}
			impl.Status.Run.Companion = &aiv1.CompanionRun{Project: "group/infra", MRIID: 6}
			if err := e.k8s.Status().Update(ctx, &impl); err != nil {
				t.Fatal(err)
			}
			e.step(t)
			if budget != "" {
				notes := botNotes(e.gl, 5)
				c := notes[len(notes)-1]
				if !strings.Contains(c, "Companions: group/infra!4 of #1 merged by autopilot after 2026.11.3; group/other!5 of #2 merged by hand; "+
					"group/infra!6 of #1 not landed, its home merge request was not merged.") {
					t.Fatalf("the operator's record lists the companions:\n%s", c)
				}
				return
			}
			prompt := e.engine.calls[0].Prompt
			for _, want := range []string{`"companions": [`, `"mergeRequestIID": 4`, `"release": "2026.11.3"`, `"mergedBy": "autopilot"`,
				`"project": "group/other"`, `"mergedBy": "hand"`, `"mergeRequestIID": 6`, `"state": "homeNotMerged"`} {
				if !strings.Contains(prompt, want) {
					t.Errorf("the facts miss %s", want)
				}
			}
			if strings.Contains(prompt, `"mergeRequestIID": 7`) {
				t.Error("a companion of another milestone is not listed")
			}
		})
	}
}

func TestRetroText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"marker and comments", "**Retro** <!-- hidden -->done\n\n" + marker.Retrospective, "**Retro** done"},
		{"unclosed comment", "kept <!-- to the end\nall", "kept"},
		{"tags", `<p align="center">a <img src=x onerror="alert(1)"/> <br/>b</p></details>`, "a  b"},
		{"code kept", "use `<b>` and\n```\n<div>x</div>\n```\nnot <i>this</i>", "use `<b>` and\n```\n<div>x</div>\n```\nnot this"},
		{"no tags", "a < b, c > d, <https://example.com>, 1<2", "a < b, c > d, <https://example.com>, 1<2"},
	} {
		if got := retroText(c.in); got != c.want {
			t.Errorf("%s: retroText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	long := strings.Repeat("ä", aiv1.MaxRetroText) // two bytes each
	got := retroText("<b>" + long + "</b>")
	if len(got) > aiv1.MaxRetroText || !strings.HasSuffix(got, "…") || !utf8.ValidString(got) || !strings.HasPrefix(got, "ää") {
		t.Errorf("long text: %d bytes, valid %v, ends %q", len(got), utf8.ValidString(got), got[len(got)-6:])
	}
	if s := strings.Repeat("a", aiv1.MaxRetroText); retroText(s) != s {
		t.Error("text at the limit is cut")
	}
}
