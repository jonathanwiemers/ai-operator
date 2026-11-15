package rules

import (
	"strings"
	"testing"
)

func TestParallel(t *testing.T) {
	for _, c := range []struct {
		desc string
		want int32
		bad  bool
	}{
		{desc: spec},
		{desc: spec + "\n/agent parallel 1", want: 1},
		{desc: "/agent parallel 3\n" + spec, want: 3},
		{desc: spec + "\n  /agent parallel 10  \r\n", want: 10},
		{desc: spec + "\n/agent parallel 2\n/agent parallel 5", want: 2}, // the first line counts
		{desc: spec + "\n/agent parallels 2"},                            // not the parallel command
		{desc: spec + "\nRaise it with /agent parallel 2"},               // not at the start of a line
		{desc: spec + "\n/agent parallel", bad: true},
		{desc: spec + "\n/agent parallel 0", bad: true},
		{desc: spec + "\n/agent parallel 11", bad: true},
		{desc: spec + "\n/agent parallel x", bad: true},
		{desc: spec + "\n/agent parallel -1", bad: true},
		{desc: spec + "\n/agent parallel 2.0", bad: true},
		{desc: spec + "\n/agent parallel 2 extra", bad: true},
	} {
		got, err := Parallel(c.desc, "/agent")
		if got != c.want || (err != nil) != c.bad {
			t.Errorf("Parallel(%q) = %d, %v; want %d, error %v", c.desc, got, err, c.want, c.bad)
		}
	}
}

func TestParallelLineIsNoPartOfTheSpec(t *testing.T) {
	for _, desc := range []string{
		spec + "\n/agent parallel 2",
		spec + "\n\n/agent parallel 1",
		"/agent autopilot\nBuild a new checkout.\n\n/agent parallel 3\n\nPay with PayPal.",
		spec + "\n/agent parallel nonsense", // an invalid line is still the operator's
		spec + "\n/agent owners @alice\n/agent parallel 2\n/agent rule R1: prompt x",
	} {
		if got := strings.TrimSpace(Spec(desc, "/agent")); got != spec {
			t.Errorf("the parallel line must not change the spec:\n%q\ngot\n%q", desc, got)
		}
	}
}

func TestSetParallel(t *testing.T) {
	for _, c := range []struct {
		desc string
		want string
	}{
		{desc: spec + "\n/agent parallel 2", want: spec + "\n/agent parallel 4"},
		{desc: "/agent parallel 2\n" + spec, want: "/agent parallel 4\n" + spec},
		{desc: spec + "\n  /agent parallel 2  \r\nmore", want: spec + "\n/agent parallel 4\r\nmore"},
		{desc: spec + "\n/agent parallel 2\n/agent parallel 5", want: spec + "\n/agent parallel 4\n/agent parallel 5"}, // the first line counts
		{desc: spec + "\n/agent parallel x", want: spec + "\n/agent parallel 4"},
		{desc: spec + "\n", want: spec + "\n\n/agent parallel 4"},
		{desc: "", want: "/agent parallel 4"},
	} {
		got := SetParallel(c.desc, "/agent", 4)
		if got != c.want {
			t.Errorf("SetParallel(%q) = %q; want %q", c.desc, got, c.want)
		}
		if n, err := Parallel(got, "/agent"); err != nil || n != 4 {
			t.Errorf("SetParallel(%q) reads back as %d, %v", c.desc, n, err)
		}
		if strings.TrimSpace(Spec(got, "/agent")) != strings.TrimSpace(Spec(c.desc, "/agent")) {
			t.Errorf("SetParallel(%q) changed the spec: %q", c.desc, got)
		}
	}
}
