package controller

import (
	"context"
	"maps"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// RequirementsStore stores the requirement counts of the Desk's milestones.
type RequirementsStore interface {
	SetMilestoneRequirements(ctx context.Context, key store.ProjectKey, iid int64, n int) (bool, error)
}

// requirementCounts are the counts to record per project: those of the
// planned open milestones from the last full poll, and what was written.
type requirementCounts struct {
	mu      sync.Mutex
	open    map[store.ProjectKey]map[int64]int
	swept   map[store.ProjectKey]bool // newest closed milestones done
	written map[store.ProjectKey]map[int64]int
}

func (c *requirementCounts) note(key store.ProjectKey, counts map[int64]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		c.open, c.swept, c.written = map[store.ProjectKey]map[int64]int{}, map[store.ProjectKey]bool{}, map[store.ProjectKey]map[int64]int{}
	}
	c.open[key] = counts
	if c.written[key] == nil {
		c.written[key] = map[int64]int{}
	}
}

// record writes the noted counts that changed since the last write and,
// once per start, those of the newest closed milestones. Errors are logged
// at V(1); the next poll tries again. The lock is not held over requests;
// a project reconciles one poll at a time.
func (c *requirementCounts) record(ctx context.Context, st RequirementsStore, gl gitlab.API, key store.ProjectKey, project string) {
	c.mu.Lock()
	counts, swept, written := maps.Clone(c.open[key]), c.swept[key], maps.Clone(c.written[key])
	c.mu.Unlock()
	if counts == nil {
		return
	}
	if !swept {
		closed, err := gl.ListClosedMilestones(ctx, project, 1)
		if err != nil {
			log.FromContext(ctx).V(1).Info("closed milestones' requirements skipped", "error", err.Error())
		}
		for _, m := range closed {
			counts[m.IID] = rules.CountRequirements(m.Description)
		}
		swept = err == nil
	}
	for iid, n := range counts {
		if w, ok := written[iid]; ok && w == n {
			continue
		}
		if _, err := st.SetMilestoneRequirements(ctx, key, iid, n); err != nil {
			log.FromContext(ctx).V(1).Info("milestone requirements not stored", "milestone", iid, "error", err.Error())
			continue
		}
		written[iid] = n
	}
	c.mu.Lock()
	c.swept[key], c.written[key] = swept, written
	c.mu.Unlock()
}
