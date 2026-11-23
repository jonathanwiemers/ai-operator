package webhook

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clocktesting "k8s.io/utils/clock/testing"
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
	receiver   *Receiver
	deliveries *Deliveries
	woken      []types.NamespacedName
	logs       []string
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
	e := &env{deliveries: NewDeliveries(clocktesting.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)))}
	e.receiver = &Receiver{
		Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		Wake: func(n types.NamespacedName) {
			// The delivery is recorded before the wake.
			if _, ok := e.deliveries.Last(n); !ok {
				t.Errorf("%s woken before its delivery was recorded", n)
			}
			e.woken = append(e.woken, n)
		},
		Deliveries: e.deliveries,
		Log:        funcr.New(func(prefix, args string) { e.logs = append(e.logs, prefix+" "+args) }, funcr.Options{Verbosity: 10}),
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
			if _, ok := e.deliveries.Last(app); ok {
				t.Error("a refused delivery was recorded")
			}
		})
	}
}

func TestAcceptedDeliveryIsRecorded(t *testing.T) {
	e := standard(t, project("team", "other", "group/other", "app-hook"))
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusAccepted, app)
	if got, ok := e.deliveries.Last(app); !ok || !got.Equal(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("Last(app) = %v, %v, want the delivery's time", got, ok)
	}
	if _, ok := e.deliveries.Last(types.NamespacedName{Namespace: "team", Name: "other"}); ok {
		t.Error("a project that was not woken got a delivery")
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

func TestNoteOnAnIssueIsRecorded(t *testing.T) {
	cases := []struct {
		name, body string
		want       bool
	}{
		{"note on an issue", `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"Issue"},"issue":{"iid":7}}`, true},
		{"note on a merge request", `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"MergeRequest"},"merge_request":{"iid":7}}`, false},
		{"issue event", `{"object_kind":"issue","project":{"path_with_namespace":"group/app"},"object_attributes":{"iid":7},"issue":{"iid":7}}`, false},
		{"note without an issue", `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"Issue"}}`, false},
		{"odd issue field", `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"Issue"},"issue":"7"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := standard(t)
			e.expect(t, e.deliver(http.MethodPost, c.body, goodToken), http.StatusAccepted, app)
			if _, ok := e.deliveries.NoteAt(app, 7); ok != c.want {
				t.Errorf("note on #7 recorded: %v, want %v", ok, c.want)
			}
		})
	}
}

func TestRefusedNoteIsNotRecorded(t *testing.T) {
	e := standard(t)
	body := `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"Issue"},"issue":{"iid":7}}`
	e.expect(t, e.deliver(http.MethodPost, body, otherTok), http.StatusUnauthorized)
	if _, ok := e.deliveries.NoteAt(app, 7); ok {
		t.Error("a refused note was recorded")
	}
}

// projectSet is a set on group "group" whose template's webhook secret is
// secretName, with entries hooked on paths.
func projectSet(t *testing.T, ns, name, secretName string, hooked ...string) *aiv1.AgentProjectSet {
	t.Helper()
	s := &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	s.Spec.Template = &runtime.RawExtension{Raw: []byte(`{"webhook":{"enabled":true,"url":"https://hooks.example","secretRef":{"name":"` + secretName + `","key":"token"}}}`)}
	for i, p := range hooked {
		s.Status.Projects = append(s.Status.Projects, aiv1.ProjectSetEntry{Path: p, State: aiv1.ProjectSetDormant, HookID: int64(i + 1)})
	}
	// An entry without a hook does not count.
	s.Status.Projects = append(s.Status.Projects, aiv1.ProjectSetEntry{Path: "group/nohook", State: aiv1.ProjectSetDormant})
	return s
}

// withSets wakes sets into e.sets.
func (e *env) withSets(sets *[]types.NamespacedName) *env {
	e.receiver.WakeSet = func(n types.NamespacedName) { *sets = append(*sets, n) }
	return e
}

var jo = types.NamespacedName{Namespace: "team", Name: "jo"}

func TestSetHookWakesTheSet(t *testing.T) {
	var sets []types.NamespacedName
	e := newEnv(t, projectSet(t, "team", "jo", "set-hook", "group/dormant"), secret("team", "set-hook", goodToken)).withSets(&sets)
	before := testutil.ToFloat64(deliveries.WithLabelValues("note", "set"))
	e.expect(t, e.deliver(http.MethodPost, payload("Group/Dormant"), goodToken), http.StatusAccepted)
	if !slices.Equal(sets, []types.NamespacedName{jo}) {
		t.Fatalf("sets woken %v", sets)
	}
	if got := testutil.ToFloat64(deliveries.WithLabelValues("note", "set")) - before; got != 1 {
		t.Fatalf("set deliveries counted %v", got)
	}

	for _, c := range []struct{ name, path, token string }{
		{"wrong token", "group/dormant", otherTok},
		{"no hook on the path", "group/nohook", goodToken},
		{"unknown path", "group/unknown", goodToken},
	} {
		t.Run(c.name, func(t *testing.T) {
			sets = nil
			e.expect(t, e.deliver(http.MethodPost, payload(c.path), c.token), http.StatusUnauthorized)
			if len(sets) != 0 {
				t.Fatalf("sets woken %v", sets)
			}
		})
	}
}

func TestAgentProjectWinsOverASetHook(t *testing.T) {
	var sets []types.NamespacedName
	e := standard(t, projectSet(t, "team", "jo", "app-hook", "group/app")).withSets(&sets)
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusAccepted, app)
	if len(sets) != 0 {
		t.Fatalf("sets woken %v", sets)
	}
}

func TestHeartbeatWakesNothing(t *testing.T) {
	var sets []types.NamespacedName
	e := standard(t, projectSet(t, "team", "jo", "app-hook", "group/app")).withSets(&sets)
	before := testutil.ToFloat64(deliveries.WithLabelValues("note", "heartbeat"))
	e.deliveries.ExpectHeartbeat(app)
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusAccepted)
	if len(sets) != 0 {
		t.Fatalf("a heartbeat woke sets %v", sets)
	}
	if _, ok := e.deliveries.Last(app); !ok {
		t.Fatal("the heartbeat was not recorded")
	}
	if got := testutil.ToFloat64(deliveries.WithLabelValues("note", "heartbeat")) - before; got != 1 {
		t.Fatalf("heartbeats counted %v", got)
	}
	// The next one is a real delivery.
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusAccepted, app)
}

// readCounter counts the reads of a request body.
type readCounter struct {
	r     io.Reader
	reads int
}

func (c *readCounter) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

func TestBodyIsNotReadWithoutAValidToken(t *testing.T) {
	for _, c := range []struct {
		name   string
		header []string // nil: no X-Gitlab-Token header
	}{
		{"missing header", nil},
		{"empty header", []string{""}},
		{"wrong token", []string{otherTok}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := standard(t)
			// Over the cap: read, it would answer 413 and not 401.
			body := &readCounter{r: strings.NewReader(payload("group/app") + strings.Repeat(" ", MaxBody))}
			req := httptest.NewRequest(http.MethodPost, Path, body)
			if c.header != nil {
				req.Header["X-Gitlab-Token"] = c.header
			}
			rec := httptest.NewRecorder()
			e.receiver.ServeHTTP(rec, req)
			e.expect(t, rec, http.StatusUnauthorized)
			if body.reads != 0 {
				t.Errorf("the body was read %d times before the token was checked", body.reads)
			}
		})
	}
}

func TestSecretWithoutTheKeyIsRefused(t *testing.T) {
	s := secret("team", "app-hook", "")
	s.Data = map[string][]byte{"other": []byte(goodToken)}
	e := newEnv(t, project("team", "app", "group/app", "app-hook"), s)
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), goodToken), http.StatusUnauthorized)
}

func TestTokenOfAnotherProjectIsRefused(t *testing.T) {
	// Each project's token wakes only that project, whatever the body names.
	e := standard(t, project("team", "other", "group/other", "other-hook"), secret("team", "other-hook", otherTok))
	e.expect(t, e.deliver(http.MethodPost, payload("group/other"), goodToken), http.StatusUnauthorized)
	e.expect(t, e.deliver(http.MethodPost, payload("group/app"), otherTok), http.StatusUnauthorized)
	if _, ok := e.deliveries.Last(app); ok {
		t.Error("a refused delivery was recorded")
	}
}

func TestTokenOfAnotherSetIsRefused(t *testing.T) {
	var sets []types.NamespacedName
	e := newEnv(t,
		projectSet(t, "team", "jo", "jo-hook", "group/one"), secret("team", "jo-hook", goodToken),
		projectSet(t, "team", "kim", "kim-hook", "group/two"), secret("team", "kim-hook", otherTok),
		// The set's secret is read from the set's namespace only.
		projectSet(t, "elsewhere", "lee", "lee-hook", "group/three"), secret("team", "lee-hook", goodToken),
	).withSets(&sets)
	e.expect(t, e.deliver(http.MethodPost, payload("group/two"), goodToken), http.StatusUnauthorized)
	e.expect(t, e.deliver(http.MethodPost, payload("group/three"), goodToken), http.StatusUnauthorized)
	if len(sets) != 0 {
		t.Fatalf("sets woken %v", sets)
	}
}

func TestReplayedDeliveryOnlyWakesAgain(t *testing.T) {
	// There is no delivery ID to deduplicate on: a replay is one more wake,
	// which the Waker coalesces, and the note's time is taken again.
	// Commands are deduplicated by GitLab's note IDs, not by deliveries.
	e := standard(t)
	body := `{"object_kind":"note","project":{"path_with_namespace":"group/app"},"object_attributes":{"noteable_type":"Issue"},"issue":{"iid":7}}`
	for range 2 {
		if rec := e.deliver(http.MethodPost, body, goodToken); rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if !slices.Equal(e.woken, []types.NamespacedName{app, app}) {
		t.Errorf("woken = %v, want app twice", e.woken)
	}
	if _, ok := e.deliveries.NoteAt(app, 7); !ok {
		t.Error("the note was not recorded")
	}
}
