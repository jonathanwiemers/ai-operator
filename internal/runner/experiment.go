package runner

import (
	"context"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// assignArm decides an issue's arm in experiment e: none ("") when the
// issue has explicit routing labels (explicitRouting), else ArmFor's.
func assignArm(spec aiv1.AgentProjectSpec, e aiv1.Experiment, issue *gitlab.Issue) aiv1.ExperimentAssignment {
	a := aiv1.ExperimentAssignment{Name: e.Name}
	if !explicitRouting(spec.Workflow, issue) {
		a.Arm = aiv1.ArmFor(spec.GitLab.Project, e, issue.IID).Name
	}
	return a
}

// explicitRouting reports whether an issue carries a model label, or a
// profile or effort label that is not in its plan-labels marker: one a
// human or a command set. Without a marker every such label is explicit.
func explicitRouting(wf aiv1.WorkflowSpec, issue *gitlab.Issue) bool {
	planned, _ := plannedLabels(issue.Description)
	for _, l := range issue.Labels {
		switch {
		case strings.HasPrefix(l, wf.ModelLabelPrefix):
			return true
		case strings.HasPrefix(l, wf.ProfileLabelPrefix), strings.HasPrefix(l, wf.EffortLabelPrefix):
			if !slices.Contains(planned, l) {
				return true
			}
		}
	}
	return false
}

// runArm returns the arm a run applies: the recorded one, while the
// project's experiment is valid, has the same name and still has that arm.
func runArm(spec aiv1.AgentProjectSpec, a *aiv1.ExperimentAssignment) (aiv1.ExperimentArm, bool) {
	if a == nil || a.Arm == "" {
		return aiv1.ExperimentArm{}, false
	}
	e, ok := spec.ValidExperiment()
	if !ok || e.Name != a.Name {
		return aiv1.ExperimentArm{}, false
	}
	return e.Arm(a.Arm)
}

// armLabels returns the issue's labels with the arm's profile and effort in
// place of the planner's: a profile or effort label the plan did not set
// (planned, the plan-labels marker) keeps winning, so the precedence is
// label or command > arm > project config. IssueRole then reads them like
// any label: it caps the effort and ignores it on cursor.
func armLabels(wf aiv1.WorkflowSpec, labels, planned []string, arm aiv1.ExperimentArm) []string {
	out := slices.Clone(labels)
	set := func(prefix, value string) {
		if value == "" {
			return
		}
		for _, l := range out {
			if strings.HasPrefix(l, prefix) && !slices.Contains(planned, l) {
				return
			}
		}
		out = slices.DeleteFunc(out, func(l string) bool { return strings.HasPrefix(l, prefix) })
		out = append(out, prefix+value)
	}
	set(wf.ProfileLabelPrefix, arm.Profile)
	set(wf.EffortLabelPrefix, arm.Effort)
	return out
}

// experimentArm assigns the issue's arm at its first implement run (one
// status patch) and returns the arm this run applies. Later runs keep the
// assignment, also when the labels change; the pilot never touches it.
func (run *implementRun) experimentArm(ctx context.Context) (aiv1.ExperimentArm, bool) {
	r, task := run.r, run.task
	if task.Status.Experiment == nil {
		e, ok := run.spec.ValidExperiment()
		if !ok {
			return aiv1.ExperimentArm{}, false
		}
		a := assignArm(run.spec, e, run.issue)
		if err := r.patchStatus(ctx, func(s *aiv1.AgentTaskStatus) { s.Experiment = &a }); err != nil {
			// The hash gives the same arm next time, unless a label changes.
			r.log().Info("could not record the experiment arm", "experiment", a.Name, "arm", a.Arm, "error", err.Error())
		}
		task.Status.Experiment = &a
		r.log().Info("experiment arm assigned", "experiment", a.Name, "arm", a.Arm)
	}
	arm, ok := runArm(run.spec, task.Status.Experiment)
	r.arm = ""
	if ok {
		r.arm = arm.Name
	}
	return arm, ok
}
