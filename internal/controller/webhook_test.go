package controller

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const hookURL = "https://op.example/hooks/gitlab"

// newWebhookEnv is a project env with webhooks enabled for hookURL, the
// secret "s3cret" and a clock the test moves.
func newWebhookEnv(t *testing.T) (*projectEnv, *time.Time) {
	t.Helper()
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hook", Namespace: ns}, Data: map[string][]byte{"token": []byte("s3cret\n")}}
	if err := e.c.Create(e.ctx, secret); err != nil {
		t.Fatal(err)
	}
	e.setWebhook(func(w *aiv1.WebhookSpec) {
		*w = aiv1.WebhookSpec{Enabled: true, URL: hookURL,
			SecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hook"}, Key: "token"}}
	})
	return e, &now
}

func (e *projectEnv) setWebhook(edit func(*aiv1.WebhookSpec)) {
	e.t.Helper()
	p := e.project()
	edit(&p.Spec.Webhook)
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) setHookSecret(value string) {
	e.t.Helper()
	var s corev1.Secret
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "hook"}, &s); err != nil {
		e.t.Fatal(err)
	}
	s.Data["token"] = []byte(value)
	if err := e.c.Update(e.ctx, &s); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) webhookCondition() *metav1.Condition {
	return meta.FindStatusCondition(e.project().Status.Conditions, aiv1.ConditionWebhookRegistered)
}

func (e *projectEnv) wantWebhookCondition(status metav1.ConditionStatus, reason string) {
	e.t.Helper()
	c := e.webhookCondition()
	if c == nil || c.Status != status || c.Reason != reason {
		e.t.Fatalf("want WebhookRegistered=%s/%s, got %+v", status, reason, c)
	}
}

func (e *projectEnv) wantHookWrites(want ...string) {
	e.t.Helper()
	if !slices.Equal(e.gl.HookWrites, want) {
		e.t.Fatalf("hook writes %v, want %v", e.gl.HookWrites, want)
	}
}

func TestWebhookRegisteredOnce(t *testing.T) {
	e, now := newWebhookEnv(t)
	foreign := gitlabfake.Hook{ProjectHook: gitlab.ProjectHook{ID: 7, URL: "https://ci.example/hook", PushEvents: true}, Token: "theirs"}
	e.gl.Hooks = []gitlabfake.Hook{foreign}

	e.reconcile()
	if len(e.gl.Hooks) != 2 || e.gl.Hooks[0] != foreign {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	ours := e.gl.Hooks[1]
	want := gitlabfake.Hook{ProjectHook: gitlab.ProjectHook{ID: ours.ID, URL: hookURL, IssuesEvents: true, NoteEvents: true,
		MergeRequestsEvents: true, PipelineEvents: true, PushEvents: true, EnableSSLVerification: true}, Token: "s3cret"}
	if ours != want {
		t.Fatalf("hook %+v, want %+v", ours, want)
	}
	e.wantHookWrites("add " + itoa(ours.ID))
	e.wantWebhookCondition(metav1.ConditionTrue, "Registered")
	st := e.project().Status.Webhook
	if st == nil || st.HookID != ours.ID || st.ConfigHash == "" || st.CheckedAt == nil || !st.CheckedAt.Time.Equal(*now) {
		t.Fatalf("status %+v", st)
	}
	if strings.Contains(st.ConfigHash, "s3cret") {
		t.Fatal("the status must not carry the secret")
	}

	// The next polls make no hook call at all.
	lists := e.gl.HookLists
	for range 3 {
		*now = now.Add(10 * time.Minute)
		e.reconcile()
	}
	e.wantHookWrites("add " + itoa(ours.ID))
	if e.gl.HookLists != lists {
		t.Fatalf("%d hook lists, want %d", e.gl.HookLists, lists)
	}
	e.wantWebhookCondition(metav1.ConditionTrue, "Registered")
}

func TestWebhookSecretChangeEditsTheHook(t *testing.T) {
	e, now := newWebhookEnv(t)
	e.reconcile()
	id := e.gl.Hooks[0].ID

	e.setHookSecret("rotated")
	*now = now.Add(time.Minute)
	e.reconcile()
	if len(e.gl.Hooks) != 1 || e.gl.Hooks[0].ID != id || e.gl.Hooks[0].Token != "rotated" {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	e.wantHookWrites("add "+itoa(id), "edit "+itoa(id))
}

func TestWebhookURLChangeEditsTheHook(t *testing.T) {
	e, now := newWebhookEnv(t)
	e.reconcile()
	id := e.gl.Hooks[0].ID

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.URL = "https://new.example/hooks/gitlab" })
	*now = now.Add(time.Minute)
	e.reconcile()
	if len(e.gl.Hooks) != 1 || e.gl.Hooks[0].ID != id || e.gl.Hooks[0].URL != "https://new.example/hooks/gitlab" || e.gl.Hooks[0].Token != "s3cret" {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	e.wantHookWrites("add "+itoa(id), "edit "+itoa(id))
}

func TestWebhookAdoptsTheHookForItsURL(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.gl.Hooks = []gitlabfake.Hook{{ProjectHook: gitlab.ProjectHook{ID: 7, URL: hookURL, PushEvents: true}, Token: "old"}}

	e.reconcile()
	if len(e.gl.Hooks) != 1 || e.gl.Hooks[0].Token != "s3cret" || !e.gl.Hooks[0].NoteEvents {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	e.wantHookWrites("edit 7")
	if st := e.project().Status.Webhook; st == nil || st.HookID != 7 {
		t.Fatalf("status %+v", st)
	}
}

func TestWebhookDeletedByHandIsRecreatedAfterTheRecheck(t *testing.T) {
	e, now := newWebhookEnv(t)
	e.reconcile()
	first := e.gl.Hooks[0].ID
	e.gl.Hooks = nil

	*now = now.Add(30 * time.Minute)
	e.reconcile()
	e.wantHookWrites("add " + itoa(first))

	*now = now.Add(31 * time.Minute)
	e.reconcile()
	if len(e.gl.Hooks) != 1 || e.gl.Hooks[0].ID == first {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	second := e.gl.Hooks[0].ID
	e.wantHookWrites("add "+itoa(first), "add "+itoa(second))
	if st := e.project().Status.Webhook; st == nil || st.HookID != second {
		t.Fatalf("status %+v", st)
	}

	// A recheck that finds the hook as it was writes nothing.
	*now = now.Add(61 * time.Minute)
	e.reconcile()
	e.wantHookWrites("add "+itoa(first), "add "+itoa(second))
	if st := e.project().Status.Webhook; !st.CheckedAt.Time.Equal(*now) {
		t.Fatalf("recheck not recorded: %+v", st)
	}
}

func TestWebhookForbiddenIsAConditionAndTheSyncGoesOn(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.gl.HookError = &gitlab.Error{StatusCode: http.StatusForbidden, Method: "GET", Path: "hooks"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")

	e.reconcile() // fails on a Synced condition that is not True
	e.wantWebhookCondition(metav1.ConditionFalse, "Forbidden")
	if c := e.webhookCondition(); !strings.Contains(c.Message, "Maintainer") {
		t.Fatalf("message %q", c.Message)
	}
	if _, ok := e.tasks()["shop-42-add-paypal-button"]; !ok {
		t.Fatalf("the rest of the sync must run, tasks %v", keys(e.tasks()))
	}

	// Tried again on the next poll.
	e.gl.HookError = nil
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionTrue, "Registered")
	if len(e.gl.Hooks) != 1 {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
}

func TestWebhookOtherErrorsAreFailed(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.gl.HookError = &gitlab.Error{StatusCode: http.StatusUnprocessableEntity, Method: "POST", Path: "hooks", Body: "url is blocked"}
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionFalse, "Failed")
	if c := e.webhookCondition(); !strings.Contains(c.Message, "url is blocked") {
		t.Fatalf("message %q", c.Message)
	}
	if e.project().Status.Webhook != nil {
		t.Fatal("nothing registered, nothing recorded")
	}
}

func TestWebhookInvalidWithoutURLOrSecret(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.setWebhook(func(w *aiv1.WebhookSpec) { w.URL = "" })
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionFalse, "Invalid")

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.URL = hookURL; w.SecretRef.Key = "missing" })
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionFalse, "Invalid")

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.SecretRef = nil })
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionFalse, "Invalid")
	e.wantHookWrites()
}

