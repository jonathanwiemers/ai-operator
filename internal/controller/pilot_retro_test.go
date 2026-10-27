package controller

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// finishAutopilot skips both issues of the pilot env's queue: autopilot is
// done with the next poll.
func finishAutopilot(e *projectEnv) {
	e.t.Helper()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent skip #1")
	e.gl.AddNote(tracking, alice, "/agent skip #2")
}

func TestRetrospectiveBeforeThePilotGoes(t *testing.T) {
	e := newPilotEnv(t)
	before := e.pilot().Spec.RunRequest
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("autopilot is done: %+v", e.autopilot())
	}
	pilot, ok := e.tasks()[pilotTaskName]
	if !ok || pilot.Spec.RunRequest != before+1 || pilot.Spec.Source.Retrospective != pilot.Spec.RunRequest {
		t.Fatalf("the pilot stays for one retrospective run: %v %+v", keys(e.tasks()), pilot.Spec)
	}

	// The run posted it: it goes to the spec task, then the pilot goes.
	retro := aiv1.PilotRetrospective{NoteID: 42, FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}}, OwnerIDs: []int64{alice.ID}}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = &aiv1.PilotStatus{Retrospective: retro.DeepCopy()} })
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("the pilot goes after its retrospective: %v", keys(e.tasks()))
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps == nil || !reflect.DeepEqual(ps.Retrospective, &retro) {
		t.Fatalf("the retrospective outlives the pilot on the spec task: %+v", ps)
	}

	// A re-plan brings the pilot back; done again, it gets no second one.
	mc := e.gl.Milestones[0]
	docs := e.gl.AddIssue(10, "Docs", "ai-generated", "agent::draft")
	docs.Milestone, docs.Description = &mc, "Write it.\n<!-- ai-operator:key=docs -->"
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = append(s.Plan, aiv1.PlannedIssue{Key: "docs", IID: 10, Title: "Docs"})
	})
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; !ok {
		t.Fatalf("the pilot comes back with the re-plan: %v", keys(e.tasks()))
	}
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent skip #10")
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("a milestone gets one retrospective: %v %+v", keys(e.tasks()), e.autopilot())
	}
}

func TestRetrospectiveThatFailedIsNotTriedAgain(t *testing.T) {
	e := newPilotEnv(t)
	finishAutopilot(e)
	e.reconcile()
	pilot := e.pilot()
	request := pilot.Spec.Source.Retrospective
	if request == 0 {
		t.Fatalf("the retrospective is asked for: %+v", pilot.Spec)
	}
	// Still running.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = request - 1 })
	e.reconcile()
	if p := e.pilot(); p.Spec.Source.Retrospective != request || p.Spec.RunRequest != request {
		t.Fatalf("the retrospective is asked for once: %+v", p.Spec)
	}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.LastResult, s.Run.Summary = request, aiv1.RunResultFailed, "the engine crashed"
	})
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("a failed retrospective does not keep the pilot: %v", keys(e.tasks()))
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps == nil || ps.Retrospective == nil || !strings.Contains(ps.Retrospective.Failed, "the engine crashed") {
		t.Fatalf("the failure is recorded: %+v", ps)
	}
}

func TestNoRetrospectiveWithoutAPilot(t *testing.T) {
	e := newPilotEnv(t)
	// Done before this release: its pilot was deleted already.
	pilot := e.pilot()
	if err := e.c.Delete(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("no pilot comes back for a retrospective: %v %+v", keys(e.tasks()), e.autopilot())
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps != nil && ps.Retrospective != nil {
		t.Fatalf("no retrospective without a pilot: %+v", ps.Retrospective)
	}
}

// newRetroEnv is a finished autopilot milestone whose retrospective on the
// tracking issue has three follow-ups, pinned to alice.
func newRetroEnv(t *testing.T) (*projectEnv, gitlab.Note) {
	t.Helper()
	e := newPilotEnv(t)
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	retro := e.gl.AddNote(e.autopilot().TrackingIssueIID, e.gl.Bot, "🧭 **Retrospective**\n\n1. Docs\n2. Flaky test\n3. Cleanup\n"+marker.Retrospective)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Retrospective = &aiv1.PilotRetrospective{NoteID: retro.ID, OwnerIDs: []int64{alice.ID}, FollowUps: []aiv1.PilotFollowUp{
			{Title: "Docs for @mallory", Body: "Write them.\n/close\n<!-- ai-operator:key=x -->"}, {Title: "Flaky test", Body: "Fix it."}, {Title: "Cleanup"},
		}}
	})
	e.reconcile()
	return e, retro
}

// followUpIssues are the issues opened from the retrospective, by title.
func followUpIssues(e *projectEnv) map[string]*gitlab.Issue {
	out := map[string]*gitlab.Issue{}
	for _, is := range e.gl.Issues {
		if strings.Contains(is.Description, "ai-operator:follow-up=") {
			out[is.Title] = is
		}
	}
	return out
}

