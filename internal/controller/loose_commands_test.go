package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// bob is the loose issues queue's owner in newLooseEnv (notify.users).
var bob = gitlab.User{ID: 9, Username: "bob"}

// newLooseCommandEnv is a queue owned by bob (GitLab) and alice@example.com
// (Desk), with its tracking issue.
func newLooseCommandEnv(t *testing.T) (*projectEnv, int64) {
	t.Helper()
	e := newLooseEnv(t, nil)
	e.gl.Users = []gitlab.User{alice, bob, mallory}
	tracking := e.queue().Status.Autopilot.TrackingIssueIID
	if tracking == 0 {
		t.Fatal("the queue has its tracking issue")
	}
	return e, tracking
}

// looseItems are the queue's items as "iid:state", in order.
func (e *projectEnv) looseItems() []string {
	e.t.Helper()
	var out []string
	for _, it := range e.queue().Status.Autopilot.Items {
		out = append(out, fmt.Sprintf("%d:%s", it.IID, it.State))
	}
	return out
}

func (e *projectEnv) wantItems(what string, want ...string) {
	e.t.Helper()
	if got := e.looseItems(); !slices.Equal(got, want) {
		e.t.Fatalf("%s: items %v, want %v", what, got, want)
	}
}

// replyTo is the bot's reply to note id on issue iid; it fails unless there
// is exactly one.
func (e *projectEnv) replyTo(iid int64, id int64) string {
	e.t.Helper()
	var got []string
	for _, b := range botComments(e, iid) {
		if strings.Contains(b, replyToMarker(id)) {
			got = append(got, b)
		}
	}
	if len(got) != 1 {
		e.t.Fatalf("note %d on #%d has %d replies, want 1: %q", id, iid, len(got), got)
	}
	return got[0]
}

// sayLoose comments body as who on the tracking issue, syncs and returns the
// bot's one reply to it.
func (e *projectEnv) sayLoose(tracking int64, who gitlab.User, body string) string {
	e.t.Helper()
	n := e.gl.AddNote(tracking, who, body)
	e.reconcile()
	return e.replyTo(tracking, n.ID)
}

func TestLooseTakeOneAndSeveral(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(350, "Flaky helm-lint", "ci::flaky", "ai-generated")
	e.gl.AddIssue(421, "Runner timeout")
	e.gl.AddIssue(46, "In work", "agent::working")
	e.gl.AddIssue(128, "Later")

	reply := e.sayLoose(tracking, bob, "/agent take #350 #421")
	if !strings.Contains(reply, "Taken into the loose issues queue: #350, #421.") || strings.Contains(reply, "Not taken") {
		t.Fatalf("one reply lists both: %q", reply)
	}
	e.wantItems("taken in order, the first starts", "350:Active", "421:Pending")
	for iid, pos := range map[int64]int{350: 1, 421: 2} {
		want := fmt.Sprintf("🛠️ Taken into the loose issues queue by @bob (position %d).", pos)
		if got := botComments(e, iid); len(got) != 1 || got[0] != want {
			t.Fatalf("#%d: notes %q, want %q", iid, got, want)
		}
	}
	it := e.queue().Status.Autopilot.Items[0]
	if it.TakenBy != "@bob" || it.TakenAt == nil {
		t.Fatalf("taken by and at: %+v", it)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "### Pending\n\n1. #421\n") || !strings.Contains(d, "- #350") {
		t.Fatalf("the tracking issue lists the pending issues:\n%s", d)
	}

	// An issue in work is active at once, beyond maxParallel; the next
	// pending one is second, counted at the take.
	e.sayLoose(tracking, bob, "/agent take 46, 128")
	e.wantItems("in work is active", "350:Active", "421:Pending", "46:Active", "128:Pending")
	if got := botComments(e, 46); len(got) != 1 || got[0] != "🛠️ Taken into the loose issues queue by @bob (active)." {
		t.Fatalf("#46: %q", got)
	}
	if got := botComments(e, 128); len(got) != 1 || !strings.Contains(got[0], "(position 2)") {
		t.Fatalf("#128: %q", got)
	}
	if !e.gl.Issues[46].HasLabel("agent::working") {
		t.Fatal("a take changes no labels")
	}
}

