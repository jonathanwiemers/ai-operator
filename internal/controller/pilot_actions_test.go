package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// propose plays the pilot's run: it records a proposal made on the
// current state, as the runner does.
func (e *projectEnv) propose(summary string, actions ...aiv1.PilotAction) {
	e.t.Helper()
	st := e.tasks()[specTaskName]
	tasks := map[int64]*aiv1.AgentTask{}
	for _, t := range e.tasks() {
		if t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil && t.Spec.Milestone != nil && t.Spec.Milestone.IID == st.Spec.Milestone.IID {
			tasks[t.Spec.Issue.IID] = &t
		}
	}
	p := aiv1.PilotProposal{Summary: summary, Actions: actions, State: aiv1.PilotState(&st, tasks, actions)}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Proposed = append(s.Pilot.Proposed, p)
	})
}

// proposalNote returns the bot's comment with proposal id on the tracking
// issue.
func (e *projectEnv) proposalNote(id int64) gitlab.Note {
	e.t.Helper()
	for _, n := range e.gl.IssueNotes[e.autopilot().TrackingIssueIID] {
		if n.Author.ID == e.gl.Bot.ID && strings.Contains(n.Body, fmt.Sprintf("<!-- ai-operator:proposal id=%d ", id)) {
			return n
		}
	}
	e.t.Fatalf("no proposal P%d on the tracking issue:\n%s", id, strings.Join(botComments(e, e.autopilot().TrackingIssueIID), "\n---\n"))
	return gitlab.Note{}
}

// lastReply is the bot's newest comment on the tracking issue.
func (e *projectEnv) lastReply() string {
	c := botComments(e, e.autopilot().TrackingIssueIID)
	return c[len(c)-1]
}

// toolEffect is what a tool did to #1 and to the issues: its run requests,
// review head, instruction, and the issues titled "Agent image lacks Rust".
type toolEffect struct {
	runs        int64
	reviewFor   string
	instruction aiv1.PilotInstruction
	opened      int
}

func (e *projectEnv) toolEffect() toolEffect {
	e.t.Helper()
	t := e.implTaskOf(1)
	out := toolEffect{runs: t.Spec.RunRequest, reviewFor: t.Spec.Source.ReviewFor}
	if pi := t.Spec.Source.PilotInstruction; pi != nil {
		out.instruction = *pi
	}
	for _, is := range e.gl.Issues {
		if is.Title == "Agent image lacks Rust" {
			out.opened++
		}
	}
	return out
}

// newProposalEnv is the pilot env with #1 active and pushed at sha1, in a
// project whose pilot only proposes.
func newProposalEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newManagedEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Pilot.Autonomy = aiv1.PilotPropose })
	return e
}

// newManagedEnv is the pilot env with #1 active and pushed at sha1, on an
// autopilot milestone, whose pilot manages by default.
func newManagedEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newPilotEnv(t)
	if e.implTaskOf(1) == nil {
		t.Fatal("expected a task for the active issue #1")
	}
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		s.Run.PushedSHA = "sha1"
		s.Run.ObservedRunRequest = e.implTaskOf(1).Spec.RunRequest
	})
	return e
}

