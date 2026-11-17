package v1alpha1

import (
	"slices"
	"strings"
	"testing"
)

func testExperiment(name string, n int) Experiment {
	e := Experiment{Name: name}
	for i := range n {
		e.Arms = append(e.Arms, ExperimentArm{Name: string(rune('a' + i))})
	}
	return e
}

func TestArmForStable(t *testing.T) {
	e := Experiment{Name: "cost-levers", Arms: []ExperimentArm{{Name: "baseline"}, {Name: "fresh"}, {Name: "medium"}, {Name: "sonnet"}}}
	a := ArmFor("group/app", e, 42)
	for range 3 {
		if b := ArmFor("/Group/App/", e, 42); b.Name != a.Name {
			t.Fatalf("same inputs gave %s and %s", a.Name, b.Name)
		}
	}
	// Golden values: the hash must not change across Go versions or
	// refactorings, or running issues would switch arms.
	var got []string
	for iid := int64(1); iid <= 8; iid++ {
		got = append(got, ArmFor("group/app", e, iid).Name)
	}
	if want := "sonnet medium medium medium baseline medium medium baseline"; strings.Join(got, " ") != want {
		t.Fatalf("arms of IIDs 1..8: %q, want %q", strings.Join(got, " "), want)
	}
}

func TestArmForSpread(t *testing.T) {
	for _, n := range []int{2, 3, 4} {
		e := testExperiment("spread", n)
		counts := map[string]int{}
		const issues = 2000
		for iid := int64(1); iid <= issues; iid++ {
			counts[ArmFor("group/app", e, iid).Name]++
		}
		share := float64(issues) / float64(n)
		for _, a := range e.Arms {
			if c := float64(counts[a.Name]); c < share*0.9 || c > share*1.1 {
				t.Errorf("%d arms: arm %s got %v issues, want %v ±10%%", n, a.Name, c, share)
			}
		}
	}
}

func TestArmForReshufflesByName(t *testing.T) {
	a, b := testExperiment("one", 4), testExperiment("two", 4)
	same := 0
	for iid := int64(1); iid <= 200; iid++ {
		if ArmFor("group/app", a, iid).Name == ArmFor("group/app", b, iid).Name {
			same++
		}
	}
	// Independent assignments agree on about a quarter of the issues.
	if same > 100 {
		t.Fatalf("%d of 200 issues kept their arm under another experiment name", same)
	}
}

func TestExperimentErrors(t *testing.T) {
	spec := func(arms ...ExperimentArm) AgentProjectSpec {
		s := AgentProjectSpec{}
		s.Agents.Implement.Experiment = &Experiment{Name: "x", Arms: arms}
		return s
	}
	for _, tc := range []struct {
		name string
		spec AgentProjectSpec
		want []string
	}{
		{"valid", spec(ExperimentArm{Name: "baseline"}, ExperimentArm{Name: "fresh", StepSession: StepSessionFresh},
			ExperimentArm{Name: "medium", Effort: "medium"}, ExperimentArm{Name: "strong", Profile: "strong"}), nil},
		{"effort above max", spec(ExperimentArm{Name: "baseline"}, ExperimentArm{Name: "max", Effort: "max"}),
			[]string{"arm max: the effort max is above agents.maxEffort high"}},
		{"unknown profile", spec(ExperimentArm{Name: "baseline"}, ExperimentArm{Name: "sonnet", Profile: "balanced"}),
			[]string{"arm sonnet: the project has no profile balanced"}},
		{"two empty arms", spec(ExperimentArm{Name: "a"}, ExperimentArm{Name: "b"}),
			[]string{"2 arms override nothing; at most one may"}},
		{"none", AgentProjectSpec{}, nil},
	} {
		got := tc.spec.ExperimentErrors()
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
		if _, ok := tc.spec.ValidExperiment(); ok != (tc.want == nil && tc.spec.Agents.Implement.Experiment != nil) {
			t.Errorf("%s: ValidExperiment ok=%v", tc.name, ok)
		}
	}
	// A raised maxEffort allows the arm.
	s := spec(ExperimentArm{Name: "baseline"}, ExperimentArm{Name: "max", Effort: "max"})
	s.Agents.MaxEffort = "max"
	if errs := s.ExperimentErrors(); errs != nil {
		t.Errorf("maxEffort max: %q", errs)
	}
}

func TestEstimatedTokens(t *testing.T) {
	if got := EstimatedTokens(81234); got != 20308 {
		t.Fatalf("EstimatedTokens = %d", got)
	}
}
