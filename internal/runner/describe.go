package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The runner owns the part of the merge request description between these
// markers; text around it (the closing pattern, a human's notes) stays.
const (
	descriptionBegin = "<!-- ai-operator:description -->"
	descriptionEnd   = "<!-- /ai-operator:description -->"
)

// descriptionPlaceholder fills the block until the first push is described.
const descriptionPlaceholder = "_The agent is working on this. The description follows with the first push; progress, reports and decisions are in the comments._"

// legacyDescription is what merge requests were opened with before the
// runner wrote descriptions.
const legacyDescription = "Closes #%d\n\nThe agent works on this; its progress, reports and decisions are in the comments."

const describeSystemPrompt = `You write the description of a merge request in an automated GitLab
workflow. Another agent implemented the change; reviewers and a human read
your description to understand it before they read the diff.

Describe the whole change of the branch against the default branch as it is
now, not the history of how it got there. You get the issue, the diff and the
previous description, if there is one: keep what is still right, fix what
changed.

- why: the problem or goal, in one to three sentences, from the issue.
- what: what changes for users or callers, as a short list.
- how: the approach and the main code changes (files, types, functions) a
  reviewer should know, as a short list; trade-offs and anything to look at
  closely.
- verification: the tests added or changed and how the change was checked.
  Claim no checks you cannot see in the diff or the implementer's summary.
- diagram: a mermaid diagram of the change (flowchart, sequenceDiagram,
  stateDiagram-v2, classDiagram or erDiagram), only when it makes the change
  easier to grasp: a new or changed flow between components, a state machine,
  a data model. At most about 15 nodes, no styling. Empty for small, local,
  docs-only or config-only changes. Only the diagram source, no code fence.

Markdown without headings, terse. Do not mention people with "@". You cannot
change files or talk to GitLab; a separate program updates the merge request
from your structured answer.`

const describeSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["why", "what", "how", "verification", "diagram"],
  "properties": {
    "why": {"type": "string"},
    "what": {"type": "string"},
    "how": {"type": "string"},
    "verification": {"type": "string"},
    "diagram": {"type": "string", "description": "Mermaid source without a code fence, or empty."}
  }
}`

// DescribeAnswer is the describer's answer.
type DescribeAnswer struct {
	Why          string `json:"why"`
	What         string `json:"what"`
	How          string `json:"how"`
	Verification string `json:"verification"`
	Diagram      string `json:"diagram"`
}

// newDescription is the description a merge request is opened with.
func newDescription(iid int64) string {
	return descriptionBlock(descriptionPlaceholder) + fmt.Sprintf("\n\nCloses #%d", iid)
}

func descriptionBlock(body string) string {
	return descriptionBegin + "\n" + body + "\n" + descriptionEnd
}

// managedDescription returns the text of the runner's block in a
// description, and whether there is one.
func managedDescription(desc string) (string, bool) {
	i := strings.Index(desc, descriptionBegin)
	if i < 0 {
		return "", false
	}
	rest := desc[i+len(descriptionBegin):]
	j := strings.Index(rest, descriptionEnd)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:j]), true
}

// withDescription puts body into the runner's block of desc. Without a
// block, the legacy placeholder is replaced; a description a human wrote
// follows the block.
func withDescription(desc, body string, iid int64) string {
	block := descriptionBlock(body)
	if i := strings.Index(desc, descriptionBegin); i >= 0 {
		if j := strings.Index(desc[i:], descriptionEnd); j >= 0 {
			return desc[:i] + block + desc[i+j+len(descriptionEnd):]
		}
	}
	if d := strings.TrimSpace(desc); d == "" || d == fmt.Sprintf(legacyDescription, iid) {
		return block + fmt.Sprintf("\n\nCloses #%d", iid)
	}
	return block + "\n\n" + desc
}

// mermaidTypes are the diagram types a description may carry.
var mermaidTypes = []string{"flowchart", "graph", "sequenceDiagram", "stateDiagram", "stateDiagram-v2", "classDiagram", "erDiagram"}

// maxDiagram bounds the diagram source; bigger ones do not help a reviewer.
const maxDiagram = 5000

// cleanDiagram returns the diagram source without a code fence, or "" if it
// is not a diagram the description should carry.
func cleanDiagram(d string) string {
	d = strings.TrimSpace(d)
	if rest, ok := strings.CutPrefix(d, "```"); ok {
		rest = strings.TrimPrefix(rest, "mermaid")
		d = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	if d == "" || len(d) > maxDiagram || strings.Contains(d, "```") || strings.Contains(d, "<!--") {
		return ""
	}
	first := strings.Fields(d)[0]
	for _, t := range mermaidTypes {
		if first == t {
			return d
		}
	}
	return ""
}

