package runner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// Decisions are the choices an agent made that a reviewer could reasonably
// want differently. Every run posts its decisions in one comment on the issue,
// and the spec agent's decisions for an issue go into its description. Each
// decision has a stable ID, D<issue>-<n> for the implementer and
// D<issue>-P<n> for the planner, so a human can change it later with
// "/agent revisit <id> <instruction>". GitLab is the source of truth for the
// IDs: they are numbered after the highest one found on the issue.

// Decision is one recorded decision.
type Decision struct {
	Title        string   `json:"title"`
	Question     string   `json:"question"`
	Choice       string   `json:"choice"`
	Why          string   `json:"why"`
	Alternatives []string `json:"alternatives"`
	// Supersedes is the ID of a decision this one replaces after a revisit.
	Supersedes string `json:"supersedes,omitempty"`
}

const (
	decisionsHeader = "📌 **Decisions**"
	// maxDecisions bounds one decision comment.
	maxDecisions = 8000
)

var (
	decisionMarker = marker.Decision
	revisitCommand = regexp.MustCompile(`(?is)^\s*(\S+?):?\s+revisit\s+(D\d+-P?\d+)(?:[\s:]+(.*))?$`)
	// revisitWord spots comments that try to revisit but do not parse.
	revisitWord = regexp.MustCompile(`(?is)^\s*(\S+?):?\s+(?:please\s+)?revisit\b`)
)

// isDecisionComment reports whether a note is one of the bot's decision
// comments. Only those carry implementer decisions; the bot's other comments
// quote agent text and are not trusted to.
func isDecisionComment(n gitlab.Note, botID int64) bool {
	return botID != 0 && n.Author.ID == botID && !n.System && strings.HasPrefix(n.Body, decisionsHeader)
}

// nextDecisionNumber returns the number for the next implementer decision of
// an issue: one after the highest in the bot's decision comments. Planner
// decisions (P) live in the description and are numbered on their own.
func nextDecisionNumber(issue *gitlab.Issue, notes []gitlab.Note, botID int64) int {
	highest := 0
	for _, n := range notes {
		if !isDecisionComment(n, botID) {
			continue
		}
		for _, m := range decisionMarker.FindAllStringSubmatch(n.Body, -1) {
			if m[2] != strconv.FormatInt(issue.IID, 10) || m[3] == "P" {
				continue
			}
			if num, _ := strconv.Atoi(m[4]); num > highest {
				highest = num
			}
		}
	}
	return highest + 1
}

// decisionID formats the ID of the n-th decision of an issue.
func decisionID(iid int64, planner bool, n int) string {
	if planner {
		return fmt.Sprintf("D%d-P%d", iid, n)
	}
	return fmt.Sprintf("D%d-%d", iid, n)
}

