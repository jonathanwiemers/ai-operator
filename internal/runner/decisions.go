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
	revisitCommand = regexp.MustCompile(`(?s)^\s*(\S+)\s+revisit\s+(D\d+-P?\d+)\b[\s:]*(.*)$`)
)

// nextDecisionNumber returns the number for the next decision of issue iid:
// planner decisions (P) and implementer decisions are numbered separately.
// Only the bot's notes and the issue description are trusted.
func nextDecisionNumber(issue *gitlab.Issue, notes []gitlab.Note, botID int64, planner bool) int {
	highest := 0
	scan := func(body string) {
		for _, m := range decisionMarker.FindAllStringSubmatch(body, -1) {
			if m[2] != strconv.FormatInt(issue.IID, 10) || (m[3] == "P") != planner {
				continue
			}
			if n, _ := strconv.Atoi(m[4]); n > highest {
				highest = n
			}
		}
	}
	scan(issue.Description)
	for _, n := range notes {
		if botID != 0 && n.Author.ID == botID && !n.System {
			scan(n.Body)
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

// cleanDecisionText neutralises markers and mentions in agent text that goes
// into a decision.
func cleanDecisionText(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "<!-- ai-operator:", "<!-- ai-operator :")
	return strings.ReplaceAll(s, "@", "@​")
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
	for i, d := range ds {
		if strings.TrimSpace(d.Title) == "" && strings.TrimSpace(d.Choice) == "" {
			continue
		}
		out = append(out, numberedDecision{ID: decisionID(iid, planner, first+i), Decision: d})
	}
	return out
}

// renderDecisionsComment renders the decision comment of a run.
func renderDecisionsComment(runID, prefix string, ds []numberedDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s in run `%s`. Change one with `%s revisit <id> <what you want instead>`.\n", decisionsHeader, runID, prefix)
	for _, d := range ds {
		b.WriteString("\n")
		b.WriteString(renderDecision(d.ID, d.Decision))
	}
	return truncate(b.String(), maxDecisions)
}

// renderPlannedDecisions renders the decisions section of a planned issue's
// description.
func renderPlannedDecisions(ds []numberedDecision) string {
	if len(ds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Decisions\n\n")
	b.WriteString("_Made by the planning agent. Change one with a comment `/agent revisit <id> <what you want instead>`._\n")
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
}

// findRevisits returns the revisit commands in the given human notes, with
// the decisions they refer to looked up in the issue and the bot's notes.
func findRevisits(issue *gitlab.Issue, all, fresh []gitlab.Note, botID int64, prefix string) []revisit {
	var out []revisit
	seen := map[string]bool{}
	for _, n := range fresh {
		if n.System || (botID != 0 && n.Author.ID == botID) {
			continue
		}
		m := revisitCommand.FindStringSubmatch(n.Body)
		if m == nil || m[1] != prefix || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		rv := revisit{ID: m[2], Instruction: strings.TrimSpace(m[3])}
		if sec := decisionSection(issue.Description, rv.ID); sec != "" {
			rv.Decision = sec
		} else {
			for i := range all {
				bn := &all[i]
				if botID == 0 || bn.Author.ID != botID || bn.System {
					continue
				}
				if sec := decisionSection(bn.Body, rv.ID); sec != "" {
					rv.Decision, rv.Note = sec, bn
					break
				}
			}
		}
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

// buildRevisitPrompt tells the implementer which decisions to change.
func buildRevisitPrompt(rvs []revisit) string {
	var b strings.Builder
	b.WriteString("## Decisions to revisit\n\n")
	b.WriteString("A human asked to change these decisions. Change the implementation accordingly, and record the new " +
		"decision with `supersedes` set to the old decision's ID.\n")
	for _, rv := range rvs {
		fmt.Fprintf(&b, "\n### %s\n\n", rv.ID)
		if rv.Decision != "" {
			fmt.Fprintf(&b, "%s\n\n", rv.Decision)
		} else {
			b.WriteString("(This decision was not found on the issue; use the instruction and the thread.)\n\n")
		}
		fmt.Fprintf(&b, "**Requested change:** %s\n", rv.Instruction)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
