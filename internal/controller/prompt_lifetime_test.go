package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// issuePrompt is an assessment of issue iid: retry it (recommended) or do
// nothing.
func issuePrompt(kind aiv1.PromptKind, iid int64) aiv1.Prompt {
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: kind, IssueIID: iid}, Question: fmt.Sprintf("#%d is stuck: what now?", iid),
		Options: []aiv1.PromptOption{
			{ID: "A", Label: "Retry it", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Recommended: true},
			{ID: "B", Label: "Do nothing"},
		}}
}

func TestMainHealthPromptsCloseWhenMainIsGreen(t *testing.T) {
	e := newManagedEnv(t)
	e.gl.AddIssue(7, "main is red", "ci::main-red")
	e.gl.AddIssue(8, "flaky rust:test", "ci::flaky")
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 7})
	st := e.task(specTaskName)
	red, flaky, anyRed := mainPrompt("red"), mainPrompt("flaky"), mainPrompt("any")
	red.Source.IssueIID, flaky.Source.IssueIID = 7, 8
	e.addPrompt(st, st, red)
	e.addPrompt(st, st, flaky)
	e.addPrompt(st, st, anyRed)

	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=superseded Q3=open" {
		t.Fatalf("red over #7: the flaky prompt of #8 goes, the others stay: %s", got)
	}

	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.reconcile()
	st = e.task(specTaskName)
	if got := promptStates(st); got != "Q1=superseded Q2=superseded Q3=superseded" {
		t.Fatalf("green again: every main health prompt goes in the same poll: %s", got)
	}
	if q := st.Status.Prompts[0]; q.ClosedAt == nil || q.Answer != nil {
		t.Fatalf("a superseded prompt is closed, unanswered: %+v", q)
	}

	// Flaky holds the prompts of the flaky issues of the last pipeline.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, Flaky: []aiv1.FlakyJobReport{{Job: "rust:test", IssueIID: 8}}})
	e.addPrompt(st, st, flaky)
	e.addPrompt(st, st, red)
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=superseded Q2=superseded Q3=superseded Q4=open Q5=superseded" {
		t.Fatalf("flaky #8: its prompt stays, the red one goes: %s", got)
	}
}

func TestPromptsOfAClosedIssueAreSuperseded(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 2))
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 9)) // gone from GitLab
	revisit := issuePrompt(aiv1.PromptRevisit, 2)
	revisit.Options[0].Actions = nil
	e.addPrompt(st, st, revisit)
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=open Q3=superseded Q4=open" {
		t.Fatalf("open issues keep their prompts, a deleted one does not: %s", got)
	}

	e.gl.Issues[2].State = "closed"
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=superseded Q3=superseded Q4=open" {
		t.Fatalf("closing #2 supersedes its prompt, not a revisit of its decision: %s", got)
	}
}

func TestProposalPromptsExpireAfterADay(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptProposal, 1))
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	posted := e.task(specTaskName).Status.Prompts[0].PostedAt.Time

	e.gl.Clock = posted.Add(proposalTTL - time.Minute)
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=open" {
		t.Fatalf("a proposal is open for a day: %s", got)
	}
	e.gl.Clock = posted.Add(proposalTTL)
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=expired Q2=open" {
		t.Fatalf("a proposal expires after a day: %s", got)
	}
	e.gl.Clock = posted.Add(3 * proposalTTL)
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=expired Q2=open" {
		t.Fatalf("other prompts do not expire by time: %s", got)
	}
}

func TestAgentQuestionIsSupersededWhenTheRunMovesOn(t *testing.T) {
	e := newManagedEnv(t)
	impl := e.task(e.taskOf(1))
	q := e.addPrompt(e.task(specTaskName), impl, agentQuestion(1))
	if q.Source.RunRequest != impl.Spec.RunRequest || q.Source.RunRequest == 0 {
		t.Fatalf("the question records the run it came from: %+v, request %d", q.Source, impl.Spec.RunRequest)
	}
	e.reconcile()
	if got := promptStates(e.task(e.taskOf(1))); got != "Q1=open" {
		t.Fatalf("the question waits while the run does: %s", got)
	}

	impl = e.task(e.taskOf(1))
	impl.Spec.RunRequest++ // retried without the answer
	if err := e.c.Update(e.ctx, impl); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := promptStates(e.task(e.taskOf(1))); got != "Q1=superseded" {
		t.Fatalf("a newer run supersedes the question: %s", got)
	}
}

