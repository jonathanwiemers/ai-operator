package history

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// The kinds of events.
const (
	KindNote        = "note"
	KindRunStarted  = "run.started"
	KindRunFinished = "run.finished"
	KindPush        = "push"
	KindCI          = "ci"
	KindReview      = "review"
	KindNeeds       = "needs"
	KindMerged      = "merged"
	KindClosed      = "closed"
	KindAutopilot   = "autopilot"
	KindProposal    = "proposal"
)

// maxText bounds the texts copied from the status into an event.
const maxText = 4000

// Differ turns changes of AgentTasks into events. It remembers when each
// task's current run started, for the run's duration.
type Differ struct {
	mu      sync.Mutex
	started map[types.UID]time.Time
}

// NewDiffer returns an empty differ.
func NewDiffer() *Differ { return &Differ{started: map[types.UID]time.Time{}} }

// Watch records the events of every AgentTask update and deletion in the
// cache. Adds (start, resync) record nothing: only real changes do.
func Watch(ctx context.Context, informers cache.Informers, r *Recorder, clk func() time.Time) error {
	inf, err := informers.GetInformer(ctx, &aiv1.AgentTask{})
	if err != nil {
		return err
	}
	d := NewDiffer()
	_, err = inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: func(o, n any) {
			old, ok1 := o.(*aiv1.AgentTask)
			cur, ok2 := n.(*aiv1.AgentTask)
			if ok1 && ok2 {
				for _, e := range d.Update(old, cur, clk()) {
					r.Record(e)
				}
			}
		},
		DeleteFunc: func(o any) {
			if t, ok := o.(toolscache.DeletedFinalStateUnknown); ok {
				o = t.Obj
			}
			if t, ok := o.(*aiv1.AgentTask); ok {
				r.Record(d.Delete(t, clk()))
			}
		},
	})
	return err
}

