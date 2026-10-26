package controller

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const ownersOnly = "Only the owners of this milestone can give `/agent` commands here (`workflow.commandsFrom: owners`)."

func commandsFromOwners(p *aiv1.AgentProject) { p.Spec.Workflow.CommandsFrom = aiv1.CommandsFromOwners }

// newOwnersEnv is a planned milestone owned by alice, with commandsFrom:
// owners, and its issue #42 in review with merge request !1.
func newOwnersEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.patchProject(commandsFromOwners)
	e.gl.Users = []gitlab.User{alice, mallory}
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

const issueTaskName = "shop-42-add-paypal-button"

func countIn(notes []gitlab.Note, s string) int {
	n := 0
	for _, note := range notes {
		n += strings.Count(note.Body, s)
	}
	return n
}

func TestCommandsFromOwnersOnIssuesAndMergeRequests(t *testing.T) {
	e := newOwnersEnv(t)
	before := e.tasks()[issueTaskName].Spec
	e.gl.AddNote(42, mallory, "/agent please also handle refunds")
	e.reconcile()
	// Only what any comment moves: when the notes were read.
	if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != before.RunRequest || task.Spec.Source.LastCommandNoteID != before.Source.LastCommandNoteID ||
		task.Spec.Source.LastRoutingNoteID != before.Source.LastRoutingNoteID || task.Spec.Source.ReviewFor != before.Source.ReviewFor {
		t.Fatalf("a non-owner's command changes nothing: %+v, was %+v", task.Spec.Source, before.Source)
	}
	if got := countIn(e.gl.IssueNotes[42], ownersOnly); got != 1 || !slices.Equal(e.gl.Labels(42), []string{"agent::review"}) {
		t.Fatalf("expected one reply and no run, got %d replies, labels %v", got, e.gl.Labels(42))
	}
	if _, ok := e.tasks()[specTaskName].Status.Pilot.Rejected["8"]; !ok {
		t.Fatalf("the reply is recorded on the milestone's spec task: %+v", e.tasks()[specTaskName].Status.Pilot)
	}

	// Within a day: no run, no label, no reply, also on the merge request, as
	// "/agent use", or from someone who took an owner's username.
	e.gl.AddMRNote(1, mallory, "/agent review")
	e.gl.AddNote(42, mallory, "/agent use claude opus")
	e.gl.AddNote(42, gitlab.User{ID: 9, Username: "alice"}, "/agent go on")
	e.reconcile()
	if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != before.RunRequest || task.Spec.Source.LastCommandNoteID != before.Source.LastCommandNoteID {
		t.Fatalf("non-owners' commands start nothing: %+v", task.Spec)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("a non-owner's /agent use sets no label: %v", labels)
	}
	if got := countIn(e.gl.MRNotes[1], ownersOnly); got != 0 {
		t.Fatalf("one reply a day per user, got %d on the MR", got)
	}
	if got := countIn(e.gl.IssueNotes[42], ownersOnly); got != 2 {
		t.Fatalf("the other user is told once too, got %d", got)
	}

	// A day later the old commands are not answered again; a new one is.
	e.gl.Clock = e.gl.Clock.Add(25 * time.Hour)
	e.gl.AddNote(42, alice, "thanks")
	e.reconcile()
	if got := countIn(e.gl.IssueNotes[42], ownersOnly); got != 2 {
		t.Fatalf("handled commands are not answered again, got %d", got)
	}
	e.gl.AddMRNote(1, mallory, "/agent now?")
	e.reconcile()
	if got := countIn(e.gl.MRNotes[1], ownersOnly); got != 1 {
		t.Fatalf("a day later the user is told again, where they wrote, got %d", got)
	}

	// The owner's command works as before; the bot's do not count.
	e.gl.AddNote(42, e.gl.Bot, "/agent I am the bot")
	e.reconcile()
	if got := e.tasks()[issueTaskName].Spec.RunRequest; got != before.RunRequest {
		t.Fatalf("the bot's comment is no command, run request %d", got)
	}
	e.gl.AddMRNote(1, alice, "/agent use claude opus\nand handle refunds")
	e.reconcile()
	if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != before.RunRequest+1 || !slices.Contains(e.gl.Labels(42), "agent-model::claude/opus") ||
		!slices.Contains(e.gl.Labels(42), "agent::working") {
		t.Fatalf("an owner's command runs and routes: %+v %v", task.Spec, e.gl.Labels(42))
	}
}

