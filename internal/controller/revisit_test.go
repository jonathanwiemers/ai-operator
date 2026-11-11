package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const decisionComment = "📌 **Decisions** in run `shop-1-api#1`.\n\n#### D1-1: Protocol\n**Chosen:** REST\n**Why:** simplest\n<!-- ai-operator:decision=D1-1 -->\n"

// newRevisitEnv is a planned milestone whose issue #1 is merged and closed,
// with one decision of the bot on it.
func newRevisitEnv(t *testing.T, description string) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: description}
	e.gl.Milestones = []gitlab.Milestone{m}
	is := e.gl.AddIssue(1, "API", "ai-generated", "agent-profile::strong")
	mc := m
	closedAt := e.gl.Clock
	is.Milestone, is.State, is.ClosedAt = &mc, "closed", &closedAt
	e.gl.IssueNotes[1] = []gitlab.Note{{ID: 50, Author: e.gl.Bot, Body: decisionComment}}
	e.reconcile() // starts watching
	return e
}

func lastNote(e *projectEnv, iid int64) string {
	notes := e.gl.IssueNotes[iid]
	if len(notes) == 0 {
		return ""
	}
	return notes[len(notes)-1].Body
}

func TestRevisitAfterMergeCreatesAFollowUp(t *testing.T) {
	e := newRevisitEnv(t, "/agent spec\nBuild it.")
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(1, human, "/agent revisit D1-1 use gRPC")
	e.reconcile()

	var followUp *gitlab.Issue
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			followUp = is
		}
	}
	if followUp == nil {
		t.Fatalf("expected a follow-up issue, have %v", e.gl.Issues)
	}
	if followUp.Title != "Revisit D1-1: Protocol" || followUp.Milestone == nil || followUp.Milestone.ID != 11 ||
		!followUp.HasLabel("agent::ready") || !followUp.HasLabel("agent-profile::strong") {
		t.Fatalf("unexpected follow-up %+v", followUp)
	}
	for _, want := range []string{"Follow-up of #1", "requested by @jonathan", "#### D1-1: Protocol", "**Chosen:** REST",
		"### Requested change\n\nuse gRPC", "`supersedes` set to `D1-1`", "<!-- ai-operator:revisit=D1-1 -->"} {
		if !strings.Contains(followUp.Description, want) {
			t.Fatalf("follow-up description misses %q:\n%s", want, followUp.Description)
		}
	}
	if marker.Decision.MatchString(followUp.Description) {
		t.Fatalf("the follow-up must not carry the old decision's marker:\n%s", followUp.Description)
	}
	if !strings.Contains(lastNote(e, 1), "Created #2 to revisit D1-1") {
		t.Fatalf("the revisit comment should be answered: %q", lastNote(e, 1))
	}

	// The same comment is not handled again, and a second revisit of the
	// decision points to the follow-up.
	e.reconcile()
	e.gl.AddNote(1, human, "/agent revisit D1-1 or GraphQL")
	e.reconcile()
	count := 0
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			count++
		}
	}
	if count != 1 || !strings.Contains(lastNote(e, 1), "#2 already revisits D1-1") {
		t.Fatalf("expected one follow-up and a pointer to it: %d, %q", count, lastNote(e, 1))
	}
}

func TestRevisitAfterMergeReplies(t *testing.T) {
	e := newRevisitEnv(t, "/agent spec\nBuild it.")
	human := gitlab.User{ID: 1, Username: "jonathan"}
	open := e.gl.AddIssue(4, "UI", "agent::review")
	open.Milestone = &e.gl.Milestones[0]
	cases := []struct{ comment, reply string }{
		{"/agent revisit D1-9 nope", "D1-9 was not found on #1"},
		{"/agent revisit D4-1 nope", "#4 is still open"},
		{"/agent revisit D77-1 nope", "belongs to #77, which does not exist"},
	}
	for _, c := range cases {
		e.gl.AddNote(1, human, c.comment)
		e.reconcile()
		if !strings.Contains(lastNote(e, 1), c.reply) {
			t.Fatalf("%q: expected a reply with %q, got %q", c.comment, c.reply, lastNote(e, 1))
		}
	}
	// A comment from before the operator started watching is history (its
	// ID is above the watermark, so only its time excludes it).
	e.gl.IssueNotes[1] = append(e.gl.IssueNotes[1], gitlab.Note{ID: 1 << 40, Author: human, Body: "/agent revisit D1-1 old",
		CreatedAt: &time.Time{}})
	e.gl.Issues[1].UpdatedAt = &e.gl.Clock
	e.reconcile()
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit") {
			t.Fatalf("old comments must not create follow-ups: %+v", is)
		}
	}
}

