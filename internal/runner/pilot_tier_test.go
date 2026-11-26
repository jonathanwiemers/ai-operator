package runner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestPilotSetTierIsInTheSchemaAndMapped(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(actionSchema), &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Properties["tool"].Enum, "setTier") || !strings.HasPrefix(s.Properties["profile"].Description, "setTier: ") ||
		!strings.Contains(s.Properties["issue"].Description, "setTier") {
		t.Fatalf("the schema has setTier and its profile: %+v", s.Properties)
	}
	got := pilotActions([]ProposalAction{{Tool: "setTier", Issue: 12, Profile: "deep"}})
	if len(got) != 1 || got[0] != (aiv1.PilotAction{Tool: "setTier", IssueIID: 12, Profile: "deep"}) {
		t.Fatalf("profile is mapped: %+v", got)
	}
	for _, want := range []string{"- setTier, issue N, profile P: ", "only one tier up", "It always waits for an owner's yes", "no label changes\nbut setTier"} {
		if !strings.Contains(pilotSystemPrompt, want) {
			t.Errorf("the pilot's prompt misses %q", want)
		}
	}
}

func TestPilotStateListsTheProfilesInOrder(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	e := enginesOf(&aiv1.AgentProject{}, spec, time.Now())
	want := []pilotProfile{{"fast", "claude-sonnet-5-5", "medium"}, {"balanced", "claude-sonnet-5-5", "high"},
		{"strong", "claude-opus-5-5", "medium"}, {"deep", "claude-opus-5-5", "high"}}
	if !slices.Equal(e.Profiles, want) {
		t.Fatalf("profiles, lowest first: %+v", e.Profiles)
	}
}
