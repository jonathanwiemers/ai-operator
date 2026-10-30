package runner

import (
	"fmt"
	"sort"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/feeds"
)

const (
	maxEntriesPerFeed = 30
	maxEntriesTotal   = 200
)

// selectFeedEntries keeps entries published since previousStart (or 24 h
// back), never earlier than 48 h, drops URLs already in previousURLs, and
// caps at 30 per feed and 200 in all. A failed feed becomes a FeedEntries
// with Err and a warning.
func selectFeedEntries(sources []aiv1.ResearchSource, results []feeds.Result, previousURLs []string, previousStart, now time.Time) (out []FeedEntries, warnings []string) {
	windowStart := now.Add(-24 * time.Hour)
	if !previousStart.IsZero() {
		windowStart = previousStart
	}
	if floor := now.Add(-48 * time.Hour); windowStart.Before(floor) {
		windowStart = floor
	}
	prev := map[string]bool{}
	for _, u := range previousURLs {
		prev[string(blocks.URLHash(u))] = true
	}
	type picked struct {
		feed int
		e    feeds.Entry
	}
	var all []picked
	out = make([]FeedEntries, len(sources))
	for i, src := range sources {
		fe := FeedEntries{Name: src.Name, Topic: src.Topic, URL: src.URL}
		if i >= len(results) || results[i].Err != nil {
			err := "no result"
			if i < len(results) && results[i].Err != nil {
				err = results[i].Err.Error()
			}
			fe.Err = err
			out[i] = fe
			warnings = append(warnings, fmt.Sprintf("feed %q: %s", fe.label(), err))
			continue
		}
		var kept []feeds.Entry
		for _, e := range results[i].Entries {
			if !e.Published.IsZero() && e.Published.Before(windowStart) {
				continue
			}
			if prev[string(blocks.URLHash(e.URL))] {
				continue
			}
			kept = append(kept, e)
		}
		sort.SliceStable(kept, func(i, j int) bool { return newerFirst(kept[i].Published, kept[j].Published) })
		if len(kept) > maxEntriesPerFeed {
			kept = kept[:maxEntriesPerFeed]
		}
		for _, e := range kept {
			all = append(all, picked{i, e})
		}
		fe.Entries = kept
		out[i] = fe
	}
	if len(all) <= maxEntriesTotal {
		return out, warnings
	}
	sort.SliceStable(all, func(i, j int) bool { return newerFirst(all[i].e.Published, all[j].e.Published) })
	keep := map[string]bool{}
	for _, p := range all[:maxEntriesTotal] {
		keep[fmt.Sprintf("%d\x00%s", p.feed, p.e.URL)] = true
	}
	for i := range out {
		if out[i].Err != "" {
			continue
		}
		var trimmed []feeds.Entry
		for _, e := range out[i].Entries {
			if keep[fmt.Sprintf("%d\x00%s", i, e.URL)] {
				trimmed = append(trimmed, e)
			}
		}
		out[i].Entries = trimmed
	}
	return out, warnings
}

// newerFirst orders entries newest first, undated ones last.
func newerFirst(a, b time.Time) bool {
	if a.IsZero() != b.IsZero() {
		return b.IsZero()
	}
	return a.After(b)
}
