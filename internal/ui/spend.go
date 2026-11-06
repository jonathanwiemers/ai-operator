package ui

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// SpendTotalsView sums calls. CostUSD is "" when no cost is known (cursor
// calls without pricing), never "0.00"; UnknownCalls are left out of it.
type SpendTotalsView struct {
	CostUSD          string `json:"costUSD"`
	CostKnown        bool   `json:"costKnown"`
	UnknownCalls     int64  `json:"unknownCalls"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	Calls            int64  `json:"calls"`
	SubagentCalls    int64  `json:"subagentCalls"`
	DurationMS       int64  `json:"durationMS"`
	// LegacyCostUSD is the part of CostUSD from runs before agent_calls.
	LegacyCostUSD string `json:"legacyCostUSD,omitempty"`
}

// ModelSplitView is one model's share of a child; Main is the calls' own
// model, the others ran as subagents.
type ModelSplitView struct {
	Model     string `json:"model"`
	Main      bool   `json:"main"`
	Tokens    int64  `json:"tokens"`
	CostUSD   string `json:"costUSD"`
	CostKnown bool   `json:"costKnown"`
}

// SpendCallView is what a call-level child carries besides its totals.
type SpendCallView struct {
	Kind          string  `json:"kind"`
	Role          string  `json:"role"`
	Engine        string  `json:"engine"`
	Model         string  `json:"model"`
	Effort        string  `json:"effort,omitempty"`
	Resumed       bool    `json:"resumed"`
	FallbackFrom  string  `json:"fallbackFrom,omitempty"`
	CostSource    string  `json:"costSource" tstype:"'reported' | 'estimated' | 'unknown'"`
	Turns         int32   `json:"turns"`
	ContextSeries []int64 `json:"contextSeries"`
	SubagentTurns []int32 `json:"subagentTurns"`
	// ReadGateBlocks is how often the read gate denied a tool call.
	ReadGateBlocks int32 `json:"readGateBlocks"`
}

// SpendChildView is one part of the scope at the next level: a milestone
// (key its IID, "" for none), an issue (its IID), a run (task/run) or a
// call (its seq, with the call's fields).
type SpendChildView struct {
	Level  string           `json:"level" tstype:"SpendLevel"`
	Key    string           `json:"key"`
	Label  string           `json:"label"`
	At     time.Time        `json:"at"`
	Totals SpendTotalsView  `json:"totals"`
	Models []ModelSplitView `json:"models"`
	// Companion is the companion project of an issue that has one (issue
	// rows only): its calls edit both trees and count to the home issue.
	Companion      string `json:"companion,omitempty"`
	*SpendCallView `tstype:",extends"`
}

// SpendBreakdownView sums the calls of one kind, role or model.
type SpendBreakdownView struct {
	Key    string          `json:"key"`
	Totals SpendTotalsView `json:"totals"`
}

// SubagentBreakdownView sums a role's calls that ran subagents or none;
// subagentTokens are the tokens of the subagent models.
type SubagentBreakdownView struct {
	Role           string          `json:"role"`
	WithSubagents  bool            `json:"withSubagents"`
	Totals         SpendTotalsView `json:"totals"`
	SubagentTokens int64           `json:"subagentTokens"`
	ReadGateBlocks int64           `json:"readGateBlocks"`
}

// SpendView is what a scope cost. byReviewRound is keyed by round ("1",
// "2", …), bySession by "fresh" or "resumed" (implement calls only).
type SpendView struct {
	Totals         SpendTotalsView         `json:"totals"`
	Children       []SpendChildView        `json:"children"`
	ByKind         []SpendBreakdownView    `json:"byKind"`
	ByRole         []SpendBreakdownView    `json:"byRole"`
	ByModel        []SpendBreakdownView    `json:"byModel"`
	ByReviewRound  []SpendBreakdownView    `json:"byReviewRound"`
	BySession      []SpendBreakdownView    `json:"bySession"`
	BySubagents    []SubagentBreakdownView `json:"bySubagents"`
	CacheReadShare float64                 `json:"cacheReadShare"`
}

// spend returns what a project, milestone, issue, task or run cost, from
// agent_calls: ?namespace=&project= (required), &milestone=, &issue=,
// &task=, &run= (needs task).
func (s *Server) spend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sc := store.SpendScope{Namespace: q.Get("namespace"), Project: q.Get("project"), Task: q.Get("task")}
	if sc.Namespace == "" || sc.Project == "" {
		badRequest(w, "namespace and project are required")
		return
	}
	if !s.cfg.Research.Enabled() || !s.authz.CanView(principalOf(r), sc.Namespace, sc.Project) {
		notFound(w)
		return
	}
	for _, p := range []struct {
		name string
		v    *int64
	}{{"milestone", &sc.MilestoneIID}, {"issue", &sc.IssueIID}, {"run", &sc.Run}} {
		v := q.Get(p.name)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			badRequest(w, p.name+" must be a positive number")
			return
		}
		*p.v = n
	}
	if sc.Run != 0 && sc.Task == "" {
		badRequest(w, "run needs task")
		return
	}
	st := s.research.Load()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the history database is not connected yet"})
		return
	}
	sp, err := st.Spend(r.Context(), sc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	v := spendView(sp)
	s.markCompanions(r.Context(), sc, &v)
	writeJSON(w, http.StatusOK, v)
}

// markCompanions sets the companion project on the issue rows of v whose
// issue has one. The live state is read only when v has issue rows; when it
// cannot be read the rows go without the marker (logged).
func (s *Server) markCompanions(ctx context.Context, sc store.SpendScope, v *SpendView) {
	if !slices.ContainsFunc(v.Children, func(c SpendChildView) bool { return c.Level == store.SpendIssue }) {
		return
	}
	var ap aiv1.AgentProject
	if err := s.reader.Get(ctx, client.ObjectKey{Namespace: sc.Namespace, Name: sc.Project}, &ap); err != nil {
		if !apierrors.IsNotFound(err) {
			s.log.Error(err, "cannot read the project for the companion markers", "namespace", sc.Namespace, "project", sc.Project)
		}
		return
	}
	var list aiv1.AgentTaskList
	if err := s.reader.List(ctx, &list, client.InNamespace(sc.Namespace)); err != nil {
		s.log.Error(err, "cannot read the tasks for the companion markers", "namespace", sc.Namespace, "project", sc.Project)
		return
	}
	markCompanions(v, issueCompanions(&ap, projectTasks(list.Items, &ap)))
}

// markCompanions sets the companion of each issue row from byIssue.
func markCompanions(v *SpendView, byIssue map[int64]string) {
	for i := range v.Children {
		c := &v.Children[i]
		if c.Level != store.SpendIssue {
			continue
		}
		if iid, err := strconv.ParseInt(c.Key, 10, 64); err == nil {
			c.Companion = byIssue[iid]
		}
	}
}

// projectTasks are the tasks of ap among tasks.
func projectTasks(tasks []aiv1.AgentTask, ap *aiv1.AgentProject) []aiv1.AgentTask {
	var out []aiv1.AgentTask
	for _, t := range tasks {
		if t.Namespace == ap.Namespace && t.Spec.ProjectRef == ap.Name {
			out = append(out, t)
		}
	}
	return out
}

func spendView(sp store.Spend) SpendView {
	v := SpendView{Totals: spendTotalsView(sp.Totals), Children: make([]SpendChildView, 0, len(sp.Children)),
		ByKind: breakdownViews(sp.ByKind), ByRole: breakdownViews(sp.ByRole), ByModel: breakdownViews(sp.ByModel),
		ByReviewRound: breakdownViews(sp.ByReviewRound), BySession: breakdownViews(sp.BySession),
		BySubagents: make([]SubagentBreakdownView, 0, len(sp.BySubagents)), CacheReadShare: sp.CacheReadShare}
	for _, b := range sp.BySubagents {
		v.BySubagents = append(v.BySubagents, SubagentBreakdownView{Role: b.Role, WithSubagents: b.WithSubagents,
			Totals: spendTotalsView(b.Totals), SubagentTokens: b.SubagentTokens,
			ReadGateBlocks: b.ReadGateBlocks})
	}
	for _, c := range sp.Children {
		cv := SpendChildView{Level: c.Level, Key: c.Key, Label: c.Label, At: c.At, Totals: spendTotalsView(c.Totals),
			Models: make([]ModelSplitView, 0, len(c.Models))}
		for _, m := range c.Models {
			cv.Models = append(cv.Models, ModelSplitView{Model: m.Model, Main: m.Main, Tokens: m.Tokens,
				CostUSD: showKnownCost(m.CostUSD, m.CostKnown), CostKnown: m.CostKnown})
		}
		if call := c.Call; call != nil {
			cv.SpendCallView = &SpendCallView{Kind: call.Kind, Role: call.Role, Engine: call.Engine, Model: call.Model,
				Effort: call.Effort, Resumed: call.Resumed, FallbackFrom: call.FallbackFrom, CostSource: call.CostSource,
				Turns: call.Turns, ContextSeries: call.ContextSeries, SubagentTurns: call.SubagentTurns,
				ReadGateBlocks: call.ReadGateBlocks}
			if cv.ContextSeries == nil {
				cv.ContextSeries = []int64{}
			}
			if cv.SubagentTurns == nil {
				cv.SubagentTurns = []int32{}
			}
		}
		v.Children = append(v.Children, cv)
	}
	return v
}

func breakdownViews(bs []store.SpendBreakdown) []SpendBreakdownView {
	out := make([]SpendBreakdownView, 0, len(bs))
	for _, b := range bs {
		out = append(out, SpendBreakdownView{Key: b.Key, Totals: spendTotalsView(b.Totals)})
	}
	return out
}

func spendTotalsView(t store.SpendTotals) SpendTotalsView {
	v := SpendTotalsView{CostUSD: showKnownCost(t.CostUSD, t.CostKnown), CostKnown: t.CostKnown,
		UnknownCalls: t.UnknownCalls, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
		CacheReadTokens: t.CacheReadTokens, CacheWriteTokens: t.CacheWriteTokens, Calls: t.Calls,
		SubagentCalls: t.SubagentCalls, DurationMS: t.DurationMS}
	if t.LegacyCostUSD > 0 {
		v.LegacyCostUSD = aiv1.ShowCost(t.LegacyCostUSD)
	}
	return v
}

// showKnownCost is usd for display, "" when unknown.
func showKnownCost(usd float64, known bool) string {
	if !known {
		return ""
	}
	return aiv1.ShowCost(usd)
}
