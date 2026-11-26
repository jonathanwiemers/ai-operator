package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// withLoose switches the loose issues queue on (and polls, so its Queue task
// exists) or leaves it off.
func (e *projectEnv) withLoose(on bool) {
	e.t.Helper()
	if !on {
		return
	}
	e.setLoose(true)
	e.reconcile()
	e.queue()
}

// handedOverLoose asserts that flaky issue #iid went into the loose issues
// queue, taken by by, with the operator's task, not into the milestone's
// queue, and returns its queue item.
func (e *projectEnv) handedOverLoose(iid int64, by string) aiv1.AutopilotItem {
	e.t.Helper()
	e.handedOver(iid, 0)
	it := e.looseItem(iid)
	if !it.FlakyFix || it.TakenBy != by || it.TakenAt == nil {
		e.t.Fatalf("the queue item is a flaky fix taken by %s: %+v", by, it)
	}
	if ap := e.autopilot(); slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == iid }) {
		e.t.Fatalf("the running milestone is not touched: %+v", ap.Items)
	}
	return it
}

// lastNote is the bot's newest comment on issue iid.
func (e *projectEnv) lastNote(iid int64) string {
	e.t.Helper()
	notes := botComments(e, iid)
	if len(notes) == 0 {
		e.t.Fatalf("no comment of the bot on #%d", iid)
	}
	return notes[len(notes)-1]
}

func TestAutoFixFlakyWithTheLooseQueue(t *testing.T) {
	for _, loose := range []bool{false, true} {
		t.Run(fmt.Sprintf("looseIssues.enabled=%v", loose), func(t *testing.T) {
			e := newPilotEnv(t)
			e.withLoose(loose)
			e.setMainHealth(aiv1.MainHealthSpec{AutoFixFlaky: true})
			advance := func(d time.Duration) { e.gl.Clock = e.gl.Clock.Add(d) }
			flakyMain(e, "sha1", advance)
			flakyMain(e, "sha2", advance)
			is := e.issuesWith("ci::flaky")[0]
			if !loose {
				e.handedOver(is.IID, 3)
				if !slices.ContainsFunc(e.autopilot().Items, func(it aiv1.AutopilotItem) bool { return it.IID == is.IID && it.FlakyFix }) {
					t.Fatalf("the issue joins the autopilot's queue: %+v", e.autopilot().Items)
				}
				if n := e.lastNote(is.IID); n != "🛠️ Handed to the agent (`mainHealth.autoFixFlaky`), in milestone \"Checkout v2\", queued on its autopilot." {
					t.Fatalf("the note names the milestone: %s", n)
				}
				return
			}
			e.handedOverLoose(is.IID, "mainHealth.autoFixFlaky")
			if n := e.lastNote(is.IID); n != "🛠️ Handed to the agent (`mainHealth.autoFixFlaky`), in the loose issues queue." {
				t.Fatalf("the note names the loose issues queue: %s", n)
			}
			spec := e.project().Spec.WithDefaults()
			if _, err := checkFlaky(e.ctx, e.gl, spec, e.gl.Bot.ID, e.project().Status.MainHealth, is.IID, 0, false); err == nil ||
				!strings.Contains(err.Error(), fmt.Sprintf("#%d was handed to the agent already, in the loose issues queue at ", is.IID)) {
				t.Fatalf("a second hand-over points to the queue: %v", err)
			}
			// The queue starts it as any taken issue.
			e.reconcile()
			if it := e.looseItem(is.IID); it.State != aiv1.AutopilotItemActive || !hasAny(*e.gl.Issues[is.IID], spec.Workflow.ActiveLabels()...) {
				t.Fatalf("the queue starts the flaky fix: %+v %v", it, e.gl.Labels(is.IID))
			}
		})
	}
}

func TestPilotFixFlakyWithTheLooseQueue(t *testing.T) {
	for _, loose := range []bool{false, true} {
		t.Run(fmt.Sprintf("looseIssues.enabled=%v", loose), func(t *testing.T) {
			e := newManagedEnv(t)
			e.withLoose(loose)
			e.flakyIssue(40, 2)
			e.proposeFor(0, "event/flaky/901/40@1", aiv1.PilotAction{Tool: "fixFlaky", IssueIID: 40})
			e.reconcile()
			e.reconcile()
			prefix := `🛠️ Handed to the agent (the pilot of milestone "Checkout v2", on its own, audited on the tracking issue), `
			if !loose {
				e.handedOver(40, 3)
				if n := e.lastNote(40); n != prefix+`in milestone "Checkout v2", queued on its autopilot.` {
					t.Fatalf("the note names the milestone: %s", n)
				}
				return
			}
			if it := e.handedOverLoose(40, "the pilot"); it.State == aiv1.AutopilotItemPending && slices.Contains(e.gl.Labels(40), "agent::ready") {
				t.Fatalf("a pending item gets its label when the queue starts it: %v", e.gl.Labels(40))
			}
			if n := e.lastNote(40); n != prefix+"in the loose issues queue." {
				t.Fatalf("the note names the loose issues queue: %s", n)
			}
			if d := e.gl.Issues[40].Description; strings.Contains(d, "ignore previous instructions") {
				t.Fatalf("the log never leaves the occurrence comments: %s", d)
			}

			// A third occurrence is refused with a pointer to the queue.
			e.flakyAgain(40, 902)
			e.proposeFor(0, "event/flaky/902/40@3", aiv1.PilotAction{Tool: "fixFlaky", IssueIID: 40})
			e.reconcile()
			e.reconcile()
			if r := e.lastReply(); !strings.Contains(r, "#40 was handed to the agent already, in the loose issues queue at ") {
				t.Fatalf("a second hand-over is refused with a pointer: %s", r)
			}
		})
	}
}

