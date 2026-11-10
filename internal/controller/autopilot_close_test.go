package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// doneAndClosed finishes the pilot env's autopilot by skipping both issues,
// then closes them by hand: nothing is open in the milestone.
func doneAndClosed(e *projectEnv) {
	e.t.Helper()
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		e.t.Fatalf("autopilot is done: %+v", e.autopilot())
	}
	e.gl.Issues[1].State = "closed"
	e.gl.Issues[2].State = "closed"
}

func countComments(e *projectEnv, iid int64, part string) int {
	n := 0
	for _, c := range botComments(e, iid) {
		if strings.Contains(c, part) {
			n++
		}
	}
	return n
}

// retroDone puts the pilot's retrospective on the pilot and lets the next
// poll copy it to the spec task and delete the pilot, which stays while its
// follow-ups are open.
func retroDone(e *projectEnv, retro aiv1.PilotRetrospective) {
	e.t.Helper()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = &aiv1.PilotStatus{Retrospective: retro.DeepCopy()} })
	e.reconcile()
	open := !followUpsSettled(&retro, e.r.Now())
	if _, ok := e.tasks()[pilotTaskName]; ok != open {
		e.t.Fatalf("the pilot goes after its retrospective, once its follow-ups are settled (open %v): %v", open, keys(e.tasks()))
	}
}

func TestDoneAutopilotClosesItsMilestoneAfterTheRetrospective(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	doneAndClosed(e)
	e.reconcile()
	if e.gl.Milestones[0].State == "closed" || countComments(e, tracking, "stays open") != 0 {
		t.Fatalf("the pilot still writes the retrospective: milestone %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}

	retroDone(e, aiv1.PilotRetrospective{NoteID: 42})
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" || countComments(e, tracking, "The milestone is closed") != 1 {
		t.Fatalf("the milestone closes once the retrospective is in: %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}
	e.reconcile()
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatalf("a closed milestone is no longer planned: %v", keys(e.tasks()))
	}
	if countComments(e, tracking, "The milestone is closed") != 1 {
		t.Fatalf("one comment: %v", botComments(e, tracking))
	}
}

func TestDoneAutopilotWaitsForTheFollowUps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(e *projectEnv, retro int64)
	}{
		{"all created", func(e *projectEnv, _ int64) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Retrospective.Created = map[string]int64{"1": 20} })
		}},
		{"a week later", func(e *projectEnv, _ int64) { e.gl.Clock = e.gl.Clock.Add(followUpWindow) }},
		{"dismissed by an owner", func(e *projectEnv, retro int64) {
			e.gl.AddReply(e.autopilot().TrackingIssueIID, retro, alice, "create none")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPilotEnv(t)
			doneAndClosed(e)
			posted := metav1.NewTime(e.gl.Clock)
			note := e.gl.AddNote(e.autopilot().TrackingIssueIID, e.gl.Bot, "🧭 **Retrospective**\n\n1. Docs")
			retroDone(e, aiv1.PilotRetrospective{NoteID: note.ID, PostedAt: &posted, OwnerIDs: []int64{alice.ID},
				FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}}})
			e.gl.Clock = e.gl.Clock.Add(time.Hour)
			e.reconcile()
			if e.gl.Milestones[0].State == "closed" {
				t.Fatal("the owners can still open the follow-ups")
			}
			tc.after(e, note.ID)
			e.reconcile()
			if e.gl.Milestones[0].State != "closed" {
				t.Fatalf("the follow-ups are settled: milestone %s", e.gl.Milestones[0].State)
			}
		})
	}
}

