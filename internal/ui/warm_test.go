package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const warmPilot = "/api/v1/projects/team-a/shop/tasks/shop-pilot-m3/warm"

func warmEnv(t *testing.T, role string, patches *int) *testEnv {
	t.Helper()
	env := newTestEnv(t, withChat(oidcConfig(
		Rule{Roles: []string{"owners"}, Projects: []string{"team-a/shop"}, Chat: true},
		Rule{Roles: []string{"viewers"}, Projects: []string{"team-a/shop"}},
	)))
	pilot := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-pilot-m3"},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate},
	}
	if err := env.reader.Create(context.Background(), pilot); err != nil {
		t.Fatal(err)
	}
	env.server.SetResearchWriter(interceptor.NewClient(env.reader.(client.WithWatch), interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			*patches++
			return c.Patch(ctx, obj, p, opts...)
		},
	}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
	env.login(t, "/")
	return env
}

func (e *testEnv) warmRequested(t *testing.T, task string) *metav1.Time {
	t.Helper()
	var got aiv1.AgentTask
	if err := e.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: task}, &got); err != nil {
		t.Fatal(err)
	}
	return got.Spec.WarmRequestedAt
}

func TestWarmTask(t *testing.T) {
	var patches int
	env := warmEnv(t, "owners", &patches)

	if code := env.send(t, http.MethodPost, warmPilot, struct{}{}, nil); code != http.StatusAccepted {
		t.Fatalf("owner: %d", code)
	}
	first := env.warmRequested(t, "shop-pilot-m3")
	if first == nil || time.Since(first.Time) > time.Minute || patches != 1 {
		t.Fatalf("warmRequestedAt = %v after %d patches", first, patches)
	}
	// Within a minute: accepted, not written.
	if code := env.send(t, http.MethodPost, warmPilot, struct{}{}, nil); code != http.StatusAccepted || patches != 1 {
		t.Fatalf("debounce: %d, %d patches", code, patches)
	}
	if got := env.warmRequested(t, "shop-pilot-m3"); !got.Equal(first) {
		t.Errorf("debounced request changed the time: %v != %v", got, first)
	}
	// An old request is renewed.
	old := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	var task aiv1.AgentTask
	_ = env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-pilot-m3"}, &task)
	task.Spec.WarmRequestedAt = &old
	if err := env.reader.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	if code := env.send(t, http.MethodPost, warmPilot, struct{}{}, nil); code != http.StatusAccepted || patches != 2 {
		t.Fatalf("renew: %d, %d patches", code, patches)
	}
	// Not a Coordinate task.
	if code := env.send(t, http.MethodPost, "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal/warm", struct{}{}, nil); code != http.StatusNotFound {
		t.Errorf("implement task: %d", code)
	}
	if patches != 2 || env.warmRequested(t, "shop-42-add-paypal") != nil {
		t.Errorf("implement task patched: %d", patches)
	}
}

func TestWarmTaskNonOwner(t *testing.T) {
	var patches int
	env := warmEnv(t, "viewers", &patches)
	if code := env.send(t, http.MethodPost, warmPilot, struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer: %d", code)
	}
	if patches != 0 || env.warmRequested(t, "shop-pilot-m3") != nil {
		t.Errorf("viewer patched: %d", patches)
	}
}
