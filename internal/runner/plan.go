package runner

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// PlanRun answers an AgentPlan's chat messages in the plan's pod. The
// Runner brings the Kubernetes client, namespace, agent, mirror, state dir,
// clock and logger, and the git code that fetches the default branch.
type PlanRun struct {
	*Runner
	PlanName string
	GitLab   planGitLab
	// MaxBudgetUSD is the cap AI_PLAN_MAX_BUDGET_USD; a raised
	// spec.maxBudgetUSD wins, so a raise needs no new pod.
	MaxBudgetUSD string
	// wake signals changes of the plan while Loop runs.
	wake <-chan struct{}
}

const (
	maxPlanDraft = 64 << 10
	// maxPlanTitle mirrors the CRD's MaxLength of status.title, in runes.
	maxPlanTitle = 255
)

// planTitleFrom cleans the agent's title: its first line, trimmed, cut to
// maxPlanTitle runes.
func planTitleFrom(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxPlanTitle {
		s = strings.TrimSpace(string(r[:maxPlanTitle]))
	}
	return s
}

// turn answers msg with the spec role's engine session and writes the reply
// and the agent's drafts to the plan's status. A draft is only written while
// its revision is the one the turn started from: an owner's save wins.
// A newer interrupt message cancels the turn; a pod stop leaves it in
// flight, and the next pod sends it again, once.
func (p *PlanRun) turn(ctx context.Context, plan *aiv1.AgentPlan, project *aiv1.AgentProject, msg aiv1.ChatMessage) error {
	st, err := p.loadState()
	if err != nil {
		return err
	}
	budget := plan.BudgetUSD(p.MaxBudgetUSD)
	left := aiv1.BudgetLeft(budget, plan.Status.SpendUSD, 0)
	if left <= 0 {
		return p.answer(ctx, &st, func(*aiv1.AgentPlanStatus) []aiv1.ChatReply {
			return []aiv1.ChatReply{{MessageID: msg.ID, At: metav1.Time{Time: p.now()},
				Error: fmt.Sprintf("the plan's budget of $%s is used; raise it on the Desk", aiv1.ShowCost(aiv1.CostOf(budget)))}}
		})
	}
	resend := st.ChatInFlight != nil && st.ChatInFlight.MessageID == msg.ID
	if resend && st.ChatInFlight.Resent {
		return p.answer(ctx, &st, func(*aiv1.AgentPlanStatus) []aiv1.ChatReply {
			return []aiv1.ChatReply{{MessageID: msg.ID, At: metav1.Time{Time: p.now()}, Error: "interrupted by a restart"}}
		})
	}
	st.ChatInFlight = &chatInFlight{MessageID: msg.ID, Resent: resend}
	if err := p.saveState(st); err != nil {
		return err
	}
	if err := p.patchPlan(ctx, func(s *aiv1.AgentPlanStatus) { s.Phase = aiv1.AgentPlanWorking }); err != nil {
		return err
	}
	p.run = nil // a run log per turn, not one growing for the pod's life
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard, by, stop := &chatTurnState{}, make(chan int64, 1), make(chan struct{})
	go p.watchInterrupt(ctx, msg.ID, stop, func(id int64) {
		if by <- id; guard.interrupt() {
			cancel()
		}
	})
	out, sha, call, errStr := p.ask(turnCtx, &st, plan, project.Spec.WithDefaults(), msg, left)
	close(stop)
	if err := p.saveState(st); err != nil {
		p.Log.Error(err, "saving the plan state")
	}
	var also []aiv1.ChatReply
	at := metav1.Time{Time: p.now()}
	if guard.finish() {
		out.Drafts, out.Title, out.Decisions, errStr, also = nil, "", nil, "interrupted", []aiv1.ChatReply{{MessageID: <-by, At: at}}
	} else if ctx.Err() != nil {
		return ctx.Err() // the pod stops: still in flight
	}
	base, title := map[int64]int64{}, planTitleFrom(out.Title)
	for _, d := range plan.DraftList() {
		base[d.Key] = d.Revision
	}
	answers := msg.Decisions
	if errStr != "" {
		answers = nil // a failed or interrupted turn applies no answers: the owner sends them again
	}
	return p.answer(ctx, &st, func(s *aiv1.AgentPlanStatus) []aiv1.ChatReply {
		notes, refused := applyPlanDrafts(s, base, out.Drafts, sha)
		note := textutil.Truncate(strings.Join(append([]string{""}, notes...), "\n\n"), 2048)
		text := textutil.Truncate(prose(out.Reply), maxChatReplyBytes-128-len(note)) + note
		// The list follows the drafts: not applied when every changed draft
		// was refused. The owner's keeps sign off even when it is not applied.
		merged := false
		if len(out.Decisions) > 0 && !allPlanDraftsRefused(refused) {
			if err := validPlanDecisions(s.Decisions, s.DecisionSeq, out.Decisions); err != nil {
				p.Log.Info("plan decisions not applied", "error", err.Error())
				text += "\n\n" + planDecisionsInvalid
			} else {
				s.Decisions, s.DecisionSeq = mergePlanDecisions(s.Decisions, s.DecisionSeq, out.Decisions,
					answers, msg.Author, msg.AuthorName, at.Time)
				merged = true
			}
		}
		if !merged {
			s.Decisions = signOffKept(s.Decisions, answers, msg.Author, msg.AuthorName, at.Time)
		}
		if title != "" && s.Title == "" && plan.Spec.Title == "" {
			s.Title = title // the agent names the plan once
		}
		if call != nil {
			addPlanCall(s, *call)
		}
		s.Chat.LastTurnTime = &at
		if s.Phase == aiv1.AgentPlanWorking {
			s.Phase = aiv1.AgentPlanReady
		}
		return append([]aiv1.ChatReply{{MessageID: msg.ID, Text: text, At: at, Error: errStr}}, also...)
	})
}

