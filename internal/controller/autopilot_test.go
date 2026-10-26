package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

const specTaskName = "shop-spec-m3-checkout-v2"

// newAutopilotEnv is an autopilot milestone that was planned into #1 (API)
// and #2 (UI, after the API), both drafts.
func newAutopilotEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newUnplannedAutopilotEnv(t)
	st := e.tasks()[specTaskName]
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = st.Spec.RunRequest // planned
		s.Plan = []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}}
	})
	return e
}

// newUnplannedAutopilotEnv is an autopilot milestone after its first poll,
// while its first planning run is pending: #1 (API) and #2 (UI) are written
// but not in the plan yet.
func newUnplannedAutopilotEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent autopilot\nBuild a new checkout."}
	e.gl.Milestones = []gitlab.Milestone{m}
	for _, is := range []struct {
		iid        int64
		title, key string
	}{{1, "API", "api"}, {2, "UI", "ui"}} {
		issue := e.gl.AddIssue(is.iid, is.title, "ai-generated", "agent::draft")
		mc := m
		issue.Milestone = &mc
		issue.Description = "Build it.\n<!-- ai-operator:key=" + is.key + " -->"
		issue.WebURL = fmt.Sprintf("https://gitlab.example/group/app/-/issues/%d", is.iid)
	}
	e.reconcile()
	if st := e.tasks()[specTaskName]; !st.Spec.Autopilot || !st.RunPending() {
		t.Fatalf("the autopilot marker must plan the milestone on autopilot: %+v", st.Spec)
	}
	return e
}

func (e *projectEnv) patchStatus(name string, mutate func(*aiv1.AgentTaskStatus)) {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &task); err != nil {
		e.t.Fatal(err)
	}
	orig := task.DeepCopy()
	mutate(&task.Status)
	if err := e.c.Status().Patch(e.ctx, &task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) autopilot() *aiv1.AutopilotStatus {
	e.t.Helper()
	ap := e.tasks()[specTaskName].Status.Autopilot
	if ap == nil {
		e.t.Fatal("no autopilot status")
	}
	return ap
}

// finishRun plays the runner and the CI loop: the issue's run pushed sha, the
// review was clean, the pipeline is green and the issue is in review.
func (e *projectEnv) finishRun(iid int64, sha string, mutate func(*aiv1.RunStatus)) string {
	e.t.Helper()
	var name string
	for n, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == iid {
			name = n
		}
	}
	if name == "" {
		e.t.Fatalf("no implement task for #%d", iid)
	}
	mr, err := e.gl.CreateMergeRequest(e.ctx, "g/p", gitlab.CreateMergeRequestOptions{
		SourceBranch: "agent/" + sha, TargetBranch: "main", Title: "Draft: issue"})
	if err != nil {
		e.t.Fatal(err)
	}
	e.gl.MRs[mr.IID].SHA = sha
	task := e.tasks()[name]
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = task.Spec.RunRequest
		s.Run.LastResult = aiv1.RunResultSucceeded
		s.Run.PushedSHA, s.Run.ReviewedSHA = sha, sha
		s.Run.MergeRequestIID, s.Run.MergeRequestURL = mr.IID, mr.WebURL
		s.CI = &aiv1.CIStatus{SHA: sha, State: aiv1.CIStateSuccess}
		if mutate != nil {
			mutate(&s.Run)
		}
	})
	e.gl.SetLabels(iid, "ai-generated", "agent::review")
	return name
}

func trackingComments(e *projectEnv) string {
	var b strings.Builder
	for _, n := range e.gl.IssueNotes[e.autopilot().TrackingIssueIID] {
		b.WriteString(n.Body + "\n")
	}
	return b.String()
}

