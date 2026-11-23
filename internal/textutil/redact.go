package textutil

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// RedactedText replaces a secret that Redact finds.
const RedactedText = "[redacted]"

// minSecretLen is the shortest value Redact and EnvSecrets treat as a
// secret: shorter values (a port, a flag) would redact ordinary text.
const minSecretLen = 8

// tokenRE matches credentials by their documented prefixes: GitLab tokens
// (personal, project and group access, OAuth application, deploy, runner,
// job, trigger, feed, incoming mail, agent, SCIM, feature flag) and Anthropic
// API keys and OAuth tokens.
var tokenRE = regexp.MustCompile(`\b(?:gl(?:pat|oas|dt|rt|rtr|cbt|ptt|ft|imt|agent|soat|ffct)-|sk-ant-)[0-9A-Za-z_.-]{8,}`)

// urlCredRE matches the user and password of a URL, https://user:pass@host.
// No quotes or backslashes: in JSON a match stays inside one string.
var urlCredRE = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@"'\\]+:[^/\s@"'\\]+@`)

// Redact replaces in s each of secrets (values shorter than 8 bytes are
// ignored), every token with a known prefix (glpat-, sk-ant-, …) and the
// credentials of URLs with RedactedText. Callers pass the secret values they
// hold; the patterns catch tokens they do not know.
func Redact(s string, secrets ...string) string {
	sorted := slices.SortedFunc(slices.Values(secrets), func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, v := range sorted { // longest first: no secret is cut by a shorter one inside it
		if len(v) >= minSecretLen {
			s = strings.ReplaceAll(s, v, RedactedText)
		}
	}
	s = tokenRE.ReplaceAllLiteralString(s, RedactedText)
	return urlCredRE.ReplaceAllString(s, "${1}"+RedactedText+"@")
}

// EnvSecrets returns the values of the variables in env (NAME=value) whose
// names mark a credential: ending in TOKEN or _KEY, or holding SECRET or
// PASSWORD. Values shorter than 8 bytes or without a letter are left out: a
// number would also match counts and IDs, and in JSON (the agent server's
// responses) a JSON number.
func EnvSecrets(env []string) []string {
	var out []string
	for _, kv := range env {
		name, v, _ := strings.Cut(kv, "=")
		n := strings.ToUpper(name)
		hasLetter := strings.ContainsFunc(v, unicode.IsLetter)
		if len(v) >= minSecretLen && hasLetter && (strings.HasSuffix(n, "TOKEN") || strings.HasSuffix(n, "_KEY") ||
			strings.Contains(n, "SECRET") || strings.Contains(n, "PASSWORD")) {
			out = append(out, v)
		}
	}
	return out
}
