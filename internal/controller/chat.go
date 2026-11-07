package controller

import (
	"context"
	"slices"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const (
	maxChatQueued     = 20
	chatPollInterval  = 30 * time.Second
	chatListenMinWait = 5 * time.Second
	chatListenMaxWait = 5 * time.Minute
	chatDBRetry       = 5 * time.Second
	// chatSweepAge is how old a delivered message must be before sweep
	// checks that it is still on its task.
	chatSweepAge = time.Minute
	// chatQueuedLimit is generous so rows waiting behind a full task (20
	// in its spec) do not starve other tasks' rows.
	chatQueuedLimit = 500
)

// ChatDB is the research database as the chat dispatcher uses it.
type ChatDB interface {
	Ready() <-chan struct{}
	Store() *store.Store
}

// Chat hands Desk chat_messages to AgentTasks and copies replies back.
// As a leader-elected runnable it listens (and polls) for queued rows; as a
// reconciler it inserts status.chat.replies into the table and drops the
// answered messages from the spec.
type Chat struct {
	client.Client
	// APIReader reads tasks without the cache for dispatch, so a message
	// is appended to the current list; optional (falls back to Client).
	APIReader client.Reader
	DB        ChatDB
	Retention time.Duration
	Log       logr.Logger
	Now       func() time.Time
	// Wake asks the project reconciler to sync after a chat command was
	// delivered onto a task (optional).
	Wake func(types.NamespacedName)
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects,verbs=get;list;watch

// NeedLeaderElection makes the dispatch loop leader-only.
func (c *Chat) NeedLeaderElection() bool { return true }

func (c *Chat) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Start waits for the database, then dispatches queued messages until ctx
// ends: once immediately, on each LISTEN notification, and every poll.
func (c *Chat) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-c.DB.Ready():
	}
	wake := make(chan struct{}, 1)
	go c.listenLoop(ctx, wake)
	c.dispatch(ctx)
	tick := time.NewTicker(chatPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			c.dispatch(ctx)
			c.sweep(ctx)
		case <-wake:
			c.dispatch(ctx)
		}
	}
}

func (c *Chat) listenLoop(ctx context.Context, wake chan<- struct{}) {
	for delay := chatListenMinWait; ; delay = min(2*delay, chatListenMaxWait) {
		s := c.DB.Store()
		if s == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}
		err := s.ListenChat(ctx, func(int64) {
			select {
			case wake <- struct{}{}:
			default:
			}
		})
		if ctx.Err() != nil {
			return
		}
		c.Log.Error(err, "chat listen failed, retrying", "in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// dispatchQueued is the unit-test entry for one poll of queued rows.
func (c *Chat) dispatchQueued(ctx context.Context) {
	c.dispatch(ctx)
}

func (c *Chat) dispatch(ctx context.Context) {
	s := c.DB.Store()
	if s == nil {
		return
	}
	msgs, err := s.QueuedMessages(ctx, chatQueuedLimit)
	if err != nil {
		c.Log.Error(err, "listing queued chat messages")
		return
	}
	for _, m := range msgs {
		c.dispatchOne(ctx, s, m)
	}
}

// sweep fails delivered messages that are no longer on their task: the
// task was deleted (issue closed, task recreated) before the message was
// answered. A delivered message is on the task's spec until its reply is
// stored, which marks it answered first, so this finds only lost ones.
func (c *Chat) sweep(ctx context.Context) {
	s := c.DB.Store()
	if s == nil {
		return
	}
	msgs, err := s.DeliveredMessages(ctx, c.now().Add(-chatSweepAge), chatQueuedLimit)
	if err != nil {
		c.Log.Error(err, "listing delivered chat messages")
		return
	}
	reader := c.APIReader
	if reader == nil {
		reader = c.Client
	}
	for _, m := range msgs {
		var task aiv1.AgentTask
		err := reader.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Task}, &task)
		if err != nil && !apierrors.IsNotFound(err) {
			c.Log.Error(err, "getting task for delivered chat message", "id", m.ID, "task", m.Task)
			continue
		}
		if err == nil && slices.ContainsFunc(task.Spec.Source.ChatMessages, func(cm aiv1.ChatMessage) bool { return cm.ID == m.ID }) {
			continue
		}
		if err := s.SetMessageState(ctx, []int64{m.ID}, "failed", "the task was removed before an answer"); err != nil {
			c.Log.Error(err, "marking lost chat message failed", "id", m.ID)
			continue
		}
		c.Log.Info("chat message", "id", m.ID, "task", m.Task, "state", "failed", "reason", "task removed")
	}
}

