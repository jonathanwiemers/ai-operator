package runner

import (
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func turnDecision(id string) planTurnDecision {
	return planTurnDecision{ID: id, Title: "Where " + id, Question: "Which page for " + id + "?",
		Options: []string{"Rail", "Footer"}, Recommended: 1, Why: "Seen on every page", State: "locked"}
}

// storedDecision is d as a turn at at stored it, at version v.
func storedDecision(d planTurnDecision, v int64, at time.Time) aiv1.PlanDecision {
	return aiv1.PlanDecision{ID: d.ID, Title: d.Title, Question: d.Question, Options: d.Options,
		Recommended: int32(d.Recommended), Why: d.Why, State: aiv1.PlanDecisionState(d.State),
		Version: v, DecidedBy: "agent", DecidedAt: metav1.Time{Time: at}}
}

func TestValidPlanDecisions(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	prev := []aiv1.PlanDecision{storedDecision(turnDecision("D1"), 1, t0), storedDecision(turnDecision("D3"), 1, t0)}
	with := func(id string, f func(*planTurnDecision)) planTurnDecision {
		d := turnDecision(id)
		f(&d)
		return d
	}
	many := make([]planTurnDecision, 31)
	for i := range many {
		many[i] = turnDecision(fmt.Sprintf("D%d", i+4))
	}
	for name, tc := range map[string]struct {
		in   []planTurnDecision
		want string // part of the error, "" for valid
	}{
		"kept and new":       {in: []planTurnDecision{turnDecision("D3"), turnDecision("D1"), turnDecision("D4")}},
		"D2 dropped, D5 new": {in: []planTurnDecision{turnDecision("D5")}},
		"30 entries":         {in: many[:30]},
		"open, padded, 5 opts": {in: []planTurnDecision{with("D4", func(d *planTurnDecision) {
			d.State, d.ID, d.Options = "open", " D4 ", []string{"a", "b", "c", "d", "e"}
		})}},
		"limits in runes":       {in: []planTurnDecision{with("D4", func(d *planTurnDecision) { d.Title = " " + strings.Repeat("é", 120) + " " })}},
		"empty why":             {in: []planTurnDecision{with("D4", func(d *planTurnDecision) { d.Why = "" })}},
		"31 entries":            {in: many, want: "31 decisions"},
		"one option":            {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Options, d.Recommended = []string{"Rail"}, 0 })}, want: "1 options"},
		"six options":           {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Options = []string{"a", "b", "c", "d", "e", "f"} })}, want: "6 options"},
		"blank option":          {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Options[0] = "  " })}, want: "option 0"},
		"long option":           {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Options[1] = strings.Repeat("x", 501) })}, want: "option 1"},
		"dropped ID reused":     {in: []planTurnDecision{with("D2", func(d *planTurnDecision) { d.Title = "Something else" })}, want: "D2 reuses"},
		"dropped ID same text":  {in: []planTurnDecision{turnDecision("D2")}, want: "D2 reuses"},
		"recommended too high":  {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Recommended = 2 })}, want: "recommends option 2"},
		"recommended negative":  {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Recommended = -1 })}, want: "recommends option -1"},
		"duplicate ID":          {in: []planTurnDecision{turnDecision("D4"), turnDecision("D4")}, want: "twice"},
		"bad ID":                {in: []planTurnDecision{turnDecision("D0")}, want: "not D1"},
		"lowercase ID":          {in: []planTurnDecision{turnDecision("d4")}, want: "not D1"},
		"ID beyond int32":       {in: []planTurnDecision{turnDecision("D99999999999")}, want: "not D1"},
		"bad state":             {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.State = "done" })}, want: "state"},
		"blank title":           {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Title = " " })}, want: "title"},
		"long title":            {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Title = strings.Repeat("é", 121) })}, want: "title has 121"},
		"long question":         {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Question = strings.Repeat("x", 1001) })}, want: "question"},
		"blank question":        {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Question = "" })}, want: "question"},
		"long why":              {in: []planTurnDecision{with("D1", func(d *planTurnDecision) { d.Why = strings.Repeat("x", 1001) })}, want: "why"},
		"new below seq, no gap": {in: []planTurnDecision{turnDecision("D1"), turnDecision("D3"), turnDecision("D2")}, want: "D2 reuses"},
	} {
		err := validPlanDecisions(prev, 3, tc.in)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want %q", name, err, tc.want)
		}
	}
}

