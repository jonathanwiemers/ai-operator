package rules

import (
	"os"
	"testing"
)

// planner is the Requirements section and the Suggested order of the
// milestone planner's description (13 requirements).
const planner = `## Requirements

### Groundwork (Go)

- **R1** ` + "`AgentPlan`" + ` CRD types, ` + "`make generate manifests`" + `, chart RBAC for
  the UI writer (Decision 1). No controller yet.
- **R2** Config: plan.idleTimeout, plan.retention (Decision 7).
- **R3** Migration: chat_messages.plan column; tests.

### Operator and runner (Go)

- **R4** Plan controller: pod lifecycle (Decisions 2, 6).
- **R5** Chat dispatch for plan targets onto the plan pod (Decision 3).
- **R6** Runner plan turn: resume session, budget cap (Decisions 2–4, 7).

### UI server (Go)

- **R7** Plan endpoints; view structs, ` + "`make ts-types`" + `.
- **R8** Create milestone from a plan (Decision 8), sharing code with
  createFollowUp; tests with a fake GitLab.
- **R9** Estimate endpoint (Decision 10) with store query and tests.

### Web

- **R10** Plans list page and entry points (Decision 11).
- **R11** Plan page: chat, draft editor, newer-draft diff (Decision 5).
- **R12** Create dialog with toggles and the estimate (Decisions 8, 10).

### Docs

- **R13** docs/ui.md (Planner); close #47 with a link.

## Constraints

- Under 50 production lines per MR; Go and web in separate MRs; R1–R3 first.

## Suggested order

R1, R2, R3 → R4 → R5 → R6 → R7 → R8, R9 → R10 → R11 → R12 → R13.
`

func TestCountRequirements(t *testing.T) {
	for _, c := range []struct {
		name, desc string
		want       int
	}{
		{"empty", "", 0},
		{"bold", "- **R1** One\n- **R2** Two", 2},
		{"plain", "- R1 One\n* R2 Two\n+ R3 Three", 3},
		{"colon", "R1: One\n  R2: Two", 2},
		{"numbered", "1. R1 One\n2) **R2** Two", 2},
		{"table", "| R | What |\n|---|---|\n| R1 One | a |\n| **R2** Two | b |", 2},
		{"duplicates", "- **R1** One\n- R1 again\n- R01 padded", 1},
		{"prose", "Like R1 before, see R3.\n- Depends on R2.\n- **R4** Real", 1},
		{"not an ID", "- Rx\n- R1a\n- Release\nRR1", 0},
		{"fenced", "- R1 One\n```\n- R2 code\n```\n~~~md\nR3: code\n~~~\n- R4 Four", 2},
		{"unclosed fence", "- R1 One\n```\n- R2 code", 1},
		{"planner", planner, 13},
	} {
		if got := CountRequirements(c.desc); got != c.want {
			t.Errorf("%s: CountRequirements = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestCountRequirementsSpec(t *testing.T) {
	b, err := os.ReadFile("../../docs/specs/desk-queue-closeout.spec.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := CountRequirements(string(b)); got != 8 {
		t.Errorf("desk-queue-closeout: CountRequirements = %d, want 8", got)
	}
}