func TestDoneAutopilotKeepsAMilestoneWithOpenIssues(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	// No pilot: nothing to wait for but the issues.
	pilot := e.pilot()
	if err := e.c.Delete(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	finishAutopilot(e) // skipped, still open
	e.reconcile()
	e.reconcile()
	e.reconcile()
	const waits = "The milestone stays open while #1, #2 are open"
	if e.gl.Milestones[0].State == "closed" || countComments(e, tracking, waits) != 1 || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("skipped issues keep the milestone open, said once: %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}

	e.gl.Issues[2].State = "closed"
	e.reconcile()
	e.reconcile()
	if countComments(e, tracking, "The milestone stays open while #1 is open") != 1 {
		t.Fatalf("a changed list is said again: %v", botComments(e, tracking))
	}
	e.gl.Issues[1].State = "closed"
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("closed once nothing is open: %s", e.gl.Milestones[0].State)
	}
}

func TestDoneAutopilotDoesNotCloseWhileReplanning(t *testing.T) {
	e := newPilotEnv(t)
	doneAndClosed(e)
	e.reconcile()
	retroDone(e, aiv1.PilotRetrospective{NoteID: 42})
	// The owner edits the milestone before the next poll: it is planned again.
	e.gl.Milestones[0].Description += "\nAlso a receipt page."
	e.reconcile()
	st := e.tasks()[specTaskName]
	if !st.RunPending() || e.gl.Milestones[0].State == "closed" {
		t.Fatalf("a milestone that is planned again stays open: milestone %s", e.gl.Milestones[0].State)
	}
}

func TestDoneAutopilotClosesAfterAFailedRetrospective(t *testing.T) {
	e := newPilotEnv(t)
	doneAndClosed(e)
	e.reconcile()
	retroDone(e, aiv1.PilotRetrospective{Failed: "the run ended without one", FollowUps: []aiv1.PilotFollowUp{{Title: "Docs"}}})
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("a failed retrospective has nothing to wait for: milestone %s", e.gl.Milestones[0].State)
	}
}

// replan plays a finished planning run of the spec task: plan is what it
// recorded, and the run request is observed.
func replan(e *projectEnv, plan []aiv1.PlannedIssue) {
	e.t.Helper()
	st := e.task(specTaskName)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = plan
		s.Run.ObservedRunRequest = st.Spec.RunRequest
	})
}

// notDone fails unless autopilot of the env's milestone is still undone:
// no done comment, tracking issue and milestone open, no retrospective.
func notDone(e *projectEnv, tracking int64) {
	e.t.Helper()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotDone {
		e.t.Fatalf("an empty queue is not done: %+v", ap)
	}
	if n := countComments(e, tracking, "Autopilot is done"); n != 0 {
		e.t.Fatalf("no done comment: %v", botComments(e, tracking))
	}
	if is := e.gl.Issues[tracking]; is == nil || is.State == "closed" {
		e.t.Fatalf("the tracking issue stays open: %+v", is)
	}
	if e.gl.Milestones[0].State == "closed" {
		e.t.Fatal("the milestone stays open")
	}
	if coord, ok := e.tasks()[pilotTaskName]; ok && coord.Spec.Source.Retrospective != 0 {
		e.t.Fatalf("no retrospective: %+v", coord.Spec.Source)
	}
}

// Bloop %20: a planned milestone, nothing started yet, whose owner edits the
// description. The re-plan records an empty plan, so the queue drops both
// pending issues, and an accepted endMilestone then reached done with 0 of 0.
func TestEmptyQueueIsNeverDone(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent pause")
	e.reconcile()
	replan(e, []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}})
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || len(ap.Items) != 2 || ap.Items[0].State != aiv1.AutopilotItemPending {
		t.Fatalf("planned and paused before anything started: %+v", ap)
	}
	if _, ok := e.tasks()[pilotTaskName]; !ok {
		t.Fatalf("the milestone has a pilot: %v", keys(e.tasks()))
	}

	e.gl.Milestones[0].Description += "\nAlso a receipt page."
	e.reconcile()
	if !e.task(specTaskName).RunPending() {
		t.Fatal("the edit plans the milestone again")
	}
	replan(e, []aiv1.PlannedIssue{})
	if err := e.r.queueCommand(e.ctx, e.task(specTaskName), aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "endMilestone"}, Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.reconcile()
	e.reconcile()
	notDone(e, tracking)
	if ap := e.autopilot(); len(ap.Items) != 0 || ap.Phase != aiv1.AutopilotPaused {
		t.Fatalf("the empty plan dropped the pending issues, the pause stays: %+v", ap)
	}
	if countComments(e, tracking, "Nothing to end (@alice via the pilot): nothing is planned in this milestone") != 1 ||
		countComments(e, tracking, "close the milestone in GitLab or remove `/agent autopilot`") != 1 {
		t.Fatalf("endMilestone says there is nothing to end, once: %v", botComments(e, tracking))
	}

	// Planned again with both issues, the queue goes on.
	e.gl.Milestones[0].Description += "\nAnd a receipt mail."
	e.reconcile()
	replan(e, []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}})
	e.reconcile()
	if ap := e.autopilot(); len(ap.Items) != 2 {
		t.Fatalf("the new plan fills the queue again: %+v", ap)
	}
	notDone(e, tracking)
}

func TestEndMilestoneBeforeThePlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	if err := e.r.queueCommand(e.ctx, e.task(specTaskName), aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "endMilestone"}, Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	notDone(e, tracking)
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || countComments(e, tracking, "Nothing to end") != 1 {
		t.Fatalf("a milestone still being planned keeps running: %+v\n%v", ap, botComments(e, tracking))
	}
}

func TestDoneWithAnEmptyQueueKeepsThePhase(t *testing.T) {
	for _, phase := range []aiv1.AutopilotPhase{aiv1.AutopilotRunning, aiv1.AutopilotPaused} {
		a := &autopilot{ap: &aiv1.AutopilotStatus{Phase: phase, TrackingIssueIID: 7}, log: logr.Discard()}
		// No GitLab: done must not close or comment anything.
		if err := a.done(context.Background()); err != nil || a.ap.Phase != phase {
			t.Fatalf("done with no items keeps %s: %v %+v", phase, err, a.ap)
		}
	}
}

// Every issue merged, autopilot keeps running while a companion of the
// milestone has not landed, says so once, and is done once it has.
func TestAutopilotIsDoneOnceItsCompanionsHaveLanded(t *testing.T) {
	e, infra := newCompanionQueueEnv(t, "")
	cmr := mergeWithCompanion(e, infra) // #2 starts right away
	e.reconcile()
	e.finishRun(2, "sha-ui", nil)
	e.reconcile() // marked ready
	e.reconcile() // merged
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	tracking := ap.TrackingIssueIID
	const waits = "Every issue of the queue is through. Autopilot is done once the companions group/infra!1 have landed (merged or closed)."
	if !slices.Equal(e.gl.Merged, []int64{1, 2}) || ap.Phase != aiv1.AutopilotRunning || !slices.Equal(ap.CloseWaitsForCompanions, []string{"group/infra!1"}) {
		t.Fatalf("both merged, autopilot waits for the companion: merged %v, %+v", e.gl.Merged, ap)
	}
	if countComments(e, tracking, waits) != 1 || countComments(e, tracking, "Autopilot is done:") != 0 || e.gl.Issues[tracking].State != "opened" {
		t.Fatalf("the wait is said once, the tracking issue stays open:\n%v", botComments(e, tracking))
	}
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "▶️ running, every issue is through; done once the companions group/infra!1 have landed") {
		t.Fatalf("the tracking issue names the open companions:\n%s", body)
	}

	cmr.State, cmr.MergeCommitSHA = "merged", "merge-sha-infra"
	e.reconcile()
	ap = e.autopilot()
	if ap.Phase != aiv1.AutopilotDone || len(ap.CloseWaitsForCompanions) != 0 || countComments(e, tracking, "Autopilot is done: 2 of 2 issues merged") != 1 {
		t.Fatalf("done once the companion has landed: %+v\n%v", ap, botComments(e, tracking))
	}
}

