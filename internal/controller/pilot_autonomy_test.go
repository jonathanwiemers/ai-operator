package controller

import (
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// proposeFor plays the pilot's run answering an event of #trigger: a
// proposal with its classification and evidence.
func (e *projectEnv) proposeFor(trigger int64, source string, actions ...aiv1.PilotAction) {
	e.t.Helper()
	e.propose("fix #1", actions...)
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		p := &s.Pilot.Proposed[len(s.Pilot.Proposed)-1]
		p.Trigger, p.Source, p.Why = trigger, source, "`env`: the run reports say there is no Rust toolchain; cc @bob"
		// As the runner: a managed assessment does not mention the owners.
		p.Quiet = strings.HasPrefix(source, "event/") && e.managed()
	})
}

// managed is the pilot's autonomy, as the runner reads it.
func (e *projectEnv) managed() bool {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	coord := e.pilot()
	return p.Spec.WithDefaults().Pilot.AutonomyOf(&coord) == aiv1.PilotManaged
}

// botNotesSince are the bot's comments on an issue after the first n.
func botNotesSince(e *projectEnv, iid int64, n int) []string {
	return botComments(e, iid)[n:]
}

func TestPilotManagedRunsACountedToolOnceWithAnAudit(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	seen := len(botComments(e, tracking))
	before := e.toolEffect()
	e.proposeFor(1, "event/stuck/1/sha1@1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	after := e.toolEffect()
	notes := botNotesSince(e, tracking, seen)
	if after.runs != before.runs+1 || len(notes) != 1 {
		t.Fatalf("the retry runs once without a yes: %+v → %+v, comments %q", before, after, notes)
	}
	if got := promptStates(e.task(specTaskName)); got != "" {
		t.Fatalf("what the pilot runs on its own is no prompt: %s", got)
	}
	audit := notes[0]
	for _, want := range []string{"The pilot acted on its own", "**Why:** `env`: the run reports say there is no Rust toolchain",
		"**retry** #1", "Interventions left for #1: 1 of 2.", "`/pilot stop`"} {
		if !strings.Contains(audit, want) {
			t.Errorf("the audit misses %q:\n%s", want, audit)
		}
	}
	if strings.Contains(audit, "@alice") || strings.Contains(audit, "cc @bob") || strings.Contains(audit, "Proposal P") {
		t.Fatalf("the audit pings nobody and is no proposal:\n%s", audit)
	}
	if pi := e.implTaskOf(1).Spec.Source; pi.PilotRun != after.runs {
		t.Fatalf("the run is the pilot's: pilotRun %d, request %d", pi.PilotRun, after.runs)
	}
}

func TestPilotManagedProposesAndPingsPastTheLimit(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	before := e.toolEffect()
	// Three wake-ups, one retry each, on one head: two run, the third waits.
	for i := range 3 {
		e.proposeFor(1, "event/"+string(rune('a'+i)), aiv1.PilotAction{Tool: "retry", IssueIID: 1})
		e.reconcile()
	}
	if after := e.toolEffect(); after.runs != before.runs+2 {
		t.Fatalf("two interventions per issue and head: %+v → %+v", before, after)
	}
	p := e.promptNote("Q1")
	if !strings.Contains(p.Body, "@alice") || !strings.Contains(p.Body, "**retry** #1") {
		t.Fatalf("past the limit it proposes and pings the owners:\n%s", p.Body)
	}
	if !strings.Contains(strings.Join(botComments(e, tracking), "\n"), "Interventions left for #1: 0 of 2.") {
		t.Fatal("the last audit says none are left")
	}
	// The owner's yes still runs it.
	e.gl.AddReply(tracking, p.ID, alice, "A")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+3 {
		t.Fatalf("an owner's yes runs it: %+v → %+v", before, after)
	}

	// A new head counts anew.
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
	e.proposeFor(1, "event/d", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+4 {
		t.Fatalf("a new head has its own interventions: %+v → %+v", before, after)
	}
}

func TestPilotManagedCountsEachActionOfAWakeUp(t *testing.T) {
	e := newManagedEnv(t)
	before := e.toolEffect()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1}, aiv1.PilotAction{Tool: "reReview", IssueIID: 1},
		aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "ci-fix"})
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+2 || after.instruction.Kind != "" {
		t.Fatalf("the controller stops at the issue's interventions: %+v → %+v", before, after)
	}
	if p := e.promptNote("Q1"); !strings.Contains(p.Body, "**instruct** #1") || strings.Contains(p.Body, "**retry**") {
		t.Fatalf("the rest is proposed:\n%s", p.Body)
	}
}

