package webhook

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const (
	goodToken = "s3cret-token"
	otherTok  = "other-token"
)

func project(ns, name, path, secret string) *aiv1.AgentProject {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	p.Spec.GitLab.Project = path
	if secret != "" {
		p.Spec.Webhook.SecretRef = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: "token",
		}
	}
	return p
}

func secret(ns, name, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       map[string][]byte{"token": []byte(value)},
	}
}

type env struct {
	receiver *Receiver
	woken    []types.NamespacedName
	logs     []string
}

func newEnv(t *testing.T, objs ...client.Object) *env {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aiv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	e := &env{}
	e.receiver = &Receiver{
		Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		Wake:   func(n types.NamespacedName) { e.woken = append(e.woken, n) },
		Log:    funcr.New(func(prefix, args string) { e.logs = append(e.logs, prefix+" "+args) }, funcr.Options{Verbosity: 10}),
	}
	return e
}

// standard is one AgentProject on group/app with its webhook secret.
func standard(t *testing.T, extra ...client.Object) *env {
	return newEnv(t, append([]client.Object{
		project("team", "app", "group/app", "app-hook"),
		secret("team", "app-hook", goodToken+"\n"),
	}, extra...)...)
}

func (e *env) deliver(method, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, Path, strings.NewReader(body))
	req.Header.Set("X-Gitlab-Event", "Note Hook")
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	rec := httptest.NewRecorder()
	e.receiver.ServeHTTP(rec, req)
	return rec
}

func payload(path string) string {
	return `{"object_kind":"note","project":{"id":1,"path_with_namespace":"` + path + `"},"object_attributes":{"note":"/agent run"}}`
}

func (e *env) expect(t *testing.T, rec *httptest.ResponseRecorder, code int, woken ...types.NamespacedName) {
	t.Helper()
	if rec.Code != code {
		t.Errorf("status = %d, want %d (%s)", rec.Code, code, rec.Body.String())
	}
	if !slices.Equal(e.woken, woken) {
		t.Errorf("woken = %v, want %v", e.woken, woken)
	}
}

var app = types.NamespacedName{Namespace: "team", Name: "app"}

func TestRightTokenWakesProject(t *testing.T) {
	e := standard(t, project("team", "other", "group/other", "app-hook"))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusAccepted, app)
}

func TestProjectPathIsCaseInsensitive(t *testing.T) {
	e := standard(t)
	e.expect(t, e.deliver(http.MethodPost, payload("Group/App"), goodToken), http.StatusAccepted, app)
}

func TestRefused(t *testing.T) {
	cases := []struct {
		name, body, token string
	}{
		{"wrong token", payload("group/app"), otherTok},
		{"token prefix", payload("group/app"), goodToken[:4]},
		{"missing token", payload("group/app"), ""},
		{"unknown project", payload("group/unknown"), goodToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := standard(t)
			e.expect(t, e.deliver(http.MethodPost, c.body, c.token), http.StatusUnauthorized)
		})
	}
}

func TestProjectWithoutSecretRefIsRefused(t *testing.T) {
	e := newEnv(t, project("team", "app", "group/app", ""), secret("team", "app-hook", goodToken))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusUnauthorized)
}

func TestMissingOrEmptySecretIsRefused(t *testing.T) {
	e := newEnv(t, project("team", "app", "group/app", "app-hook"))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusUnauthorized)

	e = newEnv(t, project("team", "app", "group/app", "app-hook"), secret("team", "app-hook", " \n"))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), " "), http.StatusUnauthorized)

	e = newEnv(t, project("team", "app", "group/app", "app-hook"), secret("team", "app-hook", ""))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), ""), http.StatusUnauthorized)
}

func TestSecretFromProjectNamespace(t *testing.T) {
	// A secret of the same name in another namespace does not count.
	e := newEnv(t, project("team", "app", "group/app", "app-hook"), secret("elsewhere", "app-hook", goodToken))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusUnauthorized)
}

func TestOnlyMatchingSecretOfSameGitLabProjectIsWoken(t *testing.T) {
	e := standard(t,
		project("staging", "app", "group/app", "staging-hook"),
		secret("staging", "staging-hook", otherTok),
	)
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), otherTok), http.StatusAccepted,
		types.NamespacedName{Namespace: "staging", Name: "app"})
}

func TestBodyOverLimit(t *testing.T) {
	e := standard(t)
	// A valid object first, so a decoder reading only that would accept it.
	body := payload("group/app") + strings.Repeat(" ", MaxBody)
	e.expect(t, e.deliver(http.MethodPost, body, goodToken), http.StatusRequestEntityTooLarge)
}

func TestBodyAtLimitIsRead(t *testing.T) {
	e := standard(t)
	body := payload("group/app")
	body += strings.Repeat(" ", MaxBody-len(body))
	e.expect(t, e.deliver(http.MethodPost, body, goodToken), http.StatusAccepted, app)
}

func TestMalformedBody(t *testing.T) {
	for _, body := range []string{`{"project":`, `[]`, `{}`, `{"project":{"path_with_namespace":""}}`, `{"project":"group/app"}`} {
		e := standard(t)
		e.expect(t, e.deliver(http.MethodPost, body, goodToken), http.StatusBadRequest)
	}
}

func TestOtherMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodHead} {
		e := standard(t)
		rec := e.deliver(m, payload("group/app"), goodToken)
		e.expect(t, rec, http.StatusMethodNotAllowed)
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Errorf("%s: Allow = %q", m, got)
		}
	}
}

func TestLogsNeitherBodyNorToken(t *testing.T) {
	e := standard(t)
	const marker = "body-only-marker"
	body := `{"project":{"path_with_namespace":"group/app"},"object_attributes":{"note":"` + marker + `"}}`
	e.deliver(http.MethodPost, body, goodToken)
	e.deliver(http.MethodPost, body, otherTok)
	e.deliver(http.MethodPost, `{"broken":"`+marker, goodToken)
	e.deliver(http.MethodPost, body+strings.Repeat(" ", MaxBody), goodToken)

	logs := strings.Join(e.logs, "\n")
	if len(e.logs) != 4 {
		t.Errorf("want one log line per delivery, got:\n%s", logs)
	}
	for _, s := range []string{marker, goodToken, otherTok} {
		if strings.Contains(logs, s) {
			t.Errorf("logs contain %q:\n%s", s, logs)
		}
	}
	for _, want := range []string{`"project"="group/app"`, `"event"="Note Hook"`, `"remote"="192.0.2.1:1234"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %s:\n%s", want, logs)
		}
	}
}

func TestLoggedValuesAreClipped(t *testing.T) {
	e := standard(t)
	long := strings.Repeat("a", 4096)
	e.deliver(http.MethodPost, payload(long), goodToken)
	if logs := strings.Join(e.logs, "\n"); strings.Contains(logs, long[:300]) {
		t.Errorf("project path logged unclipped: %d bytes", len(logs))
	}
}
