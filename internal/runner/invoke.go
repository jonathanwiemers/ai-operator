package runner

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// invoke runs a role, resuming the given session when possible. If the
// session cannot be resumed (e.g. the workspace was archived), it starts a
// fresh session with the full prompt.
//
// Usage and rate limits come back as *UsageLimitError. If the project already
// knows the engine is limited, the engine is not called at all. A role with a
// fallback whose engine is not limited runs on it instead (see fallBack).
// Once the operator set spec.budgetStop, no call starts: *BudgetStopError.
func (r *Runner) invoke(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	if err := r.budgetStop(ctx); err != nil {
		if r.cutsOff(inv) {
			// No call ran, so none is retried; usually a clean tree, the
			// call before ended normally.
			r.checkpoint(ctx, cutBudgetStop, nil, true) // no call to note it on
		}
		return nil, err
	}
	// Before a fallback too: whichever engine runs the call takes them.
	inv, fullPrompt = withOwnerMessages(r.promptOwnerMessages(ctx, inv), inv, fullPrompt)
	if near := r.nearLimit(ctx, role); near != nil {
		// fallBack hands the soft limit back when there is no usable
		// fallback: then the role runs on its own engine.
		if res, err := r.fallBack(ctx, role, inv, fullPrompt, nil, near, near); err != error(near) {
			return res, err
		}
	}
	res, err := r.invokeOn(ctx, role, inv, fullPrompt)
	if lim, ok := AsUsageLimit(err); ok {
		lim.Model = inv.Model
		if role.Fallback != nil {
			return r.fallBack(ctx, role, inv, fullPrompt, res, err, lim)
		}
	}
	return res, err
}

// invokeOn runs a role on its own engine (see invoke). The result says
// which engine and model ran.
func (r *Runner) invokeOn(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string) (*Result, error) {
	inv = r.withStepGate(role, r.withReadGate(role, r.withSubagents(role, inv)))
	inv.LogPrompt = r.logPrompts
	inv.CountLines = implementerKind(inv.Kind)
	// A running claude implementer call can take a message (LocalAgent.Message).
	inv.StreamInput = role.Engine == aiv1.EngineClaude && implementerKind(inv.Kind)
	if len(r.changedPins) > 0 && r.run != nil {
		inv.PerRunToolchains = toolchainsRunName(r.run.id)
	}
	if lim, ok := r.activeLimit(role.Engine); ok {
		return nil, &UsageLimitError{Engine: role.Engine, ResetAt: lim.Until.Time, Message: "project-wide limit: " + lim.Message}
	}
	redialed, retried := false, false
	// A call stopped for owner messages goes on as a resumed call: last is
	// the call that ran last, resumed the messages it took and stoppedCost
	// what the stopped calls cost.
	last, resumed, stoppedCost := inv, []aiv1.OwnerMessage(nil), 0.0
	run := func(inv Invocation) (*Result, error) {
		for {
			last = inv
			r.markInFlight(inv)
			start := r.now()
			stopMessages := r.watchOwnerMessages(ctx, inv)
			res, err := r.Agent.Run(ctx, role.Engine, inv)
			stoppedFor := stopMessages()
			// No error, so neither a limit nor a checkpoint below.
			stopped := err == nil && res != nil && res.Stopped && stoppedFor != nil
			lost := r.agentLost(ctx, err, start)
			cpReason := ""
			if lost == nil {
				err = classifyLimit(role.Engine, err, res)
				// The agent is up: save the work of an implementer call cut off.
				cpReason = r.cutReason(ctx, inv, res, err)
				// A checkpoint saves nothing of a merge in progress but
				// aborts it: a fallback that goes on with the conflicts
				// needs it, and checkpoints itself when it is cut off.
				if cpReason == cutUsageLimit && inv.Kind == aiv1.CallKindConflict && r.fallsBack(role) {
					cpReason = ""
				}
			} else if retriesRestart(lost, redialed, retried) && r.cutsOff(inv) {
				// Saved before the reset below throws the edits away; the
				// retried call continues from the checkpoint.
				cpReason = cutContainerRestart
			}
			cpSHA := ""
			if cpReason != "" {
				cpRes := res
				if lost != nil {
					cpRes = nil
				}
				// A call that was never sent is not recorded below.
				_, cpSHA = r.checkpoint(ctx, cpReason, cpRes, lost != nil && lost.notSent)
			}
			if implementerKind(inv.Kind) {
				r.implLeft, r.implCut = 0, cpReason != ""
				if res != nil && res.CallUsage != nil && cpSHA == "" {
					r.implLeft = res.CallUsage.LinesAdded + res.CallUsage.LinesDeleted
				}
			}
			if lost == nil || !lost.notSent {
				c := r.newCall(role.Engine, inv, res, start)
				if cpSHA != "" {
					c.Checkpoint, c.CheckpointSHA = cpReason, cpSHA
				}
				c.StoppedForMessage = stopped
				r.recordCall(ctx, c)
			}
			if stopped {
				// Not a failure: the workspace stays as it is, the step goes on.
				res.Engine, res.Model = role.Engine, inv.Model
				r.recordUsage(ctx, role.Engine, res.Usage)
				r.rememberSession(res)
				stoppedCost += res.CostUSD
				var took []aiv1.OwnerMessage
				inv, took = r.resumeAfterStop(ctx, inv, res.SessionID, fullPrompt, stoppedFor)
				resumed = append(resumed, took...)
				continue
			}
			switch {
			case lost == nil:
				if res != nil {
					res.Engine, res.Model = role.Engine, inv.Model
					r.recordUsage(ctx, role.Engine, res.Usage)
					r.rememberSession(res)
				}
				return res, err
			case !lost.back:
				return nil, lost
			case lost.notSent && !redialed:
				// The agent was down before the call, nothing ran: call again.
				redialed = true
			case retriesRestart(lost, redialed, retried):
				// A crash may not happen again; running out of memory would.
				// The killed call's edits (saved above when it is the
				// implementer's) and a stale git lock go first.
				retried = true
				r.log().Info("agent container restarted during the call, trying once more", "reason", lost.Term.Reason, "exitCode", lost.Term.ExitCode)
				if err := r.Agent.Reset(ctx, homeTree); err != nil {
					return nil, lost
				}
			default:
				return nil, lost
			}
		}
	}
	if inv.Resume == "" {
		inv.Prompt = fullPrompt
	}
	res, err := run(inv)
	if retriedFresh(last, res, err) {
		r.log().Info("could not resume session, starting fresh", "session", last.Resume, "error", err.Error())
		inv.Resume = ""
		inv.Prompt = fullPrompt
		if len(resumed) > 0 {
			inv.Prompt += "\n\n" + ownerMessagesPrompt(resumed)
		}
		res, err = run(inv)
	}
	if res != nil {
		// The step's result: the stopped calls' cost adds up with it.
		res.CostUSD += stoppedCost
	}
	return res, err
}

