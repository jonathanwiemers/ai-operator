package controller

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const pilotTaskName = "shop-pilot-m3-checkout-v2"

var (
	alice   = gitlab.User{ID: 7, Username: "alice"}
	mallory = gitlab.User{ID: 8, Username: "mallory"}
)

// newPilotEnv is an autopilot milestone owned by alice, with its tracking
// issue and its pilot.
func newPilotEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	e.reconcile()
	pilot, ok := e.tasks()[pilotTaskName]
	if !ok || pilot.Spec.Type != aiv1.TaskTypeCoordinate || pilot.Spec.Issue == nil || pilot.Spec.Issue.IID != e.autopilot().TrackingIssueIID {
		t.Fatalf("an autopilot milestone gets a pilot on its tracking issue: %+v", pilot.Spec)
	}
	return e
}

func (e *projectEnv) pilot() aiv1.AgentTask {
	e.t.Helper()
	return e.tasks()[pilotTaskName]
}

func botComments(e *projectEnv, iid int64) []string {
	var out []string
	for _, n := range e.gl.IssueNotes[iid] {
		if n.Author.ID == e.gl.Bot.ID {
			out = append(out, n.Body)
		}
	}
	return out
}

func TestPilotGetsTheOwnersQuestionOnce(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	// Neither of these is addressed to the pilot.
	for _, body := range []string{"thanks @ai-bot, why?", "> /pilot why?", "`/pilot why?`", "    /pilot why?", "/agent pause"} {
		e.gl.AddNote(tracking, alice, body)
	}
	e.gl.AddNote(tracking, e.gl.Bot, "/pilot I am the bot")
	n := e.gl.AddNote(tracking, alice, "/pilot why are we paused?")
	e.reconcile()
	e.reconcile()
	// The question is request 1; "/agent pause" wakes the pilot with request 2.
	pilot := e.pilot()
	msgs := pilot.Spec.Source.PilotMessages
	if len(msgs) != 1 || msgs[0].NoteID != n.ID || msgs[0].Body != "why are we paused?" || msgs[0].IssueIID != tracking ||
		msgs[0].Author != "alice" || msgs[0].Request != 1 || pilot.Spec.RunRequest != 2 {
		t.Fatalf("expected the one question, once, with one run request: %+v (request %d)", msgs, pilot.Spec.RunRequest)
	}
	if c := botComments(e, tracking); strings.Contains(strings.Join(c, "\n"), "🧭") {
		t.Fatalf("the bot's own comment is not answered: %q", c)
	}

	// Once answered, a mention of the bot is the next message; the answered
	// one goes.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = 2 })
	e.gl.AddNote(tracking, alice, "@ai-bot and now?")
	e.reconcile()
	msgs = e.pilot().Spec.Source.PilotMessages
	if len(msgs) != 1 || msgs[0].Body != "and now?" || msgs[0].Request != 3 {
		t.Fatalf("expected only the new message: %+v", msgs)
	}
}

func TestPilotStartsBeforeThePlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	if pilot, ok := e.tasks()[pilotTaskName]; !ok || pilot.Spec.Issue == nil || pilot.Spec.Issue.IID != tracking {
		t.Fatalf("the pilot starts on the tracking issue before the plan: %+v", pilot.Spec)
	}
	n := e.gl.AddNote(tracking, alice, "/pilot what will be planned?")
	e.reconcile()
	msgs := e.pilot().Spec.Source.PilotMessages
	if len(msgs) != 1 || msgs[0].NoteID != n.ID || len(e.tasks()[specTaskName].Status.Plan) != 0 {
		t.Fatalf("the pilot gets the question before the plan: %+v", msgs)
	}
}

func TestPilotIgnoresCommentsFromBeforeIt(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile() // the tracking issue
	tracking := e.autopilot().TrackingIssueIID
	// An operator from before the pilot: no pilot status yet.
	e.gl.AddNote(tracking, alice, "@ai-bot an old question")
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = nil })
	e.reconcile()
	e.reconcile()
	if msgs := e.pilot().Spec.Source.PilotMessages; len(msgs) != 0 {
		t.Fatalf("a comment from before the pilot listened is history: %+v", msgs)
	}
}

func TestPilotMentionCanBeTurnedOff(t *testing.T) {
	e := newPilotEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { f := false; p.Spec.Pilot.Mention = &f })
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "@ai-bot why?")
	e.reconcile()
	if msgs := e.pilot().Spec.Source.PilotMessages; len(msgs) != 0 {
		t.Fatalf("pilot.mention: false turns the mention off: %+v", msgs)
	}
}

