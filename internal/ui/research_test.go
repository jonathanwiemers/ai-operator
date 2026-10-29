package ui

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// researchRules: news-readers read agents/news*, admins every research,
// shop-devs only the factory.
var researchRules = []Rule{
	{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
	{Roles: []string{"news-readers"}, Research: ResearchRead, Researches: []string{"agents/news*"}},
	{Roles: []string{"admins"}, Research: ResearchRead},
}

// researchEnv is a test env with research on and the AgentResearches
// agents/news (scheduled news flash), agents/report-1 (one-off) and
// team/secret. st is the store; nil leaves a zero store that must not be
// used (the endpoints answer before they reach it).
func researchEnv(t *testing.T, cfg *Config, st *store.Store) *testEnv {
	t.Helper()
	env := newTestEnv(t, withResearch(cfg, false))
	if st == nil {
		st = &store.Store{}
	}
	env.server.SetResearchStore(st)
	started := metav1.NewTime(time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC))
	for _, ar := range []*aiv1.AgentResearch{
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "news"},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "shop", Prompt: "News", View: aiv1.ResearchViewNewsflash,
				Depth: aiv1.ResearchDepthQuick, Schedule: "0 7 * * *", TimeZone: "Europe/Berlin"},
			Status: aiv1.AgentResearchStatus{Phase: aiv1.ResearchPhaseScheduled, LastRun: &started, LastModel: "claude-sonnet-5-5",
				LastEngine: aiv1.EngineClaude, Run: &aiv1.ResearchRunStatus{ID: "r1", StartedAt: &started, Engine: aiv1.EngineClaude,
					Model: "claude-sonnet-5-5", Reason: "chosen: claude/claude-sonnet-5-5", Outcome: aiv1.ResearchOutcomeSucceeded,
					EngineUsage: []aiv1.EngineUsage{{Engine: aiv1.EngineClaude}}}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "report-1",
				Annotations: map[string]string{aiv1.AnnotationCreatedBy: "ada@example.com"}},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "shop", Prompt: "Why?", View: aiv1.ResearchViewReport, Depth: aiv1.ResearchDepthDeep},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "secret"},
			Spec:       aiv1.AgentResearchSpec{ProjectRef: "billing", Prompt: "Secret", View: aiv1.ResearchViewReport},
		},
	} {
		if err := env.reader.Create(context.Background(), ar); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func loginWithRoles(t *testing.T, env *testEnv, roles ...string) {
	t.Helper()
	m := map[string]any{}
	for _, r := range roles {
		m[r] = map[string]any{"1": "acme"}
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": m}
	env.login(t, "/")
}

func researchNames(list []ResearchSummary) []string {
	var out []string
	for _, r := range list {
		out = append(out, r.Namespace+"/"+r.Name)
	}
	return out
}

func TestResearchAccess(t *testing.T) {
	run := uuid.NewString()
	cases := []struct {
		name  string
		roles []string
		list  []string // nil: the list answers 404
		// hidden are not found on every endpoint: researches the user may
		// not read and ones that do not exist. Readable ones need the store
		// (the zero store here); TestResearchResults covers them.
		hidden []string
	}{
		{"factory only", []string{"shop-devs"}, nil,
			[]string{"agents/news", "agents/report-1", "team/secret", "team/missing"}},
		{"no rule", []string{"other"}, nil, []string{"agents/news", "team/secret"}},
		{"glob", []string{"news-readers"}, []string{"agents/news"},
			[]string{"agents/report-1", "team/secret", "team/news", "agents/news-missing"}},
		{"every research", []string{"admins", "shop-devs"}, []string{"agents/news", "agents/report-1", "team/secret"},
			[]string{"agents/missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := researchEnv(t, oidcConfig(researchRules...), nil)
			loginWithRoles(t, env, tc.roles...)
			var list []ResearchSummary
			code := env.get(t, "/api/v1/researches", &list)
			if tc.list == nil {
				if code != http.StatusNotFound {
					t.Errorf("list: status %d, want 404", code)
				}
				if code := env.get(t, "/api/v1/research/search?q=news", nil); code != http.StatusNotFound {
					t.Errorf("search: status %d, want 404", code)
				}
			} else if code != http.StatusOK || !slices.Equal(researchNames(list), tc.list) {
				t.Errorf("list: status %d, researches %v, want %v", code, researchNames(list), tc.list)
			}
			for _, key := range tc.hidden {
				base := "/api/v1/researches/" + key
				for _, path := range []string{base, base + "/runs", base + "/runs/" + run} {
					if code := env.get(t, path, nil); code != http.StatusNotFound {
						t.Errorf("%s: status %d, want 404", path, code)
					}
				}
			}
		})
	}
}

func TestResearchSummaries(t *testing.T) {
	env := researchEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}}, nil)
	var list []ResearchSummary
	if code := env.get(t, "/api/v1/researches", &list); code != http.StatusOK || len(list) != 3 {
		t.Fatalf("status %d, %d researches", code, len(list))
	}
	news, report := list[0], list[1]
	if news.View != aiv1.ResearchViewNewsflash || news.Schedule != "0 7 * * *" || news.TimeZone != "Europe/Berlin" ||
		news.OneOff || news.Phase != aiv1.ResearchPhaseScheduled || news.LastModel != "claude-sonnet-5-5" ||
		news.Run == nil || news.Run.Reason != "chosen: claude/claude-sonnet-5-5" || news.Run.Outcome != aiv1.ResearchOutcomeSucceeded {
		t.Errorf("news = %+v", news)
	}
	if !report.OneOff || report.CreatedBy != "ada@example.com" || report.Depth != aiv1.ResearchDepthDeep || report.Run != nil {
		t.Errorf("report-1 = %+v", report)
	}
	// The run line leaves out the usage readings.
	resp, err := env.client.Get(env.srv.URL + "/api/v1/researches")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if run, _ := raw[0]["run"].(map[string]any); run == nil || run["engineUsage"] != nil {
		t.Errorf("run line = %v", raw[0]["run"])
	}
}

