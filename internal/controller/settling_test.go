package controller

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestAutopilotSettling(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	posted := metav1.NewTime(now.Add(-time.Hour))
	settling := func(mutate func(st *aiv1.AgentTask)) *aiv1.AgentTask {
		st := &aiv1.AgentTask{
			Spec: aiv1.AgentTaskSpec{Autopilot: true, RunRequest: 2},
			Status: aiv1.AgentTaskStatus{
				Run:       aiv1.RunStatus{ObservedRunRequest: 2},
				Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone},
				Pilot: &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{NoteID: 42, PostedAt: &posted,
					FollowUps: []aiv1.PilotFollowUp{{Title: "Docs"}}}},
			},
		}
		if mutate != nil {
			mutate(st)
		}
		return st
	}
	for _, c := range []struct {
		name   string
		mutate func(st *aiv1.AgentTask)
		want   bool
	}{
		{"retrospective posted, window open", nil, true},
		{"retrospective failed", func(st *aiv1.AgentTask) { st.Status.Pilot.Retrospective.Failed = "the run crashed" }, false},
		{"follow-ups dismissed", func(st *aiv1.AgentTask) {
			st.Status.Pilot.Retrospective.DismissedAt = &metav1.Time{Time: now}
		}, false},
		{"follow-ups all created", func(st *aiv1.AgentTask) { st.Status.Pilot.Retrospective.Created = map[string]int64{"1": 20} }, false},
		{"window over", func(st *aiv1.AgentTask) {
			st.Status.Pilot.Retrospective.PostedAt = &metav1.Time{Time: now.Add(-aiv1.FollowUpWindow)}
		}, false},
		{"re-plan pending", func(st *aiv1.AgentTask) { st.Spec.RunRequest = 3 }, false},
		{"phase Running", func(st *aiv1.AgentTask) { st.Status.Autopilot.Phase = aiv1.AutopilotRunning }, false},
		{"autopilot off", func(st *aiv1.AgentTask) { st.Spec.Autopilot = false }, false},
		{"no autopilot status", func(st *aiv1.AgentTask) { st.Status.Autopilot = nil }, false},
		{"no retrospective yet", func(st *aiv1.AgentTask) { st.Status.Pilot.Retrospective = nil }, false},
		{"no pilot status", func(st *aiv1.AgentTask) { st.Status.Pilot = nil }, false},
	} {
		if got := autopilotSettling(settling(c.mutate), now); got != c.want {
			t.Errorf("%s: autopilotSettling = %v, want %v", c.name, got, c.want)
		}
	}
}

// settleGitLab records the GitLab calls of the autopilot sync (syncAutopilot)
// that read or write the milestone's issues, its tracking issue and its
// releases, in order. The project's other syncs, such as syncRevisits, are
// not recorded.
type settleGitLab struct {
	*gitlabfake.Fake
	calls []string
}

func (g *settleGitLab) record(call string) {
	pc := make([]uintptr, 32)
	frames := runtime.CallersFrames(pc[:runtime.Callers(3, pc)])
	for f, more := frames.Next(); ; f, more = frames.Next() {
		if strings.HasSuffix(f.Function, ".(*AgentProjectReconciler).syncAutopilot") {
			g.calls = append(g.calls, call)
			return
		}
		if !more {
			return
		}
	}
}

func (g *settleGitLab) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	g.record("ListIssues")
	return g.Fake.ListIssues(ctx, project, opts)
}

func (g *settleGitLab) GetIssue(ctx context.Context, project string, iid int64) (*gitlab.Issue, error) {
	g.record(fmt.Sprintf("GetIssue #%d", iid))
	return g.Fake.GetIssue(ctx, project, iid)
}

func (g *settleGitLab) UpdateIssue(ctx context.Context, project string, iid int64, opts gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	g.record(fmt.Sprintf("UpdateIssue #%d", iid))
	return g.Fake.UpdateIssue(ctx, project, iid, opts)
}

func (g *settleGitLab) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	g.record(fmt.Sprintf("ListIssueNotes #%d", iid))
	return g.Fake.ListIssueNotes(ctx, project, iid)
}

