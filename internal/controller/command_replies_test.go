package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

var commenter = gitlab.User{ID: 1, Username: "jonathan"}

// commandReplies are the bot's replies to comments on an issue.
func commandReplies(e *projectEnv, iid int64) []string {
	var out []string
	for _, n := range e.gl.IssueNotes[iid] {
		if n.Author.ID == e.gl.Bot.ID && strings.Contains(n.Body, "<!-- ai-operator:reply-to=") {
			out = append(out, n.Body)
		}
	}
	return out
}

// newRepliesEnv is a project on the fake's clock after its first poll,
// which starts watching.
func newRepliesEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.reconcile()
	return e
}

func TestCommandRepliesOnUnlabelledGeneratedIssue(t *testing.T) {
	e := newRepliesEnv(t)
	e.gl.AddIssue(5, "Docs", "ai-generated")
	n := e.gl.AddNote(5, commenter, "/agent please write the docs")
	e.reconcile()
	got := commandReplies(e, 5)
	if len(got) != 1 || !strings.Contains(got[0], "#5 has no `agent::ready` label. Add it to hand #5 to the agent.") ||
		!strings.Contains(got[0], replyToMarker(n.ID)) {
		t.Fatalf("want one reply naming the label, got %q", got)
	}
	if len(e.tasks()) != 0 {
		t.Fatalf("the comment must start nothing: %v", keys(e.tasks()))
	}
	// The next polls do not answer it again.
	e.reconcile()
	e.reconcile()
	if got := commandReplies(e, 5); len(got) != 1 {
		t.Fatalf("answered again: %q", got)
	}
	if cr := e.project().Status.CommandReplies; cr == nil || len(cr.Replied) != 1 || cr.Replied[0] != n.ID {
		t.Fatalf("the reply must be recorded: %+v", cr)
	}
}

func TestCommandRepliesOnFlakyIssue(t *testing.T) {
	e := newRepliesEnv(t)
	e.gl.AddIssue(6, "🔁 Flaky CI job: `test`", "ai-generated", "ci::flaky")
	e.gl.AddNote(6, commenter, "/pilot why is this flaky?")
	routing := e.gl.AddNote(6, commenter, "/agent use claude opus")
	e.gl.AddNote(6, commenter, "Just a remark, no /agent command.")
	e.reconcile()
	got := commandReplies(e, 6)
	if len(got) != 2 || !strings.Contains(got[0], "Add it to hand #6 to the agent") ||
		!strings.Contains(got[1], "#6 has no `agent::ready` label. This is applied once it has.") || !strings.Contains(got[1], replyToMarker(routing.ID)) {
		t.Fatalf("want a reply to the pilot comment and one to the routing command, got %q", got)
	}
}

func TestCommandRepliesLeaveLabelledAndOldComments(t *testing.T) {
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.gl.AddIssue(5, "Docs", "ai-generated")
	e.gl.AddNote(5, commenter, "/agent from before the operator watched")
	e.gl.Clock = e.gl.Clock.Add(time.Hour)
	e.reconcile() // starts watching
	e.gl.AddIssue(7, "API", "ai-generated", "agent::ready")
	e.gl.AddNote(7, commenter, "/agent use the existing logger")
	e.gl.AddNote(5, e.gl.Bot, "/agent the bot's own comment")
	e.reconcile()
	if got := append(commandReplies(e, 5), commandReplies(e, 7)...); len(got) != 0 {
		t.Fatalf("old, own and labelled issues' comments must get no reply: %q", got)
	}
	if e.implTaskOf(7) == nil {
		t.Fatal("the labelled issue is handled as before")
	}
}

func TestCommandRepliesSurviveALostStatus(t *testing.T) {
	e := newRepliesEnv(t)
	e.gl.AddIssue(5, "Docs", "ai-generated")
	e.gl.AddNote(5, commenter, "/agent go")
	e.reconcile()
	if got := commandReplies(e, 5); len(got) != 1 {
		t.Fatalf("want one reply, got %q", got)
	}
	// The status lost the replies and an older window: the marker in the
	// reply keeps the comment from being answered twice.
	p := e.project()
	orig := p.DeepCopy()
	p.Status.CommandReplies = &aiv1.CommandRepliesStatus{Since: &metav1.Time{Time: e.gl.Clock.Add(-time.Minute)}}
	if err := e.c.Status().Patch(e.ctx, &p, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := commandReplies(e, 5); len(got) != 1 {
		t.Fatalf("answered again after the status was lost: %q", got)
	}
}

func TestCommandRepliesKeepTheWindowOnErrors(t *testing.T) {
	e := newRepliesEnv(t)
	e.gl.AddIssue(5, "Docs", "ai-generated")
	e.gl.AddNote(5, commenter, "/agent go")
	since := e.project().Status.CommandReplies.Since
	e.gl.ListNoteErrors = map[int64]error{5: &gitlab.Error{StatusCode: 502}}
	e.reconcile() // the sync does not fail
	if cr := e.project().Status.CommandReplies; !cr.Since.Equal(since) || len(commandReplies(e, 5)) != 0 {
		t.Fatalf("a transient error must keep the window: %+v", cr)
	}
	e.gl.ListNoteErrors = nil
	e.gl.Clock = e.gl.Clock.Add(time.Hour) // beyond the clock skew
	e.reconcile()
	if got := commandReplies(e, 5); len(got) != 1 {
		t.Fatalf("the next poll answers it: %q", got)
	}
}

func TestCommandRepliesOnAutopilotItemWithoutLabel(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	// #2 waits for #1; a human wrote it, so only the milestone has it.
	e.gl.SetLabels(2)
	e.gl.AddNote(2, commenter, "/agent start already")
	e.gl.AddNote(tracking, commenter, "/agent what now?")
	e.reconcile()
	if got := commandReplies(e, 2); len(got) != 1 || !strings.Contains(got[0], "#2 has no `agent::ready` label") {
		t.Fatalf("want one reply on the autopilot item, got %q", got)
	}
	if got := commandReplies(e, tracking); len(got) != 0 {
		t.Fatalf("the tracking issue takes its own commands: %q", got)
	}
}

func TestCommandRepliesOnWebhookTriggeredSync(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.gl.Clock = clk.Now()
	e.r.Deliveries.Record(shop)
	e.wantPolls(gl, clk, F, L) // a delivery syncs fully, the next poll is light
	clk.Step(time.Minute)
	e.gl.Clock = clk.Now()
	e.gl.AddIssue(5, "Docs", "ai-generated")
	e.gl.AddNote(5, commenter, "/agent go")
	e.r.Deliveries.Record(shop) // the note's delivery
	if !e.poll(gl) {
		t.Fatal("a delivery must sync fully")
	}
	if got := commandReplies(e, 5); len(got) != 1 {
		t.Fatalf("the sync the delivery started must answer it: %q", got)
	}
}
