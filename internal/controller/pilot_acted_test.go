package controller

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestPilotActedLogsAManagedAction(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	seen := len(botComments(e, tracking))
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	acted := e.pilot().Status.Pilot.Acted
	if len(acted) != 1 {
		t.Fatalf("one entry per proposal run without a prompt: %+v", acted)
	}
	a := acted[0]
	if a.By != "" || !a.Ran || !slices.Equal(a.Issues, []int64{1}) || a.At.IsZero() ||
		a.Summary != "fix #1" || !strings.HasPrefix(a.Why, "`env`: the run reports") {
		t.Fatalf("the pilot on its own, ran, counted against #1: %+v", a)
	}
	notes := botNotesSince(e, tracking, seen)
	if len(a.Lines) != 1 || !strings.Contains(a.Lines[0], "**retry** #1") || len(notes) != 1 || !strings.Contains(notes[0], "\n\n"+a.Lines[0]+"\n\n") {
		t.Fatalf("the lines are the audit comment's: %q\n%q", a.Lines, notes)
	}
}

func TestPilotActedLogsWhoAcceptedAProposal(t *testing.T) {
	e := newManagedEnv(t)
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{Accepted: []aiv1.PilotProposal{
			{ID: 4, Summary: "retry #1", AcceptedBy: "alice", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}},
			{ID: 5, Summary: "retry #2", AcceptedBy: "alice", Prompt: "Q1", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 2}}},
		}}
	})
	e.reconcile()
	acted := e.pilot().Status.Pilot.Acted
	if len(acted) != 1 || acted[0].By != "alice" || acted[0].Summary != "retry #1" || !slices.Equal(acted[0].Issues, []int64{1}) {
		t.Fatalf("an owner's accept is logged with the owner, a prompt's answer is not: %+v", acted)
	}
}

func TestPilotActedLogsAProposalThatRanNothing(t *testing.T) {
	e := newManagedEnv(t)
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{
			Accepted:         []aiv1.PilotProposal{{Summary: "retry #1", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Trigger: 1}},
			Interventions:    map[string]aiv1.PilotIntervention{"1": {Head: "sha1", Count: 1}},
			AutonomyOverride: &aiv1.PilotAutonomyOverride{Autonomy: aiv1.PilotPropose, By: "alice"},
		}
	})
	e.reconcile()
	acted := e.pilot().Status.Pilot.Acted
	if len(acted) != 1 || acted[0].Ran || !strings.Contains(acted[0].Lines[0], "not run: the pilot proposes now") {
		t.Fatalf("a proposal whose actions were all refused is logged as not run: %+v", acted)
	}
}

func TestPilotActedKeepsTheNewest(t *testing.T) {
	e := newManagedEnv(t)
	var old []aiv1.PilotActed
	for i := range aiv1.MaxPilotActed {
		old = append(old, aiv1.PilotActed{Summary: "old " + strconv.Itoa(i), Ran: true})
	}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Acted = old
	})
	e.proposeFor(1, "event/a", aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	e.reconcile()
	acted := e.pilot().Status.Pilot.Acted
	if len(acted) != aiv1.MaxPilotActed || acted[0].Summary != "old 1" || acted[len(acted)-1].Summary != "fix #1" {
		t.Fatalf("the 31st drops the oldest: %d entries, first %q, last %q", len(acted), acted[0].Summary, acted[len(acted)-1].Summary)
	}
}

func TestPilotActedCutsUntrustedText(t *testing.T) {
	var lines []string
	for i := range 10 {
		lines = append(lines, strconv.Itoa(i)+strings.Repeat("é", 200))
	}
	p := aiv1.PilotProposal{
		Summary:    strings.Repeat("ü", 200), // 400 bytes
		Why:        strings.Repeat("€", 400), // 1200 bytes
		AcceptedBy: "alice",
		Trigger:    3,
		Actions: []aiv1.PilotAction{
			{Tool: "retry", IssueIID: 2}, {Tool: "openIssue", Title: "x"}, {Tool: "fixFlaky"}, {Tool: "reReview", IssueIID: 2},
		},
	}
	a := pilotActed(time.Now(), p, lines, []error{errors.New("no"), nil, errors.New("no"), errors.New("no")}, nil)
	if len(a.Summary) > aiv1.MaxPilotActedSummary || len(a.Summary) < aiv1.MaxPilotActedSummary-1 || !utf8.ValidString(a.Summary) ||
		len(a.Why) > aiv1.MaxPilotActedWhy || !utf8.ValidString(a.Why) {
		t.Fatalf("summary and why are cut on a character: %d, %d bytes", len(a.Summary), len(a.Why))
	}
	if len(a.Lines) != aiv1.MaxPilotActedLines || !strings.HasPrefix(a.Lines[7], "7") {
		t.Fatalf("the first eight lines are kept: %d", len(a.Lines))
	}
	for _, l := range a.Lines {
		if len(l) > aiv1.MaxPilotActedLine || !utf8.ValidString(l) {
			t.Fatalf("a line is cut on a character: %d bytes", len(l))
		}
	}
	if !slices.Equal(a.Issues, []int64{2, 3, 0}) || !a.Ran || a.By != "alice" {
		t.Fatalf("issues in action order without repeats, ran with one action: %+v", a)
	}
	if got := cutText("ok\xffno", 10); got != "ok�no" {
		t.Fatalf("invalid UTF-8 is replaced: %q", got)
	}
}
