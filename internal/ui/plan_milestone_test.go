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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

const newPlanMilestone = shopPlans + "/shop-plan-new/milestone"

// planSteerer is a chat-enabled UI with shop's plans, the reader as the
// writer client, ada@example.com logged in with steer and connected to an
// in-memory GitLab.
func planSteerer(t *testing.T) (*testEnv, *gitlabfake.Fake) {
	t.Helper()
	env, _ := steeringEnvOf(t, withChat(oidcConfig(Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true})))
	addPlans(t, env)
	env.server.SetResearchWriter(env.reader)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	return env, followUpGitLab(env)
}

// specOwners gives shop's live Spec task the owners and owner subjects.
func specOwners(t *testing.T, env *testEnv, owners, subjects []string) {
	t.Helper()
	var st aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Owners, st.Spec.OwnerSubjects = owners, subjects
	if err := env.reader.Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
}

func planMilestoneBody(spec, autopilot bool) map[string]any {
	return map[string]any{"title": " Refunds ", "description": "## Goal\n\nRefunds.\n/agent owners @mallory\n",
		"spec": spec, "autopilot": autopilot}
}

func TestPlanMilestone(t *testing.T) {
	for _, c := range []struct {
		name            string
		spec, autopilot bool
		markers         []string
	}{
		{"spec and autopilot", true, true, []string{"/agent spec", "/agent autopilot"}},
		{"spec only", true, false, []string{"/agent spec"}},
		{"none", false, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, fake := planSteerer(t)
			var logMu sync.Mutex
			var logs []string
			env.server.log = funcr.New(func(prefix, args string) {
				logMu.Lock()
				defer logMu.Unlock()
				logs = append(logs, prefix+" "+args)
			}, funcr.Options{Verbosity: 9})
			specOwners(t, env, []string{"bob", "alice"}, []string{"bea", "ada@example.com"})

			code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(c.spec, c.autopilot))
			url := env.server.cfg.GitLab.URL + "/acme/shop/-/milestones/1"
			if code != http.StatusCreated || out["iid"] != float64(1) || out["webURL"] != url {
				t.Fatalf("create: %d %v", code, out)
			}
			if len(fake.Milestones) != 1 {
				t.Fatalf("milestones %+v", fake.Milestones)
			}
			lines := append(c.markers, "/agent owners @alice @bob", "/agent owner-subjects ada@example.com bea")
			want := "## Goal\n\nRefunds.\n\n" + strings.Join(lines, "\n")
			if got := fake.Milestones[0]; got.Title != "Refunds" || got.Description != want {
				t.Errorf("created %q with\n%s\nwant\n%s", got.Title, got.Description, want)
			}
			st := env.getPlan(t, "shop-plan-new").Status
			if st.Phase != aiv1.AgentPlanCreated || st.CreatedMilestoneIID != 1 || st.CreatedMilestoneURL != url ||
				st.Draft != "## Goal\n\nRefunds.\n/agent owners @mallory" || st.DraftRevision != 1 || st.DraftBy != "owner" {
				t.Errorf("status: %+v", st)
			}
			logMu.Lock()
			if !slices.ContainsFunc(logs, func(l string) bool {
				return strings.Contains(l, `"action"="plan.milestone"`) && strings.Contains(l, `"user"="ada@example.com"`) &&
					strings.Contains(l, `"name"="shop-plan-new"`) && strings.Contains(l, `"created"=1`)
			}) {
				t.Errorf("the audit line: %q", logs)
			}
			for _, l := range logs {
				if strings.Contains(l, "Refunds") {
					t.Errorf("the text was logged: %s", l)
				}
			}
			logMu.Unlock()

			// A second create is refused before GitLab.
			if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusConflict ||
				out["error"] != "a milestone was already created from this plan" || len(fake.Milestones) != 1 {
				t.Errorf("second create: %d %v, %d milestones", code, out, len(fake.Milestones))
			}
		})
	}
}

