package runner

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
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

// promptEngine records the invocations of one-shot calls and sessions.
type promptEngine struct {
	fakeSessionEngine
	mu   sync.Mutex
	invs []Invocation
}

func (e *promptEngine) record(inv Invocation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.invs = append(e.invs, inv)
}

func (e *promptEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	e.record(inv)
	return &Result{Text: "ok"}, nil
}

func (e *promptEngine) StartSession(ctx context.Context, inv Invocation) (Session, error) {
	e.record(inv)
	return e.fakeSessionEngine.StartSession(ctx, inv)
}

func TestLocalAgentLanguageRule(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		language, system, want string
	}{
		{"en", "You are a reviewer.", "You are a reviewer.\n\n" + englishRule},
		{"en", "", englishRule},
		{"", "You are a reviewer.", "You are a reviewer."},
		{"xx-!!", "You are a reviewer.", "You are a reviewer."},
	} {
		eng := &promptEngine{}
		tmp := t.TempDir()
		a := &LocalAgent{
			Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
			Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: eng},
			Language:  tc.language,
		}
		inv := Invocation{SystemPrompt: tc.system, ReadOnly: true}
		if _, err := a.Run(ctx, aiv1.EngineClaude, inv); err != nil {
			t.Fatal(err)
		}
		if _, err := a.OpenSession(ctx, aiv1.EngineClaude, inv); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"first", "second"} {
			if _, err := a.PromptSession(ctx, text, nil); err != nil {
				t.Fatal(err)
			}
		}
		// Opening again (a restarted or resumed session) starts from the
		// caller's prompt, not from the last one.
		if _, err := a.OpenSession(ctx, aiv1.EngineClaude, inv); err != nil {
			t.Fatal(err)
		}
		if len(eng.invs) != 3 {
			t.Fatalf("language %q: %d engine calls, want 3", tc.language, len(eng.invs))
		}
		for i, got := range eng.invs {
			if got.SystemPrompt != tc.want {
				t.Errorf("language %q, call %d: SystemPrompt %q, want %q", tc.language, i, got.SystemPrompt, tc.want)
			}
		}
	}
}

// TestNoPromptMirrorsLanguage keeps the role prompts from telling the model
// to answer in the language it reads: the language rule decides.
func TestNoPromptMirrorsLanguage(t *testing.T) {
	if strings.Contains(pilotSystemPrompt, "language of the message") {
		t.Error("pilotSystemPrompt mirrors the message's language")
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, "language of the message") {
				t.Errorf("%s: a prompt says %q", fset.Position(lit.Pos()), "language of the message")
			}
			return true
		})
	}
}
