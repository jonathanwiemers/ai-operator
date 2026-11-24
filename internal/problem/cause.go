package problem

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxCauseQuote bounds, in characters, the question or error a cause quotes.
const maxCauseQuote = 200

// Cause is one line naming why the task's issue needs input, from
// status.run.needsInputCause, without a link. Empty without a cause (a run
// of an older release). The quoted question or error is the agent's.
func Cause(task *aiv1.AgentTask) string {
	if task == nil {
		return ""
	}
	run := task.Status.Run
	switch run.NeedsInputCause {
	case aiv1.NeedsInputCIRed:
		var jobs []string
		if ci := task.Status.CI; ci != nil {
			jobs = slices.Sorted(slices.Values(ci.FailedJobs))
		}
		return "CI is red after the fix rounds: failing jobs " + JobList(jobs)
	case aiv1.NeedsInputRunFailed:
		return "the run failed: " + runError(run)
	case aiv1.NeedsInputAgentQuestion:
		q := run.Summary
		if len(run.Questions) > 0 {
			q = run.Questions[0].Question
		}
		return "the agent asks: " + OneLine(q, maxCauseQuote)
	case aiv1.NeedsInputNothingPushed:
		return "the run pushed no commits to the merge request"
	}
	return ""
}

// runError is a failed run's error: its engine and reset time for a usage
// limit, else the first line of the summary.
func runError(run aiv1.RunStatus) string {
	for _, l := range run.UsageLimits {
		if !strings.Contains(run.Summary, string(l.Engine)+" usage limit") {
			continue
		}
		if l.Until.IsZero() {
			return fmt.Sprintf("%s usage limit", l.Engine)
		}
		// The day of the run, not of the poll: the text must not change at
		// midnight.
		day := l.Until.Time
		if run.LastActivityTime != nil {
			day = run.LastActivityTime.Time
		}
		return fmt.Sprintf("%s usage limit, resets %s", l.Engine, ResetTime(l.Until.Time, day))
	}
	return OneLine(run.Summary, maxCauseQuote)
}

// ResetTime is t as "15:04 UTC", with the date when it is not on day's date.
func ResetTime(t, day time.Time) string {
	t, day = t.UTC(), day.UTC()
	if t.Format(time.DateOnly) == day.Format(time.DateOnly) {
		return t.Format("15:04 UTC")
	}
	return t.Format("2006-01-02 15:04 UTC")
}

// OneLine is the first line of s with its whitespace collapsed, at most n
// characters; "…" marks a cut or lines left out.
func OneLine(s string, n int) string {
	first, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line := strings.Join(strings.Fields(first), " ")
	cut := strings.TrimSpace(rest) != ""
	if utf8.RuneCountInString(line) > n || cut && utf8.RuneCountInString(line) >= n {
		line, cut = strings.TrimRight(string([]rune(line)[:n-1]), " "), true
	}
	if cut {
		line += "…"
	}
	return line
}

// JobList is jobs in backticks, comma-separated, or "(none reported)".
func JobList(jobs []string) string {
	if len(jobs) == 0 {
		return "(none reported)"
	}
	quoted := make([]string, len(jobs))
	for i, j := range jobs {
		quoted[i] = "`" + j + "`"
	}
	return strings.Join(quoted, ", ")
}
