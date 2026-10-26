package controller

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// Labels and markers of the issues the main health watcher opens.
const (
	mainRedLabel  = "ci::main-red"
	flakyLabel    = "ci::flaky"
	mainRedMarker = "<!-- ai-operator:main-red -->"
	// How much of the failed jobs and their logs the issue shows; GitLab
	// refuses titles over 255 characters.
	mainRedLogLines, mainRedLogBytes, mainRedJobs = 40, 4000, 3
)

// syncMainHealth checks the newest finished pipeline of the default branch
// every interval; jobs are listed only when it changed:
//
//	green                         -> green (closes the main-red issue)
//	red, new pipeline             -> retrying: its failed jobs are retried
//	green after the retry         -> flaky: one issue per job
//	red after the retry           -> red: the main-red issue, the owners pinged
//
// The status changes only when every step succeeded, so a failed one is
// repeated on the next check. With autoFixFlaky, a flaky issue goes to the
// agent at its second occurrence, into the milestone of lane (nil: none).
func (r *AgentProjectReconciler) syncMainHealth(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, lane *aiv1.AgentTask) error {
	if spec.MainHealth.Disabled {
		project.Status.MainHealth = nil
		return nil
	}
	cur := project.Status.MainHealth
	if cur == nil {
		cur = &aiv1.MainHealthStatus{State: aiv1.MainHealthGreen}
		project.Status.MainHealth = cur
	}
	now := metav1.NewTime(r.now())
	if cur.CheckedAt != nil && now.Sub(cur.CheckedAt.Time) < spec.MainHealth.Interval.Duration {
		return nil
	}
	cur.CheckedAt = &now
	st := cur.DeepCopy()
	if err := r.checkMainHealth(ctx, gl, spec, project.Status.BotUserID, st); err != nil {
		return err
	}
	if spec.MainHealth.AutoFixFlaky && cur.State == aiv1.MainHealthRetrying && st.State == aiv1.MainHealthFlaky {
		if err := r.autoFixFlaky(ctx, gl, spec, project.Status.BotUserID, st, lane); err != nil {
			return err
		}
	}
	project.Status.MainHealth = st
	if !spec.MainHealth.AutoFix || st.IssueIID == 0 || st.AutoFixIssueIID != 0 {
		return nil
	}
	return r.autoFix(ctx, gl, spec, st)
}

// autoFix hands the main-red issue to the agent (mainHealth.autoFix), once
// per red episode: also when switched on while red, not when a human
// already labelled or closed it, nor an older issue whose logs are not
// marked as untrusted data.
func (r *AgentProjectReconciler) autoFix(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.MainHealthStatus) error {
	wf := spec.Workflow
	is, err := gl.GetIssue(ctx, spec.GitLab.Project, st.IssueIID)
	if err != nil {
		return fmt.Errorf("getting issue #%d: %w", st.IssueIID, err)
	}
	if is.State == "opened" && !hasAny(*is, wf.ActiveLabels()...) && strings.Contains(is.Description, marker.UntrustedData) {
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: []string{wf.ReadyLabel}}); err != nil {
			return fmt.Errorf("moving issue #%d to %q: %w", is.IID, wf.ReadyLabel, err)
		}
	}
	st.AutoFixIssueIID = is.IID
	return nil
}

// freshMainHealth returns the main health if the watcher checked it within
// the given number of intervals; nil if it is disabled or older.
func (r *AgentProjectReconciler) freshMainHealth(spec aiv1.AgentProjectSpec, st *aiv1.MainHealthStatus, intervals time.Duration) *aiv1.MainHealthStatus {
	if spec.MainHealth.Disabled || st == nil || st.CheckedAt == nil || r.now().Sub(st.CheckedAt.Time) > intervals*spec.MainHealth.Interval.Duration {
		return nil
	}
	return st
}

