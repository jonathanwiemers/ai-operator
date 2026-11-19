package controller

import (
	"errors"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// companionLinesEnv is a home issue #1 of milestone %3 with a companion
// merge request in group/infra (+12/-3 production), after the home merge;
// its history events are captured.
type companionLinesEnv struct {
	*projectEnv
	infra  *gitlabfake.Fake
	cmr    *gitlab.MergeRequest
	events []store.Event
}

func newCompanionLinesEnv(t *testing.T, autoMerge bool) *companionLinesEnv {
	t.Helper()
	pe, infra := newCompanionGateEnv(t)
	l := &companionLinesEnv{projectEnv: pe, infra: infra}
	pe.r.recordEvent = func(ev store.Event) { l.events = append(l.events, ev) }
	if autoMerge {
		withInfraTarget(pe, true)
		pe.gl.Milestones[0].Description = landingMilestone
	}
	_, l.cmr = pe.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	infra.MRDiffs = map[int64][]gitlab.Diff{l.cmr.IID: {{OldPath: "clusters/app/deploy.yaml", NewPath: "clusters/app/deploy.yaml", Diff: patch(12, 3)}}}
	if autoMerge {
		pe.gl.SetLabels(1, "ai-generated", "agent::review", "agent-repo::group/infra")
	}
	pe.reconcile() // marked ready
	pe.reconcile() // home merged
	if !slices.Equal(pe.gl.Merged, []int64{1}) {
		t.Fatalf("the home MR merges: %v", pe.gl.Merged)
	}
	infra.AddPipeline("sha-infra", "agent/app-1-api", "success")
	pe.reconcile() // the companion is recorded
	if cs := pe.project().Status.Companions; len(cs) != 1 || cs[0].State.Ended() {
		t.Fatalf("one companion entry waiting to land: %+v", cs)
	}
	return l
}

func (l *companionLinesEnv) entry() aiv1.CompanionStatus {
	l.t.Helper()
	cs := l.project().Status.Companions
	if len(cs) != 1 {
		l.t.Fatalf("one companion entry: %+v", cs)
	}
	return cs[0]
}

func (l *companionLinesEnv) diffCalls() int { return l.infra.DiffLists[l.cmr.IID] }

// mergedByHand merges the companion merge request outside the operator.
func (l *companionLinesEnv) mergedByHand() {
	l.cmr.State, l.cmr.MergeCommitSHA = "merged", "merge-sha-infra"
}

// expectMerged checks the entry is merged with lines (nil: none) and that one
// companion.merged event was recorded for it; then a few more polls make no
// diffs call and record nothing.
func (l *companionLinesEnv) expectMerged(lines *aiv1.MergedLines, reason string) {
	l.t.Helper()
	c := l.entry()
	if c.State != aiv1.LandingMerged || c.EndedAt == nil || !strings.Contains(c.Reason, reason) || (reason == "" && c.Reason != "") {
		l.t.Fatalf("the companion lands: %+v", c)
	}
	if (lines == nil) != (c.MergedLines == nil) || (lines != nil && *lines != *c.MergedLines) {
		l.t.Fatalf("mergedLines = %+v, want %+v", c.MergedLines, lines)
	}
	if len(l.events) != 1 {
		l.t.Fatalf("one event: %+v", l.events)
	}
	ev := l.events[0]
	if ev.Kind != history.KindCompanionMerged || ev.Key != "companion/group/infra/1/merged" || ev.Namespace != ns || ev.Project != "shop" ||
		ev.MilestoneIID != 3 || ev.IssueIID != 1 || ev.MergeRequestIID != l.cmr.IID || !ev.At.Equal(c.EndedAt.Time) ||
		ev.Summary != "Companion group/infra!1 merged (#1)" || ev.Data["companion"] != "group/infra" {
		l.t.Fatalf("the companion.merged event of the home issue: %+v", ev)
	}
	got, ok := ev.Data["lines"].(*aiv1.MergedLines)
	if ok != (lines != nil) || (lines != nil && *got != *lines) {
		l.t.Fatalf("data.lines = %+v, want %+v", ev.Data["lines"], lines)
	}
	calls := l.diffCalls()
	for range 2 {
		l.reconcile()
	}
	if len(l.events) != 1 || l.diffCalls() != calls {
		l.t.Fatalf("later polls record nothing and count nothing: %d events, %d diffs calls (was %d)", len(l.events), l.diffCalls(), calls)
	}
}

var infraLines = &aiv1.MergedLines{Production: aiv1.LineCount{Added: 12, Deleted: 3}}

func TestCompanionMergedByHandRecordsLines(t *testing.T) {
	l := newCompanionLinesEnv(t, false)
	l.mergedByHand()
	calls := l.diffCalls()
	l.reconcile()
	if n := l.diffCalls() - calls; n != 1 {
		t.Fatalf("one diffs call at the merge: %d", n)
	}
	l.expectMerged(infraLines, "")
}

// A failing diffs call lands the companion as before and records the event
// without lines.
func TestCompanionMergedLinesDiffError(t *testing.T) {
	l := newCompanionLinesEnv(t, false)
	l.infra.DiffErrors = map[int64]error{l.cmr.IID: errors.New("boom")}
	l.mergedByHand()
	l.reconcile()
	l.expectMerged(nil, "")
}

func TestCompanionAutopilotMergeRecordsLines(t *testing.T) {
	l := newCompanionLinesEnv(t, true)
	l.reconcile() // autopilot merges it
	if !slices.Equal(l.infra.Merged, []int64{l.cmr.IID}) {
		t.Fatalf("autopilot merges the companion: %v %+v", l.infra.Merged, l.entry())
	}
	if c := l.entry(); c.State != aiv1.LandingMerging || len(l.events) != 0 {
		t.Fatalf("merging until the default branch is green: %+v %+v", c, l.events)
	}
	l.infra.AddPipeline("merge-sha-infra", "main", "success")
	l.reconcile()
	l.expectMerged(infraLines, "")
}

// An owner's resume records a companion autopilot merged on a red default
// branch as merged: it gets its lines and its event too.
func TestCompanionResumeRecordsLines(t *testing.T) {
	l := newCompanionLinesEnv(t, true)
	l.reconcile() // autopilot merges it
	l.infra.AddPipeline("merge-sha-infra", "main", "failed")
	for range 3 {
		l.reconcile() // blocked, paused
	}
	ap := l.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCompanion || len(l.events) != 0 {
		t.Fatalf("paused over the red default branch, nothing recorded: %+v %+v", ap, l.events)
	}
	l.gl.AddNote(ap.TrackingIssueIID, alice, "/agent resume")
	l.reconcile()
	l.expectMerged(infraLines, "recorded as merged after a resume")
}
