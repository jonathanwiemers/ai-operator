package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const chatPlanName = "shop-plan-1"

func chatPlan(name string) *aiv1.AgentPlan {
	return &aiv1.AgentPlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: metav1.NewTime(t0)},
		Spec:       aiv1.AgentPlanSpec{ProjectRef: "shop", CreatedBy: "user-1"},
		Status:     aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle},
	}
}

// newPlanChatEnv is newChatEnv with the default plan budget of $5 and the
// plan wakes recorded.
func newPlanChatEnv(t *testing.T, objs ...client.Object) (*chatEnv, *[]types.NamespacedName) {
	t.Helper()
	e := newChatEnv(t, objs...)
	var woke []types.NamespacedName
	e.chat.Plan = PlanConfig{MaxBudgetUSD: "5"}
	e.chat.WakePlan = func(n types.NamespacedName) { woke = append(woke, n) }
	return e, &woke
}

func (e *chatEnv) insertPlanUser(plan, body, kind, key string) int64 {
	e.t.Helper()
	id, created, err := e.store.InsertUserMessage(e.ctx, store.ChatMessage{
		Namespace: ns, Project: "shop", Plan: plan, Author: "user-1", AuthorName: "Ada",
		Kind: kind, Body: body, ClientKey: key, ExpiresAt: e.now.Add(time.Hour),
	})
	if err != nil || !created {
		e.t.Fatalf("InsertUserMessage = %d, %v, %v", id, created, err)
	}
	return id
}

func (e *chatEnv) planMsgState(id int64, plan string) (string, string) {
	e.t.Helper()
	msgs, err := e.store.PlanMessages(e.ctx, ns, "shop", plan, 0, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return m.State, m.Reason
		}
	}
	e.t.Fatalf("plan message %d not found", id)
	return "", ""
}

func (e *chatEnv) plan(name string) *aiv1.AgentPlan {
	e.t.Helper()
	var plan aiv1.AgentPlan
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &plan); err != nil {
		e.t.Fatal(err)
	}
	return &plan
}

func TestChatDispatchPlanDelivered(t *testing.T) {
	e, woke := newPlanChatEnv(t, chatProject(true), chatPlan(chatPlanName), chatTask(chatTaskName))
	one := e.insertPlanUser(chatPlanName, "and the version", "message", "p2")
	two := e.insertPlanUser(chatPlanName, "remove the strip", "message", "p1")
	task := e.insertUser(chatTaskName, "hello", "message", "t1")
	e.chat.dispatchQueued(e.ctx)

	for _, id := range []int64{one, two} {
		if st, reason := e.planMsgState(id, chatPlanName); st != "delivered" || reason != "" {
			t.Fatalf("plan message %d: state = %q %q, want delivered", id, st, reason)
		}
	}
	msgs := e.plan(chatPlanName).Spec.ChatMessages
	if len(msgs) != 2 || msgs[0].ID != one || msgs[1].ID != two || msgs[0].Text != "and the version" || msgs[0].Author != "user-1" {
		t.Fatalf("plan chatMessages = %+v, want both in id order", msgs)
	}
	want := types.NamespacedName{Namespace: ns, Name: chatPlanName}
	if len(*woke) != 2 || (*woke)[0] != want || (*woke)[1] != want {
		t.Fatalf("plan wakes = %v, want %v once per message", *woke, want)
	}
	// Task messages go to the task as before, not to a plan.
	if st, _ := e.msgState(task, chatTaskName); st != "delivered" {
		t.Fatalf("task message state = %q, want delivered", st)
	}
	if tm := e.task(chatTaskName).Spec.Source.ChatMessages; len(tm) != 1 || tm[0].ID != task {
		t.Fatalf("task chatMessages = %+v", tm)
	}
}

