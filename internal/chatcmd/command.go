// Package chatcmd parses Desk chat command bodies (kind=command).
package chatcmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// modelRE is a model name: no spaces or backticks, at most 64 characters.
var modelRE = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{1,64}$`)

// Command is a structured Desk chat command body.
type Command struct {
	Cmd      string `json:"cmd"`
	Engine   string `json:"engine,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Issue    int64  `json:"issue,omitempty"`
	Proposal int64  `json:"proposal,omitempty"`
	// SHA is the head a merge is for.
	SHA string `json:"sha,omitempty"`
	// Prompt ("Q3") is the prompt an answer is for, with either the chosen
	// Option ("A".."E") or the owner's free Text.
	Prompt string `json:"prompt,omitempty"`
	Option string `json:"option,omitempty"`
	Text   string `json:"text,omitempty"`
	// Decision ("D12-3") is the decision a revisit is for, with the change
	// asked for as Text, or without Text to open a revisit prompt.
	Decision string `json:"decision,omitempty"`
	// USD is the budget of the issue ("40", or "default" for the
	// project's), for budget.
	USD string `json:"usd,omitempty"`
	// FollowUps are the retrospective's follow-ups (1..9) a draft is for,
	// deduplicated and sorted by Parse.
	FollowUps []int `json:"followUps,omitempty"`
}

// maxFollowUp is the highest follow-up number of a retrospective.
const maxFollowUp = 9

// shaRE is an abbreviated or full commit SHA.
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// promptRE is a prompt id, optionRE an option id.
var (
	promptRE = regexp.MustCompile(`^Q[1-9][0-9]*$`)
	optionRE = regexp.MustCompile(`^[A-E]$`)
)

// Parse reads a command body. Unknown fields are rejected.
func Parse(body string) (Command, error) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var c Command
	if err := dec.Decode(&c); err != nil {
		return Command{}, fmt.Errorf("invalid command: %w", err)
	}
	// Trailing junk after one JSON value.
	if dec.More() {
		return Command{}, fmt.Errorf("invalid command: trailing data")
	}
	c.Cmd = strings.ToLower(strings.TrimSpace(c.Cmd))
	if c.Cmd == "" {
		return Command{}, fmt.Errorf("cmd is required")
	}
	c.Engine = strings.ToLower(strings.TrimSpace(c.Engine))
	c.Model = strings.TrimSpace(c.Model)
	c.Effort = strings.ToLower(strings.TrimSpace(c.Effort))
	c.SHA = strings.ToLower(strings.TrimSpace(c.SHA))
	if c.SHA != "" && c.Cmd != "merge" {
		return Command{}, fmt.Errorf("%s takes no sha", c.Cmd)
	}
	if (c.Prompt != "" || c.Option != "" || c.Text != "") && c.Cmd != "answer" && c.Cmd != "revisit" {
		return Command{}, fmt.Errorf("%s takes no prompt, option or text", c.Cmd)
	}
	if c.Decision != "" && c.Cmd != "revisit" {
		return Command{}, fmt.Errorf("%s takes no decision", c.Cmd)
	}
	c.Prompt = strings.ToUpper(strings.TrimSpace(c.Prompt))
	c.Option = strings.ToUpper(strings.TrimSpace(c.Option))
	c.Text = strings.TrimSpace(c.Text)
	c.Decision = strings.ToUpper(strings.TrimSpace(c.Decision))
	c.USD = strings.TrimSpace(c.USD)
	if c.USD != "" && c.Cmd != "budget" {
		return Command{}, fmt.Errorf("%s takes no usd", c.Cmd)
	}
	if c.FollowUps != nil && c.Cmd != "draft" {
		return Command{}, fmt.Errorf("%s takes no followUps", c.Cmd)
	}
	switch c.Cmd {
	case "retry", "review", "pause", "resume", "start", "unstart", "endmilestone":
		if err := c.noArgs(); err != nil {
			return Command{}, err
		}
	case "use":
		if err := c.parseUse(); err != nil {
			return Command{}, err
		}
	case "effort":
		if err := c.parseEffort(); err != nil {
			return Command{}, err
		}
	case "skip":
		if c.Issue <= 0 {
			return Command{}, fmt.Errorf("skip needs issue > 0")
		}
		if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Proposal != 0 {
			return Command{}, fmt.Errorf("skip takes only issue")
		}
	case "merge":
		if c.Issue <= 0 || !shaRE.MatchString(c.SHA) {
			return Command{}, fmt.Errorf("merge needs issue > 0 and the sha of its head")
		}
		if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Proposal != 0 {
			return Command{}, fmt.Errorf("merge takes only issue and sha")
		}
	case "accept", "reject":
		if c.Proposal <= 0 {
			return Command{}, fmt.Errorf("%s needs proposal > 0", c.Cmd)
		}
		if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 {
			return Command{}, fmt.Errorf("%s takes only proposal", c.Cmd)
		}
	case "answer":
		if err := c.parseAnswer(); err != nil {
			return Command{}, err
		}
	case "budget":
		if err := c.parseBudget(); err != nil {
			return Command{}, err
		}
	case "revisit":
		if err := c.parseRevisit(); err != nil {
			return Command{}, err
		}
	case "draft":
		if err := c.parseDraft(); err != nil {
			return Command{}, err
		}
	default:
		return Command{}, fmt.Errorf("unknown cmd %q", c.Cmd)
	}
	return c, nil
}

