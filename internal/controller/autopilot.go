package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/conventional"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// syncAutopilot drives an autopilot milestone, one step per poll:
//
//	no active issue         -> move the next issue whose dependencies are merged to ready
//	active issue in review  -> merge gate: pipeline green, review clean, MR mergeable -> merge
//	MR needs a rebase       -> ask GitLab to rebase, then treat the new head as a push
//	merged                  -> record it, close the issue if needed, next issue
//	needs a human           -> pause with a comment on the tracking issue
//
// The operator only calls the GitLab API here; no model decides whether to
// merge. State lives in the spec task's status.autopilot, the tracking issue
// shows it.
func (r *AgentProjectReconciler) syncAutopilot(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, pilot *aiv1.AgentTask, lane bool, ahead []int64) error {
	switch {
	case st.Spec.Milestone == nil:
		return nil
	case !st.Spec.Autopilot && (st.Status.Autopilot == nil || st.Status.Autopilot.Phase == aiv1.AutopilotOff):
		// Never on, or already off: Desk verbs sent meanwhile get an answer.
		return r.refuseChatCommands(ctx, st, "The autopilot is off for this milestone.")
	}
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	if st.Status.Autopilot != nil {
		ap = st.Status.Autopilot.DeepCopy()
	}
	a := &autopilot{r: r, gl: gl, gate: gate, project: project, spec: spec, st: st, ap: ap, tasks: tasks, pilotTask: pilot, lane: lane, ahead: ahead,
		log: log.FromContext(ctx).WithValues("milestone", st.Spec.Milestone.Title)}
	err := a.sync(ctx)
	// Save what was done even if a later step failed: its GitLab side
	// effects (a created tracking issue, a requested rebase) have happened.
	if serr := a.save(ctx); err == nil {
		err = serr
	}
	// Desk answers after the status: the verbs are idempotent, so a restart
	// between the two writes applies a verb again and answers once. Re-get
	// so a status patch response cannot wipe the Spec fields.
	if err == nil && a.chatMark > 0 {
		var fresh aiv1.AgentTask
		if err = r.Get(ctx, client.ObjectKeyFromObject(st), &fresh); err == nil {
			// Appended to the fresh list: chatreply may have dropped
			// answered ones meanwhile.
			fresh.Spec.Source.ChatAnswers = append(fresh.Spec.Source.ChatAnswers, a.chatAnswers...)
			fresh.Spec.Source.LastChatCommandID = a.chatMark
			if n := len(fresh.Spec.Source.ChatAnswers); n > maxChatAnswers {
				fresh.Spec.Source.ChatAnswers = append([]aiv1.ChatReply(nil), fresh.Spec.Source.ChatAnswers[n-maxChatAnswers:]...)
			}
			err = r.Update(ctx, &fresh)
			if err == nil {
				st.Spec.Source.ChatAnswers = fresh.Spec.Source.ChatAnswers
				st.Spec.Source.LastChatCommandID = fresh.Spec.Source.LastChatCommandID
			}
		}
	}
	if err == nil && !st.Spec.Autopilot {
		// Turned off by this sync: verbs that came too late are answered.
		err = r.refuseChatCommands(ctx, st, "The autopilot is off for this milestone.")
	}
	return err
}

// refuseChatCommands answers the Desk commands of task newer than its
// LastChatCommandID with text, without applying them, so they do not stay
// unanswered on a task that no longer handles them.
func (r *AgentProjectReconciler) refuseChatCommands(ctx context.Context, task *aiv1.AgentTask, text string) error {
	src := &task.Spec.Source
	var answers []aiv1.ChatReply
	mark := src.LastChatCommandID
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			answers = append(answers, aiv1.ChatReply{MessageID: m.ID, Text: text, At: metav1.Now()})
			mark = max(mark, m.ID)
		}
	}
	if len(answers) == 0 {
		return nil
	}
	src.ChatAnswers = append(src.ChatAnswers, answers...)
	src.LastChatCommandID = mark
	if n := len(src.ChatAnswers); n > maxChatAnswers {
		src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
	}
	return r.Update(ctx, task)
}

func (a *autopilot) sync(ctx context.Context) error {
	if !a.st.Spec.Autopilot {
		return a.off(ctx)
	}
	if a.ap.Phase == aiv1.AutopilotOff {
		a.ap.Phase = aiv1.AutopilotRunning
	}
	issues, err := a.gl.ListIssues(ctx, a.spec.GitLab.Project, gitlab.ListIssuesOptions{Milestone: a.st.Spec.Milestone.Title, State: "all"})
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", a.st.Spec.Milestone.Title, err)
	}
	a.issues = map[int64]gitlab.Issue{}
	for _, is := range issues {
		a.issues[is.IID] = is
	}
	if err := a.loadMainRedFix(ctx); err != nil {
		return err
	}
	order, cycle := queueOrder(a.st.Status.Plan)
	a.ap.Items = mergeQueue(a.ap.Items, order)
	if err := a.ensureTracking(ctx); err != nil {
		return err
	}
	if err := a.repoNotices(ctx); err != nil {
		return err
	}
	if err := a.commands(ctx); err != nil {
		return err
	}
	if err := a.chatCommands(ctx); err != nil {
		return err
	}
	// After the commands: a resume while the budget is spent pauses again.
	if err := a.budget(ctx); err != nil {
		return err
	}
	if err := a.emptyPlan(ctx); err != nil {
		return err
	}
	if len(a.st.Status.Plan) == 0 {
		// Not planned yet, the planning run failed or planned no issues:
		// the tracking issue says so, nothing is picked and the milestone
		// is not done.
		return a.updateTracking(ctx)
	}
	if err := a.autoResume(ctx); err != nil {
		return err
	}
	if err := a.companionLanding(ctx); err != nil {
		return err
	}
	if err := a.unstick(ctx); err != nil {
		return err
	}
	if err := a.freezeOnMainRed(ctx); err != nil {
		return err
	}
	if err := a.healBaseFailing(ctx); err != nil {
		return err
	}
	if err := a.mainRedLane(ctx); err != nil {
		return err
	}
	switch {
	case a.ap.Phase != aiv1.AutopilotRunning && a.ap.Phase != aiv1.AutopilotDone:
	case len(cycle) > 0:
		if err := a.pause(ctx, aiv1.PauseCycle, 0, "the plan has a dependency cycle between "+issueList(cycle)); err != nil {
			return err
		}
	default:
		if err := a.step(ctx); err != nil {
			return err
		}
		if a.ap.Phase == aiv1.AutopilotDone {
			if err := a.closeMilestone(ctx); err != nil {
				return err
			}
		}
	}
	return a.updateTracking(ctx)
}

// emptyPlan pauses a running autopilot whose planning run succeeded but
// planned no issues (decision 8), and lifts that pause once a re-plan is
// pending (the spec was changed) or the plan has issues. An empty re-plan
// pauses again. A spec task that never ran, a pending and a failed planning
// run do not pause.
func (a *autopilot) emptyPlan(ctx context.Context) error {
	run := a.st.Status.Run
	empty := len(a.st.Status.Plan) == 0 && run.ObservedRunRequest > 0 && !a.st.RunPending() && run.LastResult == aiv1.RunResultSucceeded
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PauseKind == aiv1.PauseEmptyPlan {
		if empty {
			return nil
		}
		why := "the milestone is being planned again"
		if len(a.st.Status.Plan) > 0 {
			why = fmt.Sprintf("the milestone was planned again with %d issues", len(a.st.Status.Plan))
		}
		a.log.Info("autopilot continues by itself", "why", why)
		return a.resumed(ctx, aiv1.ResolvedByOperator, "", why)
	}
	if !empty || a.ap.Phase != aiv1.AutopilotRunning {
		return nil
	}
	return a.pause(ctx, aiv1.PauseEmptyPlan, 0, "the planning run succeeded but planned no issues")
}

// off stops autopilot after its marker was removed or the milestone is no
// longer planned, and says so on the tracking issue.
func (a *autopilot) off(ctx context.Context) error {
	a.log.Info("autopilot switched off")
	if a.ap.Phase != aiv1.AutopilotDone {
		if err := a.comment(ctx, "⏹️ Autopilot is off: the marker was removed from the milestone, or the milestone is no longer planned. "+
			"Nothing is merged any more; open issues continue in the normal flow."); err != nil {
			return err
		}
	}
	prev := a.ap.Phase
	a.ap.Phase = aiv1.AutopilotOff // for the description
	if err := a.closeTracking(ctx); err != nil {
		a.ap.Phase = prev // tried again on the next poll
		return err
	}
	// A pause does not outlive off: turned on again, it starts running.
	a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = "", "", 0, false
	return nil
}

// autopilot is one poll of one autopilot milestone.
type autopilot struct {
	r       *AgentProjectReconciler
	gl      gitlab.API
	gate    *commandGate
	project *aiv1.AgentProject
	spec    aiv1.AgentProjectSpec
	st      *aiv1.AgentTask
	ap      *aiv1.AutopilotStatus
	tasks   map[int64]*aiv1.AgentTask
	issues  map[int64]gitlab.Issue
	// pilotTask is the milestone's Coordinate task, if there is one.
	pilotTask *aiv1.AgentTask
	log       logr.Logger
	// proj is read once per poll, when needed.
	proj *gitlab.Project
	// lane: this milestone drives the main-red issue, mainRed while red.
	lane bool
	// ahead are the older autopilot milestones of the project still open
	// (queuedAhead); holding: pick has an issue to start but waits for them.
	ahead   []int64
	holding bool
	mainRed int64
	// New Desk command answers to write after the autopilot status is
	// saved (a status patch must not wipe them from the Spec).
	chatMark    int64
	chatAnswers []aiv1.ChatReply
	// companionOpen caches whether a task's companion merge request is open,
	// by ref; holds are the pending issues a companion held back in this
	// poll's startable, for the tracking issue.
	companionOpen map[string]bool
	holds         map[int64]*openCompanion
}

// queueOrder orders the plan topologically: dependencies first, ties in plan
// order. It returns the issues of a dependency cycle, if there is one.
func queueOrder(plan []aiv1.PlannedIssue) ([]int64, []int64) {
	index := map[int64]int{}
	for i, p := range plan {
		index[p.IID] = i
	}
	waiting := make([]int, len(plan))
	dependants := map[int64][]int64{}
	for _, p := range plan {
		for _, d := range p.DependsOn {
			if _, ok := index[d]; ok {
				waiting[index[p.IID]]++
				dependants[d] = append(dependants[d], p.IID)
			}
		}
	}
	var order []int64
	done := make([]bool, len(plan))
	for len(order) < len(plan) {
		next := -1
		for i := range plan {
			if !done[i] && waiting[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var cycle []int64
			for i, p := range plan {
				if !done[i] {
					cycle = append(cycle, p.IID)
				}
			}
			return order, cycle
		}
		done[next] = true
		order = append(order, plan[next].IID)
		for _, dep := range dependants[plan[next].IID] {
			waiting[index[dep]]--
		}
	}
	return order, nil
}

// mergeQueue keeps the state of known items and appends new ones in queue
// order. Items the plan dropped stay once they were started, so the history
// is kept; pending ones are dropped with the plan. Revisit follow-ups and
// flaky fixes are not in the plan and always stay.
func mergeQueue(items []aiv1.AutopilotItem, order []int64) []aiv1.AutopilotItem {
	known := map[int64]aiv1.AutopilotItem{}
	for _, it := range items {
		known[it.IID] = it
	}
	out := make([]aiv1.AutopilotItem, 0, len(order))
	seen := map[int64]bool{}
	for _, iid := range order {
		it, ok := known[iid]
		if !ok {
			it = aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemPending}
		}
		out = append(out, it)
		seen[iid] = true
	}
	for _, it := range items {
		if !seen[it.IID] && (it.State != aiv1.AutopilotItemPending || it.RevisitOf != "" || it.FlakyFix) {
			out = append(out, it)
		}
	}
	return out
}

