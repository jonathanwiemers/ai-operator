package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// oneOffRules: writers write agents/*, admins every research, elsewhere
// only team/*, readers read, news-readers read agents/news; shop-devs see
// only the factory.
var oneOffRules = []Rule{
	{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
	{Roles: []string{"readers"}, Research: ResearchRead},
	{Roles: []string{"news-readers"}, Research: ResearchRead, Researches: []string{"agents/news"}},
	{Roles: []string{"writers"}, Research: ResearchWrite, Researches: []string{"agents/*"}},
	{Roles: []string{"admins"}, Research: ResearchWrite},
	{Roles: []string{"elsewhere"}, Research: ResearchWrite, Researches: []string{"team/*"}},
}

// oneOffEnv is a test env with allowCreate in namespace agents, the fake
// client as the writer, the projects agents/desk (with a writer login) and
// agents/plain (without), and the researches agents/news (scheduled),
// agents/q-mine (ada's one-off), agents/q-bob (bob's) and team/q-ada (ada's,
// elsewhere).
func oneOffEnv(t *testing.T, cfg *Config) *testEnv {
	t.Helper()
	env := newTestEnv(t, withResearch(cfg, true))
	env.server.SetResearchWriter(env.reader)
	// The list needs a store, not its database.
	env.server.SetResearchStore(&store.Store{})
	ada := map[string]string{aiv1.AnnotationCreatedBy: "ada@example.com"}
	for _, obj := range []client.Object{
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "desk"},
			Spec: aiv1.AgentProjectSpec{Research: &aiv1.ProjectResearch{WriterSecret: &aiv1.SecretLogin{Name: "writer"}}}},
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "plain"}},
		&aiv1.AgentResearch{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "news", Annotations: ada},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "desk", Prompt: "News", Schedule: "0 7 * * *"}},
		&aiv1.AgentResearch{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "q-mine", Annotations: ada},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "desk", Prompt: "Mine"}},
		&aiv1.AgentResearch{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "q-bob",
			Annotations: map[string]string{aiv1.AnnotationCreatedBy: "bob@example.com"}},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "desk", Prompt: "Bob's"}},
		&aiv1.AgentResearch{ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "q-ada", Annotations: ada},
			Spec: aiv1.AgentResearchSpec{ProjectRef: "billing", Prompt: "Elsewhere"}},
	} {
		if err := env.reader.Create(context.Background(), obj); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func (e *testEnv) do(t *testing.T, method, path string, body []byte, header http.Header) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (e *testEnv) del(t *testing.T, path string) int {
	t.Helper()
	return e.do(t, http.MethodDelete, path, nil, nil).StatusCode
}

func (e *testEnv) researchExists(t *testing.T, namespace, name string) bool {
	t.Helper()
	return e.reader.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, &aiv1.AgentResearch{}) == nil
}

func (e *testEnv) oneOffs(t *testing.T) []aiv1.AgentResearch {
	t.Helper()
	var list aiv1.AgentResearchList
	if err := e.reader.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	var out []aiv1.AgentResearch
	for _, ar := range list.Items {
		if strings.HasPrefix(ar.Name, "q-2") {
			out = append(out, ar)
		}
	}
	return out
}

var question = map[string]any{"project": "desk", "prompt": "What changed in Go 1.26?"}

func TestOneOffAccess(t *testing.T) {
	cases := []struct {
		name               string
		roles              []string
		form, create, own  int
		createdAfterCreate bool
	}{
		{"factory only", []string{"shop-devs"}, 404, 404, 404, false},
		{"no rule", []string{"other"}, 404, 404, 404, false},
		{"read only", []string{"readers"}, 403, 403, 403, false},
		// Read only, and agents/q-mine is not even readable.
		{"read one", []string{"news-readers"}, 403, 403, 404, false},
		// The write grant in another namespace: the configured one is not theirs.
		{"write elsewhere", []string{"elsewhere"}, 403, 403, 404, false},
		{"write", []string{"writers"}, 200, 201, 204, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := oneOffEnv(t, oidcConfig(oneOffRules...))
			loginWithRoles(t, env, tc.roles...)
			if code := env.get(t, "/api/v1/research/form", nil); code != tc.form {
				t.Errorf("form: %d, want %d", code, tc.form)
			}
			if code, out := env.postJSON(t, "/api/v1/researches", question); code != tc.create {
				t.Errorf("create: %d %v, want %d", code, out, tc.create)
			}
			if got := len(env.oneOffs(t)) == 1; got != tc.createdAfterCreate {
				t.Errorf("created: %v", got)
			}
			if code := env.del(t, "/api/v1/researches/agents/q-mine"); code != tc.own {
				t.Errorf("delete own: %d, want %d", code, tc.own)
			}
		})
	}
}

