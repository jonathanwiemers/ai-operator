package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
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

func TestRetrospectiveCreateNoneDismissesTheRestOnce(t *testing.T) {
	e, retro := newRetroEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddReply(tracking, retro.ID, alice, "create 1")
	e.reconcile()
	docs := followUpIssues(e)["Docs for @\u200bmallory"]
	if docs == nil {
		t.Fatal("follow-up 1 is opened")
	}

	e.gl.AddReply(tracking, retro.ID, alice, "Create none.")
	e.reconcile()
	got := e.tasks()[specTaskName].Status.Pilot.Retrospective
	if got.DismissedBy != alice.Username || got.DismissedAt == nil || !maps.Equal(got.Created, map[string]int64{"1": docs.IID}) {
		t.Fatalf("an owner's create none dismisses the rest, what was opened stays: %+v", got)
	}
	if !followUpsSettled(got, e.r.Now()) {
		t.Fatal("dismissed follow-ups are settled")
	}
	want := "🧭 The remaining follow-ups are dismissed; the milestone can close. `create 1` still opens one."
	if r := threadReplies(e, retro.ID); len(r) != 2 || r[1] != want {
		t.Fatalf("the dismissal is answered in the thread: %q", r)
	}
	at := got.DismissedAt

	// Again: recorded and answered once.
	e.gl.AddReply(tracking, retro.ID, alice, "create none")
	e.reconcile()
	if got := e.tasks()[specTaskName].Status.Pilot.Retrospective; !got.DismissedAt.Equal(at) || len(threadReplies(e, retro.ID)) != 2 {
		t.Fatalf("a repeat changes nothing: %+v %q", got, threadReplies(e, retro.ID))
	}

	// Dismissal settles; it does not forbid.
	e.gl.AddReply(tracking, retro.ID, alice, "create 2")
	e.reconcile()
	flaky := followUpIssues(e)["Flaky test"]
	if flaky == nil || e.tasks()[specTaskName].Status.Pilot.Retrospective.Created["2"] != flaky.IID {
		t.Fatalf("create 2 after create none opens it: %v", slices.Collect(maps.Keys(followUpIssues(e))))
	}
}

func TestRetrospectiveCreateNoneOnlyFromAPinnedOwnerInTheThread(t *testing.T) {
	e, retro := newRetroEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	other := e.gl.AddNote(tracking, e.gl.Bot, "🤖 Autopilot is done.")
	e.gl.AddReply(tracking, retro.ID, mallory, "create none")
	e.gl.AddNote(tracking, alice, "create none")
	e.gl.AddReply(tracking, other.ID, alice, "create none")
	e.reconcile()
	e.reconcile()
	if got := e.tasks()[specTaskName].Status.Pilot.Retrospective; got.DismissedAt != nil || got.DismissedBy != "" || len(threadReplies(e, retro.ID)) != 0 {
		t.Fatalf("a create none from no owner or outside the thread does nothing: %+v %q", got, threadReplies(e, retro.ID))
	}
}

// fakeSpend is a spend source with one answer, recording the scopes asked.
type fakeSpend struct {
	spend  store.Spend
	err    error
	scopes []store.SpendScope
}

func (f *fakeSpend) Spend(_ context.Context, sc store.SpendScope) (store.Spend, error) {
	f.scopes = append(f.scopes, sc)
	return f.spend, f.err
}

func TestRetrospectiveRequestCarriesTheSpend(t *testing.T) {
	e := newPilotEnv(t)
	known := func(usd float64, tokens int64) store.SpendTotals {
		return store.SpendTotals{CostUSD: usd, CostKnown: true, Calls: 1, InputTokens: tokens}
	}
	src := &fakeSpend{spend: store.Spend{
		Totals: store.SpendTotals{CostUSD: 12.5, CostKnown: true, Calls: 9, InputTokens: 100, OutputTokens: 20, CacheReadTokens: 3000, CacheWriteTokens: 400, SubagentCalls: 4, UnmeteredCalls: 2, OldBasisCalls: 3},
		Children: []store.SpendChild{
			{Level: store.SpendIssue, Key: "1", Label: "#1", Totals: known(2, 10)},
			{Level: store.SpendMilestone, Key: "3", Label: "Milestone %3", Totals: known(9, 10)},
			{Level: store.SpendIssue, Key: "2", Label: "#2", Totals: known(5, 20)},
			{Level: store.SpendIssue, Key: "4", Label: "#4", Totals: store.SpendTotals{Calls: 2, UnknownCalls: 2, InputTokens: 900}},
			{Level: store.SpendIssue, Key: "5", Label: "#5", Totals: known(1, 5)},
		},
		ByKind:  []store.SpendBreakdown{{Key: "implement", Totals: known(8, 50)}, {Key: "review", Totals: known(3, 40)}, {Key: "spec", Totals: known(1, 1)}, {Key: "retro", Totals: known(0.5, 1)}},
		ByModel: []store.SpendBreakdown{{Key: "claude-opus", Totals: known(10, 70)}, {Key: "claude-haiku", Totals: known(0.5, 30)}, {Key: "auto", Totals: store.SpendTotals{Calls: 2, UnknownCalls: 2, UnmeteredCalls: 2}}},
	}}
	e.r.Spend = src
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	pilot := e.pilot()
	if pilot.Spec.Source.Retrospective == 0 {
		t.Fatalf("the retrospective is asked for: %+v", pilot.Spec)
	}
	if len(src.scopes) != 1 || src.scopes[0] != (store.SpendScope{Namespace: pilot.Namespace, Project: pilot.Spec.ProjectRef, MilestoneIID: pilot.Spec.Milestone.IID}) {
		t.Fatalf("the milestone's spend is read once with the request: %+v", src.scopes)
	}
	want := &aiv1.SpendSummary{CostUSD: "12.5000", CostKnown: true, SubagentCalls: 4, UnmeteredCalls: 2, Calls: 9, OldBasisCalls: 3,
		Tokens:    aiv1.SpendTokens{Input: 100, Output: 20, CacheRead: 3000, CacheWrite: 400},
		TopIssues: []aiv1.SpendDriver{{Key: "2", Label: "#2", CostUSD: "5.0000", Tokens: 20}, {Key: "1", Label: "#1", CostUSD: "2.0000", Tokens: 10}, {Key: "5", Label: "#5", CostUSD: "1.0000", Tokens: 5}},
		TopKinds:  []aiv1.SpendDriver{{Key: "implement", Label: "implement", CostUSD: "8.0000", Tokens: 50}, {Key: "review", Label: "review", CostUSD: "3.0000", Tokens: 40}, {Key: "spec", Label: "spec", CostUSD: "1.0000", Tokens: 1}},
		TopModels: []aiv1.SpendDriver{{Key: "claude-opus", Label: "claude-opus", CostUSD: "10.0000", Tokens: 70}, {Key: "claude-haiku", Label: "claude-haiku", CostUSD: "0.5000", Tokens: 30}, {Key: "auto", Label: "cursor auto (unmetered)", Unmetered: true}},
	}
	if got := pilot.Spec.Source.RetroSpend; !reflect.DeepEqual(got, want) {
		t.Fatalf("the request carries the milestone's spend with its top 3 drivers:\n got %+v\nwant %+v", got, want)
	}
}