// Update returns the events of the change from old to cur at now.
func (d *Differ) Update(old, cur *aiv1.AgentTask, now time.Time) []store.Event {
	if old.ResourceVersion == cur.ResourceVersion {
		return nil
	}
	prev, ns := &old.Status, &cur.Status
	var out []store.Event
	add := func(key, kind, summary string, data map[string]any) *store.Event {
		e := base(cur, now)
		e.Key, e.Kind, e.Summary, e.Data = taskKey(cur, key), kind, summary, data
		out = append(out, e)
		return &out[len(out)-1]
	}
	ref := issueRef(cur)

	if ns.Phase == aiv1.TaskPhaseRunning && prev.Phase != aiv1.TaskPhaseRunning {
		d.mu.Lock()
		d.started[cur.UID] = now
		d.mu.Unlock()
		add(fmt.Sprintf("run/%d/start", cur.Spec.RunRequest), KindRunStarted, "Run started on "+ref,
			map[string]any{"request": cur.Spec.RunRequest})
	}
	if ns.Run.RunCount > prev.Run.RunCount {
		data := map[string]any{"result": string(ns.Run.LastResult), "runCount": ns.Run.RunCount}
		if s := clip(ns.Run.Summary); s != "" {
			data["runSummary"] = s
		}
		if c, ok := costDelta(prev.Run.CostUSD, ns.Run.CostUSD); ok {
			data["costUSD"] = c
		}
		d.mu.Lock()
		if t, ok := d.started[cur.UID]; ok {
			data["durationSec"] = int64(now.Sub(t).Seconds())
			delete(d.started, cur.UID)
		}
		d.mu.Unlock()
		summary := fmt.Sprintf("Run %d on %s", ns.Run.RunCount, ref)
		if ns.Run.LastResult != "" {
			summary += ": " + string(ns.Run.LastResult)
		}
		add(fmt.Sprintf("run/%d", ns.Run.RunCount), KindRunFinished, summary, data)
		switch ns.Run.LastResult {
		case aiv1.RunResultNeedsInput, aiv1.RunResultFailed:
			reason := "question"
			if ns.Run.LastResult == aiv1.RunResultFailed {
				reason = "failed"
			}
			add(fmt.Sprintf("needs/run/%d", ns.Run.RunCount), KindNeeds, needsSummary(reason, ref),
				map[string]any{"reason": reason, "message": clip(ns.Run.Summary)})
		}
	}
	if ns.Run.PushedSHA != "" && ns.Run.PushedSHA != prev.Run.PushedSHA {
		e := add("push/"+ns.Run.PushedSHA, KindPush, fmt.Sprintf("Pushed %s to %s", short(ns.Run.PushedSHA), ref),
			map[string]any{"sha": ns.Run.PushedSHA, "branch": ns.Run.Branch})
		if ns.Run.PushedAt != nil {
			e.At = ns.Run.PushedAt.Time
		}
	}
	if c := ns.CI; c != nil && c.State != aiv1.CIStateNone && (c.State.Final() || c.State == aiv1.CIStateFixing) &&
		(prev.CI == nil || prev.CI.State != c.State || prev.CI.PipelineID != c.PipelineID) {
		id := strconv.FormatInt(c.PipelineID, 10)
		if c.PipelineID == 0 {
			id = c.SHA
		}
		data := map[string]any{"state": string(c.State), "pipelineID": c.PipelineID, "round": ns.Run.CIRound}
		if c.PipelineURL != "" {
			data["pipelineURL"] = c.PipelineURL
		}
		if len(c.FailedJobs) > 0 {
			data["failedJobs"] = c.FailedJobs
		}
		add("ci/"+id+"/"+string(c.State), KindCI, fmt.Sprintf("CI %s on %s", ciWord(c.State), ref), data)
	}
	if ns.Run.ReviewedSHA != "" && ns.Run.ReviewedSHA != prev.Run.ReviewedSHA {
		add("review/"+ns.Run.ReviewedSHA, KindReview,
			fmt.Sprintf("Review of %s: %d blocking", ref, ns.Run.BlockingFindings),
			map[string]any{"sha": ns.Run.ReviewedSHA, "blocking": ns.Run.BlockingFindings, "reviewErrors": ns.Run.ReviewErrors})
	}
	if b := ns.Blocked; b != nil && (prev.Blocked == nil || !prev.Blocked.Since.Equal(&b.Since)) {
		add(fmt.Sprintf("needs/blocked/%d", b.Since.Unix()), KindNeeds, needsSummary("blocked", ref),
			map[string]any{"reason": "blocked: " + string(b.Reason), "message": clip(b.Message)})
	}
	if ns.Phase == aiv1.TaskPhaseBlocked && prev.Phase != aiv1.TaskPhaseBlocked {
		add(fmt.Sprintf("needs/phase/%d", ns.Run.RunCount), KindNeeds, needsSummary("stopped", ref),
			map[string]any{"reason": "stopped", "message": clip(ns.Message)})
	}
	out = append(out, d.autopilot(cur, prev.Autopilot, ns.Autopilot, now)...)
	out = append(out, d.proposals(cur, prev.Pilot, ns.Pilot, now)...)
	return out
}

func (d *Differ) autopilot(t *aiv1.AgentTask, old, cur *aiv1.AutopilotStatus, now time.Time) []store.Event {
	if cur == nil {
		return nil
	}
	var out []store.Event
	if old == nil || old.Phase != cur.Phase {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("autopilot/%s/%s", cur.Phase, t.ResourceVersion))
		e.Kind, e.IssueIID = KindAutopilot, cur.TrackingIssueIID
		e.Summary = fmt.Sprintf("Autopilot %s", strings.ToLower(string(cur.Phase)))
		e.Data = map[string]any{"phase": string(cur.Phase)}
		if cur.PausedReason != "" {
			e.Data["reason"] = clip(cur.PausedReason)
		}
		out = append(out, e)
	}
	was := map[int64]aiv1.AutopilotItemState{}
	if old != nil {
		for _, it := range old.Items {
			was[it.IID] = it.State
		}
	}
	for _, it := range cur.Items {
		if it.State != aiv1.AutopilotItemMerged || was[it.IID] == aiv1.AutopilotItemMerged {
			continue
		}
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("merged/%d", it.IID))
		e.Kind, e.IssueIID, e.MergeRequestIID = KindMerged, it.IID, it.MergeRequestIID
		e.Summary = fmt.Sprintf("Merged !%d (#%d)", it.MergeRequestIID, it.IID)
		e.Data = map[string]any{"sha": it.MergedSHA}
		if it.MergeRequestURL != "" {
			e.Data["mergeRequestURL"] = it.MergeRequestURL
		}
		out = append(out, e)
	}
	return out
}

