package store

import (
	"encoding/json"
	"strconv"
)

// Money is an amount in USD that may be unknown: calls without a cost
// (cursor without pricing) never add $0. The zero Money is unknown.
//
// In JSON it is the amount with 2 decimals, "" when unknown, never
// "0.00": the web shows it as it is. tygo makes it a string (Money in
// web/src/generated/store.ts).
type Money struct {
	USD   float64
	Known bool
}

// USD is the known amount usd.
func USD(usd float64) Money { return Money{USD: usd, Known: true} }

// String is the amount with 2 decimals, "" when unknown.
func (m Money) String() string {
	if !m.Known {
		return ""
	}
	return strconv.FormatFloat(m.USD, 'f', 2, 64)
}

// Add is the sum of m and o: known when either is, unknown amounts add
// nothing.
func (m Money) Add(o Money) Money {
	switch {
	case !o.Known:
		return m
	case !m.Known:
		return o
	}
	return USD(m.USD + o.USD)
}

// MarshalJSON writes String as a JSON string.
func (m Money) MarshalJSON() ([]byte, error) {
	return strconv.AppendQuote(nil, m.String()), nil
}

// UnmarshalJSON reads what MarshalJSON writes: "" is unknown, any other
// string a known amount.
func (m *Money) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*m = Money{}
		return nil
	}
	usd, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*m = USD(usd)
	return nil
}
