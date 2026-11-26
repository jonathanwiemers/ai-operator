package runner

import (
	"context"

	"github.com/jonathanwiemers/ai-operator/internal/attachments"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// runAttachments is what the steps of a run need to hand over the files
// their comments link: the project whose uploads count and the run's links,
// which the folder keeps after a step's own.
type runAttachments struct {
	proj  attachments.Project
	links []attachments.Link
}

// addAttachments hands the uploads linked in the implementer's prompts to
// the agent and adds the Attachments section to both: the full prompt lists
// the links of the comments, the issue and the milestone, the resumed one
// those of its new comments. One fetch fills the folder for both, as a
// resumed call falls back to the full prompt when its session is gone; the
// new comments are the newest, so the caps keep their files first. The notes
// are the ones the prompts show. Without links neither prompt changes and the
// folder of an earlier run is removed.
func (run *implementRun) addAttachments(ctx context.Context, full, resumed string, issueNotes, mrNotes, newIssueNotes, newMRNotes []gitlab.Note) (string, string) {
	proj := planAttachmentProject(run.proj)
	links := implementAttachmentLinks(proj, run.issue, run.milestone, issueNotes, mrNotes)
	run.attachments = runAttachments{proj: proj, links: links}
	res := run.r.fetchAttachments(ctx, run.gl, run.glProject, links)
	if len(res) == 0 {
		return full, resumed
	}
	full = withSection(full, renderAttachments("The files linked in the comments, the issue and the milestone, newest first.", res))
	newLinks := implementAttachmentLinks(proj, nil, nil, newIssueNotes, newMRNotes)
	resumed = withSection(resumed, renderAttachments("The files linked in the new comments, newest first.", attachmentsOf(res, newLinks)))
	return full, resumed
}

// stepAttachments hands over the files a step's comments link, ahead of the
// run's, and is the step's section listing the step's links with intro. When
// the comments link nothing it is "", and the folder stays as the run left
// it: it holds what the full prompt lists.
func (r *Runner) stepAttachments(ctx context.Context, dl attachments.Downloader, project string, atts runAttachments, intro string, issueNotes, mrNotes []gitlab.Note) string {
	links := implementAttachmentLinks(atts.proj, nil, nil, issueNotes, mrNotes)
	if len(links) == 0 {
		return ""
	}
	res := r.fetchAttachments(ctx, dl, project, joinLinks(links, atts.links))
	return renderAttachments(intro, attachmentsOf(res, links))
}
