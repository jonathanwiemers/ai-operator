package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/profile.txt: package a has 6 of 10 statements covered (60.0).
// dup lists both of its blocks twice, once covered and once not, so it is
// 100.0 only when duplicates keep the highest count. The module root has one
// covered statement.
const profile = "testdata/profile.txt"

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		baseline string // "" for no baseline file
		want     map[string]string
		wantCode int
	}{
		{
			name:     "drop over 0.2 fails",
			baseline: "a 60.3\ndup 100.0\n. 100.0\n",
			want:     map[string]string{"a": "drop", "dup": "ok", ".": "ok"},
			wantCode: 1,
		},
		{
			name:     "drop within 0.2 passes",
			baseline: "a 60.2\ndup 100.0\n. 100.0\n",
			want:     map[string]string{"a": "ok"},
		},
		{
			name:     "equal",
			baseline: "a 60.0\ndup 100.0\n. 100.0\n",
			want:     map[string]string{"a": "ok", "dup": "ok", ".": "ok"},
		},
		{
			name:     "rise",
			baseline: "a 41.5\ndup 100.0\n. 100.0\n",
			want:     map[string]string{"a": "ok"},
		},
		{
			name:     "new package",
			baseline: "dup 100.0\n. 100.0\n",
			want:     map[string]string{"a": "new"},
		},
		{
			name:     "removed package",
			baseline: "a 60.0\ndup 100.0\n. 100.0\ninternal/old 90.0\n",
			want:     map[string]string{"internal/old": "gone", "a": "ok"},
		},
		{
			name: "no baseline file",
			want: map[string]string{"a": "new", "dup": "new", ".": "new"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			baseline := filepath.Join(t.TempDir(), "baseline.txt")
			if tc.baseline != "" {
				if err := os.WriteFile(baseline, []byte(tc.baseline), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{"-module", "example.com/m", "-profile", profile, "-baseline", baseline}, &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("exit code %d, want %d\n%s%s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			got := statuses(stdout.String())
			for pkg, want := range tc.want {
				if got[pkg] != want {
					t.Errorf("%s: status %q, want %q\n%s", pkg, got[pkg], want, stdout.String())
				}
			}
			if code == 1 && !strings.Contains(stdout.String(), "a (-0.3)") {
				t.Errorf("failure message does not name the package and its drop:\n%s", stdout.String())
			}
		})
	}
}

// statuses maps each table row's package to its status column.
func statuses(out string) map[string]string {
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) == 5 {
			got[f[0]] = f[4]
		}
	}
	return got
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "baseline.txt")
	// The old baseline has a drop: -write records the new values anyway.
	old := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(old, []byte("a 90.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-module", "example.com/m", "-profile", profile, "-baseline", old, "-write", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d\n%s%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := ". 100.0\na 60.0\ndup 100.0\n"; string(data) != want {
		t.Errorf("baseline file:\n%s\nwant:\n%s", data, want)
	}
}

func TestParseProfileErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"no mode line": "example.com/m/a/a.go:1.1,2.2 6 1\n",
		"bad count":    "mode: set\nexample.com/m/a/a.go:1.1,2.2 6 x\n",
		"no position":  "mode: set\na.go 6 1\n",
	} {
		if _, err := parseProfile(strings.NewReader(in), "example.com/m"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
