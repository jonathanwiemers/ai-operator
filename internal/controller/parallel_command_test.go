package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// ownedParallelEnv is newParallelEnv (three roots, a limit of max) owned by
// alice, with ada@x as its owner subject, polled once: the tracking issue
// is there.
func ownedParallelEnv(t *testing.T, max int32) *projectEnv {
	t.Helper()
	e := newParallelEnv(t, max, false)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description = "/agent owners @alice\n/agent owner-subjects ada@x\n" + e.gl.Milestones[0].Description
	e.reconcile()
	if e.autopilot().TrackingIssueIID == 0 {
		t.Fatalf("no tracking issue: %+v", e.autopilot())
	}
	return e
}

// parallelLine is the milestone description's parallel line, "" without one.
func (e *projectEnv) parallelLine() string {
	for _, l := range strings.Split(e.gl.Milestones[0].Description, "\n") {
		if strings.HasPrefix(l, "/agent parallel") {
			return l
		}
	}
	return ""
}

func TestOwnerSetsParallelOnTheTrackingIssue(t *testing.T) {
	e := ownedParallelEnv(t, 1)
	e.wantStates("one at a time", map[int64]aiv1.AutopilotItemState{1: active, 2: pending, 4: pending})
	tracking := e.autopilot().TrackingIssueIID
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "`/agent parallel <n>` (owners)") {
		t.Fatalf("the tracking issue offers parallel:\n%s", body)
	}
	planned := e.tasks()[specTaskName].Spec.RunRequest
	e.gl.AddNote(tracking, alice, "/agent parallel 2")
	e.reconcile()
	if got := e.parallelLine(); got != "/agent parallel 2" {
		t.Fatalf("the description's line = %q:\n%s", got, e.gl.Milestones[0].Description)
	}
	if c := trackingComments(e); !strings.Contains(c, "🔢 Up to 2 issues run at the same time (by @alice).") {
		t.Fatalf("the change is said on the tracking issue:\n%s", c)
	}
	e.reconcile()
	st := e.tasks()[specTaskName]
	if st.Spec.MaxParallel != 2 || e.autopilot().MaxParallel != 2 || st.Spec.RunRequest != planned {
		t.Fatalf("the next poll reads the line and re-plans nothing: task %+v, status %d", st.Spec, e.autopilot().MaxParallel)
	}
	e.wantStates("two at a time", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})

	// Back to 1: #4 waits while #2 runs.
	e.gl.AddNote(tracking, alice, "/agent parallel 1")
	e.reconcile()
	if got := e.parallelLine(); got != "/agent parallel 1" || strings.Count(e.gl.Milestones[0].Description, "/agent parallel") != 1 {
		t.Fatalf("the line is replaced:\n%s", e.gl.Milestones[0].Description)
	}
	e.reconcile()
	if got := e.autopilot().MaxParallel; got != 1 {
		t.Fatalf("limit %d", got)
	}
	e.mergeItem(1)
	e.reconcile()
	e.wantStates("one at a time again", map[int64]aiv1.AutopilotItemState{1: merged, 2: active, 4: pending})

	// The same value again writes and says nothing new.
	desc := e.gl.Milestones[0].Description
	e.gl.AddNote(tracking, alice, "/agent parallel 1")
	e.reconcile()
	if c := trackingComments(e); strings.Count(c, "🔢") != 2 || !strings.Contains(c, "Up to 1 issues run at the same time already.") ||
		e.gl.Milestones[0].Description != desc {
		t.Fatalf("the same value is not written twice:\n%s", c)
	}
}

func TestParallelCommandRefusalsAndUsage(t *testing.T) {
	e := ownedParallelEnv(t, 3)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, mallory, "/agent parallel 1")
	e.reconcile()
	if c := trackingComments(e); !strings.Contains(c, notOwnerParallel) || e.parallelLine() != "" {
		t.Fatalf("a non-owner is refused, the description stays:\n%s\n%s", c, e.gl.Milestones[0].Description)
	}
	for _, arg := range []string{"0", "11", "two", ""} {
		e.gl.AddNote(tracking, alice, "/agent parallel "+arg)
		e.reconcile()
	}
	if c := trackingComments(e); strings.Count(c, "`parallel N` needs a number from 1 to 10, e.g. `/agent parallel 2`.") != 4 || e.parallelLine() != "" {
		t.Fatalf("out of range gets the usage:\n%s", c)
	}
	e.gl.AddNote(tracking, alice, "/agent frobnicate")
	e.reconcile()
	if c := trackingComments(e); !strings.Contains(c, "`/agent parallel N` (owners)") {
		t.Fatalf("an unknown command lists parallel:\n%s", c)
	}
}

// deskSayTo sends Desk commands to the spec task and returns the answers.
func (e *projectEnv) deskSayTo(author string, bodies ...string) []string {
	e.t.Helper()
	st := e.tasks()[specTaskName]
	id := st.Spec.Source.LastChatCommandID
	for _, b := range bodies {
		id++
		st.Spec.Source.ChatMessages = append(st.Spec.Source.ChatMessages,
			aiv1.ChatMessage{ID: id, Kind: "command", Text: b, Author: author, AuthorName: "Ada"})
	}
	before := len(st.Spec.Source.ChatAnswers)
	if err := e.c.Update(e.ctx, &st); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
	var got []string
	for _, a := range e.tasks()[specTaskName].Spec.Source.ChatAnswers[before:] {
		got = append(got, a.Text)
	}
	return got
}

func TestDeskParallelCommand(t *testing.T) {
	e := ownedParallelEnv(t, 3)
	if got := e.deskSayTo("eve@x", `{"cmd":"parallel","parallel":2}`); !slices.Equal(got, []string{notOwnerParallel}) || e.parallelLine() != "" {
		t.Fatalf("a non-owner is refused: %q\n%s", got, e.gl.Milestones[0].Description)
	}
	if got := e.deskSayTo("ada@x", `{"cmd":"parallel","parallel":11}`); len(got) != 1 || !strings.Contains(got[0], "parallel needs only parallel, from 1 to 10") {
		t.Fatalf("out of range: %q", got)
	}
	if got := e.deskSayTo("ada@x", `{"cmd":"parallel","parallel":2}`); !slices.Equal(got, []string{"Up to 2 issues run at the same time."}) {
		t.Fatalf("answers = %q", got)
	}
	if got := e.parallelLine(); got != "/agent parallel 2" {
		t.Fatalf("the description's line = %q", got)
	}
	if c := trackingComments(e); !strings.Contains(c, "🔢 Up to 2 issues run at the same time (by Ada via the Desk).") {
		t.Fatalf("the change is said on the tracking issue:\n%s", c)
	}
	e.reconcile()
	if got := e.autopilot().MaxParallel; got != 2 {
		t.Fatalf("the next poll reads the line: %d", got)
	}
	if got := e.deskSayTo("ada@x", `{"cmd":"parallel","parallel":2}`); !slices.Equal(got, []string{"Up to 2 issues run at the same time already."}) ||
		strings.Count(trackingComments(e), "🔢") != 1 {
		t.Fatalf("the same value is not written twice: %q", got)
	}
}
