package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// notOwnerRelease answers a `release` or `no-release` by someone who is not
// an owner.
const notOwnerRelease = "Only the milestone's owners may decide on releases."

// maxOwnerReleaseWhy is how long the reason of an owner's release record is.
const maxOwnerReleaseWhy = 200

// ownerRelease is `/agent release [reason]` on the tracking issue, owners
// only: a Waiting owner record at the newest commit of the default branch,
// the SHA of its newest pipeline (the releaser waits until that pipeline is
// green). The reason is optional; without one the record says who asked.
func (a *autopilot) ownerRelease(ctx context.Context, reason, by string) (string, bool, error) {
	base, err := a.base(ctx)
	if err != nil {
		return "", false, err
	}
	ps, err := a.gl.ListPipelines(ctx, a.spec.GitLab.Project, gitlab.ListPipelinesOptions{Ref: base, Limit: 1})
	if err != nil {
		return "", false, fmt.Errorf("listing the pipelines of %s: %w", base, err)
	}
	if len(ps) == 0 || ps[0].SHA == "" {
		return fmt.Sprintf("`release` needs a pipeline on %s to know its newest commit; there is none yet.", base), false, nil
	}
	head := ps[0].SHA
	why := oneLine(reason, maxOwnerReleaseWhy)
	if why == "" {
		why = "asked by " + by
	}
	// Comment first: if it fails, the command is applied again on the next
	// poll.
	if err := a.comment(ctx, sentence(fmt.Sprintf("📦 Release decided after %s by %s: %s", short(head), by, untrusted(why)))); err != nil {
		return "", false, err
	}
	rec := aiv1.ReleaseRecord{ID: nextReleaseID(a.ap.Releases), By: aiv1.ReleaseByOwner, Actor: by, Why: why,
		SHA: head, State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(a.r.now())}
	a.ap.Releases = append(a.ap.Releases, rec)
	a.log.Info("owner release added", "release", rec.ID, "sha", head, "by", by)
	return fmt.Sprintf("Release decided after %s.", short(head)), true, nil
}

// ownerNoRelease is `/agent no-release #N [reason]`, owners only: it drops
// the Planned and Waiting records after #N that are not Final. Running and
// ended records are left alone; without a record to drop it answers with
// its usage.
func (a *autopilot) ownerNoRelease(ctx context.Context, arg, by string) (string, bool, error) {
	first, reason, _ := strings.Cut(strings.TrimSpace(arg), " ")
	iid, _ := strconv.ParseInt(strings.TrimPrefix(first, "#"), 10, 64)
	var drop []int
	for i, o := range a.ap.Releases {
		if iid != 0 && o.AfterIssue == iid && !o.Final && (o.State == aiv1.ReleasePlanned || o.State == aiv1.ReleaseWaiting) {
			drop = append(drop, i)
		}
	}
	if len(drop) == 0 {
		return fmt.Sprintf("`no-release` needs an issue with a planned release that has not started, e.g. `%s no-release #12 not yet`.", a.spec.Workflow.CommandPrefix), false, nil
	}
	why := oneLine(reason, maxOwnerReleaseWhy)
	dropped := "dropped by " + by
	shown := dropped
	if why != "" {
		dropped += ": " + why
		shown += ": " + untrusted(why)
	}
	if err := a.comment(ctx, sentence(fmt.Sprintf("⏹️ Release after #%d %s", iid, shown))); err != nil {
		return "", false, err
	}
	now := metav1.NewTime(a.r.now())
	for _, i := range drop {
		o := &a.ap.Releases[i]
		o.State, o.Reason, o.EndedAt = aiv1.ReleaseDropped, dropped, &now
		a.log.Info("owner dropped a release", "release", o.ID, "issue", iid, "by", by)
	}
	return fmt.Sprintf("Release after #%d dropped.", iid), true, nil
}
