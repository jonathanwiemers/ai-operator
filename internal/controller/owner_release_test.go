package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// ownerReleases are the owner records of the milestone.
func ownerReleases(e *projectEnv) []aiv1.ReleaseRecord {
	var out []aiv1.ReleaseRecord
	for _, r := range e.autopilot().Releases {
		if r.By == aiv1.ReleaseByOwner {
			out = append(out, r)
		}
	}
	return out
}

func TestOwnerRelease(t *testing.T) {
	for _, tc := range []struct{ name, body, why, said string }{
		{name: "with a reason", body: "/agent release ship it\nnow, please", why: "ship it…", said: "📦 Release decided after main-hea by @alice: ship it…"},
		{name: "without a reason", body: "/agent release", why: "asked by @alice", said: "📦 Release decided after main-hea by @alice: asked by @\u200balice."},
		{name: "upper case, with a colon", body: "/agent Release: [x](http://evil) @bob", why: "[x](http://evil) @bob", said: `\[x\]\(http:`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPilotEnv(t)
			e.gl.AddPipeline("main-old", "main", "success")
			e.gl.AddPipeline("main-head", "main", "running")
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, tc.body)
			e.reconcile()
			e.reconcile() // a repeated poll does not apply it twice
			got := ownerReleases(e)
			if len(got) != 1 || got[0].Actor != "@alice" || got[0].Why != tc.why || got[0].SHA != "main-head" || got[0].Final {
				t.Fatalf("one owner record at main's head: %+v", got)
			}
			if c := trackingComments(e); strings.Count(c, "Release decided after") != 1 || !strings.Contains(c, tc.said) {
				t.Fatalf("the decision is said once:\n%s", c)
			}
		})
	}
}

func TestOwnerReleaseWithoutAPipelineOnMain(t *testing.T) {
	e := newPilotEnv(t)
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent release")
	e.reconcile()
	if got := ownerReleases(e); len(got) != 0 {
		t.Fatalf("no head, no record: %+v", got)
	}
	if !strings.Contains(trackingComments(e), "`release` needs a pipeline on main") {
		t.Fatalf("the usage is answered:\n%s", trackingComments(e))
	}
}

// seedReleases gives the milestone a Planned record after #4, a Waiting one
// after #5 (no commit yet), a Running one after #6 and a Final one after #4.
func seedReleases(e *projectEnv) {
	e.t.Helper()
	running := e.gl.AddPipeline("main-r", "main", "running")
	now := metav1.Now()
	e.patchAutopilot(func(ap *aiv1.AutopilotStatus) {
		ap.Releases = []aiv1.ReleaseRecord{
			{ID: 1, By: aiv1.ReleaseByPilot, AfterIssue: 4, Why: "users see it", State: aiv1.ReleasePlanned, DecidedAt: now},
			{ID: 2, By: aiv1.ReleaseByPilot, AfterIssue: 5, Why: "needed next", State: aiv1.ReleaseWaiting, DecidedAt: now},
			{ID: 3, By: aiv1.ReleaseByPlanner, AfterIssue: 6, SHA: "main-r", State: aiv1.ReleaseRunning, PipelineID: running, Attempts: 1, StartedAt: &now, DecidedAt: now},
			{ID: 4, By: aiv1.ReleaseByFinal, AfterIssue: 4, Final: true, State: aiv1.ReleasePlanned, DecidedAt: now},
		}
	})
}

func releaseState(e *projectEnv, id int64) aiv1.ReleaseRecord {
	e.t.Helper()
	for _, r := range e.autopilot().Releases {
		if r.ID == id {
			return r
		}
	}
	e.t.Fatalf("no release %d", id)
	return aiv1.ReleaseRecord{}
}

func TestOwnerNoRelease(t *testing.T) {
	e := newPilotEnv(t)
	seedReleases(e)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent no-release #4 not worth a deploy")
	e.gl.AddNote(tracking, alice, "/agent norelease #5")
	e.reconcile()
	e.reconcile()
	if r := releaseState(e, 1); r.State != aiv1.ReleaseDropped || r.Reason != "dropped by @alice: not worth a deploy" {
		t.Fatalf("#4's planned release is dropped: %+v", r)
	}
	if r := releaseState(e, 2); r.State != aiv1.ReleaseDropped || r.Reason != "dropped by @alice" {
		t.Fatalf("#5's waiting release is dropped: %+v", r)
	}
	if r := releaseState(e, 4); r.State != aiv1.ReleasePlanned {
		t.Fatalf("the final release is the operator's: %+v", r)
	}
	c := trackingComments(e)
	if strings.Count(c, "⏹️ Release after #4 dropped by @alice: not worth a deploy.") != 1 || strings.Count(c, "⏹️ Release after #5 dropped by @alice.") != 1 {
		t.Fatalf("each drop is said once:\n%s", c)
	}

	e.gl.AddNote(tracking, alice, "/agent no-release #6")
	e.gl.AddNote(tracking, alice, "/agent no-release #99")
	e.gl.AddNote(tracking, alice, "/agent no-release")
	e.reconcile()
	if r := releaseState(e, 3); r.State != aiv1.ReleaseRunning {
		t.Fatalf("a running release is untouched: %+v", r)
	}
	if got := strings.Count(trackingComments(e), "`no-release` needs an issue with a planned release"); got != 3 {
		t.Fatalf("running, unknown and missing issues get the usage, got %d:\n%s", got, trackingComments(e))
	}
}

func TestReleaseCommandsByANonOwner(t *testing.T) {
	e := newPilotEnv(t) // commandsFrom: members, the default
	seedReleases(e)
	e.gl.AddPipeline("main-head", "main", "success")
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, mallory, "/agent release now")
	e.gl.AddNote(tracking, mallory, "/agent no-release #4")
	e.reconcile()
	e.reconcile()
	if got := ownerReleases(e); len(got) != 0 {
		t.Fatalf("a non-owner adds no release: %+v", got)
	}
	if r := releaseState(e, 1); r.State != aiv1.ReleasePlanned {
		t.Fatalf("a non-owner drops nothing: %+v", r)
	}
	if got := countIn(e.gl.IssueNotes[tracking], notOwnerRelease); got != 2 {
		t.Fatalf("one refusal per comment, got %d:\n%s", got, trackingComments(e))
	}
}
