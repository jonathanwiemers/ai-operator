// Package marker holds the hidden markers the operator and the runner put
// into GitLab text, so both can find their own issues, comments and
// decisions again. Only text written by the bot user may be trusted to carry
// them; callers check the author.
package marker

import (
	"regexp"
	"strconv"
	"strings"
)

// TrackingIssue marks the operator's tracking issue of an autopilot
// milestone.
const TrackingIssue = "<!-- ai-operator:autopilot -->"

// IsTrackingIssue reports whether an issue description belongs to the
// tracking issue of an autopilot milestone.
func IsTrackingIssue(description string) bool {
	return strings.Contains(description, TrackingIssue)
}

// UntrustedData starts the part of a text that quotes output nobody vetted
// (CI job logs): the runner gives everything after it to the agent as data,
// never as instructions. It only lowers trust, so any author may write it.
const UntrustedData = "<!-- ai-operator:untrusted-data -->"

// Decision matches the marker after a recorded decision. Groups: 1 the ID
// (D42-3 or D42-P1), 2 the issue IID, 3 "P" for planning decisions, 4 the
// number, 5 " superseded" once it was replaced.
var Decision = regexp.MustCompile(`<!-- ai-operator:decision=(D(\d+)-(P?)(\d+))( superseded)? -->`)

// decisionHeading matches the heading a decision section starts with.
var decisionHeading = regexp.MustCompile(`(?m)^#### (D\d+-P?\d+): (.*)$`)

// DecisionRef is a decision found in GitLab text.
type DecisionRef struct {
	ID         string
	Title      string
	Superseded bool
}

// Decisions lists the decisions in body: every heading followed by its own
// marker.
func Decisions(body string) []DecisionRef {
	var out []DecisionRef
	for _, h := range decisionHeading.FindAllStringSubmatchIndex(body, -1) {
		id := body[h[2]:h[3]]
		rest := body[h[1]:]
		m := Decision.FindStringSubmatch(rest)
		if m == nil || m[1] != id {
			continue
		}
		// The marker must belong to this section, not a later one.
		if next := decisionHeading.FindStringIndex(rest); next != nil && next[0] < strings.Index(rest, m[0]) {
			continue
		}
		out = append(out, DecisionRef{ID: id, Title: strings.TrimSpace(body[h[4]:h[5]]), Superseded: m[5] != ""})
	}
	return out
}

// RoutingReply marks the operator's answers to "/agent use" and "/agent
// effort", so a recreated task does not apply the commands again.
const RoutingReply = "<!-- ai-operator:routing -->"

// DecisionsHeader starts every decision comment of the bot.
const DecisionsHeader = "📌 **Decisions**"

// Revisit parses "<prefix> revisit <id> <instruction>": groups 1 the prefix,
// 2 the ID, 3 the instruction.
var Revisit = regexp.MustCompile(`(?is)^\s*(\S+?):?\s+revisit\s+(D\d+-P?\d+)(?:[\s:]+(.*))?$`)

// RevisitOf marks a follow-up issue created to revisit a decision of another
// issue; group 1 is the decision ID.
var RevisitOf = regexp.MustCompile(`<!-- ai-operator:revisit=(D\d+-P?\d+) -->`)

// DecisionSection extracts the section of decision id from body: from its
// heading to its marker. It is empty if body has no such decision.
func DecisionSection(body, id string) string {
	end := -1
	for _, loc := range Decision.FindAllStringSubmatchIndex(body, -1) {
		if body[loc[2]:loc[3]] == id {
			end = loc[1]
			break
		}
	}
	if end < 0 {
		return ""
	}
	start := strings.LastIndex(body[:end], "#### "+id+":")
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(body[start:end])
}

// DecisionIssue returns the issue IID a decision ID belongs to.
func DecisionIssue(id string) int64 {
	m := decisionID.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	iid, _ := strconv.ParseInt(m[1], 10, 64)
	return iid
}

var decisionID = regexp.MustCompile(`^D(\d+)-P?\d+$`)

// Clean makes untrusted text (agent output, human instructions) safe to put
// into text the bot writes: HTML outside code spans is escaped (an unclosed
// <details> or <!-- would hide everything after it, and a copied marker
// would count as the bot's), and mentions are broken up.
func Clean(s string) string {
	s = strings.TrimSpace(s)
	// With unbalanced backticks there are no reliable code spans: escape
	// everything.
	spans := strings.Count(s, "`")%2 == 0
	var b strings.Builder
	inCode := false
	for _, r := range s {
		switch {
		case r == '`' && spans:
			inCode = !inCode
		case r == '<' && !inCode:
			b.WriteString("&lt;")
			continue
		case r == '@' && !inCode:
			b.WriteString("@\u200b")
			continue
		}
		b.WriteRune(r)
	}
	// Markers inside code spans must not count either.
	return strings.ReplaceAll(b.String(), "<!-- ai-operator:", "<!-- ai-operator :")
}

// Proposal ends a proposal of the pilot: its ID and the state it was made
// on. Group 1 is the ID.
var Proposal = regexp.MustCompile(`<!-- ai-operator:proposal id=(\d+) state=(\w*) -->`)

// Prompt ends the comment that mirrors a prompt. Groups: 1 the ID (Q3), 2
// the Unix time it was posted, which tells it from a prompt with the same
// ID before it.
var Prompt = regexp.MustCompile(`<!-- ai-operator:prompt=(Q\d+) posted=(\d+) -->`)

// Retrospective marks the pilot's retrospective of a finished autopilot
// milestone on its tracking issue; owners open its follow-ups in its thread.
const Retrospective = "<!-- ai-operator:retrospective -->"

// FlakyOccurrence starts the comment of one occurrence of a flaky job on
// its issue, with the pipeline: "<!-- ai-operator:flaky-occurrence=<id> -->".
const FlakyOccurrence = "<!-- ai-operator:flaky-occurrence="

// FlakyPipeline is the pipeline of an occurrence comment, false for
// another comment.
func FlakyPipeline(body string) (int64, bool) {
	rest, ok := strings.CutPrefix(body, FlakyOccurrence)
	id, _, _ := strings.Cut(rest, " ")
	pid, err := strconv.ParseInt(id, 10, 64)
	return pid, ok && err == nil
}
