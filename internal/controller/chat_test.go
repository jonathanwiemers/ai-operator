package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

type chatDB struct {
	ready chan struct{}
	store *store.Store
}

func (d chatDB) Ready() <-chan struct{} { return d.ready }
func (d chatDB) Store() *store.Store    { return d.store }

type chatEnv struct {
	t     *testing.T
	c     client.Client
	chat  *Chat
	store *store.Store
	ctx   context.Context
	now   time.Time
}

const chatTaskName = "shop-42-chat"

func newChatEnv(t *testing.T, objs ...client.Object) *chatEnv {
	t.Helper()
	s := storetest.New(t)
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).
		Build()
	ready := make(chan struct{})
	close(ready)
	e := &chatEnv{t: t, c: c, store: s, ctx: context.Background(), now: t0}
	e.chat = &Chat{
		Client:    c,
		DB:        chatDB{ready: ready, store: s},
		Retention: history.DefaultRetention,
		Log:       zap.New(zap.UseDevMode(false)),
		Now:       func() time.Time { return e.now },
	}
	return e
}

func chatProject(enabled bool) *aiv1.AgentProject {
	p := testProject()
	p.Spec.Agents.Chat = enabled
	return p
}

func chatTask(name string) *aiv1.AgentTask {
	return &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, UID: types.UID("uid-" + name),
			CreationTimestamp: metav1.NewTime(t0),
			Labels:            map[string]string{LabelProject: "shop", LabelType: "implement"},
		},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement,
			Issue: &aiv1.IssueRef{IID: 42, Title: "Add PayPal"}},
	}
}

func (e *chatEnv) insertUser(task, body, kind, key string) int64 {
	e.t.Helper()
	if kind == "" {
		kind = "message"
	}
	id, created, err := e.store.InsertUserMessage(e.ctx, store.ChatMessage{
		Namespace: ns, Project: "shop", Task: task, Author: "user-1", AuthorName: "Ada",
		Kind: kind, Body: body, ClientKey: key, ExpiresAt: e.now.Add(time.Hour),
	})
	if err != nil || !created {
		e.t.Fatalf("InsertUserMessage = %d, %v, %v", id, created, err)
	}
	return id
}

func (e *chatEnv) msgState(id int64, task string) (string, string) {
	e.t.Helper()
	msgs, err := e.store.ChatMessages(e.ctx, ns, "shop", task, 0, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return m.State, m.Reason
		}
	}
	queued, err := e.store.QueuedMessages(e.ctx, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range queued {
		if m.ID == id {
			return m.State, m.Reason
		}
	}
	e.t.Fatalf("message %d not found", id)
	return "", ""
}

func (e *chatEnv) task(name string) *aiv1.AgentTask {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &task); err != nil {
		e.t.Fatal(err)
	}
	return &task
}

func TestChatDispatchDelivered(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "hello", "message", "d1")
	e.chat.dispatchQueued(e.ctx)

	st, reason := e.msgState(id, chatTaskName)
	if st != "delivered" || reason != "" {
		t.Fatalf("state = %q %q, want delivered", st, reason)
	}
	msgs := e.task(chatTaskName).Spec.Source.ChatMessages
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Text != "hello" || msgs[0].Author != "user-1" {
		t.Fatalf("chatMessages = %+v", msgs)
	}
}

func TestChatDispatchIdempotent(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "hello", "message", "i1")
	e.chat.dispatchQueued(e.ctx)
	if err := e.store.SetMessageState(e.ctx, []int64{id}, "queued", ""); err != nil {
		t.Fatal(err)
	}
	e.chat.dispatchQueued(e.ctx)
	msgs := e.task(chatTaskName).Spec.Source.ChatMessages
	if len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("duplicated chatMessages = %+v", msgs)
	}
	st, _ := e.msgState(id, chatTaskName)
	if st != "delivered" {
		t.Fatalf("state = %q", st)
	}
}