func (g *settleGitLab) ListPipelines(ctx context.Context, project string, opts gitlab.ListPipelinesOptions) ([]gitlab.Pipeline, error) {
	g.record("ListPipelines")
	return g.Fake.ListPipelines(ctx, project, opts)
}

func (g *settleGitLab) count(call string) int {
	n := 0
	for _, c := range g.calls {
		if c == call {
			n++
		}
	}
	return n
}

// newSettlingEnv is a done autopilot milestone with its issues closed whose
// retrospective has two open follow-ups, pinned to alice, and an hour of its
// window gone: it is settling. The GitLab calls are recorded from here on.
func newSettlingEnv(t *testing.T) (*projectEnv, *settleGitLab, gitlab.Note) {
	t.Helper()
	e := newPilotEnv(t)
	doneAndClosed(e)
	posted := metav1.NewTime(e.gl.Clock)
	note := e.gl.AddNote(e.autopilot().TrackingIssueIID, e.gl.Bot, "🧭 **Retrospective**\n\n1. Docs\n2. Flaky test\n"+marker.Retrospective)
	retroDone(e, aiv1.PilotRetrospective{NoteID: note.ID, PostedAt: &posted, OwnerIDs: []int64{alice.ID},
		FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}, {Title: "Flaky test"}}})
	e.gl.Clock = e.gl.Clock.Add(time.Hour)
	e.reconcile()
	e.reconcile()
	st := e.task(specTaskName)
	if !autopilotSettling(st, e.r.Now()) || e.gl.Milestones[0].State == "closed" {
		t.Fatalf("the milestone is settling: %+v %+v", st.Status.Autopilot, st.Status.Pilot)
	}
	// The setup's polls synced it: the next one is due.
	e.gl.Clock = e.gl.Clock.Add(settleInterval)
	gl := &settleGitLab{Fake: e.gl}
	e.r.NewGitLab = func(string, string) gitlab.API { return gl }
	return e, gl, note
}

// wantSettled fails unless the last poll synced the milestone on the
// settling path: its tracking issue's notes were read once, and first.
func wantSettled(e *projectEnv, gl *settleGitLab) {
	e.t.Helper()
	notes := fmt.Sprintf("ListIssueNotes #%d", e.autopilot().TrackingIssueIID)
	if len(gl.calls) == 0 || gl.calls[0] != notes || gl.count(notes) != 1 {
		e.t.Fatalf("a settling milestone reads its tracking issue's notes once, first: %q", gl.calls)
	}
}

// wantFull fails unless the last poll ran the full sync with the notes the
// settling path read: they are listed once, before the milestone's issues.
func wantFull(e *projectEnv, gl *settleGitLab) {
	e.t.Helper()
	notes := fmt.Sprintf("ListIssueNotes #%d", e.autopilot().TrackingIssueIID)
	if gl.count(notes) != 1 || gl.count("ListIssues") != 1 || slices.Index(gl.calls, notes) > slices.Index(gl.calls, "ListIssues") {
		e.t.Fatalf("a command takes the full sync, with the notes read once: %q", gl.calls)
	}
}

func TestSettlingMilestoneReadsOnlyItsTrackingIssueNotes(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.reconcile()
	if want := []string{fmt.Sprintf("ListIssueNotes #%d", tracking)}; !slices.Equal(gl.calls, want) {
		t.Fatalf("a quiet poll of a settling milestone makes one request: %q", gl.calls)
	}

	// The same, for the sync alone: any other GitLab call panics on the nil
	// API.
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	strict := &notesOnlyGitLab{fake: e.gl}
	st := e.task(specTaskName)
	before := st.Status.Autopilot.DeepCopy()
	if err := e.r.syncAutopilot(e.ctx, strict, nil, &project, project.Spec.WithDefaults(), st, nil, nil, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strict.notes != 1 {
		t.Fatalf("the sync lists the notes once: %d", strict.notes)
	}
	if got := e.autopilot(); got.TrackingHash != before.TrackingHash || got.Phase != aiv1.AutopilotDone {
		t.Fatalf("nothing changed: %+v", got)
	}
}

// notesOnlyGitLab answers ListIssueNotes from the fake; every other call
// panics on the nil embedded API.
type notesOnlyGitLab struct {
	gitlab.API
	fake  *gitlabfake.Fake
	notes int
}

func (g *notesOnlyGitLab) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	g.notes++
	return g.fake.ListIssueNotes(ctx, project, iid)
}

