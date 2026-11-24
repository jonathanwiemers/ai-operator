package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const messageTask = "shop-42-add-paypal-button"

// messageEnv is a runner of an Implement task in the middle of a run that
// started at start, its agent running engine.
func messageEnv(t *testing.T, engine Engine, start time.Time) (*Runner, client.Client) {
	t.Helper()
	old := messagesPoll
	messagesPoll = 50 * time.Millisecond
	t.Cleanup(func() { messagesPoll = old })
	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: messageTask, Namespace: "agents"},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: 1, Issue: &aiv1.IssueRef{IID: 42}},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).WithStatusSubresource(&aiv1.AgentTask{}).Build()
	r := newTestRunner(t, t.TempDir(), k8s, gitlabfake.New(), engine, messageTask)
	r.run = &runLog{log: logr.Discard(), start: start}
	return r, k8s
}

func ownerMessage(id string, at time.Time) aiv1.OwnerMessage {
	return aiv1.OwnerMessage{ID: id, Owner: "alice", Text: "Words " + id + ".", At: metav1.Time{Time: at}}
}

// addOwnerMessages appends msgs to the task, as the controller does.
func addOwnerMessages(t *testing.T, k8s client.Client, msgs ...aiv1.OwnerMessage) {
	t.Helper()
	var task aiv1.AgentTask
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: messageTask}, &task); err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		task.Spec.Source.OwnerMessages = aiv1.AppendOwnerMessage(task.Spec.Source.OwnerMessages, m)
	}
	if err := k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
}

func messageTaskNow(t *testing.T, k8s client.Client) *aiv1.AgentTask {
	t.Helper()
	var task aiv1.AgentTask
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: messageTask}, &task); err != nil {
		t.Fatal(err)
	}
	return &task
}

// streamCall runs a claude implementer call through invoke; the channel
// gets its result.
func streamCall(ctx context.Context, r *Runner) <-chan *Result {
	out := make(chan *Result, 1)
	go func() {
		res, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineClaude},
			Invocation{Role: "implement", Kind: aiv1.CallKindImplement, JSONSchema: `{"type":"object"}`}, "implement #42")
		if err != nil {
			res = &Result{Text: "error: " + err.Error()}
		}
		out <- res
	}()
	return out
}

// streamedMessages is how many later user messages the fake claude took in.
func streamedMessages(t *testing.T, res *Result) int {
	t.Helper()
	var out struct{ Messages int }
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		t.Fatalf("no structured result: %+v", res)
	}
	return out.Messages
}