func (r *AgentProjectReconciler) checkMainHealth(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, bot int64, st *aiv1.MainHealthStatus) error {
	mh, glProject := spec.MainHealth, spec.GitLab.Project
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil || proj.DefaultBranch == "" {
		return err
	}
	// Skipped and canceled pipelines say nothing about the branch: the
	// newest one that does counts.
	ps, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{Ref: proj.DefaultBranch, Scope: "finished", Limit: 10})
	i := slices.IndexFunc(ps, func(p gitlab.Pipeline) bool {
		return p.Status == "success" || p.Status == "manual" || p.Status == "failed"
	})
	if err != nil || i < 0 {
		return err
	}
	// While a retry runs, the newest finished pipeline is an older one.
	p, same := ps[i], ps[i].ID == st.PipelineID
	red := p.Status == "failed"
	if p.ID < st.PipelineID || same && st.State != aiv1.MainHealthRetrying && red == (st.State == aiv1.MainHealthRed) {
		return nil
	}
	setState := func(s aiv1.MainHealthState) {
		if st.State != s {
			st.State, st.Since = s, st.CheckedAt
		}
	}
	retried := same && st.State == aiv1.MainHealthRetrying
	retriedIDs := st.RetriedJobIDs
	st.PipelineID, st.SHA, st.RetriedJobIDs, st.Flaky = p.ID, p.SHA, nil, nil
	if !red {
		if retried && *mh.Issue {
			if st.Flaky, err = reportFlaky(ctx, gl, glProject, bot, p, st.FailedJobs, retriedIDs); err != nil {
				return err
			}
		}
		if st.IssueIID != 0 && *mh.Issue {
			note := fmt.Sprintf("✅ green again at `%s` (%s).", p.SHA, p.WebURL)
			if st.AutoFixIssueIID == st.IssueIID {
				note += " A merge request of the agent that is still open is left for a human to merge or close."
			}
			if _, err := gl.CreateIssueNote(ctx, glProject, st.IssueIID, note); err != nil {
				return fmt.Errorf("commenting on issue #%d: %w", st.IssueIID, err)
			}
			if _, err := gl.UpdateIssue(ctx, glProject, st.IssueIID, gitlab.UpdateIssueOptions{StateEvent: "close"}); err != nil {
				return fmt.Errorf("closing issue #%d: %w", st.IssueIID, err)
			}
		}
		st.IssueIID, st.ReportedJobs, st.AutoFixIssueIID = 0, nil, 0
		if retried {
			setState(aiv1.MainHealthFlaky)
		} else {
			setState(aiv1.MainHealthGreen)
			st.FailedJobs = nil
		}
		return nil
	}

	jobs, err := gl.ListPipelineJobs(ctx, glProject, p.ID, "failed")
	if err != nil {
		return fmt.Errorf("listing failed jobs of pipeline %d: %w", p.ID, err)
	}
	jobs = slices.DeleteFunc(jobs, func(j gitlab.Job) bool { return j.AllowFailure })
	st.FailedJobs = nil
	for _, j := range jobs {
		st.FailedJobs = append(st.FailedJobs, jobName(j.Name))
		st.RetriedJobIDs = append(st.RetriedJobIDs, j.ID)
	}
	if !same && *mh.RetryFailedJobs {
		log.FromContext(ctx).Info("default branch is red, retrying its failed jobs once", "pipeline", p.ID, "jobs", st.FailedJobs)
		if _, err = gl.RetryPipeline(ctx, glProject, p.ID); err == nil {
			setState(aiv1.MainHealthRetrying)
			st.RetriedPipelineID = p.ID
			return nil
		}
		log.FromContext(ctx).Info("could not retry the default branch's pipeline", "pipeline", p.ID, "error", err.Error())
	}
	st.RetriedJobIDs = nil
	if *mh.Issue && (st.IssueIID == 0 || !slices.Equal(st.ReportedJobs, st.FailedJobs)) {
		if err := r.reportMainRed(ctx, gl, spec, bot, proj.DefaultBranch, p, jobs, st); err != nil {
			return err
		}
	}
	setState(aiv1.MainHealthRed)
	return nil
}

// reportMainRed opens the main-red issue, or comments on it when the
// failing jobs changed. Only a new issue pings the owners.
func (r *AgentProjectReconciler) reportMainRed(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, bot int64, branch string, p gitlab.Pipeline, jobs []gitlab.Job, st *aiv1.MainHealthStatus) error {
	glProject := spec.GitLab.Project
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n🚑 `%s` is red: pipeline %s at `%s`, failing jobs %s.\n", mainRedMarker, branch, p.WebURL, p.SHA, someJobs(st.FailedJobs))
	fmt.Fprintf(&b, "\nMake `%s` green again: find and fix the cause of the failing jobs. If a test fails only sometimes, stabilise it or quarantine it; do not delete it.\n\n%s\n", branch, marker.UntrustedData)
	for _, j := range jobs[:min(len(jobs), mainRedJobs)] {
		trace, err := gl.JobTrace(ctx, glProject, j.ID)
		if err != nil {
			trace = fmt.Sprintf("(log not available: %v)", err)
		}
		fmt.Fprintf(&b, "\n#### `%s`\n\n```text\n%s\n```\n", jobName(j.Name), logTail(trace))
	}
	title := fmt.Sprintf("🚑 `%s` is red: %s", branch, someJobs(st.FailedJobs))
	if st.IssueIID == 0 {
		if is, err := findIssue(ctx, gl, glProject, bot, mainRedLabel, mainRedMarker); err != nil {
			return err
		} else if is != nil {
			st.IssueIID = is.IID
		}
	}
	if st.IssueIID != 0 {
		if _, err := gl.UpdateIssue(ctx, glProject, st.IssueIID, gitlab.UpdateIssueOptions{Title: title}); err != nil {
			return fmt.Errorf("updating issue #%d: %w", st.IssueIID, err)
		}
		if _, err := gl.CreateIssueNote(ctx, glProject, st.IssueIID, b.String()); err != nil {
			return fmt.Errorf("commenting on issue #%d: %w", st.IssueIID, err)
		}
	} else {
		if spec.Notify.Wants(aiv1.NotifyMainRed) {
			b.WriteString(notify.Mention(notify.Resolve("", spec.Workflow.CommandPrefix, spec.Notify.Users, r.DefaultOwners)))
		}
		is, err := gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{Title: title, Description: b.String(), Labels: "ai-generated," + mainRedLabel})
		if err != nil {
			return fmt.Errorf("creating the main-red issue: %w", err)
		}
		st.IssueIID = is.IID
	}
	st.ReportedJobs = st.FailedJobs
	return nil
}

