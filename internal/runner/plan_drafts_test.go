package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// twoDrafts is a plan with drafts 1 and 2, draft 2 by the owner.
func twoDrafts() aiv1.AgentPlanStatus {
	return aiv1.AgentPlanStatus{DraftSeq: 2, Drafts: []aiv1.PlanDraft{
		{Key: 1, Title: "One", Draft: "## Goal\n\nOne.", Revision: 2, By: "agent", SHA: "old"},
		{Key: 2, Title: "Two", Draft: "## Goal\n\nTwo.", Revision: 1, By: "owner"},
	}}
}

// One turn adds a draft and updates another; a draft left out or sent
// unchanged keeps its text and revision.
func TestApplyPlanDraftsAddUpdateUnchanged(t *testing.T) {
	s := twoDrafts()
	notes, refused := applyPlanDrafts(&s, map[int64]int64{1: 2, 2: 1}, []planTurnDraft{
		{Key: 1, Title: "  One, revised\nsecond line", Draft: "## Goal\n\nOne, revised.\n"},
		{Key: 0, Title: "Three", Draft: "## Goal\n\nThree."},
		{Key: 2, Title: "Ignored", Draft: "## Goal\n\nTwo."},
	}, "new")
	if len(notes) != 0 || !maps.Equal(refused, map[int64]bool{1: false, 3: false}) {
		t.Fatalf("notes %q, refused %v", notes, refused)
	}
	want := []aiv1.PlanDraft{
		{Key: 1, Title: "One, revised", Draft: "## Goal\n\nOne, revised.", Revision: 3, By: "agent", SHA: "new"},
		{Key: 2, Title: "Two", Draft: "## Goal\n\nTwo.", Revision: 1, By: "owner"},
		{Key: 3, Title: "Three", Draft: "## Goal\n\nThree.", Revision: 1, By: "agent", SHA: "new"},
	}
	if !reflect.DeepEqual(s.Drafts, want) || s.DraftSeq != 3 {
		t.Fatalf("drafts (seq %d):\n%+v\nwant\n%+v", s.DraftSeq, s.Drafts, want)
	}
	// An empty title keeps the draft's own.
	applyPlanDrafts(&s, map[int64]int64{3: 1}, []planTurnDraft{{Key: 3, Draft: "## Goal\n\nThree, again."}}, "new")
	if s.Drafts[2].Title != "Three" || s.Drafts[2].Revision != 2 {
		t.Fatalf("draft 3: %+v", s.Drafts[2])
	}
}

// The owner's newer revision of draft 1 is kept while draft 2 is applied.
func TestApplyPlanDraftsOwnerWinsPerDraft(t *testing.T) {
	s := twoDrafts()
	notes, refused := applyPlanDrafts(&s, map[int64]int64{1: 1, 2: 1}, []planTurnDraft{
		{Key: 1, Draft: "Agent one."}, {Key: 2, Draft: "Agent two."},
	}, "new")
	if !slices.Equal(notes, []string{fmt.Sprintf(planDraftKept, 1)}) || !maps.Equal(refused, map[int64]bool{1: true, 2: false}) {
		t.Fatalf("notes %q, refused %v", notes, refused)
	}
	if s.Drafts[0].Draft != "## Goal\n\nOne." || s.Drafts[0].Revision != 2 || s.Drafts[1].Draft != "Agent two." || s.Drafts[1].Revision != 2 {
		t.Fatalf("drafts: %+v", s.Drafts)
	}
}

func TestApplyPlanDraftsRefusals(t *testing.T) {
	full := aiv1.AgentPlanStatus{DraftSeq: 7}
	for k := int64(3); k <= 7; k++ {
		full.Drafts = append(full.Drafts, aiv1.PlanDraft{Key: k, Draft: "x", Revision: 1})
	}
	full.Drafts[0].CreatedMilestoneIID = 41
	big := strings.Repeat("x", maxPlanDraft+1)
	for name, tc := range map[string]struct {
		in   planTurnDraft
		note string
		key  int64
	}{
		"a sixth draft":          {planTurnDraft{Key: 0, Draft: "Six."}, planDraftSixth, 0},
		"an unknown key":         {planTurnDraft{Key: 2, Draft: "Removed."}, fmt.Sprintf(planDraftUnknown, 2), 2},
		"a created draft":        {planTurnDraft{Key: 3, Draft: "Changed."}, "(Draft 3 is created as %41 and read-only; your change was not applied.)", 3},
		"an over-64 KiB draft":   {planTurnDraft{Key: 4, Draft: big}, fmt.Sprintf(planDraftLarge, "draft 4"), 4},
		"a negative key":         {planTurnDraft{Key: -1, Draft: "Odd."}, fmt.Sprintf(planDraftUnknown, -1), -1},
		"an over-64 KiB new one": {planTurnDraft{Key: 0, Draft: big}, fmt.Sprintf(planDraftLarge, "new draft"), 0},
	} {
		s := *full.DeepCopy()
		if name == "an over-64 KiB new one" {
			s.Drafts = s.Drafts[:4]
		}
		before := slices.Clone(s.Drafts)
		notes, refused := applyPlanDrafts(&s, map[int64]int64{3: 1, 4: 1, 5: 1, 6: 1, 7: 1}, []planTurnDraft{tc.in}, "new")
		if !slices.Equal(notes, []string{tc.note}) || !maps.Equal(refused, map[int64]bool{tc.key: true}) || !allPlanDraftsRefused(refused) {
			t.Errorf("%s: notes %q, refused %v", name, notes, refused)
		}
		if !reflect.DeepEqual(s.Drafts, before) || s.DraftSeq != 7 {
			t.Errorf("%s: drafts changed: %+v", name, s.Drafts)
		}
	}
}

