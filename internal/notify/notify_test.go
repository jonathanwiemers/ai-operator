package notify

import (
	"context"
	"slices"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestMilestoneOwners(t *testing.T) {
	for _, tc := range []struct {
		desc  string
		want  []string
		found bool
	}{
		{"/agent spec\nBuild it.", nil, false},
		{"Build it.\n/agent owners @alice bob, @alice @no@pe\n", []string{"alice", "bob"}, true},
		{"  /agent owners\t@carol  \r\nmore", []string{"carol"}, true},
		{"/agent owners", nil, true}, // nobody: mentions off
		{"/agent ownership @dave", nil, false},
		{"see /agent owners @erin", nil, false}, // not at the start of a line
		{"/agent owners @alice, @bob.", []string{"alice", "bob"}, true},
	} {
		got, found := MilestoneOwners(tc.desc, "/agent")
		if found != tc.found || !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %v %v, want %v %v", tc.desc, got, found, tc.want, tc.found)
		}
	}
}

func TestWithoutOwnersKeepsTheSpec(t *testing.T) {
	plain := "/agent spec\nBuild it.\nThen ship it."
	for _, desc := range []string{
		"/agent spec\n/agent owners @alice\nBuild it.\nThen ship it.",
		"/agent spec\nBuild it.\nThen ship it.\n/agent owners @alice @bob",
		"/agent spec\nBuild it.\n/agent owners\nThen ship it.",
	} {
		if got := WithoutOwners(desc, "/agent"); got != plain && got != plain+"\n" {
			t.Errorf("%q: got %q", desc, got)
		}
	}
}

func TestWithoutOwnersKeepsParagraphs(t *testing.T) {
	plain := "/agent spec\n\nBuild it.\n\nShip it."
	for _, desc := range []string{
		"/agent spec\n\n/agent owners @x\n\nBuild it.\n\nShip it.",
		"/agent spec\n\nBuild it.\n\nShip it.\n\n/agent owners @x",
		"/agent owners @x\n\n/agent spec\n\nBuild it.\n\nShip it.",
	} {
		if got := WithoutOwners(desc, "/agent"); got != plain {
			t.Errorf("%q: got %q", desc, got)
		}
	}
}

func TestResolvePrefersTheMostSpecific(t *testing.T) {
	if got := Resolve("/agent owners @m", "/agent", []string{"p"}, []string{"d"}); !slices.Equal(got, []string{"m"}) {
		t.Errorf("milestone: %v", got)
	}
	if got := Resolve("/agent owners", "/agent", []string{"p"}, []string{"d"}); len(got) != 0 {
		t.Errorf("an empty milestone line names nobody: %v", got)
	}
	if got := Resolve("plan", "/agent", []string{"@p", "p"}, []string{"d"}); !slices.Equal(got, []string{"p"}) {
		t.Errorf("project: %v", got)
	}
	if got := Resolve("", "/agent", nil, []string{"d", ""}); !slices.Equal(got, []string{"d"}) {
		t.Errorf("default: %v", got)
	}
}

func TestMention(t *testing.T) {
	if got := Mention([]string{"alice", "@bob"}); got != "\n\ncc @alice @bob" {
		t.Errorf("got %q", got)
	}
	if got := Mention(nil); got != "" {
		t.Errorf("no owners, no line: %q", got)
	}
}

func TestDefuse(t *testing.T) {
	for in, want := range map[string]string{
		"ping @alice now":       "ping @​alice now",
		"@bob first":            "@​bob first",
		"(@carol)":              "(@​carol)",
		"mail dev@example.com":  "mail dev@example.com",
		"no mention @ all":      "no mention @ all",
		"twice @a and @b":       "twice @​a and @​b",
		"path ./@scope/pkg":     "path ./@\u200bscope/pkg",
		"all of you .@all":      "all of you .@\u200ball",
		"-@alice /@bob @@carol": "-@\u200balice /@\u200bbob @@\u200bcarol",
		"/merge\n  /close\n> /approve\nnot /label mid-line": "\u200b/merge\n  \u200b/close\n> \u200b/approve\nnot /label mid-line",
	} {
		if got := Defuse(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestRequestReview(t *testing.T) {
	ctx := context.Background()
	gl := gitlabfake.New()
	gl.Users = []gitlab.User{{ID: 7, Username: "alice"}, {ID: 8, Username: "bob"}}
	mr, _ := gl.CreateMergeRequest(ctx, "g/p", gitlab.CreateMergeRequestOptions{SourceBranch: "b", TargetBranch: "main", Title: "x"})

	names, err := RequestReview(ctx, gl, "g/p", mr.IID, []string{"alice", "ghost", "bob"})
	if err != nil || !slices.Equal(names, []string{"alice", "bob"}) {
		t.Fatalf("unknown users are skipped: %v %v", names, err)
	}
	if got := gl.MRs[mr.IID].Reviewers; len(got) != 2 {
		t.Fatalf("reviewers %+v", got)
	}
	// Reviewers a human picked stay.
	_, _ = gl.UpdateMergeRequest(ctx, "g/p", mr.IID, gitlab.UpdateMergeRequestOptions{ReviewerIDs: []int64{8}})
	if names, err := RequestReview(ctx, gl, "g/p", mr.IID, []string{"alice"}); err != nil || names != nil {
		t.Fatalf("an MR with reviewers is left alone: %v %v", names, err)
	}
	if got := gl.MRs[mr.IID].Reviewers; len(got) != 1 || got[0].Username != "bob" {
		t.Fatalf("reviewers %+v", got)
	}
}