func (a *autopilot) item(iid int64) *aiv1.AutopilotItem {
	if fix := a.ap.MainRedFix; fix != nil && fix.IID == iid {
		return fix
	}
	for i := range a.ap.Items {
		if a.ap.Items[i].IID == iid {
			return &a.ap.Items[i]
		}
	}
	return nil
}

func (a *autopilot) deps(iid int64) []int64 {
	for _, p := range a.st.Status.Plan {
		if p.IID == iid {
			return p.DependsOn
		}
	}
	return nil
}

// step does at most one thing for the queue.
func (a *autopilot) step(ctx context.Context) error {
	for i := range a.ap.Items {
		if a.ap.Items[i].State == aiv1.AutopilotItemActive {
			return a.drive(ctx, &a.ap.Items[i])
		}
	}
	if a.st.RunPending() {
		return nil // the milestone is being planned again; pick from the new plan
	}
	return a.pick(ctx)
}

// pick activates the next pending issue whose dependencies are merged: the
// plan's, and open issues it is linked as "blocked by" to in GitLab
// (Premium), so a link a human adds holds an issue back too (startable).
// With none, it pauses stuck; unstick lifts that pause. An older open
// milestone holds the start back: one open merge request per project, so
// milestones do not rebase each other's reviewed changes away.
func (a *autopilot) pick(ctx context.Context) error {
	s, err := a.startable(ctx)
	if err != nil {
		return err
	}
	wf := a.spec.Workflow
	switch it := s.it; {
	case s.dep != nil:
		// Skipped, then finished by hand: the dependency is met.
		return a.merged(ctx, s.dep, s.mr)
	case it != nil && s.closed && s.done:
		// Done before autopilot got to it (merged in the normal flow, or by
		// hand).
		return a.merged(ctx, it, s.mr)
	case it != nil && s.closed:
		if err := a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed or deleted before autopilot got to it, so it is skipped.", it.IID)); err != nil {
			return err
		}
		it.State = aiv1.AutopilotItemSkipped
		return nil
	case it != nil && len(a.ahead) > 0:
		a.log.Info("autopilot waits for its turn", "issue", it.IID, "behind", a.ahead)
		a.holding = true
		return nil
	case it != nil:
		a.log.Info("autopilot starts the next issue", "issue", it.IID)
		if !hasAny(a.issues[it.IID], wf.ActiveLabels()...) { // else a human already handed it over
			if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
				AddLabels: []string{wf.ReadyLabel}, RemoveLabels: []string{wf.DraftLabel, wf.DoneLabel},
			}); err != nil {
				return fmt.Errorf("moving issue #%d to %q: %w", it.IID, wf.ReadyLabel, err)
			}
		}
		it.State = aiv1.AutopilotItemActive
		a.ap.Phase = aiv1.AutopilotRunning // a re-plan can add issues after "done"
		a.ap.CloseWaitsForCompanions = nil
		return nil
	case len(s.waiting) > 0 && s.landing:
		// A companion that lands by rule holds an issue back: a wait, not a
		// pause; it starts once the companion has landed.
		a.log.Info("autopilot waits for a companion to land", "waiting", s.waiting)
		a.ap.Phase = aiv1.AutopilotRunning
		a.ap.CloseWaitsForCompanions = nil
		return nil
	case len(s.waiting) > 0:
		// Everything left waits for something autopilot does not do: a
		// skipped issue, an issue outside the queue, or a cycle of links.
		return a.pause(ctx, aiv1.PauseStuck, 0, fmt.Sprintf("nothing can start: %s; merge the issues they wait for by hand (or close them with `%s`), "+
			"remove the links, `skip` the waiting ones, `unskip` the skipped ones or let the waiting ones `startAnyway`", strings.Join(s.waiting, ", "), wf.DoneLabel))
	case a.ap.Phase != aiv1.AutopilotDone:
		return a.done(ctx)
	}
	return nil
}

// startCheck is what pick would do next (startable).
type startCheck struct {
	// dep is a skipped dependency finished by hand (mr: its merged MR, if
	// any), to be recorded as merged.
	dep *aiv1.AutopilotItem
	// it is the pending item to start; closed if its issue is closed or
	// gone, done if it was finished by hand then (mr).
	it           *aiv1.AutopilotItem
	closed, done bool
	mr           *gitlab.MergeRequest
	// waiting says why each pending item cannot start, when none can;
	// landing: one of them waits for a companion that lands by rule, so the
	// queue goes on by itself.
	waiting []string
	landing bool
}

// startable finds what pick would do next, without doing it (it only reads
// GitLab): the first pending item whose dependencies are merged (a skipped
// one is met for an item with StartAnyway) and that no open issue blocks by
// a link, or a skipped dependency done by hand. With none, waiting lists
// why each pending item cannot start; empty: nothing is pending. An item
// with a companion on project P waits while another companion on P from
// the milestone is open (companionHold).
func (a *autopilot) startable(ctx context.Context) (startCheck, error) {
	var s startCheck
	waits := map[int64]string{} // why a pending issue cannot start
	a.holds = map[int64]*openCompanion{}
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemPending {
			continue
		}
		blocked := false
		for _, d := range a.deps(it.IID) {
			dep := a.item(d)
			if dep == nil || dep.State == aiv1.AutopilotItemMerged {
				continue
			}
			if dep.State == aiv1.AutopilotItemSkipped {
				mr, done, err := a.doneByHand(ctx, d)
				if err != nil {
					return s, err
				}
				if done {
					return startCheck{dep: dep, mr: mr}, nil
				}
				if it.StartAnyway {
					continue // an owner let it start without the skipped one
				}
				waits[it.IID] = fmt.Sprintf("#%d waits for the skipped #%d", it.IID, d)
			}
			blocked = true
			break
		}
		if blocked {
			continue
		}
		if is, ok := a.issues[it.IID]; !ok || is.State != "opened" {
			// Done before autopilot got to it, or given up.
			mr, done, err := a.doneByHand(ctx, it.IID)
			if err != nil {
				return s, err
			}
			return startCheck{it: it, closed: true, done: done, mr: mr}, nil
		}
		hold, err := a.companionHold(ctx, it)
		if err != nil {
			return s, err
		}
		if hold != nil {
			waits[it.IID] = fmt.Sprintf("#%d %s", it.IID, hold.waits())
			a.holds[it.IID] = hold
			s.landing = s.landing || hold.landing
			continue
		}
		blockers, err := a.blockedByLinks(ctx, it.IID)
		if err != nil {
			return s, err
		}
		if len(blockers) > 0 {
			var outside []string
			for _, b := range blockers {
				if b.queued {
					continue // the queue gets there
				}
				outside = append(outside, b.ref)
			}
			if len(outside) > 0 {
				waits[it.IID] = fmt.Sprintf("#%d is blocked by %s (issue link)", it.IID, strings.Join(outside, ", "))
			}
			continue
		}
		return startCheck{it: it}, nil
	}
	for _, it := range a.ap.Items {
		if it.State != aiv1.AutopilotItemPending {
			continue
		}
		why, ok := waits[it.IID]
		if !ok {
			why = fmt.Sprintf("#%d waits for another pending issue (a cycle of issue links?)", it.IID)
		}
		s.waiting = append(s.waiting, why)
	}
	return s, nil
}

// unstick lifts a stuck pause once pick can do something again: an issue
// can start (a link was removed, an unskip, a startAnyway), a skipped
// dependency was done by hand (recorded as merged), a waiting issue was
// closed, or nothing waits any more. While nothing can start it stays.
func (a *autopilot) unstick(ctx context.Context) error {
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PauseKind != aiv1.PauseStuck || a.st.RunPending() {
		return nil
	}
	s, err := a.startable(ctx)
	if err != nil {
		return err
	}
	var why string
	switch {
	case s.dep != nil:
		if err := a.merged(ctx, s.dep, s.mr); err != nil {
			return err
		}
		why = fmt.Sprintf("the skipped #%d was done by hand", s.dep.IID)
	case s.it != nil && s.closed:
		why = fmt.Sprintf("#%d was closed", s.it.IID)
	case s.it != nil:
		why = fmt.Sprintf("#%d can start now", s.it.IID)
	case len(s.waiting) == 0:
		why = "no issue waits any more"
	case s.landing:
		why = "the queue waits for a companion to land, and goes on by itself once it has"
	default:
		return nil
	}
	a.log.Info("autopilot continues by itself", "why", why)
	return a.resumed(ctx, aiv1.ResolvedByOperator, "", why)
}

// done marks the milestone done, closes its tracking issue and says so.
// With an empty queue it does nothing: a milestone whose plan is empty or
// was dropped by a re-plan merged nothing and is not done (Bloop %20).
// While companions of the milestone have not landed it keeps running and
// says once per change which ones it waits for (CloseWaitsForCompanions).
func (a *autopilot) done(ctx context.Context) error {
	if len(a.ap.Items) == 0 {
		a.log.Info("autopilot is not done: its queue is empty")
		return nil
	}
	open, err := a.landingCompanions(ctx)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		if err := a.reportCompanionWaits(ctx, open, "⏳ Every issue of the queue is through. Autopilot is done once the companions %s have landed (merged or closed)."); err != nil {
			return err
		}
		a.ap.Phase = aiv1.AutopilotRunning
		return nil
	}
	a.ap.CloseWaitsForCompanions = nil
	merged := 0
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemMerged {
			merged++
		}
	}
	a.log.Info("autopilot milestone done", "merged", merged)
	// Close first and comment after, so a failing close does not repeat
	// the comment.
	prev := a.ap.Phase
	a.ap.Phase = aiv1.AutopilotDone // for the description
	if err := a.closeTracking(ctx); err != nil {
		a.ap.Phase = prev // tried again on the next poll
		return err
	}
	return a.comment(ctx, fmt.Sprintf("✅ Autopilot is done: %d of %d issues merged. The decisions are listed in the description.", merged, len(a.ap.Items))+
		a.mention(aiv1.NotifyAutopilotDone))
}