// A key sent twice: its first change is applied, the second named.
func TestApplyPlanDraftsKeyTwice(t *testing.T) {
	s := twoDrafts()
	notes, refused := applyPlanDrafts(&s, map[int64]int64{1: 2, 2: 1}, []planTurnDraft{
		{Key: 1, Draft: "First."}, {Key: 1, Draft: "Second."}, {Key: 1, Draft: "First."},
	}, "new")
	if !slices.Equal(notes, []string{fmt.Sprintf(planDraftTwice, 1)}) || !maps.Equal(refused, map[int64]bool{1: false}) ||
		s.Drafts[0].Draft != "First." || s.Drafts[0].Revision != 3 {
		t.Fatalf("notes %q, refused %v, draft %+v", notes, refused, s.Drafts[0])
	}
}

// A plan from before status.drafts: its single draft becomes key 1 and the
// change applies to it.
func TestApplyPlanDraftsMovesOldDraft(t *testing.T) {
	s := aiv1.AgentPlanStatus{Title: "Old", Draft: "Old words.", DraftRevision: 3, DraftBy: "owner"}
	notes, refused := applyPlanDrafts(&s, map[int64]int64{1: 3}, []planTurnDraft{{Key: 1, Draft: "New words."}}, "new")
	if len(notes) != 0 || !maps.Equal(refused, map[int64]bool{1: false}) || s.Draft != "" || s.DraftSeq != 1 ||
		!reflect.DeepEqual(s.Drafts, []aiv1.PlanDraft{{Key: 1, Title: "Old", Draft: "New words.", Revision: 4, By: "agent", SHA: "new"}}) {
		t.Fatalf("notes %q, refused %v, status %+v", notes, refused, s)
	}
	// Even without a change, the move happens.
	s = aiv1.AgentPlanStatus{Draft: "Old words.", DraftRevision: 3}
	if _, refused := applyPlanDrafts(&s, nil, nil, "new"); len(refused) != 0 || len(s.Drafts) != 1 || s.Drafts[0].Key != 1 || s.Draft != "" {
		t.Fatalf("not moved: %+v", s)
	}
}

func TestAllPlanDraftsRefused(t *testing.T) {
	for _, tc := range []struct {
		refused map[int64]bool
		want    bool
	}{
		{nil, false},
		{map[int64]bool{1: true}, true},
		{map[int64]bool{1: true, 0: true}, true},
		{map[int64]bool{1: true, 2: false}, false},
	} {
		if got := allPlanDraftsRefused(tc.refused); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.refused, got, tc.want)
		}
	}
}

// planTurnWith answers a plan turn with drafts and a decisions list in JSON.
func planTurnWith(reply string, drafts []planTurnDraft, decisions string) func(Invocation) (*Result, error) {
	return func(Invocation) (*Result, error) {
		b, _ := json.Marshal(PlanTurn{Reply: reply, Drafts: drafts})
		b = []byte(strings.Replace(string(b), `"decisions":null`, `"decisions":`+decisions, 1))
		return &Result{SessionID: "s", CostUSD: 0.1, Structured: b}, nil
	}
}

