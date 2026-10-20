// Package marker holds the hidden markers the operator and the runner put
// into GitLab text, so both can find their own issues, comments and
// decisions again. Only text written by the bot user may be trusted to carry
// them; callers check the author.
package marker

import (
	"regexp"
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