// endMilestone ends the milestone at an owner's answer (by): the pending
// issues are skipped, so none is picked any more, and autopilot is done
// after the active issue. During a pause for a budget the active issue
// cannot go on without spend: it is skipped too, the pause ends and
// autopilot is done now. With an empty queue it only answers that there is
// nothing to end.
func (a *autopilot) endMilestone(ctx context.Context, by string) error {
	if len(a.ap.Items) == 0 {
		// Nothing to skip and nothing to be done with (done): the state stays.
		a.log.Info("autopilot milestone not ended: nothing is planned", "by", by)
		return a.comment(ctx, fmt.Sprintf("⏹️ Nothing to end (%s): nothing is planned in this milestone, so autopilot is not done. "+
			"To stop it, close the milestone in GitLab or remove `%s autopilot` from its description.", by, a.spec.Workflow.CommandPrefix))
	}
	budget := a.budgetPaused()
	var skipped []int64
	var active *aiv1.AutopilotItem
	for i := range a.ap.Items {
		switch it := &a.ap.Items[i]; {
		case it.State == aiv1.AutopilotItemPending, it.State == aiv1.AutopilotItemActive && budget:
			it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
			skipped = append(skipped, it.IID)
		case it.State == aiv1.AutopilotItemActive:
			active = it
		}
	}
	a.log.Info("autopilot milestone ends here", "by", by, "skipped", skipped)
	what := "nothing is left to skip"
	if len(skipped) > 0 {
		what = issueList(skipped) + " skipped"
	}
	if active != nil {
		return a.comment(ctx, fmt.Sprintf("⏹️ The milestone ends here (%s): %s. Autopilot picks no further issue and is done after #%d.", by, what, active.IID))
	}
	if err := a.comment(ctx, fmt.Sprintf("⏹️ The milestone ends here (%s): %s.", by, what)); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotDone {
		return nil
	}
	prev := *a.ap
	a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = "", "", 0, false
	if prev.Phase == aiv1.AutopilotPaused {
		a.ap.ResumedBy = resolverOf(by) // the pause ends with the milestone
	}
	if err := a.done(ctx); err != nil {
		a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = prev.PausedReason, prev.PauseKind, prev.PausedFor, prev.ResumeWhenReady
		a.ap.ResumedBy = prev.ResumedBy
		return err
	}
	return nil
}

// followUpWindow is how long the owners have to open a retrospective's
// follow-ups with "create" before autopilot closes the milestone.
const followUpWindow = 7 * 24 * time.Hour

// closeMilestone closes the milestone of a done autopilot once the
// retrospective and its follow-ups are settled, no issue in it is open and
// all its companions have landed.
// A closed milestone is no longer planned: the next poll deletes its tasks,
// so the comment after the close is not repeated.
func (a *autopilot) closeMilestone(ctx context.Context) error {
	var retro *aiv1.PilotRetrospective
	if ps := a.st.Status.Pilot; ps != nil {
		retro = ps.Retrospective
	}
	switch {
	case a.st.RunPending():
		return nil // a re-plan may add issues and continue the queue
	case retro == nil && a.pilotTask != nil:
		return nil // the pilot is still writing it
	case retro != nil && !followUpsSettled(retro, a.r.now()):
		return nil
	}
	var open []int64
	for iid, is := range a.issues {
		if is.State == "opened" && iid != a.ap.TrackingIssueIID {
			open = append(open, iid)
		}
	}
	slices.Sort(open)
	if len(open) > 0 && !slices.Equal(open, a.ap.CloseWaitsFor) {
		verb := "are"
		if len(open) == 1 {
			verb = "is"
		}
		if err := a.comment(ctx, fmt.Sprintf("📌 The milestone stays open while %s %s open. Autopilot closes it once they are closed.",
			issueList(open), verb)); err != nil {
			return err
		}
		a.ap.CloseWaitsFor = open
	}
	// A companion recorded after done (its issue finished by hand): the
	// milestone waits for it to land too.
	companions, err := a.landingCompanions(ctx)
	if err != nil {
		return err
	}
	if len(companions) > 0 {
		if err := a.reportCompanionWaits(ctx, companions, "📌 The milestone stays open while the companions %s have not landed. Autopilot closes it once they are merged or closed."); err != nil {
			return err
		}
	}
	a.ap.CloseWaitsForCompanions = companions
	if len(open) > 0 || len(companions) > 0 {
		return nil
	}
	a.log.Info("closing the done autopilot milestone")
	if err := a.gl.CloseMilestone(ctx, a.spec.GitLab.Project, a.st.Spec.Milestone.ID); err != nil && !gitlab.IsNotFound(err) {
		return fmt.Errorf("closing the milestone: %w", err)
	}
	a.ap.CloseWaitsFor, a.ap.CloseWaitsForCompanions = nil, nil
	return a.comment(ctx, "🏁 The milestone is closed.")
}

// followUpsSettled reports whether the retrospective's follow-ups no longer
// need the milestone: there are none, all were opened or taken over by
// another milestone, or the window to open them is over.
func followUpsSettled(retro *aiv1.PilotRetrospective, now time.Time) bool {
	if retro.Failed != "" || len(retro.FollowUps) == 0 || retro.PostedAt == nil || !now.Before(retro.PostedAt.Add(followUpWindow)) {
		return true
	}
	for k := range retro.FollowUps {
		key := strconv.Itoa(k + 1)
		_, created := retro.Created[key]
		if _, taken := retro.Taken[key]; !created && !taken {
			return false
		}
	}
	return true
}

// blocker is an open issue a queued issue is linked as "blocked by" to.
type blocker struct {
	ref string
	// queued: pending or active in this queue, so autopilot gets there.
	queued bool
}

// blockedByLinks returns the open issues iid is linked as "blocked by" to.
// Such links only exist on GitLab Premium. The plan's own dependencies are
// linked too and met once their issue is merged and closed; links the
// operator made for dependencies the plan dropped since do not count.
func (a *autopilot) blockedByLinks(ctx context.Context, iid int64) ([]blocker, error) {
	links, err := a.gl.ListIssueLinks(ctx, a.spec.GitLab.Project, iid)
	if gitlab.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing the links of #%d: %w", iid, err)
	}
	proj, err := a.glProject(ctx)
	if err != nil {
		return nil, err
	}
	var planned aiv1.PlannedIssue
	for _, p := range a.st.Status.Plan {
		if p.IID == iid {
			planned = p
		}
	}
	var out []blocker
	for _, l := range links {
		if l.LinkType != gitlab.LinkIsBlockedBy || l.State != "opened" {
			continue
		}
		if l.ProjectID != proj.ID {
			ref := l.References.Full
			if ref == "" {
				ref = fmt.Sprintf("#%d of project %d", l.IID, l.ProjectID)
			}
			out = append(out, blocker{ref: ref})
			continue
		}
		if slices.Contains(planned.Linked, l.IID) && !slices.Contains(planned.DependsOn, l.IID) {
			continue // the plan dropped this dependency; its link is stale
		}
		dep := a.item(l.IID)
		queued := dep != nil && (dep.State == aiv1.AutopilotItemPending || dep.State == aiv1.AutopilotItemActive)
		out = append(out, blocker{ref: fmt.Sprintf("#%d", l.IID), queued: queued})
	}
	return out, nil
}

// doneByHand reports whether a closed issue was finished outside autopilot:
// its MR was merged, a merged MR of the project closed it, or it was closed as
// done. It returns the merged MR, if any. (GitLab's closed_by only answers for
// open issues, so the issue's state events are used.)
func (a *autopilot) doneByHand(ctx context.Context, iid int64) (*gitlab.MergeRequest, bool, error) {
	is, ok := a.issues[iid]
	if !ok || is.State == "opened" {
		return nil, false, nil
	}
	if it := a.item(iid); it != nil && it.MergeRequestIID != 0 {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return nil, false, fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
		}
		if err == nil && mr.State == "merged" {
			return mr, true, nil
		}
	}
	// The issue's last state change says whether a merge request closed it;
	// merely mentioning the issue does not count.
	events, err := a.gl.ListIssueStateEvents(ctx, a.spec.GitLab.Project, iid)
	if err != nil {
		return nil, false, fmt.Errorf("listing the state events of #%d: %w", iid, err)
	}
	if n := len(events); n > 0 && events[n-1].State == "closed" && events[n-1].SourceMergeRequest != nil {
		src := events[n-1].SourceMergeRequest
		proj, err := a.gl.GetProject(ctx, a.spec.GitLab.Project)
		if err != nil {
			return nil, false, fmt.Errorf("getting project: %w", err)
		}
		if src.ProjectID == proj.ID {
			mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, src.IID)
			if err != nil && !gitlab.IsNotFound(err) {
				return nil, false, fmt.Errorf("getting merge request !%d: %w", src.IID, err)
			}
			if err == nil && mr.State == "merged" {
				return mr, true, nil
			}
		}
	}
	return nil, is.HasLabel(a.spec.Workflow.DoneLabel), nil
}

