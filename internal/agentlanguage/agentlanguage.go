// Package agentlanguage owns the language rule of the agents: the line
// appended to every system prompt that makes the agent write in the
// language of the agent container's AI_AGENT_LANGUAGE tag.
package agentlanguage

import (
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// languageRule is the rule that makes the agent write in the language of
// tag (the agent container's AI_AGENT_LANGUAGE), the language named in
// English. An empty tag, one that does not parse or one without an English
// name gives "": no rule, never an error.
func languageRule(tag string) string {
	if tag == "" {
		return ""
	}
	t, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	name := display.English.Languages().Name(t)
	if name == "" {
		return ""
	}
	return "Language: write everything you produce in " + name + ": answers, GitLab comments, " +
		"issue and merge request titles and descriptions, commit messages, code comments, decisions, " +
		"milestone drafts and research results. Do this even when the owner, the issue, the comments " +
		"or the sources use another language. Keep quotes, code, identifiers, commands, file names " +
		"and error messages as they are."
}

// WithLanguageRule appends the rule of tag to system, after a blank line
// unless system is empty; without a rule it returns system unchanged.
func WithLanguageRule(system, tag string) string {
	rule := languageRule(tag)
	switch {
	case rule == "":
		return system
	case system == "":
		return rule
	}
	return system + "\n\n" + rule
}
