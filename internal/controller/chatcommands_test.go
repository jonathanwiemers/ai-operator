package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestApplyChatCommands(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-42", Namespace: ns},
		Spec: aiv1.AgentTaskSpec{
			ProjectRef: "shop", Type: aiv1.TaskTypeImplement,
			Issue: &aiv1.IssueRef{IID: 42},
			Source: aiv1.SourceState{
				ChatMessages: []aiv1.ChatMessage{
					{ID: 10, Kind: "command", Text: `{"cmd":"retry"}`, Author: "ada"},
					{ID: 11, Kind: "command", Text: `{"cmd":"retry"}`, Author: "ada"},
				},
			},
		},
	}
	spec := testProject().Spec.WithDefaults()

	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || !run {
		t.Fatalf("retry: run=%v err=%v", run, err)
	}
	if task.Spec.Source.LastChatCommandID != 11 {
		t.Fatalf("LastChatCommandID = %d", task.Spec.Source.LastChatCommandID)
	}
	if len(task.Spec.Source.ChatAnswers) != 2 {
		t.Fatalf("answers = %+v", task.Spec.Source.ChatAnswers)
	}
	// Caller bumps RunRequest once for the whole sync.
	before := task.Spec.Source.LastChatCommandID
	answers := len(task.Spec.Source.ChatAnswers)
	run, err = e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || run {
		t.Fatalf("second sync must be idempotent: run=%v err=%v", run, err)
	}
	if task.Spec.Source.LastChatCommandID != before || len(task.Spec.Source.ChatAnswers) != answers {
		t.Fatalf("replayed: mark=%d answers=%d", task.Spec.Source.LastChatCommandID, len(task.Spec.Source.ChatAnswers))
	}
}

func TestApplyChatCommandsUse(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working", "agent-model::cursor/auto")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Source: aiv1.SourceState{
			ChatMessages: []aiv1.ChatMessage{
				{ID: 5, Kind: "command", Text: `{"cmd":"use","engine":"claude","model":"opus"}`, Author: "bob"},
			},
		}},
	}
	spec := testProject().Spec.WithDefaults()
	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || run {
		t.Fatalf("use starts no run: run=%v err=%v", run, err)
	}
	if !slices.Contains(is.Labels, "agent-model::claude/opus") {
		t.Fatalf("labels = %v", is.Labels)
	}
	if n := len(task.Spec.Source.ChatAnswers); n != 1 || !strings.Contains(task.Spec.Source.ChatAnswers[0].Text, "claude/opus") {
		t.Fatalf("answer = %+v", task.Spec.Source.ChatAnswers)
	}
	if task.Spec.Source.LastChatCommandID != 5 {
		t.Fatalf("mark = %d", task.Spec.Source.LastChatCommandID)
	}
}

func TestApplyChatCommandsReviewWithoutHead(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Source: aiv1.SourceState{
			ChatMessages: []aiv1.ChatMessage{
				{ID: 7, Kind: "command", Text: `{"cmd":"review"}`, Author: "ada"},
			},
		}},
	}
	spec := testProject().Spec.WithDefaults()
	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, &gitlab.MergeRequest{SHA: "abc"})
	if err != nil || run {
		t.Fatalf("review without pushed head: run=%v err=%v", run, err)
	}
	if got := task.Spec.Source.ChatAnswers[0].Text; got != "There is no pushed head of the agent to review." {
		t.Fatalf("answer = %q", got)
	}
	if task.Spec.Source.ReviewFor != "" {
		t.Fatalf("ReviewFor = %q", task.Spec.Source.ReviewFor)
	}
}

