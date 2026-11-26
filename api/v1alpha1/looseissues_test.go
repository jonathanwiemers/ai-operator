package v1alpha1

import (
	"testing"
	"time"
)

func TestLooseIssuesPilotHelpers(t *testing.T) {
	zero, two := int32(0), int32(2)
	for name, tc := range map[string]struct {
		spec   LooseIssuesPilotSpec
		issues int32
		hours  time.Duration
	}{
		"nil":  {LooseIssuesPilotSpec{}, 5, 24 * time.Hour},
		"zero": {LooseIssuesPilotSpec{RoundIssues: &zero, RoundHours: &zero}, 0, 0},
		"set":  {LooseIssuesPilotSpec{RoundIssues: &two, RoundHours: &two}, 2, 2 * time.Hour},
	} {
		if got := tc.spec.Issues(); got != tc.issues {
			t.Errorf("%s: Issues() = %d, want %d", name, got, tc.issues)
		}
		if got := tc.spec.Duration(); got != tc.hours {
			t.Errorf("%s: Duration() = %s, want %s", name, got, tc.hours)
		}
	}
}
