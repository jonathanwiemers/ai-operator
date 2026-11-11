package runner

import (
	"context"
	"encoding/json"
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
	for _, want := range []string{`"state": "Merged"`, `"mergeRequestIID": 9`, "\"decisions\": [\n        \"D1-P1\"\n      ]", `"revisitOf": "D1-P1"`, "Autopilot paused: both reviewers",
		`"costUSD": "1.50"`, `"duration": "3h0m0s"`, "Build it."} {
		if !strings.Contains(inv.Prompt, want) {
			t.Errorf("the facts miss %s", want)
		}
	}
	// Small facts: nothing trimmed, decisions as IDs, cost and time before
	// the queue.
	if p := inv.Prompt; strings.Contains(p, "Use REST") || strings.Contains(p, `"trimmed"`) ||
		strings.Index(p, `"duration"`) > strings.Index(p, `"queue"`) || strings.Index(p, `"autopilotComments"`) > strings.Index(p, `"queue"`) {
		t.Errorf("the facts' shape:\n%s", p)
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
	for _, want := range []string{`"costUSD": "42.12"`, `"costFrom": "agent calls"`, `"subagentCalls": 3`, `"unmeteredCalls": 2`, `"unmetered": true`, `"topIssues": [`, `"label": "claude-opus"`} {
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

// #274: a milestone with one cursor auto call. The facts carry the count
// next to costKnown and the auto row flagged, without cost or tokens. With
// no call unmetered, the count is still there as 0: a summary without the
// field (omitempty in the CRD type) would read the same as one built before
// the count existed.
func TestRetroFactsCarryTheUnmeteredCount(t *testing.T) {
	for _, unmetered := range []int64{1, 0} {
		t.Run(fmt.Sprint(unmetered), func(t *testing.T) {
			e := newRetroEnv(t, "")
			task := e.task(t)
			// As spendSummary builds it from the store
			// (TestRetroSpendOfACursorAutoCall in internal/controller).
			s := &aiv1.SpendSummary{CostUSD: "0.5000", CostKnown: true, UnmeteredCalls: unmetered,
				Tokens:    aiv1.SpendTokens{Input: 10, Output: 20, CacheRead: 300},
				TopModels: []aiv1.SpendDriver{{Key: "claude-opus-5-5", Label: "claude-opus-5-5", CostUSD: "0.5000", Tokens: 330}}}
			if unmetered > 0 {
				s.TopModels = append(s.TopModels, aiv1.SpendDriver{Key: "auto", Label: aiv1.CursorAutoLabel, Unmetered: true})
			}
			// Through JSON, as the API server stores it.
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			task.Spec.Source.RetroSpend = nil
			if err := json.Unmarshal(b, &task.Spec.Source.RetroSpend); err != nil {
				t.Fatal(err)
			}
			if err := e.k8s.Update(context.Background(), &task); err != nil {
				t.Fatal(err)
			}
			e.step(t)
			if len(e.engine.calls) != 1 {
				t.Fatalf("expected one retrospective call, got %d", len(e.engine.calls))
			}
			var facts struct {
				Spend map[string]json.RawMessage `json:"spend"`
			}
			if err := json.Unmarshal([]byte(retroFactsOf(t, e.engine.calls[0].Prompt)), &facts); err != nil {
				t.Fatal(err)
			}
			if got := string(facts.Spend["unmeteredCalls"]); got != fmt.Sprint(unmetered) {
				t.Errorf("spend.unmeteredCalls = %q, want %d", got, unmetered)
			}
			if got := string(facts.Spend["costKnown"]); got != "true" {
				t.Errorf("spend.costKnown = %q, want true", got)
			}
			var models []map[string]any
			if err := json.Unmarshal(facts.Spend["topModels"], &models); err != nil {
				t.Fatal(err)
			}
			var auto map[string]any
			for _, m := range models {
				if m["key"] == "auto" {
					auto = m
				}
			}
			switch {
			case unmetered == 0 && auto != nil:
				t.Errorf("an auto row without auto calls: %v", models)
			case unmetered == 0:
			case auto == nil:
				t.Errorf("no auto row in %v", models)
			case auto["unmetered"] != true || auto["label"] != "cursor auto (unmetered)":
				t.Errorf("auto row %v, want unmetered, labelled cursor auto (unmetered)", auto)
			case auto["costUSD"] != nil || auto["tokens"] != nil:
				t.Errorf("auto row %v has a cost or tokens", auto)
			}
		})
	}
}

// #276: the facts name the milestone's token basis and carry the old basis
// count, 0 included; the cost drivers' total line notes an old or mixed
// basis.
func TestRetroFactsNameTheTokenBasis(t *testing.T) {
	const total = "- Total: $0.50, 330 tokens (input 10, output 20, cache read 300, cache write 0)"
	for _, tc := range []struct {
		name       string
		old, calls int64
		line       string
	}{
		{"mixed", 1, 2, total + ", 1 of 2 calls on the old token basis: tokens not comparable with newer milestones\n"},
		{"old", 2, 2, total + ", old token basis: tokens not comparable with newer milestones\n"},
		{"current", 0, 2, total + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetroEnv(t, "")
			task := e.task(t)
			s := &aiv1.SpendSummary{CostUSD: "0.5000", CostKnown: true, Calls: tc.calls, OldBasisCalls: tc.old,
				Tokens: aiv1.SpendTokens{Input: 10, Output: 20, CacheRead: 300}}
			// Through JSON, as the API server stores it.
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			task.Spec.Source.RetroSpend = nil
			if err := json.Unmarshal(b, &task.Spec.Source.RetroSpend); err != nil {
				t.Fatal(err)
			}
			if err := e.k8s.Update(context.Background(), &task); err != nil {
				t.Fatal(err)
			}
			e.step(t)
			if len(e.engine.calls) != 1 {
				t.Fatalf("expected one retrospective call, got %d", len(e.engine.calls))
			}
			var facts struct {
				Spend map[string]json.RawMessage `json:"spend"`
			}
			if err := json.Unmarshal([]byte(retroFactsOf(t, e.engine.calls[0].Prompt)), &facts); err != nil {
				t.Fatal(err)
			}
			if got := string(facts.Spend["tokenBasis"]); got != `"`+tc.name+`"` {
				t.Errorf("spend.tokenBasis = %s, want %q", got, tc.name)
			}
			if got := string(facts.Spend["oldBasisCalls"]); got != fmt.Sprint(tc.old) {
				t.Errorf("spend.oldBasisCalls = %q, want %d", got, tc.old)
			}
			notes := botNotes(e.gl, 5)
			if c := notes[len(notes)-1]; !strings.Contains(c, "**Cost drivers**\n\n"+tc.line) {
				t.Errorf("the total line is not %q:\n%s", tc.line, c)
			}
		})
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

// retroFactsOf returns the facts in the retrospective's prompt.
func retroFactsOf(t *testing.T, prompt string) string {
	t.Helper()
	_, rest, ok := strings.Cut(prompt, "```json\n")
	facts, _, ok2 := strings.Cut(rest, "\n```\n</untrusted-data>")
	if !ok || !ok2 {
		t.Fatalf("no facts in the prompt:\n%s", prompt)
	}
	return facts
}

// A 40-issue milestone with 20 decisions each, long titles, 500-rune
// summaries, a long spec and 60 more comments fits: valid JSON under
// maxRetroFacts with every queue entry, every pause, resume, skip and end
// comment with its time, the cost and the duration.
func TestRetroFactsOfALongMilestoneFit(t *testing.T) {
	e := newRetroEnv(t, "")
	ctx := context.Background()
	e.gl.Milestones[0].Description += "\n" + strings.Repeat("The spec goes on and on. ", 600)
	long := strings.Repeat("ä", 230)
	var items []aiv1.AutopilotItem
	for i := range int64(40) {
		iid := 100 + i
		e.gl.AddIssue(iid, fmt.Sprintf("#%d %s", iid, long)).Milestone = &e.gl.Milestones[0]
		it := aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemMerged, MergeRequestIID: 1000 + iid}
		for d := range 20 {
			it.Decisions = append(it.Decisions, aiv1.DecisionRef{ID: fmt.Sprintf("D%d-%d", iid, d+1), Title: "A decision with a long title " + long,
				URL: fmt.Sprintf("https://gitlab.example/group/app/-/issues/%d#note_%d", iid, 4_000_000_000+d), Superseded: d == 3})
		}
		if i == 0 {
			it.Decisions = append(it.Decisions, it.Decisions[:5]...) // 25: 5 more
		}
		items = append(items, it)
		impl := &aiv1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("shop-%d-x", iid), Namespace: "agents", Labels: map[string]string{labelProject: "shop"}},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Milestone: &aiv1.MilestoneRef{ID: 11, IID: 3},
				Issue: &aiv1.IssueRef{IID: iid}},
		}
		if err := e.k8s.Create(ctx, impl); err != nil {
			t.Fatal(err)
		}
		impl.Status.Run.CostUSD, impl.Status.Run.Summary = "0.5000", strings.Repeat("ö", 600)
		if err := e.k8s.Status().Update(ctx, impl); err != nil {
			t.Fatal(err)
		}
	}
	var st aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-spec-m3-checkout-v2"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Autopilot.Items = items
	if err := e.k8s.Status().Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	// 60 more comments of the bot: 24 essential among other ones, all long.
	essential := []string{"⏸️ Autopilot paused: CI is red on #%d.", "▶️ Autopilot continues: #%d is merged (ended by @alice).", "⏭️ #%d was closed without a merge, so it is skipped."}
	for i := range 60 {
		body := fmt.Sprintf("📌 Other news about #%d.", 100+i)
		if i%5 < 2 || i%5 == 4 && i < 40 {
			body = fmt.Sprintf(essential[i%3], 100+i)
		}
		e.gl.AddNote(5, e.gl.Bot, "🤖 "+body+" "+strings.Repeat("ü", 600))
	}
	e.gl.AddNote(5, e.gl.Bot, "🤖 ⏹️ The milestone ends here (by @alice): nothing left.")
	e.gl.AddNote(5, e.gl.Bot, "🤖 🏁 The milestone is closed.")
	e.gl.AddNote(5, e.gl.Bot, "🤖 ✅ Autopilot is done: 40 of 40 issues merged.")
	task := e.task(t)
	task.Spec.Source.RetroSpend = &aiv1.SpendSummary{CostUSD: "123.4567", CostKnown: true}
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.step(t)
	if len(e.engine.calls) != 1 {
		t.Fatalf("expected one retrospective call, got %d", len(e.engine.calls))
	}
	facts := retroFactsOf(t, e.engine.calls[0].Prompt)
	if !json.Valid([]byte(facts)) || len(facts) > maxRetroFacts {
		t.Fatalf("facts of %d bytes, valid %v", len(facts), json.Valid([]byte(facts)))
	}
	var v struct {
		Trimmed   []string `json:"trimmed"`
		CostUSD   string   `json:"costUSD"`
		CostFrom  string   `json:"costFrom"`
		Duration  string   `json:"duration"`
		Autopilot []string `json:"autopilotComments"`
		Queue     []struct {
			IID          int64    `json:"iid"`
			Title        string   `json:"title"`
			State        string   `json:"state"`
			MergeRequest int64    `json:"mergeRequestIID"`
			Decisions    []string `json:"decisions"`
			Superseded   []string `json:"supersededDecisions"`
			More         int      `json:"moreDecisions"`
		} `json:"queue"`
	}
	if err := json.Unmarshal([]byte(facts), &v); err != nil {
		t.Fatal(err)
	}
	if v.CostUSD != "123.46" || v.CostFrom != "agent calls" || v.Duration != "3h0m0s" || !slices.Contains(v.Trimmed, "summaries") {
		t.Fatalf("cost %q from %q, duration %q, trimmed %q", v.CostUSD, v.CostFrom, v.Duration, v.Trimmed)
	}
	if len(v.Queue) != 40 {
		t.Fatalf("expected 40 queue entries, got %d", len(v.Queue))
	}
	for i, q := range v.Queue {
		iid := int64(100 + i)
		if q.IID != iid || !strings.HasPrefix(q.Title, fmt.Sprintf("#%d ää", iid)) || q.State != "Merged" || q.MergeRequest != 1000+iid ||
			len(q.Decisions) != 19 || q.Decisions[0] != fmt.Sprintf("D%d-1", iid) || !slices.Equal(q.Superseded, []string{fmt.Sprintf("D%d-4", iid)}) {
			t.Fatalf("queue entry %d: %+v", i, q)
		}
		if want := map[bool]int{true: 5}[i == 0]; q.More != want {
			t.Errorf("#%d: %d more decisions, want %d", iid, q.More, want)
		}
	}
	if strings.Contains(facts, "A decision with a long title") || strings.Contains(facts, "#note_") {
		t.Error("decisions are IDs only")
	}
	for _, n := range e.gl.IssueNotes[5] {
		body := strings.TrimPrefix(n.Body, "🤖 ")
		if n.Author.ID != e.gl.Bot.ID || !slices.ContainsFunc(retroEssential, func(p string) bool { return strings.HasPrefix(body, p) }) {
			continue
		}
		head := n.CreatedAt.UTC().Format(time.RFC3339) + " " + textPrefix(n.Body, 40)
		if !slices.ContainsFunc(v.Autopilot, func(c string) bool { return strings.HasPrefix(c, head) }) {
			t.Errorf("the facts miss the comment %q", head)
		}
	}
}