func TestPilotFixFlakyInAMilestoneIsRefusedWithTheLooseQueue(t *testing.T) {
	e := newManagedEnv(t)
	e.withLoose(true)
	e.flakyIssue(40, 2).Milestone = &e.gl.Milestones[0]
	e.proposeFor(0, "event/flaky/901/40@1", aiv1.PilotAction{Tool: "fixFlaky", IssueIID: 40})
	e.reconcile()
	e.reconcile()
	if r := e.lastReply(); !strings.Contains(r, `(not run: #40 is in milestone "Checkout v2" already)`) {
		t.Fatalf("an issue in a milestone cannot join the loose issues queue: %s", r)
	}
	if flakyLatch(e.project().Status.MainHealth, 40) != nil || slices.ContainsFunc(e.queue().Status.Autopilot.Items, func(it aiv1.AutopilotItem) bool { return it.IID == 40 }) {
		t.Fatalf("nothing is handed over: %+v", e.queue().Status.Autopilot.Items)
	}
}

func TestPilotFixFlakyOnAnOwnersYesWithTheLooseQueue(t *testing.T) {
	for _, loose := range []bool{false, true} {
		t.Run(fmt.Sprintf("looseIssues.enabled=%v", loose), func(t *testing.T) {
			e := newProposalEnv(t)
			e.withLoose(loose)
			e.flakyIssue(40, 2)
			e.propose("fix the flaky test", aiv1.PilotAction{Tool: "fixFlaky", IssueIID: 40})
			e.reconcile()
			e.reconcile()
			if !strings.Contains(e.promptNote("Q1").Body, "**fixFlaky** #40") || flakyLatch(e.project().Status.MainHealth, 40) != nil {
				t.Fatal("the pilot proposes, nothing runs without an answer")
			}
			e.choose("Q1", "A", alice)
			e.reconcile()
			if !slices.Contains(e.gl.Labels(40), "agent::ready") {
				t.Fatalf("an owner's choice starts at once: %v", e.gl.Labels(40))
			}
			n := e.lastNote(40)
			if !loose {
				e.handedOver(40, 3)
				if !strings.HasSuffix(n, "in milestone \"Checkout v2\", queued on its autopilot, with `agent::ready`.") {
					t.Fatalf("the note names the milestone: %s", n)
				}
				return
			}
			if it := e.handedOverLoose(40, "@alice"); it.State != aiv1.AutopilotItemActive {
				t.Fatalf("an owner's choice is active at once: %+v", it)
			}
			if !strings.Contains(n, "answered by `@alice`") || !strings.HasSuffix(n, "in the loose issues queue, with `agent::ready`.") {
				t.Fatalf("the note names the loose issues queue: %s", n)
			}
		})
	}
}

// A failed description update takes the issue off the loose issues queue
// again, and an issue taken by an owner before only gets the flag.
func TestHandOverFlakyLooseQueueUndoAndTakenBefore(t *testing.T) {
	e := newPilotEnv(t)
	e.withLoose(true)
	e.setMainHealth(aiv1.MainHealthSpec{AutoFixFlaky: true})
	e.flakyIssue(40, 2)
	spec, mh, qt := e.project().Spec.WithDefaults(), &aiv1.MainHealthStatus{}, e.queue()
	is := e.gl.Issues[40]
	e.gl.UpdateErrors = map[int64]error{40: &gitlab.Error{StatusCode: 500, Method: "PUT", Path: "issue"}}
	if err := e.r.handOverFlaky(e.ctx, e.gl, spec, mh, nil, qt, is, "test", "mainHealth.autoFixFlaky", false); err == nil {
		t.Fatal("the failed update is an error")
	}
	if items := e.queue().Status.Autopilot.Items; slices.ContainsFunc(items, func(it aiv1.AutopilotItem) bool { return it.IID == 40 }) || len(mh.FlakyFixes) != 0 {
		t.Fatalf("the failed hand-over leaves the queue as it was: %+v", items)
	}
	e.gl.UpdateErrors = nil

	taken := aiv1.AutopilotItem{IID: 40, State: aiv1.AutopilotItemPending, TakenBy: "@bob"}
	items := looseFlakyItems([]aiv1.AutopilotItem{{IID: 40, State: aiv1.AutopilotItemMerged}, taken}, 40, "the pilot", e.project().CreationTimestamp, false)
	if len(items) != 2 || !items[1].FlakyFix || items[1].TakenBy != "@bob" || items[1].State != aiv1.AutopilotItemPending {
		t.Fatalf("an issue in the queue already only gets the flag: %+v", items)
	}
	items = looseFlakyItems([]aiv1.AutopilotItem{{IID: 40, State: aiv1.AutopilotItemMerged}}, 40, "@alice", e.project().CreationTimestamp, true)
	if len(items) != 1 || items[0].State != aiv1.AutopilotItemActive || !items[0].FlakyFix || items[0].TakenBy != "@alice" {
		t.Fatalf("a merged item is taken again as a new one: %+v", items)
	}
}
