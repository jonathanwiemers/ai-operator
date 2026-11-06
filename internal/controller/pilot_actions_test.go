package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// pilotState is aiv1.PilotState of the milestone now, with the tasks of
// its issues.
func (e *projectEnv) pilotState(actions []aiv1.PilotAction) string {
	e.t.Helper()
	st := e.tasks()[specTaskName]
	tasks := map[int64]*aiv1.AgentTask{}
	for _, t := range e.tasks() {
		if t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil && t.Spec.Milestone != nil && t.Spec.Milestone.IID == st.Spec.Milestone.IID {
			tasks[t.Spec.Issue.IID] = &t
		}
	}
	return aiv1.PilotState(&st, tasks, actions)
}

// propose plays the pilot's run: it records a proposal made on the
// current state, as the runner does.
func (e *projectEnv) propose(summary string, actions ...aiv1.PilotAction) {
	e.t.Helper()
	p := aiv1.PilotProposal{Summary: summary, Actions: actions, State: e.pilotState(actions)}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Proposed = append(s.Pilot.Proposed, p)
	})
}

// legacyProposal records proposal P<id> on the current state as the
// operator posted it before prompts, pinned to alice, with its comment on
// the tracking issue, and returns the comment.
func (e *projectEnv) legacyProposal(id int64, summary string, actions ...aiv1.PilotAction) gitlab.Note {
	e.t.Helper()
	posted := metav1.NewTime(e.r.now())
	p := aiv1.PilotProposal{ID: id, Summary: summary, Actions: actions, State: e.pilotState(actions), PostedAt: &posted, OwnerIDs: []int64{alice.ID}}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Proposals, s.Pilot.LastProposal = append(s.Pilot.Proposals, p), id
	})
	return e.gl.AddNote(e.tracking(), e.gl.Bot, fmt.Sprintf("🧭 **Proposal P%[1]d:** %[2]s\n\n<!-- ai-operator:proposal id=%[1]d state=%[3]s -->", id, summary, p.State))
}

// tracking is the milestone's tracking issue.
func (e *projectEnv) tracking() int64 {
	e.t.Helper()
	return trackingIssue(e.task(specTaskName), e.task(pilotTaskName))
}

// promptNote returns the bot's comment of prompt id on the tracking issue.
func (e *projectEnv) promptNote(id string) gitlab.Note {
	e.t.Helper()
	for _, n := range promptNotes(e, e.tracking()) {
		if strings.Contains(n.Body, "<!-- ai-operator:prompt="+id+" ") {
			return n
		}
	}
	e.t.Fatalf("no prompt %s on the tracking issue:\n%s", id, strings.Join(botComments(e, e.tracking()), "\n---\n"))
	return gitlab.Note{}
}

// choose replies with an option's letter as by in the thread of prompt id.
func (e *projectEnv) choose(id, option string, by gitlab.User) {
	e.t.Helper()
	e.gl.AddReply(e.tracking(), e.promptNote(id).ID, by, option)
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

func TestPilotToolsRunOnceOnAnOwnersAnswer(t *testing.T) {
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
			tracking := e.tracking()
			before := e.toolEffect()
			e.propose("fix #1", tc.action)
			e.reconcile()
			e.reconcile()
			p := e.prompt(specTaskName, "Q1")
			if p.Source.Kind != aiv1.PromptProposal || p.Question != "fix #1" || len(p.Options) != 2 || !slices.Equal(p.OwnerIDs, []int64{alice.ID}) {
				t.Fatalf("the proposal is prompt Q1, pinned to alice: %+v", p)
			}
			if a, b := p.Options[0], p.Options[1]; a.Label != "Do it" || !a.Recommended || a.Detail != "fix #1" || len(a.Actions) != 1 ||
				a.Actions[0].Tool != tc.action.Tool || b.Label != "Do nothing" || len(b.Actions) != 0 {
				t.Fatalf("A does it (recommended), B does nothing: %+v", p.Options)
			}
			if !strings.Contains(p.Context, "**"+tc.action.Tool+"**") {
				t.Fatalf("the context lists the action: %q", p.Context)
			}
			if c := strings.Join(botComments(e, tracking), "\n"); strings.Count(c, "<!-- ai-operator:prompt=Q1 ") != 1 || strings.Contains(c, "Proposal P") {
				t.Fatalf("the prompt is mirrored once, with no proposal comment:\n%s", c)
			}
			if after := e.toolEffect(); after != before || e.gl.DiscussionLists != 0 {
				t.Fatalf("nothing runs without an answer, and no thread is read: %+v → %+v, %d lists", before, after, e.gl.DiscussionLists)
			}
			e.choose("Q1", "A", alice)
			e.reconcile()
			after := e.toolEffect()
			if !tc.ran(before, after) || !strings.Contains(e.lastReply(), "✅ **Q1** answered by `@alice` in GitLab: **A** Do it") ||
				!strings.Contains(e.lastReply(), "**"+tc.action.Tool+"**") {
				t.Fatalf("the tool runs on the owner's answer: %+v → %+v, reply %q", before, after, e.lastReply())
			}
			// Exactly once: another answer runs nothing.
			e.choose("Q1", "A", alice)
			e.reconcile()
			e.reconcile()
			if again := e.toolEffect(); again != after || !strings.Contains(e.lastReply(), "Q1 was answered already by @alice: Do it.") {
				t.Fatalf("a proposal runs once: %+v → %+v, reply %q", after, again, e.lastReply())
			}
		})
	}
}