func (c *Chat) dispatchOne(ctx context.Context, s *store.Store, m store.ChatMessage) {
	fail := func(reason string) {
		if err := s.SetMessageState(ctx, []int64{m.ID}, "failed", reason); err != nil {
			c.Log.Error(err, "marking chat message failed", "id", m.ID)
			return
		}
		c.Log.Info("chat message", "id", m.ID, "task", m.Task, "state", "failed")
	}
	reader := c.APIReader
	if reader == nil {
		reader = c.Client
	}
	var task aiv1.AgentTask
	if err := reader.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Task}, &task); err != nil {
		if apierrors.IsNotFound(err) {
			fail("task not found")
			return
		}
		c.Log.Error(err, "getting task for chat message", "id", m.ID, "task", m.Task)
		return
	}
	if task.Spec.ProjectRef != m.Project {
		fail("task belongs to another project")
		return
	}
	var project aiv1.AgentProject
	if err := c.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Project}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			fail("project not found")
			return
		}
		c.Log.Error(err, "getting project for chat message", "id", m.ID, "project", m.Project)
		return
	}
	if !project.Spec.Agents.Chat {
		fail("live chat is not enabled")
		return
	}
	if m.Kind == "command" {
		cmd, err := chatcmd.Parse(m.Body)
		if err != nil {
			fail(err.Error())
			return
		}
		if !chatCommandAllowed(task, cmd) {
			fail("not supported on this task yet")
			return
		}
	}
	for _, existing := range task.Spec.Source.ChatMessages {
		if existing.ID == m.ID {
			if err := s.SetMessageState(ctx, []int64{m.ID}, "delivered", ""); err != nil {
				c.Log.Error(err, "marking chat message delivered", "id", m.ID)
				return
			}
			c.Log.Info("chat message", "id", m.ID, "task", m.Task, "state", "delivered")
			return
		}
	}
	if len(task.Spec.Source.ChatMessages) >= maxChatQueued {
		return
	}
	orig := task.DeepCopy()
	task.Spec.Source.ChatMessages = append(task.Spec.Source.ChatMessages, aiv1.ChatMessage{
		ID:         m.ID,
		Kind:       m.Kind,
		Text:       m.Body,
		Author:     m.Author,
		AuthorName: m.AuthorName,
		At:         metav1.NewTime(m.At),
	})
	slices.SortFunc(task.Spec.Source.ChatMessages, func(a, b aiv1.ChatMessage) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	// The patch replaces the whole list: the optimistic lock makes a
	// concurrent change (the reply copier dropping answered messages) a
	// conflict, and the row stays queued for the next round.
	if err := c.Patch(ctx, &task, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return
		}
		c.Log.Error(err, "patching chat message onto task", "id", m.ID, "task", m.Task)
		return
	}
	if err := s.SetMessageState(ctx, []int64{m.ID}, "delivered", ""); err != nil {
		c.Log.Error(err, "marking chat message delivered", "id", m.ID)
		return
	}
	c.Log.Info("chat message", "id", m.ID, "task", m.Task, "state", "delivered")
	if m.Kind == "command" && c.Wake != nil {
		c.Wake(types.NamespacedName{Namespace: m.Namespace, Name: m.Project})
	}
}

