package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// maxReleaseNotes caps the release notes in bytes, well under GitLab's limit
// for a release description, leaving room for what a tag pipeline adds.
const maxReleaseNotes = 4000

// maxUntrustedText caps one piece of untrusted text (a reason, a title) in
// the release notes and comments, in characters.
const maxUntrustedText = 200

// releaseItem is a merged issue of the milestone, for the release notes.
type releaseItem struct {
	IID, MR int64
	Title   string
}

// markdownEscaper escapes the characters of markdown links, images, HTML
// and code, and breaks URLs so GitLab does not link them.
var markdownEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`,
	"<", `\<`, ">", `\>`, "!", `\!`, "`", "\\`", "://", ":\u200b//", "www.", "www\u200b.")

// untrusted renders agent text harmless for the release notes and the
// tracking issue: one valid UTF-8 line of at most maxUntrustedText
// characters, mentions and quick actions defused, markdown escaped.
func untrusted(s string) string {
	s = strings.Join(strings.Fields(strings.ToValidUTF8(s, "\uFFFD")), " ")
	if r := []rune(s); len(r) > maxUntrustedText {
		s = string(r[:maxUntrustedText-1]) + "…"
	}
	return markdownEscaper.Replace(notify.Defuse(s))
}

// sentence ends s with a full stop unless it ends with a punctuation mark.
func sentence(s string) string {
	if s == "" || strings.ContainsAny(s[len(s)-1:], ".!?") || strings.HasSuffix(s, "…") {
		return s
	}
	return s + "."
}

// releaseDecider is who decided rec, capitalized for the start of a line.
func releaseDecider(rec aiv1.ReleaseRecord) string {
	switch rec.By {
	case aiv1.ReleaseByPlanner:
		return "The planner"
	case aiv1.ReleaseByPilot:
		return "The pilot"
	case aiv1.ReleaseByOwner:
		return cmp.Or(untrusted(rec.Actor), "An owner")
	case aiv1.ReleaseByCompanion:
		return "A companion"
	}
	return "The operator" // final, sweep
}

// releaseWhyLine is the "## Why" line of the notes: "<who> released after
// #N: <why>". The planner's and the pilot's why is labelled as theirs; the
// final release names the milestone.
func releaseWhyLine(rec aiv1.ReleaseRecord, milestone string) string {
	s := releaseDecider(rec) + " released"
	if rec.AfterIssue != 0 {
		s += fmt.Sprintf(" after #%d", rec.AfterIssue)
	}
	why := untrusted(rec.Why)
	switch {
	case rec.Final && milestone != "":
		return s + ": the end of milestone " + sentence(untrusted(milestone))
	case why == "":
		return s + "."
	case rec.By == aiv1.ReleaseByPlanner || rec.By == aiv1.ReleaseByPilot:
		return fmt.Sprintf("%s. Reason given by the %s: %s", s, rec.By, sentence(why))
	}
	return s + ": " + sentence(why)
}

// releaseNotes is the description of the GitLab release, markdown: why (who, after which
// issue, the reason), the issues and merge requests the release contains
// ("#IID <title> (!MR)", titles from items) and a link to the milestone.
// Agent text in it is untrusted. It is at most maxReleaseNotes bytes: the
// list is cut at whole lines, ending with "… and N more issues".
func releaseNotes(rec aiv1.ReleaseRecord, issues, mrs []int64, items []releaseItem, milestone, milestoneURL string) string {
	head := "## Why\n\n" + releaseWhyLine(rec, milestone) + "\n"
	var lines []string
	listed := map[int64]bool{}
	for _, iid := range issues {
		line := fmt.Sprintf("- #%d", iid)
		if i := slices.IndexFunc(items, func(it releaseItem) bool { return it.IID == iid }); i >= 0 {
			it := items[i]
			if t := untrusted(it.Title); t != "" {
				line += " " + t
			}
			if it.MR != 0 && slices.Contains(mrs, it.MR) {
				line += fmt.Sprintf(" (!%d)", it.MR)
				listed[it.MR] = true
			}
		}
		lines = append(lines, line)
	}
	for _, mr := range mrs {
		if !listed[mr] {
			lines = append(lines, fmt.Sprintf("- !%d", mr))
		}
	}
	if len(lines) > 0 {
		head += "\n## Contains\n\n"
	}
	tail := ""
	if milestone != "" && milestoneURL != "" {
		tail = fmt.Sprintf("\nMilestone: [%s](%s)\n", untrusted(milestone), milestoneURL)
	}
	return capNotes(head, lines, tail)
}

// capNotes joins head, lines (one per line) and tail into at most
// maxReleaseNotes bytes: it drops lines from the end for a line "… and N
// more issues", and as a last resort cuts at a character.
func capNotes(head string, lines []string, tail string) string {
	more := func(n int) string {
		if n == 1 {
			return "- … and 1 more issue\n"
		}
		return fmt.Sprintf("- … and %d more issues\n", n)
	}
	size := len(head) + len(tail)
	for _, l := range lines {
		size += len(l) + 1
	}
	keep := len(lines)
	fits := func() bool {
		if keep < len(lines) {
			return size+len(more(len(lines)-keep)) <= maxReleaseNotes
		}
		return size <= maxReleaseNotes
	}
	for keep > 0 && !fits() {
		keep--
		size -= len(lines[keep]) + 1
	}
	var b strings.Builder
	b.WriteString(head)
	for _, l := range lines[:keep] {
		b.WriteString(l + "\n")
	}
	if keep < len(lines) {
		b.WriteString(more(len(lines) - keep))
	}
	b.WriteString(tail)
	return cutUTF8(b.String(), maxReleaseNotes)
}

// cutUTF8 cuts the valid UTF-8 string s to at most n bytes, at a character.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// milestoneURL is the milestone's GitLab page, "" outside milestones.
func (x *releaser) milestoneURL(proj *gitlab.Project) string {
	if x.milestone.IID == 0 || proj.WebURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/-/milestones/%d", strings.TrimSuffix(proj.WebURL, "/"), x.milestone.IID)
}

// releaseAnnouncement is the tracking comment rec is due, "" for none: one
// when its release starts to run (a retried attempt is the same run), and
// one when that run ends. The final record's end is commented also without a
// run, since it decides when the milestone is done; a dropped one is not
// (the owner who dropped it was answered).
func releaseAnnouncement(rec aiv1.ReleaseRecord) string {
	switch {
	case rec.State == aiv1.ReleaseRunning && rec.Announced != aiv1.ReleaseRunning:
		who := string(rec.By)
		if rec.By == aiv1.ReleaseByOwner && rec.Actor != "" {
			who = untrusted(rec.Actor)
		}
		s := "🚀 Release started (" + who + ")"
		if why := untrusted(rec.Why); why != "" {
			s += ": " + why
		}
		s = sentence(s)
		if len(rec.Issues) > 0 {
			s += " Contains " + issueList(rec.Issues) + "."
		} else {
			s += " Contains no new issue of the milestone."
		}
		switch {
		case rec.ReleaseURL != "":
			s += " Release " + rec.Version + ": " + rec.ReleaseURL
		case rec.Version != "":
			s += " Release " + rec.Version + "."
		}
		return s
	case !rec.State.Ended() || rec.Announced == rec.State || rec.Announced != aiv1.ReleaseRunning && !rec.Final:
		return ""
	}
	reason := notify.Defuse(rec.Reason)
	link := func(s string) string {
		if rec.ReleaseURL == "" {
			return sentence(s)
		}
		return s + ": " + rec.ReleaseURL
	}
	switch rec.State {
	case aiv1.ReleaseReleased:
		return link("📦 Released " + rec.Version)
	case aiv1.ReleaseAlreadyReleased:
		return link("📦 Already released in " + rec.Version + " (" + reason + ")")
	case aiv1.ReleaseNotNeeded:
		return sentence("📦 No release needed: " + reason)
	case aiv1.ReleaseNotApplicable:
		return sentence("📦 No release: " + reason)
	case aiv1.ReleaseFailed:
		if rec.Final {
			return sentence("❌ The final release failed twice: "+reason) + " The milestone is not done without it."
		}
		return sentence("❌ The release failed twice: "+reason) + " The queue goes on."
	}
	return ""
}

// announceReleases posts the tracking comments the records are due
// (releaseAnnouncement) and records each in Announced, so a comment that
// failed is posted on the next poll and none is posted twice.
func (a *autopilot) announceReleases(ctx context.Context) error {
	for i := range a.ap.Releases {
		rec := &a.ap.Releases[i]
		if body := releaseAnnouncement(*rec); body != "" {
			if err := a.comment(ctx, body); err != nil {
				return err
			}
			rec.Announced = rec.State
		}
	}
	return nil
}
