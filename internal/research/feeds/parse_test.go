package feeds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseRSS(t *testing.T) {
	ents, err := Parse(mustOpen(t, "rss.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 3 {
		t.Fatalf("got %d entries", len(ents))
	}
	e := ents[0]
	if e.Title != "Hello & welcome" {
		t.Errorf("title: %q", e.Title)
	}
	if e.URL != "https://example.com/news/hello" {
		t.Errorf("url: %q", e.URL)
	}
	if !strings.Contains(e.Summary, "ö") || strings.Contains(e.Summary, "evil") || strings.Contains(e.Summary, "<") {
		t.Errorf("summary: %q", e.Summary)
	}
	want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.FixedZone("-0700", -7*3600))
	if !e.Published.Equal(want) {
		t.Errorf("published: %v want %v", e.Published, want)
	}
	if ents[1].URL != "https://example.com/news/encoded" {
		t.Errorf("guid link: %q", ents[1].URL)
	}
	if ents[1].Summary != "From content:encoded" {
		t.Errorf("encoded summary: %q", ents[1].Summary)
	}
	if !ents[2].Published.IsZero() {
		t.Errorf("missing pubDate should be zero, got %v", ents[2].Published)
	}
}

func TestParseAtom(t *testing.T) {
	ents, err := Parse(mustOpen(t, "atom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("got %d entries", len(ents))
	}
	if ents[0].URL != "https://example.com/posts/multi" {
		t.Errorf("relative alternate: %q", ents[0].URL)
	}
	want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	if !ents[0].Published.Equal(want) {
		t.Errorf("updated as published: %v", ents[0].Published)
	}
	if ents[1].Summary != "The body" {
		t.Errorf("content fallback: %q", ents[1].Summary)
	}
}

func TestParseISO88591(t *testing.T) {
	ents, err := Parse(mustOpen(t, "iso88591.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("got %d", len(ents))
	}
	if ents[0].Title != "Café" {
		t.Errorf("title: %q", ents[0].Title)
	}
	if ents[0].Summary != "Straße" {
		t.Errorf("summary: %q", ents[0].Summary)
	}
}

func TestParseUnknownRoot(t *testing.T) {
	_, err := Parse(mustOpen(t, "unknown.xml"))
	if err == nil || !strings.Contains(err.Error(), "unknown root") {
		t.Fatalf("got %v", err)
	}
}

func mustOpen(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLongSummaryCut(t *testing.T) {
	long := strings.Repeat("word ", 200)
	body := `<?xml version="1.0"?><rss version="2.0"><channel><link>https://example.com/</link>` +
		`<item><title>t</title><link>https://example.com/x</link><description>` + long + `</description></item></channel></rss>`
	ents, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(ents[0].Summary); n != MaxSummary+1 {
		t.Errorf("len=%d want %d", n, MaxSummary+1)
	}
	if !strings.HasSuffix(ents[0].Summary, "…") {
		t.Errorf("no ellipsis: %q", ents[0].Summary)
	}
}
