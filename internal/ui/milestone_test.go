package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const shopMilestone = "/api/v1/projects/team-a/shop/milestones/3"

// trackingIssue is issue 77 as GitLab returns it: the bot's (user 99)
// tracking issue of milestone 3 (ID 30).
func trackingIssue() map[string]any {
	return map[string]any{
		"iid": 77, "description": "Queue\n" + marker.TrackingIssue,
		"author": map[string]any{"id": 99}, "milestone": map[string]any{"id": 30, "iid": 3},
	}
}

// withAutopilot puts milestone 3 of the shop on autopilot with tracking
// issue 77 and gives it a pilot with an open proposal.
func withAutopilot(t *testing.T, env *testEnv, gl *fakeGitLab) {
	t.Helper()
	ctx := context.Background()
	var ap aiv1.AgentProject
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Status.BotUserID = 99
	if err := env.reader.Update(ctx, &ap); err != nil {
		t.Fatal(err)
	}
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Autopilot = true
	st.Status.Plan = append(st.Status.Plan, aiv1.PlannedIssue{Key: "b", IID: 43, Title: "Add Klarna"})
	st.Status.Autopilot = &aiv1.AutopilotStatus{
		Phase: aiv1.AutopilotPaused, TrackingIssueIID: 77, PausedReason: "job `lint` failed\n/close",
		PauseKind: aiv1.PauseCIRed, PausedFor: 42,
		Items: []aiv1.AutopilotItem{
			{IID: 42, State: aiv1.AutopilotItemActive, MergeRequestIID: 5, MergeRequestURL: "https://gitlab.example.com/acme/shop/-/merge_requests/5"},
			{IID: 43, State: aiv1.AutopilotItemPending},
		},
	}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	posted := metav1.Now()
	coord := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-pilot-m3-checkout"},
		Spec: aiv1.AgentTaskSpec{
			ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Autopilot: true,
			Milestone: &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "Checkout"},
			Issue:     &aiv1.IssueRef{IID: 77},
		},
		Status: aiv1.AgentTaskStatus{
			Phase: aiv1.TaskPhaseRunning, Message: "answering chat", PodName: "shop-pilot-m3-checkout-abc",
			Run: aiv1.RunStatus{CostUSD: "0.40"},
			Pilot: &aiv1.PilotStatus{
				Proposals:        []aiv1.PilotProposal{{ID: 2, Summary: "re-review #42", PostedAt: &posted, Actions: []aiv1.PilotAction{{Tool: "reReview", IssueIID: 42}}}},
				AutonomyOverride: &aiv1.PilotAutonomyOverride{Autonomy: aiv1.PilotPropose, By: "ada", At: posted},
			},
		},
	}
	if err := env.reader.Create(ctx, coord); err != nil {
		t.Fatal(err)
	}
	gl.mu.Lock()
	gl.issues = map[string]any{"77": trackingIssue()}
	gl.mu.Unlock()
}