func TestAutopilotWorksThroughTheQueue(t *testing.T) {
	e := newAutopilotEnv(t)

	// First poll with a plan: tracking issue, and the API goes first
	// although the plan lists the UI first, because the UI depends on it.
	e.reconcile()
	ap := e.autopilot()
	tracking := e.gl.Issues[ap.TrackingIssueIID]
	if tracking == nil || !strings.HasPrefix(tracking.Title, "🤖 Autopilot: Checkout v2") || !marker.IsTrackingIssue(tracking.Description) {
		t.Fatalf("tracking issue not created: %+v", tracking)
	}
	if !slices.Equal(e.gl.Labels(1), []string{"ai-generated", "agent::ready"}) || !e.gl.Issues[2].HasLabel("agent::draft") {
		t.Fatalf("only #1 should be handed over: #1 %v, #2 %v", e.gl.Labels(1), e.gl.Labels(2))
	}
	if ap.Items[0].IID != 1 || ap.Items[0].State != aiv1.AutopilotItemActive || ap.Items[1].State != aiv1.AutopilotItemPending {
		t.Fatalf("unexpected queue %+v", ap.Items)
	}

	// The operator picks the issue up as usual; its task runs on autopilot.
	e.reconcile()
	impl := e.tasks()["shop-1-api"]
	if !impl.Spec.Autopilot || !e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("issue should be picked up on autopilot: %+v %v", impl.Spec, e.gl.Labels(1))
	}

	// Run done, clean review, green pipeline, decision recorded.
	e.gl.IssueNotes[1] = append(e.gl.IssueNotes[1], gitlab.Note{ID: 77, Author: e.gl.Bot,
		Body: "📌 **Decisions** in run x\n\n#### D1-1: Protocol\n**Chosen:** REST\n<!-- ai-operator:decision=D1-1 -->\n"})
	e.finishRun(1, "sha-api", nil)
	e.reconcile() // the draft MR is marked ready
	if e.gl.MRs[1].Draft || len(e.gl.Merged) != 0 {
		t.Fatalf("the MR should be marked ready first: %+v", e.gl.MRs[1])
	}
	e.reconcile() // merged
	if !slices.Equal(e.gl.Merged, []int64{1}) || e.gl.Issues[1].State != "closed" || !e.gl.Issues[1].HasLabel("agent::done") {
		t.Fatalf("expected !1 merged and #1 closed: merged %v, #1 %+v", e.gl.Merged, e.gl.Issues[1])
	}
	ap = e.autopilot()
	if ap.Items[0].State != aiv1.AutopilotItemMerged || ap.Items[0].MergedSHA != "merge-sha-api" ||
		len(ap.Items[0].Decisions) != 1 || ap.Items[0].Decisions[0].ID != "D1-1" || !strings.HasSuffix(ap.Items[0].Decisions[0].URL, "#note_77") {
		t.Fatalf("merge not recorded: %+v", ap.Items[0])
	}

	// Next poll: the API's task is gone with its issue, the UI starts.
	e.reconcile()
	if _, ok := e.tasks()["shop-1-api"]; ok {
		t.Fatal("the merged issue's task should be deleted")
	}
	if !e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("#2 should be handed over after #1 merged: %v", e.gl.Labels(2))
	}
	body := e.gl.Issues[ap.TrackingIssueIID].Description
	for _, want := range []string{"- [x] #1 API: merged !1 (`merge-sh`)", "- [ ] #2 UI: **in progress**", "[D1-1](", "Protocol (#1)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tracking issue misses %q:\n%s", want, body)
		}
	}

	e.reconcile()
	e.finishRun(2, "sha-ui", nil)
	e.reconcile()
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1, 2}) || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("both MRs should be merged and the milestone done: %v %s", e.gl.Merged, e.autopilot().Phase)
	}
	if e.gl.Issues[ap.TrackingIssueIID].State != "closed" || !strings.Contains(trackingComments(e), "Autopilot is done: 2 of 2 issues merged") {
		t.Fatalf("tracking issue should be closed with a summary:\n%s", trackingComments(e))
	}
}

func TestAutopilotTrackingIssueBeforeThePlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	ap := e.autopilot()
	tracking := e.gl.Issues[ap.TrackingIssueIID]
	if tracking == nil || tracking.Title != "🤖 Autopilot: Checkout v2" || !marker.IsTrackingIssue(tracking.Description) ||
		!slices.Equal(tracking.Labels, []string{"ai-generated"}) || tracking.Milestone == nil || tracking.Milestone.ID != 11 {
		t.Fatalf("the tracking issue must exist before the first plan: %+v", tracking)
	}
	for _, want := range []string{"📝 planning, the planning agent is writing the issues.", "_Listed once the milestone is planned._"} {
		if !strings.Contains(tracking.Description, want) {
			t.Fatalf("tracking issue misses %q:\n%s", want, tracking.Description)
		}
	}
	if ap.Phase != aiv1.AutopilotRunning || len(ap.Items) != 0 || !e.gl.Issues[1].HasLabel("agent::draft") {
		t.Fatalf("nothing is queued before the plan: %+v, #1 %v", ap, e.gl.Labels(1))
	}
}

func TestAutopilotWaitsForAFailedFirstPlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	st := e.tasks()[specTaskName]
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.LastResult = st.Spec.RunRequest, aiv1.RunResultFailed
	})
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	tracking := e.gl.Issues[ap.TrackingIssueIID]
	if ap.Phase != aiv1.AutopilotRunning || tracking.State != "opened" || e.gl.Issues[1].HasLabel("agent::ready") || e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("without a plan autopilot is neither done nor picking: %+v, #1 %v, #2 %v", ap, e.gl.Labels(1), e.gl.Labels(2))
	}
	if !strings.Contains(tracking.Description, "planning has not succeeded yet") || strings.Contains(trackingComments(e), "Autopilot is done") {
		t.Fatalf("the tracking issue must say that planning failed:\n%s\n%s", tracking.Description, trackingComments(e))
	}

	// The plan arrives: the queue starts.
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.LastResult = aiv1.RunResultSucceeded
		s.Plan = []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}}
	})
	e.reconcile()
	if !e.gl.Issues[1].HasLabel("agent::ready") || e.autopilot().TrackingIssueIID != ap.TrackingIssueIID {
		t.Fatalf("the plan starts the queue on the same tracking issue: %+v, #1 %v", e.autopilot(), e.gl.Labels(1))
	}
	if body := e.gl.Issues[ap.TrackingIssueIID].Description; !strings.Contains(body, "▶️ running.") || !strings.Contains(body, "- [ ] #1 API: **in progress**") {
		t.Fatalf("the tracking issue must list the plan:\n%s", body)
	}
}

func TestAutopilotPauseBeforeThePlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	ap := e.autopilot()
	desc := e.gl.Issues[tracking].Description
	if ap.Phase != aiv1.AutopilotPaused || ap.PausedReason != "paused by @jonathan" {
		t.Fatalf("pause works before the plan, status keeps the raw reason: %+v", ap)
	}
	if !strings.Contains(desc, "paused, "+notify.Defuse("paused by @jonathan")) {
		t.Fatalf("the tracking description must defuse the pause reason:\n%s", desc)
	}

	// Planned while paused: still nothing starts until resumed.
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = e.tasks()[specTaskName].Spec.RunRequest
		s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, Title: "API"}}
	})
	e.reconcile()
	if e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("a pause from before the plan holds the queue: %v", e.gl.Labels(1))
	}
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	if !e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("resume starts the queue: %+v, #1 %v", e.autopilot(), e.gl.Labels(1))
	}
}

func TestAutopilotPausesAndResumes(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "2 blocking review findings") || len(e.gl.Merged) != 0 {
		t.Fatalf("blocking findings must pause, not merge: %+v", ap)
	}
	if !strings.Contains(trackingComments(e), "Autopilot paused: 2 blocking review findings") {
		t.Fatalf("the pause must be explained on the tracking issue:\n%s", trackingComments(e))
	}
	// Paused polls change nothing and do not repeat the comment.
	e.reconcile()
	if n := strings.Count(trackingComments(e), "Autopilot paused"); n != 1 {
		t.Fatalf("the pause comment must be posted once, got %d", n)
	}

	// A human fixes it and resumes.
	e.patchStatus("shop-1-api", func(s *aiv1.AgentTaskStatus) { s.Run.BlockingFindings = 0 })
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("resume should continue with the merge: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
	if strings.Contains(trackingComments(e), "Autopilot continues") {
		t.Fatal("the resume command, not the automatic one, must have continued here")
	}

	// "/agent pause" stops it, "/agent skip #2" skips the UI, and the
	// milestone is done.
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotPaused || e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("pause must stop the queue: %+v %v", e.autopilot(), e.gl.Labels(2))
	}
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent skip #2")
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone || e.autopilot().Items[1].State != aiv1.AutopilotItemSkipped {
		t.Fatalf("skipping the last issue should finish the milestone: %+v", e.autopilot())
	}
}

// A pause reason that quotes a mention and a quick action is inert in the
// tracking comment and description; the owners' ping still works; status
// keeps the raw reason for prompts.
func TestAutopilotPauseDefusesReason(t *testing.T) {
	e := newAutopilotEnv(t)
	e.r.DefaultOwners = []string{"alice"}
	e.reconcile()
	e.reconcile()
	const job = "@someone\n/close"
	name := e.finishRun(1, "sha-api", nil)
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateFailed, FailedJobs: []string{job}}
	})
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "@someone") || !strings.Contains(ap.PausedReason, "/close") {
		t.Fatalf("status keeps the raw reason: %+v", ap)
	}
	if strings.Contains(ap.PausedReason, "\u200b") {
		t.Fatalf("status must not store a defused reason: %q", ap.PausedReason)
	}
	defused := notify.Defuse(ap.PausedReason)
	comment, desc := trackingComments(e), e.gl.Issues[ap.TrackingIssueIID].Description
	if !strings.Contains(comment, defused) || strings.Contains(comment, ap.PausedReason) {
		t.Fatalf("the pause comment must use the defused reason, not the raw one:\n%s", comment)
	}
	if !strings.Contains(desc, defused) || strings.Contains(desc, ap.PausedReason) {
		t.Fatalf("the tracking description must use the defused reason, not the raw one:\n%s", desc)
	}
	if !strings.Contains(comment, "cc @alice") || strings.Contains(comment, "cc @\u200balice") {
		t.Fatalf("the owners' mention must stay a real ping:\n%s", comment)
	}
}

func TestAutopilotMergeGate(t *testing.T) {
	cases := []struct {
		name   string
		run    func(*aiv1.RunStatus)
		mr     func(*gitlab.MergeRequest)
		ci     *aiv1.CIStatus
		reason string
	}{
		{name: "reviewer failed", run: func(r *aiv1.RunStatus) { r.ReviewErrors = 1 }, reason: "reviewers failed"},
		{name: "unreviewed head", run: func(r *aiv1.RunStatus) { r.ReviewedSHA = "older" }, reason: "was not reviewed"},
		{name: "head moved", mr: func(m *gitlab.MergeRequest) { m.SHA = "someone-else" }, reason: "someone pushed"},
		{name: "approval", mr: func(m *gitlab.MergeRequest) { m.DetailedMergeStatus, m.Draft = "not_approved", false }, reason: "needs an approval"},
		{name: "canceled", ci: &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateCanceled}, reason: "was canceled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newAutopilotEnv(t)
			e.reconcile()
			e.reconcile()
			name := e.finishRun(1, "sha-api", c.run)
			if c.ci != nil {
				e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = c.ci })
			}
			if c.mr != nil {
				c.mr(e.gl.MRs[1])
			}
			e.reconcile()
			ap := e.autopilot()
			if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, c.reason) || len(e.gl.Merged) != 0 {
				t.Fatalf("expected a pause with %q and no merge: %+v, merged %v", c.reason, ap, e.gl.Merged)
			}
		})
	}
}

