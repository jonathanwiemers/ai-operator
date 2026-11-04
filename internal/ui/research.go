package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Limits of the research read endpoints.
const (
	defaultRunsLimit = 20
	maxRunsLimit     = 100
	maxSearchQuery   = 200 // characters
	maxSearchHits    = 50
)

// ResearchSummary is an AgentResearch as the research pages show it.
type ResearchSummary struct {
	Namespace  string             `json:"namespace"`
	Name       string             `json:"name"`
	View       aiv1.ResearchView  `json:"view"`
	Depth      aiv1.ResearchDepth `json:"depth"`
	Schedule   string             `json:"schedule,omitempty"`
	TimeZone   string             `json:"timeZone,omitempty"`
	Phase      aiv1.ResearchPhase `json:"phase,omitempty"`
	LastRun    *metav1.Time       `json:"lastRun,omitempty"`
	NextRun    *metav1.Time       `json:"nextRun,omitempty"`
	LastEngine aiv1.Engine        `json:"lastEngine,omitempty"`
	LastModel  string             `json:"lastModel,omitempty"`
	// Run is the current or last run, for the run line
	// ("claude/claude-sonnet-5-5 · 3 min · 07:00"), without its usage readings.
	Run           *aiv1.ResearchRunStatus `json:"run,omitempty"`
	SkippedReason string                  `json:"skippedReason,omitempty"`
	Waiting       *aiv1.ResearchWaiting   `json:"waiting,omitempty"`
	OneOff        bool                    `json:"oneOff"`
	CreatedBy     string                  `json:"createdBy,omitempty"`
	// CanDelete: the user may delete it from the UI (their own one-off).
	CanDelete bool `json:"canDelete"`
}

// ResearchDetail is a research as its pages show it: the summary with the
// prompt, the topics, the feed sources and the fixed agent (nil for auto).
type ResearchDetail struct {
	ResearchSummary
	Prompt  string                `json:"prompt"`
	Topics  []string              `json:"topics,omitempty"`
	Sources []aiv1.ResearchSource `json:"sources,omitempty"`
	Agent   *aiv1.ResearchAgent   `json:"agent,omitempty"`
}

