package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// queueFixture is a Queue task with #350 active, held and with a merge
// request, #421 pending, #46 merged and #52 left, and the tasks of #350
// (pushed, stalled) and #421.
func queueFixture(phase aiv1.AutopilotPhase) (*aiv1.AgentTask, map[int64]*aiv1.AgentTask) {
	qt := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue}, Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{
		Phase: phase, Items: []aiv1.AutopilotItem{
			{IID: 350, State: aiv1.AutopilotItemActive, MergeRequestIID: 7, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI is red"}},
			{IID: 421, State: aiv1.AutopilotItemPending},
			{IID: 46, State: aiv1.AutopilotItemMerged},
			{IID: 52, State: aiv1.AutopilotItemLeft},
		}}}}
	stalled := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha350"},
		Conditions: []metav1.Condition{{Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue}}}}
	return qt, map[int64]*aiv1.AgentTask{350: stalled, 421: {}}
}

func TestQueuePilotToolsOnQueueIssues(t *testing.T) {
	qt, tasks := queueFixture(aiv1.AutopilotRunning)
	if !meta.IsStatusConditionTrue(tasks[350].Status.Conditions, aiv1.ConditionStalled) {
		t.Fatal("the fixture's run is stalled")
	}
	kind := aiv1.PilotInstructKinds[0]
	for _, tc := range []struct {
		a    aiv1.PilotAction
		want string
	}{
		// Allowed (Decision 5).
		{aiv1.PilotAction{Tool: "retry", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "reReview", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 350, Kind: kind}, ""},
		{aiv1.PilotAction{Tool: "retryPipeline", PipelineID: 900}, ""},
		{aiv1.PilotAction{Tool: "rebase", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "mergeBase", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "fixFlaky", IssueIID: 77}, ""},
		{aiv1.PilotAction{Tool: "openIssue", Title: "Runner image lacks git"}, ""},
		{aiv1.PilotAction{Tool: "markDone", IssueIID: 421}, ""},
		{aiv1.PilotAction{Tool: "pause"}, ""},
		{aiv1.PilotAction{Tool: "resume", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 421}, ""},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 350}, ""},
		{aiv1.PilotAction{Tool: "restartRun", IssueIID: 350}, ""},
		// Refused with their reasons.
		{aiv1.PilotAction{Tool: "revisit", Decision: "D1-1"}, "`revisit` is not on the loose issues queue: it has no decisions to revisit"},
		{aiv1.PilotAction{Tool: "setRule", Kind: "switch", Note: "ci.maxFixRounds=2"}, "`setRule` is not on the loose issues queue: standing rules are not on the queue"},
		{aiv1.PilotAction{Tool: "forgetRule", Rule: "R1"}, "`forgetRule` is not on the loose issues queue: standing rules are not on the queue"},
		{aiv1.PilotAction{Tool: "editSpec", Spec: "new"}, "`editSpec` is not on the loose issues queue: it has no spec to edit"},
		{aiv1.PilotAction{Tool: "endMilestone"}, "`endMilestone` is not on the loose issues queue: it is no milestone and does not end"},
		{aiv1.PilotAction{Tool: "unskip", IssueIID: 421}, "`unskip` is not on the loose issues queue: a skipped issue leaves the queue, and an owner takes it again"},
		{aiv1.PilotAction{Tool: "startAnyway", IssueIID: 421}, "`startAnyway` is not on the loose issues queue: its issues have no dependencies"},
		{aiv1.PilotAction{Tool: "raiseBudget", Amount: "40"}, "`raiseBudget` is not on the loose issues queue: the pilot's budget counts per round"},
		{aiv1.PilotAction{Tool: "merge", IssueIID: 350}, "`merge` is not a tool of the pilot; merging, closing and pushing are not available"},
		// Outside the queue: finished, never taken.
		{aiv1.PilotAction{Tool: "retry", IssueIID: 46}, "#46 is not in the loose issues queue"},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 52}, "#52 is not in the loose issues queue"},
		{aiv1.PilotAction{Tool: "instruct", IssueIID: 999, Kind: kind}, "#999 is not in the loose issues queue"},
		// The queue's own checks.
		{aiv1.PilotAction{Tool: "markDone", IssueIID: 350}, "#350 is active; only a pending issue can be marked done"},
		{aiv1.PilotAction{Tool: "resume", IssueIID: 421}, "#421 is not held"},
		{aiv1.PilotAction{Tool: "resume"}, "the loose issues queue is not paused"},
		{aiv1.PilotAction{Tool: "rebase", IssueIID: 421}, "#421 is not the active issue of the queue"},
		{aiv1.PilotAction{Tool: "restartRun", IssueIID: 421}, "the run is moving again"},
	} {
		err := validAction(tc.a, qt, tasks, nil)
		if got := fmt.Sprint(err); tc.want == "" && err != nil || tc.want != "" && got != tc.want {
			t.Errorf("%s #%d: %v, want %q", tc.a.Tool, tc.a.IssueIID, err, tc.want)
		}
	}
	// Every refused tool has its reason.
	for tool := range queueRefused {
		if err := validAction(aiv1.PilotAction{Tool: tool}, qt, tasks, nil); err == nil || !strings.Contains(err.Error(), "is not on the loose issues queue: ") {
			t.Errorf("%s: %v", tool, err)
		}
	}

	paused, _ := queueFixture(aiv1.AutopilotPaused)
	for _, tc := range []struct {
		a    aiv1.PilotAction
		want string
	}{
		{aiv1.PilotAction{Tool: "pause"}, "the loose issues queue is paused already"},
		{aiv1.PilotAction{Tool: "resume"}, ""},
	} {
		if err := validAction(tc.a, paused, tasks, nil); fmt.Sprint(err) != tc.want && (tc.want != "" || err != nil) {
			t.Errorf("paused %s: %v, want %q", tc.a.Tool, err, tc.want)
		}
	}
	off, _ := queueFixture(aiv1.AutopilotOff)
	if err := validAction(aiv1.PilotAction{Tool: "retry", IssueIID: 350}, off, tasks, nil); fmt.Sprint(err) != "the loose issues queue is off" {
		t.Errorf("off: %v", err)
	}
}