// drive moves the active issue towards its merge.
func (a *autopilot) drive(ctx context.Context, it *aiv1.AutopilotItem) error {
	wf := a.spec.Workflow
	glProject := a.spec.GitLab.Project
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	if task != nil && it.RebasedFrom != "" && task.Spec.Source.MergeBaseFor == it.RebasedFrom && task.Status.Run.MergedBaseFor != it.RebasedFrom {
		// The agent was asked to merge the default branch instead of the
		// rebase (it conflicted); its push is reviewed and checked anew. A
		// request already handled for this head says nothing about this rebase.
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	}
	if !ok || is.State != "opened" {
		// Closed: merged by hand, or given up.
		mr, done, err := a.doneByHand(ctx, it.IID)
		if err != nil {
			return err
		}
		if done {
			return a.merged(ctx, it, mr)
		}
		if err := a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed without a merge, so it is skipped.", it.IID)); err != nil {
			return err
		}
		it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
		return nil
	}
	switch g := a.gateCheck(it, is, task); g.state {
	case gateWaiting:
		return nil // the agent or CI is still working on it
	case gateCIUnfinished:
		// In review, but the CI loop, which only follows issues in
		// working, never finished this commit: hand it back. It always
		// ends in review or needs-input, so this does not repeat.
		a.log.Info("autopilot hands the issue back to the CI loop", "issue", it.IID, "sha", task.Status.Run.PushedSHA)
		if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
		}); err != nil {
			return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
		}
		return nil
	case gateReReview:
		return a.reReview(ctx, it, task)
	case gateFailed:
		if it.RebasedFrom == "" || g.kind != aiv1.PauseCIRed || task.Status.CI.State != aiv1.CIStateBaseFailing {
			return a.failGate(ctx, it, is, task, g)
		}
		// The red CI is the old head's (healBaseFailing): rebased hands the
		// new head to CI.
	}
	run := task.Status.Run

	mr, err := a.gl.GetMergeRequest(ctx, glProject, it.MergeRequestIID)
	if err != nil {
		return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
	}
	base, err := a.base(ctx)
	if err != nil {
		return err
	}
	switch {
	case mr.State == "merged":
		return a.merged(ctx, it, mr)
	case mr.State != "opened":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d was %s without a merge", mr.IID, mr.State))
	case mr.TargetBranch != "" && mr.TargetBranch != base:
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d targets `%s`, not the default branch `%s`", mr.IID, mr.TargetBranch, base))
	case it.RebasedFrom != "":
		return a.rebased(ctx, it, mr, task)
	case run.CheckpointHead(mr.SHA):
		// A run was cut off and saved its work: the run it continues in
		// moves the head on. Never ready, CI or a merge for it; the runner
		// set the draft, which is not a human holding the MR.
		a.log.Info("autopilot waits: the head of the merge request is a checkpoint", "mr", mr.IID, "sha", mr.SHA)
		it.MarkedReady = false
		return nil
	case mr.SHA != run.PushedSHA:
		return a.pauseHead(ctx, it, fmt.Sprintf("the head of !%d (%s) is not the commit the agent pushed and CI checked (%s); someone pushed to the branch. "+
			"Comment `%s …` on #%d so the agent takes it over, or merge by hand", mr.IID, short(mr.SHA), short(run.PushedSHA), wf.CommandPrefix, it.IID))
	case !a.spec.CI.Disabled && task.Status.CI != nil && task.Status.CI.State == aiv1.CIStateNone && !it.MarkedReady:
		// No pipeline ran, and projects may skip pipelines for drafts (a run
		// can also mark the MR ready without a push, which starts none): mark
		// it ready and run CI before anything is merged.
		return a.readyForCI(ctx, it, mr, task)
	}
	// The companion's merge request must be able to follow (companionGate).
	if pass, err := a.companionGate(ctx, it, task); err != nil || !pass {
		return err
	}
	switch mr.DetailedMergeStatus {
	case "mergeable":
	case "draft_status":
		if run.CheckpointHead(mr.SHA) {
			return nil // never ready (see above)
		}
		if it.MarkedReady {
			return a.pauseMR(ctx, it, fmt.Sprintf("!%d was marked draft again; mark it ready, or merge by hand", mr.IID))
		}
		res, err := a.gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
		switch {
		case err != nil:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready: %v", mr.IID, err))
		case res.Draft:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready (title %q); mark it ready by hand", mr.IID, res.Title))
		}
		it.MarkedReady = true
		a.log.Info("autopilot marked the merge request ready", "mr", mr.IID)
		return nil // merged on the next poll, once GitLab rechecked it
	case "need_rebase":
		return a.rebase(ctx, it, mr, task)
	case "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
		return nil
	case "conflict":
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("!%d has merge conflicts with the default branch", mr.IID))
	case "not_approved":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d needs an approval before it can be merged", mr.IID))
	case "discussions_not_resolved":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d has unresolved discussions", mr.IID))
	default:
		return a.pauseMR(ctx, it, fmt.Sprintf("GitLab reports !%d as %q", mr.IID, mr.DetailedMergeStatus))
	}

	a.log.Info("autopilot merges", "mr", mr.IID, "sha", mr.SHA)
	opts := gitlab.MergeOptions{SHA: run.PushedSHA, ShouldRemoveSourceBranch: true}
	if wf.ConventionalCommits != nil && *wf.ConventionalCommits {
		// GitLab ignores the message when the project does not squash.
		opts.SquashCommitMessage = conventional.Title(gitlab.ReadyTitle(mr.Title), wf.WithDefaults().CommitTypes) + fmt.Sprintf("\n\nRefs #%d", it.IID)
	}
	res, err := a.gl.MergeMergeRequest(ctx, glProject, mr.IID, opts)
	switch code := gitlab.StatusCode(err); {
	case err == nil:
		if res.State != "merged" {
			return nil // GitLab merges asynchronously; seen on the next poll
		}
		return a.merged(ctx, it, res)
	case code == 401 || code == 403:
		return a.pauseMR(ctx, it, fmt.Sprintf("the bot may not merge !%d into the default branch (HTTP %d); allow Developers to merge, or give the bot the Maintainer role", mr.IID, code))
	case code == 406:
		return a.rebase(ctx, it, mr, task)
	case code >= 400 && code < 500:
		return a.pauseMR(ctx, it, fmt.Sprintf("GitLab refused to merge !%d: %v", mr.IID, err))
	default:
		return fmt.Errorf("merging !%d: %w", mr.IID, err)
	}
}

// readyForCI marks a draft MR ready, starts a merge request pipeline and hands
// the issue back to the CI loop, which follows that pipeline. If there is
// still none, the gate goes on as before (the MR is not a draft any more).
func (a *autopilot) readyForCI(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	glProject := a.spec.GitLab.Project
	wf := a.spec.Workflow
	if task.Status.Run.CheckpointHead(mr.SHA) {
		return nil // a checkpoint is never marked ready or checked
	}
	if mr.Draft || mr.DetailedMergeStatus == "draft_status" {
		res, err := a.gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
		switch {
		case err != nil:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready: %v", mr.IID, err))
		case res.Draft:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready (title %q); mark it ready by hand", mr.IID, res.Title))
		}
		a.log.Info("autopilot marked the merge request ready to run CI", "mr", mr.IID)
	}
	// Also for an MR that was ready already: a draft after this is a human
	// holding it.
	it.MarkedReady = true
	if err := a.gl.CreateMergeRequestPipeline(ctx, glProject, mr.IID); err != nil {
		if code := gitlab.StatusCode(err); code >= 400 && code < 500 {
			// No merge request pipelines in this project (or no CI at all):
			// nothing to wait for; the gate goes on with the ready MR.
			a.log.Info("no merge request pipeline possible, merging without CI", "mr", mr.IID, "error", err.Error())
			return nil
		}
		return fmt.Errorf("starting a pipeline for !%d: %w", mr.IID, err)
	}
	orig := task.DeepCopy()
	task.Status.CI = homeCIReset(task.Status.CI, mr.SHA)
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
	}
	return nil
}

// glProject returns the GitLab project, read once per poll.
func (a *autopilot) glProject(ctx context.Context) (*gitlab.Project, error) {
	if a.proj == nil {
		proj, err := a.gl.GetProject(ctx, a.spec.GitLab.Project)
		if err != nil {
			return nil, fmt.Errorf("getting project: %w", err)
		}
		a.proj = proj
	}
	return a.proj, nil
}

// base returns the project's default branch.
func (a *autopilot) base(ctx context.Context) (string, error) {
	proj, err := a.glProject(ctx)
	if err != nil {
		return "", err
	}
	return proj.DefaultBranch, nil
}

// pauseActive pauses because of the active issue, unless its MR was merged
// meanwhile (by hand, while the issue is still open).
func (a *autopilot) pauseActive(ctx context.Context, it *aiv1.AutopilotItem, kind aiv1.PauseKind, reason string) error {
	if it.MergeRequestIID != 0 && a.ap.Phase != aiv1.AutopilotPaused {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
		}
		if err == nil && mr.State == "merged" {
			return a.merged(ctx, it, mr)
		}
	}
	return a.pause(ctx, kind, it.IID, a.about(it, reason))
}

// pauseMR pauses because of the active issue's MR (approvals, a push, its
// state): autopilot continues by itself only if it is merged by hand.
func (a *autopilot) pauseMR(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	return a.pause(ctx, aiv1.PauseMergeRequest, it.IID, a.about(it, reason))
}

// pauseHead pauses because the MR's head is not the one the run reviewed: a
// run that takes it over can settle that, then autopilot continues by itself
// (autoResume).
func (a *autopilot) pauseHead(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	if err := a.pauseMR(ctx, it, reason); err != nil {
		return err
	}
	a.ap.ResumeWhenReady = a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID
	return nil
}

// pauseRun pauses because of the active issue's run, review or CI, which a
// later run can fix: then autopilot continues by itself (autoResume).
func (a *autopilot) pauseRun(ctx context.Context, it *aiv1.AutopilotItem, kind aiv1.PauseKind, reason string) error {
	if err := a.pauseActive(ctx, it, kind, reason); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID {
		a.ap.ResumeWhenReady = true
	}
	return nil
}

// autoResume ends a pause about the active issue that no longer holds: it was
// merged or closed by hand, or (for a pause over its run, review or CI) a
// later run made it pass the gate; a pauseHead also needs the run to have
// taken over the MR's head. If a later run fixed the reason but the
// issue fails the gate for another one, the pause is announced again with
// that reason. A pause of any other kind waits for "resume".
func (a *autopilot) autoResume(ctx context.Context) error {
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor == 0 {
		return nil
	}
	it := a.item(a.ap.PausedFor)
	if it == nil || it.State != aiv1.AutopilotItemActive {
		// A budget pause can be for a pending issue; budget ends it.
		if a.ap.PauseKind != aiv1.PauseBudget {
			a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		}
		return nil
	}
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		// A run during the pause may have opened another MR.
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	resumeBy := func(by aiv1.PauseResolver, why string) error {
		if err := a.unblockActive(ctx); err != nil {
			return err
		}
		a.log.Info("autopilot continues by itself", "issue", it.IID, "why", why)
		return a.resumed(ctx, by, fmt.Sprintf("#%d", it.IID), why)
	}
	resume := func(why string) error { return resumeBy(aiv1.ResolvedByOperator, why) }
	is, ok := a.issues[it.IID]
	if !ok || is.State != "opened" {
		mr, done, err := a.doneByHand(ctx, it.IID)
		if err != nil {
			return err
		}
		if done && mr != nil {
			return resume(fmt.Sprintf("!%d was merged", mr.IID))
		}
		if done {
			return resume(fmt.Sprintf("#%d was closed as done", it.IID))
		}
		return resume(fmt.Sprintf("#%d was closed", it.IID))
	}
	if it.MergeRequestIID != 0 {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		switch {
		case err != nil && !gitlab.IsNotFound(err):
			// Tried again on the next poll; the tracking issue still updates.
			a.log.Info("could not check the paused issue's merge request", "mr", it.MergeRequestIID, "error", err.Error())
			return nil
		case err == nil && mr.State == "merged":
			if err := resume(fmt.Sprintf("!%d was merged", mr.IID)); err != nil {
				return err
			}
			return a.merged(ctx, it, mr)
		case err == nil && a.ap.PauseKind == aiv1.PauseMergeRequest && task != nil && mr.SHA != task.Status.Run.PushedSHA:
			// The gate would judge the head before the rebase: no run took
			// the new one over yet.
			return nil
		}
	}
	if a.ap.PauseKind == aiv1.PauseCompanion && task != nil {
		// The pause holds as long as the companion's merge request does.
		v, err := a.companionCheck(ctx, task)
		switch {
		case err != nil:
			a.log.Info("could not check the paused issue's companion", "issue", it.IID, "error", err.Error())
		case v.state == companionOK || v.state == companionRebased:
			return resume(fmt.Sprintf("the companion of #%d can go on", it.IID))
		}
		return nil
	}
	if !a.ap.ResumeWhenReady {
		return nil
	}
	switch g := a.gateCheck(it, is, task); {
	case g.state == gatePassed && a.overridden(it, task.Status.Run.PushedSHA, task.Status.Run):
		return resumeBy(aiv1.ResolvedByLaterRun, fmt.Sprintf("#%d is green and may merge without a full review", it.IID))
	case g.state == gatePassed:
		return resumeBy(aiv1.ResolvedByLaterRun, fmt.Sprintf("#%d is reviewed and green again", it.IID))
	case g.state == gateReReview:
		return a.reReview(ctx, it, task)
	case g.state == gateFailed && a.about(it, g.reason) != a.ap.PausedReason:
		// A later run fixed the old reason, but something else holds the
		// issue now: say so (and mention the owners), instead of keeping
		// the stale reason.
		a.log.Info("autopilot stays paused for another reason", "issue", it.IID, "was", a.ap.PausedReason, "now", g.reason)
		return a.failGate(ctx, it, is, task, g)
	}
	return nil
}