// A companion recorded after done (its issue was finished by hand) keeps
// the milestone open, said once, until it lands.
func TestDoneAutopilotKeepsAMilestoneWithOpenCompanions(t *testing.T) {
	e := newPilotEnv(t)
	infra := withCompanion(e)
	tracking := e.autopilot().TrackingIssueIID
	pilot := e.pilot()
	if err := e.c.Delete(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	doneAndClosed(e)
	cmr, err := infra.CreateMergeRequest(e.ctx, "group/infra", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/app-1-api", TargetBranch: "main", Title: "Draft: API"})
	if err != nil {
		t.Fatal(err)
	}
	infra.MRs[cmr.IID].SHA, infra.MRs[cmr.IID].DetailedMergeStatus = "c1", "draft_status"
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		t.Fatal(err)
	}
	p.Status.Companions = []aiv1.CompanionStatus{{HomeIssueIID: 1, MilestoneIID: 3, HomeMRIID: 7, HomeMergeSHA: "m1",
		Project: "group/infra", MRIID: cmr.IID, HeadSHA: "c1", State: aiv1.LandingWaitingRelease}}
	if err := e.c.Status().Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.reconcile()
	const waits = "The milestone stays open while the companions group/infra!1 have not landed. Autopilot closes it once they are merged or closed."
	if e.gl.Milestones[0].State == "closed" || countComments(e, tracking, waits) != 1 ||
		!slices.Equal(e.autopilot().CloseWaitsForCompanions, []string{"group/infra!1"}) {
		t.Fatalf("an open companion keeps the milestone open, said once: %s, %+v\n%v", e.gl.Milestones[0].State, e.autopilot(), botComments(e, tracking))
	}

	infra.MRs[cmr.IID].State = "closed"
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" || len(e.autopilot().CloseWaitsForCompanions) != 0 || countComments(e, tracking, waits) != 1 {
		t.Fatalf("closed once the companion has landed, the list cleared: %s, %+v", e.gl.Milestones[0].State, e.autopilot())
	}
}

// taggedMain makes the env's project tag its default branch: 2026.11.1 is a
// merge commit of an hour ago, main's head h1 is green and contains both
// merges, and the change since the tag is code.
func taggedMain(e *projectEnv) {
	e.t.Helper()
	created := e.gl.Clock.Add(-time.Hour)
	e.gl.AddTag("2026.11.1", gitlab.TagCommit{ID: "t1", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created})
	e.gl.BranchHeads = map[string]string{"main": "h1"}
	e.gl.AddPipeline("h1", "main", "success")
	e.gl.Compares = map[string]*gitlab.Compare{"h1..merge-sha-api": {}, "h1..merge-sha-ui": {},
		"2026.11.1..merge-sha-ui": {Diffs: []gitlab.Diff{{OldPath: "docs/usage.md", NewPath: "docs/usage.md"}, {OldPath: "main.go", NewPath: "main.go"}}}}
}

// queueMerged records #1 and then #2 as merged (merge-sha-api,
// merge-sha-ui) and closes both: the next poll is through the queue. A
// skipped #1 is skipped instead.
func queueMerged(e *projectEnv, skipped bool) {
	e.t.Helper()
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		for i := range s.Autopilot.Items {
			it := &s.Autopilot.Items[i]
			at := metav1.NewTime(e.gl.Clock.Add(time.Duration(it.IID) * time.Minute))
			it.State, it.MergedSHA, it.MergedAt = aiv1.AutopilotItemMerged, map[int64]string{1: "merge-sha-api", 2: "merge-sha-ui"}[it.IID], &at
			if skipped && it.IID == 1 {
				it.State, it.MergedSHA, it.MergedAt = aiv1.AutopilotItemSkipped, "", nil
			}
		}
	})
	e.gl.Issues[1].State, e.gl.Issues[2].State = "closed", "closed"
}

// commentedBefore reports whether the first bot comment with first comes
// before the first one with then, both posted.
func commentedBefore(e *projectEnv, iid int64, first, then string) bool {
	i := slices.IndexFunc(botComments(e, iid), func(c string) bool { return strings.Contains(c, first) })
	j := slices.IndexFunc(botComments(e, iid), func(c string) bool { return strings.Contains(c, then) })
	return i >= 0 && j > i
}

// finalRec is the autopilot's final release record.
func finalRec(e *projectEnv) aiv1.ReleaseRecord {
	e.t.Helper()
	for _, r := range e.autopilot().Releases {
		if r.Final {
			return r
		}
	}
	e.t.Fatalf("no final release: %+v", e.autopilot().Releases)
	return aiv1.ReleaseRecord{}
}

