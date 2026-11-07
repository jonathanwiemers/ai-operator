package history

import (
	"context"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/pricing"
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

	// KindCheckpoint is a pushed checkpoint no agent call carries (budget
	// stop, lost pod, OOM kill): status.run.runCheckpoints.
	KindCheckpoint = "checkpoint"

	// KindAutopilotPaused is a new autopilot pause, KindAutopilotResumed
	// its end with who resolved it (aiv1.PauseResolver).
	KindAutopilotPaused  = "autopilot.paused"
	KindAutopilotResumed = "autopilot.resumed"

	// KindPilotWriteFailed is a status write the API server rejected.
	KindPilotWriteFailed = "pilot.writeFailed"

	KindPromptPosted   = "prompt.posted"
	KindPromptAnswered = "prompt.answered"
	KindPromptClosed   = "prompt.closed"

	// KindRetrospective is a milestone's retrospective, posted on its
	// tracking issue: once per milestone, when the spec task gets its note.
	KindRetrospective = "retrospective"
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
// cache, and the calls of every AgentTask the recorder has not seen. Adds
// (start, resync) record no events, only real changes do; they record the
// task's calls and call-less checkpoints, idempotently, as does the
// recorder's start for every task in the cache. Adds and updates of spec
// tasks record their milestone's snapshot, as does the start; deleting one
// closes it.
func Watch(ctx context.Context, c cache.Cache, r *Recorder, clk func() time.Time) error {
	inf, err := c.GetInformer(ctx, &aiv1.AgentTask{})
	if err != nil {
		return err
	}
	list := func(ctx context.Context) ([]aiv1.AgentTask, error) {
		var tasks aiv1.AgentTaskList
		err := c.List(ctx, &tasks)
		return tasks.Items, err
	}
	r.existing = func(ctx context.Context) ([]store.Call, error) {
		tasks, err := list(ctx)
		var out []store.Call
		for i := range tasks {
			out = append(out, Calls(nil, &tasks[i])...)
		}
		return out, err
	}
	r.existingEvents = func(ctx context.Context) ([]store.Event, error) {
		tasks, err := list(ctx)
		var out []store.Event
		for i := range tasks {
			out = append(out, Checkpoints(nil, &tasks[i], clk())...)
		}
		return out, err
	}
	r.existingSnaps = func(ctx context.Context) ([]store.MilestoneSnapshot, error) {
		tasks, err := list(ctx)
		var out []store.MilestoneSnapshot
		for i := range tasks {
			if m, ok := Snapshot(&tasks[i], "", clk()); ok {
				out = append(out, m)
			}
		}
		return out, err
	}
	d := NewDiffer()
	_, err = inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(o any) {
			if t, ok := o.(*aiv1.AgentTask); ok {
				for _, call := range Calls(nil, t) {
					r.RecordCall(call)
				}
				// Idempotent like the calls: checkpoints pushed while the
				// operator did not watch.
				for _, e := range Checkpoints(nil, t, clk()) {
					r.Record(e)
				}
				r.snapshotTask(t, clk())
			}
		},
		UpdateFunc: func(o, n any) {
			old, ok1 := o.(*aiv1.AgentTask)
			cur, ok2 := n.(*aiv1.AgentTask)
			if ok1 && ok2 {
				for _, e := range d.Update(old, cur, clk()) {
					r.Record(e)
				}
				for _, call := range Calls(old, cur) {
					r.RecordCall(call)
				}
				r.snapshotTask(cur, clk())
			}
		},
		DeleteFunc: func(o any) {
			if t, ok := o.(toolscache.DeletedFinalStateUnknown); ok {
				o = t.Obj
			}
			if t, ok := o.(*aiv1.AgentTask); ok {
				r.Record(d.Delete(t, clk()))
				r.closeTask(t, clk())
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
		if s := textutil.Truncate(ns.Run.Summary, maxText); s != "" {
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
				map[string]any{"reason": reason, "message": textutil.Truncate(ns.Run.Summary, maxText)})
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
			map[string]any{"reason": "blocked: " + string(b.Reason), "message": textutil.Truncate(b.Message, maxText)})
	}
	if ns.Phase == aiv1.TaskPhaseBlocked && prev.Phase != aiv1.TaskPhaseBlocked {
		add(fmt.Sprintf("needs/phase/%d", ns.Run.RunCount), KindNeeds, needsSummary("stopped", ref),
			map[string]any{"reason": "stopped", "message": textutil.Truncate(ns.Message, maxText)})
	}
	out = append(out, d.autopilot(cur, prev.Autopilot, ns.Autopilot, now)...)
	out = append(out, pauses(cur, prev.Autopilot, ns.Autopilot, now)...)
	out = append(out, d.proposals(cur, prev.Pilot, ns.Pilot, now)...)
	out = append(out, writeFailures(cur, prev.WriteFailures, ns.WriteFailures, now)...)
	out = append(out, d.prompts(cur, prev.Prompts, ns.Prompts, now)...)
	out = append(out, retrospective(cur, prev.Pilot, ns.Pilot, now)...)
	out = append(out, Checkpoints(old, cur, now)...)
	return out
}

// retrospective emits the event of a spec task's retrospective when its
// note ID first appears. The key holds only the note, so a milestone gets
// one event even when its spec task is recreated.
func retrospective(t *aiv1.AgentTask, old, cur *aiv1.PilotStatus, now time.Time) []store.Event {
	if t.Spec.Type != aiv1.TaskTypeSpec || cur == nil || cur.Retrospective == nil || cur.Retrospective.NoteID == 0 {
		return nil
	}
	if old != nil && old.Retrospective != nil && old.Retrospective.NoteID != 0 {
		return nil
	}
	r := cur.Retrospective
	e := base(t, now)
	e.Key, e.Kind, e.Summary = fmt.Sprintf("retro/%d", r.NoteID), KindRetrospective, "Retrospective posted"
	if ap := t.Status.Autopilot; ap != nil && ap.TrackingIssueIID != 0 {
		e.IssueIID = ap.TrackingIssueIID
	}
	e.Data = map[string]any{"noteID": r.NoteID, "followUps": len(r.FollowUps)}
	if r.PostedAt != nil {
		e.At = r.PostedAt.Time
	}
	return []store.Event{e}
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
			e.Data["reason"] = textutil.Truncate(cur.PausedReason, maxText)
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

// pauses returns the autopilot.paused and autopilot.resumed events of the
// change from old to cur. A pause starts when the phase becomes Paused, or
// when the kind (or the clock, PausedAt) changes while paused; a changed
// reason with the same kind is the same pause. The pause it replaces, or
// one the phase leaves, resolves as status.autopilot.resumedBy says, else
// as the owner for Off (the marker was removed) and the operator otherwise.
// Keys hold the pause's start and kind, so a replayed update is no new
// event.
func pauses(t *aiv1.AgentTask, old, cur *aiv1.AutopilotStatus, now time.Time) []store.Event {
	if cur == nil {
		return nil
	}
	was := old != nil && old.Phase == aiv1.AutopilotPaused
	is := cur.Phase == aiv1.AutopilotPaused
	fresh := is && (!was || old.PauseKind != cur.PauseKind ||
		old.PausedAt != nil && cur.PausedAt != nil && !old.PausedAt.Equal(cur.PausedAt))
	var out []store.Event
	emit := func(kind string, p *aiv1.AutopilotStatus, summary string) *store.Event {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("%s/%s/%s", kind, pauseStart(t, p), p.PauseKind))
		e.Kind, e.IssueIID, e.Summary = kind, cur.TrackingIssueIID, textutil.Truncate(summary, maxText)
		e.Data = map[string]any{"kind": string(p.PauseKind), "reason": textutil.Truncate(p.PausedReason, maxText), "item": p.PausedFor}
		out = append(out, e)
		return &out[len(out)-1]
	}
	if was && (!is || fresh) {
		by := aiv1.ResolvedByOperator
		// A pause replaced by a new one ended by itself (causeGone).
		via := aiv1.ResumedViaCauseGone
		switch {
		case !is && cur.ResumedBy != "":
			by = cur.ResumedBy
			if cur.ResumedVia != "" {
				via = cur.ResumedVia
			}
		case cur.Phase == aiv1.AutopilotOff:
			by, via = aiv1.ResolvedByOwner, aiv1.ResumedViaCommand
		}
		e := emit(KindAutopilotResumed, old, fmt.Sprintf("Autopilot resumed (%s) by %s", pauseWord(old), by))
		e.Data["resolvedBy"] = string(by)
		e.Data["via"] = string(via)
		if old.PausedAt != nil {
			e.Data["pausedSec"] = int64(now.Sub(old.PausedAt.Time).Seconds())
		}
	}
	if fresh {
		e := emit(KindAutopilotPaused, cur, fmt.Sprintf("Autopilot paused (%s): %s", pauseWord(cur), cur.PausedReason))
		if cur.PausedAt != nil {
			e.At = cur.PausedAt.Time
		}
	}
	return out
}

// pauseStart is the start of the pause in p for its event keys: PausedAt,
// or the task's resource version for a pause from before PausedAt existed.
func pauseStart(t *aiv1.AgentTask, p *aiv1.AutopilotStatus) string {
	if p.PausedAt == nil {
		return "rv" + t.ResourceVersion
	}
	return strconv.FormatInt(p.PausedAt.Unix(), 10)
}

// pauseWord is the kind of the pause in p for a summary.
func pauseWord(p *aiv1.AutopilotStatus) string {
	if p.PauseKind == "" {
		return "unknown"
	}
	return string(p.PauseKind)
}

// writeFailures emits an event for every failure that is new or whose count
// grew.
func writeFailures(t *aiv1.AgentTask, old, cur []aiv1.WriteFailure, now time.Time) []store.Event {
	was := map[string]int32{}
	for _, f := range old {
		was[f.Key] = f.Count
	}
	var out []store.Event
	for _, f := range cur {
		if n, ok := was[f.Key]; ok && f.Count <= n {
			continue
		}
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("writefail/%s/%d", f.Key, f.Count))
		e.Kind = KindPilotWriteFailed
		e.Summary = "Status write rejected: " + f.Source
		e.Data = map[string]any{"source": f.Source, "error": textutil.Truncate(f.Error, maxText), "count": f.Count}
		if f.At != nil {
			e.At = f.At.Time
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
		e.Summary = fmt.Sprintf("P%d %s: %s", p.ID, state, textutil.Truncate(p.Summary, maxText))
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

// prompts returns the events of the prompts that were posted, answered or
// closed from old to cur. A prompt that left the bounded list emits nothing.
// Free text and context stay out of the data: they are untrusted and the
// status has them.
func (d *Differ) prompts(t *aiv1.AgentTask, old, cur []aiv1.Prompt, now time.Time) []store.Event {
	was := map[string]aiv1.PromptState{}
	for _, p := range old {
		was[p.ID] = p.State
	}
	var out []store.Event
	emit := func(p aiv1.Prompt, state aiv1.PromptState) {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("prompt/%s/%s", p.ID, state))
		e.Kind = KindPromptPosted
		data := map[string]any{"promptID": p.ID, "promptKind": string(p.Source.Kind), "state": string(state)}
		if p.Source.IssueIID != 0 {
			e.IssueIID = p.Source.IssueIID
		}
		if p.Source.MRIID != 0 {
			e.MergeRequestIID = p.Source.MRIID
		}
		data["issueIID"], data["mrIID"] = e.IssueIID, e.MergeRequestIID
		switch state {
		case aiv1.PromptOpen:
			e.Summary = fmt.Sprintf("%s posted: %s", p.ID, textutil.Truncate(p.Question, maxText))
			if p.PostedAt != nil {
				e.At = p.PostedAt.Time
			}
		case aiv1.PromptAnswered:
			e.Kind = KindPromptAnswered
			label := ""
			a := p.Answer
			if a != nil {
				for _, o := range p.Options {
					if o.ID == a.OptionID {
						label = o.Label
					}
				}
				data["optionID"], data["optionLabel"], data["by"], data["via"] = a.OptionID, label, a.By, a.Via
				e.Actor = a.By
				if !a.At.IsZero() {
					e.At = a.At.Time
				}
			}
			by := ""
			if a != nil && a.By != "" {
				by = " by " + a.By
			}
			if label == "" {
				label = "free text"
			}
			e.Summary = textutil.Truncate(fmt.Sprintf("%s answered%s: %s", p.ID, by, label), maxText)
		default:
			e.Kind = KindPromptClosed
			e.Summary = fmt.Sprintf("%s %s", p.ID, state)
			if p.ClosedAt != nil {
				e.At = p.ClosedAt.Time
			}
		}
		e.Data = data
		out = append(out, e)
	}
	for _, p := range cur {
		if p.ID == "" {
			continue
		}
		prev, seen := was[p.ID]
		if !seen {
			emit(p, aiv1.PromptOpen)
			prev = aiv1.PromptOpen
		}
		if p.State == prev || p.State == aiv1.PromptOpen || p.State == "" {
			continue
		}
		emit(p, p.State)
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

// Calls returns the agent calls of cur whose seq is not among old's calls
// (all of them when old is nil), keyed by the task's UID and the seq. At is
// the call's start, ExpiresAt is left to the recorder.
func Calls(old, cur *aiv1.AgentTask) []store.Call {
	seen := map[int64]bool{}
	if old != nil {
		for _, c := range old.Status.Run.Calls {
			seen[c.Seq] = true
		}
	}
	var out []store.Call
	for _, c := range cur.Status.Run.Calls {
		if c.Seq <= 0 || seen[c.Seq] {
			continue
		}
		e := base(cur, c.At.Time)
		call := StoreCall(c)
		call.Key = fmt.Sprintf("call/%s/%d", cur.UID, c.Seq)
		call.Namespace, call.Project, call.Task = e.Namespace, e.Project, e.Task
		call.MilestoneIID, call.IssueIID, call.MergeRequestIID = e.MilestoneIID, e.IssueIID, e.MergeRequestIID
		out = append(out, call)
	}
	return out
}

// StoreCall is c as a store row without its key and the namespace,
// project, task and IIDs it belongs to.
func StoreCall(c aiv1.AgentCall) store.Call {
	call := store.Call{At: c.At.Time,
		Run: c.Run, Seq: c.Seq, Kind: c.Kind, Role: c.Role, Engine: string(c.Engine), Model: c.Model,
		Effort: c.Effort, Resumed: c.Resumed, FallbackFrom: string(c.FallbackFrom),
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens,
		CacheReadTokens: c.CacheReadTokens, CacheWriteTokens: c.CacheWriteTokens,
		CostSource: c.CostSource, Turns: c.Turns, DurationMS: c.DurationMS,
		SubagentCalls: c.SubagentCalls, ReadGateBlocks: c.ReadGateBlocks, ContextSeries: c.ContextSeries, SubagentTurns: c.SubagentTurns,
		Checkpoint: c.Checkpoint, CheckpointSHA: c.CheckpointSHA, LinesAdded: c.LinesAdded, LinesDeleted: c.LinesDeleted,
		StepGateNudges: c.StepGateNudges}
	switch c.CostSource {
	case aiv1.CostReported, aiv1.CostEstimated:
		call.CostUSD = costOf(c.CostUSD)
	default:
		// The table knows three sources; anything else is unknown.
		call.CostSource = aiv1.CostUnknown
	}
	for _, m := range c.Models {
		call.Models = append(call.Models, store.CallModel{Model: m.Model,
			InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadTokens, CacheWriteTokens: m.CacheWriteTokens,
			CostUSD: costOf(m.CostUSD)})
	}
	return call
}

// Estimate prices c from table when its cost is unknown and it has tokens:
// the cost becomes the estimate, the source estimated. Reported costs and
// calls without tokens or price stay as they are; a nil table prices none.
func Estimate(c *store.Call, table *pricing.Table) {
	if c.CostSource != aiv1.CostUnknown {
		return
	}
	if usd, ok := table.Estimate(c.Engine, c.Model, c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens); ok {
		c.CostUSD, c.CostSource = &usd, aiv1.CostEstimated
	}
}

// costOf is the cost string s in USD, nil when it is empty.
func costOf(s string) *float64 {
	if s == "" {
		return nil
	}
	v := aiv1.CostOf(s)
	return &v
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

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Checkpoints returns the events of cur's call-less checkpoints
// (status.run.runCheckpoints) that old does not have (all of them when old
// is nil), one per checkpoint, at its push (now if unknown). The key holds
// the run and the commit, so a replayed update or an add of a task already
// seen is no new event. The spend queries count these with the calls that
// carry a checkpoint.
func Checkpoints(old, cur *aiv1.AgentTask, now time.Time) []store.Event {
	type id struct {
		run int64
		sha string
	}
	seen := map[id]bool{}
	if old != nil {
		for _, c := range old.Status.Run.RunCheckpoints {
			seen[id{c.Run, c.SHA}] = true
		}
	}
	var out []store.Event
	for _, c := range cur.Status.Run.RunCheckpoints {
		if c.SHA == "" || seen[id{c.Run, c.SHA}] {
			continue
		}
		e := base(cur, now)
		if !c.At.IsZero() {
			e.At = c.At.Time
		}
		e.Key = taskKey(cur, fmt.Sprintf("checkpoint/%d/%s", c.Run, c.SHA))
		e.Kind = KindCheckpoint
		e.Summary = fmt.Sprintf("Checkpoint %s (%s) on %s", short(c.SHA), c.Reason, issueRef(cur))
		e.Data = map[string]any{"reason": c.Reason, "sha": c.SHA, "run": c.Run, "step": c.Step,
			"files": c.Files, "added": c.Added, "deleted": c.Deleted}
		out = append(out, e)
	}
	return out
}
