package runner

import (
	"errors"
	"fmt"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/feeds"
)

func TestSelectFeedEntries(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	entry := func(title, rawURL string, pub time.Time) feeds.Entry {
		return feeds.Entry{Title: title, URL: rawURL, Published: pub, Summary: "s"}
	}
	src := func(name, url, topic string) aiv1.ResearchSource {
		return aiv1.ResearchSource{Name: name, URL: url, Topic: topic}
	}

	t.Run("window without previous run uses 24h", func(t *testing.T) {
		sources := []aiv1.ResearchSource{src("a", "https://a.example/feed", "")}
		results := []feeds.Result{{Entries: []feeds.Entry{
			entry("in", "https://a.example/1", now.Add(-12*time.Hour)),
			entry("out", "https://a.example/2", now.Add(-25*time.Hour)),
			entry("undated", "https://a.example/3", time.Time{}),
		}}}
		out, warnings := selectFeedEntries(sources, results, nil, time.Time{}, now)
		if len(warnings) != 0 || len(out) != 1 || len(out[0].Entries) != 2 {
			t.Fatalf("got %+v warnings %v", out, warnings)
		}
		if out[0].Entries[0].Title != "in" || out[0].Entries[1].Title != "undated" {
			t.Errorf("order/kept: %+v", out[0].Entries)
		}
	})

	t.Run("window with previous run start", func(t *testing.T) {
		prevStart := now.Add(-6 * time.Hour)
		sources := []aiv1.ResearchSource{src("a", "https://a.example/feed", "")}
		results := []feeds.Result{{Entries: []feeds.Entry{
			entry("new", "https://a.example/1", now.Add(-3*time.Hour)),
			entry("old", "https://a.example/2", now.Add(-7*time.Hour)),
		}}}
		out, _ := selectFeedEntries(sources, results, nil, prevStart, now)
		if len(out[0].Entries) != 1 || out[0].Entries[0].Title != "new" {
			t.Fatalf("got %+v", out[0].Entries)
		}
	})

	t.Run("48h floor", func(t *testing.T) {
		sources := []aiv1.ResearchSource{src("a", "https://a.example/feed", "")}
		results := []feeds.Result{{Entries: []feeds.Entry{
			entry("ok", "https://a.example/1", now.Add(-47*time.Hour)),
			entry("too old", "https://a.example/2", now.Add(-49*time.Hour)),
		}}}
		out, _ := selectFeedEntries(sources, results, nil, now.Add(-72*time.Hour), now)
		if len(out[0].Entries) != 1 || out[0].Entries[0].Title != "ok" {
			t.Fatalf("48h floor: %+v", out[0].Entries)
		}
	})

	t.Run("previous URL dedupe with normalisation", func(t *testing.T) {
		sources := []aiv1.ResearchSource{src("a", "https://a.example/feed", "")}
		results := []feeds.Result{{Entries: []feeds.Entry{
			entry("dup", "https://Example.com/x#frag", now),
			entry("keep", "https://a.example/y", now),
		}}}
		out, _ := selectFeedEntries(sources, results, []string{"HTTPS://example.com/x"}, time.Time{}, now)
		if len(out[0].Entries) != 1 || out[0].Entries[0].Title != "keep" {
			t.Fatalf("dedupe: %+v", out[0].Entries)
		}
	})

	t.Run("per-feed and total caps and sort", func(t *testing.T) {
		var entries []feeds.Entry
		for i := range 40 {
			entries = append(entries, entry(fmt.Sprintf("e%d", i), fmt.Sprintf("https://a.example/%d", i), now.Add(-time.Duration(i)*time.Minute)))
		}
		sources := []aiv1.ResearchSource{src("a", "https://a.example/feed", "")}
		out, _ := selectFeedEntries(sources, []feeds.Result{{Entries: entries}}, nil, time.Time{}, now)
		if len(out[0].Entries) != maxEntriesPerFeed || out[0].Entries[0].Title != "e0" || out[0].Entries[29].Title != "e29" {
			t.Fatalf("per-feed cap/sort: len=%d first=%s last=%s", len(out[0].Entries), out[0].Entries[0].Title, out[0].Entries[len(out[0].Entries)-1].Title)
		}

		sources = make([]aiv1.ResearchSource, 10)
		results := make([]feeds.Result, 10)
		for i := range sources {
			sources[i] = src(fmt.Sprintf("f%d", i), fmt.Sprintf("https://f%d.example/feed", i), "")
			var es []feeds.Entry
			for j := range 25 {
				es = append(es, entry(fmt.Sprintf("%d-%d", i, j),
					fmt.Sprintf("https://f%d.example/%d", i, j),
					now.Add(-time.Duration(i*25+j)*time.Minute)))
			}
			results[i] = feeds.Result{Entries: es}
		}
		out, _ = selectFeedEntries(sources, results, nil, time.Time{}, now)
		total := 0
		for _, f := range out {
			total += len(f.Entries)
		}
		if total != maxEntriesTotal {
			t.Fatalf("total cap: %d, want %d", total, maxEntriesTotal)
		}
	})

	t.Run("failed feed", func(t *testing.T) {
		sources := []aiv1.ResearchSource{src("tagesschau", "https://tagesschau.example/feed", "Politik")}
		results := []feeds.Result{{Err: errors.New("HTTP 503")}}
		out, warnings := selectFeedEntries(sources, results, nil, time.Time{}, now)
		if len(out) != 1 || out[0].Err != "HTTP 503" || len(out[0].Entries) != 0 {
			t.Fatalf("failed feed: %+v", out[0])
		}
		if len(warnings) != 1 || warnings[0] != `feed "tagesschau": HTTP 503` {
			t.Errorf("warnings = %q", warnings)
		}
	})

	t.Run("failed feed uses URL as label", func(t *testing.T) {
		sources := []aiv1.ResearchSource{src("", "https://a.example/feed", "")}
		results := []feeds.Result{{Err: errors.New("timeout")}}
		_, warnings := selectFeedEntries(sources, results, nil, time.Time{}, now)
		if len(warnings) != 1 || warnings[0] != `feed "https://a.example/feed": timeout` {
			t.Errorf("warnings = %q", warnings)
		}
	})
}