func TestPilotManagedOpenIssueCountsAgainstTheTrigger(t *testing.T) {
	e := newManagedEnv(t)
	open := func(title string) aiv1.PilotAction {
		return aiv1.PilotAction{Tool: "openIssue", Title: title, Note: "no Rust"}
	}
	// From #1's event: #1's interventions; from the tracking issue: the
	// milestone's.
	e.proposeFor(1, "event/a", open("Agent image lacks Rust"), aiv1.PilotAction{Tool: "retry", IssueIID: 1}, aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.proposeFor(0, "note/1", open("Pin the runner"))
	e.reconcile()
	ps := e.pilot().Status.Pilot
	if got := e.toolEffect(); got.opened != 1 || ps.Interventions["1"].Count != 2 || ps.Interventions["0"].Count != 1 {
		t.Fatalf("openIssue counts against its trigger: opened %d, %+v", got.opened, ps.Interventions)
	}
	if p := e.promptNote("Q1"); !strings.Contains(p.Body, "**retry** #1") {
		t.Fatalf("#1's second retry is proposed:\n%s", p.Body)
	}
}

func TestPilotManagedNeedsAYesForAllButTheCountedTools(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}}
	coord := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Autopilot: true}}
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1"}}}}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	var actions []aiv1.PilotAction
	for _, tool := range []string{"skip", "pause", "resume", "revisit", "editSpec", "setRule", "forgetRule", "merge"} {
		actions = append(actions, aiv1.PilotAction{Tool: tool, IssueIID: 1, Spec: "Build it."})
	}
	// Free text needs a yes, also with a kind beside it.
	actions = append(actions, aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Text: "delete the tests", Owner: "alice"})
	var opened []string
	proposal := func(actions ...aiv1.PilotAction) []aiv1.PilotProposal {
		return []aiv1.PilotProposal{{Actions: actions, State: aiv1.PilotState(st, tasks, actions)}}
	}
	rest, own, _ := manage(spec, st, coord, tasks, proposal(actions...), &opened)
	if len(own) != 0 || len(rest) != 1 || len(rest[0].Actions) != len(actions) {
		t.Fatalf("queue tools, spec edits, rules and free text wait for a yes: own %+v", own)
	}
	instruct := aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "rerun", Text: "and push to main", Owner: "alice"}
	_, own, _ = manage(spec, st, coord, tasks, proposal(instruct), &opened)
	if len(own) != 1 || own[0].Actions[0].Text != "" || own[0].Actions[0].Owner != "" {
		t.Fatalf("an instruct it runs on its own is a fixed kind and a note: %+v", own)
	}
}

func TestPilotManagedQueueToolStillNeedsAYes(t *testing.T) {
	e := newManagedEnv(t)
	before, runs := e.queueEffect(), e.toolEffect()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "skip", IssueIID: 2}, aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	p := e.promptNote("Q1")
	if after := e.queueEffect(); after != before || e.toolEffect().runs != runs.runs+1 ||
		!strings.Contains(p.Body, "**skip**") || strings.Contains(p.Body, "**retry**") || !strings.Contains(p.Body, "@alice") {
		t.Fatalf("the retry runs, the skip waits for a yes and pings: %+v → %+v\n%s", before, after, p.Body)
	}
	e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "A")
	e.reconcile()
	e.reconcile()
	if after := e.queueEffect(); after.two != aiv1.AutopilotItemSkipped {
		t.Fatalf("the owner's yes skips #2: %+v", after)
	}
}

