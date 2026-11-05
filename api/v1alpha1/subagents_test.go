package v1alpha1

import (
	"strings"
	"testing"
)

func TestAgentsJSON(t *testing.T) {
	scout := Subagent{Name: "scout", Description: "Searches the code", Model: "haiku", Tools: []string{"Read", "Grep", "Glob"}, Prompt: "Find things."}
	for name, tc := range map[string]struct {
		subagents []Subagent
		want      string
	}{
		"none":  {nil, ""},
		"scout": {[]Subagent{scout}, `{"scout":{"description":"Searches the code","model":"haiku","prompt":"Find things.","tools":["Read","Grep","Glob"]}}`},
		"two sorted, model and tools omitted": {
			[]Subagent{scout, {Name: "logs", Description: "Reads CI logs", Prompt: "Summarise the failure."}},
			`{"logs":{"description":"Reads CI logs","prompt":"Summarise the failure."},"scout":{"description":"Searches the code","model":"haiku","prompt":"Find things.","tools":["Read","Grep","Glob"]}}`,
		},
	} {
		got, err := RoleSpec{Subagents: tc.subagents}.AgentsJSON()
		if err != nil || got != tc.want {
			t.Errorf("%s: AgentsJSON() = %s, %v, want %s", name, got, err, tc.want)
		}
	}
}

func TestSubagentSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	scout := func() map[string]any {
		return map[string]any{"name": "scout", "description": "Searches", "model": "haiku", "tools": []any{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}, "prompt": "Find things."}
	}
	admit := func(where string, sub map[string]any) []string {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		agents := spec(obj)["agents"].(map[string]any)
		switch where {
		case "implement":
			agents["implement"].(map[string]any)["subagents"] = []any{sub}
		case "profile":
			agents["profiles"].([]any)[0].(map[string]any)["subagents"] = []any{sub}
		case "review":
			agents["review"].(map[string]any)["subagents"] = []any{sub}
		}
		_, errs := crd.admit(obj)
		return errs
	}
	for _, where := range []string{"implement", "profile", "review"} {
		if errs := admit(where, scout()); len(errs) > 0 {
			t.Errorf("scout on %s rejected: %v", where, errs)
		}
	}
	for name, tc := range map[string]struct {
		edit func(s map[string]any)
		want string
	}{
		"empty name":        {func(s map[string]any) { s["name"] = "" }, "subagents[0].name"},
		"upper name":        {func(s map[string]any) { s["name"] = "Scout" }, "subagents[0].name"},
		"long name":         {func(s map[string]any) { s["name"] = "s" + strings.Repeat("x", 40) }, "subagents[0].name"},
		"no name":           {func(s map[string]any) { delete(s, "name") }, "subagents[0].name"},
		"empty description": {func(s map[string]any) { s["description"] = "" }, "subagents[0].description"},
		"empty prompt":      {func(s map[string]any) { s["prompt"] = "" }, "subagents[0].prompt"},
		"no prompt":         {func(s map[string]any) { delete(s, "prompt") }, "subagents[0].prompt"},
		"unknown tool":      {func(s map[string]any) { s["tools"] = []any{"Read", "mcp__gitlab"} }, "subagents[0].tools[1]"},
	} {
		sub := scout()
		tc.edit(sub)
		if errs := admit("profile", sub); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
}
