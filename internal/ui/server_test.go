package ui

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeIdP is a minimal OpenID provider: discovery, keys, token, userinfo.
type fakeIdP struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	user map[string]any // claims of the ID token
	info map[string]any // extra claims of the userinfo response

	mu    sync.Mutex
	codes map[string]pendingCode
}

type pendingCode struct {
	nonce, challenge string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key, codes: map[string]pendingCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss := idp.srv.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss,
			"authorization_endpoint":                iss + "/authorize",
			"token_endpoint":                        iss + "/token",
			"jwks_uri":                              iss + "/keys",
			"userinfo_endpoint":                     iss + "/userinfo",
			"end_session_endpoint":                  iss + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.mu.Lock()
		pc, ok := idp.codes[r.Form.Get("code")]
		delete(idp.codes, r.Form.Get("code"))
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims := map[string]any{
			"iss": idp.srv.URL, "aud": "ui-client", "nonce": pc.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		for k, v := range idp.user {
			claims[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idp.sign(t, claims),
		})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		out := map[string]any{"sub": idp.user["sub"]}
		for k, v := range idp.info {
			out[k] = v
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// authorize plays the provider's login page: it accepts the authorization
// request and returns the redirect back to the app.
func (idp *fakeIdP) authorize(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, idp.srv.URL+"/authorize") {
		t.Fatalf("not an authorization URL: %s", authURL)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "ui-client" {
		t.Fatalf("authorization request: %v", q)
	}
	code := rand.Text()
	idp.mu.Lock()
	idp.codes[code] = pendingCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	idp.mu.Unlock()
	return q.Get("redirect_uri") + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
}

func testObjects() []client.Object {
	project := func(ns, name string) *aiv1.AgentProject {
		return &aiv1.AgentProject{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec:       aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/" + name}},
		}
	}
	return []client.Object{
		project("team-a", "shop"),
		project("team-b", "billing"),
		&aiv1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-spec-m3-checkout"},
			Spec: aiv1.AgentTaskSpec{
				ProjectRef: "shop", Type: aiv1.TaskTypeSpec, RunRequest: 1,
				Milestone: &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "Checkout"},
			},
			Status: aiv1.AgentTaskStatus{
				Run:  aiv1.RunStatus{ObservedRunRequest: 1, LastResult: aiv1.RunResultSucceeded, CostUSD: "1.25"},
				Plan: []aiv1.PlannedIssue{{Key: "a", IID: 42, Title: "Add PayPal"}},
			},
		},
		&aiv1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-42-add-paypal"},
			Spec: aiv1.AgentTaskSpec{
				ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: 2,
				Milestone: &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "Checkout"},
				Issue:     &aiv1.IssueRef{IID: 42, Title: "Add PayPal"},
			},
			Status: aiv1.AgentTaskStatus{
				Phase: aiv1.TaskPhaseRunning,
				Run:   aiv1.RunStatus{ObservedRunRequest: 1, CurrentStep: "10 run implementer", CostUSD: "0.50"},
			},
		},
		&aiv1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-b", Name: "billing-7-invoice"},
			Spec:       aiv1.AgentTaskSpec{ProjectRef: "billing", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 7}},
		},
	}
}

type testEnv struct {
	idp    *fakeIdP
	srv    *httptest.Server
	hub    *Hub
	client *http.Client
}