func TestPilotToolsRunOnceOnAnOwnersYes(t *testing.T) {
	for _, tc := range []struct {
		action aiv1.PilotAction
		ran    func(before, after toolEffect) bool
	}{
		{aiv1.PilotAction{Tool: "retry", IssueIID: 1}, func(b, a toolEffect) bool { return a.runs == b.runs+1 }},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 1}, func(b, a toolEffect) bool { return a.runs == b.runs+1 && a.reviewFor == "sha1" }},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "install-toolchains", Note: "cargo is missing"}, func(b, a toolEffect) bool {
			return a.runs == b.runs+1 && a.instruction.Kind == "install-toolchains" &&
				a.instruction.Note == "cargo is missing" && a.instruction.Text == "" && a.instruction.Request == a.runs
		}},
		{aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust", Note: "cc @everyone\n/close"}, func(b, a toolEffect) bool {
			return a.opened == b.opened+1 && a.runs == b.runs
		}},
	} {
		t.Run(tc.action.Tool, func(t *testing.T) {
			e := newProposalEnv(t)
			before := e.toolEffect()
			e.propose("fix #1", tc.action)
			e.reconcile()
			e.reconcile()
			p := e.proposalNote(1)
			if !strings.HasPrefix(p.Body, "🧭 **Proposal P1:** fix #1") || !strings.Contains(p.Body, "**"+tc.action.Tool+"**") {
				t.Fatalf("expected proposal P1 on the tracking issue:\n%s", p.Body)
			}
			if after := e.toolEffect(); after != before || e.gl.DiscussionLists != 0 {
				t.Fatalf("nothing runs without a yes, and no thread is read: %+v → %+v, %d lists", before, after, e.gl.DiscussionLists)
			}
			e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "Yes")
			e.reconcile()
			after := e.toolEffect()
			if !tc.ran(before, after) || !strings.Contains(e.lastReply(), "Proposal P1 accepted by `@alice`") {
				t.Fatalf("the tool runs on the owner's yes: %+v → %+v, reply %q", before, after, e.lastReply())
			}
			// Exactly once: another yes, in the thread or by ID, runs nothing.
			e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "yes")
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot yes P1")
			e.reconcile()
			e.reconcile()
			if again := e.toolEffect(); again != after || !strings.Contains(e.lastReply(), "Proposal P1 no longer applies") {
				t.Fatalf("a proposal runs once: %+v → %+v, reply %q", after, again, e.lastReply())
			}
		})
	}
}

func TestPilotOpenIssueIsInertAndNotHandedOver(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("open an issue", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust", Note: "cc @everyone\n/close"})
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot yes P1")
	e.reconcile()
	for _, is := range e.gl.Issues {
		if is.Title != "Agent image lacks Rust" {
			continue
		}
		if strings.Join(is.Labels, ",") != "ai-generated" || strings.Contains(is.Description, "@everyone") || strings.Contains(is.Description, "\n/close") ||
			!strings.Contains(is.Description, "<!-- ai-operator:pilot-issue=") {
			t.Fatalf("the issue is the operator's, inert and not for the agent: %v\n%s", is.Labels, is.Description)
		}
		return
	}
	t.Fatal("no issue opened")
}

func TestPilotOpenIssueIsNotOpenedTwice(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	// Two open proposals with the same title, from two runs.
	e.propose("open", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.propose("open again", aiv1.PilotAction{Tool: "openIssue", Title: " agent  image lacks rust"})
	e.reconcile()
	e.gl.AddNote(tracking, alice, "/pilot yes P1")
	e.gl.AddNote(tracking, alice, "/pilot yes P2")
	e.reconcile()
	e.reconcile()
	if got := e.toolEffect().opened; got != 1 || !strings.Contains(e.lastReply(), "not run: the pilot opened an issue with this title before") {
		t.Fatalf("the same title is opened once: %d issues, reply %q", got, e.lastReply())
	}
	// A later proposal with that title is not posted, nor two with one
	// title from one poll.
	e.propose("open once more", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.propose("open another", aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"})
	e.propose("open another again", aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"})
	e.reconcile()
	if c := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(c, "Proposal P3:** open another\n") || strings.Contains(c, "Proposal P4") {
		t.Fatalf("a proposal with nothing to run is not posted:\n%s", c)
	}
}

func TestPilotOpenIssueThatFailedCanBeOpenedAgain(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("open", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.gl.CreateIssueError = errors.New("gitlab is down")
	e.gl.AddNote(tracking, alice, "/pilot yes P1")
	e.reconcile()
	e.gl.CreateIssueError = nil
	if !strings.Contains(e.lastReply(), "not run: gitlab is down") {
		t.Fatalf("the failure is reported: %q", e.lastReply())
	}
	e.propose("open again", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.gl.AddNote(tracking, alice, "/pilot yes P2")
	e.reconcile()
	if e.toolEffect().opened != 1 {
		t.Fatalf("a title that failed is not taken: %q", e.lastReply())
	}
}

func TestPilotYesOnAnIssueRunsAtTheEndOfThePoll(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	before := e.toolEffect()
	// Accepted on #1 itself, whose task the poll updates too.
	e.gl.AddNote(1, alice, "/pilot yes P1")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || !strings.Contains(lastNote(e, 1), "the outcome follows on #") ||
		!strings.Contains(e.lastReply(), "Proposal P1 accepted by `@alice`") {
		t.Fatalf("accepted on the issue, run once in the same poll: %+v → %+v, %q", before, after, lastNote(e, 1))
	}
}

func TestPilotActionValidation(t *testing.T) {
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1"}}}, 2: {}}
	for _, tc := range []struct {
		a    aiv1.PilotAction
		want string
	}{
		{aiv1.PilotAction{Tool: "retry", IssueIID: 1}, ""},
		{aiv1.PilotAction{Tool: "retry", IssueIID: 7}, "#7 is not an issue of this milestone"},
		{aiv1.PilotAction{Tool: "merge", IssueIID: 1}, "`merge` is not a tool of the pilot"},
		{aiv1.PilotAction{Tool: "close", IssueIID: 1}, "not a tool"},
		{aiv1.PilotAction{Tool: "push", IssueIID: 1}, "not a tool"},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 2}, "no pushed head"},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "merge !9"}, "not a kind of instruction"},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "rerun"}, ""},
		{aiv1.PilotAction{Tool: "openIssue"}, "needs a title"},
		{aiv1.PilotAction{Tool: "openIssue", Title: "Old"}, "opened an issue with this title before"},
	} {
		err := validAction(tc.a, &aiv1.AgentTask{}, tasks, []string{titleKey("old")})
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: got %v, want %q", tc.a, err, tc.want)
		}
	}
}