// toolchainsRunName turns a run ID ("<task>#<request>") into the name of its
// per-run toolchain directories: [a-z0-9-] and at most 100 characters, the run
// number at the end kept.
func toolchainsRunName(id string) string {
	name := strings.Trim(strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return c
		}
		return '-'
	}, strings.ToLower(id)), "-")
	if len(name) > 100 {
		name = strings.TrimLeft(name[len(name)-100:], "-")
	}
	return name
}

// withSubagents gives a claude call the role's subagents (--agents) and the
// line telling it to delegate to them. role is what the call runs on, so a
// fallback's engine counts, not the configured one. Cursor has no subagents:
// the call runs without them, with a warning once per run and role. The line
// ends the system prompt; withReadGate appends its sentence to it.
func (r *Runner) withSubagents(role aiv1.RoleSpec, inv Invocation) Invocation {
	if len(role.Subagents) == 0 {
		return inv
	}
	if role.Engine != aiv1.EngineClaude {
		if r.run.warnOnce("subagents/" + inv.Role + "/" + string(role.Engine)) {
			r.log().Info(fmt.Sprintf("subagents are claude only; ignored for role %s on %s", inv.Role, role.Engine))
		}
		return inv
	}
	agents, err := role.AgentsJSON()
	if err != nil {
		r.log().Error(err, "subagents ignored", "role", inv.Role)
		return inv
	}
	inv.Agents = agents
	inv.SystemPrompt += "\n\n" + subagentsNote(role.Subagents)
	return inv
}

func roleInvocation(role aiv1.RoleSpec, system string, tools []string, schema string, readOnly bool) Invocation {
	if len(role.AllowedTools) > 0 {
		tools = role.AllowedTools
	}
	var bash int32
	if role.BashMaxOutputChars != nil {
		bash = *role.BashMaxOutputChars
	}
	return Invocation{
		BashMaxOutputChars: bash,
		SystemPrompt:       withInstructions(system, role.Instructions),
		Model:              role.Model,
		Effort:             role.Effort,
		MaxBudgetUSD:       role.MaxBudgetUSD,
		AllowedTools:       tools,
		JSONSchema:         schema,
		ReadOnly:           readOnly,
	}
}