// The queue through, done waits until the final release, run by the
// operator, contains the last merge.
func TestDoneWaitsForTheFinalRelease(t *testing.T) {
	e := newPilotEnv(t)
	taggedMain(e)
	tracking := e.autopilot().TrackingIssueIID
	e.finishRun(1, "sha-api", nil)
	e.reconcile() // marked ready
	e.reconcile() // merged
	e.reconcile() // #2 starts
	e.reconcile()
	e.finishRun(2, "sha-ui", nil)
	e.reconcile()
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if !slices.Equal(e.gl.Merged, []int64{1, 2}) || ap.Phase != aiv1.AutopilotRunning || countComments(e, tracking, "Autopilot is done") != 0 {
		t.Fatalf("both merged, not done before the release: merged %v, %+v\n%v", e.gl.Merged, ap, botComments(e, tracking))
	}
	rec := finalRec(e)
	if rec.By != aiv1.ReleaseByFinal || rec.SHA != "merge-sha-ui" || rec.AfterIssue != 2 || rec.Why != "the milestone's last merge" ||
		rec.State != aiv1.ReleaseRunning || len(e.gl.CreatedPipelines) != 1 || len(ap.Releases) != 1 {
		t.Fatalf("one final release, running for #2's merge: %+v, pipelines %+v", ap.Releases, e.gl.CreatedPipelines)
	}
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "every issue is through; done once a release contains the last merge merge-sh") {
		t.Fatalf("the tracking issue says what done waits for:\n%s", body)
	}
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotRunning || len(e.gl.CreatedPipelines) != 1 || len(e.autopilot().Releases) != 1 {
		t.Fatalf("still waiting, nothing doubled: %+v", e.autopilot())
	}
	if countComments(e, tracking, "🚀 Release started (final): the milestone's last merge. Contains #1, #2. Pipeline https://") != 1 {
		t.Fatalf("the start is commented once:\n%v", botComments(e, tracking))
	}

	e.gl.SetPipelineStatus(rec.PipelineID, "success")
	created := e.gl.Clock
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "h1", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, "merge-sha-api", "merge-sha-ui")
	e.reconcile()
	rec = finalRec(e)
	if rec.State != aiv1.ReleaseReleased || rec.Version != "2026.11.2" || !slices.Equal(rec.Issues, []int64{1, 2}) {
		t.Fatalf("released with both issues: %+v", rec)
	}
	if e.autopilot().Phase != aiv1.AutopilotDone || countComments(e, tracking, "Autopilot is done: 2 of 2 issues merged") != 1 {
		t.Fatalf("done once released: %+v\n%v", e.autopilot(), botComments(e, tracking))
	}
	e.reconcile()
	if countComments(e, tracking, "📦 Released 2026.11.2") != 1 || countComments(e, tracking, "🚀 Release started") != 1 ||
		!commentedBefore(e, tracking, "📦 Released", "Autopilot is done") {
		t.Fatalf("the end is commented once, before done:\n%v", botComments(e, tracking))
	}
}

// Nothing release-relevant since the newest tag: NotNeeded, no pipeline.
func TestFinalReleaseNotNeeded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		diffs []gitlab.Diff
		why   string
	}{
		{"docs only", []gitlab.Diff{{OldPath: "docs/usage.md", NewPath: "docs/usage.md"}, {OldPath: "README.md", NewPath: "README.md"}},
			"nothing release-relevant since 2026.11.1"},
		{"an empty diff", nil, "no change since 2026.11.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPilotEnv(t)
			taggedMain(e)
			// An older tag listed first: the highest version counts.
			created := e.gl.Clock.Add(-48 * time.Hour)
			e.gl.AddTag("2026.10.9", gitlab.TagCommit{ID: "t0", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created})
			e.gl.Compares["2026.11.1..merge-sha-ui"] = &gitlab.Compare{Diffs: tc.diffs}
			queueMerged(e, false)
			e.reconcile()
			rec := finalRec(e)
			if rec.State != aiv1.ReleaseNotNeeded || rec.Reason != tc.why || rec.EndedAt == nil {
				t.Fatalf("want NotNeeded (%s), got %+v", tc.why, rec)
			}
			if e.autopilot().Phase != aiv1.AutopilotDone || len(e.gl.CreatedPipelines) != 0 {
				t.Fatalf("done without a pipeline: %+v %+v", e.autopilot(), e.gl.CreatedPipelines)
			}
			tracking := e.autopilot().TrackingIssueIID
			if countComments(e, tracking, "📦 No release needed: "+tc.why+".") != 1 || !commentedBefore(e, tracking, "No release needed", "Autopilot is done") {
				t.Fatalf("the final decision is commented before done:\n%v", botComments(e, tracking))
			}
		})
	}
}

