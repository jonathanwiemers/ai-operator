package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

var tierLabelsBefore = []string{"agent-profile::strong", "agent-effort::medium", "agent-model::claude/claude-sonnet-5-5", "bug"}

// withTierLabels adds tierLabelsBefore to #1 and returns its labels.
func withTierLabels(e *projectEnv) []string {
	e.gl.Issues[1].Labels = append(e.gl.Issues[1].Labels, tierLabelsBefore...)
	return slices.Clone(e.gl.Issues[1].Labels)
}

// tierLabels are the labels of #1 that withTierLabels is about, sorted:
// its profile, model and effort labels and "bug".
func tierLabels(e *projectEnv) []string {
	var out []string
	for _, l := range e.gl.Issues[1].Labels {
		if l == "bug" || strings.HasPrefix(l, "agent-profile::") || strings.HasPrefix(l, "agent-model::") || strings.HasPrefix(l, "agent-effort::") {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

func TestPilotSetTierOnAnOwnersAccept(t *testing.T) {
	e := newManagedEnv(t)
	labels := withTierLabels(e)
	before := e.toolEffect()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{Accepted: []aiv1.PilotProposal{{ID: 4, Summary: "#1 to deep", AcceptedBy: "alice",
			Actions: []aiv1.PilotAction{{Tool: "setTier", IssueIID: 1, Profile: "deep"}}}}}
	})
	e.reconcile()
	if got := tierLabels(e); !slices.Equal(got, []string{"agent-profile::deep", "bug"}) {
		t.Fatalf("the profile label is swapped, the model and effort labels are dropped: %v", got)
	}
	for _, l := range labels[:len(labels)-len(tierLabelsBefore)] {
		if !slices.Contains(e.gl.Issues[1].Labels, l) {
			t.Fatalf("the other labels stay: %q is gone from %v", l, e.gl.Issues[1].Labels)
		}
	}
	if after := e.toolEffect(); after.runs != before.runs {
		t.Fatalf("a tier change requests no run: %d → %d", before.runs, after.runs)
	}
	acted := e.pilot().Status.Pilot.Acted
	want := "#1 now runs on tier deep (claude-opus-5-5, effort high); removed agent-profile::strong, agent-effort::medium, agent-model::claude/claude-sonnet-5-5"
	if len(acted) != 1 || !acted[0].Ran || len(acted[0].Lines) != 1 || !strings.Contains(acted[0].Lines[0], "**setTier** #1 to tier deep") ||
		!strings.HasSuffix(acted[0].Lines[0], "; "+want) {
		t.Fatalf("the acted entry names the tier and the removed labels: %+v", acted)
	}
	if role, profile, _ := e.project().Spec.WithDefaults().IssueRole(e.gl.Issues[1].Labels); profile != "deep" || role.Model != "claude-opus-5-5" || role.Effort != "high" {
		t.Fatalf("the next run resolves the deep tier: %s %s·%s", profile, role.Model, role.Effort)
	}
}

func TestPilotSetTierNeverRunsOnItsOwn(t *testing.T) {
	e := newManagedEnv(t)
	labels := withTierLabels(e)
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "setTier", IssueIID: 1, Profile: "deep"})
	e.reconcile()
	if got := e.gl.Issues[1].Labels; !slices.Equal(got, labels) {
		t.Fatalf("a managing pilot does not change a tier on its own: %v", got)
	}
	if ps, prompts := e.pilot().Status.Pilot, e.task(specTaskName).Status.Prompts; len(ps.Acted) != 0 || len(prompts) != 1 ||
		!strings.Contains(fmt.Sprint(prompts[0]), "setTier") {
		t.Fatalf("it is put to the owners as a prompt, not acted on: %+v %+v", ps.Acted, prompts)
	}

	// An accepted proposal without an owner (as manage records its own) is refused.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot.Accepted = []aiv1.PilotProposal{{Summary: "#1 to deep", Trigger: 1, Actions: []aiv1.PilotAction{{Tool: "setTier", IssueIID: 1, Profile: "deep"}}}}
	})
	e.reconcile()
	acted := e.pilot().Status.Pilot.Acted
	if got := e.gl.Issues[1].Labels; !slices.Equal(got, labels) || len(acted) != 1 || acted[0].Ran ||
		!strings.Contains(acted[0].Lines[0], "not run: a tier change needs an owner's yes") {
		t.Fatalf("without an owner's yes it does not run: %v %+v", got, acted)
	}
}

