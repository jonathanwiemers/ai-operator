package ui

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const shopDecisions = "/api/v1/projects/team-a/shop/decisions"

// fakeDecisions returns rows and records the queries.
type fakeDecisions struct {
	rows    []store.DecisionResult
	queries []store.DecisionQuery
}

func (f *fakeDecisions) Decisions(_ context.Context, q store.DecisionQuery) ([]store.DecisionResult, error) {
	f.queries = append(f.queries, q)
	return f.rows, nil
}

// decisionEnvFake is a store with D42-1 of the open issue 42 in milestone 3,
// revisited by a comment.
func decisionEnvFake() (store.DecisionResult, *fakeDecisions) {
	d := store.DecisionResult{DecisionRow: store.DecisionRow{MilestoneIID: 3, ID: "D42-1",
		IssueIID: 42, Kind: store.DecisionImplementer, Title: "Currency", Question: "Which currency?", Choice: "EUR",
		Why: "Shop is in the EU", IssueTitle: "Add PayPal", IssueState: "opened", PostedAt: time.Now()},
		Revisits: []store.RevisitRequestRow{{DecisionID: "D42-1", Source: store.RevisitComment, RequestedBy: "ada",
			Instruction: "use USD"}}}
	return d, &fakeDecisions{rows: []store.DecisionResult{d}}
}

// decisionEnv is planEnv with the roles and decisionEnvFake's store.
func decisionEnv(t *testing.T, roles ...string) (*testEnv, *fakeDecisions) {
	t.Helper()
	env := planEnv(t, true, roles...)
	_, fake := decisionEnvFake()
	env.server.decisionReads = fake
	return env, fake
}

func TestDecisionsAuthz(t *testing.T) {
	env, fake := decisionEnv(t, "devs")
	var got DecisionsResponse
	if code := env.get(t, shopDecisions, &got); code != http.StatusOK {
		t.Fatalf("viewer: status %d", code)
	}
	if !got.Available || len(got.Decisions) != 1 {
		t.Fatalf("viewer: %+v", got)
	}
	d := got.Decisions[0]
	if d.ID != "D42-1" || d.Issue.IID != 42 || d.Issue.Title != "Add PayPal" || d.Issue.State != "opened" ||
		d.Issue.Task != "shop-42-add-paypal" || d.Alternatives == nil || len(d.Revisits) != 1 || d.Revisits[0].Instruction != "use USD" {
		t.Errorf("view: %+v", d)
	}
	// A viewer is no owner: the text, but no mode.
	if d.Revisit.Mode != "" || d.Revisit.Task != "" || !strings.HasPrefix(d.Revisit.Text, "Comment this command on #42") {
		t.Errorf("viewer's revisit: %+v", d.Revisit)
	}
	if code := env.get(t, "/api/v1/projects/team-b/billing/decisions", nil); code != http.StatusNotFound {
		t.Errorf("other project: status %d, want 404", code)
	}

	stranger, fake2 := decisionEnv(t)
	if code := stranger.get(t, shopDecisions, nil); code != http.StatusNotFound {
		t.Errorf("stranger: status %d, want 404", code)
	}
	if len(fake.queries) != 1 || len(fake2.queries) != 0 {
		t.Errorf("queries %d and %d, want 1 and 0", len(fake.queries), len(fake2.queries))
	}
}

func TestDecisionsOwnerGetsTheMode(t *testing.T) {
	for _, steer := range []bool{true, false} {
		env, _ := newSteeringEnv(t, Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Chat: true, Steer: steer})
		withChat(env.server.cfg)
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
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
		env.login(t, "/")
		_, fake := decisionEnvFake()
		env.server.decisionReads = fake

		var got DecisionsResponse
		if code := env.get(t, shopDecisions+"?milestone=3", &got); code != http.StatusOK || len(got.Decisions) != 1 {
			t.Fatalf("steer %v: status %d, %+v", steer, code, got)
		}
		want := RevisitView{Mode: RevisitModeComment, Task: "shop-42-add-paypal",
			Text: "Comments on #42. Its next run changes the code and posts a decision that supersedes D42-1."}
		if !steer {
			want = RevisitView{Mode: RevisitModeCommand, Command: "/agent revisit D42-1 ",
				Text: "Comment this command on #42 or the tracking issue."}
		}
		if r := got.Decisions[0].Revisit; r != want {
			t.Errorf("steer %v: revisit %+v, want %+v", steer, r, want)
		}
	}
}

