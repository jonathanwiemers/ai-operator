package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
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
			pl := env.getPlan(t, "shop-plan-new")
			st, d := pl.Status, planDraft(pl)
			if st.Phase != aiv1.AgentPlanCreated || st.CreatedMilestoneIID != 1 || d.CreatedMilestoneIID != 1 || d.CreatedMilestoneURL != url ||
				d.Draft != "## Goal\n\nRefunds.\n/agent owners @mallory" || d.Revision != 1 || d.By != "owner" || d.Key != 1 ||
				st.Title != "Refunds" || d.Title != "Refunds" || pl.Active() {
				t.Errorf("status: %+v", st)
			}
			logMu.Lock()
			if !slices.ContainsFunc(logs, func(l string) bool {
				return strings.Contains(l, `"action"="plan.milestone"`) && strings.Contains(l, `"user"="ada@example.com"`) &&
					strings.Contains(l, `"name"="shop-plan-new"`) && strings.Contains(l, `"key"=1`) && strings.Contains(l, `"created"=1`)
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
				out["error"] != "the plan has created its milestone" || len(fake.Milestones) != 1 {
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
	agentTitled := env.getPlan(t, "shop-plan-new")
	agentTitled.Status.Title = "Refunds by the agent"
	if err := env.reader.Status().Update(context.Background(), agentTitled); err != nil {
		t.Fatal(err)
	}
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
		if st := env.getPlan(t, "shop-plan-new").Status; st.Phase != aiv1.AgentPlanIdle || st.CreatedMilestoneIID != 0 || len(st.Drafts) != 0 ||
			st.Title != "Refunds by the agent" {
			t.Errorf("GitLab %d: status %+v", c.gitlab, st)
		}
	}
	// Connected again, the plan creates its milestone.
	fake.CreateMilestoneError = nil
	env.connectGitLab(t, "good-code")
	if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusCreated {
		t.Errorf("after the failures: %d %v", code, out)
	}
	if st := env.getPlan(t, "shop-plan-new").Status; st.Title != "Refunds" {
		t.Errorf("the milestone's title settles the agent's: %q", st.Title)
	}
}

// A plan from before status.drafts shows its single draft, and the first
// write moves it to key 1: the deprecated fields are empty after the edit,
// and the milestone is recorded on key 1.
func TestPlanOldDraftEditAndCreate(t *testing.T) {
	env, _ := planSteerer(t)
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Draft, pl.Status.DraftRevision, pl.Status.DraftBy, pl.Status.DraftSHA = "## Old", 2, "agent", "abc"
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	var v PlanView
	if code := env.get(t, shopPlans+"/shop-plan-new", &v); code != http.StatusOK || len(v.Drafts) != 1 || firstDraft(v) != (PlanDraftView{Key: 1, Draft: "## Old", Revision: 2, By: "agent", SHA: "abc"}) {
		t.Fatalf("view: %d %+v", code, v)
	}
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{2, "## Owner"}, &v); code != http.StatusOK || firstDraft(v).Draft != "## Owner" || firstDraft(v).Revision != 3 {
		t.Fatalf("edit: %d %+v", code, v)
	}
	st := env.getPlan(t, "shop-plan-new").Status
	want := []aiv1.PlanDraft{{Key: 1, Draft: "## Owner", Revision: 3, By: "owner", SHA: "abc"}}
	if !slices.Equal(st.Drafts, want) || st.DraftSeq != 1 || st.Draft != "" || st.DraftRevision != 0 || st.DraftBy != "" || st.DraftSHA != "" {
		t.Fatalf("not moved to key 1: %+v", st)
	}
	if code, out := env.postJSON(t, newPlanMilestone, planMilestoneBody(true, true)); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	url := env.server.cfg.GitLab.URL + "/acme/shop/-/milestones/1"
	st = env.getPlan(t, "shop-plan-new").Status
	if d := st.Drafts; len(d) != 1 || d[0].Key != 1 || d[0].CreatedMilestoneIID != 1 || d[0].CreatedMilestoneURL != url || d[0].Revision != 4 ||
		st.CreatedMilestoneIID != 1 || st.CreatedMilestoneURL != "" {
		t.Fatalf("milestone not on key 1: %+v", st)
	}
	if code := env.get(t, shopPlans+"/shop-plan-new", &v); code != http.StatusOK || v.CreatedMilestoneIID != 1 || v.CreatedMilestoneURL != url {
		t.Fatalf("view after create: %d %+v", code, v.PlanListItem)
	}
}