func TestPilotProposalPromptAnsweredInTheDesk(t *testing.T) {
	e := newAnswerEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Pilot.Autonomy = aiv1.PilotPropose })
	before := e.toolEffect()
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.propose("retry #1 again", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if r := e.deskCommand(401, "alice@example.com", answerBody("Q2", "B")); r != "Q2 answered: Do nothing." {
		t.Fatalf("B answers it: %q", r)
	}
	if p := e.prompt(specTaskName, "Q2"); p.State != aiv1.PromptAnswered || p.Answer.OptionID != "B" || e.toolEffect() != before {
		t.Fatalf("B records doing nothing and runs nothing: %+v", p)
	}
	if r := e.deskCommand(402, "alice@example.com", answerBody("Q1", "A")); r != "Q1 answered: Do it. The outcome follows on the prompt." {
		t.Fatalf("A answers it: %q", r)
	}
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); e.toolEffect().runs != before.runs+1 || !strings.Contains(p.Answer.Outcome, "**retry** #1") {
		t.Fatalf("A runs the retry once and records it: %+v", p.Answer)
	}
}

func TestPilotOpenIssueIsInertAndNotHandedOver(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("open an issue", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust", Note: "cc @everyone\n/close"})
	e.reconcile()
	e.choose("Q1", "A", alice)
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
	// Two open proposals with the same title, from two runs.
	e.propose("open", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.propose("open again", aiv1.PilotAction{Tool: "openIssue", Title: " agent  image lacks rust"})
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.choose("Q2", "A", alice)
	e.reconcile()
	e.reconcile()
	if got := e.toolEffect().opened; got != 1 || !strings.Contains(e.lastReply(), "not run: the pilot opened an issue with this title before") {
		t.Fatalf("the same title is opened once: %d issues, reply %q", got, e.lastReply())
	}
	// A later proposal with that title is not put, nor two with one title
	// from one poll.
	e.propose("open once more", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.propose("open another", aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"})
	e.propose("open another again", aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"})
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=answered Q2=answered Q3=open" || e.prompt(specTaskName, "Q3").Question != "open another" {
		t.Fatalf("a proposal with nothing to run is not put: %s", got)
	}
}

func TestPilotOpenIssueThatFailedCanBeOpenedAgain(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("open", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.gl.CreateIssueError = errors.New("gitlab is down")
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.gl.CreateIssueError = nil
	if !strings.Contains(e.lastReply(), "not run: gitlab is down") {
		t.Fatalf("the failure is reported: %q", e.lastReply())
	}
	e.propose("open again", aiv1.PilotAction{Tool: "openIssue", Title: "Agent image lacks Rust"})
	e.reconcile()
	e.choose("Q2", "A", alice)
	e.reconcile()
	if e.toolEffect().opened != 1 {
		t.Fatalf("a title that failed is not taken: %q", e.lastReply())
	}
}

func TestPilotLegacyProposalIsAnsweredUntilItExpires(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.tracking()
	before := e.toolEffect()
	// Posted before the upgrade; new proposals do not count P<n> on.
	p := e.legacyProposal(3, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.propose("retry #1 later", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if ps := e.pilot().Status.Pilot; ps.LastProposal != 3 || len(ps.Proposals) != 1 || e.prompt(specTaskName, "Q1").Question != "retry #1 later" {
		t.Fatalf("P3 stays open next to the new prompt Q1: %+v", ps)
	}
	e.gl.AddReply(tracking, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || !strings.Contains(e.lastReply(), "Proposal P3 accepted by `@alice`") {
		t.Fatalf("an owner's yes in its thread runs P3: %+v → %+v, reply %q", before, after, e.lastReply())
	}

	e.legacyProposal(4, "retry #1 again", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	runs := e.toolEffect().runs
	e.gl.Clock = e.gl.Clock.Add(proposalTTL + time.Minute)
	e.gl.AddNote(tracking, alice, "/pilot yes P4")
	e.reconcile()
	if e.toolEffect().runs != runs || !strings.Contains(e.lastReply(), "Proposal P4 no longer applies") {
		t.Fatalf("after 24 h P4 runs nothing: reply %q", e.lastReply())
	}
}

func TestPilotLegacyYesOnAnIssueRunsAtTheEndOfThePoll(t *testing.T) {
	e := newProposalEnv(t)
	e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
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
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1"}}}, 2: {},
		3: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1", ReviewedSHA: "sha1", LastResult: aiv1.RunResultSucceeded}}},
		4: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1", ReviewedSHA: "sha1", LastResult: aiv1.RunResultSucceeded, ReviewErrors: 1}}},
		5: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1", ReviewedSHA: "sha1", LastResult: aiv1.RunResultFailed}}}}
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
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 1}, ""},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 3}, "already reviewed"},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 4}, ""},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 5}, ""},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "merge !9"}, "not a kind of instruction"},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "rerun"}, ""},
		{aiv1.PilotAction{Tool: "openIssue"}, "needs a title"},
		{aiv1.PilotAction{Tool: "openIssue", Title: "Old"}, "opened an issue with this title before"},
		{aiv1.PilotAction{Tool: "retryPipeline"}, "retryPipeline needs the pipeline"},
		{aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 77}, ""},
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
	p := e.prompt(specTaskName, "Q1")
	if body := e.promptNote("Q1").Body; !strings.Contains(body, "not run: `merge` is not a tool") || !strings.Contains(body, "not run: #9 is not an issue of this milestone") {
		t.Fatalf("the prompt says what the operator refuses:\n%s", body)
	}
	if a := p.Options[0].Actions; len(a) != 1 || a[0].Tool != "retry" || a[0].IssueIID != 1 {
		t.Fatalf("A has only the action the operator runs: %+v", a)
	}
	e.choose("Q1", "A", alice)
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || e.implTaskOf(9).Spec.RunRequest != nine || len(e.gl.Merged) != 0 {
		t.Fatalf("only the retry of #1 runs: #1 %d→%d, #9 %d→%d, merged %v", before.runs, after.runs, nine, e.implTaskOf(9).Spec.RunRequest, e.gl.Merged)
	}

	// A proposal of nothing but refused actions is not put.
	e.propose("merge", aiv1.PilotAction{Tool: "merge", IssueIID: 1})
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=answered" {
		t.Fatalf("nothing to run, nothing put: %s", got)
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
			p := e.promptNote("Q1")
			before := e.toolEffect()
			tc.change(e)
			e.gl.AddReply(e.tracking(), p.ID, alice, "A")
			e.reconcile()
			if after := e.toolEffect(); after != before || !strings.Contains(e.lastReply(), "Q1 no longer applies") {
				t.Fatalf("an expired proposal runs nothing: %+v → %+v, reply %q", before, after, e.lastReply())
			}
		})
	}
}