func (c Command) noArgs() error {
	if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 || c.Proposal != 0 {
		return fmt.Errorf("%s takes no arguments", c.Cmd)
	}
	return nil
}

func (c *Command) parseAnswer() error {
	switch {
	case !promptRE.MatchString(c.Prompt):
		return fmt.Errorf("answer needs a prompt Q<n>")
	case (c.Option == "") == (c.Text == ""):
		return fmt.Errorf("answer needs either an option or a text")
	case c.Option != "" && !optionRE.MatchString(c.Option):
		return fmt.Errorf("option %q is not A to E", c.Option)
	case len(c.Text) > aiv1.MaxPromptText:
		return fmt.Errorf("the text is longer than %d bytes", aiv1.MaxPromptText)
	case c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 || c.Proposal != 0:
		return fmt.Errorf("answer takes only prompt and option or text")
	}
	return nil
}

// parseRevisit checks a revisit command: a decision id, and optionally the
// change asked for.
func (c *Command) parseRevisit() error {
	switch {
	case marker.DecisionIssue(c.Decision) == 0:
		return fmt.Errorf("revisit needs a decision D<issue>-<n>")
	case len(c.Text) > aiv1.MaxPromptText:
		return fmt.Errorf("the text is longer than %d bytes", aiv1.MaxPromptText)
	case c.Prompt != "" || c.Option != "" || c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 || c.Proposal != 0:
		return fmt.Errorf("revisit takes only decision and text")
	}
	return nil
}

// parseDraft checks a draft command: the follow-ups' numbers, each 1..9,
// at least one.
func (c *Command) parseDraft() error {
	if c.noArgs() != nil {
		return fmt.Errorf("draft takes only followUps")
	}
	if len(c.FollowUps) == 0 {
		return fmt.Errorf("draft needs followUps, e.g. [1, 3]")
	}
	for _, n := range c.FollowUps {
		if n < 1 || n > maxFollowUp {
			return fmt.Errorf("follow-up %d is not 1 to %d", n, maxFollowUp)
		}
	}
	slices.Sort(c.FollowUps)
	c.FollowUps = slices.Compact(c.FollowUps)
	return nil
}

