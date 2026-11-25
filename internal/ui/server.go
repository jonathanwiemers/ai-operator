package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jonathanwiemers/ai-operator/internal/claims"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/ratelimit"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Server serves the UI, its API and the login flow.
type Server struct {
	cfg    *Config
	reader client.Reader
	hub    *Hub
	authz  *Authorizer
	oidc   *oidcClient
	// sessions is nil with auth mode none.
	sessions *Sessions
	// static is the built app (index.html, assets/), nil if it was not
	// built into the binary.
	static fs.FS
	log    logr.Logger

	// streams counts the open event streams per user.
	streamsMu sync.Mutex
	streams   map[string]int
	// logs reads the agent pods' logs for transcripts; nil disables them.
	logs LogSource
	// gitlab connects users' GitLab accounts; nil without gitlab.url.
	gitlab *gitlabConn
	// notes rate-limits the comments users post, creates the one-off
	// researches they start, planCreates the plans; chats rate-limits Desk
	// chat posts.
	notes, creates, planCreates, chats *ratelimit.RateLimiter
	// writer creates and deletes one-off researches and plans; nil without
	// research.allowCreate and the Desk chat.
	writer client.Client
	// research is the reader's store, nil until connected.
	research atomic.Pointer[store.Store]
	// pauses reads milestones' pauses instead of research (tests).
	pauses pauseReader
	// milestoneRows reads the milestones' snapshot instead of research
	// (tests).
	milestoneRows milestoneLister
	// milestoneDetails reads a milestone's snapshot instead of research
	// (tests).
	milestoneDetails milestoneDetailReader
	// retroSpend reads the cost drivers instead of research (tests).
	retroSpend spendReader
	// overviewSpend sums the overview's spend instead of research (tests).
	overviewSpend spendSincer
	// costReads reads the Costs page's figures instead of research (tests).
	costReads costReader
	// estimateReads reads the estimates' history instead of research (tests).
	estimateReads estimateReader
	// decisionReads reads the stored decisions instead of research (tests).
	decisionReads decisionReader
	// planDecisionReads reads plans' decision history instead of research
	// (tests).
	planDecisionReads planDecisionReader
	// chat is the chat writer's store, nil until connected or without
	// the writer login.
	chat atomic.Pointer[store.Store]
	// historyRetention is how long new chat rows expire after (matches
	// the operator's --history-retention).
	historyRetention time.Duration
	// version is the binary's release; selfProject the normalised GitLab path
	// of the project whose releases the Desk shows (empty without one).
	version     string
	selfProject string
}

// SetHistoryRetention sets how long new chat messages are kept.
func (s *Server) SetHistoryRetention(d time.Duration) {
	if d > 0 {
		s.historyRetention = d
	}
}

// SetAbout sets the release the binary was built as and the self project.
func (s *Server) SetAbout(version, selfProject string) {
	s.version, s.selfProject = version, selfProject
}

// SetLogSource enables live transcripts.
func (s *Server) SetLogSource(l LogSource) { s.logs = l }

