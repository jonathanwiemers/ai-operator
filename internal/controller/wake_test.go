package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
)

// fakeManager is just enough of a manager for SetupWithManager: it keeps
// the controller instead of running it, and its cache never sends events.
type fakeManager struct {
	manager.Manager
	scheme    *runtime.Scheme
	cache     cache.Cache
	runnables []manager.Runnable
}

func (m *fakeManager) GetScheme() *runtime.Scheme { return m.scheme }
func (m *fakeManager) GetCache() cache.Cache      { return m.cache }
func (m *fakeManager) GetLogger() logr.Logger     { return logr.Discard() }
func (m *fakeManager) Add(r manager.Runnable) error {
	m.runnables = append(m.runnables, r)
	return nil
}
func (m *fakeManager) GetControllerOptions() config.Controller {
	return config.Controller{SkipNameValidation: ptr.To(true)}
}

// TestWebhookDeliverySyncsProject runs a delivery through the receiver and
// the waker into the controller set up by SetupWithManager.
func TestWebhookDeliverySyncsProject(t *testing.T) {
	e := newProjectEnv(t)
	p := testProject()
	if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Webhook.SecretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hook"}, Key: "token"}
	if err := e.c.Update(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	hook := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hook", Namespace: ns}, Data: map[string][]byte{"token": []byte("hook-token")}}
	if err := e.c.Create(e.ctx, hook); err != nil {
		t.Fatal(err)
	}
	var syncs atomic.Int32
	e.r.NewGitLab = func(string, string) gitlab.API { syncs.Add(1); return e.gl }

	waker := webhook.NewWaker(5*time.Second, 10, clock.RealClock{})
	e.r.Wakes = waker.Events()
	mgr := &fakeManager{scheme: e.c.Scheme(), cache: &informertest.FakeInformers{Scheme: e.c.Scheme()}}
	if err := e.r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	if len(mgr.runnables) != 1 {
		t.Fatalf("SetupWithManager added %d runnables, want the controller", len(mgr.runnables))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.runnables[0].Start(ctx) }()

	receiver := &webhook.Receiver{Reader: e.c, Wake: waker.Wake, Log: logr.Discard()}
	for range 3 {
		req := httptest.NewRequest(http.MethodPost, webhook.Path, strings.NewReader(`{"project":{"path_with_namespace":"group/app"}}`))
		req.Header.Set("X-Gitlab-Token", "hook-token")
		rec := httptest.NewRecorder()
		receiver.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("delivery = %d, want 202", rec.Code)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for syncs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// The burst is coalesced: one sync now, the trailing one only after 5 s.
	time.Sleep(200 * time.Millisecond)
	if got := syncs.Load(); got != 1 {
		t.Fatalf("syncs after the deliveries = %d, want 1", got)
	}
}

func TestSetupWithoutWakesStarts(t *testing.T) {
	e := newProjectEnv(t)
	mgr := &fakeManager{scheme: e.c.Scheme(), cache: &informertest.FakeInformers{Scheme: e.c.Scheme()}}
	if err := e.r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := mgr.runnables[0].Start(ctx); err != nil {
		t.Fatalf("controller without wakes: %v", err)
	}
}
