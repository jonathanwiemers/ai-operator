package ui

import (
	"net/http"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// MetricsLowIssues is the fewest merged issues a group needs to compare it
// with others: below, MetricsGroup.Low is set.
const MetricsLowIssues = 5

// MetricsGroup is what a group of issues cost per merged line: an experiment
// arm of a project, a model, an effort or a step-session mode (Key, "" for
// none). An issue counts whole for one group: its arm, or the model, effort
// and step session of its implement calls with the largest cost. Issues and
// MergedLines are of its merged issues, CostUSD (known cost) and Tokens of
// all its issues with calls in the range, merged or not. CostPer100Lines is
// CostUSD per 100 merged lines, 0 without merged lines. Low: fewer than
// MetricsLowIssues merged issues, too few to compare.
type MetricsGroup struct {
	// Project (namespace/name) and Experiment, the name of the project's
	// current experiment when Key is one of its arms, are set for arms only.
	Project         string  `json:"project,omitempty"`
	Experiment      string  `json:"experiment,omitempty"`
	Key             string  `json:"key"`
	Issues          int64   `json:"issues"`
	MergedLines     int64   `json:"mergedLines"`
	CostUSD         float64 `json:"costUSD"`
	Tokens          int64   `json:"tokens"`
	CostPer100Lines float64 `json:"costPer100Lines"`
	Low             bool    `json:"low"`
}

// RoleMedian is the median base context of a role's calls that started
// their session (the first turn's context: system prompt, tools, CLAUDE.md),
// over Calls calls.
type RoleMedian struct {
	Role         string `json:"role"`
	MedianTokens int64  `json:"medianTokens"`
	Calls        int64  `json:"calls"`
}

// ToolUsage is what a tool's results put into the context: Calls results of
// Bytes, Tokens estimated from them.
type ToolUsage struct {
	Tool   string `json:"tool"`
	Calls  int64  `json:"calls"`
	Bytes  int64  `json:"bytes"`
	Tokens int64  `json:"tokens"`
}

// ProjectContext is what fills a project's contexts: the median base
// context per role and the tools with the largest results, at most five.
type ProjectContext struct {
	Project     string       `json:"project"`
	BaseContext []RoleMedian `json:"baseContext"`
	TopTools    []ToolUsage  `json:"topTools"`
}

// MetricsView is the answer of GET /api/v1/costs/metrics: what a merged line
// cost by experiment arm, model, effort and step-session mode, and what fills
// the context, over the Costs page's range and projects. The kind and engine
// filters do not apply: an issue counts whole. Arms is empty without
// experiment data.
type MetricsView struct {
	Arms          []MetricsGroup   `json:"arms"`
	ByModel       []MetricsGroup   `json:"byModel"`
	ByEffort      []MetricsGroup   `json:"byEffort"`
	ByStepSession []MetricsGroup   `json:"byStepSession"`
	Context       []ProjectContext `json:"context"`
}

// costsMetrics answers the Costs page's metrics over the viewer's projects,
// for the parameters of costs (group, kind and engine are read and checked,
// not used).
func (s *Server) costsMetrics(w http.ResponseWriter, r *http.Request) {
	p, visible, _, st, ok := s.costRequest(w, r, time.Now())
	if !ok {
		return
	}
	m, err := st.Metrics(r.Context(), p.q)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metricsView(m, visible))
}

// metricsView turns the store's metrics into the view; projects (by
// namespace/name) name the arms' experiments.
func metricsView(m store.Metrics, projects map[string]*aiv1.AgentProject) MetricsView {
	v := MetricsView{Arms: metricsGroups(m.Arms), ByModel: metricsGroups(m.ByModel), ByEffort: metricsGroups(m.ByEffort),
		ByStepSession: metricsGroups(m.ByStepSession), Context: []ProjectContext{}}
	for i := range v.Arms {
		ap := projects[v.Arms[i].Project]
		if ap == nil || ap.Spec.Agents.Implement.Experiment == nil {
			continue
		}
		exp := ap.Spec.Agents.Implement.Experiment
		if _, ok := exp.Arm(v.Arms[i].Key); ok {
			v.Arms[i].Experiment = exp.Name
		}
	}
	at := map[string]int{}
	ctx := func(project string) *ProjectContext {
		i, ok := at[project]
		if !ok {
			i = len(v.Context)
			at[project] = i
			v.Context = append(v.Context, ProjectContext{Project: project, BaseContext: []RoleMedian{}, TopTools: []ToolUsage{}})
		}
		return &v.Context[i]
	}
	// Both come ordered by project.
	for _, b := range m.BaseContext {
		c := ctx(b.Namespace + "/" + b.Project)
		c.BaseContext = append(c.BaseContext, RoleMedian{Role: b.Role, MedianTokens: b.MedianTokens, Calls: b.Calls})
	}
	for _, u := range m.TopTools {
		c := ctx(u.Namespace + "/" + u.Project)
		c.TopTools = append(c.TopTools, ToolUsage{Tool: u.Tool, Calls: u.Calls, Bytes: u.Bytes, Tokens: aiv1.EstimatedTokens(u.Bytes)})
	}
	return v
}

func metricsGroups(gs []store.MetricsGroup) []MetricsGroup {
	out := make([]MetricsGroup, 0, len(gs))
	for _, g := range gs {
		v := MetricsGroup{Key: g.Key, Issues: g.Issues, MergedLines: g.MergedLines, CostUSD: g.CostUSD, Tokens: g.Tokens,
			Low: g.Issues < MetricsLowIssues}
		if g.Project != "" {
			v.Project = g.Namespace + "/" + g.Project
		}
		if g.MergedLines > 0 {
			v.CostPer100Lines = g.CostUSD * 100 / float64(g.MergedLines)
		}
		out = append(out, v)
	}
	return out
}