func TestSettlingMilestoneOpensAFollowUp(t *testing.T) {
	e, gl, retro := newSettlingEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddReply(tracking, retro.ID, alice, "create 1")
	e.reconcile()
	wantSettled(e, gl)
	docs := followUpIssues(e)["Docs"]
	if docs == nil || e.task(specTaskName).Status.Pilot.Retrospective.Created["1"] != docs.IID {
		t.Fatalf("create 1 opens the follow-up while settling: %v %q", followUpIssues(e), gl.calls)
	}
	if !autopilotSettling(e.task(specTaskName), e.r.Now()) || e.gl.Milestones[0].State == "closed" {
		t.Fatal("follow-up 2 is still open: the milestone keeps settling")
	}
}

func TestSettlingMilestoneClosesAfterCreateNone(t *testing.T) {
	e, gl, retro := newSettlingEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddReply(tracking, retro.ID, alice, "create none")
	e.reconcile()
	wantSettled(e, gl)
	if st := e.task(specTaskName); st.Status.Pilot.Retrospective.DismissedAt == nil || autopilotSettling(st, e.r.Now()) {
		t.Fatalf("create none dismisses the follow-ups: %+v", st.Status.Pilot.Retrospective)
	}
	if e.gl.Milestones[0].State == "closed" {
		t.Fatal("the settling poll does not close the milestone")
	}
	gl.calls = nil
	e.reconcile()
	if gl.count("ListIssues") != 1 || e.gl.Milestones[0].State != "closed" {
		t.Fatalf("the next poll is a full sync that closes the milestone: %s %q", e.gl.Milestones[0].State, gl.calls)
	}
}

func TestSettlingMilestoneClosesOnceTheWindowEnds(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	e.gl.Clock = e.gl.Clock.Add(aiv1.FollowUpWindow)
	e.reconcile()
	if gl.count("ListIssues") != 1 || e.gl.Milestones[0].State != "closed" {
		t.Fatalf("after the window the full sync closes the milestone: %s %q", e.gl.Milestones[0].State, gl.calls)
	}
}

func TestSettlingMilestoneTakesTheFullSyncForACommand(t *testing.T) {
	for _, c := range []struct {
		name    string
		command func(e *projectEnv)
		applied func(ap *aiv1.AutopilotStatus, st *aiv1.AgentTask) bool
	}{
		{"tracking issue", func(e *projectEnv) {
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent skip #1")
		}, func(ap *aiv1.AutopilotStatus, _ *aiv1.AgentTask) bool { return ap.LastCommandNoteID > 0 }},
		{"pilot", func(e *projectEnv) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
				s.Pilot.Commands = append(s.Pilot.Commands, aiv1.PilotCommand{Seq: s.Autopilot.LastPilotCommand + 1,
					Action: aiv1.PilotAction{Tool: "skip", IssueIID: 1}, Owner: alice.Username})
			})
		}, func(ap *aiv1.AutopilotStatus, st *aiv1.AgentTask) bool {
			return ap.LastPilotCommand == st.Status.Pilot.Commands[len(st.Status.Pilot.Commands)-1].Seq
		}},
		{"Desk", func(e *projectEnv) {
			st := e.task(specTaskName)
			st.Spec.Source.ChatMessages = append(st.Spec.Source.ChatMessages,
				aiv1.ChatMessage{ID: st.Spec.Source.LastChatCommandID + 1, Kind: "command", Text: `{"cmd":"pause"}`, Author: "ada@x"})
			if err := e.c.Update(e.ctx, st); err != nil {
				e.t.Fatal(err)
			}
		}, func(_ *aiv1.AutopilotStatus, st *aiv1.AgentTask) bool {
			return st.Spec.Source.LastChatCommandID == st.Spec.Source.ChatMessages[len(st.Spec.Source.ChatMessages)-1].ID
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, gl, _ := newSettlingEnv(t)
			lastNote := e.autopilot().LastCommandNoteID
			c.command(e)
			e.reconcile()
			wantFull(e, gl)
			st := e.task(specTaskName)
			if !c.applied(st.Status.Autopilot, st) || c.name == "tracking issue" && st.Status.Autopilot.LastCommandNoteID == lastNote {
				t.Fatalf("the command is applied: %+v", st.Status.Autopilot)
			}
		})
	}
}

