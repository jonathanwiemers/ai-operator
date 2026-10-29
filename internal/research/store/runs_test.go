package store_test

import (
	"crypto/rand"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestWritePath(t *testing.T) {
	for _, asWriter := range []bool{false, true} {
		t.Run(fmt.Sprintf("writer=%v", asWriter), func(t *testing.T) {
			owner := storetest.New(t)
			s := owner
			if asWriter {
				role := "agents.research_writer_" + randomHex(t)
				s = storetest.Role(t, role)
				if err := owner.Migrate(t.Context(), store.Grants{Writers: []string{role}}); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Microsecond)
			run := newRun("agents", "news", now)
			if err := s.StartRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			// A second start, e.g. a restarted runner, changes nothing.
			again := run
			again.Engine, again.Status = "cursor", store.StatusFailed
			if err := s.StartRun(ctx, again); err != nil {
				t.Fatal(err)
			}
			if got := readRun(t, owner, run.ID); got.status != store.StatusRunning || got.engine != "claude" || got.finished {
				t.Errorf("after two starts: %+v, want the first, running", got)
			}

			res := result("https://Example.com/a#top", "https://example.com/b", "https://example.com/a")
			if err := s.SaveResult(ctx, run.ID, res, now); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishRun(ctx, run.ID, store.StatusDegraded, 0.42, "", []string{"dropped block 3"}, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			// A later finish, e.g. by the controller, does not change the outcome.
			if err := s.FinishRun(ctx, run.ID, store.StatusFailed, 0, "pod deadline", []string{"late"}, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			got := readRun(t, owner, run.ID)
			want := runRow{status: store.StatusDegraded, engine: "claude", finished: true, cost: 0.42, warnings: []string{"chosen first", "dropped block 3"}}
			if !got.equal(want) {
				t.Errorf("run = %+v, want %+v", got, want)
			}

			var title, firstURL string
			var expires time.Time
			if err := owner.Pool().QueryRow(ctx, `SELECT title, expires_at, blocks->1->'items'->0->>'url'
				FROM research_results WHERE run_id = $1`, run.ID).Scan(&title, &expires, &firstURL); err != nil {
				t.Fatal(err)
			}
			if title != "Morning news" || !expires.Equal(run.ExpiresAt) || firstURL != "https://Example.com/a#top" {
				t.Errorf("result: title %q, expires %v, first URL %q", title, expires, firstURL)
			}
			if n := count(t, owner.Pool(), "research_items"); n != 2 {
				t.Errorf("%d items, want 2 (one per distinct URL hash)", n)
			}

			urls, err := s.PreviousItemURLs(ctx, "agents", "news", 200)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(urls, []string{"https://Example.com/a#top", "https://example.com/b"}) {
				t.Errorf("PreviousItemURLs = %q", urls)
			}
		})
	}
}

func TestFirstSeenCarriesOver(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	day1 := time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)
	for _, step := range []struct {
		namespace, research string
		at                  time.Time
		urls                []string
	}{
		{"agents", "news", day1, []string{"https://example.com/a"}},
		{"agents", "news", day2, []string{"https://example.com/a", "https://example.com/b"}},
		// Other researches have their own first_seen.
		{"agents", "other", day2, []string{"https://example.com/a"}},
		{"team", "news", day2, []string{"https://example.com/a"}},
	} {
		run := newRun(step.namespace, step.research, step.at)
		if err := s.StartRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveResult(ctx, run.ID, result(step.urls...), step.at); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		namespace, research, url string
		want                     []time.Time
	}{
		{"agents", "news", "https://example.com/a", []time.Time{day1, day1}},
		{"agents", "news", "https://example.com/b", []time.Time{day2}},
		{"agents", "other", "https://example.com/a", []time.Time{day2}},
		{"team", "news", "https://example.com/a", []time.Time{day2}},
	} {
		var got []time.Time
		if err := s.Pool().QueryRow(ctx, `SELECT array_agg(i.first_seen ORDER BY r.started_at)
			FROM research_items i JOIN research_runs r ON r.id = i.run_id
			WHERE r.namespace = $1 AND r.research = $2 AND i.url_hash = $3`,
			c.namespace, c.research, blocks.URLHash(c.url)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !slices.EqualFunc(got, c.want, time.Time.Equal) {
			t.Errorf("%s/%s %s: first_seen %v, want %v", c.namespace, c.research, c.url, got, c.want)
		}
	}
}

func TestPreviousItemURLs(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	if urls, err := s.PreviousItemURLs(ctx, "agents", "news", 200); err != nil || urls != nil {
		t.Fatalf("without results: %q, %v", urls, err)
	}
	day1 := time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		namespace, research string
		at                  time.Time
		url                 string
	}{
		{"agents", "news", day1, "https://example.com/old"},
		{"agents", "news", day1.Add(24 * time.Hour), "https://example.com/latest"},
		{"agents", "other", day1.Add(48 * time.Hour), "https://example.com/other"},
		{"team", "news", day1.Add(48 * time.Hour), "https://example.com/team"},
	} {
		run := newRun(r.namespace, r.research, r.at)
		if err := s.StartRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveResult(ctx, run.ID, result(r.url, r.url+"/2", r.url+"/3"), r.at); err != nil {
			t.Fatal(err)
		}
	}
	// A run without a result does not hide the latest result.
	if err := s.StartRun(ctx, newRun("agents", "news", day1.Add(72*time.Hour))); err != nil {
		t.Fatal(err)
	}
	urls, err := s.PreviousItemURLs(ctx, "agents", "news", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(urls, []string{"https://example.com/latest", "https://example.com/latest/2"}) {
		t.Errorf("PreviousItemURLs = %q, want the first two of agents/news' latest result", urls)
	}
}

func TestFinishRunOrInsert(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	now := time.Now().UTC()
	finished := now.Add(time.Minute)

	// The pod died before the runner wrote anything.
	unknown := newRun("agents", "news", now)
	unknown.Status, unknown.FinishedAt, unknown.Error = store.StatusFailed, &finished, "pod failed"
	if err := s.FinishRunOrInsert(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if got := readRun(t, s, unknown.ID); !got.equal(runRow{status: store.StatusFailed, engine: "claude", finished: true, warnings: []string{"chosen first"}}) {
		t.Errorf("inserted run = %+v", got)
	}
	// The runner starting late does not revive it.
	if err := s.StartRun(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if got := readRun(t, s, unknown.ID); got.status != store.StatusFailed {
		t.Errorf("after a late start the run is %s, want failed", got.status)
	}

	running := newRun("agents", "news", now)
	if err := s.StartRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	dead := running
	dead.Status, dead.FinishedAt, dead.Warnings = store.StatusFailed, &finished, []string{"deadline"}
	if err := s.FinishRunOrInsert(ctx, dead); err != nil {
		t.Fatal(err)
	}
	if got := readRun(t, s, running.ID); !got.equal(runRow{status: store.StatusFailed, engine: "claude", finished: true, warnings: []string{"chosen first", "deadline"}}) {
		t.Errorf("finished run = %+v", got)
	}
	// A finished run stays as it is.
	dead.Status = store.StatusSucceeded
	if err := s.FinishRunOrInsert(ctx, dead); err != nil {
		t.Fatal(err)
	}
	if got := readRun(t, s, running.ID); got.status != store.StatusFailed || len(got.warnings) != 2 {
		t.Errorf("after a second finish: %+v", got)
	}

	dead.FinishedAt = nil
	if err := s.FinishRunOrInsert(ctx, dead); err == nil {
		t.Error("FinishRunOrInsert without a finished time: want an error")
	}
}

func TestSaveResultUnknownRun(t *testing.T) {
	s := storetest.New(t)
	if err := s.SaveResult(t.Context(), newUUID(), result("https://example.com"), time.Now()); err == nil {
		t.Fatal("SaveResult for an unknown run: want an error")
	}
	if n := count(t, s.Pool(), "research_results"); n != 0 {
		t.Errorf("%d results, want 0", n)
	}
}

func TestDeleteExpiredWritten(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	now := time.Now()
	for _, expires := range []time.Time{now.Add(-time.Minute), now.Add(time.Minute)} {
		run := newRun("agents", "news", now.Add(-time.Hour))
		run.ExpiresAt = expires
		if err := s.StartRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveResult(ctx, run.ID, result("https://example.com/a", "https://example.com/b"), now); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.DeleteExpired(ctx, now); err != nil || n != 1 {
		t.Fatalf("DeleteExpired = %d, %v, want 1", n, err)
	}
	for table, want := range map[string]int{"research_runs": 1, "research_results": 1, "research_items": 2} {
		if n := count(t, s.Pool(), table); n != want {
			t.Errorf("%d rows left in %s, want %d", n, table, want)
		}
	}
}

func newRun(namespace, research string, started time.Time) store.Run {
	return store.Run{
		ID: newUUID(), Namespace: namespace, Research: research, StartedAt: started,
		Engine: "claude", Model: "claude-sonnet-5-5", ChoiceReason: "chosen: claude/claude-sonnet-5-5",
		Warnings: []string{"chosen first"}, ExpiresAt: started.Add(720 * time.Hour).UTC().Truncate(time.Microsecond),
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// result is a news flash with one item per URL after a heading.
func result(urls ...string) blocks.Result {
	items := &blocks.Items{}
	for i, u := range urls {
		items.Items = append(items.Items, blocks.Item{Topic: "tech", Title: fmt.Sprintf("Item %d", i), URL: u, Importance: 2})
	}
	return blocks.Result{SchemaVersion: 1, Title: "Morning news", Summary: "Topics of the day", Blocks: []blocks.Block{
		{Type: blocks.TypeHeading, Heading: &blocks.Heading{Text: "Tech", Level: 2}},
		{Type: blocks.TypeItems, Items: items},
	}}
}

type runRow struct {
	status, engine string
	finished       bool
	cost           float64
	warnings       []string
}

func (r runRow) equal(o runRow) bool {
	return r.status == o.status && r.engine == o.engine && r.finished == o.finished && r.cost == o.cost &&
		slices.Equal(r.warnings, o.warnings)
}

func readRun(t *testing.T, s *store.Store, id string) runRow {
	t.Helper()
	var r runRow
	if err := s.Pool().QueryRow(t.Context(), `SELECT status, engine, finished_at IS NOT NULL,
		coalesce(cost_usd, 0)::float8, warnings FROM research_runs WHERE id = $1`, id).
		Scan(&r.status, &r.engine, &r.finished, &r.cost, &r.warnings); err != nil {
		t.Fatal(err)
	}
	return r
}