func TestAutopilotRefusedMergeAndRebase(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"

	// GitLab rebases; the new head goes to the CI loop like a push.
	e.reconcile()
	if !slices.Equal(e.gl.Rebased, []int64{1}) {
		t.Fatalf("expected a rebase, got %v", e.gl.Rebased)
	}
	e.reconcile()
	task := e.tasks()[name]
	if task.Status.Run.PushedSHA != "sha-api-rebased" || task.Status.Run.ReviewedSHA != "sha-api-rebased" || task.Status.CI != nil ||
		!e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("the rebased head should be handed to the CI loop: %+v %v", task.Status, e.gl.Labels(1))
	}

	// The pipeline of the rebased head is green, but the bot may not merge.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-api-rebased", State: aiv1.CIStateSuccess}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.gl.MergeErrors = map[int64]error{1: &gitlab.Error{StatusCode: 401, Method: "PUT", Path: "merge"}}
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "may not merge") {
		t.Fatalf("a refused merge must pause with the reason: %+v", ap)
	}
}

func TestAutopilotOffWhenTheMarkerIsRemoved(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.Milestones[0].Description = "/agent spec\nBuild a new checkout."
	e.reconcile()
	st := e.tasks()[specTaskName]
	if st.Spec.Autopilot || st.Status.Autopilot.Phase != aiv1.AutopilotOff || e.gl.Issues[tracking].State != "closed" {
		t.Fatalf("removing the marker must switch autopilot off: %+v %+v", st.Spec, st.Status.Autopilot)
	}
	// Without any marker the milestone is no longer planned at all.
	e.gl.Milestones[0].Description = "Build a new checkout."
	e.reconcile()
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatal("the spec task should be deleted")
	}
}

func TestAutopilotPausesOnADependencyCycle(t *testing.T) {
	e := newAutopilotEnv(t)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, DependsOn: []int64{2}}, {Key: "ui", IID: 2, DependsOn: []int64{1}}}
	})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "cycle between #1, #2") {
		t.Fatalf("a cycle must pause: %+v", ap)
	}
}

func TestQueueOrder(t *testing.T) {
	order, cycle := queueOrder([]aiv1.PlannedIssue{
		{IID: 5, DependsOn: []int64{7}}, {IID: 6}, {IID: 7, DependsOn: []int64{6, 99}},
	})
	if !slices.Equal(order, []int64{6, 7, 5}) || cycle != nil {
		t.Fatalf("order %v cycle %v", order, cycle)
	}
}

func TestAutopilotOffWhenTheMilestoneIsNoLongerPlanned(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.Milestones[0].Description = "Build a new checkout."
	e.reconcile()
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatal("the spec task should be deleted")
	}
	if e.gl.Issues[tracking].State != "closed" || !strings.Contains(e.gl.IssueNotes[tracking][len(e.gl.IssueNotes[tracking])-1].Body, "Autopilot is off") {
		t.Fatalf("the tracking issue must be closed with a note: %+v", e.gl.Issues[tracking])
	}
}

func TestAutopilotRebaseKeepsTheReviewOnlyForTheSameChange(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	e.reconcile() // rebase requested
	// Someone pushes before the operator sees the rebased head.
	e.gl.MRDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1 +1 @@\n-old\n+change of !1\n+and more\n"}}}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "not the one that was reviewed") || ap.Items[0].RebasedFrom != "" {
		t.Fatalf("a changed diff after the rebase must pause: %+v", ap)
	}
	if run := e.tasks()[name].Status.Run; run.ReviewedSHA != "sha-api" || run.PushedSHA != "sha-api" || len(e.gl.Merged) != 0 {
		t.Fatalf("the new head must not count as reviewed: %+v, merged %v", run, e.gl.Merged)
	}
}

// mergeBaseRequested reports whether the next run of #1 merges the default
// branch for head sha, and the issue went back to working.
func mergeBaseRequested(e *projectEnv, name, sha string) bool {
	task := e.tasks()[name]
	return task.Spec.Source.MergeBaseFor == sha && task.RunPending() && e.gl.Issues[1].HasLabel("agent::working")
}

func TestAutopilotLetsTheAgentResolveConflicts(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "conflict"
	e.reconcile()
	if !mergeBaseRequested(e, name, "sha-api") || e.autopilot().Phase != aiv1.AutopilotRunning || len(e.gl.Merged) != 0 {
		t.Fatalf("conflicts must go to the agent: %+v, %+v, %v", e.tasks()[name].Spec, e.autopilot(), e.gl.Labels(1))
	}
	// The run merged the default branch and pushed; review and CI are clean.
	task := e.tasks()[name]
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = task.Spec.RunRequest
		s.Run.MergedBaseFor = "sha-api"
		s.Run.PushedSHA, s.Run.ReviewedSHA = "sha-merged", "sha-merged"
		s.CI = &aiv1.CIStatus{SHA: "sha-merged", State: aiv1.CIStateSuccess}
	})
	e.gl.MRs[1].SHA, e.gl.MRs[1].DetailedMergeStatus = "sha-merged", "mergeable"
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the resolved MR should be merged: %v, %+v", e.gl.Merged, e.autopilot())
	}
}

