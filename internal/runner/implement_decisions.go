package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// postDecisions posts the run's decisions on the issue, numbered after the
// highest decision ID there, and marks the revisited decisions that a new
// decision supersedes. Like the notes it is best effort and never fails the
// run; it returns notices for the run's report.
func (r *Runner) postDecisions(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, notes []gitlab.Note, botID int64, prefix string, ds []Decision, revisits []revisit) []string {
	numbered := numberDecisions(ds, issue.IID, false, nextDecisionNumber(issue, notes, botID))
	if len(numbered) == 0 && len(revisits) == 0 {
		return nil
	}
	s := r.step(ctx, "post decisions", "decisions", len(numbered), "revisits", len(revisits))
	if len(numbered) > 0 {
		for _, body := range renderDecisionsComments(r.run.id, prefix, numbered) {
			if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, body); err != nil {
				_ = s.fail(fmt.Errorf("posting decisions: %w", err))
				return nil
			}
		}
	}

	// A revisited decision is only marked when a new decision replaces it.
	// All marks on one comment (or the description) go into one update.
	var notices []string
	bodies := map[int64]string{}
	var planned []revisit
	marked := 0
	for _, rv := range revisits {
		by := ""
		for _, d := range numbered {
			if strings.EqualFold(strings.TrimSpace(d.Supersedes), rv.ID) {
				by = d.ID
				break
			}
		}
		switch {
		case rv.Decision == "":
		case by == "":
			notices = append(notices, fmt.Sprintf("Decision %s was revisited, but no new decision says it replaces it, so it is not marked superseded.", rv.ID))
		case rv.Note != nil:
			body, ok := bodies[rv.Note.ID]
			if !ok {
				body = rv.Note.Body
			}
			bodies[rv.Note.ID] = markSuperseded(body, rv.ID, by, r.run.id)
			marked++
		default:
			rv.Instruction = by // the successor, for the description below
			planned = append(planned, rv)
			marked++
		}
	}
	for id, body := range bodies {
		if _, err := gl.UpdateIssueNote(ctx, glProject, issue.IID, id, body); err != nil {
			_ = s.fail(fmt.Errorf("marking decisions superseded: %w", err))
			return notices
		}
	}
	if len(planned) > 0 {
		// Planning decisions of older issues live in the description, which
		// a human may have edited during the run: mark them on the current
		// version.
		fresh, err := gl.GetIssue(ctx, glProject, issue.IID)
		if err != nil {
			_ = s.fail(fmt.Errorf("reading the issue description: %w", err))
			return notices
		}
		desc := fresh.Description
		for _, rv := range planned {
			desc = markSuperseded(desc, rv.ID, rv.Instruction, r.run.id)
		}
		if desc != fresh.Description {
			if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{Description: desc}); err != nil {
				_ = s.fail(fmt.Errorf("marking planning decisions superseded: %w", err))
				return notices
			}
		}
	}
	// A follow-up issue (created by the bot) revisits a decision of a merged
	// issue: the new decision supersedes that one, on the other issue.
	if m := marker.RevisitOf.FindStringSubmatch(issue.Description); m != nil && botID != 0 && issue.Author.ID == botID {
		for _, d := range numbered {
			if !strings.EqualFold(strings.TrimSpace(d.Supersedes), m[1]) {
				continue
			}
			if err := r.supersedeElsewhere(ctx, gl, glProject, botID, m[1], d.ID); err != nil {
				_ = s.fail(err)
				return notices
			}
			marked++
			break
		}
	}
	result := "none posted"
	if len(numbered) > 0 {
		result = fmt.Sprintf("%s to %s posted", numbered[0].ID, numbered[len(numbered)-1].ID)
	}
	if marked > 0 {
		result += fmt.Sprintf(", %d superseded", marked)
	}
	s.done(result)
	return notices
}

// supersedeElsewhere marks decision id on its own issue as superseded by the
// decision by of this run: in the bot's decision comment that holds it, or in
// the description for planning decisions of older issues.
func (r *Runner) supersedeElsewhere(ctx context.Context, gl gitlab.API, glProject string, botID int64, id, by string) error {
	iid := marker.DecisionIssue(id)
	notes, err := gl.ListIssueNotes(ctx, glProject, iid)
	if err != nil {
		return fmt.Errorf("reading #%d to mark %s superseded: %w", iid, id, err)
	}
	for _, n := range notes {
		if !isDecisionComment(n, botID) || decisionSection(n.Body, id) == "" {
			continue
		}
		if body := markSuperseded(n.Body, id, by, r.run.id); body != n.Body {
			if _, err := gl.UpdateIssueNote(ctx, glProject, iid, n.ID, body); err != nil {
				return fmt.Errorf("marking %s superseded on #%d: %w", id, iid, err)
			}
		}
		return nil
	}
	if !strings.Contains(id, "-P") {
		return nil
	}
	target, err := gl.GetIssue(ctx, glProject, iid)
	if err != nil {
		return fmt.Errorf("reading #%d to mark %s superseded: %w", iid, id, err)
	}
	if desc := markSuperseded(target.Description, id, by, r.run.id); desc != target.Description {
		if _, err := gl.UpdateIssue(ctx, glProject, iid, gitlab.UpdateIssueOptions{Description: desc}); err != nil {
			return fmt.Errorf("marking %s superseded on #%d: %w", id, iid, err)
		}
	}
	return nil
}

// steer is how an owner steers task, for a report: viaComment for a project
// that takes GitLab input, else what to do in the Desk, with the task's link.
func (r *Runner) steer(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, viaComment, inDesk string) string {
	if spec.Workflow.Takes(aiv1.InputGitLab) {
		return viaComment
	}
	if link := desklink.Task(r.DeskURL, task.Namespace, task.Spec.ProjectRef, task.Name); link != "" {
		return inDesk + " in the Desk: " + link
	}
	return inDesk + " in the Desk"
}

// emptySummary stands in for a summary the agent did not write.
func emptySummary(pushed bool, sha string) string {
	if pushed {
		return fmt.Sprintf("The agent left no summary. This run pushed its changes (up to `%s`); the diff shows them.", shortSHA(sha))
	}
	return "The agent left no summary and changed nothing in this run."
}

func newerThan(notes []gitlab.Note, id int64) []gitlab.Note {
	var out []gitlab.Note
	for _, n := range notes {
		if n.ID > id {
			out = append(out, n)
		}
	}
	return out
}

func maxNoteID(notes []gitlab.Note, cur int64) int64 {
	for _, n := range notes {
		if n.ID > cur {
			cur = n.ID
		}
	}
	return cur
}

// codeList renders file names as inline code.
func codeList(files []string) string {
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = "`" + strings.ReplaceAll(f, "`", "'") + "`"
	}
	return strings.Join(parts, ", ")
}

// humanNotes counts the notes written by humans.
func humanNotes(notes []gitlab.Note, botID int64) int {
	n := 0
	for _, note := range notes {
		if !note.System && (botID == 0 || note.Author.ID != botID) {
			n++
		}
	}
	return n
}

// ownNotes keeps the bot's own notes (reports, decisions, plan decisions).
func ownNotes(notes []gitlab.Note, botID int64) []gitlab.Note {
	return slices.DeleteFunc(notes, func(n gitlab.Note) bool { return n.System || botID == 0 || n.Author.ID != botID })
}
