package marker

import (
	"regexp"
	"strings"
)

// ParsedDecision is one decision section read back into its fields.
type ParsedDecision struct {
	ID, Title, Question, Choice, Why string
	Alternatives                     []string
	Supersedes                       string // from the _Supersedes <id>._ line
	Superseded                       bool   // the marker's " superseded" suffix
	SupersededBy                     string // from "Superseded by <id>", empty for "Revisited in run"
}

var (
	supersedesLine   = regexp.MustCompile(`^_Supersedes (\S+?)\._\s*$`)
	supersededByLine = regexp.MustCompile("^> ⚠️ Superseded by (D\\d+-P?\\d+)")
)

// ParseDecision reads a section cut out by DecisionSection (or any text with
// the heading and the marker). It is false without a "#### <id>: " heading or
// a marker for the same ID.
func ParseDecision(section string) (ParsedDecision, bool) {
	var d ParsedDecision
	h := decisionHeading.FindStringSubmatchIndex(section)
	if h == nil {
		return d, false
	}
	d.ID = section[h[2]:h[3]]
	d.Title = strings.TrimSpace(section[h[4]:h[5]])
	rest := section[h[1]:]
	var m []string
	for _, c := range Decision.FindAllStringSubmatch(rest, -1) {
		if c[1] == d.ID {
			m = c
			break
		}
	}
	if m == nil {
		return d, false
	}
	d.Superseded = m[5] != ""

	var field *string
	var alts string
	for _, line := range strings.Split(rest, "\n") {
		switch {
		case strings.HasPrefix(line, "**Question:**"):
			field, line = &d.Question, strings.TrimPrefix(line, "**Question:**")
		case strings.HasPrefix(line, "**Chosen:**"):
			field, line = &d.Choice, strings.TrimPrefix(line, "**Chosen:**")
		case strings.HasPrefix(line, "**Why:**"):
			field, line = &d.Why, strings.TrimPrefix(line, "**Why:**")
		case strings.HasPrefix(line, "**Alternatives:**"):
			field, line = &alts, strings.TrimPrefix(line, "**Alternatives:**")
		case strings.HasPrefix(line, "_Supersedes"):
			field = nil
			if s := supersedesLine.FindStringSubmatch(line); s != nil {
				d.Supersedes = s[1]
			}
			continue
		case strings.HasPrefix(line, "> ⚠️"):
			field = nil
			if s := supersededByLine.FindStringSubmatch(line); s != nil {
				d.SupersededBy = s[1]
			}
			continue
		case strings.HasPrefix(line, "<!-- ai-operator:decision="):
			field = nil
			continue
		}
		if field != nil {
			*field += line + "\n"
		}
	}
	d.Question = strings.TrimSpace(d.Question)
	d.Choice = strings.TrimSpace(d.Choice)
	d.Why = strings.TrimSpace(d.Why)
	for _, a := range strings.Split(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(alts), ";")), "; ") {
		if a = strings.TrimSpace(a); a != "" {
			d.Alternatives = append(d.Alternatives, a)
		}
	}
	return d, true
}

// ParseDecisions parses every decision section of body, in order.
func ParseDecisions(body string) []ParsedDecision {
	var out []ParsedDecision
	for _, ref := range Decisions(body) {
		if d, ok := ParseDecision(DecisionSection(body, ref.ID)); ok {
			out = append(out, d)
		}
	}
	return out
}
