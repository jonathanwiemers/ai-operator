package chatcmd

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		body    string
		want    Command
		wantErr string
	}{
		{`{"cmd":"retry"}`, Command{Cmd: "retry"}, ""},
		{`{"cmd":"review"}`, Command{Cmd: "review"}, ""},
		{`{"cmd":"pause"}`, Command{Cmd: "pause"}, ""},
		{`{"cmd":"resume"}`, Command{Cmd: "resume"}, ""},
		{`{"cmd":"use","engine":"claude","model":"opus","effort":"high"}`,
			Command{Cmd: "use", Engine: "claude", Model: "opus", Effort: "high"}, ""},
		{`{"cmd":"use","engine":"Claude","model":"opus"}`,
			Command{Cmd: "use", Engine: "claude", Model: "opus"}, ""},
		{`{"cmd":"use","default":true}`, Command{Cmd: "use", Default: true}, ""},
		{`{"cmd":"effort","effort":"high"}`, Command{Cmd: "effort", Effort: "high"}, ""},
		{`{"cmd":"effort","effort":"default"}`, Command{Cmd: "effort", Effort: "default"}, ""},
		{`{"cmd":"skip","issue":12}`, Command{Cmd: "skip", Issue: 12}, ""},
		{`{"cmd":"merge","issue":12,"sha":" 5A0E000 "}`, Command{Cmd: "merge", Issue: 12, SHA: "5a0e000"}, ""},
		{`{"cmd":"accept","proposal":3}`, Command{Cmd: "accept", Proposal: 3}, ""},
		{`{"cmd":"reject","proposal":3}`, Command{Cmd: "reject", Proposal: 3}, ""},
		{`{}`, Command{}, "cmd is required"},
		{`{"cmd":""}`, Command{}, "cmd is required"},
		{`{"cmd":"retry","extra":1}`, Command{}, "invalid command"},
		{`{"cmd":"nope"}`, Command{}, `unknown cmd "nope"`},
		{`{"cmd":"retry","engine":"claude"}`, Command{}, "retry takes no arguments"},
		{`{"cmd":"use"}`, Command{}, "use needs engine"},
		{`{"cmd":"use","engine":"gpt","model":"x"}`, Command{}, "use needs engine"},
		{`{"cmd":"use","engine":"claude"}`, Command{}, "use needs a model"},
		{"{\"cmd\":\"use\",\"engine\":\"claude\",\"model\":\"a b\"}", Command{}, "not a plain model name"},
		{"{\"cmd\":\"use\",\"engine\":\"claude\",\"model\":\"x`y\"}", Command{}, "not a plain model name"},
		{`{"cmd":"use","engine":"claude","model":"opus","effort":"nope"}`, Command{}, "not valid"},
		{`{"cmd":"use","default":true,"model":"x"}`, Command{}, "use default takes no other"},
		{`{"cmd":"effort"}`, Command{}, "effort needs an effort"},
		{`{"cmd":"effort","effort":"nope"}`, Command{}, "not valid"},
		{`{"cmd":"skip"}`, Command{}, "skip needs issue"},
		{`{"cmd":"skip","issue":0}`, Command{}, "skip needs issue"},
		{`{"cmd":"skip","issue":1,"engine":"claude"}`, Command{}, "skip takes only issue"},
		{`{"cmd":"merge","issue":12}`, Command{}, "merge needs issue > 0 and the sha"},
		{`{"cmd":"merge","issue":12,"sha":"5a0e"}`, Command{}, "merge needs issue > 0 and the sha"},
		{`{"cmd":"merge","issue":12,"sha":"5a0e000","effort":"high"}`, Command{}, "merge takes only issue and sha"},
		{`{"cmd":"skip","issue":12,"sha":"5a0e000"}`, Command{}, "skip takes no sha"},
		{`{"cmd":"accept"}`, Command{}, "accept needs proposal"},
		{`{"cmd":"accept","proposal":0}`, Command{}, "accept needs proposal"},
		{`{"cmd":"reject","proposal":1,"issue":2}`, Command{}, "reject takes only proposal"},
		{`{"cmd":"retry"}{"cmd":"review"}`, Command{}, "trailing data"},
	} {
		got, err := Parse(tc.body)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want it to mention %q", tc.body, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.body, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.body, got, tc.want)
		}
	}
}

func TestImplementOnlyAndRouting(t *testing.T) {
	for _, cmd := range []string{"retry", "review", "use", "effort"} {
		c := Command{Cmd: cmd}
		if cmd == "use" {
			c.Engine, c.Model = "claude", "opus"
		}
		if cmd == "effort" {
			c.Effort = "high"
		}
		if !c.ImplementOnly() {
			t.Errorf("%s should be ImplementOnly", cmd)
		}
		if c.Autopilot() || c.ProposalOnly() {
			t.Errorf("%s should not be Autopilot or ProposalOnly", cmd)
		}
	}
	for _, cmd := range []string{"pause", "resume", "skip", "merge"} {
		c := Command{Cmd: cmd}
		if !c.Autopilot() || c.ImplementOnly() || c.ProposalOnly() {
			t.Errorf("%s should be Autopilot only", cmd)
		}
	}
	for _, cmd := range []string{"accept", "reject"} {
		c := Command{Cmd: cmd}
		if !c.ProposalOnly() || c.ImplementOnly() || c.Autopilot() {
			t.Errorf("%s should be ProposalOnly only", cmd)
		}
	}
	for _, cmd := range []string{"pause", "resume", "skip", "accept", "reject"} {
		if (Command{Cmd: cmd}).ImplementOnly() {
			t.Errorf("%s should not be ImplementOnly", cmd)
		}
	}

	r, ok := (Command{Cmd: "use", Default: true}).Routing()
	if !ok || r.Verb != "use" || !slices.Equal(r.Args, []string{"default"}) || !r.Only {
		t.Fatalf("use default: %+v %v", r, ok)
	}
	r, ok = (Command{Cmd: "use", Engine: "claude", Model: "opus", Effort: "high"}).Routing()
	if !ok || !slices.Equal(r.Args, []string{"claude", "opus", "high"}) {
		t.Fatalf("use model: %+v %v", r, ok)
	}
	r, ok = (Command{Cmd: "effort", Effort: "default"}).Routing()
	if !ok || r.Verb != "effort" || !slices.Equal(r.Args, []string{"default"}) || !r.Only {
		t.Fatalf("effort: %+v %v", r, ok)
	}
	if _, ok := (Command{Cmd: "retry"}).Routing(); ok {
		t.Fatal("retry is not routing")
	}
}
