package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// followUps opens the follow-ups of the milestone's retrospective that an
// owner pinned in it names with "create 1 2" as a reply in its thread on
// the tracking issue, or dismisses the rest with "create none", and answers
// there. Anyone else's "create", and one outside the thread, does
// nothing. The threads are read only when there is such a comment.
func (a *autopilot) followUps(ctx context.Context, notes []gitlab.Note) error {
	var retro *aiv1.PilotRetrospective
	if ps := a.st.Status.Pilot; ps != nil {
		retro = ps.Retrospective
	}
	if retro == nil || retro.NoteID == 0 {
		return nil
	}
	since := max(retro.LastNoteID, retro.NoteID)
	var asks []gitlab.Note
	for _, n := range notes {
		if _, ok := command.RetroCreate(n.Body); (ok || command.RetroNone(n.Body)) && n.ID > since && !n.System && n.Author.ID != a.project.Status.BotUserID &&
			slices.Contains(retro.OwnerIDs, n.Author.ID) {
			asks = append(asks, n)
		}
	}
	if len(asks) > 0 {
		threads, err := a.gl.ListIssueDiscussions(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
		if err != nil {
			return fmt.Errorf("listing the threads of the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
		}
		i := slices.IndexFunc(threads, func(d gitlab.Discussion) bool { return len(d.Notes) > 0 && d.Notes[0].ID == retro.NoteID })
		for _, n := range asks {
			if i < 0 || !slices.ContainsFunc(threads[i].Notes[1:], func(r gitlab.Note) bool { return r.ID == n.ID }) {
				continue
			}
			act := a.createFollowUps
			if command.RetroNone(n.Body) {
				act = a.dismissFollowUps
			}
			if err := act(ctx, threads[i].ID, n); err != nil {
				return err
			}
		}
	}
	if latest := max(since, newestNote(notes)); latest != a.st.Status.Pilot.Retrospective.LastNoteID {
		return a.r.patchPilot(ctx, a.st, func(ps *aiv1.PilotStatus) { ps.Retrospective.LastNoteID = latest })
	}
	return nil
}

// dismissFollowUps records an owner's "create none", once: the remaining
// follow-ups are settled and the milestone can close. What was opened or
// taken over stays, and each can still be opened. It answers in the
// retrospective's thread only the first time.
func (a *autopilot) dismissFollowUps(ctx context.Context, thread string, n gitlab.Note) error {
	if a.st.Status.Pilot.Retrospective.DismissedAt != nil {
		return nil
	}
	if err := a.r.patchPilot(ctx, a.st, func(ps *aiv1.PilotStatus) {
		ps.Retrospective.DismissedBy, ps.Retrospective.DismissedAt = n.Author.Username, &metav1.Time{Time: a.r.now()}
	}); err != nil {
		return err
	}
	a.log.Info("follow-ups of the retrospective dismissed", "by", n.Author.Username)
	body := "🧭 The remaining follow-ups are dismissed; the milestone can close. `create 1` still opens one."
	if _, err := a.gl.ReplyIssueDiscussion(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, thread, body); err != nil {
		a.log.Info("could not answer a create none", "note", n.ID, "error", err.Error())
	}
	return nil
}

// createFollowUps opens the follow-ups an owner's "create" names, each once
// (recorded right after it is opened), and answers in the retrospective's
// thread with the links. One that GitLab refuses is reported, not tried
// again until an owner asks again.
func (a *autopilot) createFollowUps(ctx context.Context, thread string, n gitlab.Note) error {
	nums, _ := command.RetroCreate(n.Body)
	count := len(a.st.Status.Pilot.Retrospective.FollowUps)
	if nums == nil { // all
		for k := 1; k <= count; k++ {
			nums = append(nums, k)
		}
	}
	m := a.st.Spec.Milestone
	var lines []string
	for _, k := range nums {
		key := strconv.Itoa(k)
		retro := a.st.Status.Pilot.Retrospective
		if iid, ok := retro.Created[key]; ok {
			lines = append(lines, fmt.Sprintf("- %d: #%d (opened before)", k, iid))
			continue
		}
		if iid, ok := retro.Taken[key]; ok {
			lines = append(lines, fmt.Sprintf("- %d: taken over by the milestone %%%d", k, iid))
			continue
		}
		if k < 1 || k > count {
			lines = append(lines, fmt.Sprintf("- %d: unknown, the retrospective has %d follow-ups", k, count))
			continue
		}
		f := retro.FollowUps[k-1]
		is, err := a.gl.CreateIssue(ctx, a.spec.GitLab.Project, gitlab.CreateIssueOptions{
			Title: inertLine(f.Title, 200),
			Description: fmt.Sprintf("%s\n\n_Follow-up from the retrospective of %s (#%d)._\n\n<!-- ai-operator:follow-up=%d-%d -->",
				inert(f.Body), inertLine(m.Title, 200), a.ap.TrackingIssueIID, m.IID, k),
			Labels: a.spec.Workflow.GeneratedLabel,
		})
		if err != nil {
			a.log.Info("could not open a follow-up of the retrospective", "followUp", k, "error", err.Error())
			lines = append(lines, fmt.Sprintf("- %d: GitLab did not open it; ask again", k))
			continue
		}
		if err := a.r.patchPilot(ctx, a.st, func(ps *aiv1.PilotStatus) {
			if ps.Retrospective.Created == nil {
				ps.Retrospective.Created = map[string]int64{}
			}
			ps.Retrospective.Created[key] = is.IID
		}); err != nil {
			return err
		}
		a.log.Info("follow-up of the retrospective opened", "followUp", k, "issue", is.IID, "by", n.Author.Username)
		lines = append(lines, fmt.Sprintf("- %d: #%d", k, is.IID))
	}
	body := "🧭 The retrospective has no follow-ups."
	if len(lines) > 0 {
		body = "🧭 Follow-ups of the retrospective:\n" + strings.Join(lines, "\n")
	}
	if _, err := a.gl.ReplyIssueDiscussion(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, thread, body); err != nil {
		a.log.Info("could not answer a create of follow-ups", "note", n.ID, "error", err.Error())
	}
	return nil
}
