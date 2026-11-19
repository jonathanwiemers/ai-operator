package controller

import (
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// heartbeats returns the test deliveries asked for so far.
func (e *projectEnv) heartbeats() []string {
	var out []string
	for _, w := range e.gl.HookWrites {
		if strings.HasPrefix(w, "test ") {
			out = append(out, w)
		}
	}
	return out
}

func TestHeartbeatAfterHalfTheSilenceThresholdAndNotTwiceWithin(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	start := clk.Now()
	var at []time.Duration
	for range 60 {
		clk.Step(time.Minute)
		n := len(e.heartbeats())
		e.poll(gl)
		if len(e.heartbeats()) > n {
			at = append(at, clk.Now().Sub(start))
		}
	}
	// Silence threshold 30m: the first after 15m plus a jitter under 3m45s.
	if len(at) < 2 || at[0] < 15*time.Minute || at[0] >= 19*time.Minute {
		t.Fatalf("heartbeats at %v", at)
	}
	for i := 1; i < len(at); i++ {
		if d := at[i] - at[i-1]; d < 15*time.Minute {
			t.Fatalf("heartbeats %s apart: %v", d, at)
		}
	}
	p := e.project()
	if st := p.Status.Webhook; st.HeartbeatTrigger != "push_events" || st.HeartbeatTime == nil {
		t.Fatalf("webhook status %+v", st)
	}
	// Without a delivery the hook does not work: still Silent.
	e.wantDeliveries(metav1.ConditionFalse, "Silent", time.Time{})
}

func TestHeartbeatFallsBackToTheNextTriggerOnA4xx(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	id := e.project().Status.Webhook.HookID
	e.gl.HookTestErrors = map[string]error{"push_events": &gitlab.Error{StatusCode: http.StatusBadRequest}}
	clk.Step(20 * time.Minute)
	e.poll(gl)
	want := []string{"test " + itoa(id) + " push_events", "test " + itoa(id) + " issues_events"}
	if got := e.heartbeats(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("heartbeats %v, want %v", got, want)
	}
	if got := e.project().Status.Webhook.HeartbeatTrigger; got != "issues_events" {
		t.Fatalf("trigger %q", got)
	}
	// The stored trigger goes first next time.
	clk.Step(20 * time.Minute)
	e.poll(gl)
	if got := e.heartbeats(); len(got) != 3 || got[2] != "test "+itoa(id)+" issues_events" {
		t.Fatalf("heartbeats %v", got)
	}

	// A rate limit or a server error stops; the time is kept all the same.
	e.gl.HookTestErrors = map[string]error{"issues_events": &gitlab.Error{StatusCode: http.StatusTooManyRequests}}
	clk.Step(20 * time.Minute)
	e.poll(gl)
	if got := e.heartbeats(); len(got) != 4 {
		t.Fatalf("heartbeats %v", got)
	}
	if st := e.project().Status.Webhook; st.HeartbeatTime == nil || !st.HeartbeatTime.Time.Equal(clk.Now()) {
		t.Fatalf("webhook status %+v", st)
	}
}

func TestHeartbeatDeliveryKeepsTheProjectReceivingWithLightPolls(t *testing.T) {
	e, clk, gl := newPollingEnv(t)
	e.r.Deliveries.Record(shop)
	if !e.poll(gl) {
		t.Fatal("a delivery syncs fully")
	}
	// The receiver: the test delivery GitLab sends right after the call.
	e.gl.OnHookTest = func() {
		if !e.r.Deliveries.RecordHook(shop) {
			t.Error("the announced test delivery is not a heartbeat")
		}
	}
	var heartbeat time.Time
	for range 40 {
		clk.Step(time.Minute)
		n := len(e.heartbeats())
		full := e.poll(gl)
		if len(e.heartbeats()) > n {
			heartbeat = clk.Now()
		}
		// Full syncs come only every fallbackInterval (10m).
		if full && clk.Now().Minute()%10 != 0 {
			t.Fatalf("full sync at %s after a heartbeat", clk.Now().Format(time.TimeOnly))
		}
	}
	if heartbeat.IsZero() {
		t.Fatal("no heartbeat")
	}
	// 40m after the last event, the heartbeats keep it receiving.
	e.wantDeliveries(metav1.ConditionTrue, "Receiving", heartbeat)
}

func TestHeartbeatNotForSetCoveredOrHooklessProjects(t *testing.T) {
	c := newChildEnv(t)
	for range 40 {
		c.clk.Step(time.Minute)
		c.discovered(time.Minute)
		c.reconcile()
	}
	if got := c.heartbeats(); len(got) != 0 {
		t.Fatalf("a set-covered project sent heartbeats: %v", got)
	}

	e, clk, gl := newPollingEnv(t)
	p := e.project()
	p.Status.Webhook.HookID = 0
	if err := e.c.Status().Update(e.ctx, &p); err != nil {
		t.Fatal(err)
	}
	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled = false })
	for range 40 {
		clk.Step(time.Minute)
		e.poll(gl)
	}
	if got := e.heartbeats(); len(got) != 0 {
		t.Fatalf("a project without a hook sent heartbeats: %v", got)
	}
}
