package v1alpha1

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// pauseDocs splits the "When it pauses" section of docs/usage.md into its
// "#### `kind`" subsections, by kind.
func pauseDocs(t *testing.T, doc string) map[string]string {
	t.Helper()
	start := strings.Index(doc, "\n### When it pauses\n")
	if start < 0 {
		t.Fatal(`docs/usage.md has no "### When it pauses" section`)
	}
	section := doc[start+1:]
	if end := strings.Index(section[len("### "):], "\n### "); end >= 0 {
		section = section[:len("### ")+end]
	}
	heading := regexp.MustCompile("(?m)^#### `([A-Za-z]+)`[ \t]*$")
	subs := map[string]string{}
	locs := heading.FindAllStringSubmatchIndex(section, -1)
	for i, loc := range locs {
		end := len(section)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		subs[section[loc[2]:loc[3]]] = section[loc[1]:end]
	}
	return subs
}

// docs/usage.md has a subsection per PauseKind under "When it pauses", and
// it names the /agent command of every remedy in PauseExits.
func TestPauseKindsDocumented(t *testing.T) {
	data, err := os.ReadFile("../../docs/usage.md")
	if err != nil {
		t.Fatal(err)
	}
	subs := pauseDocs(t, string(data))
	known := map[string]bool{}
	for _, kind := range PauseKinds {
		known[string(kind)] = true
		body, ok := subs[string(kind)]
		if !ok {
			t.Errorf("docs/usage.md: no \"#### `%s`\" subsection under \"When it pauses\"", kind)
			continue
		}
		for _, e := range PauseExits(kind) {
			if e.Command == "" {
				continue
			}
			if !regexp.MustCompile("/agent " + regexp.QuoteMeta(e.Command) + `\b`).MatchString(body) {
				t.Errorf("docs/usage.md, `%s`: exit %s needs `/agent %s`", kind, e.ID, e.Command)
			}
		}
	}
	for kind := range subs {
		if !known[kind] {
			t.Errorf("docs/usage.md: subsection `%s` is no PauseKind", kind)
		}
	}
}
