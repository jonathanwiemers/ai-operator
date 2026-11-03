// Package chatcmd parses Desk chat command bodies (kind=command).
package chatcmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
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
}

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
	switch c.Cmd {
	case "retry", "review", "pause", "resume":
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
	case "accept", "reject":
		if c.Proposal <= 0 {
			return Command{}, fmt.Errorf("%s needs proposal > 0", c.Cmd)
		}
		if c.Engine != "" || c.Model != "" || c.Effort != "" || c.Default || c.Issue != 0 {
			return Command{}, fmt.Errorf("%s takes only proposal", c.Cmd)
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
// in slice 9a (retry, review, use, effort).
func (c Command) ImplementOnly() bool {
	switch c.Cmd {
	case "retry", "review", "use", "effort":
		return true
	default:
		return false
	}
}

// Routing turns a use or effort command into the GitLab routing shape so
// command.Apply can be reused.
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
	default:
		return command.Routing{}, false
	}
}