func TestDecisionsQuery(t *testing.T) {
	env, fake := decisionEnv(t, "devs")
	long := strings.Repeat("ä", maxDecisionQuery+5)
	cases := []struct {
		query string
		want  store.DecisionQuery
	}{
		{"", store.DecisionQuery{}},
		{"?milestone=0&state=all", store.DecisionQuery{State: "all"}},
		{"?milestone=3&q=+currency+&state=superseded", store.DecisionQuery{Milestone: 3, Q: "currency", State: "superseded"}},
		{"?state=active&q=" + long, store.DecisionQuery{State: "active", Q: strings.Repeat("ä", maxDecisionQuery)}},
	}
	for _, tc := range cases {
		if code := env.get(t, shopDecisions+tc.query, nil); code != http.StatusOK {
			t.Fatalf("%q: status %d", tc.query, code)
		}
		got := fake.queries[len(fake.queries)-1]
		tc.want.Project = store.ProjectKey{Namespace: "team-a", Name: "shop"}
		if got != tc.want {
			t.Errorf("%q: query %+v, want %+v", tc.query, got, tc.want)
		}
	}
	n := len(fake.queries)
	for _, q := range []string{"?milestone=x", "?milestone=-1", "?milestone=1.5"} {
		if code := env.get(t, shopDecisions+q, nil); code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, code)
		}
	}
	if len(fake.queries) != n {
		t.Error("a bad milestone reached the store")
	}
}

func TestDecisionsWithoutDatabase(t *testing.T) {
	env, _ := decisionEnv(t, "devs")
	env.server.decisionReads = nil
	code, body := env.getJSON(t, shopDecisions)
	if code != http.StatusOK || body["available"] != false {
		t.Fatalf("status %d, body %v", code, body)
	}
	if ds, ok := body["decisions"].([]any); !ok || len(ds) != 0 {
		t.Errorf("decisions %v, want []", body["decisions"])
	}
}

