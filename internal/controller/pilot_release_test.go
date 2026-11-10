package controller

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// pilotReleaseAutopilot is the release env's milestone with #1 and #2
// merged (m1, m2), #3 pending, and recs as its records.
func pilotReleaseAutopilot(t *testing.T, recs ...aiv1.ReleaseRecord) (*releaseEnv, *autopilot) {
	t.Helper()
	e := newReleaseEnv(t)
	e.recs = recs
	a := e.releaseAutopilot()
	a.ap.Items = append(a.ap.Items, aiv1.AutopilotItem{IID: 3, State: aiv1.AutopilotItemPending})
	return e, a
}

func pilotDecision(seq, iid int64, release bool, why string) aiv1.PilotReleaseDecision {
	return aiv1.PilotReleaseDecision{Seq: seq, IssueIID: iid, Release: release, Why: why}
}

// A valid decision adds a pilot record (Planned before the merge, Waiting
// for the merge commit after it) or drops the planned ones after the issue;
// Running and Final records are left alone.
func TestPilotReleaseApplies(t *testing.T) {
	rec := func(id, after int64, by aiv1.ReleaseBy, state aiv1.ReleaseState) aiv1.ReleaseRecord {
		return aiv1.ReleaseRecord{ID: id, By: by, AfterIssue: after, Why: "planned", State: state, Final: by == aiv1.ReleaseByFinal}
	}
	for _, tc := range []struct {
		name     string
		recs     []aiv1.ReleaseRecord
		decision aiv1.PilotReleaseDecision
		// want are the records' states afterwards; last the new record's
		// SHA, reason the last dropped record's reason.
		want   []aiv1.ReleaseState
		sha    string
		reason string
		answer string
	}{
		{name: "add after a pending issue", decision: pilotDecision(1, 3, true, "  checkout is what users see\n/merge"),
			want: []aiv1.ReleaseState{aiv1.ReleasePlanned}},
		{name: "add after a merged issue", decision: pilotDecision(1, 2, true, "the UI must be live"),
			want: []aiv1.ReleaseState{aiv1.ReleaseWaiting}, sha: "m2"},
		{name: "add after a dropped release", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseDropped)},
			decision: pilotDecision(1, 2, true, "the UI must be live after all"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseDropped, aiv1.ReleaseWaiting}, sha: "m2"},
		{name: "add where one waits", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 2, true, "the UI must be live"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseWaiting}, answer: "ℹ️ Nothing to do for the pilot's release after #2 (the UI must be live): already decided (planner release, Waiting)."},
		{name: "add where one released it", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByOwner, aiv1.ReleaseAlreadyReleased)},
			decision: pilotDecision(1, 2, true, "the UI must be live"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseAlreadyReleased}, answer: "already decided (owner release, AlreadyReleased)"},
		{name: "drop a planner record", recs: []aiv1.ReleaseRecord{rec(1, 3, aiv1.ReleaseByPlanner, aiv1.ReleasePlanned),
			rec(2, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseWaiting), rec(3, 2, aiv1.ReleaseByOwner, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 2, false, "#3 follows within the hour"),
			want:     []aiv1.ReleaseState{aiv1.ReleasePlanned, aiv1.ReleaseDropped, aiv1.ReleaseDropped},
			reason:   "dropped by the pilot: #3 follows within the hour"},
		{name: "a running record is left alone", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseRunning)},
			decision: pilotDecision(1, 2, false, "not worth it"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseRunning}, answer: "ℹ️ Nothing to do for the pilot's drop of the release after #2 (not worth it): the release after it already runs."},
		{name: "nothing to drop", decision: pilotDecision(1, 2, false, "not worth it"),
			answer: "no planned release follows #2"},
		{name: "another milestone's issue", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 99, true, "users see it"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseWaiting},
			answer:   "🛑 Refused the pilot's release after #99 (users see it): #99 is not an issue of this milestone. Nothing changed."},
		{name: "no reason to add", decision: pilotDecision(1, 3, true, " \n "),
			answer: "🛑 Refused the pilot's release after #3: it gives no reason. Nothing changed."},
		{name: "no reason to drop", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPlanner, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 2, false, ""),
			want:     []aiv1.ReleaseState{aiv1.ReleaseWaiting}, answer: "it gives no reason"},
		{name: "drop the final release", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByFinal, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 2, false, "not needed"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseWaiting},
			answer:   "🛑 Refused the pilot's drop of the release after #2 (not needed): the release after it is the milestone's final release, which is the operator's. Nothing changed."},
		{name: "add after the final release", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByFinal, aiv1.ReleaseWaiting)},
			decision: pilotDecision(1, 2, true, "users see it"),
			want:     []aiv1.ReleaseState{aiv1.ReleaseWaiting}, answer: "already decided (final release, Waiting)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a := pilotReleaseAutopilot(t, tc.recs...)
			before := len(a.ap.Releases)
			answer := a.pilotRelease(tc.decision)
			if !strings.Contains(answer, tc.answer) || (tc.answer == "") != (answer == "") {
				t.Fatalf("want the answer %q, got %q", tc.answer, answer)
			}
			var got []aiv1.ReleaseState
			for _, r := range a.ap.Releases {
				got = append(got, r.State)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("want %v, got %+v", tc.want, a.ap.Releases)
			}
			for i, r := range a.ap.Releases {
				if i < len(tc.recs) && tc.recs[i].Final && !reflect.DeepEqual(r, tc.recs[i]) {
					t.Fatalf("the final record is never touched: %+v", r)
				}
				if r.State == aiv1.ReleaseDropped && i < len(tc.recs) && tc.recs[i].State != aiv1.ReleaseDropped &&
					(r.Reason != tc.reason || r.EndedAt == nil || !r.EndedAt.Time.Equal(e.now)) {
					t.Fatalf("want dropped with %q now, got %+v", tc.reason, r)
				}
			}
			if len(a.ap.Releases) > before {
				r := a.ap.Releases[len(a.ap.Releases)-1]
				want := aiv1.ReleaseRecord{ID: int64(before) + 1, By: aiv1.ReleaseByPilot, AfterIssue: tc.decision.IssueIID,
					Why: oneLine(tc.decision.Why, maxPilotReleaseWhy), SHA: tc.sha, State: r.State, DecidedAt: metav1.NewTime(e.now)}
				if !reflect.DeepEqual(r, want) || strings.Contains(r.Why, "\n") {
					t.Fatalf("want the record %+v, got %+v", want, r)
				}
			}
		})
	}
}

