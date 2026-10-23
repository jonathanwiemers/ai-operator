package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeGitLab is GitLab's OAuth endpoints, the user and the notes API.
type fakeGitLab struct {
	srv *httptest.Server

	mu       sync.Mutex
	issued   map[string]string // access token -> username
	refresh  map[string]string // refresh token -> username
	posted   []string          // "issue 42 <token>: body"
	expiries int               // expires_in of issued tokens
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	t.Helper()
	g := &fakeGitLab{issued: map[string]string{}, refresh: map[string]string{}, expiries: 7200}
	issue := func(w http.ResponseWriter, user string) {
		at, rt := fmt.Sprintf("at-%d", len(g.issued)+1), fmt.Sprintf("rt-%d", len(g.refresh)+1)
		g.issued[at], g.refresh[rt] = user, user
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": at, "refresh_token": rt, "token_type": "Bearer", "expires_in": g.expiries})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			issue(w, "ada-gl")
		case "refresh_token":
			user, ok := g.refresh[r.Form.Get("refresh_token")]
			if !ok {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			delete(g.refresh, r.Form.Get("refresh_token"))
			issue(w, user)
		}
	})
	auth := func(r *http.Request) (string, bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		u, ok := g.issued[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		return u, ok
	}
	mux.HandleFunc("GET /api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth(r)
		if !ok {
			http.Error(w, "{}", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "username": u})
	})
	notes := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth(r); !ok {
			http.Error(w, "{}", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			var b struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			g.mu.Lock()
			g.posted = append(g.posted, fmt.Sprintf("%s %s %s: %s", r.PathValue("kind"), r.PathValue("iid"), r.Header.Get("Authorization"), b.Body))
			g.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 900, "body": b.Body, "author": map[string]any{"username": "ada-gl"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "body": "please add tests", "author": map[string]any{"username": "bob"}}})
	}
	mux.HandleFunc("GET /api/v4/projects/{project}/{kind}/{iid}/notes", notes)
	mux.HandleFunc("POST /api/v4/projects/{project}/{kind}/{iid}/notes", notes)
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// newSteeringEnv is a UI whose shop project lives on a fake GitLab.
func newSteeringEnv(t *testing.T, rules ...Rule) (*testEnv, *fakeGitLab) {
	t.Helper()
	gl := newFakeGitLab(t)
	cfg := oidcConfig(rules...)
	cfg.GitLab = GitLabConfig{URL: gl.srv.URL, ClientID: "gl-client"}
	cfg.GitLabClientSecret = "gl-secret"
	env := newTestEnv(t, cfg)
	var ap aiv1.AgentProject
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Spec.GitLab.URL = gl.srv.URL
	if err := env.reader.Update(context.Background(), &ap); err != nil {
		t.Fatal(err)
	}
	return env, gl
}

