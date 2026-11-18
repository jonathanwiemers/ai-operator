package controller

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const planDecisionsComment = "📌 **Decisions** of the plan in run `shop-spec-m3#1`. Change one with `/agent revisit <id> <what you want instead>`.\n\n" +
	"#### D2-P1: Layout\n**Question:** Which layout?\n**Chosen:** Grid\n**Why:** dense\n**Alternatives:** List; Cards\n<!-- ai-operator:decision=D2-P1 -->\n\n" +
	"#### D2-P2: Colors\n**Chosen:** Brand\n**Why:** consistent\n<!-- ai-operator:decision=D2-P2 -->\n\n<!-- ai-operator:plan-decisions -->"

// decisionsEnv is a planned milestone with the bot's issues #1 (closed, an
// implementer decision), #2 (open, the plan's decisions, and an old one in
// its description), #3 (a human's, with a decision-looking description) and
// the tracking issue #4. Every issue was last updated a year ago.
type decisionsEnv struct {
	*projectEnv
	recorded []store.IssueDecisions
}

func newDecisionsEnv(t *testing.T) *decisionsEnv {
	t.Helper()
	e := &decisionsEnv{projectEnv: newProjectEnv(t)}
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.r.recordDecisions = func(d store.IssueDecisions) { e.recorded = append(e.recorded, d) }
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\nBuild it."}
	e.gl.Milestones = []gitlab.Milestone{m}
	human := gitlab.User{ID: 1, Username: "jonathan"}
	issue := func(iid int64, title string, author gitlab.User, description string) *gitlab.Issue {
		is := e.gl.AddIssue(iid, title)
		mc := m
		is.Milestone, is.Author, is.Description = &mc, author, description
		return is
	}
	api := issue(1, "API", e.gl.Bot, "Build the API.")
	closedAt := e.gl.Clock
	api.State, api.ClosedAt = "closed", &closedAt
	e.gl.IssueNotes[1] = []gitlab.Note{{ID: 50, Author: e.gl.Bot, Body: decisionComment, CreatedAt: &closedAt}}
	issue(2, "UI", e.gl.Bot, "Build the UI.\n\n### Decisions\n\n#### D2-P1: Old layout\n**Chosen:** Table\n<!-- ai-operator:decision=D2-P1 -->\n\n"+
		"#### D2-P9: Fonts\n**Chosen:** System\n**Why:** fast\n<!-- ai-operator:decision=D2-P9 -->\n")
	e.gl.IssueNotes[2] = []gitlab.Note{{ID: 60, Author: e.gl.Bot, Body: planDecisionsComment, CreatedAt: &closedAt}}
	issue(3, "Docs", human, "#### D3-P1: Tone\n**Chosen:** Formal\n<!-- ai-operator:decision=D3-P1 -->\n")
	issue(4, "Autopilot: Checkout v2", e.gl.Bot, marker.TrackingIssue+"\n")
	e.gl.IssueNotes[4] = []gitlab.Note{{ID: 70, Author: e.gl.Bot, Body: decisionComment}}
	e.age(1, 2, 3, 4)
	return e
}

// age makes the issues last updated a year ago.
func (e *decisionsEnv) age(iids ...int64) {
	old := e.gl.Clock.AddDate(-1, 0, 0)
	for _, iid := range iids {
		e.gl.Issues[iid].UpdatedAt = &old
	}
}

// watermark is the spec task's status.decisions.checkedAt, zero when unset.
func (e *decisionsEnv) watermark() time.Time {
	e.t.Helper()
	ds := e.tasks()[specTaskName].Status.Decisions
	if ds == nil {
		return time.Time{}
	}
	return ds.CheckedAt.Time
}

// read returns the issues recorded since the last call, in order, and the
// newest record of each.
func (e *decisionsEnv) read() ([]int64, map[int64]store.IssueDecisions) {
	var iids []int64
	last := map[int64]store.IssueDecisions{}
	for _, d := range e.recorded {
		iids = append(iids, d.IssueIID)
		last[d.IssueIID] = d
	}
	e.recorded = nil
	return iids, last
}

func ids(d store.IssueDecisions) []string {
	var out []string
	for _, r := range d.Decisions {
		out = append(out, r.ID)
	}
	return out
}