func TestPilotRejectsOthersPerComment(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	const rejection = "Only the owners of this milestone can instruct me."
	first := e.gl.AddNote(tracking, mallory, "/pilot skip #1")
	// Same username as an owner, another user: still not an owner.
	e.gl.AddNote(tracking, gitlab.User{ID: 9, Username: "alice"}, "/pilot skip #1")
	e.reconcile()
	e.gl.AddNote(tracking, mallory, "/pilot skip #1, now")
	e.reconcile()
	e.reconcile()
	c := strings.Join(botComments(e, tracking), "\n")
	if got := strings.Count(c, rejection); got != 3 || !strings.Contains(c, replyToMarker(first.ID)) {
		t.Fatalf("each comment gets one rejection, got %d:\n%s", got, c)
	}
	if p := e.pilot(); len(p.Spec.Source.PilotMessages) != 0 || p.Spec.RunRequest != 0 {
		t.Fatalf("no owner, no message and no run: %+v", p.Spec)
	}
	// Up to the cap of replies a day per author; beyond it, nothing.
	for i := 2; i < maxRepliesPerDay+2; i++ {
		e.gl.AddNote(tracking, mallory, fmt.Sprintf("/pilot skip #1, %d", i))
	}
	e.reconcile()
	if got := strings.Count(strings.Join(botComments(e, tracking), "\n"), rejection); got != maxRepliesPerDay+1 {
		t.Fatalf("want %d rejections, the cap for mallory and one for the other user, got %d", maxRepliesPerDay+1, got)
	}
}

func TestPilotListensOnTheMilestonesIssues(t *testing.T) {
	e := newPilotEnv(t)
	e.gl.SetLabels(2, "agent::needs-input")
	e.reconcile() // the task of #2
	n := e.gl.AddNote(2, alice, "/pilot what does #2 wait for?")
	e.reconcile()
	if msgs := e.pilot().Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != n.ID || msgs[0].IssueIID != 2 {
		t.Fatalf("a question on an issue of the milestone goes to its pilot: %+v", msgs)
	}

	// An issue without a planned milestone has no pilot: everyone is told.
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Notify.Users = []string{"alice"} })
	e.gl.AddIssue(9, "Elsewhere", "agent::needs-input")
	e.reconcile()
	e.gl.AddNote(9, mallory, "/pilot hello?")
	e.gl.AddNote(9, alice, "/pilot hello?")
	e.reconcile()
	if c := botComments(e, 9); len(c) != 2 || !strings.Contains(c[0], "not in a planned milestone") || !strings.Contains(c[1], "not in a planned milestone") {
		t.Fatalf("expected a no-pilot answer per comment, got %q", c)
	}
}

func TestPilotFailedReplyDoesNotHoldUpTheOthers(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.NoteErrors = map[int64]error{tracking: errors.New("gitlab is down")}
	e.gl.AddNote(tracking, mallory, "/pilot skip #1")
	n := e.gl.AddNote(tracking, alice, "/pilot why?")
	e.reconcile()
	e.gl.NoteErrors = nil
	e.reconcile()
	if msgs := e.pilot().Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != n.ID {
		t.Fatalf("the owner's message is handed over although the rejection failed: %+v", msgs)
	}
	if c := botComments(e, tracking); strings.Contains(strings.Join(c, "\n"), "Only the owners") {
		t.Fatalf("a failed rejection is not posted again: %q", c)
	}
}

func TestPilotCoordinateNeedsOwners(t *testing.T) {
	e := newProjectEnv(t)
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(1, "API", "agent::needs-input").Milestone = &m
	e.reconcile()
	e.gl.AddNote(1, mallory, "/pilot coordinate")
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || !strings.Contains(strings.Join(botComments(e, 1), "\n"), "A pilot needs owners") {
		t.Fatalf("without owners nobody starts a pilot: %v %q", keys(e.tasks()), botComments(e, 1))
	}
}

func TestPilotCoordinateStartsAPilot(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n/agent owners @alice\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(1, "API", "agent::needs-input").Milestone = &m
	e.reconcile()
	e.gl.AddNote(1, alice, "/pilot why?")
	e.reconcile()
	if c := botComments(e, 1); len(c) != 1 || !strings.Contains(c[0], "`/pilot coordinate`") {
		t.Fatalf("without a pilot the answer says how to start one: %q", c)
	}
	e.gl.AddNote(1, alice, "/pilot coordinate")
	e.reconcile()
	pilot, ok := e.tasks()[pilotTaskName]
	if !ok || pilot.Spec.Issue == nil || pilot.Spec.Autopilot {
		t.Fatalf("/pilot coordinate creates the pilot: %+v", pilot.Spec)
	}
	tracking := pilot.Spec.Issue.IID
	if is := e.gl.Issues[tracking]; is.Title != "🧭 Pilot: Checkout v2" || is.Milestone == nil || is.Milestone.ID != 11 {
		t.Fatalf("the pilot gets a tracking issue in the milestone: %+v", is)
	}
	n := e.gl.AddNote(tracking, alice, "/pilot why are we here?")
	e.reconcile()
	e.reconcile()
	pilot = e.tasks()[pilotTaskName]
	// One run request for the message, one for #1, which needs input.
	if msgs := pilot.Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != n.ID || pilot.Spec.RunRequest != 2 {
		t.Fatalf("the pilot listens on its tracking issue: %+v", msgs)
	}
	e.gl.AddNote(1, alice, "/pilot coordinate")
	e.reconcile()
	if len(e.tasks()) != 3 || !strings.Contains(strings.Join(botComments(e, 1), "\n"), "listens on #") {
		t.Fatalf("a second coordinate starts no second pilot: %v", keys(e.tasks()))
	}
}

