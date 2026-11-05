package rules

import (
	"strings"
	"testing"
)

func TestBudget(t *testing.T) {
	for _, c := range []struct {
		desc, want string
		bad        bool
	}{
		{desc: spec},
		{desc: spec + "\n/agent budget 5", want: "5.0000"},
		{desc: "/agent budget 12.5\n" + spec, want: "12.5000"},
		{desc: spec + "\n  /agent budget $0.75  \r\n", want: "0.7500"},
		{desc: spec + "\n/agent budget 5\n/agent budget 50", want: "5.0000"}, // the first line counts
		{desc: spec + "\n/agent budgets 5"},                                  // not the budget command
		{desc: spec + "\nRaise it with /agent budget 5"},                     // not at the start of a line
		{desc: spec + "\n/agent budget", bad: true},
		{desc: spec + "\n/agent budget 0", bad: true},
		{desc: spec + "\n/agent budget 0.00", bad: true},
		{desc: spec + "\n/agent budget -5", bad: true},
		{desc: spec + "\n/agent budget five", bad: true},
		{desc: spec + "\n/agent budget 1e3", bad: true},
		{desc: spec + "\n/agent budget Inf", bad: true},
		{desc: spec + "\n/agent budget 5 USD", bad: true},
		{desc: spec + "\n/agent budget 5,50", bad: true},
	} {
		got, err := Budget(c.desc, "/agent")
		if got != c.want || (err != nil) != c.bad {
			t.Errorf("Budget(%q) = %q, %v; want %q, error %v", c.desc, got, err, c.want, c.bad)
		}
	}
}

func TestBudgetLineIsNoPartOfTheSpec(t *testing.T) {
	for _, desc := range []string{
		spec + "\n/agent budget 5",
		spec + "\n\n/agent budget 50",
		"/agent autopilot\nBuild a new checkout.\n\n/agent budget 5\n\nPay with PayPal.",
		spec + "\n/agent budget nonsense", // an invalid line is still the operator's
		spec + "\n/agent owners @alice\n/agent budget 5\n/agent rule R1: prompt x",
	} {
		if got := strings.TrimSpace(Spec(desc, "/agent")); got != spec {
			t.Errorf("the budget line must not change the spec:\n%q\ngot\n%q", desc, got)
		}
	}
}

func TestSetBudget(t *testing.T) {
	for _, c := range []struct{ desc, want string }{
		{desc: spec + "\n/agent budget 5", want: spec + "\n/agent budget 48"},
		{desc: "/agent budget 5\n" + spec, want: "/agent budget 48\n" + spec},
		{desc: spec + "\n  /agent budget $5  \r\nmore", want: spec + "\n/agent budget 48\r\nmore"},
		{desc: spec + "\n/agent budget 5\n/agent budget 50", want: spec + "\n/agent budget 48\n/agent budget 50"}, // the first line counts
		{desc: spec + "\n/agent budget five", want: spec + "\n/agent budget 48"},
		{desc: spec + "\n", want: spec + "\n\n/agent budget 48"},
		{desc: "", want: "/agent budget 48"},
	} {
		got := SetBudget(c.desc, "/agent", "48")
		if got != c.want {
			t.Errorf("SetBudget(%q) = %q; want %q", c.desc, got, c.want)
		}
		if usd, err := Budget(got, "/agent"); err != nil || usd != "48.0000" {
			t.Errorf("SetBudget(%q) reads back as %q, %v", c.desc, usd, err)
		}
		if strings.TrimSpace(Spec(got, "/agent")) != strings.TrimSpace(Spec(c.desc, "/agent")) {
			t.Errorf("SetBudget(%q) changed the spec: %q", c.desc, got)
		}
	}
}
