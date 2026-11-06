package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The queue and the companions of a milestone (docs/specs/
// cross-repo-changes.spec.md, decision 10): the next issue does not wait for
// a companion to land, except that an issue with a companion on project P
// does not start while another companion on P from the same milestone is
// open. The milestone is done only when all its companions have landed.

// openCompanion is a companion merge request of the milestone that has not
// landed (merged or closed).
type openCompanion struct {
	// ref names it, "<project>!<iid>"; project is its normalized path.
	ref, project string
	// home is the issue it belongs to.
	home int64
	// landing: it lands by rule (its home merge request was merged, or is
	// about to be by autopilot); else it waits for a human, since its home
	// issue was skipped or not started by autopilot.
	landing bool
}

// openCompanions lists the milestone's companions that have not landed:
// the entries of status.companions (after the home merge) that are neither
// merged nor closed, then the open companion merge requests of the queue's
// tasks that are not recorded there yet.
func (a *autopilot) openCompanions(ctx context.Context) ([]openCompanion, error) {
	var out []openCompanion
	if a.project == nil {
		return nil, nil
	}
	entries := a.project.Status.Companions
	for i := range entries {
		e := &entries[i]
		if e.MilestoneIID != a.st.Spec.Milestone.IID || e.State.Ended() {
			continue
		}
		out = append(out, openCompanion{ref: landingRef(e), project: aiv1.NormalizeProject(e.Project), home: e.HomeIssueIID, landing: true})
	}
	for _, it := range a.ap.Items {
		t := a.tasks[it.IID]
		if t == nil || t.Status.Run.Companion == nil || t.Status.Run.Companion.MRIID == 0 {
			continue
		}
		c := t.Status.Run.Companion
		if findCompanion(entries, c.Project, c.MRIID) != nil {
			continue // its entry above says whether it is open
		}
		open, err := a.companionMROpen(ctx, c)
		if err != nil {
			return nil, err
		}
		if open {
			landing := it.State == aiv1.AutopilotItemActive || it.State == aiv1.AutopilotItemMerged
			out = append(out, openCompanion{ref: companionRef(c), project: aiv1.NormalizeProject(c.Project), home: it.IID, landing: landing})
		}
	}
	return out, nil
}

// companionMROpen reports whether a task's companion merge request is open,
// read once per poll.
func (a *autopilot) companionMROpen(ctx context.Context, c *aiv1.CompanionRun) (bool, error) {
	ref := companionRef(c)
	if open, ok := a.companionOpen[ref]; ok {
		return open, nil
	}
	mr, err := a.gl.GetMergeRequest(ctx, c.Project, c.MRIID)
	switch {
	case gitlab.IsNotFound(err):
	case err != nil:
		return false, fmt.Errorf("getting the companion merge request %s: %w", ref, err)
	}
	open := err == nil && mr.State == "opened"
	if a.companionOpen == nil {
		a.companionOpen = map[string]bool{}
	}
	a.companionOpen[ref] = open
	return open, nil
}

// landingCompanions are the refs of the open companions the milestone's
// done waits for, sorted: those that land by rule. A skipped issue's open
// companion does not land without its home merge; the open issue keeps the
// milestone open instead (CloseWaitsFor).
func (a *autopilot) landingCompanions(ctx context.Context) ([]string, error) {
	open, err := a.openCompanions(ctx)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, c := range open {
		if c.landing && !slices.Contains(refs, c.ref) {
			refs = append(refs, c.ref)
		}
	}
	slices.Sort(refs)
	return refs, nil
}

// companionOf is the normalized companion project of a queued issue: its
// task's spec.companion, else the issue's agent-repo label if one of the
// milestone's repo lines names that project; "" for none.
func (a *autopilot) companionOf(iid int64) string {
	if t := a.tasks[iid]; t != nil && t.Spec.Companion != nil {
		return aiv1.NormalizeProject(t.Spec.Companion.Project)
	}
	is, ok := a.issues[iid]
	if !ok {
		return ""
	}
	for _, l := range is.Labels {
		if !strings.HasPrefix(l, aiv1.RepoLabelPrefix) {
			continue
		}
		p := aiv1.NormalizeProject(strings.TrimPrefix(l, aiv1.RepoLabelPrefix))
		for _, rp := range a.st.Spec.Repos {
			if rp.Problem == "" && aiv1.NormalizeProject(rp.Project) == p {
				return p
			}
		}
		return "" // scoped: an issue has at most one
	}
	return ""
}

// companionHold is the open companion of another issue of the milestone on
// the companion project of it, which holds it back; nil if there is none.
func (a *autopilot) companionHold(ctx context.Context, it *aiv1.AutopilotItem) (*openCompanion, error) {
	p := a.companionOf(it.IID)
	if p == "" {
		return nil, nil
	}
	open, err := a.openCompanions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range open {
		if c := &open[i]; c.project == p && c.home != it.IID {
			return c, nil
		}
	}
	return nil, nil
}

// waits says how the companion holds an issue back.
func (c *openCompanion) waits() string {
	if c.landing {
		return fmt.Sprintf("waits for the companion %s of #%d to land", c.ref, c.home)
	}
	return fmt.Sprintf("waits for the companion %s of #%d, which is open (merge or close it)", c.ref, c.home)
}

// reportCompanionWaits comments format (with the list of refs) on the
// tracking issue once per change of the set, and records it as
// CloseWaitsForCompanions.
func (a *autopilot) reportCompanionWaits(ctx context.Context, refs []string, format string) error {
	if slices.Equal(refs, a.ap.CloseWaitsForCompanions) {
		return nil
	}
	if err := a.comment(ctx, fmt.Sprintf(format, strings.Join(refs, ", "))); err != nil {
		return err
	}
	a.log.Info("autopilot waits for companions to land", "companions", refs)
	a.ap.CloseWaitsForCompanions = refs
	return nil
}