func TestAutopilotChatCommandsPauseResumeSkip(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	st := e.tasks()[specTaskName]
	notesBefore := len(e.gl.IssueNotes[e.autopilot().TrackingIssueIID])

	st.Spec.Source.ChatMessages = []aiv1.ChatMessage{
		{ID: 101, Kind: "command", Text: `{"cmd":"pause"}`, Author: "ada@x", AuthorName: "Ada"},
		{ID: 102, Kind: "command", Text: `{"cmd":"resume"}`, Author: "ada@x", AuthorName: "Ada"},
		{ID: 103, Kind: "command", Text: `{"cmd":"skip","issue":2}`, Author: "ada@x"},
		{ID: 104, Kind: "command", Text: `{"cmd":"skip","issue":99}`, Author: "ada@x"},
	}
	if err := e.c.Update(e.ctx, &st); err != nil {
		t.Fatal(err)
	}
	e.reconcile()

	st = e.tasks()[specTaskName]
	if st.Spec.Source.LastChatCommandID != 104 {
		t.Fatalf("mark = %d", st.Spec.Source.LastChatCommandID)
	}
	if n := len(st.Spec.Source.ChatAnswers); n != 4 {
		t.Fatalf("answers = %+v", st.Spec.Source.ChatAnswers)
	}
	got := make([]string, len(st.Spec.Source.ChatAnswers))
	for i, a := range st.Spec.Source.ChatAnswers {
		got[i] = a.Text
	}
	want := []string{"Autopilot paused.", "Autopilot resumed.", "#2 skipped.", "`skip` needs a planned issue that is not merged yet, e.g. `/agent skip #12`."}
	if !slices.Equal(got, want) {
		t.Fatalf("answers = %q", got)
	}
	if e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("phase after resume = %s", e.autopilot().Phase)
	}
	if it := e.autopilot().Items; !slices.ContainsFunc(it, func(x aiv1.AutopilotItem) bool {
		return x.IID == 2 && x.State == aiv1.AutopilotItemSkipped
	}) {
		t.Fatalf("items = %+v", it)
	}
	if n := len(e.gl.IssueNotes[e.autopilot().TrackingIssueIID]); n != notesBefore {
		t.Fatalf("Desk path must not comment on GitLab: notes %d → %d", notesBefore, n)
	}

	answers := len(st.Spec.Source.ChatAnswers)
	mark := st.Spec.Source.LastChatCommandID
	e.reconcile()
	st = e.tasks()[specTaskName]
	if st.Spec.Source.LastChatCommandID != mark || len(st.Spec.Source.ChatAnswers) != answers {
		t.Fatalf("second sync replayed: mark=%d answers=%d", st.Spec.Source.LastChatCommandID, len(st.Spec.Source.ChatAnswers))
	}
}

