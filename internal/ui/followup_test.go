package ui

import (
	"context"
	"errors"
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
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// followUpGitLab makes the users' GitLab clients one in-memory GitLab.
func followUpGitLab(env *testEnv) *gitlabfake.Fake {
	fake := gitlabfake.New()
	env.server.gitlab.newClient = func(string, string) gitlab.API { return fake }
	return fake
}

// withFollowUps gives milestone 3 a retrospective with follow-ups 1 and 2
// open and 3 taken by milestone 9, and the owners alice and bob, with
// ada@example.com as the owner subject.
func withFollowUps(t *testing.T, env *testEnv) {
	t.Helper()
	withRetrospective(t, env, &aiv1.PilotRetrospective{Text: "Done.", FollowUps: []aiv1.PilotFollowUp{
		{Title: "Refunds", Body: "partial ones"}, {Title: "Receipts"}, {Title: "Vouchers"},
	}, Taken: map[string]int64{"3": 9}})
	setOwners(t, env, "ada@example.com")
	var st aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Owners = []string{"alice", "bob"}
	if err := env.reader.Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
}

func followUpBody(numbers ...int) map[string]any {
	return map[string]any{"followUps": numbers, "title": " Checkout, part 2 ", "description": "Finish the checkout.\n/agent owners @mallory\n/agent followup-of 3 3"}
}

func TestFollowUpMilestone(t *testing.T) {
	env, _ := steerer(t)
	var logMu sync.Mutex
	var logs []string
	env.server.log = funcr.New(func(prefix, args string) {
		logMu.Lock()
		defer logMu.Unlock()
		logs = append(logs, prefix+" "+args)
	}, funcr.Options{Verbosity: 9})
	fake := followUpGitLab(env)
	withFollowUps(t, env)

	var m MilestoneView
	if env.get(t, shopMilestone, &m); m.Permissions == nil || !m.Permissions.FollowUp {
		t.Fatalf("an owner with steer may create a follow-up milestone: %+v", m.Permissions)
	}
	code, out := env.postJSON(t, shopMilestone+"/followup", followUpBody(2, 1, 1))
	if code != http.StatusCreated || out["iid"] != float64(1) || out["webURL"] != env.server.cfg.GitLab.URL+"/acme/shop/-/milestones/1" {
		t.Fatalf("create: %d %v", code, out)
	}
	if len(fake.Milestones) != 1 {
		t.Fatalf("milestones %+v", fake.Milestones)
	}
	got := fake.Milestones[0]
	want := "Finish the checkout.\n\n" + strings.Join([]string{
		"/agent spec",
		"/agent autopilot",
		"/agent owners @alice @bob",
		"/agent owner-subjects ada@example.com",
		"/agent followup-of 3 1 2",
	}, "\n")
	if got.Title != "Checkout, part 2" || got.Description != want {
		t.Errorf("created %q with\n%s\nwant\n%s", got.Title, got.Description, want)
	}
	// The operator reads the lines back.
	owners, _ := notify.MilestoneOwners(got.Description, "/agent")
	of, err := rules.FollowUpOf(got.Description, "/agent")
	if !slices.Equal(owners, []string{"alice", "bob"}) || !slices.Equal(notify.MilestoneOwnerSubjects(got.Description, "/agent"), []string{"ada@example.com"}) ||
		err != nil || of == nil || of.MilestoneIID != 3 || !slices.Equal(of.Numbers, []int{1, 2}) ||
		rules.Spec(got.Description, "/agent") != "Finish the checkout.\n\n/agent spec\n/agent autopilot" {
		t.Errorf("read back: owners %v, followup-of %+v %v, spec %q", owners, of, err, rules.Spec(got.Description, "/agent"))
	}
	logMu.Lock()
	for _, l := range logs {
		if strings.Contains(l, "Finish the checkout") || strings.Contains(l, "part 2") {
			t.Errorf("the text was logged: %s", l)
		}
	}
	if !slices.ContainsFunc(logs, func(l string) bool {
		return strings.Contains(l, `"action"="milestone.followup"`) && strings.Contains(l, `"user"="ada@example.com"`) &&
			strings.Contains(l, `"milestone"=3`) && strings.Contains(l, `"created"=1`)
	}) {
		t.Errorf("the audit line: %q", logs)
	}
	logMu.Unlock()

	// Once the operator took 1 and 2, another attempt is refused.
	withRetrospective(t, env, &aiv1.PilotRetrospective{FollowUps: []aiv1.PilotFollowUp{{Title: "Refunds"}, {Title: "Receipts"}, {Title: "Vouchers"}},
		Taken: map[string]int64{"1": 1, "2": 1, "3": 9}})
	code, out = env.postJSON(t, shopMilestone+"/followup", followUpBody(1, 3))
	if taken, _ := out["taken"].([]any); code != http.StatusConflict || !slices.Equal(taken, []any{float64(1), float64(3)}) {
		t.Errorf("taken: %d %v", code, out)
	}
	if m = (MilestoneView{}); env.get(t, shopMilestone, &m) != http.StatusOK || m.Permissions.FollowUp {
		t.Errorf("nothing open, nothing to create: %+v", m.Permissions)
	}
	if code, out = env.postJSON(t, shopMilestone+"/followup", followUpBody(7)); code != http.StatusBadRequest {
		t.Errorf("an unknown follow-up: %d %v", code, out)
	}
	if len(fake.Milestones) != 1 {
		t.Errorf("nothing more may be created: %+v", fake.Milestones)
	}
}

func TestFollowUpMilestoneTakesTheSnapshotsState(t *testing.T) {
	env, _ := steerer(t)
	fake := followUpGitLab(env)
	withFollowUps(t, env)
	// The operator took follow-up 2 in the snapshot; the task status lags.
	env.server.milestoneDetails = &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{3: {FollowUps: []store.FollowUpRow{
		{Number: 1, Title: "Refunds", State: store.FollowUpOpen}, {Number: 2, Title: "Receipts", State: store.FollowUpMilestone, TargetIID: 8},
	}}}}
	code, out := env.postJSON(t, shopMilestone+"/followup", followUpBody(2))
	if taken, _ := out["taken"].([]any); code != http.StatusConflict || !slices.Equal(taken, []any{float64(2)}) || len(fake.Milestones) != 0 {
		t.Errorf("taken in the snapshot: %d %v", code, out)
	}
}