func TestPilotManagedRunsNothingMadeOnAStateThatIsGone(t *testing.T) {
	e := newManagedEnv(t)
	before := e.toolEffect()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
	e.reconcile()
	if after := e.toolEffect(); after != before {
		t.Fatalf("a proposal made on an old head does not run on its own: %+v → %+v", before, after)
	}
	// Nor is it put to the owners: their answer could not run it either.
	if got := promptStates(e.task(specTaskName)); got != "" || len(e.pilot().Status.Pilot.Proposed) != 0 {
		t.Fatalf("no prompt: %q", got)
	}
}

func TestPilotStoppedBeforeItsOwnActionsRanRefundsThem(t *testing.T) {
	e := newManagedEnv(t)
	before := e.toolEffect()
	// Taken over in a poll whose run did not get through, then stopped.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{
			Accepted:         []aiv1.PilotProposal{{Summary: "retry #1", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Trigger: 1}},
			Interventions:    map[string]aiv1.PilotIntervention{"1": {Head: "sha1", Count: 1}},
			AutonomyOverride: &aiv1.PilotAutonomyOverride{Autonomy: aiv1.PilotPropose, By: "alice"},
		}
	})
	e.reconcile()
	ps := e.pilot().Status.Pilot
	if after := e.toolEffect(); after != before || ps.Interventions["1"].Count != 0 ||
		!strings.Contains(e.lastReply(), "ran none of its own actions") || !strings.Contains(e.lastReply(), "not run: the pilot proposes now") {
		t.Fatalf("a stop before the run refuses and refunds it: %+v → %+v, %+v, %q", before, after, ps.Interventions, e.lastReply())
	}
}

func TestPilotStatusConflictLeavesTheObjectAsItWas(t *testing.T) {
	e := newManagedEnv(t)
	coord := e.pilot()
	stale := coord.DeepCopy()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.Summary = "moved on" })
	err := e.r.patchProposals(e.ctx, stale, func(ps *aiv1.PilotStatus) {
		ps.Interventions = map[string]aiv1.PilotIntervention{"1": {Count: 2}}
	})
	if err == nil || stale.Status.Pilot != nil && stale.Status.Pilot.Interventions != nil {
		t.Fatalf("a conflict fails and keeps nothing of the change: %v, %+v", err, stale.Status.Pilot)
	}
}

func TestPilotManagedSaysWhenItHasNothingToRun(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	seen := len(botComments(e, tracking))
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "merge", IssueIID: 1})
	e.reconcile()
	notes := botNotesSince(e, tracking, seen)
	if len(notes) != 1 || !strings.Contains(notes[0], "proposed nothing the operator runs") || !strings.HasSuffix(notes[0], "cc @alice") {
		t.Fatalf("its assessment pinged nobody, so this does: %q", notes)
	}
}

func TestPilotStoppedAfterAQuietAssessmentPingsWithItsProposal(t *testing.T) {
	e := newManagedEnv(t)
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot stop")
	before := e.toolEffect()
	e.reconcile()
	if after := e.toolEffect(); after != before || !strings.Contains(e.promptNote("Q1").Body, "cc @alice") {
		t.Fatalf("stopped, the proposal of a quiet assessment runs nothing and pings: %+v → %+v", before, after)
	}
}

func TestPilotProposeRunsNothingWithoutAYes(t *testing.T) {
	e := newProposalEnv(t)
	before := e.toolEffect()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	if after := e.toolEffect(); after != before || strings.Contains(e.promptNote("Q1").Body, "@alice") {
		t.Fatalf("propose runs nothing and the proposal pings nobody: %+v → %+v", before, after)
	}
}

