package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
)

func TestLoosePauseAndResume(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	n := e.gl.AddNote(tracking, bob, "/agent pause")
	e.reconcile()
	ap := e.queue().Status.Autopilot
	if ap.Phase != aiv1.AutopilotPaused || ap.PausedReason != "paused by @bob" || ap.PauseKind != aiv1.PauseOwner {
		t.Fatalf("the owner pauses the queue: %+v", ap)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "⏸️ paused, paused by @\u200bbob.") {
		t.Fatalf("the tracking issue shows the pause:\n%s", d)
	}
	for _, b := range botComments(e, tracking) {
		if strings.Contains(b, replyToMarker(n.ID)) {
			t.Fatalf("an applied pause is not answered: %q", b)
		}
	}

	// Paused: nothing merges, and nothing starts in the freed slot.
	e.finishRun(350, "sha-350", nil)
	e.say(tracking, bob, "/agent drop #421")
	e.reconcile()
	e.reconcile()
	if len(e.gl.Merged) != 0 {
		t.Fatalf("a paused queue merges nothing: %v", e.gl.Merged)
	}
	e.wantItems("a paused queue starts nothing", "350:Active", "421:Left", "46:Pending")
	if task := e.implTaskOf(350); task.Spec.Source.AutopilotPause != "paused by @bob" {
		t.Fatalf("the implement task gets the pause: %q", task.Spec.Source.AutopilotPause)
	}

	e.say(tracking, bob, "/agent resume")
	e.pollUntil(4, "#350 merges after the resume", func() bool { return e.looseItem(350).State == merged })
	if ap := e.queue().Status.Autopilot; ap.Phase != aiv1.AutopilotRunning || ap.PausedReason != "" {
		t.Fatalf("resumed: %+v", ap)
	}
	e.pollUntil(2, "#46 starts", func() bool { return e.looseItem(46).State == active })
}

func TestLooseResumeLiftsHolds(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 2)
	e.reconcile()
	hold := func(iids ...int) {
		e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
			for _, i := range iids {
				s.Autopilot.Items[i].Hold = &aiv1.ItemHold{Kind: aiv1.PauseConfig, Reason: "the setup is broken"}
			}
		})
	}
	hold(0)
	if reply := e.sayLoose(tracking, bob, "/agent resume #46"); !strings.Contains(reply, "`resume #N` needs a held issue") {
		t.Fatalf("a pending issue has no hold: %q", reply)
	}
	e.say(tracking, bob, "/agent resume #350")
	e.reconcile()
	if it := e.looseItem(350); it.Hold != nil {
		t.Fatalf("resume #N lifts the hold: %+v", it)
	}
	e.wantNotes(tracking, "▶️ #350 goes on", 1)

	// A plain resume lifts every hold (D850-P1).
	hold(0, 1)
	e.say(tracking, bob, "/agent resume")
	e.reconcile()
	if a, b := e.looseItem(350), e.looseItem(421); a.Hold != nil || b.Hold != nil {
		t.Fatalf("resume lifts every hold: %+v %+v", a, b)
	}
	e.wantNotes(tracking, "▶️ #350, #421 go on: resumed", 1)
}

func TestLooseMergeOverride(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.finishRun(350, "sha-350", func(r *aiv1.RunStatus) { r.BlockingFindings = 2 })
	e.reconcile()
	if it := e.looseItem(350); it.Hold == nil || it.Hold.Kind != aiv1.PauseFindings {
		t.Fatalf("findings hold #350: %+v", it)
	}
	if !strings.Contains(e.gl.Issues[tracking].Description, "- #350: 2 blocking review findings") {
		t.Fatalf("the hold is shown:\n%s", e.gl.Issues[tracking].Description)
	}
	if c := strings.Join(botComments(e, tracking), "\n"); strings.Contains(c, "`/agent skip") {
		t.Fatalf("the ways out name no skip, which the queue refuses:\n%s", c)
	}
	e.say(tracking, bob, "/agent merge #350 sha-350")
	e.pollUntil(3, "the override merges #350", func() bool { return e.looseItem(350).State == merged })
	if ov := e.queue().Status.Autopilot.MergeOverride; ov == nil || ov.IssueIID != 350 || ov.By != "@bob" {
		t.Fatalf("the override is recorded: %+v", ov)
	}
}

func TestLooseRebaseAndMergeBase(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.taskOf(350)
	e.redRun(350, "sha-350")
	e.reconcile()
	mr := e.looseItem(350).MergeRequestIID
	if reply := e.sayLoose(tracking, bob, "/agent rebase #421"); !strings.Contains(reply, "`rebase` needs the active issue with a merge request") {
		t.Fatalf("a pending issue is refused: %q", reply)
	}
	e.say(tracking, bob, "/agent rebase #350")
	e.reconcile()
	if !slices.Equal(e.gl.Rebased, []int64{mr}) || e.looseItem(350).RebasedFrom != "sha-350" {
		t.Fatalf("the owner's rebase: rebased %v, %+v", e.gl.Rebased, e.looseItem(350))
	}
	if strings.Contains(strings.Join(botComments(e, tracking), "\n"), "is not on the loose issues queue") {
		t.Fatal("rebase is a queue command")
	}

	e, tracking = newLooseRunEnv(t, 0)
	e.reconcile()
	name := e.taskOf(350)
	e.redRun(350, "sha-350")
	e.reconcile()
	e.say(tracking, bob, "/agent mergeBase #350")
	e.reconcile()
	if task := e.tasks()[name]; task.Spec.Source.MergeBaseFor != "sha-350" || !task.RunPending() || !e.gl.Issues[350].HasLabel("agent::working") {
		t.Fatalf("the agent merges the default branch: %+v", e.tasks()[name].Spec.Source)
	}
}

