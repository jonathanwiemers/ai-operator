package runner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// Limits for what a CI fix prompt contains: enough to see the failure,
// little enough to keep the run cheap.
const (
	maxCIJobs        = 3
	maxCILogLines    = 150
	maxCILogBytes    = 8000
	ciExcerptElision = "[… earlier output omitted …]"
)

var (
	ansiEscape     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	sectionMarkers = regexp.MustCompile(`section_(start|end):\d+:[^\s]*`)
)

// ciExcerpt cleans a job log (ANSI colours, GitLab section markers, progress
// lines overwritten with \r) and keeps its end, where failures are reported.
func ciExcerpt(trace string) string {
	trace = ansiEscape.ReplaceAllString(trace, "")
	trace = sectionMarkers.ReplaceAllString(trace, "")
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(trace, "\r\n", "\n"), "\n") {
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	elided := false
	if len(lines) > maxCILogLines {
		lines, elided = lines[len(lines)-maxCILogLines:], true
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxCILogBytes {
		out, elided = out[len(out)-maxCILogBytes:], true
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	if elided {
		out = ciExcerptElision + "\n" + out
	}
	return out
}

// failedJobs returns the failed jobs of a pipeline that actually fail it.
func failedJobs(ctx context.Context, gl gitlab.API, glProject string, pipelineID int64) ([]gitlab.Job, error) {
	jobs, err := gl.ListPipelineJobs(ctx, glProject, pipelineID, "failed")
	if err != nil {
		return nil, err
	}
	var out []gitlab.Job
	for _, j := range jobs {
		if !j.AllowFailure {
			out = append(out, j)
		}
	}
	return out, nil
}

// buildCIPrompt asks the implementer to fix a red pipeline. The jobs and
// their logs come from CI and are therefore given as untrusted data.
func buildCIPrompt(ctx context.Context, gl gitlab.API, glProject string, pipelineID int64) (string, error) {
	jobs, err := failedJobs(ctx, gl, glProject, pipelineID)
	if err != nil {
		return "", fmt.Errorf("listing failed jobs of pipeline %d: %w", pipelineID, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The CI pipeline for your last push failed (pipeline %d). Find and fix the cause, then run the relevant checks locally.\n", pipelineID)
	if len(jobs) == 0 {
		b.WriteString("\nNo failed job details are available; check the build and tests yourself.\n")
	}
	b.WriteString(marker.UntrustedData)
	for i, j := range jobs {
		if i == maxCIJobs {
			fmt.Fprintf(&b, "\n%d more failed jobs are not shown.\n", len(jobs)-maxCIJobs)
			break
		}
		trace, err := gl.JobTrace(ctx, glProject, j.ID)
		if err != nil {
			trace = fmt.Sprintf("(log not available: %v)", err)
		}
		fmt.Fprintf(&b, "\n### Job `%s` (stage %s)\n\n```text\n%s\n```\n", j.Name, j.Stage, strings.ReplaceAll(ciExcerpt(trace), "`", "'"))
	}
	return untrustedData(b.String()), nil
}
