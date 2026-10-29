/*
Copyright 2026 Jonathan Wiemers.

Licensed under the Apache License, Version 2.0.
*/
package main

import (
	"bytes"
	"crypto/sha256"
	"maps"
	"os"
	"regexp"
	"testing"

	"sigs.k8s.io/yaml"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
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