func TestPilotProposalMadeOnAStateThatIsGoneIsNotPut(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
	e.reconcile()
	if ps := e.pilot().Status.Pilot; len(ps.Proposed) != 0 || len(e.task(specTaskName).Status.Prompts) != 0 {
		t.Fatalf("an answer could not run it: it is dropped: %+v", ps)
	}
}

func TestPilotProposalIsPutOnceForItsSource(t *testing.T) {
	e := newProposalEnv(t)
	p := aiv1.PilotProposal{Summary: "retry #1", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Source: "event/1", Trigger: 1, Quiet: true,
		Why: "`infra`: the runner died"}
	p.State = e.pilotState(p.Actions)
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = &aiv1.PilotStatus{Proposed: []aiv1.PilotProposal{p}} })
	e.reconcile()
	q := e.prompt(specTaskName, "Q1")
	if q.Source != (aiv1.PromptSource{Kind: aiv1.PromptProposal, IssueIID: 1, Key: "event/1"}) || q.Quiet ||
		!strings.HasPrefix(q.Context, "**Why:** `infra`: the runner died\n\n- **retry** #1") {
		t.Fatalf("the prompt has the proposal's source, reasons and the owners' mention: %+v", q)
	}
	if body := e.promptNote("Q1").Body; !strings.Contains(body, "cc @alice") {
		t.Fatalf("a quiet assessment's proposal pings the owners, on the tracking issue:\n%s", body)
	}
	// Its run's status was not written (a conflict): it is not put twice.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Proposed = []aiv1.PilotProposal{p} })
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=open" || len(e.pilot().Status.Pilot.Proposed) != 0 {
		t.Fatalf("one prompt per source: %s", got)
	}
}

