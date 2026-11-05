package readgate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minLines = 350

// fixture creates the files of the gate tests in a temporary directory.
func fixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines := func(n int) string { return strings.Repeat("x\n", n) }
	write("small.ts", lines(20))
	write("big.ts", lines(2000))
	write("exact.ts", lines(minLines))
	write("over.ts", lines(minLines+1))
	write("exact-no-newline.ts", lines(minLines-1)+"x")
	write("over-no-newline.ts", lines(minLines)+"x")
	write("empty.ts", "")
	write("sub/big.ts", lines(2000))
	write("sub/small.ts", lines(5))
	write("my file.ts", lines(2000))
	write("glob/a.ts", lines(5))
	write("glob/b.ts", lines(2000))
	write("small-glob/a.ts", lines(5))
	write("small-glob/b.ts", lines(5))
	for _, name := range []string{"img.png", "IMG.PNG", "photo.JPEG", "doc.pdf", "DOC.PDF", "nb.ipynb", "NB.IPYNB"} {
		write(name, lines(2000))
	}
	if err := os.Symlink(filepath.Join(dir, "big.ts"), filepath.Join(dir, "link.ts")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readInputJSON(t testing.TB, fields map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecideRead(t *testing.T) {
	dir := fixture(t)
	abs := func(name string) string { return filepath.Join(dir, name) }
	for _, tc := range []struct {
		name  string
		input map[string]any
		cwd   string
		deny  bool
	}{
		{"small file", map[string]any{"file_path": abs("small.ts")}, "", false},
		{"big file", map[string]any{"file_path": abs("big.ts")}, "", true},
		{"exactly minLines", map[string]any{"file_path": abs("exact.ts")}, "", false},
		{"minLines+1", map[string]any{"file_path": abs("over.ts")}, "", true},
		{"minLines without trailing newline", map[string]any{"file_path": abs("exact-no-newline.ts")}, "", false},
		{"minLines+1 without trailing newline", map[string]any{"file_path": abs("over-no-newline.ts")}, "", true},
		{"empty file", map[string]any{"file_path": abs("empty.ts")}, "", false},
		{"offset", map[string]any{"file_path": abs("big.ts"), "offset": 100}, "", false},
		{"limit", map[string]any{"file_path": abs("big.ts"), "limit": 50}, "", false},
		{"offset and limit", map[string]any{"file_path": abs("big.ts"), "offset": 1, "limit": 50}, "", false},
		{"offset null", map[string]any{"file_path": abs("big.ts"), "offset": nil}, "", true},
		{"pages", map[string]any{"file_path": abs("big.ts"), "pages": "1-3"}, "", false},
		{"png", map[string]any{"file_path": abs("img.png")}, "", false},
		{"PNG upper case", map[string]any{"file_path": abs("IMG.PNG")}, "", false},
		{"JPEG", map[string]any{"file_path": abs("photo.JPEG")}, "", false},
		{"pdf", map[string]any{"file_path": abs("doc.pdf")}, "", false},
		{"PDF upper case", map[string]any{"file_path": abs("DOC.PDF")}, "", false},
		{"ipynb", map[string]any{"file_path": abs("nb.ipynb")}, "", false},
		{"IPYNB upper case", map[string]any{"file_path": abs("NB.IPYNB")}, "", false},
		{"missing path", map[string]any{"file_path": abs("missing.ts")}, "", false},
		{"directory", map[string]any{"file_path": abs("sub")}, "", false},
		{"device", map[string]any{"file_path": os.DevNull}, "", false},
		{"symlink to big file", map[string]any{"file_path": abs("link.ts")}, "", true},
		{"relative path with cwd", map[string]any{"file_path": "sub/big.ts"}, dir, true},
		{"relative small path with cwd", map[string]any{"file_path": "sub/small.ts"}, dir, false},
		{"empty path", map[string]any{"file_path": ""}, dir, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Cwd: tc.cwd, ToolName: "Read", ToolInput: readInputJSON(t, tc.input)}
			deny, reason := Decide(in, minLines, []string{"scout"})
			if deny != tc.deny {
				t.Fatalf("deny = %v (%q), want %v", deny, reason, tc.deny)
			}
			if deny && !strings.Contains(reason, Marker) {
				t.Errorf("reason %q lacks the marker %q", reason, Marker)
			}
		})
	}
}