// withThreeDrafts gives shop-plan-new the uncreated drafts 1, 2 and 3 with
// the texts.
func withThreeDrafts(t *testing.T, env *testEnv, texts ...string) {
	t.Helper()
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Drafts, pl.Status.DraftSeq = nil, 3
	for i, text := range texts {
		pl.Status.Drafts = append(pl.Status.Drafts, aiv1.PlanDraft{Key: int64(i + 1), Title: "Draft", Draft: text, Revision: 1, By: "agent"})
	}
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
}

func draftBody(key int64, description string) map[string]any {
	return map[string]any{"key": key, "title": "Part " + strconv.FormatInt(key, 10), "description": description, "spec": true}
}

// Drafts are created in list order only, each %draft-N of a created draft
// becomes its %IID, and the phase is Created with the last draft.
func TestPlanMilestoneDraftsInOrder(t *testing.T) {
	env, fake := planSteerer(t)
	withThreeDrafts(t, env, "## One", "builds on %draft-1", "builds on %draft-1 and %draft-2")
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "builds on %draft-1")); code != http.StatusConflict ||
		out["error"] != "create draft 1 first" || len(fake.Milestones) != 0 {
		t.Fatalf("draft 2 first: %d %v", code, out)
	}
	texts := []string{"## One", "builds on %draft-1", "builds on %draft-1 and %draft-2"}
	for i, text := range texts {
		key := int64(i + 1)
		code, out := env.postJSON(t, newPlanMilestone, draftBody(key, text))
		if code != http.StatusCreated || out["iid"] != float64(key) {
			t.Fatalf("draft %d: %d %v", key, code, out)
		}
		pl := env.getPlan(t, "shop-plan-new")
		if last := key == 3; (pl.Status.Phase == aiv1.AgentPlanCreated) != last || pl.Active() == last {
			t.Errorf("after draft %d: phase %s, active %v", key, pl.Status.Phase, pl.Active())
		}
	}
	if got := fake.Milestones[2]; got.Title != "Part 3" || !strings.HasPrefix(got.Description, "builds on %1 and %2\n") {
		t.Errorf("draft 3 created as %q:\n%s", got.Title, got.Description)
	}
	st := env.getPlan(t, "shop-plan-new").Status
	if d := st.Drafts[1]; d.Draft != "builds on %1" || d.CreatedMilestoneIID != 2 || d.Revision != 2 || d.By != "owner" || d.Title != "Part 2" {
		t.Errorf("draft 2: %+v", d)
	}
	if st.Title != "Part 1" || st.CreatedMilestoneIID != 1 {
		t.Errorf("the first created draft names the plan: %q %d", st.Title, st.CreatedMilestoneIID)
	}
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "again")); code != http.StatusConflict || len(fake.Milestones) != 3 {
		t.Errorf("created again: %d %v", code, out)
	}
}

func TestPlanMilestoneDraftRefusals(t *testing.T) {
	env, fake := planSteerer(t)
	withDrafts(t, env) // key 1 is %41, then keys 2 and 4
	for name, c := range map[string]struct {
		body  map[string]any
		code  int
		error string
	}{
		"created":   {draftBody(1, "x"), http.StatusConflict, "draft 1 is already created as %41"},
		"out of":    {draftBody(4, "x"), http.StatusConflict, "create draft 2 first"},
		"unknown":   {draftBody(3, "x"), http.StatusNotFound, ""},
		"forward":   {draftBody(2, "after %draft-4"), http.StatusUnprocessableEntity, "%draft-4 is not created yet; create it first or remove the reference"},
		"self":      {draftBody(2, "see %draft-2"), http.StatusUnprocessableEntity, "%draft-2 is not created yet; create it first or remove the reference"},
		"no key":    {draftBody(2, "%draft-9 and %draft-4, %draft-9"), http.StatusUnprocessableEntity, "%draft-9, %draft-4 are not created yet; create them first or remove the references"},
		"title ref": {map[string]any{"key": 2, "title": "After %draft-4", "description": "x"}, http.StatusUnprocessableEntity, "%draft-4 is not created yet; create it first or remove the reference"},
	} {
		if code, out := env.postJSON(t, newPlanMilestone, c.body); code != c.code || c.error != "" && out["error"] != c.error {
			t.Errorf("%s: %d %v, want %d %q", name, code, out, c.code, c.error)
		}
	}
	if st := env.getPlan(t, "shop-plan-new").Status; len(fake.Milestones) != 0 || st.Drafts[1].CreatedMilestoneIID != 0 || st.Drafts[1].Revision != 3 {
		t.Errorf("created %+v, status %+v", fake.Milestones, st)
	}
	// Key 0 is the first draft without a milestone: %draft-1 becomes %41.
	if code, out := env.postJSON(t, newPlanMilestone, map[string]any{"title": "Two", "description": "builds on %draft-1."}); code != http.StatusCreated {
		t.Fatalf("key 0: %d %v", code, out)
	}
	st := env.getPlan(t, "shop-plan-new").Status
	if d := st.Drafts[1]; d.Key != 2 || d.Draft != "builds on %41." || d.CreatedMilestoneIID != 1 || !strings.HasPrefix(fake.Milestones[0].Description, "builds on %41.\n") {
		t.Errorf("draft 2: %+v, created %q", d, fake.Milestones[0].Description)
	}
	if st.Title == "Two" || st.CreatedMilestoneIID != 41 || st.Phase == aiv1.AgentPlanCreated {
		t.Errorf("not the first created draft: %+v", st)
	}
}

