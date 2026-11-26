package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const queuePilotName = "shop-loose-issues-pilot"

func (e *projectEnv) queuePilot() *aiv1.AgentTask {
	e.t.Helper()
	coord, ok := e.tasks()[queuePilotName]
	if !ok {
		e.t.Fatal("no pilot of the loose issues queue")
	}
	return &coord
}

func TestQueuePilotFollowsTheQueue(t *testing.T) {
	e := newLooseEnv(t, nil)
	e.reconcile()
	tracking := e.queue().Status.Autopilot.TrackingIssueIID
	coord := e.queuePilot()
	if coord.Spec.Type != aiv1.TaskTypeCoordinate || coord.Spec.QueueTask != queueTaskName || coord.Spec.Milestone != nil ||
		coord.Spec.Issue == nil || coord.Spec.Issue.IID != tracking || !coord.Spec.Autopilot || len(coord.Spec.Rules) != 0 ||
		!slices.Equal(coord.Spec.Owners, []string{"bob"}) || !slices.Equal(coord.Spec.OwnerSubjects, []string{"alice@example.com"}) ||
		coord.Spec.RunRequest != 0 {
		t.Fatalf("queue pilot: %+v", coord.Spec)
	}

	// The owners follow the queue's.
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.LooseIssues.Owners = []string{"carol"} })
	e.reconcile()
	if coord := e.queuePilot(); !slices.Equal(coord.Spec.Owners, []string{"carol"}) {
		t.Fatalf("owners follow: %+v", coord.Spec.Owners)
	}

	// Switched off, the pilot goes.
	e.setLoose(false)
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[queuePilotName]; ok {
		t.Fatal("the queue's pilot stays while the queue is off")
	}
}

func TestQueuePilotNoneWhileDisabled(t *testing.T) {
	e := newProjectEnv(t)
	e.reconcile()
	e.reconcile()
	for name, task := range e.tasks() {
		if task.Spec.Type == aiv1.TaskTypeCoordinate {
			t.Fatalf("no pilot while looseIssues is off: %s", name)
		}
	}
}

func TestQueuePilotWakesForAHeldItem(t *testing.T) {
	e := newLooseEnv(t, nil)
	e.reconcile()
	e.gl.AddIssue(6, "Held one", "agent::working")
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items = []aiv1.AutopilotItem{{IID: 6, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI is red"}}}
	})
	e.reconcile()
	e.reconcile()
	coord := e.queuePilot()
	var keys []string
	for _, ev := range coord.Spec.Source.PilotEvents {
		keys = append(keys, ev.Key)
	}
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "stuck/6/") || coord.Spec.RunRequest != 1 ||
		coord.Status.Pilot == nil || !slices.Equal(coord.Status.Pilot.Events, keys) {
		t.Fatalf("one wake for the held item: %v (request %d) %+v", keys, coord.Spec.RunRequest, coord.Status.Pilot)
	}
}

// queuePilotKeys are the keys of the events handed to the queue's pilot.
func (e *projectEnv) queuePilotKeys() []string {
	e.t.Helper()
	var keys []string
	for _, ev := range e.queuePilot().Spec.Source.PilotEvents {
		keys = append(keys, ev.Key)
	}
	return keys
}

func TestQueuePilotWakesForABlockAndAStall(t *testing.T) {
	e, _ := newLooseRunEnv(t, 0)
	e.reconcile()
	e.gl.SetLabels(350, "agent::working")
	e.reconcile()
	e.ended(350, e.gl.Clock, aiv1.RunResultFailed, aiv1.BlockInfra)
	e.reconcile()
	e.reconcile()
	if keys := e.queuePilotKeys(); len(keys) != 1 || !strings.HasPrefix(keys[0], "infra/350/") {
		t.Fatalf("one wake for the blocked queue item: %v", keys)
	}
	e.patchStatus(e.taskOf(350), func(s *aiv1.AgentTaskStatus) {
		s.Conditions = []metav1.Condition{{Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue, Reason: aiv1.StallReasonSilent,
			LastTransitionTime: metav1.NewTime(e.gl.Clock)}}
	})
	e.reconcile()
	keys := e.queuePilotKeys()
	if !slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, aiv1.RunStalledEventPrefix+"350/") }) {
		t.Fatalf("a stalled queue run wakes the queue's pilot: %v", keys)
	}
	// The default branch's health stays with the lane's pilot.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 50, PipelineID: 900, FailedJobs: []string{"test"}})
	e.reconcile()
	if got := e.queuePilotKeys(); slices.ContainsFunc(got, func(k string) bool { return strings.HasPrefix(k, aiv1.MainRedEventPrefix) }) {
		t.Fatalf("main red does not wake the queue's pilot: %v", got)
	}
}