func TestOneOffForm(t *testing.T) {
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	loginWithRoles(t, env, "writers")
	var form ResearchForm
	if code := env.get(t, "/api/v1/research/form", &form); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if form.Namespace != "agents" || len(form.Projects) != 2 || len(form.Depths) != 2 || len(form.Views) != 2 || len(form.Efforts) == 0 {
		t.Fatalf("form = %+v", form)
	}
	desk, plain := form.Projects[0], form.Projects[1]
	if desk.Name != "desk" || !desk.Available || desk.Reason != "" || plain.Name != "plain" || plain.Available ||
		!strings.Contains(plain.Reason, "writerSecret") {
		t.Errorf("projects = %+v", form.Projects)
	}
	if len(desk.Models) != 3 || desk.Models[1] != (ModelChoice{Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5", Label: "claude/sonnet"}) {
		t.Errorf("models = %+v", desk.Models)
	}
}

// capture logs the server's info lines.
type capture struct {
	mu    sync.Mutex
	lines []string
}

func (c *capture) audit() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, l := range c.lines {
		if strings.Contains(l, `"msg"="audit"`) {
			out = append(out, l)
		}
	}
	return out
}

func captureLog(env *testEnv) *capture {
	c := &capture{}
	env.server.log = funcr.New(func(prefix, args string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.lines = append(c.lines, args)
	}, funcr.Options{})
	return c
}

