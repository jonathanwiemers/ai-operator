package command

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

func TestParseRouting(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
		want Routing
	}{
		{"/agent use claude opus", true, Routing{Verb: "use", Args: []string{"claude", "opus"}, Only: true}},
		{"  /agent: USE cursor auto\n", true, Routing{Verb: "use", Args: []string{"cursor", "auto"}, Only: true}},
		{"/agent effort high\nand please also fix the tests", true, Routing{Verb: "effort", Args: []string{"high"}}},
		{"/agent use `claude` opus", true, Routing{Verb: "use", Args: []string{"claude", "opus"}, Only: true}},
		{"/agent fix the tests", false, Routing{}},
		{"/agent use the existing logger instead of fmt.Println", false, Routing{}},
		{"/agent effort should go into the parser", false, Routing{}},
		{"/agent use claude gpt-9", true, Routing{Verb: "use", Args: []string{"claude", "gpt-9"}, Only: true}},
		{"/agent use Claude Opus HIGH", true, Routing{Verb: "use", Args: []string{"Claude", "Opus", "HIGH"}, Only: true}},
		{"/agents use claude opus", false, Routing{}},
		{"please /agent use claude opus", false, Routing{}},
		{"/agent budget 40", true, Routing{Verb: "budget", Args: []string{"40"}, Only: true}},
		{"/agent budget default", true, Routing{Verb: "budget", Args: []string{"default"}, Only: true}},
		{"/agent budget five", true, Routing{Verb: "budget", Args: []string{"five"}, Only: true}}, // answered as invalid
		{"/agent budget the API calls carefully", false, Routing{}},
		{"/agent", false, Routing{}},
	} {
		got, ok := ParseRouting(tc.body, "/agent")
		if ok != tc.ok || got.Verb != tc.want.Verb || !slices.Equal(got.Args, tc.want.Args) || got.Only != tc.want.Only {
			t.Errorf("%q: got %+v %v, want %+v %v", tc.body, got, ok, tc.want, tc.ok)
		}
	}
	notes := []gitlab.Note{{ID: 1, Body: "/agent use claude opus"}, {ID: 2, Body: "/agent effort high\nalso the docs"}, {ID: 3, Body: "hi"}, {ID: 4, Body: "/agent review"}}
	if got := WithoutRouting(notes, "/agent"); len(got) != 2 || got[0].ID != 2 {
		t.Errorf("only pure commands are dropped: %+v", got)
	}
}

func TestParseRoutingTruncatesLongArguments(t *testing.T) {
	r, ok := ParseRouting("/agent use claude "+strings.Repeat("x", 5000), "/agent")
	if !ok || len(r.Args[1]) > maxArg+len("…") {
		t.Fatalf("a long argument must be cut: %d bytes", len(r.Args[1]))
	}
}

func TestApplyEffortOnCursor(t *testing.T) {
	ch := Apply(testSpec(), nil, []string{"agent-profile::fast"}, []Routing{{Verb: "effort", Args: []string{"high"}}})
	if !slices.Equal(ch.Add, []string{"agent-effort::high"}) || !strings.Contains(ch.Lines[0], "no effort setting") {
		t.Fatalf("the label is set, but the answer says it does not count on cursor: %+v", ch)
	}
	if ch := Apply(testSpec(), nil, nil, []Routing{use("Claude", "OPUS")}); !slices.Equal(ch.Add, []string{"agent-model::claude/opus"}) {
		t.Fatalf("names match in any case: %+v", ch)
	}
	if r := (Change{Lines: []string{"x"}}).Reply(); !strings.HasSuffix(r, marker.RoutingReply) {
		t.Fatalf("the answer carries the marker: %q", r)
	}
}

