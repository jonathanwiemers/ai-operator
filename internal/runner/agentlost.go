package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// How long the runner waits for a restarted agent container, and for the
// operator to record why it stopped; and how long a run started again with
// more memory waits, so the operator can replace the pod first.
var (
	agentBackTimeout   = 3 * time.Minute
	terminationTimeout = 30 * time.Second
	agentLostPoll      = 2 * time.Second
	oomRetryDelay      = 30 * time.Second
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
			"Give it more memory in spec.agents.resources of the AgentProject (limits and requests) or raise spec.agents.maxMemory; the next run starts in a new pod with it.", after, t.MemoryLimit)
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

// retriesRestart reports whether invokeOn tries a call again after it lost
// the agent container: back, the call was sent (or already redialed), it
// crashed rather than ran out of memory, and it was not tried again yet.
func retriesRestart(lost *AgentLostError, redialed, retried bool) bool {
	return lost.back && (!lost.notSent || redialed) && !retried && lost.Term != nil && !lost.Term.OOMKilled()
}

// oomRetry returns the memory limit the run starts again with, if it lost the
// agent container to an OOM kill and the operator raises the limit (it
// computes the same from the same record): see agents.maxMemory.
func (r *Runner) oomRetry(err error, task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) string {
	var lost *AgentLostError
	if !errors.As(err, &lost) || !lost.Term.OOMKilled() || task.Status.Run.OOMRetries >= aiv1.MaxOOMRetries {
		return ""
	}
	current, perr := resource.ParseQuantity(lost.Term.MemoryLimit)
	if perr != nil {
		return ""
	}
	next, ok := spec.Agents.RaisedMemory(current)
	if !ok {
		return ""
	}
	return next.String()
}

// retryWithMoreMemory keeps the run request pending after an OOM kill. It
// waits a little, so the operator replaces the pod, which was started with
// the old limit, before the run starts again.
func (r *Runner) retryWithMoreMemory(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, err error, memory string) error {
	now := r.now()
	r.log().Info("agent container ran out of memory, starting the run again with more", "memory", memory,
		"retries", task.Status.Run.OOMRetries+1, "steps", r.run.lines())
	if task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
		var lost *AgentLostError
		errors.As(err, &lost)
		edits := "the killed call's uncommitted edits are lost."
		if !spec.Checkpoint.Disabled {
			edits = "the new pod first saves the killed implementer call's uncommitted edits as a checkpoint."
		}
		body := fmt.Sprintf("🤖 The agent container ran out of memory after %s and was killed (OOMKilled, memory limit %s). "+
			"The run starts again with %s (restart %d of at most %d); %s",
			lost.After.Round(time.Second), lost.Term.MemoryLimit, memory, task.Status.Run.OOMRetries+1, aiv1.MaxOOMRetries, edits)
		if _, cerr := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body+r.run.markdown(out.CostUSD)); cerr != nil {
			r.log().Info("could not post the out of memory note", "error", cerr.Error())
		}
	}
	return r.patchRunWithCalls(ctx, func(s *aiv1.RunStatus) {
		// The request stays pending, but nothing is in progress any more.
		s.ActiveRunRequest = s.ObservedRunRequest
		s.CurrentStep = ""
		s.DeferredUntil = &metav1.Time{Time: now.Add(oomRetryDelay)}
		// Not a usage limit: an engine left over from an earlier deferral
		// would hold up the project's other tasks.
		s.BlockReason = ""
		s.ClearDeferringLimit()
		s.OOMRetries++
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = textutil.Truncate(err.Error(), 2000)
		s.CostUSD = aiv1.AddCost(s.CostUSD, out.CostUSD)
	})
}