// reportFlaky records the jobs that passed on retry: an occurrence comment
// on each job's flaky issue, opened on its first occurrence with the
// one-liner only. The comment names the failing tests and quotes the tail
// of the failed attempt's log (ids, in the order of jobs), both as
// untrusted data. A large matrix reports its first jobs only.
func reportFlaky(ctx context.Context, gl gitlab.API, glProject string, bot int64, p gitlab.Pipeline, jobs []string, ids []int64) ([]aiv1.FlakyJobReport, error) {
	list, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Labels: []string{flakyLabel}})
	if err != nil {
		return nil, fmt.Errorf("listing issues with label %q: %w", flakyLabel, err)
	}
	var out []aiv1.FlakyJobReport
	for i, j := range jobs[:min(len(jobs), mainRedJobs)] {
		line := fmt.Sprintf("🔁 `%s` failed in pipeline %s at `%s` and passed on retry.", j, p.WebURL, p.SHA)
		trace := "(the log of the failed attempt was not recorded)"
		if i < len(ids) {
			if trace, err = gl.JobTrace(ctx, glProject, ids[i]); err != nil {
				trace = fmt.Sprintf("(log not available: %v)", err)
			}
		}
		rep := aiv1.FlakyJobReport{Job: j, Tests: failingTests(trace), LogTail: logTail(trace)}
		m := flakyMarker(j)
		is := ownIssue(list, bot, m)
		if is == nil {
			if is, err = gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{Title: "🔁 Flaky CI job: `" + j + "`", Description: m + "\n" + line, Labels: "ai-generated," + flakyLabel}); err != nil {
				return nil, fmt.Errorf("reporting flaky job %q: %w", j, err)
			}
			// Two job names cut to the same one share it.
			list = append(list, *is)
		}
		rep.IssueIID = is.IID
		// A check that failed after the comment repeats it: once per
		// pipeline.
		notes, err := gl.ListIssueNotes(ctx, glProject, is.IID)
		if err != nil {
			return nil, fmt.Errorf("listing the comments of issue #%d: %w", is.IID, err)
		}
		if !slices.Contains(flakyOccurrences(notes, bot), p.ID) {
			if _, err = gl.CreateIssueNote(ctx, glProject, is.IID, flakyOccurrence(p.ID, line, rep)); err != nil {
				return nil, fmt.Errorf("reporting flaky job %q on issue #%d: %w", j, is.IID, err)
			}
		}
		out = append(out, rep)
	}
	return out, nil
}

// flakyMarker starts the description of a job's flaky issue.
func flakyMarker(job string) string { return "<!-- ai-operator:flaky-job=" + job + " -->" }

// flakyOccurrenceMarker starts an occurrence comment, with its pipeline.
const flakyOccurrenceMarker = "<!-- ai-operator:flaky-occurrence="

// flakyOccurrences are the pipelines of the bot's occurrence comments
// among notes, each once.
func flakyOccurrences(notes []gitlab.Note, bot int64) []int64 {
	var out []int64
	for _, n := range notes {
		rest, ok := strings.CutPrefix(n.Body, flakyOccurrenceMarker)
		id, _, _ := strings.Cut(rest, " ")
		pid, err := strconv.ParseInt(id, 10, 64)
		if ok && err == nil && n.Author.ID == bot && !slices.Contains(out, pid) {
			out = append(out, pid)
		}
	}
	return out
}