func textPrefix(s string, runes int) string {
	return string([]rune(s)[:min(runes, utf8.RuneCountInString(s))])
}

// Shortening the summaries is enough here: the rest stays, and trimmed
// says so.
func TestFitRetroFactsShortensSummariesFirst(t *testing.T) {
	v := retroFactsDoc{Milestone: "M", Spec: "spec", CostUSD: "1.00", CostFrom: "agent calls", Duration: "1h0m0s",
		Spend: &retroSpendFacts{SpendSummary: &aiv1.SpendSummary{CostUSD: "1.0000", CostKnown: true}, TokenBasis: tokenBasisCurrent}}
	for i := range 10 {
		v.Queue = append(v.Queue, retroIssue{IID: int64(i), Title: "t", State: "Merged", Summary: strings.Repeat("x", 500)})
	}
	comments := []retroComment{{at: "2026-11-01T10:00:00Z", body: "🤖 📌 other " + strings.Repeat("y", 500)}, {at: "2026-11-01T11:00:00Z", body: "🤖 ⏸️ paused", essential: true}}
	full, err := fitRetroFacts(v, comments, 1<<20)
	if err != nil || strings.Contains(string(full), "trimmed") || !strings.Contains(string(full), "\n  \"spec\": \"spec\",\n") {
		t.Fatalf("under the limit nothing is trimmed: %v\n%s", err, full)
	}
	// Just over: without indentation, nothing is lost.
	compact, err := fitRetroFacts(v, comments, len(full)-1)
	if err != nil || strings.Contains(string(compact), "trimmed") || strings.Contains(string(compact), "\n") {
		t.Fatalf("without indentation: %v\n%s", err, compact)
	}
	b, err := fitRetroFacts(v, comments, len(compact)-500)
	if err != nil || !json.Valid(b) || len(b) > len(compact)-500 {
		t.Fatalf("%d bytes, %v", len(b), err)
	}
	var got retroFactsDoc
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Trimmed, []string{"summaries"}) || utf8.RuneCountInString(got.Queue[0].Summary) != 201 || got.Spec != "spec" || len(got.Autopilot) != 2 {
		t.Fatalf("only the summaries are shortened: %+v", got)
	}
	if v.Queue[0].Summary != strings.Repeat("x", 500) {
		t.Fatal("the caller's facts are not changed")
	}

	// Far too small: everything but the essentials goes, still valid JSON.
	b, err = fitRetroFacts(v, comments, 10)
	if err != nil || !json.Valid(b) {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.Contains(string(b), `"spend":{"costUSD":"1.0000","costKnown":true,"tokens":{},"unmeteredCalls":0,"oldBasisCalls":0,"tokenBasis":"current"}`) {
		t.Errorf("the spend stays whole, its unmetered and old basis counts 0 included:\n%s", b)
	}
	got = retroFactsDoc{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Trimmed, []string{"summaries", "spec", "comments", "otherComments"}) || len(got.Queue) != 10 || got.Queue[0].Summary != "" ||
		got.Spec != "" || !slices.Equal(got.Autopilot, []string{"2026-11-01T11:00:00Z 🤖 ⏸️ paused"}) || got.CostUSD != "1.00" || got.Duration != "1h0m0s" {
		t.Fatalf("the essentials stay: %+v", got)
	}
}