func TestRevisitOf(t *testing.T) {
	task := func(name string) *aiv1.AgentTask {
		return &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}
	pilot, impl := task("shop-pilot-m3"), task("shop-42-add-paypal")
	base := func(state string) store.DecisionResult {
		return store.DecisionResult{DecisionRow: store.DecisionRow{ID: "D42-3", IssueIID: 42, IssueState: state}}
	}
	with := func(d store.DecisionResult, f func(*store.DecisionResult)) store.DecisionResult {
		f(&d)
		return d
	}
	followUp := func(state string, applied bool) func(*store.DecisionResult) {
		return func(d *store.DecisionResult) {
			d.Revisits = []store.RevisitRequestRow{{Source: store.RevisitFollowUp, FollowUpIID: 57, FollowUpState: state, Applied: applied}}
		}
	}
	ms := milestoneTaskSet{pilot: pilot, implement: impl, autopilot: true, ready: "agent::go", prefix: "/bot"}
	noPilot := ms
	noPilot.pilot = nil
	manual := ms
	manual.autopilot = false
	stored := milestoneTaskSet{ready: "agent::go", prefix: "/bot"}

	command := RevisitView{Mode: RevisitModeCommand, Command: "/bot revisit D42-3 ",
		Text: "Comment this command on #42 or the tracking issue."}
	comment := RevisitView{Mode: RevisitModeComment, Task: impl.Name,
		Text: "Comments on #42. Its next run changes the code and posts a decision that supersedes D42-3."}
	cases := []struct {
		name         string
		d            store.DecisionResult
		ms           milestoneTaskSet
		owner, steer bool
		want         RevisitView
	}{
		{"superseded", with(base("closed"), func(d *store.DecisionResult) { d.Superseded, d.SupersededBy = true, "D57-1" }), ms, true, true,
			RevisitView{Mode: RevisitModeNone, Text: "Already replaced by D57-1."}},
		{"superseded without successor", with(base("closed"), func(d *store.DecisionResult) { d.Superseded = true }), ms, true, true,
			RevisitView{Mode: RevisitModeNone, Text: "Already replaced."}},
		{"open follow-up", with(base("closed"), followUp("opened", false)), ms, true, true,
			RevisitView{Mode: RevisitModeNone, Text: "#57 already revisits D42-3; comment there."}},
		{"none for a non-owner too", with(base("closed"), followUp("opened", false)), ms, false, false,
			RevisitView{Mode: RevisitModeNone, Text: "#57 already revisits D42-3; comment there."}},
		{"closed follow-up", with(base("closed"), followUp("closed", false)), ms, true, true,
			RevisitView{Mode: RevisitModePrompt, Task: pilot.Name, Text: "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, " +
				"queued after the planned issues. D42-3 is marked superseded when the follow-up posts its new decision."}},
		{"prompt on autopilot", base("closed"), ms, true, false,
			RevisitView{Mode: RevisitModePrompt, Task: pilot.Name, Text: "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, " +
				"queued after the planned issues. D42-3 is marked superseded when the follow-up posts its new decision."}},
		{"prompt without autopilot", base("closed"), manual, true, false,
			RevisitView{Mode: RevisitModePrompt, Task: pilot.Name, Text: "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, " +
				"and the follow-up starts right away (agent::go). D42-3 is marked superseded when the follow-up posts its new decision."}},
		{"closed without pilot", base("closed"), noPilot, true, true, command},
		{"comment", base("opened"), ms, true, true, comment},
		{"comment without steer", base("opened"), ms, true, false, command},
		{"open without implement task", base("opened"), stored, true, true, command},
		{"stored milestone", base("closed"), stored, true, true, command},
		{"non-owner prompt", base("closed"), ms, false, true, RevisitView{Text: "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, " +
			"queued after the planned issues. D42-3 is marked superseded when the follow-up posts its new decision."}},
		{"non-owner comment", base("opened"), ms, false, true, RevisitView{Text: comment.Text}},
		{"non-owner command", base("opened"), ms, false, false, RevisitView{Text: command.Text, Command: command.Command}},
	}
	for _, tc := range cases {
		if got := revisitOf(tc.d, tc.ms, tc.owner, tc.steer); got != tc.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

func TestMilestoneTaskSetOf(t *testing.T) {
	tasks := []aiv1.AgentTask{
		{ObjectMeta: metav1.ObjectMeta{Name: "spec"}, Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Autopilot: true,
			Milestone: &aiv1.MilestoneRef{IID: 3}}, Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pilot"}, Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{IID: 3}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "done"}, Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 41}},
			Status: aiv1.AgentTaskStatus{Phase: aiv1.TaskPhaseArchived}},
		{ObjectMeta: metav1.ObjectMeta{Name: "live"}, Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 42}},
			Status: aiv1.AgentTaskStatus{Phase: aiv1.TaskPhaseHibernated}},
	}
	if mt := milestoneTaskSetOf(tasks, 3); mt.spec.Name != "spec" || mt.pilot.Name != "pilot" || !mt.autopilot {
		t.Errorf("milestone 3: %+v", mt)
	}
	tasks[0].Status.Autopilot.Phase = aiv1.AutopilotOff
	if mt := milestoneTaskSetOf(tasks, 3); mt.autopilot {
		t.Error("autopilot off counts as on")
	}
	if mt := milestoneTaskSetOf(tasks, 4); mt.spec != nil || mt.pilot != nil || mt.autopilot {
		t.Errorf("milestone 4: %+v", mt)
	}
	if liveIssueTask(tasks, 41) != nil || liveIssueTask(tasks, 42).Name != "live" {
		t.Error("live implement task: an archived task counts, or a hibernated one does not")
	}
}
