package runner

import (
	"context"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Owner messages (spec.source.ownerMessages) reach the implementer in three
// ways, each recorded in status.run.deliveredMessages before it counts:
//   - into a running claude implementer call (stream), by watchOwnerMessages;
//   - into a running call that cannot take them (cursor, or claude without a
//     replay): it is stopped and invokeOn resumes it with them (resume);
//   - into the prompt of the next implementer call (promptOwnerMessages), as
//     resume when the message came during this run, else as nextRun.
//
// The task is read fresh each time and a recorded ID is never delivered
// again, so a pod restart after a delivery does not repeat it. Mid-run
// deliveries (stream and resume) are capped per run (midRunLeft).

// messagesPoll is how often a call's watcher looks at the task when the
// watch is quiet or fails. A variable so tests can shorten it.
var messagesPoll = 2 * time.Second

// midRunLeft is how many more owner messages the run may take mid-run.
func (rl *runLog) midRunLeft() int {
	if rl == nil {
		return 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return max(aiv1.MaxMidRunDeliveries-rl.midRun, 0)
}

func (rl *runLog) noteMidRun(n int) {
	if rl != nil {
		rl.mu.Lock()
		rl.midRun += n
		rl.mu.Unlock()
	}
}

// promptOwnerMessages records the owner messages pending when an implementer
// call starts as delivered to it and returns them as a prompt section ("" when
// none, or when the record failed: they wait for a later call). Messages from
// before the run started are nextRun; later ones are resume, within the cap.
func (r *Runner) promptOwnerMessages(ctx context.Context, inv Invocation) string {
	if !implementerKind(inv.Kind) || r.run == nil {
		return ""
	}
	task, err := r.taskNow(ctx)
	if err != nil {
		r.log().Info("could not read the owner messages", "error", err.Error())
		return ""
	}
	left := r.run.midRunLeft()
	// metav1.Time keeps whole seconds: one from the run's first second
	// counts as mid-run.
	started := r.run.start.Truncate(time.Second)
	var take []aiv1.OwnerMessage
	how := map[string]aiv1.DeliveryHow{}
	for _, m := range task.PendingOwnerMessages() {
		if m.At.Time.Before(started) {
			how[m.ID] = aiv1.DeliveryNextRun
		} else if left > 0 {
			left--
			how[m.ID] = aiv1.DeliveryResume
		} else {
			continue // waits for the next run
		}
		take = append(take, m)
	}
	if len(take) == 0 {
		return ""
	}
	if err := r.recordDeliveries(ctx, take, func(m aiv1.OwnerMessage) aiv1.DeliveryHow { return how[m.ID] }); err != nil {
		r.log().Info("could not record the owner messages, they wait for a later call", "error", err.Error())
		return ""
	}
	for _, h := range how {
		if h == aiv1.DeliveryResume {
			r.run.noteMidRun(1)
		}
	}
	r.log().Info("owner messages go into the implementer's prompt", "messages", len(take), "kind", inv.Kind)
	return ownerMessagesPrompt(take)
}

// withOwnerMessages puts section into a call's prompts the way
// pilotInstruction goes in: before the resumed prompt, after the full one.
func withOwnerMessages(section string, inv Invocation, fullPrompt string) (Invocation, string) {
	if section == "" {
		return inv, fullPrompt
	}
	if inv.Prompt != "" {
		inv.Prompt = section + "\n\n" + inv.Prompt
	}
	if fullPrompt != "" {
		fullPrompt += "\n\n" + section
	}
	return inv, fullPrompt
}

// watchOwnerMessages delivers owner messages into the implementer call inv
// while it runs: a stream-input call takes them in (Agent.Message); any other
// call, or one that did not take them, is stopped (Agent.Stop) so invokeOn
// resumes it with them. The returned func stops the watcher, waits for it
// and returns the messages the call was stopped for (nil: no stop); call it
// when the call returns.
func (r *Runner) watchOwnerMessages(ctx context.Context, inv Invocation) func() []aiv1.OwnerMessage {
	if !implementerKind(inv.Kind) || r.run == nil {
		return func() []aiv1.OwnerMessage { return nil }
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var stoppedFor []aiv1.OwnerMessage
	wake := r.watchObject(ctx, &aiv1.AgentTaskList{}, r.TaskName)
	go func() {
		defer close(done)
		tried := map[string]bool{}
		for {
			if stoppedFor = r.deliverOwnerMessages(ctx, inv, tried); stoppedFor != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-time.After(messagesPoll):
			}
		}
	}()
	return func() []aiv1.OwnerMessage {
		cancel()
		<-done
		return stoppedFor
	}
}

// deliverOwnerMessages takes the pending owner messages, within the cap, to
// the running call inv. A stream-input call gets them as one user message,
// recorded as stream once the call replayed it; messages it did not take
// (tried) are not written again in this call. Messages a call could not take
// stop it (stopFor), unless its result came first: then they stay pending.
func (r *Runner) deliverOwnerMessages(ctx context.Context, inv Invocation, tried map[string]bool) []aiv1.OwnerMessage {
	task, err := r.taskNow(ctx)
	if err != nil {
		return nil
	}
	var batch []aiv1.OwnerMessage
	ids := make([]string, 0)
	for _, m := range task.PendingOwnerMessages() {
		if !tried[m.ID] && len(batch) < r.run.midRunLeft() {
			batch = append(batch, m)
			ids = append(ids, m.ID)
		}
	}
	if len(batch) == 0 {
		return nil
	}
	if !inv.StreamInput {
		return r.stopFor(ctx, batch)
	}
	res, err := r.Agent.Message(ctx, MessageRequest{ID: strings.Join(ids, ","), Text: ownerMessagesPrompt(batch)})
	if err != nil && strings.Contains(err.Error(), errNoStreamCall.Error()) {
		return nil // nothing was written: the call is not (or no longer) live
	}
	for _, id := range ids {
		tried[id] = true
	}
	if err == nil && res.Delivered {
		r.run.noteMidRun(len(batch))
		// Recorded even when the call ends meanwhile: the call took them.
		if err := r.recordDeliveries(context.WithoutCancel(ctx), batch, func(aiv1.OwnerMessage) aiv1.DeliveryHow { return aiv1.DeliveryStream }); err != nil {
			r.log().Error(err, "recording owner messages delivered into the running call", "messages", ids)
			return nil
		}
		r.log().Info("owner messages delivered into the running call", "messages", ids)
		return nil
	}
	reason := res.Reason
	if err != nil {
		reason = err.Error()
	}
	if ctx.Err() != nil || res.Answered {
		// The call ended first: nothing to stop.
		r.log().Info("the call answered before it took the owner messages in; they wait for the next implementer call",
			"messages", len(batch), "reason", reason)
		return nil
	}
	r.log().Info("the running call did not take the owner messages in; stopping it to resume with them",
		"messages", len(batch), "reason", reason)
	return r.stopFor(ctx, batch)
}

// stopFor stops the running call gracefully for msgs and returns them; nil
// when the call cannot be stopped (not running yet, or it answered): they
// stay pending.
func (r *Runner) stopFor(ctx context.Context, msgs []aiv1.OwnerMessage) []aiv1.OwnerMessage {
	if err := r.Agent.Stop(ctx); err != nil {
		r.log().V(1).Info("could not stop the running call for owner messages", "messages", len(msgs), "error", err.Error())
		return nil
	}
	r.log().Info("stopped the running call for owner messages", "messages", len(msgs))
	return msgs
}

// resumeAfterStop is the call that goes on after inv was stopped for msgs
// (with the session ID seen, maybe none). msgs are recorded as resume before
// it starts. It resumes the session with them; without a session ID it is
// the step's call again, fullPrompt with them after it, on the edits the
// stopped call left. It returns the call and the messages it took: a failed
// record leaves msgs pending, and the call goes on without them.
func (r *Runner) resumeAfterStop(ctx context.Context, inv Invocation, session, fullPrompt string, msgs []aiv1.OwnerMessage) (Invocation, []aiv1.OwnerMessage) {
	if err := r.recordDeliveries(ctx, msgs, func(aiv1.OwnerMessage) aiv1.DeliveryHow { return aiv1.DeliveryResume }); err != nil {
		r.log().Error(err, "recording owner messages for the resumed call; they wait for a later call")
		msgs = nil
	} else {
		r.run.noteMidRun(len(msgs))
	}
	inv.Resume = session
	if session != "" {
		inv.Prompt = resumeMessagesPrompt(msgs)
	} else if inv.Prompt = fullPrompt; len(msgs) > 0 {
		inv.Prompt += "\n\n" + ownerMessagesPrompt(msgs)
	}
	r.log().Info("resuming the stopped call with the owner messages", "messages", len(msgs), "session", session)
	return inv, msgs
}

// recordDeliveries adds msgs to status.run.deliveredMessages as delivered to
// the call recorded next (the running one, or the one about to start), how
// says the way. IDs recorded already are left as they are; deliveries of
// messages no longer in spec.source.ownerMessages are dropped.
func (r *Runner) recordDeliveries(ctx context.Context, msgs []aiv1.OwnerMessage, how func(aiv1.OwnerMessage) aiv1.DeliveryHow) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t, err := r.taskNow(ctx)
		if err != nil {
			return err
		}
		orig := t.DeepCopy()
		r.callsMu.Lock()
		seq := t.Status.Run.LastCallSeq + int64(len(r.pendingCalls)) + 1
		r.callsMu.Unlock()
		at := metav1.Time{Time: r.now()}
		for _, m := range msgs {
			if t.Delivery(m.ID) == nil {
				t.Status.Run.DeliveredMessages = append(t.Status.Run.DeliveredMessages, aiv1.DeliveredMessage{ID: m.ID, Call: seq, How: how(m), At: at})
			}
		}
		keep := t.Status.Run.DeliveredMessages[:0:0]
		for _, d := range t.Status.Run.DeliveredMessages {
			for _, m := range t.Spec.Source.OwnerMessages {
				if m.ID == d.ID {
					keep = append(keep, d)
					break
				}
			}
		}
		t.Status.Run.DeliveredMessages = keep
		return r.K8s.Status().Patch(ctx, t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	})
}

// taskNow reads the runner's task fresh.
func (r *Runner) taskNow(ctx context.Context) (*aiv1.AgentTask, error) {
	var t aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