func TestPilotYesCountsOnlyFromAnOwnerInTheThread(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.tracking()
	p := e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.propose("retry #1 again", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
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

	// A bare "/pilot yes" names no proposal: refused, with the open ones
	// and the open prompts.
	e.gl.AddNote(tracking, alice, "/pilot yes")
	e.reconcile()
	if after, r := e.toolEffect(), e.lastReply(); after != before || !strings.Contains(r, "Which proposal?") || !strings.Contains(r, "P1: retry #1") ||
		!strings.Contains(r, "Open prompts, answered in the Desk:\n- Q1: retry #1 again — Answer in the Desk.") {
		t.Fatalf("a bare yes is refused with the open proposals and prompts: %+v, reply %q", after, r)
	}

	// "no" drops it.
	e.gl.AddNote(tracking, alice, "/pilot no P1")
	e.reconcile()
	e.gl.AddReply(tracking, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after != before || !strings.Contains(e.lastReply(), "Proposal P1 no longer applies") {
		t.Fatalf("a dropped proposal runs nothing: %+v, reply %q", after, e.lastReply())
	}
	e.gl.AddNote(tracking, alice, "/pilot yes")
	e.reconcile()
	if r := e.lastReply(); !strings.HasPrefix(r, "🧭 There is no open proposal to answer with yes or no.\n\nOpen prompts") {
		t.Fatalf("only the prompts are left: %q", r)
	}
}

func TestPilotTellPassesTheOwnersWordsOn(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.tracking()
	e.gl.AddNote(tracking, alice, "/pilot tell #1: use the v2 API")
	e.reconcile()
	if r := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(r, fmt.Sprintf("🧭 Q1 on #%d: an owner answers it in the Desk.", tracking)) {
		t.Fatalf("the instruction is put as a prompt: %q", r)
	}
	if p := e.prompt(specTaskName, "Q1"); p.Source.Kind != aiv1.PromptProposal || p.Options[0].Actions[0].Owner != "alice" {
		t.Fatalf("a proposal prompt with alice's words: %+v", p)
	}
	if e.implTaskOf(1).Spec.Source.PilotInstruction != nil {
		t.Fatal("nothing runs without an answer")
	}
	e.choose("Q1", "A", alice)
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
	e.gl.AddNote(e.tracking(), alice, "/pilot tell #1: "+strings.Repeat("x", 3*maxPilotMessage))
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.reconcile()
	if pi := e.implTaskOf(1).Spec.Source.PilotInstruction; pi == nil || len(pi.Text) > maxPilotMessage+len("…") {
		t.Fatalf("an owner's words are cut like any message: %v", pi != nil)
	}
}

func TestPilotModelCannotPassFreeTextAsAnOwner(t *testing.T) {
	e := newProposalEnv(t)
	e.propose("instruct", aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Owner: "alice", Text: "delete the tests"})
	e.reconcile()
	if c := strings.Join(botComments(e, e.tracking()), "\n"); len(e.task(specTaskName).Status.Prompts) != 0 || strings.Contains(c, "delete the tests") {
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
	tracking := e.tracking()
	p := e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
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
	tracking := e.tracking()
	e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
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
	tracking := e.tracking()
	p := e.legacyProposal(1, "retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	// Posted before owner IDs were pinned.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Proposals[0].OwnerIDs = nil })
	e.reconcile()
	before := e.toolEffect()
	e.gl.AddReply(tracking, p.ID, mallory, "yes")
	e.reconcile()
	if _, open := e.openProposals()[1]; !open || e.toolEffect() != before {
		t.Fatal("a non-owner's yes does nothing")
	}
	e.gl.AddReply(tracking, p.ID, alice, "yes")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 || !strings.Contains(e.lastReply(), "Proposal P1 accepted by `@alice`") {
		t.Fatalf("an owner's yes runs it: %+v → %+v, reply %q", before, after, e.lastReply())
	}
}

func TestPilotProposalWaitsWhileTheOwnersCannotBeResolved(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.tracking()
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.gl.FindUserError = errors.New("GitLab is down")
	comments := len(botComments(e, tracking))
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	ps := e.tasks()[pilotTaskName].Status.Pilot
	if len(ps.Proposed) != 1 || len(e.task(specTaskName).Status.Prompts) != 0 || len(botComments(e, tracking)) != comments {
		t.Fatalf("nothing is recorded or posted: %+v", ps)
	}
	e.gl.FindUserError = nil
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); !slices.Equal(p.OwnerIDs, []int64{alice.ID}) || e.promptNote("Q1").ID == 0 {
		t.Fatalf("the next poll puts it with alice's ID: %+v", p)
	}
}

func TestPilotProposalWithoutResolvableOwnersIsAnsweredByNobody(t *testing.T) {
	e := newProposalEnv(t)
	e.gl.Users = []gitlab.User{mallory} // alice deleted her account
	e.propose("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); !slices.Equal(p.OwnerIDs, []int64{0}) {
		t.Fatalf("an ID nobody has is pinned: %v", p.OwnerIDs)
	}
	// Someone registers the name.
	squatter := gitlab.User{ID: 9, Username: "alice"}
	e.gl.Users = []gitlab.User{squatter, mallory}
	before := e.toolEffect()
	e.choose("Q1", "A", squatter)
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || e.toolEffect() != before {
		t.Fatal("an answer from whoever takes an owner's name later does nothing")
	}
}

func TestCheckRetryPipeline(t *testing.T) {
	for _, tc := range []struct {
		mh *aiv1.MainHealthStatus
		ok bool
	}{
		{nil, false},
		{&aiv1.MainHealthStatus{State: aiv1.MainHealthRed, PipelineID: 77}, true},
		{&aiv1.MainHealthStatus{State: aiv1.MainHealthRed, PipelineID: 78}, false},
		{&aiv1.MainHealthStatus{State: aiv1.MainHealthRetrying, PipelineID: 77}, false},
		{&aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, PipelineID: 77}, false},
		{&aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, PipelineID: 77}, false},
	} {
		err := checkRetryPipeline(tc.mh, 77)
		if tc.ok != (err == nil) || err != nil && err.Error() != "pipeline 77 is not the red pipeline of the default branch" {
			t.Errorf("%+v: got %v, want ok=%v", tc.mh, err, tc.ok)
		}
	}
}