func TestSettlingMilestoneMovesItsOpenReleases(t *testing.T) {
	for _, c := range []struct {
		state aiv1.ReleaseState
		moved bool
	}{{aiv1.ReleaseRunning, true}, {aiv1.ReleaseReleased, false}} {
		t.Run(string(c.state), func(t *testing.T) {
			e, gl, _ := newSettlingEnv(t)
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
				s.Autopilot.Releases = append(s.Autopilot.Releases, aiv1.ReleaseRecord{ID: nextReleaseID(s.Autopilot.Releases),
					By: aiv1.ReleaseByOwner, AfterIssue: 1, State: c.state, Version: "2026.11.1"})
			})
			e.reconcile()
			wantSettled(e, gl)
			if moved := gl.count("ListPipelines") > 0; moved != c.moved {
				t.Fatalf("a %s record is moved on: %v, want %v: %q", c.state, moved, c.moved, gl.calls)
			}
		})
	}
}

func TestDoneMilestoneTakesTheFullSyncWhenNotSettling(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(e *projectEnv)
	}{
		{"follow-ups settled", func(e *projectEnv) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
				s.Pilot.Retrospective.Created = map[string]int64{"1": 20, "2": 21}
			})
		}},
		{"retrospective failed", func(e *projectEnv) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Retrospective.Failed = "the run crashed" })
		}},
		{"re-plan pending", func(e *projectEnv) {
			st := e.task(specTaskName)
			st.Spec.RunRequest++
			if err := e.c.Update(e.ctx, st); err != nil {
				e.t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, gl, _ := newSettlingEnv(t)
			c.setup(e)
			if autopilotSettling(e.task(specTaskName), e.r.Now()) {
				t.Fatal("not settling")
			}
			e.reconcile()
			if gl.count("ListIssues") == 0 {
				t.Fatalf("the full sync lists the milestone's issues: %q", gl.calls)
			}
		})
	}
}

// milestoneGitLab counts the poll's ListIssues calls by state and milestone
// and its ListIssueNotes calls by issue.
type milestoneGitLab struct {
	*gitlabfake.Fake
	calls map[string]int
}

func (g *milestoneGitLab) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	g.calls[fmt.Sprintf("ListIssues %s %q", opts.State, opts.Milestone)]++
	return g.Fake.ListIssues(ctx, project, opts)
}

func (g *milestoneGitLab) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	g.calls[fmt.Sprintf("ListIssueNotes #%d", iid)]++
	return g.Fake.ListIssueNotes(ctx, project, iid)
}

// milestoneIssues is how many ListIssues calls named the milestone title,
// and how many of them with State all.
func (g *milestoneGitLab) milestoneIssues(title string) (n, all int) {
	for call, c := range g.calls {
		if strings.HasPrefix(call, "ListIssues ") && strings.HasSuffix(call, fmt.Sprintf(" %q", title)) {
			n += c
			if strings.HasPrefix(call, "ListIssues all ") {
				all += c
			}
		}
	}
	return n, all
}

// addSettlingMilestone adds an autopilot milestone, without a pilot, that is
// done with its retrospective posted on its tracking issue, and returns its
// spec task's name.
func addSettlingMilestone(e *projectEnv, iid int64) string {
	e.t.Helper()
	title := fmt.Sprintf("Done %d", iid)
	e.gl.Milestones = append(e.gl.Milestones, gitlab.Milestone{ID: 10 + iid, IID: iid, Title: title, State: "active", Description: "/agent autopilot\nDone."})
	tracking := 100 + iid
	e.gl.AddIssue(tracking, "Autopilot: "+title)
	note := e.gl.AddNote(tracking, e.gl.Bot, "🧭 **Retrospective**\n\n1. Docs\n"+marker.Retrospective)
	e.reconcile()
	name := ""
	for n, st := range e.tasks() {
		if st.Spec.Type == aiv1.TaskTypeSpec && st.Spec.Milestone != nil && st.Spec.Milestone.IID == iid {
			name = n
		}
	}
	if name == "" {
		e.t.Fatalf("no spec task for milestone %d: %v", iid, keys(e.tasks()))
	}
	runs := e.tasks()[name].Spec.RunRequest
	posted := metav1.NewTime(e.gl.Clock)
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = runs
		s.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, TrackingIssueIID: tracking}
		s.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{NoteID: note.ID, PostedAt: &posted,
			FollowUps: []aiv1.PilotFollowUp{{Title: "Docs"}}}}
	})
	if !autopilotSettling(e.task(name), e.r.Now()) {
		e.t.Fatalf("milestone %d is settling", iid)
	}
	return name
}

