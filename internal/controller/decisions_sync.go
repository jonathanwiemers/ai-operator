package controller

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// syncDecisions copies the decisions of the milestone of the spec task st
// into the history database: of every issue GitLab lists as updated since
// status.decisions.checkedAt (minus the clock skew), open or closed; of every
// issue once when the status is unset (the backfill). Each issue read
// replaces its stored decisions and the revisit requests read from it (the
// tracking issue has only revisit requests). Reading a follow-up also reads
// the issue whose decision it revisits, once per sweep: its decision may now
// be superseded. The watermark moves only after every listed
// issue was read; on an error the next poll reads the window again. Without
// a database it reads nothing.
func (r *AgentProjectReconciler) syncDecisions(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) error {
	record := r.decisionRecorder()
	if st.Spec.Milestone == nil || record == nil {
		return nil
	}
	now := r.now()
	ms := st.Spec.Milestone
	opts := gitlab.ListIssuesOptions{Milestone: ms.Title, State: "all"}
	if ds := st.Status.Decisions; ds != nil {
		after := ds.CheckedAt.Add(-revisitClockSkew)
		opts.UpdatedAfter = &after
	}
	issues, err := gl.ListIssues(ctx, spec.GitLab.Project, opts)
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", ms.Title, err)
	}
	key := store.ProjectKey{Namespace: st.Namespace, Name: st.Spec.ProjectRef}
	botID := project.Status.BotUserID
	queued := map[int64]bool{}
	for _, is := range issues {
		queued[is.IID] = true
	}
	// issues grows by the issues follow-ups revisit, each read once.
	for i := 0; i < len(issues); i++ {
		is := &issues[i]
		notes, err := gl.ListIssueNotes(ctx, spec.GitLab.Project, is.IID)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", is.IID, err)
		}
		d := store.IssueDecisions{Project: key, MilestoneIID: ms.IID, IssueIID: is.IID}
		if !marker.IsTrackingIssue(is.Description) { // it lists decision links, it has none of its own
			d = issueDecisions(key, ms.IID, is, notes, botID)
		}
		d.Revisits = issueRevisits(is, notes, botID, spec.Workflow.CommandPrefix)
		record(d)
		// Its follow-up may have superseded a decision of the revisited issue.
		if m := marker.RevisitOf.FindStringSubmatch(is.Description); m != nil && botID != 0 && is.Author.ID == botID {
			iid := marker.DecisionIssue(m[1])
			if queued[iid] {
				continue
			}
			queued[iid] = true
			target, err := gl.GetIssue(ctx, spec.GitLab.Project, iid)
			switch {
			case gitlab.IsNotFound(err):
			case err != nil:
				return fmt.Errorf("getting issue #%d: %w", iid, err)
			case target.Milestone != nil && target.Milestone.ID == ms.ID:
				issues = append(issues, *target)
			}
		}
	}
	if len(issues) == 0 && st.Status.Decisions != nil {
		return nil // nothing new: keep the window, save a status write
	}
	orig := st.DeepCopy()
	st.Status.Decisions = &aiv1.DecisionSyncStatus{CheckedAt: metav1.Time{Time: now}}
	return r.Status().Patch(ctx, st, client.MergeFrom(orig))
}