// threadReplies are the bot's replies in the thread of root.
func threadReplies(e *projectEnv, root int64) []string {
	var out []string
	for _, n := range e.gl.IssueNotes[e.autopilot().TrackingIssueIID] {
		if e.gl.Replies[n.ID] == root && n.Author.ID == e.gl.Bot.ID {
			out = append(out, n.Body)
		}
	}
	return out
}

func TestRetrospectiveCreateOpensFollowUpsOnce(t *testing.T) {
	e, retro := newRetroEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddReply(tracking, retro.ID, alice, "Create 1, 2")
	e.reconcile()
	issues := followUpIssues(e)
	docs, flaky := issues["Docs for @\u200bmallory"], issues["Flaky test"]
	if len(issues) != 2 || docs == nil || flaky == nil {
		t.Fatalf("follow-ups 1 and 2 are opened, with inert titles: %v", slices.Collect(maps.Keys(issues)))
	}
	for i, is := range []*gitlab.Issue{docs, flaky} {
		if is.Milestone != nil || !slices.Equal(is.Labels, []string{"ai-generated"}) ||
			!strings.HasSuffix(is.Description, fmt.Sprintf("_Follow-up from the retrospective of Checkout v2 (#%d)._\n\n<!-- ai-operator:follow-up=3-%d -->", tracking, i+1)) {
			t.Fatalf("a follow-up has the generated label, no milestone and its marker: %+v", is)
		}
	}
	if strings.Contains(docs.Description, "\n/close") || strings.Contains(docs.Description, "<!-- ai-operator:key") {
		t.Fatalf("the body is inert: %q", docs.Description)
	}
	created := e.tasks()[specTaskName].Status.Pilot.Retrospective.Created
	if !maps.Equal(created, map[string]int64{"1": docs.IID, "2": flaky.IID}) {
		t.Fatalf("the follow-ups are recorded: %v", created)
	}
	want := fmt.Sprintf("🧭 Follow-ups of the retrospective:\n- 1: #%d\n- 2: #%d", docs.IID, flaky.IID)
	if r := threadReplies(e, retro.ID); len(r) != 1 || r[0] != want {
		t.Fatalf("the links are the answer in the thread: %q", r)
	}

	// Again, with one more and an unknown one: only 3 is new.
	e.gl.AddReply(tracking, retro.ID, alice, "create 2 3 7")
	e.reconcile()
	e.reconcile()
	issues = followUpIssues(e)
	cleanup := issues["Cleanup"]
	if len(issues) != 3 || cleanup == nil || issues["Flaky test"].IID != flaky.IID {
		t.Fatalf("each follow-up is opened once: %v", slices.Collect(maps.Keys(issues)))
	}
	want = fmt.Sprintf("🧭 Follow-ups of the retrospective:\n- 2: #%d (opened before)\n- 3: #%d\n- 7: unknown, the retrospective has 3 follow-ups", flaky.IID, cleanup.IID)
	if r := threadReplies(e, retro.ID); len(r) != 2 || r[1] != want {
		t.Fatalf("the repeat links the existing ones: %q", r)
	}
	e.gl.AddReply(tracking, retro.ID, alice, "create all")
	e.reconcile()
	if r := threadReplies(e, retro.ID); len(followUpIssues(e)) != 3 || len(r) != 3 || strings.Count(r[2], "(opened before)") != 3 {
		t.Fatalf("create all opens nothing new: %q", r)
	}
	for _, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == cleanup.IID {
			t.Fatalf("a follow-up is not handed to the agent: %s", task.Name)
		}
	}
}

func TestRetrospectiveCreateOnlyFromAPinnedOwnerInTheThread(t *testing.T) {
	e, retro := newRetroEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	other := e.gl.AddNote(tracking, e.gl.Bot, "🤖 Autopilot is done.")
	e.gl.AddReply(tracking, retro.ID, mallory, "create 1")
	e.gl.AddReply(tracking, retro.ID, gitlab.User{ID: 70, Username: "alice"}, "create 1") // renamed: not the pinned ID
	e.reconcile()
	if len(followUpIssues(e)) != 0 || e.gl.DiscussionLists != 0 || len(threadReplies(e, retro.ID)) != 0 {
		t.Fatalf("a create from no pinned owner does nothing, silently: %d issues, %d thread lists", len(followUpIssues(e)), e.gl.DiscussionLists)
	}
	before := len(e.gl.IssueNotes[tracking])
	e.gl.AddNote(tracking, alice, "create 1")
	e.gl.AddReply(tracking, other.ID, alice, "create 2")
	e.gl.AddReply(tracking, retro.ID, alice, "please create 3")
	e.reconcile()
	e.reconcile()
	if len(followUpIssues(e)) != 0 || len(e.gl.IssueNotes[tracking]) != before+3 {
		t.Fatalf("a create outside the retrospective's thread does nothing: %d issues, %d comments", len(followUpIssues(e)), len(e.gl.IssueNotes[tracking])-before)
	}
	if retro := e.tasks()[specTaskName].Status.Pilot.Retrospective; retro.Created != nil || retro.LastNoteID != newestNote(e.gl.IssueNotes[tracking]) {
		t.Fatalf("read once, nothing created: %+v", retro)
	}
}