func TestAnsweredPromptsAreNeverChanged(t *testing.T) {
	e := newManagedEnv(t)
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed})
	st := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("main"))
	e.addPrompt(st, st, issuePrompt(aiv1.PromptProposal, 2))
	at := metav1.NewTime(e.r.Now())
	answer := &aiv1.PromptAnswer{OptionID: "B", By: "alice", At: at, Via: "desk"}
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		for i := range s.Prompts {
			s.Prompts[i].State, s.Prompts[i].Answer, s.Prompts[i].ClosedAt = aiv1.PromptAnswered, answer, &at
		}
	})

	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.gl.Issues[2].State = "closed"
	e.gl.Clock = e.gl.Clock.Add(2 * proposalTTL)
	e.reconcile()
	for _, p := range e.task(specTaskName).Status.Prompts {
		// Closed before the reason was recorded: the fields stay empty.
		if p.State != aiv1.PromptAnswered || p.Answer == nil || p.Answer.By != "alice" || !p.ClosedAt.Equal(&at) ||
			p.ClosedReason != "" || p.ClosedBy != "" || p.ReplacedBy != "" {
			t.Fatalf("an answered prompt stays as it was: %+v", p)
		}
	}
}

func TestSyncPromptsWritesOnlyWhenSomethingCloses(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("a"))
	e.addPrompt(st, st, mainPrompt("b"))
	e.addPrompt(st, st, mainPrompt("c"))
	patches := 0
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if obj.GetName() == specTaskName {
				patches++
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})

	st = e.task(specTaskName)
	never := func(aiv1.Prompt) (promptClose, bool) { return promptClose{}, false }
	if err := e.r.syncPrompts(e.ctx, st, never); err != nil || patches != 0 {
		t.Fatalf("nothing resolved, nothing written: %d patches, %v", patches, err)
	}

	asked := 0
	notC := func(p aiv1.Prompt) (promptClose, bool) {
		asked++
		c, _ := superseded(aiv1.ClosedMainHealthy)
		return c, p.Source.Key != "c"
	}
	if err := e.r.syncPrompts(e.ctx, st, notC); err != nil || patches != 1 {
		t.Fatalf("two prompts resolved, one write: %d patches, %v", patches, err)
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=superseded Q2=superseded Q3=open" {
		t.Fatalf("the resolved prompts close: %s", got)
	}
	for _, p := range e.task(specTaskName).Status.Prompts {
		want := closeOf(aiv1.ClosedMainHealthy, aiv1.ResolvedByOperator, "")
		if p.ID == "Q3" {
			want = closeOf("", "", "")
		}
		if got := closeOf(p.ClosedReason, p.ClosedBy, p.ReplacedBy); got != want {
			t.Errorf("%s records %s, want %s", p.ID, got, want)
		}
	}
	asked = 0
	if err := e.r.syncPrompts(e.ctx, st, notC); err != nil || patches != 1 || asked != 1 {
		t.Fatalf("closed prompts are not asked again, nor written: %d patches, %d asked, %v", patches, asked, err)
	}

	// A poll with prompts that stay open writes none of them.
	e.reconcile()
	before := patches
	e.reconcile()
	if patches != before {
		t.Fatalf("a poll that closes nothing does not patch the spec task: %d patches", patches-before)
	}
}

