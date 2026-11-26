package runner

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// pilotRound is the round the queue's pilot task works in, nil for a
// milestone's pilot: milestones have no rounds.
func pilotRound(task *aiv1.AgentTask) *aiv1.PilotRound {
	if !queuePilot(task) {
		return nil
	}
	return task.Spec.Source.PilotRound
}

// enterRound starts the round of the queue's pilot in s: a round number the
// runner has not worked in drops the pilot's sessions of every engine, so
// the round's first call starts fresh and knows the previous round only from
// the note in its prompt. It reports whether s changed.
func (s *state) enterRound(task *aiv1.AgentTask) bool {
	rd := pilotRound(task)
	if rd == nil || rd.Number == s.PilotRound {
		return false
	}
	for k := range s.Sessions {
		if strings.HasPrefix(k, sessionKey("pilot", "")) {
			delete(s.Sessions, k)
		}
	}
	s.PilotRound = rd.Number
	return true
}

// pilotBudgetSpent says that the pilot's budget is used up, so then: for the
// milestone, or for the queue's round until the next round starts.
func pilotBudgetSpent(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, then string) string {
	what, until := "milestone", ""
	if pilotRound(task) != nil {
		what, until = "round", " until the next round starts"
	}
	return fmt.Sprintf("The pilot's budget for this %s ($%s, `pilot.maxBudgetUSD`) is used up, so %s%s.", what, spec.Pilot.MaxBudgetUSD, then, until)
}

// RoundNoteAnswer is the queue's pilot's note of the round that ends.
type RoundNoteAnswer struct {
	Note string `json:"note"`
}

const roundNoteSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["note"],
  "properties": {
    "note": {"type": "string", "description": "The round note in markdown, at most 1500 characters: what merged, what is held and why, and the open threads."}
  }
}`

// roundNotePrompt asks for the note of the round that ends, with the queue's
// state.
const roundNotePrompt = `This round of the loose issues queue ends. Write the round note: the
only thing your next round knows of this one, besides the queue's state, the
open prompts and the open proposals. At most %d characters, in markdown:
what merged, what is held and why, and the open threads (what you were
looking into, what you would try next). Facts and plans only: no greeting,
no proposal, no actions.

## Queue state

%s
`

// runRoundNote answers spec.source.roundNote when it is due: one call that
// resumes the round's session (the round that ends: the operator starts the
// next one after the note) with the queue's state, recorded in
// status.pilot.roundNote. A call that fails or hits a usage limit, and a
// round whose budget is used up, record why there is no note; the operator
// then posts a note of the facts. The runner never posts the note. It
// returns the call's error, after the record.
func (r *Runner) runRoundNote(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, out *Outcome, st *state) error {
	rq := task.Spec.Source.RoundNote
	if !queuePilot(task) || rq <= task.Status.Run.ObservedRunRequest || rq > task.Spec.RunRequest {
		return nil
	}
	if ps := task.Status.Pilot; ps != nil && ps.RoundNote != nil && ps.RoundNote.Request == rq {
		return nil // recorded before the run was cut off
	}
	out.Summary = "wrote the round note"
	rec := &aiv1.PilotRoundNote{Request: rq}
	note, err := r.roundNote(ctx, gl, task, project, spec, out, st)
	if err != nil {
		rec.Failed = textutil.Truncate(err.Error(), 500)
		out.Summary = "no round note: " + rec.Failed
	} else {
		rec.Note = note
	}
	if rerr := r.recordRoundNote(ctx, task, rec); rerr != nil {
		return rerr
	}
	return err
}

// roundNote is the call of runRoundNote.
func (r *Runner) roundNote(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, out *Outcome, st *state) (string, error) {
	left := pilotLeft(spec, task, out)
	if left < 0.01 {
		return "", fmt.Errorf("the pilot's budget for this round ($%s, `pilot.maxBudgetUSD`) is used up", spec.Pilot.MaxBudgetUSD)
	}
	s := r.step(ctx, "read queue state", "for", "round note")
	queue, _, _, err := r.queuePilotState(ctx, gl, task, project, spec)
	if err != nil {
		return "", s.fail(err)
	}
	s.done(fmt.Sprintf("%d bytes", len(queue)))
	role := spec.Agents.Pilot
	inv := roleInvocation(role, pilotSystem(task), readOnlyTools, roundNoteSchema, true)
	prompt := fmt.Sprintf(roundNotePrompt, aiv1.MaxRoundNote, queue)
	inv.Role, inv.Kind, inv.MaxBudgetUSD, inv.Prompt = "pilot", aiv1.CallKindPilot, aiv1.ShowCost(left), prompt
	inv.Resume = st.session("pilot", role.Engine)
	s = r.step(ctx, "run round note", "engine", role.Engine, "model", role.Model, "resume", inv.Resume != "")
	res, err := r.invoke(ctx, role, inv, prompt)
	if res != nil {
		out.CostUSD += res.CostUSD
	}
	var a RoundNoteAnswer
	if err == nil {
		err = res.Decode(&a)
	}
	if _, limited := AsUsageLimit(err); limited {
		// Not deferred: the operator posts the facts and starts the next
		// round. Not wrapped, so the run is not deferred either.
		return "", s.fail(fmt.Errorf("the call hit a usage limit: %v", err))
	}
	if err != nil {
		return "", s.fail(err)
	}
	note := textutil.UntrustedMarkdown(a.Note, aiv1.MaxRoundNote)
	if note == "" {
		return "", s.fail(fmt.Errorf("the note is empty"))
	}
	st.setSession("pilot", ranOn(role, res), res.SessionID)
	if err := r.saveState(*st); err != nil {
		r.log().Error(err, "saving the pilot's session")
	}
	s.done(agentResult(role, res, "noted"), "costUSD", res.CostUSD)
	return note, nil
}

// recordRoundNote sets status.pilot.roundNote.
func (r *Runner) recordRoundNote(ctx context.Context, task *aiv1.AgentTask, note *aiv1.PilotRoundNote) error {
	return r.writeFailed(ctx, task, "recordRoundNote", retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var t aiv1.AgentTask
		if err := r.K8s.Get(ctx, client.ObjectKeyFromObject(task), &t); err != nil {
			return err
		}
		orig := t.DeepCopy()
		if t.Status.Pilot == nil {
			t.Status.Pilot = &aiv1.PilotStatus{}
		}
		t.Status.Pilot.RoundNote = note
		return r.K8s.Status().Patch(ctx, &t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	}))
}
