package controller

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestFollowUpOfLineIsCarriedAndPlansNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	base := "/agent autopilot\nBuild a new checkout."
	e.gl.Milestones[0].Description = base
	e.reconcile()
	e.reconcile()
	planned := e.tasks()[specTaskName].Spec
	if planned.FollowUpOf != nil {
		t.Fatalf("without the line there is no value: %+v", planned.FollowUpOf)
	}

	for _, c := range []struct {
		line string
		want *aiv1.FollowUpOf
	}{
		{line: "\n\n/agent followup-of 12 3, 1 3", want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{1, 3}}},
		{line: "\n\n/agent followup-of 12 2", want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{2}}},
		{line: "\n\n/agent followup-of 12 10"}, // malformed: no value
		{line: ""},                             // removed
	} {
		e.gl.Milestones[0].Description = base + c.line
		e.reconcile()
		st := e.tasks()[specTaskName].Spec
		if !reflect.DeepEqual(st.FollowUpOf, c.want) {
			t.Fatalf("%q: the spec task carries %+v, want %+v", c.line, st.FollowUpOf, c.want)
		}
		if st.RunRequest != planned.RunRequest || st.Source.DescriptionHash != planned.Source.DescriptionHash {
			t.Fatalf("%q: the line re-plans nothing: run %d hash %s, was run %d hash %s", c.line,
				st.RunRequest, st.Source.DescriptionHash, planned.RunRequest, planned.Source.DescriptionHash)
		}
	}
}

// addMilestone adds the active milestone iid with description to the env's
// GitLab.
func addMilestone(e *projectEnv, iid int64, description string) {
	e.t.Helper()
	e.gl.Milestones = append(e.gl.Milestones, gitlab.Milestone{ID: 100 + iid, IID: iid, Title: fmt.Sprintf("Next %d", iid),
		State: "active", Description: description})
}

// specOf is the spec task of milestone iid.
func specOf(e *projectEnv, iid int64) *aiv1.AgentTask {
	e.t.Helper()
	for _, t := range e.tasks() {
		if t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Milestone != nil && t.Spec.Milestone.IID == iid {
			return &t
		}
	}
	e.t.Fatalf("no spec task of %%%d: %v", iid, keys(e.tasks()))
	return nil
}

// notices are the bot's followup-of notices on any issue.
func notices(e *projectEnv) []string {
	var out []string
	for iid := range e.gl.IssueNotes {
		for _, c := range botComments(e, iid) {
			if strings.Contains(c, "with its line `/agent followup-of") {
				out = append(out, fmt.Sprintf("#%d: %s", iid, c))
			}
		}
	}
	slices.Sort(out)
	return out
}

// takenOf is the Taken of the env's retrospective (%3).
func takenOf(e *projectEnv) map[string]int64 {
	e.t.Helper()
	return e.tasks()[specTaskName].Status.Pilot.Retrospective.Taken
}

func TestFollowUpOfOpenSourceSettlesItsFollowUps(t *testing.T) {
	e, _ := newRetroEnv(t)
	posted := metav1.NewTime(e.gl.Clock)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Retrospective.PostedAt = &posted })
	e.gl.Issues[1].State, e.gl.Issues[2].State = "closed", "closed"
	e.reconcile()
	if e.gl.Milestones[0].State == "closed" {
		t.Fatal("the follow-ups hold the milestone open")
	}

	addMilestone(e, 5, "/agent autopilot\nThe rest.\n/agent followup-of 3 1 2 3")
	e.reconcile()
	if got := takenOf(e); !maps.Equal(got, map[string]int64{"1": 5, "2": 5, "3": 5}) {
		t.Fatalf("%%5 took all three: %v", got)
	}
	want := &aiv1.FollowUpOfStatus{MilestoneIID: 3, Numbers: []int{1, 2, 3}}
	if got := specOf(e, 5).Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("taken follow-ups are settled, no week's wait: %s", e.gl.Milestones[0].State)
	}
	e.reconcile() // the closed milestone's tasks go
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatal("the closed milestone's task is deleted")
	}
	if got := specOf(e, 5).Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("what was taken stays without the source: %+v", got)
	}
	if n := notices(e); len(n) != 0 {
		t.Fatalf("all taken, no notice: %q", n)
	}
}

// A dismissed follow-up of an open source is not taken over, as of a closed
// one in the snapshot.
func TestFollowUpOfOpenSourceDismissed(t *testing.T) {
	e, retro := newRetroEnv(t)
	e.gl.AddReply(e.autopilot().TrackingIssueIID, retro.ID, alice, "create none")
	e.reconcile()
	addMilestone(e, 5, "/agent autopilot\nThe rest.\n/agent followup-of 3 1")
	e.reconcile()
	if got := specOf(e, 5).Status.FollowUpOf; got == nil || len(got.Numbers) != 0 || got.Problem != "follow-up 1 was dismissed" || len(takenOf(e)) != 0 {
		t.Fatalf("a dismissed follow-up is not taken: %+v %v", got, takenOf(e))
	}
}

