package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The answer's and the assessment's release decision is optional, an object
// with issue, release and why, nothing else.
func TestPilotSchemasHaveAnOptionalReleaseDecision(t *testing.T) {
	for name, schema := range map[string]string{"pilot": pilotSchema, "assess": assessSchema} {
		var s struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Required             []string        `json:"required"`
				AdditionalProperties *bool           `json:"additionalProperties"`
				Properties           json.RawMessage `json:"properties"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(schema), &s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rel, ok := s.Properties["release"]
		if !ok || slices.Contains(s.Required, "release") {
			t.Fatalf("%s: an optional release property: %+v", name, s)
		}
		var props map[string]any
		if err := json.Unmarshal(rel.Properties, &props); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rel.Required, []string{"issue", "release", "why"}) || rel.AdditionalProperties == nil || *rel.AdditionalProperties || len(props) != 3 {
			t.Fatalf("%s: release needs issue, release and why only: %+v", name, rel)
		}
	}
}

// An answer's release decision lands in status.pilot.releaseDecisions with
// the next Seq, its reason cut to one line; the newest 20 are kept, and a
// source is recorded once.
func TestPilotRecordsAReleaseDecisionOfAnAnswer(t *testing.T) {
	e := newPilotEnv(t, "")
	ctx := context.Background()
	task := e.task(t)
	task.Status.Pilot = &aiv1.PilotStatus{}
	for i := range aiv1.MaxPilotReleaseDecisions {
		task.Status.Pilot.ReleaseDecisions = append(task.Status.Pilot.ReleaseDecisions,
			aiv1.PilotReleaseDecision{Seq: int64(i + 1), IssueIID: 1, Release: true, Why: "old", Source: fmt.Sprintf("note/%d", i)})
	}
	if err := e.k8s.Status().Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.engine.release = &ReleaseAnswer{Issue: 1, Release: true, Why: "  the new API is what users see\n/merge"}
	e.step(t)
	ds := e.task(t).Status.Pilot.ReleaseDecisions
	if len(ds) != aiv1.MaxPilotReleaseDecisions || ds[0].Seq != 2 {
		t.Fatalf("the newest %d are kept: %+v", aiv1.MaxPilotReleaseDecisions, ds)
	}
	d := ds[len(ds)-1]
	if d.Seq != 21 || d.IssueIID != 1 || !d.Release || d.Why != "the new API is what users see" || d.Source != "note/2000" || d.At.IsZero() {
		t.Fatalf("recorded decision: %+v", d)
	}
	if replies := botNotes(e.gl, 5); len(replies) != 1 || strings.Contains(replies[0], "could not be recorded") {
		t.Fatalf("the reply: %q", replies)
	}

	// The same source again records nothing; another one the next Seq.
	cur := e.task(t)
	drop := &ReleaseAnswer{Issue: 1, Release: false, Why: "not yet"}
	if err := e.r.recordReleaseDecision(ctx, &cur, drop, "note/2000"); err != nil {
		t.Fatal(err)
	}
	if ds := e.task(t).Status.Pilot.ReleaseDecisions; ds[len(ds)-1].Seq != 21 {
		t.Fatalf("a source is recorded once: %+v", ds[len(ds)-1])
	}
	if err := e.r.recordReleaseDecision(ctx, &cur, drop, "note/2001"); err != nil {
		t.Fatal(err)
	}
	if ds := e.task(t).Status.Pilot.ReleaseDecisions; ds[len(ds)-1].Seq != 22 || ds[len(ds)-1].Release {
		t.Fatalf("the next decision: %+v", ds[len(ds)-1])
	}
}

// An answer without a release decision records none.
func TestPilotAnswerWithoutReleaseRecordsNone(t *testing.T) {
	e := newPilotEnv(t, "")
	e.step(t)
	if ps := e.task(t).Status.Pilot; ps != nil && len(ps.ReleaseDecisions) != 0 {
		t.Fatalf("no decision: %+v", ps.ReleaseDecisions)
	}
}

// An assessment's release decision is recorded too, with its event as the
// source.
func TestPilotRecordsAReleaseDecisionOfAnAssessment(t *testing.T) {
	e := newAssessEnv(t, "")
	e.engine.assessment.Release = &ReleaseAnswer{Issue: 1, Release: false, Why: "#1 is not ready for users"}
	e.step(t)
	ps := e.task(t).Status.Pilot
	if ps == nil || len(ps.ReleaseDecisions) != 1 {
		t.Fatalf("one decision: %+v", ps)
	}
	if d := ps.ReleaseDecisions[0]; d.Seq != 1 || d.IssueIID != 1 || d.Release || d.Why != "#1 is not ready for users" || d.Source != "event/stuck/1/sha3@1" {
		t.Fatalf("recorded decision: %+v", d)
	}
	if len(ps.Proposed) != 1 {
		t.Fatalf("the assessment's prompt is recorded as before: %+v", ps.Proposed)
	}
}

// The state shows the milestone's release records once, in their short form.
func TestPilotStateHasTheReleases(t *testing.T) {
	e := newPilotEnv(t, "")
	ctx := context.Background()
	var specTask aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-spec-m3-checkout-v2"}, &specTask); err != nil {
		t.Fatal(err)
	}
	specTask.Status.Autopilot.Releases = []aiv1.ReleaseRecord{
		{ID: 1, By: aiv1.ReleaseByPlanner, Why: "the API is live", AfterIssue: 1, SHA: "abc123", State: aiv1.ReleaseReleased, Version: "2026.11.3",
			PipelineURL: "https://gitlab.example/p/1"},
		{ID: 2, By: aiv1.ReleaseByFinal, State: aiv1.ReleasePlanned, Final: true},
	}
	if err := e.k8s.Status().Update(ctx, &specTask); err != nil {
		t.Fatal(err)
	}
	e.step(t)
	p := e.engine.calls[0].Prompt
	want := `"releases": [
    {
      "by": "planner",
      "why": "the API is live",
      "afterIssue": 1,
      "state": "Released",
      "version": "2026.11.3"
    },
    {
      "by": "final",
      "state": "Planned",
      "final": true
    }
  ]`
	if !strings.Contains(p, want) {
		t.Fatalf("the state misses the releases:\n%s", p)
	}
	if strings.Contains(p, "abc123") || strings.Contains(p, "pipelineURL") || strings.Count(p, `"releases"`) != 1 {
		t.Fatalf("the releases once, in their short form:\n%s", p)
	}
}
