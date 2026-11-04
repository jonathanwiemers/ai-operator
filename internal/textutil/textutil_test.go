package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"ascii cut", "hello world", 5, "hello…"},
		{"empty", "", 0, ""},
		{"zero", "hello", 0, "…"},
		{"negative", "hello", -3, "…"},
		{"umlaut kept whole", "ääää", 4, "ää…"},
		{"umlaut mid rune", "ääää", 3, "ä…"},
		{"umlaut exact bytes", "ääää", 8, "ääää"},
		{"emoji mid rune", "a😀b", 3, "a…"},
		{"emoji whole", "a😀b", 5, "a😀…"},
		{"rune wider than n", "😀", 2, "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Truncate(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("invalid UTF-8: %q", got)
			}
		})
	}
}

func TestTruncateCharacterLimit(t *testing.T) {
	s := strings.Repeat("ä", 300)
	if got := Truncate(s, 254); utf8.RuneCountInString(got) > 255 {
		t.Errorf("%d characters", utf8.RuneCountInString(got))
	}
}
