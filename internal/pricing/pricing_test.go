package pricing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const file = `
models:
  "cursor/auto": {input: 1.25, output: 10, cacheRead: 0.125, cacheWrite: 1.25}
  "gpt-5": {input: 2, output: 20}
  "cursor/gpt-5": {input: 3, output: 30}
  "cursor/*": {input: 1, output: 1}
`

func load(t *testing.T, content string) (*Table, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pricing.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestEstimateLookupOrder(t *testing.T) {
	tab, err := load(t, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		engine, model string
		want          float64
	}{
		{"cursor", "gpt-5", 33},   // exact engine/model before the bare model
		{"claude", "gpt-5", 22},   // the bare model
		{"cursor", "sonnet-5", 2}, // the engine fallback
		{"cursor", "", 2},         // no model: only the engine fallback
		{"cursor", "auto", 11.25}, // exact
	} {
		got, ok := tab.Estimate(tc.engine, tc.model, 1e6, 1e6, 0, 0)
		if !ok || got != tc.want {
			t.Errorf("Estimate(%s, %s) = %v, %v; want %v", tc.engine, tc.model, got, ok, tc.want)
		}
	}
	if _, ok := tab.Estimate("claude", "opus", 1e6, 0, 0, 0); ok {
		t.Errorf("a model without a price was estimated")
	}
}

func TestEstimatePerMillion(t *testing.T) {
	tab, err := Parse([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	// 20000 in × 1.25 + 5000 out × 10 + 400000 cache read × 0.125 + 10000 cache write × 1.25, per million.
	got, ok := tab.Estimate("cursor", "auto", 20000, 5000, 400000, 10000)
	if want := 0.1375; !ok || got != want {
		t.Errorf("Estimate = %v, %v; want %v", got, ok, want)
	}
	// Tiny calls round to 4 decimals like reported costs.
	if got, _ := tab.Estimate("cursor", "auto", 1, 0, 0, 0); got != 0 {
		t.Errorf("one token = %v, want 0 at 4 decimals", got)
	}
	if _, ok := tab.Estimate("cursor", "auto", 0, 0, 0, 0); ok {
		t.Errorf("a call without tokens was estimated")
	}
	var none *Table
	if _, ok := none.Estimate("cursor", "auto", 10, 10, 0, 0); ok {
		t.Errorf("a nil table estimated")
	}
}

func TestParseIsStrict(t *testing.T) {
	for name, content := range map[string]string{
		"unknown top-level key": "model: {}",
		"unknown price key":     `models: {"cursor/auto": {input: 1, outputs: 2}}`,
		"negative price":        `models: {"cursor/auto": {input: -1}}`,
		"empty key":             `models: {"": {input: 1}}`,
		"not a number":          `models: {"cursor/auto": {input: cheap}}`,
	} {
		if _, err := Parse([]byte(content)); err == nil || !strings.Contains(err.Error(), "pricing") {
			t.Errorf("%s: err = %v, want a pricing error", name, err)
		}
	}
	if _, err := load(t, ""); err != nil {
		t.Errorf("an empty file: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Errorf("a missing file loaded")
	}
}