func (d *Differ) proposals(t *aiv1.AgentTask, old, cur *aiv1.PilotStatus, now time.Time) []store.Event {
	if cur == nil {
		return nil
	}
	var oldOpen, oldAccepted map[int64]bool
	if old != nil {
		oldOpen, oldAccepted = ids(old.Proposals), ids(old.Accepted)
	}
	curAccepted := ids(cur.Accepted)
	var out []store.Event
	emit := func(p aiv1.PilotProposal, state string) {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("proposal/%d/%s", p.ID, state))
		e.Kind = KindProposal
		e.Summary = fmt.Sprintf("P%d %s: %s", p.ID, state, clip(p.Summary))
		e.Data = map[string]any{"proposalID": p.ID, "proposalState": state}
		if p.AcceptedBy != "" {
			e.Actor = p.AcceptedBy
		}
		if state == "posted" && p.PostedAt != nil {
			e.At = p.PostedAt.Time
		}
		out = append(out, e)
	}
	for _, p := range cur.Proposals {
		if p.ID != 0 && !oldOpen[p.ID] {
			emit(p, "posted")
		}
	}
	for _, p := range cur.Accepted {
		if p.ID != 0 && !oldAccepted[p.ID] {
			emit(p, "accepted")
		}
	}
	if old != nil {
		curOpen := ids(cur.Proposals)
		for _, p := range old.Proposals {
			if p.ID != 0 && !curOpen[p.ID] && !curAccepted[p.ID] && !oldAccepted[p.ID] {
				emit(p, "closed")
			}
		}
	}
	return out
}

// Delete returns the event of a task that is gone: its issue was closed or
// left the agent's scope.
func (d *Differ) Delete(t *aiv1.AgentTask, now time.Time) store.Event {
	d.mu.Lock()
	delete(d.started, t.UID)
	d.mu.Unlock()
	e := base(t, now)
	e.Key, e.Kind, e.Summary = taskKey(t, "closed"), KindClosed, issueRef(t)+" closed"
	return e
}

func base(t *aiv1.AgentTask, now time.Time) store.Event {
	e := store.Event{At: now, Namespace: t.Namespace, Project: t.Spec.ProjectRef, Task: t.Name,
		MergeRequestIID: t.Status.Run.MergeRequestIID}
	if t.Spec.Milestone != nil {
		e.MilestoneIID = t.Spec.Milestone.IID
	}
	if t.Spec.Issue != nil {
		e.IssueIID = t.Spec.Issue.IID
	}
	return e
}

func taskKey(t *aiv1.AgentTask, key string) string {
	return fmt.Sprintf("task/%s/%s", t.UID, key)
}

func issueRef(t *aiv1.AgentTask) string {
	switch {
	case t.Spec.Issue != nil:
		return fmt.Sprintf("#%d", t.Spec.Issue.IID)
	case t.Spec.Milestone != nil:
		return fmt.Sprintf("%%%d", t.Spec.Milestone.IID)
	}
	return t.Name
}

func needsSummary(reason, ref string) string {
	switch reason {
	case "question":
		return ref + " asks you"
	case "failed":
		return ref + " failed"
	case "blocked":
		return ref + " is blocked"
	}
	return ref + " stopped"
}

func ciWord(s aiv1.CIState) string {
	switch s {
	case aiv1.CIStateSuccess:
		return "passed"
	case aiv1.CIStateFailed:
		return "failed"
	case aiv1.CIStateFixing:
		return "red, fixing"
	case aiv1.CIStateBaseFailing:
		return "red on the base"
	case aiv1.CIStateCanceled:
		return "canceled"
	}
	return strings.ToLower(string(s))
}

// costDelta is the spend of the run that took the accumulated cost from
// before to after, as a decimal string.
func costDelta(before, after string) (string, bool) {
	d := aiv1.CostOf(after) - aiv1.CostOf(before)
	if d <= 0 {
		return "", false
	}
	return aiv1.FormatCost(d), true
}

func ids(ps []aiv1.PilotProposal) map[int64]bool {
	m := make(map[int64]bool, len(ps))
	for _, p := range ps {
		m[p.ID] = true
	}
	return m
}

func clip(s string) string {
	if len(s) <= maxText {
		return s
	}
	// Cut at a rune boundary.
	i := maxText
	for i > 0 && s[i]&0xC0 == 0x80 {
		i--
	}
	return s[:i] + "…"
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
