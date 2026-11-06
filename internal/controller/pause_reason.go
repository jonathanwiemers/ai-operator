package controller

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxReasonQuote bounds, in characters, the question or error a needs-input
// reason quotes; the link has the rest.
const maxReasonQuote = 200

// needsInputReason is "#N needs input: <why>" for issue iid from the cause
// recorded next to its needs-input label (status.run.needsInputCause). It
// holds only what stays the same while the cause does, no times relative to
// now and no counters, so a pause over it comments once. False without a
// cause (a run of an older release): the caller keeps its generic text. The
// quoted question or error is the agent's: the caller defuses it.
func needsInputReason(iid int64, task *aiv1.AgentTask) (string, bool) {
	if task == nil {
		return "", false
	}
	run := task.Status.Run
	var why string
	switch run.NeedsInputCause {
	case aiv1.NeedsInputCIRed:
		var jobs []string
		url := ""
		if ci := task.Status.CI; ci != nil {
			jobs, url = slices.Sorted(slices.Values(ci.FailedJobs)), ci.PipelineURL
		}
		why = "CI is red after the fix rounds: failing jobs " + jobList(jobs) + linked(url)
	case aiv1.NeedsInputRunFailed:
		why = "the run failed: " + runError(run) + linked(run.NeedsInputURL)
	case aiv1.NeedsInputAgentQuestion:
		q := run.Summary
		if len(run.Questions) > 0 {
			q = run.Questions[0].Question
		}
		why = "the agent asks: " + oneLine(q, maxReasonQuote) + linked(run.NeedsInputURL)
	default:
		return "", false
	}
	return fmt.Sprintf("#%d needs input: %s", iid, why), true
}

// runError is a failed run's error for a reason: its engine and reset time
// for a usage limit, else the first line of the summary.
func runError(run aiv1.RunStatus) string {
	for _, l := range run.UsageLimits {
		if !strings.Contains(run.Summary, string(l.Engine)+" usage limit") {
			continue
		}
		if l.Until.IsZero() {
			return fmt.Sprintf("%s usage limit", l.Engine)
		}
		// The day of the run, not of the poll: the reason must not change at
		// midnight.
		day := l.Until.Time
		if run.LastActivityTime != nil {
			day = run.LastActivityTime.Time
		}
		return fmt.Sprintf("%s usage limit, resets %s", l.Engine, resetTime(l.Until.Time, day))
	}
	return oneLine(run.Summary, maxReasonQuote)
}

// resetTime is t as "15:04 UTC", with the date when it is not on day's date.
func resetTime(t, day time.Time) string {
	t, day = t.UTC(), day.UTC()
	if t.Format(time.DateOnly) == day.Format(time.DateOnly) {
		return t.Format("15:04 UTC")
	}
	return t.Format("2006-01-02 15:04 UTC")
}

// oneLine is the first line of s with its whitespace collapsed, at most n
// characters; "…" marks a cut or lines left out.
func oneLine(s string, n int) string {
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

// linked is " (url)", or nothing without a URL.
func linked(url string) string {
	if url == "" {
		return ""
	}
	return " (" + url + ")"
}