func (e *projectEnv) patchProject(mutate func(*aiv1.AgentProject)) {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	mutate(&p)
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

func TestPilotMovesToTheAutopilotsTrackingIssue(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n/agent owners @alice\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(1, "API", "agent::needs-input").Milestone = &m
	e.reconcile()
	e.gl.AddNote(1, alice, "/pilot coordinate")
	e.reconcile()
	own := e.tasks()[pilotTaskName].Spec.Issue.IID

	e.gl.Milestones[0].Description = "/agent autopilot\n/agent owners @alice\nBuild it."
	e.reconcile()
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = e.tasks()[specTaskName].Spec.RunRequest
		s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, Title: "API"}}
	})
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	pilot := e.tasks()[pilotTaskName]
	if tracking == 0 || tracking == own || pilot.Spec.Issue.IID != tracking || !pilot.Spec.Autopilot {
		t.Fatalf("the pilot follows the autopilot's tracking issue #%d: %+v", tracking, pilot.Spec)
	}
	if is := e.gl.Issues[own]; is.State != "closed" || !strings.Contains(strings.Join(botComments(e, own), "\n"), "moved to #") {
		t.Fatalf("the issue /pilot coordinate opened is closed with a pointer: %s %q", is.State, botComments(e, own))
	}
}

func TestPilotEndsWhenTheAutopilotIsDone(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent skip #1")
	e.gl.AddNote(tracking, alice, "/agent skip #2")
	e.reconcile()
	// After its retrospective (TestRetrospectiveBeforeThePilotGoes).
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{NoteID: 99}}
	})
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("a done autopilot ends its pilot: %v %+v", keys(e.tasks()), e.autopilot())
	}

	// Everyone is told there is no pilot, also for "coordinate", and no
	// pilot comes back.
	e.gl.AddNote(tracking, mallory, "/pilot hello?")
	e.gl.AddNote(tracking, alice, "/pilot how did it go?")
	e.gl.AddNote(tracking, alice, "/pilot coordinate")
	e.reconcile()
	e.reconcile()
	if c := strings.Join(botComments(e, tracking), "\n"); strings.Count(c, "autopilot of this milestone is done") != 3 || strings.Contains(c, "Only the owners") {
		t.Fatalf("expected a no-pilot answer per comment: %q", c)
	}
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("a done autopilot gets no pilot: %v", keys(e.tasks()))
	}

	// A re-plan continues the queue, and the pilot comes back.
	mc := e.gl.Milestones[0]
	docs := e.gl.AddIssue(10, "Docs", "ai-generated", "agent::draft")
	docs.Milestone, docs.Description = &mc, "Write it.\n<!-- ai-operator:key=docs -->"
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = append(s.Plan, aiv1.PlannedIssue{Key: "docs", IID: 10, Title: "Docs"})
	})
	e.reconcile()
	e.reconcile()
	if p, ok := e.tasks()[pilotTaskName]; !ok || e.autopilot().Phase != aiv1.AutopilotRunning || p.Spec.Issue == nil || p.Spec.Issue.IID != tracking {
		t.Fatalf("autopilot running again brings the pilot back: %v %+v", keys(e.tasks()), e.autopilot())
	}
}

func TestPilotCoordinateEndsWithItsIssue(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n/agent owners @alice\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(1, "API", "agent::needs-input").Milestone = &m
	e.reconcile()
	e.gl.AddNote(1, alice, "/pilot coordinate")
	e.reconcile()
	e.reconcile()
	pilot, ok := e.tasks()[pilotTaskName]
	if !ok {
		t.Fatalf("/pilot coordinate creates the pilot: %v", keys(e.tasks()))
	}
	e.gl.Issues[pilot.Spec.Issue.IID].State = "closed"
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("closing the pilot's issue ends the pilot: %v", keys(e.tasks()))
	}
}
