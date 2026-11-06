package store

import (
	"fmt"
	"testing"
	"time"
)

var pauseBase = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

// pauseEvent is an operator pause event of task u1 (keyed like
// internal/history) at minute min for a pause that started at minute start.
func pauseEvent(kind string, min, start int, pk, by string, data map[string]any) Event {
	return pauseEventOf("u1", kind, min, start, pk, by, data)
}

func pauseEventOf(uid, kind string, min, start int, pk, by string, data map[string]any) Event {
	d := map[string]any{"kind": pk, "reason": pk + " reason", "item": float64(88)}
	if kind == resumedKind {
		d["resolvedBy"] = by
		d["pausedSec"] = float64((min - start) * 60)
	}
	for k, v := range data {
		d[k] = v
	}
	startAt := pauseBase.Add(time.Duration(start) * time.Minute)
	return Event{Key: fmt.Sprintf("task/%s/%s/%d/%s", uid, kind, startAt.Unix(), pk), Kind: kind,
		At: pauseBase.Add(time.Duration(min) * time.Minute), Data: d}
}

func TestPairPauses(t *testing.T) {
	now := pauseBase.Add(100 * time.Minute)
	events := []Event{
		pauseEvent(pausedKind, 0, 0, "ciRed", "", nil),
		pauseEvent(resumedKind, 10, 0, "ciRed", "selfHealed", nil),
		pauseEvent(pausedKind, 20, 20, "stuck", "", nil),
		// Another kind: the operator ends the stuck pause, a new one starts.
		pauseEvent(resumedKind, 50, 20, "stuck", "owner", nil),
		pauseEvent(pausedKind, 50, 50, "ciRed", "", nil),
		pauseEvent(resumedKind, 70, 50, "ciRed", "operator", nil),
		// Still open: counts up to now.
		pauseEvent(pausedKind, 90, 90, "budget", "", nil),
	}
	h := pairPauses(events, now)
	if len(h.Pauses) != 4 {
		t.Fatalf("pauses = %+v", h.Pauses)
	}
	want := []struct {
		kind, by string
		sec      int64
		open     bool
	}{{"ciRed", "selfHealed", 600, false}, {"stuck", "owner", 1800, false}, {"ciRed", "operator", 1200, false}, {"budget", "", 600, true}}
	for i, w := range want {
		p := h.Pauses[i]
		if p.Kind != w.kind || p.ResolvedBy != w.by || p.Seconds != w.sec || (p.Until == nil) != w.open ||
			p.Item != 88 || p.Reason != w.kind+" reason" {
			t.Errorf("pause %d = %+v, want %+v", i, p, w)
		}
	}
	if !h.Pauses[0].Since.Equal(pauseBase) || !h.Pauses[0].Until.Equal(pauseBase.Add(10*time.Minute)) {
		t.Errorf("first pause %v – %v", h.Pauses[0].Since, h.Pauses[0].Until)
	}
	tot := h.Totals
	if tot.Count != 4 || tot.PausedSec != 4200 || tot.MeanResumeSec != 1200 {
		t.Errorf("totals = %+v", tot)
	}
	if k := tot.ByKind["ciRed"]; k.Count != 2 || k.Sec != 1800 {
		t.Errorf("ciRed = %+v", k)
	}
	if k := tot.ByKind["budget"]; k.Count != 1 || k.Sec != 600 {
		t.Errorf("budget = %+v", k)
	}
	if r := tot.ByResolver; len(r) != 3 || r["selfHealed"] != 1 || r["owner"] != 1 || r["operator"] != 1 {
		t.Errorf("by resolver = %v", r)
	}
}

func TestPairPausesWithoutTheirPartner(t *testing.T) {
	now := pauseBase.Add(100 * time.Minute)
	h := pairPauses([]Event{
		// The paused event expired: the resumed one says how long it took.
		pauseEvent(resumedKind, 30, 5, "ciRed", "owner", nil),
		// Paused under a resource-version key, resumed under PausedAt.
		{Key: "task/u2/autopilot.paused/rv7/stuck", Kind: pausedKind, At: pauseBase.Add(40 * time.Minute),
			Data: map[string]any{"kind": "stuck"}},
		pauseEventOf("u2", resumedKind, 45, 40, "stuck", "pilot", nil),
		// A missed resume: the next pause of the task ends it.
		pauseEventOf("u3", pausedKind, 60, 60, "", "", nil),
		pauseEventOf("u3", pausedKind, 80, 80, "budget", "", nil),
		{Key: "not an operator key", Kind: pausedKind, At: pauseBase},
	}, now)
	if len(h.Pauses) != 4 {
		t.Fatalf("pauses = %+v", h.Pauses)
	}
	if p := h.Pauses[0]; p.Kind != "ciRed" || !p.Since.Equal(pauseBase.Add(5*time.Minute)) || p.Seconds != 1500 || p.ResolvedBy != "owner" {
		t.Errorf("expired paused event: %+v", p)
	}
	if p := h.Pauses[1]; p.Kind != "stuck" || p.Seconds != 300 || p.ResolvedBy != "pilot" {
		t.Errorf("resource-version key: %+v", p)
	}
	if p := h.Pauses[2]; p.Kind != "unknown" || p.Until == nil || p.Seconds != 1200 || p.ResolvedBy != "" {
		t.Errorf("missed resume: %+v", p)
	}
	if p := h.Pauses[3]; p.Until != nil || p.Seconds != 1200 {
		t.Errorf("open: %+v", p)
	}
	if r := h.Totals.ByResolver; r["unknown"] != 1 || r["owner"] != 1 || r["pilot"] != 1 {
		t.Errorf("by resolver = %v", r)
	}
	if h := pairPauses(nil, now); h.Pauses == nil || h.Totals.ByKind == nil || h.Totals.ByResolver == nil || h.Totals.MeanResumeSec != 0 {
		t.Errorf("no events: %+v", h)
	}
}

func TestPairPausesResumedAfterTheNextPause(t *testing.T) {
	// The resumed event of the ciRed pause has the time of the next pause
	// but sorts after it.
	h := pairPauses([]Event{
		pauseEvent(pausedKind, 0, 0, "ciRed", "", nil),
		pauseEvent(pausedKind, 10, 10, "stuck", "", nil),
		pauseEvent(resumedKind, 10, 0, "ciRed", "operator", nil),
		pauseEvent(resumedKind, 30, 10, "stuck", "owner", nil),
	}, pauseBase.Add(time.Hour))
	if len(h.Pauses) != 2 {
		t.Fatalf("pauses = %+v", h.Pauses)
	}
	if p := h.Pauses[0]; p.Kind != "ciRed" || p.ResolvedBy != "operator" || p.Seconds != 600 {
		t.Errorf("ciRed = %+v", p)
	}
	if p := h.Pauses[1]; p.Kind != "stuck" || p.ResolvedBy != "owner" || p.Seconds != 1200 {
		t.Errorf("stuck = %+v", p)
	}
	if h.Totals.Count != 2 || h.Totals.ByResolver["unknown"] != 0 {
		t.Errorf("totals = %+v", h.Totals)
	}
}