// issueRevisits is every revisit request read from the issue is: the human
// revisit commands among its notes (with the project's prefix and an ID),
// and is itself when it is the bot's follow-up revisiting a decision.
func issueRevisits(is *gitlab.Issue, notes []gitlab.Note, botID int64, prefix string) []store.RevisitRequestRow {
	var out []store.RevisitRequestRow
	if m := marker.RevisitOf.FindStringSubmatch(is.Description); m != nil && botID != 0 && is.Author.ID == botID {
		by, instruction := parseFollowUp(is.Description)
		r := store.RevisitRequestRow{DecisionID: m[1], Source: store.RevisitFollowUp, RequestedBy: by, Instruction: instruction,
			URL: is.WebURL, FollowUpIID: is.IID, FollowUpState: is.State}
		if is.CreatedAt != nil {
			r.At = *is.CreatedAt
		}
		out = append(out, r)
	}
	for _, n := range notes {
		if !IsCommand(n, botID, prefix) {
			continue
		}
		m := marker.Revisit.FindStringSubmatch(n.Body)
		if m == nil || m[1] != prefix {
			continue
		}
		r := store.RevisitRequestRow{DecisionID: strings.ToUpper(m[2]), Source: store.RevisitComment, RequestedBy: n.Author.Username,
			Instruction: strings.TrimSpace(m[3]), URL: fmt.Sprintf("%s#note_%d", is.WebURL, n.ID)}
		if n.CreatedAt != nil {
			r.At = *n.CreatedAt
		}
		out = append(out, r)
	}
	return out
}

// decisionRecorder is where syncDecisions records: the test's hook, else
// History; nil without a database.
func (r *AgentProjectReconciler) decisionRecorder() func(store.IssueDecisions) {
	if r.recordDecisions != nil {
		return r.recordDecisions
	}
	if r.History == nil {
		return nil
	}
	return r.History.RecordIssueDecisions
}

// isBotDecisionComment is the runner's rule for a decision comment: by the
// bot, not a system note, starting with the decisions header.
func isBotDecisionComment(n gitlab.Note, botID int64) bool {
	return botID != 0 && n.Author.ID == botID && !n.System && strings.HasPrefix(n.Body, marker.DecisionsHeader)
}

// decisionRun is the run ID in the first line of a decision comment.
var decisionRun = regexp.MustCompile("^[^\n]* in run `([^`\n]+)`")

// issueDecisions is every decision the issue is holds, with its notes: the
// sections of the bot's decision comments, then the planner decisions in
// the description of an issue the bot created that no comment carries. An
// ID in several comments is the newest comment's (notes come oldest first),
// at the place it was first found. An issue without any has an empty set,
// which clears its stored decisions.
func issueDecisions(p store.ProjectKey, milestoneIID int64, is *gitlab.Issue, notes []gitlab.Note, botID int64) store.IssueDecisions {
	out := store.IssueDecisions{Project: p, MilestoneIID: milestoneIID, IssueIID: is.IID}
	at := map[string]int{} // index in out.Decisions by ID
	add := func(body, runID, url string, posted *time.Time, description bool) {
		for _, d := range marker.ParseDecisions(body) {
			planner := strings.Contains(d.ID, "-P")
			i, seen := at[d.ID]
			if description && (seen || !planner) {
				continue
			}
			row := store.DecisionRow{MilestoneIID: milestoneIID, ID: d.ID, IssueIID: is.IID, Kind: store.DecisionImplementer,
				Title: d.Title, Question: d.Question, Choice: d.Choice, Why: d.Why, Alternatives: d.Alternatives,
				Supersedes: d.Supersedes, Superseded: d.Superseded, RunID: runID, URL: url,
				IssueTitle: is.Title, IssueState: is.State}
			if planner {
				row.Kind = store.DecisionPlanner
			}
			if j := strings.LastIndex(d.ID, "-"); j >= 0 {
				row.Seq, _ = strconv.Atoi(strings.TrimPrefix(d.ID[j+1:], "P"))
			}
			if posted != nil {
				row.PostedAt = *posted
			}
			if seen {
				out.Decisions[i] = row
				continue
			}
			at[d.ID] = len(out.Decisions)
			out.Decisions = append(out.Decisions, row)
		}
	}
	for _, n := range notes {
		if !isBotDecisionComment(n, botID) {
			continue
		}
		var runID string
		if m := decisionRun.FindStringSubmatch(n.Body); m != nil {
			runID = m[1]
		}
		add(n.Body, runID, fmt.Sprintf("%s#note_%d", is.WebURL, n.ID), n.CreatedAt, false)
	}
	if botID != 0 && is.Author.ID == botID {
		add(is.Description, "", is.WebURL, is.CreatedAt, true)
	}
	return out
}