func TestCreateOneOff(t *testing.T) {
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	logs := captureLog(env)
	loginWithRoles(t, env, "writers")
	code, out := env.postJSON(t, "/api/v1/researches", map[string]any{"project": "desk", "prompt": "  What changed?  ",
		"depth": "deep", "view": "newsflash", "topics": []string{" go ", "k8s"},
		"agent": map[string]string{"engine": "claude", "model": "sonnet", "effort": "high"}})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	list := env.oneOffs(t)
	if len(list) != 1 {
		t.Fatalf("%d one-offs", len(list))
	}
	ar := list[0]
	if !regexp.MustCompile(`^q-` + time.Now().UTC().Format("20060102") + `-[a-z2-7]{6}$`).MatchString(ar.Name) {
		t.Errorf("name %q", ar.Name)
	}
	if out["namespace"] != "agents" || out["name"] != ar.Name || ar.Namespace != "agents" {
		t.Errorf("answer %v for %s/%s", out, ar.Namespace, ar.Name)
	}
	want := aiv1.AgentResearchSpec{ProjectRef: "desk", Prompt: "What changed?", Topics: []string{"go", "k8s"},
		View: aiv1.ResearchViewNewsflash, Depth: aiv1.ResearchDepthDeep,
		Agent: &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5", Effort: "high"}}
	b1, _ := json.Marshal(ar.Spec)
	b2, _ := json.Marshal(want)
	if !bytes.Equal(b1, b2) || !ar.OneOff() {
		t.Errorf("spec = %s, want %s", b1, b2)
	}
	if ar.Annotations[aiv1.AnnotationCreatedBy] != "ada@example.com" {
		t.Errorf("annotations = %v", ar.Annotations)
	}
	audit := logs.audit()
	if len(audit) != 1 || !strings.Contains(audit[0], `"action"="research.create" "user"="ada@example.com" "namespace"="agents" "name"="`+ar.Name+`"`) ||
		!strings.Contains(audit[0], `"depth"="deep" "agent"="claude/claude-sonnet-5-5@high"`) {
		t.Errorf("audit = %q", audit)
	}

	// The defaults: auto, quick, report.
	if code, out := env.postJSON(t, "/api/v1/researches", question); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	for _, ar := range env.oneOffs(t) {
		if ar.Spec.Prompt == question["prompt"] && (ar.Spec.Agent != nil || ar.Spec.Depth != aiv1.ResearchDepthQuick || ar.Spec.View != aiv1.ResearchViewReport) {
			t.Errorf("defaults: %+v", ar.Spec)
		}
	}
	if audit := logs.audit(); len(audit) != 2 || !strings.Contains(audit[1], `"agent"="auto"`) {
		t.Errorf("audit = %q", audit)
	}
}

func TestCreateOneOffValidation(t *testing.T) {
	with := func(kv ...any) map[string]any {
		m := map[string]any{"project": "desk", "prompt": "Why?"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	topics := make([]string, maxTopics+1)
	for i := range topics {
		topics[i] = "t" + strconv.Itoa(i)
	}
	cases := []struct {
		name  string
		body  map[string]any
		code  int
		msg   string
		field string
	}{
		{"no prompt", with("prompt", "   "), 400, "question", "prompt"},
		{"long prompt", with("prompt", strings.Repeat("x", maxPromptLength+1)), 400, "question", "prompt"},
		{"too many topics", with("topics", topics), 400, "topics", "topics"},
		{"long topic", with("topics", []string{strings.Repeat("x", maxTopicLength+1)}), 400, "topic", "topics"},
		{"empty topic", with("topics", []string{" "}), 400, "topic", "topics"},
		{"depth", with("depth", "forever"), 400, "depth", "depth"},
		{"view", with("view", "html"), 400, "view", "view"},
		{"unknown model", with("agent", map[string]string{"engine": "claude", "model": "gpt"}), 400, "not an allowed model", "agent"},
		{"effort", with("agent", map[string]string{"engine": "claude", "model": "opus", "effort": "huge"}), 400, "effort", "agent"},
		{"no project", with("project", ""), 400, "project", "project"},
		{"missing project", with("project", "shop"), 400, `no project "shop" in agents`, "project"},
		{"no writer login", with("project", "plain"), 400, "writerSecret", "project"},
		{"a namespace", with("namespace", "team"), 400, "invalid request", ""},
		{"a schedule", with("schedule", "* * * * *"), 400, "invalid request", ""},
		{"too large", with("prompt", strings.Repeat("x", maxCreateBody)), 413, "32 KiB", ""},
	}
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	loginWithRoles(t, env, "writers")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := env.postJSON(t, "/api/v1/researches", tc.body)
			if msg, _ := out["error"].(string); code != tc.code || !strings.Contains(msg, tc.msg) {
				t.Errorf("%d %q, want %d with %q", code, msg, tc.code, tc.msg)
			}
			if field, _ := out["field"].(string); field != tc.field {
				t.Errorf("field %q, want %q", field, tc.field)
			}
		})
	}
	if list := env.oneOffs(t); len(list) != 0 {
		t.Errorf("created %d", len(list))
	}
}