func TestAutopilotPausesWhenTheAgentCouldNotResolveConflicts(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	// Already tried for this head, and it still conflicts.
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.Source.MergeBaseFor = "sha-api"
	if err := e.c.Update(e.ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "conflict"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "did not resolve") || e.tasks()[name].Spec.RunRequest != task.Spec.RunRequest {
		t.Fatalf("a conflict the agent could not resolve must pause, not loop: %+v", ap)
	}
}

func TestAutopilotRebaseConflictGoesToTheAgent(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	e.gl.RebaseConflict = true
	e.reconcile() // rebase requested, ends in a conflict
	e.reconcile()
	if !mergeBaseRequested(e, name, "sha-api") || e.autopilot().Items[0].RebasedFrom != "" {
		t.Fatalf("a rebase with conflicts must go to the agent: %+v %+v", e.tasks()[name].Spec.Source, e.autopilot().Items[0])
	}
}

func TestAutopilotFailedRebaseGoesToTheAgent(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	e.reconcile()
	// GitLab did not rebase: same head, still behind. It may still refresh
	// the MR for a while, then it pauses.
	e.gl.MRs[1].SHA, e.gl.MRs[1].DetailedMergeStatus = "sha-api", "need_rebase"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.Items[0].RebasedFrom != "sha-api" {
		t.Fatalf("a fresh rebase gets time to show up: %+v", ap)
	}
	e.gl.Clock = e.gl.Clock.Add(10 * time.Minute)
	e.reconcile()
	if !mergeBaseRequested(e, "shop-1-api", "sha-api") {
		t.Fatalf("a rebase that changed nothing goes to the agent: %+v", e.autopilot())
	}
}

func TestAutopilotRecreatesADeletedTrackingIssue(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	old := e.autopilot().TrackingIssueIID
	delete(e.gl.Issues, old)
	e.reconcile()
	if now := e.autopilot().TrackingIssueIID; now == old || e.gl.Issues[now] == nil {
		t.Fatalf("a new tracking issue should be created: old #%d, now #%d", old, now)
	}
}

func TestAutopilotCountsIssuesFinishedByHand(t *testing.T) {
	e := newAutopilotEnv(t)
	// #1 was merged in the normal flow before the milestone went on autopilot.
	e.gl.Issues[1].State = "closed"
	mr, _ := e.gl.CreateMergeRequest(e.ctx, "g/p", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/1", TargetBranch: "main"})
	e.gl.MRs[mr.IID].State, e.gl.MRs[mr.IID].MergeCommitSHA = "merged", "by-hand"
	e.gl.ClosedBy(1, mr.IID)
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Items[0].State != aiv1.AutopilotItemMerged || ap.Items[0].MergedSHA != "by-hand" {
		t.Fatalf("#1 should count as merged: %+v", ap.Items)
	}
	if ap.Items[1].State != aiv1.AutopilotItemActive || !e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("#2 should start once #1 counts as merged: %+v %v", ap.Items, e.gl.Labels(2))
	}
}

func TestAutopilotSkippedThenMergedByHand(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(tracking, human, "/agent: skip #1")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "waits for the skipped #1") {
		t.Fatalf("#2 must wait for the skipped #1: %+v", ap)
	}
	// The human finishes #1 and resumes.
	e.gl.Issues[1].State = "closed"
	e.gl.SetLabels(1, "ai-generated", "agent::done")
	e.gl.AddNote(tracking, human, "/agent resume")
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Items[0].State != aiv1.AutopilotItemMerged || ap.Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("#1 done by hand should unblock #2: %+v", ap)
	}
}

func TestAutopilotHandsAnUncheckedHeadBackToCI(t *testing.T) {
	for _, ci := range []*aiv1.CIStatus{nil, {SHA: "older", State: aiv1.CIStateSuccess}, {SHA: "sha-api", State: aiv1.CIStateFixing}} {
		e := newAutopilotEnv(t)
		e.reconcile()
		e.reconcile()
		name := e.finishRun(1, "sha-api", nil)
		e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = ci })
		e.reconcile()
		if !e.gl.Issues[1].HasLabel("agent::working") || len(e.gl.Merged) != 0 {
			t.Fatalf("CI %+v: a head CI did not finish must go back to working: %v", ci, e.gl.Labels(1))
		}
		// The CI loop finishes it (a fix run that pushed nothing fails) and
		// it does not come back to review on its own.
		e.reconcile()
		if ci != nil && ci.State == aiv1.CIStateFixing && !e.gl.Issues[1].HasLabel("agent::needs-input") {
			t.Fatalf("the CI loop should end the fix round: %v", e.gl.Labels(1))
		}
	}
}

func TestAutopilotDoesNotRebaseAnUncheckableChange(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	e.gl.MRDiffs = map[int64][]gitlab.Diff{1: {{NewPath: "big.json", TooLarge: true}}}
	e.reconcile()
	if !mergeBaseRequested(e, "shop-1-api", "sha-api") || len(e.gl.Rebased) != 0 {
		t.Fatalf("an uncheckable change is merged by the agent, not rebased: %+v, rebased %v", e.autopilot(), e.gl.Rebased)
	}
}