func TestPilotStopAndGo(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	before := e.toolEffect()
	// Not from someone who is no owner.
	e.gl.AddNote(tracking, mallory, "/pilot stop")
	e.reconcile()
	if ps := e.pilot().Status.Pilot; ps != nil && ps.AutonomyOverride != nil || !strings.Contains(e.lastReply(), "Only the owners") {
		t.Fatalf("a non-owner cannot stop the pilot: %+v, %q", ps, e.lastReply())
	}
	e.gl.AddNote(1, alice, "/pilot stop")
	e.reconcile()
	o := e.pilot().Status.Pilot.AutonomyOverride
	if o == nil || o.Autonomy != aiv1.PilotPropose || o.By != "alice" || !strings.Contains(lastNote(e, 1), "Stopped by `@alice`") {
		t.Fatalf("an owner's stop is kept with who and when: %+v, %q", o, lastNote(e, 1))
	}
	// It holds over the project's value and a restart of the operator.
	e.r = &AgentProjectReconciler{Client: e.c, Scheme: e.r.Scheme, NewGitLab: e.r.NewGitLab}
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if after := e.toolEffect(); after != before || !strings.Contains(e.promptNote("Q1").Body, "**retry** #1") {
		t.Fatalf("stopped, it proposes: %+v → %+v", before, after)
	}
	e.gl.AddNote(tracking, alice, "/pilot go")
	e.reconcile()
	if !strings.Contains(e.lastReply(), "Managed again") {
		t.Fatalf("go: %q", e.lastReply())
	}
	e.proposeFor(1, "event/b", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 {
		t.Fatalf("after go it manages again: %+v → %+v", before, after)
	}
}

func TestPilotGoCannotRaiseAProposeProject(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	before := e.toolEffect()
	e.gl.AddNote(tracking, alice, "/pilot go")
	e.reconcile()
	if !strings.Contains(e.lastReply(), "still proposes") {
		t.Fatalf("go says it cannot raise it: %q", e.lastReply())
	}
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if after := e.toolEffect(); after != before {
		t.Fatalf("go cannot raise a propose project: %+v → %+v", before, after)
	}
}

func TestPilotAutonomyIsNotSetByTheMilestone(t *testing.T) {
	e := newProposalEnv(t)
	before := e.toolEffect()
	// Lines in the description, rule-like or not, and a "/pilot go" in it.
	e.gl.Milestones[0].Description += "\n/agent rule R1: pilot.autonomy managed\npilot.autonomy: managed\n/pilot go"
	e.reconcile()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	if after := e.toolEffect(); after != before || e.pilot().Status.Pilot.AutonomyOverride != nil {
		t.Fatalf("the milestone cannot change autonomy: %+v → %+v", before, after)
	}
	// Nor can the pilot: there is no tool for it.
	e.proposeFor(1, "event/b", aiv1.PilotAction{Tool: "setAutonomy", Kind: "managed"})
	e.reconcile()
	if e.pilot().Status.Pilot.AutonomyOverride != nil {
		t.Fatal("the pilot cannot change autonomy")
	}
}

func TestPilotRunsKeepTheCIRoundsAHumansDoNot(t *testing.T) {
	e := newManagedEnv(t)
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "ci-fix"})
	e.reconcile()
	if src := e.implTaskOf(1).Spec; src.Source.PilotRun != src.RunRequest {
		t.Fatalf("the pilot's run is marked: %d vs request %d", src.Source.PilotRun, src.RunRequest)
	}
	e.gl.AddNote(1, alice, "/agent fix the lint job")
	e.reconcile()
	if src := e.implTaskOf(1).Spec; src.Source.PilotRun == src.RunRequest {
		t.Fatalf("a human's run is not the pilot's: %d vs request %d", src.Source.PilotRun, src.RunRequest)
	}
	// An owner's own words via the pilot are the owner's run.
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot tell #1: use the v2 API")
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.reconcile()
	if src := e.implTaskOf(1).Spec; src.Source.PilotRun == src.RunRequest || src.Source.PilotInstruction.Text == "" {
		t.Fatalf("an owner's words are not the pilot's run: %d vs request %d", src.Source.PilotRun, src.RunRequest)
	}
}

