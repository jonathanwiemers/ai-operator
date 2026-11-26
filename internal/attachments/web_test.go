package attachments

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestWebLimits keeps the Desk's copy of the limits and extensions
// (web/src/attachments.ts) equal to this package's.
func TestWebLimits(t *testing.T) {
	src, err := os.ReadFile("../../web/src/attachments.ts")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	match := func(re string) string {
		t.Helper()
		m := regexp.MustCompile(re).FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("web/src/attachments.ts has no match for %s", re)
		}
		return m[1]
	}
	product := 1
	for _, f := range strings.Split(match(`export const maxFileBytes = ([0-9* ]+)\n`), "*") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			t.Fatalf("maxFileBytes: %v", err)
		}
		product *= n
	}
	if product != MaxFileBytes {
		t.Errorf("web maxFileBytes = %d, want %d", product, MaxFileBytes)
	}
	if got := match(`export const maxFiles = (\d+)\n`); got != strconv.Itoa(MaxFiles) {
		t.Errorf("web maxFiles = %s, want %d", got, MaxFiles)
	}
	var exts []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(match(`export const allowedExtensions = \[([^\]]*)\]`), -1) {
		exts = append(exts, q[1])
	}
	if strings.Join(exts, " ") != strings.Join(Extensions, " ") {
		t.Errorf("web allowedExtensions = %q, want %q", exts, Extensions)
	}
}