func TestChatDispatchPlanDecisions(t *testing.T) {
	e, _ := newPlanChatEnv(t, chatProject(true), chatPlan(chatPlanName))
	one := int32(1)
	id, _, err := e.store.InsertUserMessage(e.ctx, store.ChatMessage{
		Namespace: ns, Project: "shop", Plan: chatPlanName, Author: "user-1", AuthorName: "Ada", ClientKey: "pd1",
		ExpiresAt: e.now.Add(time.Hour), Decisions: []store.DecisionAnswer{
			{ID: "D1", Version: 2, Keep: true}, {ID: "D2", Version: 1, Option: &one}, {ID: "D3", Version: 4, Other: "Use OIDC"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := e.insertPlanUser(chatPlanName, "and the version", "message", "pd2")
	e.chat.dispatchQueued(e.ctx)

	msgs := e.plan(chatPlanName).Spec.ChatMessages
	if len(msgs) != 2 || msgs[0].ID != id || msgs[1].ID != plain {
		t.Fatalf("plan chatMessages = %+v, want both", msgs)
	}
	want := []aiv1.PlanDecisionAnswer{{ID: "D1", Version: 2, Keep: true}, {ID: "D2", Version: 1, Option: &one}, {ID: "D3", Version: 4, Other: "Use OIDC"}}
	if got := msgs[0]; got.Text != "" || !reflect.DeepEqual(got.Decisions, want) {
		t.Errorf("answered message = %+v, want empty text and answers %+v", got, want)
	}
	if msgs[1].Decisions != nil {
		t.Errorf("message without answers carries %+v", msgs[1].Decisions)
	}
}

func TestPlanAnswers(t *testing.T) {
	if got := planAnswers(nil); got != nil {
		t.Errorf("planAnswers(nil) = %+v, want nil", got)
	}
	two := int32(2)
	got := planAnswers([]store.DecisionAnswer{{ID: "D1", Version: 3, Option: &two}, {ID: "D2", Version: 1, Other: "x"}})
	want := []aiv1.PlanDecisionAnswer{{ID: "D1", Version: 3, Option: &two}, {ID: "D2", Version: 1, Other: "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planAnswers = %+v, want %+v", got, want)
	}
}

func TestChatDispatchPlanIdempotent(t *testing.T) {
	e, woke := newPlanChatEnv(t, chatProject(true), chatPlan(chatPlanName))
	id := e.insertPlanUser(chatPlanName, "hello", "message", "pi1")
	e.chat.dispatchQueued(e.ctx)
	if err := e.store.SetMessageState(e.ctx, []int64{id}, "queued", ""); err != nil {
		t.Fatal(err)
	}
	e.chat.dispatchQueued(e.ctx)

	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("duplicated plan chatMessages = %+v", msgs)
	}
	if st, _ := e.planMsgState(id, chatPlanName); st != "delivered" {
		t.Fatalf("state = %q, want delivered", st)
	}
	if len(*woke) != 1 {
		t.Fatalf("plan wakes = %v, want one: a message already on the plan does not wake it", *woke)
	}
}

func TestChatDispatchPlanWaitsForSetProject(t *testing.T) {
	plan := chatPlan(chatPlanName)
	plan.Labels = map[string]string{aiv1.LabelProjectSet: "jo-wiemers"}
	plan.Status = aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanStarting, Message: planWaitMessage}
	e, woke := newPlanChatEnv(t, plan)
	id := e.insertPlanUser(chatPlanName, "plan a CSV export", "message", "w1")
	e.chat.dispatchQueued(e.ctx)
	if st, reason := e.planMsgState(id, chatPlanName); st != "queued" || reason != "" || len(*woke) != 0 {
		t.Fatalf("state = %q %q, wakes %v; want queued while the set creates the project", st, reason, *woke)
	}

	if err := e.c.Create(e.ctx, chatProject(true)); err != nil {
		t.Fatal(err)
	}
	e.chat.dispatchQueued(e.ctx)
	if st, _ := e.planMsgState(id, chatPlanName); st != "delivered" || len(*woke) != 1 {
		t.Fatalf("state = %q, wakes %v; want delivered once the project exists", st, *woke)
	}
	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("plan chatMessages = %+v", msgs)
	}
}

func TestChatDispatchFailedWaitingPlan(t *testing.T) {
	plan := chatPlan(chatPlanName)
	plan.Labels = map[string]string{aiv1.LabelProjectSet: "jo-wiemers"}
	plan.Status = aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanFailed, Message: "The project set did not create shop: archived"}
	e, _ := newPlanChatEnv(t, plan)
	id := e.insertPlanUser(chatPlanName, "plan a CSV export", "message", "f1")
	e.chat.dispatchQueued(e.ctx)
	if st, reason := e.planMsgState(id, chatPlanName); st != "failed" || reason != plan.Status.Message {
		t.Fatalf("state = %q %q, want failed with the plan's message", st, reason)
	}
}

func TestChatDispatchPlanRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chat   bool
		plan   func(*aiv1.AgentPlan) // nil: no plan
		kind   string
		reason string // "" means delivered
	}{
		{name: "missing plan", chat: true, kind: "message", reason: "plan not found"},
		{name: "other project", chat: true, kind: "message", reason: "plan belongs to another project",
			plan: func(p *aiv1.AgentPlan) { p.Spec.ProjectRef = "other" }},
		{name: "chat off", chat: false, kind: "message", reason: "live chat is not enabled",
			plan: func(*aiv1.AgentPlan) {}},
		{name: "created", chat: true, kind: "message", reason: "the milestone was already created from this plan",
			plan: func(p *aiv1.AgentPlan) { p.Status.Phase, p.Status.CreatedMilestoneIID = aiv1.AgentPlanCreated, 7 }},
		{name: "failed", chat: true, kind: "message", reason: "pod failed 3 times",
			plan: func(p *aiv1.AgentPlan) { p.Status.Phase, p.Status.Message = aiv1.AgentPlanFailed, "pod failed 3 times" }},
		{name: "command", chat: true, kind: "command", reason: "commands are not supported on a plan",
			plan: func(*aiv1.AgentPlan) {}},
		{name: "budget used", chat: true, kind: "message", reason: "the plan's budget is used; raise it on the Desk",
			plan: func(p *aiv1.AgentPlan) { p.Status.SpendUSD = "5.0000" }},
		{name: "raised budget below cap", chat: true, kind: "message",
			plan: func(p *aiv1.AgentPlan) { p.Spec.MaxBudgetUSD, p.Status.SpendUSD = "10", "5.0000" }},
		{name: "raised budget used", chat: true, kind: "message", reason: "the plan's budget is used; raise it on the Desk",
			plan: func(p *aiv1.AgentPlan) { p.Spec.MaxBudgetUSD, p.Status.SpendUSD = "10", "10.2500" }},
		{name: "interrupt at the cap", chat: true, kind: "interrupt",
			plan: func(p *aiv1.AgentPlan) { p.Status.SpendUSD = "5.0000" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{chatProject(tc.chat)}
			if tc.plan != nil {
				p := chatPlan(chatPlanName)
				tc.plan(p)
				objs = append(objs, p)
			}
			e, woke := newPlanChatEnv(t, objs...)
			body := "hello"
			if tc.kind == "command" {
				body = `{"cmd":"retry"}`
			}
			id := e.insertPlanUser(chatPlanName, body, tc.kind, "r-"+tc.name)
			e.chat.dispatchQueued(e.ctx)

			st, reason := e.planMsgState(id, chatPlanName)
			if tc.reason == "" {
				if st != "delivered" || len(e.plan(chatPlanName).Spec.ChatMessages) != 1 || len(*woke) != 1 {
					t.Fatalf("state = %q %q, wakes %v; want delivered onto the plan", st, reason, *woke)
				}
				return
			}
			if st != "failed" || reason != tc.reason {
				t.Fatalf("state = %q %q, want failed %q", st, reason, tc.reason)
			}
			if len(*woke) != 0 {
				t.Fatalf("plan wakes = %v, want none", *woke)
			}
			if tc.plan != nil {
				if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 0 {
					t.Fatalf("plan chatMessages = %+v, want none", msgs)
				}
			}
		})
	}
}

func TestChatSweepFailsMessagesOfRemovedPlans(t *testing.T) {
	e, _ := newPlanChatEnv(t, chatProject(true), chatPlan(chatPlanName), chatPlan("shop-plan-gone"))
	kept := e.insertPlanUser(chatPlanName, "hello", "message", "ps1")
	lost := e.insertPlanUser("shop-plan-gone", "hello", "message", "ps2")
	e.chat.dispatchQueued(e.ctx)
	if err := e.c.Delete(e.ctx, e.plan("shop-plan-gone")); err != nil {
		t.Fatal(err)
	}
	// Rows are stamped by the database clock; sweep only looks at old ones.
	e.now = time.Now().Add(2 * chatSweepAge)
	e.chat.sweep(e.ctx)

	if st, _ := e.planMsgState(kept, chatPlanName); st != "delivered" {
		t.Fatalf("message on its plan: state = %q, want delivered", st)
	}
	if st, reason := e.planMsgState(lost, "shop-plan-gone"); st != "failed" || reason != "the plan was removed before an answer" {
		t.Fatalf("message of a removed plan: state = %q %q", st, reason)
	}
}

// planReplyEnv has a plan with one dispatched message id and the reply rep
// in its status; the last call ran on claude.
func planReplyEnv(t *testing.T, rep aiv1.ChatReply) (*chatEnv, int64) {
	t.Helper()
	e, _ := newPlanChatEnv(t, chatProject(true), chatPlan(chatPlanName))
	id := e.insertPlanUser(chatPlanName, "remove the strip", "message", "r1")
	e.chat.dispatchQueued(e.ctx)
	plan := e.plan(chatPlanName)
	orig := plan.DeepCopy()
	rep.MessageID, rep.At = id, metav1.NewTime(e.now)
	plan.Status.Chat = &aiv1.ChatStatus{Replies: []aiv1.ChatReply{rep}}
	plan.Status.Calls = []aiv1.AgentCall{{Engine: aiv1.EngineCursor}, {Engine: aiv1.EngineClaude}}
	if err := e.c.Status().Patch(e.ctx, plan, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	return e, id
}

func (e *chatEnv) reconcilePlanReply(c client.Client) error {
	e.t.Helper()
	chat := *e.chat
	chat.Client = c
	_, err := chatPlanReply{&chat}.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: chatPlanName}})
	return err
}