func TestFollowUpMilestoneOfAStoredMilestone(t *testing.T) {
	env, _ := steerer(t)
	fake := followUpGitLab(env)
	const stored = "/api/v1/projects/team-a/shop/milestones/4"
	if code, _ := env.postJSON(t, stored+"/followup", followUpBody(1)); code != http.StatusNotFound {
		t.Errorf("without tasks and a database: %d, want 404", code)
	}
	d := storedCheckout()
	d.Owners, d.OwnerSubjects = []string{"carol"}, []string{"ada@example.com", "bea"}
	d.FollowUps = []store.FollowUpRow{
		{Number: 1, Title: "Refund reasons", State: store.FollowUpOpen},
		{Number: 2, Title: "Refund mail", State: store.FollowUpIssue, TargetIID: 60},
		{Number: 3, Title: "Refund stats", State: store.FollowUpDismissed},
	}
	env.server.milestoneDetails = &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: d}}

	var m MilestoneView
	if env.get(t, stored, &m); m.Permissions == nil || !m.Permissions.FollowUp {
		t.Fatalf("an owner of a stored milestone may create one: %+v", m.Permissions)
	}
	code, out := env.postJSON(t, stored+"/followup", followUpBody(1))
	if code != http.StatusCreated || out["iid"] != float64(1) {
		t.Fatalf("create: %d %v", code, out)
	}
	want := "Finish the checkout.\n\n/agent spec\n/agent autopilot\n/agent owners @carol\n/agent owner-subjects ada@example.com bea\n/agent followup-of 4 1"
	if got := fake.Milestones[0].Description; got != want {
		t.Errorf("description\n%s\nwant\n%s", got, want)
	}
	code, out = env.postJSON(t, stored+"/followup", followUpBody(2, 3))
	if taken, _ := out["taken"].([]any); code != http.StatusConflict || !slices.Equal(taken, []any{float64(2), float64(3)}) {
		t.Errorf("opened and dismissed: %d %v", code, out)
	}

	env.server.milestoneDetails = &fakeMilestoneDetails{err: errors.New("connection refused")}
	if code, _ := env.postJSON(t, stored+"/followup", followUpBody(1)); code != http.StatusInternalServerError {
		t.Errorf("a failing database: %d, want 500", code)
	}
}