func TestPilotRetryPipelineRetriesTheRedPipelineOnce(t *testing.T) {
	e := newProposalEnv(t)
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, PipelineID: 77})
	e.propose("retry the red pipeline", aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 77})
	e.reconcile()
	e.reconcile()
	if body := e.promptNote("Q1").Body; !strings.Contains(body, "**retryPipeline** 77: its failed jobs run again") || len(e.gl.Retried) != 0 {
		t.Fatalf("the prompt describes the retry, nothing runs without an answer (retried %v):\n%s", e.gl.Retried, body)
	}
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.reconcile()
	if !slices.Equal(e.gl.Retried, []int64{77}) {
		t.Fatalf("the answer retries pipeline 77 once: %v", e.gl.Retried)
	}

	// Another pipeline is not put to the owners.
	e.propose("retry an old pipeline", aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 70},
		aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if body := e.promptNote("Q2").Body; !strings.Contains(body, "not run: pipeline 70 is not the red pipeline of the default branch") {
		t.Fatalf("the prompt says why pipeline 70 is refused:\n%s", body)
	}
	if a := e.prompt(specTaskName, "Q2").Options[0].Actions; len(a) != 1 || a[0].Tool != "retry" {
		t.Fatalf("A has only the action the operator runs: %+v", a)
	}

	// Green again before the answer: checked again when it runs.
	e.propose("retry it again", aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 77})
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, PipelineID: 79})
	e.choose("Q3", "A", alice)
	e.reconcile()
	if p := e.prompt(specTaskName, "Q3"); !slices.Equal(e.gl.Retried, []int64{77}) || !strings.Contains(p.Answer.Outcome, "pipeline 77 is not the red pipeline") {
		t.Fatalf("a green branch is not retried: %v, %+v", e.gl.Retried, p.Answer)
	}
}