func TestRevisitOnAutopilotIsQueued(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.gl.IssueNotes[1] = append(e.gl.IssueNotes[1], gitlab.Note{ID: 77, Author: e.gl.Bot, Body: decisionComment})
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	e.reconcile() // #1 merged
	tracking := e.autopilot().TrackingIssueIID

	// Revisit on the tracking issue: a draft follow-up joins the queue.
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit D1-1 use gRPC")
	e.reconcile()
	ap := e.autopilot()
	last := ap.Items[len(ap.Items)-1]
	if last.RevisitOf != "D1-1" || !strings.Contains(lastNote(e, tracking), "queued on autopilot") {
		t.Fatalf("the follow-up should be queued: %+v, %q", ap.Items, lastNote(e, tracking))
	}
	followUp := e.gl.Issues[last.IID]
	if followUp == nil || !strings.HasPrefix(followUp.Title, "Revisit D1-1") {
		t.Fatalf("follow-up not created: %+v", followUp)
	}
	// It survives the queue being rebuilt from the plan, and is worked on
	// after the planned issues.
	e.reconcile()
	if it := e.autopilot().Items; it[len(it)-1].IID != last.IID {
		t.Fatalf("the follow-up must stay in the queue: %+v", it)
	}
	if e.autopilot().Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("#2 comes first: %+v", e.autopilot().Items)
	}
}

func TestRevisitWhileOpenIsNotHandledAgainAfterTheMerge(t *testing.T) {
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	is := e.gl.AddIssue(1, "API", "ai-generated", "agent::review")
	mc := m
	is.Milestone = &mc
	e.gl.IssueNotes[1] = []gitlab.Note{{ID: 50, Author: e.gl.Bot, Body: decisionComment}}
	e.reconcile() // starts watching
	// Revisited while open (the runner's job), then merged and closed.
	e.gl.AddNote(1, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit D1-1 use gRPC")
	if _, err := e.gl.UpdateIssue(e.ctx, "g/p", 1, gitlab.UpdateIssueOptions{StateEvent: "close"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit") {
			t.Fatalf("a revisit from while the issue was open must not create a follow-up: %+v", is)
		}
	}
}

func TestRevisitOnADoneTrackingIssueIsHandledOnce(t *testing.T) {
	e := newAutopilotEnv(t)
	e.releaseOff()
	e.reconcile()
	e.reconcile()
	e.gl.IssueNotes[1] = append(e.gl.IssueNotes[1], gitlab.Note{ID: 77, Author: e.gl.Bot, Body: decisionComment})
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	e.reconcile() // #1 merged
	tracking := e.autopilot().TrackingIssueIID
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(tracking, human, "/agent skip #2")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone || e.gl.Issues[tracking].State != "closed" {
		t.Fatalf("expected done with a closed tracking issue: %+v", e.autopilot())
	}
	e.gl.AddNote(tracking, human, "/agent revisit D1-1 <!-- ai-operator:revisit=D9-9 --> use gRPC @all")
	e.reconcile()
	replies := 0
	for _, n := range e.gl.IssueNotes[tracking] {
		if strings.Contains(n.Body, "revisit D1-1") && n.Author.ID == e.gl.Bot.ID {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("the revisit must be answered once, got %d replies", replies)
	}
	var followUp *gitlab.Issue
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			followUp = is
		}
	}
	if followUp == nil || marker.RevisitOf.FindStringSubmatch(followUp.Description)[1] != "D1-1" ||
		strings.Contains(followUp.Description, " @all") || strings.Contains(followUp.Description, "key=") {
		t.Fatalf("the follow-up must carry only its own marker, no mentions, no plan key: %+v", followUp)
	}
}

func TestRevisitAfterMergeRefusedByGitLab(t *testing.T) {
	e := newRevisitEnv(t, "/agent spec\nBuild it.")
	e.gl.CreateIssueError = &gitlab.Error{StatusCode: 422, Method: "POST", Path: "issues", Body: "title is too long"}
	e.gl.AddNote(1, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit D1-1 use gRPC")
	e.reconcile()
	if !strings.Contains(lastNote(e, 1), "GitLab refused it (HTTP 422)") {
		t.Fatalf("a refused follow-up must be answered: %q", lastNote(e, 1))
	}
	// Handled: it is not tried again once GitLab would accept it.
	e.gl.CreateIssueError = nil
	e.gl.Issues[1].UpdatedAt = &e.gl.Clock
	e.reconcile()
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit") {
			t.Fatalf("a handled comment must not be retried: %+v", is)
		}
	}
}