// prose makes agent-written text safe for the description: no mentions, no
// HTML comments that could end the runner's block.
func prose(s string) string {
	return notify.Defuse(strings.ReplaceAll(strings.TrimSpace(s), "<!--", "&lt;!--"))
}

// renderDescription renders the describer's answer; the diagram comes last.
func renderDescription(a DescribeAnswer) (string, error) {
	if strings.TrimSpace(a.What) == "" {
		return "", fmt.Errorf("the describer returned no description")
	}
	var b strings.Builder
	for _, sec := range []struct{ title, text string }{
		{"Why", a.Why}, {"What", a.What}, {"How", a.How}, {"Verification", a.Verification},
	} {
		if strings.TrimSpace(sec.text) != "" {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", sec.title, prose(sec.text))
		}
	}
	if d := cleanDiagram(a.Diagram); d != "" {
		fmt.Fprintf(&b, "### Diagram\n\n```mermaid\n%s\n```\n\n", d)
	}
	b.WriteString("<sub>Written by the agent from the branch's diff and rewritten after each push that changes it; text outside this part stays.</sub>")
	return b.String(), nil
}

func buildDescribePrompt(issue *gitlab.Issue, summary, previous, base, stat, diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Describe the merge request of issue #%d: %s\n\n", issue.IID, issue.Title)
	fmt.Fprintf(&b, "## Issue description\n\n%s\n\n", strings.TrimSpace(issue.Description))
	if s := strings.TrimSpace(summary); s != "" {
		fmt.Fprintf(&b, "## Summary of the implementer's last run\n\n%s\n\n", s)
	}
	if previous != "" && previous != descriptionPlaceholder {
		fmt.Fprintf(&b, "## Previous description\n\n%s\n\n", previous)
	}
	fmt.Fprintf(&b, "## Changed files\n\n```\n%s\n```\n\n", stat)
	if len(diff) <= maxInlineDiff {
		fmt.Fprintf(&b, "## Diff\n\n```diff\n%s\n```\n", diff)
	} else {
		fmt.Fprintf(&b, "The diff is too large to include. Inspect it with `git diff origin/%s...HEAD -- <file>`.\n", base)
	}
	return b.String()
}

// describe rewrites the runner's part of the merge request description when
// the branch's diff changed since it was last described. It never fails the
// run: a problem comes back as a notice for the report. first is set when
// the description was written for the first time.
func (r *Runner) describe(ctx context.Context, gl gitlab.API, glProject string, role aiv1.DescribeSpec, issue *gitlab.Issue, mrIID int64,
	base, summary string, st *state, out *Outcome) (first bool, notice string) {
	s := r.step(ctx, "describe merge request")
	skip := func(why string, err error) (bool, string) {
		s.done("not updated: "+why, "error", err.Error())
		return false, "The merge request description was not updated: " + why
	}
	diff, stat, err := r.Agent.Diff(ctx, base)
	if err != nil {
		return skip(truncate(err.Error(), 300), err)
	}
	if strings.TrimSpace(diff) == "" {
		s.done("no changes to describe")
		return false, ""
	}
	sum := sha256.Sum256([]byte(diff))
	hash := hex.EncodeToString(sum[:])
	if hash == st.DescribedDiff {
		s.done("the diff is described already")
		return false, ""
	}
	mr, err := gl.GetMergeRequest(ctx, glProject, mrIID)
	if err != nil {
		return skip("reading the merge request failed", err)
	}
	previous, ok := managedDescription(mr.Description)
	first = !ok || previous == descriptionPlaceholder
	inv := roleInvocation(role.RoleSpec, describeSystemPrompt, readOnlyTools, describeSchema, true)
	inv.NoPersistence = true
	res, err := r.invoke(ctx, role.RoleSpec, inv, buildDescribePrompt(issue, summary, previous, base, stat, diff))
	if res != nil {
		out.CostUSD += res.CostUSD
	}
	var answer DescribeAnswer
	if err == nil {
		err = res.Decode(&answer)
	}
	if err != nil {
		return skip("the describer failed: "+truncate(err.Error(), 300), err)
	}
	// Describers are read-only; discard anything they touched anyway.
	if err := r.Agent.Reset(ctx); err != nil {
		return skip("resetting the checkout failed", err)
	}
	body, err := renderDescription(answer)
	if err != nil {
		return skip(err.Error(), err)
	}
	if _, err := gl.UpdateMergeRequest(ctx, glProject, mrIID, gitlab.UpdateMergeRequestOptions{
		Description: withDescription(mr.Description, body, issue.IID),
	}); err != nil {
		return skip("GitLab refused it", err)
	}
	st.DescribedDiff = hash
	_ = r.saveState(*st)
	s.done(agentResult(role.RoleSpec, res, ""), "costUSD", res.CostUSD, "diagram", cleanDiagram(answer.Diagram) != "", "first", first)
	return first, ""
}