// Without owners on the live Spec task the owners are the project's notify
// users; the creator is always an owner subject.
func TestPlanMilestoneOwnersFallback(t *testing.T) {
	env, fake := planSteerer(t)
	var ap aiv1.AgentProject
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Spec.Notify.Users = []string{"zoe", "carol"}
	if err := env.reader.Update(context.Background(), &ap); err != nil {
		t.Fatal(err)
	}
	if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	desc := fake.Milestones[0].Description
	owners, _ := notify.MilestoneOwners(desc, "/agent")
	if !slices.Equal(owners, []string{"carol", "zoe"}) || !slices.Equal(notify.MilestoneOwnerSubjects(desc, "/agent"), []string{"ada@example.com"}) {
		t.Errorf("owners %v, subjects %v in\n%s", owners, notify.MilestoneOwnerSubjects(desc, "/agent"), desc)
	}
}

func TestPlanMilestoneRefused(t *testing.T) {
	env, fake := planSteerer(t)
	for name, c := range map[string]struct {
		path string
		body any
		want int
	}{
		"created plan":   {shopPlans + "/shop-plan-old/milestone", planMilestoneBody(true, true), http.StatusConflict},
		"unknown plan":   {shopPlans + "/nope/milestone", planMilestoneBody(true, true), http.StatusNotFound},
		"no title":       {newPlanMilestone, map[string]any{"title": " ", "description": "x"}, http.StatusBadRequest},
		"two lines":      {newPlanMilestone, map[string]any{"title": "a\nb", "description": "x"}, http.StatusBadRequest},
		"no description": {newPlanMilestone, map[string]any{"title": "x", "description": "\n"}, http.StatusBadRequest},
		"long":           {newPlanMilestone, map[string]any{"title": "x", "description": strings.Repeat("x", maxFollowUpDescription+1)}, http.StatusBadRequest},
		"not JSON":       {newPlanMilestone, "x", http.StatusBadRequest},
	} {
		if code, out := env.postJSON(t, c.path, c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, c.want)
		}
	}
	specOwners(t, env, nil, []string{"bea"})
	if code, _ := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusForbidden {
		t.Errorf("not an owner subject: %d", code)
	}
	if st := env.getPlan(t, "shop-plan-new").Status; len(fake.Milestones) != 0 || st.Phase != aiv1.AgentPlanIdle {
		t.Errorf("created %+v, status %+v", fake.Milestones, st)
	}
}

// Another writer changed the plan since the UI read it: the claim fails and
// nothing reaches GitLab.
func TestPlanMilestoneClaimConflict(t *testing.T) {
	env, fake := planSteerer(t)
	once := sync.Once{}
	env.server.SetResearchWriter(interceptor.NewClient(env.reader.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			once.Do(func() {
				other := env.getPlan(t, obj.GetName())
				other.Status.Phase = aiv1.AgentPlanCreated
				if err := c.Status().Update(ctx, other); err != nil {
					t.Fatal(err)
				}
			})
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	}))
	if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusConflict {
		t.Errorf("claim conflict: %d %v", code, out)
	}
	if len(fake.Milestones) != 0 {
		t.Errorf("created %+v", fake.Milestones)
	}
}

func TestPlanMilestoneGitLabRefuses(t *testing.T) {
	env, fake := planSteerer(t)
	for _, c := range []struct{ gitlab, want int }{
		{http.StatusForbidden, http.StatusForbidden},
		{http.StatusUnprocessableEntity, http.StatusConflict},
		{http.StatusInternalServerError, http.StatusBadGateway},
		{http.StatusUnauthorized, http.StatusConflict},
	} {
		fake.CreateMilestoneError = &gitlab.Error{StatusCode: c.gitlab, Method: "POST", Path: "milestones"}
		code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true))
		if code != c.want {
			t.Errorf("GitLab %d: %d %v, want %d", c.gitlab, code, out, c.want)
		}
		if c.gitlab == http.StatusUnauthorized && out["login"] != pathGitLabLogin {
			t.Errorf("a rejected token asks to connect again: %v", out)
		}
		// The plan stays usable.
		if st := env.getPlan(t, "shop-plan-new").Status; st.Phase != aiv1.AgentPlanIdle || st.CreatedMilestoneIID != 0 || st.DraftRevision != 0 {
			t.Errorf("GitLab %d: status %+v", c.gitlab, st)
		}
	}
	// Connected again, the plan creates its milestone.
	fake.CreateMilestoneError = nil
	env.connectGitLab(t, "good-code")
	if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusCreated {
		t.Errorf("after the failures: %d %v", code, out)
	}
}