func TestPilotChatCommandsAcceptReject(t *testing.T) {
	e := newProposalEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects alice@example.com"
	e.reconcile()
	pilot := e.pilot()
	if !slices.Equal(pilot.Spec.OwnerSubjects, []string{"alice@example.com"}) {
		t.Fatalf("OwnerSubjects = %v", pilot.Spec.OwnerSubjects)
	}
	// Proposals posted before prompts are accepted in the Desk until they
	// expire.
	e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if len(e.pilot().Status.Pilot.Proposals) != 1 {
		t.Fatalf("expected open proposal: %+v", e.pilot().Status.Pilot)
	}
	before := e.toolEffect()

	pilot = e.pilot()
	pilot.Spec.Source.ChatMessages = []aiv1.ChatMessage{
		{ID: 201, Kind: "command", Text: `{"cmd":"accept","proposal":1}`, Author: "mallory@x", AuthorName: "Mallory"},
	}
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	pilot = e.pilot()
	if len(pilot.Status.Pilot.Proposals) != 1 {
		t.Fatalf("non-owner must not change proposals: %+v", pilot.Status.Pilot)
	}
	if n := len(pilot.Spec.Source.ChatAnswers); n != 1 ||
		pilot.Spec.Source.ChatAnswers[0].Text != "Only the milestone's owners may answer proposals." {
		t.Fatalf("non-owner answer = %+v", pilot.Spec.Source.ChatAnswers)
	}
	if after := e.toolEffect(); after != before {
		t.Fatalf("non-owner ran tools: %+v → %+v", before, after)
	}

	pilot.Spec.Source.ChatMessages = append(pilot.Spec.Source.ChatMessages, aiv1.ChatMessage{
		ID: 202, Kind: "command", Text: `{"cmd":"accept","proposal":1}`, Author: "alice@example.com", AuthorName: "Alice",
	})
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	pilot = e.pilot()
	if len(pilot.Status.Pilot.Proposals) != 0 {
		t.Fatalf("owner accept must drop the open proposal: %+v", pilot.Status.Pilot)
	}
	if after := e.toolEffect(); after.runs != before.runs+1 {
		t.Fatalf("owner accept must run the tool: %+v → %+v", before, after)
	}
	var acceptAns string
	for _, a := range pilot.Spec.Source.ChatAnswers {
		if a.MessageID == 202 {
			acceptAns = a.Text
		}
	}
	if !strings.Contains(acceptAns, "Proposal P1 accepted") {
		t.Fatalf("accept answer = %q", acceptAns)
	}

	e.legacyProposal(2, "retry again", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	pilot = e.pilot()
	rejectID := int64(203)
	pilot.Spec.Source.ChatMessages = append(pilot.Spec.Source.ChatMessages, aiv1.ChatMessage{
		ID: rejectID, Kind: "command", Text: `{"cmd":"reject","proposal":2}`, Author: "alice@example.com",
	})
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	runs := e.toolEffect().runs
	e.reconcile()
	pilot = e.pilot()
	if len(pilot.Status.Pilot.Proposals) != 0 {
		t.Fatalf("reject must drop the proposal: %+v", pilot.Status.Pilot)
	}
	if e.toolEffect().runs != runs {
		t.Fatalf("reject must not run tools")
	}
	var rejectAns string
	for _, a := range pilot.Spec.Source.ChatAnswers {
		if a.MessageID == rejectID {
			rejectAns = a.Text
		}
	}
	if !strings.Contains(rejectAns, "dropped") {
		t.Fatalf("reject answer = %q", rejectAns)
	}
}

// Two proposal answers in one sync: each one's status patch replaces the
// task, and neither answer may get lost with it.
func TestPilotChatCommandsBatchKeepsAnswers(t *testing.T) {
	e := newProposalEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects alice@example.com"
	e.reconcile()
	e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.legacyProposal(2, "retry #1 again", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if n := len(e.pilot().Status.Pilot.Proposals); n != 2 {
		t.Fatalf("open proposals = %d, want 2", n)
	}

	pilot := e.pilot()
	pilot.Spec.Source.ChatMessages = []aiv1.ChatMessage{
		{ID: 301, Kind: "command", Text: `{"cmd":"reject","proposal":1}`, Author: "alice@example.com"},
		{ID: 302, Kind: "command", Text: `{"cmd":"reject","proposal":2}`, Author: "alice@example.com"},
	}
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	pilot = e.pilot()
	got := map[int64]string{}
	for _, a := range pilot.Spec.Source.ChatAnswers {
		got[a.MessageID] = a.Text
	}
	if !strings.Contains(got[301], "P1 dropped") || !strings.Contains(got[302], "P2 dropped") {
		t.Fatalf("answers = %v, want both proposals dropped", got)
	}
	if pilot.Spec.Source.LastChatCommandID != 302 {
		t.Fatalf("LastChatCommandID = %d, want 302", pilot.Spec.Source.LastChatCommandID)
	}
}

// Verbs sent to a milestone whose autopilot was switched off are answered,
// not left on the task.
func TestAutopilotChatCommandsAnsweredWhenOff(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.gl.Milestones[0].Description = "/agent spec\nBuild a new checkout."
	e.reconcile()
	st := e.tasks()[specTaskName]
	if st.Spec.Autopilot {
		t.Fatal("autopilot still on")
	}
	st.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 111, Kind: "command", Text: `{"cmd":"pause"}`, Author: "ada@x"}}
	if err := e.c.Update(e.ctx, &st); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	st = e.tasks()[specTaskName]
	if st.Spec.Source.LastChatCommandID != 111 || len(st.Spec.Source.ChatAnswers) != 1 ||
		st.Spec.Source.ChatAnswers[0].Text != "The autopilot is off for this milestone." {
		t.Fatalf("mark %d, answers %+v", st.Spec.Source.LastChatCommandID, st.Spec.Source.ChatAnswers)
	}
}

func TestApplyChatCommandsBudget(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Source: aiv1.SourceState{
			ChatMessages: []aiv1.ChatMessage{
				{ID: 5, Kind: "command", Text: `{"cmd":"budget","usd":"40"}`, Author: "bob"},
			},
		}},
	}
	spec := testProject().Spec.WithDefaults()
	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || run || task.Spec.BudgetUSD != "40.0000" {
		t.Fatalf("budget sets the issue's budget and starts no run: run=%v err=%v budget=%q", run, err, task.Spec.BudgetUSD)
	}
	if n := len(task.Spec.Source.ChatAnswers); n != 1 || !strings.Contains(task.Spec.Source.ChatAnswers[0].Text, "$40.00") {
		t.Fatalf("answer = %+v", task.Spec.Source.ChatAnswers)
	}
	if !chatCommandAllowed(*task, chatcmd.Command{Cmd: "budget", USD: "40"}) {
		t.Fatal("budget is delivered onto implement tasks")
	}
}
