package controller

import (
	"fmt"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// PlanConfig is the operator's plan.* settings (Decision 6 and 7).
type PlanConfig struct {
	IdleTimeout  time.Duration
	Retention    time.Duration
	MaxBudgetUSD string
}

// Validate refuses non-positive durations and a budget that is not a
// decimal above 0.
func (c PlanConfig) Validate() error {
	switch {
	case c.IdleTimeout <= 0:
		return fmt.Errorf("plan idle timeout must be positive, got %s", c.IdleTimeout)
	case c.Retention <= 0:
		return fmt.Errorf("plan retention must be positive, got %s", c.Retention)
	}
	if _, err := aiv1.ParseBudget(c.MaxBudgetUSD); err != nil {
		return fmt.Errorf("plan max budget: %w", err)
	}
	return nil
}
