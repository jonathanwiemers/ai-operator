package main

import "testing"

func TestValidAgentLanguage(t *testing.T) {
	for _, s := range []string{"en", "de", "pt-BR", ""} {
		if err := validAgentLanguage(s); err != nil {
			t.Errorf("validAgentLanguage(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"xx-!!", "not a tag"} {
		if err := validAgentLanguage(s); err == nil {
			t.Errorf("validAgentLanguage(%q) = nil, want an error", s)
		}
	}
}
