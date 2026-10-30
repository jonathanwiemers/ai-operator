package feeds

import (
	"html"
	"regexp"
	"strings"
)

// Title and summary length caps (runes).
const (
	MaxTitle   = 200
	MaxSummary = 400
)

var reJunk, reTag = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>`), regexp.MustCompile(`(?s)<[^>]*>`)

// plainText strips HTML (incl. script/style), decodes entities, collapses
// whitespace and cuts to max runes with a trailing '…'.
func plainText(s string, max int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(reTag.ReplaceAllString(reJunk.ReplaceAllString(s, ""), ""))), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
