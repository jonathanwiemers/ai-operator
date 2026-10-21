package runner

import (
	"strings"
	"testing"
)

func TestResourcesNote(t *testing.T) {
	if resourcesNote("", "") != "" {
		t.Fatal("no limits, no note")
	}
	note := resourcesNote("6Gi", "2")
	if !strings.Contains(note, "a memory limit of 6Gi and 2 CPUs.") || !strings.Contains(note, "container is killed") || !strings.Contains(note, "--maxWorkers=2") {
		t.Fatalf("unexpected note %q", note)
	}
	if note := resourcesNote("", "2"); !strings.Contains(note, "has 2 CPUs.") || strings.Contains(note, "memory") || !strings.Contains(note, "--maxWorkers=2") {
		t.Fatalf("without a memory limit there is no kill to warn of: %q", note)
	}
}