// ResearchRun is a stored run.
type ResearchRun struct {
	ID           string     `json:"id"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	Status       string     `json:"status"`
	Engine       string     `json:"engine,omitempty"`
	Model        string     `json:"model,omitempty"`
	Effort       string     `json:"effort,omitempty"`
	CostUSD      string     `json:"costUSD"`
	ChoiceReason string     `json:"choiceReason,omitempty"`
	Error        string     `json:"error,omitempty"`
	Warnings     []string   `json:"warnings,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt"`
}

// ResearchResult is a stored result, validated again. Blocks are in the
// block schema's JSON form; items carry "new": true when their URL first
// appeared in this run.
type ResearchResult struct {
	RunID     string    `json:"runID"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Blocks    []any     `json:"blocks"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Warnings  []string  `json:"warnings,omitempty"`
}

type itemsView struct {
	Type  string     `json:"type"`
	Items []itemView `json:"items"`
}

type itemView struct {
	blocks.Item
	New bool `json:"new,omitempty"`
}

// SearchHit is a result that matches a search.
type SearchHit struct {
	RunID     string    `json:"runID"`
	Namespace string    `json:"namespace"`
	Research  string    `json:"research"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"createdAt"`
}

func summarizeResearch(ar *aiv1.AgentResearch, canDelete bool) ResearchSummary {
	st := ar.Status
	out := ResearchSummary{
		Namespace: ar.Namespace, Name: ar.Name, View: ar.Spec.View, Depth: ar.Spec.Depth,
		Schedule: ar.Spec.Schedule, TimeZone: ar.Spec.TimeZone, Phase: st.Phase, LastRun: st.LastRun, NextRun: st.NextRun,
		LastEngine: st.LastEngine, LastModel: st.LastModel, SkippedReason: st.SkippedReason, Waiting: st.Waiting,
		OneOff: ar.OneOff(), CreatedBy: ar.Annotations[aiv1.AnnotationCreatedBy], CanDelete: canDelete,
	}
	if st.Run != nil {
		run := *st.Run
		run.EngineUsage, run.UsageLimits = nil, nil
		out.Run = &run
	}
	return out
}

func (s *Server) detailOf(r *http.Request, ar *aiv1.AgentResearch) ResearchDetail {
	return ResearchDetail{ResearchSummary: summarizeResearch(ar, s.canDelete(principalOf(r), ar)), Prompt: ar.Spec.Prompt, Topics: ar.Spec.Topics, Sources: ar.Spec.Sources, Agent: ar.Spec.Agent}
}

func runView(r store.Run) ResearchRun {
	return ResearchRun{ID: r.ID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Status: r.Status, Engine: r.Engine,
		Model: r.Model, Effort: r.Effort, CostUSD: aiv1.FormatCost(r.CostUSD),
		ChoiceReason: r.ChoiceReason, Error: r.Error, Warnings: r.Warnings, ExpiresAt: r.ExpiresAt}
}

// revalidate runs a stored title, summary and blocks through blocks.Validate
// again: the browser gets nothing the validator would refuse, whatever was
// written to the database.
func revalidate(title, summary string, raw json.RawMessage) (blocks.Result, []string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Not escaped and compacted, so the size is the size Validate checked.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		SchemaVersion int             `json:"schemaVersion"`
		Title         string          `json:"title"`
		Summary       string          `json:"summary"`
		Blocks        json.RawMessage `json:"blocks"`
	}{blocks.SchemaVersion, title, summary, raw}); err != nil {
		return blocks.Result{}, nil, err
	}
	return blocks.Validate(buf.Bytes())
}

// resultView validates row again and marks its new items. An invalid result
// is replaced by a warning and logged.
func (s *Server) resultView(row store.ResultRow) *ResearchResult {
	out := &ResearchResult{RunID: row.RunID, Blocks: []any{}, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	res, warnings, err := revalidate(row.Title, row.Summary, row.Blocks)
	if err != nil {
		s.log.Error(err, "stored research result failed validation, not sent", "run", row.RunID)
		out.Title = "Result withheld"
		out.Warnings = []string{"The stored result failed validation and is not shown."}
		return out
	}
	out.Title, out.Summary, out.Warnings = res.Title, res.Summary, warnings
	isNew := map[string]bool{}
	for _, h := range row.NewURLHashes {
		isNew[string(h)] = true
	}
	for _, b := range res.Blocks {
		if b.Items == nil {
			out.Blocks = append(out.Blocks, b)
			continue
		}
		items := make([]itemView, len(b.Items.Items))
		for i, it := range b.Items.Items {
			items[i] = itemView{Item: it, New: isNew[string(blocks.URLHash(it.URL))]}
		}
		out.Blocks = append(out.Blocks, itemsView{Type: blocks.TypeItems, Items: items})
	}
	return out
}

// readableResearches lists the researches the user may read, by namespace
// and name.
func (s *Server) readableResearches(w http.ResponseWriter, r *http.Request) ([]aiv1.AgentResearch, bool) {
	var list aiv1.AgentResearchList
	if err := s.reader.List(r.Context(), &list); err != nil {
		s.internalError(w, err)
		return nil, false
	}
	p := principalOf(r)
	out := slices.DeleteFunc(list.Items, func(ar aiv1.AgentResearch) bool {
		return !s.authz.CanReadResearch(p, ar.Namespace, ar.Name)
	})
	slices.SortFunc(out, func(a, b aiv1.AgentResearch) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	return out, true
}

// loadResearch reads the research of the request path. One the user may not
// read is reported as not found, like a project.
func (s *Server) loadResearch(w http.ResponseWriter, r *http.Request) (*aiv1.AgentResearch, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	if !s.authz.CanReadResearch(principalOf(r), ns, name) {
		notFound(w)
		return nil, false
	}
	var ar aiv1.AgentResearch
	if err := s.reader.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &ar); err != nil {
		if apierrors.IsNotFound(err) {
			notFound(w)
		} else {
			s.internalError(w, err)
		}
		return nil, false
	}
	return &ar, true
}

// researches lists the researches the user may read.
func (s *Server) researches(w http.ResponseWriter, r *http.Request, _ *store.Store) {
	list, ok := s.readableResearches(w, r)
	if !ok {
		return
	}
	p, out := principalOf(r), make([]ResearchSummary, len(list))
	for i := range list {
		out[i] = summarizeResearch(&list[i], s.canDelete(p, &list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// researchDetail returns a research with its latest result and that
// result's run (both null without one).
func (s *Server) researchDetail(w http.ResponseWriter, r *http.Request, st *store.Store) {
	ar, ok := s.loadResearch(w, r)
	if !ok {
		return
	}
	var (
		run    *ResearchRun
		result *ResearchResult
	)
	switch row, err := st.LatestResult(r.Context(), ar.Namespace, ar.Name); {
	case err == nil:
		stored, err := st.GetRun(r.Context(), row.RunID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		v := runView(stored)
		run, result = &v, s.resultView(row)
	case !errors.Is(err, store.ErrNotFound):
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"research": s.detailOf(r, ar), "run": run, "result": result})
}

// researchRuns pages through a research's runs, newest first: ?before= (RFC
// 3339, the startedAt of the last run of the previous page) and ?limit=.
func (s *Server) researchRuns(w http.ResponseWriter, r *http.Request, st *store.Store) {
	ar, ok := s.loadResearch(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	before := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before must be an RFC 3339 time"})
			return
		}
		before = t
	}
	limit := defaultRunsLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxRunsLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1 to 100"})
			return
		}
		limit = n
	}
	runs, err := st.ListRuns(r.Context(), ar.Namespace, ar.Name, before, limit)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]ResearchRun, len(runs))
	for i, run := range runs {
		out[i] = runView(run)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// researchRun returns one run of a research with the research and the run's
// result (null without one), like researchDetail; a run of another research
// is not found.
func (s *Server) researchRun(w http.ResponseWriter, r *http.Request, st *store.Store) {
	ar, ok := s.loadResearch(w, r)
	if !ok {
		return
	}
	run, err := st.GetRun(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound), err == nil && (run.Namespace != ar.Namespace || run.Research != ar.Name):
		notFound(w)
		return
	case err != nil:
		s.internalError(w, err)
		return
	}
	var result *ResearchResult
	switch row, err := st.GetResult(r.Context(), run.ID); {
	case err == nil:
		result = s.resultView(row)
	case !errors.Is(err, store.ErrNotFound):
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"research": s.detailOf(r, ar), "run": runView(run), "result": result})
}

// researchSearch searches the results of the researches the user may read
// (?q= in web search syntax), best match first.
func (s *Server) researchSearch(w http.ResponseWriter, r *http.Request, st *store.Store) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < 1 || n > maxSearchQuery || !utf8.ValidString(q) || strings.ContainsRune(q, 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q must have 1 to 200 characters"})
		return
	}
	list, ok := s.readableResearches(w, r)
	if !ok {
		return
	}
	allowed := make([]types.NamespacedName, len(list))
	for i, ar := range list {
		allowed[i] = types.NamespacedName{Namespace: ar.Namespace, Name: ar.Name}
	}
	hits, err := st.Search(r.Context(), q, allowed, maxSearchHits)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := []SearchHit{}
	for _, h := range hits {
		// Title and summary are checked like a result's; a hit that fails is left out.
		if _, _, err := revalidate(h.Title, h.Summary, json.RawMessage("[]")); err != nil {
			s.log.Error(err, "stored research result failed validation, search hit not sent", "run", h.RunID)
			continue
		}
		out = append(out, SearchHit{RunID: h.RunID, Namespace: h.Namespace, Research: h.Research,
			Title: h.Title, Summary: h.Summary, CreatedAt: h.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": out})
}