func TestQueuePilotLeavesTheMilestonePilotAsItWas(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Autopilot: true, Milestone: &aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Checkout"}},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemActive}, {IID: 2, State: aiv1.AutopilotItemSkipped}}}}}
	tasks := map[int64]*aiv1.AgentTask{1: {}}
	for _, tc := range []struct {
		a    aiv1.PilotAction
		want string
	}{
		{aiv1.PilotAction{Tool: "revisit", Decision: "D1-1"}, ""},
		{aiv1.PilotAction{Tool: "endMilestone"}, ""},
		{aiv1.PilotAction{Tool: "unskip", IssueIID: 2}, ""},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 1}, ""},
		{aiv1.PilotAction{Tool: "retry", IssueIID: 1}, ""},
		{aiv1.PilotAction{Tool: "retry", IssueIID: 5}, "#5 is not an issue of this milestone with a task"},
		{aiv1.PilotAction{Tool: "resume"}, "autopilot is running"},
	} {
		if err := validAction(tc.a, st, tasks, nil); fmt.Sprint(err) != tc.want && (tc.want != "" || err != nil) {
			t.Errorf("milestone %s: %v, want %q", tc.a.Tool, err, tc.want)
		}
	}
	if got := pilotOf(st); got != `milestone "Checkout"` {
		t.Errorf("pilotOf: %s", got)
	}
}

func TestQueuePilotChecksTheIssueInGitLab(t *testing.T) {
	e, _ := newLooseRunEnv(t, 0)
	spec := e.project().Spec.WithDefaults()
	qt := e.queue()
	e.gl.Issues[421].Milestone = &gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout"}
	e.gl.AddIssue(500, "Not taken")
	e.gl.AddIssue(501, "Closed").State = "closed"
	for _, tc := range []struct {
		iid  int64
		want string
	}{
		{350, ""},
		{421, "#421 is not in the loose issues queue"},
		{500, "#500 is not in the loose issues queue"},
		{501, "#501 is closed"},
		{999, "#999 does not exist"},
	} {
		if err := checkOpenIssue(e.ctx, e.gl, spec, qt, tc.iid); fmt.Sprint(err) != tc.want && (tc.want != "" || err != nil) {
			t.Errorf("#%d: %v, want %q", tc.iid, err, tc.want)
		}
	}
}

// proposeQueue plays a run of the queue's pilot answering an event of
// #trigger with actions, made on the queue's current state.
func (e *projectEnv) proposeQueue(trigger int64, source string, actions ...aiv1.PilotAction) {
	e.t.Helper()
	tasks := map[int64]*aiv1.AgentTask{}
	for _, a := range actions {
		if t := e.implTaskOf(a.IssueIID); t != nil {
			tasks[a.IssueIID] = t
		}
	}
	p := aiv1.PilotProposal{Summary: fmt.Sprintf("help #%d", trigger), Why: "the job log says the runner timed out", Trigger: trigger,
		Source: source, Quiet: true, Actions: actions, State: aiv1.PilotState(e.queue(), tasks, actions)}
	e.patchStatus(queuePilotName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Proposed = append(s.Pilot.Proposed, p)
	})
}

