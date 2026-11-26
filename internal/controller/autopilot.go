package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/conventional"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func (a *autopilot) sync(ctx context.Context) error {
	if !a.st.Spec.Autopilot {
		return a.off(ctx)
	}
	if a.ap.Phase == aiv1.AutopilotOff {
		a.ap.Phase = aiv1.AutopilotRunning
	}
	a.ap.MaxParallel = a.maxParallel()
	if now := a.r.now(); autopilotSettling(a.st, now) {
		done, err := a.settle(ctx)
		if err != nil {
			return err // tried again on the next poll
		}
		// After settle compared: a stamp alone never re-renders the tracking issue.
		a.ap.SettleSyncedAt = &metav1.Time{Time: now}
		if done {
			return nil
		}
	}
	if err := a.loadIssues(ctx); err != nil {
		return err
	}
	if err := a.loadMainRedFix(ctx); err != nil {
		return err
	}
	order, cycle := queueOrder(a.st.Status.Plan)
	a.ap.Items = mergeQueue(a.ap.Items, order)
	a.planReleases()
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
	if err := a.resumeHolds(ctx); err != nil {
		return err
	}
	if err := a.companionLanding(ctx); err != nil {
		return err
	}
	if err := a.unstick(ctx); err != nil {
		return err
	}
	if err := a.releasedByHand(ctx); err != nil {
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
	// Every poll, also while paused: the queue does not wait for releases.
	if err := a.companionReleases(ctx); err != nil {
		return err
	}
	if err := a.releases(ctx); err != nil {
		return err
	}
	return a.updateTracking(ctx)
}

// loadIssues lists the milestone's issues, open and closed, into a.issues.
func (a *autopilot) loadIssues(ctx context.Context) error {
	issues, err := a.gl.ListIssues(ctx, a.spec.GitLab.Project, gitlab.ListIssuesOptions{Milestone: a.st.Spec.Milestone.Title, State: "all"})
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", a.st.Spec.Milestone.Title, err)
	}
	a.issues = map[int64]gitlab.Issue{}
	for _, is := range issues {
		a.issues[is.IID] = is
	}
	return nil
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
		return a.resumed(ctx, byOperator, why)
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

// maxParallel is the effective parallel-issue limit for this milestone:
// the description's `/agent parallel N` when set, else the project's. The
// loose issues queue has its own, looseIssues.maxParallel.
func (a *autopilot) maxParallel() int32 {
	if a.loose {
		return a.spec.LooseIssues.Parallel()
	}
	if a.st.Spec.MaxParallel > 0 {
		return a.st.Spec.MaxParallel
	}
	return a.spec.Autopilot.MaxParallel
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
	ahead []int64
	// peers are the project's spec tasks by milestone iid (nil when
	// switching off), for the move command's running milestone and refusals.
	peers   map[int64]*aiv1.AgentTask
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
	// notes are the tracking issue's notes when settle read them already.
	notes []gitlab.Note
	// loose: st is the project's Queue task (the loose issues queue), not a
	// milestone's spec task: st.Spec.Milestone is nil.
	loose bool
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

// onDepCycle reports whether iid reaches itself through dependencies that are
// still pending: a real cycle, which can never start.
func (a *autopilot) onDepCycle(iid int64) bool {
	seen := map[int64]bool{}
	var walk func(int64) bool
	walk = func(n int64) bool {
		for _, d := range a.deps(n) {
			if d == iid {
				return true
			}
			if it := a.item(d); it == nil || it.State != aiv1.AutopilotItemPending || seen[d] {
				continue
			}
			seen[d] = true
			if walk(d) {
				return true
			}
		}
		return false
	}
	return walk(iid)
}

// step drives every active item in queue order until one pauses, then starts
// pending items while slots are free (maxParallel): an issue starts once its
// dependencies are merged, without waiting for a whole layer of the plan.
// A held item is not driven: its hold ends elsewhere.
func (a *autopilot) step(ctx context.Context) error {
	phase, before := a.ap.Phase, a.active() // Running, or Done when a re-plan added issues
	for i := range a.ap.Items {
		if it := &a.ap.Items[i]; it.State == aiv1.AutopilotItemActive && it.Hold == nil && a.ap.Phase == phase {
			if err := a.drive(ctx, it); err != nil {
				return err
			}
		}
	}
	// A poll that merged or skipped an item starts nothing: the next one
	// sees GitLab after the merge, as when the queue ran one issue at a time.
	if a.ap.Phase != phase || a.active() < before || a.st.RunPending() {
		return nil // paused; or being planned again, pick from the new plan
	}
	if a.loose {
		// Unrelated issues: all held is no stuck pause, they wait for an owner.
		return a.pickLoose(ctx)
	}
	for range len(a.ap.Items) + 1 {
		running := a.running()
		if running >= int(a.maxParallel()) {
			return nil
		}
		if err := a.pick(ctx); err != nil {
			return err
		}
		if a.running() <= running || a.ap.Phase == aiv1.AutopilotPaused || a.holding {
			break
		}
	}
	return a.stuckOnHolds(ctx)
}

// stuckOnHolds pauses stuck when nothing can move: every active item is held
// and no pending one can start. An active main-red fix moves (it is never
// held), so that is no stuck.
func (a *autopilot) stuckOnHolds(ctx context.Context) error {
	held := a.allHeld()
	fix := a.ap.MainRedFix != nil && a.ap.MainRedFix.State == aiv1.AutopilotItemActive
	if len(held) == 0 || fix || a.ap.Phase != aiv1.AutopilotRunning || a.holding {
		return nil
	}
	s, err := a.startable(ctx)
	if err != nil || s.dep != nil || s.it != nil || s.landing {
		return err
	}
	reason := fmt.Sprintf("nothing can move: every running issue is held (%s)", strings.Join(held, ", "))
	if len(s.waiting) > 0 {
		reason += "; " + strings.Join(s.waiting, ", ")
	}
	return a.pause(ctx, aiv1.PauseStuck, 0, reason)
}

// allHeld lists the active items as "#N: kind" if every one is held, else
// (also with none) nil.
func (a *autopilot) allHeld() []string {
	var held []string
	for _, it := range a.ap.Items {
		if it.State != aiv1.AutopilotItemActive {
			continue
		}
		if it.Hold == nil {
			return nil
		}
		held = append(held, fmt.Sprintf("#%d: %s", it.IID, it.Hold.Kind))
	}
	return held
}

// unheld is the first active item that is not held, or nil.
func (a *autopilot) unheld() *aiv1.AutopilotItem {
	for i := range a.ap.Items {
		if it := &a.ap.Items[i]; it.State == aiv1.AutopilotItemActive && it.Hold == nil {
			return it
		}
	}
	return nil
}

// active counts the active items.
func (a *autopilot) active() int {
	n := 0
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemActive {
			n++
		}
	}
	return n
}