func TestConflictsWithoutAutopilot(t *testing.T) {
	for _, on := range []bool{true, false} {
		e := newAutopilotEnv(t)
		if !on {
			var p aiv1.AgentProject
			if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
				t.Fatal(err)
			}
			off := false
			p.Spec.Workflow.ResolveConflicts = &off
			if err := e.c.Update(e.ctx, &p); err != nil {
				t.Fatal(err)
			}
		}
		// The milestone leaves autopilot; the MR in review conflicts.
		e.reconcile()
		e.reconcile()
		name := e.finishRun(1, "sha-api", nil)
		e.gl.Milestones[0].Description = "/agent spec\nBuild a new checkout."
		e.gl.MRs[1].Draft, e.gl.MRs[1].HasConflicts = false, true
		e.reconcile()
		e.reconcile() // asked once per head
		task := e.tasks()[name]
		if got := task.Spec.Source.MergeBaseFor == "sha-api" && e.gl.Issues[1].HasLabel("agent::working"); got != on {
			t.Fatalf("resolveConflicts=%v: merge requested %v (%+v, %v)", on, got, task.Spec.Source, e.gl.Labels(1))
		}
		if on && task.Spec.RunRequest != 2 {
			t.Fatalf("one run per conflicting head, got run request %d", task.Spec.RunRequest)
		}
	}
}

func TestAutopilotDraftAgainPauses(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].DetailedMergeStatus = "draft_status"
	e.reconcile() // marked ready
	e.gl.MRs[1].Title, e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = "Draft: issue", true, "draft_status"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "marked draft again") || len(e.gl.Merged) != 0 {
		t.Fatalf("a draft set again by a human must pause: %+v", ap)
	}
}

func TestAutopilotOffAfterDoneSaysNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent skip #1")
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent skip #2")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("expected done: %+v", e.autopilot())
	}
	e.gl.Milestones[0].State = "closed"
	e.gl.Milestones[0].Description = "Build a new checkout."
	e.reconcile()
	for _, n := range e.gl.IssueNotes[tracking] {
		if strings.Contains(n.Body, "Autopilot is off") {
			t.Fatalf("a finished autopilot needs no off note: %q", n.Body)
		}
	}
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatal("the spec task should be deleted")
	}
}

func TestAutopilotOnlyForPlannedIssues(t *testing.T) {
	e := newAutopilotEnv(t)
	extra := e.gl.AddIssue(5, "By hand", "agent::ready")
	extra.Milestone = &e.gl.Milestones[0]
	e.reconcile()
	e.reconcile()
	if task, ok := e.tasks()["shop-5-by-hand"]; !ok || task.Spec.Autopilot {
		t.Fatalf("an issue outside the plan follows the normal flow: %+v", task.Spec)
	}
	if task := e.tasks()["shop-1-api"]; !task.Spec.Autopilot {
		t.Fatalf("a planned issue runs on autopilot: %+v", task.Spec)
	}
}

func TestAutopilotDoesNotCountAMentionAsAMerge(t *testing.T) {
	e := newAutopilotEnv(t)
	// #1 was closed by hand without a merge; a merged MR of another issue
	// only mentions it, and the close was not done by a merge request.
	e.gl.Issues[1].State = "closed"
	mr, _ := e.gl.CreateMergeRequest(e.ctx, "g/p", gitlab.CreateMergeRequestOptions{SourceBranch: "other", TargetBranch: "main"})
	e.gl.MRs[mr.IID].State = "merged"
	e.gl.StateEvents = map[int64][]gitlab.StateEvent{1: {{ID: 1, State: "closed"}}}
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Items[0].State != aiv1.AutopilotItemSkipped || ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "waits for the skipped #1") {
		t.Fatalf("#1 closed without a merge must be skipped and block #2: %+v", ap)
	}
}

func TestAutopilotHonoursBlockedByLinks(t *testing.T) {
	e := newAutopilotEnv(t)
	// A human links the API (planned first) as blocked by an issue outside
	// the queue; the UI depends on the API by plan.
	e.gl.AddIssue(9, "Infra", "agent::review")
	if err := e.gl.CreateIssueLink(e.ctx, "g/p", 1, 9, gitlab.LinkIsBlockedBy); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "#1 is blocked by #9 (issue link)") ||
		e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("a blocked-by link must hold the issue back: %+v", ap)
	}
	// #9 is done: resume and the API starts.
	e.gl.Issues[9].State = "closed"
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	if ap := e.autopilot(); ap.Items[0].State != aiv1.AutopilotItemActive || !e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("with the blocker closed the issue starts: %+v", ap)
	}
}

func TestAutopilotLinkBetweenQueuedIssuesReorders(t *testing.T) {
	e := newAutopilotEnv(t)
	// The plan lists #1 before #4 without a dependency; a human links #1
	// as blocked by #4.
	four := e.gl.AddIssue(4, "Schema", "ai-generated", "agent::draft")
	four.Milestone = &e.gl.Milestones[0]
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1}, {Key: "schema", IID: 4}, {Key: "ui", IID: 2, DependsOn: []int64{1}}}
	})
	if err := e.gl.CreateIssueLink(e.ctx, "g/p", 1, 4, gitlab.LinkIsBlockedBy); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[1].IID != 4 || ap.Items[1].State != aiv1.AutopilotItemActive || ap.Items[0].State != aiv1.AutopilotItemPending {
		t.Fatalf("#4 must start first, #1 waits for it: %+v", ap.Items)
	}
}

