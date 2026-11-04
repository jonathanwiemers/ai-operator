package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// mainPrompt is a main health prompt with the source key: retry #1
// (recommended) or do nothing.
func mainPrompt(key string) aiv1.Prompt {
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptMainHealth, Key: key}, Question: "main is red: what now?",
		Options: []aiv1.PromptOption{
			{ID: "A", Label: "Retry #1", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Recommended: true},
			{ID: "B", Label: "Do nothing"},
		}}
}

// agentQuestion is an agent's question in free text.
func agentQuestion(iid int64) aiv1.Prompt {
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptNeedsInput, IssueIID: iid}, Question: "Which API?", AllowFreeText: true}
}

func (e *projectEnv) task(name string) *aiv1.AgentTask {
	e.t.Helper()
	var t aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &t); err != nil {
		e.t.Fatal(err)
	}
	return &t
}

func (e *projectEnv) addPrompt(st, holder *aiv1.AgentTask, p aiv1.Prompt) aiv1.Prompt {
	e.t.Helper()
	got, err := e.r.addPrompt(e.ctx, st, holder, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// promptStates are the ids and states of a task's prompts, in order.
func promptStates(t *aiv1.AgentTask) string {
	var out []string
	for _, p := range t.Status.Prompts {
		out = append(out, p.ID+"="+string(p.State))
	}
	return strings.Join(out, " ")
}

func TestPromptIDsCountPerMilestone(t *testing.T) {
	e := newManagedEnv(t)
	impl := e.taskOf(1)
	if got := e.addPrompt(e.task(specTaskName), e.task(specTaskName), mainPrompt("main")); got.ID != "Q1" || got.State != aiv1.PromptOpen || got.PostedAt == nil {
		t.Fatalf("the first prompt is Q1, open and posted: %+v", got)
	}
	// The agent's question lives on its task, its id is the milestone's.
	if got := e.addPrompt(e.task(specTaskName), e.task(impl), agentQuestion(1)); got.ID != "Q2" {
		t.Fatalf("the agent's question counts on: %s", got.ID)
	}
	st := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("other"))
	if st.Status.LastPrompt != 3 || promptStates(st) != "Q1=open Q3=open" {
		t.Fatalf("the spec task counts and holds the milestone's prompts: %d, %s", st.Status.LastPrompt, promptStates(st))
	}
	if it := e.task(impl); it.Status.LastPrompt != 0 || promptStates(it) != "Q2=open" {
		t.Fatalf("the implement task holds its question, without a counter: %d, %s", it.Status.LastPrompt, promptStates(it))
	}

	// An issue without a milestone counts on its own task.
	e.gl.AddIssue(9, "Elsewhere", "agent::needs-input")
	e.reconcile()
	nine := e.task(e.taskOf(9))
	e.addPrompt(nil, nine, agentQuestion(9))
	e.addPrompt(nil, nine, agentQuestion(9))
	if nine.Status.LastPrompt != 2 || promptStates(nine) != "Q1=open Q2=open" || e.task(specTaskName).Status.LastPrompt != 3 {
		t.Fatalf("without a spec task the holder counts: %d, %s", nine.Status.LastPrompt, promptStates(nine))
	}

	// A prompt Validate refuses takes no id.
	bad := mainPrompt("main")
	bad.Question = ""
	if _, err := e.r.addPrompt(e.ctx, st, st, bad); err == nil || !strings.Contains(err.Error(), "no question") {
		t.Fatalf("a prompt without a question is refused: %v", err)
	}
	if got := e.addPrompt(st, st, mainPrompt("x")); got.ID != "Q4" {
		t.Fatalf("a refused prompt leaves no gap: %s", got.ID)
	}
}

func TestPromptSupersedesTheOpenPromptOfItsSource(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("main"))
	e.addPrompt(st, st, mainPrompt("other"))
	e.addPrompt(st, st, mainPrompt(""))
	e.addPrompt(st, st, mainPrompt(""))
	revisit := aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptRevisit, Key: "main"}, Question: "Revisit?",
		Options: []aiv1.PromptOption{{ID: "A", Label: "Keep", Recommended: true}, {ID: "B", Label: "Change"}}}
	e.addPrompt(st, st, revisit)
	e.addPrompt(st, st, mainPrompt("main"))
	if got := promptStates(e.task(specTaskName)); got != "Q1=superseded Q2=open Q3=open Q4=open Q5=open Q6=open" {
		t.Fatalf("only the open prompt of the same kind and key goes: %s", got)
	}
	if q := e.task(specTaskName).Status.Prompts[0]; q.ClosedAt == nil {
		t.Fatalf("a superseded prompt is closed: %+v", q)
	}
}