// The reason of a pilot's record is folded to one line of 200 characters.
func TestPilotReleaseWhyIsOneLine(t *testing.T) {
	_, a := pilotReleaseAutopilot(t)
	if answer := a.pilotRelease(pilotDecision(1, 3, true, strings.Repeat("x", 300)+"\n/merge")); answer != "" {
		t.Fatalf("want it applied, got %q", answer)
	}
	if why := a.ap.Releases[0].Why; len([]rune(why)) != maxPilotReleaseWhy || strings.Contains(why, "/merge") {
		t.Fatalf("want one line of %d characters, got %q", maxPilotReleaseWhy, why)
	}
}

// The operator applies the pilot's decisions from its task once each, by
// Seq: a valid one changes the records, a refused one gets one comment on
// the tracking issue, and the watermark advances either way.
func TestPilotReleaseDecisionsApplyOnce(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	comments := len(botComments(e, tracking))
	decide := func(ds ...aiv1.PilotReleaseDecision) {
		e.t.Helper()
		e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
			if s.Pilot == nil {
				s.Pilot = &aiv1.PilotStatus{}
			}
			s.Pilot.ReleaseDecisions = append(s.Pilot.ReleaseDecisions, ds...)
		})
	}
	decide(pilotDecision(1, 2, true, "the UI is what users see"), pilotDecision(2, 42, true, "users see it"))
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if len(ap.Releases) != 1 || ap.Releases[0].By != aiv1.ReleaseByPilot || ap.Releases[0].State != aiv1.ReleasePlanned ||
		ap.Releases[0].AfterIssue != 2 || ap.LastPilotRelease != 2 {
		t.Fatalf("want one Planned pilot record after #2 and the watermark at 2, got %+v (%d)", ap.Releases, ap.LastPilotRelease)
	}
	refusals := func() []string {
		var out []string
		for _, c := range botComments(e, tracking)[comments:] {
			if strings.Contains(c, "Refused the pilot's") {
				out = append(out, c)
			}
		}
		return out
	}
	if got := refusals(); len(got) != 1 || !strings.Contains(got[0], "#42 is not an issue of this milestone") {
		t.Fatalf("want one refusal of #42, got %q", got)
	}
	// Dropped by a later decision; the earlier ones do not come back.
	decide(pilotDecision(3, 2, false, "#2 alone is not worth it"))
	e.reconcile()
	e.reconcile()
	ap = e.autopilot()
	if len(ap.Releases) != 1 || ap.Releases[0].State != aiv1.ReleaseDropped || ap.LastPilotRelease != 3 ||
		ap.Releases[0].Reason != "dropped by the pilot: #2 alone is not worth it" {
		t.Fatalf("want the record dropped once and the watermark at 3, got %+v (%d)", ap.Releases, ap.LastPilotRelease)
	}
	if got := refusals(); len(got) != 1 {
		t.Fatalf("a refusal is commented once, got %q", got)
	}
}