func TestAutopilotIgnoresRelatesToLinks(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.FreeTier = true
	e.gl.AddIssue(9, "Infra", "agent::review")
	if err := e.gl.CreateIssueLink(e.ctx, "g/p", 1, 9, gitlab.LinkRelatesTo); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if ap := e.autopilot(); ap.Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("relates_to has no direction and does not block: %+v", ap)
	}
}

func TestAutopilotLinkEdgeCases(t *testing.T) {
	t.Run("deleted pending issue", func(t *testing.T) {
		e := newAutopilotEnv(t)
		delete(e.gl.Issues, 1)
		e.reconcile() // must not fail on the links of a missing issue
		if ap := e.autopilot(); ap.Items[0].State != aiv1.AutopilotItemSkipped {
			t.Fatalf("a deleted issue is skipped: %+v", ap.Items)
		}
	})
	t.Run("other project", func(t *testing.T) {
		e := newAutopilotEnv(t)
		// Local #2 exists in the queue; the link is to #2 of another project.
		foreign := gitlab.LinkedIssue{IID: 2, ProjectID: 77, State: "opened", LinkType: gitlab.LinkIsBlockedBy}
		foreign.References.Full = "infra/ops#2"
		e.gl.ForeignLinks = map[int64][]gitlab.LinkedIssue{1: {foreign}}
		e.reconcile()
		ap := e.autopilot()
		if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "#1 is blocked by infra/ops#2 (issue link)") {
			t.Fatalf("a link to another project's issue must name it: %+v", ap)
		}
	})
	t.Run("dropped plan dependency", func(t *testing.T) {
		e := newAutopilotEnv(t)
		// The operator linked #1 to #2 for a dependency the plan dropped.
		if err := e.gl.CreateIssueLink(e.ctx, "g/p", 1, 2, gitlab.LinkIsBlockedBy); err != nil {
			t.Fatal(err)
		}
		e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
			s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, Linked: []int64{2}}, {Key: "ui", IID: 2}}
		})
		e.reconcile()
		if ap := e.autopilot(); ap.Items[0].IID != 1 || ap.Items[0].State != aiv1.AutopilotItemActive {
			t.Fatalf("a stale link the operator made must not block: %+v", ap)
		}
	})
}

func TestAutopilotRunsCIBeforeMergingADraftWithoutPipeline(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	// The project skips pipelines for drafts: CI found none for the push.
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = true, "draft_status"
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateNone} })
	e.reconcile()
	task := e.tasks()[name]
	if e.gl.MRs[1].Draft || !slices.Equal(e.gl.MRPipelines, []int64{1}) || task.Status.CI != nil ||
		!e.gl.Issues[1].HasLabel("agent::working") || len(e.gl.Merged) != 0 {
		t.Fatalf("a draft without pipeline must be marked ready and checked by CI first: draft %v, pipelines %v, ci %+v, labels %v, merged %v",
			e.gl.MRs[1].Draft, e.gl.MRPipelines, task.Status.CI, e.gl.Labels(1), e.gl.Merged)
	}
	// The pipeline is green: merged.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateSuccess} })
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected the merge after CI: %v %+v", e.gl.Merged, e.autopilot())
	}
}

// A run can mark the MR ready without pushing (its last push was a draft's):
// the head still had no pipeline, so CI runs before the merge all the same.
func TestAutopilotRunsCIBeforeMergingAReadyMRWithoutPipeline(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateNone} })
	e.gl.MRs[1].Title, e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = "issue", false, "mergeable"
	title := e.gl.MRs[1].Title
	e.reconcile()
	if !slices.Equal(e.gl.MRPipelines, []int64{1}) || e.tasks()[name].Status.CI != nil || !e.gl.Issues[1].HasLabel("agent::working") ||
		len(e.gl.Merged) != 0 || e.gl.MRs[1].Title != title || !e.autopilot().Items[0].MarkedReady {
		t.Fatalf("a ready MR without pipeline must be checked by CI first: pipelines %v, labels %v, merged %v, title %q",
			e.gl.MRPipelines, e.gl.Labels(1), e.gl.Merged, e.gl.MRs[1].Title)
	}
}

func TestAutopilotMergesWhenNoMergeRequestPipelineIsPossible(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = true, "draft_status"
	e.gl.NoMRPipelines = true
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateNone} })
	e.reconcile() // marked ready, no pipeline possible
	if e.gl.MRs[1].Draft || !e.gl.Issues[1].HasLabel("agent::review") {
		t.Fatalf("without MR pipelines it stays in review, ready: draft %v, labels %v", e.gl.MRs[1].Draft, e.gl.Labels(1))
	}
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected the merge without another wait: %v %+v", e.gl.Merged, e.autopilot())
	}
}

func TestAutopilotContinuesByItselfOnceTheIssueIsFixed(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 || !ap.ResumeWhenReady {
		t.Fatalf("expected a pause over #1's review: %+v", ap)
	}
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotPaused {
		t.Fatal("still not fixed: stays paused")
	}
	// "/agent fix the findings": a later run leaves none.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.Run.BlockingFindings, s.Run.RunCount = 0, 2 })
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) || !strings.Contains(trackingComments(e), "Autopilot continues: #1 is reviewed and green again") {
		t.Fatalf("expected autopilot to continue and merge: merged %v, %+v\n%s", e.gl.Merged, e.autopilot(), trackingComments(e))
	}
}

