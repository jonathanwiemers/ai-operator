package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// planTurnEngine answers plan turns with answers[i] for the i-th call.
type planTurnEngine struct {
	mu      sync.Mutex
	calls   []Invocation
	ctxs    []context.Context
	answers []func(inv Invocation) (*Result, error)
}

func (e *planTurnEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	e.mu.Lock()
	e.calls, e.ctxs = append(e.calls, inv), append(e.ctxs, ctx)
	answer := e.answers[len(e.calls)-1]
	e.mu.Unlock()
	return answer(inv)
}

func planAnswer(session, reply, draft string) func(Invocation) (*Result, error) {
	return func(Invocation) (*Result, error) {
		b, _ := json.Marshal(PlanTurn{Reply: reply, Draft: draft})
		return &Result{SessionID: session, CostUSD: 0.1, Structured: b}, nil
	}
}

type planEnv struct {
	k8s     client.Client
	project *aiv1.AgentProject
	run     *PlanRun
	engine  *planTurnEngine
	mainSHA string
}

func newPlanEnv(t *testing.T, answers ...func(Invocation) (*Result, error)) *planEnv {
	t.Helper()
	e := newImplementEnv(t)
	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec:       aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"}, Agents: aiv1.AgentsSpec{Image: "x"}},
	}
	plan := &aiv1.AgentPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-plan-1", Namespace: "agents"},
		Spec:       aiv1.AgentPlanSpec{ProjectRef: "shop", Title: "Show the version", CreatedBy: "alice"},
		Status:     aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanReady},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, plan).
		WithStatusSubresource(&aiv1.AgentPlan{}).Build()
	engine := &planTurnEngine{answers: answers}
	r := newTestRunner(t, e.tmp, k8s, e.gl, engine, "")
	sha := strings.TrimSpace(gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", "main"))
	return &planEnv{k8s: k8s, project: project, run: &PlanRun{Runner: r, PlanName: plan.Name, GitLab: e.gl}, engine: engine, mainSHA: sha}
}

func (e *planEnv) plan(t *testing.T) *aiv1.AgentPlan {
	t.Helper()
	var p aiv1.AgentPlan
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop-plan-1"}, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func (e *planEnv) turn(t *testing.T, id int64, text string) *aiv1.AgentPlan {
	t.Helper()
	if err := e.run.turn(context.Background(), e.plan(t), e.project, chatMsg(id, "message", text, "alice")); err != nil {
		t.Fatal(err)
	}
	return e.plan(t)
}

// ownerSave saves a draft as the Desk does.
func (e *planEnv) ownerSave(t *testing.T, draft string) {
	t.Helper()
	p := e.plan(t)
	p.Status.Draft, p.Status.DraftRevision, p.Status.DraftBy = draft, p.Status.DraftRevision+1, "owner"
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func lastReply(t *testing.T, p *aiv1.AgentPlan, id int64) aiv1.ChatReply {
	t.Helper()
	if p.Status.Chat == nil || len(p.Status.Chat.Replies) == 0 {
		t.Fatalf("no replies: %+v", p.Status)
	}
	rep := p.Status.Chat.Replies[len(p.Status.Chat.Replies)-1]
	if rep.MessageID != id {
		t.Fatalf("last reply is for %d, want %d", rep.MessageID, id)
	}
	return rep
}

func TestPlanTurnWritesReplyAndDraft(t *testing.T) {
	e := newPlanEnv(t, planAnswer("sess-plan-1", "Which page?", "## Goal\n\nShow the version.\n"))
	p := e.turn(t, 10, "remove the milestone strip and show the version")

	rep := lastReply(t, p, 10)
	if rep.Text != "Which page?" || rep.Error != "" {
		t.Fatalf("reply: %+v", rep)
	}
	s := p.Status
	if s.Draft != "## Goal\n\nShow the version." || s.DraftRevision != 1 || s.DraftBy != "agent" || s.DraftSHA != e.mainSHA {
		t.Fatalf("draft: %q rev %d by %q sha %q (main %s)", s.Draft, s.DraftRevision, s.DraftBy, s.DraftSHA, e.mainSHA)
	}
	if s.Phase != aiv1.AgentPlanReady || s.Chat.LastTurnTime == nil {
		t.Fatalf("phase %s, lastTurnTime %v", s.Phase, s.Chat.LastTurnTime)
	}
	inv := e.engine.calls[0]
	if inv.Resume != "" || !inv.ReadOnly || inv.JSONSchema != planTurnSchema || inv.Role != "plan" ||
		!strings.HasPrefix(inv.SystemPrompt, planSystemPrompt) || strings.Join(inv.AllowedTools, ",") != strings.Join(planTools, ",") {
		t.Fatalf("invocation: resume %q readOnly %v role %q tools %v", inv.Resume, inv.ReadOnly, inv.Role, inv.AllowedTools)
	}
	for _, want := range []string{"The plan's title: Show the version", "main at " + e.mainSHA, "remove the milestone strip", "# GitLab project group/app"} {
		if !strings.Contains(inv.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, inv.Prompt)
		}
	}
	if st := savedState(t, e.run.Runner); st.session("plan", aiv1.EngineClaude) != "sess-plan-1" {
		t.Fatalf("stored sessions %v", st.Sessions)
	}
}