// A project without release tags: NotApplicable, done, no pause; the
// Planned record after the skipped #1 is dropped.
func TestFinalReleaseWithoutTags(t *testing.T) {
	e := newPilotEnv(t)
	e.planRelease(1)
	e.reconcile()
	queueMerged(e, true)
	e.reconcile()
	ap := e.autopilot()
	if got := releaseStates(ap); !slices.Equal(got, []string{"1:Dropped", "2:NotApplicable"}) {
		t.Fatalf("want #1's planned release dropped and the final one not applicable, got %v: %+v", got, ap.Releases)
	}
	if ap.Releases[0].Reason != "its issue was not merged" || finalRec(e).Reason != "group/app does not tag its default branch" {
		t.Fatalf("the reasons: %+v", ap.Releases)
	}
	if ap.Phase != aiv1.AutopilotDone || ap.PauseKind != "" || len(e.gl.CreatedPipelines) != 0 {
		t.Fatalf("done, no pause, no pipeline: %+v %+v", ap, e.gl.CreatedPipelines)
	}
	if countComments(e, ap.TrackingIssueIID, "📦 No release: group/app does not tag its default branch.") != 1 ||
		countComments(e, ap.TrackingIssueIID, "Release started") != 0 {
		t.Fatalf("one line for the final decision, the dropped one silent:\n%v", botComments(e, ap.TrackingIssueIID))
	}
}

// failFinal lets the env's final release fail twice: the milestone pauses.
func failFinal(e *projectEnv) {
	e.t.Helper()
	taggedMain(e)
	queueMerged(e, false)
	e.reconcile()
	for n := 1; n <= 2; n++ {
		if rec := finalRec(e); rec.State != aiv1.ReleaseRunning || rec.Attempts != int32(n) || len(e.gl.CreatedPipelines) != n {
			e.t.Fatalf("attempt %d runs: %+v", n, rec)
		}
		e.gl.SetPipelineStatus(e.gl.CreatedPipelines[n-1].ID, "failed")
		e.reconcile()
	}
	ap := e.autopilot()
	if rec := finalRec(e); rec.State != aiv1.ReleaseFailed || ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseRelease || ap.PausedFor != 0 {
		e.t.Fatalf("failed twice, paused for the release: %+v, %+v", rec, ap)
	}
	if !strings.Contains(ap.PausedReason, "the final release failed: the release pipeline") {
		e.t.Fatalf("the pause says why: %q", ap.PausedReason)
	}
	e.reconcile()
	if len(e.gl.CreatedPipelines) != 2 || countComments(e, ap.TrackingIssueIID, "Autopilot paused: the final release failed") != 1 {
		e.t.Fatalf("no third attempt, the pause said once: %+v\n%v", e.gl.CreatedPipelines, botComments(e, ap.TrackingIssueIID))
	}
	if countComments(e, ap.TrackingIssueIID, "🚀 Release started (final)") != 1 || countComments(e, ap.TrackingIssueIID, "❌ The final release failed twice: the release pipeline") != 1 ||
		!commentedBefore(e, ap.TrackingIssueIID, "❌ The final release failed", "Autopilot paused") {
		e.t.Fatalf("one start for both attempts, the failure once before the pause:\n%v", botComments(e, ap.TrackingIssueIID))
	}
}

