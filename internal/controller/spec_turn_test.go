package controller

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func turnMilestone(iid int64, description string) gitlab.Milestone {
	return gitlab.Milestone{ID: 100 + iid, IID: iid, Title: fmt.Sprintf("M%d", iid), State: "active", Description: description}
}

func phaseTask(phase aiv1.AutopilotPhase) *aiv1.AgentTask {
	t := &aiv1.AgentTask{}
	if phase != "" {
		t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: phase}
	}
	return t
}

// markedTask is a spec task with a run-next mark set minutes after a fixed
// time.
func markedTask(minutes int) *aiv1.AgentTask {
	at := metav1.NewTime(time.Date(2026, 11, 13, 9, minutes, 0, 0, time.UTC))
	return &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{StartRequestedBy: "@alice", StartRequestedAt: &at}}}
}

func TestQueuedAhead(t *testing.T) {
	const ap, sp = "/agent autopilot\nBuild it.", "/agent spec\nBuild it."
	for _, tc := range []struct {
		name       string
		milestones []gitlab.Milestone
		tasks      map[int64]*aiv1.AgentTask
		want       map[int64][]int64
	}{
		{
			name:       "chain: %7 open, %9 waits, %10 waits behind both",
			milestones: []gitlab.Milestone{turnMilestone(10, ap), turnMilestone(7, ap), turnMilestone(9, ap)},
			tasks:      map[int64]*aiv1.AgentTask{7: phaseTask(aiv1.AutopilotRunning), 9: phaseTask("")},
			want:       map[int64][]int64{7: nil, 9: {7}, 10: {7, 9}},
		},
		{
			name:       "a paused older milestone counts as open",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap)},
			tasks:      map[int64]*aiv1.AgentTask{7: phaseTask(aiv1.AutopilotPaused)},
			want:       map[int64][]int64{7: nil, 9: {7}},
		},
		{
			name:       "a done older milestone holds back nothing",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap), turnMilestone(10, ap)},
			tasks:      map[int64]*aiv1.AgentTask{7: phaseTask(aiv1.AutopilotDone)},
			want:       map[int64][]int64{7: nil, 9: nil, 10: {9}},
		},
		{
			name:       "an older milestone off autopilot or not selected holds back nothing",
			milestones: []gitlab.Milestone{turnMilestone(5, "no marker"), turnMilestone(7, sp), turnMilestone(9, ap)},
			tasks:      map[int64]*aiv1.AgentTask{7: phaseTask(aiv1.AutopilotOff)},
			want:       map[int64][]int64{9: nil},
		},
		{
			name: "a closed older milestone holds back nothing",
			milestones: []gitlab.Milestone{
				{ID: 107, IID: 7, Title: "M7", State: "closed", Description: ap}, turnMilestone(9, ap),
			},
			want: map[int64][]int64{9: nil},
		},
		{
			name:       "an older milestone without a spec task yet is open",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap)},
			want:       map[int64][]int64{7: nil, 9: {7}},
		},
		{
			name:       "a marked younger milestone ranks before the older ones",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap), turnMilestone(10, ap)},
			tasks:      map[int64]*aiv1.AgentTask{7: phaseTask(aiv1.AutopilotRunning), 10: markedTask(0)},
			want:       map[int64][]int64{10: nil, 7: {10}, 9: {10, 7}},
		},
		{
			name:       "two marks keep mark order",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap), turnMilestone(10, ap)},
			tasks:      map[int64]*aiv1.AgentTask{9: markedTask(5), 10: markedTask(1)},
			want:       map[int64][]int64{10: nil, 9: {10}, 7: {10, 9}},
		},
		{
			name:       "a start without a mark time does not reorder",
			milestones: []gitlab.Milestone{turnMilestone(7, ap), turnMilestone(9, ap)},
			tasks:      map[int64]*aiv1.AgentTask{9: {Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{StartRequestedBy: "@alice"}}}},
			want:       map[int64][]int64{7: nil, 9: {7}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := queuedAhead(testProject().Spec, nil, tc.milestones, tc.tasks)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for iid, want := range tc.want {
				if g, ok := got[iid]; !ok || !slices.Equal(g, want) {
					t.Fatalf("%%%d: got %v, want %v (all: %v)", iid, g, want, got)
				}
			}
		})
	}
}

// specFor is the spec task of milestone iid.
func (e *projectEnv) specFor(iid int64) aiv1.AgentTask {
	e.t.Helper()
	for _, task := range e.tasks() {
		if task.Spec.Type == aiv1.TaskTypeSpec && task.Spec.Milestone != nil && task.Spec.Milestone.IID == iid {
			return task
		}
	}
	e.t.Fatalf("no spec task for %%%d", iid)
	return aiv1.AgentTask{}
}

func (e *projectEnv) setPhase(iid int64, phase aiv1.AutopilotPhase) {
	e.t.Helper()
	e.patchStatus(e.specFor(iid).Name, func(s *aiv1.AgentTaskStatus) {
		if s.Autopilot == nil {
			s.Autopilot = &aiv1.AutopilotStatus{}
		}
		s.Autopilot.Phase = phase
	})
}

func (e *projectEnv) lightPollChanged() bool {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	changed, err := e.r.milestonesChanged(e.ctx, &p, p.Spec)
	if err != nil {
		e.t.Fatal(err)
	}
	return changed
}