// NewServer wires the server. reader is a cache-backed client; static holds
// the built single page app.
func NewServer(cfg *Config, reader client.Reader, hub *Hub, static fs.FS, log logr.Logger) (*Server, error) {
	s := &Server{cfg: cfg, reader: reader, hub: hub, authz: NewAuthorizer(cfg), log: log, streams: map[string]int{},
		notes: ratelimit.NewRateLimiter(notesPerWindow, notesWindow), creates: ratelimit.NewRateLimiter(createsPerWindow, createsWindow),
		planCreates: ratelimit.NewRateLimiter(plansPerWindow, createsWindow),
		chats:       ratelimit.NewRateLimiter(notesPerWindow, notesWindow), historyRetention: history.DefaultRetention}
	if _, err := fs.Stat(static, "index.html"); err == nil {
		s.static = static
	}
	// Only auth mode none serves without login: an empty or unknown mode
	// (a config that skipped Default and Validate) is refused, not anonymous.
	if cfg.Auth.Mode != AuthNone && cfg.Auth.Mode != AuthOIDC {
		return nil, fmt.Errorf("auth.mode must be oidc or none, not %q", cfg.Auth.Mode)
	}
	if cfg.Auth.Mode == AuthOIDC {
		claims, err := claims.NewClaimMapper(cfg.OIDC.Claims)
		if err != nil {
			return nil, err
		}
		if s.sessions, err = NewSessions(cfg.SessionKey, cfg.Secure(), cfg.Auth.SessionTTL.Duration); err != nil {
			return nil, err
		}
		if !slices.Contains(cfg.OIDC.Scopes, "offline_access") {
			log.Info("oidc.scopes lacks offline_access: the provider will not return a refresh token, logins last auth.sessionTTL and are not renewed; add offline_access to oidc.scopes")
		}
		s.oidc = &oidcClient{cfg: cfg, sessions: s.sessions, claims: claims, log: log.WithName("oidc"),
			renewals: renewals{m: map[[32]byte]*renewFlight{}, now: time.Now}}
		if cfg.GitLab.URL != "" {
			s.gitlab = &gitlabConn{cfg: cfg, sessions: s.sessions,
				newClient: func(baseURL, token string) gitlab.API { return gitlab.NewOAuth(baseURL, token) },
				newFixed:  func(baseURL, token string) gitlab.API { return gitlab.New(baseURL, token) },
			}
		}
	}
	return s, nil
}

// route is an API route: its pattern ("METHOD /path") and its handler,
// wrapped in authed, researchHandler or writeHandler.
type route struct {
	pattern string
	handler http.Handler
}

