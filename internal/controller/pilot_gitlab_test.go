package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/runner"
)

func TestPilotGitLabDeferralsMatchTheRunner(t *testing.T) {
	if maxGitLabDeferrals != runner.MaxDeferrals {
		t.Fatalf("maxGitLabDeferrals is %d, runner.MaxDeferrals %d", maxGitLabDeferrals, runner.MaxDeferrals)
	}
}

// gitLabTasks is a milestone with issue 2, and its task with blocked and run.
func gitLabTasks(blocked aiv1.BlockReason, run aiv1.RunStatus) (*aiv1.AgentTask, map[int64]gitlab.Issue, map[int64]*aiv1.AgentTask) {
	m := &gitlab.Milestone{IID: 3}
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}, Status: aiv1.AgentTaskStatus{Plan: []aiv1.PlannedIssue{{IID: 2}}}}
	issues := map[int64]gitlab.Issue{2: {IID: 2, Milestone: m, Labels: []string{"agent::blocked"}}}
	task := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: run, Blocked: &aiv1.BlockedStatus{Reason: blocked, Message: "GitLab: 429"}}}
	return st, issues, map[int64]*aiv1.AgentTask{2: task}
}

// A GitLab deferral is one event per deferral, with its retry time; the
// operator retries it.
func TestPilotEventOfAGitLabDeferral(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	deferred := time.Date(2026, 11, 26, 23, 35, 0, 0, time.UTC)
	run := aiv1.RunStatus{PushedSHA: "sha2", Deferrals: 2, LastActivityTime: &metav1.Time{Time: deferred},
		DeferredUntil: &metav1.Time{Time: deferred.Add(6 * time.Minute)}}
	st, issues, tasks := gitLabTasks(aiv1.BlockGitLab, run)
	evs := pilotEvents(spec, nil, time.Time{}, st, issues, tasks, true, nil, deferred)
	want := "#2 is deferred by GitLab's rate limit until 2026-11-26 23:41 UTC (deferral 2 of 20); the operator retries it, nothing to do."
	if len(evs) != 1 || evs[0].Key != "gitlab/2/1795736100" || evs[0].IssueIID != 2 || evs[0].Text != want {
		t.Fatalf("got %+v, want gitlab/2/1795736100 with %q", evs, want)
	}
	// The next deferral of the same issue is a new event.
	tasks[2].Status.Run.LastActivityTime = &metav1.Time{Time: deferred.Add(6 * time.Minute)}
	if evs := pilotEvents(spec, nil, time.Time{}, st, issues, tasks, true, nil, deferred); len(evs) != 1 || evs[0].Key != "gitlab/2/1795736460" {
		t.Fatalf("got %+v, want the next deferral's key", evs)
	}
	// A run held only by another run's limit has counted no deferral.
	tasks[2].Status.Run.Deferrals = 0
	if evs := pilotEvents(spec, nil, time.Time{}, st, issues, tasks, true, nil, deferred); len(evs) != 1 ||
		evs[0].Text != "#2 is deferred by GitLab's rate limit until 2026-11-26 23:41 UTC; the operator retries it, nothing to do." {
		t.Fatalf("got %+v, want no deferral count", evs)
	}
}

// After the deferral cap the infra event says so, with the project's limit.
func TestPilotEventOfAGitLabFailureAfterTheCap(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	run := aiv1.RunStatus{PushedSHA: "sha2", FailureKind: aiv1.FailureGitLab}
	st, issues, tasks := gitLabTasks(aiv1.BlockInfra, run)
	until := time.Date(2026, 11, 26, 23, 41, 0, 0, time.UTC)
	for _, tc := range []struct {
		until time.Time
		want  string
	}{
		{until, "#2 is blocked (infra): GitLab's rate limit outlasted the automatic retries; `retry` is allowed once the limit has passed (`limitedUntil` 2026-11-26 23:41 UTC)."},
		{time.Time{}, "#2 is blocked (infra): GitLab's rate limit outlasted the automatic retries; `retry` is allowed once the limit has passed (it has passed)."},
	} {
		evs := pilotEvents(spec, nil, tc.until, st, issues, tasks, true, nil, until)
		if len(evs) != 1 || evs[0].Key != "infra/2/sha2" || evs[0].Text != tc.want {
			t.Fatalf("got %+v, want infra/2/sha2 with %q", evs, tc.want)
		}
	}
}

