package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func (e *implementEnv) status(t *testing.T) aiv1.RunStatus {
	t.Helper()
	var got aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got); err != nil {
		t.Fatal(err)
	}
	return got.Status.Run
}

// A run that changes nothing on a merge request without a pushed head
// leaves the issue in needs-input, never in review: the merge gate would
// only find it there and blame the configuration.
func TestFirstRunWithoutChangesStaysOutOfReview(t *testing.T) {
	for _, autopilot := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "autopilot"}[autopilot], func(t *testing.T) {
			e := newImplementEnv(t)
			e.configure(t, func(p *aiv1.AgentProjectSpec) { p.CI.Disabled = true }, func(s *aiv1.AgentTaskSpec) {
				s.Owners, s.Autopilot = []string{"alice"}, autopilot
			})
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, quietEngine{}, e.task.Name)
			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := e.gl.Labels(42); !slices.Equal(got, []string{"agent::needs-input"}) {
				t.Fatalf("the issue must not enter review: %v", got)
			}
			run := e.status(t)
			if run.NeedsInputCause != aiv1.NeedsInputNothingPushed || run.PushedSHA != "" || run.LastResult != aiv1.RunResultSucceeded {
				t.Fatalf("the cause is recorded on a succeeded run: %+v", run)
			}
			mrNotes := e.gl.MRNotes[1]
			report := mrNotes[len(mrNotes)-1]
			if run.NeedsInputURL != fmt.Sprintf("%s#note_%d", e.gl.MRs[1].WebURL, report.ID) {
				t.Fatalf("the cause links the MR report %d: %q", report.ID, run.NeedsInputURL)
			}
			const want = "This run pushed no commits to !1, so #42 stays out of review: the agent changed nothing. Comment `/agent …` with what to change, or close the issue."
			if !strings.Contains(report.Body, want) || strings.Contains(report.Body, "@alice") {
				t.Fatalf("the MR report says it, without a mention:\n%s", report.Body)
			}
			notes := slices.DeleteFunc(slices.Clone(e.gl.IssueNotes[42]), func(n gitlab.Note) bool { return strings.Contains(n.Body, "Working on this in") })
			if len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "🤖 "+want) {
				t.Fatalf("one more issue comment says it: %v", notes)
			}
			if mentioned := strings.Contains(notes[0].Body, "@alice"); mentioned == autopilot {
				t.Fatalf("the owners are mentioned only off autopilot (autopilot %v):\n%s", autopilot, notes[0].Body)
			}
			if !e.gl.MRs[1].Draft {
				t.Fatal("the MR stays a draft")
			}
		})
	}
}

// A run that pushed, and a later run on that MR that changes nothing (an
// answer to a question, say), go to review as before.
func TestRerunWithoutChangesAfterAPushGoesToReview(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.CI.Disabled = true }, func(*aiv1.AgentTaskSpec) {})
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	pushed := e.status(t).PushedSHA
	if got := e.gl.Labels(42); pushed == "" || !slices.Equal(got, []string{"agent::review"}) {
		t.Fatalf("a run that pushed goes to review: %v, pushed %q", got, pushed)
	}
	e.gl.SetLabels(42, "agent::working")
	e.rerun(t, r) // writes the same file again: nothing changes
	run := e.status(t)
	if got := e.gl.Labels(42); !slices.Equal(got, []string{"agent::review"}) || run.NeedsInputCause != "" || run.PushedSHA != pushed {
		t.Fatalf("a rerun on a pushed head goes to review: %v, %+v", got, run)
	}
	if notes := e.gl.IssueNotes[42]; slices.ContainsFunc(notes, func(n gitlab.Note) bool { return strings.Contains(n.Body, "pushed no commits") }) {
		t.Fatalf("no nothing-pushed comment: %v", notes)
	}
}