// routes lists the routes that need a login. Each handler checks the
// Authorizer for the project or research in its path; the route test walks
// this list, so register new routes here, not in Handler.
func (s *Server) routes() []route {
	var rs []route
	if s.gitlab != nil && s.gitlab.oauth() {
		rs = []route{
			{"GET " + pathGitLabLogin, s.authed(s.gitlab.login)},
			{"GET " + pathGitLabCallback, s.authed(s.gitlab.callback(s))},
			{"POST " + pathGitLabLogout, s.authed(s.gitlab.logout)},
		}
	}
	rs = append(rs, []route{
		{"GET /api/v1/gitlab", s.authed(s.gitlabStatus)},
		{"GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread", s.authed(s.thread)},
		{"POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes", s.authed(s.postNote)},
		{"GET /api/v1/me", s.authed(s.me)},
		{"GET /api/v1/about", s.authed(s.about)},
		{"GET /api/v1/features", s.authed(s.features)},
		{"GET /api/v1/projects", s.authed(s.projects)},
		{"GET /api/v1/projectsets", s.authed(s.projectSets)},
		{"GET /api/v1/projects/{namespace}/{name}", s.authed(s.project)},
		{"GET /api/v1/projects/{namespace}/{name}/tasks/{task}", s.authed(s.task)},
		{"GET /api/v1/milestones", s.authed(s.milestones)},
		{"GET /api/v1/search", s.authed(s.search)},
		{"GET /api/v1/overview", s.authed(s.overview)},
		{"GET /api/v1/projects/{namespace}/{name}/milestones/{iid}", s.authed(s.milestone)},
		{"GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}", s.authed(s.issue)},
		{"GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread", s.authed(s.milestoneThread)},
		{"GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/problems", s.authed(s.problems)},
		{"GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/problems/{id}", s.authed(s.problemThread)},
		{"POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes", s.authed(s.postMilestoneNote)},
		{"POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followup", s.authed(s.createFollowUp)},
		{"POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followups/dismiss", s.authed(s.dismissFollowUps)},
		{"POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}/close", s.authed(s.closeHoldingIssue)},
		{"GET /api/v1/plans", s.authed(s.allPlans)},
		{"GET /api/v1/projects/{namespace}/{name}/plans/{plan}", s.authed(s.plan)},
		{"POST /api/v1/projects/{namespace}/{name}/plans", s.authed(s.createPlan)},
		{"DELETE /api/v1/projects/{namespace}/{name}/plans/{plan}", s.authed(s.deletePlan)},
		{"PUT /api/v1/projects/{namespace}/{name}/plans/{plan}/draft", s.authed(s.savePlanDraft)},
		{"DELETE /api/v1/projects/{namespace}/{name}/plans/{plan}/drafts/{key}", s.authed(s.removePlanDraft)},
		{"PUT /api/v1/projects/{namespace}/{name}/plans/{plan}/drafts/order", s.authed(s.orderPlanDrafts)},
		{"POST /api/v1/projects/{namespace}/{name}/plans/{plan}/budget", s.authed(s.raisePlanBudget)},
		{"POST /api/v1/projects/{namespace}/{name}/plans/{plan}/milestone", s.authed(s.createPlanMilestone)},
		{"GET /api/v1/projects/{namespace}/{name}/plans/{plan}/chat", s.authed(s.getPlanChat)},
		{"POST /api/v1/projects/{namespace}/{name}/plans/{plan}/chat", s.authed(s.postPlanChat)},
		{"GET /api/v1/projects/{namespace}/{name}/plans/{plan}/decisions", s.authed(s.planDecisionHistory)},
		{"GET /api/v1/projects/{namespace}/{name}/tasks/{task}/transcript", s.authed(s.transcript)},
		{"GET /api/v1/projects/{namespace}/{name}/tasks/{task}/logs", s.authed(s.rawLogs)},
		{"GET /api/v1/projects/{namespace}/{name}/tasks/{task}/chat", s.authed(s.getChat)},
		{"POST /api/v1/projects/{namespace}/{name}/tasks/{task}/chat", s.authed(s.postChat)},
		{"POST /api/v1/projects/{namespace}/{name}/tasks/{task}/warm", s.authed(s.warmTask)},
		{"GET /api/v1/events", s.authed(s.events)},
		{"GET /api/v1/stream", s.authed(s.stream)},
		{"GET /api/v1/inbox", s.authed(s.inbox)},
		{"GET /api/v1/projects/{namespace}/{name}/history", s.authed(s.history)},
		{"GET /api/v1/projects/{namespace}/{name}/estimate", s.authed(s.estimate)},
		{"GET /api/v1/projects/{namespace}/{name}/decisions", s.authed(s.decisions)},
		{"GET /api/v1/spend", s.authed(s.spend)},
		{"GET /api/v1/costs", s.authed(s.costs)},
		{"GET /api/v1/costs/top", s.authed(s.costsTop)},
		{"GET /api/v1/costs/metrics", s.authed(s.costsMetrics)},
		{"GET /api/v1/metrics/code", s.authed(s.metricsCode)},
		{"GET /api/v1/metrics/flow", s.authed(s.metricsFlow)},
		{"GET /api/v1/metrics/pauses", s.authed(s.metricsPauses)},
		{"GET /api/v1/timeline", s.authed(s.timeline)},
	}...)
	if s.cfg.Research.Enabled() {
		rs = append(rs, []route{
			{"GET /api/v1/researches", s.researchHandler(s.researches)},
			{"GET /api/v1/researches/{namespace}/{name}", s.researchHandler(s.researchDetail)},
			{"GET /api/v1/researches/{namespace}/{name}/runs", s.researchHandler(s.researchRuns)},
			{"GET /api/v1/researches/{namespace}/{name}/runs/{id}", s.researchHandler(s.researchRun)},
		}...)
	}
	// Always registered, so they answer 404 (not 405) without allowCreate.
	return append(rs, []route{
		{"GET /api/v1/research/form", s.writeHandler(s.researchForm)},
		{"POST /api/v1/researches", s.writeHandler(s.createResearch)},
		{"DELETE /api/v1/researches/{namespace}/{name}", s.writeHandler(s.deleteResearch)},
	}...)
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.oidc != nil {
		mux.HandleFunc("GET "+pathLogin, s.oidc.login)
		mux.HandleFunc("GET "+pathCallback, s.oidc.callback)
		mux.HandleFunc("POST "+pathLogout, s.oidc.logout)
	}
	for _, rt := range s.routes() {
		mux.Handle(rt.pattern, rt.handler)
	}
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("GET /oauth2/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("GET /", s.app)
	// Rejects cross-origin requests that change state (the logout, comments,
	// one-off researches), on top of the SameSite cookies.
	var h http.Handler = http.NewCrossOriginProtection().Handler(mux)
	if s.cfg.Auth.Mode == AuthNone {
		h = localHostOnly(h)
	}
	return securityHeaders(s.cfg.Secure(), h)
}

type ctxKey int

const (
	principalKey ctxKey = iota
	expiryKey
	refreshKey
)

// authed runs h for logged-in users and answers 401 to everyone else. An
// expired session with a live refresh cookie is renewed first; when that
// fails, both cookies are cleared.
func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Auth.Mode == AuthNone {
			ctx := context.WithValue(r.Context(), principalKey, Principal{Username: "anonymous"})
			h(w, r.WithContext(ctx))
			return
		}
		rs, _ := s.sessions.Refresh(r)
		sess, ok := s.sessions.Get(r)
		if !ok && rs != nil {
			sess, rs, ok = s.oidc.renewSession(w, r, rs)
		}
		if !ok {
			if _, err := r.Cookie(s.sessions.RefreshCookie()); err == nil {
				s.sessions.Clear(w, s.sessions.SessionCookie())
				s.sessions.Clear(w, s.sessions.RefreshCookie())
			}
			writeErrorResponse(w, http.StatusUnauthorized, ErrorResponse{Error: "login required", Login: pathLogin})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, sess.Principal)
		ctx = context.WithValue(ctx, expiryKey, time.Unix(sess.Expires, 0))
		ctx = context.WithValue(ctx, refreshKey, rs)
		h(w, r.WithContext(ctx))
	})
}