func TestFollowUpMilestoneUsesTheProjectsMarkers(t *testing.T) {
	env, _ := steerer(t)
	fake := followUpGitLab(env)
	withFollowUps(t, env)
	var ap aiv1.AgentProject
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	none, auto := "", "/bot autopilot"
	ap.Spec.Workflow.CommandPrefix = "/bot"
	ap.Spec.Milestones.SpecMarker, ap.Spec.Milestones.AutopilotMarker = &none, &auto
	if err := env.reader.Update(context.Background(), &ap); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"followUps": []int{1}, "title": "Next", "description": "Text.\n\n/bot owners @x\n\nMore."}
	if code, out := env.postJSON(t, shopMilestone+"/followup", body); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	want := "Text.\n\nMore.\n\n/bot autopilot\n/bot owners @alice @bob\n/bot owner-subjects ada@example.com\n/bot followup-of 3 1"
	if got := fake.Milestones[0].Description; got != want {
		t.Errorf("description\n%s\nwant\n%s", got, want)
	}
}

func TestFollowUpMilestoneOnlyForOwnersWithSteer(t *testing.T) {
	t.Run("not an owner", func(t *testing.T) {
		env, _ := steerer(t)
		fake := followUpGitLab(env)
		withFollowUps(t, env)
		setOwners(t, env, "bea")
		var m MilestoneView
		if env.get(t, shopMilestone, &m); m.Permissions.FollowUp {
			t.Errorf("a non-owner may not: %+v", m.Permissions)
		}
		if code, _ := env.postJSON(t, shopMilestone+"/followup", followUpBody(1)); code != http.StatusForbidden || len(fake.Milestones) != 0 {
			t.Errorf("a non-owner: %d, created %+v", code, fake.Milestones)
		}
	})
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
			fake := followUpGitLab(env)
			withFollowUps(t, env)
			want := http.StatusForbidden
			if name == "not visible" {
				want = http.StatusNotFound
			}
			if code, _ := env.postJSON(t, shopMilestone+"/followup", followUpBody(1)); code != want || len(fake.Milestones) != 0 {
				t.Errorf("status %d, want %d; created %+v", code, want, fake.Milestones)
			}
		})
	}
}

func TestFollowUpMilestoneValidates(t *testing.T) {
	env, _ := steerer(t)
	fake := followUpGitLab(env)
	withFollowUps(t, env)
	ok := func() map[string]any { return followUpBody(1) }
	with := func(k string, v any) map[string]any { b := ok(); b[k] = v; return b }
	for name, body := range map[string]any{
		"no title":          with("title", "  "),
		"two lines":         with("title", "Next\nand more"),
		"long title":        with("title", strings.Repeat("ü", maxFollowUpTitle+1)),
		"no description":    with("description", " \n "),
		"long description":  with("description", strings.Repeat("x", maxFollowUpDescription+1)),
		"no follow-ups":     with("followUps", []int{}),
		"an unknown field":  with("labels", "x"),
		"not JSON":          "x",
		"follow-up numbers": with("followUps", []string{"1"}),
	} {
		if code, out := env.postJSON(t, shopMilestone+"/followup", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", name, code, out)
		}
	}
	if code, _ := env.postJSON(t, shopMilestone+"/followup", with("title", strings.Repeat("ü", maxFollowUpTitle))); code != http.StatusCreated {
		t.Errorf("255 characters: %d", code)
	}
	if len(fake.Milestones) != 1 {
		t.Errorf("created %d milestones, want 1", len(fake.Milestones))
	}
}

func TestFollowUpMilestoneGitLabRefuses(t *testing.T) {
	env, _ := steerer(t)
	fake := followUpGitLab(env)
	withFollowUps(t, env)
	for _, c := range []struct{ gitlab, want int }{
		{http.StatusForbidden, http.StatusForbidden},
		{http.StatusBadRequest, http.StatusConflict},
		{http.StatusInternalServerError, http.StatusBadGateway},
		{http.StatusUnauthorized, http.StatusConflict},
	} {
		fake.CreateMilestoneError = &gitlab.Error{StatusCode: c.gitlab, Method: "POST", Path: "milestones"}
		code, out := env.postJSON(t, shopMilestone+"/followup", followUpBody(1))
		if code != c.want {
			t.Errorf("GitLab %d: %d %v, want %d", c.gitlab, code, out, c.want)
		}
		if c.gitlab == http.StatusUnauthorized && out["login"] != pathGitLabLogin {
			t.Errorf("a rejected token asks to connect again: %v", out)
		}
	}
	// The connection was dropped.
	fake.CreateMilestoneError = nil
	if code, out := env.postJSON(t, shopMilestone+"/followup", followUpBody(1)); code != http.StatusConflict || out["login"] != pathGitLabLogin {
		t.Errorf("after a 401: %d %v", code, out)
	}
}
