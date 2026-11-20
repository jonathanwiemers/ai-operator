package history

import (
	"slices"
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func only(es []store.Event, ks ...string) []store.Event {
	var out []store.Event
	for _, e := range es {
		if slices.Contains(ks, e.Kind) {
			out = append(out, e)
		}
	}
	return out
}

func milestoneTask(rv string, mut func(*aiv1.AgentTask)) *aiv1.AgentTask {
	return task(rv, func(t *aiv1.AgentTask) {
		t.Spec.Type, t.Spec.Issue = aiv1.TaskTypeSpec, nil
		t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 7}
		if mut != nil {
			mut(t)
		}
	})
}

func held(iid int64, since time.Time, reason string) aiv1.AutopilotItem {
	return aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemActive,
		Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: reason, Since: metav1.Time{Time: since}}}
}

// A held issue opens a problem with the hold's reason; the hold that ends
// resolves it, by the operator when nobody answered or resumed.
func TestDifferProblemOpensAndResolves(t *testing.T) {
	since := now.Add(-5 * time.Minute).Truncate(time.Second)
	clear := milestoneTask("1", func(t *aiv1.AgentTask) {
		t.Status.Autopilot.Items = []aiv1.AutopilotItem{{IID: 5, State: aiv1.AutopilotItemActive}}
	})
	stuck := milestoneTask("2", func(t *aiv1.AgentTask) {
		t.Status.Autopilot.Items = []aiv1.AutopilotItem{held(5, since, "CI of !9\nis red")}
	})

	es := only(NewDiffer().Update(clear, stuck, now), KindProblemOpened, KindProblemResolved, KindHoldSet)
	if got := kinds(es); !slices.Equal(got, []string{
		"hold.set task/u1/hold/5/" + itoa(since.Unix()) + "/hold.set",
		"problem.opened task/u1/problem/5/problem.opened/2",
	}) {
		t.Fatalf("open: %v", got)
	}
	if e := es[1]; e.IssueIID != 5 || e.MilestoneIID != 20 || e.Data["cause"] != "held (ciRed): CI of !9 is red" {
		t.Errorf("opened %+v", e)
	}
	if e := es[0]; !e.At.Equal(since) || e.Data["holdKind"] != "ciRed" {
		t.Errorf("hold.set %+v", e)
	}

	clear.ResourceVersion = "3"
	es = only(NewDiffer().Update(stuck, clear, now), KindProblemOpened, KindProblemResolved, KindHoldCleared)
	if got := kinds(es); !slices.Equal(got, []string{
		"hold.cleared task/u1/hold/5/" + itoa(since.Unix()) + "/hold.cleared",
		"problem.resolved task/u1/problem/5/problem.resolved/3",
	}) {
		t.Fatalf("resolve: %v", got)
	}
	if es[1].Data["resolvedBy"] != "operator" {
		t.Errorf("resolved %+v", es[1])
	}

	// No change, no events; a task that is not a spec task has no problems.
	if es := only(NewDiffer().Update(stuck, stuck, now), KindProblemOpened, KindProblemResolved); len(es) != 0 {
		t.Errorf("same state: %v", kinds(es))
	}
	impl := stuck.DeepCopy()
	impl.Spec.Type = aiv1.TaskTypeImplement
	if es := problems(impl, clear, impl, now); len(es) != 0 {
		t.Errorf("implement task: %v", kinds(es))
	}
}