// acceptQueue plays an owner accepting actions of the queue's pilot.
func (e *projectEnv) acceptQueue(by string, actions ...aiv1.PilotAction) {
	e.t.Helper()
	e.patchStatus(queuePilotName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Accepted = append(s.Pilot.Accepted, aiv1.PilotProposal{ID: 1, Summary: "as the owner asked", AcceptedBy: by, Actions: actions})
	})
}

// newQueuePilotEnv is the loose queue with #350 active and its task, #421 and
// #46 pending, the pilot's comments so far history.
func newQueuePilotEnv(t *testing.T) (*projectEnv, int64) {
	t.Helper()
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.reconcile()
	if e.implTaskOf(350) == nil {
		t.Fatal("#350 has its task")
	}
	return e, tracking
}

func TestQueuePilotManagedRetryIsCountedAndAudited(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	spec := e.project().Spec.WithDefaults()
	if spec.Pilot.AutonomyOf(e.queuePilot()) != aiv1.PilotManaged {
		t.Fatal("the queue's pilot manages, as on autopilot")
	}
	seen := len(botComments(e, tracking))
	request := e.implTaskOf(350).Spec.RunRequest
	e.proposeQueue(350, "event/stuck/350/sha@1", aiv1.PilotAction{Tool: "retry", IssueIID: 350})
	e.reconcile()
	if got := e.implTaskOf(350).Spec.RunRequest; got != request+1 {
		t.Fatalf("the retry runs on its own: request %d → %d", request, got)
	}
	notes := botNotesSince(e, tracking, seen)
	limit := spec.Pilot.MaxInterventionsPerIssue
	if len(notes) != 1 || !strings.Contains(notes[0], "The pilot acted on its own") || !strings.Contains(notes[0], "**retry** #350") ||
		!strings.Contains(notes[0], fmt.Sprintf("Interventions left for #350: %d of %d.", limit-1, limit)) {
		t.Fatalf("one audit on the tracking issue: %q", notes)
	}
	ps := e.queuePilot().Status.Pilot
	if c := ps.Interventions["350"]; c.Count != 1 || len(ps.Acted) != 1 || !slices.Equal(ps.Acted[0].Issues, []int64{350}) || !ps.Acted[0].Ran {
		t.Fatalf("counted and logged: %+v %+v", ps.Interventions, ps.Acted)
	}
	if prompts := e.queue().Status.Prompts; len(prompts) != 0 {
		t.Fatalf("what the pilot runs on its own is no prompt: %+v", prompts)
	}

	// Past the limit, the owners get a prompt on the Queue task instead.
	for i := range limit {
		e.proposeQueue(350, fmt.Sprintf("event/stuck/350/sha@%d", i+2), aiv1.PilotAction{Tool: "retry", IssueIID: 350})
		e.reconcile()
	}
	if got := e.implTaskOf(350).Spec.RunRequest; got != request+int64(limit) {
		t.Fatalf("at most %d on their own: request %d → %d", limit, request, got)
	}
	if prompts := e.queue().Status.Prompts; len(prompts) != 1 || prompts[0].Source.Kind != aiv1.PromptProposal || prompts[0].State != aiv1.PromptOpen {
		t.Fatalf("the retry past the limit is put to the owners: %+v", prompts)
	}
}

func TestQueuePilotRevisitIsRefusedWithItsReason(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	seen := len(botComments(e, tracking))
	e.proposeQueue(350, "event/stuck/350/sha@1", aiv1.PilotAction{Tool: "revisit", Decision: "D1-1", Note: "try the other runner"})
	e.reconcile()
	notes := strings.Join(botNotesSince(e, tracking, seen), "\n")
	if !strings.Contains(notes, "`revisit` is not on the loose issues queue: it has no decisions to revisit") {
		t.Fatalf("the refusal and its reason are on the tracking issue: %q", notes)
	}
	if ps := e.queuePilot().Status.Pilot; len(ps.Accepted) != 0 || len(ps.Commands) != 0 || len(e.queue().Status.Prompts) != 0 {
		t.Fatalf("nothing runs and nothing is put: %+v", ps)
	}
}