func TestPlanTurnResumesStoredSession(t *testing.T) {
	draft := "## Goal\n\nShow the version."
	e := newPlanEnv(t, planAnswer("sess-plan-1", "Which page?", draft), planAnswer("sess-plan-1", "Noted.", draft))
	e.turn(t, 10, "show the version")
	p := e.turn(t, 11, "the footer")

	if rep := lastReply(t, p, 11); rep.Text != "Noted." || len(p.Status.Chat.Replies) != 2 {
		t.Fatalf("replies: %+v", p.Status.Chat.Replies)
	}
	inv := e.engine.calls[1]
	if inv.Resume != "sess-plan-1" || strings.Contains(inv.Prompt, "The plan's title") {
		t.Fatalf("second turn: resume %q, prompt:\n%s", inv.Resume, inv.Prompt)
	}
	if p.Status.DraftRevision != 1 {
		t.Fatalf("an unchanged draft is no new revision: %d", p.Status.DraftRevision)
	}
}

func TestPlanTurnRelaysOwnerEditOnce(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "Saw it.", ""), planAnswer("s", "Ok.", ""))
	e.ownerSave(t, "## Goal\n\nOwner's words.")
	e.turn(t, 10, "look at my edit")
	p := e.turn(t, 11, "and now?")

	const relay = "The owner edited the draft; it is now:\n\n## Goal\n\nOwner's words."
	if !strings.Contains(e.engine.calls[0].Prompt, relay) {
		t.Fatalf("first turn does not relay the edit:\n%s", e.engine.calls[0].Prompt)
	}
	if strings.Contains(e.engine.calls[1].Prompt, "The owner edited the draft") {
		t.Fatalf("second turn relays the edit again:\n%s", e.engine.calls[1].Prompt)
	}
	if p.Status.Draft != "## Goal\n\nOwner's words." || p.Status.DraftBy != "owner" || p.Status.DraftRevision != 1 {
		t.Fatalf("an empty agent draft keeps the owner's: %+v", p.Status)
	}
}

func TestPlanTurnOwnerSaveDuringTurnWins(t *testing.T) {
	e := newPlanEnv(t, nil, planAnswer("s", "Ok.", ""))
	e.engine.answers[0] = func(inv Invocation) (*Result, error) {
		e.ownerSave(t, "## Goal\n\nOwner's words.") // while the turn runs
		return planAnswer("s", "Here is a draft.", "## Goal\n\nAgent's words.")(inv)
	}
	p := e.turn(t, 10, "draft it")

	if p.Status.Draft != "## Goal\n\nOwner's words." || p.Status.DraftBy != "owner" || p.Status.DraftRevision != 1 {
		t.Fatalf("the owner's save was overwritten: %+v", p.Status)
	}
	if rep := lastReply(t, p, 10); rep.Text != "Here is a draft.\n\n"+planDraftKept {
		t.Fatalf("reply: %q", rep.Text)
	}
	// The next turn gets the owner's draft.
	e.turn(t, 11, "go on")
	if !strings.Contains(e.engine.calls[1].Prompt, "The owner edited the draft; it is now:\n\n## Goal\n\nOwner's words.") {
		t.Fatalf("next turn lacks the owner's draft:\n%s", e.engine.calls[1].Prompt)
	}
}

func TestPlanTurnInvalidAnswer(t *testing.T) {
	e := newPlanEnv(t, func(Invocation) (*Result, error) {
		return &Result{SessionID: "s", Text: "I think the draft should"}, nil
	})
	p := e.turn(t, 10, "draft it")

	rep := lastReply(t, p, 10)
	if rep.Error != "the agent's answer was not valid" || rep.Text != "I think the draft should" {
		t.Fatalf("reply: %+v", rep)
	}
	if p.Status.Draft != "" || p.Status.DraftRevision != 0 || p.Status.Phase != aiv1.AgentPlanReady {
		t.Fatalf("status: %+v", p.Status)
	}
}

func TestPlanTurnEngineErrors(t *testing.T) {
	e := newPlanEnv(t,
		func(Invocation) (*Result, error) { return nil, errors.New("claude exited with status 1") },
		func(Invocation) (*Result, error) {
			return &Result{IsError: true, Text: "Claude AI usage limit reached"}, errors.New("claude reported an error: Claude AI usage limit reached")
		})
	p := e.turn(t, 10, "draft it")
	if rep := lastReply(t, p, 10); !strings.Contains(rep.Error, "claude exited with status 1") || p.Status.Phase != aiv1.AgentPlanReady {
		t.Fatalf("reply %+v, phase %s", rep, p.Status.Phase)
	}
	p = e.turn(t, 11, "again")
	if rep := lastReply(t, p, 11); !strings.HasPrefix(rep.Error, "usage limit") {
		t.Fatalf("reply: %+v", rep)
	}
}

// A result without structured output arrives from the agent container with
// Structured "null": Decode reads the text, as for an engine without schemas.
func TestResultDecodeNullStructured(t *testing.T) {
	var turn PlanTurn
	if err := (&Result{Structured: json.RawMessage("null"), Text: `{"reply":"hi","draft":""}`}).Decode(&turn); err != nil || turn.Reply != "hi" {
		t.Fatalf("turn %+v, err %v", turn, err)
	}
	if err := (&Result{Structured: json.RawMessage("null"), Text: "no json"}).Decode(&turn); err == nil {
		t.Fatal("no error for a text without JSON")
	}
}