// An answered question resolves its problem by the owner who answered; a
// pause over the whole milestone is problem 0, resumed by its resolver.
func TestDifferProblemResolvedBy(t *testing.T) {
	q := aiv1.Prompt{ID: "p1", State: aiv1.PromptOpen, Question: "Retry or skip?",
		Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 5}}
	asked := milestoneTask("1", func(t *aiv1.AgentTask) { t.Status.Prompts = []aiv1.Prompt{q} })
	answered := milestoneTask("2", func(t *aiv1.AgentTask) {
		a := q
		a.State, a.Answer = aiv1.PromptAnswered, &aiv1.PromptAnswer{OptionID: "retry", By: "jonathan"}
		t.Status.Prompts = []aiv1.Prompt{a}
	})
	es := only(NewDiffer().Update(milestoneTask("0", nil), asked, now), KindProblemOpened)
	if len(es) != 1 || es[0].IssueIID != 5 || es[0].Data["cause"] != "question: Retry or skip?" {
		t.Fatalf("asked: %+v", es)
	}
	es = only(NewDiffer().Update(asked, answered, now), KindProblemResolved)
	if len(es) != 1 || es[0].Data["resolvedBy"] != "jonathan" {
		t.Fatalf("answered: %+v", es)
	}

	paused := milestoneTask("3", func(t *aiv1.AgentTask) {
		ap := t.Status.Autopilot
		ap.Phase, ap.PauseKind, ap.PausedReason = aiv1.AutopilotPaused, aiv1.PauseBudget, "budget of $20 spent"
		ap.PausedAt = &metav1.Time{Time: now}
	})
	es = only(NewDiffer().Update(milestoneTask("2", nil), paused, now), KindProblemOpened)
	if len(es) != 1 || es[0].IssueIID != 0 || es[0].Data["cause"] != "paused (budget): budget of $20 spent" {
		t.Fatalf("paused: %+v", es)
	}
	resumed := milestoneTask("4", func(t *aiv1.AgentTask) { t.Status.Autopilot.ResumedBy = aiv1.ResolvedByPilot })
	es = only(NewDiffer().Update(paused, resumed, now), KindProblemResolved)
	if len(es) != 1 || es[0].IssueIID != 0 || es[0].Data["resolvedBy"] != "pilot" {
		t.Fatalf("resumed: %+v", es)
	}
}

// A hold that restarts (a new since) clears the old and sets the new one.
func TestDifferHoldRestarts(t *testing.T) {
	a, b := now.Add(-time.Hour).Truncate(time.Second), now.Add(-time.Minute).Truncate(time.Second)
	old := milestoneTask("1", func(t *aiv1.AgentTask) { t.Status.Autopilot.Items = []aiv1.AutopilotItem{held(5, a, "red")} })
	cur := milestoneTask("2", func(t *aiv1.AgentTask) { t.Status.Autopilot.Items = []aiv1.AutopilotItem{held(5, b, "red again")} })
	es := only(NewDiffer().Update(old, cur, now), KindHoldSet, KindHoldCleared, KindProblemOpened)
	if got := kinds(es); !slices.Equal(got, []string{
		"hold.cleared task/u1/hold/5/" + itoa(a.Unix()) + "/hold.cleared",
		"hold.set task/u1/hold/5/" + itoa(b.Unix()) + "/hold.set",
	}) {
		t.Fatalf("restart: %v", got)
	}
}

// Each new pilot act is one event per issue it names (0 when none); acts
// already in the old status are not repeated.
func TestDifferPilotActed(t *testing.T) {
	at1, at2 := metav1.NewTime(now.Add(-time.Hour)), metav1.NewTime(now)
	first := aiv1.PilotActed{At: at1, Summary: "retried CI", Issues: []int64{5}, Ran: true}
	second := aiv1.PilotActed{At: at2, By: "jonathan", Summary: "skipped #5 and #6", Why: "owner said so",
		Issues: []int64{6, 5, 6}, Ran: true}
	third := aiv1.PilotActed{At: at2, Summary: "resumed the milestone"}
	coord := func(rv string, acts ...aiv1.PilotActed) *aiv1.AgentTask {
		return task(rv, func(t *aiv1.AgentTask) {
			t.Spec.Type, t.Spec.Issue = aiv1.TaskTypeCoordinate, nil
			t.Status.Pilot = &aiv1.PilotStatus{Acted: acts}
		})
	}
	es := only(NewDiffer().Update(coord("1", first), coord("2", first, second, third), now), KindPilotActed)
	var got []int64
	for _, e := range es {
		got = append(got, e.IssueIID)
	}
	if !slices.Equal(got, []int64{5, 6, 0}) {
		t.Fatalf("issues %v (%v)", got, kinds(es))
	}
	if e := es[0]; e.Summary != "jonathan (via pilot): skipped #5 and #6" || e.Data["why"] != "owner said so" ||
		!e.At.Equal(now) || e.Actor != "jonathan" || e.MilestoneIID != 20 {
		t.Errorf("acted %+v", e)
	}
	if e := es[2]; e.Summary != "Pilot: resumed the milestone (did not run)" {
		t.Errorf("not run %+v", e)
	}
	if es[0].Key == es[2].Key {
		t.Errorf("same key for two acts at one time: %s", es[0].Key)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
