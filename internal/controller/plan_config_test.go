package controller

import (
	"strings"
	"testing"
	"time"
)

func TestPlanConfigValidate(t *testing.T) {
	valid := PlanConfig{IdleTimeout: 30 * time.Minute, Retention: 720 * time.Hour, MaxBudgetUSD: "5"}
	cases := []struct {
		name    string
		mutate  func(*PlanConfig)
		wantErr string
	}{
		{name: "defaults"},
		{name: "zero idle", mutate: func(c *PlanConfig) { c.IdleTimeout = 0 }, wantErr: "idle timeout"},
		{name: "negative idle", mutate: func(c *PlanConfig) { c.IdleTimeout = -time.Minute }, wantErr: "idle timeout"},
		{name: "zero retention", mutate: func(c *PlanConfig) { c.Retention = 0 }, wantErr: "retention"},
		{name: "negative retention", mutate: func(c *PlanConfig) { c.Retention = -time.Hour }, wantErr: "retention"},
		{name: "empty budget", mutate: func(c *PlanConfig) { c.MaxBudgetUSD = "" }, wantErr: "max budget"},
		{name: "zero budget", mutate: func(c *PlanConfig) { c.MaxBudgetUSD = "0" }, wantErr: "max budget"},
		{name: "negative budget", mutate: func(c *PlanConfig) { c.MaxBudgetUSD = "-1" }, wantErr: "max budget"},
		{name: "non-decimal budget", mutate: func(c *PlanConfig) { c.MaxBudgetUSD = "five" }, wantErr: "max budget"},
		{name: "decimal budget", mutate: func(c *PlanConfig) { c.MaxBudgetUSD = "12.50" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			err := c.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
