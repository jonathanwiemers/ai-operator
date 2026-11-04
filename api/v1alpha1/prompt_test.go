package v1alpha1

import (
	"strings"
	"testing"
)

func promptOf(kind PromptKind, opts ...PromptOption) Prompt {
	for i := range opts {
		opts[i].ID = string(rune('A' + i))
		if opts[i].Label == "" {
			opts[i].Label = "option " + opts[i].ID
		}
	}
	return Prompt{ID: "Q1", Source: PromptSource{Kind: kind}, Question: "What now?", Options: opts}
}

var (
	act  = PromptOption{Actions: []PilotAction{{Tool: "retry", IssueIID: 7}}}
	rec  = PromptOption{Actions: []PilotAction{{Tool: "fixFlaky", IssueIID: 7}}, Recommended: true}
	none = PromptOption{}
	from = PromptOption{Actions: []PilotAction{{Tool: "instruct", IssueIID: 7}}, FromAnswer: true}
)

func TestPromptValidate(t *testing.T) {
	with := func(p Prompt, f func(*Prompt)) Prompt { f(&p); return p }
	long := func(n int) string { return strings.Repeat("x", n+1) }
	for name, tc := range map[string]struct {
		p    Prompt
		want string // empty: valid
	}{
		"proposal":            {promptOf(PromptProposal, rec, none), ""},
		"four options":        {promptOf(PromptMainHealth, rec, act, act, none), ""},
		"four and from":       {promptOf(PromptMainHealth, rec, act, act, none, from), ""},
		"no actions at all":   {promptOf(PromptRevisit, PromptOption{Recommended: true}, none), ""},
		"needsInput options":  {promptOf(PromptNeedsInput, PromptOption{Recommended: true}, none), ""},
		"needsInput freetext": {with(promptOf(PromptNeedsInput), func(p *Prompt) { p.AllowFreeText = true }), ""},
		"answered": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) {
			p.State, p.Answer = PromptAnswered, &PromptAnswer{OptionID: "A", By: "jo", Via: "desk"}
		}), ""},

		"no recommended":    {promptOf(PromptProposal, act, none), "0 recommended"},
		"two recommended":   {promptOf(PromptProposal, rec, PromptOption{Recommended: true}), "2 recommended"},
		"five options":      {promptOf(PromptMainHealth, rec, act, act, act, none), "5 options"},
		"one option":        {promptOf(PromptBudget, PromptOption{Recommended: true}), "1 options"},
		"no options":        {promptOf(PromptAssessment), "0 options"},
		"two from answer":   {promptOf(PromptProposal, rec, none, from, from), "follows the option"},
		"option after from": {promptOf(PromptProposal, rec, from, none), "follows the option"},
		"from no actions":   {promptOf(PromptProposal, rec, none, PromptOption{FromAnswer: true}), "has no actions"},
		"no do nothing":     {promptOf(PromptProposal, rec, act), `no "do nothing"`},
		"needsInput bare":   {promptOf(PromptNeedsInput), "needs free text"},
		"needsInput one":    {promptOf(PromptNeedsInput, PromptOption{Recommended: true}), "1 options"},
		"empty tool":        {promptOf(PromptProposal, PromptOption{Actions: []PilotAction{{}}, Recommended: true}, none), "without a tool"},
		"unknown kind":      {promptOf("chat", rec, none), "kind"},
		"unknown state":     {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.State = "done" }), "state"},
		"no question":       {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Question = "" }), "no question"},
		"no label":          {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Options[1].Label = "" }), "no label"},
		"answer to nothing": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Answer = &PromptAnswer{OptionID: "C"} }), `option "C"`},

		"id empty":     {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.ID = "" }), "id"},
		"id P":         {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.ID = "P1" }), "id"},
		"id zero":      {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.ID = "Q0" }), "id"},
		"id leading 0": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.ID = "Q01" }), "id"},
		"option id":    {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Options[1].ID = "C" }), `id "C", want B`},
		"option id lc": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Options[0].ID = "a" }), `want A`},

		"long question": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Question = long(MaxPromptQuestion) }), "question is longer"},
		"long context":  {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Context = long(MaxPromptContext) }), "context is longer"},
		"long label":    {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Options[0].Label = long(MaxPromptOptionLabel) }), "label is longer"},
		"long detail":   {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Options[1].Detail = long(MaxPromptOptionDetail) }), "detail is longer"},
		"long text":     {with(promptOf(PromptProposal, rec, none), func(p *Prompt) { p.Answer = &PromptAnswer{Text: long(MaxPromptText)} }), "text is longer"},
		"long outcome": {with(promptOf(PromptProposal, rec, none), func(p *Prompt) {
			p.Answer = &PromptAnswer{OptionID: "A", Outcome: long(MaxPromptText)}
		}), "outcome is longer"},
	} {
		err := tc.p.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one with %q", name, err, tc.want)
		}
	}
	// The bounds themselves are allowed.
	p := promptOf(PromptProposal, rec, none)
	p.Question, p.Context = strings.Repeat("x", MaxPromptQuestion), strings.Repeat("x", MaxPromptContext)
	p.Options[0].Label, p.Options[0].Detail = strings.Repeat("x", MaxPromptOptionLabel), strings.Repeat("x", MaxPromptOptionDetail)
	p.Answer = &PromptAnswer{Text: strings.Repeat("x", MaxPromptText)}
	if err := p.Validate(); err != nil {
		t.Errorf("at the bounds: %v", err)
	}
}

func TestPromptOption(t *testing.T) {
	p := promptOf(PromptMainHealth, rec, act, none, from)
	for id, want := range map[string]int{"A": 0, "B": 1, "C": 2, "D": 3, "E": -1, "": -1, "a": -1} {
		got := p.Option(id)
		if want < 0 && got != nil || want >= 0 && got != &p.Options[want] {
			t.Errorf("Option(%q) = %+v, want option %d", id, got, want)
		}
	}
	if got := p.DoNothing(); got != &p.Options[2] {
		t.Errorf("DoNothing() = %+v, want C", got)
	}
	if got := promptOf(PromptProposal, rec, act).DoNothing(); got != nil {
		t.Errorf("DoNothing() without one = %+v", got)
	}
}

func TestPromptStateClosed(t *testing.T) {
	for s, want := range map[PromptState]bool{"": false, PromptOpen: false, PromptAnswered: true, PromptSuperseded: true, PromptExpired: true} {
		if s.Closed() != want {
			t.Errorf("%q.Closed() = %v", s, !want)
		}
	}
}