func TestPilotRefusesActionsOutsideTheMilestone(t *testing.T) {
	e := newProposalEnv(t)
	// #9 has a task, but in no milestone.
	e.gl.AddIssue(9, "Elsewhere", "agent::needs-input")
	e.reconcile()
	before := e.toolEffect()
	nine := e.implTaskOf(9).Spec.RunRequest
	e.propose("merge and more", aiv1.PilotAction{Tool: "merge", IssueIID: 1}, aiv1.PilotAction{Tool: "retry", IssueIID: 9},
		aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	p := e.proposalNote(1)
	if !strings.Contains(p.Body, "not run: `merge` is not a tool") || !strings.Contains(p.Body, "not run: #9 is not an issue of this milestone") {
		t.Fatalf("the proposal says what the operator refuses:\n%s", p.Body)
	}
	e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || e.implTaskOf(9).Spec.RunRequest != nine || len(e.gl.Merged) != 0 {
		t.Fatalf("only the retry of #1 runs: #1 %d→%d, #9 %d→%d, merged %v", before.runs, after.runs, nine, e.implTaskOf(9).Spec.RunRequest, e.gl.Merged)
	}

	// A proposal of nothing but refused actions is not posted.
	e.propose("merge", aiv1.PilotAction{Tool: "merge", IssueIID: 1})
	e.reconcile()
	if c := strings.Join(botComments(e, e.autopilot().TrackingIssueIID), "\n"); strings.Contains(c, "Proposal P2") {
		t.Fatalf("nothing to run, nothing posted:\n%s", c)
	}
}

func TestPilotExpiredProposalIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(e *projectEnv)
	}{
		{"after 24 h", func(e *projectEnv) { e.gl.Clock = e.gl.Clock.Add(proposalTTL + time.Minute) }},
		{"the head moved", func(e *projectEnv) {
			e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
		}},
		{"the pause changed", func(e *projectEnv) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot.PausedReason = "paused by @alice" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newProposalEnv(t)
			e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
			e.reconcile()
			p := e.proposalNote(1)
			before := e.toolEffect()
			tc.change(e)
			e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "yes")
			e.reconcile()
			if after := e.toolEffect(); after != before || !strings.Contains(e.lastReply(), "Proposal P1 no longer applies") {
				t.Fatalf("an expired proposal runs nothing: %+v → %+v, reply %q", before, after, e.lastReply())
			}
		})
	}
}

