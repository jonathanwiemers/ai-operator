package runner

import (
	"context"
	"errors"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// tickAnswered runs one tick and returns the plan's answeredFor.
func (e *planEnv) tickAnswered(t *testing.T) string {
	t.Helper()
	if did, err := e.run.tick(context.Background()); !did || err != nil {
		t.Fatalf("tick: did %v, err %v", did, err)
	}
	return e.plan(t).Status.AnsweredFor
}

func TestPlanAnsweredForEveryTurn(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "Which page?", ""),
		func(Invocation) (*Result, error) { return nil, errors.New("claude exited with status 1") }, nil)

	e.post(t, chatMsg(10, "message", "draft it", "bob"))
	if got := e.tickAnswered(t); got != "bob" {
		t.Fatalf("after a normal turn: %q", got)
	}
	e.post(t, chatMsg(11, "message", "again", "carol"))
	if got := e.tickAnswered(t); got != "carol" {
		t.Fatalf("after a failed turn: %q", got)
	}

	// An interrupted turn answers its own author, not the interrupter.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.run.wake = e.run.watchObject(ctx, &aiv1.AgentPlanList{}, "shop-plan-1")
	started := e.blockTurn(2)
	e.post(t, chatMsg(12, "message", "once more", "alice"))
	done := e.tickAsync(context.Background())
	<-started
	e.post(t, chatMsg(13, "interrupt", "stop", "dave"))
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if got := e.plan(t).Status.AnsweredFor; got != "alice" {
		t.Fatalf("after an interrupted turn: %q", got)
	}

	// An interrupt with no turn to stop is not a turn.
	e.post(t, chatMsg(14, "interrupt", "stop", "erin"))
	if got := e.tickAnswered(t); got != "alice" {
		t.Fatalf("after a no-turn interrupt: %q", got)
	}

	// A message in flight during two restarts gets the restart reply.
	e.post(t, chatMsg(15, "message", "and now?", "frank"))
	e.stuckWorking(t)
	if err := e.run.saveState(state{ChatInFlight: &chatInFlight{MessageID: 15, Resent: true}}); err != nil {
		t.Fatal(err)
	}
	if got := e.tickAnswered(t); got != "frank" {
		t.Fatalf("after the restart reply: %q", got)
	}
	e.finished(t, "the restart reply")

	// The budget-used reply counts as a failed turn.
	e.stuckWorking(t)
	p := e.plan(t)
	p.Spec.MaxBudgetUSD, p.Spec.ChatMessages = "0.01", append(p.Spec.ChatMessages, chatMsg(16, "message", "more", "gina"))
	if err := e.k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if got := e.tickAnswered(t); got != "gina" || len(e.engine.calls) != 3 {
		t.Fatalf("after the budget reply: %q after %d engine calls", got, len(e.engine.calls))
	}
	e.finished(t, "the budget reply")
}

// stuckWorking leaves the plan Working without a turn time, as a pod cut in
// a turn before any turn finished.
func (e *planEnv) stuckWorking(t *testing.T) {
	t.Helper()
	p := e.plan(t)
	p.Status.Phase, p.Status.Chat.LastTurnTime = aiv1.AgentPlanWorking, nil
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

// finished fails unless the last turn left the plan Ready with a turn time.
func (e *planEnv) finished(t *testing.T, what string) {
	t.Helper()
	if s := e.plan(t).Status; s.Phase != aiv1.AgentPlanReady || s.Chat.LastTurnTime == nil {
		t.Fatalf("after %s: phase %s, lastTurnTime %v", what, s.Phase, s.Chat.LastTurnTime)
	}
}