// The essential comments are kept also beyond maxRetroComments; the others
// are the newest.
func TestRetroCommentsKeepTheEssentialOnes(t *testing.T) {
	bot := gitlab.User{ID: 9}
	var notes []gitlab.Note
	for i := range 100 {
		body := fmt.Sprintf("🤖 📌 note %d", i)
		if i < 70 && i%2 == 0 {
			body = fmt.Sprintf("🤖 ⏸️ Autopilot paused %d", i)
		}
		notes = append(notes, gitlab.Note{Author: bot, Body: body})
	}
	notes = append(notes, gitlab.Note{Author: gitlab.User{ID: 7}, Body: "🤖 ⏸️ fake"}, gitlab.Note{Author: bot, Body: "no prefix"})
	cs := retroComments(notes, 9)
	essential := 0
	for _, c := range cs {
		if c.essential {
			essential++
		}
	}
	if essential != 35 || len(cs) != 60 || cs[0].body != "🤖 ⏸️ Autopilot paused 0" || cs[len(cs)-1].body != "🤖 📌 note 99" ||
		slices.ContainsFunc(cs, func(c retroComment) bool { return c.body == "🤖 📌 note 69" }) {
		t.Fatalf("%d essential of %d: first %q, last %q", essential, len(cs), cs[0].body, cs[len(cs)-1].body)
	}
}