func TestLooseTakeRefusals(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(5, "Closed").State = "closed"
	e.gl.AddIssue(6, "In a milestone").Milestone = &gitlab.Milestone{ID: 1, IID: 3, Title: "Q4 @all"}
	e.gl.AddIssue(7, "Main red", "ai-generated", "ci::red")
	e.gl.AddIssue(8, "Queued")
	e.gl.AddIssue(9, "Fine")
	e.setMainHealthStatus(aiv1.MainHealthStatus{IssueIID: 7})
	e.sayLoose(tracking, bob, "/agent take #8")

	reply := e.sayLoose(tracking, bob, fmt.Sprintf("/agent take #5 #404 #6 #%d #7 #8 #9", tracking))
	for _, want := range []string{
		"Taken into the loose issues queue: #9.",
		"Not taken: #5 is closed; #404 is closed; #6 is in milestone " + fmt.Sprintf("%q", inertLine("Q4 @all", 100)) + "; " +
			fmt.Sprintf("#%d is a tracking issue of the operator; #7 is the main-red issue; #8 is in the queue already.", tracking),
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply %q, want it to contain %q", reply, want)
		}
	}
	e.wantItems("only the fine one joins", "8:Active", "9:Pending")
	for _, iid := range []int64{5, 6, 7} {
		if got := botComments(e, iid); len(got) != 0 {
			t.Fatalf("a refused issue gets no note: #%d %q", iid, got)
		}
	}

	// Taking only refused issues names them, nothing else.
	if reply := e.sayLoose(tracking, bob, "/agent take #5"); !strings.HasPrefix(reply, "🤖 Not taken: #5 is closed.") {
		t.Fatalf("all refused: %q", reply)
	}
}

func TestLooseTakeAutoFixIssueIsMainRed(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(12, "Fix main")
	e.setMainHealthStatus(aiv1.MainHealthStatus{AutoFixIssueIID: 12})
	if reply := e.sayLoose(tracking, bob, "/agent take #12"); !strings.Contains(reply, "#12 is the main-red issue") {
		t.Fatalf("the auto-fix issue is the main-red issue: %q", reply)
	}
}

func TestLooseTakeOperatorIssueWithoutTrackingMarker(t *testing.T) {
	// A generated issue that is not a tracking issue (a flaky job's, a
	// follow-up) may be taken.
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(13, "Follow-up", "ai-generated")
	e.gl.AddIssue(14, "Pasted marker").Description = marker.TrackingIssue
	e.sayLoose(tracking, bob, "/agent take #13 #14")
	e.wantItems("both join", "13:Active", "14:Pending")
}

func TestLooseTakeAgainAfterLeaving(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(20, "Twenty")
	e.gl.AddIssue(21, "Twenty-one")
	e.sayLoose(tracking, bob, "/agent take #20 #21")
	e.say(tracking, bob, "/agent drop #20")
	e.wantItems("dropped, the next starts", "20:Left", "21:Active")
	e.gl.Issues[20].Labels = nil // a human took it out of the flow again
	e.sayLoose(tracking, bob, "/agent take #20")
	e.wantItems("taken again at the end, reset", "21:Active", "20:Pending")
	if it := e.queue().Status.Autopilot.Items[1]; it.LeftReason != "" || it.LeftAt != nil {
		t.Fatalf("a fresh item: %+v", it)
	}
	if got := botComments(e, 20); len(got) != 2 || !strings.Contains(got[1], "(position 1)") {
		t.Fatalf("#20 is commented again with its new position: %q", got)
	}
}

