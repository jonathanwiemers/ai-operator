package runner

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// recordUsage keeps an engine's newest usage reading on the task and in the
// project status the run works with. Reviewers call engines in parallel, so
// the whole list is written under a lock. A reading that cannot be written
// never fails the run.
func (r *Runner) recordUsage(ctx context.Context, engine aiv1.Engine, u *aiv1.EngineUsage) {
	if u == nil {
		return
	}
	reading := *u
	reading.Engine, reading.ObservedAt = engine, metav1.Time{Time: r.now()}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.usage = aiv1.MergeEngineUsage(r.usage, reading)
	r.projectStatus.EngineUsage = aiv1.MergeEngineUsage(r.projectStatus.EngineUsage, reading)
	usage := r.usage
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) { s.EngineUsage = usage }); err != nil {
		r.log().Info("could not record the engine's usage", "engine", engine, "error", err.Error())
	}
}