func TestFollowUpOfFirstComeWins(t *testing.T) {
	e, retro := newRetroEnv(t)
	// In one poll the lower milestone wins.
	addMilestone(e, 5, "/agent autopilot\nMore.\n/agent followup-of 3 1")
	addMilestone(e, 4, "/agent autopilot\nThe rest.\n/agent followup-of 3 1 2")
	e.reconcile()
	if got := takenOf(e); !maps.Equal(got, map[string]int64{"1": 4, "2": 4}) {
		t.Fatalf("%%4 took 1 and 2: %v", got)
	}
	want := &aiv1.FollowUpOfStatus{MilestoneIID: 3, Problem: "follow-up 1 is taken by %4", Noticed: true}
	if got := specOf(e, 5).Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("%%5 took nothing: %+v, want %+v", got, want)
	}
	// A later one gets what is left.
	addMilestone(e, 6, "/agent autopilot\nLast.\n/agent followup-of 3 2 3")
	e.reconcile()
	e.reconcile()
	if got := takenOf(e); !maps.Equal(got, map[string]int64{"1": 4, "2": 4, "3": 6}) {
		t.Fatalf("%%6 took 3: %v", got)
	}
	if got := specOf(e, 6).Status.FollowUpOf; !slices.Equal(got.Numbers, []int{3}) || got.Problem != "follow-up 2 is taken by %4" {
		t.Fatalf("%%6: %+v", got)
	}
	n := notices(e)
	if len(n) != 2 || !strings.Contains(n[0]+n[1], "The milestone %5 took over nothing with its line `/agent followup-of 3 1`. Ignored: follow-up 1 is taken by %4.") ||
		!strings.Contains(n[0]+n[1], "The milestone %6 took over follow-up 3 of %3 with its line `/agent followup-of 3 2 3`. Ignored: follow-up 2 is taken by %4.") {
		t.Fatalf("one notice each: %q", n)
	}
	if got := specOf(e, 4).Status.FollowUpOf; got.Problem != "" || got.Noticed {
		t.Fatalf("%%4 has no problem: %+v", got)
	}

	// Taken follow-ups are not opened as issues.
	e.gl.AddReply(e.autopilot().TrackingIssueIID, retro.ID, alice, "create 1 3")
	e.reconcile()
	if r := threadReplies(e, retro.ID); len(r) != 1 || len(followUpIssues(e)) != 0 ||
		r[0] != "🧭 Follow-ups of the retrospective:\n- 1: taken over by the milestone %4\n- 3: taken over by the milestone %6" {
		t.Fatalf("create refuses taken follow-ups: %q", r)
	}
}

func TestFollowUpOfUnknownIsNoticedOnce(t *testing.T) {
	e, _ := newRetroEnv(t)
	addMilestone(e, 5, "/agent autopilot\nMore.\n/agent followup-of 3 1 7 8")
	for range 3 {
		e.reconcile()
	}
	want := &aiv1.FollowUpOfStatus{MilestoneIID: 3, Numbers: []int{1}, Problem: "the retrospective of %3 has no follow-ups 7, 8", Noticed: true}
	if got := specOf(e, 5).Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if n := notices(e); len(n) != 1 {
		t.Fatalf("one notice, not one per poll: %q", n)
	}

	// Another milestone, unknown without a spec task or the database: a new
	// notice, on %5's tracking issue now. What %3 gave stays given.
	e.gl.Milestones[1].Description = "/agent autopilot\nMore.\n/agent followup-of 9 2"
	for range 2 {
		e.reconcile()
	}
	want = &aiv1.FollowUpOfStatus{MilestoneIID: 9, Problem: "%9 has no retrospective the operator knows of", Noticed: true}
	if got := specOf(e, 5).Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	tracking := specOf(e, 5).Status.Autopilot.TrackingIssueIID
	if n := notices(e); len(n) != 2 || !strings.HasPrefix(n[1], fmt.Sprintf("#%d: ", tracking)) || !strings.Contains(n[1], "Ignored: %9 has no retrospective") {
		t.Fatalf("the new problem is noticed on #%d: %q", tracking, n)
	}
	if got := takenOf(e); !maps.Equal(got, map[string]int64{"1": 5}) {
		t.Fatalf("taking is not undone: %v", got)
	}

	// A malformed line: noticed once, with how to write it.
	e.gl.Milestones[1].Description = "/agent autopilot\nMore.\n/agent followup-of 3 x"
	for range 2 {
		e.reconcile()
	}
	got := specOf(e, 5).Status.FollowUpOf
	if got.MilestoneIID != 9 || !got.Noticed || !strings.HasPrefix(got.Problem, `"x" is no follow-up number (1 to 9): write `+"`/agent followup-of <milestone iid> <numbers>`") {
		t.Fatalf("malformed: %+v", got)
	}
	if n := notices(e); len(n) != 2 {
		t.Fatalf("not counted as a followup-of notice: %q", n)
	}
	if c := countComments(e, tracking, "📌 The line `/agent followup-of` of the milestone %5 is ignored: \"x\" is no follow-up number"); c != 1 {
		t.Fatalf("one notice of the malformed line, got %d: %q", c, botComments(e, tracking))
	}

	addMilestone(e, 6, "/agent autopilot\nSelf.\n/agent followup-of 6 1")
	e.reconcile()
	if got := specOf(e, 6).Status.FollowUpOf; got.Problem != "a milestone takes over no follow-ups of its own" {
		t.Fatalf("its own: %+v", got)
	}
}

