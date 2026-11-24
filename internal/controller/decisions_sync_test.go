package controller

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
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
	if !slices.Equal(read, []int64{1, 2, 3, 4}) {
		t.Fatalf("the backfill read %v, want #1 to #4", read)
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

// revisitsOf returns the revisit requests recorded for issue iid.
func revisitsOf(t *testing.T, got map[int64]store.IssueDecisions, iid int64) []store.RevisitRequestRow {
	t.Helper()
	d, ok := got[iid]
	if !ok {
		t.Fatalf("#%d was not read", iid)
	}
	return d.Revisits
}

// Human revisit commands are recorded from the issue they are on, the
// tracking issue included; the bot's, other prefixes and ones without an ID
// are not.
func TestSyncDecisionsRecordsRevisitComments(t *testing.T) {
	e := newDecisionsEnv(t)
	e.reconcile()
	e.read()

	owner := gitlab.User{ID: 1, Username: "jonathan"}
	n := e.gl.AddNote(2, owner, "/agent revisit d2-p1   use a list  ")
	e.gl.AddNote(2, owner, "/agent revisit later, the layout")
	e.gl.AddNote(2, owner, "/bot revisit D2-P1 use cards")
	e.gl.AddNote(2, owner, "/agents revisit D2-P1 use cards")
	e.gl.AddNote(2, e.gl.Bot, "/agent revisit D2-P1 use a table")
	tn := e.gl.AddNote(4, owner, "/agent revisit D1-1: use gRPC")
	e.reconcile()
	_, got := e.read()

	want := []store.RevisitRequestRow{{DecisionID: "D2-P1", Source: store.RevisitComment, RequestedBy: "jonathan",
		Instruction: "use a list", URL: e.gl.Issues[2].WebURL + "#note_" + strconv.FormatInt(n.ID, 10), At: *n.CreatedAt}}
	if rs := revisitsOf(t, got, 2); !reflect.DeepEqual(rs, want) {
		t.Errorf("#2 revisits =\n%+v\nwant\n%+v", rs, want)
	}
	if !slices.Equal(ids(got[2]), []string{"D2-P1", "D2-P2", "D2-P9"}) {
		t.Errorf("#2 decisions = %v, want them kept next to the revisits", ids(got[2]))
	}
	tracking := got[4]
	if len(tracking.Decisions) != 0 || len(tracking.Revisits) != 1 || tracking.Revisits[0].DecisionID != "D1-1" ||
		tracking.Revisits[0].Instruction != "use gRPC" || !tracking.Revisits[0].At.Equal(*tn.CreatedAt) {
		t.Errorf("tracking issue = %+v, want the revisit of D1-1 only", tracking)
	}
}

// A follow-up the bot created for a revisit is recorded with the requester
// and the instruction its description names and its state; the issue whose
// decision it revisits is read in the same sweep, once.
func TestSyncDecisionsRecordsFollowUps(t *testing.T) {
	e := newDecisionsEnv(t)
	e.reconcile()
	e.read()

	api := e.gl.Issues[1]
	followUp := func(iid int64, by, instruction string) *gitlab.Issue {
		section := marker.DecisionSection(decisionComment, "D1-1")
		is := e.gl.AddIssue(iid, "Revisit D1-1: Protocol")
		is.Milestone, is.Author = api.Milestone, e.gl.Bot
		is.Description = renderFollowUp("D1-1", section, instruction, gitlab.Note{Author: gitlab.User{Username: by}}, api, "/agent")
		return is
	}
	fu := followUp(57, "jonathan", "use gRPC")
	e.reconcile()
	read, got := e.read()
	if !slices.Equal(read, []int64{57, 1}) {
		t.Fatalf("read %v, want the follow-up #57, then #1 it revisits", read)
	}
	want := []store.RevisitRequestRow{{DecisionID: "D1-1", Source: store.RevisitFollowUp, RequestedBy: "jonathan",
		Instruction: "use gRPC", URL: fu.WebURL, FollowUpIID: 57, FollowUpState: "opened", At: *fu.CreatedAt}}
	if rs := revisitsOf(t, got, 57); !reflect.DeepEqual(rs, want) {
		t.Errorf("#57 revisits =\n%+v\nwant\n%+v", rs, want)
	}
	if !slices.Equal(ids(got[1]), []string{"D1-1"}) {
		t.Errorf("#1 re-read = %v", ids(got[1]))
	}

	// Both updated: #1 is read once. A Desk request keeps "via the Desk".
	e.age(57)
	desk := followUp(58, "jonathan"+viaDesk, "use GraphQL")
	desk.State = "closed"
	now := e.gl.Clock
	api.UpdatedAt = &now
	e.reconcile()
	read, got = e.read()
	if !slices.Equal(read, []int64{1, 58}) {
		t.Fatalf("read %v, want #1 and #58 once each", read)
	}
	if rs := revisitsOf(t, got, 58); len(rs) != 1 || rs[0].RequestedBy != "jonathan via the Desk" ||
		rs[0].Instruction != "use GraphQL" || rs[0].FollowUpState != "closed" {
		t.Errorf("#58 revisits = %+v, want the Desk request", rs)
	}

	// A human's issue with the marker is no follow-up; a revisited issue of
	// another milestone is not read.
	e.age(1, 58)
	human := followUp(59, "jonathan", "x")
	human.Author = gitlab.User{ID: 1, Username: "jonathan"}
	other := followUp(60, "jonathan", "x")
	other.Description = strings.ReplaceAll(other.Description, "D1-1", "D3-P1")
	e.gl.Issues[3].Milestone = &gitlab.Milestone{ID: 12, IID: 4, Title: "Later"}
	e.reconcile()
	read, got = e.read()
	if !slices.Equal(read, []int64{59, 60}) || len(got[59].Revisits) != 0 || len(got[60].Revisits) != 1 {
		t.Errorf("read %v (%+v, %+v), want #59 without a request and #60 alone", read, got[59].Revisits, got[60].Revisits)
	}
}

// parseFollowUp reads back what renderFollowUp wrote.
func TestParseFollowUpRoundTrip(t *testing.T) {
	target := &gitlab.Issue{IID: 42}
	section := marker.DecisionSection(decisionComment, "D1-1")
	for _, tc := range []struct{ by, instruction, want string }{
		{"jo", "use EUR", "use EUR"},
		{"jo.wiemers via the Desk", "use EUR\n\nand round to cents", "use EUR\n\nand round to cents"},
		{"jo via the pilot", " <b>bold</b> ping @ann ", marker.Clean("<b>bold</b> ping @ann")},
		{"jo", "", "(no instruction given; see the comments on #42)"},
	} {
		desc := renderFollowUp("D42-3", section, tc.instruction, gitlab.Note{Author: gitlab.User{Username: tc.by}}, target, "/agent")
		if by, instruction := parseFollowUp(desc); by != tc.by || instruction != tc.want {
			t.Errorf("parseFollowUp(%q) = %q, %q; want %q, %q", desc, by, instruction, tc.by, tc.want)
		}
	}
	if by, instruction := parseFollowUp("Build the API.\n<!-- ai-operator:revisit=D1-1 -->"); by != "" || instruction != "" {
		t.Errorf("a description renderFollowUp did not write = %q, %q", by, instruction)
	}
}

// With a Desk URL the tracking issue's Decisions section starts with a link
// to the milestone's Decisions tab; without one it is unchanged.
func TestTrackingDecisionsLinkTheDesk(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, ProjectRef: "shop", Milestone: &aiv1.MilestoneRef{IID: 9, Title: "M9"}}}
	st.Namespace = ns
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemMerged,
		Decisions: []aiv1.DecisionRef{{ID: "D1-1", Title: "Protocol", URL: "https://gitlab.example/group/app/-/issues/1#note_50"}}}}}
	render := func(r *AgentProjectReconciler, ap *aiv1.AutopilotStatus) string {
		a := &autopilot{r: r, st: st, ap: ap, log: logr.Discard(), spec: aiv1.AgentProjectSpec{Workflow: aiv1.WorkflowSpec{CommandPrefix: "/agent"}}}
		body := a.renderTracking()
		_, section, _ := strings.Cut(body, "\n### Decisions\n\n")
		return section
	}
	link := "[All decisions on the Desk](https://desk.example/projects/agents/shop/milestones/9?panel=decisions)\n\n"
	list := "- [D1-1](https://gitlab.example/group/app/-/issues/1#note_50) Protocol (#1)\n"
	placeholder := "_Listed here once an issue is merged; until then they are on the issue._\n"
	desk := &AgentProjectReconciler{DeskURL: "https://desk.example"}
	for _, tc := range []struct {
		name string
		r    *AgentProjectReconciler
		ap   *aiv1.AutopilotStatus
		want string
	}{
		{"list with the Desk", desk, ap, link + list},
		{"placeholder with the Desk", desk, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}, link + placeholder},
		{"list without the Desk", &AgentProjectReconciler{}, ap, list},
		{"placeholder without a reconciler", nil, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}, placeholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(tc.r, tc.ap); !strings.HasPrefix(got, tc.want) {
				t.Errorf("Decisions section =\n%s\nwant it to start with\n%s", got, tc.want)
			}
		})
	}
}