func TestAutopilotContinuesWhenTheIssueIsMergedByHand(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.gl.SetLabels(1, "ai-generated", "agent::needs-input")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 {
		t.Fatalf("expected a pause over #1: %+v", ap)
	}
	// Merged by hand; GitLab closes the issue.
	mr := e.tasks()[name].Status.Run.MergeRequestIID
	e.gl.MRs[mr].State = "merged"
	e.gl.Issues[1].State = "closed"
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].State != aiv1.AutopilotItemMerged || !e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("a hand merge must count and the next issue start: %+v %v", ap, e.gl.Labels(2))
	}
	if !strings.Contains(trackingComments(e), fmt.Sprintf("Autopilot continues: !%d was merged", mr)) {
		t.Fatalf("expected the continue note:\n%s", trackingComments(e))
	}
}

func TestAutopilotPausedByHandWaitsForResume(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.Run.BlockingFindings = 0 })
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || len(e.gl.Merged) != 0 {
		t.Fatalf("a pause by hand waits for resume: %+v merged %v", ap, e.gl.Merged)
	}
}

// A pause over the MR (here: an approval) ends when it is merged by hand,
// also while the issue is still open.
func TestAutopilotMRPauseEndsWithAHandMerge(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	mr := e.tasks()[name].Status.Run.MergeRequestIID
	e.gl.MRs[mr].Draft, e.gl.MRs[mr].DetailedMergeStatus = false, "not_approved"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 || ap.ResumeWhenReady {
		t.Fatalf("expected an MR pause: %+v", ap)
	}
	e.gl.MRs[mr].State = "merged"
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].State != aiv1.AutopilotItemMerged {
		t.Fatalf("the hand merge must count: %+v", ap)
	}
}

// The Bloop case: an instruction for the agent written on the tracking issue
// is answered once, without quoting it, and points at the active issue.
func TestAutopilotAnswersUnknownTrackingCommandsOnce(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	human := gitlab.User{ID: 1, Username: "jonathan"}
	const answer = "This tracking issue only understands"
	e.gl.AddNote(tracking, human, "/agent investigate the root and fix it @someone\n/close")
	// A failed answer is tried again on the next poll.
	e.gl.NoteErrors = map[int64]error{tracking: errors.New("gitlab is down")}
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	e.gl.NoteErrors = nil
	e.reconcile()
	comments := trackingComments(e)
	if n := strings.Count(comments, answer); n != 1 {
		t.Fatalf("an unknown command must get exactly one answer, got %d:\n%s", n, comments)
	}
	if !strings.Contains(comments, "comment `/agent …` on #1, the active issue") || !strings.Contains(comments, "`/agent skip #N`") ||
		!strings.Contains(comments, "Did you mean `/pilot …`?") {
		t.Fatalf("the answer must name the commands and the active issue:\n%s", comments)
	}
	for _, n := range e.gl.IssueNotes[tracking] {
		if strings.Contains(n.Body, answer) && (strings.Contains(n.Body, "investigate") || strings.Contains(n.Body, "@someone") || strings.Contains(n.Body, "/close")) {
			t.Fatalf("the answer must not quote the comment:\n%s", n.Body)
		}
	}
	// Later polls do not answer the same note again.
	e.reconcile()
	e.reconcile()
	if n := strings.Count(trackingComments(e), answer); n != 1 {
		t.Fatalf("a note must be answered once, got %d answers", n)
	}

	// Known commands, and a well-formed revisit (syncRevisits' job), get no
	// such answer; a bare command and a malformed revisit do.
	for _, body := range []string{"/agent pause", "/agent resume", "/agent: skip #2", "/agent revisit D1-1 use gRPC"} {
		e.gl.AddNote(tracking, human, body)
	}
	e.reconcile()
	if n := strings.Count(trackingComments(e), answer); n != 1 {
		t.Fatalf("known commands must not be answered as unknown, got %d answers:\n%s", n, trackingComments(e))
	}
	if ap := e.autopilot(); ap.Items[1].State != aiv1.AutopilotItemSkipped || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("known commands must work as before: %+v", ap)
	}
	e.gl.AddNote(tracking, human, "/agent")
	e.gl.AddNote(tracking, human, "/agent revisit the API please")
	e.reconcile()
	if n := strings.Count(trackingComments(e), answer); n != 3 {
		t.Fatalf("a bare command and a malformed revisit must be answered, got %d answers:\n%s", n, trackingComments(e))
	}
}

// A run during a pause over its issue is told why autopilot paused; once the
// pause is over, the reason is gone from its task.
func TestAutopilotPauseReasonReachesTheIssueTask(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	e.reconcile()
	if got := e.tasks()[name].Spec.Source.AutopilotPause; !strings.Contains(got, "2 blocking review findings") {
		t.Fatalf("the pause reason must be copied onto the paused issue's task, got %q", got)
	}
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.Run.BlockingFindings, s.Run.RunCount = 0, 2 })
	e.reconcile()
	e.reconcile()
	if task, ok := e.tasks()[name]; ok && task.Spec.Source.AutopilotPause != "" {
		t.Fatalf("the pause reason must be cleared after the pause: %q", task.Spec.Source.AutopilotPause)
	}
}