// waitDelivered waits until id is recorded as delivered.
func waitDelivered(ctx context.Context, t *testing.T, k8s client.Client, id string) {
	t.Helper()
	for {
		task := messageTaskNow(t, k8s)
		if task.Delivery(id) != nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("%s was not delivered: %+v", id, task.Status.Run.DeliveredMessages)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessagesStreamedAsOneBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := filepath.Join(t.TempDir(), "args")
	r, k8s := messageEnv(t, fakeClaudeStream(t, "FAKE_CLAUDE_WORK=3", "FAKE_CLAUDE_ARGS="+args), time.Now())
	done := streamCall(ctx, r)
	waitStarted(ctx, t, args)
	// Two messages pending at the same time go in as one user message.
	addOwnerMessages(t, k8s, ownerMessage("tell/1", time.Now()), ownerMessage("tell/2", time.Now()))
	res := <-done
	if res.SessionID == "" || streamedMessages(t, res) != 1 || res.CostUSD != 0.25 {
		t.Fatalf("the call takes one message in and ends with its one result: %+v", res)
	}
	run := messageTaskNow(t, k8s).Status.Run
	if len(run.Calls) != 1 || len(run.DeliveredMessages) != 2 {
		t.Fatalf("one call, two deliveries: %+v", run)
	}
	for _, d := range run.DeliveredMessages {
		if d.How != aiv1.DeliveryStream || d.Call != run.Calls[0].Seq || d.At.IsZero() {
			t.Errorf("a streamed message is recorded as stream with the call's Seq %d: %+v", run.Calls[0].Seq, d)
		}
	}
	if run.Interruptions != 0 || run.Calls[0].StoppedForMessage {
		t.Errorf("a delivery is no interruption: %+v", run)
	}
}

func TestMessagesMidRunCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := filepath.Join(t.TempDir(), "args")
	r, k8s := messageEnv(t, fakeClaudeStream(t, "FAKE_CLAUDE_WORK=8", "FAKE_CLAUDE_ARGS="+args), time.Now())
	done := streamCall(ctx, r)
	waitStarted(ctx, t, args) // after the prompt path: all stream
	for i := 1; i <= aiv1.MaxMidRunDeliveries; i++ {
		id := fmt.Sprintf("tell/%d", i)
		addOwnerMessages(t, k8s, ownerMessage(id, time.Now()))
		waitDelivered(ctx, t, k8s, id)
	}
	addOwnerMessages(t, k8s, ownerMessage("tell/6", time.Now()))
	res := <-done
	if n := streamedMessages(t, res); n != aiv1.MaxMidRunDeliveries {
		t.Fatalf("at most %d messages go in mid-run, the call took %d", aiv1.MaxMidRunDeliveries, n)
	}
	task := messageTaskNow(t, k8s)
	if pending := task.PendingOwnerMessages(); len(pending) != 1 || pending[0].ID != "tell/6" {
		t.Fatalf("the sixth waits for the next run: %+v", pending)
	}
	// Neither the next implementer call of this run takes it...
	if s := r.promptOwnerMessages(ctx, Invocation{Kind: aiv1.CallKindFix}); s != "" {
		t.Fatalf("the cap counts prompt deliveries too: %q", s)
	}
	// ...nor a later one: the next run's first implementer call does.
	r.run = &runLog{log: logr.Discard(), start: time.Now().Add(time.Second)}
	if s := r.promptOwnerMessages(ctx, Invocation{Kind: aiv1.CallKindImplement}); !strings.Contains(s, "Words tell/6.") {
		t.Fatalf("the next run's prompt has the sixth: %q", s)
	}
	if d := messageTaskNow(t, k8s).Delivery("tell/6"); d == nil || d.How != aiv1.DeliveryNextRun {
		t.Fatalf("recorded as nextRun: %+v", d)
	}
}

func TestMessagesDeliveredIntoTheNextImplementerCall(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	engine := &scriptedEngine{}
	r, k8s := messageEnv(t, engine, start)
	// One pending since before the run, one that came during it (e.g.
	// during a review or a CI wait).
	addOwnerMessages(t, k8s, ownerMessage("tell/old", start.Add(-time.Hour)), ownerMessage("chat/new", start.Add(time.Minute)))

	review := Invocation{Role: "bugbot", Kind: aiv1.CallKindReview, ReadOnly: true, Prompt: "review"}
	if _, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineClaude}, review, "review"); err != nil {
		t.Fatal(err)
	}
	if p := engine.calls[0].Prompt; p != "review" {
		t.Fatalf("a reviewer gets no owner messages: %q", p)
	}
	if run := messageTaskNow(t, k8s).Status.Run; len(run.DeliveredMessages) != 0 {
		t.Fatalf("nothing is delivered to a reviewer: %+v", run.DeliveredMessages)
	}

	fix := Invocation{Role: "implement", Kind: aiv1.CallKindFix, Resume: "sess-impl", Prompt: "Fix the findings."}
	if _, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineCursor}, fix, "the full prompt"); err != nil {
		t.Fatal(err)
	}
	want := "## Message from @alice via the pilot\n\nWords tell/old.\n\n## Message from @alice via the pilot\n\nWords chat/new.\n\n" +
		"Take this into account and go on with the task; answer as before.\n\nFix the findings."
	if p := engine.calls[1].Prompt; p != want {
		t.Fatalf("the fix call's prompt:\n%s\nwant:\n%s", p, want)
	}
	run := messageTaskNow(t, k8s).Status.Run
	fixSeq := run.Calls[len(run.Calls)-1].Seq
	old, nw := messageTaskNow(t, k8s).Delivery("tell/old"), messageTaskNow(t, k8s).Delivery("chat/new")
	if old == nil || old.How != aiv1.DeliveryNextRun || old.Call != fixSeq || nw == nil || nw.How != aiv1.DeliveryResume || nw.Call != fixSeq {
		t.Fatalf("recorded before the call with its Seq %d: %+v", fixSeq, run.DeliveredMessages)
	}
	if run.Interruptions != 0 {
		t.Fatalf("interruptions = %d", run.Interruptions)
	}

	// A new pod (a fresh runner state) reads the record: nothing again.
	r.run = &runLog{log: logr.Discard(), start: time.Now()}
	if _, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineCursor}, fix, "the full prompt"); err != nil {
		t.Fatal(err)
	}
	if p := engine.calls[2].Prompt; p != "Fix the findings." {
		t.Fatalf("a delivered message is not delivered again: %q", p)
	}
	if run := messageTaskNow(t, k8s).Status.Run; len(run.DeliveredMessages) != 2 {
		t.Fatalf("deliveries: %+v", run.DeliveredMessages)
	}
}