func TestQueuePilotScope(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	spec := testProject().Spec
	blocked := func(sha string) *aiv1.AgentTask {
		return &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement},
			Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: sha}, Blocked: &aiv1.BlockedStatus{Reason: aiv1.BlockInfra, Message: "node gone"}}}
	}
	tasks := map[int64]*aiv1.AgentTask{6: blocked("a"), 7: blocked("b"), 8: blocked("c")}
	issues := map[int64]gitlab.Issue{6: {IID: 6}, 7: {IID: 7, Milestone: &gitlab.Milestone{IID: 3}}, 8: {IID: 8}}
	qt := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue}, Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{
		Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{{IID: 6, State: aiv1.AutopilotItemActive}, {IID: 8, State: aiv1.AutopilotItemMerged}}}}}
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 3}, Autopilot: true}}
	mh := &aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 50}
	keys := func(evs []aiv1.PilotEvent) []string {
		var out []string
		for _, ev := range evs {
			out = append(out, ev.Key)
		}
		return out
	}
	// The queue: its unfinished item only; not the milestone's issue, not a
	// finished item, not main red.
	if got := keys(pilotEvents(spec, mh, qt, issues, tasks, false, nil, now)); !slices.Equal(got, []string{"infra/6/a"}) {
		t.Errorf("queue pilot events: %v", got)
	}
	// The milestone's pilot gets what it got before.
	if got := keys(pilotEvents(spec, mh, st, issues, tasks, true, nil, now)); !slices.Equal(got, []string{"infra/7/b", "main-red/50"}) {
		t.Errorf("milestone pilot events: %v", got)
	}
}

func TestQueuePilotRefusesActions(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.reconcile()
	e.patchStatus(queuePilotName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{Accepted: []aiv1.PilotProposal{{ID: 1, Summary: "retry #350", AcceptedBy: "bob",
			Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 350}}}}}
	})
	e.reconcile()
	if ps := e.queuePilot().Status.Pilot; ps == nil || len(ps.Accepted) != 0 {
		t.Fatalf("the accepted proposal is handled once: %+v", ps)
	}
	if !slices.ContainsFunc(e.gl.IssueNotes[tracking], func(n gitlab.Note) bool { return strings.Contains(n.Body, "actions need a milestone") }) {
		t.Fatalf("the refusal is on the tracking issue: %+v", e.gl.IssueNotes[tracking])
	}
}

func TestQueuePilotListensOnTheTrackingIssueAndTheQueuesIssues(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.gl.SetLabels(350, "agent::working")
	e.reconcile() // the pilot, the task of #350
	e.reconcile() // the comments so far are history
	before := e.queuePilot().Spec.RunRequest

	n := e.gl.AddNote(tracking, bob, "/pilot why is #421 held?")
	e.reconcile()
	coord := e.queuePilot()
	if msgs := coord.Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != n.ID || msgs[0].IssueIID != tracking ||
		msgs[0].Body != "why is #421 held?" || msgs[0].Author != "bob" || coord.Spec.RunRequest != before+1 || msgs[0].Request != coord.Spec.RunRequest {
		t.Fatalf("the owner's question on the tracking issue goes to the queue's pilot: %+v (request %d)", msgs, coord.Spec.RunRequest)
	}

	n = e.gl.AddNote(350, bob, "/pilot why is #350 slow?")
	e.reconcile()
	if msgs := e.queuePilot().Spec.Source.PilotMessages; !slices.ContainsFunc(msgs, func(m aiv1.PilotMessage) bool { return m.NoteID == n.ID && m.IssueIID == 350 }) {
		t.Fatalf("a question on a queue issue goes to the queue's pilot: %+v", msgs)
	}

	// Not an owner: the usual rejection, and nothing for the pilot.
	request := e.queuePilot().Spec.RunRequest
	e.gl.AddNote(tracking, mallory, "/pilot why?")
	e.gl.AddNote(350, mallory, "/pilot why?")
	e.reconcile()
	if e.queuePilot().Spec.RunRequest != request {
		t.Fatal("a non-owner's comment wakes nobody")
	}
	for _, iid := range []int64{tracking, 350} {
		if c := strings.Join(botComments(e, iid), "\n"); !strings.Contains(c, "Only the owners of the loose issues queue can instruct me.") {
			t.Fatalf("a non-owner is rejected on #%d: %q", iid, c)
		}
	}
}

func TestQueuePilotAnswersCoordinateAndRules(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.reconcile()
	request := e.queuePilot().Spec.RunRequest
	for _, msg := range []string{"/pilot coordinate", "/pilot rules", "/pilot forget R1"} {
		e.gl.AddNote(tracking, bob, msg)
	}
	e.reconcile()
	c := strings.Join(botComments(e, tracking), "\n")
	if !strings.Contains(c, fmt.Sprintf("The pilot of the loose issues queue listens on #%d.", tracking)) ||
		strings.Count(c, "Standing rules are not on the loose issues queue") != 2 {
		t.Fatalf("coordinate names the tracking issue, rules are refused: %q", c)
	}
	if coord := e.queuePilot(); coord.Spec.RunRequest != request || len(coord.Spec.Source.PilotMessages) != 0 {
		t.Fatalf("answered by the operator, not the pilot: %+v", coord.Spec)
	}
}
