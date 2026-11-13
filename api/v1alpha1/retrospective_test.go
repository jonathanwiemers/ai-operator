package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFollowUpsSettled(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	posted := metav1.NewTime(now.Add(-time.Hour))
	two := []PilotFollowUp{{Title: "a"}, {Title: "b"}}
	for _, c := range []struct {
		name  string
		retro PilotRetrospective
		want  bool
	}{
		{"none", PilotRetrospective{PostedAt: &posted}, true},
		{"open", PilotRetrospective{PostedAt: &posted, FollowUps: two}, false},
		{"one created", PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}}, false},
		{"one taken", PilotRetrospective{PostedAt: &posted, FollowUps: two, Taken: map[string]int64{"2": 5}}, false},
		{"created and taken", PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}, Taken: map[string]int64{"2": 5}}, true},
		{"all taken", PilotRetrospective{PostedAt: &posted, FollowUps: two, Taken: map[string]int64{"1": 5, "2": 5}}, true},
		{"a week later", PilotRetrospective{PostedAt: &metav1.Time{Time: now.Add(-FollowUpWindow)}, FollowUps: two}, true},
		{"dismissed", PilotRetrospective{PostedAt: &posted, FollowUps: two, Created: map[string]int64{"1": 9}, DismissedAt: &posted}, true},
	} {
		if got := c.retro.FollowUpsSettled(now); got != c.want {
			t.Errorf("%s: FollowUpsSettled = %v, want %v", c.name, got, c.want)
		}
	}
}