func TestSettlingMilestonesSyncEveryInterval(t *testing.T) {
	e, _, _ := newSettlingEnv(t)
	e.r.recordDecisions = func(store.IssueDecisions) {}
	settling := map[string]int64{"Checkout v2": e.autopilot().TrackingIssueIID} // title: tracking issue
	for _, iid := range []int64{4, 5} {
		addSettlingMilestone(e, iid)
		settling[fmt.Sprintf("Done %d", iid)] = 100 + iid
	}
	e.gl.Milestones = append(e.gl.Milestones, gitlab.Milestone{ID: 16, IID: 6, Title: "Running", State: "active", Description: "/agent autopilot\nBuild it."})
	e.reconcile()
	gl := &milestoneGitLab{Fake: e.gl, calls: map[string]int{}}
	e.r.NewGitLab = func(string, string) gitlab.API { return gl }
	// Two due polls: the first one also catches the revisits up with the
	// retrospectives just posted.
	for range 2 {
		e.gl.Clock = e.gl.Clock.Add(settleInterval)
		e.reconcile()
	}
	e.gl.Clock = e.gl.Clock.Add(settleInterval)

	start := e.gl.Clock
	// Polls that read each tracking issue's notes: the settle sync and the
	// revisits read them on a due poll, nothing on the others.
	read := map[string]int{}
	for at := time.Duration(0); ; at = min(at+2*time.Minute, settleInterval) {
		e.gl.Clock = start.Add(at)
		gl.calls = map[string]int{}
		e.reconcile()
		for title, tracking := range settling {
			n, all := gl.milestoneIssues(title)
			if all > 0 || at > 0 && at < settleInterval && n > 0 {
				t.Fatalf("at %v milestone %q lists its issues: %v", at, title, gl.calls)
			}
			if n := gl.calls[fmt.Sprintf("ListIssueNotes #%d", tracking)]; n > 2 {
				t.Fatalf("at %v the notes of %q are read %d times", at, title, n)
			} else if n > 0 {
				read[title]++
			}
		}
		if _, all := gl.milestoneIssues("Running"); all == 0 {
			t.Fatalf("at %v the running milestone takes the full sync: %v", at, gl.calls)
		}
		if at < settleInterval {
			for title := range settling {
				if n := read[title]; n != 1 {
					t.Fatalf("by %v the tracking issue of %q was read on %d polls, want one", at, title, n)
				}
			}
		}
		if at == settleInterval {
			break
		}
	}
	for title := range settling {
		if n := read[title]; n != 2 {
			t.Fatalf("at %v the tracking issue of %q is read again: %d polls", settleInterval, title, n)
		}
	}
	if st := e.task(specTaskName); !st.Status.Autopilot.SettleSyncedAt.Time.Equal(start.Add(settleInterval)) {
		t.Fatalf("settleSyncedAt is the last sync: %v", st.Status.Autopilot.SettleSyncedAt)
	}

	// A restart keeps the time in the status: no milestone syncs at once.
	e.r = &AgentProjectReconciler{Client: e.c, Scheme: e.r.Scheme, Now: e.r.Now, recordDecisions: e.r.recordDecisions,
		NewGitLab: func(string, string) gitlab.API { return gl }}
	e.gl.Clock = e.gl.Clock.Add(2 * time.Minute)
	gl.calls = map[string]int{}
	e.reconcile()
	for title, tracking := range settling {
		if n := gl.calls[fmt.Sprintf("ListIssueNotes #%d", tracking)]; n != 0 {
			t.Fatalf("after a restart %q is not synced before it is due: %v", title, gl.calls)
		}
	}
}

