package runner

import (
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// A reviewer whose project sets nothing runs on claude at the review
// default, passed to the CLI as --effort.
func TestReviewerCallPassesTheDefaultEffort(t *testing.T) {
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Review.RoleSpec = aiv1.RoleSpec{} }, nil)
	calls := rec.roleCalls("bugbot")
	if len(calls) == 0 {
		t.Fatal("bugbot did not run")
	}
	for _, c := range calls {
		args := strings.Join((&ClaudeCLI{}).Args(c), " ")
		if c.Model != aiv1.DefaultLightModel || !strings.Contains(args, "--effort "+aiv1.DefaultReviewEffort) {
			t.Fatalf("reviewer call on %q, args %q, want --effort %s", c.Model, args, aiv1.DefaultReviewEffort)
		}
	}
}
