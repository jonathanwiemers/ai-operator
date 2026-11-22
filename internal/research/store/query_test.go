package store_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// asReader returns a store connected as a role with the reader's grants.
func asReader(t *testing.T, owner *store.Store) *store.Store {
	t.Helper()
	role := "research_reader_" + randomHex(t)
	reader := storetest.Role(t, role)
	if err := owner.Migrate(t.Context(), store.Grants{Reader: role}); err != nil {
		t.Fatal(err)
	}
	return reader
}

// seed stores a run of namespace/research started at with a result saved a
// minute later, and returns the run.
func seed(t *testing.T, s *store.Store, namespace, research string, at time.Time, res blocks.Result) store.Run {
	t.Helper()
	run := newRun(namespace, research, at)
	if err := s.StartRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveResult(t.Context(), run.ID, res, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(t.Context(), run.ID, store.StatusSucceeded, 0.25, "", nil, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestReadQueriesAsReader(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	day1 := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	day2, day3 := day1.Add(24*time.Hour), day1.Add(48*time.Hour)
	run1 := seed(t, owner, "agents", "news", day1, result("https://example.com/a"))
	run2 := seed(t, owner, "agents", "news", day2, result("https://example.com/a", "https://example.com/b"))
	// A failed run has no result.
	run3 := newRun("agents", "news", day3)
	if err := owner.StartRun(ctx, run3); err != nil {
		t.Fatal(err)
	}
	if err := owner.FinishRun(ctx, run3.ID, store.StatusFailed, 0, "timeout", nil, day3.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	seed(t, owner, "agents", "other", day3, result("https://example.com/b"))

	far := day3.Add(time.Hour)
	page, err := s.ListRuns(ctx, "agents", "news", far, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ids := runIDs(page); !slices.Equal(ids, []string{run3.ID, run2.ID}) {
		t.Fatalf("first page = %v, want runs 3 and 2", ids)
	}
	next, err := s.ListRuns(ctx, "agents", "news", page[1].StartedAt, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ids := runIDs(next); !slices.Equal(ids, []string{run1.ID}) {
		t.Errorf("second page = %v, want run 1", ids)
	}
	if none, err := s.ListRuns(ctx, "agents", "missing", far, 10); err != nil || len(none) != 0 {
		t.Errorf("runs of an unknown research: %v, %v", none, err)
	}

	got, err := s.GetRun(ctx, run3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed || got.Error != "timeout" || got.FinishedAt == nil || got.Research != "news" ||
		got.ChoiceReason != run3.ChoiceReason || !got.StartedAt.Equal(day3) {
		t.Errorf("GetRun = %+v", got)
	}
	if got, err := s.GetRun(ctx, run2.ID); err != nil || got.CostUSD != 0.25 || got.Status != store.StatusSucceeded {
		t.Errorf("GetRun(run 2) = %+v, %v", got, err)
	}
	// A running run has no finish time and no cost (both NULL).
	running := newRun("agents", "running", day3)
	if err := owner.StartRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetRun(ctx, running.ID); err != nil || got.FinishedAt != nil || got.CostUSD != 0 ||
		got.Status != store.StatusRunning || !slices.Equal(got.Warnings, running.Warnings) {
		t.Errorf("GetRun(running) = %+v, %v", got, err)
	}
	for _, id := range []string{newUUID(), "not-a-uuid", ""} {
		if _, err := s.GetRun(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("GetRun(%q): %v, want ErrNotFound", id, err)
		}
		if _, err := s.GetResult(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("GetResult(%q): %v, want ErrNotFound", id, err)
		}
	}
	if _, err := s.GetResult(ctx, run3.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetResult of a failed run: %v, want ErrNotFound", err)
	}

	// Items are new in the run their URL first appeared in.
	for _, c := range []struct {
		run  store.Run
		want []string
	}{
		{run1, []string{"https://example.com/a"}},
		{run2, []string{"https://example.com/b"}},
	} {
		res, err := s.GetResult(ctx, c.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if res.RunID != c.run.ID || res.Title != "Morning news" || !res.ExpiresAt.Equal(c.run.ExpiresAt) ||
			!res.CreatedAt.Equal(c.run.StartedAt.Add(time.Minute)) {
			t.Errorf("result of %s = %+v", c.run.ID, res)
		}
		if !sameHashes(res.NewURLHashes, c.want) {
			t.Errorf("result of the run at %v: %d new hashes, want %v", c.run.StartedAt, len(res.NewURLHashes), c.want)
		}
		if r, _, err := blocks.Validate([]byte(`{"schemaVersion":1,"title":"t","blocks":` + string(res.Blocks) + `}`)); err != nil ||
			len(r.Blocks) != 2 || r.Blocks[1].Items == nil {
			t.Errorf("stored blocks %s: %v", res.Blocks, err)
		}
	}

	latest, err := s.LatestResult(ctx, "agents", "news")
	if err != nil {
		t.Fatal(err)
	}
	if latest.RunID != run2.ID || !sameHashes(latest.NewURLHashes, []string{"https://example.com/b"}) {
		t.Errorf("LatestResult = run %s, want run 2 (the failed run 3 has none)", latest.RunID)
	}
	if _, err := s.LatestResult(ctx, "agents", "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("LatestResult of an unknown research: %v, want ErrNotFound", err)
	}
}

func runIDs(runs []store.Run) []string {
	var ids []string
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// sameHashes reports whether hashes are the URL hashes of urls, in any order.
func sameHashes(hashes [][]byte, urls []string) bool {
	if len(hashes) != len(urls) {
		return false
	}
	for _, u := range urls {
		if !slices.ContainsFunc(hashes, func(h []byte) bool { return bytes.Equal(h, blocks.URLHash(u)) }) {
			return false
		}
	}
	return true
}
