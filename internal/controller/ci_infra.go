package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// infraLogTail is how much of the end of a failed job's log is searched
// for the runner's signatures.
const infraLogTail = 64 << 10

// infraFailure reports whether a red pipeline failed because of the
// runner, not the code: every failing job (not allow-failure) matches
// spec, on its failure_reason or else on a log pattern in the end of its
// log. It stops at the first job that does not match, so no more logs are
// read; signature names what matched.
func infraFailure(ctx context.Context, gl gitlab.API, spec aiv1.CIInfraSpec, project string, failed []gitlab.Job) (signature string, ok bool, err error) {
	var sigs []string
	for _, j := range failed {
		if j.AllowFailure {
			continue
		}
		sig, err := infraSignature(ctx, gl, spec, project, j)
		if err != nil || sig == "" {
			return "", false, err
		}
		sigs = append(sigs, jobName(j.Name)+": "+sig)
	}
	if len(sigs) == 0 {
		return "", false, nil
	}
	// The first jobs only, so a large matrix stays short.
	sig := strings.Join(sigs[:min(len(sigs), mainRedJobs)], ", ")
	if n := len(sigs) - mainRedJobs; n > 0 {
		sig += fmt.Sprintf(" and %d more", n)
	}
	return sig, true, nil
}

// infraSignature is what marks job j as failed by the runner, or "".
func infraSignature(ctx context.Context, gl gitlab.API, spec aiv1.CIInfraSpec, project string, j gitlab.Job) (string, error) {
	if j.FailureReason != "" && slices.Contains(spec.FailureReasons, j.FailureReason) {
		return j.FailureReason, nil
	}
	if len(spec.LogPatterns) == 0 {
		return "", nil
	}
	trace, err := gl.JobTrace(ctx, project, j.ID)
	if err != nil {
		// Without its log the job counts as the code's: the agent sees it.
		log.FromContext(ctx).Info("could not read a failed job's log, so it does not count as the runner's", "project", project, "job", j.ID, "error", err.Error())
		return "", nil
	}
	tail := strings.ToLower(trace[max(0, len(trace)-infraLogTail):])
	for _, p := range spec.LogPatterns {
		if p != "" && strings.Contains(tail, strings.ToLower(p)) {
			return p, nil
		}
	}
	return "", nil
}

// infraRetryNotes posts the note on the first infra retry of a tree's head
// (open: the status before this poll); later retries are only logged.
func infraRetryNotes(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, open, ci *aiv1.CIStatus, run aiv1.RunStatus) error {
	notes := []string{infraRetryNote(spec, open.Infra, ci.Infra, ci.PipelineURL, "")}
	if cc := ci.Companion; cc != nil {
		var before *aiv1.CIInfraRetry
		if open.Companion != nil {
			before = open.Companion.Infra
		}
		notes = append(notes, infraRetryNote(spec, before, cc.Infra, cc.PipelineURL, " of the companion repository "+run.Companion.Project))
	}
	for _, n := range notes {
		if n == "" {
			continue
		}
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, is.IID, n); err != nil {
			return fmt.Errorf("commenting on issue #%d: %w", is.IID, err)
		}
	}
	return nil
}

// infraRetryNote is the note on the first infra retry of a tree's head
// (of is "" or " of the companion repository …"), or "" unless this poll
// scheduled it (before: the tree's record before the poll).
func infraRetryNote(spec aiv1.AgentProjectSpec, before, now *aiv1.CIInfraRetry, url, of string) string {
	if now == nil || now.DueAt == nil || now.Retries != 0 || before != nil && before.DueAt != nil {
		return ""
	}
	return fmt.Sprintf("🔁 The pipeline %s%s failed because of the runner, not the code (%s). The operator retries the failed jobs in %s; this uses no fix round.",
		url, of, now.Signature, roundDuration(spec.CI.InfraFailures.InfraBackoff(0)))
}

// infraHeldNote is the note when the infra retries of a tree ran out.
func infraHeldNote(in *aiv1.CIInfraRetry, url, of string) string {
	return fmt.Sprintf("🤖 CI%s is still red because of the runner, not the code, after %d retries (%s): %s. Fix the runner and retry the pipeline; the issue moves on by itself once CI is green.",
		of, in.Retries, url, in.Signature)
}