func (c *Command) parseUse() error {
	if c.Default {
		if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Issue != 0 || c.Proposal != 0 {
			return fmt.Errorf("use default takes no other arguments")
		}
		return nil
	}
	if c.Engine != string(aiv1.EngineClaude) && c.Engine != string(aiv1.EngineCursor) {
		return fmt.Errorf("use needs engine claude or cursor")
	}
	if c.Model == "" {
		return fmt.Errorf("use needs a model")
	}
	// The model becomes a label and is echoed in a code span: one plain
	// token, like the "/agent use" argument.
	if !modelRE.MatchString(c.Model) {
		return fmt.Errorf("model %q is not a plain model name", c.Model)
	}
	if c.Effort != "" && !aiv1.ValidEffort(c.Effort) {
		return fmt.Errorf("effort %q is not valid", c.Effort)
	}
	if c.Issue != 0 || c.Proposal != 0 {
		return fmt.Errorf("use takes no issue or proposal")
	}
	return nil
}

func (c *Command) parseEffort() error {
	if c.Effort == "" {
		return fmt.Errorf("effort needs an effort level or \"default\"")
	}
	if c.Effort != "default" && !aiv1.ValidEffort(c.Effort) {
		return fmt.Errorf("effort %q is not valid", c.Effort)
	}
	if c.Engine != "" || c.Model != "" || c.Default || c.Issue != 0 || c.Proposal != 0 {
		return fmt.Errorf("effort takes only effort")
	}
	return nil
}

// ImplementOnly reports whether the command is handled on Implement tasks
// (retry, review, use, effort, budget).
func (c Command) ImplementOnly() bool {
	switch c.Cmd {
	case "retry", "review", "use", "effort", "budget":
		return true
	default:
		return false
	}
}

// Autopilot reports whether the command is an autopilot verb (pause, resume,
// skip, merge, start, unstart, endmilestone) for the Spec task of an
// autopilot milestone.
func (c Command) Autopilot() bool {
	switch c.Cmd {
	case "pause", "resume", "skip", "merge", "start", "unstart", "endmilestone":
		return true
	default:
		return false
	}
}

// ProposalOnly reports whether the command answers a pilot proposal
// (accept, reject) on a Coordinate task.
func (c Command) ProposalOnly() bool {
	switch c.Cmd {
	case "accept", "reject":
		return true
	default:
		return false
	}
}

// PromptCmd reports whether the command answers a prompt (answer); the
// Coordinate task takes the milestone's.
func (c Command) PromptCmd() bool {
	return c.Cmd == "answer"
}

// RevisitCmd reports whether the command revisits a decision of the
// milestone (revisit); the Coordinate task takes it.
func (c Command) RevisitCmd() bool {
	return c.Cmd == "revisit"
}

// DraftCmd reports whether the command asks the milestone's pilot for a
// draft of a follow-up milestone (draft); the Coordinate task takes it.
func (c Command) DraftCmd() bool {
	return c.Cmd == "draft"
}

// Routing turns a use, effort or budget command into the GitLab routing
// shape so command.Apply can be reused.
func (c Command) Routing() (command.Routing, bool) {
	switch c.Cmd {
	case "use":
		if c.Default {
			return command.Routing{Verb: "use", Args: []string{"default"}, Only: true}, true
		}
		args := []string{c.Engine, c.Model}
		if c.Effort != "" {
			args = append(args, c.Effort)
		}
		return command.Routing{Verb: "use", Args: args, Only: true}, true
	case "effort":
		return command.Routing{Verb: "effort", Args: []string{c.Effort}, Only: true}, true
	case "budget":
		return command.Routing{Verb: "budget", Args: []string{c.USD}, Only: true}, true
	default:
		return command.Routing{}, false
	}
}

// parseBudget checks a budget command: usd is a budget above 0 or
// "default".
func (c *Command) parseBudget() error {
	if c.USD == "" {
		return fmt.Errorf("budget needs usd, e.g. \"40\", or \"default\"")
	}
	if strings.EqualFold(c.USD, "default") {
		c.USD = "default"
	} else if _, err := aiv1.ParseBudget(c.USD); err != nil {
		return err
	}
	if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 || c.Proposal != 0 {
		return fmt.Errorf("budget takes only usd")
	}
	return nil
}
