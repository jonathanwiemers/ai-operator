package rules

import (
	"strings"
	"testing"
)

func TestRepos(t *testing.T) {
	desc := strings.Join([]string{
		"/agent spec",
		"/agent repo jo.wiemers/infra/cluster",
		"  /agent repo https://gitlab.com/jo.wiemers/Bloop/-/tree/main merge  ",
		"/agent repo https://gitlab.com/jo.wiemers/other.git MERGE\r",
		"/agent repo",
		"/agent repo jo.wiemers/infra/cluster merge",
		"/agent repo jo.wiemers/x please",
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
		{Project: "jo.wiemers/infra/cluster", Raw: "/agent repo jo.wiemers/infra/cluster"},
		{Project: "jo.wiemers/bloop", Merge: true, Raw: "/agent repo https://gitlab.com/jo.wiemers/Bloop/-/tree/main merge"},
		{Project: "jo.wiemers/other", Merge: true, Raw: "/agent repo https://gitlab.com/jo.wiemers/other.git MERGE"},
		{Raw: "/agent repo", Problem: "write"},
		{Raw: "/agent repo jo.wiemers/infra/cluster merge", Problem: "repeats the line"},
		{Raw: "/agent repo jo.wiemers/x please", Problem: "only `merge`"},
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
		"jo.wiemers/infra/cluster":                          "jo.wiemers/infra/cluster",
		"/Jo.Wiemers/Infra/Cluster/":                        "jo.wiemers/infra/cluster",
		"https://gitlab.com/jo.wiemers/infra/cluster":       "jo.wiemers/infra/cluster",
		"https://gitlab.com/jo.wiemers/infra/cluster/":      "jo.wiemers/infra/cluster",
		"https://gitlab.com/jo.wiemers/infra/cluster.git":   "jo.wiemers/infra/cluster",
		"https://gitlab.com/jo.wiemers/infra/cluster/-/mrs": "jo.wiemers/infra/cluster",
		"https://gitlab.example/jo.wiemers/infra/cluster/-": "jo.wiemers/infra/cluster",
		"http://gitlab.local:8080/a/b?x=1":                  "a/b",
		"cluster":                                           "",
		"https://gitlab.com/cluster":                        "",
		"https://gitlab.com/":                               "",
		"ssh://git@gitlab.com/a/b":                          "",
		"a/../b":                                            "",
		"a/b c":                                             "",
		"a//b":                                              "",
		"a/-b":                                              "",
		"":                                                  "",
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