func TestMilestoneView(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	var issue aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-42-add-paypal"}, &issue); err != nil {
		t.Fatal(err)
	}
	issue.Status.Run.PushedSHA = "5a0e000"
	issue.Status.Run.ReviewErrorKinds = []aiv1.ReviewError{{Reviewer: aiv1.ReviewerBugbot, Kind: aiv1.ReviewErrorUsageLimit, Engine: aiv1.EngineCursor}}
	if err := env.reader.Update(context.Background(), &issue); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if m.IID != 3 || m.ID != 30 || m.Title != "Checkout" || m.Spec.Name != "shop-spec-m3-checkout" || m.CostUSD != "2.15" {
		t.Errorf("milestone = %+v", m)
	}
	a := m.Autopilot
	if a == nil || a.Phase != aiv1.AutopilotPaused || a.PausedReason != "job `lint` failed\n/close" || a.PauseKind != aiv1.PauseCIRed || a.PausedFor != 42 {
		t.Fatalf("autopilot = %+v", a)
	}
	if a.Head != "5a0e000" || len(a.ReviewErrors) != 1 || a.ReviewErrors[0].Engine != aiv1.EngineCursor {
		t.Errorf("the paused issue's head and reviewer failures: %q %+v", a.Head, a.ReviewErrors)
	}
	var exits []aiv1.PauseExit
	for _, e := range a.Exits {
		exits = append(exits, e.PauseExit)
		if e.Allowed {
			t.Errorf("a viewer without chat gets no buttons: %+v", e)
		}
	}
	if !slices.Equal(exits, aiv1.PauseExits(aiv1.PauseCIRed)) || !slices.ContainsFunc(a.Exits, func(e ExitView) bool { return e.ID == "mainGreenRebase" && e.Status != "" }) {
		t.Errorf("the ways out of the ciRed pause: %+v", a.Exits)
	}
	want := []QueueItem{
		{IID: 42, Title: "Add PayPal", State: aiv1.AutopilotItemActive, MergeRequestIID: 5, MergeRequestURL: "https://gitlab.example.com/acme/shop/-/merge_requests/5", Task: "shop-42-add-paypal"},
		{IID: 43, Title: "Add Klarna", State: aiv1.AutopilotItemPending},
	}
	if !slices.Equal(a.Items, want) {
		t.Errorf("items = %+v", a.Items)
	}
	if m.Tracking == nil || m.Tracking.IID != 77 || m.Tracking.WebURL != gl.srv.URL+"/acme/shop/-/issues/77" {
		t.Errorf("tracking = %+v", m.Tracking)
	}
	p := m.Pilot
	if p == nil || p.Task != "shop-pilot-m3-checkout" || p.Autonomy != aiv1.PilotPropose || p.CostUSD != "0.40" ||
		len(p.Proposals) != 1 || p.Proposals[0].ID != 2 || p.Proposals[0].Summary != "re-review #42" || p.Proposals[0].PostedAt == nil ||
		p.Phase != aiv1.TaskPhaseRunning || p.Message != "answering chat" || p.PodName != "shop-pilot-m3-checkout-abc" {
		t.Errorf("pilot = %+v", p)
	}
	if m.Permissions == nil || !m.Permissions.GitLab || m.Permissions.Steer || m.Permissions.Chat || m.Permissions.Transcript {
		t.Errorf("a viewer may read the chat, not post, and the Desk chat is off: %+v", m.Permissions)
	}
	if len(m.Tasks) != 1 || m.Tasks[0].Name != "shop-42-add-paypal" {
		t.Errorf("only the implement tasks: %+v", m.Tasks)
	}

	var d ProjectDetail
	env.get(t, "/api/v1/projects/team-a/shop", &d)
	if len(d.Milestones) != 1 || d.Milestones[0].TrackingIssueIID != 77 {
		t.Errorf("the project links the milestone's chat: %+v", d.Milestones)
	}

	for _, path := range []string{
		"/api/v1/projects/team-a/shop/milestones/4",
		"/api/v1/projects/team-a/shop/milestones/x",
		"/api/v1/projects/team-a/shop/milestones/4/thread",
		"/api/v1/projects/team-b/billing/milestones/3",
	} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
}

