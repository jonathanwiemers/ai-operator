package runner

import (
	"encoding/json"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// readGateTimeout is the hook's timeout in seconds; the gate fails open.
const readGateTimeout = 5

// hookCommand is one command hook of claude's settings.
type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// hookMatcher runs its hooks on the tools its matcher (a regex) names.
type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

// hookSettings is claude's --settings JSON: only hooks, by event
// (PreToolUse, PostToolUse). The read gate and the step-size gate share
// one object.
type hookSettings struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// withHooks adds matchers of an event to the settings JSON (empty for
// none) and returns the merged JSON. The settings are the runner's own.
func withHooks(settings, event string, matchers ...hookMatcher) string {
	s := hookSettings{Hooks: map[string][]hookMatcher{}}
	if settings != "" {
		if err := json.Unmarshal([]byte(settings), &s); err != nil {
			panic(fmt.Sprintf("settings %q: %v", settings, err)) // built here
		}
	}
	s.Hooks[event] = append(s.Hooks[event], matchers...)
	out, err := json.Marshal(s)
	if err != nil {
		panic(err) // only strings and ints
	}
	return string(out)
}

// readGateSettings is claude's --settings JSON for the read gate: only the
// PreToolUse hooks for Read and Bash, both running `ai-runner read-gate`.
// Subagent names match ^[a-z][a-z0-9-]*$, so the command needs no quoting.
func readGateSettings(minLines int, subagents []string) string {
	command := fmt.Sprintf("ai-runner read-gate --min-lines %d", minLines)
	if len(subagents) > 0 {
		command += " --subagents " + strings.Join(subagents, ",")
	}
	h := []hookCommand{{Type: "command", Command: command, Timeout: readGateTimeout}}
	return withHooks("", "PreToolUse", hookMatcher{Matcher: "Read", Hooks: h}, hookMatcher{Matcher: "Bash", Hooks: h})
}

// readGateNote is the prompt sentence of a role with a read gate. With
// subagents it extends their line; without, it names the built-in ones.
func readGateNote(minLines int, subagents bool) string {
	ask := "a subagent such as Explore"
	if subagents {
		ask = "a subagent"
	}
	return fmt.Sprintf("Reads of files over %d lines are blocked; ask %s or read a section with offset/limit.", minLines, ask)
}

// withReadGate gives a claude call the role's read gate: the hooks
// (--settings) and the sentence in the system prompt. It runs after
// withSubagents, whose line ends the system prompt when the call got
// subagents (inv.Agents set); the sentence joins that line. Like subagents,
// the gate is claude only: on cursor (own engine or fallback) the call runs
// without it, with a warning once per run and role.
func (r *Runner) withReadGate(role aiv1.RoleSpec, inv Invocation) Invocation {
	if role.ReadGate == nil {
		return inv
	}
	if role.Engine != aiv1.EngineClaude {
		if r.run.warnOnce("readgate/" + inv.Role + "/" + string(role.Engine)) {
			r.log().Info(fmt.Sprintf("read gate is claude only; ignored for role %s on %s", inv.Role, role.Engine))
		}
		return inv
	}
	lines := role.ReadGate.Lines()
	var names []string
	if inv.Agents != "" {
		for _, s := range role.Subagents {
			names = append(names, s.Name)
		}
	}
	inv.Settings = readGateSettings(lines, names)
	sep := "\n\n"
	if len(names) > 0 {
		sep = " "
	}
	inv.SystemPrompt += sep + readGateNote(lines, len(names) > 0)
	return inv
}
