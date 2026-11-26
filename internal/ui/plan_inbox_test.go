package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// readyPlan is shop's plan name in team-a, whose last turn answered alice's
// message 7 with reply.
func readyPlan(name string, reply aiv1.ChatReply) aiv1.AgentPlan {
	turn := metav1.NewTime(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))
	return aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: name},
		Spec: aiv1.AgentPlanSpec{ProjectRef: "shop", Title: "Spec title"},
		Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanReady, Title: "Refunds", AnsweredFor: "alice",
			Chat: &aiv1.ChatStatus{LastTurnTime: &turn, Replies: []aiv1.ChatReply{reply}}}}
}

func TestPlanInboxOf(t *testing.T) {
	projects := map[string]*aiv1.AgentProject{"team-a/shop": {ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop"}}}
	answer := aiv1.ChatReply{MessageID: 7, Text: "\n  Here is the draft.  \nMore lines."}
	long := strings.Repeat("é", 200)
	cases := map[string]struct {
		user   string
		change func(*aiv1.AgentPlan)
		want   string // "key|title|reason", "" for no item
	}{
		"sender":          {"alice", nil, "team-a/shop/plan/p/7|Refunds|Here is the draft."},
		"another user":    {"bob", nil, ""},
		"no user":         {"", func(p *aiv1.AgentPlan) { p.Status.AnsweredFor = "" }, ""},
		"not viewable":    {"alice", func(p *aiv1.AgentPlan) { p.Spec.ProjectRef = "billing" }, ""},
		"working":         {"alice", func(p *aiv1.AgentPlan) { p.Status.Phase = aiv1.AgentPlanWorking }, ""},
		"created":         {"alice", func(p *aiv1.AgentPlan) { p.Status.Phase = aiv1.AgentPlanCreated }, ""},
		"failed":          {"alice", func(p *aiv1.AgentPlan) { p.Status.Phase = aiv1.AgentPlanFailed }, ""},
		"idle":            {"alice", func(p *aiv1.AgentPlan) { p.Status.Phase = aiv1.AgentPlanIdle }, "team-a/shop/plan/p/7|Refunds|Here is the draft."},
		"next message":    {"alice", func(p *aiv1.AgentPlan) { p.Spec.ChatMessages = []aiv1.ChatMessage{{ID: 8, Kind: "message"}} }, ""},
		"no answeredFor":  {"alice", func(p *aiv1.AgentPlan) { p.Status.AnsweredFor = "" }, ""},
		"no turn reply":   {"alice", func(p *aiv1.AgentPlan) { p.Status.Chat.Replies = []aiv1.ChatReply{{MessageID: 9}} }, ""},
		"no chat":         {"alice", func(p *aiv1.AgentPlan) { p.Status.Chat = nil }, ""},
		"next turn":       {"alice", func(p *aiv1.AgentPlan) { p.Status.Chat.Replies[0].MessageID = 12 }, "team-a/shop/plan/p/12|Refunds|Here is the draft."},
		"spec title":      {"alice", func(p *aiv1.AgentPlan) { p.Status.Title = "" }, "team-a/shop/plan/p/7|Spec title|Here is the draft."},
		"untitled":        {"alice", func(p *aiv1.AgentPlan) { p.Status.Title, p.Spec.Title = "", "" }, "team-a/shop/plan/p/7|Untitled plan|Here is the draft."},
		"long first line": {"alice", func(p *aiv1.AgentPlan) { p.Status.Chat.Replies[0].Text = long + "\nnext" }, "team-a/shop/plan/p/7|Refunds|" + truncateRunes(long, 140)},
		"error body":      {"alice", func(p *aiv1.AgentPlan) { p.Status.Chat.Replies[0].Error = "usage limit until 12:00" }, "team-a/shop/plan/p/7|Refunds|The turn did not finish: usage limit until 12:00"},
		"interrupt skipped": {"alice", func(p *aiv1.AgentPlan) {
			p.Status.Chat.Replies = []aiv1.ChatReply{{MessageID: 7, Error: "interrupted"}, {MessageID: 8}}
		}, "team-a/shop/plan/p/7|Refunds|The turn did not finish: interrupted"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := readyPlan("p", answer)
			if c.change != nil {
				c.change(&p)
			}
			items := planInboxOf(projects, []aiv1.AgentPlan{p}, c.user)
			if c.want == "" {
				if len(items) != 0 {
					t.Fatalf("items: %+v", items)
				}
				return
			}
			if len(items) != 1 {
				t.Fatalf("items: %+v", items)
			}
			it := items[0]
			if got := it.Key + "|" + it.Title + "|" + it.Reason; got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if it.Kind != "plan" || it.Notify != "plan" || it.Plan != "p" || it.Project != "shop" || it.Namespace != "team-a" ||
				it.At == nil || !it.At.Equal(p.Status.Chat.LastTurnTime.Time) {
				t.Fatalf("item: %+v", it)
			}
		})
	}
}

func TestPlanInboxOfNewestFirst(t *testing.T) {
	projects := map[string]*aiv1.AgentProject{"team-a/shop": {}}
	older, newer := readyPlan("a", aiv1.ChatReply{MessageID: 1, Text: "a"}), readyPlan("b", aiv1.ChatReply{MessageID: 2, Text: "b"})
	later := metav1.NewTime(newer.Status.Chat.LastTurnTime.Add(time.Minute))
	newer.Status.Chat.LastTurnTime = &later
	items := planInboxOf(projects, []aiv1.AgentPlan{older, newer}, "alice")
	if len(items) != 2 || items[0].Plan != "b" || items[1].Plan != "a" {
		t.Fatalf("items: %+v", items)
	}
}

// The Inbox lists a ready plan for the sender of the answered message only.
func TestInboxListsReadyPlanForSender(t *testing.T) {
	for _, chat := range []bool{true, false} {
		env := planEnv(t, chat, "devs")
		for name, user := range map[string]string{"ada-plan": "ada@example.com", "bea-plan": "bea"} {
			p := readyPlan(name, aiv1.ChatReply{MessageID: 3, Text: "Done."})
			p.Status.AnsweredFor = user
			if err := env.reader.Create(context.Background(), &p); err != nil {
				t.Fatal(err)
			}
		}
		var inbox InboxResponse
		if code := env.get(t, "/api/v1/inbox", &inbox); code != 200 {
			t.Fatalf("inbox: %d", code)
		}
		var keys []string
		for _, it := range inbox.Items {
			if it.Kind == "plan" {
				keys = append(keys, it.Key)
			}
		}
		if want := "team-a/shop/plan/ada-plan/3"; chat && (len(keys) != 1 || keys[0] != want) || !chat && len(keys) != 0 {
			t.Fatalf("chat %v: plan items %v", chat, keys)
		}
	}
}
