package textutil

import (
	"slices"
	"testing"
)

func TestRedact(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"known value", "key is abcdefgh1234 here", []string{"abcdefgh1234"}, "key is [redacted] here"},
		{"every occurrence", "x=abcdefgh1234 y=abcdefgh1234", []string{"abcdefgh1234"}, "x=[redacted] y=[redacted]"},
		{"longest first", "tok=abcdefgh12345678", []string{"abcdefgh", "abcdefgh12345678"}, "tok=[redacted]"},
		{"short values are no secrets", "port 8080 is open", []string{"8080", ""}, "port 8080 is open"},
		{"gitlab token by prefix", "token glpat-TESTSECRET leaked", nil, "token [redacted] leaked"},
		{"anthropic key by prefix", `{"key":"sk-ant-api03-TESTSECRET_x"}`, nil, `{"key":"[redacted]"}`},
		{"prefix alone is kept", "a token like glpat-… or glpat-x", nil, "a token like glpat-… or glpat-x"},
		{"url credentials", "fatal: https://oauth2:hunter2@gitlab.com/a/b.git not found", nil, "fatal: https://[redacted]@gitlab.com/a/b.git not found"},
		{"url credentials stay in their JSON string", `{"a":"https://x","b":"c:d@e"}`, nil, `{"a":"https://x","b":"c:d@e"}`},
		{"url without credentials", "see https://gitlab.com/a/b and user@host", nil, "see https://gitlab.com/a/b and user@host"},
		{"nothing to redact", "plain text", nil, "plain text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in, tc.secrets...); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnvSecrets(t *testing.T) {
	env := []string{
		"GITLAB_TOKEN=glpat-TESTSECRET",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-TESTSECRET",
		"ANTHROPIC_API_KEY=sk-ant-TESTSECRET",
		"AI_RESEARCH_DB_PASSWORD=db-TESTSECRET",
		"client_secret=lower-TESTSECRET",
		"SHORT_TOKEN=abc",
		"NUMBER_KEY=1234567890",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=320000000",
		"HOME=/home/agent/workspace",
		"NO_VALUE_TOKEN",
	}
	want := []string{"glpat-TESTSECRET", "oauth-TESTSECRET", "sk-ant-TESTSECRET", "db-TESTSECRET", "lower-TESTSECRET"}
	if got := EnvSecrets(env); !slices.Equal(got, want) {
		t.Errorf("EnvSecrets = %q, want %q", got, want)
	}
}
