package v1alpha1

import "encoding/json"

// agentDef is one entry of claude's --agents JSON. Checked against Claude
// Code 2.1.314 (`claude --help`): an object keyed by agent name, each with
// description and prompt, optionally tools (a list of tool names) and model
// (an alias or model id; omitted, the agent inherits the main model).
// Fields are in alphabetical order, so the JSON has sorted keys.
type agentDef struct {
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
}

// AgentsJSON renders the role's subagents as claude's --agents argument, or
// "" when the role has none. The output is deterministic: names are sorted
// (encoding/json sorts map keys) and so are the fields.
func (r RoleSpec) AgentsJSON() (string, error) {
	if len(r.Subagents) == 0 {
		return "", nil
	}
	agents := make(map[string]agentDef, len(r.Subagents))
	for _, s := range r.Subagents {
		agents[s.Name] = agentDef{Description: s.Description, Model: s.Model, Prompt: s.Prompt, Tools: s.Tools}
	}
	out, err := json.Marshal(agents)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
