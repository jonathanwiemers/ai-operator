package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func planTitled(session, reply, title string) func(Invocation) (*Result, error) {
	return func(Invocation) (*Result, error) {
		b, _ := json.Marshal(PlanTurn{Reply: reply, Title: title})
		return &Result{SessionID: session, CostUSD: 0.1, Structured: b}, nil
	}
}

// untitled clears the plan's spec.title, as the Desk's Plan milestone does.
func (e *planEnv) untitled(t *testing.T) *planEnv {
	t.Helper()
	p := e.plan(t)
	p.Spec.Title = ""
	if err := e.k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return e
}

// The agent names an untitled plan once: the first title stays (R3).
func TestPlanTurnTitlesOnce(t *testing.T) {
	e := newPlanEnv(t, planTitled("s", "Which rail?", "Desk: version in the rail"),
		planTitled("s", "Ok.", "Something else")).untitled(t)
	if p := e.turn(t, 10, "add a version line to the rail"); p.Status.Title != "Desk: version in the rail" {
		t.Fatalf("first turn: title %q", p.Status.Title)
	}
	if p := e.turn(t, 11, "the left rail"); p.Status.Title != "Desk: version in the rail" {
		t.Fatalf("second turn: title %q", p.Status.Title)
	}
	const ask = "## Title\n\nThe plan has no title yet: give one in title."
	if !strings.Contains(e.engine.calls[0].Prompt, ask) || strings.Contains(e.engine.calls[1].Prompt, ask) {
		t.Fatalf("the title is asked for while the plan has none only:\n%s\n---\n%s", e.engine.calls[0].Prompt, e.engine.calls[1].Prompt)
	}
}

// A plan the owner titled keeps it: no ask, and the answer's title is ignored.
func TestPlanTurnKeepsSpecTitle(t *testing.T) {
	e := newPlanEnv(t, planTitled("s", "Which rail?", "Desk: version in the rail"))
	if p := e.turn(t, 10, "add a version line"); p.Status.Title != "" {
		t.Fatalf("title %q on a plan with spec.title", p.Status.Title)
	}
	if strings.Contains(e.engine.calls[0].Prompt, "## Title") {
		t.Fatalf("prompt asks for a title:\n%s", e.engine.calls[0].Prompt)
	}
}

// An untitled plan keeps asking after an empty title.
func TestPlanTurnEmptyTitleAsksAgain(t *testing.T) {
	e := newPlanEnv(t, planTitled("s", "What for?", " \n "), planTitled("s", "Ok.", "Version in the rail")).untitled(t)
	if p := e.turn(t, 10, "hm"); p.Status.Title != "" {
		t.Fatalf("blank title written: %q", p.Status.Title)
	}
	if p := e.turn(t, 11, "a version line"); p.Status.Title != "Version in the rail" {
		t.Fatalf("second turn: title %q", p.Status.Title)
	}
}

func TestPlanTurnMultiLineTitle(t *testing.T) {
	e := newPlanEnv(t, planTitled("s", "Ok.", "  Version in the rail  \nand more\n")).untitled(t)
	if p := e.turn(t, 10, "go"); p.Status.Title != "Version in the rail" {
		t.Fatalf("title %q", p.Status.Title)
	}
}