// Loop answers the plan's messages, oldest first, until ctx ends. An
// interrupt with no turn to stop gets an empty reply.
func (p *PlanRun) Loop(ctx context.Context) error {
	if !p.waitAgent(ctx) {
		return nil
	}
	p.wake = p.watchObject(ctx, &aiv1.AgentPlanList{}, p.PlanName)
	for ctx.Err() == nil {
		did, err := p.tick(ctx)
		if err != nil && ctx.Err() == nil {
			p.Log.Error(err, "plan turn failed") // retried after the poll interval
		}
		if !did {
			select {
			case <-ctx.Done():
			case <-p.wake:
			case <-time.After(cmp.Or(p.PollInterval, 10*time.Second)):
			}
		}
	}
	return nil
}

func (p *PlanRun) tick(ctx context.Context) (bool, error) {
	plan, project, st, err := p.load(ctx)
	msg, ok := nextChatMessage(plan.Spec.ChatMessages, plan.Status.Chat, st)
	if err != nil || !ok || !plan.Active() {
		return false, err
	}
	if msg.Kind == "interrupt" {
		err = p.answer(ctx, &st, func(*aiv1.AgentPlanStatus) []aiv1.ChatReply { return []aiv1.ChatReply{{MessageID: msg.ID}} })
	} else {
		err = p.turn(ctx, plan, project, msg)
	}
	return err == nil, err
}

// load reads the plan, its project and the runner's state.
func (p *PlanRun) load(ctx context.Context) (*aiv1.AgentPlan, *aiv1.AgentProject, state, error) {
	plan, project := &aiv1.AgentPlan{}, &aiv1.AgentProject{}
	err := p.K8s.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: p.PlanName}, plan)
	if err == nil {
		err = p.K8s.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: plan.Spec.ProjectRef}, project)
	}
	st, serr := p.loadState()
	return plan, project, st, cmp.Or(err, serr)
}

