package ui

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// secretMarker is in the names and titles of the project ns/p, which the
// route test's user may not see.
const secretMarker = "zq7"

// routeValues fills a route pattern's wildcards with ns/p and its task,
// milestone, issue, plan and draft.
var routeValues = strings.NewReplacer("{namespace}", "ns", "{name}", "p", "{task}", "p-42-zq7",
	"{iid}", "3", "{issue}", "42", "{id}", "1", "{plan}", "p-plan-zq7", "{key}", "1")

// routeBodies are bodies a write route would take, so a route without a
// check gets past its validation and answers 2xx.
var routeBodies = map[string]string{
	"POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followup": `{"title":"zq7","followUps":[1]}`,
	"POST /api/v1/researches": `{"project":"p","prompt":"zq7"}`,
}

// storeSpy is every history reader the Desk has; it records the projects
// it is asked about and answers nothing.
type storeSpy struct {
	mu    sync.Mutex
	asked []store.ProjectKey
}

func (f *storeSpy) ask(keys ...store.ProjectKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, keys...)
}

// askedFor reports whether ns/p was asked about since the last call.
func (f *storeSpy) askedFor() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := slices.Contains(f.asked, store.ProjectKey{Namespace: "ns", Name: "p"})
	f.asked = nil
	return asked
}

func (f *storeSpy) CostSeries(_ context.Context, q store.CostQuery, _, _ string) ([]store.CostPoint, error) {
	f.ask(q.Projects...)
	return nil, nil
}

func (f *storeSpy) CostBreakdowns(_ context.Context, q store.CostQuery) (store.CostBreakdowns, error) {
	f.ask(q.Projects...)
	return store.CostBreakdowns{}, nil
}

func (f *storeSpy) CostTop(_ context.Context, q store.CostQuery, _ int) (store.CostTop, error) {
	f.ask(q.Projects...)
	return store.CostTop{}, nil
}

func (f *storeSpy) Metrics(_ context.Context, q store.CostQuery) (store.Metrics, error) {
	f.ask(q.Projects...)
	return store.Metrics{}, nil
}

func (f *storeSpy) PausesInRange(_ context.Context, q store.CostQuery, _ string, _ time.Time) (store.PauseRange, error) {
	f.ask(q.Projects...)
	return store.PauseRange{}, nil
}

func (f *storeSpy) FlowInRange(_ context.Context, q store.CostQuery, _ string, _ time.Time) (store.FlowRange, error) {
	f.ask(q.Projects...)
	return store.FlowRange{}, nil
}

func (f *storeSpy) CodeInRange(_ context.Context, q store.CostQuery, _ string) (store.CodeRange, error) {
	f.ask(q.Projects...)
	return store.CodeRange{}, nil
}

func (f *storeSpy) TimelineEvents(_ context.Context, q store.TimelineQuery, _ time.Time) ([]store.Event, bool, error) {
	f.ask(q.Projects...)
	return nil, false, nil
}

func (f *storeSpy) Milestones(_ context.Context, q store.MilestoneQuery) ([]store.MilestoneRow, error) {
	f.ask(q.Projects...)
	return nil, nil
}

func (f *storeSpy) SpendSince(_ context.Context, projects []store.ProjectKey, _ time.Time) (store.SpendTotals, error) {
	f.ask(projects...)
	return store.SpendTotals{}, nil
}

func (f *storeSpy) Milestone(_ context.Context, project store.ProjectKey, _ int64, _ time.Time) (store.MilestoneDetail, error) {
	f.ask(project)
	return store.MilestoneDetail{}, nil
}

func (f *storeSpy) IssueFigures(_ context.Context, project store.ProjectKey, _, _ int64, _ time.Time) (store.IssueFigures, error) {
	f.ask(project)
	return store.IssueFigures{}, nil
}

func (f *storeSpy) Pauses(_ context.Context, project store.ProjectKey, _ int64, _ time.Time) (store.PauseHistory, error) {
	f.ask(project)
	return store.PauseHistory{}, nil
}

func (f *storeSpy) Spend(_ context.Context, sc store.SpendScope) (store.Spend, error) {
	f.ask(store.ProjectKey{Namespace: sc.Namespace, Name: sc.Project})
	return store.Spend{}, nil
}

func (f *storeSpy) Decisions(_ context.Context, q store.DecisionQuery) ([]store.DecisionResult, error) {
	f.ask(q.Project)
	return nil, nil
}

func (f *storeSpy) MilestoneSpendHistory(_ context.Context, project store.ProjectKey, _ int) ([]store.MilestoneSpend, error) {
	f.ask(project)
	return nil, nil
}

func (f *storeSpy) MilestoneRequirements(_ context.Context, key store.ProjectKey, _ int64) (int, error) {
	f.ask(key)
	return 0, nil
}

func (f *storeSpy) PlanDecisionHistory(_ context.Context, namespace, project, _ string) ([]store.PlanDecisionVersion, error) {
	f.ask(store.ProjectKey{Namespace: namespace, Name: project})
	return nil, nil
}