func TestFailedFinalReleaseResumeRetries(t *testing.T) {
	e := newPilotEnv(t)
	failFinal(e)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent resume")
	e.reconcile()
	ap := e.autopilot()
	rec := finalRec(e)
	if ap.Phase != aiv1.AutopilotRunning || ap.ResumedVia != aiv1.ResumedViaCommand || rec.State != aiv1.ReleaseRunning || rec.Attempts != 1 || len(e.gl.CreatedPipelines) != 3 {
		t.Fatalf("resume starts the final release over: %+v, %+v", ap, rec)
	}
	e.gl.SetPipelineStatus(rec.PipelineID, "success")
	created := e.gl.Clock
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "h1", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, "merge-sha-ui")
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseReleased || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("released and done: %+v, %+v", rec, e.autopilot())
	}
	if countComments(e, tracking, "🚀 Release started (final)") != 2 || countComments(e, tracking, "📦 Released 2026.11.2") != 1 {
		t.Fatalf("the retried release is a run of its own:\n%v", botComments(e, tracking))
	}
}

func TestFailedFinalReleaseEndMilestone(t *testing.T) {
	e := newPilotEnv(t)
	failFinal(e)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, mallory, "/agent endMilestone")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotPaused || countComments(e, tracking, "Only the milestone's owners may end the milestone.") != 1 {
		t.Fatalf("owners only: %+v\n%v", e.autopilot(), botComments(e, tracking))
	}
	e.gl.AddNote(tracking, alice, "/agent endMilestone")
	e.reconcile()
	ap := e.autopilot()
	if rec := finalRec(e); rec.State != aiv1.ReleaseDropped || rec.Reason != "ended without a release by @alice" {
		t.Fatalf("the final release is dropped: %+v", rec)
	}
	if ap.Phase != aiv1.AutopilotDone || ap.ResumedVia != aiv1.ResumedViaCommand || len(e.gl.CreatedPipelines) != 2 ||
		countComments(e, tracking, "Autopilot is done: 2 of 2 issues merged") != 1 {
		t.Fatalf("done without a release: %+v\n%v", ap, botComments(e, tracking))
	}
}

// A tag made by hand that contains the last merge lifts the pause.
func TestFailedFinalReleaseReleasedByHand(t *testing.T) {
	e := newPilotEnv(t)
	failFinal(e)
	e.reconcile()
	created := e.gl.Clock
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "h2", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, "merge-sha-ui")
	e.reconcile()
	ap := e.autopilot()
	if rec := finalRec(e); rec.State != aiv1.ReleaseAlreadyReleased || rec.Version != "2026.11.2" {
		t.Fatalf("already released by hand: %+v", rec)
	}
	if ap.Phase != aiv1.AutopilotDone || ap.ResumedVia != aiv1.ResumedViaCauseGone || len(e.gl.CreatedPipelines) != 2 ||
		countComments(e, ap.TrackingIssueIID, "the tag 2026.11.2 contains the milestone's last merge") != 1 {
		t.Fatalf("the pause ends by itself and autopilot is done: %+v\n%v", ap, botComments(e, ap.TrackingIssueIID))
	}
	if countComments(e, ap.TrackingIssueIID, "📦") != 0 {
		t.Fatalf("the resume comment says it, no second line:\n%v", botComments(e, ap.TrackingIssueIID))
	}
}

// The companions are waited for only once the final release has ended.
func TestFinalReleaseBeforeTheCompanions(t *testing.T) {
	e, infra := newCompanionQueueEnv(t, "")
	taggedMain(e)
	cmr := mergeWithCompanion(e, infra)
	e.reconcile()
	e.finishRun(2, "sha-ui", nil)
	e.reconcile()
	e.reconcile()
	e.reconcile()
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	const waits = "Autopilot is done once the companions group/infra!1 have landed"
	if rec := finalRec(e); rec.State != aiv1.ReleaseRunning || countComments(e, tracking, waits) != 0 || len(e.autopilot().CloseWaitsForCompanions) != 0 {
		t.Fatalf("the final release first: %+v, %+v\n%v", rec, e.autopilot(), botComments(e, tracking))
	}

	e.gl.SetPipelineStatus(finalRec(e).PipelineID, "success")
	created := e.gl.Clock
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "h1", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, "merge-sha-api", "merge-sha-ui")
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseReleased || e.autopilot().Phase != aiv1.AutopilotRunning || countComments(e, tracking, waits) != 1 {
		t.Fatalf("released, now the companions: %+v, %+v\n%v", rec, e.autopilot(), botComments(e, tracking))
	}
	cmr.State, cmr.MergeCommitSHA = "merged", "merge-sha-infra"
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("done once the companion has landed: %+v", e.autopilot())
	}
}

