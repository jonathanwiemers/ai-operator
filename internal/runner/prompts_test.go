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
	if !strings.Contains(note, "a memory limit of 6Gi and 2 CPUs") || !strings.Contains(note, "--maxWorkers=2") {
		t.Fatalf("unexpected note %q", note)
	}
}
