package ui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr/funcr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const closeIssue42 = shopMilestone + "/issues/42/close"

// withHoldingIssue makes milestone 3 a done autopilot milestone held open by
// issues 42 and 44, with ada@example.com as its owner subject, and gives the
// users' GitLab those issues.
func withHoldingIssue(t *testing.T, env *testEnv, phase aiv1.AutopilotPhase) *gitlabfake.Fake {
	t.Helper()
	fake := followUpGitLab(env)
	fake.AddIssue(42, "Fix the cart")
	fake.AddIssue(43, "Add Klarna")
	fake.AddIssue(44, "Docs")
	var st aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Autopilot, st.Spec.OwnerSubjects = true, []string{"ada@example.com"}
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: phase, CloseWaitsFor: []int64{42, 44}}
	if err := env.reader.Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
	return fake
}

// untouched fails when the fake GitLab got a note or an issue update.
func untouched(t *testing.T, fake *gitlabfake.Fake) {
	t.Helper()
	if len(fake.IssueUpdates) != 0 || len(fake.IssueNotes[42]) != 0 || len(fake.IssueNotes[43]) != 0 {
		t.Errorf("GitLab was written: updates %+v, notes %+v", fake.IssueUpdates, fake.IssueNotes)
	}
}

func TestCloseHoldingIssue(t *testing.T) {
	env, _ := steerer(t)
	var logMu sync.Mutex
	var logs []string
	env.server.log = funcr.New(func(prefix, args string) {
		logMu.Lock()
		defer logMu.Unlock()
		logs = append(logs, prefix+" "+args)
	}, funcr.Options{Verbosity: 9})
	fake := withHoldingIssue(t, env, aiv1.AutopilotDone)

	code, out := env.postJSON(t, closeIssue42, nil)
	if code != http.StatusOK || out["iid"] != float64(42) || out["webURL"] != env.server.cfg.GitLab.URL+"/acme/shop/-/issues/42" {
		t.Fatalf("close: %d %v", code, out)
	}
	notes := fake.IssueNotes[42]
	if len(notes) != 1 || notes[0].Body != "Closed from the Desk by @ada-gl to finish %3." {
		t.Errorf("notes %+v", notes)
	}
	if len(fake.IssueUpdates) != 1 || fake.IssueUpdates[0].StateEvent != "close" || fake.Issues[42].State != "closed" {
		t.Errorf("updates %+v, issue %+v", fake.IssueUpdates, fake.Issues[42])
	}
	if fake.Issues[44].State == "closed" {
		t.Error("only the named issue is closed")
	}
	logMu.Lock()
	defer logMu.Unlock()
	if !slices.ContainsFunc(logs, func(l string) bool {
		return strings.Contains(l, `"action"="issue.close"`) && strings.Contains(l, `"user"="ada@example.com"`) &&
			strings.Contains(l, `"gitlabUser"="ada-gl"`) && strings.Contains(l, `"milestone"=3`) && strings.Contains(l, `"issue"=42`)
	}) {
		t.Errorf("the audit line: %q", logs)
	}
}

func TestCloseHoldingIssueRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase aiv1.AutopilotPhase
		path  string
		edit  func(t *testing.T, env *testEnv)
		want  int
	}{
		{"not holding", aiv1.AutopilotDone, shopMilestone + "/issues/43/close", nil, http.StatusConflict},
		{"not done", aiv1.AutopilotRunning, closeIssue42, nil, http.StatusConflict},
		{"not an owner", aiv1.AutopilotDone, closeIssue42, func(t *testing.T, env *testEnv) { setOwners(t, env, "bea") }, http.StatusForbidden},
		{"no milestone", aiv1.AutopilotDone, "/api/v1/projects/team-a/shop/milestones/4/issues/42/close", nil, http.StatusNotFound},
		{"no issue number", aiv1.AutopilotDone, shopMilestone + "/issues/x/close", nil, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := steerer(t)
			fake := withHoldingIssue(t, env, tc.phase)
			if tc.edit != nil {
				tc.edit(t, env)
			}
			if code, out := env.postJSON(t, tc.path, nil); code != tc.want {
				t.Errorf("status %d %v, want %d", code, out, tc.want)
			}
			untouched(t, fake)
		})
	}
	t.Run("no autopilot", func(t *testing.T) {
		env, _ := steerer(t)
		fake := followUpGitLab(env)
		fake.AddIssue(42, "Fix the cart")
		if code, _ := env.postJSON(t, closeIssue42, nil); code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
		untouched(t, fake)
	})
	t.Run("no GitLab token", func(t *testing.T) {
		env, _ := newSteeringEnv(t, Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true})
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
		env.login(t, "/")
		fake := withHoldingIssue(t, env, aiv1.AutopilotDone)
		if code, out := env.postJSON(t, closeIssue42, nil); code != http.StatusConflict || out["login"] != pathGitLabLogin {
			t.Errorf("status %d %v, want 409 asking to connect", code, out)
		}
		untouched(t, fake)
	})
	for name, rule := range map[string]Rule{
		"view only":   {Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		"not visible": {Roles: []string{"devs"}, Projects: []string{"team-b/*"}, Steer: true},
	} {
		t.Run(name, func(t *testing.T) {
			env, _ := newSteeringEnv(t, rule)
			env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
			env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
			env.login(t, "/")
			env.connectGitLab(t, "good-code")
			fake := withHoldingIssue(t, env, aiv1.AutopilotDone)
			want := http.StatusForbidden
			if name == "not visible" {
				want = http.StatusNotFound
			}
			if code, _ := env.postJSON(t, closeIssue42, nil); code != want {
				t.Errorf("status %d, want %d", code, want)
			}
			untouched(t, fake)
		})
	}
}

