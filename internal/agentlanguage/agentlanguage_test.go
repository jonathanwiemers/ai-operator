package agentlanguage

import (
	"strings"
	"testing"
)

const englishRule = "Language: write everything you produce in English: answers, GitLab comments, " +
	"issue and merge request titles and descriptions, commit messages, code comments, decisions, " +
	"milestone drafts and research results. Do this even when the owner, the issue, the comments " +
	"or the sources use another language. Keep quotes, code, identifiers, commands, file names " +
	"and error messages as they are."

func TestLanguageRule(t *testing.T) {
	if got := languageRule("en"); got != englishRule || !strings.Contains(got, "in English:") {
		t.Errorf("languageRule(en) = %q", got)
	}
	for tag, name := range map[string]string{"de": "German", "pt-BR": "Portuguese", "EN": "English"} {
		if got := languageRule(tag); !strings.Contains(got, name+":") {
			t.Errorf("languageRule(%q) = %q, want it to name %s", tag, got, name)
		}
	}
	for _, tag := range []string{"", "xx-!!", "not a tag"} {
		if got := languageRule(tag); got != "" {
			t.Errorf("languageRule(%q) = %q, want no rule", tag, got)
		}
	}
}
