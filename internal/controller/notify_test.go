package controller

import (
	"context"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func (e *projectEnv) setNotify(n aiv1.NotifySpec) {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	p.Spec.Notify = n
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

func TestOwnersOfTasks(t *testing.T) {
	e := newProjectEnv(t)
	e.r.DefaultOwners = []string{"ops"}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\n\nBuild a new checkout."}
	e.gl.Milestones = []gitlab.Milestone{m}
	in := e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	mc := m
	in.Milestone = &mc
	e.gl.AddIssue(43, "Fix typo", "agent::working")
	owners := func(name string) []string { return e.tasks()[name].Spec.Owners }

	e.reconcile()
	for _, name := range []string{"shop-spec-m3-checkout-v2", "shop-42-add-paypal-button", "shop-43-fix-typo"} {
		if got := owners(name); !slices.Equal(got, []string{"ops"}) {
			t.Fatalf("%s: without project owners the operator's default counts, got %v", name, got)
		}
	}

	e.setNotify(aiv1.NotifySpec{Users: []string{"@lead"}})
	e.reconcile()
	if got := owners("shop-42-add-paypal-button"); !slices.Equal(got, []string{"lead"}) {
		t.Fatalf("the project's owners replace the default, got %v", got)
	}

	// The milestone names its own owners, as a paragraph of its own; that
	// is no change of the spec.
	e.gl.Milestones[0].Description = "/agent spec\n\n/agent owners @alice, bob\n\nBuild a new checkout."
	e.reconcile()
	if got := owners("shop-spec-m3-checkout-v2"); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Fatalf("milestone owners: %v", got)
	}
	if got := owners("shop-42-add-paypal-button"); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Fatalf("the milestone's issues take its owners: %v", got)
	}
	if got := owners("shop-43-fix-typo"); !slices.Equal(got, []string{"lead"}) {
		t.Fatalf("an issue without milestone keeps the project's owners: %v", got)
	}
	if rr := e.tasks()["shop-spec-m3-checkout-v2"].Spec.RunRequest; rr != 1 {
		t.Fatalf("changing the owners must not re-plan, run request %d", rr)
	}
}

func TestCIRedMentionsOwners(t *testing.T) {
	e := newCIEnv(t, "sha1")
	e.r.DefaultOwners = []string{"alice"}
	e.agentPushed("sha1", 2, 0) // no fix rounds left
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	if !strings.Contains(e.notes(), "still red") || !strings.Contains(e.notes(), "cc @alice") {
		t.Fatalf("red CI must mention the owners:\n%s", e.notes())
	}
}

func TestCIGreenRequestsReview(t *testing.T) {
	e := newCIEnv(t, "sha1")
	e.r.DefaultOwners = []string{"alice", "ghost"}
	e.gl.Users = []gitlab.User{{ID: 7, Username: "alice"}}
	mr, _ := e.gl.CreateMergeRequest(e.ctx, "group/app", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/42", TargetBranch: "main", Title: "Add PayPal button"})
	task := e.ciTask()
	orig := task.DeepCopy()
	task.Status.Run.MergeRequestIID = mr.IID
	if err := e.c.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.gl.AddPipeline("sha1", "agent/42", "success")
	e.reconcile()
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("green should move to review, has %v", labels)
	}
	got := e.gl.MRs[mr.IID]
	if len(got.Reviewers) != 1 || got.Reviewers[0].Username != "alice" {
		t.Fatalf("the known owners should review: %+v", got.Reviewers)
	}
	notes := e.gl.MRNotes[mr.IID]
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "Ready for review") || !strings.Contains(notes[0].Body, "cc @alice @ghost") {
		t.Fatalf("expected one review ping on the MR: %+v", notes)
	}
	// The same commit ending the loop again (a pipeline run again in
	// review) pings nobody again.
	task = e.ciTask()
	ci := task.Status.CI.DeepCopy()
	if ci.PingedFor != aiv1.NotifyReview {
		t.Fatalf("the ping must be recorded: %+v", ci)
	}
	is := *e.gl.Issues[42]
	if err := e.r.finishCI(e.ctx, e.gl, testProject().Spec.WithDefaults(), is, &task, ci, aiv1.CIStateSuccess, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(e.gl.MRNotes[mr.IID]); n != 1 {
		t.Fatalf("pinged twice for one commit: %d notes", n)
	}
	if e.notes() != "" {
		t.Fatalf("a green pipeline needs no issue comment, got %q", e.notes())
	}
}

func TestOwnersLineDoesNotSelectAMilestone(t *testing.T) {
	e := newProjectEnv(t)
	var p aiv1.AgentProject
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p)
	empty := ""
	p.Spec.Milestones.SpecMarker = &empty // every milestone with a description
	if err := e.c.Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	e.gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Later", State: "active", Description: "/agent owners @alice"}}
	e.reconcile()
	if len(e.tasks()) != 0 {
		t.Fatalf("an owners line alone is no spec: %v", keys(e.tasks()))
	}
}

func TestCIGreenWithoutReviewEvent(t *testing.T) {
	e := newCIEnv(t, "sha1")
	f := false
	e.setNotify(aiv1.NotifySpec{Users: []string{"alice"}, Events: []aiv1.NotifyEvent{aiv1.NotifyCIFailed}, RequestReview: &f})
	e.gl.Users = []gitlab.User{{ID: 7, Username: "alice"}}
	mr, _ := e.gl.CreateMergeRequest(e.ctx, "group/app", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/42", TargetBranch: "main", Title: "x"})
	task := e.ciTask()
	orig := task.DeepCopy()
	task.Status.Run.MergeRequestIID = mr.IID
	if err := e.c.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.gl.AddPipeline("sha1", "agent/42", "success")
	e.reconcile()
	if got := e.gl.MRs[mr.IID]; len(got.Reviewers) != 0 || len(e.gl.MRNotes[mr.IID]) != 0 {
		t.Fatalf("review pings are switched off: reviewers %+v, notes %+v", got.Reviewers, e.gl.MRNotes[mr.IID])
	}
}

func TestAutopilotPauseMentionsOwners(t *testing.T) {
	e := newAutopilotEnv(t)
	e.r.DefaultOwners = []string{"alice"}
	e.reconcile()
	e.reconcile()
	e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	if c := trackingComments(e); !strings.Contains(c, "Autopilot paused") || !strings.Contains(c, "cc @alice") {
		t.Fatalf("the pause must mention the owners:\n%s", c)
	}
}
