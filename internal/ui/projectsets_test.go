package ui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func testProjectSet(namespace, name string) *aiv1.AgentProjectSet {
	discovered := metav1.NewTime(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	return &aiv1.AgentProjectSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: aiv1.AgentProjectSetSpec{
			GitLab:       aiv1.ProjectSetGitLab{URL: "https://gitlab.example.com", Group: "acme"},
			Include:      []string{"acme/**"},
			Exclude:      []string{"acme/archived/**"},
			PollInterval: metav1.Duration{Duration: 2 * time.Minute},
		},
		Status: aiv1.AgentProjectSetStatus{
			LastDiscoveryTime: &discovered,
			Counts:            aiv1.ProjectSetCounts{Active: 1, Excluded: 1},
			Projects: []aiv1.ProjectSetEntry{
				{Path: "acme/web", State: aiv1.ProjectSetActive, Child: "web"},
				{Path: "acme/archived/old", State: aiv1.ProjectSetExcluded, Reason: "matches exclude acme/archived/**"},
			},
			DroppedTargets: []string{"acme/archived/old"},
			Conditions:     []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Discovered"}},
		},
	}
}

func createAll(t *testing.T, env *testEnv, objs ...client.Object) {
	t.Helper()
	for _, o := range objs {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectSetsVisibleAndMapped(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"shop-devs"}, Projects: []string{"team-a/*"}}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-devs": map[string]any{"1": "acme"}}}
	createAll(t, env, testProjectSet("team-b", "acme"), testProjectSet("team-a", "acme"))
	env.login(t, "/")

	var sets []ProjectSetSummary
	if code := env.get(t, "/api/v1/projectsets", &sets); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(sets) != 1 || sets[0].Namespace != "team-a" || sets[0].Name != "acme" {
		t.Fatalf("sets = %+v", sets)
	}
	s := sets[0]
	if s.Group != "acme" || len(s.Include) != 1 || len(s.Exclude) != 1 || s.PollInterval != "2m" {
		t.Errorf("spec fields = %+v", s)
	}
	if s.LastDiscoveryTime == nil || s.Counts.Active != 1 || s.Counts.Excluded != 1 {
		t.Errorf("status fields = %+v", s)
	}
	if len(s.Projects) != 2 || s.Projects[0].Child != "web" || s.Projects[1].State != aiv1.ProjectSetExcluded {
		t.Errorf("projects = %+v", s.Projects)
	}
	if len(s.DroppedTargets) != 1 || len(s.Conditions) != 1 {
		t.Errorf("dropped %v, conditions %v", s.DroppedTargets, s.Conditions)
	}
}

func TestProjectSetsSorted(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	createAll(t, env, testProjectSet("team-b", "acme"), testProjectSet("team-a", "zeta"), testProjectSet("team-a", "acme"))
	var sets []ProjectSetSummary
	env.get(t, "/api/v1/projectsets", &sets)
	var got []string
	for _, s := range sets {
		got = append(got, s.Namespace+"/"+s.Name)
	}
	if strings.Join(got, ",") != "team-a/acme,team-a/zeta,team-b/acme" {
		t.Errorf("order = %v", got)
	}
}

func getBody(t *testing.T, env *testEnv, path string) (int, string) {
	t.Helper()
	resp, err := env.client.Get(env.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func TestProjectSetsEmpty(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	if code, body := getBody(t, env, "/api/v1/projectsets"); code != http.StatusOK || body != "[]" {
		t.Errorf("status %d, body %q", code, body)
	}
}

func TestProjectSetsWithoutCRD(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	env.server.reader = interceptor.NewClient(env.reader.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*aiv1.AgentProjectSetList); ok {
				return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: aiv1.GroupVersion.Group, Kind: "AgentProjectSet"}}
			}
			return c.List(ctx, list, opts...)
		},
	})
	if code, body := getBody(t, env, "/api/v1/projectsets"); code != http.StatusOK || body != "[]" {
		t.Errorf("status %d, body %q", code, body)
	}
}

func TestProjectsSetName(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	controller := true
	owner := metav1.OwnerReference{APIVersion: aiv1.GroupVersion.String(), Kind: "AgentProjectSet", Name: "acme", UID: "u1", Controller: &controller}
	child := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{
		Namespace: "team-a", Name: "acme-web",
		Labels:          map[string]string{aiv1.LabelProjectSet: "acme"},
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	labelOnly := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{
		Namespace: "team-a", Name: "acme-labelled",
		Labels: map[string]string{aiv1.LabelProjectSet: "acme"},
	}}
	other := owner
	other.Name = "other"
	wrongOwner := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{
		Namespace: "team-a", Name: "acme-other",
		Labels:          map[string]string{aiv1.LabelProjectSet: "acme"},
		OwnerReferences: []metav1.OwnerReference{other},
	}}
	createAll(t, env, child, labelOnly, wrongOwner)

	var projects []ProjectSummary
	if code := env.get(t, "/api/v1/projects", &projects); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	got := map[string]string{}
	for _, p := range projects {
		got[p.Name] = p.Set
	}
	want := map[string]string{"acme-web": "acme", "acme-labelled": "", "acme-other": "", "shop": "", "billing": ""}
	for name, set := range want {
		if s, ok := got[name]; !ok || s != set {
			t.Errorf("%s: set %q (listed %v), want %q", name, s, ok, set)
		}
	}
}