func TestChatDispatchChatDisabled(t *testing.T) {
	e := newChatEnv(t, chatProject(false), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "hello", "message", "off1")
	e.chat.dispatchQueued(e.ctx)
	st, reason := e.msgState(id, chatTaskName)
	if st != "failed" || reason != "live chat is not enabled" {
		t.Fatalf("state = %q %q", st, reason)
	}
	if n := len(e.task(chatTaskName).Spec.Source.ChatMessages); n != 0 {
		t.Fatalf("chatMessages = %d", n)
	}
}

func TestChatDispatchMissingTask(t *testing.T) {
	e := newChatEnv(t, chatProject(true))
	id := e.insertUser("missing-task", "hello", "message", "m1")
	e.chat.dispatchQueued(e.ctx)
	st, reason := e.msgState(id, "missing-task")
	if st != "failed" || reason != "task not found" {
		t.Fatalf("state = %q %q", st, reason)
	}
}

func TestChatDispatchCommand(t *testing.T) {
	var woke []types.NamespacedName
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	e.chat.Wake = func(n types.NamespacedName) { woke = append(woke, n) }

	id := e.insertUser(chatTaskName, `{"cmd":"retry"}`, "command", "c-retry")
	e.chat.dispatchQueued(e.ctx)
	st, reason := e.msgState(id, chatTaskName)
	if st != "delivered" || reason != "" {
		t.Fatalf("retry state = %q %q", st, reason)
	}
	msgs := e.task(chatTaskName).Spec.Source.ChatMessages
	if len(msgs) != 1 || msgs[0].Kind != "command" || msgs[0].Text != `{"cmd":"retry"}` {
		t.Fatalf("chatMessages = %+v", msgs)
	}
	if len(woke) != 1 || woke[0] != (types.NamespacedName{Namespace: ns, Name: "shop"}) {
		t.Fatalf("Wake = %v", woke)
	}

	bad := e.insertUser(chatTaskName, `{"cmd":"nope"}`, "command", "c-bad")
	e.chat.dispatchQueued(e.ctx)
	st, reason = e.msgState(bad, chatTaskName)
	if st != "failed" || !strings.Contains(reason, "unknown cmd") {
		t.Fatalf("invalid body = %q %q", st, reason)
	}

	pause := e.insertUser(chatTaskName, `{"cmd":"pause"}`, "command", "c-pause")
	e.chat.dispatchQueued(e.ctx)
	st, reason = e.msgState(pause, chatTaskName)
	if st != "failed" || reason != "not supported on this task yet" {
		t.Fatalf("pause on implement = %q %q", st, reason)
	}
}

func TestChatDispatchCommandOnSpecTask(t *testing.T) {
	task := chatTask("shop-spec-m1")
	task.Spec.Type = aiv1.TaskTypeSpec
	task.Spec.Issue = nil
	e := newChatEnv(t, chatProject(true), task)
	id := e.insertUser(task.Name, `{"cmd":"retry"}`, "command", "c-spec")
	e.chat.dispatchQueued(e.ctx)
	st, reason := e.msgState(id, task.Name)
	if st != "failed" || reason != "not supported on this task yet" {
		t.Fatalf("state = %q %q", st, reason)
	}

	pause := e.insertUser(task.Name, `{"cmd":"pause"}`, "command", "c-pause-no-ap")
	e.chat.dispatchQueued(e.ctx)
	st, reason = e.msgState(pause, task.Name)
	if st != "failed" || reason != "not supported on this task yet" {
		t.Fatalf("pause without autopilot = %q %q", st, reason)
	}

	task = e.task(task.Name)
	task.Spec.Autopilot = true
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	ok := e.insertUser(task.Name, `{"cmd":"pause"}`, "command", "c-pause-ap")
	e.chat.dispatchQueued(e.ctx)
	st, reason = e.msgState(ok, task.Name)
	if st != "delivered" || reason != "" {
		t.Fatalf("pause on autopilot Spec = %q %q", st, reason)
	}
}