func TestGitLabLimitedUntil(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 40, 0, 0, time.UTC)
	project := &aiv1.AgentProject{}
	if got := gitLabLimitedUntil(project, now); !got.IsZero() {
		t.Fatalf("no status: got %v", got)
	}
	project.Status.GitLab = &aiv1.GitLabProjectStatus{RateLimit: &aiv1.GitLabRateLimitStatus{LimitedUntil: &metav1.Time{Time: now.Add(time.Minute)}}}
	if got := gitLabLimitedUntil(project, now); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("in the future: got %v", got)
	}
	if got := gitLabLimitedUntil(project, now.Add(time.Minute)); !got.IsZero() {
		t.Fatalf("passed: got %v", got)
	}
}

// A retry waits for GitLab: none while a deferral is pending, and after the
// cap only once the project's limit has passed.
func TestValidRetryUnderGitLabsLimit(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 40, 0, 0, time.UTC)
	later := &metav1.Time{Time: now.Add(6 * time.Minute)}
	deferred := aiv1.AgentTaskStatus{Run: aiv1.RunStatus{BlockReason: aiv1.BlockGitLab, DeferredUntil: later}}
	capped := aiv1.AgentTaskStatus{Run: aiv1.RunStatus{FailureKind: aiv1.FailureGitLab}, Blocked: &aiv1.BlockedStatus{Reason: aiv1.BlockInfra}}
	infra := aiv1.AgentTaskStatus{Blocked: &aiv1.BlockedStatus{Reason: aiv1.BlockInfra}}
	retry := aiv1.PilotAction{Tool: "retry", IssueIID: 2}
	for _, tc := range []struct {
		name   string
		status aiv1.AgentTaskStatus
		limit  gitLabLimit
		want   string
	}{
		{"deferred", deferred, gitLabLimit{until: later.Time, now: now}, "#2 waits out GitLab's rate limit until 2026-11-26 23:46 UTC; the operator retries it"},
		{"deferred, the project's limit passed", deferred, gitLabLimit{now: now}, "#2 waits out GitLab's rate limit until 2026-11-26 23:46 UTC; the operator retries it"},
		{"deferral time passed", deferred, gitLabLimit{now: later.Add(time.Second)}, "<nil>"},
		{"capped before limitedUntil", capped, gitLabLimit{until: later.Time, now: now}, "GitLab's rate limit holds until 2026-11-26 23:46 UTC; #2 can be retried after it"},
		{"capped after limitedUntil", capped, gitLabLimit{now: now}, "<nil>"},
		{"another infra block", infra, gitLabLimit{until: later.Time, now: now}, "<nil>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks := map[int64]*aiv1.AgentTask{2: {Status: tc.status}}
			if err := validRetry(retry, tasks, tc.limit); fmt.Sprint(err) != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if err := validRetry(aiv1.PilotAction{Tool: "reReview", IssueIID: 2}, tasks, tc.limit); err != nil {
				t.Fatalf("other tools pass: %v", err)
			}
		})
	}
}

// A managing pilot runs the retry after the cap on its own and counts it,
// but not before the limit has passed.
func TestPilotManagesTheRetryAfterTheGitLabCap(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 40, 0, 0, time.UTC)
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}}
	coord := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Autopilot: true}}
	tasks := map[int64]*aiv1.AgentTask{1: {Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1", FailureKind: aiv1.FailureGitLab},
		Blocked: &aiv1.BlockedStatus{Reason: aiv1.BlockInfra}}}}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	retry := []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}
	proposed := []aiv1.PilotProposal{{Actions: retry, State: aiv1.PilotState(st, tasks, retry)}}
	var opened []string
	rest, own, _ := manage(spec, st, coord, tasks, gitLabLimit{until: now.Add(time.Minute), now: now}, proposed, &opened)
	if len(own) != 0 || len(rest) != 1 {
		t.Fatalf("before limitedUntil the retry is no action of its own: own %+v, rest %+v", own, rest)
	}
	rest, own, counts := manage(spec, st, coord, tasks, gitLabLimit{now: now}, proposed, &opened)
	if len(own) != 1 || len(rest) != 0 || counts["1"].Count != 1 || counts["1"].Head != "sha1" {
		t.Fatalf("after it the pilot retries on its own and counts it: own %+v, rest %+v, counts %+v", own, rest, counts)
	}
}

