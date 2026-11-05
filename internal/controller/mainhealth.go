package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	// A pipeline canceled again after its retry; its issue freezes nothing.
	mainCanceledLabel  = "ci::main-canceled"
	mainCanceledMarker = "<!-- ai-operator:main-canceled -->"
	// How many handled canceled pipelines the status keeps.
	maxCanceled = 10
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
// A canceled pipeline is retried once and reported if canceled again
// (checkCanceled). The status changes only when every step succeeded, so a
// failed one is repeated on the next check. With autoFixFlaky, a flaky issue goes to the
// agent at its second occurrence, into the milestone of lane (nil: none).
func (r *AgentProjectReconciler) syncMainHealth(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, lane *aiv1.AgentTask) error {
	if spec.MainHealth.Disabled {
		var kept *aiv1.MainHealthStatus
		if cur := project.Status.MainHealth; cur != nil && len(cur.FlakyFixes) > 0 {
			// The hand-overs stay exempt from the title filter.
			kept = &aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, FlakyFixes: cur.FlakyFixes}
		}
		project.Status.MainHealth = kept
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
	proj, err := gl.GetProject(ctx, spec.GitLab.Project)
	if err != nil || proj.DefaultBranch == "" {
		return err
	}
	ps, err := gl.ListPipelines(ctx, spec.GitLab.Project, gitlab.ListPipelinesOptions{Ref: proj.DefaultBranch, Scope: "finished", Limit: 10})
	if err != nil {
		return err
	}
	st := cur.DeepCopy()
	if err := r.checkMainHealth(ctx, gl, spec, project.Status.BotUserID, proj.DefaultBranch, ps, st); err != nil {
		return err
	}
	if spec.MainHealth.AutoFixFlaky && cur.State == aiv1.MainHealthRetrying && st.State == aiv1.MainHealthFlaky {
		if err := r.autoFixFlaky(ctx, gl, spec, project.Status.BotUserID, st, lane); err != nil {
			// The check repeats; the hand-overs that went through stay.
			cur.FlakyFixes = st.FlakyFixes
			return err
		}
	}
	project.Status.MainHealth = st
	if spec.MainHealth.AutoFix && st.IssueIID != 0 && st.AutoFixIssueIID == 0 {
		err = r.autoFix(ctx, gl, spec, st)
	}
	return errors.Join(err, r.checkCanceled(ctx, gl, spec, project.Status.BotUserID, proj.DefaultBranch, ps, st))
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

// checkMainHealth checks ps, the newest finished pipelines of branch.
func (r *AgentProjectReconciler) checkMainHealth(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, bot int64, branch string, ps []gitlab.Pipeline, st *aiv1.MainHealthStatus) error {
	mh, glProject := spec.MainHealth, spec.GitLab.Project
	// Skipped and canceled pipelines say nothing about the branch: the
	// newest one that does counts.
	i := slices.IndexFunc(ps, func(p gitlab.Pipeline) bool {
		return p.Status == "success" || p.Status == "manual" || p.Status == "failed"
	})
	if i < 0 {
		return nil
	}
	var err error
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
		if err := r.reportMainRed(ctx, gl, spec, bot, branch, p, jobs, st); err != nil {
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

// checkCanceled retries a canceled pipeline among ps, the newest finished
// pipelines of branch, once if its commit has no successful pipeline of its
// own (mainHealth.retryCanceled). Canceled again, or if the retry is refused,
// it is reported once. A canceled pipeline with a newer one that is not
// canceled or skipped is superseded (GitLab's auto-cancel of redundant
// pipelines): the branch is fast-forward only, so the newer commit covers it.
// The first check records the newest pipeline as the baseline: canceled
// pipelines up to it are left alone.
func (r *AgentProjectReconciler) checkCanceled(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, bot int64, branch string, ps []gitlab.Pipeline, st *aiv1.MainHealthStatus) error {
	if !*spec.MainHealth.RetryCanceled {
		st.CanceledSince, st.Canceled = 0, nil
		return nil
	}
	if st.CanceledSince == 0 {
		if len(ps) > 0 {
			st.CanceledSince = ps[0].ID
		}
		return nil
	}
	defer func() {
		slices.SortFunc(st.Canceled, func(a, b aiv1.CanceledPipeline) int { return cmp.Compare(b.PipelineID, a.PipelineID) })
		st.Canceled = st.Canceled[:min(len(st.Canceled), maxCanceled)]
	}()
	glProject := spec.GitLab.Project
	// ps holds finished pipelines only: the running ones are listed when
	// none of ps covers a canceled pipeline.
	covering, listed := newestCovering(ps), false
	for _, p := range slices.Backward(ps) {
		i := slices.IndexFunc(st.Canceled, func(c aiv1.CanceledPipeline) bool { return c.PipelineID == p.ID })
		if p.Status != "canceled" || p.ID <= st.CanceledSince || i >= 0 && (st.Canceled[i].Reported || st.Canceled[i].Superseded) {
			continue
		}
		if p.ID > covering && !listed {
			all, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{Ref: branch, Limit: 10})
			if err != nil {
				return fmt.Errorf("listing the pipelines of %s: %w", branch, err)
			}
			covering, listed = max(covering, newestCovering(all)), true
		}
		if p.ID < covering {
			log.FromContext(ctx).Info("canceled default branch pipeline is superseded by a newer one", "pipeline", p.ID, "sha", p.SHA, "newer", covering)
			if i < 0 {
				st.Canceled = append(st.Canceled, aiv1.CanceledPipeline{PipelineID: p.ID, SHA: p.SHA})
				i = len(st.Canceled) - 1
			}
			st.Canceled[i].Superseded = true
			continue
		}
		same, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: p.SHA, Ref: branch, Scope: "finished"})
		if err != nil {
			return fmt.Errorf("listing the pipelines of %s: %w", p.SHA, err)
		}
		if slices.ContainsFunc(same, func(q gitlab.Pipeline) bool { return q.Status == "success" }) {
			continue
		}
		if i < 0 {
			st.Canceled = append(st.Canceled, aiv1.CanceledPipeline{PipelineID: p.ID, SHA: p.SHA})
			i = len(st.Canceled) - 1
		}
		c := &st.Canceled[i]
		if !c.Retried {
			log.FromContext(ctx).Info("default branch pipeline was canceled, retrying it once", "pipeline", p.ID, "sha", p.SHA)
			if _, err = gl.RetryPipeline(ctx, glProject, p.ID); err == nil {
				c.Retried = true
				continue
			}
			log.FromContext(ctx).Info("could not retry the canceled pipeline", "pipeline", p.ID, "error", err.Error())
		}
		if err := r.reportCanceled(ctx, gl, spec, bot, branch, p, c.Retried, st.IssueIID); err != nil {
			return err
		}
		c.Reported = true
	}
	return nil
}