func testSpec() aiv1.AgentProjectSpec {
	s := aiv1.AgentProjectSpec{}
	s.Agents.Profiles = []aiv1.AgentProfile{{Name: "fast", RoleSpec: aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto"}}}
	return s.WithDefaults()
}

func use(args ...string) Routing { return Routing{Verb: "use", Args: args, Only: true} }

func TestApplyUse(t *testing.T) {
	spec := testSpec()
	ch := Apply(spec, nil, []string{"agent::working", "agent-model::cursor/auto", "agent-effort::low"}, []Routing{use("claude", "opus", "high")})
	if !slices.Equal(ch.Add, []string{"agent-model::claude/opus", "agent-effort::high"}) ||
		!slices.Equal(ch.Remove, []string{"agent-model::cursor/auto", "agent-effort::low"}) {
		t.Fatalf("labels: %+v", ch)
	}
	if len(ch.Lines) != 2 || !strings.Contains(ch.Lines[0], "`claude/opus`") {
		t.Fatalf("lines: %v", ch.Lines)
	}
	// The full model name works too, and a profile's model is allowed.
	if ch := Apply(spec, nil, nil, []Routing{use("claude", "claude-sonnet-5-5")}); !slices.Equal(ch.Add, []string{"agent-model::claude/sonnet"}) {
		t.Fatalf("model name: %+v", ch)
	}
	if ch := Apply(spec, nil, nil, []Routing{use("cursor", "auto")}); !slices.Equal(ch.Add, []string{"agent-model::cursor/auto"}) {
		t.Fatalf("profile model: %+v", ch)
	}
	// Already set: nothing to change, still answered.
	if ch := Apply(spec, nil, []string{"agent-model::claude/opus"}, []Routing{use("claude", "opus")}); len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 {
		t.Fatalf("unchanged: %+v", ch)
	}
}

func TestApplyDefaultsAndEffort(t *testing.T) {
	spec := testSpec()
	ch := Apply(spec, nil, []string{"agent-model::claude/opus", "agent-effort::high"}, []Routing{use("default"), {Verb: "effort", Args: []string{"default"}}})
	if len(ch.Add) != 0 || !slices.Equal(ch.Remove, []string{"agent-model::claude/opus", "agent-effort::high"}) {
		t.Fatalf("defaults remove the labels: %+v", ch)
	}
	// Above the cap (default high): lowered and said so.
	ch = Apply(spec, nil, nil, []Routing{{Verb: "effort", Args: []string{"MAX"}}})
	if !slices.Equal(ch.Add, []string{"agent-effort::high"}) || !strings.Contains(strings.Join(ch.Lines, " "), "above the project's maximum") {
		t.Fatalf("capped: %+v", ch)
	}
	// The last command per setting wins.
	ch = Apply(spec, nil, nil, []Routing{{Verb: "effort", Args: []string{"low"}}, use("claude", "sonnet", "medium")})
	if !slices.Equal(ch.Add, []string{"agent-model::claude/sonnet", "agent-effort::medium"}) {
		t.Fatalf("last wins: %+v", ch)
	}
}

func TestApplyRejects(t *testing.T) {
	spec := testSpec()
	for _, c := range []Routing{use("claude", "gpt-9"), use("openai", "opus"), use("claude", "opus", "huge"), use("claude"), {Verb: "effort", Args: []string{"lots"}}} {
		ch := Apply(spec, nil, []string{"agent-model::claude/opus"}, []Routing{c})
		if len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 {
			t.Errorf("%+v must change nothing and say why: %+v", c, ch)
		}
	}
	ch := Apply(spec, nil, nil, []Routing{use("claude", "gpt-9")})
	if !strings.Contains(ch.Lines[0], "`claude opus`") || !strings.Contains(ch.Lines[0], "`cursor auto`") {
		t.Fatalf("the answer lists the allowed models: %s", ch.Lines[0])
	}
}

func TestIsReview(t *testing.T) {
	for body, want := range map[string]bool{
		"/agent review":            true,
		"  /agent: Review \n":      true,
		"/agent review the logger": false,
		"/agent please review":     false,
		"review":                   false,
	} {
		if got := IsReview(body, "/agent"); got != want {
			t.Errorf("IsReview(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestApplyBudget(t *testing.T) {
	budget := func(usd string) Routing { return Routing{Verb: "budget", Args: []string{usd}, Only: true} }
	ch := Apply(testSpec(), nil, []string{"agent::working"}, []Routing{budget("40")})
	if ch.Budget == nil || *ch.Budget != "40.0000" || len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 || !strings.Contains(ch.Lines[0], "$40.00") {
		t.Fatalf("a budget sets the issue's budget and no label: %+v", ch)
	}
	ch = Apply(testSpec(), nil, nil, []Routing{budget("40"), budget("DEFAULT")})
	if ch.Budget == nil || *ch.Budget != "" || !strings.Contains(ch.Lines[0], "back to the project's") {
		t.Fatalf("the last budget wins, default is the project's: %+v", ch)
	}
	for _, bad := range []string{"0", "five", "-3", "$"} {
		ch := Apply(testSpec(), nil, nil, []Routing{budget(bad)})
		if ch.Budget != nil || len(ch.Lines) != 1 || !strings.Contains(ch.Lines[0], "`/agent budget "+bad+"`") {
			t.Errorf("%q must change nothing and say why: %+v", bad, ch)
		}
	}
	ch = Apply(testSpec(), nil, nil, []Routing{budget("40"), budget("five")})
	if ch.Budget == nil || *ch.Budget != "40.0000" || len(ch.Lines) != 2 {
		t.Fatalf("an invalid budget keeps the valid one before it: %+v", ch)
	}
}

func TestParseRoutingRepo(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
		want []string
	}{
		{"/agent repo jo.wiemers/infra/cluster", true, []string{"jo.wiemers/infra/cluster"}},
		{"/agent repo https://gitlab.com/Jo.Wiemers/Infra/Cluster/-/tree/main", true, []string{"jo.wiemers/infra/cluster"}},
		{"/agent repo https://gitlab.com/jo.wiemers/infra/cluster.git\nplease", true, []string{"jo.wiemers/infra/cluster"}},
		{"/agent repo NONE", true, []string{"none"}},
		{"/agent repo `a/b`", true, []string{"a/b"}},
		{"/agent repo jo.wiemers/infra/cluster merge", false, nil}, // merge is the milestone's
		{"/agent repo a/b c/d", false, nil},
		{"/agent repo cleanup", false, nil}, // an instruction for the agent
		{"/agent repo", false, nil},
		{"/agent repo " + strings.Repeat("a/", 200) + "b", false, nil},
	} {
		got, ok := ParseRouting(tc.body, "/agent")
		if ok != tc.ok || (ok && (got.Verb != "repo" || !slices.Equal(got.Args, tc.want))) {
			t.Errorf("%q: got %+v %v, want %v %v", tc.body, got, ok, tc.want, tc.ok)
		}
	}
	long := "a/" + strings.Repeat("b", 100)
	if r, ok := ParseRouting("/agent repo "+long, "/agent"); !ok || r.Args[0] != long {
		t.Errorf("a project path is not cut: %+v", r)
	}
}

func TestApplyRepo(t *testing.T) {
	repo := func(arg string) Routing { return Routing{Verb: "repo", Args: []string{arg}, Only: true} }
	targets := []aiv1.CrossRepoTarget{{Project: "jo.wiemers/infra/cluster", Paths: []string{"clusters/"}}, {Project: "jo.wiemers/bloop", Paths: []string{"x/"}}}
	ch := Apply(testSpec(), targets, []string{"agent::ready", "agent-repo::jo.wiemers/bloop"}, []Routing{repo("jo.wiemers/infra/cluster")})
	if !slices.Equal(ch.Add, []string{"agent-repo::jo.wiemers/infra/cluster"}) || !slices.Equal(ch.Remove, []string{"agent-repo::jo.wiemers/bloop"}) ||
		len(ch.Lines) != 1 || !strings.Contains(ch.Lines[0], "`jo.wiemers/infra/cluster`") {
		t.Fatalf("the label replaces the other agent-repo label: %+v", ch)
	}
	if ch := Apply(testSpec(), targets, []string{"agent-repo::jo.wiemers/infra/cluster"}, []Routing{repo("jo.wiemers/infra/cluster")}); len(ch.Add)+len(ch.Remove) != 0 {
		t.Fatalf("a set label stays: %+v", ch)
	}
	ch = Apply(testSpec(), targets, []string{"agent-repo::jo.wiemers/infra/cluster", "agent::ready"}, []Routing{repo("none")})
	if len(ch.Add) != 0 || !slices.Equal(ch.Remove, []string{"agent-repo::jo.wiemers/infra/cluster"}) || !strings.Contains(ch.Lines[0], "no companion") {
		t.Fatalf("none removes the label: %+v", ch)
	}
	ch = Apply(testSpec(), targets, []string{"agent-repo::jo.wiemers/bloop"}, []Routing{repo("jo.wiemers/personal/kubernetes")})
	if len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 || !strings.Contains(ch.Lines[0], "`jo.wiemers/infra/cluster`, `jo.wiemers/bloop`") ||
		!strings.Contains(ch.Lines[0], "`/agent repo none`") {
		t.Fatalf("an unknown target changes nothing and lists the allowed ones: %+v", ch)
	}
	if ch := Apply(testSpec(), nil, nil, []Routing{repo("a/b")}); len(ch.Add) != 0 || !strings.Contains(ch.Lines[0], "has none") {
		t.Fatalf("without targets the answer says there are none: %+v", ch)
	}
	ch = Apply(testSpec(), targets, nil, []Routing{repo("jo.wiemers/bloop"), repo("unknown/x"), repo("JO.WIEMERS/infra/cluster")})
	if !slices.Equal(ch.Add, []string{"agent-repo::jo.wiemers/infra/cluster"}) || len(ch.Lines) != 2 {
		t.Fatalf("the last valid command wins: %+v", ch)
	}
}