// retroReleasesTask sets the milestone's release records on the spec task.
func retroReleasesTask(t *testing.T, e *pilotEnv, recs []aiv1.ReleaseRecord) {
	t.Helper()
	var st aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop-spec-m3-checkout-v2"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Autopilot.Releases = recs
	if err := e.k8s.Status().Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
}

// The facts carry the milestone's releases, the final one included, with the
// agent's reason on one line and shortened.
func TestRetroFactsCarryTheReleases(t *testing.T) {
	e := newRetroEnv(t, "")
	start, end := metav1.NewTime(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)), metav1.NewTime(time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC))
	retroReleasesTask(t, e, []aiv1.ReleaseRecord{
		{ID: 1, By: aiv1.ReleaseByPlanner, Why: "users see it\nnow " + strings.Repeat("x", 300), AfterIssue: 1, State: aiv1.ReleaseReleased,
			Version: "2026.11.4", Issues: []int64{1}, StartedAt: &start, EndedAt: &end},
		{ID: 2, By: aiv1.ReleaseByPilot, Why: "not needed", AfterIssue: 2, State: aiv1.ReleaseDropped},
		{ID: 3, By: aiv1.ReleaseByFinal, State: aiv1.ReleaseNotNeeded, Reason: "docs only since 2026.11.4", Final: true},
	})
	e.step(t)
	var v retroFactsDoc
	if err := json.Unmarshal([]byte(retroFactsOf(t, e.engine.calls[0].Prompt)), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Releases) != 3 {
		t.Fatalf("releases: %+v", v.Releases)
	}
	r := v.Releases[0]
	if r.By != "planner" || r.AfterIssue != 1 || r.State != "Released" || r.Version != "2026.11.4" || !slices.Equal(r.Issues, []int64{1}) ||
		r.StartedAt != "2026-11-02T10:00:00Z" || r.EndedAt != "2026-11-02T10:05:00Z" ||
		!strings.HasPrefix(r.Why, "users see it now xxx") || utf8.RuneCountInString(r.Why) != maxRetroReleaseWhy+1 {
		t.Errorf("released: %+v", r)
	}
	if v.Releases[1].State != "Dropped" || v.Releases[1].By != "pilot" {
		t.Errorf("dropped: %+v", v.Releases[1])
	}
	if f := v.Releases[2]; f.By != "final" || f.State != "NotNeeded" || f.Reason != "docs only since 2026.11.4" {
		t.Errorf("final: %+v", f)
	}
}