func TestCommandsFromOwnersWithoutOwners(t *testing.T) {
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.patchProject(commandsFromOwners)
	e.gl.AddIssue(42, "Add PayPal button", "agent::review")
	e.reconcile()
	e.gl.AddNote(42, mallory, "/agent go")
	e.reconcile()
	task := e.tasks()[issueTaskName]
	if task.Spec.RunRequest != 0 || !strings.Contains(lastNote(e, 42), "add a line `/agent owners @…`") {
		t.Fatalf("without owners nobody's command counts, and the reply says so: %d %q", task.Spec.RunRequest, lastNote(e, 42))
	}
	if _, ok := task.Status.Pilot.Rejected["8"]; !ok {
		t.Fatalf("outside a milestone the reply is recorded on the task: %+v", task.Status.Pilot)
	}

	// notify.users own the issues without a milestone.
	e.gl.Users = []gitlab.User{alice}
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Notify.Users = []string{"alice"} })
	e.gl.AddNote(42, alice, "/agent go")
	e.reconcile()
	if got := e.tasks()[issueTaskName].Spec.RunRequest; got != 1 {
		t.Fatalf("notify.users' command counts, run request %d", got)
	}
}

func TestCommandsFromMembersLooksUpNobody(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.FindUserError = errors.New("no lookups expected")
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n/agent owners @alice\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(42, "Add PayPal button", "agent::review").Milestone = &m
	e.reconcile()
	e.gl.AddNote(42, mallory, "/agent please also handle refunds")
	e.reconcile()
	if got := e.tasks()[issueTaskName].Spec.RunRequest; got != 1 || countIn(e.gl.IssueNotes[42], "commandsFrom") != 0 {
		t.Fatalf("with members anyone's command counts: run request %d, notes %+v", got, e.gl.IssueNotes[42])
	}
}

func TestCommandsFromOwnersOnTheTrackingIssue(t *testing.T) {
	e := newPilotEnv(t)
	e.patchProject(commandsFromOwners)
	tracking := e.autopilot().TrackingIssueIID
	before := *e.autopilot()
	e.gl.AddNote(tracking, mallory, "/agent pause")
	e.gl.AddNote(tracking, mallory, "/agent skip #1")
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != before.Phase || !slices.EqualFunc(ap.Items, before.Items, func(a, b aiv1.AutopilotItem) bool { return a.IID == b.IID && a.State == b.State }) {
		t.Fatalf("a non-owner's pause and skip change nothing: %+v, was %+v", ap, before)
	}
	if got := countIn(e.gl.IssueNotes[tracking], ownersOnly); got != 1 {
		t.Fatalf("one reply a day, got %d", got)
	}

	e.gl.AddNote(tracking, alice, "/agent pause")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "@alice") {
		t.Fatalf("an owner's pause pauses: %+v", ap)
	}
}

func TestCommandsFromOwnersOnRevisits(t *testing.T) {
	e := newRevisitEnv(t, "/agent spec\n/agent owners @alice\nBuild it.")
	e.patchProject(commandsFromOwners)
	e.gl.Users = []gitlab.User{alice, mallory}
	revisits := func() int {
		n := 0
		for _, is := range e.gl.Issues {
			if strings.HasPrefix(is.Title, "Revisit D1-1") {
				n++
			}
		}
		return n
	}
	e.gl.AddNote(1, mallory, "/agent revisit D1-1 use gRPC")
	e.reconcile()
	e.gl.AddNote(1, mallory, "/agent revisit D1-1 use gRPC, please")
	e.reconcile()
	if revisits() != 0 || countIn(e.gl.IssueNotes[1], ownersOnly) != 1 {
		t.Fatalf("a non-owner's revisit creates nothing and is answered once: %d follow-ups, notes %+v", revisits(), e.gl.IssueNotes[1])
	}
	e.gl.AddNote(1, alice, "/agent revisit D1-1 use gRPC")
	e.reconcile()
	if revisits() != 1 {
		t.Fatalf("an owner's revisit creates the follow-up")
	}
}