// In one turn the owner's save of draft 1 wins, draft 2 is applied, the
// reply names draft 1 and the decisions follow the applied draft.
func TestPlanTurnSeveralDrafts(t *testing.T) {
	d1 := decisionsJSON(decisionJSON("D1", "Placement", "locked", 0, "Rail", "Footer"))
	e := newPlanEnv(t, nil, planTurnWith("All kept.", []planTurnDraft{{Key: 1, Draft: "Again."}}, d1))
	p := e.plan(t)
	p.Status.Drafts, p.Status.DraftSeq = twoDrafts().Drafts, 2
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	e.engine.answers[0] = func(inv Invocation) (*Result, error) {
		e.ownerSave(t, "## Goal\n\nOwner's one.") // draft 1, while the turn runs
		return planTurnWith("Both.", []planTurnDraft{{Key: 1, Draft: "Agent one."}, {Key: 2, Title: "Two", Draft: "Agent two."}}, d1)(inv)
	}
	p = e.turn(t, 10, "rework both")
	if d := p.Status.Drafts; len(d) != 2 || d[0].Draft != "## Goal\n\nOwner's one." || d[0].By != "owner" || d[1].Draft != "Agent two." || d[1].Revision != 2 {
		t.Fatalf("drafts: %+v", d)
	}
	if rep := lastReply(t, p, 10); rep.Text != "Both.\n\n"+fmt.Sprintf(planDraftKept, 1) {
		t.Fatalf("reply: %q", rep.Text)
	}
	if len(p.Status.Decisions) != 1 {
		t.Fatalf("the decisions follow the applied draft 2: %+v", p.Status.Decisions)
	}
	// Every changed draft refused: the list is not applied.
	e.engine.answers[1] = planTurnWith("All kept.", []planTurnDraft{{Key: 1, Draft: "Again."}}, decisionsJSON())
	p = e.plan(t)
	p.Status.Drafts[0].CreatedMilestoneIID = 41
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p = e.turn(t, 11, "drop them")
	if len(p.Status.Decisions) != 1 || lastReply(t, p, 11).Text != "All kept.\n\n(Draft 1 is created as %41 and read-only; your change was not applied.)" {
		t.Fatalf("reply %q, decisions %+v", lastReply(t, p, 11).Text, p.Status.Decisions)
	}
}

// firstPlanDraft is the plan's first draft; zero without one.
func firstPlanDraft(plan *aiv1.AgentPlan) aiv1.PlanDraft {
	if list := plan.DraftList(); len(list) > 0 {
		return list[0]
	}
	return aiv1.PlanDraft{}
}

// planEditedRelay is the prompt's lines naming draft key as edited by the owner.
func planEditedRelay(key int64) string {
	return fmt.Sprintf("The owner edited these drafts since you last saw them. Read their files before you answer, "+
		"build on their edits and keep their changes:\n\n- draft %d: .plan-context/drafts/%d.md\n", key, key)
}

// A turn writes every draft to .plan-context/drafts/<key>.md in the agent's
// checkout.
func TestPlanTurnWritesDraftFiles(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "Read them.", ""))
	p := e.plan(t)
	p.Status.Drafts, p.Status.DraftSeq = twoDrafts().Drafts, 2
	p.Status.Drafts[0].CreatedMilestoneIID = 41
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	e.turn(t, 10, "go on")
	repo := filepath.Join(filepath.Dir(e.run.StateDir), "agent", "repo")
	for key, want := range map[int64]string{
		1: "> Draft 1 \"One\": created as %41 (read-only). Its text starts below this line.\n\n## Goal\n\nOne.\n",
		2: "> Draft 2 \"Two\": not created. Its text starts below this line.\n\n## Goal\n\nTwo.\n",
	} {
		b, err := os.ReadFile(filepath.Join(repo, planDraftPath(key)))
		if err != nil || string(b) != want {
			t.Errorf("draft %d: %q, %v", key, b, err)
		}
	}
	for _, want := range []string{"- draft 1 \"One\": created as %41 (read-only), .plan-context/drafts/1.md\n",
		"- draft 2 \"Two\": not created, .plan-context/drafts/2.md\n", planEditedRelay(2)} {
		if !strings.Contains(e.engine.calls[0].Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, e.engine.calls[0].Prompt)
		}
	}
	if out := gitCmd(t, repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("git status shows: %s", out)
	}
}

// A state from before several drafts: its PlanSeenRevision is draft 1's, so
// the owner's edit is not sent again; the turn writes PlanSeen.
func TestPlanSeenFromOldRevision(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "Ok.", ""), planAnswer("s", "Ok.", ""))
	p := e.plan(t)
	p.Status.Draft, p.Status.DraftRevision, p.Status.DraftBy = "## Goal\n\nOld words.", 3, "owner"
	if err := e.k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := e.run.saveState(state{PlanSeenRevision: 3}); err != nil {
		t.Fatal(err)
	}
	e.turn(t, 10, "go on")
	if strings.Contains(e.engine.calls[0].Prompt, "The owner edited") {
		t.Fatalf("the seen edit is sent again:\n%s", e.engine.calls[0].Prompt)
	}
	if st := savedState(t, e.run.Runner); st.PlanSeenRevision != 0 || !maps.Equal(st.PlanSeen, map[int64]int64{1: 3}) {
		t.Fatalf("state: seen %v, old %d", st.PlanSeen, st.PlanSeenRevision)
	}
	// A newer owner revision of draft 1 is sent once.
	e.ownerSave(t, "## Goal\n\nNewer words.")
	e.turn(t, 11, "and now")
	if !strings.Contains(e.engine.calls[1].Prompt, planEditedRelay(1)) {
		t.Fatalf("the newer edit is not sent:\n%s", e.engine.calls[1].Prompt)
	}
	if st := savedState(t, e.run.Runner); !maps.Equal(st.PlanSeen, map[int64]int64{1: 4}) {
		t.Fatalf("seen %v", st.PlanSeen)
	}
}