// fakeFollowUps is the snapshot of closed milestones by IID.
type fakeFollowUps map[int64]store.MilestoneFollowUps

func (f fakeFollowUps) FollowUpsOf(_ context.Context, p store.ProjectKey, iid int64) (store.MilestoneFollowUps, error) {
	if m, ok := f[iid]; ok && p == (store.ProjectKey{Namespace: ns, Name: "shop"}) {
		return m, nil
	}
	return store.MilestoneFollowUps{}, store.ErrNotFound
}

func TestFollowUpOfClosedSourceFromTheSnapshot(t *testing.T) {
	e := newAutopilotEnv(t)
	e.r.FollowUps = fakeFollowUps{1: {FollowUps: []store.FollowUpRow{
		{Number: 1, Title: "Backfill", State: store.FollowUpOpen},
		{Number: 2, Title: "Docs", State: store.FollowUpIssue, TargetIID: 40},
		{Number: 3, Title: "Flaky", State: store.FollowUpDismissed},
		{Number: 4, Title: "Cleanup", State: store.FollowUpMilestone, TargetIID: 2},
		{Number: 5, Title: "Mine", State: store.FollowUpMilestone, TargetIID: 3}, // recorded already
	}}}
	e.gl.Milestones[0].Description += "\n/agent followup-of 1 1 2 3 4 5"
	for range 2 {
		e.reconcile()
	}
	want := &aiv1.FollowUpOfStatus{MilestoneIID: 1, Numbers: []int{1, 5}, Noticed: true,
		Problem: "follow-up 2 was opened as #40; follow-up 3 was dismissed; follow-up 4 is taken by %2"}
	if got := e.tasks()[specTaskName].Status.FollowUpOf; !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if n := notices(e); len(n) != 1 || !strings.HasPrefix(n[0], fmt.Sprintf("#%d: ", e.autopilot().TrackingIssueIID)) {
		t.Fatalf("one notice on the tracking issue: %q", n)
	}

	// Without the database a closed milestone is unknown, but what was taken stays.
	e.r.FollowUps = nil
	e.reconcile()
	if got := e.tasks()[specTaskName].Status.FollowUpOf; got.Problem != "%1 has no retrospective the operator knows of" || !slices.Equal(got.Numbers, []int{1, 5}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestFollowUpsSettled(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	posted := metav1.NewTime(now.Add(-time.Hour))
	two := []aiv1.PilotFollowUp{{Title: "a"}, {Title: "b"}}
	for _, c := range []struct {
		name  string
		retro aiv1.PilotRetrospective
		want  bool
	}{
		{"none", aiv1.PilotRetrospective{PostedAt: &posted}, true},
		{"open", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two}, false},
		{"one created", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}}, false},
		{"one taken", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two, Taken: map[string]int64{"2": 5}}, false},
		{"created and taken", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}, Taken: map[string]int64{"2": 5}}, true},
		{"all taken", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two, Taken: map[string]int64{"1": 5, "2": 5}}, true},
		{"a week later", aiv1.PilotRetrospective{PostedAt: &metav1.Time{Time: now.Add(-followUpWindow)}, FollowUps: two}, true},
		{"dismissed", aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}, DismissedAt: &posted}, true},
	} {
		if got := followUpsSettled(&c.retro, now); got != c.want {
			t.Errorf("%s: followUpsSettled = %v, want %v", c.name, got, c.want)
		}
	}
}
