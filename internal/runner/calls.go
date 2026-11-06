package runner

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// newCall describes one engine call of the run that started at start for
// status.run.calls. res may be nil (the call failed without a result).
func (r *Runner) newCall(engine aiv1.Engine, inv Invocation, res *Result, start time.Time) aiv1.AgentCall {
	c := agentCall(engine, inv, res, start, r.now())
	c.Run = r.runNumber
	return c
}

// agentCall describes one engine call that ran from start to end; res may
// be nil. Seq and Run are left to the caller.
func agentCall(engine aiv1.Engine, inv Invocation, res *Result, start, end time.Time) aiv1.AgentCall {
	c := aiv1.AgentCall{
		At: metav1.Time{Time: start}, Kind: inv.Kind, Role: inv.Role, Engine: engine, Model: inv.Model, Effort: inv.Effort,
		Resumed: inv.Resume != "", FallbackFrom: inv.FallbackFrom, CostSource: aiv1.CostUnknown,
		DurationMS: end.Sub(start).Milliseconds(),
	}
	if res == nil {
		return c
	}
	if engine == aiv1.EngineClaude {
		// Cursor reports no cost; the operator estimates it from tokens.
		c.CostUSD, c.CostSource = aiv1.FormatCost(res.CostUSD), aiv1.CostReported
	}
	u := res.CallUsage
	if u == nil {
		return c
	}
	if c.Model == "" {
		c.Model = u.MainModel()
	}
	c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens = u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens
	c.Turns, c.SubagentCalls, c.ReadGateBlocks = u.Turns, u.SubagentCalls, u.ReadGateBlocks
	if u.DurationMS > 0 {
		c.DurationMS = u.DurationMS
	}
	for _, m := range u.Models {
		c.Models = append(c.Models, aiv1.CallModelUsage{Model: m.Model, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadTokens, CacheWriteTokens: m.CacheWriteTokens, CostUSD: aiv1.FormatCost(m.CostUSD)})
	}
	c.ContextSeries, c.SubagentTurns = u.ContextSeries, u.SubagentTurns
	return c
}

// recordCall adds a call to the pending ones and writes them to the status
// at once, so spend is visible (and budgets can be checked) between calls.
func (r *Runner) recordCall(ctx context.Context, c aiv1.AgentCall) {
	r.callsMu.Lock()
	r.pendingCalls = append(r.pendingCalls, c)
	r.callsMu.Unlock()
	if err := r.patchRunWithCalls(ctx, nil); err != nil {
		r.log().Info("could not record the agent call, trying again with the next status update", "kind", c.Kind, "error", err.Error())
	}
}

// patchRunWithCalls is patchRun that also appends the pending calls; mutate
// may be nil. The calls stay pending when the patch fails, for the next one
// to write.
func (r *Runner) patchRunWithCalls(ctx context.Context, mutate func(*aiv1.RunStatus)) error {
	r.callsMu.Lock()
	defer r.callsMu.Unlock()
	pending := r.pendingCalls
	if mutate == nil && len(pending) == 0 {
		return nil // another call's patch wrote them
	}
	err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
		aiv1.AppendCalls(s, pending...)
		if mutate != nil {
			mutate(s)
		}
	})
	if err == nil {
		r.pendingCalls = nil
	}
	return err
}