// A GitLab failure leaves the draft without a milestone and the plan active.
func TestPlanMilestoneDraftGitLabRefuses(t *testing.T) {
	env, fake := planSteerer(t)
	withDrafts(t, env)
	fake.CreateMilestoneError = &gitlab.Error{StatusCode: http.StatusInternalServerError, Method: "POST", Path: "milestones"}
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "two")); code != http.StatusBadGateway {
		t.Fatalf("create: %d %v", code, out)
	}
	pl := env.getPlan(t, "shop-plan-new")
	if d := pl.Status.Drafts[1]; d.CreatedMilestoneIID != 0 || d.Draft != "- **R1** a\n- **R2** b\n" || d.Revision != 3 || !pl.Active() || len(pl.Status.Drafts) != 3 {
		t.Errorf("status %+v", pl.Status)
	}
	fake.CreateMilestoneError = nil
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "two")); code != http.StatusCreated {
		t.Errorf("after the failure: %d %v", code, out)
	}
}

// Of two creates of the same draft at once, one wins and one gets 409.
func TestPlanMilestoneDraftConcurrent(t *testing.T) {
	env, fake := planSteerer(t)
	withDrafts(t, env)
	b, _ := json.Marshal(draftBody(2, "two"))
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			req, _ := http.NewRequest(http.MethodPost, env.srv.URL+newPlanMilestone, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			if resp, err := env.client.Do(req); err == nil {
				codes[i] = resp.StatusCode
				resp.Body.Close()
			}
		})
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{http.StatusCreated, http.StatusConflict}) || len(fake.Milestones) != 1 {
		t.Errorf("codes %v, %d milestones", codes, len(fake.Milestones))
	}
}

// A draft being created is read-only and shows as creating, without a link.
func TestPlanDraftBeingCreated(t *testing.T) {
	env, _ := planSteerer(t)
	withDrafts(t, env)
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Drafts[1].CreatedMilestoneIID = draftCreating
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	var v PlanView
	if code := env.get(t, shopPlans+"/shop-plan-new", &v); code != http.StatusOK || !v.Drafts[1].Creating || v.Drafts[1].CreatedMilestoneIID != 0 ||
		v.Drafts[0].Creating || v.DraftsCreated != 1 || len(v.CreatedMilestones) != 1 {
		t.Fatalf("view: %d %+v", code, v)
	}
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "two")); code != http.StatusConflict || out["error"] != "draft 2 is being created" {
		t.Errorf("create: %d %v", code, out)
	}
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(4, "four")); code != http.StatusConflict || out["error"] != "create draft 2 first" {
		t.Errorf("create the next: %d %v", code, out)
	}
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{Key: 2, BaseRevision: 3, Draft: "x"}, &map[string]any{}); code != http.StatusConflict {
		t.Errorf("edit: %d", code)
	}
}

func TestResolveDraftRefs(t *testing.T) {
	drafts := []aiv1.PlanDraft{{Key: 1, CreatedMilestoneIID: 41}, {Key: 2, CreatedMilestoneIID: draftCreating}, {Key: 3}, {Key: 12, CreatedMilestoneIID: 50}}
	for _, c := range []struct {
		text, want string
		refs       []string
	}{
		{"builds on %draft-1.", "builds on %41.", nil},
		{"%draft-12 and %draft-1, (%draft-1)", "%50 and %41, (%41)", nil},
		{"%draft-1x %draft-1_ %draft- draft-1 %draft-1", "%draft-1x %draft-1_ %draft- draft-1 %41", nil},
		{"%draft-2 %draft-3 %draft-3 %draft-7", "%draft-2 %draft-3 %draft-3 %draft-7", []string{"%draft-2", "%draft-3", "%draft-7"}},
		{"self %draft-3", "self %draft-3", []string{"%draft-3"}},
		{"%draft-99999999999999999999", "%draft-99999999999999999999", []string{"%draft-99999999999999999999"}},
	} {
		got, refs := resolveDraftRefs(c.text, drafts, 3)
		if got != c.want || !slices.Equal(refs, c.refs) {
			t.Errorf("%q: %q %v, want %q %v", c.text, got, refs, c.want, c.refs)
		}
	}
}