func TestMergePlanDecisions(t *testing.T) {
	t0, t1 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	signed := storedDecision(turnDecision("D1"), 2, t0)
	signed.DecidedBy, signed.DecidedByName, signed.SignedOffBy, signed.SignedOffByName, signed.SignedOffAt =
		"sub-alice", "Alice", "sub-alice", "Alice", &metav1.Time{Time: t0}
	prev := []aiv1.PlanDecision{signed, storedDecision(turnDecision("D2"), 1, t0), storedDecision(turnDecision("D3"), 3, t0)}
	change := func(f func(*planTurnDecision)) []planTurnDecision {
		d := turnDecision("D1")
		f(&d)
		return []planTurnDecision{d}
	}
	for name, tc := range map[string]struct {
		in      []planTurnDecision
		version int64
		agent   bool // a new version by the agent, not signed off
		seq     int32
	}{
		"unchanged":    {in: []planTurnDecision{turnDecision("D1")}, version: 2, seq: 3},
		"why only":     {in: change(func(d *planTurnDecision) { d.Why = "Better reason" }), version: 2, seq: 3},
		"padding only": {in: change(func(d *planTurnDecision) { d.Title, d.Options = " "+d.Title+"\n", []string{" Rail", "Footer "} }), version: 2, seq: 3},
		"title":        {in: change(func(d *planTurnDecision) { d.Title = "Placement" }), version: 3, agent: true, seq: 3},
		"question":     {in: change(func(d *planTurnDecision) { d.Question = "Where?" }), version: 3, agent: true, seq: 3},
		"options":      {in: change(func(d *planTurnDecision) { d.Options = []string{"Rail", "Footer", "Header"} }), version: 3, agent: true, seq: 3},
		"recommended":  {in: change(func(d *planTurnDecision) { d.Recommended = 0 }), version: 3, agent: true, seq: 3},
		"state":        {in: change(func(d *planTurnDecision) { d.State = "open" }), version: 3, agent: true, seq: 3},
		"new decision": {in: change(func(d *planTurnDecision) { d.ID = "D7" }), version: 1, agent: true, seq: 7},
	} {
		in := tc.in
		got, seq := mergePlanDecisions(prev, 3, in, nil, "", "", t1)
		if len(got) != 1 || seq != tc.seq {
			t.Fatalf("%s: %d decisions, seq %d, want 1 and %d", name, len(got), seq, tc.seq)
		}
		d := got[0]
		if d.Version != tc.version || d.Why != strings.TrimSpace(in[0].Why) || d.Title != strings.TrimSpace(in[0].Title) {
			t.Errorf("%s: version %d why %q title %q", name, d.Version, d.Why, d.Title)
		}
		if tc.agent {
			if d.DecidedBy != "agent" || d.DecidedByName != "" || !d.DecidedAt.Time.Equal(t1) || d.SignedOffBy != "" || d.SignedOffByName != "" || d.SignedOffAt != nil {
				t.Errorf("%s: a new version keeps who-fields: %+v", name, d)
			}
		} else if d.DecidedBy != "sub-alice" || d.DecidedByName != "Alice" || !d.DecidedAt.Time.Equal(t0) ||
			d.SignedOffBy != "sub-alice" || d.SignedOffAt == nil || !d.SignedOffAt.Time.Equal(t0) {
			t.Errorf("%s: an unchanged version lost its who-fields: %+v", name, d)
		}
	}
	// The agent's order wins; left-out entries are dropped; prev is not changed.
	got, seq := mergePlanDecisions(prev, 3, []planTurnDecision{turnDecision("D3"), turnDecision("D1"), turnDecision("D4")}, nil, "", "", t1)
	if ids := strings.Join(planDecisionIDs(got), ","); ids != "D3,D1,D4" || seq != 4 || got[0].Version != 3 || got[2].Version != 1 {
		t.Fatalf("merged %s seq %d: %+v", ids, seq, got)
	}
	got[1].Options[0] = "changed"
	if prev[0].Options[0] != "Rail" {
		t.Fatal("the merge shares the previous list's options")
	}
}