func TestAssessmentOfAPauseClosesWithThePause(t *testing.T) {
	now := time.Date(2026, 11, 6, 19, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { return &metav1.Time{Time: now.Add(d)} }
	paused := func(reason string, iid int64, since *metav1.Time) *aiv1.AutopilotStatus {
		return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: reason, PausedFor: iid, PausedAt: since}
	}
	running := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	since := at(-time.Hour)
	held := func(since *metav1.Time) *aiv1.AutopilotStatus {
		return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI red", Since: *since}}}}
	}
	assessment := func(iid int64, key string) aiv1.Prompt {
		return aiv1.Prompt{PostedAt: at(-30 * time.Minute), Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: iid, Key: "event/" + key + "@3"}}
	}
	owner := assessment(0, "pause-"+hashString("paused by @owner")+"/0/")
	stalled := assessment(1, fmt.Sprintf("%s%d", aiv1.StalledEventPrefix, since.Unix()))
	stuck := assessment(1, "stuck/1/sha1")
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1"}}}}
	moved := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha2"}}}}

	for _, tc := range []struct {
		name  string
		p     aiv1.Prompt
		ap    *aiv1.AutopilotStatus
		tasks map[int64]*aiv1.AgentTask
		over  bool
	}{
		{"owner's pause holds", owner, paused("paused by @owner", 0, since), tasks, false},
		{"owner's pause resumed", owner, running, tasks, true},
		{"another pause without an issue", owner, paused("the plan has a cycle", 0, since), tasks, true},
		{"stalled pause holds", stalled, paused("x", 1, since), tasks, false},
		{"stalled pause lifted", stalled, running, tasks, true},
		{"a later pause after the stalled one", stalled, paused("x", 1, at(-time.Minute)), tasks, true},
		{"stuck pause holds", stuck, paused("x", 1, since), tasks, false},
		{"a later pause of the issue", stuck, paused("y", 1, at(-time.Minute)), tasks, true},
		{"the head moved", stuck, paused("x", 1, since), moved, true},
		{"hold holds", stuck, held(since), tasks, false},
		{"a later hold of the issue", stuck, held(at(-time.Minute)), tasks, true},
		{"held, the head moved", stuck, held(since), moved, true},
		{"needs input without a pause, same head", stuck, running, tasks, false},
		{"needs input without a pause, head moved", stuck, running, moved, true},
		{"not about a pause", assessment(1, "reviewRounds/1/sha1"), running, moved, false},
	} {
		st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: tc.ap}}
		if got := pauseOver(tc.p, st, tc.tasks); got != tc.over {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.over)
		}
	}
}

// closeOf shows a prompt's close fields as "reason/by/replacedBy".
func closeOf(reason aiv1.PromptClosedReason, by aiv1.PauseResolver, replacedBy string) string {
	return fmt.Sprintf("%s/%s/%s", reason, by, replacedBy)
}

func TestPauseOverRecordsWhoEndedThePause(t *testing.T) {
	now := time.Date(2026, 11, 6, 19, 0, 0, 0, time.UTC)
	since := &metav1.Time{Time: now.Add(-time.Hour)}
	posted := &metav1.Time{Time: now.Add(-30 * time.Minute)}
	assessment := func(iid int64, key string) aiv1.Prompt {
		return aiv1.Prompt{PostedAt: posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: iid, Key: "event/" + key + "@3"}}
	}
	pause := assessment(0, "pause-"+hashString("paused by @owner")+"/0/")
	stalled := assessment(1, fmt.Sprintf("%s%d", aiv1.StalledEventPrefix, since.Unix()))
	stuck := assessment(1, "stuck/1/sha1")
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha2"}}}}
	issues := map[int64]gitlab.Issue{1: {IID: 1}}
	resumed := func(by aiv1.PauseResolver) *aiv1.AutopilotStatus {
		return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, ResumedBy: by}
	}
	newer := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "another", PausedAt: since, ResumedBy: aiv1.ResolvedByPilot}

	for _, tc := range []struct {
		name string
		p    aiv1.Prompt
		ap   *aiv1.AutopilotStatus
		by   aiv1.PauseResolver
	}{
		{"the pilot ended the pause", pause, resumed(aiv1.ResolvedByPilot), aiv1.ResolvedByPilot},
		{"an owner ended the pause", pause, resumed(aiv1.ResolvedByOwner), aiv1.ResolvedByOwner},
		{"the pilot lifted the stalled pause", stalled, resumed(aiv1.ResolvedByPilot), aiv1.ResolvedByPilot},
		{"nobody recorded", pause, resumed(""), aiv1.ResolvedByOperator},
		{"a newer pause", pause, newer, aiv1.ResolvedByOperator},
		{"a stuck event's head moved", stuck, resumed(aiv1.ResolvedByPilot), aiv1.ResolvedByOperator},
	} {
		st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: tc.ap}}
		got, ok := promptResolved(now, nil, issues, tasks, st, func(int64) bool { return false })(tc.p)
		want := promptClose{State: aiv1.PromptSuperseded, Reason: aiv1.ClosedPauseOver, By: tc.by}
		if !ok || got != want {
			t.Errorf("%s: got %+v %v, want %+v", tc.name, got, ok, want)
		}
	}
}

