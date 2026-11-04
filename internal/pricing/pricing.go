// Package pricing estimates the USD cost of agent calls whose engine
// reports tokens but no cost (cursor), from a table of prices per model.
package pricing

import (
	"fmt"
	"math"
	"os"

	"sigs.k8s.io/yaml"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Price is a model's price in USD per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Table holds the prices keyed by "<engine>/<model>", a bare model or
// "<engine>/*". A nil table has no prices.
type Table struct {
	Models map[string]Price `json:"models"`
}

// Load reads the pricing file at path, strictly: unknown keys and negative
// prices are errors.
func Load(path string) (*Table, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses a pricing file, see Load.
func Parse(b []byte) (*Table, error) {
	var t Table
	if err := yaml.UnmarshalStrict(b, &t); err != nil {
		return nil, fmt.Errorf("pricing: %w", err)
	}
	for k, p := range t.Models {
		if k == "" {
			return nil, fmt.Errorf("pricing: empty model key")
		}
		for _, v := range []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("pricing: model %q: prices must be finite and not negative", k)
			}
		}
	}
	return &t, nil
}

// Estimate is the cost in USD (4 decimals) of a call of model on engine
// with the given tokens. It is false when the table has no price for the
// model or the call has no tokens. The price is the first of
// "<engine>/<model>", the bare model and "<engine>/*".
func (t *Table) Estimate(engine, model string, in, out, cacheRead, cacheWrite int64) (float64, bool) {
	if t == nil || in+out+cacheRead+cacheWrite <= 0 {
		return 0, false
	}
	p, ok := t.lookup(engine, model)
	if !ok {
		return 0, false
	}
	usd := (float64(in)*p.Input + float64(out)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6
	return aiv1.CostOf(aiv1.FormatCost(usd)), true
}

func (t *Table) lookup(engine, model string) (Price, bool) {
	keys := []string{engine + "/" + model, model, engine + "/*"}
	if model == "" {
		keys = keys[2:]
	}
	for _, k := range keys {
		if p, ok := t.Models[k]; ok {
			return p, true
		}
	}
	return Price{}, false
}
