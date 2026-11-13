package rules

import (
	"strings"
	"testing"
)

func TestRepos(t *testing.T) {
	desc := strings.Join([]string{
		"/agent spec",
		"/agent repo example/infra",
		"  /agent repo https://gitlab.com/example/Orbit/-/tree/main merge  ",
		"/agent repo https://gitlab.com/example/other.git MERGE\r",
		"/agent repo",
		"/agent repo example/infra merge",
		"/agent repo example/x please",
		"/agent repo infra",
		"/agent repo a/b c d",
		"/agent repository x/y",
		"Also see /agent repo x/y",
		"/agent repo ftp://gitlab.com/a/b",
		"/agent repo ../etc",
		"Build it.",
	}, "\n")
	got := Repos(desc, "/agent")
	want := []RepoLine{
		{Project: "example/infra", Raw: "/agent repo example/infra"},
		{Project: "example/orbit", Merge: true, Raw: "/agent repo https://gitlab.com/example/Orbit/-/tree/main merge"},
		{Project: "example/other", Merge: true, Raw: "/agent repo https://gitlab.com/example/other.git MERGE"},
		{Raw: "/agent repo", Problem: "write"},
		{Raw: "/agent repo example/infra merge", Problem: "repeats the line"},
		{Raw: "/agent repo example/x please", Problem: "only `merge`"},
		{Raw: "/agent repo infra", Problem: "no project path"},
		{Raw: "/agent repo a/b c d", Problem: "write"},
		{Raw: "/agent repo ftp://gitlab.com/a/b", Problem: "no project path"},
		{Raw: "/agent repo ../etc", Problem: "no project path"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Project != w.Project || g.Merge != w.Merge || g.Raw != w.Raw || (w.Problem == "") != (g.Problem == "") || !strings.Contains(g.Problem, w.Problem) {
			t.Errorf("line %d: got %+v, want %+v", i, g, w)
		}
	}
	if got := Repos("/agent spec\nBuild it.", "/agent"); got != nil {
		t.Errorf("no repo lines: %+v", got)
	}
}

func TestProjectPath(t *testing.T) {
	for in, want := range map[string]string{
		"example/infra":                          "example/infra",
		"/Example/Infra/":                        "example/infra",
		"https://gitlab.com/example/infra":       "example/infra",
		"https://gitlab.com/example/infra/":      "example/infra",
		"https://gitlab.com/example/infra.git":   "example/infra",
		"https://gitlab.com/example/infra/-/mrs": "example/infra",
		"https://gitlab.example/example/infra/-": "example/infra",
		"http://gitlab.local:8080/a/b?x=1":       "a/b",
		"cluster":                                "",
		"https://gitlab.com/cluster":             "",
		"https://gitlab.com/":                    "",
		"ssh://git@gitlab.com/a/b":               "",
		"a/../b":                                 "",
		"a/b c":                                  "",
		"a//b":                                   "",
		"a/-b":                                   "",
		"":                                       "",
	} {
		got, ok := ProjectPath(in)
		if got != want || ok != (want != "") {
			t.Errorf("ProjectPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// The repo lines stay in the spec, so changing one changes the spec
// task's description hash.
func TestRepoLinesArePartOfTheSpec(t *testing.T) {
	desc := spec + "\n/agent repo a/b merge\n/agent owners @alice"
	if got := Spec(desc, "/agent"); !strings.Contains(got, "/agent repo a/b merge") {
		t.Fatalf("the repo line must stay in the spec: %q", got)
	}
	if Spec(desc, "/agent") == Spec(strings.Replace(desc, " merge", "", 1), "/agent") {
		t.Fatal("dropping merge must change the spec")
	}
}