func TestAssessmentOfARedCIHoldClosesWhenItLifts(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { return &metav1.Time{Time: now.Add(d)} }
	posted := at(-time.Hour)
	stuck := aiv1.Prompt{PostedAt: posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 1, Key: "event/stuck/1/sha1@3"}}
	task := func(recovered *metav1.Time, cause aiv1.NeedsInputCause) map[int64]*aiv1.AgentTask {
		return map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{
			Run: aiv1.RunStatus{PushedSHA: "sha1", NeedsInputCause: cause},
			CI:  &aiv1.CIStatus{SHA: "sha1", State: aiv1.CIStateSuccess, RecoveredAt: recovered}}}}
	}
	lifted := task(at(-time.Minute), "")
	held := func(kind aiv1.PauseKind, since *metav1.Time) *aiv1.AutopilotStatus {
		return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: kind, Reason: "held", Since: *since}}}}
	}
	running := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemActive}}}
	issues := map[int64]gitlab.Issue{1: {IID: 1}}
	never := func(int64) bool { return false }

	for _, tc := range []struct {
		name  string
		p     aiv1.Prompt
		ap    *aiv1.AutopilotStatus
		tasks map[int64]*aiv1.AgentTask
		want  aiv1.PromptClosedReason // "": kept
	}{
		{"the hold lifted", stuck, running, lifted, aiv1.ClosedCIGreen},
		{"the hold lifted, no autopilot", stuck, nil, lifted, aiv1.ClosedCIGreen},
		{"the hold is still there", stuck, held(aiv1.PauseCIRed, at(-2*time.Hour)), lifted, ""},
		{"held again for another reason", stuck, held(aiv1.PauseFindings, at(-2*time.Hour)), lifted, ""},
		{"paused for the issue", stuck, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 1, PausedAt: at(-2 * time.Hour)}, lifted, ""},
		{"still in needs-input for red CI", stuck, running, task(at(-time.Minute), aiv1.NeedsInputCIRed), ""},
		{"no CI recovery", stuck, running, task(nil, ""), ""},
		{"posted after the recovery", stuck, running, task(at(-2*time.Hour), ""), ""},
		{"another head's event", aiv1.Prompt{PostedAt: posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 1, Key: "event/stuck/1/sha0@3"}}, nil, lifted, ""},
		{"not a stuck event", aiv1.Prompt{PostedAt: posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 1, Key: "event/reviewRounds/1/sha1@3"}}, running, lifted, ""},
	} {
		st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: tc.ap}}
		got, ok := promptResolved(now, nil, issues, tc.tasks, st, never)(tc.p)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: closed as %+v, want it kept", tc.name, got)
			}
			continue
		}
		if want := (promptClose{State: aiv1.PromptSuperseded, Reason: tc.want, By: aiv1.ResolvedByOperator}); !ok || got != want {
			t.Errorf("%s: got %+v %v, want %+v", tc.name, got, ok, want)
		}
	}
}