func TestPromptListIsBounded(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	list := func(states ...aiv1.PromptState) []aiv1.Prompt {
		var out []aiv1.Prompt
		for i, s := range states {
			out = append(out, aiv1.Prompt{ID: fmt.Sprintf("Q%d", i+1), State: s})
		}
		return out
	}
	open := slices.Repeat([]aiv1.PromptState{aiv1.PromptOpen}, aiv1.MaxPrompts+1)

	kept, gone := boundPrompts(list(open...), now)
	if len(kept) != aiv1.MaxPrompts || kept[0].ID != "Q2" || kept[len(kept)-1].ID != "Q21" ||
		len(gone) != 1 || gone[0].ID != "Q1" || gone[0].State != aiv1.PromptSuperseded || gone[0].ClosedAt == nil {
		t.Fatalf("all open: the oldest is superseded and goes: %d kept from %s, gone %+v", len(kept), kept[0].ID, gone)
	}

	mixed := slices.Clone(open)
	mixed[3], mixed[7] = aiv1.PromptAnswered, aiv1.PromptExpired
	mixed = append(mixed, aiv1.PromptOpen)
	kept, gone = boundPrompts(list(mixed...), now)
	if len(kept) != aiv1.MaxPrompts || len(gone) != 0 || slices.ContainsFunc(kept, func(p aiv1.Prompt) bool { return p.State.Closed() }) {
		t.Fatalf("mixed: the closed go first: %s, gone %+v", promptStates(&aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Prompts: kept}}), gone)
	}

	short := list(aiv1.PromptOpen, aiv1.PromptAnswered)
	if kept, gone = boundPrompts(short, now); len(kept) != 2 || len(gone) != 0 {
		t.Fatalf("a short list stays: %+v, %+v", kept, gone)
	}

	// Through addPrompt: twenty open ones and an answered one in the list.
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	for i := range aiv1.MaxPrompts {
		e.addPrompt(st, st, mainPrompt(fmt.Sprint(i)))
	}
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Prompts[5].State = aiv1.PromptAnswered })
	st = e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("new"))
	if got := st.Status.Prompts; len(got) != aiv1.MaxPrompts || got[0].ID != "Q1" || slices.ContainsFunc(got, func(p aiv1.Prompt) bool { return p.ID == "Q6" }) {
		t.Fatalf("the answered prompt goes first: %s", promptStates(st))
	}
	e.addPrompt(st, st, mainPrompt("newer"))
	if got := e.task(specTaskName).Status.Prompts; len(got) != aiv1.MaxPrompts || got[0].ID != "Q2" || got[len(got)-1].ID != "Q22" {
		t.Fatalf("then the oldest open one: %s", promptStates(st))
	}
}

func TestPromptsAddedConcurrentlyGetTheirOwnIDs(t *testing.T) {
	e := newManagedEnv(t)
	impl := e.taskOf(1)
	// Copies read before either write: the second write conflicts.
	st1, st2, st3 := e.task(specTaskName), e.task(specTaskName), e.task(specTaskName)
	im1, im2 := e.task(impl), e.task(impl)
	a := e.addPrompt(st1, st1, mainPrompt("a"))
	b := e.addPrompt(st2, st2, mainPrompt("b"))
	c := e.addPrompt(st3, im1, agentQuestion(1))
	d := e.addPrompt(st3, im2, agentQuestion(1))
	if ids := []string{a.ID, b.ID, c.ID, d.ID}; !slices.Equal(ids, []string{"Q1", "Q2", "Q3", "Q4"}) {
		t.Fatalf("each prompt gets its own id: %v", ids)
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=open" {
		t.Fatalf("neither milestone prompt is lost: %s", got)
	}
	if got := promptStates(e.task(impl)); got != "Q3=open Q4=open" {
		t.Fatalf("neither question is lost: %s", got)
	}
	if st1.Status.LastPrompt != 1 || st2.Status.LastPrompt != 2 || im2.Status.LastPrompt != 0 || len(im2.Status.Prompts) != 2 {
		t.Fatal("the copies are the server's afterwards")
	}
}

func TestPatchPromptsRetriesOnAConflict(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("a"))
	stale := e.task(specTaskName)
	e.addPrompt(st, st, mainPrompt("b"))
	if err := e.r.patchPrompts(e.ctx, stale, func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			list[i].State = aiv1.PromptExpired
		}
		return list
	}); err != nil {
		t.Fatal(err)
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=expired Q2=expired" {
		t.Fatalf("the change applies to the fresh copy: %s", got)
	}
}

// validOptions checks p's options on the pilot env's milestone.
func (e *projectEnv) validOptions(p *aiv1.Prompt, opened []string, fromModel bool) ([]string, error) {
	e.t.Helper()
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		e.t.Fatal(err)
	}
	st := e.task(specTaskName)
	tasks, err := e.r.issueTasks(e.ctx, &project, st.Spec.Milestone.IID)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.r.validOptions(e.ctx, e.gl, &project, project.Spec, st, tasks, opened, p, fromModel)
}

