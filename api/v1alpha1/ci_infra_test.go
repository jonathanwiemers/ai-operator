package v1alpha1

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCIInfraDefaults(t *testing.T) {
	c := *CISpec{}.WithDefaults().InfraFailures
	if !slices.Equal(c.FailureReasons, []string{"runner_system_failure", "stuck_or_timeout_failure"}) {
		t.Errorf("failureReasons = %v", c.FailureReasons)
	}
	if !slices.Equal(c.LogPatterns, []string{"EAI_AGAIN", "error resolving DNS", "Temporary failure in name resolution", "OOMKilled"}) {
		t.Errorf("logPatterns = %v", c.LogPatterns)
	}
	if *c.MaxRetries != 6 || c.Backoff.Duration != 2*time.Minute {
		t.Errorf("maxRetries = %d, backoff = %s", *c.MaxRetries, c.Backoff.Duration)
	}

	// Set fields replace their defaults; 0 retries stays 0.
	zero := int32(0)
	c = CIInfraSpec{LogPatterns: []string{"x"}, FailureReasons: []string{}, MaxRetries: &zero}.WithDefaults()
	if !slices.Equal(c.LogPatterns, []string{"x"}) || len(c.FailureReasons) != 0 || *c.MaxRetries != 0 {
		t.Errorf("custom = %+v", c)
	}
}

func TestCIInfraBackoff(t *testing.T) {
	c := CIInfraSpec{Backoff: metav1.Duration{Duration: 2 * time.Minute}}
	var got []time.Duration
	for n := range int32(7) {
		got = append(got, c.InfraBackoff(n))
	}
	want := []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	if !slices.Equal(got, want) {
		t.Errorf("backoffs = %v, want %v", got, want)
	}
	if d := (CIInfraSpec{Backoff: metav1.Duration{Duration: time.Hour}}).InfraBackoff(0); d != 30*time.Minute {
		t.Errorf("a backoff over the cap = %s", d)
	}
}