func (e *projectEnv) wantWaiting(iid int64, behind ...int64) {
	e.t.Helper()
	st := e.specFor(iid)
	if st.Spec.RunRequest != 0 || st.RunPending() || !slices.Equal(st.Spec.QueuedBehind, behind) {
		e.t.Fatalf("%%%d must wait behind %v with run request 0: %+v", iid, behind, st.Spec)
	}
}

func (e *projectEnv) wantPlanning(iid int64, description string) {
	e.t.Helper()
	st := e.specFor(iid)
	if st.Spec.RunRequest != 1 || st.Spec.QueuedBehind != nil || st.Spec.Source.DescriptionHash != hashString(description) {
		e.t.Fatalf("%%%d must ask for its first run with the current hash: %+v", iid, st.Spec)
	}
}

func TestAutopilotMilestoneWaitsForItsTurn(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(7, "/agent autopilot\nFirst.")}
	e.reconcile()
	e.wantPlanning(7, "/agent autopilot\nFirst.")

	// Two younger autopilot milestones: a chain of waiting ones.
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(9, "/agent autopilot\nSecond."), turnMilestone(10, "/agent autopilot\nThird."))
	e.reconcile()
	e.reconcile()
	e.wantWaiting(9, 7)
	e.wantWaiting(10, 7, 9)

	// An edit while waiting plans nothing; the hash stays current.
	e.gl.Milestones[1].Description = "/agent autopilot\nSecond, edited."
	e.reconcile()
	e.wantWaiting(9, 7)
	if got := e.specFor(9).Spec.Source.DescriptionHash; got != hashString(e.gl.Milestones[1].Description) {
		t.Fatalf("the waiting task keeps the hash current: %s", got)
	}

	// A paused older milestone keeps the younger ones waiting.
	e.setPhase(7, aiv1.AutopilotPaused)
	e.reconcile()
	e.wantWaiting(9, 7)
	e.wantWaiting(10, 7, 9)

	if e.lightPollChanged() {
		t.Fatal("the light poll must see nothing to do while M9 waits")
	}

	// %7 done: %9's turn comes, with the description of that moment.
	e.setPhase(7, aiv1.AutopilotDone)
	if !e.lightPollChanged() {
		t.Fatal("the light poll must see that the turn of M9 has come")
	}
	e.gl.Milestones[1].Description = "/agent autopilot\nSecond, edited again."
	e.reconcile()
	e.wantPlanning(9, "/agent autopilot\nSecond, edited again.")
	e.wantWaiting(10, 9)
	e.reconcile()
	if got := e.specFor(9).Spec.RunRequest; got != 1 {
		t.Fatalf("the first run is asked for once: run request %d", got)
	}

	// %9 closed (no longer listed): %10's turn comes.
	e.gl.Milestones = []gitlab.Milestone{e.gl.Milestones[0], e.gl.Milestones[2]}
	e.reconcile()
	e.wantPlanning(10, "/agent autopilot\nThird.")
}

func TestOlderMilestoneOffAutopilotStartsTheNext(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(7, "/agent autopilot\nFirst."), turnMilestone(9, "/agent autopilot\nSecond.")}
	e.reconcile()
	e.wantWaiting(9, 7)

	e.gl.Milestones[0].Description = "/agent spec\nFirst."
	e.reconcile()
	e.wantPlanning(9, "/agent autopilot\nSecond.")
}

func TestWaitingMilestoneLeavingAutopilotPlans(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(7, "/agent autopilot\nFirst."), turnMilestone(9, "/agent autopilot\nSecond.")}
	e.reconcile()
	e.wantWaiting(9, 7)

	// Without autopilot it has no queue, an owner reviews it (decision 12).
	e.gl.Milestones[1].Description = "/agent spec\nSecond."
	e.reconcile()
	e.wantPlanning(9, "/agent spec\nSecond.")
}

func TestSpecMilestoneWithoutAutopilotPlansOnCreation(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(7, "/agent autopilot\nFirst."), turnMilestone(9, "/agent spec\nSecond.")}
	e.reconcile()
	e.wantPlanning(9, "/agent spec\nSecond.")
}

func TestRolloutLeavesSpecTasksThatAskedForARun(t *testing.T) {
	e := newProjectEnv(t)
	// Both planned before this change: each asked for its first run.
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(9, "/agent autopilot\nSecond.")}
	e.reconcile()
	e.gl.Milestones = append([]gitlab.Milestone{turnMilestone(7, "/agent autopilot\nFirst.")}, e.gl.Milestones...)
	e.reconcile()
	before := e.specFor(9)
	if before.Spec.RunRequest != 1 {
		t.Fatalf("setup: %+v", before.Spec)
	}

	e.reconcile()
	after := e.specFor(9)
	if !reflect.DeepEqual(after.Spec, before.Spec) || after.Spec.QueuedBehind != nil || after.Spec.RunRequest != 1 {
		t.Fatalf("a task that asked for a run is not touched: %+v", after.Spec)
	}

	// A description change re-plans as today.
	e.gl.Milestones[1].Description += "\nMore."
	e.reconcile()
	if st := e.specFor(9); st.Spec.RunRequest != 2 || st.Spec.QueuedBehind != nil {
		t.Fatalf("a planned milestone re-plans on an edit: %+v", st.Spec)
	}
}
