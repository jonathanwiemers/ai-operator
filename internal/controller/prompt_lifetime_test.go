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
		if p.State != aiv1.PromptAnswered || p.Answer == nil || p.Answer.By != "alice" || !p.ClosedAt.Equal(&at) {
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
	never := func(aiv1.Prompt) (aiv1.PromptState, bool) { return "", false }
	if err := e.r.syncPrompts(e.ctx, st, never); err != nil || patches != 0 {
		t.Fatalf("nothing resolved, nothing written: %d patches, %v", patches, err)
	}

	asked := 0
	notC := func(p aiv1.Prompt) (aiv1.PromptState, bool) {
		asked++
		return aiv1.PromptSuperseded, p.Source.Key != "c"
	}
	if err := e.r.syncPrompts(e.ctx, st, notC); err != nil || patches != 1 {
		t.Fatalf("two prompts resolved, one write: %d patches, %v", patches, err)
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=superseded Q2=superseded Q3=open" {
		t.Fatalf("the resolved prompts close: %s", got)
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

	for _, tc := range []struct {
		name  string
		p     aiv1.Prompt
		state aiv1.PromptState
	}{
		{"fresh proposal", proposal, ""},
		{"proposal after a day", old, aiv1.PromptExpired},
		{"proposal on a changed milestone", moved, aiv1.PromptSuperseded},
		{"main red over its issue", withMain(7), ""},
		{"main red over another issue", withMain(8), aiv1.PromptSuperseded},
		{"main red, no issue", withMain(0), ""},
		{"question of the current run", question(3), ""},
		{"question of an older run", question(2), aiv1.PromptSuperseded},
		{"question without its run", question(0), ""},
		{"budget", aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptBudget}}, ""},
		{"open issue", issuePrompt(aiv1.PromptAssessment, 1), ""},
		{"merged issue, still listed", issuePrompt(aiv1.PromptAssessment, 2), aiv1.PromptSuperseded},
		{"closed issue", issuePrompt(aiv1.PromptAssessment, 5), aiv1.PromptSuperseded},
		{"issue not listed, open", issuePrompt(aiv1.PromptAssessment, 6), ""},
		{"revisit of a closed issue's decision", issuePrompt(aiv1.PromptRevisit, 5), ""},
	} {
		asked = nil
		state, ok := promptResolved(now, mh, issues, tasks, st, closed)(tc.p)
		if state != tc.state || ok != (tc.state != "") {
			t.Errorf("%s: got %q %v, want %q", tc.name, state, ok, tc.state)
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