func TestPilotYesCountsOnlyFromAnOwnerInTheThread(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	p := e.proposalNote(1)
	before := e.toolEffect()
	e.gl.AddReply(tracking, p.ID, mallory, "yes")                               // no owner
	e.gl.AddReply(tracking, p.ID, gitlab.User{ID: 9, Username: "alice"}, "yes") // an owner's name, another user
	e.gl.AddReply(tracking, p.ID, alice, "> yes\n\nmallory said that")          // quoted
	e.gl.AddNote(tracking, alice, "yes")                                        // not in the thread
	e.gl.AddNote(tracking, mallory, "/pilot yes P1")
	e.reconcile()
	e.reconcile()
	if after := e.toolEffect(); after != before {
		t.Fatalf("no owner's yes in the thread, nothing runs: %+v → %+v", before, after)
	}
	if c := strings.Join(botComments(e, tracking), "\n"); strings.Contains(c, "accepted") {
		t.Fatalf("nothing is accepted:\n%s", c)
	}

	// A bare "/pilot yes" names no proposal: refused, with the open ones.
	e.gl.AddNote(tracking, alice, "/pilot yes")
	e.reconcile()
	if after := e.toolEffect(); after != before || !strings.Contains(e.lastReply(), "Which proposal?") || !strings.Contains(e.lastReply(), "P1: retry #1") {
		t.Fatalf("a bare yes is refused with the open proposals: %+v, reply %q", after, e.lastReply())
	}

	// "no" drops it.
	e.gl.AddNote(tracking, alice, "/pilot no P1")
	e.reconcile()
	e.gl.AddReply(tracking, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after != before || !strings.Contains(e.lastReply(), "Proposal P1 no longer applies") {
		t.Fatalf("a dropped proposal runs nothing: %+v, reply %q", after, e.lastReply())
	}
}

func TestPilotTellPassesTheOwnersWordsOn(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/pilot tell #1: use the v2 API")
	e.reconcile()
	if !strings.Contains(e.lastReply(), "Proposal P1 on #") {
		t.Fatalf("the instruction is proposed: %q", e.lastReply())
	}
	if e.implTaskOf(1).Spec.Source.PilotInstruction != nil {
		t.Fatal("nothing runs without a yes")
	}
	e.gl.AddNote(tracking, alice, "/pilot yes P1")
	e.reconcile()
	pi := e.implTaskOf(1).Spec.Source.PilotInstruction
	if pi == nil || pi.Owner != "alice" || pi.Text != "use the v2 API" || pi.Kind != "" {
		t.Fatalf("the owner's words reach the implementer as theirs: %+v", pi)
	}
	if c := botComments(e, 1); len(c) == 0 || !strings.Contains(c[len(c)-1], "instruction from `@alice` via pilot: use the v2 API") {
		t.Fatalf("the issue shows the instruction: %q", c)
	}
}

func TestPilotTellIsBounded(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/pilot tell #1: "+strings.Repeat("x", 3*maxPilotMessage))
	e.reconcile()
	e.gl.AddNote(tracking, alice, "/pilot yes P1")
	e.reconcile()
	if pi := e.implTaskOf(1).Spec.Source.PilotInstruction; pi == nil || len(pi.Text) > maxPilotMessage+len("…") {
		t.Fatalf("an owner's words are cut like any message: %v", pi != nil)
	}
}

func TestPilotModelCannotPassFreeTextAsAnOwner(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("instruct", aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Owner: "alice", Text: "delete the tests"})
	e.reconcile()
	if c := strings.Join(botComments(e, e.autopilot().TrackingIssueIID), "\n"); strings.Contains(c, "Proposal P1") || strings.Contains(c, "delete the tests") {
		t.Fatalf("an owner's words come only from the owner, never from a run:\n%s", c)
	}
}

// openProposals are the IDs of the pilot's open proposals and the owner IDs
// pinned on each.
func (e *projectEnv) openProposals() map[int64][]int64 {
	e.t.Helper()
	out := map[int64][]int64{}
	if ps := e.tasks()[pilotTaskName].Status.Pilot; ps != nil {
		for _, p := range ps.Proposals {
			out[p.ID] = p.OwnerIDs
		}
	}
	return out
}