// Bloop #331: rust:lint stayed red after the fix rounds, #1 went to
// needs-input; the pilot assessed it (env: no Rust toolchain) and, on an
// autopilot milestone, instructs install-toolchains without a yes.
func TestPilotManagesBloop331(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.SetLabels(1, "agent::needs-input")
	e.reconcile()
	e.reconcile()
	keys, _ := e.pilotEventKeys()
	if len(keys) == 0 || !strings.HasPrefix(keys[0], "stuck/1/sha1") {
		t.Fatalf("needs-input wakes the pilot: %v", keys)
	}
	seen := len(botComments(e, tracking))
	before := e.toolEffect()
	// The runner's assessment records its proposal.
	e.proposeFor(1, "event/"+keys[0]+"@1", aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "install-toolchains",
		Note: "`cargo fmt --check` failed; the run reports say no Rust toolchain"})
	e.assessed()
	e.reconcile()
	after := e.toolEffect()
	if after.runs != before.runs+1 || after.instruction.Kind != "install-toolchains" || after.instruction.Text != "" ||
		!strings.Contains(after.instruction.Note, "no Rust toolchain") {
		t.Fatalf("install-toolchains runs without a yes: %+v → %+v", before, after)
	}
	notes := botNotesSince(e, tracking, seen)
	if len(notes) != 1 || !strings.Contains(notes[0], "The pilot acted on its own") || !strings.Contains(notes[0], "`install-toolchains`") ||
		!strings.Contains(notes[0], "Interventions left for #1: 1 of 2.") || strings.Contains(notes[0], "@alice") {
		t.Fatalf("one audit comment, no ping: %q", notes)
	}
	if c := lastNote(e, 1); !strings.Contains(c, "Instruction from the pilot (on its own") {
		t.Fatalf("#1 shows the instruction: %q", c)
	}
}

// A managing pilot rebases the active MR without a yes, counted against
// the issue's head and audited; past pilot.maxInterventionsPerIssue it
// proposes.
func TestPilotManagedRebasesTheActiveMR(t *testing.T) {
	e := newManagedEnv(t)
	e.pauseBaseFailing()
	e.gl.MRs[1].RebaseInProgress = true // the head stays while GitLab rebases
	tracking := e.autopilot().TrackingIssueIID
	seen := len(botComments(e, tracking))
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "rebase", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	notes := strings.Join(botNotesSince(e, tracking, seen), "\n")
	if !slices.Equal(e.gl.Rebased, []int64{1}) || e.autopilot().Phase != aiv1.AutopilotRunning || e.autopilot().ResumedBy != aiv1.ResolvedByPilot {
		t.Fatalf("the pilot rebases !1 on its own and the pause ends: rebased %v, %+v\n%s", e.gl.Rebased, e.autopilot(), notes)
	}
	for _, want := range []string{"The pilot acted on its own", "**rebase** #1", "Interventions left for #1: 1 of 2.",
		"▶️ Autopilot continues: the pilot asked to rebase !1 onto `main` (ended by the pilot)."} {
		if !strings.Contains(notes, want) {
			t.Errorf("the tracking issue misses %q:\n%s", want, notes)
		}
	}
	if got := promptStates(e.task(specTaskName)); got != "" {
		t.Fatalf("what the pilot runs on its own is no prompt: %s", got)
	}
	// Two more on the same head: the second counts (and is refused while
	// the rebase runs), the third is proposed.
	for _, src := range []string{"event/b", "event/c"} {
		e.proposeFor(1, src, aiv1.PilotAction{Tool: "rebase", IssueIID: 1})
		e.reconcile()
		e.reconcile()
	}
	if ps := e.pilot().Status.Pilot; ps.Interventions["1"].Count != 2 || !slices.Equal(e.gl.Rebased, []int64{1}) {
		t.Fatalf("two interventions on #1's head: %+v, rebased %v", ps.Interventions, e.gl.Rebased)
	}
	if c := trackingComments(e); !strings.Contains(c, "a rebase of !1 is in progress") {
		t.Fatalf("the second rebase is answered:\n%s", c)
	}
	if p := e.promptNote("Q1"); !strings.Contains(p.Body, "**rebase** #1") || !strings.Contains(p.Body, "@alice") {
		t.Fatalf("past the limit it proposes and pings the owners:\n%s", p.Body)
	}
}
