package runner

import (
	"cmp"
	"context"
	"strings"

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
}

const (
	planDraftKept  = "(Your saved edit was kept; this turn's draft was not applied.)"
	planDraftLarge = "(This turn's draft is over 64 KiB and was not applied.)"
	maxPlanDraft   = 64 << 10
)

// turn answers msg with the spec role's engine session and writes the reply
// and the agent's draft to the plan's status. The draft is only written while
// the revision is the one the turn started from: an owner's save wins.
func (p *PlanRun) turn(ctx context.Context, plan *aiv1.AgentPlan, project *aiv1.AgentProject, msg aiv1.ChatMessage) error {
	st, err := p.loadState()
	if err != nil {
		return err
	}
	if err := p.patchPlan(ctx, func(s *aiv1.AgentPlanStatus) { s.Phase = aiv1.AgentPlanWorking }); err != nil {
		return err
	}
	p.run = nil // a run log per turn, not one growing for the pod's life
	out, sha, errStr := p.ask(ctx, &st, plan, project.Spec.WithDefaults(), msg)
	if err := p.saveState(st); err != nil {
		p.Log.Error(err, "saving the plan state")
	}
	base, draft, at := plan.Status.DraftRevision, strings.TrimSpace(out.Draft), metav1.Time{Time: p.now()}
	return p.patchPlan(ctx, func(s *aiv1.AgentPlanStatus) {
		text := textutil.Truncate(prose(out.Reply), maxChatReplyBytes-128)
		switch {
		case draft == "" || draft == s.Draft:
		case s.DraftRevision != base:
			text += "\n\n" + planDraftKept
		case len(draft) > maxPlanDraft:
			text += "\n\n" + planDraftLarge
		default:
			s.Draft, s.DraftRevision, s.DraftBy, s.DraftSHA = draft, base+1, "agent", sha
		}
		if s.Chat == nil {
			s.Chat = &aiv1.ChatStatus{}
		}
		s.Chat.Replies = appendChatReply(s.Chat.Replies, aiv1.ChatReply{MessageID: msg.ID, Text: text, At: at, Error: errStr}, nil)
		s.Chat.LastTurnTime = &at
		if s.Phase == aiv1.AgentPlanWorking {
			s.Phase = aiv1.AgentPlanReady
		}
	})
}

// ask fetches the default branch and runs the turn on the engine, resuming
// the plan session. errStr says why it did not answer; out.Reply then holds
// the engine's raw text, if any.
func (p *PlanRun) ask(ctx context.Context, st *state, plan *aiv1.AgentPlan, spec aiv1.AgentProjectSpec, msg aiv1.ChatMessage) (out PlanTurn, sha, errStr string) {
	proj, err := p.GitLab.GetProject(ctx, spec.GitLab.Project)
	if err == nil {
		err = p.syncWorkspace(ctx, proj.HTTPURLToRepo, proj.DefaultBranch, proj.DefaultBranch, nil)
	}
	if err != nil {
		return out, "", "fetching the default branch: " + textutil.Truncate(err.Error(), 200)
	}
	sha, _ = p.Mirror.resolve(ctx, "refs/remotes/origin/"+proj.DefaultBranch)
	gl, err := planContext(ctx, p.GitLab, spec.GitLab.Project)
	if err != nil {
		p.Log.Info("plan turn without the GitLab context", "error", err.Error())
	}
	role, rev := spec.Agents.Spec, plan.Status.DraftRevision
	inv := roleInvocation(role, planSystemPrompt, planTools, planTurnSchema, true)
	inv.Role, inv.Kind, inv.Resume, inv.LogPrompt = "plan", aiv1.CallKindPlan, st.session("plan", role.Engine), spec.Agents.LogPrompts
	inv.Prompt = buildPlanPrompt(planTurnInput{Message: msg.Text, Branch: proj.DefaultBranch, SHA: sha, GitLab: gl,
		FirstTurn: inv.Resume == "", Title: plan.Spec.Title, Draft: plan.Status.Draft,
		Edited: plan.Status.DraftBy == "owner" && rev > st.PlanSeenRevision})
	res, err := p.Agent.Run(ctx, role.Engine, inv)
	if res != nil {
		out.Reply = cmp.Or(res.Text, string(res.Structured))
		if res.SessionID != "" {
			st.setSession("plan", role.Engine, res.SessionID)
		}
	}
	err = classifyLimit(role.Engine, err, res)
	if lim, ok := AsUsageLimit(err); ok {
		return out, sha, shortUsageLimit(lim)
	}
	if err != nil {
		return out, sha, textutil.Truncate(err.Error(), 200)
	}
	var t PlanTurn
	if res == nil || res.Decode(&t) != nil {
		return out, sha, "the agent's answer was not valid"
	}
	st.PlanSeenRevision = rev
	return t, sha, ""
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