func TestQueuePilotSkipDropsTheIssue(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	e.acceptQueue("bob", aiv1.PilotAction{Tool: "skip", IssueIID: 421})
	e.reconcile() // queued for the queue
	e.reconcile() // applied as a drop
	it := e.looseItem(421)
	if it.State != aiv1.AutopilotItemLeft || it.LeftReason != "dropped by @bob via the pilot" || it.LeftAt == nil {
		t.Fatalf("#421 left the queue with the reason: %+v", it)
	}
	if !slices.ContainsFunc(botComments(e, tracking), func(b string) bool {
		return strings.Contains(b, "↩️ #421 left the queue: dropped by @bob via the pilot.")
	}) {
		t.Fatalf("the drop is logged on the tracking issue: %q", botComments(e, tracking))
	}
	if ap := e.queue().Status.Autopilot; ap.LastPilotCommand != 1 {
		t.Fatalf("the command is applied once: %d", ap.LastPilotCommand)
	}

	// The pilot on its own, as Decision 10 of the queue words it.
	e.acceptQueue("", aiv1.PilotAction{Tool: "skip", IssueIID: 46})
	e.reconcile()
	e.reconcile()
	if it := e.looseItem(46); it.State != aiv1.AutopilotItemLeft || it.LeftReason != "dropped by the pilot" {
		t.Fatalf("#46 dropped by the pilot: %+v", it)
	}
	if ap := e.queue().Status.Autopilot; ap.LastPilotCommand != 2 {
		t.Fatalf("the second command is applied once: %d", ap.LastPilotCommand)
	}
	// Applied commands go; the last keeps the sequence.
	e.acceptQueue("bob", aiv1.PilotAction{Tool: "pause"})
	e.reconcile()
	e.reconcile()
	if cmds := e.queue().Status.Pilot.Commands; len(cmds) != 2 || cmds[0].Seq != 2 || cmds[1].Seq != 3 {
		t.Fatalf("pruned on the queue's mark: %+v", cmds)
	}
	if ap := e.queue().Status.Autopilot; ap.Phase != aiv1.AutopilotPaused || ap.PausedReason != "paused by @bob via the pilot" {
		t.Fatalf("the pilot's pause is the queue's: %s %q", ap.Phase, ap.PausedReason)
	}
}

func TestQueuePilotOpenIssueStaysOutOfTheQueue(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	e.acceptQueue("bob", aiv1.PilotAction{Tool: "openIssue", Title: "Runner image lacks git", Note: "the job needs git"})
	e.reconcile()
	var opened *gitlab.Issue
	for _, is := range e.gl.Issues {
		if is.Title == "Runner image lacks git" {
			opened = is
		}
	}
	if opened == nil || opened.Milestone != nil || !strings.Contains(opened.Description, "_Opened by the pilot of the loose issues queue (") {
		t.Fatalf("the issue is opened without a milestone: %+v", opened)
	}
	if slices.ContainsFunc(e.queue().Status.Autopilot.Items, func(it aiv1.AutopilotItem) bool { return it.IID == opened.IID }) {
		t.Fatalf("not taken into the queue: %v", e.looseItems())
	}
	want := fmt.Sprintf("opened #%d, not in the queue; an owner takes it with `/agent take #%d`", opened.IID, opened.IID)
	if !slices.ContainsFunc(botComments(e, tracking), func(b string) bool { return strings.Contains(b, want) }) {
		t.Fatalf("the reply says how an owner takes it: %q", botComments(e, tracking))
	}
}

func TestQueuePilotRefusesAnIssueOutsideTheQueue(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	e.gl.AddIssue(500, "Never taken")
	e.gl.Issues[421].Milestone = &gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout"}
	e.acceptQueue("bob", aiv1.PilotAction{Tool: "markDone", IssueIID: 500}, aiv1.PilotAction{Tool: "markDone", IssueIID: 421})
	e.reconcile()
	c := strings.Join(botComments(e, tracking), "\n")
	for _, want := range []string{"(not run: #500 is not in the loose issues queue)", "(not run: #421 is not in the loose issues queue)"} {
		if !strings.Contains(c, want) {
			t.Errorf("refused: %q not in %q", want, c)
		}
	}
	for _, iid := range []int64{500, 421} {
		if e.gl.Issues[iid].State != "opened" {
			t.Errorf("#%d stays open", iid)
		}
	}
}

func TestQueuePilotTellReachesTheImplementer(t *testing.T) {
	e, tracking := newQueuePilotEnv(t)
	e.started(350)
	n := e.gl.AddNote(tracking, bob, "/pilot tell #350: use the v2 runner image")
	e.reconcile()
	if c := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(c, "🧭 Passed on to #350's running implementer") ||
		!strings.Contains(c, "Told with `/pilot tell` by `@bob`") {
		t.Fatalf("the reply says where it goes: %q", c)
	}
	msgs := e.implTaskOf(350).Spec.Source.OwnerMessages
	if len(msgs) != 1 || msgs[0].Owner != "bob" || msgs[0].Text != "use the v2 runner image" || msgs[0].ID != fmt.Sprintf("tell/%d", n.ID) {
		t.Fatalf("#350's implementer has the owner's words: %+v", msgs)
	}

	// Not a queue issue: not passed on.
	e.gl.AddIssue(500, "Never taken")
	n = e.gl.AddNote(tracking, bob, "/pilot tell #500: hello")
	e.reconcile()
	if c := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(c, "Not passed on: #500 is not in the loose issues queue.") {
		t.Fatalf("refused outside the queue: %q", c)
	}
}
