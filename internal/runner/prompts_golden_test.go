package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The system prompts are cached by the engines: a change, also of one byte,
// costs every session its cache. They change only on purpose, together with
// these hashes (sha256 of the prompt).
func TestSystemPromptsAreUnchanged(t *testing.T) {
	for name, tc := range map[string]struct{ prompt, sha string }{
		"spec":       {specSystemPrompt, "5572ea727d8ec0d0cfd1277e4a6d2c9e7dcf0f6feb676def02dee2ad8dd37544"},
		"implement":  {implementSystemPrompt, "8fd4977e803db331e4f19625382c5289e23f8bedbfc52097e1dbbdb1e8affa5c"},
		"bugbot":     {bugbotSystemPrompt, "22853472d19cf325d2ba58ec9d5f8d5b7c1b43ef8ecf5d81b74305a116105e50"},
		"security":   {securitySystemPrompt, "9251740c21e1e3f4f12d08efbd0a08f133ad8520a515d0478c92de5631de1f69"},
		"pilot":      {pilotSystemPrompt, "cb6c982cccd5e5a4aa77d52c45e57d5595da6f9d718c3e449e47b37923b6bfaa"},
		"pilotRules": {pilotRulesPrompt, "acba6301c97a35c00ccceba25e5275d3db2e66e555005e75124be01d51aad954"},
		"retro":      {retroSystemPrompt, "4045a57f426e954ee971306d52634806668213370b3815ceafac61fa292c1167"},
		"describe":   {describeSystemPrompt, "602fe49622e80041a3e0bdce43c8d88040b51583e796d07f8c57a7b062c6bb0e"},
		"research":   {researchSystemPrompt, "e8ded9ce76bf4b15b38ebdb9313e158d4d72d26489446bd76f3f40b762b44bea"},
		"draft":      {draftSystemPrompt, "60e6df7e59924c3e7325284788a9196f6021accb7320bd31fe40ba0060366402"},
		"plan":       {planSystemPrompt, "8d4442edb750c13136dcde2da1ff2d833d4f08bfe5b1ee33eea2e7fd93591bc1"},
	} {
		sum := sha256.Sum256([]byte(tc.prompt))
		if got := hex.EncodeToString(sum[:]); got != tc.sha {
			t.Errorf("the %s system prompt changed: sha256 %s, want %s", name, got, tc.sha)
		}
	}
}

// The planner slices changes that span several layers (#258: #179 put a
// migration, the store, the runner and the UI into one slice).
func TestSpecPromptSlicesAcrossLayers(t *testing.T) {
	flat := strings.Join(strings.Fields(specSystemPrompt), " ")
	for _, want := range []string{
		"- Split a change that spans several layers (database migration, store, runner or controller, API types, web UI) into slices of at most two layers each",
		"measurement and UI work in particular goes into its own slice after the data it shows.",
		"Give a slice that adds a database migration nothing else that is large.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the spec system prompt lacks %q", want)
		}
	}
}

// Sibling issues may run at the same time, so the planner must keep their
// file sets apart (or wire dependsOn) — Decision 4 / #467.
func TestSpecPromptSiblingRule(t *testing.T) {
	flat := strings.Join(strings.Fields(specSystemPrompt), " ")
	want := "Issues without a dependency between them may be implemented at the same time, so they must not change the same files; if they would, make one depend on the other."
	if !strings.Contains(flat, want) {
		t.Errorf("the spec system prompt lacks the sibling rule %q", want)
	}
}

// Issues may run in parallel and a problem holds only its issue, so the
// pilot must know of holds and how they end — Decision 6 / #468.
func TestPilotPromptParallelAndHolds(t *testing.T) {
	flat := strings.Join(strings.Fields(pilotSystemPrompt), " ")
	for _, want := range []string{
		"issues of the milestone may be implemented at the same time, up to autopilot.maxParallel",
		"land (rebase, CI, merge) one at a time, in the order they became ready",
		"A problem with one issue holds that issue (autopilot.items[].hold) and the others go on",
		`A hold ends by itself once its cause is gone, or with "/agent resume #N".`,
		"the merge request of an active queue item",
		"a pause or hold about the issue ends; e.g.",
		"an autopilot pause, a held issue,",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the pilot system prompt lacks %q", want)
		}
	}
	if strings.Contains(flat, "the active queue item") {
		t.Error("the pilot system prompt still speaks of the one active queue item")
	}
}

// TestConflictFreshPrompt pins the prompt of a fresh conflict call byte
// for byte (golden).
func TestConflictFreshPrompt(t *testing.T) {
	issue := &gitlab.Issue{IID: 42, Title: "Add PayPal button", Description: "As a shopper I want to pay with PayPal."}
	got := buildConflictFreshPrompt(issue, "Did the button.", " a.go | 2 +-\n 1 file changed\n", "## Merge conflicts\n\nResolve.\n")
	want := "Resolve the merge conflicts of the merge request for issue #42. You start in a new session: " +
		"the work for the issue was done in another one and is committed on this branch (see `git log` and the diff " +
		"against the default branch). Resolve the conflicts only; do not continue or change the work for the issue " +
		"beyond what the merge needs.\n\n" +
		"## Add PayPal button\n\nAs a shopper I want to pay with PayPal.\n\n" +
		"## The implementer's notes\n\nCheck them against the code before relying on them. Your handoff " +
		"replaces them: keep them and add what the merge changed.\n\nDid the button.\n\n" +
		"## Changes of the merge request\n\n```\n a.go | 2 +-\n 1 file changed\n```\n\n" +
		"## Merge conflicts\n\nResolve.\n"
	if got != want {
		t.Fatalf("fresh conflict prompt changed:\n%q\nwant\n%q", got, want)
	}
	// Without notes and stat, those sections are left out.
	bare := buildConflictFreshPrompt(issue, " ", "\n", "C")
	if strings.Contains(bare, "notes") || strings.Contains(bare, "## Changes") || !strings.HasSuffix(bare, "pay with PayPal.\n\nC") {
		t.Fatalf("bare prompt: %q", bare)
	}
}
