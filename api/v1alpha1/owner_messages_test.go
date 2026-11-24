package v1alpha1

import (
	"fmt"
	"strings"
	"testing"
)

func TestAppendOwnerMessage(t *testing.T) {
	var l []OwnerMessage
	l = AppendOwnerMessage(l, OwnerMessage{ID: "tell/1", Text: "a"})
	l = AppendOwnerMessage(l, OwnerMessage{ID: "tell/1", Text: "b"})
	if len(l) != 1 || l[0].Text != "a" {
		t.Fatalf("dedupe: %v", l)
	}
	l = AppendOwnerMessage(nil, OwnerMessage{ID: "x", Text: strings.Repeat("é", MaxOwnerMessageText+10)})
	if n := len([]rune(l[0].Text)); n != MaxOwnerMessageText {
		t.Errorf("truncated to %d", n)
	}
	l = nil
	for i := range 25 {
		l = AppendOwnerMessage(l, OwnerMessage{ID: fmt.Sprint(i)})
	}
	if len(l) != MaxOwnerMessages || l[0].ID != "5" || l[19].ID != "24" {
		t.Errorf("cap: %d first %s last %s", len(l), l[0].ID, l[len(l)-1].ID)
	}
}

func TestPendingOwnerMessages(t *testing.T) {
	task := &AgentTask{}
	task.Spec.Source.OwnerMessages = []OwnerMessage{{ID: "a"}, {ID: "b"}}
	if got := task.PendingOwnerMessages(); len(got) != 2 || task.Delivery("a") != nil {
		t.Fatalf("no run: %v", got)
	}
	task.Status.Run = RunStatus{DeliveredMessages: []DeliveredMessage{{ID: "a", Call: 3, How: DeliveryStream}}}
	got := task.PendingOwnerMessages()
	if len(got) != 1 || got[0].ID != "b" {
		t.Errorf("pending: %v", got)
	}
	if d := task.Delivery("a"); d == nil || d.Call != 3 || task.Delivery("b") != nil {
		t.Errorf("delivery: %v", d)
	}
}

func TestOwnerMessageSchema(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	msg := func(text string) map[string]any {
		return map[string]any{"id": "tell/1", "owner": "u", "text": text, "at": "2026-11-03T12:00:00Z"}
	}
	errsOn := func(obj map[string]any, field string) string {
		_, errs := crd.admit(obj)
		var out []string
		for _, e := range errs {
			for _, line := range strings.Split(e, "\n") {
				if strings.Contains(line, field) {
					out = append(out, line)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	src := func(m ...any) map[string]any {
		return map[string]any{"spec": map[string]any{"source": map[string]any{"ownerMessages": m}}}
	}
	if e := errsOn(src(msg(strings.Repeat("x", 4000))), "ownerMessages"); e != "" {
		t.Errorf("4000 chars rejected: %s", e)
	}
	if e := errsOn(src(msg(strings.Repeat("x", 4001))), "ownerMessages"); !strings.Contains(e, "text") {
		t.Errorf("4001 chars: %q", e)
	}
	if e := errsOn(src(repeat(msg("a"), 21)...), "ownerMessages"); e == "" {
		t.Error("21 messages admitted")
	}
	if e := errsOn(src(repeat(msg("a"), 20)...), "ownerMessages"); e != "" {
		t.Errorf("20 messages rejected: %s", e)
	}
	del := func(how string) map[string]any {
		d := map[string]any{"id": "tell/1", "call": int64(2), "how": how, "at": "2026-11-03T12:00:00Z"}
		return map[string]any{"status": map[string]any{"run": map[string]any{"deliveredMessages": []any{d}}}}
	}
	for _, how := range []string{"stream", "resume", "nextRun"} {
		if e := errsOn(del(how), "deliveredMessages"); e != "" {
			t.Errorf("how %s rejected: %s", how, e)
		}
	}
	if e := errsOn(del("telepathy"), "deliveredMessages"); !strings.Contains(e, "how") {
		t.Errorf("how telepathy: %q", e)
	}
}