func TestLooseTakeRetryCommentsOnce(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(30, "Thirty")
	e.gl.AddIssue(31, "Thirty-one")
	e.gl.NoteErrors = map[int64]error{31: &gitlab.Error{StatusCode: 500, Method: "POST", Path: "notes"}}
	n := e.gl.AddNote(tracking, bob, "/agent take #30 #31")
	e.reconcileFailing()
	e.wantItems("the failed take starts nothing", "30:Pending")
	if len(botComments(e, tracking)) != 0 {
		t.Fatal("no reply before the command went through")
	}
	delete(e.gl.NoteErrors, 31)
	e.reconcile()
	e.wantItems("the retry adds the second", "30:Active", "31:Pending")
	if got := botComments(e, 30); len(got) != 1 {
		t.Fatalf("#30 is commented once: %q", got)
	}
	if reply := e.replyTo(tracking, n.ID); !strings.Contains(reply, "Taken into the loose issues queue: #30, #31.") {
		t.Fatalf("the retry's reply names both as taken: %q", reply)
	}
}

func TestLooseTakeCapAndUsage(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	var many []string
	for i := range 21 {
		many = append(many, fmt.Sprintf("#%d", 100+i))
	}
	if reply := e.sayLoose(tracking, bob, "/agent take "+strings.Join(many, " ")); !strings.Contains(reply, "take needs 1 to 20 issues") {
		t.Fatalf("21 issues: %q", reply)
	}
	if reply := e.sayLoose(tracking, bob, "/agent take"); !strings.Contains(reply, "Write `/agent take #N [#M …]`.") {
		t.Fatalf("no issue: %q", reply)
	}
	e.wantItems("nothing taken")
}

func TestLooseNonOwnerAndOtherWords(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(40, "Forty")
	if reply := e.sayLoose(tracking, mallory, "/agent take #40"); !strings.Contains(reply, notOwnerQueue) {
		t.Fatalf("non-owner: %q", reply)
	}
	e.wantItems("a non-owner takes nothing")
	if got := botComments(e, 40); len(got) != 0 {
		t.Fatalf("no note on a refused take: %q", got)
	}
	for _, word := range []string{"parallel 2", "move top", "release", "no-release #1", "endMilestone", "skip #1", "unskip #1", "start", "startAnyway #1"} {
		if reply := e.sayLoose(tracking, bob, "/agent "+word); !strings.Contains(reply, "is not on the loose issues queue") {
			t.Fatalf("%s: %q", word, reply)
		}
	}
}

func TestLooseDropPendingAndActive(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(45, "Pending")
	e.gl.AddIssue(46, "In work", "agent::review")
	e.sayLoose(tracking, bob, "/agent take #45 #46")
	e.wantItems("taken", "45:Pending", "46:Active")

	before := len(botComments(e, tracking))
	n := e.gl.AddNote(tracking, bob, "/agent drop #45")
	e.reconcile()
	e.wantItems("pending dropped", "45:Left", "46:Active")
	if it := e.queue().Status.Autopilot.Items[0]; it.LeftReason != "dropped by @bob" || it.LeftAt == nil {
		t.Fatalf("left with its reason: %+v", it)
	}
	e.wantNotes(tracking, "↩️ #45 left the queue: dropped by @bob.", 1)
	for _, b := range botComments(e, tracking) {
		if strings.Contains(b, replyToMarker(n.ID)) {
			t.Fatalf("an applied drop is logged, not answered twice: %q", b)
		}
	}

	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items[1].Hold = &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI is red"}
		s.Autopilot.Landing = 46
	})
	e.gl.AddNote(tracking, bob, "/agent drop #46")
	e.reconcile()
	e.wantItems("held dropped", "45:Left", "46:Left")
	ap := e.queue().Status.Autopilot
	if it := ap.Items[1]; it.LeftReason != "dropped by @bob; continues in the manual flow" || it.Hold != nil || ap.Landing != 0 {
		t.Fatalf("an active one continues in the manual flow: %+v landing %d", it, ap.Landing)
	}
	e.wantNotes(tracking, "↩️ #46 left the queue: dropped by @bob; continues in the manual flow.", 1)
	if !slices.Equal(e.gl.Issues[46].Labels, []string{"agent::review"}) {
		t.Fatalf("its labels stay: %v", e.gl.Issues[46].Labels)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "### Left") {
		t.Fatalf("the tracking issue lists the left issues:\n%s", d)
	}

	// Not in the queue (any more): a usage reply.
	if reply := e.sayLoose(tracking, bob, "/agent drop #46"); !strings.Contains(reply, "`drop` needs an issue that is pending, active or held") {
		t.Fatalf("drop of a left issue: %q", reply)
	}
	if len(botComments(e, tracking)) != before+3 {
		t.Fatalf("two logs and one usage reply: %q", botComments(e, tracking)[before:])
	}
}

