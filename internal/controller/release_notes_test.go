package controller

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const testMilestoneURL = "https://gitlab.example/group/app/-/milestones/3"

func TestReleaseNotes(t *testing.T) {
	items := []releaseItem{{IID: 1, MR: 11, Title: "API"}, {IID: 2, MR: 12, Title: "UI"}, {IID: 3, Title: "Docs"}}
	contains := "\n## Contains\n\n- #1 API (!11)\n- #2 UI (!12)\n- #3 Docs\n- !13\n" +
		"\nMilestone: [Checkout v2](" + testMilestoneURL + ")\n"
	for _, tc := range []struct {
		name string
		rec  aiv1.ReleaseRecord
		why  string
	}{
		{name: "final", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByFinal, Final: true, Why: finalWhy, AfterIssue: 3},
			why: "The operator released after #3: the end of milestone Checkout v2."},
		{name: "planner", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByPlanner, Why: "users see the API.", AfterIssue: 1},
			why: "The planner released after #1. Reason given by the planner: users see the API."},
		{name: "pilot", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByPilot, Why: "the UI needs it live", AfterIssue: 2},
			why: "The pilot released after #2. Reason given by the pilot: the UI needs it live."},
		{name: "owner", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByOwner, Actor: "@alice", Why: "a demo"},
			why: "@\u200balice released: a demo."},
		{name: "owner without a reason", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByOwner},
			why: "An owner released."},
		{name: "sweep", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseBySweep, Why: "main has merges older than 24h0m0s"},
			why: "The operator released: main has merges older than 24h0m0s."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseNotes(tc.rec, []int64{1, 2, 3}, []int64{11, 12, 13}, items, "Checkout v2", testMilestoneURL)
			if want := "## Why\n\n" + tc.why + "\n" + contains; got != want {
				t.Fatalf("want\n%s\ngot\n%s", want, got)
			}
		})
	}
	// Outside milestones, with nothing listed: only why.
	got := releaseNotes(aiv1.ReleaseRecord{By: aiv1.ReleaseBySweep, Why: "old merges"}, nil, nil, nil, "", "")
	if want := "## Why\n\nThe operator released: old merges.\n"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// Agent text pings nobody, runs nothing, links nothing and renders no
// markup; it is one line of at most 200 characters.
func TestReleaseNotesUntrusted(t *testing.T) {
	evil := "@all look\n/close [x](http://evil) ![i](http://evil/i.png) <img src=x onerror=alert(1)> `code` \\[y](www.evil.com)"
	rec := aiv1.ReleaseRecord{By: aiv1.ReleaseByPilot, Why: evil, AfterIssue: 1}
	got := releaseNotes(rec, []int64{1}, nil, []releaseItem{{IID: 1, Title: evil}}, "<b>m</b> @devs", testMilestoneURL)
	for _, bad := range []string{"@all", "@devs", "\n/close", "http://", "www.evil"} {
		if strings.Contains(got, bad) {
			t.Errorf("the notes contain %q:\n%s", bad, got)
		}
	}
	// Every markup character of agent text is escaped (an odd number of
	// backslashes before it); the milestone's link is the operator's.
	body := got[:strings.LastIndex(got, "\nMilestone: [")]
	for i := 0; i < len(body); i++ {
		if !strings.ContainsRune("[]()<>!`", rune(body[i])) {
			continue
		}
		n := 0
		for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
			n++
		}
		if n%2 == 0 {
			t.Errorf("unescaped %q at %d: %q", body[i], i, body[max(0, i-20):i+1])
		}
	}
	if !strings.Contains(got, `Reason given by the pilot: @`+"\u200b"+`all look /close \[x\]\(http:`+"\u200b"+`//evil\)`) {
		t.Errorf("want the pilot's reason on one line, labelled and escaped:\n%s", got)
	}
	if !strings.Contains(got, "Milestone: [\\<b\\>m\\</b\\> @\u200bdevs]("+testMilestoneURL+")") {
		t.Errorf("want the milestone's link with an escaped title:\n%s", got)
	}

	long := untrusted(strings.Repeat("ä", 300))
	if n := utf8.RuneCountInString(long); n != maxUntrustedText || !strings.HasSuffix(long, "…") {
		t.Fatalf("want %d characters ending in …, got %d: %q", maxUntrustedText, n, long)
	}
	if got := untrusted("a\xffb"); got != "a\uFFFDb" {
		t.Fatalf("want valid UTF-8, got %q", got)
	}
}

// With many issues the notes are cut at whole lines to 4000 bytes, ending
// with how many issues are left out, and stay valid UTF-8 with multi-byte
// titles.
func TestReleaseNotesCap(t *testing.T) {
	for _, title := range []string{"Short title", strings.Repeat("Größe 🚀 ", 40)} {
		var items []releaseItem
		var issues, mrs []int64
		for i := int64(1); i <= 300; i++ {
			items = append(items, releaseItem{IID: i, MR: 1000 + i, Title: title})
			issues, mrs = append(issues, i), append(mrs, 1000+i)
		}
		rec := aiv1.ReleaseRecord{By: aiv1.ReleaseByPlanner, Why: strings.Repeat("ü", 500), AfterIssue: 300}
		got := releaseNotes(rec, issues, mrs, items, "Checkout v2", testMilestoneURL)
		if len(got) > maxReleaseNotes || !utf8.ValidString(got) {
			t.Fatalf("want at most %d bytes of valid UTF-8, got %d", maxReleaseNotes, len(got))
		}
		listed := strings.Count(got, "\n- #")
		if listed == 0 || listed == 300 {
			t.Fatalf("want some issues listed, got %d", listed)
		}
		more := fmt.Sprintf("\n- … and %d more issues\n\nMilestone: [Checkout v2](%s)\n", 300-listed, testMilestoneURL)
		if !strings.HasSuffix(got, more) {
			t.Fatalf("want the notes to end with %q, got %q", more, got[len(got)-200:])
		}
		// One more line would not have fit.
		next := fmt.Sprintf("- #%d %s (!%d)\n", listed+1, untrusted(title), 1000+listed+1)
		moreNow, moreNext := fmt.Sprintf("- … and %d more issues\n", 300-listed), fmt.Sprintf("- … and %d more issues\n", 299-listed)
		if len(got)+len(next)-len(moreNow)+len(moreNext) <= maxReleaseNotes {
			t.Fatalf("cut too early: %d bytes with %d issues", len(got), listed)
		}
	}
	// One left out is "issue".
	if got := capNotes("h\n", []string{"- #1", strings.Repeat("b", 3995)}, ""); !strings.HasSuffix(got, "- … and 1 more issue\n") {
		t.Fatalf("want one more issue, got %q", got)
	}
	// The last resort cuts at a character.
	if got := capNotes(strings.Repeat("é", 2001), nil, ""); len(got) != maxReleaseNotes || !utf8.ValidString(got) {
		t.Fatalf("want %d bytes of valid UTF-8, got %d", maxReleaseNotes, len(got))
	}
}