func TestResearchOff(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	for _, path := range []string{"/api/v1/researches", "/api/v1/researches/agents/news",
		"/api/v1/researches/agents/news/runs", "/api/v1/research/search?q=x"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s without research: status %d, want 404", path, code)
		}
	}
}

func TestEventOfResearch(t *testing.T) {
	e, ok := eventOf(&aiv1.AgentResearch{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "news"}})
	if !ok || e != (Event{Kind: EventResearch, Namespace: "agents", Name: "news"}) {
		t.Errorf("event = %+v, %v", e, ok)
	}
}

func TestResearchEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []string
		want  []string // names of the events that come through
	}{
		{"glob", []string{"news-readers"}, []string{"news", "news-2"}},
		{"factory only", []string{"shop-devs"}, []string{"shop", "shop"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := researchEnv(t, oidcConfig(researchRules...), nil)
			loginWithRoles(t, env, tc.roles...)
			next := openEvents(t, env)
			for _, e := range []Event{
				{Kind: EventResearch, Namespace: "team", Name: "secret"},
				{Kind: EventResearch, Namespace: "agents", Name: "news"},
				{Kind: "project", Namespace: "team-a", Name: "shop", Project: "shop"},
				{Kind: EventResearch, Namespace: "agents", Name: "report-1"},
				// A research named like a project the user may see is still a research.
				{Kind: EventResearch, Namespace: "team-a", Name: "shop"},
				{Kind: EventResearch, Namespace: "agents", Name: "news-2"},
				{Kind: "project", Namespace: "team-a", Name: "shop", Project: "shop"},
			} {
				env.hub.Publish(e)
			}
			var got []string
			for range tc.want {
				got = append(got, next().Name)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("events %v, want %v", got, tc.want)
			}
		})
	}
}

