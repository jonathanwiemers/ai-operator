package main

import "testing"

func TestValidAgentLanguage(t *testing.T) {
	for _, s := range []string{"en", ""} {
		if err := validAgentLanguage(s); err != nil {
			t.Errorf("validAgentLanguage(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"xx-!!"} {
		if err := validAgentLanguage(s); err == nil {
			t.Errorf("validAgentLanguage(%q) = nil, want an error", s)
		}
	}
}