func TestWebhookDisableDeletesOnlyOurHook(t *testing.T) {
	e, now := newWebhookEnv(t)
	foreign := gitlabfake.Hook{ProjectHook: gitlab.ProjectHook{ID: 7, URL: "https://ci.example/hook"}, Token: "theirs"}
	e.gl.Hooks = []gitlabfake.Hook{foreign}
	e.reconcile()
	id := e.gl.Hooks[1].ID

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled = false })
	*now = now.Add(time.Minute)
	e.reconcile()
	if len(e.gl.Hooks) != 1 || e.gl.Hooks[0] != foreign {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
	e.wantHookWrites("add "+itoa(id), "delete "+itoa(id))
	if p := e.project(); p.Status.Webhook != nil || meta.FindStatusCondition(p.Status.Conditions, aiv1.ConditionWebhookRegistered) != nil {
		t.Fatalf("status %+v conditions %+v", p.Status.Webhook, p.Status.Conditions)
	}

	// Nothing more to delete.
	e.reconcile()
	e.wantHookWrites("add "+itoa(id), "delete "+itoa(id))
}

func TestWebhookDisableWithTheHookGone(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.reconcile()
	e.gl.Hooks = nil

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled = false })
	e.reconcile()
	if e.project().Status.Webhook != nil || e.webhookCondition() != nil {
		t.Fatal("a 404 on delete counts as done")
	}
}

func TestWebhookDisableForbiddenKeepsTheHookID(t *testing.T) {
	e, _ := newWebhookEnv(t)
	e.reconcile()
	id := e.gl.Hooks[0].ID

	e.setWebhook(func(w *aiv1.WebhookSpec) { w.Enabled = false })
	e.gl.HookError = &gitlab.Error{StatusCode: http.StatusForbidden, Method: "DELETE", Path: "hooks"}
	e.reconcile()
	e.wantWebhookCondition(metav1.ConditionFalse, "Forbidden")
	if st := e.project().Status.Webhook; st == nil || st.HookID != id {
		t.Fatalf("status %+v", st)
	}

	e.gl.HookError = nil
	e.reconcile()
	if len(e.gl.Hooks) != 0 || e.project().Status.Webhook != nil {
		t.Fatalf("hooks %+v", e.gl.Hooks)
	}
}

func TestWebhookOffTouchesNothing(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.HookError = &gitlab.Error{StatusCode: http.StatusForbidden}
	e.reconcile()
	if c := meta.FindStatusCondition(e.project().Status.Conditions, aiv1.ConditionWebhookRegistered); c != nil {
		t.Fatalf("condition %+v", c)
	}
}