func TestSettlingMilestoneWakesForADeskCommand(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	e.reconcile()
	wantSettled(e, gl)
	gl.calls = nil
	e.reconcile()
	if len(gl.calls) != 0 {
		t.Fatalf("a poll right after a settle sync leaves the milestone alone: %q", gl.calls)
	}
	st := e.task(specTaskName)
	st.Spec.Source.ChatMessages = append(st.Spec.Source.ChatMessages,
		aiv1.ChatMessage{ID: st.Spec.Source.LastChatCommandID + 1, Kind: "command", Text: `{"cmd":"pause"}`, Author: "ada@x"})
	if err := e.c.Update(e.ctx, st); err != nil {
		t.Fatal(err)
	}
	e.gl.Clock = e.gl.Clock.Add(time.Minute)
	e.reconcile()
	wantFull(e, gl)
	st = e.task(specTaskName)
	if st.Spec.Source.LastChatCommandID != st.Spec.Source.ChatMessages[len(st.Spec.Source.ChatMessages)-1].ID {
		t.Fatalf("the Desk command is applied on the next poll: %+v", st.Spec.Source)
	}
}

func TestSettlingMilestoneTakesTheFullSyncAfterAReplan(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	e.reconcile()
	wantSettled(e, gl)
	st := e.task(specTaskName)
	st.Spec.RunRequest++
	if err := e.c.Update(e.ctx, st); err != nil {
		t.Fatal(err)
	}
	gl.calls = nil
	e.gl.Clock = e.gl.Clock.Add(time.Minute)
	e.reconcile()
	if gl.count("ListIssues") != 1 {
		t.Fatalf("a re-plan right after a settle sync takes the full sync: %q", gl.calls)
	}
}

func TestSettlingMilestoneOpensAFollowUpWithinTheInterval(t *testing.T) {
	e, gl, retro := newSettlingEnv(t)
	e.reconcile()
	wantSettled(e, gl)
	synced := e.gl.Clock
	e.gl.AddReply(e.autopilot().TrackingIssueIID, retro.ID, alice, "create 1")
	// Polls every 2m, up to one settleInterval after the sync.
	for at := 2 * time.Minute; ; at = min(at+2*time.Minute, settleInterval) {
		e.gl.Clock = synced.Add(at)
		e.reconcile()
		if opened := followUpIssues(e)["Docs"] != nil; opened != (at == settleInterval) {
			t.Fatalf("at %v the follow-up is opened: %v, want only once the sync is due", at, opened)
		}
		if at == settleInterval {
			break
		}
	}
	if e.task(specTaskName).Status.Pilot.Retrospective.Created["1"] == 0 {
		t.Fatal("the retrospective records the created follow-up")
	}
}

// A command that leaves Done ends settling: the full-sync fallback applies
// it, and later polls are full syncs while the phase is not Done.
func TestSettlingMilestoneTakesTheFullSyncAfterACommandLeavesDone(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	e.reconcile()
	wantSettled(e, gl)
	e.gl.Issues[1].State = "opened"
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent unskip #1")
	e.gl.Clock = e.gl.Clock.Add(settleInterval)
	gl.calls = nil
	e.reconcile()
	wantFull(e, gl)
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotDone || autopilotSettling(e.task(specTaskName), e.r.Now()) {
		t.Fatalf("the unskip leaves Done: %+v", ap)
	}
	for range 2 {
		gl.calls = nil
		e.gl.Clock = e.gl.Clock.Add(2 * time.Minute)
		e.reconcile()
		if gl.count("ListIssues") != 1 {
			t.Fatalf("a running milestone takes the full sync: %q", gl.calls)
		}
	}
}

func TestSettlingMilestoneClosesOnTheFirstPollAfterTheWindow(t *testing.T) {
	e, gl, _ := newSettlingEnv(t)
	end := e.task(specTaskName).Status.Pilot.Retrospective.PostedAt.Add(aiv1.FollowUpWindow)
	e.gl.Clock = end.Add(-time.Minute)
	e.reconcile()
	wantSettled(e, gl)
	if e.gl.Milestones[0].State == "closed" {
		t.Fatal("the window is still open")
	}
	e.gl.Clock = end
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("the first poll after the window closes the milestone, synced or not: %q", gl.calls)
	}
}