func TestPromptResolved(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	posted := metav1.NewTime(now.Add(-time.Hour))
	st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemActive}, {IID: 2, State: aiv1.AutopilotItemMerged}}}}}
	impl := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{RunRequest: 3}, Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1"}}}
	tasks := map[int64]*aiv1.AgentTask{1: impl}
	issues := map[int64]gitlab.Issue{1: {IID: 1}, 2: {IID: 2}}
	var asked []int64
	closed := func(iid int64) bool { asked = append(asked, iid); return iid == 5 }
	mh := &aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 7}

	proposal := issuePrompt(aiv1.PromptProposal, 0)
	proposal.PostedAt = &posted
	proposal.PilotState = proposalState(st, tasks, proposal)
	moved := proposal
	moved.PilotState = "0123456789abcdef"
	old := proposal
	old.PostedAt = &metav1.Time{Time: now.Add(-proposalTTL)}
	question := func(run int64) aiv1.Prompt {
		q := agentQuestion(1)
		q.Source.RunRequest = run
		return q
	}
	withMain := func(iid int64) aiv1.Prompt {
		p := mainPrompt("")
		p.Source.IssueIID = iid
		return p
	}

	budget := aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptBudget, Key: budgetKey80 + "10"}}
	pause := aiv1.Prompt{PostedAt: &posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, Key: "event/pause-" + hashString("x") + "/0/@3"}}
	op := aiv1.ResolvedByOperator
	gone := func(reason aiv1.PromptClosedReason) promptClose {
		return promptClose{State: aiv1.PromptSuperseded, Reason: reason, By: op}
	}

	for _, tc := range []struct {
		name string
		p    aiv1.Prompt
		want promptClose
	}{
		{"fresh proposal", proposal, promptClose{}},
		{"proposal after a day", old, promptClose{State: aiv1.PromptExpired, Reason: aiv1.ClosedExpired, By: op}},
		{"proposal on a changed milestone", moved, gone(aiv1.ClosedStateChanged)},
		{"main red over its issue", withMain(7), promptClose{}},
		{"main red over another issue", withMain(8), gone(aiv1.ClosedMainHealthy)},
		{"main red, no issue", withMain(0), promptClose{}},
		{"question of the current run", question(3), promptClose{}},
		{"question of an older run", question(2), gone(aiv1.ClosedAgentRanAgain)},
		{"question without its run", question(0), promptClose{}},
		{"budget without a key", aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptBudget}}, promptClose{}},
		{"budget no longer set", budget, gone(aiv1.ClosedBudgetResolved)},
		{"assessment of a pause that ended", pause, gone(aiv1.ClosedPauseOver)},
		{"open issue", issuePrompt(aiv1.PromptAssessment, 1), promptClose{}},
		{"merged issue, still listed", issuePrompt(aiv1.PromptAssessment, 2), gone(aiv1.ClosedIssueDone)},
		{"closed issue", issuePrompt(aiv1.PromptAssessment, 5), gone(aiv1.ClosedIssueDone)},
		{"issue not listed, open", issuePrompt(aiv1.PromptAssessment, 6), promptClose{}},
		{"revisit of a closed issue's decision", issuePrompt(aiv1.PromptRevisit, 5), promptClose{}},
	} {
		asked = nil
		got, ok := promptResolved(now, mh, issues, tasks, st, closed)(tc.p)
		if got != tc.want || ok != (tc.want.State != "") {
			t.Errorf("%s: got %+v %v, want %+v", tc.name, got, ok, tc.want)
		}
		if iid := tc.p.Source.IssueIID; len(asked) > 0 && (asked[0] != iid || issues[iid].IID != 0 || len(asked) > 1) {
			t.Errorf("%s: GetIssue only for the prompt's issue missing from the lists, asked %v", tc.name, asked)
		}
	}

	// Main health switched off, or green: no main health prompt holds.
	for _, mh := range []*aiv1.MainHealthStatus{nil, {State: aiv1.MainHealthGreen}} {
		if _, ok := promptResolved(now, mh, issues, tasks, st, closed)(withMain(0)); !ok {
			t.Errorf("main health %+v: the prompt is superseded", mh)
		}
	}
	for _, state := range []aiv1.MainHealthState{aiv1.MainHealthRetrying, aiv1.MainHealthFlaky} {
		if _, ok := promptResolved(now, &aiv1.MainHealthStatus{State: state}, issues, tasks, st, closed)(withMain(0)); ok {
			t.Errorf("main %s: the prompt holds", state)
		}
	}
}