// openEvents opens the event stream and returns a function that reads the
// next event.
func openEvents(t *testing.T, env *testEnv) func() Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.srv.URL+"/api/v1/events", nil)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
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
	return func() Event {
		var e Event
		if err := json.Unmarshal([]byte(readUntil("data: ")), &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
}

// readerStore returns the owner store of a fresh schema and a store
// connected as a role with the reader's grants.
func readerStore(t *testing.T) (owner, reader *store.Store) {
	t.Helper()
	owner = storetest.New(t)
	role := "ui_reader_" + strings.ToLower(rand.Text()[:10])
	reader = storetest.Role(t, role)
	if err := owner.Migrate(t.Context(), store.Grants{Reader: role}); err != nil {
		t.Fatal(err)
	}
	return owner, reader
}

// seedRun stores a run of namespace/name at with a news flash of urls.
func seedRun(t *testing.T, s *store.Store, namespace, name string, at time.Time, title string, urls ...string) store.Run {
	t.Helper()
	run := store.Run{ID: uuid.NewString(), Namespace: namespace, Research: name, StartedAt: at,
		Engine: "claude", Model: "claude-sonnet-5-5", ExpiresAt: at.Add(720 * time.Hour)}
	if err := s.StartRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	items := &blocks.Items{}
	for _, u := range urls {
		items.Items = append(items.Items, blocks.Item{Topic: "tech", Title: "About " + u, URL: u, Importance: 2})
	}
	res := blocks.Result{SchemaVersion: 1, Title: title, Summary: "Topics of the day", Blocks: []blocks.Block{
		{Type: blocks.TypeHeading, Heading: &blocks.Heading{Text: "Tech", Level: 2}},
		{Type: blocks.TypeItems, Items: items},
	}}
	if err := s.SaveResult(t.Context(), run.ID, res, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(t.Context(), run.ID, store.StatusSucceeded, 0.125, "", nil, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return run
}

// resultResponse is the JSON of a result with its items' new flags.
type resultResponse struct {
	RunID    string   `json:"runID"`
	Title    string   `json:"title"`
	Warnings []string `json:"warnings"`
	Blocks   []struct {
		Type  string `json:"type"`
		Items []struct {
			URL string `json:"url"`
			New bool   `json:"new"`
		} `json:"items"`
	} `json:"blocks"`
}

// newURLs returns the URLs of the result's items marked new.
func (r *resultResponse) newURLs() []string {
	var out []string
	for _, b := range r.Blocks {
		for _, it := range b.Items {
			if it.New {
				out = append(out, it.URL)
			}
		}
	}
	return out
}

func TestResearchResults(t *testing.T) {
	owner, reader := readerStore(t)
	env := researchEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}}, reader)
	day1 := time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)
	run1 := seedRun(t, owner, "agents", "news", day1, "Monday", "https://example.com/a")
	run2 := seedRun(t, owner, "agents", "news", day2, "Tuesday", "https://example.com/a", "https://example.com/b")
	other := seedRun(t, owner, "agents", "report-1", day2, "Report", "https://example.com/c")

	var detail struct {
		Research ResearchSummary `json:"research"`
		Result   *resultResponse `json:"result"`
	}
	if code := env.get(t, "/api/v1/researches/agents/news", &detail); code != http.StatusOK || detail.Result == nil {
		t.Fatalf("status %d, result %v", code, detail.Result)
	}
	if detail.Research.Name != "news" || detail.Result.RunID != run2.ID || detail.Result.Title != "Tuesday" ||
		len(detail.Result.Blocks) != 2 || detail.Result.Blocks[0].Type != blocks.TypeHeading {
		t.Errorf("detail = %+v", detail)
	}
	if got := detail.Result.newURLs(); !slices.Equal(got, []string{"https://example.com/b"}) {
		t.Errorf("new items of the latest run = %v, want only b", got)
	}
	var none struct {
		Result *resultResponse `json:"result"`
	}
	if code := env.get(t, "/api/v1/researches/team/secret", &none); code != http.StatusOK || none.Result != nil {
		t.Errorf("research without results: status %d, result %+v", code, none.Result)
	}

	var runs struct {
		Runs []ResearchRun `json:"runs"`
	}
	ids := func() []string {
		var out []string
		for _, r := range runs.Runs {
			out = append(out, r.ID)
		}
		return out
	}
	base := "/api/v1/researches/agents/news/runs"
	if code := env.get(t, base, &runs); code != http.StatusOK || !slices.Equal(ids(), []string{run2.ID, run1.ID}) {
		t.Errorf("runs: status %d, %v", code, ids())
	}
	if runs.Runs[0].CostUSD != "0.125" || runs.Runs[0].Status != store.StatusSucceeded || runs.Runs[0].FinishedAt == nil {
		t.Errorf("run = %+v", runs.Runs[0])
	}
	if code := env.get(t, base+"?limit=1", &runs); code != http.StatusOK || !slices.Equal(ids(), []string{run2.ID}) {
		t.Errorf("first page: status %d, %v", code, ids())
	}
	next := base + "?limit=1&before=" + url.QueryEscape(runs.Runs[0].StartedAt.Format(time.RFC3339Nano))
	if code := env.get(t, next, &runs); code != http.StatusOK || !slices.Equal(ids(), []string{run1.ID}) {
		t.Errorf("second page: status %d, %v", code, ids())
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=x", "?before=yesterday"} {
		if code := env.get(t, base+q, nil); code != http.StatusBadRequest {
			t.Errorf("runs%s: status %d, want 400", q, code)
		}
	}

	var one struct {
		Run    ResearchRun     `json:"run"`
		Result *resultResponse `json:"result"`
	}
	if code := env.get(t, base+"/"+run1.ID, &one); code != http.StatusOK || one.Run.ID != run1.ID || one.Result == nil {
		t.Fatalf("run 1: status %d, %+v", code, one)
	}
	if got := one.Result.newURLs(); !slices.Equal(got, []string{"https://example.com/a"}) {
		t.Errorf("new items of the first run = %v, want a", got)
	}
	// A run of another research, an unknown run and a malformed id.
	for _, id := range []string{other.ID, uuid.NewString(), "nope"} {
		if code := env.get(t, base+"/"+id, nil); code != http.StatusNotFound {
			t.Errorf("run %s under agents/news: status %d, want 404", id, code)
		}
	}

	// Content made invalid in the database is not sent.
	storetest.Exec(t, `UPDATE research_results SET blocks = $2 WHERE run_id = $1`, run2.ID,
		`[{"type":"links","links":[{"title":"Click","url":"javascript:alert(1)"}]}]`)
	resp, err := env.client.Get(env.srv.URL + "/api/v1/researches/agents/news")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	_, _ = bufio.NewReader(resp.Body).WriteTo(&body)
	if strings.Contains(body.String(), "javascript:") || !strings.Contains(body.String(), "failed validation") {
		t.Errorf("invalid stored result sent: %s", body.String())
	}
}

