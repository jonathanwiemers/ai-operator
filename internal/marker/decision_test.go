package marker

import (
	"reflect"
	"testing"
)

func TestParseDecisionPlannerFromDescription(t *testing.T) {
	body := "Intro text.\n\n" + DecisionsHeader + "\n\n#### D7-P1: Use cents\n**Question:** How are prices stored?\nmore question\n**Chosen:** Integer cents\n**Why:** Exact.\nNo floats.\n**Alternatives:** Floats; Decimal strings; \n_Supersedes D3-P2._\n<!-- ai-operator:decision=D7-P1 -->\n\nTail."
	got, ok := ParseDecision(DecisionSection(body, "D7-P1"))
	want := ParsedDecision{ID: "D7-P1", Title: "Use cents", Question: "How are prices stored?\nmore question", Choice: "Integer cents",
		Why: "Exact.\nNo floats.", Alternatives: []string{"Floats", "Decimal strings"}, Supersedes: "D3-P2"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (%v), want %+v", got, ok, want)
	}
}

func TestParseDecisionMinimal(t *testing.T) {
	got, ok := ParseDecision("#### D1-1: T\n**Chosen:** A\n<!-- ai-operator:decision=D1-1 -->")
	if !ok || got.Choice != "A" || got.Question != "" || got.Why != "" || len(got.Alternatives) != 0 || got.Superseded {
		t.Fatalf("got %+v (%v)", got, ok)
	}
}

func TestParseDecisionSuperseded(t *testing.T) {
	got, _ := ParseDecision("#### D1-1: T\n**Chosen:** A\n**Why:** w\n> ⚠️ Superseded by D4-2 in run `x#1`\n<!-- ai-operator:decision=D1-1 superseded -->")
	if !got.Superseded || got.SupersededBy != "D4-2" || got.Why != "w" {
		t.Fatalf("got %+v", got)
	}
	got, _ = ParseDecision("#### D1-1: T\n**Chosen:** A\n> ⚠️ Revisited in run `x#1`\n<!-- ai-operator:decision=D1-1 superseded -->")
	if !got.Superseded || got.SupersededBy != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDecisionRejects(t *testing.T) {
	for name, s := range map[string]string{
		"no marker":     "#### D1-1: T\n**Chosen:** A\n",
		"no heading":    "**Chosen:** A\n<!-- ai-operator:decision=D1-1 -->",
		"other marker":  "#### D1-1: T\n**Chosen:** A\n<!-- ai-operator:decision=D1-2 -->",
		"empty section": "",
	} {
		if _, ok := ParseDecision(s); ok {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseDecisionsOverComment(t *testing.T) {
	body := DecisionsHeader + "\n\n#### D5-1: A\n**Chosen:** a\n<!-- ai-operator:decision=D5-1 -->\n\n#### D5-2: B\n**Chosen:** b\n**Alternatives:** x; y\n<!-- ai-operator:decision=D5-2 -->\n\n#### D5-3: C\n**Chosen:** c\n_Supersedes D5-1._\n<!-- ai-operator:decision=D5-3 -->\n"
	got := ParseDecisions(body)
	if len(got) != 3 || got[0].ID != "D5-1" || got[1].Alternatives[1] != "y" || got[2].Supersedes != "D5-1" {
		t.Fatalf("got %+v", got)
	}
}