func TestManagedPilotRetryPipelineIsCounted(t *testing.T) {
	e := newManagedEnv(t)
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, PipelineID: 77})
	e.propose("retry the red pipeline", aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 77})
	e.reconcile()
	e.reconcile()
	if ps := e.pilot().Status.Pilot; !slices.Equal(e.gl.Retried, []int64{77}) || ps.Interventions["0"].Count != 1 {
		t.Fatalf("a managing pilot retries on its own, counted against the milestone: %v, %+v", e.gl.Retried, ps.Interventions)
	}
}

// rebase and mergeBase only target the active queue item with an MR.
func TestPilotRebaseAndMergeBaseValidation(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Autopilot: true}, Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{
		Phase: aiv1.AutopilotPaused,
		Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemActive, MergeRequestIID: 5}, {IID: 2, State: aiv1.AutopilotItemPending},
			{IID: 3, State: aiv1.AutopilotItemMerged, MergeRequestIID: 6}},
		MainRedFix: &aiv1.AutopilotItem{IID: 99, State: aiv1.AutopilotItemActive, MergeRequestIID: 7},
	}}}
	noMR := st.DeepCopy()
	noMR.Status.Autopilot.Items[0].MergeRequestIID = 0
	off := st.DeepCopy()
	off.Status.Autopilot.Phase = aiv1.AutopilotDone
	for _, tool := range []string{"rebase", "mergeBase"} {
		for _, tc := range []struct {
			st   *aiv1.AgentTask
			iid  int64
			want string
		}{
			{st, 1, ""},
			{st, 2, "#2 is not the active issue of the queue"},
			{st, 3, "#3 is not the active issue of the queue"},
			{st, 99, "#99 is not the active issue of the queue"},
			{st, 0, "#0 is not the active issue of the queue"},
			{noMR, 1, "#1 has no merge request yet"},
			{off, 1, "this milestone has no autopilot queue"},
		} {
			a := aiv1.PilotAction{Tool: tool, IssueIID: tc.iid}
			err := validAction(a, tc.st, nil, nil)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("%s #%d: got %v, want %q", tool, tc.iid, err, tc.want)
			}
		}
	}
	if d := describeAction(aiv1.PilotAction{Tool: "rebase", IssueIID: 1}, nil); d != "- **rebase** #1: GitLab rebases its MR onto the default branch; the review holds if the change is unchanged" {
		t.Errorf("rebase reads %q", d)
	}
	if d := describeAction(aiv1.PilotAction{Tool: "mergeBase", IssueIID: 1}, nil); d != "- **mergeBase** #1: the agent merges the default branch into its MR" {
		t.Errorf("mergeBase reads %q", d)
	}
}

// Under propose, a mergeBase is a prompt; an owner's answer hands the active
// MR to the agent (spec.source.mergeBaseFor) and lifts the ciRed pause.
func TestPilotMergeBaseRunsOnAnOwnersAnswer(t *testing.T) {
	e := newProposalEnv(t)
	name := e.pauseBaseFailing()
	e.propose("merge main into !1", aiv1.PilotAction{Tool: "mergeBase", IssueIID: 1})
	e.reconcile()
	if body := e.promptNote("Q1").Body; !strings.Contains(body, "**mergeBase** #1: the agent merges the default branch into its MR") ||
		e.tasks()[name].Spec.Source.MergeBaseFor != "" {
		t.Fatalf("a prompt, nothing runs without an answer:\n%s", body)
	}
	e.choose("Q1", "A", alice)
	e.reconcile()
	e.reconcile()
	if !mergeBaseRequested(e, name, "sha-api") || e.autopilot().Phase != aiv1.AutopilotRunning || e.autopilot().ResumedBy != aiv1.ResolvedByOwner {
		t.Fatalf("the answer hands !1 to the agent and ends the pause: %+v, %+v", e.tasks()[name].Spec.Source, e.autopilot())
	}
	if c := trackingComments(e); !strings.Contains(c, "▶️ Autopilot continues: @alice via the pilot asked the agent to merge `main` into !1.") {
		t.Fatalf("the tracking issue says who asked:\n%s", c)
	}
}