func TestMessagesInAFreshPrompt(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	engine := &scriptedEngine{}
	r, k8s := messageEnv(t, engine, start)
	addOwnerMessages(t, k8s, ownerMessage("tell/1", start.Add(-time.Hour)))
	inv := Invocation{Role: "implement", Kind: aiv1.CallKindImplement}
	if _, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineCursor}, inv, "Implement #42."); err != nil {
		t.Fatal(err)
	}
	if p := engine.calls[0].Prompt; !strings.HasPrefix(p, "Implement #42.\n\n## Message from @alice via the pilot\n\nWords tell/1.") {
		t.Fatalf("a fresh call gets the messages after its prompt: %q", p)
	}
	// Deliveries of messages the spec no longer holds are dropped.
	task := messageTaskNow(t, k8s)
	task.Spec.Source.OwnerMessages = []aiv1.OwnerMessage{ownerMessage("tell/2", start.Add(-time.Hour))}
	if err := k8s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineCursor}, inv, "Implement #42."); err != nil {
		t.Fatal(err)
	}
	if d := messageTaskNow(t, k8s).Status.Run.DeliveredMessages; len(d) != 1 || d[0].ID != "tell/2" || d[0].How != aiv1.DeliveryNextRun {
		t.Fatalf("deliveries: %+v", d)
	}
}

// waitStarted waits until the fake claude wrote its arguments: the call
// started, after its prompt took the messages pending then.
func waitStarted(ctx context.Context, t *testing.T, args string) {
	t.Helper()
	for {
		if _, err := os.Stat(args); err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("the call did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageWithoutReplayStaysPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	old := messageWait
	messageWait = 300 * time.Millisecond
	t.Cleanup(func() { messageWait = old })
	args := filepath.Join(t.TempDir(), "args")
	r, k8s := messageEnv(t, fakeClaudeStream(t, "FAKE_CLAUDE_WORK=2", "FAKE_CLAUDE_NO_REPLAY=1", "FAKE_CLAUDE_ARGS="+args), time.Now())
	done := streamCall(ctx, r)
	waitStarted(ctx, t, args)
	addOwnerMessages(t, k8s, ownerMessage("tell/1", time.Now()))
	res := <-done
	if n := streamedMessages(t, res); n != 1 {
		t.Fatalf("the message is written into the call once, not again: %d", n)
	}
	task := messageTaskNow(t, k8s)
	if len(task.Status.Run.DeliveredMessages) != 0 || len(task.PendingOwnerMessages()) != 1 || r.run.midRunLeft() != aiv1.MaxMidRunDeliveries {
		t.Fatalf("without a replay it stays pending for the next implementer call: %+v", task.Status.Run)
	}
}