// connectGitLab runs the GitLab connection the way the browser does.
func (e *testEnv) connectGitLab(t *testing.T, code string) string {
	t.Helper()
	noFollow := *e.client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Get(e.srv.URL + pathGitLabLogin + "?return=" + url.QueryEscape("/projects/team-a/shop"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	auth, _ := url.Parse(resp.Header.Get("Location"))
	q := auth.Query()
	if q.Get("scope") != "api" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != e.srv.URL+pathGitLabCallback {
		t.Fatalf("authorize URL %s", auth)
	}
	resp, err = noFollow.Get(e.srv.URL + pathGitLabCallback + "?code=" + code + "&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

func (e *testEnv) postJSON(t *testing.T, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestSteering(t *testing.T) {
	env, gl := newSteeringEnv(t,
		Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true},
	)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
	env.login(t, "/")

	var st GitLabStatusView
	env.get(t, "/api/v1/gitlab", &st)
	if !st.Enabled || st.Connected {
		t.Fatalf("before connecting: %+v", st)
	}
	if code := env.get(t, shopTask+"/thread", nil); code != http.StatusConflict {
		t.Fatalf("thread without a connection: %d", code)
	}
	if loc := env.connectGitLab(t, "bad-code"); !strings.Contains(loc, "gitlab_error=token") {
		t.Fatalf("a failed exchange goes back with an error: %s", loc)
	}
	if loc := env.connectGitLab(t, "good-code"); loc != "/projects/team-a/shop" {
		t.Fatalf("back to the page: %s", loc)
	}
	env.get(t, "/api/v1/gitlab", &st)
	if !st.Connected || st.Username != "ada-gl" {
		t.Fatalf("connected: %+v", st)
	}
	var task struct{ Permissions map[string]bool }
	env.get(t, shopTask, &task)
	if !task.Permissions["gitlab"] || !task.Permissions["steer"] {
		t.Fatalf("permissions %+v", task.Permissions)
	}

	var thread struct{ Parts []ThreadPart }
	if code := env.get(t, shopTask+"/thread", &thread); code != http.StatusOK || len(thread.Parts) != 1 ||
		thread.Parts[0].Target != "issue" || thread.Parts[0].IID != 42 || len(thread.Parts[0].Notes) != 1 {
		t.Fatalf("thread %d %+v", code, thread)
	}

	code, out := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": " /agent use claude opus\nand add tests "})
	if code != http.StatusOK || out["author"] != "ada-gl" {
		t.Fatalf("post: %d %v", code, out)
	}
	if len(gl.posted) != 1 || gl.posted[0] != "issues 42 Bearer at-1: /agent use claude opus\nand add tests" {
		t.Fatalf("posted with the user's token: %q", gl.posted)
	}
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "mr", "body": "x"}); code != http.StatusBadRequest {
		t.Fatalf("a target the task does not have: %d", code)
	}
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": strings.Repeat("x", maxNoteBody+1)}); code != http.StatusBadRequest {
		t.Fatalf("too long: %d", code)
	}
	for i := 0; i < notesPerWindow-1; i++ {
		env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "x"})
	}
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "x"}); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", code)
	}

	// The access token expires: it is refreshed with the refresh token.
	env.server.sessions.now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if code := env.get(t, shopTask+"/thread", &thread); code != http.StatusOK {
		t.Fatalf("after expiry: %d", code)
	}
	env.server.sessions.now = time.Now

	// Disconnect.
	if code, _ := env.postJSON(t, pathGitLabLogout, map[string]string{}); code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	env.get(t, "/api/v1/gitlab", &st)
	if st.Connected {
		t.Fatal("disconnected")
	}
}

func TestSteeringNeedsTheGrant(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	var task struct{ Permissions map[string]bool }
	env.get(t, shopTask, &task)
	if task.Permissions["steer"] || !task.Permissions["gitlab"] {
		t.Fatalf("permissions %+v", task.Permissions)
	}
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "x"}); code != http.StatusForbidden {
		t.Fatalf("without steer: %d", code)
	}
	if code, _ := env.postJSON(t, "/api/v1/projects/team-b/billing/tasks/billing-7-invoice/notes", map[string]string{"target": "issue", "body": "x"}); code != http.StatusNotFound {
		t.Fatalf("a project the user cannot see: %d", code)
	}
	if len(gl.posted) != 0 {
		t.Fatalf("nothing may be posted: %q", gl.posted)
	}
	// The thread needs no grant beyond seeing the project.
	if code := env.get(t, shopTask+"/thread", nil); code != http.StatusOK {
		t.Fatalf("thread: %d", code)
	}
}

func TestGitLabTokenBelongsToItsUser(t *testing.T) {
	env, _ := newSteeringEnv(t, Rule{Roles: []string{"*"}, Steer: true})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	// Another user logs in in the same browser.
	env.idp.user = map[string]any{"sub": "u2", "email": "bob@example.com"}
	env.login(t, "/")
	var st GitLabStatusView
	env.get(t, "/api/v1/gitlab", &st)
	if st.Connected {
		t.Fatal("bob must not use ada's GitLab token")
	}
}