// The last merge is the newest by MergedAt, not the last in queue order; a
// merge recorded without a time counts as older.
func TestLastMerge(t *testing.T) {
	at := func(m int) *metav1.Time { return &metav1.Time{Time: time.Date(2026, 11, 10, 12, m, 0, 0, time.UTC)} }
	a := &autopilot{ap: &aiv1.AutopilotStatus{Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemMerged, MergedSHA: "m1"},
		{IID: 2, State: aiv1.AutopilotItemMerged, MergedSHA: "m2", MergedAt: at(5)},
		{IID: 3, State: aiv1.AutopilotItemMerged, MergedSHA: "m3", MergedAt: at(1)},
		{IID: 4, State: aiv1.AutopilotItemMerged},
		{IID: 5, State: aiv1.AutopilotItemSkipped, MergedSHA: "m5", MergedAt: at(9)},
	}}}
	if got := a.lastMerge(); got == nil || got.IID != 2 {
		t.Fatalf("want #2, got %+v", got)
	}
	a.ap.Items = a.ap.Items[:1]
	if got := a.lastMerge(); got == nil || got.IID != 1 {
		t.Fatalf("want #1, got %+v", got)
	}
}

// A merge after the final record was created moves it to the newer commit
// while it is not Running; there is still one record.
func TestFinalReleaseFollowsALaterMerge(t *testing.T) {
	e := newPilotEnv(t)
	taggedMain(e)
	e.gl.AddPipeline("h2", "main", "failed") // main is red: the release waits
	queueMerged(e, false)
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseWaiting || rec.SHA != "merge-sha-ui" {
		t.Fatalf("waiting for a green main: %+v", rec)
	}
	later := func(iid int64, sha string) {
		e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
			at := metav1.NewTime(e.gl.Clock.Add(time.Hour))
			s.Autopilot.Items = append(s.Autopilot.Items, aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemMerged,
				MergedSHA: sha, MergedAt: &at, RevisitOf: "D1-1"})
		})
	}
	later(5, "merge-sha-5")
	e.gl.Compares["2026.11.1..merge-sha-5"] = &gitlab.Compare{Diffs: []gitlab.Diff{{OldPath: "main.go", NewPath: "main.go"}}}
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseWaiting || rec.SHA != "merge-sha-5" || rec.AfterIssue != 5 || len(e.autopilot().Releases) != 1 {
		t.Fatalf("moved to the later merge: %+v", e.autopilot().Releases)
	}

	// Running, it keeps its commit.
	e.gl.AddPipeline("h1", "main", "success")
	e.gl.Compares["h1..merge-sha-5"] = &gitlab.Compare{}
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseRunning {
		t.Fatalf("the release runs: %+v", rec)
	}
	later(6, "merge-sha-6")
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseRunning || rec.SHA != "merge-sha-5" {
		t.Fatalf("a running release keeps its commit: %+v", rec)
	}
}

// With release.mode Off the final release is NotApplicable, without a look
// at the tags' diff.
func TestFinalReleaseModeOff(t *testing.T) {
	e := newPilotEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Workflow.Release.Mode = aiv1.ReleaseOff })
	taggedMain(e)
	e.gl.Compares["2026.11.1..merge-sha-ui"] = &gitlab.Compare{}
	queueMerged(e, false)
	e.reconcile()
	if rec := finalRec(e); rec.State != aiv1.ReleaseNotApplicable || rec.Reason != "release.mode is Off" || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("not applicable, done: %+v, %+v", rec, e.autopilot())
	}
}