func principalOf(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey).(Principal)
	return p
}

func sessionExpiry(r *http.Request) time.Time {
	t, _ := r.Context().Value(expiryKey).(time.Time)
	return t
}

// refreshOf is the login's refresh state, nil when it is not renewed.
func refreshOf(r *http.Request) *RefreshState {
	rs, _ := r.Context().Value(refreshKey).(*RefreshState)
	return rs
}

// app serves the single page app: files as they are, every other path as
// index.html, which routes in the browser. The app holds no data, so it is
// served without login; it asks for one when the API answers 401.
func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.Error(w, "The web UI was not built into this binary (make ui, then go build).", http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if st, err := fs.Stat(s.static, name); err == nil && !st.IsDir() {
			switch {
			case strings.HasPrefix(name, "assets/"):
				// File names carry a content hash.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case name == "sw.js":
				// The service worker's name is fixed: a new build must reach it.
				w.Header().Set("Cache-Control", "no-cache")
			case name == "manifest.webmanifest":
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			// #nosec G703 -- name is path.Clean'ed and read from the embedded app's fs.FS, which rejects ".." and absolute names
			http.ServeFileFS(w, r, s.static, name)
			return
		}
		// Missing assets are errors; any other path is a page of the app,
		// even with a dot in it (project names may have one).
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	b, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Error(err, "cannot read index.html")
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// localHostOnly refuses requests for other host names. Without login the UI
// relies on listening on localhost only; a page on another site could still
// reach it through a DNS name that resolves to 127.0.0.1 (DNS rebinding),
// but its requests carry that name.
func localHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !isLoopback(strings.Trim(host, "[]")) {
			http.Error(w, "Without login the UI only answers requests for localhost.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows only the app's own scripts, styles, API,
// service worker and manifest.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; " +
	"worker-src 'self'; manifest-src 'self'"

func securityHeaders(secure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
