package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestStepGateFlags: bad or missing flags give no output on stdout and one
// line on stderr (the process exits 0); a valid call reaches the gate,
// whose fail-open errors are logged the same way.
func TestStepGateFlags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		stdin  string
		stderr string
	}{
		{"missing --max-lines", nil, `{}`, "step-gate: no nudge after error: --max-lines 0, want > 0\n"},
		{"negative --max-lines", []string{"--max-lines", "-3"}, `{}`, "step-gate: no nudge after error: --max-lines -3, want > 0\n"},
		{"unknown flag", []string{"--max-lines", "10", "--bogus"}, `{}`, "step-gate: no nudge after error: flag provided but not defined: -bogus\n"},
		{"ignored tool", []string{"--max-lines", "10", "--state-dir", t.TempDir()}, `{"tool_name":"Read"}`, ""},
		{"malformed stdin", []string{"--max-lines", "10"}, `{`, "step-gate: no nudge after error: parsing hook input: unexpected end of JSON input\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			stepGate(tc.args, strings.NewReader(tc.stdin), &stdout, &stderr)
			if stdout.Len() != 0 {
				t.Fatalf("stdout %q, want nothing", stdout.String())
			}
			if stderr.String() != tc.stderr {
				t.Fatalf("stderr %q, want %q", stderr.String(), tc.stderr)
			}
		})
	}
}
