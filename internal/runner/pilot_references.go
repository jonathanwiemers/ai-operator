package runner

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

const (
	// pilotOtherMilestones caps the other open milestones whose issues are
	// read, the most recently updated first.
	pilotOtherMilestones = 10
	// pilotNoteReads caps the Pending and Skipped items whose notes are read.
	pilotNoteReads = 20
	// pilotReferences caps the lines of references.
	pilotReferences = 20
)

var (
	// replacesRef is "replaces #X" or "replaces %N #X".
	replacesRef = regexp.MustCompile(`(?i)\breplaces\s+(?:%\d+\s+)?#(\d+)\b`)
	// milestoneRef is "%N #X".
	milestoneRef = regexp.MustCompile(`%(\d+)\s+#(\d+)\b`)
	// mentionedIn is GitLab's system note "mentioned in (issue )?#Y".
	mentionedIn = regexp.MustCompile(`(?i)^mentioned in (?:issue )?#(\d+)\b`)
)

// Relations of a reference, the strongest first.
var relationRank = map[string]int{"replaces": 0, "references": 1, "mentions": 2}

// references lists the issues of other open milestones that reference the
// issues of milestone m: "replaces" or "%<m> #X" in their descriptions, and
// "mentioned in" system notes on m's Pending and Skipped items. One line per
// pair, e.g. `#125 (%5 "Desk prompts", opened) replaces #83: <title>`.
// GitLab errors are logged and return no lines: it is context only.
func (r *Runner) references(ctx context.Context, gl gitlab.API, glProject string, m *gitlab.Milestone, issues []gitlab.Issue, items []aiv1.AutopilotItem) []string {
	lines, err := references(ctx, gl, glProject, m, issues, items)
	if err != nil {
		r.log().Error(err, "listing references from other milestones", "milestone", m.Title)
		return nil
	}
	return lines
}

func references(ctx context.Context, gl gitlab.API, glProject string, m *gitlab.Milestone, issues []gitlab.Issue, items []aiv1.AutopilotItem) ([]string, error) {
	ours := map[int64]bool{}
	for _, is := range issues {
		ours[is.IID] = true
	}
	milestones, err := gl.ListMilestones(ctx, glProject)
	if err != nil {
		return nil, fmt.Errorf("listing milestones: %w", err)
	}
	milestones = slices.DeleteFunc(milestones, func(o gitlab.Milestone) bool { return o.ID == m.ID })
	updated := func(o gitlab.Milestone) time.Time {
		if o.UpdatedAt == nil {
			return time.Time{}
		}
		return *o.UpdatedAt
	}
	slices.SortFunc(milestones, func(a, b gitlab.Milestone) int {
		return cmp.Or(updated(b).Compare(updated(a)), cmp.Compare(b.IID, a.IID))
	})
	type other struct {
		issue     gitlab.Issue
		milestone gitlab.Milestone
	}
	others := map[int64]other{}
	type pair struct{ from, to int64 }
	rel := map[pair]string{}
	add := func(p pair, relation string) {
		if old, ok := rel[p]; !ok || relationRank[relation] < relationRank[old] {
			rel[p] = relation
		}
	}
	for _, o := range milestones[:min(len(milestones), pilotOtherMilestones)] {
		list, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: o.Title, State: "all"})
		if err != nil {
			return nil, fmt.Errorf("listing issues of milestone %q: %w", o.Title, err)
		}
		for _, is := range list {
			if ours[is.IID] {
				continue
			}
			others[is.IID] = other{is, o}
			for _, g := range replacesRef.FindAllStringSubmatch(is.Description, -1) {
				if x, _ := strconv.ParseInt(g[1], 10, 64); ours[x] {
					add(pair{is.IID, x}, "replaces")
				}
			}
			for _, g := range milestoneRef.FindAllStringSubmatch(is.Description, -1) {
				n, _ := strconv.ParseInt(g[1], 10, 64)
				if x, _ := strconv.ParseInt(g[2], 10, 64); n == m.IID && ours[x] {
					add(pair{is.IID, x}, "references")
				}
			}
		}
	}
	reads := 0
	for _, it := range items {
		if it.State != aiv1.AutopilotItemPending && it.State != aiv1.AutopilotItemSkipped || !ours[it.IID] {
			continue
		}
		if reads++; reads > pilotNoteReads || len(others) == 0 {
			break
		}
		notes, err := gl.ListIssueNotes(ctx, glProject, it.IID)
		if err != nil {
			return nil, fmt.Errorf("listing notes of #%d: %w", it.IID, err)
		}
		for _, n := range notes {
			if g := mentionedIn.FindStringSubmatch(n.Body); n.System && g != nil {
				if y, _ := strconv.ParseInt(g[1], 10, 64); others[y].issue.IID != 0 {
					add(pair{y, it.IID}, "mentions")
				}
			}
		}
	}
	pairs := make([]pair, 0, len(rel))
	for p := range rel {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(a, b pair) int { return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to)) })
	var lines []string
	for _, p := range pairs[:min(len(pairs), pilotReferences)] {
		o := others[p.from]
		lines = append(lines, fmt.Sprintf("#%d (%%%d %q, %s) %s #%d: %s", p.from, o.milestone.IID,
			textutil.Truncate(o.milestone.Title, 100), o.issue.State, rel[p], p.to, textutil.Truncate(o.issue.Title, 100)))
	}
	return lines, nil
}