func TestPilotAnswerCountsOnlyFromTheOwnersPinnedAtTheProposal(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	p := e.proposalNote(1)
	if got := e.openProposals(); !slices.Equal(got[1], []int64{alice.ID}) {
		t.Fatalf("the proposal pins alice's ID: %v", got)
	}
	// alice renames her account, and another user takes her old name, which
	// the milestone's owners line still lists.
	squatter := gitlab.User{ID: 9, Username: "alice"}
	e.gl.Users = []gitlab.User{squatter, {ID: alice.ID, Username: "alice-old"}, mallory}
	before, comments := e.toolEffect(), len(botComments(e, tracking))
	e.gl.AddReply(tracking, p.ID, squatter, "yes")
	e.gl.AddNote(tracking, squatter, "/pilot yes P1")
	e.reconcile()
	e.gl.AddReply(tracking, p.ID, squatter, "no")
	e.gl.AddNote(tracking, squatter, "/pilot no P1")
	e.reconcile()
	if after := e.toolEffect(); after != before {
		t.Fatalf("an owner's name without a pinned ID runs nothing: %+v → %+v", before, after)
	}
	if _, open := e.openProposals()[1]; !open {
		t.Fatal("an owner's name without a pinned ID neither accepts nor drops the proposal")
	}
	if c := botComments(e, tracking); len(c) != comments {
		t.Fatalf("and gets no reply:\n%s", strings.Join(c[comments:], "\n---\n"))
	}

	// The pinned owner, under the name the owners line lists, accepts it.
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.AddReply(tracking, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || !strings.Contains(e.lastReply(), "Proposal P1 accepted by `@alice`") {
		t.Fatalf("the pinned owner's yes runs it: %+v → %+v, reply %q", before, after, e.lastReply())
	}
}

func TestPilotAnswerByIDCountsOnlyFromAPinnedOwner(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	squatter := gitlab.User{ID: 9, Username: "alice"}
	e.gl.Users = []gitlab.User{squatter, mallory}
	before := e.toolEffect()
	e.gl.AddNote(tracking, squatter, "/pilot yes P1")
	e.reconcile()
	if _, open := e.openProposals()[1]; !open || e.toolEffect() != before {
		t.Fatal("a yes by ID from an unpinned user does nothing")
	}
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.AddNote(1, alice, "/pilot yes P1")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 {
		t.Fatalf("the pinned owner's yes on an issue runs it: %+v → %+v", before, after)
	}
}

func TestPilotProposalWithoutPinnedOwnersKeepsTheCheckByName(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	// Posted before owner IDs were pinned.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Proposals[0].OwnerIDs = nil })
	before := e.toolEffect()
	e.gl.AddReply(tracking, e.proposalNote(1).ID, mallory, "yes")
	e.reconcile()
	if _, open := e.openProposals()[1]; !open || e.toolEffect() != before {
		t.Fatal("a non-owner's yes does nothing")
	}
	e.gl.AddReply(tracking, e.proposalNote(1).ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || !strings.Contains(e.lastReply(), "Proposal P1 accepted by `@alice`") {
		t.Fatalf("an owner's yes runs it: %+v → %+v, reply %q", before, after, e.lastReply())
	}
}

func TestPilotProposalWaitsWhileTheOwnersCannotBeResolved(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.gl.FindUserError = errors.New("GitLab is down")
	comments := len(botComments(e, tracking))
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	ps := e.tasks()[pilotTaskName].Status.Pilot
	if len(ps.Proposed) != 1 || len(ps.Proposals) != 0 || ps.LastProposal != 0 || len(botComments(e, tracking)) != comments {
		t.Fatalf("nothing is recorded or posted: %+v", ps)
	}
	e.gl.FindUserError = nil
	e.reconcile()
	if p := e.proposalNote(1); !strings.Contains(p.Body, "Proposal P1:") || !slices.Equal(e.openProposals()[1], []int64{alice.ID}) {
		t.Fatalf("the next poll posts it with alice's ID: %v", e.openProposals())
	}
}

func TestPilotProposalWithoutResolvableOwnersIsAnsweredByNobody(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.Users = []gitlab.User{mallory} // alice deleted her account
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if got := e.openProposals(); !slices.Equal(got[1], []int64{0}) {
		t.Fatalf("an ID nobody has is pinned: %v", got)
	}
	// Someone registers the name.
	squatter := gitlab.User{ID: 9, Username: "alice"}
	e.gl.Users = []gitlab.User{squatter, mallory}
	before := e.toolEffect()
	e.gl.AddReply(tracking, e.proposalNote(1).ID, squatter, "yes")
	e.reconcile()
	if _, open := e.openProposals()[1]; !open || e.toolEffect() != before {
		t.Fatal("a yes from whoever takes an owner's name later does nothing")
	}
}
