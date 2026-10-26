package command

import (
	"regexp"
	"strings"
)

var forgetRE = regexp.MustCompile(`(?i)^forget\s+(R[0-9]+)\s*[.!]?$`)

// PilotRules reads the pilot's rule commands: "rules" lists the
// milestone's standing rules, "forget R<n>" (forget: the ID) proposes
// removing one.
func PilotRules(s string) (list bool, forget string) {
	s = strings.TrimSpace(s)
	if m := forgetRE.FindStringSubmatch(s); m != nil {
		return false, strings.ToUpper(m[1])
	}
	return strings.EqualFold(strings.TrimRight(s, ".!?"), "rules"), ""
}
