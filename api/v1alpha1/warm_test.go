package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWarmUntilTakesTheLaterTime(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	chat := 15 * time.Minute
	for _, tc := range []struct {
		name      string
		until     *metav1.Time
		requested *metav1.Time
		want      time.Time
	}{
		{name: "neither"},
		{name: "operator only", until: &metav1.Time{Time: at}, want: at},
		{name: "desk only", requested: &metav1.Time{Time: at}, want: at.Add(chat)},
		{name: "operator later", until: &metav1.Time{Time: at.Add(time.Hour)}, requested: &metav1.Time{Time: at}, want: at.Add(time.Hour)},
		{name: "desk later", until: &metav1.Time{Time: at}, requested: &metav1.Time{Time: at.Add(time.Minute)}, want: at.Add(time.Minute + chat)},
	} {
		task := AgentTask{Spec: AgentTaskSpec{WarmUntil: tc.until, WarmRequestedAt: tc.requested}}
		if got := task.WarmUntil(chat); !got.Equal(tc.want) {
			t.Errorf("%s: WarmUntil = %v, want %v", tc.name, got, tc.want)
		}
	}
}
