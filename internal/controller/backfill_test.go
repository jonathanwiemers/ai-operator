package controller

import (
	"errors"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// The poll steps the backfill: the first lists the closed milestones, the
// next saves the one the operator planned; a closed milestone it never
// planned stays out.
func TestPollStepsBackfill(t *testing.T) {
	s := storetest.New(t)
	ready := make(chan struct{})
	close(ready)
	e := newProjectEnv(t)
	clk := clocktesting.NewFakeClock(time.Now())
	e.r.Backfill = history.NewBackfill(chatDB{ready: ready, store: s}, time.Hour, clk, zap.New(zap.UseDevMode(false)))
	done := time.Now().Add(-48 * time.Hour)
	e.gl.Milestones = []gitlab.Milestone{
		{ID: 31, IID: 3, Title: "Old checkout", State: "closed", Description: "/agent spec\nBuild a checkout.", UpdatedAt: &done},
		{ID: 32, IID: 4, Title: "Human sprint", State: "closed"},
	}
	m := e.gl.Milestones[0]
	e.gl.Issues[7] = &gitlab.Issue{IID: 7, Title: "Cart", State: "closed", Milestone: &m}

	e.reconcile()
	clk.Step(15 * time.Second)
	e.reconcile()
	clk.Step(15 * time.Second)
	e.reconcile()
	shop := store.ProjectKey{Namespace: ns, Name: "shop"}
	got, err := s.Milestone(t.Context(), shop, 3, time.Now())
	if err != nil {
		t.Fatalf("closed milestone 3 not backfilled: %v", err)
	}
	if got.Title != "Old checkout" || len(got.Issues) != 1 || got.Issues[0].State != "closed" {
		t.Errorf("backfilled milestone = %+v", got)
	}
	if _, err := s.Milestone(t.Context(), shop, 4, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("milestone 4, never planned: %v, want not found", err)
	}
	if _, complete, err := s.ProjectListed(t.Context(), shop); err != nil || !complete {
		t.Errorf("ProjectListed: complete %v, %v; want complete", complete, err)
	}
}