func TestDeleteOneOff(t *testing.T) {
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	logs := captureLog(env)
	loginWithRoles(t, env, "admins")
	var list []ResearchSummary
	if code := env.get(t, "/api/v1/researches", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	for _, r := range list {
		if want := r.Namespace+"/"+r.Name == "agents/q-mine"; r.CanDelete != want {
			t.Errorf("%s/%s: canDelete %v", r.Namespace, r.Name, r.CanDelete)
		}
	}
	for path, want := range map[string]int{
		"agents/q-bob":   http.StatusForbidden, // someone else's
		"agents/news":    http.StatusForbidden, // scheduled
		"team/q-ada":     http.StatusForbidden, // not in research.namespace
		"agents/missing": http.StatusNotFound,
	} {
		if code := env.del(t, "/api/v1/researches/"+path); code != want {
			t.Errorf("delete %s: %d, want %d", path, code, want)
		}
	}
	for _, key := range [][2]string{{"agents", "q-bob"}, {"agents", "news"}, {"team", "q-ada"}} {
		if !env.researchExists(t, key[0], key[1]) {
			t.Errorf("%s/%s was deleted", key[0], key[1])
		}
	}
	if code := env.del(t, "/api/v1/researches/agents/q-mine"); code != http.StatusNoContent || env.researchExists(t, "agents", "q-mine") {
		t.Fatalf("delete own: %d", code)
	}
	if audit := logs.audit(); len(audit) != 1 || !strings.Contains(audit[0], `"action"="research.delete" "user"="ada@example.com" "namespace"="agents" "name"="q-mine"`) {
		t.Errorf("audit = %q", audit)
	}
	if code := env.del(t, "/api/v1/researches/agents/q-mine"); code != http.StatusNotFound {
		t.Errorf("delete again: %d", code)
	}
}

func TestCreateRateLimit(t *testing.T) {
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	loginWithRoles(t, env, "writers")
	b, _ := json.Marshal(question)
	for i := range createsPerWindow {
		if resp := env.do(t, http.MethodPost, "/api/v1/researches", b, nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: %d", i, resp.StatusCode)
		}
	}
	resp := env.do(t, http.MethodPost, "/api/v1/researches", b, nil)
	if after, _ := strconv.Atoi(resp.Header.Get("Retry-After")); resp.StatusCode != http.StatusTooManyRequests || after < 3500 || after > 3600 {
		t.Fatalf("over the limit: %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if n := len(env.oneOffs(t)); n != createsPerWindow {
		t.Errorf("%d created", n)
	}
}

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(2, time.Hour)
	t0 := time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC)
	for _, at := range []time.Duration{0, 10 * time.Minute} {
		if ok, _ := l.allow("ada", t0.Add(at)); !ok {
			t.Fatalf("at %v refused", at)
		}
	}
	if ok, wait := l.allow("ada", t0.Add(30*time.Minute)); ok || wait != 30*time.Minute {
		t.Errorf("third: %v, wait %v", ok, wait)
	}
	if ok, _ := l.allow("bob", t0.Add(30*time.Minute)); !ok {
		t.Error("bob has his own limit")
	}
	if ok, _ := l.allow("ada", t0.Add(time.Hour)); !ok {
		t.Error("the first left the window")
	}
}

func TestOneOffCrossOrigin(t *testing.T) {
	env := oneOffEnv(t, oidcConfig(oneOffRules...))
	loginWithRoles(t, env, "writers")
	b, _ := json.Marshal(question)
	cross := http.Header{"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"cross-site"}}
	if resp := env.do(t, http.MethodPost, "/api/v1/researches", b, cross); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin create: %d", resp.StatusCode)
	}
	if resp := env.do(t, http.MethodDelete, "/api/v1/researches/agents/q-mine", nil, cross); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin delete: %d", resp.StatusCode)
	}
	if len(env.oneOffs(t)) != 0 || !env.researchExists(t, "agents", "q-mine") {
		t.Error("a cross-origin request changed something")
	}
}

func TestOneOffWithoutAllowCreate(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"no allowCreate": withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false),
		"no research":    {Auth: AuthConfig{Mode: AuthNone}},
	} {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t, cfg)
			if env.server.writer != nil {
				t.Fatal("a writer without allowCreate")
			}
			if code := env.get(t, "/api/v1/research/form", nil); code != http.StatusNotFound {
				t.Errorf("form: %d", code)
			}
			if code, _ := env.postJSON(t, "/api/v1/researches", question); code != http.StatusNotFound {
				t.Errorf("create: %d", code)
			}
			if code := env.del(t, "/api/v1/researches/agents/q-mine"); code != http.StatusNotFound {
				t.Errorf("delete: %d", code)
			}
		})
	}
}

func TestOneOffAuthNone(t *testing.T) {
	env := oneOffEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	if code, out := env.postJSON(t, "/api/v1/researches", question); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	list := env.oneOffs(t)
	if len(list) != 1 || list[0].Annotations[aiv1.AnnotationCreatedBy] != "anonymous" {
		t.Fatalf("one-offs = %+v", list)
	}
	if code := env.del(t, "/api/v1/researches/agents/"+list[0].Name); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
}