func keepAnswer(id string, v int64) aiv1.PlanDecisionAnswer {
	return aiv1.PlanDecisionAnswer{ID: id, Version: v, Keep: true}
}

func optionAnswer(id string, v int64, o int32) aiv1.PlanDecisionAnswer {
	return aiv1.PlanDecisionAnswer{ID: id, Version: v, Option: &o}
}

// The who-fields of a turn's list follow the answered message (R3,
// Decision 5): who decided each version and who signed it off.
func TestPlanDecisionsWho(t *testing.T) {
	t0, t1 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	byX := storedDecision(turnDecision("D1"), 2, t0)
	signOff(&byX, "sub-x", "Xavier", t0)
	open := turnDecision("D1")
	open.State = "open"
	moved := turnDecision("D1") // the agent applies a change: another recommendation
	moved.Recommended = 0
	locked := storedDecision(turnDecision("D1"), 2, t0)
	type who struct {
		version           int64
		decided, signed   string // "" for not signed off
		decidedAt, signAt time.Time
	}
	for name, tc := range map[string]struct {
		prev    aiv1.PlanDecision
		out     []planTurnDecision // nil: the turn leaves the list unchanged
		answers []aiv1.PlanDecisionAnswer
		author  string
		failed  bool // an interrupted or failed turn applies no answers
		want    who
	}{
		"new version without answers": {prev: locked, out: []planTurnDecision{moved},
			want: who{version: 3, decided: "agent", decidedAt: t1}},
		"applied change by X": {prev: locked, out: []planTurnDecision{moved}, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 2, 0)}, author: "sub-x",
			want: who{version: 3, decided: "sub-x", signed: "sub-x", decidedAt: t1, signAt: t1}},
		"applied other answer by X": {prev: locked, out: []planTurnDecision{moved},
			answers: []aiv1.PlanDecisionAnswer{{ID: "D1", Version: 2, Other: "Neither"}}, author: "sub-x",
			want: who{version: 3, decided: "sub-x", signed: "sub-x", decidedAt: t1, signAt: t1}},
		"keep on the current version": {prev: locked, out: []planTurnDecision{turnDecision("D1")}, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, author: "sub-x",
			want: who{version: 2, decided: "agent", signed: "sub-x", decidedAt: t0, signAt: t1}},
		"keep, list unchanged": {prev: locked, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, author: "sub-x",
			want: who{version: 2, decided: "agent", signed: "sub-x", decidedAt: t0, signAt: t1}},
		"recommended option is a keep": {prev: locked, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 2, 1)}, author: "sub-x",
			want: who{version: 2, decided: "agent", signed: "sub-x", decidedAt: t0, signAt: t1}},
		"later keep by Y replaces X": {prev: byX, out: []planTurnDecision{turnDecision("D1")}, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, author: "sub-y",
			want: who{version: 2, decided: "agent", signed: "sub-y", decidedAt: t0, signAt: t1}},
		"later keep by Y, list unchanged": {prev: byX, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, author: "sub-y",
			want: who{version: 2, decided: "agent", signed: "sub-y", decidedAt: t0, signAt: t1}},
		"unapplied change": {prev: locked, out: []planTurnDecision{turnDecision("D1")}, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 2, 0)}, author: "sub-x",
			want: who{version: 2, decided: "agent", decidedAt: t0}},
		"unapplied change, list unchanged": {prev: byX, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 2, 0)}, author: "sub-y",
			want: who{version: 2, decided: "agent", signed: "sub-x", decidedAt: t0, signAt: t0}},
		"keep on a stale version": {prev: locked, out: []planTurnDecision{turnDecision("D1")}, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 1)}, author: "sub-x",
			want: who{version: 2, decided: "agent", decidedAt: t0}},
		"keep on a stale version, list unchanged": {prev: locked, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 1)}, author: "sub-x",
			want: who{version: 2, decided: "agent", decidedAt: t0}},
		"stale change applied": {prev: locked, out: []planTurnDecision{moved}, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 1, 0)}, author: "sub-x",
			want: who{version: 3, decided: "agent", decidedAt: t1}},
		"keep, the agent changes it anyway": {prev: locked, out: []planTurnDecision{moved}, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, author: "sub-x",
			want: who{version: 3, decided: "agent", decidedAt: t1}},
		"keep on an open entry the agent locks": {prev: storedDecision(open, 1, t0), out: []planTurnDecision{turnDecision("D1")},
			answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 1)}, author: "sub-x",
			want: who{version: 2, decided: "sub-x", signed: "sub-x", decidedAt: t1, signAt: t1}},
		"keep on an open entry left open": {prev: storedDecision(open, 1, t0), answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 1)}, author: "sub-x",
			want: who{version: 1, decided: "agent", decidedAt: t0}},
		"interrupted turn": {prev: byX, out: []planTurnDecision{moved}, answers: []aiv1.PlanDecisionAnswer{optionAnswer("D1", 2, 0)}, author: "sub-y", failed: true,
			want: who{version: 2, decided: "agent", signed: "sub-x", decidedAt: t0, signAt: t0}},
		"no author": {prev: locked, answers: []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)},
			want: who{version: 2, decided: "agent", decidedAt: t0}},
	} {
		prev := []aiv1.PlanDecision{tc.prev}
		var got []aiv1.PlanDecision
		names := map[string]string{"sub-x": "Xavier", "sub-y": "Yvonne"}
		switch {
		case tc.failed: // as PlanRun.turn: no list and no answers (see TestPlanFailedTurnSignsOffNothing)
			got = signOffKept(prev, nil, tc.author, names[tc.author], t1)
		case tc.out == nil:
			got = signOffKept(prev, tc.answers, tc.author, names[tc.author], t1)
		default:
			got, _ = mergePlanDecisions(prev, 1, tc.out, tc.answers, tc.author, names[tc.author], t1)
		}
		d := got[0]
		var signAt time.Time
		if d.SignedOffAt != nil {
			signAt = d.SignedOffAt.Time
		}
		w := tc.want
		decidedName := map[string]string{"agent": ""}[w.decided]
		if w.decided != "agent" {
			decidedName = names[w.decided]
		}
		if d.Version != w.version || d.DecidedBy != w.decided || d.DecidedByName != decidedName || !d.DecidedAt.Time.Equal(w.decidedAt) ||
			d.SignedOffBy != w.signed || d.SignedOffByName != names[w.signed] || (d.SignedOffAt != nil) != (w.signed != "") || !signAt.Equal(w.signAt) {
			t.Errorf("%s: got version %d decided %s/%q at %v, signed %s/%q at %v; want %+v", name,
				d.Version, d.DecidedBy, d.DecidedByName, d.DecidedAt.Time, d.SignedOffBy, d.SignedOffByName, signAt, w)
		}
	}
	// A sign-off does not write into the list it was given.
	prev := []aiv1.PlanDecision{storedDecision(turnDecision("D1"), 2, t0)}
	if signOffKept(prev, []aiv1.PlanDecisionAnswer{keepAnswer("D1", 2)}, "sub-x", "Xavier", t1); prev[0].SignedOffBy != "" {
		t.Fatal("signOffKept changed its input")
	}
}
