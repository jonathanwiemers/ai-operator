/*
Copyright 2026 Jonathan Wiemers.

Licensed under the Apache License, Version 2.0.
*/
package main

import (
	"bytes"
	"crypto/sha256"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// The seed's results pass the validator, so the pages show them; the
// hostile one fails, so the UI must withhold it. The seed hashes item URLs
// with sha256 in SQL, which matches blocks.URLHash only for normalized URLs.
func TestSeedResults(t *testing.T) {
	sql, err := os.ReadFile("../research-seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	good := regexp.MustCompile(`(?s)\$json\$(.*?)\$json\$`).FindAllSubmatch(sql, -1)
	if len(good) != 3 {
		t.Fatalf("%d $json$ results, want 3", len(good))
	}
	types := map[string]bool{}
	for i, m := range good {
		res, warnings, err := blocks.Validate(bytes.TrimSpace(m[1]))
		if err != nil || len(warnings) > 0 {
			t.Errorf("result %d: %v, warnings %v", i, err, warnings)
			continue
		}
		for _, b := range res.Blocks {
			types[b.Type] = true
			if b.Items == nil {
				continue
			}
			for _, it := range b.Items.Items {
				if sum := sha256.Sum256([]byte(it.URL)); !bytes.Equal(sum[:], blocks.URLHash(it.URL)) {
					t.Errorf("item URL %s is not normalized", it.URL)
				}
			}
		}
	}
	for _, typ := range []string{blocks.TypeHeading, blocks.TypeMarkdown, blocks.TypeItems, blocks.TypeTable,
		blocks.TypeStats, blocks.TypeLinks} {
		if !types[typ] {
			t.Errorf("no %s block in the seed", typ)
		}
	}
	hostile := regexp.MustCompile(`(?s)\$hostile\$(.*?)\$hostile\$`).FindAllSubmatch(sql, -1)
	if len(hostile) != 1 {
		t.Fatalf("%d $hostile$ results, want 1", len(hostile))
	}
	if _, _, err := blocks.Validate(bytes.TrimSpace(hostile[0][1])); err == nil {
		t.Error("the hostile result passes validation")
	}
}

// The seed applies to a migrated database, and the Spend tab sums its agent
// calls and the run from before token tracking; #77's run 1 counts its calls,
// not its run.finished event, and the cursor fix has no cost.
func TestSeedSpend(t *testing.T) {
	st := storetest.New(t)
	sql, err := os.ReadFile("../research-seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	storetest.Exec(t, string(sql))
	ctx := t.Context()
	sum := func(sc store.SpendScope) store.Spend {
		t.Helper()
		sc.Namespace, sc.Project = "research-dev", "dev"
		sp, err := st.Spend(ctx, sc)
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("%s: $%.4f, want $%.4f", what, got, want)
		}
	}

	p := sum(store.SpendScope{})
	near("project", p.Totals.CostUSD.USD, 11.06)
	if p.Totals.Calls != 10 || p.Totals.UnknownCalls != 1 || len(p.Children) != 2 {
		t.Errorf("project: %d calls, %d unknown, %d children", p.Totals.Calls, p.Totals.UnknownCalls, len(p.Children))
	}

	m := sum(store.SpendScope{MilestoneIID: 4})
	near("milestone", m.Totals.CostUSD.USD, 11.01)
	near("milestone legacy", m.Totals.LegacyCostUSD.USD, 1.75)
	var keys []string
	for _, c := range m.Children {
		keys = append(keys, c.Level+"/"+c.Key)
	}
	if want := []string{"issue/77", "issue/78", "milestone/4"}; !slices.Equal(keys, want) {
		t.Errorf("milestone children %v, want %v", keys, want)
	}
	if m.Totals.SubagentCalls != 2 {
		t.Errorf("milestone: %d subagent calls, want 2", m.Totals.SubagentCalls)
	}

	i := sum(store.SpendScope{MilestoneIID: 4, IssueIID: 77})
	near("#77", i.Totals.CostUSD.USD, 6.63)
	if len(i.Children) != 2 {
		t.Errorf("#77: %d runs, want 2", len(i.Children))
	}

	r := sum(store.SpendScope{MilestoneIID: 4, IssueIID: 77, Task: "dev-issue-77", Run: 2})
	if len(r.Children) != 2 || r.Totals.UnknownCalls != 1 {
		t.Fatalf("#77 run 2: %d calls, %d unknown", len(r.Children), r.Totals.UnknownCalls)
	}
	if fix := r.Children[0].Call; fix == nil || fix.CostSource != "unknown" || fix.CostUSD != nil || fix.FallbackFrom != "claude" {
		t.Errorf("#77 run 2 call 4: %+v", fix)
	}
}

// The seed's Desk milestones: a running, a paused, a waiting (running without
// an active issue) and two done ones, one with a retrospective and its three
// follow-ups, the older one without spend. The paused one shows paused time.
func TestSeedDeskMilestones(t *testing.T) {
	st := storetest.New(t)
	sql, err := os.ReadFile("../research-seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Twice in a row: the second run replaces the rows.
	storetest.Exec(t, string(sql))
	storetest.Exec(t, string(sql))
	ctx := t.Context()
	dev := store.ProjectKey{Namespace: "research-dev", Name: "dev"}
	web := store.ProjectKey{Namespace: "research-dev", Name: "web"}
	rows, err := st.Milestones(ctx, store.MilestoneQuery{Projects: []store.ProjectKey{dev, web}})
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		p   string
		iid int64
	}
	got := map[key]store.MilestoneRow{}
	for _, r := range rows {
		got[key{r.Project.Name, r.IID}] = r
	}
	if len(rows) != 5 {
		t.Fatalf("%d milestones, want 5", len(rows))
	}
	if r := got[key{"web", 2}]; r.Phase != "running" || r.Issues != 5 || r.Merged != 2 || !r.Spend.CostUSD.Known {
		t.Errorf("running: %+v", r)
	}
	if r := got[key{"web", 3}]; r.Phase != "paused" || r.State != "open" {
		t.Errorf("paused: %+v", r)
	}
	if r := got[key{"dev", 5}]; r.Phase != "running" || r.Merged != 1 {
		t.Errorf("waiting: %+v", r)
	}
	if r := got[key{"dev", 3}]; r.State != "closed" || r.Spend.CostUSD.Known {
		t.Errorf("old done milestone has spend: %+v", r)
	}
	if r := got[key{"dev", 4}]; r.State != "closed" || !r.Spend.CostUSD.Known || r.FollowUpsOpen != 1 {
		t.Errorf("done: %+v", r)
	}

	d, err := st.Milestone(ctx, dev, 4, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.RetroText, "## Leftovers") || strings.Contains(d.RetroText, "<") || d.RetroPostedAt.IsZero() {
		t.Errorf("retrospective: %q posted %v", d.RetroText, d.RetroPostedAt)
	}
	var states []string
	for _, f := range d.FollowUps {
		states = append(states, f.State)
	}
	if want := []string{"open", "issue", "dismissed"}; !slices.Equal(states, want) || d.FollowUps[1].TargetIID == 0 {
		t.Errorf("follow-ups %v: %+v", states, d.FollowUps)
	}

	p, err := st.Milestone(ctx, web, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Issues) != 3 || p.Issues[0].PausedSec < 5400 || p.Issues[0].WallSec == 0 || p.Issues[0].AgentMS == 0 {
		t.Errorf("paused milestone issues: %+v", p.Issues)
	}
}

// hack/research-dev.yaml has an AgentResearch for every research of the seed
// in its namespace, with fields the CRD knows.
func TestDevResearches(t *testing.T) {
	raw, err := os.ReadFile("../research-dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, doc := range bytes.Split(raw, []byte("\n---\n")) {
		var ar aiv1.AgentResearch
		if err := yaml.UnmarshalStrict(doc, &ar); err != nil {
			t.Fatal(err)
		}
		if ar.Kind == "AgentResearch" {
			if ar.Namespace != "research-dev" || ar.Spec.Prompt == "" || ar.Spec.ProjectRef == "" {
				t.Errorf("research %s/%s: %+v", ar.Namespace, ar.Name, ar.Spec)
			}
			names[ar.Name] = true
		}
	}
	sql, err := os.ReadFile("../research-seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	seeded := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\('[0-9a-f-]{36}', '([a-z-]+)'`).FindAllSubmatch(sql, -1) {
		seeded[string(m[1])] = true
	}
	if !maps.Equal(names, seeded) {
		t.Errorf("researches in research-dev.yaml %v, in the seed %v", names, seeded)
	}
}