// watchInterrupt calls interrupt with the first unanswered interrupt message
// newer than current, until stop closes.
func (p *PlanRun) watchInterrupt(ctx context.Context, current int64, stop <-chan struct{}, interrupt func(int64)) {
	for {
		plan, _, st, err := p.load(ctx)
		for _, m := range plan.Spec.ChatMessages {
			if err == nil && m.Kind == "interrupt" && m.ID > current && !answeredChat(plan.Status.Chat, st, m.ID) {
				interrupt(m.ID)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-p.wake:
		case <-time.After(2 * time.Second):
		}
	}
}

// answer patches the replies mutate returns into the plan's status, marks
// their messages answered and clears the turn in flight.
func (p *PlanRun) answer(ctx context.Context, st *state, mutate func(*aiv1.AgentPlanStatus) []aiv1.ChatReply) error {
	var ids []int64
	err := p.patchPlan(ctx, func(s *aiv1.AgentPlanStatus) {
		s.Chat, ids = cmp.Or(s.Chat, &aiv1.ChatStatus{}), nil
		for _, rep := range mutate(s) {
			s.Chat.Replies, ids = appendChatReply(s.Chat.Replies, rep, nil), append(ids, rep.MessageID)
		}
	})
	if err != nil {
		return err
	}
	st.ChatAnswered = append(st.ChatAnswered, ids...)
	st.ChatAnswered, st.ChatInFlight = st.ChatAnswered[max(0, len(st.ChatAnswered)-maxChatAnswered):], nil
	return p.saveState(*st)
}

// ask fetches the default branch and runs the turn on the engine, resuming
// the plan session, with at most left USD. errStr says why it did not
// answer; out.Reply then holds the engine's raw text, if any. call is the
// engine call, nil when the engine did not run.
func (p *PlanRun) ask(ctx context.Context, st *state, plan *aiv1.AgentPlan, spec aiv1.AgentProjectSpec, msg aiv1.ChatMessage, left float64) (out PlanTurn, sha string, call *aiv1.AgentCall, errStr string) {
	proj, err := p.GitLab.GetProject(ctx, spec.GitLab.Project)
	if err == nil {
		err = p.syncWorkspace(ctx, proj.HTTPURLToRepo, proj.DefaultBranch, proj.DefaultBranch, nil)
	}
	if err != nil {
		return out, "", nil, "fetching the default branch: " + textutil.Truncate(err.Error(), 200)
	}
	sha, _ = p.Mirror.resolve(ctx, "refs/remotes/origin/"+proj.DefaultBranch)
	refs, drafts := planRefs(msg.Text), plan.DraftList()
	milestones := p.writePlanFiles(ctx, plan.Spec.ProjectRef, spec.GitLab.Project, refs, drafts)
	gl, err := planContext(ctx, p.GitLab, spec.GitLab.Project, milestones)
	if err != nil {
		p.Log.Info("plan turn without the GitLab context", "error", err.Error())
	}
	role := spec.Agents.Spec
	lines, revs := planDraftLines(drafts, st.planSeen())
	inv := roleInvocation(role, planSystemPrompt, planTools, planTurnSchema, true)
	inv.Role, inv.Kind, inv.Resume, inv.LogPrompt = "plan", aiv1.CallKindPlan, st.session("plan", role.Engine), spec.Agents.LogPrompts
	inv.Prompt = buildPlanPrompt(planTurnInput{Message: msg.Text, Refs: planRefList(refs, milestones), Branch: proj.DefaultBranch, SHA: sha, GitLab: gl,
		FirstTurn: inv.Resume == "", Title: plan.Spec.Title, Drafts: lines,
		Untitled:   plan.Spec.Title == "" && plan.Status.Title == "",
		MaxMRLines: spec.MaxMRLines(), Constraints: spec.HouseConstraints(),
		DecisionIDs: planDecisionIDs(plan.Status.Decisions), NextDecisionID: int(plan.Status.DecisionSeq) + 1,
		Answers: msg.Decisions, Decisions: plan.Status.Decisions})
	if st.ChatInFlight != nil && st.ChatInFlight.Resent {
		inv.Prompt = chatResendPrefix + "\n\n" + inv.Prompt
	}
	if roleCap := aiv1.CostOf(inv.MaxBudgetUSD); roleCap > 0 {
		left = min(left, roleCap)
	}
	if !math.IsInf(left, 1) {
		inv.MaxBudgetUSD = aiv1.ShowCost(left) // claude only
	}
	start := p.now()
	res, err := p.Agent.Run(ctx, role.Engine, inv)
	c := agentCall(role.Engine, inv, res, start, p.now())
	call = &c
	if res != nil {
		out.Reply = cmp.Or(res.Text, string(res.Structured))
		if res.SessionID != "" {
			st.setSession("plan", role.Engine, res.SessionID)
		}
	}
	err = classifyLimit(role.Engine, err, res)
	if lim, ok := AsUsageLimit(err); ok {
		return out, sha, call, shortUsageLimit(lim)
	}
	if err != nil {
		return out, sha, call, textutil.Truncate(err.Error(), 200)
	}
	var t PlanTurn
	if res == nil || res.Decode(&t) != nil {
		return out, sha, call, "the agent's answer was not valid"
	}
	st.PlanSeen, st.PlanSeenRevision = revs, 0
	return t, sha, call, ""
}

// addPlanCall appends c to the plan's calls, bounded as a run's are, and
// adds its tokens and, when known, its cost to the plan's totals.
func addPlanCall(s *aiv1.AgentPlanStatus, c aiv1.AgentCall) {
	rs := aiv1.RunStatus{Calls: s.Calls}
	if n := len(s.Calls); n > 0 {
		rs.LastCallSeq = s.Calls[n-1].Seq
	}
	aiv1.AppendCalls(&rs, c)
	s.Calls, s.Tokens = rs.Calls, s.Tokens+c.InputTokens+c.OutputTokens+c.CacheReadTokens+c.CacheWriteTokens
	if c.CostSource == aiv1.CostReported {
		s.SpendUSD = aiv1.AddCost(s.SpendUSD, aiv1.CostOf(c.CostUSD))
	}
}

// patchPlan merge-patches the plan's status with an optimistic lock, so
// mutate always sees the status it changes.
func (p *PlanRun) patchPlan(ctx context.Context, mutate func(*aiv1.AgentPlanStatus)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var plan aiv1.AgentPlan
		if err := p.K8s.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: p.PlanName}, &plan); err != nil {
			return err
		}
		orig := plan.DeepCopy()
		mutate(&plan.Status)
		return p.K8s.Status().Patch(ctx, &plan, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	})
}
