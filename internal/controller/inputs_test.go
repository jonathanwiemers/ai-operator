package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const (
	deskBase  = "https://desk.example"
	deskReply = "Nothing happens: this project takes instructions in the Desk: "
)

func inputs(in ...aiv1.InputSource) func(*aiv1.AgentProject) {
	return func(p *aiv1.AgentProject) { p.Spec.Workflow.Inputs = in }
}

// newInputsEnv is a planned milestone owned by alice and its issue #42 in
// review with merge request !1, in a project with the given inputs.
func newInputsEnv(t *testing.T, in ...aiv1.InputSource) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.r.DeskURL = deskBase
	e.patchProject(inputs(in...))
	e.gl.Users = []gitlab.User{alice}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n/agent owners @alice\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(42, "Add PayPal button", "agent::review").Milestone = &m
	e.reconcile()
	task := e.tasks()[issueTaskName]
	orig := task.DeepCopy()
	task.Status.Run.MergeRequestIID = 1
	if err := e.c.Status().Patch(e.ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.gl.MRs[1] = &gitlab.MergeRequest{IID: 1, State: "opened", SHA: "sha-head"}
	e.reconcile()
	return e
}

func TestDeskInputAnswersCommandsOnTheIssueAndMR(t *testing.T) {
	e := newInputsEnv(t, aiv1.InputDesk)
	before := e.tasks()[issueTaskName].Spec
	e.gl.AddNote(42, alice, "/agent please also handle refunds")
	e.gl.AddNote(42, alice, "/agent use claude opus")
	e.gl.AddNote(42, alice, "/pilot what is left?")
	e.gl.AddNote(42, alice, "Just a remark.")
	e.gl.AddMRNote(1, alice, "/agent review")
	e.reconcile()
	e.reconcile()
	task := e.tasks()[issueTaskName]
	if task.Spec.RunRequest != before.RunRequest || task.Spec.Source.ReviewFor != before.Source.ReviewFor || !slices.Equal(e.gl.Labels(42), []string{"agent::review"}) {
		t.Fatalf("with inputs [desk] a command changes nothing: %+v, labels %v", task.Spec, e.gl.Labels(42))
	}
	page := desklink.Task(deskBase, ns, "shop", issueTaskName)
	if got := countIn(e.gl.IssueNotes[42], deskReply+page); got != 3 {
		t.Fatalf("want one reply per command and pilot comment on the issue, got %d:\n%q", got, botComments(e, 42))
	}
	if got := countIn(e.gl.MRNotes[1], deskReply+page); got != 1 {
		t.Fatalf("want one reply on the MR, got %d", got)
	}
	if c := strings.Join(botComments(e, 42), "\n"); strings.Contains(c, "no pilot yet") {
		t.Fatalf("the pilot comment is not routed:\n%s", c)
	}

	// Re-polls do not reply again.
	e.reconcile()
	e.reconcile()
	if got := countIn(e.gl.IssueNotes[42], deskReply) + countIn(e.gl.MRNotes[1], deskReply); got != 4 {
		t.Fatalf("answered again: %d replies", got)
	}

	// An open prompt of the issue is where the reply points.
	e.patchStatus(issueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Prompts = []aiv1.Prompt{{ID: "Q1", Source: aiv1.PromptSource{Kind: aiv1.PromptNeedsInput, IssueIID: 42},
			Question: "Which provider?", AllowFreeText: true, State: aiv1.PromptOpen}}
	})
	e.gl.AddNote(42, alice, "/agent use Braintree")
	e.reconcile()
	if want := desklink.Prompt(deskBase, new(e.tasks()[issueTaskName]), "Q1"); countIn(e.gl.IssueNotes[42], deskReply+want) != 1 {
		t.Fatalf("want a reply with the prompt's link %s, got:\n%q", want, botComments(e, 42))
	}
	if got := e.tasks()[issueTaskName].Spec.RunRequest; got != before.RunRequest {
		t.Fatalf("still no run: %d", got)
	}
}

func TestGitLabAndDeskInputTakeCommands(t *testing.T) {
	e := newInputsEnv(t, aiv1.InputGitLab, aiv1.InputDesk)
	before := e.tasks()[issueTaskName].Spec
	e.gl.AddNote(42, alice, "/agent use claude opus\nand handle refunds")
	e.reconcile()
	if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != before.RunRequest+1 || !slices.Contains(e.gl.Labels(42), "agent-model::claude/opus") {
		t.Fatalf("with gitlab input the command runs and routes: %+v %v", task.Spec, e.gl.Labels(42))
	}
	if got := countIn(e.gl.IssueNotes[42], deskReply); got != 0 {
		t.Fatalf("no Desk reply with gitlab input, got %d", got)
	}
}

func TestDeskInputAnswersCommandsOnTheTrackingIssue(t *testing.T) {
	e := newPilotEnv(t)
	e.r.DeskURL = deskBase
	e.patchProject(inputs(aiv1.InputDesk))
	tracking := e.autopilot().TrackingIssueIID
	before := *e.autopilot()
	e.gl.AddNote(tracking, alice, "/agent pause")
	e.gl.AddNote(tracking, alice, "/pilot why are we paused?")
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != before.Phase {
		t.Fatalf("the pause is not taken: %+v", ap)
	}
	if p := e.pilot(); len(p.Spec.Source.PilotMessages) != 0 {
		t.Fatalf("the pilot gets no message: %+v", p.Spec.Source.PilotMessages)
	}
	page := desklink.Milestone(deskBase, ns, "shop", 3)
	if got := countIn(e.gl.IssueNotes[tracking], deskReply+page); got != 2 {
		t.Fatalf("want one reply per comment, got %d:\n%q", got, botComments(e, tracking))
	}
}

func TestDeskInputAnswersRevisits(t *testing.T) {
	e := newRevisitEnv(t, "/agent spec\nBuild it.")
	e.r.DeskURL = deskBase
	e.patchProject(inputs(aiv1.InputDesk))
	e.gl.AddNote(1, commenter, "/agent revisit D1-1 use gRPC")
	e.reconcile()
	e.reconcile()
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			t.Fatalf("no follow-up with inputs [desk]: %+v", is)
		}
	}
	if got := countIn(e.gl.IssueNotes[1], deskReply+desklink.Milestone(deskBase, ns, "shop", 3)); got != 1 {
		t.Fatalf("want one reply, got %d:\n%q", got, botComments(e, 1))
	}
}

func TestDeskInputAnswersCommandsOnUnlabelledIssues(t *testing.T) {
	e := newRepliesEnv(t)
	e.patchProject(inputs(aiv1.InputDesk))
	e.gl.AddIssue(5, "Docs", "ai-generated")
	e.gl.AddNote(5, commenter, "/agent please write the docs")
	e.reconcile()
	e.reconcile()
	got := commandReplies(e, 5)
	if len(got) != 1 || !strings.Contains(got[0], toDesk("")) || !strings.Contains(got[0], "To hand #5 to the agent, add the `agent::ready` label.") {
		t.Fatalf("want one Desk reply naming the label, got %q", got)
	}
	if len(e.tasks()) != 0 {
		t.Fatalf("the comment starts nothing: %v", keys(e.tasks()))
	}
}