// gateState is where the active issue stands at the merge gate's run,
// review and CI checks (the MR itself is checked when the gate runs).
type gateState int

const (
	// gateWaiting: the agent or CI is still working on it.
	gateWaiting gateState = iota
	// gateCIUnfinished: in review, but the CI loop never finished the
	// pushed commit.
	gateCIUnfinished
	// gateFailed: it cannot be merged; reason says why.
	gateFailed
	// gateReReview: its reviewers failed transiently; reviewed again
	// after review.retryAfter.
	gateReReview
	// gatePassed: reviewed and green.
	gatePassed
)

type gateResult struct {
	state  gateState
	kind   aiv1.PauseKind
	reason string
	// byRun: a later run can fix it (pauseRun), else a human must
	// (pauseActive).
	byRun bool
	// block: the pause also blocks the issue, for no human's fault.
	block aiv1.BlockReason
}

// gateCheck checks the active issue's run, review and CI against the merge
// gate. drive pauses on a failure; while paused, autoResume continues on a
// pass and announces a failure for another reason.
func (a *autopilot) gateCheck(it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask) gateResult {
	wf := a.spec.Workflow
	failed := func(byRun bool, kind aiv1.PauseKind, format string, args ...any) gateResult {
		return gateResult{state: gateFailed, kind: kind, reason: fmt.Sprintf(format, args...), byRun: byRun}
	}
	switch {
	case is.HasLabel(wf.NeedsInputLabel) && task != nil && companionCIRed(task) != "":
		// The CI loop handed the red companion to a human after the fix
		// rounds: red CI, in the companion.
		return failed(true, aiv1.PauseCIRed, "%s", companionCIRed(task))
	case is.HasLabel(wf.NeedsInputLabel):
		if reason, ok := needsInputReason(it.IID, task); ok {
			return failed(true, aiv1.PauseNeedsInput, "%s", reason)
		}
		return failed(true, aiv1.PauseNeedsInput, "#%d needs input (see its last comments): CI is still red, the run failed or the agent asked a question", it.IID)
	case !hasAny(is, wf.ActiveLabels()...):
		return failed(false, aiv1.PauseConfig, "#%d is open but has no workflow label any more; move it back to `%s` or close it", it.IID, wf.ReviewLabel)
	case !is.HasLabel(wf.ReviewLabel) || task == nil || task.RunPending() || task.Status.Blocked != nil:
		return gateResult{state: gateWaiting} // blocked: waits for an outside reason
	}
	run := task.Status.Run
	// An owner's merge override skips the review checks for its head.
	reviewed := !a.overridden(it, run.PushedSHA, run)
	switch {
	case run.LastResult != aiv1.RunResultSucceeded:
		return failed(true, aiv1.PauseRunFailed, "the last run of #%d ended with %q", it.IID, run.LastResult)
	case reviewed && run.BlockingFindings > 0:
		return failed(true, aiv1.PauseFindings, "%d blocking review findings remain on !%d", run.BlockingFindings, it.MergeRequestIID)
	case reviewed && run.ReviewErrors > 0:
		return a.reviewErrors(it, task)
	case it.MergeRequestIID == 0 || run.PushedSHA == "":
		return failed(false, aiv1.PauseConfig, "#%d is in review without a pushed merge request", it.IID)
	case len(a.spec.Agents.Review.Reviewers) == 0:
		return failed(false, aiv1.PauseConfig, "no reviewers are configured (`agents.review.reviewers`), and autopilot only merges reviewed changes")
	case reviewed && run.ReviewedSHA != run.PushedSHA:
		return failed(true, aiv1.PauseNotReviewed, "the head of !%d (%s) was not reviewed (the last review saw %s)", it.MergeRequestIID, short(run.PushedSHA), short(run.ReviewedSHA))
	case reviewed && run.CompanionUnreviewed():
		// A companion-only push (a run without reviewers) after the review.
		c := run.Companion
		return failed(true, aiv1.PauseNotReviewed, "the companion head of #%d (%s, %s) was not reviewed (the last review saw %s)",
			it.IID, c.Project, short(c.PushedSHA), cmp.Or(short(c.ReviewedSHA), "none"))
	}
	if a.spec.CI.Disabled {
		return gateResult{state: gatePassed}
	}
	switch ci := task.Status.CI; {
	case ci == nil || ci.SHA != run.PushedSHA || !ci.State.Final():
		return gateResult{state: gateCIUnfinished}
	case ci.State == aiv1.CIStateCanceled:
		return failed(true, aiv1.PauseCICanceled, "the pipeline of !%d was canceled", it.MergeRequestIID)
	case ci.State == aiv1.CIStateBaseFailing:
		return failed(true, aiv1.PauseCIRed, "CI of !%d is red with jobs that also fail on the default branch (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	case ci.State == aiv1.CIStateFailed:
		return failed(true, aiv1.PauseCIRed, "CI of !%d is red (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	}
	// The companion must be green on its pushed head too. No pipeline (None)
	// passes like at home: its landing checks it again before its merge.
	if c := run.Companion; c != nil && c.PushedSHA != "" {
		ref := companionRef(c)
		switch cc := task.Status.CI.Companion; {
		case cc == nil || cc.SHA != c.PushedSHA || !cc.State.Final():
			return gateResult{state: gateCIUnfinished}
		case cc.State == aiv1.CIStateCanceled:
			return failed(true, aiv1.PauseCICanceled, "the pipeline of the companion %s was canceled", ref)
		case cc.State == aiv1.CIStateFailed:
			return failed(true, aiv1.PauseCIRed, "CI of the companion %s is red (%s)", ref, jobList(cc.FailedJobs))
		}
	}
	return gateResult{state: gatePassed}
}

// companionRef names a companion merge request, "group/infra!3", or its
// project without one.
func companionRef(c *aiv1.CompanionRun) string {
	if c.MRIID == 0 {
		return c.Project
	}
	return fmt.Sprintf("%s!%d", c.Project, c.MRIID)
}

// companionCIRed is the reason a companion pipeline that stayed red after
// the fix rounds holds the issue, "" if it did not.
func companionCIRed(task *aiv1.AgentTask) string {
	c, ci := task.Status.Run.Companion, task.Status.CI
	if c == nil || c.PushedSHA == "" || ci == nil || ci.SHA != task.Status.Run.PushedSHA || ci.State == aiv1.CIStateFailed ||
		ci.Companion == nil || ci.Companion.SHA != c.PushedSHA || ci.Companion.State != aiv1.CIStateFailed {
		return ""
	}
	return fmt.Sprintf("CI of the companion %s is still red after %d fix rounds (%s)", companionRef(c), task.Status.Run.CIRound, jobList(ci.Companion.FailedJobs))
}

// mergeBase asks the agent to merge the default branch into the MR and
// resolve the conflicts (spec.source.mergeBaseFor), once per head. The run
// is reviewed and checked by CI like any push, then the gate runs again. If
// that is switched off or was tried for this head already, it pauses.
func (a *autopilot) mergeBase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask, why string) error {
	wf := a.spec.Workflow
	switch {
	case !*wf.ResolveConflicts:
		return a.pauseMR(ctx, it, why+"; resolve them on the branch (or merge the default branch into it), then `resume`")
	case task.Spec.Source.MergeBaseFor == mr.SHA:
		return a.pauseMR(ctx, it, why+", and merging the default branch did not resolve it; resolve the conflicts on the branch, then `resume`")
	}
	a.log.Info("autopilot asks the agent to merge the default branch", "mr", mr.IID, "sha", mr.SHA, "reason", why)
	if err := a.r.requestMergeBase(ctx, a.gl, a.spec, task, it.IID, mr.SHA); err != nil {
		return err
	}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	return nil
}

// rebase asks GitLab to rebase the MR, once per item. The reviewed change is
// hashed first, so the rebased head can be checked against it. If a rebase
// cannot help, the agent merges the default branch instead.
func (a *autopilot) rebase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	diff, err := a.diffHash(ctx, a.spec.GitLab.Project, mr.IID)
	if err != nil {
		return err
	}
	if diff == "" {
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("!%d needs a rebase, but its change is too large to check that a rebase keeps it", mr.IID))
	}
	if err := a.gl.RebaseMergeRequest(ctx, a.spec.GitLab.Project, mr.IID); err != nil {
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("GitLab could not rebase !%d (%v)", mr.IID, err))
	}
	a.log.Info("autopilot rebases the merge request", "mr", mr.IID, "from", mr.SHA)
	it.Rebases++
	it.RebasedFrom, it.RebasedDiff = mr.SHA, diff
	it.RebasedAt = &metav1.Time{Time: a.r.now()}
	return nil
}

// rebaseGrace is how long GitLab may take to show a rebased head after the
// rebase itself finished.
const rebaseGrace = 5 * time.Minute

// rebased hands the rebased head to the CI loop like a new push, once GitLab
// is done and the change is still the one that was reviewed.
func (a *autopilot) rebased(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	unrebased := func(reason string) error {
		if err := a.pauseHead(ctx, it, reason); err != nil {
			return err
		}
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
		return nil
	}
	switch {
	case mr.RebaseInProgress:
		return nil
	case mr.MergeError != "" && mr.SHA == it.RebasedFrom:
		// The branch did not move: the agent merges the default branch.
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d failed (%s)", mr.IID, mr.MergeError))
	case mr.MergeError != "":
		return unrebased(fmt.Sprintf("the rebase of !%d failed: %s", mr.IID, mr.MergeError))
	case mr.SHA == it.RebasedFrom && mr.DetailedMergeStatus == "mergeable":
		// Nothing to rebase after all; the gate runs again.
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
		return nil
	case mr.SHA == it.RebasedFrom && mr.DetailedMergeStatus == "conflict":
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d ran into conflicts", mr.IID))
	case mr.SHA == it.RebasedFrom && it.RebasedAt != nil && a.r.now().Sub(it.RebasedAt.Time) < rebaseGrace:
		return nil // GitLab refreshes the MR after the rebase
	case mr.SHA == it.RebasedFrom:
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d did not go through (GitLab reports %q)", mr.IID, mr.DetailedMergeStatus))
	}
	// The review only holds if the rebase moved the change without changing
	// it; a commit pushed meanwhile, or a rebase that had to adapt lines,
	// changes the diff.
	diff, err := a.diffHash(ctx, a.spec.GitLab.Project, mr.IID)
	if err != nil {
		return err
	}
	if diff == "" || diff != it.RebasedDiff {
		if err := unrebased(fmt.Sprintf("after the rebase, the change of !%d is not the one that was reviewed (someone pushed, or the rebase changed lines). "+
			"The reviewers take the new head over and autopilot goes on once it is reviewed and green; "+
			"or comment `%s …` on #%d so the agent takes it over, or merge by hand", mr.IID, a.spec.Workflow.CommandPrefix, it.IID)); err != nil {
			return err
		}
		var companion string
		if c := task.Status.Run.Companion; c != nil {
			companion = c.PushedSHA
		}
		if err := a.r.askReview(ctx, task, aiv1.ReviewHeads(mr.SHA, companion)); err != nil {
			return err
		}
		return a.r.requestRun(ctx, a.gl, a.spec, task, it.IID)
	}
	a.log.Info("autopilot rebase done, waiting for CI", "mr", mr.IID, "sha", mr.SHA)
	orig := task.DeepCopy()
	if task.Status.Run.ReviewedSHA == it.RebasedFrom {
		task.Status.Run.ReviewedSHA = mr.SHA // not for a head an override let through
	}
	task.Status.Run.PushedSHA = mr.SHA
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	task.Status.CI = homeCIReset(task.Status.CI, mr.SHA)
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if a.overridden(it, it.RebasedFrom, orig.Status.Run) {
		a.ap.MergeOverride.SHA = mr.SHA // the same change, like its review
	}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	if a.spec.CI.Disabled {
		return nil // merged on the next poll
	}
	// Back to working, so the CI loop follows the new pipeline. If this
	// fails, the gate hands the unchecked head back on the next poll.
	wf := a.spec.Workflow
	if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
	}
	return nil
}