// After the cap and the limit, the managing pilot's retry runs and is
// audited on the tracking issue.
func TestPilotManagedRetriesAfterTheGitLabCap(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		s.Blocked = &aiv1.BlockedStatus{Reason: aiv1.BlockInfra, Message: "GitLab: 429"}
		s.Run.FailureKind = aiv1.FailureGitLab
	})
	seen := len(botComments(e, tracking))
	before := e.toolEffect()
	e.proposeFor(1, "event/infra/1/sha1@1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	after := e.toolEffect()
	notes := botNotesSince(e, tracking, seen)
	if after.runs != before.runs+1 || len(notes) != 1 || !strings.Contains(notes[0], "**retry** #1") || !strings.Contains(notes[0], "Interventions left for #1: 1 of 2.") {
		t.Fatalf("the retry runs once without a yes, audited: %+v → %+v, comments %q", before, after, notes)
	}
}

func TestGitLabPattern(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 40, 0, 0, time.UTC)
	key := func(iid int64, at string) string {
		t, _ := time.Parse("15:04", at)
		return fmt.Sprintf("gitlab/%d/%d", iid, time.Date(2026, 11, 26, t.Hour(), t.Minute(), 0, 0, time.UTC).Unix())
	}
	marker := fmt.Sprintf("gitlab-pattern/%d", time.Date(2026, 11, 26, 23, 0, 0, 0, time.UTC).Unix())
	three := []string{key(22, "23:35"), "infra/2/sha", key(7, "23:05"), key(22, "23:21"), key(5, "22:30")}
	line, m := gitLabPattern(three, 100, now)
	if want := "GitLab rate-limited runs 3× in the last hour (2026-11-26 23:05 UTC, 2026-11-26 23:21 UTC, 2026-11-26 23:35 UTC; peak 100 requests/min observed)."; line != want || m != marker {
		t.Fatalf("got %q, %q; want %q, %q", line, m, want, marker)
	}
	if line, _ := gitLabPattern(three, 0, now); line != "GitLab rate-limited runs 3× in the last hour (2026-11-26 23:05 UTC, 2026-11-26 23:21 UTC, 2026-11-26 23:35 UTC)." {
		t.Fatalf("without a peak: %q", line)
	}
	if line, m := gitLabPattern(three[1:], 100, now); line != "" || m != "" {
		t.Fatalf("two in the last hour: %q", line)
	}
	if line, _ := gitLabPattern(append(three, marker), 100, now); line != "" {
		t.Fatalf("the hour had it: %q", line)
	}
	if line, _ := gitLabPattern(append(three, "gitlab/9/0", "gitlab/9/x"), 100, now); !strings.Contains(line, "3×") {
		t.Fatalf("keys without a time do not count: %q", line)
	}
}

// Three GitLab deferrals within the hour put the pattern line into the next
// pilot note on the tracking issue, once in the hour.
func TestPilotNoteCarriesTheGitLabPatternOncePerHour(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	now := e.r.now()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		for i, ago := range []time.Duration{50, 20, 5} {
			s.Pilot.Events = append(s.Pilot.Events, fmt.Sprintf("gitlab/%d/%d", 10+i, now.Add(-ago*time.Minute).Unix()))
		}
	})
	seen := len(botComments(e, tracking))
	e.proposeFor(1, "event/stuck/1/sha1@1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	notes := botNotesSince(e, tracking, seen)
	if len(notes) != 1 || !strings.Contains(notes[0], "The pilot acted on its own") || !strings.Contains(notes[0], "GitLab rate-limited runs 3× in the last hour") {
		t.Fatalf("the audit carries the pattern line: %q", notes)
	}
	seen += len(notes)
	e.proposeFor(1, "event/stuck/1/sha1@2", aiv1.PilotAction{Tool: "reReview", IssueIID: 1})
	e.reconcile()
	e.reconcile()
	notes = botNotesSince(e, tracking, seen)
	if len(notes) != 1 || strings.Contains(notes[0], "GitLab rate-limited") {
		t.Fatalf("the next note in the hour has no line: %q", notes)
	}
}
