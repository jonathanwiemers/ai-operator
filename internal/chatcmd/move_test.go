package chatcmd

import (
	"strings"
	"testing"
)

func TestParseMove(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    Move
		wantErr string
	}{
		{[]string{"top"}, Move{To: "top"}, ""},
		{[]string{"Bottom", "", ""}, Move{To: "bottom"}, ""},
		{[]string{"before", "%6"}, Move{Before: 6}, ""},
		{[]string{"BEFORE", "6"}, Move{Before: 6}, ""},
		{[]string{"after", "%12", ""}, Move{After: 12}, ""},
		{[]string{"after", "6"}, Move{After: 6}, ""},
		{nil, Move{}, "move needs exactly one of"},
		{[]string{"", ""}, Move{}, "move needs exactly one of"},
		{[]string{"up"}, Move{}, "move needs exactly one of"},
		{[]string{"top", "now"}, Move{}, "move needs exactly one of"},
		{[]string{"before"}, Move{}, "move needs exactly one of"},
		{[]string{"before", "%x"}, Move{}, `"%x" is not a milestone`},
		{[]string{"after", "%0"}, Move{}, `"%0" is not a milestone`},
		{[]string{"after", "#6"}, Move{}, `"#6" is not a milestone`},
		{[]string{"before", "%6", "please"}, Move{}, "move needs exactly one of"},
	} {
		got, err := ParseMove(tc.args)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want it to mention %q", tc.args, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %+v, %v, want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestMoveOfACommandAndItsWords(t *testing.T) {
	for _, tc := range []struct {
		body, words string
	}{
		{`{"cmd":"move","to":"top"}`, "top"},
		{`{"cmd":"move","to":"bottom"}`, "bottom"},
		{`{"cmd":"move","before":6}`, "before %6"},
		{`{"cmd":"move","after":6}`, "after %6"},
	} {
		c, err := Parse(tc.body)
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		mv, ok := c.Move()
		if !ok || mv.String() != tc.words {
			t.Errorf("%s: move %+v (%v), words %q, want %q", tc.body, mv, ok, mv.String(), tc.words)
		}
		// The words parse back to the same move: both paths agree.
		if back, err := ParseMove(strings.Fields(mv.String())); err != nil || back != mv {
			t.Errorf("%s: words %q parse to %+v, %v", tc.body, mv.String(), back, err)
		}
	}
	if _, ok := (Command{Cmd: "start"}).Move(); ok {
		t.Error("start is not a move command")
	}
}