func TestResearchSearch(t *testing.T) {
	owner, reader := readerStore(t)
	env := researchEnv(t, oidcConfig(researchRules...), reader)
	at := time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC)
	news := seedRun(t, owner, "agents", "news", at, "Zeppelin news", "https://example.com/a")
	seedRun(t, owner, "team", "secret", at, "Zeppelin secrets", "https://example.com/b")
	// Results of a research that no longer exists, even one the glob matches.
	seedRun(t, owner, "agents", "news-deleted", at, "Zeppelin archive", "https://example.com/c")
	// A title made invalid in the database: the hit is left out.
	bad := seedRun(t, owner, "agents", "news", at.Add(time.Hour), "Zeppelin later", "https://example.com/d")
	storetest.Exec(t, `UPDATE research_results SET title = 'Zeppelin '||repeat('x', 400) WHERE run_id = $1`, bad.ID)
	loginWithRoles(t, env, "news-readers")

	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	if code := env.get(t, "/api/v1/research/search?q=zeppelin", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(out.Hits) != 1 || out.Hits[0].RunID != news.ID || out.Hits[0].Research != "news" || out.Hits[0].Title != "Zeppelin news" {
		t.Errorf("hits = %+v, want only agents/news", out.Hits)
	}
	for _, q := range []string{"", "%20%20", strings.Repeat("a", 201), "%FF"} {
		if code := env.get(t, "/api/v1/research/search?q="+q, nil); code != http.StatusBadRequest {
			t.Errorf("q=%.20s: status %d, want 400", q, code)
		}
	}
	if code := env.get(t, "/api/v1/research/search?q="+url.QueryEscape(strings.Repeat("é", 200)), nil); code != http.StatusOK {
		t.Errorf("200 characters: status %d", code)
	}
}

// TestResultView checks the validation on read without a database.
func TestResultView(t *testing.T) {
	s := &Server{log: logr.Discard()}
	row := func(title string, blocksJSON string, newURLs ...string) store.ResultRow {
		r := store.ResultRow{RunID: "r1", Title: title, Summary: "S", Blocks: json.RawMessage(blocksJSON)}
		for _, u := range newURLs {
			r.NewURLHashes = append(r.NewURLHashes, blocks.URLHash(u))
		}
		return r
	}
	// As jsonb returns it: with spaces, keys reordered, an unknown block type.
	good := row("News", `[{"text": "Tech", "type": "heading", "level": 2}, {"type": "video", "src": "x"},
		{"type": "items", "items": [{"url": "https://Example.com/a#x", "title": "A", "importance": 3},
			{"url": "https://example.com/b", "title": "B <b>", "importance": 1}]}]`, "https://example.com/a")
	out := s.resultView(good)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var got resultResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "News" || len(got.Blocks) != 2 || got.Blocks[0].Type != blocks.TypeHeading ||
		!slices.Equal(got.newURLs(), []string{"https://Example.com/a#x"}) || len(got.Warnings) != 1 ||
		!strings.Contains(got.Warnings[0], "unknown type") {
		t.Errorf("result = %s", b)
	}
	if !strings.Contains(string(b), `"text":"Tech","level":2`) || !strings.Contains(string(b), `"importance":1}`) {
		t.Errorf("blocks not in the schema's form: %s", b)
	}

	for name, bad := range map[string]store.ResultRow{
		"javascript link": row("News", `[{"type":"links","links":[{"title":"x","url":"javascript:alert(1)"}]}]`),
		"raw HTML":        row("News", `[{"type":"markdown","text":"<script>alert(1)</script>"}]`),
		"control title":   row("News\u0007", `[]`),
		"not a list":      row("News", `{"type":"heading"}`),
		"oversized":       row("News", `[{"type":"markdown","text":"`+strings.Repeat("x", blocks.MaxMarkdownBytes+1)+`"}]`),
	} {
		out := s.resultView(bad)
		b, _ := json.Marshal(out)
		if len(out.Blocks) != 0 || out.Title != "Result withheld" || out.Summary != "" || len(out.Warnings) != 1 ||
			strings.Contains(string(b), "javascript:") || strings.Contains(string(b), "script>") {
			t.Errorf("%s: sent %.300s", name, b)
		}
	}
}