// Reconcile copies status.chat.replies and spec.source.chatAnswers whose
// message is still in the spec into chat_messages, then drops those
// messages (and their answers) from the spec. An operator answer is
// preferred over a runner reply for the same message (they cannot both
// exist for a command: the runner skips those).
func (c *Chat) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	select {
	case <-c.DB.Ready():
	default:
		return ctrl.Result{RequeueAfter: chatDBRetry}, nil
	}
	s := c.DB.Store()
	if s == nil {
		return ctrl.Result{RequeueAfter: chatDBRetry}, nil
	}

	logger := log.FromContext(ctx)
	var task aiv1.AgentTask
	if err := c.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if len(task.Spec.Source.ChatMessages) == 0 {
		return ctrl.Result{}, nil
	}

	replies := map[int64]aiv1.ChatReply{}
	if task.Status.Chat != nil {
		for _, r := range task.Status.Chat.Replies {
			replies[r.MessageID] = r
		}
	}
	answers := map[int64]aiv1.ChatReply{}
	for _, a := range task.Spec.Source.ChatAnswers {
		answers[a.MessageID] = a
	}
	if len(replies) == 0 && len(answers) == 0 {
		return ctrl.Result{}, nil
	}

	orig := task.DeepCopy()
	var keep []aiv1.ChatMessage
	dropped := false
	for _, m := range task.Spec.Source.ChatMessages {
		if ans, ok := answers[m.ID]; ok {
			if _, err := s.InsertReply(ctx, store.ChatMessage{
				Namespace: task.Namespace,
				Project:   task.Spec.ProjectRef,
				Task:      task.Name,
				Role:      "system",
				Body:      ans.Text,
				ReplyTo:   m.ID,
				ExpiresAt: c.now().Add(c.Retention),
			}); err != nil {
				logger.Error(err, "inserting chat command answer", "messageID", m.ID)
				return ctrl.Result{}, err
			}
			dropped = true
			continue
		}
		rep, ok := replies[m.ID]
		if !ok {
			keep = append(keep, m)
			continue
		}
		role, body := "agent", rep.Text
		if rep.Error != "" {
			role = "system"
			body = "error: " + rep.Error
			if rep.Text != "" {
				body += "\n" + rep.Text
			}
		}
		if _, err := s.InsertReply(ctx, store.ChatMessage{
			Namespace:  task.Namespace,
			Project:    task.Spec.ProjectRef,
			Task:       task.Name,
			AuthorName: chatAuthorName(&task),
			Role:       role,
			Body:       body,
			ReplyTo:    m.ID,
			ExpiresAt:  c.now().Add(c.Retention),
		}); err != nil {
			logger.Error(err, "inserting chat reply", "messageID", m.ID)
			return ctrl.Result{}, err
		}
		dropped = true
	}
	if !dropped {
		return ctrl.Result{}, nil
	}
	task.Spec.Source.ChatMessages = keep
	keptIDs := map[int64]bool{}
	for _, m := range keep {
		keptIDs[m.ID] = true
	}
	var keepAnswers []aiv1.ChatReply
	for _, a := range task.Spec.Source.ChatAnswers {
		if keptIDs[a.MessageID] {
			keepAnswers = append(keepAnswers, a)
		}
	}
	task.Spec.Source.ChatAnswers = keepAnswers
	// Optimistic lock: a stale cached task must not drop a message the
	// dispatcher appended meanwhile (the patch replaces the whole list).
	if err := c.Patch(ctx, &task, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// chatAuthorName is the engine name when the task status has one reading.
func chatAuthorName(task *aiv1.AgentTask) string {
	if n := len(task.Status.Run.EngineUsage); n > 0 {
		return string(task.Status.Run.EngineUsage[n-1].Engine)
	}
	return ""
}

// chatCommandAllowed reports whether cmd may be delivered onto task.
func chatCommandAllowed(task aiv1.AgentTask, cmd chatcmd.Command) bool {
	switch {
	case (cmd.ImplementOnly() || cmd.PromptCmd()) && task.Spec.Type == aiv1.TaskTypeImplement:
		return true
	case cmd.Autopilot() && task.Spec.Type == aiv1.TaskTypeSpec && task.Spec.Autopilot:
		return true
	case (cmd.ProposalOnly() || cmd.PromptCmd() || cmd.RevisitCmd() || cmd.DraftCmd()) && task.Spec.Type == aiv1.TaskTypeCoordinate:
		return true
	default:
		return false
	}
}

// SetupWithManager registers the reply reconciler.
func (c *Chat) SetupWithManager(mgr ctrl.Manager) error {
	if c.Log.GetSink() == nil {
		c.Log = ctrl.Log.WithName("chat")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentTask{}).
		Named("chatreply").
		Complete(c)
}
