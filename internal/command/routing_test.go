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
	ch := Apply(testSpec(), []string{"agent-profile::fast"}, []Routing{{Verb: "effort", Args: []string{"high"}}})
	if !slices.Equal(ch.Add, []string{"agent-effort::high"}) || !strings.Contains(ch.Lines[0], "no effort setting") {
		t.Fatalf("the label is set, but the answer says it does not count on cursor: %+v", ch)
	}
	if ch := Apply(testSpec(), nil, []Routing{use("Claude", "OPUS")}); !slices.Equal(ch.Add, []string{"agent-model::claude/opus"}) {
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
	ch := Apply(spec, []string{"agent::working", "agent-model::cursor/auto", "agent-effort::low"}, []Routing{use("claude", "opus", "high")})
	if !slices.Equal(ch.Add, []string{"agent-model::claude/opus", "agent-effort::high"}) ||
		!slices.Equal(ch.Remove, []string{"agent-model::cursor/auto", "agent-effort::low"}) {
		t.Fatalf("labels: %+v", ch)
	}
	if len(ch.Lines) != 2 || !strings.Contains(ch.Lines[0], "`claude/opus`") {
		t.Fatalf("lines: %v", ch.Lines)
	}
	// The full model name works too, and a profile's model is allowed.
	if ch := Apply(spec, nil, []Routing{use("claude", "claude-sonnet-5-5")}); !slices.Equal(ch.Add, []string{"agent-model::claude/sonnet"}) {
		t.Fatalf("model name: %+v", ch)
	}
	if ch := Apply(spec, nil, []Routing{use("cursor", "auto")}); !slices.Equal(ch.Add, []string{"agent-model::cursor/auto"}) {
		t.Fatalf("profile model: %+v", ch)
	}
	// Already set: nothing to change, still answered.
	if ch := Apply(spec, []string{"agent-model::claude/opus"}, []Routing{use("claude", "opus")}); len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 {
		t.Fatalf("unchanged: %+v", ch)
	}
}

func TestApplyDefaultsAndEffort(t *testing.T) {
	spec := testSpec()
	ch := Apply(spec, []string{"agent-model::claude/opus", "agent-effort::high"}, []Routing{use("default"), {Verb: "effort", Args: []string{"default"}}})
	if len(ch.Add) != 0 || !slices.Equal(ch.Remove, []string{"agent-model::claude/opus", "agent-effort::high"}) {
		t.Fatalf("defaults remove the labels: %+v", ch)
	}
	// Above the cap (default high): lowered and said so.
	ch = Apply(spec, nil, []Routing{{Verb: "effort", Args: []string{"MAX"}}})
	if !slices.Equal(ch.Add, []string{"agent-effort::high"}) || !strings.Contains(strings.Join(ch.Lines, " "), "above the project's maximum") {
		t.Fatalf("capped: %+v", ch)
	}
	// The last command per setting wins.
	ch = Apply(spec, nil, []Routing{{Verb: "effort", Args: []string{"low"}}, use("claude", "sonnet", "medium")})
	if !slices.Equal(ch.Add, []string{"agent-model::claude/sonnet", "agent-effort::medium"}) {
		t.Fatalf("last wins: %+v", ch)
	}
}

func TestApplyRejects(t *testing.T) {
	spec := testSpec()
	for _, c := range []Routing{use("claude", "gpt-9"), use("openai", "opus"), use("claude", "opus", "huge"), use("claude"), {Verb: "effort", Args: []string{"lots"}}} {
		ch := Apply(spec, []string{"agent-model::claude/opus"}, []Routing{c})
		if len(ch.Add)+len(ch.Remove) != 0 || len(ch.Lines) != 1 {
			t.Errorf("%+v must change nothing and say why: %+v", c, ch)
		}
	}
	ch := Apply(spec, nil, []Routing{use("claude", "gpt-9")})
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
