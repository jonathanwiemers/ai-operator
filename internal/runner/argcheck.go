package runner

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Values from outside the runner (branch names from GitLab and CRDs, models
// and efforts from labels and CRDs, session IDs from engine output) end up in
// the argv of git and the engine CLIs. These checks keep them operands: a
// value that could be read as an option, a ref expression or a refspec is
// refused before any process starts (lean-codebase spec, R12).

// checkBranch refuses a branch name git check-ref-format --branch would
// refuse, and any name that starts with "-", so it can never be read as an
// option, a revision range, a refspec or a path out of refs/heads/.
func checkBranch(name string) error {
	if why := branchProblem(name); why != "" {
		return fmt.Errorf("branch name %q is not allowed: %s", name, why)
	}
	return nil
}

func branchProblem(name string) string {
	switch {
	case name == "":
		return "it is empty"
	case len(name) > 255:
		return "it is longer than 255 bytes"
	case strings.HasPrefix(name, "-"):
		return "it starts with -"
	case name == "@" || strings.Contains(name, "@{"):
		return "it is @ or contains @{"
	case strings.Contains(name, ".."), strings.Contains(name, "//"):
		return "it contains .. or //"
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."):
		return "it starts or ends with / or ends with ."
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return fmt.Sprintf("it contains %q", r)
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".lock") {
			return "a component starts with . or ends with .lock"
		}
	}
	return ""
}

// checkBranches is checkBranch for each name.
func checkBranches(names ...string) error {
	for _, n := range names {
		if err := checkBranch(n); err != nil {
			return err
		}
	}
	return nil
}

// engineValue is what a model, an effort or a session ID may look like: no
// leading "-", no whitespace, no shell or quoting characters. It admits names
// such as "claude-opus-5-5", "sonnet[1m]", "us.anthropic.claude-x-v1:0" and
// UUIDs.
var engineValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\[\]-]{0,199}$`)

// checkEngineArgs refuses an invocation whose model, effort, session ID or
// tool lists would not reach the engine as the value of their flag: claude's
// --resume takes an optional value, so "--resume -x" would make -x a flag.
func checkEngineArgs(inv Invocation) error {
	for _, v := range [][2]string{{"model", inv.Model}, {"effort", inv.Effort}, {"session ID", inv.Resume}} {
		if v[1] != "" && !engineValue.MatchString(v[1]) {
			return fmt.Errorf("%s %q is not allowed: it must start with a letter or digit and hold no spaces", v[0], v[1])
		}
	}
	for _, tool := range slices.Concat(inv.AllowedTools, inv.DisallowedTools) {
		if strings.HasPrefix(tool, "-") {
			return fmt.Errorf("tool %q is not allowed: it starts with -", tool)
		}
	}
	return nil
}

// promptOperand keeps a prompt given as the last argument (cursor-agent's)
// from being read as an option: one starting with "-" gets a leading space.
func promptOperand(prompt string) string {
	if strings.HasPrefix(prompt, "-") {
		return " " + prompt
	}
	return prompt
}
