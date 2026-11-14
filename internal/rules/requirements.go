package rules

import (
	"regexp"
	"strings"
)

// requirementLine matches a requirement ID R<n> at the start of a line,
// list item or table row ("- **R1** …", "1. R2 …", "R3: …", "| R4 … |"),
// optionally bold.
var requirementLine = regexp.MustCompile(`^[ \t]*(?:(?:[-*+]|\d+[.)])[ \t]+|\|[ \t]*)?(?:\*\*|__)?R(\d+)\b`)

// CountRequirements counts the distinct requirement IDs of a milestone
// description: R<n> starting a line, list item or table row. Mentions in prose
// ("see R3") and lines inside code fences do not count.
func CountRequirements(description string) int {
	seen := map[string]bool{}
	fence := ""
	for line := range strings.Lines(description) {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if m := requirementLine.FindStringSubmatch(line); m != nil {
			seen[strings.TrimLeft(m[1], "0")] = true
		}
	}
	return len(seen)
}