func TestDecideBash(t *testing.T) {
	dir := fixture(t)
	for _, tc := range []struct {
		command string
		deny    bool
	}{
		// whole-file dumps
		{"cat big.ts", true},
		{"cat small.ts", false},
		{"cat exact.ts", false},
		{"cat over.ts", true},
		{"cat small.ts big.ts", true},
		{"cat -n big.ts", true},
		{"cat -A big.ts", true},
		{"cat --number big.ts", true},
		{"cat -- big.ts", true},
		{"cat - big.ts", true},
		{"/bin/cat big.ts", true},
		{"less big.ts", true},
		{"less -N big.ts", true},
		{"more big.ts", true},
		{"cat " + filepath.Join(dir, "big.ts"), true},
		{"cat missing.ts", false},
		{"cat sub", false},
		{"cat", false},
		// sequences
		{"cat small.ts; cat big.ts", true},
		{"cat small.ts && cat big.ts", true},
		{"cat small.ts\ncat big.ts", true},
		{"cat small.ts; cat sub/small.ts", false},
		{"cd sub && cat big.ts", true},
		{"cd sub && cat small.ts", false},
		{"cd sub; cat big.ts", true},
		{"cd " + filepath.Join(dir, "sub") + " && cat big.ts", true},
		{"cd /nonexistent && cat big.ts", false},
		{"cd $(git rev-parse --show-toplevel) && cat big.ts", false},
		{"cd - && cat big.ts", false},
		{"cd && cat big.ts", false},
		{"cd ~/x && cat big.ts", false},
		{"cd sub && cat " + filepath.Join(dir, "big.ts"), true},
		{"git status && cat big.ts", true},
		// quoting
		{`cat "my file.ts"`, true},
		{`cat 'my file.ts'`, true},
		{`cat my\ file.ts`, true},
		{`cat "big.ts`, false},
		{`cat 'big.ts`, false},
		{`cat big.ts\`, false},
		{"cat \\\n big.ts", true},
		{"cat big.ts # the whole file", true},
		{"# cat big.ts", false},
		{`echo "x; cat big.ts"`, false},
		// globs
		{"cat glob/*.ts", true},
		{"cat small-glob/*.ts", false},
		{"cat glob/b.t?", true},
		{"cat glob/[b].ts", true},
		{`cat "glob/*.ts"`, false},
		{"cat nomatch/*.ts", false},
		{"cd glob && cat *.ts", true},
		// head and tail
		{"head big.ts", false},
		{"tail big.ts", false},
		{"head -n 50 big.ts", false},
		{"head -n50 big.ts", false},
		{"head -50 big.ts", false},
		{"head --lines=50 big.ts", false},
		{"head --lines 50 big.ts", false},
		{"head -n 350 big.ts", false},
		{"head -n 351 big.ts", true},
		{"head -n 5000 big.ts", true},
		{"head -n5000 big.ts", true},
		{"head -5000 big.ts", true},
		{"head --lines=5000 big.ts", true},
		{"head --lines 5000 big.ts", true},
		{"head -n 99999999999999999999 big.ts", true},
		{"head -n -10 big.ts", true},
		{"head -n +10 big.ts", false},
		{"tail -n 50 big.ts", false},
		{"tail -n 5000 big.ts", true},
		{"tail -n +1 big.ts", true},
		{"tail -n +2000 big.ts", true},
		{"tail -n -50 big.ts", false},
		{"tail -q -n 5000 big.ts", true},
		{"head -n 5000 small.ts", false},
		{"head -n", false},
		{"head -n x big.ts", false},
		{"head -c 100 big.ts", false},
		{"head --bytes=100 big.ts", false},
		{"tail -f big.ts", false},
		{"tail -F big.ts", false},
		{"tail -n 5000 -f big.ts", false},
		{"cat -z big.ts", false},
		{"cat --unknown big.ts", false},
		{"more -d big.ts", false},
		// pipes, redirects and other operators
		{"cat big.ts | grep x", false},
		{"cat big.ts|grep x", false},
		{"cat big.ts > copy.ts", false},
		{"cat < big.ts", false},
		{"cat big.ts 2>&1", false},
		{"cat big.ts || true", false},
		{"cat big.ts &", false},
		{"cat $(ls big.ts)", false},
		{"cat `ls big.ts`", false},
		{"cat $F", false},
		{`cat "$F"`, false},
		{"(cat big.ts)", false},
		{"cat big.ts | grep x; cat big.ts", true},
		// other commands
		{"sed -n '10,40p' big.ts", false},
		{"grep -n x big.ts", false},
		{"wc -l big.ts", false},
		{"echo cat big.ts", false},
		{"", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			in := Input{Cwd: dir, ToolName: "Bash", ToolInput: readInputJSON(t, map[string]any{"command": tc.command})}
			deny, reason := Decide(in, minLines, nil)
			if deny != tc.deny {
				t.Fatalf("deny = %v (%q), want %v", deny, reason, tc.deny)
			}
			if deny && !strings.Contains(reason, Marker) {
				t.Errorf("reason %q lacks the marker %q", reason, Marker)
			}
		})
	}
}

func TestDenyReason(t *testing.T) {
	dir := fixture(t)
	read := Input{Cwd: dir, ToolName: "Read", ToolInput: readInputJSON(t, map[string]any{"file_path": "big.ts"})}
	_, reason := Decide(read, minLines, []string{"scout", "writer"})
	want := "File big.ts is over 350 lines (3.9 KiB; read gate: 350). Ask a subagent (scout, writer) for what you need from it, or read only the section you edit with offset/limit."
	if reason != want {
		t.Errorf("Read reason\n got %q\nwant %q", reason, want)
	}
	_, reason = Decide(read, minLines, nil)
	if !strings.Contains(reason, "Ask a subagent such as Explore for what you need") {
		t.Errorf("Read reason without subagents = %q", reason)
	}
	bash := Input{Cwd: dir, ToolName: "Bash", ToolInput: readInputJSON(t, map[string]any{"command": "cat small.ts glob/*.ts"})}
	_, reason = Decide(bash, minLines, []string{"scout"})
	want = "File glob/b.ts is over 350 lines (3.9 KiB; read gate: 350). Ask a subagent (scout) for what you need from it, or read only the section you edit with sed -n 'A,Bp' or the Read tool with offset/limit."
	if reason != want {
		t.Errorf("Bash reason\n got %q\nwant %q", reason, want)
	}
}

// recorded returns a hook input from testdata with cwd and file_path
// pointed at dir/big.ts.
func recorded(t *testing.T, name, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["cwd"] = dir
	m["tool_input"].(map[string]any)["file_path"] = filepath.Join(dir, "big.ts")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func run(stdin []byte, subagents []string) (stdout, stderr string) {
	var o, e bytes.Buffer
	Run(bytes.NewReader(stdin), &o, &e, minLines, subagents)
	return o.String(), e.String()
}

func TestRunRecorded(t *testing.T) {
	dir := fixture(t)
	stdout, stderr := run(recorded(t, "pretooluse-main.json", dir), []string{"scout"})
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"File ` +
		filepath.Join(dir, "big.ts") +
		` is over 350 lines (3.9 KiB; read gate: 350). Ask a subagent (scout) for what you need from it, or read only the section you edit with offset/limit."}}` + "\n"
	if stdout != want || stderr != "" {
		t.Errorf("main thread: stdout\n got %q\nwant %q\nstderr %q", stdout, want, stderr)
	}
	stdout, stderr = run(recorded(t, "pretooluse-subagent.json", dir), []string{"scout"})
	if stdout != "" || stderr != "" {
		t.Errorf("subagent: stdout %q, stderr %q, want both empty", stdout, stderr)
	}
}

func TestRunAllows(t *testing.T) {
	dir := fixture(t)
	for _, tc := range []struct {
		name    string
		stdin   string
		errLine bool
	}{
		{"small file", `{"cwd":"` + dir + `","tool_name":"Read","tool_input":{"file_path":"small.ts"}}`, false},
		{"subagent", `{"cwd":"` + dir + `","agent_id":"a1","agent_type":"scout","tool_name":"Read","tool_input":{"file_path":"big.ts"}}`, false},
		{"agent_type only is the main thread", `{"cwd":"` + dir + `","agent_type":"scout","tool_name":"Read","tool_input":{"file_path":"small.ts"}}`, false},
		{"malformed JSON", `{"tool_name":`, true},
		{"empty stdin", ``, true},
		{"unknown tool", `{"tool_name":"Grep","tool_input":{"pattern":"x"}}`, true},
		{"bad tool input", `{"tool_name":"Read","tool_input":"nope"}`, true},
		{"bad Bash input", `{"tool_name":"Bash","tool_input":{"command":1}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := run([]byte(tc.stdin), nil)
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !tc.errLine {
				if stderr != "" {
					t.Errorf("stderr = %q, want empty", stderr)
				}
				return
			}
			if !strings.HasPrefix(stderr, "read-gate: allowed after error: ") || strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") {
				t.Errorf("stderr = %q, want one line", stderr)
			}
		})
	}
}

func TestRunUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	dir := fixture(t)
	p := filepath.Join(dir, "big.ts")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := run([]byte(`{"tool_name":"Read","tool_input":{"file_path":"`+p+`"}}`), nil)
	if stdout != "" || !strings.HasPrefix(stderr, "read-gate: allowed after error: ") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom\nsecond line") }

func TestRunRecoversPanic(t *testing.T) {
	var o, e bytes.Buffer
	Run(panicReader{}, &o, &e, minLines, nil)
	if o.String() != "" || e.String() != "read-gate: allowed after error: panic: boom second line\n" {
		t.Errorf("stdout %q, stderr %q", o.String(), e.String())
	}
}

func BenchmarkDecideBigFile(b *testing.B) {
	dir := b.TempDir()
	p := filepath.Join(dir, "huge.ts")
	if err := os.WriteFile(p, bytes.Repeat([]byte("const x = 1; // a typical line of code\n"), 200_000), 0o644); err != nil {
		b.Fatal(err)
	}
	in := Input{ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)}
	for b.Loop() {
		if deny, _ := Decide(in, minLines, nil); !deny {
			b.Fatal("not denied")
		}
	}
}