// #274: the chain from the history database to the retrospective request.
// A milestone with one claude call (cost and tokens) and one cursor auto
// call that reported nothing, as a review cursor ran without usage (or that
// failed on its usage limit without a result) leaves it. The real store's
// spend becomes RetroSpend with the count and the auto row not metered.
func TestRetroSpendOfACursorAutoCall(t *testing.T) {
	s := storetest.New(t)
	e := newPilotEnv(t)
	pilot := e.pilot()
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	cost := 0.5
	opus := "claude-opus-5-5"
	claude := store.Call{Key: "c1", At: at, Namespace: pilot.Namespace, Project: pilot.Spec.ProjectRef, MilestoneIID: pilot.Spec.Milestone.IID,
		IssueIID: 1, Task: "shop-1", Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude", Model: opus,
		InputTokens: 10, OutputTokens: 20, CacheReadTokens: 300, CostUSD: &cost, CostSource: "reported", ExpiresAt: exp,
		Models: []store.CallModel{{Model: opus, InputTokens: 10, OutputTokens: 20, CacheReadTokens: 300, CostUSD: &cost}}}
	cursor := store.Call{Key: "c2", At: at.Add(time.Minute), Namespace: pilot.Namespace, Project: pilot.Spec.ProjectRef, MilestoneIID: pilot.Spec.Milestone.IID,
		IssueIID: 1, Task: "shop-1", Run: 1, Seq: 2, Kind: "review", Role: "bugbot", Engine: "cursor", Model: "auto",
		CostSource: "unknown", ExpiresAt: exp}
	if _, err := s.InsertCalls(ctx, []store.Call{claude, cursor}); err != nil {
		t.Fatal(err)
	}
	e.r.Spend = s
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	got := e.pilot().Spec.Source.RetroSpend
	if got == nil {
		t.Fatal("the retrospective request carries no spend")
	}
	if got.UnmeteredCalls != 1 || !got.CostKnown || got.CostUSD != "0.5000" {
		t.Errorf("spend = %+v, want 1 unmetered call next to a known $0.50", got)
	}
	// Neither call records a token basis: both are on the old one.
	if got.Calls != 2 || got.OldBasisCalls != 2 {
		t.Errorf("spend = %+v, want 2 calls, both on the old token basis", got)
	}
	var auto *aiv1.SpendDriver
	for i, d := range got.TopModels {
		if d.Key == "auto" {
			auto = &got.TopModels[i]
		} else if d.Unmetered {
			t.Errorf("%s is metered: %+v", d.Key, d)
		}
	}
	if auto == nil || !auto.Unmetered || auto.Label != "cursor auto (unmetered)" || auto.CostUSD != "" || auto.Tokens != 0 {
		t.Errorf("auto model = %+v, want unmetered, labelled, without cost or tokens (models %+v)", auto, got.TopModels)
	}
	if want := (aiv1.SpendDriver{Key: "review", Label: "review", Unmetered: true}); !slices.Contains(got.TopKinds, want) {
		t.Errorf("kinds %+v, want %+v", got.TopKinds, want)
	}
}

func TestRetrospectiveRequestWithoutSpend(t *testing.T) {
	e := newPilotEnv(t)
	e.r.Spend = &fakeSpend{err: errors.New("database down")}
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if p := e.pilot(); p.Spec.Source.Retrospective == 0 || p.Spec.Source.RetroSpend != nil {
		t.Fatalf("a failed query still asks for the retrospective, without spend: %+v", p.Spec.Source)
	}
}