// The first poll reads every issue once (the backfill) and sets the
// watermark; later polls read only what GitLab lists as updated since the
// watermark minus the clock skew.
func TestSyncDecisionsBackfillThenIncremental(t *testing.T) {
	e := newDecisionsEnv(t)
	e.reconcile()
	read, got := e.read()
	if !slices.Equal(read, []int64{1, 2, 3}) {
		t.Fatalf("the backfill read %v, want #1, #2 and #3 (not the tracking issue)", read)
	}
	w := e.watermark()
	if w.IsZero() {
		t.Fatal("the backfill set no watermark")
	}

	orbit := store.ProjectKey{Namespace: ns, Name: "shop"}
	posted := e.gl.IssueNotes[1][0].CreatedAt
	want := store.IssueDecisions{Project: orbit, MilestoneIID: 3, IssueIID: 1, Decisions: []store.DecisionRow{{
		MilestoneIID: 3, ID: "D1-1", IssueIID: 1, Kind: store.DecisionImplementer, Seq: 1, Title: "Protocol",
		Choice: "REST", Why: "simplest", RunID: "shop-1-api#1", URL: "https://gitlab.example/group/app/-/issues/1#note_50",
		IssueTitle: "API", IssueState: "closed", PostedAt: *posted}}}
	if !reflect.DeepEqual(got[1], want) {
		t.Errorf("#1 =\n%+v\nwant\n%+v", got[1], want)
	}
	// The comment's planner decisions, then the description's only where no
	// comment carries the ID.
	ui := got[2]
	if !slices.Equal(ids(ui), []string{"D2-P1", "D2-P2", "D2-P9"}) {
		t.Fatalf("#2 decisions = %v", ids(ui))
	}
	p1, p9 := ui.Decisions[0], ui.Decisions[2]
	if p1.Title != "Layout" || p1.Kind != store.DecisionPlanner || p1.Question != "Which layout?" ||
		!slices.Equal(p1.Alternatives, []string{"List", "Cards"}) || p1.RunID != "shop-spec-m3#1" ||
		p1.URL != "https://gitlab.example/group/app/-/issues/2#note_60" || p1.IssueState != "opened" {
		t.Errorf("D2-P1 = %+v", p1)
	}
	is2 := e.gl.Issues[2]
	if p9.Title != "Fonts" || p9.Kind != store.DecisionPlanner || p9.Seq != 9 || p9.RunID != "" ||
		p9.URL != is2.WebURL || !p9.PostedAt.Equal(*is2.CreatedAt) {
		t.Errorf("D2-P9 from the description = %+v", p9)
	}
	// A human's issue description carries no decisions.
	if d, ok := got[3]; !ok || len(d.Decisions) != 0 {
		t.Errorf("#3 = %+v, want an empty set", got[3])
	}

	// Nothing updated since: nothing read, the watermark stays.
	e.age(1, 2, 3, 4)
	e.gl.Clock = w.Add(time.Hour)
	e.reconcile()
	if read, _ := e.read(); len(read) != 0 {
		t.Errorf("read %v with nothing updated", read)
	}
	if !e.watermark().Equal(w) {
		t.Errorf("watermark %v, want %v kept", e.watermark(), w)
	}

	// Within the clock skew before the watermark: read; before it: not.
	in, out := w.Add(-4*time.Minute), w.Add(-6*time.Minute)
	e.gl.Issues[1].UpdatedAt, e.gl.Issues[2].UpdatedAt = &in, &out
	e.reconcile()
	if read, _ := e.read(); !slices.Equal(read, []int64{1}) {
		t.Errorf("read %v, want #1 only", read)
	}
	if w2 := e.watermark(); !w2.After(w) {
		t.Errorf("watermark %v did not move past %v", w2, w)
	}
}