// diffHash hashes the change of an MR of project (home, or the companion's):
// the files and their added and removed lines, without positions, so a
// rebase that only moves the change keeps the
// hash. It is empty if GitLab does not return the whole change.
func (a *autopilot) diffHash(ctx context.Context, project string, iid int64) (string, error) {
	return diffHash(ctx, a.gl, project, iid)
}

// diffHash is autopilot.diffHash with the client gl.
func diffHash(ctx context.Context, gl gitlab.API, project string, iid int64) (string, error) {
	diffs, err := gl.ListMergeRequestDiffs(ctx, project, iid)
	if err != nil {
		return "", fmt.Errorf("listing the diffs of !%d: %w", iid, err)
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].NewPath != diffs[j].NewPath {
			return diffs[i].NewPath < diffs[j].NewPath
		}
		return diffs[i].OldPath < diffs[j].OldPath
	})
	var b strings.Builder
	for _, d := range diffs {
		if d.TooLarge || d.Collapsed {
			return "", nil
		}
		fmt.Fprintf(&b, "%s\x00%s\x00%t%t%t\n", d.OldPath, d.NewPath, d.NewFile, d.RenamedFile, d.DeletedFile)
		for _, line := range strings.Split(d.Diff, "\n") {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "Binary") {
				b.WriteString(line + "\n")
			}
		}
	}
	return hashString(b.String()), nil
}

// merged records a merged item and closes its issue if GitLab did not. mr is
// nil for an issue closed as done without a known MR.
func (a *autopilot) merged(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest) error {
	glProject := a.spec.GitLab.Project
	wf := a.spec.Workflow
	is, err := a.gl.GetIssue(ctx, glProject, it.IID)
	if err != nil {
		return fmt.Errorf("getting issue #%d: %w", it.IID, err)
	}
	notes, err := a.gl.ListIssueNotes(ctx, glProject, it.IID)
	if err != nil {
		return fmt.Errorf("listing notes of issue #%d: %w", it.IID, err)
	}
	if is.State == "opened" {
		// "Closes #N" only closes the issue on the default branch.
		if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
			StateEvent: "close", AddLabels: []string{wf.DoneLabel},
			RemoveLabels: []string{wf.ReviewLabel, wf.WorkingLabel},
		}); err != nil {
			return fmt.Errorf("closing issue #%d: %w", it.IID, err)
		}
	}
	if orig := a.item(marker.DecisionIssue(it.RevisitOf)); orig != nil && it.RevisitOf != "" {
		// The follow-up superseded a decision of an earlier issue.
		ois, err := a.gl.GetIssue(ctx, glProject, orig.IID)
		if err != nil {
			return fmt.Errorf("getting issue #%d: %w", orig.IID, err)
		}
		onotes, err := a.gl.ListIssueNotes(ctx, glProject, orig.IID)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", orig.IID, err)
		}
		orig.Decisions = collectDecisions(ois, onotes, a.project.Status.BotUserID)
	}
	it.State = aiv1.AutopilotItemMerged
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	it.Decisions = collectDecisions(is, notes, a.project.Status.BotUserID)
	if mr != nil {
		it.MergedSHA = firstNonEmpty(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA)
		it.MergeRequestIID, it.MergeRequestURL = mr.IID, firstNonEmpty(mr.WebURL, it.MergeRequestURL)
	}
	a.log.Info("autopilot merged", "issue", it.IID, "mr", it.MergeRequestIID, "sha", it.MergedSHA)
	return nil
}

// collectDecisions lists the newest aiv1.MaxAutopilotItemDecisions decisions
// on an issue: the planner's in the description and the implementer's in the
// bot's comments, oldest first. The full history is in history_events.
func collectDecisions(is *gitlab.Issue, notes []gitlab.Note, botID int64) []aiv1.DecisionRef {
	var out []aiv1.DecisionRef
	for _, d := range marker.Decisions(is.Description) {
		out = append(out, aiv1.DecisionRef{ID: d.ID, Title: d.Title, URL: is.WebURL, Superseded: d.Superseded})
	}
	for _, n := range notes {
		if botID == 0 || n.Author.ID != botID || n.System {
			continue
		}
		for _, d := range marker.Decisions(n.Body) {
			out = append(out, aiv1.DecisionRef{ID: d.ID, Title: d.Title, URL: fmt.Sprintf("%s#note_%d", is.WebURL, n.ID), Superseded: d.Superseded})
		}
	}
	return capDecisions(out)
}

// capDecisions keeps the newest (last) aiv1.MaxAutopilotItemDecisions.
func capDecisions(ds []aiv1.DecisionRef) []aiv1.DecisionRef {
	if len(ds) > aiv1.MaxAutopilotItemDecisions {
		return ds[len(ds)-aiv1.MaxAutopilotItemDecisions:]
	}
	return ds
}

// pause stops the queue over issue iid (0: none) and says why and the ways
// out, once.
func (a *autopilot) pause(ctx context.Context, kind aiv1.PauseKind, iid int64, reason string) error {
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedReason == reason {
		a.ap.PausedFor = iid
		return nil
	}
	a.log.Info("autopilot paused", "reason", reason)
	// Comment first: if it fails, the pause is tried again on the next poll.
	// Defuse the reason (it can quote job names and titles); the owners'
	// mention is appended after, so it stays a real ping.
	if err := a.comment(ctx, fmt.Sprintf("⏸️ Autopilot paused: %s.\n\nWays out:\n%s", notify.Defuse(reason), a.exitLines(kind, iid))+a.mention(aiv1.NotifyAutopilotPaused)); err != nil {
		return err
	}
	a.startPause(kind)
	a.ap.PausedReason, a.ap.PauseKind = reason, kind
	a.ap.PausedFor, a.ap.ResumeWhenReady = iid, false
	return nil
}

// exitLines renders the ways out of a pause of kind over issue iid
// (aiv1.PauseExits) as a list, with the project's command prefix and labels.
// A command after a label or a GitLab action goes on the tracking issue.
func (a *autopilot) exitLines(kind aiv1.PauseKind, iid int64) string {
	wf := a.spec.Workflow
	issue, ref, head := "the issue", "#N", "<sha>"
	if iid != 0 {
		issue, ref = fmt.Sprintf("#%d", iid), fmt.Sprintf("#%d", iid)
		if t := a.tasks[iid]; t != nil && t.Status.Run.PushedSHA != "" {
			head = short(t.Status.Run.PushedSHA)
		}
	}
	args := map[string]string{"skip": " " + ref, "unskip": " " + ref, "startAnyway": " " + ref, "rebase": " " + ref, "mergeBase": " " + ref, "merge": " " + ref + " " + head}
	cmd := func(word string, target aiv1.ExitTarget) string {
		where := "here"
		if target == aiv1.ExitTargetImplement {
			where = "on " + issue
		}
		return fmt.Sprintf("`%s %s%s` %s", wf.CommandPrefix, word, args[word], where)
	}
	var b strings.Builder
	for _, e := range aiv1.PauseExits(kind) {
		var how string
		switch {
		case e.Auto:
			how = "By itself, once " + e.When
		case e.GitLab:
			how = "In GitLab, " + e.When
		case e.Prompt:
			how = fmt.Sprintf("Answer the pilot's prompt (or ask for one with `%s …`) %s", wf.PilotPrefix, e.When)
		case e.Label != "":
			how = fmt.Sprintf("Label %s `%s` %s", issue, wf.RoleLabel(e.Label), e.When)
		default:
			how = cmd(e.Command, e.Target)
			if e.When != "" {
				how += ", " + e.When
			}
		}
		if !e.Auto && e.Command != "" && (e.GitLab || e.Label != "") {
			how += ", then " + cmd(e.Command, aiv1.ExitTargetSpec)
		}
		fmt.Fprintf(&b, "- %s: %s.\n", strings.TrimSpace(how), e.Does)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// lift ends a pause: autopilot runs again. Every resume goes through here;
// by is who ended it (status.autopilot.resumedBy, for the history's
// autopilot.resumed event).
func (a *autopilot) lift(by aiv1.PauseResolver) {
	a.ap.Phase, a.ap.PausedReason, a.ap.PauseKind = aiv1.AutopilotRunning, "", ""
	a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
	a.ap.PausedAt, a.ap.ResumedBy = nil, by
}

// resumed says on the tracking issue that autopilot continues, why and who
// ended the pause, then ends it. The comment goes first: if it fails, the
// pause stays and the next poll tries again.
func (a *autopilot) resumed(ctx context.Context, by aiv1.PauseResolver, who, why string) error {
	if err := a.comment(ctx, resumeLine(by, who, why)); err != nil {
		return err
	}
	a.lift(by)
	return nil
}

// resumeLine is the "▶️ Autopilot continues" comment for a pause ended by
// by: who is the command's by for an owner, the paused issue (#N) for a
// later run. who is defused: it names a person but pings nobody.
func resumeLine(by aiv1.PauseResolver, who, why string) string {
	ended := "ended by the operator: the cause went away"
	switch {
	case by == aiv1.ResolvedByOwner && who != "":
		ended = "ended by " + notify.Defuse(who)
	case by == aiv1.ResolvedByOwner:
		ended = "ended by an owner"
	case by == aiv1.ResolvedByPilot:
		ended = "ended by the pilot"
	case by == aiv1.ResolvedByLaterRun:
		ended = "ended by a later run of " + notify.Defuse(who)
	case by == aiv1.ResolvedBySelfHealed:
		ended = "ended by the operator: it rebased the merge request"
	}
	return fmt.Sprintf("▶️ Autopilot continues: %s (%s).", why, ended)
}

// pilotSelf is the by of a command the pilot runs on its own (managed
// autonomy); an owner's answer via the pilot names the owner.
const pilotSelf = "the pilot"

// resolverOf is who ends a pause with a command by someone: the pilot on
// its own, otherwise an owner.
func resolverOf(by string) aiv1.PauseResolver {
	if by == pilotSelf {
		return aiv1.ResolvedByPilot
	}
	return aiv1.ResolvedByOwner
}

// startPause pauses the queue for kind and clears who ended the last pause.
// A new pause, also one of another kind while paused, starts its clock
// (PausedAt).
func (a *autopilot) startPause(kind aiv1.PauseKind) {
	a.ap.ResumedBy = ""
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedAt == nil || a.ap.PauseKind != kind {
		a.ap.PausedAt = &metav1.Time{Time: a.r.now()}
	}
	a.ap.Phase = aiv1.AutopilotPaused
}

// stamp records the queue's state hash (stateHash) and when it changed; a
// pause from before PausedAt existed starts its clock now.
func (a *autopilot) stamp() {
	now := &metav1.Time{Time: a.r.now()}
	switch {
	case a.ap.Phase != aiv1.AutopilotPaused:
		a.ap.PausedAt = nil
	case a.ap.PausedAt == nil:
		a.ap.PausedAt = now
	}
	if h := a.stateHash(); h != a.ap.StateHash {
		a.ap.StateHash, a.ap.StateChangedAt = h, now
	}
}

// stateHash hashes what a stuck pause waits on to change: the items (also
// the main-red fix) with their MR, rebase, startAnyway, head and CI, main
// health, the phase and the pause reason. Spend, the tracking issue and
// command marks are left out: they change without moving the queue.
func (a *autopilot) stateHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s\n", a.ap.Phase, a.ap.PausedReason)
	if mh := a.project.Status.MainHealth; mh != nil {
		fmt.Fprintf(&b, "main %s %s\n", mh.State, mh.SHA)
	}
	items := a.ap.Items
	if a.ap.MainRedFix != nil {
		items = append(slices.Clone(items), *a.ap.MainRedFix)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "%d %s !%d %s %t", it.IID, it.State, it.MergeRequestIID, it.RebasedFrom, it.StartAnyway)
		if t := a.tasks[it.IID]; t != nil {
			fmt.Fprintf(&b, " %s", t.Status.Run.PushedSHA)
			if ci := t.Status.CI; ci != nil {
				fmt.Fprintf(&b, " %s %d", ci.State, ci.PipelineID)
			}
		}
		b.WriteString("\n")
	}
	return hashString(b.String())
}

