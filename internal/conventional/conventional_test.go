package conventional

import (
	"strings"
	"testing"
)

var types = []string{"feat", "fix", "refactor", "chore"}

func TestValid(t *testing.T) {
	for _, c := range []struct {
		name, msg string
		ok        bool
	}{
		{"plain", "feat: x", true},
		{"scope", "fix(ui): x", true},
		{"breaking", "feat!: x", true},
		{"scope breaking body", "refactor(api/v1)!: x\n\nbody\nmore", true},
		{"trailing newline", "feat: x\n", true},
		{"uppercase type", "Feat: x", false},
		{"no space", "feat:x", false},
		{"73 chars", "feat: " + strings.Repeat("a", 73), false},
		{"72 chars", "feat: " + strings.Repeat("a", 72), true},
		{"unknown type", "docs: x", false},
		{"no blank line", "feat: x\nbody", false},
		{"empty subject", "feat: ", false},
		{"empty", "", false},
	} {
		if got := Valid(c.msg, types); got != c.ok {
			t.Errorf("%s: Valid(%q) = %v", c.name, c.msg, got)
		}
	}
}

func TestFallback(t *testing.T) {
	long := strings.Repeat("a", 100)
	for _, c := range []struct{ name, msg, want string }{
		{"simple", "Fix the thing", "chore(agent): fix the thing"},
		{"long", "Add " + long, "chore(agent): add " + strings.Repeat("a", 68)},
		{"multi", " Knobloo: LED \n\nbody line\nmore", "chore(agent): knobloo: LED\n\nbody line\nmore"},
		{"body without blank", "Title\nbody", "chore(agent): title\n\nbody"},
		{"empty", "", "chore(agent): update"},
	} {
		got := Fallback(c.msg)
		if got != c.want {
			t.Errorf("%s: Fallback = %q, want %q", c.name, got, c.want)
		}
		if !Valid(got, types) {
			t.Errorf("%s: fallback %q is not valid", c.name, got)
		}
	}
}

func TestTitle(t *testing.T) {
	if got := Title("feat(ui): x", types); got != "feat(ui): x" {
		t.Errorf("valid title kept: %q", got)
	}
	if got := Title("Some title", types); got != "chore(agent): some title" {
		t.Errorf("invalid title falls back: %q", got)
	}
}

func TestPatternQuotes(t *testing.T) {
	if Valid("a.b: x", []string{"a.b"}) != true || Valid("axb: x", []string{"a.b"}) {
		t.Error("types are quoted")
	}
}