func TestPlanTitleFrom(t *testing.T) {
	long := strings.Repeat("é", 300)
	for in, want := range map[string]string{
		"":                      "",
		"  A title \n second":   "A title",
		"\n\n  After blanks\nx": "After blanks",
		long:                    strings.Repeat("é", maxPlanTitle),
		"Desk: version in rail": "Desk: version in rail",
	} {
		if got := planTitleFrom(in); got != want {
			t.Errorf("planTitleFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

// An interrupted turn writes no title, even when the engine still answered.
func TestPlanInterruptedTurnWritesNoTitle(t *testing.T) {
	e := newPlanEnv(t, nil).untitled(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.run.wake = e.run.watchObject(ctx, &aiv1.AgentPlanList{}, "shop-plan-1")
	e.engine.answers[0] = func(inv Invocation) (*Result, error) {
		e.engine.mu.Lock()
		turnCtx := e.engine.ctxs[len(e.engine.ctxs)-1]
		e.engine.mu.Unlock()
		e.post(t, chatMsg(11, "interrupt", "stop", "alice"))
		<-turnCtx.Done()
		return planTitled("s", "Here.", "Version in the rail")(inv)
	}
	e.post(t, chatMsg(10, "message", "draft it", "alice"))
	if _, err := e.run.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t)
	if rep, _ := replyFor(p, 10); rep.Error != "interrupted" || p.Status.Title != "" {
		t.Fatalf("reply %+v, title %q", rep, p.Status.Title)
	}
}

// planDecided answers a plan turn with a decisions list in JSON.
func planDecided(reply, draft, decisions string) func(Invocation) (*Result, error) {
	return func(Invocation) (*Result, error) {
		b, _ := json.Marshal(PlanTurn{Reply: reply, Draft: draft})
		b = []byte(strings.Replace(string(b), `"decisions":null`, `"decisions":`+decisions, 1))
		return &Result{SessionID: "s", CostUSD: 0.1, Structured: b}, nil
	}
}

// decisionJSON is one decision of the agent's answer.
func decisionJSON(id, title, state string, recommended int, options ...string) string {
	b, _ := json.Marshal(planTurnDecision{ID: id, Title: title, Question: title + "?", Options: options,
		Recommended: recommended, Why: "because", State: state})
	return string(b)
}

func decisionsJSON(ds ...string) string { return "[" + strings.Join(ds, ",") + "]" }

func TestPlanTurnStoresDecisions(t *testing.T) {
	const draft = "## Goal\n\nShow the version.\n\n## Decisions (locked)\n\n1. In the rail."
	d1 := decisionJSON("D1", "Placement", "locked", 0, "Rail", "Footer")
	e := newPlanEnv(t,
		planDecided("Which format?", draft, decisionsJSON(d1, decisionJSON("D2", "Format", "open", 1, "Tag", "Tag and SHA"))),
		planDecided("Noted.", "", decisionsJSON(strings.Replace(d1, "because", "seen everywhere", 1),
			decisionJSON("D2", "Format", "locked", 1, "Tag", "Tag and SHA"))),
		planDecided("Ok.", "", "[]"),
		planDecided("Dropped D1.", "", decisionsJSON(decisionJSON("D2", "Format", "locked", 1, "Tag", "Tag and SHA"))),
		planAnswer("s", "Go on.", ""))
	p := e.turn(t, 10, "show the version")
	ds := p.Status.Decisions
	if len(ds) != 2 || p.Status.DecisionSeq != 2 || ds[0].ID != "D1" || ds[0].Version != 1 || ds[0].DecidedBy != "agent" ||
		ds[0].DecidedAt.IsZero() || ds[1].State != aiv1.PlanDecisionOpen || ds[1].Recommended != 1 || ds[1].Options[1] != "Tag and SHA" {
		t.Fatalf("first turn: seq %d %+v", p.Status.DecisionSeq, ds)
	}
	if rep := lastReply(t, p, 10); rep.Text != "Which format?" {
		t.Fatalf("reply: %q", rep.Text)
	}
	if strings.Contains(e.engine.calls[0].Prompt, "Decision IDs") {
		t.Fatalf("a plan without decisions names IDs:\n%s", e.engine.calls[0].Prompt)
	}

	// A changed why alone keeps the version; the answered question is locked: a new version.
	p = e.turn(t, 11, "tag and SHA")
	ds = p.Status.Decisions
	if len(ds) != 2 || ds[0].Version != 1 || ds[0].Why != "seen everywhere" || ds[1].Version != 2 || ds[1].State != aiv1.PlanDecisionLocked {
		t.Fatalf("second turn: %+v", ds)
	}
	if want := "## Decision IDs\n\nThe current decisions are D1, D2. A new decision gets D3; never reuse a dropped ID.\n\n"; !strings.Contains(e.engine.calls[1].Prompt, want) {
		t.Fatalf("second prompt lacks %q:\n%s", want, e.engine.calls[1].Prompt)
	}

	// An empty list keeps the current one.
	if p = e.turn(t, 12, "fine"); len(p.Status.Decisions) != 2 || p.Status.Decisions[1].Version != 2 {
		t.Fatalf("an empty list changed the decisions: %+v", p.Status.Decisions)
	}
	// A left-out decision is dropped; its ID stays issued.
	if p = e.turn(t, 13, "drop the placement"); len(p.Status.Decisions) != 1 || p.Status.Decisions[0].ID != "D2" || p.Status.DecisionSeq != 2 {
		t.Fatalf("drop: seq %d %+v", p.Status.DecisionSeq, p.Status.Decisions)
	}
	e.turn(t, 14, "go on")
	if want := "The current decisions are D2. A new decision gets D3;"; !strings.Contains(e.engine.calls[4].Prompt, want) {
		t.Fatalf("prompt after a drop lacks %q:\n%s", want, e.engine.calls[4].Prompt)
	}
}

func TestPlanTurnInvalidDecisionsKeepList(t *testing.T) {
	d1 := decisionJSON("D1", "Placement", "locked", 0, "Rail", "Footer")
	d2 := decisionJSON("D2", "Format", "open", 0, "Tag", "SHA")
	many := make([]string, 31)
	for i := range many {
		many[i] = decisionJSON(fmt.Sprintf("D%d", i+1), "Placement", "locked", 0, "Rail", "Footer")
	}
	invalid := map[string]string{
		"31 entries":           decisionsJSON(many...),
		"one option":           decisionsJSON(decisionJSON("D1", "Placement", "locked", 0, "Rail")),
		"dropped ID reused":    decisionsJSON(d1, decisionJSON("D2", "Other format", "open", 0, "Tag", "SHA")),
		"recommended too high": decisionsJSON(decisionJSON("D1", "Placement", "locked", 2, "Rail", "Footer")),
		"duplicate ID":         decisionsJSON(d1, d1),
	}
	answers := []func(Invocation) (*Result, error){
		planDecided("Two.", "", decisionsJSON(d1, d2)),
		planDecided("Dropped D2.", "", decisionsJSON(d1)),
	}
	var names []string
	for name, ds := range invalid {
		names = append(names, name)
		answers = append(answers, planDecided("Here.", "", ds))
	}
	e := newPlanEnv(t, answers...)
	e.turn(t, 10, "go")
	want := e.turn(t, 11, "drop the format").Status
	for i, name := range names {
		id := int64(20 + i)
		p := e.turn(t, id, name)
		if len(p.Status.Decisions) != 1 || p.Status.Decisions[0].ID != "D1" || p.Status.Decisions[0].Version != want.Decisions[0].Version ||
			p.Status.DecisionSeq != want.DecisionSeq {
			t.Errorf("%s: the list changed: seq %d %+v", name, p.Status.DecisionSeq, p.Status.Decisions)
		}
		if rep := lastReply(t, p, id); rep.Text != "Here.\n\n"+planDecisionsInvalid || rep.Error != "" {
			t.Errorf("%s: reply %+v", name, rep)
		}
	}
}

// A list whose draft is refused is not applied either (D612-P2).
func TestPlanTurnDecisionsFollowTheDraft(t *testing.T) {
	d1 := decisionsJSON(decisionJSON("D1", "Placement", "locked", 0, "Rail", "Footer"))
	e := newPlanEnv(t, nil, planDecided("Big.", strings.Repeat("x", maxPlanDraft+1), d1))
	e.engine.answers[0] = func(inv Invocation) (*Result, error) {
		e.ownerSave(t, "## Goal\n\nOwner's words.") // while the turn runs
		return planDecided("Here is a draft.", "## Goal\n\nAgent's words.", d1)(inv)
	}
	p := e.turn(t, 10, "draft it")
	if len(p.Status.Decisions) != 0 || p.Status.DecisionSeq != 0 || lastReply(t, p, 10).Text != "Here is a draft.\n\n"+planDraftKept {
		t.Fatalf("draft kept: reply %q, decisions %+v", lastReply(t, p, 10).Text, p.Status.Decisions)
	}
	p = e.turn(t, 11, "more")
	if len(p.Status.Decisions) != 0 || lastReply(t, p, 11).Text != "Big.\n\n"+planDraftLarge {
		t.Fatalf("draft too large: reply %q, decisions %+v", lastReply(t, p, 11).Text, p.Status.Decisions)
	}
}

// An interrupted turn stores no decisions, even when the engine still answered.
func TestPlanInterruptedTurnStoresNoDecisions(t *testing.T) {
	e := newPlanEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.run.wake = e.run.watchObject(ctx, &aiv1.AgentPlanList{}, "shop-plan-1")
	e.engine.answers[0] = func(inv Invocation) (*Result, error) {
		e.engine.mu.Lock()
		turnCtx := e.engine.ctxs[len(e.engine.ctxs)-1]
		e.engine.mu.Unlock()
		e.post(t, chatMsg(11, "interrupt", "stop", "alice"))
		<-turnCtx.Done()
		return planDecided("Here.", "", decisionsJSON(decisionJSON("D1", "Placement", "open", 0, "Rail", "Footer")))(inv)
	}
	e.post(t, chatMsg(10, "message", "draft it", "alice"))
	if _, err := e.run.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t)
	if rep, _ := replyFor(p, 10); rep.Error != "interrupted" || len(p.Status.Decisions) != 0 || p.Status.DecisionSeq != 0 {
		t.Fatalf("reply %+v, decisions %+v", rep, p.Status.Decisions)
	}
}
