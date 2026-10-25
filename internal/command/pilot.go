package command

import "strings"

// Pilot reads a comment addressed to the milestone's pilot: it starts with
// prefix ("/pilot") or with a mention of the bot ("@" + bot; "" turns the
// mention off). It returns the message without the address. An address
// anywhere else (mid-text, in a quote, in code) does not count.
func Pilot(body, prefix, bot string) (string, bool) {
	// Leading blank lines go; an indented first line is a code block.
	for {
		line, rest, ok := strings.Cut(body, "\n")
		if !ok || strings.TrimSpace(line) != "" {
			break
		}
		body = rest
	}
	if strings.HasPrefix(body, "    ") || strings.HasPrefix(body, "\t") {
		return "", false
	}
	body = strings.TrimSpace(body)
	var rest string
	switch {
	case prefix != "" && strings.HasPrefix(body, prefix):
		rest = body[len(prefix):]
	case bot != "" && len(body) > len(bot) && body[0] == '@' && strings.EqualFold(body[1:len(bot)+1], bot):
		rest = body[len(bot)+1:]
		// "@bot." ends the mention, "@bot.x" names someone else.
		if strings.HasPrefix(rest, ".") && len(rest) > 1 && nameChar(rest[1]) {
			return "", false
		}
	default:
		return "", false
	}
	if rest != "" && (nameChar(rest[0]) || rest[0] == '/') {
		return "", false // "/pilots", "@bot2"
	}
	return strings.TrimSpace(strings.TrimLeft(rest, ":,.")), true
}

func nameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}