func newTestEnv(t *testing.T, cfg *Config) *testEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aiv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(testObjects()...).Build()
	env := &testEnv{hub: NewHub()}
	if cfg.Auth.Mode != AuthNone {
		env.idp = newFakeIdP(t)
		cfg.OIDC.IssuerURL = env.idp.srv.URL
	}
	static := fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><div id=app></div>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	var handler http.Handler
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(env.srv.Close)
	if cfg.ExternalURL == "" {
		cfg.ExternalURL = env.srv.URL
	}
	cfg.Default()
	if err := cfg.Validate("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg, reader, env.hub, static, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	handler = s.Handler()
	jar, _ := cookiejar.New(nil)
	env.client = &http.Client{
		Jar: jar,
		// Follow redirects within the app, stop at the provider.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if env.idp != nil && strings.HasPrefix(req.URL.String(), env.idp.srv.URL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return env
}

func oidcConfig(rules ...Rule) *Config {
	return &Config{
		OIDC: OIDCConfig{
			ClientID: "ui-client",
			Claims:   Claims{Roles: `claims["urn:zitadel:iam:org:project:roles"]`},
		},
		Authorization: AuthorizationConfig{Rules: rules},
		SessionKey:    strings.Repeat("s", 32),
	}
}

func (e *testEnv) get(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

// login runs the whole login flow and returns where the app sent the
// browser at the end.
func (e *testEnv) login(t *testing.T, returnTo string) string {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + pathLogin + "?return=" + url.QueryEscape(returnTo))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login: status %d", resp.StatusCode)
	}
	callback := e.idp.authorize(t, resp.Header.Get("Location"))
	resp, err = e.client.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Request.URL.RequestURI()
}

func TestLoginAndAccess(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"shop-devs"}, Projects: []string{"team-a/*"}}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	// Zitadel returns the roles from userinfo unless told otherwise.
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-devs": map[string]any{"1": "acme"}}}

	if code := env.get(t, "/api/v1/projects", nil); code != http.StatusUnauthorized {
		t.Fatalf("before login: status %d", code)
	}
	if got := env.login(t, "/projects/team-a/shop"); got != "/projects/team-a/shop" {
		t.Errorf("after login the browser is at %q", got)
	}

	var me struct {
		Username string   `json:"username"`
		Roles    []string `json:"roles"`
	}
	env.get(t, "/api/v1/me", &me)
	if me.Username != "ada@example.com" || len(me.Roles) != 1 || me.Roles[0] != "shop-devs" {
		t.Errorf("me = %+v", me)
	}

	var projects []ProjectSummary
	env.get(t, "/api/v1/projects", &projects)
	if len(projects) != 1 || projects[0].Name != "shop" {
		t.Fatalf("projects = %+v", projects)
	}
	p := projects[0]
	if p.Tasks != 2 || p.CostUSD != "1.75" || p.Stages[StageWorking] != 1 || p.Stages[StagePlanned] != 1 {
		t.Errorf("summary = %+v", p)
	}
	if p.GitLab.WebURL != "https://gitlab.example.com/acme/shop" {
		t.Errorf("gitlab = %+v", p.GitLab)
	}

	var d ProjectDetail
	if code := env.get(t, "/api/v1/projects/team-a/shop", &d); code != http.StatusOK {
		t.Fatalf("project: status %d", code)
	}
	if len(d.Milestones) != 1 || d.Milestones[0].Spec == nil || len(d.Milestones[0].Tasks) != 1 {
		t.Fatalf("milestones = %+v", d.Milestones)
	}
	if d.Milestones[0].WebURL != "https://gitlab.example.com/acme/shop/-/milestones/3" {
		t.Errorf("milestone URL = %s", d.Milestones[0].WebURL)
	}

	var task struct {
		Task Task `json:"task"`
	}
	if code := env.get(t, "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal", &task); code != http.StatusOK {
		t.Fatalf("task: status %d", code)
	}
	if task.Task.Stage != StageWorking || task.Task.Status.Run.CurrentStep != "10 run implementer" {
		t.Errorf("task = %+v", task.Task)
	}

	// Other projects do not exist for this user.
	for _, path := range []string{
		"/api/v1/projects/team-b/billing",
		"/api/v1/projects/team-b/billing/tasks/billing-7-invoice",
		"/api/v1/projects/team-a/shop/tasks/billing-7-invoice",
		"/api/v1/projects/team-a/nope",
	} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
}

