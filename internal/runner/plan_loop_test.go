package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// blockTurn makes the engine's next call block until its context ends; it
// closes started once the call runs.
func (e *planEnv) blockTurn(i int) <-chan struct{} {
	started := make(chan struct{})
	e.engine.answers[i] = func(Invocation) (*Result, error) {
		e.engine.mu.Lock()
		ctx := e.engine.ctxs[len(e.engine.ctxs)-1]
		e.engine.mu.Unlock()
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, nil
		}
	}
	return started
}

// post hands messages to the plan as the operator's dispatch does.
func (e *planEnv) post(t *testing.T, msgs ...aiv1.ChatMessage) {
	t.Helper()
	p := e.plan(t)
	p.Spec.ChatMessages = append(p.Spec.ChatMessages, msgs...)
	if err := e.k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

// tickAsync runs one tick in the background.
func (e *planEnv) tickAsync(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := e.run.tick(ctx)
		done <- err
	}()
	return done
}

func waitErr(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("the turn did not end")
		return nil
	}
}

func replyFor(p *aiv1.AgentPlan, id int64) (aiv1.ChatReply, bool) {
	if p.Status.Chat != nil {
		for _, rep := range p.Status.Chat.Replies {
			if rep.MessageID == id {
				return rep, true
			}
		}
	}
	return aiv1.ChatReply{}, false
}

func TestPlanInterruptCancelsTurn(t *testing.T) {
	e := newPlanEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.run.wake = e.run.watchObject(ctx, &aiv1.AgentPlanList{}, "shop-plan-1")
	started := e.blockTurn(0)
	e.post(t, chatMsg(10, "message", "draft it", "alice"))
	done := e.tickAsync(context.Background())
	<-started
	e.post(t, chatMsg(11, "interrupt", "stop", "alice"))
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}

	p := e.plan(t)
	if rep, _ := replyFor(p, 10); rep.Error != "interrupted" {
		t.Fatalf("interrupted message: %+v", rep)
	}
	if rep, ok := replyFor(p, 11); !ok || rep.Error != "" || rep.Text != "" {
		t.Fatalf("interrupt message: %+v (answered %v)", rep, ok)
	}
	if p.Status.Phase != aiv1.AgentPlanReady || len(p.DraftList()) != 0 {
		t.Fatalf("status: %+v", p.Status)
	}
	st := savedState(t, e.run.Runner)
	if st.ChatInFlight != nil || !answeredChat(nil, st, 10) || !answeredChat(nil, st, 11) {
		t.Fatalf("state: in flight %+v, answered %v", st.ChatInFlight, st.ChatAnswered)
	}
	if did, err := e.run.tick(context.Background()); did || err != nil {
		t.Fatalf("a tick after the interrupt: did %v, err %v", did, err)
	}
}

func TestPlanLateInterruptChangesNothing(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "Which page?", "## Goal\n\nShow the version."))
	e.post(t, chatMsg(10, "message", "draft it", "alice"))
	if did, err := e.run.tick(context.Background()); !did || err != nil {
		t.Fatalf("turn: did %v, err %v", did, err)
	}
	e.post(t, chatMsg(11, "interrupt", "stop", "alice"))
	if did, err := e.run.tick(context.Background()); !did || err != nil {
		t.Fatalf("interrupt: did %v, err %v", did, err)
	}

	p := e.plan(t)
	if rep, _ := replyFor(p, 10); rep.Text != "Which page?" || rep.Error != "" {
		t.Fatalf("the answered turn changed: %+v", rep)
	}
	if rep, ok := replyFor(p, 11); !ok || rep.Error != "" || rep.Text != "" {
		t.Fatalf("interrupt message: %+v (answered %v)", rep, ok)
	}
	if firstPlanDraft(p).Revision != 1 || len(e.engine.calls) != 1 {
		t.Fatalf("draft revision %d, engine calls %d", firstPlanDraft(p).Revision, len(e.engine.calls))
	}
}

func TestPlanTurnCutByPodStopIsResentOnce(t *testing.T) {
	e := newPlanEnv(t, nil, planAnswer("s", "Again.", ""), nil)
	e.post(t, chatMsg(10, "message", "draft it", "alice"))

	// The pod stops during the turn: no reply, the turn stays in flight.
	started := e.blockTurn(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := e.tickAsync(ctx)
	<-started
	cancel()
	if err := waitErr(t, done); err == nil {
		t.Fatal("a cut turn reports no error")
	}
	if _, ok := replyFor(e.plan(t), 10); ok {
		t.Fatal("a cut turn has a reply")
	}
	if f := savedState(t, e.run.Runner).ChatInFlight; f == nil || f.MessageID != 10 || f.Resent {
		t.Fatalf("in flight: %+v", f)
	}

	// The next pod sends it again with the resend prefix.
	if did, err := e.run.tick(context.Background()); !did || err != nil {
		t.Fatalf("resend: did %v, err %v", did, err)
	}
	if !strings.HasPrefix(e.engine.calls[1].Prompt, chatResendPrefix) {
		t.Fatalf("resent prompt:\n%s", e.engine.calls[1].Prompt)
	}
	if rep, _ := replyFor(e.plan(t), 10); rep.Text != "Again." || rep.Error != "" {
		t.Fatalf("reply: %+v", rep)
	}
	if st := savedState(t, e.run.Runner); st.ChatInFlight != nil {
		t.Fatalf("still in flight: %+v", st.ChatInFlight)
	}

	// A resend cut again is not sent a third time.
	e.post(t, chatMsg(12, "message", "and now?", "alice"))
	if err := e.run.saveState(state{ChatInFlight: &chatInFlight{MessageID: 12, Resent: true}}); err != nil {
		t.Fatal(err)
	}
	if did, err := e.run.tick(context.Background()); !did || err != nil {
		t.Fatalf("second resend: did %v, err %v", did, err)
	}
	if rep, _ := replyFor(e.plan(t), 12); rep.Error != "interrupted by a restart" || len(e.engine.calls) != 2 {
		t.Fatalf("reply %+v after %d engine calls", rep, len(e.engine.calls))
	}
}

func TestPlanLoopAnswersOldestFirst(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "first answer", ""), planAnswer("s", "second answer", ""))
	e.run.PollInterval = 50 * time.Millisecond
	e.post(t, chatMsg(12, "message", "second question", "alice"), chatMsg(11, "command", "retry", "alice"),
		chatMsg(10, "message", "first question", "alice"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.run.Loop(ctx) }()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, ok := replyFor(e.plan(t), 12); ok {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("no answer to the second message: %+v", e.plan(t).Status.Chat)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}

	p := e.plan(t)
	if r10, _ := replyFor(p, 10); r10.Text != "first answer" {
		t.Fatalf("reply to 10: %+v", r10)
	}
	if _, ok := replyFor(p, 11); ok || len(e.engine.calls) != 2 {
		t.Fatalf("a command was answered or turns ran %d times", len(e.engine.calls))
	}
	if !strings.Contains(e.engine.calls[0].Prompt, "first question") || !strings.Contains(e.engine.calls[1].Prompt, "second question") {
		t.Fatalf("order: %q, %q", e.engine.calls[0].Prompt, e.engine.calls[1].Prompt)
	}
}
