package controller

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// notOwnerParallel answers a `parallel` by someone who is not an owner.
const notOwnerParallel = "Only the milestone's owners may change how many issues run at the same time."

// setParallel sets the milestone description's parallel line to arg (`parallel
// N`, owners only), read again so other edits stay, and says so on the
// tracking issue. The next poll reads the line into the spec task
// (syncSpecTask); the line is no part of the spec, so nothing is re-planned.
func (a *autopilot) setParallel(ctx context.Context, arg, by string) (string, bool, error) {
	prefix := a.spec.Workflow.CommandPrefix
	n, err := strconv.ParseInt(arg, 10, 32)
	if err != nil || n < 1 || n > rules.MaxParallelLine {
		return fmt.Sprintf("`parallel N` needs a number from 1 to %d, e.g. `%s parallel 2`.", rules.MaxParallelLine, prefix), false, nil
	}
	m, err := a.gl.GetMilestone(ctx, a.spec.GitLab.Project, a.st.Spec.Milestone.ID)
	if err != nil {
		return "", false, fmt.Errorf("reading the milestone: %w", err)
	}
	if cur, err := rules.Parallel(m.Description, prefix); err == nil && int64(cur) == n {
		return fmt.Sprintf("Up to %d issues run at the same time already.", n), false, nil
	}
	if _, err := a.gl.UpdateMilestoneDescription(ctx, a.spec.GitLab.Project, m.ID, rules.SetParallel(m.Description, prefix, int32(n))); err != nil {
		return "", false, fmt.Errorf("writing the parallel line: %w", err)
	}
	a.log.Info("milestone parallel limit set", "milestone", a.st.Spec.Milestone.Title, "parallel", n, "by", by)
	// After the write, which a retry would find done: a comment that fails
	// leaves the line written.
	if err := a.comment(ctx, fmt.Sprintf("🔢 Up to %d issues run at the same time (by %s).", n, by)); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("Up to %d issues run at the same time.", n), true, nil
}