// commands handles "/agent pause|resume|skip #N" on the tracking issue and
// answers any other command once, in its thread.
func (a *autopilot) commands(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	notes, err := a.gl.ListIssueNotes(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing notes of the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	a.pilot(ctx, notes)
	if err := a.followUps(ctx, notes); err != nil {
		return err
	}
	prefix := a.spec.Workflow.CommandPrefix
	for _, n := range notes {
		if n.ID <= a.ap.LastCommandNoteID || !IsCommand(n, a.project.Status.BotUserID, prefix) {
			continue
		}
		// Every comment gets one reply in its thread (replyPerNote).
		// Someone else's command is answered and handled, so it is not
		// answered again.
		tn := pilotNote{Note: n, issue: a.ap.TrackingIssueIID}
		if ok, err := a.gate.counts(ctx, n, a.st.Spec.Owners); err != nil {
			return err
		} else if !ok {
			a.gate.deny(ctx, tn, a.st.Spec.Owners, a.r.deskLink(a.spec, tn.issue, tn.issue, nil, a.st))
			a.ap.LastCommandNoteID = n.ID
			continue
		}
		rest := strings.TrimPrefix(strings.TrimSpace(n.Body), prefix)
		// Padded, so a bare command and a bare "skip" have their fields.
		fields := append(strings.Fields(strings.TrimLeft(rest, ": \t")), "", "")
		word := strings.ToLower(strings.TrimSuffix(fields[0], ":"))
		if word == "revisit" {
			// syncRevisits handles (and answers) the well-formed ones.
			if m := marker.Revisit.FindStringSubmatch(n.Body); m != nil && m[1] == prefix {
				a.ap.LastCommandNoteID = n.ID
				continue
			}
			word = ""
		}
		arg := fields[1]
		if word == "merge" || word == "start" {
			// Owners only, also where anyone may give commands.
			if ok, err := a.gate.byOwner(ctx, n, a.st.Spec.Owners); err != nil {
				return err
			} else if !ok {
				reply := notOwnerMerge
				if word == "start" {
					reply = notOwnerStart
				}
				a.r.rejectNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+reply)
				a.ap.LastCommandNoteID = n.ID
				continue
			}
		}
		if word == "merge" {
			arg += " " + fields[2]
		}
		// Tried again on the next poll when it fails.
		reply, applied, err := a.command(ctx, word, arg, "@"+n.Author.Username)
		if err != nil {
			return err
		}
		if !applied {
			// Usage and unknown commands; the reply's marker keeps a retry
			// from answering twice.
			err := a.r.replyPerNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+reply)
			if err != nil && !a.trackingGone(err) {
				return fmt.Errorf("answering a command on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
		}
		a.ap.LastCommandNoteID = n.ID
	}
	// The same commands from proposals an owner accepted; revisits are
	// syncRevisits'.
	if ps := a.st.Status.Pilot; ps != nil {
		for _, c := range ps.Commands {
			if c.Seq <= a.ap.LastPilotCommand {
				continue
			}
			by := "@" + c.Owner + " via the pilot"
			if c.Owner == "" {
				by = pilotSelf // on its own (managed)
			}
			if c.Action.Tool == "endMilestone" {
				if err := a.endMilestone(ctx, by); err != nil {
					return err
				}
			} else if c.Action.Tool != "revisit" {
				reply, applied, err := a.command(ctx, c.Action.Tool, fmt.Sprintf("#%d", c.Action.IssueIID), by)
				if err != nil {
					return err
				}
				if !applied {
					if err := a.comment(ctx, reply); err != nil {
						return err
					}
				}
			}
			a.ap.LastPilotCommand = c.Seq
		}
	}
	return nil
}

// chatCommands applies Desk pause/resume/skip on the Spec task newer than
// LastChatCommandID. Answers are kept on a until after the status save.
func (a *autopilot) chatCommands(ctx context.Context) error {
	src := &a.st.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	slices.SortFunc(pending, func(x, y aiv1.ChatMessage) int { return cmp.Compare(x.ID, y.ID) })
	now := metav1.Now()
	var answers []aiv1.ChatReply
	mark := src.LastChatCommandID
	answer := func(id int64, text string) {
		answers = append(answers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		mark = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			a.log.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if !cmd.Autopilot() {
			answer(m.ID, "not supported on this task yet")
			continue
		}
		who := m.Author
		if m.AuthorName != "" {
			who = m.AuthorName
		}
		by := who + " via the Desk"
		arg := ""
		switch cmd.Cmd {
		case "skip":
			arg = fmt.Sprintf("#%d", cmd.Issue)
		case "merge":
			if !slices.Contains(a.st.Spec.OwnerSubjects, m.Author) {
				answer(m.ID, notOwnerMerge)
				a.log.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
				continue
			}
			arg = fmt.Sprintf("#%d %s", cmd.Issue, cmd.SHA)
		}
		a.log.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
		reply, _, err := a.command(ctx, cmd.Cmd, arg, by)
		if err != nil {
			return err
		}
		answer(m.ID, reply)
	}
	a.chatMark, a.chatAnswers = mark, answers
	return nil
}

// command applies one command of the tracking issue: pause, resume,
// skip, rebase, mergeBase or merge arg, by someone. It returns the answer and whether the command was
// applied; the tracking issue gets only the answers of commands that were
// not (usage, unknown), the Desk gets every answer.
func (a *autopilot) command(ctx context.Context, word, arg, by string) (answer string, applied bool, err error) {
	switch word {
	case "pause":
		if a.ap.Phase != aiv1.AutopilotDone {
			a.startPause(aiv1.PauseOwner)
			a.ap.PausedReason, a.ap.PauseKind = "paused by "+by, aiv1.PauseOwner
			a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		}
		return "Autopilot paused.", true, nil
	case "resume":
		if a.ap.Phase == aiv1.AutopilotPaused {
			if a.landingPause() {
				a.retryLandings(by) // one more rebase for each
			}
			if err := a.unblockActive(ctx); err != nil {
				return "", false, err
			}
			if err := a.resumed(ctx, resolverOf(by), by, "resumed"); err != nil {
				return "", false, err
			}
		}
		return "Autopilot resumed.", true, nil
	case "skip":
		iid, _ := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
		it := a.item(iid)
		if it == nil || it.State == aiv1.AutopilotItemMerged {
			return fmt.Sprintf("`skip` needs a planned issue that is not merged yet, e.g. `%s skip #12`.", a.spec.Workflow.CommandPrefix), false, nil
		}
		it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
		return fmt.Sprintf("#%d skipped.", iid), true, nil
	case "unskip":
		iid, _ := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
		it := a.item(iid)
		if is, ok := a.issues[iid]; it == nil || it.State != aiv1.AutopilotItemSkipped || it == a.ap.MainRedFix || !ok || is.State != "opened" {
			return fmt.Sprintf("`unskip` needs a skipped issue that is still open, e.g. `%s unskip #12`.", a.spec.Workflow.CommandPrefix), false, nil
		}
		a.log.Info("autopilot unskips an issue", "issue", iid, "by", by)
		it.State = aiv1.AutopilotItemPending
		return fmt.Sprintf("#%d is pending again.", iid), true, nil
	case "startAnyway", "startanyway":
		return a.startAnyway(ctx, arg, by)
	case "merge":
		return a.mergeAnyway(ctx, arg, by)
	case "start":
		return a.startNow(ctx, by)
	case "rebase", "mergeBase", "mergebase":
		// The tracking issue's words are lower case, the pilot's tools not.
		return a.mrCommand(ctx, strings.Replace(word, "mergebase", "mergeBase", 1), arg, by)
	default:
		return a.unknownCommand(), false, nil
	}
}

// startAnyway lets the pending issue arg start although issues it depends
// on were skipped (AutopilotItem.StartAnyway), by someone, and says so on
// the tracking issue. Its other dependencies and links still hold.
func (a *autopilot) startAnyway(ctx context.Context, arg, by string) (string, bool, error) {
	iid, _ := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	it := a.item(iid)
	var skipped []int64
	if it != nil && it != a.ap.MainRedFix && it.State == aiv1.AutopilotItemPending {
		skipped = a.skippedDeps(iid)
	}
	if len(skipped) == 0 {
		return fmt.Sprintf("`startAnyway` needs a pending issue that depends on a skipped one, e.g. `%s startAnyway #12`.", a.spec.Workflow.CommandPrefix), false, nil
	}
	if it.StartAnyway {
		return fmt.Sprintf("#%d may already start without %s.", iid, issueList(skipped)), true, nil
	}
	which := issueList(skipped) + ", which it depends on, was skipped"
	if len(skipped) > 1 {
		which = issueList(skipped) + ", which it depends on, were skipped"
	}
	// Comment first: if it fails, the command is applied again on the next
	// poll.
	if err := a.comment(ctx, fmt.Sprintf("▶️ #%d may start although %s (%s).", iid, which, by)); err != nil {
		return "", false, err
	}
	a.log.Info("autopilot lets an issue start without its skipped dependencies", "issue", iid, "skipped", skipped, "by", by)
	it.StartAnyway = true
	return fmt.Sprintf("#%d may start although %s.", iid, which), true, nil
}

// startNow plans a milestone that waits for its turn now (`start`, owners
// only, decision 10): StartRequestedBy makes syncSpecTask ask for the first
// planning run on the next poll. A planned or planning milestone is not
// touched.
func (a *autopilot) startNow(ctx context.Context, by string) (string, bool, error) {
	if !a.waitingForTurn() {
		return "The milestone is already planned (or being planned); `start` does nothing.", false, nil
	}
	// Comment first: if it fails, the command is applied again on the next
	// poll.
	if err := a.comment(ctx, fmt.Sprintf("▶️ Planning starts now, ahead of its turn (by %s).", by)); err != nil {
		return "", false, err
	}
	a.log.Info("autopilot starts planning ahead of the milestone's turn", "by", by)
	a.ap.StartRequestedBy = by
	return "Planning starts now.", true, nil
}

// waitingForTurn reports whether the milestone waits for its turn to be
// planned: its spec task has never asked for a run, it has no plan and no
// owner asked to start it yet.
func (a *autopilot) waitingForTurn() bool {
	return a.st.Spec.RunRequest == 0 && len(a.st.Status.Plan) == 0 && a.ap.StartRequestedBy == ""
}

// skippedDeps are the dependencies of iid in the plan that are skipped.
func (a *autopilot) skippedDeps(iid int64) []int64 {
	var out []int64
	for _, d := range a.deps(iid) {
		if dep := a.item(d); dep != nil && dep.State == aiv1.AutopilotItemSkipped {
			out = append(out, d)
		}
	}
	return out
}

// unknownCommand answers a command the tracking issue does not understand,
// without quoting it: instructions for the agent go on the active issue.
func (a *autopilot) unknownCommand() string {
	prefix := a.spec.Workflow.CommandPrefix
	where := "the issue it should work on"
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemActive {
			where = fmt.Sprintf("#%d, the active issue", it.IID)
		}
	}
	start := ""
	if a.waitingForTurn() {
		start = fmt.Sprintf("`%s start`, ", prefix)
	}
	return fmt.Sprintf("This tracking issue only understands %[4]s`%[1]s pause`, `%[1]s resume`, `%[1]s skip #N`, `%[1]s unskip #N`, `%[1]s startAnyway #N`, `%[1]s rebase #N`, `%[1]s mergeBase #N`, `%[1]s merge #N` and `%[1]s revisit <id> …`. "+
		"Did you mean `%[3]s …`? The milestone's pilot answers here. To instruct the agent, comment `%[1]s …` on %[2]s.", prefix, where, a.spec.Workflow.PilotPrefix, start)
}

// ensureTracking creates the tracking issue on the first poll.
func (a *autopilot) ensureTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID != 0 {
		is, ok := a.issues[a.ap.TrackingIssueIID]
		if !ok {
			// Not in the milestone any more: moved, or deleted.
			got, err := a.gl.GetIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
				return fmt.Errorf("getting the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
			is = *got
		}
		if is.State != "opened" && a.ap.Phase != aiv1.AutopilotDone {
			// Re-planned or switched back on after it was closed.
			_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{StateEvent: "reopen"})
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
				return fmt.Errorf("reopening the tracking issue #%d: %w", is.IID, err)
			}
		}
		return nil
	}
	is, err := a.gl.CreateIssue(ctx, a.spec.GitLab.Project, gitlab.CreateIssueOptions{
		Title:       "🤖 Autopilot: " + a.st.Spec.Milestone.Title,
		Description: a.renderTracking(),
		Labels:      a.spec.Workflow.GeneratedLabel,
		MilestoneID: a.st.Spec.Milestone.ID,
	})
	if err != nil {
		return fmt.Errorf("creating the tracking issue: %w", err)
	}
	a.log.Info("autopilot tracking issue created", "issue", is.IID)
	a.ap.TrackingIssueIID = is.IID
	a.ap.TrackingHash = hashString(a.renderTracking())
	return nil
}