func TestSetTierIsNoToolThatRunsWithoutAnAccept(t *testing.T) {
	if !slices.Contains(pilotTools, "setTier") || slices.Contains(countedTools, "setTier") || slices.Contains(chatTools, "setTier") {
		t.Fatalf("setTier is a pilot tool, neither counted nor a chat tool")
	}
}

func TestCheckActionRejectsABadSetTier(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 3}}}
	st.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 1}, {Key: "b", IID: 2}}
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemActive}, {IID: 2, State: aiv1.AutopilotItemMerged},
	}}
	tasks := map[int64]*aiv1.AgentTask{5: {}}
	for _, tc := range []struct {
		action aiv1.PilotAction
		want   string
	}{
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 1, Profile: "huge"}, "`huge` is no profile of this project (fast, balanced, strong, deep)"},
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 1}, "`` is no profile of this project"},
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 9, Profile: "deep"}, "#9 is not an issue of this milestone"},
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 2, Profile: "deep"}, "#2 is merged already"},
		{aiv1.PilotAction{Tool: "setTier", Profile: "deep"}, "setTier needs the issue"},
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 1, Profile: "deep"}, ""},
		{aiv1.PilotAction{Tool: "setTier", IssueIID: 5, Profile: "fast"}, ""}, // a task, not planned
	} {
		a := tc.action
		_, err := (&AgentProjectReconciler{}).checkAction(context.Background(), nil, &aiv1.AgentProject{}, spec, st, tasks, nil, &a, true, false)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%+v: %v", tc.action, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%+v: got %v, want %q", tc.action, err, tc.want)
		}
	}
}

func TestSetTierOnTheQueueStaysInItsScope(t *testing.T) {
	qt := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue}}
	qt.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 7, State: aiv1.AutopilotItemPending}, {IID: 8, State: aiv1.AutopilotItemMerged},
	}}
	if err := validAction(aiv1.PilotAction{Tool: "setTier", IssueIID: 7, Profile: "deep"}, qt, nil, nil); err != nil {
		t.Fatalf("a pending queue item: %v", err)
	}
	for _, iid := range []int64{8, 9} {
		if err := validAction(aiv1.PilotAction{Tool: "setTier", IssueIID: iid, Profile: "deep"}, qt, nil, nil); err == nil || !strings.Contains(err.Error(), "not in the loose issues queue") {
			t.Errorf("#%d: %v", iid, err)
		}
	}
}

func TestSetTierRefusesAClosedIssue(t *testing.T) {
	gl := gitlabfake.New()
	gl.Issues[4] = &gitlab.Issue{IID: 4, State: "closed", Labels: []string{"agent-profile::fast"}}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	if _, err := setTier(context.Background(), gl, spec, aiv1.PilotAction{Tool: "setTier", IssueIID: 4, Profile: "balanced"}); err == nil || err.Error() != "#4 is closed" {
		t.Fatalf("got %v", err)
	}
	if _, err := setTier(context.Background(), gl, spec, aiv1.PilotAction{Tool: "setTier", IssueIID: 4, Profile: "gone"}); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("a profile removed since the proposal: %v", err)
	}
	if got := gl.Issues[4].Labels; !slices.Equal(got, []string{"agent-profile::fast"}) {
		t.Fatalf("labels unchanged: %v", got)
	}
}
