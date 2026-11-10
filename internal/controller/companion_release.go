package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// companionReleases adds a release decision for each companion of the
// milestone that waits for its home change to be live (decision 12, see
// docs/usage.md#releases): {companion, after the home issue, its merge
// commit, Waiting}. The releaser then runs it like any other; the companion
// goes on by itself once a tag contains the home merge. A docs-only home
// merge is live at merge and needs none. No record is added while another
// one covers the home merge (companionReleaseCovered).
func (a *autopilot) companionReleases(ctx context.Context) error {
	if a.project == nil {
		return nil
	}
	for i := range a.project.Status.Companions {
		e := &a.project.Status.Companions[i]
		if e.MilestoneIID != a.st.Spec.Milestone.IID || e.State != aiv1.LandingWaitingRelease || e.DocsOnly || e.HomeMergeSHA == "" {
			continue
		}
		covered, err := a.companionReleaseCovered(ctx, e)
		if err != nil {
			return err
		}
		if covered {
			continue
		}
		rec := aiv1.ReleaseRecord{ID: nextReleaseID(a.ap.Releases), By: aiv1.ReleaseByCompanion, AfterIssue: e.HomeIssueIID,
			SHA: e.HomeMergeSHA, State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(a.r.now()),
			Why: fmt.Sprintf("%s waits for #%d to be live", landingRef(e), e.HomeIssueIID)}
		a.ap.Releases = append(a.ap.Releases, rec)
		a.log.Info("companion release added", "release", rec.ID, "companion", landingRef(e), "issue", e.HomeIssueIID, "sha", e.HomeMergeSHA)
	}
	return nil
}

// companionReleaseCovered reports whether a record of the milestone already
// covers the home merge of e:
//   - a companion record after its home issue, in any state: a failed or
//     dropped one is not tried again, the final release covers it;
//   - a record after its home issue that has not ended or was released;
//   - a record that has not ended or was released whose commit (the released
//     one once it ran) contains the home merge.
func (a *autopilot) companionReleaseCovered(ctx context.Context, e *aiv1.CompanionStatus) (bool, error) {
	live := func(o aiv1.ReleaseRecord) bool {
		return !o.State.Ended() || o.State == aiv1.ReleaseReleased || o.State == aiv1.ReleaseAlreadyReleased
	}
	for _, o := range a.ap.Releases {
		if o.AfterIssue == e.HomeIssueIID && (o.By == aiv1.ReleaseByCompanion || live(o)) {
			return true, nil
		}
	}
	for _, o := range a.ap.Releases {
		sha := firstNonEmpty(o.ReleasedSHA, o.SHA)
		if !live(o) || sha == "" {
			continue
		}
		if sha == e.HomeMergeSHA {
			return true, nil
		}
		c, err := a.gl.CompareCommits(ctx, a.spec.GitLab.Project, sha, e.HomeMergeSHA)
		switch {
		case gitlab.IsNotFound(err):
			continue // a commit GitLab does not know contains nothing
		case err != nil:
			return false, fmt.Errorf("comparing %s with %s: %w", short(sha), short(e.HomeMergeSHA), err)
		}
		if len(c.Commits) == 0 {
			return true, nil
		}
	}
	return false, nil
}