func (a *autopilot) updateTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	body := a.renderTracking()
	if hash := hashString(body); hash != a.ap.TrackingHash {
		_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{Description: body})
		if a.trackingGone(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("updating the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
		}
		a.ap.TrackingHash = hash
	}
	return nil
}

func (a *autopilot) closeTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	if err := a.updateTracking(ctx); err != nil {
		return err
	}
	_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{StateEvent: "close"})
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("closing the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// mention returns the line that mentions the milestone's owners, if the
// event mentions them.
func (a *autopilot) mention(e aiv1.NotifyEvent) string {
	if !a.spec.Notify.Wants(e) {
		return ""
	}
	return notify.Mention(a.st.Spec.Owners)
}

func (a *autopilot) comment(ctx context.Context, body string) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	_, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, "🤖 "+body)
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commenting on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// trackingGone reports whether err says the tracking issue no longer exists.
// It is then forgotten, and a new one is created on the next poll.
func (a *autopilot) trackingGone(err error) bool {
	if a.ap.TrackingIssueIID == 0 || !gitlab.IsNotFound(err) {
		return false
	}
	a.log.Info("autopilot tracking issue is gone, a new one is created", "issue", a.ap.TrackingIssueIID)
	a.ap.TrackingIssueIID, a.ap.TrackingHash = 0, ""
	return true
}

func (a *autopilot) save(ctx context.Context) error {
	a.stamp()
	if equality.Semantic.DeepEqual(a.st.Status.Autopilot, a.ap) {
		return nil
	}
	orig := a.st.DeepCopy()
	a.st.Status.Autopilot = a.ap
	return a.r.writeFailed(ctx, a.st, "autopilot.save", a.r.Status().Patch(ctx, a.st, client.MergeFrom(orig)))
}

// renderTracking renders the tracking issue's description.
func (a *autopilot) renderTracking() string {
	var b strings.Builder
	prefix := a.spec.Workflow.CommandPrefix
	fmt.Fprintf(&b, "**Autopilot** for milestone **%s**: ", a.st.Spec.Milestone.Title)
	switch a.ap.Phase {
	case aiv1.AutopilotPaused:
		next := fmt.Sprintf("Comment `%s resume` to continue", prefix)
		if a.ap.PauseKind == aiv1.PauseEmptyPlan {
			next = "Change the milestone description to plan it again"
		}
		fmt.Fprintf(&b, "⏸️ paused, %s. %s.\n", notify.Defuse(a.ap.PausedReason), next)
	case aiv1.AutopilotDone:
		b.WriteString("✅ done.\n")
	case aiv1.AutopilotOff:
		b.WriteString("⏹️ off (the marker was removed from the milestone).\n")
	default:
		switch {
		case len(a.st.Status.Plan) > 0 && len(a.ap.CloseWaitsForCompanions) > 0:
			fmt.Fprintf(&b, "▶️ running, every issue is through; done once the companions %s have landed (merged or closed).\n",
				strings.Join(a.ap.CloseWaitsForCompanions, ", "))
		case a.holding:
			fmt.Fprintf(&b, "⏳ waiting for its turn: %s go first.\n", milestoneList(a.ahead))
		case len(a.st.Status.Plan) > 0:
			b.WriteString("▶️ running.\n")
		case a.waitingForTurn():
			// Not planned until the older milestones are through (decision 9).
			ahead := slices.Sorted(slices.Values(a.st.Spec.QueuedBehind))
			if len(ahead) == 0 {
				b.WriteString("⏳ waiting for its turn, planning starts on the next poll.\n")
				break
			}
			fmt.Fprintf(&b, "⏳ waiting for its turn: %s go first. Comment `%s start` to plan it now (owners).\n", milestoneList(ahead), prefix)
		case a.st.RunPending() || a.st.Spec.RunRequest == 0 && a.ap.StartRequestedBy != "":
			b.WriteString("📝 planning, the planning agent is writing the issues.\n")
		default:
			b.WriteString("⚠️ not planned, planning has not succeeded yet. Nothing starts until the milestone is planned.\n")
		}
	}
	b.WriteString("\nThe operator works through the planned issues one at a time, in dependency order. It merges each merge request " +
		"when its pipeline is green and its review is clean, then starts the next issue. The agents take their defaults instead of " +
		"asking; every decision is listed below and can be changed with `" + prefix + " revisit <id> <what you want instead>` on its issue.\n\n")
	start := ""
	if a.waitingForTurn() {
		start = fmt.Sprintf("`%s start` (owners: plan it now, ahead of its turn), ", prefix)
	}
	fmt.Fprintf(&b, "Commands, as comments here: %s`%s pause`, `%s resume`, `%s skip #<issue>`.\n\n### Issues\n\n", start, prefix, prefix, prefix)
	if len(a.st.Status.Plan) == 0 && len(a.ap.Items) == 0 {
		b.WriteString("_Listed once the milestone is planned._\n")
	}
	for _, it := range a.ap.Items {
		title := a.issues[it.IID].Title
		mr := ""
		if it.MergeRequestIID != 0 {
			mr = fmt.Sprintf(" !%d", it.MergeRequestIID)
		}
		switch it.State {
		case aiv1.AutopilotItemMerged:
			fmt.Fprintf(&b, "- [x] #%d %s: merged%s (`%s`)\n", it.IID, title, mr, short(it.MergedSHA))
		case aiv1.AutopilotItemSkipped:
			fmt.Fprintf(&b, "- [ ] ~~#%d %s~~: skipped\n", it.IID, title)
		case aiv1.AutopilotItemActive:
			fmt.Fprintf(&b, "- [ ] #%d %s: **in progress**%s (%s)\n", it.IID, title, mr, a.labelOf(it.IID))
		default:
			waits := ""
			if deps := a.deps(it.IID); len(deps) > 0 {
				waits = ", after " + issueList(deps)
			}
			if h := a.holds[it.IID]; h != nil {
				waits += ", " + h.waits()
			}
			fmt.Fprintf(&b, "- [ ] #%d %s: pending%s\n", it.IID, title, waits)
		}
	}
	var decisions []string
	for _, it := range a.ap.Items {
		for _, d := range it.Decisions {
			line := fmt.Sprintf("[%s](%s) %s (#%d)", d.ID, d.URL, d.Title, it.IID)
			if d.Superseded {
				line = "~~" + line + "~~ superseded"
			}
			decisions = append(decisions, "- "+line)
		}
	}
	b.WriteString(rulesSection(a.st))
	b.WriteString("\n### Decisions\n\n")
	if len(decisions) == 0 {
		b.WriteString("_Listed here once an issue is merged; until then they are on the issue._\n")
	} else {
		b.WriteString(strings.Join(decisions, "\n") + "\n")
	}
	b.WriteString("\n" + marker.TrackingIssue + "\n")
	return b.String()
}

func (a *autopilot) labelOf(iid int64) string {
	wf := a.spec.Workflow
	is := a.issues[iid]
	for _, l := range []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel} {
		if is.HasLabel(l) {
			return l
		}
	}
	return "queued"
}

func hasAny(is gitlab.Issue, labels ...string) bool {
	for _, l := range labels {
		if is.HasLabel(l) {
			return true
		}
	}
	return false
}

// milestoneList renders milestone iids as GitLab references.
func milestoneList(iids []int64) string {
	refs := make([]string, len(iids))
	for i, iid := range iids {
		refs[i] = fmt.Sprintf("%%%d", iid)
	}
	return strings.Join(refs, ", ")
}

func issueList(iids []int64) string {
	parts := make([]string, len(iids))
	for i, iid := range iids {
		parts[i] = fmt.Sprintf("#%d", iid)
	}
	return strings.Join(parts, ", ")
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
