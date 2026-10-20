package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// runLog records the steps of one run. Every step is logged when it starts
// and when it ends, published as status.run.currentStep while it runs, and
// listed in the run log appended to the GitLab comment of the run.
type runLog struct {
	// id is "<task>#<run request>"; runner and agent log lines carry it as
	// "run", and the GitLab run log shows it.
	id    string
	log   logr.Logger
	start time.Time
	steps []*step
}

// step is one step of a run.
type step struct {
	n       int
	name    string
	log     logr.Logger
	start   time.Time
	elapsed time.Duration
	result  string
	failed  bool
	ended   bool
}

func newRunLog(base logr.Logger, task *aiv1.AgentTask) *runLog {
	id := fmt.Sprintf("%s#%d", task.Name, task.Spec.RunRequest)
	kv := []any{"run", id, "type", task.Spec.Type}
	if task.Spec.Issue != nil {
		kv = append(kv, "issue", task.Spec.Issue.IID)
	}
	if task.Spec.Milestone != nil {
		kv = append(kv, "milestone", task.Spec.Milestone.IID)
	}
	return &runLog{id: id, log: base.WithValues(kv...), start: time.Now()}
}

// log returns the logger of the current run, or the runner's logger between
// runs.
func (r *Runner) log() logr.Logger {
	if r.run != nil {
		return r.run.log
	}
	return r.Log
}

// step starts the next step of the current run. kv are logged with the start
// line; the step must be ended with done or fail.
func (r *Runner) step(ctx context.Context, name string, kv ...any) *step {
	if r.run == nil {
		r.run = &runLog{log: r.Log, start: time.Now()}
	}
	n := len(r.run.steps) + 1
	s := &step{n: n, name: name, log: r.run.log.WithValues("step", n, "name", name), start: time.Now()}
	r.run.steps = append(r.run.steps, s)
	s.log.Info("step started", kv...)
	if err := r.patchRun(ctx, func(st *aiv1.RunStatus) { st.CurrentStep = s.label() }); err != nil {
		s.log.V(1).Info("could not publish the current step", "error", err.Error())
	}
	return s
}

// done ends the step successfully. result is a short human-readable outcome
// for the GitLab run log; kv are logged with the end line.
func (s *step) done(result string, kv ...any) {
	if s.ended {
		return
	}
	s.end(result)
	s.log.Info("step done", append([]any{"durationMs", s.elapsed.Milliseconds(), "result", result}, kv...)...)
}

// fail ends the step with err and returns err. A usage limit is not logged
// as an error: the run is deferred, not failed.
func (s *step) fail(err error) error {
	if s.ended {
		return err
	}
	if _, limited := AsUsageLimit(err); limited {
		s.end("usage limit")
		s.log.Info("step deferred", "durationMs", s.elapsed.Milliseconds(), "reason", err.Error())
		return err
	}
	s.failed = true
	s.end("failed")
	s.log.Error(err, "step failed", "durationMs", s.elapsed.Milliseconds())
	return err
}

func (s *step) end(result string) {
	s.ended = true
	s.elapsed = time.Since(s.start)
	s.result = result
}

func (s *step) label() string { return fmt.Sprintf("%d %s", s.n, s.name) }

// finish ends steps left open by an early return, with the run's error.
func (rl *runLog) finish(err error) {
	for _, s := range rl.steps {
		switch {
		case s.ended:
		case err != nil:
			s.fail(err)
		default:
			s.done("")
		}
	}
}

// lines summarises the steps for the "run finished" log line.
func (rl *runLog) lines() []string {
	out := make([]string, 0, len(rl.steps))
	for _, s := range rl.steps {
		line := fmt.Sprintf("%s %s", s.label(), fmtDuration(s.elapsed))
		if s.failed {
			line += " failed"
		}
		out = append(out, line)
	}
	return out
}

// markdown renders the steps so far as a collapsed table for GitLab comments.
func (rl *runLog) markdown(costUSD float64) string {
	if len(rl.steps) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n<details><summary>Run log <code>%s</code>: %d steps, %s, $%.2f</summary>\n\n",
		rl.id, len(rl.steps), fmtDuration(time.Since(rl.start)), costUSD)
	b.WriteString("| # | Step | Time | Result |\n|--:|---|--:|---|\n")
	for _, s := range rl.steps {
		elapsed, result := fmtDuration(s.elapsed), cell(s.result)
		switch {
		case !s.ended:
			elapsed, result = "…", "running"
		case s.failed:
			result = "❌ failed"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", s.n, cell(s.name), elapsed, result)
	}
	b.WriteString("\n</details>\n")
	return b.String()
}

// cell makes s safe for a single markdown table cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(truncate(s, 120), "|", `\|`)
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	default:
		return d.Round(time.Second).String()
	}
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// diffSummary returns the last line of `git diff --stat`, e.g. "2 files
// changed, 10 insertions(+)".
func diffSummary(stat string) string {
	stat = strings.TrimSpace(stat)
	if i := strings.LastIndex(stat, "\n"); i >= 0 {
		stat = stat[i+1:]
	}
	return strings.TrimSpace(stat)
}

// agentResult is the run log entry of an agent invocation.
func agentResult(role aiv1.RoleSpec, res *Result, status string) string {
	who := string(role.Engine)
	if role.Model != "" {
		who += "/" + role.Model
	}
	out := fmt.Sprintf("%s, $%.2f", who, res.CostUSD)
	if status != "" {
		out += ", " + status
	}
	return out
}

type runIDKey struct{}

// withRunID tags ctx with the run ID; the agent client sends it to the agent
// server, so its log lines carry the same "run" value as the runner's.
func withRunID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, runIDKey{}, id)
}

func runIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}