// running counts the issues that hold a slot: the active items but the held
// ones and the landing one, which counts too at a limit of 1 (one issue at a
// time). In the loose issues queue every active item holds one, held or
// landing: it never runs more than maxParallel issues.
func (a *autopilot) running() int {
	if a.loose {
		return a.active()
	}
	n := 0
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemActive && it.Hold == nil && (it.IID != a.ap.Landing || a.maxParallel() <= 1) {
			n++
		}
	}
	return n
}

// land says whether it, ready to land, may go through rebase, CI and merge
// now: ready items land one at a time, in the order they became ready
// (ReadyAt, then queue order); held items do not. The main-red fix does not
// wait.
func (a *autopilot) land(it *aiv1.AutopilotItem) bool {
	if it.ReadyAt == nil {
		it.ReadyAt = &metav1.Time{Time: a.r.now()}
	}
	if it == a.ap.MainRedFix || a.ap.Landing == it.IID {
		return true
	}
	if l := a.item(a.ap.Landing); l != nil && l.State == aiv1.AutopilotItemActive && l.Hold == nil {
		return false
	}
	var first *aiv1.AutopilotItem
	for i := range a.ap.Items {
		c := &a.ap.Items[i]
		if c.State == aiv1.AutopilotItemActive && c.Hold == nil && c.ReadyAt != nil && (first == nil || c.ReadyAt.Before(first.ReadyAt)) {
			first = c
		}
	}
	if first != it {
		return false
	}
	a.log.Info("autopilot lands", "issue", it.IID)
	a.ap.Landing = it.IID
	return true
}

// unready takes it out of the landing lane and the ready queue: merged,
// skipped, paused, or a new head that is checked again.
func (a *autopilot) unready(it *aiv1.AutopilotItem) {
	if a.ap.Landing == it.IID {
		a.ap.Landing = 0
	}
	it.ReadyAt = nil
}

// pick activates the next pending issue whose dependencies are merged: the
// plan's, and open issues it is linked as "blocked by" to in GitLab
// (Premium), so a link a human adds holds an issue back too (startable).
// With none, it pauses stuck (or is done) unless an issue is active;
// unstick lifts that pause. An older open
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
		// No hold from before a skip.
		it.State, it.Hold = aiv1.AutopilotItemActive, nil
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
	case a.active() > 0:
		// The active issues move the queue: neither stuck nor done.
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
			} else {
				waits[it.IID] = fmt.Sprintf("#%d waits for #%d", it.IID, d) // held, when stuck on holds
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
		if !ok || a.onDepCycle(it.IID) {
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
	case a.unheld() != nil: // a hold ended
		why = fmt.Sprintf("#%d goes on", a.unheld().IID)
	case len(s.waiting) == 0 && a.allHeld() == nil: // else still stuck on holds
		why = "no issue waits any more"
	case s.landing:
		why = "the queue waits for a companion to land, and goes on by itself once it has"
	default:
		return nil
	}
	a.log.Info("autopilot continues by itself", "why", why)
	return a.resumed(ctx, byOperator, why)
}

// drive moves the active issue towards its merge.
func (a *autopilot) drive(ctx context.Context, it *aiv1.AutopilotItem) error {
	wf := a.spec.Workflow
	glProject := a.spec.GitLab.Project
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	ready := false
	defer func() {
		// A new push or run, or a head that is not ready (any more): the
		// item leaves the ready queue. The landing item keeps the lane while
		// CI runs on its rebased head.
		if !ready && a.ap.Landing != it.IID {
			it.ReadyAt = nil
		}
	}()
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
		it.State, it.RebasedFrom, it.Hold = aiv1.AutopilotItemSkipped, "", nil
		a.unready(it)
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
		return a.r.backToWorking(ctx, a.gl, a.spec, it.IID)
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
	// Ready to land; one item lands at a time, the others wait (land).
	ready = true
	if !a.land(it) {
		return nil
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
	return a.r.backToWorking(ctx, a.gl, a.spec, it.IID)
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