// secretObjects are the project ns/p with a milestone, an issue task, a plan
// of the user's, and a set whose name the user may see but whose child ns/p
// they may not.
func secretObjects() []client.Object {
	ms := &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "zq7 milestone"}
	return []client.Object{
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p"},
			Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/zq7"},
				Research: &aiv1.ProjectResearch{WriterSecret: &aiv1.SecretLogin{Name: "writer"}}}},
		&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p-spec-m3-zq7"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "p", Type: aiv1.TaskTypeSpec, Milestone: ms},
			Status: aiv1.AgentTaskStatus{Plan: []aiv1.PlannedIssue{{Key: "a", IID: 42, Title: "zq7 issue"}}}},
		&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p-42-zq7"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "p", Type: aiv1.TaskTypeImplement, Milestone: ms,
				Issue: &aiv1.IssueRef{IID: 42, Title: "zq7 issue"}},
			Status: aiv1.AgentTaskStatus{Phase: aiv1.TaskPhaseRunning, PodName: "p-42-zq7"}},
		&aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p-plan-zq7"},
			Spec:   aiv1.AgentPlanSpec{ProjectRef: "p", Title: "zq7 plan", CreatedBy: "ada@example.com"},
			Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle, Draft: "# zq7"}},
		&aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "visible"},
			Status: aiv1.AgentProjectSetStatus{Projects: []aiv1.ProjectSetProjectStatus{
				{Path: "acme/zq7", State: aiv1.ProjectSetActive, Child: "p"}}}},
	}
}

// noGrantEnv is a Desk with every feature on whose user may do everything on
// ns/visible and other/*, and nothing on ns/p. The research write grant is
// on other/*: one in research.namespace (ns) lets the user start researches
// with that namespace's projects, ns/p among them, without a project grant
// (by design, see the research guide).
func noGrantEnv(t *testing.T) (*testEnv, *storeSpy) {
	t.Helper()
	cfg := withChat(oidcConfig(
		Rule{Roles: []string{"devs"}, Projects: []string{"ns/visible", "other/*"},
			Transcript: true, Steer: true, Conversation: true, Chat: true},
		Rule{Roles: []string{"devs"}, Research: ResearchWrite, Researches: []string{"other/*"}},
	))
	cfg.Research.Namespace, cfg.Research.AllowCreate = "ns", true
	cfg.GitLab = GitLabConfig{URL: "https://gitlab.example.com", ClientID: "gitlab-client"}
	env := newTestEnv(t, cfg)
	for _, o := range secretObjects() {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	spy := &storeSpy{}
	s := env.server
	s.costReads, s.milestoneRows, s.overviewSpend, s.milestoneDetails, s.pauses = spy, spy, spy, spy, spy
	s.retroSpend, s.decisionReads, s.estimateReads, s.planDecisionReads = spy, spy, spy, spy
	s.SetResearchWriter(env.reader)
	s.SetLogSource(fakeLogs{"ns/p-42-zq7/runner": "zq7 log\n", "ns/p-42-zq7/agent": "zq7 log\n"})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	return env, spy
}

// call sends method path with body and returns the status and up to the
// first second of the response.
func (e *testEnv) call(t *testing.T, method, path, body string, header http.Header) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	c := *e.client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body) // a stream ends with the deadline
	return resp.StatusCode, string(b)
}

// A user without a grant for ns/p gets no 2xx on any route of ns/p, and no
// data of ns/p from the routes without a project in the path; every write
// route rejects cross-site requests. Routes come from the route table, so a
// new route falls under this test.
func TestRoutesDenyProjectWithoutGrant(t *testing.T) {
	env, spy := noGrantEnv(t)
	// Events of ns/p, published while /api/v1/events streams.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				env.hub.Publish(Event{Kind: "task", Namespace: "ns", Name: "p-42-zq7", Project: "p"})
			}
		}
	}()
	leaks := func(body string) bool {
		return strings.Contains(body, secretMarker) || strings.Contains(body, `"ns/p"`) ||
			strings.Contains(body, `"name":"p"`) || strings.Contains(body, `"child":"p"`)
	}
	routes := env.server.routes()
	if len(routes) < 60 {
		t.Fatalf("only %d routes: is a feature off in the test config?", len(routes))
	}
	for _, rt := range routes {
		method, pattern, _ := strings.Cut(rt.pattern, " ")
		path := routeValues.Replace(pattern)
		// A route of ns/p: ns/p is in its path or its body.
		scoped := strings.Contains(pattern, "{namespace}") || routeBodies[rt.pattern] != ""
		body := routeBodies[rt.pattern]
		if body == "" {
			body = `{"body":"zq7","target":"issue","title":"zq7"}`
		}
		if method != http.MethodGet {
			code, got := env.call(t, method, path, body, http.Header{"Sec-Fetch-Site": {"cross-site"}})
			if code != http.StatusForbidden || !strings.Contains(got, "cross-origin") {
				t.Errorf("%s: a cross-site request answers %d %q, want the cross-origin 403", rt.pattern, code, got)
			}
		}
		queries := []string{""}
		if !scoped {
			// Global routes, also with the filters some of them take.
			queries = append(queries, "?q=zq7&project=ns/p", "?namespace=ns&project=p&task=p-42-zq7&q=zq7")
		}
		for _, q := range queries {
			code, got := env.call(t, method, path+q, body, nil)
			switch {
			case scoped && code >= 200 && code < 300:
				t.Errorf("%s %s: status %d without a grant: %.300s", method, path, code, got)
			case code >= 200 && code < 300 && leaks(got):
				t.Errorf("%s %s%s: data of ns/p: %.300s", method, path, q, got)
			}
			if spy.askedFor() {
				t.Errorf("%s %s%s: asked the history store about ns/p", method, path, q)
			}
		}
	}
}