// cleanDecisionText makes agent text safe for a decision: HTML outside code
// spans is escaped (an unclosed <details> or <!-- would hide everything
// after it), and mentions are broken up.
func cleanDecisionText(s string) string {
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

// renderDecision renders one decision as a markdown section ending with its
// marker.
func renderDecision(id string, d Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#### %s: %s\n", id, cleanDecisionText(oneLine(d.Title)))
	if q := cleanDecisionText(d.Question); q != "" {
		fmt.Fprintf(&b, "**Question:** %s\n", q)
	}
	fmt.Fprintf(&b, "**Chosen:** %s\n", cleanDecisionText(d.Choice))
	if why := cleanDecisionText(d.Why); why != "" {
		fmt.Fprintf(&b, "**Why:** %s\n", why)
	}
	var alts []string
	for _, a := range d.Alternatives {
		if a = cleanDecisionText(oneLine(a)); a != "" {
			alts = append(alts, a)
		}
	}
	if len(alts) > 0 {
		fmt.Fprintf(&b, "**Alternatives:** %s\n", strings.Join(alts, "; "))
	}
	if d.Supersedes != "" {
		fmt.Fprintf(&b, "_Supersedes %s._\n", cleanDecisionText(oneLine(d.Supersedes)))
	}
	fmt.Fprintf(&b, "<!-- ai-operator:decision=%s -->\n", id)
	return b.String()
}

// numberedDecisions pairs decisions with their IDs, starting at first.
type numberedDecision struct {
	ID string
	Decision
}

func numberDecisions(ds []Decision, iid int64, planner bool, first int) []numberedDecision {
	out := make([]numberedDecision, 0, len(ds))
	for _, d := range ds {
		if strings.TrimSpace(d.Title) == "" && strings.TrimSpace(d.Choice) == "" {
			continue
		}
		out = append(out, numberedDecision{ID: decisionID(iid, planner, first+len(out)), Decision: d})
	}
	return out
}

// renderDecisionsComments renders the decision comments of a run: whole
// decisions only, split into several comments if they do not fit into one,
// so no decision is cut and every ID stays findable.
func renderDecisionsComments(runID, prefix string, ds []numberedDecision) []string {
	header := func(part int) string {
		if part > 1 {
			return fmt.Sprintf("%s in run `%s` (continued).\n", decisionsHeader, runID)
		}
		return fmt.Sprintf("%s in run `%s`. Change one with `%s revisit <id> <what you want instead>`.\n", decisionsHeader, runID, prefix)
	}
	var out []string
	var b strings.Builder
	b.WriteString(header(1))
	empty := b.Len()
	for _, d := range ds {
		sec := "\n" + renderDecision(d.ID, d.Decision)
		if b.Len()+len(sec) > maxDecisions && b.Len() > empty {
			out = append(out, b.String())
			b.Reset()
			b.WriteString(header(len(out) + 1))
			empty = b.Len()
		}
		b.WriteString(sec)
	}
	return append(out, b.String())
}

// renderPlannedDecisions renders the decisions section of a planned issue's
// description.
func renderPlannedDecisions(ds []numberedDecision, prefix string) string {
	if len(ds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Decisions\n\n")
	fmt.Fprintf(&b, "_Made by the planning agent. Change one with a comment `%s revisit <id> <what you want instead>`._\n", prefix)
	for _, d := range ds {
		b.WriteString("\n")
		b.WriteString(renderDecision(d.ID, d.Decision))
	}
	return b.String()
}

// revisit is a request to change a decision.
type revisit struct {
	ID          string
	Instruction string
	// Decision is the rendered decision, empty if it was not found.
	Decision string
	// Note is the comment that holds the decision, nil if it is in the issue
	// description or was not found.
	Note *gitlab.Note
	// Superseded is set when the decision was already replaced.
	Superseded bool
	// Malformed is set for a comment that looks like a revisit but does not
	// parse; Instruction holds its first line.
	Malformed bool
}

// findRevisits returns the revisit commands in the given human notes, with
// the decisions they refer to looked up in the issue and the bot's notes.
func findRevisits(issue *gitlab.Issue, all, fresh []gitlab.Note, botID int64, prefix string) []revisit {
	var out []revisit
	index := map[string]int{}
	for _, n := range fresh {
		if n.System || (botID != 0 && n.Author.ID == botID) {
			continue
		}
		m := revisitCommand.FindStringSubmatch(n.Body)
		if m == nil || m[1] != prefix {
			if w := revisitWord.FindStringSubmatch(n.Body); w != nil && w[1] == prefix {
				first, _, _ := strings.Cut(strings.TrimSpace(n.Body), "\n")
				out = append(out, revisit{Malformed: true, Instruction: first})
			}
			continue
		}
		rv := revisit{ID: strings.ToUpper(m[2]), Instruction: strings.TrimSpace(m[3])}
		if strings.Contains(rv.ID, "-P") {
			// Planner decisions live in the description, implementer
			// decisions in the bot's decision comments.
			rv.Decision = decisionSection(issue.Description, rv.ID)
		} else {
			for i := range all {
				if !isDecisionComment(all[i], botID) {
					continue
				}
				if sec := decisionSection(all[i].Body, rv.ID); sec != "" {
					rv.Decision, rv.Note = sec, &all[i]
					break
				}
			}
		}
		rv.Superseded = strings.Contains(rv.Decision, rv.ID+" superseded -->")
		// A later revisit of the same decision replaces an earlier one.
		if i, ok := index[rv.ID]; ok {
			out[i] = rv
			continue
		}
		index[rv.ID] = len(out)
		out = append(out, rv)
	}
	return out
}

// decisionSection extracts the section of decision id from body: from its
// heading to its marker.
func decisionSection(body, id string) string {
	end := -1
	for _, loc := range decisionMarker.FindAllStringSubmatchIndex(body, -1) {
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

// markSuperseded adds a superseded line to decision id in body.
func markSuperseded(body, id, by, runID string) string {
	marker := fmt.Sprintf("<!-- ai-operator:decision=%s -->", id)
	if !strings.Contains(body, marker) {
		return body
	}
	line := fmt.Sprintf("> ⚠️ Revisited in run `%s`", runID)
	if by != "" {
		line = fmt.Sprintf("> ⚠️ Superseded by %s in run `%s`", by, runID)
	}
	return strings.Replace(body, marker, line+"\n"+fmt.Sprintf("<!-- ai-operator:decision=%s superseded -->", id), 1)
}

// buildIgnoredRevisitsPrompt tells the implementer about revisit comments it
// must not act on.
func buildIgnoredRevisitsPrompt(rvs []revisit) string {
	var b strings.Builder
	b.WriteString("## Revisit comments not to act on\n\n")
	for _, rv := range rvs {
		if rv.Malformed {
			fmt.Fprintf(&b, "- %q does not name a decision ID; treat it as a normal comment.\n", rv.Instruction)
		} else {
			fmt.Fprintf(&b, "- %s was already superseded by a later decision; ignore the comment asking to revisit it.\n", rv.ID)
		}
	}
	return b.String()
}

// buildRevisitPrompt tells the implementer which decisions to change.
func buildRevisitPrompt(rvs []revisit) string {
	var b strings.Builder
	b.WriteString("## Decisions to revisit\n\n")
	b.WriteString("A human asked to change these decisions. Change the implementation accordingly, and record the new " +
		"decision with `supersedes` set to the old decision's ID.\n")
	for _, rv := range rvs {
		fmt.Fprintf(&b, "\n### %s\n\n", rv.ID)
		if rv.Decision != "" {
			// The markers are for the runner; the agent must not copy them.
			fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(decisionMarker.ReplaceAllString(rv.Decision, "")))
		} else {
			b.WriteString("(This decision was not found on the issue; use the instruction and the thread.)\n\n")
		}
		instruction := rv.Instruction
		if instruction == "" {
			instruction = "(no instruction given; see the latest comments)"
		}
		fmt.Fprintf(&b, "**Requested change:** %s\n", instruction)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