// planReplies returns the rows answering id.
func (e *chatEnv) planReplies(id int64) []store.ChatMessage {
	e.t.Helper()
	msgs, err := e.store.PlanMessages(e.ctx, ns, "shop", chatPlanName, 0, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.ChatMessage
	for _, m := range msgs {
		if m.ReplyTo == id {
			out = append(out, m)
		}
	}
	return out
}

func TestChatPlanReplyCopied(t *testing.T) {
	e, id := planReplyEnv(t, aiv1.ChatReply{Text: "which page?"})
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	reps := e.planReplies(id)
	if len(reps) != 1 || reps[0].Role != "agent" || reps[0].Body != "which page?" || reps[0].Plan != chatPlanName ||
		reps[0].Task != "" || reps[0].Project != "shop" || reps[0].AuthorName != string(aiv1.EngineClaude) {
		t.Fatalf("replies = %+v, want one agent reply on the plan by claude", reps)
	}
	if st, _ := e.planMsgState(id, chatPlanName); st != "answered" {
		t.Fatalf("user message state = %q, want answered", st)
	}
	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 0 {
		t.Fatalf("plan chatMessages = %+v, want none", msgs)
	}
}

func TestChatPlanReplyError(t *testing.T) {
	e, id := planReplyEnv(t, aiv1.ChatReply{Text: "partial", Error: "interrupted"})
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if reps := e.planReplies(id); len(reps) != 1 || reps[0].Role != "system" || reps[0].Body != "error: interrupted\npartial" {
		t.Fatalf("replies = %+v, want one system error reply", reps)
	}
}

func TestChatPlanReplyIdempotent(t *testing.T) {
	e, id := planReplyEnv(t, aiv1.ChatReply{Text: "answer"})
	// A row from an earlier round whose spec patch was lost.
	if _, err := e.store.InsertReply(e.ctx, store.ChatMessage{
		Namespace: ns, Project: "shop", Plan: chatPlanName, Role: "agent",
		Body: "answer", ReplyTo: id, ExpiresAt: e.now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if reps := e.planReplies(id); len(reps) != 1 {
		t.Fatalf("replies = %d, want 1", len(reps))
	}
	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 0 {
		t.Fatalf("plan chatMessages = %+v, want none", msgs)
	}
}

// conflictClient fails every patch with a conflict, like a plan the
// dispatcher changed meanwhile.
type conflictClient struct{ client.Client }

func (c conflictClient) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return apierrors.NewConflict(schema.GroupResource{Group: "ai.wiemers.me", Resource: "agentplans"}, chatPlanName, nil)
}

func TestChatPlanReplyConflictKeepsSpec(t *testing.T) {
	e, id := planReplyEnv(t, aiv1.ChatReply{Text: "answer"})
	if err := e.reconcilePlanReply(conflictClient{e.c}); !apierrors.IsConflict(err) {
		t.Fatalf("err = %v, want conflict", err)
	}
	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("plan chatMessages = %+v, want the message kept", msgs)
	}
	// The next round drops it without a second row.
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if reps := e.planReplies(id); len(reps) != 1 {
		t.Fatalf("replies = %d, want 1", len(reps))
	}
	if msgs := e.plan(chatPlanName).Spec.ChatMessages; len(msgs) != 0 {
		t.Fatalf("plan chatMessages = %+v, want none", msgs)
	}
}

// setPlanDecisions replaces the plan's status.decisions by list.
func (e *chatEnv) setPlanDecisions(list ...aiv1.PlanDecision) {
	e.t.Helper()
	plan := e.plan(chatPlanName)
	orig := plan.DeepCopy()
	plan.Status.Decisions = list
	if err := e.c.Status().Patch(e.ctx, plan, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

// planHistory lists the plan's stored decision history as "ID vN: signer, …"
// lines.
func (e *chatEnv) planHistory() []string {
	e.t.Helper()
	h, err := e.store.PlanDecisionHistory(e.ctx, ns, "shop", chatPlanName)
	if err != nil {
		e.t.Fatal(err)
	}
	out := []string{}
	for _, v := range h {
		line := fmt.Sprintf("%s v%d %s:", v.ID, v.Version, v.DecidedBy)
		for _, o := range v.SignOffs {
			line += " " + o.By
		}
		out = append(out, line)
	}
	return out
}

// planDecision is version v of decision id, decided by the agent.
func planDecision(id string, v int64, at time.Time) aiv1.PlanDecision {
	return aiv1.PlanDecision{
		ID: id, Title: "Title of " + id, Question: fmt.Sprintf("Question of %s v%d", id, v),
		Options: []string{"A", "B"}, Recommended: 1, State: aiv1.PlanDecisionLocked,
		Version: v, DecidedBy: "agent", DecidedAt: metav1.NewTime(at),
	}
}

func signedOffBy(d aiv1.PlanDecision, owner string, at time.Time) aiv1.PlanDecision {
	t := metav1.NewTime(at)
	d.SignedOffBy, d.SignedOffByName, d.SignedOffAt = owner, "Name of "+owner, &t
	return d
}

func TestChatPlanReplyStoresDecisionHistory(t *testing.T) {
	e, id := planReplyEnv(t, aiv1.ChatReply{Text: "here are my questions"})
	// Rows expire relative to the clock; the store reads against its own.
	e.now = time.Now()
	turn1 := e.now.Add(-time.Hour)
	e.setPlanDecisions(planDecision("D1", 1, turn1), planDecision("D2", 1, turn1))
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if reps := e.planReplies(id); len(reps) != 1 {
		t.Fatalf("replies = %d, want 1", len(reps))
	}
	want := []string{"D1 v1 agent:", "D2 v1 agent:"}
	if got := e.planHistory(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after turn 1: history %q, want %q", got, want)
	}

	// The same status again stores nothing new.
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if got := e.planHistory(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a second reconcile: history %q, want %q", got, want)
	}

	// Turn 2: owner-x changed D2 and kept D1. No message is left to drop,
	// and the decisions are still stored.
	turn2 := turn1.Add(10 * time.Minute)
	d2 := planDecision("D2", 2, turn2)
	d2.DecidedBy = "owner-x"
	e.setPlanDecisions(signedOffBy(planDecision("D1", 1, turn1), "owner-x", turn2), signedOffBy(d2, "owner-x", turn2))
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	// Turn 3: owner-y keeps D1, which replaces owner-x in the status.
	turn3 := turn2.Add(10 * time.Minute)
	e.setPlanDecisions(signedOffBy(planDecision("D1", 1, turn1), "owner-y", turn3), signedOffBy(d2, "owner-x", turn2))
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	want = []string{"D1 v1 agent: owner-y owner-x", "D2 v2 owner-x: owner-x", "D2 v1 agent:"}
	if got := e.planHistory(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after turn 3: history %q, want %q", got, want)
	}
}

func TestChatPlanReplyWithoutDecisionsStoresNone(t *testing.T) {
	e, _ := planReplyEnv(t, aiv1.ChatReply{Text: "answer"})
	if err := e.reconcilePlanReply(e.c); err != nil {
		t.Fatal(err)
	}
	if got := e.planHistory(); len(got) != 0 {
		t.Fatalf("history = %q, want none", got)
	}
}

func TestPlanDecisionRows(t *testing.T) {
	at := metav1.NewTime(t0)
	d := signedOffBy(planDecision("D3", 2, t0), "owner-x", t0.Add(time.Minute))
	d.State, d.Why, d.DecidedByName = aiv1.PlanDecisionOpen, "because", "Agent"
	noTime := planDecision("D4", 1, t0)
	noTime.SignedOffBy = "owner-x" // without a time: not a sign-off to store
	got := planDecisionRows([]aiv1.PlanDecision{d, noTime})
	want := []store.PlanDecisionRow{
		{PlanDecision: store.PlanDecision{ID: "D3", Version: 2, Title: "Title of D3", Question: "Question of D3 v2",
			Options: []string{"A", "B"}, Recommended: 1, Why: "because", State: "open", DecidedBy: "agent",
			DecidedByName: "Agent", DecidedAt: at.Time},
			SignOff: &store.PlanDecisionSignOff{By: "owner-x", ByName: "Name of owner-x", At: t0.Add(time.Minute)}},
		{PlanDecision: store.PlanDecision{ID: "D4", Version: 1, Title: "Title of D4", Question: "Question of D4 v1",
			Options: []string{"A", "B"}, Recommended: 1, State: "locked", DecidedBy: "agent", DecidedAt: t0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planDecisionRows =\n%+v\nwant\n%+v", got, want)
	}
}
