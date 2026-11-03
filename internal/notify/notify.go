// Package notify finds the owners of a task, mentions them in the comments
// that need a human, and asks them for reviews.
package notify

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// username is a valid GitLab username.
var username = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// Usernames cleans a list of usernames: without "@", valid and unique, in
// their order.
func Usernames(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimRight(strings.TrimPrefix(strings.TrimSpace(n), "@"), ".-")
		if username.MatchString(n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func ownersLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+owners(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

func ownerSubjectsLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+owner-subjects(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

// WithoutOwners removes the owners lines from a milestone description, so
// adding, changing or removing the owners does not change the spec. A line
// set off by blank lines takes one of them along, so the paragraphs around
// it are spaced as before.
func WithoutOwners(description, prefix string) string {
	return WithoutLines(description, ownersLine(prefix))
}

// WithoutOwnerSubjects removes the owner-subjects lines from a milestone
// description, like WithoutOwners.
func WithoutOwnerSubjects(description, prefix string) string {
	return WithoutLines(description, ownerSubjectsLine(prefix))
}

// WithoutLines removes the lines re matches, spaced like WithoutOwners.
func WithoutLines(description string, re *regexp.Regexp) string {
	for {
		loc := re.FindStringIndex(description)
		if loc == nil {
			return description
		}
		before, after := description[:loc[0]], description[loc[1]:]
		if (before == "" || strings.HasSuffix(before, "\n\n")) && strings.HasPrefix(after, "\n") {
			after = after[1:]
		} else if after == "" {
			before = strings.TrimRight(before, "\n")
		}
		description = before + after
	}
}

// MilestoneOwners returns the users a milestone description names on a line
// "<prefix> owners @alice @bob", and whether it has such a line. A line
// without users names nobody, which turns the mentions off.
func MilestoneOwners(description, prefix string) ([]string, bool) {
	m := ownersLine(prefix).FindStringSubmatch(description)
	if m == nil {
		return nil, false
	}
	return Usernames(strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })), true
}

// MilestoneOwnerSubjects returns the Desk identities a milestone description
// names on a line "<prefix> owner-subjects alice@example.com bob". There is
// no project-level or default fallback: no line means nobody. Tokens are
// kept as written (unique, in order); they match chat_messages.author.
func MilestoneOwnerSubjects(description, prefix string) []string {
	m := ownerSubjectsLine(prefix).FindStringSubmatch(description)
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == '\t' || r == ',' }) {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// Resolve returns the owners of a task: the milestone's, else the project's,
// else the operator's default. The most specific one replaces the others.
func Resolve(milestoneDescription, prefix string, project, fallback []string) []string {
	if owners, ok := MilestoneOwners(milestoneDescription, prefix); ok {
		return owners
	}
	if owners := Usernames(project); len(owners) > 0 {
		return owners
	}
	return Usernames(fallback)
}

// Mention returns the line that mentions the owners, to append to a comment,
// or "" without owners. Only new comments may carry it: GitLab notifies
// again for mentions in edits.
func Mention(owners []string) string {
	owners = Usernames(owners)
	if len(owners) == 0 {
		return ""
	}
	return "\n\ncc @" + strings.Join(owners, " @")
}

// mention matches an "@" that GitLab would read as a mention: one that
// does not follow a word character.
var mention = regexp.MustCompile(`(^|[^\w])@(\w)`)

// quickAction matches a line GitLab could run as a quick action ("/merge",
// "/close", …), which it does in comments and descriptions sent through the
// API, with the bot's permissions.
var quickAction = regexp.MustCompile(`(?m)^([ \t>]*)/`)

// Defuse makes agent-written text inert before it goes to GitLab: mentions
// and quick actions get a zero-width space, so the text pings nobody and
// runs nothing.
func Defuse(s string) string {
	s = mention.ReplaceAllString(s, "$1@​$2")
	return quickAction.ReplaceAllString(s, "$1​/")
}

// RequestReview makes the owners the reviewers of a merge request that has
// none; a human's choice of reviewers is kept. Unknown users are skipped. It
// returns the users requested.
func RequestReview(ctx context.Context, gl gitlab.API, project string, mrIID int64, owners []string) ([]string, error) {
	owners = Usernames(owners)
	if len(owners) == 0 || mrIID == 0 {
		return nil, nil
	}
	mr, err := gl.GetMergeRequest(ctx, project, mrIID)
	if err != nil {
		return nil, fmt.Errorf("getting merge request !%d: %w", mrIID, err)
	}
	if len(mr.Reviewers) > 0 || mr.State != "opened" {
		return nil, nil
	}
	var ids []int64
	var names []string
	for _, o := range owners {
		u, err := gl.FindUser(ctx, o)
		if err != nil {
			return nil, fmt.Errorf("looking up user %q: %w", o, err)
		}
		if u != nil {
			ids, names = append(ids, u.ID), append(names, u.Username)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if _, err := gl.UpdateMergeRequest(ctx, project, mrIID, gitlab.UpdateMergeRequestOptions{ReviewerIDs: ids}); err != nil {
		return nil, fmt.Errorf("requesting reviews on !%d: %w", mrIID, err)
	}
	return names, nil
}
