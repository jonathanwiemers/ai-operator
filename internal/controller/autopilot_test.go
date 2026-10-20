package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

const specTaskName = "shop-spec-m3-checkout-v2"

// newAutopilotEnv is an autopilot milestone that was planned into #1 (API)
// and #2 (UI, after the API), both drafts.
func newAutopilotEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent autopilot\nBuild a new checkout."}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.reconcile()
	st := e.tasks()[specTaskName]
	if !st.Spec.Autopilot {
		t.Fatalf("the autopilot marker must plan the milestone on autopilot: %+v", st.Spec)
	}
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
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}}
	})
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
		{name: "conflict", mr: func(m *gitlab.MergeRequest) { m.DetailedMergeStatus, m.Draft = "conflict", false }, reason: "merge conflicts"},
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