func TestChatDispatchCommandOnCoordinateTask(t *testing.T) {
	task := chatTask("shop-pilot-m1")
	task.Spec.Type = aiv1.TaskTypeCoordinate
	task.Spec.Issue = &aiv1.IssueRef{IID: 9}
	e := newChatEnv(t, chatProject(true), task)

	retry := e.insertUser(task.Name, `{"cmd":"retry"}`, "command", "c-coord-retry")
	e.chat.dispatchQueued(e.ctx)
	st, reason := e.msgState(retry, task.Name)
	if st != "failed" || reason != "not supported on this task yet" {
		t.Fatalf("retry on Coordinate = %q %q", st, reason)
	}

	accept := e.insertUser(task.Name, `{"cmd":"accept","proposal":3}`, "command", "c-coord-accept")
	e.chat.dispatchQueued(e.ctx)
	st, reason = e.msgState(accept, task.Name)
	if st != "delivered" || reason != "" {
		t.Fatalf("accept on Coordinate = %q %q", st, reason)
	}
}

func TestChatReplyCopiesAnswer(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, `{"cmd":"retry"}`, "command", "ans1")
	e.chat.dispatchQueued(e.ctx)
	task := e.task(chatTaskName)
	task.Spec.Source.ChatAnswers = []aiv1.ChatReply{{
		MessageID: id, Text: "🤖 A new run is requested.", At: metav1.NewTime(e.now),
	}}
	task.Spec.Source.LastChatCommandID = id
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := e.chat.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: chatTaskName}}); err != nil {
		t.Fatal(err)
	}
	got := e.task(chatTaskName)
	if len(got.Spec.Source.ChatMessages) != 0 || len(got.Spec.Source.ChatAnswers) != 0 {
		t.Fatalf("message and answer should be dropped: msgs=%+v answers=%+v", got.Spec.Source.ChatMessages, got.Spec.Source.ChatAnswers)
	}
	msgs, err := e.store.ChatMessages(e.ctx, ns, "shop", chatTaskName, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reply *store.ChatMessage
	for i := range msgs {
		if msgs[i].ReplyTo == id {
			reply = &msgs[i]
			break
		}
	}
	if reply == nil || reply.Role != "system" || reply.Body != "🤖 A new run is requested." {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestCommandOnlyChatDoesNotNeedPod(t *testing.T) {
	task := chatTask(chatTaskName)
	task.Spec.Source.ChatMessages = []aiv1.ChatMessage{{
		ID: 1, Kind: "command", Text: `{"cmd":"retry"}`, Author: "u", At: metav1.NewTime(t0),
	}}
	e := newTaskEnv(t, task)
	e.reconcile(chatTaskName)
	var pod corev1.Pod
	if e.exists(&pod, chatTaskName) {
		t.Fatal("a command-only chatMessages list must not start a pod")
	}
	if e.task(chatTaskName).Status.Phase != aiv1.TaskPhasePending {
		t.Fatalf("phase = %s", e.task(chatTaskName).Status.Phase)
	}
}

func TestChatDispatchCap(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	task := e.task(chatTaskName)
	orig := task.DeepCopy()
	// High IDs so the new queued row (id 1 in a fresh schema) is not treated
	// as a re-dispatch of an entry already in the spec.
	for i := int64(1); i <= maxChatQueued; i++ {
		task.Spec.Source.ChatMessages = append(task.Spec.Source.ChatMessages, aiv1.ChatMessage{
			ID: 1000 + i, Kind: "message", Text: "x", Author: "u", At: metav1.NewTime(e.now),
		})
	}
	if err := e.c.Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	id := e.insertUser(chatTaskName, "overflow", "message", "cap1")
	e.chat.dispatchQueued(e.ctx)
	st, _ := e.msgState(id, chatTaskName)
	if st != "queued" {
		t.Fatalf("state = %q, want queued", st)
	}
	if n := len(e.task(chatTaskName).Spec.Source.ChatMessages); n != maxChatQueued {
		t.Fatalf("chatMessages = %d", n)
	}
}

func TestChatReplyCopied(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "q", "message", "r1")
	e.chat.dispatchQueued(e.ctx)

	task := e.task(chatTaskName)
	orig := task.DeepCopy()
	task.Status.Chat = &aiv1.ChatStatus{Replies: []aiv1.ChatReply{{
		MessageID: id, Text: "answer", At: metav1.NewTime(e.now),
	}}}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.chat.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: chatTaskName}}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.task(chatTaskName).Spec.Source.ChatMessages); n != 0 {
		t.Fatalf("chatMessages still %d", n)
	}
	st, _ := e.msgState(id, chatTaskName)
	if st != "answered" {
		t.Fatalf("state = %q", st)
	}
	msgs, err := e.store.ChatMessages(e.ctx, ns, "shop", chatTaskName, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reply *store.ChatMessage
	for i := range msgs {
		if msgs[i].ReplyTo == id {
			reply = &msgs[i]
			break
		}
	}
	if reply == nil || reply.Role != "agent" || reply.Body != "answer" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestChatReplyError(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "q", "message", "e1")
	e.chat.dispatchQueued(e.ctx)
	task := e.task(chatTaskName)
	orig := task.DeepCopy()
	task.Status.Chat = &aiv1.ChatStatus{Replies: []aiv1.ChatReply{{
		MessageID: id, Text: "partial", Error: "interrupted", At: metav1.NewTime(e.now),
	}}}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.chat.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: chatTaskName}}); err != nil {
		t.Fatal(err)
	}
	msgs, err := e.store.ChatMessages(e.ctx, ns, "shop", chatTaskName, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reply *store.ChatMessage
	for i := range msgs {
		if msgs[i].ReplyTo == id {
			reply = &msgs[i]
			break
		}
	}
	if reply == nil || reply.Role != "system" || reply.Body != "error: interrupted\npartial" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestChatReplyIdempotent(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	id := e.insertUser(chatTaskName, "q", "message", "dup1")
	e.chat.dispatchQueued(e.ctx)
	task := e.task(chatTaskName)
	orig := task.DeepCopy()
	task.Status.Chat = &aiv1.ChatStatus{Replies: []aiv1.ChatReply{{
		MessageID: id, Text: "answer", At: metav1.NewTime(e.now),
	}}}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.InsertReply(e.ctx, store.ChatMessage{
		Namespace: ns, Project: "shop", Task: chatTaskName, Role: "agent",
		Body: "answer", ReplyTo: id, ExpiresAt: e.now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.chat.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: chatTaskName}}); err != nil {
		t.Fatal(err)
	}
	msgs, err := e.store.ChatMessages(e.ctx, ns, "shop", chatTaskName, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.ReplyTo == id {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
	if got := len(e.task(chatTaskName).Spec.Source.ChatMessages); got != 0 {
		t.Fatalf("chatMessages = %d", got)
	}
}

func TestChatStartsHibernatedPod(t *testing.T) {
	task := chatTask(chatTaskName)
	task.Spec.Source.ChatMessages = []aiv1.ChatMessage{{
		ID: 1, Kind: "message", Text: "hi", Author: "u", At: metav1.NewTime(t0),
	}}
	e := newTaskEnv(t, task)
	e.reconcile(chatTaskName)
	var pod corev1.Pod
	if !e.exists(&pod, chatTaskName) {
		t.Fatal("expected pod for chat without a run request")
	}
	got := e.task(chatTaskName)
	if got.Status.Phase != aiv1.TaskPhaseRunning || got.Status.Message != "answering chat" {
		t.Fatalf("phase=%s message=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Spec.RunRequest != 0 {
		t.Fatalf("runRequest = %d", got.Spec.RunRequest)
	}
}

func TestChatIdleTimeout(t *testing.T) {
	const name = "shop-42-chat-idle"
	e := newTaskEnv(t, newImplTask(name, 1))
	e.reconcile(name)
	e.finishRun(name)
	task := e.task(name)
	orig := task.DeepCopy()
	task.Status.Chat = &aiv1.ChatStatus{LastTurnTime: &metav1.Time{Time: e.now}}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}

	e.now = e.now.Add(aiv1.DefaultIdleTimeout + time.Second)
	res := e.reconcile(name)
	var pod corev1.Pod
	if !e.exists(&pod, name) {
		t.Fatal("pod must stay past idleTimeout when chatIdleTimeout remains")
	}
	if e.task(name).Status.Phase != aiv1.TaskPhaseIdle {
		t.Fatalf("phase %s", e.task(name).Status.Phase)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > aiv1.DefaultChatIdleTimeout {
		t.Fatalf("requeue %v", res.RequeueAfter)
	}

	e.now = e.now.Add(aiv1.DefaultChatIdleTimeout)
	e.reconcile(name)
	if e.exists(&pod, name) {
		t.Fatal("pod should hibernate after chatIdleTimeout")
	}
	if e.task(name).Status.Phase != aiv1.TaskPhaseHibernated {
		t.Fatalf("phase %s", e.task(name).Status.Phase)
	}
}

// staleReader returns a fixed (old) copy of the task, like a lagging cache.
type staleReader struct {
	client.Reader
	task *aiv1.AgentTask
}

func (r staleReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if t, ok := obj.(*aiv1.AgentTask); ok && key.Name == r.task.Name {
		r.task.DeepCopyInto(t)
		return nil
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestChatDispatchStaleTaskConflicts(t *testing.T) {
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName))
	stale := e.task(chatTaskName)
	// Meanwhile another message landed in the spec (a newer resourceVersion).
	fresh := e.task(chatTaskName)
	fresh.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 9000, Kind: "message", Text: "earlier"}}
	if err := e.c.Update(e.ctx, fresh); err != nil {
		t.Fatal(err)
	}
	e.chat.APIReader = staleReader{Reader: e.c, task: stale}

	id := e.insertUser(chatTaskName, "hello", "message", "stale1")
	e.chat.dispatchQueued(e.ctx)

	// The patch from the stale copy would replace the list and drop 9000:
	// it conflicts instead, and the row stays queued for the next round.
	if st, _ := e.msgState(id, chatTaskName); st != "queued" {
		t.Fatalf("state = %q, want queued after a conflict", st)
	}
	msgs := e.task(chatTaskName).Spec.Source.ChatMessages
	if len(msgs) != 1 || msgs[0].ID != 9000 {
		t.Fatalf("chatMessages = %+v, want only 9000 kept", msgs)
	}
	e.chat.APIReader = nil
	e.chat.dispatchQueued(e.ctx)
	if msgs := e.task(chatTaskName).Spec.Source.ChatMessages; len(msgs) != 2 {
		t.Fatalf("after a fresh read chatMessages = %+v, want both", msgs)
	}
}

func TestChatSweepFailsMessagesOfRemovedTasks(t *testing.T) {
	gone := chatTask("shop-43-gone")
	e := newChatEnv(t, chatProject(true), chatTask(chatTaskName), gone)
	kept := e.insertUser(chatTaskName, "hello", "message", "s1")
	lost := e.insertUser(gone.Name, "hello", "message", "s2")
	e.chat.dispatchQueued(e.ctx)
	if err := e.c.Delete(e.ctx, e.task(gone.Name)); err != nil {
		t.Fatal(err)
	}
	// Rows are stamped by the database clock; sweep only looks at old ones.
	e.now = time.Now().Add(2 * chatSweepAge)
	e.chat.sweep(e.ctx)

	if st, _ := e.msgState(kept, chatTaskName); st != "delivered" {
		t.Fatalf("message on its task: state = %q, want delivered", st)
	}
	if st, reason := e.msgState(lost, gone.Name); st != "failed" || reason != "the task was removed before an answer" {
		t.Fatalf("message of a removed task: state = %q %q", st, reason)
	}
}