// A milestone without releases has no releases in its facts.
func TestRetroFactsWithoutReleases(t *testing.T) {
	e := newRetroEnv(t, "")
	e.step(t)
	if p := e.engine.calls[0].Prompt; strings.Contains(p, `"releases"`) {
		t.Errorf("no releases key without records:\n%s", p)
	}
}

// Too long facts lose the releases' reasons, then their issues and times,
// before anything of the queue, and name it.
func TestFitRetroFactsTrimsTheReleases(t *testing.T) {
	v := retroFactsDoc{Milestone: "M", CostUSD: "1.00", Duration: "1h0m0s"}
	for i := range 10 {
		v.Queue = append(v.Queue, retroIssue{IID: int64(i), Title: "t", State: "Merged"})
		v.Releases = append(v.Releases, retroRelease{By: "planner", Why: strings.Repeat("w", 200), State: "Released", Version: "v",
			Issues: []int64{1, 2, 3}, StartedAt: "2026-11-02T10:00:00Z", EndedAt: "2026-11-02T10:05:00Z"})
	}
	full, err := fitRetroFacts(v, nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := fitRetroFacts(v, nil, len(full)-1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fitRetroFacts(v, nil, len(compact)-100)
	if err != nil || !json.Valid(b) {
		t.Fatalf("%v\n%s", err, b)
	}
	var got retroFactsDoc
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Trimmed, []string{"releases"}) || got.Releases[0].Why != "" || got.Releases[0].Version != "v" || len(got.Releases[0].Issues) != 3 || len(got.Queue) != 10 {
		t.Fatalf("only the reasons go: %+v", got)
	}
	if v.Releases[0].Why == "" {
		t.Fatal("the caller's facts are not changed")
	}
	b, err = fitRetroFacts(v, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	got = retroFactsDoc{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Releases[0].Issues != nil || got.Releases[0].EndedAt != "" || got.Releases[0].State != "Released" || got.Releases[0].By != "planner" || len(got.Releases) != 10 || len(got.Queue) != 10 {
		t.Fatalf("the records stay: %+v", got.Releases[0])
	}
}
