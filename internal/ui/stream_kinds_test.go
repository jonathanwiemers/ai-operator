package ui

import (
	"slices"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/history"
)

// TestStreamKindsPartition: every event kind is on exactly one tab, Pilot
// or Stream.
func TestStreamKindsPartition(t *testing.T) {
	// Every history Kind constant. Add new ones here, and to pilotKinds or
	// runtimeKinds.
	all := []string{
		history.KindNote, history.KindRunStarted, history.KindRunFinished, history.KindPush, history.KindCI,
		history.KindReview, history.KindNeeds, history.KindMerged, history.KindClosed, history.KindAutopilot,
		history.KindProposal, history.KindAutopilotPaused, history.KindAutopilotResumed, history.KindPilotWriteFailed,
		history.KindPromptPosted, history.KindPromptAnswered, history.KindPromptClosed, history.KindRetrospective,
		history.KindCheckpoint,
	}
	all = append(all, chatStreamKinds...)
	for _, k := range pilotKinds {
		if slices.Contains(runtimeKinds, k) {
			t.Errorf("%s is in both lists", k)
		}
	}
	got := slices.Sorted(slices.Values(streamKinds))
	want := slices.Sorted(slices.Values(all))
	if !slices.Equal(slices.Compact(got), got) {
		t.Errorf("duplicate kinds: %v", got)
	}
	if !slices.Equal(got, want) {
		t.Errorf("pilot and runtime kinds = %v, want %v", got, want)
	}
	for _, k := range chatStreamKinds {
		if slices.Contains(historyStreamKinds, k) {
			t.Errorf("history kinds hold %s", k)
		}
	}
	if len(historyStreamKinds)+len(chatStreamKinds) != len(streamKinds) {
		t.Errorf("history kinds = %v", historyStreamKinds)
	}
}
