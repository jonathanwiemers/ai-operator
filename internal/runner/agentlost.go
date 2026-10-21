package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// How long the runner waits for a restarted agent container, and for the
// operator to record why it stopped.
var (
	agentBackTimeout   = 3 * time.Minute
	terminationTimeout = 30 * time.Second
	agentLostPoll      = 2 * time.Second
)

// AgentLostError is an agent call that lost the agent container: it stopped
// and was restarted while the call ran, or before it.
type AgentLostError struct {
	// Term is how the container stopped, as the operator recorded it; nil
	// if it recorded nothing in time.
	Term *aiv1.ContainerTermination
	// After is how long the call (or the run) had been going.
	After time.Duration
	Err   error
	// back is set once the container answered again; notSent if the call
	// never reached it.
	back, notSent bool
}

func (e *AgentLostError) Error() string {
	after := e.After.Round(time.Second)
	switch t := e.Term; {
	case t.OOMKilled() && t.MemoryLimit != "":
		return fmt.Sprintf("the agent container ran out of memory after %s and was killed (OOMKilled, memory limit %s). "+
			"Give it more memory in spec.agents.resources of the AgentProject (limits and requests); the next run starts in a new pod with it.", after, t.MemoryLimit)
	case t.OOMKilled():
		return fmt.Sprintf("the agent container was killed for lack of memory after %s (OOMKilled). It has no memory limit, so the node ran out of memory: "+
			"set spec.agents.resources of the AgentProject (requests as high as the limits) so it is scheduled where the memory is free.", after)
	case t != nil:
		return fmt.Sprintf("the agent container stopped after %s (%s, exit code %d) and was restarted (%v)", after, t.Reason, t.ExitCode, e.Err)
	default:
		return fmt.Sprintf("lost the agent container after %s (%v); it was probably restarted, `kubectl describe pod` shows why", after, e.Err)
	}
}

func (e *AgentLostError) Unwrap() error { return e.Err }

// agentLost turns an agent call that got no answer into an *AgentLostError:
// it waits for the restarted container to be ready and for the operator to
// record how it stopped during this run. Other errors, errors already turned
// into one, and calls cut off by the pod stopping give nil.
func (r *Runner) agentLost(ctx context.Context, err error, start time.Time) *AgentLostError {
	var ce *agentConnError
	var done *AgentLostError
	if !errors.As(err, &ce) || errors.As(err, &done) || ctx.Err() != nil {
		return nil
	}
	lost := &AgentLostError{After: r.now().Sub(start), Err: err, notSent: ce.notSent()}
	r.log().Info("lost the agent container, waiting for it to come back", "error", err.Error())
	poll := func(timeout time.Duration, done func() bool) bool {
		deadline := time.Now().Add(timeout)
		for !done() {
			if !time.Now().Before(deadline) {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(agentLostPoll):
			}
		}
		return true
	}
	lost.back = poll(agentBackTimeout, func() bool { return r.Agent.Ready(ctx) == nil })
	// A termination during this run (FinishedAt has second precision) that
	// was not blamed for an earlier loss.
	since := r.run.start.Truncate(time.Second)
	poll(terminationTimeout, func() bool {
		var task aiv1.AgentTask
		if r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task) != nil {
			return false
		}
		t := task.Status.AgentTermination
		if t == nil || t.FinishedAt == nil || t.FinishedAt.Time.Before(since) || t.Restarts <= r.blamedRestarts {
			return false
		}
		lost.Term, r.blamedRestarts = t, t.Restarts
		return true
	})
	return lost
}
