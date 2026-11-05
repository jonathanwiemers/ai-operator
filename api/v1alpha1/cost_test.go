package v1alpha1

import (
	"math"
	"testing"
)

func TestCostOf(t *testing.T) {
	for in, want := range map[string]float64{"": 0, "x": 0, "NaN": 0, "1.5": 1.5, "0.0123": 0.0123} {
		if got := CostOf(in); got != want {
			t.Errorf("CostOf(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAddCost(t *testing.T) {
	for _, c := range []struct {
		s    string
		usd  float64
		want string
	}{
		{"", 0, "0.0000"},
		{"", 1.5, "1.5000"},
		{"bad", 2, "2.0000"},
		{"1.2500", 0.75, "2.0000"},
		{"0.0001", 0.00004, "0.0001"},
		{"0.0001", 0.00005, "0.0002"},
	} {
		if got := AddCost(c.s, c.usd); got != c.want {
			t.Errorf("AddCost(%q, %v) = %q, want %q", c.s, c.usd, got, c.want)
		}
	}
}

func TestFormatShowCost(t *testing.T) {
	if got := FormatCost(1.23456); got != "1.2346" {
		t.Errorf("FormatCost = %q", got)
	}
	if got := ShowCost(1.23456); got != "1.23" {
		t.Errorf("ShowCost = %q", got)
	}
	if got := ShowCost(0.005); got != "0.01" && got != "0.00" {
		t.Errorf("ShowCost = %q", got)
	}
}

func TestBudgetLeft(t *testing.T) {
	for _, c := range []struct {
		budget, spent string
		extra, want   float64
	}{
		{"10", "3", 2, 5},
		{"10", "", 0, 10},
		{"1", "3", 0.5, -2.5},
	} {
		if got := BudgetLeft(c.budget, c.spent, c.extra); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("BudgetLeft(%q, %q, %v) = %v, want %v", c.budget, c.spent, c.extra, got, c.want)
		}
	}
	for _, b := range []string{"", "x"} {
		if got := BudgetLeft(b, "3", 1); !math.IsInf(got, 1) {
			t.Errorf("BudgetLeft(%q) = %v, want +Inf", b, got)
		}
	}
}

func TestParseBudget(t *testing.T) {
	for in, want := range map[string]string{"5": "5.0000", "12.5": "12.5000", "$0.75": "0.7500", "40": "40.0000"} {
		if got, err := ParseBudget(in); got != want || err != nil {
			t.Errorf("ParseBudget(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0.00", "-5", "five", "1e3", "Inf", "5 USD", "5,50", "1234567890"} {
		if got, err := ParseBudget(in); got != "" || err == nil {
			t.Errorf("ParseBudget(%q) = %q, %v; want an error", in, got, err)
		}
	}
}
