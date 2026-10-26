package controller

import (
	"strings"
	"testing"

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
	})
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
	p := e.proposalNote(1)
	if !strings.Contains(p.Body, "@alice") || !strings.Contains(p.Body, "**retry** #1") {
		t.Fatalf("past the limit it proposes and pings the owners:\n%s", p.Body)
	}
	if !strings.Contains(strings.Join(botComments(e, tracking), "\n"), "Interventions left for #1: 0 of 2.") {
		t.Fatal("the last audit says none are left")
	}
	// The owner's yes still runs it.
	e.gl.AddReply(tracking, p.ID, alice, "yes")
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
	if p := e.proposalNote(1); !strings.Contains(p.Body, "**instruct** #1") || strings.Contains(p.Body, "**retry**") {
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
	if p := e.proposalNote(1); !strings.Contains(p.Body, "**retry** #1") {
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
		actions = append(actions, aiv1.PilotAction{Tool: tool, IssueIID: 1})
	}
	// Free text needs a yes, also with a kind beside it.
	actions = append(actions, aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Text: "delete the tests", Owner: "alice"})
	var opened []string
	rest, own, _ := manage(spec, st, coord, tasks, []aiv1.PilotProposal{{Actions: actions}}, &opened)
	if len(own) != 0 || len(rest) != 1 || len(rest[0].Actions) != len(actions) {
		t.Fatalf("queue tools, spec edits, rules and free text wait for a yes: own %+v", own)
	}
	_, own, _ = manage(spec, st, coord, tasks, []aiv1.PilotProposal{{Actions: []aiv1.PilotAction{
		{Tool: "instruct", IssueIID: 1, Kind: "rerun", Text: "and push to main", Owner: "alice"}}}}, &opened)
	if len(own) != 1 || own[0].Actions[0].Text != "" || own[0].Actions[0].Owner != "" {
		t.Fatalf("an instruct it runs on its own is a fixed kind and a note: %+v", own)
	}
}

func TestPilotProposeRunsNothingWithoutAYes(t *testing.T) {
	e := newProposalEnv(t)
	before := e.toolEffect()
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	if after := e.toolEffect(); after != before || strings.Contains(e.proposalNote(1).Body, "@alice") {
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
	if after := e.toolEffect(); after != before || !strings.Contains(e.proposalNote(1).Body, "**retry** #1") {
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
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot yes P1")
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