func TestLooseDeskSteering(t *testing.T) {
	e, _ := newLooseRunEnv(t, 0)
	e.reconcile()
	e.sendCommand(queueTaskName, 1, "alice@example.com", `{"cmd":"pause"}`)
	if got := chatAnswer(*e.queue(), 1); got != "The loose issues queue is paused." || e.queue().Status.Autopilot.PausedReason != "paused by alice via the Desk" {
		t.Fatalf("Desk pause: %q %+v", got, e.queue().Status.Autopilot)
	}
	e.sendCommand(queueTaskName, 2, "alice@example.com", `{"cmd":"resume"}`)
	if got := chatAnswer(*e.queue(), 2); got != "The loose issues queue goes on." || e.queue().Status.Autopilot.Phase != aiv1.AutopilotRunning {
		t.Fatalf("Desk resume: %q", got)
	}

	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items[0].Hold = &aiv1.ItemHold{Kind: aiv1.PauseConfig, Reason: "the setup is broken"}
	})
	e.sendCommand(queueTaskName, 3, "alice@example.com", `{"cmd":"resume","issue":350}`)
	if got := chatAnswer(*e.queue(), 3); got != "#350 goes on." || e.looseItem(350).Hold != nil {
		t.Fatalf("Desk resume #N: %q", got)
	}

	e.redRun(350, "sha-350")
	e.reconcile()
	e.sendCommand(queueTaskName, 4, "alice@example.com", `{"cmd":"rebase","issue":350}`)
	if got := chatAnswer(*e.queue(), 4); len(e.gl.Rebased) != 1 || strings.Contains(got, "not on the loose issues queue") {
		t.Fatalf("Desk rebase: %q, rebased %v", got, e.gl.Rebased)
	}
	e.sendCommand(queueTaskName, 5, "alice@example.com", `{"cmd":"mergeBase","issue":421}`)
	if got := chatAnswer(*e.queue(), 5); !strings.Contains(got, "`mergeBase` needs the active issue with a merge request") {
		t.Fatalf("Desk mergeBase of a pending issue: %q", got)
	}

	e, _ = newLooseRunEnv(t, 0)
	e.reconcile()
	e.finishRun(350, "abc1234", func(r *aiv1.RunStatus) { r.BlockingFindings = 1 })
	e.reconcile()
	e.sendCommand(queueTaskName, 1, "alice@example.com", `{"cmd":"merge","issue":350,"sha":"abc1234"}`)
	if ov := e.queue().Status.Autopilot.MergeOverride; ov == nil || ov.IssueIID != 350 || ov.SHA != "abc1234" {
		t.Fatalf("Desk merge: %q %+v", chatAnswer(*e.queue(), 1), ov)
	}

	for i, body := range []string{`{"cmd":"parallel","parallel":2}`, `{"cmd":"endmilestone"}`, `{"cmd":"skip","issue":350}`, `{"cmd":"move","to":"top"}`} {
		id := int64(i + 2)
		e.sendCommand(queueTaskName, id, "alice@example.com", body)
		if got := chatAnswer(*e.queue(), id); !strings.Contains(got, "is not on the loose issues queue") {
			t.Fatalf("%s: %q", body, got)
		}
	}
	e.sendCommand(queueTaskName, 6, "mallory@example.com", `{"cmd":"pause"}`)
	if got := chatAnswer(*e.queue(), 6); got != notOwnerQueue || e.queue().Status.Autopilot.Phase != aiv1.AutopilotRunning {
		t.Fatalf("a non-owner subject: %q", got)
	}
}

func TestLooseChatCommandsAreDelivered(t *testing.T) {
	queue := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue}}
	for _, cmd := range []string{"take", "drop", "pause", "resume", "merge", "rebase", "mergebase", "move", "parallel", "endmilestone", "skip"} {
		if !chatCommandAllowed(queue, chatcmd.Command{Cmd: cmd}) {
			t.Errorf("%s reaches the Queue task, which applies or refuses it", cmd)
		}
	}
	if chatCommandAllowed(queue, chatcmd.Command{Cmd: "retry"}) {
		t.Error("an implement command does not reach the Queue task")
	}
	spec := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Autopilot: true}}
	if chatCommandAllowed(spec, chatcmd.Command{Cmd: "rebase"}) || chatCommandAllowed(spec, chatcmd.Command{Cmd: "take"}) {
		t.Error("the milestone's Desk commands are unchanged")
	}
}

func TestLooseHoldExitsNameNoSkip(t *testing.T) {
	ms := &autopilot{spec: aiv1.AgentProjectSpec{Workflow: aiv1.WorkflowSpec{CommandPrefix: "/agent"}}, ap: &aiv1.AutopilotStatus{}}
	if !strings.Contains(ms.exitLines(aiv1.PauseMergeRequest, 5, true), "`/agent skip #5`") {
		t.Fatal("a milestone's hold names skip")
	}
	loose := *ms
	loose.loose = true
	if got := loose.exitLines(aiv1.PauseMergeRequest, 5, true); strings.Contains(got, "`/agent skip") || !strings.Contains(got, "`/agent rebase #5`") {
		t.Fatalf("the queue's hold names no skip, which it refuses:\n%s", got)
	}
}
