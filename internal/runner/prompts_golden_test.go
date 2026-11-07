package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The system prompts are cached by the engines: a change, also of one byte,
// costs every session its cache. They change only on purpose, together with
// these hashes (sha256 of the prompt).
func TestSystemPromptsAreUnchanged(t *testing.T) {
	for name, tc := range map[string]struct{ prompt, sha string }{
		"spec":       {specSystemPrompt, "4b82eb18d9fbd1b1b4875c77d35740875454e33fd7d0579fb74b14a958b43484"},
		"implement":  {implementSystemPrompt, "8fd4977e803db331e4f19625382c5289e23f8bedbfc52097e1dbbdb1e8affa5c"},
		"bugbot":     {bugbotSystemPrompt, "22853472d19cf325d2ba58ec9d5f8d5b7c1b43ef8ecf5d81b74305a116105e50"},
		"security":   {securitySystemPrompt, "9251740c21e1e3f4f12d08efbd0a08f133ad8520a515d0478c92de5631de1f69"},
		"pilot":      {pilotSystemPrompt, "379121abe2062ac603343ae0de3c5d2995a18bc74a0e42803f170191f5198371"},
		"pilotRules": {pilotRulesPrompt, "acba6301c97a35c00ccceba25e5275d3db2e66e555005e75124be01d51aad954"},
		"retro":      {retroSystemPrompt, "387e6a72b08f16b0bd8f1708bae57e73e1b6f2446d193ba14523ae3d7f24f789"},
		"describe":   {describeSystemPrompt, "602fe49622e80041a3e0bdce43c8d88040b51583e796d07f8c57a7b062c6bb0e"},
		"research":   {researchSystemPrompt, "e8ded9ce76bf4b15b38ebdb9313e158d4d72d26489446bd76f3f40b762b44bea"},
		"draft":      {draftSystemPrompt, "60e6df7e59924c3e7325284788a9196f6021accb7320bd31fe40ba0060366402"},
	} {
		sum := sha256.Sum256([]byte(tc.prompt))
		if got := hex.EncodeToString(sum[:]); got != tc.sha {
			t.Errorf("the %s system prompt changed: sha256 %s, want %s", name, got, tc.sha)
		}
	}
}
