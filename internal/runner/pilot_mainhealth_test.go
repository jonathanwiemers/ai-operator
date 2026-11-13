package runner

import (
	"context"
	"fmt"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestMainHealthOptions(t *testing.T) {
	flaky := aiv1.PilotEvent{Key: "flaky/901/40", IssueIID: 40}
	red := aiv1.PilotEvent{Key: "main-red/12", IssueIID: 12, PipelineID: 900}
	for _, c := range []struct {
		name           string
		e              aiv1.PilotEvent
		classification string
		failures       int
		queue          bool
		want           string
	}{
		{"flaky twice", flaky, "flaky", 2, true, "Hand #40 to the agent+* | Ignore this occurrence | Pause the autopilot+"},
		// Nothing to retry on a green pipeline: ignoring it is the advice,
		// handing it over stays the owner's choice.
		{"flaky once", flaky, "flaky", 1, false, "Hand #40 to the agent+ | Ignore this occurrence*"},
		{"flaky called a regression", flaky, "regression", 1, false, "Hand #40 to the agent+* | Ignore this occurrence"},
		{"flaky pipeline without an issue", aiv1.PilotEvent{Key: "flaky/901", PipelineID: 901}, "flaky", 3, true, "Ignore this occurrence* | Pause the autopilot+"},
		{"red regression", red, "regression", 0, true, "Tell #12's agent: a regression+* | Retry the failed jobs+ | Ignore this occurrence | Pause the autopilot+"},
		{"red flaky", red, "flaky", 2, false, "Tell #12's agent: a flaky test+ | Retry the failed jobs+* | Ignore this occurrence"},
		{"red infra", red, "infra", 0, false, "Tell #12's agent: an infrastructure failure+ | Retry the failed jobs+* | Ignore this occurrence"},
		{"red env", red, "env", 0, false, "Tell #12's agent: the environment+ | Retry the failed jobs+* | Ignore this occurrence"},
		{"red decision", red, "decision", 0, false, "Tell #12's agent: the failing jobs+ | Retry the failed jobs+ | Ignore this occurrence*"},
		{"red unknown", red, "unknown", 0, false, "Tell #12's agent: the failing jobs+ | Retry the failed jobs+ | Ignore this occurrence*"},
		{"red without a pipeline", aiv1.PilotEvent{Key: "main-red/12", IssueIID: 12}, "infra", 0, false, "Tell #12's agent: an infrastructure failure+ | Ignore this occurrence*"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := PilotAssessment{Classification: c.classification, Evidence: "`rust:test` panicked"}
			options, _ := assessOptions(mainHealthOptions(c.e, a, c.failures, c.queue))
			if got := labels(options); got != c.want {
				t.Fatalf("options:\n got %s\nwant %s", got, c.want)
			}
			for _, o := range options {
				for _, x := range o.Actions {
					if want := map[string]aiv1.PilotAction{
						"fixFlaky":      {Tool: "fixFlaky", IssueIID: 40},
						"instruct":      {Tool: "instruct", IssueIID: 12, Kind: "ci-fix", Note: a.Evidence},
						"retryPipeline": {Tool: "retryPipeline", PipelineID: 900},
						"pause":         {Tool: "pause"},
					}[x.Tool]; x != want {
						t.Fatalf("action of %q: %+v", o.Label, x)
					}
				}
			}
		})
	}
}

// Orbit #406: a flaky rust:test on main. The operator's options are put to
// the owners, not the model's; the failures on the flaky issue decide
// whether handing it over is recommended.
func TestPilotFlakyEventIsAMainHealthPrompt(t *testing.T) {
	for _, failures := range []int{1, 2} {
		t.Run(fmt.Sprintf("failures=%d", failures), func(t *testing.T) {
			e := newAssessEnv(t, "")
			e.gl.AddIssue(40, "🔁 Flaky CI job: `rust:test`", "ci::flaky")
			for i := range failures {
				e.gl.AddNote(40, e.gl.Bot, fmt.Sprintf("<!-- ai-operator:flaky-occurrence=%d -->\n🔁 `rust:test` failed", 900+i))
			}
			// Not the bot's: counts for nothing.
			e.gl.AddNote(40, gitlab.User{ID: 8, Username: "mallory"}, "<!-- ai-operator:flaky-occurrence=999 -->")
			task := e.task(t)
			task.Spec.Autopilot = true
			task.Spec.Source.PilotEvents = []aiv1.PilotEvent{{Key: "flaky/901/40", IssueIID: 40, Request: 1,
				Text: "`rust:test` of the default branch passed only on retry (flaky issue #40)."}}
			if err := e.k8s.Update(context.Background(), &task); err != nil {
				t.Fatal(err)
			}
			e.engine.assessment.Classification = "flaky"
			e.step(t)
			p := e.proposed(t)
			want := "Hand #40 to the agent+ | Ignore this occurrence* | Pause the autopilot+"
			if failures == 2 {
				want = "Hand #40 to the agent+* | Ignore this occurrence | Pause the autopilot+"
			}
			if p.Kind != aiv1.PromptMainHealth || p.Trigger != 40 || p.Question != "A job of the default branch passed only on retry (flaky issue #40): what now?" {
				t.Fatalf("the prompt's shape: %+v", p)
			}
			if got := labels(p.Options); got != want {
				t.Fatalf("options:\n got %s\nwant %s", got, want)
			}
		})
	}
}
