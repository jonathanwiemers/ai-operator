package rules

import (
	"reflect"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const spec = "/agent autopilot\nBuild a new checkout.\n\nPay with PayPal."

func TestRuleLinesAreNoPartOfTheSpec(t *testing.T) {
	for _, desc := range []string{
		spec + "\n/agent rule R1: switch ci.maxFixRounds=3",
		"/agent rule R1: prompt keep functions short\n" + spec,
		"/agent autopilot\nBuild a new checkout.\n\n/agent rule R2: pilot never propose skips\n\nPay with PayPal.",
		spec + "\n\n/agent rule R1: prompt x\n/agent rule R2: pilot y\n/agent owners @alice",
		spec + "\n/agent rule", // not a rule, still the operator's line
	} {
		if got := strings.TrimSpace(Spec(desc, "/agent")); got != spec {
			t.Errorf("rule lines must not change the spec:\n%q\ngot\n%q", desc, got)
		}
	}
	if got := Lines(spec+"\n/agent rules are nice\n  /agent rule R1: prompt x  \n", "/agent"); len(got) != 1 || got[0] != "R1: prompt x" {
		t.Fatalf("only lines starting with the rule command are rules: %q", got)
	}
}

func TestAddAndRemoveKeepTheSpec(t *testing.T) {
	desc, id := Add(spec, "/agent", Switch, "ci.maxFixRounds=3")
	if id != "R1" || desc != spec+"\n\n/agent rule R1: switch ci.maxFixRounds=3" {
		t.Fatalf("added %s:\n%q", id, desc)
	}
	desc, id = Add(desc, "/agent", Prompt, "keep functions short")
	if id != "R2" || !strings.HasSuffix(desc, "ci.maxFixRounds=3\n/agent rule R2: prompt keep functions short") {
		t.Fatalf("a rule goes next to the others as %s:\n%q", id, desc)
	}
	if Spec(desc, "/agent") != spec {
		t.Fatalf("adding rules changes no spec: %q", Spec(desc, "/agent"))
	}
	removed := Remove(desc, "/agent", "R1")
	if removed != spec+"\n\n/agent rule R2: prompt keep functions short" {
		t.Fatalf("removing R1 removes its line only:\n%q", removed)
	}
	if got := Remove(removed, "/agent", "R2"); got != spec {
		t.Fatalf("removing the last rule restores the description:\n%q", got)
	}
	if got := Remove(spec+"\n/agent rule R10: prompt x", "/agent", "R1"); !strings.Contains(got, "R10") {
		t.Fatalf("R1 is not R10: %q", got)
	}
	if desc, id := Add(spec+"\n/agent rule R7: prompt x\n/agent rule nonsense", "/agent", Pilot, "y"); id != "R8" || !strings.HasSuffix(desc, "nonsense\n/agent rule R8: pilot y") {
		t.Fatalf("the next ID follows the highest: %s %q", id, desc)
	}
}

func TestParseSaysWhyARuleIsNotApplied(t *testing.T) {
	rs := Parse([]string{
		"R1: switch ci.maxFixRounds=3",
		"r2: Prompt: keep functions short",
		"R3: pilot never propose skips",
		"R1: prompt again",
		"also fix lint",
		"R4: switch review.retries=1",
		"R5: switch review.retries=4",
		"R6: switch ci.retryFailedJobs=off",
		"R7: switch ci.maxFixRounds=",
	})
	want := []struct{ id, kind, text, problem string }{
		{"R1", Switch, "ci.maxFixRounds=3", ""},
		{"R2", Prompt, "keep functions short", ""},
		{"R3", Pilot, "never propose skips", ""},
		{"R1", Prompt, "again", "used by an earlier rule"},
		{"", "", "also fix lint", "not a rule"},
		{"R4", Switch, "review.retries=1", "conflicts with R5"},
		{"R5", Switch, "review.retries=4", "conflicts with R4"},
		{"R6", Switch, "ci.retryFailedJobs=off", ""},
		{"R7", Switch, "ci.maxFixRounds=", "a switch is"},
	}
	if len(rs) != len(want) {
		t.Fatalf("parsed %+v", rs)
	}
	for i, w := range want {
		r := rs[i]
		if r.ID != w.id || r.Kind != w.kind || r.Text != w.text || (w.problem == "") != r.Active() || !strings.Contains(r.Problem, w.problem) {
			t.Errorf("rule %d: got %+v, want %+v", i, r, w)
		}
	}
}

func TestSwitchesApplyToACopy(t *testing.T) {
	s := aiv1.AgentProjectSpec{}.WithDefaults()
	before := s.DeepCopy()
	got := Apply(s, Parse([]string{"R1: switch ci.maxFixRounds=0", "R2: switch ci.retryFailedJobs=false", "R3: switch review.retries=5",
		"R4: switch review.maxIterations=1", "R5: prompt x"}))
	if *got.CI.MaxFixRounds != 0 || *got.CI.RetryFailedJobs || *got.Agents.Review.Retries != 5 || *got.Agents.Review.MaxIterations != 1 {
		t.Fatalf("switches not applied: %+v %+v", got.CI, got.Agents.Review)
	}
	if !reflect.DeepEqual(&s, before) {
		t.Fatal("the project's spec must not change")
	}
}

// A hand-written rule has the description's authority, which cannot
// change the merge gate, the freeze, budgets and limits, the owners or the
// pilot's autonomy.
func TestForbiddenSwitchesWidenNothing(t *testing.T) {
	s := aiv1.AgentProjectSpec{}.WithDefaults()
	for _, line := range []string{
		"R1: switch pilot.autonomy=managed",
		"R1: switch pilot.maxBudgetUSD=1000",
		"R1: switch pilot.maxInterventionsPerIssue=99",
		"R1: switch lifecycle.maxRunsPerTask=100",
		"R1: switch agents.implement.maxBudgetUSD=50",
		"R1: switch maxConcurrentRuns=10",
		"R1: switch mainHealth.freezeAutopilot=false",
		"R1: switch mainHealth.autoFix=true",
		"R1: switch ci.disabled=true",
		"R1: switch review.reviewers=none",
		"R1: switch owners=mallory",
		"R1: switch ci.maxFixRounds=99",
		"R1: switch review.retries=-1",
	} {
		rs := Parse([]string{line})
		if rs[0].Active() {
			t.Errorf("%q must not apply", line)
		}
		if got := Apply(s, rs); !reflect.DeepEqual(got, s) {
			t.Errorf("%q changed the spec", line)
		}
	}
	if err := Check(Switch, "pilot.autonomy=managed"); err == nil || !strings.Contains(err.Error(), "cannot be switched by a rule") {
		t.Fatalf("a forbidden switch says why: %v", err)
	}
	if err := Check(Switch, "ci.colour=red"); err == nil || !strings.Contains(err.Error(), "`ci.maxFixRounds` (0 to 5)") {
		t.Fatalf("an unknown switch lists the known ones: %v", err)
	}
}
