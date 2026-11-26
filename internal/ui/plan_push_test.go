package ui

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestEventOfPlan(t *testing.T) {
	e, ok := eventOf(&aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-plan-1"},
		Spec: aiv1.AgentPlanSpec{ProjectRef: "orbit"}})
	if !ok || e != (Event{Kind: EventPlan, Namespace: "agents", Name: "orbit-plan-1", Project: "orbit"}) {
		t.Errorf("event = %+v, %v", e, ok)
	}
}

// A finished turn pushes once to its sender's devices that pick plan.
func TestPushSenderPlanReady(t *testing.T) {
	env, pushes := senderEnv(t)
	withChat(env.server.cfg)
	turn := metav1.NewTime(time.Now().Add(-time.Minute))
	plan := &aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-plan-1"},
		Spec: aiv1.AgentPlanSpec{ProjectRef: "orbit", Title: "Poker"},
		Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanReady, AnsweredFor: "alice",
			Chat: &aiv1.ChatStatus{LastTurnTime: &turn, Replies: []aiv1.ChatReply{{MessageID: 1, Text: "Here."}}}}}
	if err := env.reader.Create(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	alice, aliceQuiet, bob := newFakeDevice(t), newFakeDevice(t), newFakeDevice(t)
	alice.subscribe(t, pushes, "alice", []string{"orbit"}, "plan")
	quiet := aliceQuiet.subscribe(t, pushes, "alice", []string{"orbit"}, "question")
	bob.subscribe(t, pushes, "bob", []string{"orbit"}, "plan")

	if st := cycle(t, env); st.sent != 1 {
		t.Fatalf("first cycle %+v, want 1 sent", st)
	}
	if got := alice.keys(t); !slices.Equal(got, []string{"agents/orbit/plan/orbit-plan-1/1"}) {
		t.Fatalf("alice got %v", got)
	}
	if n := len(bob.pushes()) + len(aliceQuiet.pushes()) + len(pushes.ledger(quiet)); n != 0 {
		t.Fatalf("bob or alice's device without plan got %v %v", bob.keys(t), aliceQuiet.keys(t))
	}
	if st := cycle(t, env); st.sent != 0 {
		t.Fatalf("second cycle %+v sent again", st)
	}

	plan.Status.Chat.Replies = append(plan.Status.Chat.Replies, aiv1.ChatReply{MessageID: 2, Text: "Again."})
	if err := env.reader.Status().Update(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if st := cycle(t, env); st.sent != 1 || len(alice.pushes()) != 2 {
		t.Fatalf("next turn: %+v, alice got %v", st, alice.keys(t))
	}
}
