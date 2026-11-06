package runner

import (
	"encoding/json"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// readGateTimeout is the hook's timeout in seconds; the gate fails open.
const readGateTimeout = 5

// readGateSettings is claude's --settings JSON for the read gate: only the
// PreToolUse hooks for Read and Bash, both running `ai-runner read-gate`.
// Subagent names match ^[a-z][a-z0-9-]*$, so the command needs no quoting.
func readGateSettings(minLines int, subagents []string) string {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type matcher struct {
		Matcher string `json:"matcher"`
		Hooks   []hook `json:"hooks"`
	}
	command := fmt.Sprintf("ai-runner read-gate --min-lines %d", minLines)
	if len(subagents) > 0 {
		command += " --subagents " + strings.Join(subagents, ",")
	}
	h := []hook{{Type: "command", Command: command, Timeout: readGateTimeout}}
	settings := map[string]map[string][]matcher{"hooks": {"PreToolUse": {
		{Matcher: "Read", Hooks: h},
		{Matcher: "Bash", Hooks: h},
	}}}
	out, err := json.Marshal(settings)
	if err != nil {
		panic(err) // only strings and ints
	}
	return string(out)
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
