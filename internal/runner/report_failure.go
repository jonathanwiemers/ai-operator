package runner

import (
	"context"
	"fmt"
	"os"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// failureText is a run's error for a GitLab comment. It quotes CLIs and git,
// so: without the runner's tokens, the process's credentials and known
// tokens (processSecrets), and without fence breakout, quick actions or
// mentions.
func (r *Runner) failureText(err error) string {
	msg := textutil.Redact(err.Error(), append(processSecrets(), r.GitLabToken, r.CompanionToken)...)
	return notify.Defuse(strings.ReplaceAll(textutil.Truncate(msg, 3000), "`", "'"))
}

// processSecrets are the credentials in the process's environment
// (textutil.EnvSecrets): the GitLab tokens and the Cursor key in the runner
// container, everything in development mode, where the agent runs inside.
func processSecrets() []string { return textutil.EnvSecrets(os.Environ()) }

// reportFailure tells the issue why its run failed. A run that failed for an
// outside reason (block) leaves the labels to the operator, which blocks the
// issue; any other moves it to needs-input.
func (r *Runner) reportFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, runErr error, block aiv1.BlockReason) {
	wf := spec.Workflow
	body := fmt.Sprintf("🤖 The agent run failed:\n\n```\n%s\n```\n\n", r.failureText(runErr))
	// The operator retries an infra failure once per run request.
	retry := block == aiv1.BlockInfra && task.Spec.Source.InfraRetry != task.Spec.RunRequest
	switch {
	case retry:
		body += fmt.Sprintf("The cause is outside the issue, so it is blocked (`%sinfra`) and the operator starts one new run at %s UTC (`lifecycle.infraRetryAfter`). To try earlier, ",
			wf.BlockedReasonPrefix, utcMinute(r.now().Add(spec.Lifecycle.InfraRetryAfter.Duration)))
	case block == aiv1.BlockInfra:
		body += fmt.Sprintf("This was the automatic retry, so the issue stays blocked (`%sinfra`) and waits for you. Once the cause is fixed, ", wf.BlockedReasonPrefix)
	case block != "":
		body += fmt.Sprintf("The issue is blocked (`%s%s`) and waits for you. To try again, ", wf.BlockedReasonPrefix, block)
	default:
		body += "To try again, "
	}
	body += fmt.Sprintf("%s (or move the issue back to `%s`).", r.steer(spec, task, fmt.Sprintf("comment `%s retry`", wf.CommandPrefix), "ask for a new run"), wf.ReadyLabel)
	// On autopilot the pause on the tracking issue mentions the owners; a
	// blocked issue does not pause it, and pings once it waits for a human.
	if block != "" && !retry && spec.Notify.Wants(aiv1.NotifyBlocked) || block == "" && spec.Notify.Wants(aiv1.NotifyFailed) && !task.Spec.Autopilot {
		body += notify.Mention(task.Spec.Owners)
	}
	if saved := r.run.checkpointText(); saved != "" {
		body += "\n\n" + saved
	}
	body += r.run.markdown(out.CostUSD)
	note, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body)
	if err != nil {
		r.log().Error(err, "posting failure note")
	}
	// Every failed run records its cause, also a blocked one, so a hold
	// names it. The cause goes before the label, so the operator never sees
	// the label without it; the final patch writes it again.
	out.NeedsInputCause, out.NeedsInputURL = aiv1.NeedsInputRunFailed, noteURL(task.Spec.Issue.WebURL, note)
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.NeedsInputCause, s.NeedsInputURL = out.NeedsInputCause, out.NeedsInputURL
	}); err != nil {
		r.log().Error(err, "recording the needs-input cause")
	}
	if block != "" {
		return
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, task.Spec.Issue.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.NeedsInputLabel}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		r.log().Error(err, "updating labels after failure")
	}
}

// noteURL links to note under the web URL of its issue or merge request;
// empty without either.
func noteURL(webURL string, note *gitlab.Note) string {
	if webURL == "" || note == nil || note.ID == 0 {
		return ""
	}
	return fmt.Sprintf("%s#note_%d", webURL, note.ID)
}

// reportSpecFailure tells the milestone's owners on its autopilot tracking
// issue why planning failed. The operator plans again once after an outside
// failure (block); milestones without a tracking issue only log it.
func (r *Runner) reportSpecFailure(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, runErr error, block aiv1.BlockReason) {
	ap := task.Status.Autopilot
	if ap == nil || ap.TrackingIssueIID == 0 {
		return
	}
	body := fmt.Sprintf("🤖 Planning failed:\n\n```\n%s\n```\n\n", r.failureText(runErr))
	switch {
	case block == aiv1.BlockInfra && task.Spec.Source.InfraRetry != task.Spec.RunRequest:
		body += fmt.Sprintf("The cause is outside the plan, so the operator plans again at %s UTC (`lifecycle.infraRetryAfter`), once.",
			utcMinute(r.now().Add(spec.Lifecycle.InfraRetryAfter.Duration)))
	case block == aiv1.BlockInfra:
		body += "This was the automatic retry. Once the cause is fixed, change the milestone's spec to plan again."
	default:
		body += "To plan again, change the milestone's spec."
	}
	// No autopilot pause covers planning: the owners hear of it here.
	if spec.Notify.Wants(aiv1.NotifyFailed) {
		body += notify.Mention(task.Spec.Owners)
	}
	body += r.run.markdown(out.CostUSD)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, ap.TrackingIssueIID, body); err != nil {
		r.log().Error(err, "posting planning failure note", "issue", ap.TrackingIssueIID)
	}
}