func TestLoginWithoutAccess(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"admin"}}))
	env.idp.user = map[string]any{"sub": "u2", "email": "bob@example.com"}
	env.login(t, "/")
	var projects []ProjectSummary
	if code := env.get(t, "/api/v1/projects", &projects); code != http.StatusOK || len(projects) != 0 {
		t.Fatalf("status %d, projects %+v", code, projects)
	}
}

func TestLoginFailures(t *testing.T) {
	env := newTestEnv(t, oidcConfig())
	env.idp.user = map[string]any{"sub": "u1"} // no email: the default username falls back to sub

	// A callback without a login started here.
	resp, err := env.client.Get(env.srv.URL + pathCallback + "?code=x&state=y")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.RawQuery, "login_error=") {
		t.Errorf("callback without login ended at %s", resp.Request.URL)
	}

	// A wrong state.
	resp, err = env.client.Get(env.srv.URL + pathLogin)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cb, _ := url.Parse(env.idp.authorize(t, resp.Header.Get("Location")))
	q := cb.Query()
	q.Set("state", "forged")
	cb.RawQuery = q.Encode()
	resp, err = env.client.Get(cb.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.RawQuery, "login_error=") {
		t.Errorf("forged state ended at %s", resp.Request.URL)
	}
	if code := env.get(t, "/api/v1/me", nil); code != http.StatusUnauthorized {
		t.Errorf("a failed login left a session: status %d", code)
	}
}

func TestLogout(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"*"}}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")

	// Another site cannot log the user out.
	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+pathLogout, nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site logout: status %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, env.srv.URL+pathLogout, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err = env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Redirect string `json:"redirect"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if !strings.HasPrefix(out.Redirect, env.idp.srv.URL+"/logout?") || !strings.Contains(out.Redirect, "client_id=ui-client") {
		t.Errorf("logout redirect = %q", out.Redirect)
	}
	if code := env.get(t, "/api/v1/me", nil); code != http.StatusUnauthorized {
		t.Errorf("after logout: status %d", code)
	}
}

func TestEvents(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"*"}, Projects: []string{"team-a/*"}}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.srv.URL+"/api/v1/events", nil)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewReader(resp.Body)
	readUntil := func(prefix string) string {
		for {
			l, err := lines.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			if strings.HasPrefix(l, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(l, prefix))
			}
		}
	}
	readUntil(": connected")

	// The subscription is registered before "connected" is written, so
	// both events reach the hub's filter; only the visible one comes out.
	env.hub.Publish(Event{Kind: "task", Namespace: "team-b", Name: "billing-7-invoice", Project: "billing"})
	env.hub.Publish(Event{Kind: "task", Namespace: "team-a", Name: "shop-42-add-paypal", Project: "shop"})
	var e Event
	if err := json.Unmarshal([]byte(readUntil("data: ")), &e); err != nil {
		t.Fatal(err)
	}
	if e.Namespace != "team-a" || e.Name != "shop-42-add-paypal" {
		t.Errorf("event = %+v", e)
	}
}

func TestAuthNone(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	var projects []ProjectSummary
	if code := env.get(t, "/api/v1/projects", &projects); code != http.StatusOK || len(projects) != 2 {
		t.Fatalf("status %d, projects %d", code, len(projects))
	}
	if code := env.get(t, pathLogin, nil); code != http.StatusNotFound {
		t.Errorf("login in auth mode none: status %d", code)
	}
}

func TestApp(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	for path, want := range map[string]string{
		"/":                     "<!doctype html>",
		"/projects/team-a/shop": "<!doctype html>",
		"/assets/app-1.js":      "console.log(1)",
	} {
		resp, err := env.client.Get(env.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), want) {
			t.Errorf("%s: status %d body %q", path, resp.StatusCode, body)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s: CSP %q", path, csp)
		}
	}
	for _, path := range []string{"/assets/missing.js", "/api/v1/nope"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, code)
		}
	}
}