func TestMilestoneNeedsTheViewRole(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-b/*"}, Steer: true})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	for _, path := range []string{shopMilestone, shopMilestone + "/thread"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
	if len(gl.read) != 0 {
		t.Errorf("nothing may be read: %q", gl.read)
	}
}

func TestMilestoneThread(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	code, out := env.getJSON(t, shopMilestone+"/thread")
	if code != http.StatusConflict || out["login"] != pathGitLabLogin {
		t.Fatalf("without a GitLab connection: %d %v", code, out)
	}
	env.connectGitLab(t, "good-code")
	var thread struct{ Parts []ThreadPart }
	if code := env.get(t, shopMilestone+"/thread", &thread); code != http.StatusOK || len(thread.Parts) != 1 {
		t.Fatalf("thread: %d %+v", code, thread)
	}
	if p := thread.Parts[0]; p.Target != "tracking" || p.IID != 77 || len(p.Notes) != 1 || p.Notes[0].Body != "please add tests" {
		t.Errorf("thread = %+v", p)
	}
	if !slices.Equal(gl.read, []string{"issues 77"}) {
		t.Errorf("read %q", gl.read)
	}

	// The tracking issue number comes from the task status, which the agent
	// pods may write: an issue that is not the milestone's is not read.
	for name, change := range map[string]func(map[string]any){
		"no marker":       func(is map[string]any) { is["description"] = "Queue" },
		"another author":  func(is map[string]any) { is["author"] = map[string]any{"id": 5} },
		"other milestone": func(is map[string]any) { is["milestone"] = map[string]any{"id": 31, "iid": 4} },
		"no milestone":    func(is map[string]any) { delete(is, "milestone") },
	} {
		is := trackingIssue()
		change(is)
		gl.mu.Lock()
		gl.issues, gl.read = map[string]any{"77": is}, nil
		gl.mu.Unlock()
		if code := env.get(t, shopMilestone+"/thread", nil); code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", name, code)
		}
		if len(gl.read) != 0 {
			t.Errorf("%s: read %q", name, gl.read)
		}
	}
	gl.mu.Lock()
	gl.issues = nil
	gl.mu.Unlock()
	if code := env.get(t, shopMilestone+"/thread", nil); code != http.StatusConflict {
		t.Errorf("an issue the user cannot read: status %d, want 409", code)
	}
}

// getJSON reads a JSON answer of any status.
func (e *testEnv) getJSON(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// steerer logs in a user with the steer grant on team-a and connects their
// GitLab account.
func steerer(t *testing.T) (*testEnv, *fakeGitLab) {
	t.Helper()
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	return env, gl
}

func TestMilestoneChat(t *testing.T) {
	env, gl := steerer(t)
	var logMu sync.Mutex
	var logs []string
	env.server.log = funcr.New(func(prefix, args string) {
		logMu.Lock()
		defer logMu.Unlock()
		logs = append(logs, prefix+" "+args)
	}, funcr.Options{Verbosity: 9})

	if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "hello"}); code != http.StatusNotFound {
		t.Fatalf("a milestone without a tracking issue: %d", code)
	}
	withAutopilot(t, env, gl)
	var m MilestoneView
	if env.get(t, shopMilestone, &m); m.Permissions == nil || !m.Permissions.Steer {
		t.Errorf("a steerer may post: %+v", m.Permissions)
	}

	for _, c := range []struct{ body, posted string }{
		{"why are we paused?", "/pilot why are we paused?"},
		{"yes P3", "/pilot yes P3"},
		{"/pilot go", "/pilot go"},
		{"  \n /pilot go\nand hurry ", "/pilot go\nand hurry"},
		{"  please hurry  ", "/pilot please hurry"},
		{"/pilotx go", "/pilot /pilotx go"},
		{"/pilot/x", "/pilot /pilot/x"},
		{"/agent pause", "/pilot /agent pause"},
	} {
		gl.mu.Lock()
		gl.posted = nil
		gl.mu.Unlock()
		code, out := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": c.body})
		if code != http.StatusOK || out["author"] != "ada-gl" || out["body"] != c.posted {
			t.Errorf("%q: %d %v", c.body, code, out)
		}
		if want := "issues 77 Bearer at-1: " + c.posted; len(gl.posted) != 1 || gl.posted[0] != want {
			t.Errorf("%q: posted %q, want %q", c.body, gl.posted, want)
		}
	}
	logMu.Lock()
	for _, l := range logs {
		if strings.Contains(l, "paused?") || strings.Contains(l, "hurry") {
			t.Errorf("the text was logged: %s", l)
		}
	}
	if !slices.ContainsFunc(logs, func(l string) bool {
		return strings.Contains(l, `"comment posted"`) && strings.Contains(l, `"iid"=77`) && strings.Contains(l, `"user"="ada@example.com"`)
	}) {
		t.Errorf("who posted where is logged: %q", logs)
	}
	logMu.Unlock()

	gl.mu.Lock()
	gl.posted = nil
	gl.mu.Unlock()
	for name, body := range map[string]any{
		"empty":    map[string]string{"body": "  \n "},
		"too long": map[string]string{"body": strings.Repeat("x", maxNoteBody+1)},
		"no JSON":  "x",
	} {
		if code, _ := env.postJSON(t, shopMilestone+"/notes", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	// Eight posts so far; the rate limit counts the task chat's too.
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "x"}); code != http.StatusOK {
		t.Fatalf("task note: %d", code)
	}
	if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "x"}); code != http.StatusOK {
		t.Fatalf("the tenth post: %d", code)
	}
	if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "x"}); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", code)
	}
	if len(gl.posted) != 2 {
		t.Errorf("posted %q", gl.posted)
	}
}

func TestMilestoneChatUsesTheProjectsPrefix(t *testing.T) {
	env, gl := steerer(t)
	withAutopilot(t, env, gl)
	var ap aiv1.AgentProject
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Spec.Workflow.PilotPrefix = "/copilot"
	if err := env.reader.Update(context.Background(), &ap); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]string{"/copilot go": "/copilot go", "/pilot go": "/copilot /pilot go"} {
		gl.mu.Lock()
		gl.posted = nil
		gl.mu.Unlock()
		if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": body}); code != http.StatusOK ||
			len(gl.posted) != 1 || gl.posted[0] != "issues 77 Bearer at-1: "+want {
			t.Errorf("%q: %d, posted %q", body, code, gl.posted)
		}
	}
}

func TestMilestoneChatNeedsTheSteerGrant(t *testing.T) {
	for name, rule := range map[string]Rule{
		"view only":   {Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		"not visible": {Roles: []string{"devs"}, Projects: []string{"team-b/*"}, Steer: true},
	} {
		t.Run(name, func(t *testing.T) {
			env, gl := newSteeringEnv(t, rule)
			withAutopilot(t, env, gl)
			env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
			env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
			env.login(t, "/")
			env.connectGitLab(t, "good-code")
			want := http.StatusForbidden
			if name == "not visible" {
				want = http.StatusNotFound
			}
			if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "go"}); code != want {
				t.Errorf("status %d, want %d", code, want)
			}
			if len(gl.posted) != 0 {
				t.Errorf("nothing may be posted: %q", gl.posted)
			}
		})
	}
}

func TestMilestoneChatOnlyOnTheTrackingIssue(t *testing.T) {
	env, gl := steerer(t)
	withAutopilot(t, env, gl)
	is := trackingIssue()
	is["milestone"] = map[string]any{"id": 31, "iid": 4}
	gl.mu.Lock()
	gl.issues = map[string]any{"77": is}
	gl.mu.Unlock()
	if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "go"}); code != http.StatusConflict {
		t.Errorf("another milestone's issue: status %d, want 409", code)
	}
	gl.mu.Lock()
	gl.issues = nil
	gl.mu.Unlock()
	if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "go"}); code != http.StatusConflict {
		t.Errorf("an issue the user cannot read: status %d, want 409", code)
	}
	if code, _ := env.postJSON(t, "/api/v1/projects/team-a/shop/milestones/4/notes", map[string]string{"body": "go"}); code != http.StatusNotFound {
		t.Errorf("an unplanned milestone: status %d, want 404", code)
	}
	if len(gl.posted) != 0 {
		t.Errorf("nothing may be posted: %q", gl.posted)
	}
}

func TestMilestoneChatGitLabRefuses(t *testing.T) {
	env, gl := steerer(t)
	withAutopilot(t, env, gl)
	for _, c := range []struct{ gitlab, want int }{
		{http.StatusForbidden, http.StatusForbidden},
		{http.StatusNotFound, http.StatusForbidden},
		{http.StatusInternalServerError, http.StatusBadGateway},
		// GitLab rejects the token: connect again.
		{http.StatusUnauthorized, http.StatusConflict},
	} {
		gl.mu.Lock()
		gl.refuse = c.gitlab
		gl.mu.Unlock()
		if code, _ := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "go"}); code != c.want {
			t.Errorf("GitLab %d: status %d, want %d", c.gitlab, code, c.want)
		}
	}
	var st GitLabStatusView
	env.get(t, "/api/v1/gitlab", &st)
	if st.Connected {
		t.Error("a rejected token drops the connection")
	}
}

func TestMilestoneAsksThePilotWithTheChatGrant(t *testing.T) {
	env, gl := newSteeringEnv(t,
		Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true})
	withChat(env.server.cfg)
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}, "chatters": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Permissions == nil || !m.Permissions.Chat {
		t.Fatalf("a chatter may ask the pilot: status %d, %+v", code, m.Permissions)
	}
}

func TestMilestoneMergePermission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		owners []string
		chat   bool // the chat grant and the Desk chat
		want   bool
	}{
		{"owner with chat", []string{"ada@example.com"}, true, true},
		{"one of several owners", []string{"bob@example.com", "ada@example.com"}, true, true},
		{"not an owner", []string{"bob@example.com"}, true, false},
		{"no owners", nil, true, false},
		{"owner without the chat grant", []string{"ada@example.com"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, gl := newSteeringEnv(t,
				Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
				Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: tc.chat})
			withChat(env.server.cfg)
			withAutopilot(t, env, gl)
			var st aiv1.AgentTask
			key := client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}
			if err := env.reader.Get(context.Background(), key, &st); err != nil {
				t.Fatal(err)
			}
			st.Spec.OwnerSubjects = tc.owners
			if err := env.reader.Update(context.Background(), &st); err != nil {
				t.Fatal(err)
			}
			env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
			env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}, "chatters": map[string]any{}}}
			env.login(t, "/")

			var m MilestoneView
			if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Permissions == nil {
				t.Fatalf("status %d, %+v", code, m.Permissions)
			}
			if m.Permissions.Merge != tc.want {
				t.Errorf("permissions.merge = %v, want %v", m.Permissions.Merge, tc.want)
			}
		})
	}
}

func TestMilestoneMergePermissionNeedsTheDeskChat(t *testing.T) {
	env, gl := newSteeringEnv(t,
		Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true})
	withAutopilot(t, env, gl)
	var st aiv1.AgentTask
	key := client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}
	if err := env.reader.Get(context.Background(), key, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.OwnerSubjects = []string{"ada@example.com"}
	if err := env.reader.Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"chatters": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Permissions == nil || m.Permissions.Merge {
		t.Errorf("with the Desk chat off an owner may not merge: status %d, %+v", code, m.Permissions)
	}
}

// fakePauses is a pause store that answers h or err.
type fakePauses struct {
	h   store.PauseHistory
	err error
	got []any
}

func (f *fakePauses) Pauses(_ context.Context, p store.ProjectKey, milestone int64, _ time.Time) (store.PauseHistory, error) {
	f.got = append(f.got, p, milestone)
	return f.h, f.err
}

func TestMilestonePauses(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Pauses != nil || m.Autopilot == nil {
		t.Fatalf("without a database: status %d, pauses %+v", code, m.Pauses)
	}

	since := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)
	h := store.PauseHistory{Totals: store.PauseTotals{Count: 25, PausedSec: 9000, MeanResumeSec: 300,
		ByKind: map[string]store.PauseKindTotal{"ciRed": {Count: 25, Sec: 9000}}, ByResolver: map[string]int{"selfHealed": 24}}}
	for i := range 25 {
		p := store.Pause{Kind: "ciRed", Reason: fmt.Sprint(i), Item: 42, Since: since.Add(time.Duration(i) * time.Hour), Seconds: 600}
		if i < 24 {
			u := until.Add(time.Duration(i) * time.Hour)
			p.Until, p.ResolvedBy = &u, "selfHealed"
		}
		h.Pauses = append(h.Pauses, p)
	}
	fake := &fakePauses{h: h}
	env.server.pauses = fake
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if !slices.Equal(fake.got, []any{store.ProjectKey{Namespace: "team-a", Name: "shop"}, int64(3)}) {
		t.Errorf("queried %v", fake.got)
	}
	ph := m.Pauses
	if ph == nil || len(ph.Pauses) != maxPauseViews {
		t.Fatalf("pauses = %+v", ph)
	}
	if p := ph.Pauses[0]; p.Reason != "24" || p.Until != nil || p.Kind != aiv1.PauseCIRed || p.Seconds != 600 || p.Item != 42 {
		t.Errorf("the newest pause first, open: %+v", p)
	}
	if p := ph.Pauses[1]; p.Reason != "23" || p.Until == nil || p.ResolvedBy != aiv1.ResolvedBySelfHealed {
		t.Errorf("second pause: %+v", p)
	}
	if tot := ph.Totals; tot.Count != 25 || tot.PausedSec != 9000 || tot.MeanResumeSec != 300 ||
		tot.ByKind["ciRed"] != (PauseKindTotalView{Count: 25, Sec: 9000}) || tot.ByResolver["selfHealed"] != 24 {
		t.Errorf("totals = %+v", tot)
	}

	fake.err = errors.New("connection refused")
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Pauses != nil || m.Autopilot == nil {
		t.Errorf("a failing query: status %d, pauses %+v", code, m.Pauses)
	}
}

// fakeMilestoneDetails is a snapshot store that answers the milestones it
// holds, else ErrNotFound, or err.
type fakeMilestoneDetails struct {
	d   map[int64]store.MilestoneDetail
	err error
	got []int64
}

func (f *fakeMilestoneDetails) Milestone(_ context.Context, p store.ProjectKey, iid int64, _ time.Time) (store.MilestoneDetail, error) {
	if p != (store.ProjectKey{Namespace: "team-a", Name: "shop"}) {
		return store.MilestoneDetail{}, fmt.Errorf("unexpected project %v", p)
	}
	f.got = append(f.got, iid)
	if f.err != nil {
		return store.MilestoneDetail{}, f.err
	}
	d, ok := f.d[iid]
	if !ok {
		return store.MilestoneDetail{}, store.ErrNotFound
	}
	return d, nil
}

func (f *fakeMilestoneDetails) IssueFigures(ctx context.Context, p store.ProjectKey, milestone, issue int64, now time.Time) (store.IssueFigures, error) {
	d, err := f.Milestone(ctx, p, milestone, now)
	if err != nil {
		return store.IssueFigures{}, err
	}
	for _, is := range d.Issues {
		if is.IID == issue {
			return is, nil
		}
	}
	return store.IssueFigures{}, store.ErrNotFound
}

// paypalFigures are the figures of issue 42 in the snapshot.
func paypalFigures() store.IssueFigures {
	return store.IssueFigures{IID: 42, Title: "Add PayPal", State: "merged", MRIID: 5, MRURL: "https://gitlab.example.com/acme/shop/-/merge_requests/5",
		WallSec: 3600, AgentMS: 1_500_000, PausedSec: 600,
		Spend: store.SpendTotals{CostUSD: 1.5, CostKnown: true, InputTokens: 1000, OutputTokens: 200, Calls: 3}}
}

// storedCheckout is the snapshot of the closed milestone 4 of the shop,
// its tasks gone.
func storedCheckout() store.MilestoneDetail {
	return store.MilestoneDetail{
		MilestoneRow: store.MilestoneRow{Project: store.ProjectKey{Namespace: "team-a", Name: "shop"}, IID: 4, Title: "Refunds",
			WebURL: "https://gitlab.example.com/acme/shop/-/milestones/4", State: "closed", Phase: "done", TrackingIID: 80,
			Spend: store.SpendTotals{CostUSD: 2.5, CostKnown: true, InputTokens: 2000, OutputTokens: 300, Calls: 5}},
		Issues: []store.IssueFigures{
			{IID: 50, Title: "Refund API", State: "merged", MRIID: 9, WallSec: 7200, AgentMS: 900_000,
				Spend: store.SpendTotals{CostUSD: 2.5, CostKnown: true, InputTokens: 2000, OutputTokens: 300, Calls: 5}},
			{IID: 51, Title: "Refund mail", State: "skipped"},
		},
	}
}

func TestMilestoneIssues(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	want := []MilestoneIssueRow{
		{IID: 42, Title: "Add PayPal", State: "active", MergeRequestIID: 5, MergeRequestURL: "https://gitlab.example.com/acme/shop/-/merge_requests/5", Task: "shop-42-add-paypal"},
		{IID: 43, Title: "Add Klarna", State: "pending"},
	}
	if !slices.Equal(m.Issues, want) || m.State != MilestoneStatePaused || m.Stored {
		t.Errorf("without a database the issues have no figures: %s %v %+v", m.State, m.Stored, m.Issues)
	}

	fake := &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{3: {Issues: []store.IssueFigures{paypalFigures()}}}}
	env.server.milestoneDetails = fake
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || len(m.Issues) != 2 {
		t.Fatalf("milestone: status %d, issues %+v", code, m.Issues)
	}
	if !slices.Equal(fake.got, []int64{3}) {
		t.Errorf("one call for all issues: %v", fake.got)
	}
	is := m.Issues[0]
	if is.State != "active" || is.Task != "shop-42-add-paypal" {
		t.Errorf("states and tasks stay the live ones: %+v", is)
	}
	if f := is.Figures; f == nil || f.WallSec != 3600 || f.AgentSec != 1500 || f.PausedSec != 600 || f.CostUSD != "1.50" ||
		!f.CostKnown || f.Tokens != 1200 || f.CostFrom != CostFromCalls || f.Spend != nil {
		t.Errorf("figures of #42 = %+v", is.Figures)
	}
	if m.Issues[1].Figures != nil {
		t.Errorf("an issue the snapshot lacks has no figures: %+v", m.Issues[1].Figures)
	}

	fake.err = errors.New("connection refused")
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || len(m.Issues) != 2 || m.Issues[0].Figures != nil {
		t.Errorf("a failing query: status %d, issues %+v", code, m.Issues)
	}
}

func TestStoredMilestone(t *testing.T) {
	env, gl := newSteeringEnv(t,
		Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true},
		Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true})
	withChat(env.server.cfg)
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}, "chatters": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	const stored = "/api/v1/projects/team-a/shop/milestones/4"

	// Without the database a milestone without tasks is not found.
	if code := env.get(t, stored, nil); code != http.StatusNotFound {
		t.Errorf("without a database: status %d, want 404", code)
	}

	fake := &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: storedCheckout()}}
	env.server.milestoneDetails = fake
	env.server.pauses = &fakePauses{h: store.PauseHistory{Totals: store.PauseTotals{Count: 1, PausedSec: 600},
		Pauses: []store.Pause{{Kind: "ciRed", Item: 50, Seconds: 600}}}}
	var m MilestoneView
	if code := env.get(t, stored, &m); code != http.StatusOK {
		t.Fatalf("stored milestone: status %d", code)
	}
	if !m.Stored || m.IID != 4 || m.Title != "Refunds" || m.State != MilestoneStateDone || m.WebURL != "https://gitlab.example.com/acme/shop/-/milestones/4" {
		t.Errorf("milestone = %+v", m)
	}
	if m.Spec.Name != "" || m.Autopilot != nil || m.Pilot != nil || len(m.Prompts) != 0 || len(m.Tasks) != 0 {
		t.Errorf("a stored milestone has no live parts: %+v", m)
	}
	if m.Tracking == nil || m.Tracking.IID != 80 || m.Tracking.WebURL != gl.srv.URL+"/acme/shop/-/issues/80" {
		t.Errorf("tracking = %+v", m.Tracking)
	}
	if m.CostUSD != "2.50" || !m.CostKnown || m.Tokens != 2300 || m.CostFrom != CostFromCalls {
		t.Errorf("cost = %+v", m.Cost)
	}
	if m.Pauses == nil || m.Pauses.Totals.PausedSec != 600 || len(m.Pauses.Pauses) != 1 {
		t.Errorf("pauses = %+v", m.Pauses)
	}
	if len(m.Issues) != 2 || m.Issues[0].IID != 50 || m.Issues[0].State != "merged" || m.Issues[0].MergeRequestIID != 9 || m.Issues[0].Task != "" ||
		m.Issues[0].Figures == nil || m.Issues[0].Figures.WallSec != 7200 || m.Issues[0].Figures.AgentSec != 900 || m.Issues[0].Figures.CostUSD != "2.50" {
		t.Errorf("issues = %+v", m.Issues)
	}
	if f := m.Issues[1].Figures; m.Issues[1].State != "skipped" || f == nil || f.CostKnown || f.CostUSD != "" {
		t.Errorf("an issue without calls has an unknown cost, never $0: %+v", m.Issues[1])
	}
	if p := m.Permissions; p == nil || !p.GitLab || !p.Steer || p.Chat || p.Transcript || p.Merge {
		t.Errorf("steer as live, no chat, transcript or merge: %+v", m.Permissions)
	}

	// A live milestone keeps its view; unknown milestones are not found;
	// the thread and notes need live tasks.
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Stored || m.Spec.Name != "shop-spec-m3-checkout" {
		t.Errorf("live milestone: status %d, %+v", code, m)
	}
	for _, path := range []string{"/api/v1/projects/team-a/shop/milestones/5", stored + "/thread"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
	if code, _ := env.postJSON(t, stored+"/notes", map[string]string{"body": "go"}); code != http.StatusNotFound {
		t.Errorf("notes on a stored milestone: status %d, want 404", code)
	}
	if len(gl.posted) != 0 {
		t.Errorf("nothing may be posted: %q", gl.posted)
	}

	fake.err = errors.New("connection refused")
	if code := env.get(t, stored, nil); code != http.StatusInternalServerError {
		t.Errorf("a failing query: status %d, want 500", code)
	}
}

func TestStoredMilestoneNeedsTheViewRole(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-b/*"}})
	withAutopilot(t, env, gl)
	fake := &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: storedCheckout()}}
	env.server.milestoneDetails = fake
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	if code := env.get(t, "/api/v1/projects/team-a/shop/milestones/4", nil); code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
	if len(fake.got) != 0 {
		t.Errorf("the snapshot may not be read: %v", fake.got)
	}
}

// fakeSpend answers Spend with sp and records the scopes asked.
type fakeSpend struct {
	sp  store.Spend
	err error
	got []store.SpendScope
}

func (f *fakeSpend) Spend(_ context.Context, sc store.SpendScope) (store.Spend, error) {
	f.got = append(f.got, sc)
	return f.sp, f.err
}

const retroText = "## Retrospective\n\nCI was red twice."

// withRetrospective sets the retrospective r on the spec task of
// milestone 3.
func withRetrospective(t *testing.T, env *testEnv, r *aiv1.PilotRetrospective) {
	t.Helper()
	ctx := context.Background()
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Pilot = &aiv1.PilotStatus{Retrospective: r}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
}

func TestMilestoneRetrospective(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	web := gl.srv.URL + "/acme/shop"
	spend := &fakeSpend{sp: store.Spend{Children: []store.SpendChild{
		{Level: store.SpendMilestone, Key: "3", Label: "Milestone %3", Totals: store.SpendTotals{CostUSD: 9, CostKnown: true}},
		{Level: store.SpendIssue, Key: "42", Label: "#42", Totals: store.SpendTotals{CostUSD: 1.5, CostKnown: true, InputTokens: 100}},
	}, ByRole: []store.SpendBreakdown{{Key: "implementer", Totals: store.SpendTotals{CostUSD: 2, CostKnown: true}}}}}
	env.server.retroSpend = spend

	// None: absent, and no spend is read for it.
	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK || m.Retrospective != nil || len(spend.got) != 0 {
		t.Fatalf("no retrospective: status %d, %+v, spend calls %v", code, m.Retrospective, spend.got)
	}

	posted := metav1.NewTime(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	withRetrospective(t, env, &aiv1.PilotRetrospective{NoteID: 900, PostedAt: &posted, Text: retroText,
		FollowUps: []aiv1.PilotFollowUp{{Title: "Retry CI", Body: "flaky lint"}, {Title: "Docs"}, {Title: "Backfill"}},
		Created:   map[string]int64{"1": 60}, Taken: map[string]int64{"3": 14}})
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("live: status %d", code)
	}
	r := m.Retrospective
	if r == nil || r.Text != retroText || r.NoteURL != web+"/-/issues/77#note_900" || r.PostedAt == nil || !r.PostedAt.Equal(&posted) || r.Failed != "" {
		t.Fatalf("live retrospective = %+v", r)
	}
	wantFollowUps := []FollowUpView{
		{Number: 1, Title: "Retry CI", Body: "flaky lint", State: "issue", TargetIID: 60, TargetURL: web + "/-/issues/60"},
		{Number: 2, Title: "Docs", State: "open"},
		{Number: 3, Title: "Backfill", State: "milestone", TargetIID: 14, TargetURL: web + "/-/milestones/14"},
	}
	if !slices.Equal(r.FollowUps, wantFollowUps) {
		t.Errorf("follow-ups = %+v", r.FollowUps)
	}
	wantDrivers := []CostDriverView{
		{Label: "implementer", Kind: "role", CostUSD: "2.00", CostKnown: true},
		{Label: "#42", Kind: "issue", CostUSD: "1.50", CostKnown: true, Tokens: 100},
	}
	if !slices.Equal(r.CostDrivers, wantDrivers) {
		t.Errorf("cost drivers = %+v", r.CostDrivers)
	}
	if want := (store.SpendScope{Namespace: "team-a", Project: "shop", MilestoneIID: 3}); !slices.Equal(spend.got, []store.SpendScope{want}) {
		t.Errorf("one spend call for the milestone: %v", spend.got)
	}

	// Stored: the same text after the tasks are gone; later states link.
	d := storedCheckout()
	d.RetroText, d.RetroNoteID, d.RetroPostedAt = retroText, 900, posted.Time
	d.FollowUps = []store.FollowUpRow{{Number: 1, Title: "Retry CI", State: "issue", TargetIID: 60},
		{Number: 2, Title: "Docs", State: "milestone", TargetIID: 7}, {Number: 3, Title: "Old", State: "dismissed"}}
	env.server.milestoneDetails = &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: d}}
	m = MilestoneView{}
	if code := env.get(t, "/api/v1/projects/team-a/shop/milestones/4", &m); code != http.StatusOK || !m.Stored {
		t.Fatalf("stored: status %d", code)
	}
	if r := m.Retrospective; r == nil || r.Text != retroText || r.NoteURL != web+"/-/issues/80#note_900" || r.PostedAt == nil ||
		!r.PostedAt.Time.Equal(posted.Time) || !slices.Equal(r.CostDrivers, wantDrivers) {
		t.Fatalf("stored retrospective = %+v", r)
	}
	wantStored := []FollowUpView{
		{Number: 1, Title: "Retry CI", State: "issue", TargetIID: 60, TargetURL: web + "/-/issues/60"},
		{Number: 2, Title: "Docs", State: "milestone", TargetIID: 7, TargetURL: web + "/-/milestones/7"},
		{Number: 3, Title: "Old", State: "dismissed"},
	}
	if !slices.Equal(m.Retrospective.FollowUps, wantStored) {
		t.Errorf("stored follow-ups = %+v", m.Retrospective.FollowUps)
	}

	// Failed, and a failing spend read leaves the drivers out.
	withRetrospective(t, env, &aiv1.PilotRetrospective{Failed: "the pilot gave no answer"})
	spend.err = errors.New("connection refused")
	m = MilestoneView{}
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("failed: status %d", code)
	}
	if r := m.Retrospective; r == nil || r.Failed != "the pilot gave no answer" || r.Text != "" || r.NoteURL != "" ||
		r.PostedAt != nil || len(r.FollowUps) != 0 || r.CostDrivers != nil {
		t.Errorf("failed retrospective = %+v", r)
	}
	if len(gl.posted) != 0 || len(gl.read) != 0 {
		t.Errorf("no GitLab calls: posted %q, read %q", gl.posted, gl.read)
	}
}

func TestCostDrivers(t *testing.T) {
	known := func(usd float64, tokens int64) store.SpendTotals {
		return store.SpendTotals{CostUSD: usd, CostKnown: true, InputTokens: tokens}
	}
	unknown := func(tokens int64) store.SpendTotals { return store.SpendTotals{OutputTokens: tokens} }
	sp := store.Spend{
		Children: []store.SpendChild{
			{Level: store.SpendIssue, Label: "#1", Totals: known(1, 10)},
			{Level: store.SpendIssue, Label: "#2", Totals: unknown(500)},
			{Level: store.SpendIssue, Label: "#3", Totals: known(3, 10)},
			{Level: store.SpendIssue, Label: "#4", Totals: unknown(900)},
		},
		ByRole: []store.SpendBreakdown{{Key: "reviewer", Totals: known(2, 10)}, {Key: "pilot", Totals: known(0.5, 10)}},
	}
	var got []string
	for _, d := range costDrivers(sp) {
		got = append(got, d.Label+"="+d.CostUSD)
	}
	if want := []string{"#3=3.00", "reviewer=2.00", "#1=1.00", "pilot=0.50", "#4="}; !slices.Equal(got, want) {
		t.Errorf("drivers = %v, want %v", got, want)
	}
	if got := costDrivers(store.Spend{}); got == nil || len(got) != 0 {
		t.Errorf("no spend: %v", got)
	}
}