// newestCovering is the ID of the newest pipeline of ps that is not canceled
// or skipped, 0 if none: it covers the older commits of its branch.
func newestCovering(ps []gitlab.Pipeline) int64 {
	var id int64
	for _, p := range ps {
		if p.Status != "canceled" && p.Status != "skipped" {
			id = max(id, p.ID)
		}
	}
	return id
}

// reportCanceled reports a canceled pipeline that was retried (or could not
// be): a comment on the open main-red issue (redIID), else on the
// main-canceled issue, which neither freezes autopilot nor goes to autoFix.
// Only a new issue pings the owners; with mainHealth.issue off it is logged.
func (r *AgentProjectReconciler) reportCanceled(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, bot int64, branch string, p gitlab.Pipeline, retried bool, redIID int64) error {
	if !*spec.MainHealth.Issue {
		log.FromContext(ctx).Info("default branch pipeline canceled again", "pipeline", p.ID, "sha", p.SHA)
		return nil
	}
	glProject := spec.GitLab.Project
	what := "was canceled again after its retry"
	if !retried {
		what = "was canceled and could not be retried"
	}
	line := fmt.Sprintf("⏹️ Pipeline %s at `%s` of `%s` %s; the commit has no successful pipeline, so its jobs (a release, for example) did not run. Run the pipeline again, or push a commit that covers it.\n", p.WebURL, p.SHA, branch, what)
	iid := redIID
	if iid == 0 {
		is, err := findIssue(ctx, gl, glProject, bot, mainCanceledLabel, mainCanceledMarker)
		if err != nil {
			return err
		}
		if is == nil {
			desc := mainCanceledMarker + "\n" + line
			if spec.Notify.Wants(aiv1.NotifyMainRed) {
				desc += notify.Mention(notify.Resolve("", spec.Workflow.CommandPrefix, spec.Notify.Users, r.DefaultOwners))
			}
			title := fmt.Sprintf("⏹️ `%s`: pipeline canceled twice", branch)
			if _, err := gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{Title: title, Description: desc, Labels: "ai-generated," + mainCanceledLabel}); err != nil {
				return fmt.Errorf("creating the main-canceled issue: %w", err)
			}
			return nil
		}
		iid = is.IID
	}
	if _, err := gl.CreateIssueNote(ctx, glProject, iid, line); err != nil {
		return fmt.Errorf("commenting on issue #%d: %w", iid, err)
	}
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
const flakyOccurrenceMarker = marker.FlakyOccurrence

// flakyOccurrences are the pipelines of the bot's occurrence comments
// among notes, each once.
func flakyOccurrences(notes []gitlab.Note, bot int64) []int64 {
	var out []int64
	for _, n := range notes {
		if pid, ok := marker.FlakyPipeline(n.Body); ok && n.Author.ID == bot && !slices.Contains(out, pid) {
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