func TestLooseDeskCommands(t *testing.T) {
	e, _ := newLooseCommandEnv(t)
	e.gl.AddIssue(350, "Flaky")
	e.gl.AddIssue(421, "Timeout")
	e.gl.AddIssue(5, "Closed").State = "closed"

	e.sendCommand(queueTaskName, 1, "alice@example.com", `{"cmd":"take","issues":[350,421,5]}`)
	if got := chatAnswer(*e.queue(), 1); got != "Taken into the loose issues queue: #350, #421. Not taken: #5 is closed." {
		t.Fatalf("Desk take: %q", got)
	}
	e.wantItems("taken from the Desk", "350:Active", "421:Pending")
	if got := botComments(e, 421); len(got) != 1 || got[0] != "🛠️ Taken into the loose issues queue by alice via the Desk (position 2)." {
		t.Fatalf("#421: %q", got)
	}

	e.sendCommand(queueTaskName, 2, "alice@example.com", `{"cmd":"drop","issue":350}`)
	if got := chatAnswer(*e.queue(), 2); got != "#350 left the queue: dropped by alice via the Desk; continues in the manual flow." {
		t.Fatalf("Desk drop: %q", got)
	}
	e.wantItems("dropped from the Desk, the next starts", "350:Left", "421:Active")

	e.sendCommand(queueTaskName, 3, "mallory@example.com", `{"cmd":"take","issues":[5]}`)
	e.sendCommand(queueTaskName, 4, "alice@example.com", `{"cmd":"move","to":"top"}`)
	e.sendCommand(queueTaskName, 5, "alice@example.com", `{"cmd":"take","issues":[]}`)
	qt := e.queue()
	for id, want := range map[int64]string{3: notOwnerQueue, 4: "move is not on the loose issues queue.", 5: "take needs 1 to 20 issues"} {
		if got := chatAnswer(*qt, id); !strings.Contains(got, want) {
			t.Fatalf("message %d: %q, want %q", id, got, want)
		}
	}
	if qt.Spec.Source.LastChatCommandID != 5 {
		t.Fatalf("every message is answered: mark %d", qt.Spec.Source.LastChatCommandID)
	}

	// Off: Desk commands are answered without being applied.
	e.setLoose(false)
	e.reconcile()
	e.sendCommand(queueTaskName, 6, "alice@example.com", `{"cmd":"take","issues":[421]}`)
	if got := chatAnswer(*e.queue(), 6); !strings.Contains(got, "The loose issues queue is off") {
		t.Fatalf("while off: %q", got)
	}
}

func TestLooseCommandRepliesDoNotAnswerTheTrackingIssue(t *testing.T) {
	e, tracking := newLooseCommandEnv(t)
	e.gl.AddIssue(60, "Sixty")
	// Without the bot as its author (as if moved in from another project)
	// it is still the queue's: syncCommandReplies leaves it out.
	e.gl.Issues[tracking].Author = bob
	n := e.gl.AddNote(tracking, bob, "/agent take #60")
	e.reconcile()
	e.reconcile()
	if reply := e.replyTo(tracking, n.ID); !strings.Contains(reply, "Taken into the loose issues queue: #60.") {
		t.Fatalf("the queue's reply only: %q", reply)
	}
	cr := e.project().Status.CommandReplies
	if cr != nil && slices.Contains(cr.Replied, n.ID) {
		t.Fatal("syncCommandReplies does not answer the queue's tracking issue")
	}
}