func TestPromptOptionsAreValidated(t *testing.T) {
	e := newManagedEnv(t)
	option := func(label string, rec bool, actions ...aiv1.PilotAction) aiv1.PromptOption {
		return aiv1.PromptOption{Label: label, Recommended: rec, Actions: actions}
	}
	labels := func(p aiv1.Prompt) string {
		var out []string
		for _, o := range p.Options {
			s := o.ID + ":" + o.Label
			if o.Recommended {
				s += "*"
			}
			out = append(out, s)
		}
		return strings.Join(out, " ")
	}

	p := aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment}, Question: "#1 is stuck: what now?", Options: []aiv1.PromptOption{
		option("Merge it", true, aiv1.PilotAction{Tool: "merge", IssueIID: 1}),
		option("Retry", false, aiv1.PilotAction{Tool: "retry", IssueIID: 1}),
		option("Retry #7", false, aiv1.PilotAction{Tool: "retry", IssueIID: 1}, aiv1.PilotAction{Tool: "retry", IssueIID: 7}),
		option("Do nothing", false),
	}}
	notes, err := e.validOptions(&p, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(p); got != "A:Retry B:Do nothing*" {
		t.Fatalf("an option with an action the operator refuses goes, do nothing is recommended in place of it: %s", got)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "**retry** #1") || notes[1] != "" {
		t.Fatalf("each option left has its action lines: %q", notes)
	}

	// Nothing to run is refused, naming why.
	p = aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment}, Question: "merge?", Options: []aiv1.PromptOption{
		option("Merge it", true, aiv1.PilotAction{Tool: "merge", IssueIID: 1}), option("Do nothing", false),
	}}
	if _, err := e.validOptions(&p, nil, true); err == nil || !strings.Contains(err.Error(), "`merge` is not a tool of the pilot") || p.Options[0].ID != "" {
		t.Fatalf("a prompt without an action option left is refused, unchanged: %v, %+v", err, p.Options)
	}

	// A prompt without actions needs none.
	p = aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptRevisit}, Question: "revisit D1-1?", Options: []aiv1.PromptOption{
		option("Keep it", true), option("Change it", false),
	}}
	if _, err := e.validOptions(&p, nil, true); err != nil || labels(p) != "A:Keep it* B:Change it" {
		t.Fatalf("an option without actions is no refusal: %v, %s", err, labels(p))
	}

	// Its shape is checked too.
	p.Options = p.Options[:1]
	if _, err := e.validOptions(&p, nil, true); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("Validate runs last: %v", err)
	}

	// Options are alternatives: each may open the same issue, not one opened before.
	p = aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment}, Question: "open?", Options: []aiv1.PromptOption{
		option("Open", true, aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"}),
		option("Open and retry", false, aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"}, aiv1.PilotAction{Tool: "retry", IssueIID: 1}),
		option("Open twice", false, aiv1.PilotAction{Tool: "openIssue", Title: "Pin the runner"}, aiv1.PilotAction{Tool: "openIssue", Title: "pin the  runner"}),
		option("Open the old", false, aiv1.PilotAction{Tool: "openIssue", Title: "Old"}),
		option("Do nothing", false),
	}}
	if _, err := e.validOptions(&p, []string{titleKey("old")}, true); err != nil || labels(p) != "A:Open* B:Open and retry C:Do nothing" {
		t.Fatalf("an option opens an issue once, and none opened before: %v, %s", err, labels(p))
	}
}

func TestPromptOptionsFromTheModelPassNoOwnersWords(t *testing.T) {
	e := newManagedEnv(t)
	prompt := func() aiv1.Prompt {
		return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment}, Question: "#1 is stuck: what now?", Options: []aiv1.PromptOption{
			{Label: "Rerun", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "instruct", IssueIID: 1, Kind: "rerun", Owner: "alice", Text: "delete the tests"}}},
			{Label: "Tell", Actions: []aiv1.PilotAction{{Tool: "instruct", IssueIID: 1, Owner: "alice", Text: "use the v2 API"}}},
			{Label: "Do nothing"},
		}}
	}
	p := prompt()
	notes, err := e.validOptions(&p, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if a := p.Options[0].Actions[0]; len(p.Options) != 2 || a.Owner != "" || a.Text != "" || a.Kind != "rerun" || strings.Contains(strings.Join(notes, "\n"), "delete the tests") {
		t.Fatalf("the model's actions lose the owner's words, an instruction of nothing but them goes: %+v, %q", p.Options, notes)
	}
	// The operator's own (from an owner's words) keep them.
	p = prompt()
	if _, err := e.validOptions(&p, nil, false); err != nil || len(p.Options) != 3 || p.Options[1].Actions[0].Text != "use the v2 API" || p.Options[1].Actions[0].Owner != "alice" {
		t.Fatalf("the operator's options keep the owner's words: %v, %+v", err, p.Options)
	}
}