// flakyOccurrence is the comment of one occurrence: the trusted one-liner,
// then the failing tests and the log tail as untrusted data.
func flakyOccurrence(pipeline int64, line string, rep aiv1.FlakyJobReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d -->\n%s\n\n%s\n", flakyOccurrenceMarker, pipeline, line, marker.UntrustedData)
	if len(rep.Tests) > 0 {
		b.WriteString("\nFailing tests in the log of the failed attempt:\n\n")
		for _, t := range rep.Tests {
			fmt.Fprintf(&b, "- `%s`\n", t)
		}
	}
	fmt.Fprintf(&b, "\nEnd of the failed attempt's log:\n\n```text\n%s\n```\n", rep.LogTail)
	return b.String()
}

// failingTests are the failing tests a job log names: nextest's
// "FAIL [2.715s] <binary> <test>", cargo test's "test <name> ... FAILED" and
// vitest's "FAIL <file> > <test>", made safe for code spans.
func failingTests(trace string) []string {
	trace = ansiEscape.ReplaceAllString(trace, "")
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(trace, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(traceTimestamp.ReplaceAllString(l[strings.LastIndex(l, "\r")+1:], ""))
		for _, re := range failingTest {
			m := re.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			name := strings.Join(strings.Fields(testNameChars.ReplaceAllString(m[1], "")), " ")
			name = name[:min(len(name), maxTestName)]
			if name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
			break
		}
		if len(out) == maxTests {
			break
		}
	}
	return out
}

// How many failing tests are kept, and how long each name may be.
const maxTests, maxTestName = 10, 150

var (
	failingTest = []*regexp.Regexp{
		regexp.MustCompile(`^(?:TRY \d+ )?FAIL \[\s*[\d.]+m?s\]\s+(?:\(\s*\d+/\s*\d+\)\s+)?(\S.*)$`),
		regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`),
		regexp.MustCompile(`^FAIL\s+(\S+ > .+)$`),
	}
	testNameChars = regexp.MustCompile(`[^A-Za-z0-9 :._/>()\[\]#,=+-]`)
	// GitLab's optional timestamps in front of every log line.
	traceTimestamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z \d+[OE]\+?\s?`)
)

// findIssue finds the bot's open issue with label whose description starts
// with the marker.
func findIssue(ctx context.Context, gl gitlab.API, glProject string, bot int64, label, m string) (*gitlab.Issue, error) {
	list, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Labels: []string{label}})
	if err != nil {
		return nil, fmt.Errorf("listing issues with label %q: %w", label, err)
	}
	return ownIssue(list, bot, m), nil
}

// ownIssue is the issue of list the bot wrote whose description starts with
// the marker.
func ownIssue(list []gitlab.Issue, bot int64, m string) *gitlab.Issue {
	for _, is := range list {
		if is.Author.ID == bot && strings.HasPrefix(is.Description, m) {
			return &is
		}
	}
	return nil
}

var jobNameChars = regexp.MustCompile(`[^A-Za-z0-9 :._/-]`)

// jobName makes an untrusted job name safe for titles and code spans.
func jobName(name string) string {
	name = jobNameChars.ReplaceAllString(name, "")
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}

// someJobs lists the first jobs only, so a large matrix stays short.
func someJobs(jobs []string) string {
	s := jobList(jobs[:min(len(jobs), mainRedJobs)])
	if n := len(jobs) - mainRedJobs; n > 0 {
		s += fmt.Sprintf(" and %d more", n)
	}
	return s
}

// logTail is the defused end of a job log, without colours and with no
// backticks to end its code block. The bytes are capped too: one line may be
// megabytes long.
func logTail(trace string) string {
	trace = sectionMarker.ReplaceAllString(ansiEscape.ReplaceAllString(trace, ""), "")
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(trace, "\r\n", "\n")), "\n")
	for i, l := range lines {
		// Progress output overwrites a line with \r; a lone \r also ends a
		// line in Markdown, so nothing may follow it.
		lines[i] = l[strings.LastIndex(l, "\r")+1:]
	}
	if len(lines) > mainRedLogLines {
		lines = lines[len(lines)-mainRedLogLines:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > mainRedLogBytes {
		out = strings.ToValidUTF8(out[len(out)-mainRedLogBytes:], "")
	}
	// A copied marker must not count as the bot's.
	out = strings.ReplaceAll(strings.ReplaceAll(out, "`", "'"), "<!-- ai-operator:", "<!-- ai-operator :")
	return notify.Defuse(out)
}

var (
	ansiEscape    = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	sectionMarker = regexp.MustCompile(`section_(start|end):\d+:[^\s]*`)
)