func TestCloseHoldingIssueGitLabRefuses(t *testing.T) {
	env, _ := steerer(t)
	fake := withHoldingIssue(t, env, aiv1.AutopilotDone)
	for _, c := range []struct {
		name         string
		note, update int
		want         int
	}{
		{"note forbidden", http.StatusForbidden, 0, http.StatusForbidden},
		{"issue gone", http.StatusNotFound, 0, http.StatusForbidden},
		{"close fails", 0, http.StatusInternalServerError, http.StatusBadGateway},
		{"token rejected", http.StatusUnauthorized, 0, http.StatusConflict},
	} {
		fake.NoteErrors, fake.UpdateErrors = map[int64]error{}, map[int64]error{}
		if c.note != 0 {
			fake.NoteErrors[42] = &gitlab.Error{StatusCode: c.note, Method: "POST", Path: "notes"}
		}
		if c.update != 0 {
			fake.UpdateErrors[42] = &gitlab.Error{StatusCode: c.update, Method: "PUT", Path: "issue"}
		}
		code, out := env.postJSON(t, closeIssue42, nil)
		if code != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, code, out, c.want)
		}
		if c.note == http.StatusUnauthorized && out["login"] != pathGitLabLogin {
			t.Errorf("a rejected token asks to connect again: %v", out)
		}
	}
	// The note goes first: a failed close left one note and no update.
	if len(fake.IssueNotes[42]) != 1 || len(fake.IssueUpdates) != 0 {
		t.Errorf("notes %+v, updates %+v", fake.IssueNotes[42], fake.IssueUpdates)
	}
	// The connection was dropped.
	fake.NoteErrors = nil
	if code, out := env.postJSON(t, closeIssue42, nil); code != http.StatusConflict || out["login"] != pathGitLabLogin {
		t.Errorf("after a 401: %d %v", code, out)
	}
}

func TestCloseHoldingIssueRateLimit(t *testing.T) {
	env, _ := steerer(t)
	fake := withHoldingIssue(t, env, aiv1.AutopilotDone)
	for i := range 10 {
		if code, out := env.postJSON(t, closeIssue42, nil); code != http.StatusOK {
			t.Fatalf("close %d: %d %v", i+1, code, out)
		}
	}
	if code, _ := env.postJSON(t, shopMilestone+"/issues/44/close", nil); code != http.StatusTooManyRequests {
		t.Errorf("the eleventh close: %d, want 429", code)
	}
	if fake.Issues[44].State == "closed" || len(fake.IssueNotes[44]) != 0 {
		t.Errorf("a refused close wrote: %+v %+v", fake.Issues[44], fake.IssueNotes[44])
	}
}

func TestCloseHoldingIssueWithTheFixedToken(t *testing.T) {
	gl := newFakeGitLab(t)
	cfg := oidcConfig(Rule{Roles: []string{"*"}, Steer: true})
	cfg.GitLab = GitLabConfig{URL: gl.srv.URL, TokenUser: "ada@example.com"}
	cfg.GitLabToken = "fine-grained-pat"
	env := newTestEnv(t, cfg)
	var ap aiv1.AgentProject
	_ = env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap)
	ap.Spec.GitLab.URL = gl.srv.URL
	_ = env.reader.Update(context.Background(), &ap)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")
	fake := withHoldingIssue(t, env, aiv1.AutopilotDone)
	env.server.gitlab.newFixed = env.server.gitlab.newClient
	if code, out := env.postJSON(t, closeIssue42, nil); code != http.StatusOK {
		t.Fatalf("close: %d %v", code, out)
	}
	// Every note of the shared token has one author: the note names the login.
	if notes := fake.IssueNotes[42]; len(notes) != 1 || notes[0].Body != "Closed from the Desk by ada@example.com to finish %3." {
		t.Errorf("notes %+v", notes)
	}
}