// A re-plan edits the plan's decisions comment: the renumbered decisions
// replace the issue's old set. Decision-looking comments by humans are not
// decisions.
func TestSyncDecisionsReplanAndHumanComments(t *testing.T) {
	e := newDecisionsEnv(t)
	e.reconcile()
	e.read()

	e.gl.IssueNotes[2][0].Body = strings.NewReplacer("D2-P2", "D2-P3", "D2-P1: Layout", "D2-P1: Layout v2").Replace(planDecisionsComment)
	e.gl.AddNote(1, gitlab.User{ID: 1, Username: "jonathan"},
		"📌 **Decisions** in run `fake#1`.\n\n#### D1-9: Fake\n**Chosen:** mine\n<!-- ai-operator:decision=D1-9 -->\n")
	now := e.gl.Clock
	e.gl.Issues[2].UpdatedAt = &now
	e.reconcile()
	read, got := e.read()
	if !slices.Equal(read, []int64{1, 2}) {
		t.Fatalf("read %v, want the updated #1 and #2", read)
	}
	if ui := got[2]; !slices.Equal(ids(ui), []string{"D2-P1", "D2-P3", "D2-P9"}) || ui.Decisions[0].Title != "Layout v2" {
		t.Errorf("#2 after the re-plan = %v (%+v)", ids(ui), ui.Decisions)
	}
	if !slices.Equal(ids(got[1]), []string{"D1-1"}) {
		t.Errorf("#1 = %v, want the bot's D1-1 only", ids(got[1]))
	}
}

// An ID in two bot comments, e.g. an old plan comment and the re-posted
// one, is recorded once, with the newest comment's text.
func TestIssueDecisionsNewestCommentWins(t *testing.T) {
	bot := gitlab.User{ID: 999}
	old, newer := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	is := &gitlab.Issue{IID: 2, Title: "UI", State: "opened", Author: bot, WebURL: "https://gitlab.example/group/app/-/issues/2",
		Description: "#### D2-P1: From the description\n**Chosen:** Table\n<!-- ai-operator:decision=D2-P1 -->\n"}
	replanned := strings.NewReplacer("D2-P1: Layout", "D2-P1: Layout v2", "**Chosen:** Grid", "**Chosen:** Flex",
		"shop-spec-m3#1", "shop-spec-m3#2").Replace(planDecisionsComment)
	notes := []gitlab.Note{
		{ID: 60, Author: bot, Body: strings.Replace(planDecisionsComment, "decision=D2-P1 -->", "decision=D2-P1 superseded -->", 1), CreatedAt: &old},
		{ID: 61, Author: bot, Body: replanned, CreatedAt: &newer},
	}
	got := issueDecisions(store.ProjectKey{Namespace: ns, Name: "shop"}, 3, is, notes, bot.ID)
	if !slices.Equal(ids(got), []string{"D2-P1", "D2-P2"}) {
		t.Fatalf("decisions = %v, want D2-P1 and D2-P2 once each", ids(got))
	}
	p1 := got.Decisions[0]
	if p1.Title != "Layout v2" || p1.Choice != "Flex" || p1.Superseded || p1.RunID != "shop-spec-m3#2" ||
		p1.URL != is.WebURL+"#note_61" || !p1.PostedAt.Equal(newer) {
		t.Errorf("D2-P1 = %+v, want the newest comment's", p1)
	}
	if p2 := got.Decisions[1]; p2.URL != is.WebURL+"#note_61" {
		t.Errorf("D2-P2 from %s, want the newest comment", p2.URL)
	}
}

// A failed read returns the error and keeps the watermark, so the next poll
// reads the window again; without a database nothing is read.
func TestSyncDecisionsErrorKeepsWatermark(t *testing.T) {
	e := newDecisionsEnv(t)
	e.reconcile()
	e.read()
	w := e.watermark()

	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	st := e.tasks()[specTaskName]
	now := e.gl.Clock.Add(time.Hour)
	e.gl.Clock = now
	e.gl.Issues[1].UpdatedAt, e.gl.Issues[2].UpdatedAt = &now, &now
	e.gl.ListNoteErrors = map[int64]error{2: errors.New("boom")}
	err := e.r.syncDecisions(e.ctx, e.gl, &project, project.Spec, &st)
	if err == nil || !strings.Contains(err.Error(), "listing notes of issue #2: boom") {
		t.Fatalf("err = %v, want the notes error of #2", err)
	}
	if !e.watermark().Equal(w) {
		t.Errorf("watermark %v, want %v kept", e.watermark(), w)
	}

	e.gl.ListNoteErrors = nil
	e.r.recordDecisions = nil // no database
	e.read()
	st = e.tasks()[specTaskName]
	if err := e.r.syncDecisions(e.ctx, e.gl, &project, project.Spec, &st); err != nil {
		t.Fatal(err)
	}
	if !e.watermark().Equal(w) {
		t.Error("synced without a database")
	}
}
